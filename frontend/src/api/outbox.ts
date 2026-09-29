// The outbox: writes recorded while the API was unreachable — a manual
// transaction, an edit to a row, or one field set on many rows.
//
// A create carries a client-generated idempotency key, which the create
// endpoint uses to recognise a replay — so an entry whose response was lost
// (a timeout, a killed tab) is applied exactly once when it is finally sent.
// The same key is reused on every retry, which is what makes the retry safe.
//
// Queue semantics on flush: a network failure or a server/session problem
// (401/403/5xx) stops the flush and keeps the queue in order for the next
// reconnect, while a rejected payload (4xx) is marked with the server's message
// and left for the user to retry or discard — one bad entry must not block the
// entries behind it, and it must not be retried forever either. A queue the
// browser refuses to rewrite stops the flush too and is reported as `unsaved`:
// the alternative is claiming progress that is not stored anywhere.

import type { CreateTransactionRequest } from "../types";
import type { FieldPatch, FieldValue } from "./merge";
import { ApiError } from "./errors";

const PREFIX = "fintrak_outbox:v1";

// A bounded queue: a phone that has been offline for weeks should not grow an
// unbounded localStorage payload.
export const MAX_ENTRIES = 100;

// OutboxStorageError is thrown when the browser refuses to write the queue —
// a blocked origin, a private window, a full quota. It is an error rather than
// a silent success because the caller reports a queued transaction to the user:
// a queue that does not exist is a transaction the user believes is saved and
// that will never be sent.
export class OutboxStorageError extends Error {
  constructor(
    message = "Could not write the offline queue: browser storage is unavailable or full",
  ) {
    super(message);
    this.name = "OutboxStorageError";
  }
}

// WriteOp is the API operation an edit or a bulk write applies through. It
// travels on the entry rather than being inferred from the patch's fields,
// because the same field name means different things to different endpoints and
// the flush has to reach exactly one of them.
export type WriteOp =
  | "transaction.patch" | "transaction.categorize" | "transaction.payee"
  | "transaction.billingCycle" | "transaction.tags" | "transaction.loan"
  | "transaction.recurring" | "transaction.loanDisbursement"
  | "account.put" | "accountType.put" | "group.put" | "category.put"
  | "adminCategory.put" | "payee.put" | "rule.put" | "recurring.put"
  | "recurringTerm.put" | "loanSchedule.put" | "settings.put";

// ConflictUnit is one field the merge could not attribute to a side, kept with
// the entry rather than in the UI: the queue is the only thing that survives a
// reload, and the base is what lets a dialog show what the two competing values
// were each edited away from.
export interface ConflictUnit {
  rowId: string;
  field: string;
  base: FieldValue;
  mine: FieldValue;
  theirs: FieldValue;
}

export interface PendingConflict {
  units: ConflictUnit[];
}

// WriteEnvelope is what every queued write shares. The four optional fields are
// all recorded *in the queue* rather than in the page, for the same reason the
// queue exists at all: a thing the user has to be told about cannot be a thing
// only this tab remembers.
export interface WriteEnvelope {
  // key identifies the entry within the whole queue — removeEntry matches on it
  // alone. For a create it is the endpoint's idempotency key, so a replay of an
  // entry whose response was lost is applied exactly once.
  key: string;
  queuedAt: number;
  // error is the server's rejection message from the last flush attempt.
  error?: string;
  // conflict is set when the merge held the entry for the user instead of
  // applying it, and resolution is their per-field answer once they have one:
  // recording it is what stops a decided entry from being held a second time.
  conflict?: PendingConflict;
  resolution?: Record<string, "mine" | "theirs">;
  // gone marks an entry whose row the server no longer has. That is a fact about
  // the row, not a rejection to retry, so it is held for the user like a
  // conflict and must not be mistaken for a queue that will drain on its own.
  gone?: boolean;
}

// CreateEntry is a manual transaction recorded with no connection. `kind` is
// optional and defaults to "create" on read (see parseEntries) because a queue
// written before the union existed carries none, and its shape is already this
// one — the entry is worth exactly as much as it was before the widening.
export interface CreateEntry extends WriteEnvelope {
  kind?: "create";
  request: CreateTransactionRequest;
}

// EditEntry is a field-level patch plus the base it was made against, which is
// what lets the merge tell the user's change from someone else's. `snapshot` is
// the row the form opened with, kept so a second edit to the same row queued
// before the first is flushed is based on what the first left rather than on a
// row the server has not seen yet.
export interface EditEntry extends WriteEnvelope {
  kind: "edit";
  op: WriteOp;
  rowId: string;
  base: FieldPatch;
  patch: FieldPatch;
  snapshot: FieldPatch;
}

