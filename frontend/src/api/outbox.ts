// The outbox: manual transactions recorded while the API was unreachable.
//
// Each entry carries a client-generated idempotency key, which the create
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
import { ApiError } from "./errors";

const PREFIX = "fintrak_outbox:v1";

// A bounded queue: a phone that has been offline for weeks should not grow an
// unbounded localStorage payload.
const MAX_ENTRIES = 100;

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

export interface OutboxEntry {
  // key is the create's idempotency key; it is stable across retries.
  key: string;
  queuedAt: number;
  request: CreateTransactionRequest;
  // error is the server's rejection message from the last flush attempt.
  error?: string;
}

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
let cachedEntries: OutboxEntry[] = [];

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

function parseEntries(raw: string | null): OutboxEntry[] {
  if (!raw) return [];
  try {
    const parsed: unknown = JSON.parse(raw);
    // Unchecked cast: this is our own persisted queue, and a shape change can
    // only cost a queue the user can still see and re-enter.
    return Array.isArray(parsed) ? (parsed as OutboxEntry[]) : [];
  } catch {
    return [];
  }
}

function readEntries(userId: string): OutboxEntry[] {
  return parseEntries(readRaw(userId));
}

// writeEntries persists the queue and reports whether the write landed. The
// stored text is the only copy of the entries, so a `false` here means the queue
// its caller believes in does not exist — nothing in this module holds one in
// memory (getOutboxSnapshot re-reads storage on every call).
function writeEntries(userId: string, entries: OutboxEntry[]): boolean {
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

export function getOutboxSnapshot(userId: string): OutboxEntry[] {
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
): OutboxEntry {
  const entries = readEntries(userId);
  const existing = entries.find((entry) => entry.key === key);
  if (existing) return existing;

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

  const entry: OutboxEntry = { key, queuedAt: Date.now(), request };
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
  send: (entry: OutboxEntry) => Promise<void>,
  options: { retryFailed?: boolean } = {},
): Promise<FlushOutcome> {
  let sent = 0;
  let unsaved = 0;
  for (const entry of readEntries(userId)) {
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