// BulkEntry is one field set on many rows. The base is recorded per row rather
// than once for the batch: a single base would merge a row nobody changed
// against a row that did, and hold the whole batch for a conflict on a row the
// user never opened.
export interface BulkEntry extends WriteEnvelope {
  kind: "bulk";
  op: WriteOp;
  field: string;
  value: FieldValue;
  rows: string[];
  bases: Record<string, FieldValue>;
  // The op's non-row identifiers, named after what each endpoint actually takes:
  // `seriesId` for transaction.recurring (RecurringAttachRequest), and
  // `loanAccountId` for transaction.loan (BulkLoanRequest, where omitting it
  // means detach). Detach and disbursement need neither.
  seriesId?: string;
  loanAccountId?: string;
}

export type QueuedWrite = CreateEntry | EditEntry | BulkEntry;

// OutboxEntry is the name the queue had when it held nothing but a create, kept
// so the two consumers that already speak it — the offline banner and the
// offline context — keep compiling against the union rather than being widened
// by hand. It is not a narrower type: nothing in the queue is a "just an entry".
export type OutboxEntry = QueuedWrite;

export interface FlushOutcome {
  sent: number;
  remaining: number;
  failed: number;
  // unsaved counts the entries whose queue state could not be written back:
  // the browser refused the write, so an accepted entry is still queued (its
  // replay is safe — the server recognises the client key) and a rejected one
  // still carries no message. Non-zero stops the flush and is how the caller
  // learns the queue is stuck instead of assuming it drained.
  unsaved: number;
}

const listeners = new Set<() => void>();

// The snapshot returned to useSyncExternalStore: re-read from storage on every
// call (so a write from another tab, or a cleared storage, is visible) but kept
// referentially stable while the stored text is unchanged, since a fresh array
// on each call would re-render forever.
let cachedRaw: string | null = null;
let cachedEntries: QueuedWrite[] = [];

function emit(): void {
  for (const listener of listeners) listener();
}

function storageKey(userId: string): string {
  return `${PREFIX}:${userId}`;
}

function readRaw(userId: string): string | null {
  try {
    return localStorage.getItem(storageKey(userId));
  } catch {
    return null;
  }
}

// adoptKind gives an entry with no `kind` the kind its shape already is. A queue
// written before the union existed has no `kind`, and nothing else about it
// differs from a create, so this is a reading rather than a guess: an unsent
// create is user-recorded money, and the only other reading — an entry this
// cannot place, dropped — destroys it silently and permanently the first time
// the returning user flushes. A kind that is there is left alone, so no entry is
// ever reclassified.
function adoptKind(raw: Record<string, unknown>): QueuedWrite {
  return { ...raw, kind: raw.kind ?? "create" } as QueuedWrite;
}

function parseEntries(raw: string | null): QueuedWrite[] {
  if (!raw) return [];
  try {
    const parsed: unknown = JSON.parse(raw);
    // Unchecked cast: this is our own persisted queue, and a shape change can
    // only cost a queue the user can still see and re-enter. It is why every
    // entry goes through adoptKind rather than a filter that would drop the ones
    // it cannot place, and why the default is safe to supply: the one shape a
    // pre-union entry had is a create's.
    return Array.isArray(parsed)
      ? (parsed as Record<string, unknown>[]).map(adoptKind)
      : [];
  } catch {
    return [];
  }
}

function readEntries(userId: string): QueuedWrite[] {
  return parseEntries(readRaw(userId));
}

// writeEntries persists the queue and reports whether the write landed. The
// stored text is the only copy of the entries, so a `false` here means the queue
// its caller believes in does not exist — nothing in this module holds one in
// memory (getOutboxSnapshot re-reads storage on every call).
function writeEntries(userId: string, entries: QueuedWrite[]): boolean {
  let persisted = false;
  try {
    localStorage.setItem(storageKey(userId), JSON.stringify(entries));
    persisted = true;
  } catch {
    // Storage is full, blocked or unavailable. The caller decides what to
    // report to the user; this function only refuses to lie about it.
  }
  emit();
  return persisted;
}

// subscribeOutbox lets the UI observe the queue (read it through
// getOutboxSnapshot, which keeps a stable reference between changes).
export function subscribeOutbox(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function getOutboxSnapshot(userId: string): QueuedWrite[] {
  const raw = readRaw(userId);
  if (raw !== cachedRaw) {
    cachedRaw = raw;
    cachedEntries = parseEntries(raw);
  }
  return cachedEntries;
}

export function enqueueCreate(
  userId: string,
  request: CreateTransactionRequest,
  key: string,
): CreateEntry {
  const entries = readEntries(userId);
  const existing = entries.find((entry) => entry.key === key);
  // Narrowed, not cast: a key already in the queue is this create's own earlier
  // attempt, and answering a *different* queued write with this create's request
  // would send one entry's body under another's identity.
  if (existing?.kind === "create") return existing;

  // A full queue refuses the new entry instead of evicting the oldest: the
  // entries at the front are transactions the user recorded, and dropping one to
  // make room for another destroys money data nothing can recover — silently,
  // since the header count stays at the cap either way. The refusal reaches the
  // user as this create's error, which is what lets them sync first.
  if (entries.length >= MAX_ENTRIES) {
    throw new Error(
      `Offline queue is full (${MAX_ENTRIES} unsent transactions). Reconnect to sync before recording more.`,
    );
  }

  const entry: CreateEntry = { key, queuedAt: Date.now(), request };
  if (!writeEntries(userId, [...entries, entry])) throw new OutboxStorageError();
  return entry;
}

// removeEntry drops one entry from the queue and reports whether the removal is
// durable; `false` means the entry is still queued.
export function removeEntry(userId: string, key: string): boolean {
  return writeEntries(
    userId,
    readEntries(userId).filter((entry) => entry.key !== key),
  );
}

// discardFailed drops every entry a flush rejected, and returns how many were
// durably discarded. A queue the browser refuses to rewrite throws rather than
// reporting a discard that did not happen: the rejected entries would otherwise
// stay queued forever while the user is told they were dropped.
export function discardFailed(userId: string): number {
  const entries = readEntries(userId);
  const kept = entries.filter((entry) => !entry.error);
  if (kept.length === entries.length) return 0;
  if (!writeEntries(userId, kept)) throw new OutboxStorageError();
  return entries.length - kept.length;
}

// flushOutbox sends queued creates in the order they were recorded. `send` must
// throw an error carrying the HTTP `status` for a rejected request and an error
// without one for a transport failure.
export async function flushOutbox(
  userId: string,
  send: (entry: CreateEntry) => Promise<void>,
  options: { retryFailed?: boolean } = {},
): Promise<FlushOutcome> {
  let sent = 0;
  let unsaved = 0;
  for (const entry of readEntries(userId)) {
    // Only a create is sendable through this seam: an edit or a bulk write has
    // to be merged against the server's row first, and posting one to the create
    // endpoint would be a different request wearing this entry's key. Throwing
    // rather than skipping is the point — a `continue` here would leave the entry
    // queued and counted in `remaining`, which reads as progress to a user
    // waiting for a sync that is never coming, and the only way it can arise is a
    // path this module has not written yet. So it is loud: the entry is untouched
    // and the flush stops, which is what a developer needs to see.
    if (entry.kind !== "create") {
      throw new Error(`flushOutbox cannot send a ${entry.kind} entry: not implemented yet`);
    }
    if (entry.error && !options.retryFailed) continue;

    let accepted = false;
    try {
      await send(entry);
      accepted = true;
    } catch (err) {
      // Anything that is not a definite HTTP rejection (a transport failure, or
      // an error with no status) stops the flush: the entry may not have reached
      // the server, so the queue keeps its order and retries on reconnect.
      if (!(err instanceof ApiError)) break;
      if (err.status === 401 || err.status === 403 || err.status >= 500) break;
      const entries = readEntries(userId);
      const index = entries.findIndex((candidate) => candidate.key === entry.key);
      if (index >= 0) {
        entries[index] = {
          ...entries[index],
          error: err.message || "Rejected by the server",
        };
        // An unrecorded rejection would be retried by the next flush (only a
        // marked entry is skipped) instead of ever reaching the user, so a queue
        // that cannot be rewritten stops the flush here.
        if (!writeEntries(userId, entries)) {
          unsaved += 1;
          break;
        }
      }
      continue;
    }

    if (!accepted) continue;
    // Counted where the server accepted the entry: deriving this from the queue
    // length would subtract entries the user records *during* the flush (each
    // POST can take seconds), reporting no progress and suppressing the reload
    // that shows the transaction just written.
    sent += 1;
    if (!removeEntry(userId, entry.key)) {
      // The entry the server just accepted is still queued: it would be replayed
      // on every reconnect and the queue would never shrink, so the flush stops
      // and reports it (outcome.unsaved) instead of looping or pretending.
      unsaved += 1;
      break;
    }
  }

  const remaining = readEntries(userId);
  return {
    sent,
    remaining: remaining.length,
    failed: remaining.filter((entry) => entry.error).length,
    unsaved,
  };
}
