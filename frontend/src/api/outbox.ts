// The outbox: writes recorded while the API was unreachable — a manual
// transaction, an edit to a row, or one field set on many rows.
//
// A create carries a client-generated idempotency key, which the create
// endpoint uses to recognise a replay — so an entry whose response was lost
// (a timeout, a killed tab) is applied exactly once when it is finally sent.
// The same key is reused on every retry, which is what makes the retry safe.
//
// An edit or a bulk write is a field-level patch plus the base it was made
// against, so the flush can tell the user's change from someone else's: it
// reads the server's current row, merges the two three ways (see merge.ts), and
// takes one of four routes per entry — apply it, hold a conflict for the user,
// record that the row is gone, or mark it rejected. A held entry stays in the
// queue and is re-read and re-merged on every later flush, so the user decides
// against the server's latest state rather than a snapshot that has moved again;
// what is sent is always the queued patch, never the merge's whole-row output,
// because sending the latter would revert the fields the user never touched. The
// one family whose endpoint writes every column its body can carry — the payee,
// rule, recurring and loan PUTs — is not an exception: its request is that same
// patch overlaid on the server's row, and the overlay is the op registry's
// (registry.ts's mergedRow, reached through applyOp) rather than a second merge
// here. The values in that body are still the patch's and the server's.
//
// Queue semantics on flush: a network failure or a server/session problem
// (401/403/5xx) stops the flush and keeps the queue in order for the next
// reconnect, while a rejected payload (4xx) is marked with the server's message
// and left for the user to retry or discard — one bad entry must not block the
// entries behind it, and it must not be retried forever either. A held conflict
// extends that rule to something that is not a failure at all: a question for
// the user is not a reason to stop, so the entry behind a held one is sent
// normally. A queue the browser refuses to rewrite stops the flush too and is
// reported as `unsaved`: the alternative is claiming progress that is not stored
// anywhere.

import type { CreateTransactionRequest } from "../types";
import { mergeFields, type FieldPatch, type FieldValue } from "./merge";
import { ApiError, NetworkError } from "./errors";

const PREFIX = "fintrak_outbox:v1";

// A bounded queue: a phone that has been offline for weeks should not grow an
// unbounded localStorage payload.
export const MAX_ENTRIES = 100;

// A byte budget beside the entry count, for the reason the read cache has one:
// count is not what fills the ~5MB localStorage quota. An edit carries the base
// and the snapshot of a row, so one large text field is enough to fill a
// megabyte on its own, where a create is far too small to reach this inside
// MAX_ENTRIES. The check is on the queue rather than on the kind of entry, so a
// create respects the budget too, even though nothing about it can reach it.
export const MAX_TOTAL_CHARS = 1_000_000;

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
  // conflicted counts the *fields* the merge held for the user, which is what
  // the caller has to put in front of them — a dialog over three fields is three
  // answers, and one entry can hold more than one.
  conflicted: number;
  // gone counts the *rows* the server no longer has, so a bulk write that lost
  // one row of two hundred says so instead of reporting a batch of two hundred.
  gone: number;
  // recreated counts the gone rows the user chose to write again as new rows. No
  // path in this module produces one yet — a gone row is held for the user, not
  // re-created behind their back — and the field is here because this object is
  // the caller's single view of a flush, which a counter that appears only once
  // something can increment it would change under them.
  recreated: number;
}

// TheirsReader reads what the server holds now for one row of a queued write, and
// answers `null` when the server no longer has the row. It is injected rather
// than read from a module, so the merge is testable on its own and so that every
// call site in this file shows where the row comes from: it is the server's, and
// never the offline read cache — a merge answered from the cache is a merge
// against the client's own belief, which can only agree with itself.
//
// It takes the row rather than the entry so one reader serves a single-row entry
// and each row of a bulk entry, and it must return the row whole: a field the
// response omits reads as a field the server cleared, which the merge then holds
// as a conflict the user never caused.
export type TheirsReader = (op: WriteOp, rowId: string) => Promise<FieldPatch | null>;

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

// newEntryKey mints the key an edit or a bulk write is stored and removed under.
// It is generated rather than derived from the row, because two edits to the
// same row queued before a flush are an ordinary thing to do — that is the whole
// case the projected base below exists for — and a key like `${op}:${rowId}`
// would collapse them into one entry and lose the first. The fallback mirrors
// client.ts's newClientKey: crypto.randomUUID needs a secure context, and a
// self-hosted instance reached over a LAN address without TLS is not one.
function newEntryKey(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return `e-${crypto.randomUUID()}`;
  }
  return `e-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 12)}`;
}

// enforceCapacity is the queue's refusal, and it refuses rather than evicts: the
// entries already queued are writes the user recorded, and dropping the oldest
// to make room destroys them silently — the count stays at the cap either way,
// so nothing tells the user. The refusal reaches the write as its error, which
// is what lets them sync first. Both caps raise this one error and it names both
// numbers, because the remedy is the same either way and a message naming only
// the cap that was *not* reached would misdescribe what happened.
function enforceCapacity(entries: QueuedWrite[], next: QueuedWrite): void {
  // The count is tested first so a full queue does not pay for serializing itself
  // to learn it is full.
  if (entries.length >= MAX_ENTRIES || JSON.stringify([...entries, next]).length > MAX_TOTAL_CHARS) {
    throw new Error(
      `Offline queue is full (${MAX_ENTRIES} unsent writes, ${MAX_TOTAL_CHARS} characters). Reconnect to sync before recording more.`,
    );
  }
}

// enqueueInto is the tail every enqueue ends in, because the two decisions it
// makes are the same for all three kinds of write and must be the same: a queue
// at its cap refuses, and a queue the browser will not write throws rather than
// handing back an entry that exists only in the caller.
function enqueueInto(userId: string, entries: QueuedWrite[], entry: QueuedWrite): void {
  enforceCapacity(entries, entry);
  if (!writeEntries(userId, [...entries, entry])) throw new OutboxStorageError();
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

  const entry: CreateEntry = { key, queuedAt: Date.now(), request };
  enqueueInto(userId, entries, entry);
  return entry;
}

// enqueueEdit records one field-level patch to one row, against the base it was
// made from. It follows enqueueCreate's shape for the same reason that function
// throws rather than returns a phantom success: the queue is the source of truth,
// so an edit the browser refused to store is an edit the user believes is saved.
export function enqueueEdit(
  userId: string,
  op: WriteOp,
  rowId: string,
  base: FieldPatch,
  patch: FieldPatch,
  snapshot: FieldPatch,
): EditEntry {
  const entries = readEntries(userId);
  const key = newEntryKey();
  // Unreachable with a generated key, and kept anyway: every enqueue path reads
  // the same way, and this is the guard that says a key already in the queue is
  // never written a second time.
  const existing = entries.find((entry) => entry.key === key);
  if (existing?.kind === "edit") return existing;

  // The stored base is the row as it stands *locally*: the caller's base — the
  // server's row as of the moment this form opened — advanced by every write the
  // queue will make to this row. Only the writes are overlaid, never a whole row
  // read back off an earlier entry: that entry's base is a snapshot of an earlier
  // moment, and the server does not stand still for a queued edit (a flush that
  // stopped at a 401/5xx leaves its entries queued while somebody else moves the row
  // on), so a snapshot winning here puts a field nobody queued a write for back to
  // a value the user has already read past — and the merge then holds that field as
  // a conflict between the user and a third party who never touched it.
  //
  // Projecting at all is what keeps the opposite failure away: a base that predates
  // an earlier queued write reads as the user having changed the fields that write
  // changed, so the merge holds a phantom conflict on a field they never argued
  // about. Only the projection says "the user moved this from 'first' to 'second'",
  // which is what happened.
  const entry: EditEntry = {
    key,
    queuedAt: Date.now(),
    kind: "edit",
    op,
    rowId,
    base: { ...base, ...Object.fromEntries(pendingWrites(entries, rowId)) },
    patch,
    snapshot,
  };
  enqueueInto(userId, entries, entry);
  return entry;
}

// enqueueBulk records one field set on many rows. It is a single queue entry
// because the user asked for a single action: the flush sends one request, and
// splitting it would let half of it land and be reported as progress.
export function enqueueBulk(
  userId: string,
  op: WriteOp,
  field: string,
  value: FieldValue,
  rows: string[],
  bases: Record<string, FieldValue>,
  ids?: { seriesId?: string; loanAccountId?: string },
): BulkEntry {
  const entries = readEntries(userId);
  const key = newEntryKey();
  // As in enqueueEdit: the key is generated, so this can only ever find an entry
  // this function wrote itself, and the shape is kept identical across the three.
  const existing = entries.find((entry) => entry.key === key);
  if (existing?.kind === "bulk") return existing;

  const entry: BulkEntry = {
    key,
    queuedAt: Date.now(),
    kind: "bulk",
    op,
    field,
    value,
    rows,
    bases,
    ...ids,
  };
  enqueueInto(userId, entries, entry);
  return entry;
}

// pendingWrites lists what the queue will put on one row, as `[field, value]` pairs
// in the order the entries were recorded: everything queued for the row is a writer
// to it, matched on row membership and field name and nothing else. An edit
// contributes the fields its patch carries; a bulk write contributes the one field it
// sets, for every row it lists.
//
// The op is deliberately not part of the match. The transaction ops — .patch, .payee,
// .categorize, .tags, .billingCycle, .loan, .recurring, .loanDisbursement — all
// address the same transaction row and differ only in how the write travels, and a
// bulk write and a single edit are the same kind of writer twice over. Matching on
// the op would make a queued write invisible to the next edit's base whenever the two
// went through different ops, and the merge would then hold a conflict on a field the
// user edited, over a change the user themselves made.
//
// One list for everyone who has to know what is pending for a row, because they have
// to agree: the code that stores a base and a reader that draws the row would
// otherwise disagree about what the row is about to say.
function pendingWrites(
  entries: QueuedWrite[],
  rowId: string,
): [string, FieldValue][] {
  const writes: [string, FieldValue][] = [];
  for (const entry of entries) {
    if (entry.kind === "bulk") {
      if (entry.rows.includes(rowId)) writes.push([entry.field, entry.value]);
    } else if (entry.kind === "edit" && entry.rowId === rowId) {
      for (const [field, value] of Object.entries(entry.patch)) {
        // An explicit undefined is not a field (merge.ts: absent is not null), and
        // JSON would have dropped it on the way into storage in any case.
        if (value !== undefined) writes.push([field, value]);
      }
    }
  }
  return writes;
}

// queuedPatchFor answers one field's pending value: what the row will hold once the
// queue drains, which is what a caller must draw in place of the server's value
// while an entry is still queued. The last writer of the field is what the row will
// say, so the list is walked backwards; a field nothing in the queue carries is
// undefined, which reads as untouched rather than cleared (see merge.ts: absent is
// not null).
export function queuedPatchFor(
  userId: string,
  rowId: string,
  field: string,
): FieldValue {
  const writes = pendingWrites(readEntries(userId), rowId);
  for (let i = writes.length - 1; i >= 0; i -= 1) {
    if (writes[i][0] === field) return writes[i][1];
  }
  return undefined;
}

// projectionOf is the row as it stands locally, seeded from the first queued edit's
// base. That seed is a whole-row snapshot and it belongs to a reader with no row of
// its own to start from, so it is *not* what enqueueEdit stores as a base — see the
// comment there, where a superseded snapshot would regress the fields no queued write
// touches.
function projectionOf(entries: QueuedWrite[], rowId: string): FieldPatch | null {
  const queued = entries.filter(
    (entry): entry is EditEntry => entry.kind === "edit" && entry.rowId === rowId,
  );
  // A bulk write carries no base for the row, so with no edit queued there is
  // nothing to seed from and the projection is the bulk write's own contribution.
  const writes = pendingWrites(entries, rowId);
  if (queued.length === 0 && writes.length === 0) return null;

  const row: FieldPatch = { ...queued[0]?.base };
  for (const [field, value] of writes) row[field] = value;
  return row;
}

// queuedRowProjection is that row: the first queued edit's base with every queued
// write applied in order, or null when nothing at all is queued for the row. It is
// the only answer to "what does this row say now" while the queue is not empty — the
// server's response is a row the user has since moved away from.
//
// The overlay is a plain last-write one, and a bulk write to the same row is one of
// those writers, in the order it was queued: the user may have edited a row before
// and after a bulk write, and the last one to write a field is what the row will say.
// merge.ts's valuesEqual is a comparison, not a merge, and there is nothing here to
// reconcile: these entries are the user's own.
export function queuedRowProjection(
  userId: string,
  rowId: string,
): FieldPatch | null {
  return projectionOf(readEntries(userId), rowId);
}

// removeEntry drops one entry from the queue and reports whether the removal is
// durable; `false` means the entry is still queued.
export function removeEntry(userId: string, key: string): boolean {
  return writeEntries(
    userId,
    readEntries(userId).filter((entry) => entry.key !== key),
  );
}

// mutateEntry changes one entry in place and reports whether the change was
// stored — the only question each mutator below has to answer, and the one the
// flush turns into `unsaved`. A key that is not in the queue is not a storage
// failure: the entry the caller meant is already gone, which is the state it
// asked for.
function mutateEntry(
  userId: string,
  key: string,
  change: (entry: QueuedWrite) => QueuedWrite,
): boolean {
  const entries = readEntries(userId);
  const index = entries.findIndex((entry) => entry.key === key);
  if (index < 0) return true;
  entries[index] = change(entries[index]);
  return writeEntries(userId, entries);
}

// isRejection is the one question the flush asks about a failed send: did the
// server give a definite answer about this entry? A transport failure, a session
// problem and a server error may all have reached nobody, and those keep the
// queue in order; anything else is an answer about the entry itself.
function isRejection(err: unknown): err is ApiError {
  if (!(err instanceof ApiError)) return false;
  return err.status !== 401 && err.status !== 403 && err.status < 500;
}

// markRejected records the server's message on the entry, which is what a 4xx
// means: the entry stays queued carrying the reason, is not attempted again
// until the user asks for a retry, and does not block the entries behind it.
function markRejected(userId: string, key: string, message: string): boolean {
  return mutateEntry(userId, key, (entry) => ({
    ...entry,
    error: message || "Rejected by the server",
  }));
}

// recordConflict holds a queued write for the user, and reports whether the hold
// was stored. The conflict travels with the entry rather than in the page for the
// reason the queue exists: a question the user has to be told about cannot be
// something only this tab remembers.
//
// It clears `error`, and that is what makes discardFailed's filter on `error`
// alone true rather than merely asserted. An entry can reach a conflict already
// carrying a rejection: the server refused it, the user asked for a retry, and
// the retry is a fresh plan against the server's row as it stands *then* — which
// may have moved, and which is the whole reason a second attempt can end in a
// conflict at all. Left in place, the entry would sit in both the rejected count
// and the held list, and "discard the writes the server rejected" would destroy
// an edit nobody has been told about yet. A conflict supersedes the rejection it
// answers: it is the newer information, it is about specific fields, and only
// the user can settle it.
export function recordConflict(userId: string, key: string, conflict: PendingConflict): boolean {
  return mutateEntry(userId, key, (entry) => {
    const { error: _superseded, ...rest } = entry;
    return { ...rest, conflict };
  });
}

// recordGone marks a queued write whose row the server no longer has. It is a
// fact about the row rather than a rejection to retry — there is nothing to
// retry against — so it is held for the user like a conflict.
export function recordGone(userId: string, key: string): boolean {
  return mutateEntry(userId, key, (entry) => ({ ...entry, gone: true }));
}

// resolveConflict records the user's per-field answer and drops the question with
// it: an entry whose fields are all decided is not a held conflict, and a record
// left on it would let discardConflicts destroy a write the user has resolved but
// not yet sent. There is no "applied" flag — the decision is the entry, and
// sending it again is safe because a field-level patch is idempotent.
export function resolveConflict(
  userId: string,
  key: string,
  resolution: Record<string, "mine" | "theirs">,
): boolean {
  return mutateEntry(userId, key, ({ conflict: _held, ...entry }) => ({ ...entry, resolution }));
}

// discardFailed drops every entry a flush rejected, and returns how many were
// durably discarded. A queue the browser refuses to rewrite throws rather than
// reporting a discard that did not happen: the rejected entries would otherwise
// stay queued forever while the user is told they were dropped.
//
// It filters on `error` alone, and must stay that way: an entry held for the user
// has no rejection behind it — recordConflict clears one when it records a hold,
// which is what makes that true rather than merely hoped for — and dropping it
// here would destroy an edit the user has not been told about yet.
// discardConflicts is the one that answers "the question is settled".
export function discardFailed(userId: string): number {
  const entries = readEntries(userId);
  const kept = entries.filter((entry) => !entry.error);
  if (kept.length === entries.length) return 0;
  if (!writeEntries(userId, kept)) throw new OutboxStorageError();
  return entries.length - kept.length;
}

// discardConflicts drops every entry held for the user — a conflict, or a row the
// server no longer has — and returns how many were durably discarded. It refuses
// rather than reporting a discard that did not happen, for discardFailed's reason:
// these are the user's own edits, kept in the queue precisely because nobody has
// decided about them yet.
export function discardConflicts(userId: string): number {
  const entries = readEntries(userId);
  const kept = entries.filter((entry) => !entry.conflict && !entry.gone);
  if (kept.length === entries.length) return 0;
  if (!writeEntries(userId, kept)) throw new OutboxStorageError();
  return entries.length - kept.length;
}

// WritePlan is one queued write decided, before anything is sent or written back:
// what may go out, and what is held for the user instead. Both are answers about
// the entry, so an entry is either sent whole, sent in part, or not sent at all —
// never sent and held at once, which would leave the queue claiming a write that
// the server has already taken.
interface WritePlan {
  // send is the entry as it may go out — narrowed to the rows that merged clean,
  // or carrying the values the user decided — or null when nothing in it may.
  send: QueuedWrite | null;
  // conflict is the units held for the user, or null when there are none.
  conflict: PendingConflict | null;
  // gone is how many of the entry's rows the server no longer has.
  gone: number;
}

// planEdit decides one queued edit against the server's current row.
async function planEdit(entry: EditEntry, theirs: TheirsReader): Promise<WritePlan> {
  const server = await theirs(entry.op, entry.rowId);
  // null is the row, not a field: the server does not have it, so there is
  // nothing to merge against and nothing to write to.
  if (server === null) return { send: null, conflict: null, gone: 1 };

  // A decided entry is not merged again. The user answered against a row the
  // server has since moved past, and re-reading it would hold them a second time
  // over a change they never saw — the decision stands and goes out as it stands.
  if (entry.resolution) {
    // The answers are an override of the queued patch, never a replacement for it.
    // A resolution can only name the fields that conflicted, and the fields that
    // merged clean were always going to be sent — a decided patch built from the
    // answers alone would drop the rest of the user's edit and the entry would
    // then be removed and reported as synced, taking their change to a field
    // they were never asked about with it.
    const decided: FieldPatch = { ...entry.patch };
    for (const [field, side] of Object.entries(entry.resolution)) {
      // "mine" is what the patch already carries. "theirs" is written by not
      // naming the field, which is what a field-level patch means by leaving a
      // value alone (see merge.ts: absent is not null): carrying the queued value
      // for a field the user chose to leave would re-assert the very value they
      // declined to write. A putWhole op reads that omission the other way round
      // and gets it right by itself: its endpoint writes every column, so the
      // registry overlays this patch onto the server's own row and the field the
      // user kept is written back as the value they kept (see registry.ts's
      // mergedRow).
      if (side === "theirs") delete decided[field];
    }
    // Nothing left to write is not a write. Every field answered "theirs" leaves
    // an empty patch, and the bulk path already reads an empty batch as "remove
    // without a request" (see planBulk); sending this one would put a request on
    // the wire with no fields in it and count the entry as sent for it.
    if (Object.keys(decided).length === 0) return { send: null, conflict: null, gone: 0 };
    return { send: { ...entry, patch: decided }, conflict: null, gone: 0 };
  }

  const merged = mergeFields(entry.base, entry.patch, server);
  if (merged.conflicts.length > 0) {
    return {
      send: null,
      conflict: { units: merged.conflicts.map((unit) => ({ rowId: entry.rowId, ...unit })) },
      gone: 0,
    };
  }

  // entry.patch, not merged.patch. The merge walks the union of all three key
  // sets, so its patch echoes every field of the row, and sending it would revert
  // a concurrent change to a field the user never touched — the exact bug this
  // merge exists to prevent. On a clean merge the engine's job is to confirm that
  // nothing conflicts, not to rewrite the payload. The one family for which a
  // whole-row body *is* the right payload does not change the answer either: the
  // registry builds that body from this same patch and the server's row
  // (applyOp's mergedRow), so every field in it is still one the user or the
  // server wrote.
  return { send: entry, conflict: null, gone: 0 };
}

// planBulk decides a queued bulk write: one request over rows that can end in
// three different states at once. The rows that merged clean are sent — the user
// asked for all of them, and dropping the rest along with them is the failure
// this feature exists to prevent — and the rest are recorded.
async function planBulk(entry: BulkEntry, theirs: TheirsReader): Promise<WritePlan> {
  const clean: string[] = [];
  const units: ConflictUnit[] = [];
  let gone = 0;
  // Read in the entry's own row order, so the units come out in the order the
  // user chose the rows and a conflict reads like the list they were shown.
  for (const rowId of entry.rows) {
    const server = await theirs(entry.op, rowId);
    if (server === null) {
      gone += 1;
      continue;
    }
    if (entry.resolution) {
      // "theirs" is the server's own value: the user kept it, so that row is no
      // longer part of the write.
      if (entry.resolution[entry.field] === "theirs") continue;
      clean.push(rowId);
      continue;
    }
    const merged = mergeFields(
      { [entry.field]: entry.bases[rowId] },
      { [entry.field]: entry.value },
      server,
    );
    if (merged.conflicts.length > 0) {
      units.push(...merged.conflicts.map((unit) => ({ rowId, ...unit })));
      continue;
    }
    clean.push(rowId);
  }

  return {
    // rows, not the entry's own list: what may be applied is what the caller is
    // handed, the same narrowing an edit's patch gets.
    send: clean.length > 0 ? { ...entry, rows: clean } : null,
    conflict: units.length > 0 ? { units } : null,
    gone,
  };
}

// isRowWrite is the one narrowing the flush needs, and it holds for the reason
// adoptKind does: every entry read from storage has been given a kind, so an
// entry that is not a create is an edit or a bulk write. CreateEntry declares
// `kind` optional for a v1 entry, so the type alone cannot make that leap — the
// two facts together can, and the alternative is a cast at the one place the
// whole dispatch turns on.
function isRowWrite(entry: QueuedWrite): entry is EditEntry | BulkEntry {
  return entry.kind !== "create";
}

// planWrite merges a queued edit or bulk write against the server's current row.
// A reader is required rather than optional: without one there is nothing to
// merge against, and sending the entry as it stands is the exact failure the
// merge exists to prevent, so an unwired flush is loud instead of reverting
// somebody else's change to a row the user never saw.
async function planWrite(
  entry: EditEntry | BulkEntry,
  theirs: TheirsReader | undefined,
): Promise<WritePlan> {
  if (!theirs) {
    throw new Error(
      `flushOutbox cannot merge the ${entry.kind} entry: no theirs reader was supplied`,
    );
  }
  return entry.kind === "edit" ? planEdit(entry, theirs) : planBulk(entry, theirs);
}

// flushOutbox sends the queue in the order it was recorded, through one seam the
// caller implements: a create posts it, an edit PATCHes or PUTs it, and a bulk
// calls applyMany over the rows it is given. `send` must throw an ApiError
// carrying the HTTP `status` for a rejected request and a NetworkError for a
// request that never reached the server.
export async function flushOutbox(
  userId: string,
  send: (entry: QueuedWrite) => Promise<void>,
  options: { retryFailed?: boolean; theirs?: TheirsReader } = {},
): Promise<FlushOutcome> {
  let sent = 0;
  let unsaved = 0;
  let conflicted = 0;
  let gone = 0;
  for (const entry of readEntries(userId)) {
    if (entry.error && !options.retryFailed) continue;

    // What goes out is decided before anything is sent: a create has nothing to
    // merge, and an edit or a bulk write is read and merged first, which is why
    // both arrive at the same send — one of them may be nothing but "post this"
    // and the other a request narrowed to a third of its rows, and the flush
    // must not grow a second dispatch to tell them apart.
    let plan: WritePlan;
    try {
      plan = isRowWrite(entry)
        ? await planWrite(entry, options.theirs)
        : { send: entry, conflict: null, gone: 0 };
    } catch (err) {
      // Reading the server's row is part of the flush, not a lookup beside it, so
      // a read that fails stops the flush exactly as a failed send would: nothing
      // was applied, so the queue keeps its order and retries on reconnect. Both
      // ways a server says no land there — a transport failure and a session or
      // server error alike, because a row the server will not serve right now is
      // not a row to skip past and answer later, and the reader is written over
      // api.get*, which throws ApiError for a 500. Neither is a rejection of the
      // entry, so nothing is recorded on it. Anything that is neither is a broken
      // reader rather than an offline moment, and is left loud.
      if (err instanceof NetworkError || err instanceof ApiError) break;
      throw err;
    }

    if (plan.send) {
      let accepted = false;
      try {
        await send(plan.send);
        accepted = true;
      } catch (err) {
        if (isRejection(err)) {
          // An unrecorded rejection would be retried by the next flush (only a
          // marked entry is skipped) instead of ever reaching the user, so a queue
          // that cannot be rewritten stops the flush here.
          if (!markRejected(userId, entry.key, err.message)) {
            unsaved += 1;
            break;
          }
          continue;
        }
        // A transport failure and a session or server error both stop the flush
        // with the queue in order: neither is a definite answer about this entry,
        // so nothing is recorded on it. An error that is neither an ApiError nor
        // a NetworkError is a broken dispatch, and must not be reported as an
        // offline moment — a silent stop is indistinguishable from a connection
        // that never came back, and the user is told only that the queue waits.
        if (err instanceof ApiError || err instanceof NetworkError) break;
        throw err;
      }
      if (!accepted) continue;
      // Counted where the server accepted the entry: deriving this from the queue
      // length would subtract entries the user records *during* the flush (each
      // request can take seconds), reporting no progress and suppressing the
      // reload that shows the row just written.
      sent += 1;
    }

    // A row the server no longer has never stops the flush: there is nothing to
    // decide about a row that is not there, and stranding the write behind it
    // would block the ones the user can still make. It is counted either way —
    // in the outcome, and on the entry itself when nothing in the entry could be
    // applied at all.
    if (plan.gone > 0) gone += plan.gone;

    if (plan.conflict) {
      if (!recordConflict(userId, entry.key, plan.conflict)) {
        unsaved += 1;
        break;
      }
      conflicted += plan.conflict.units.length;
      // Held, not failed, and it does not stop the flush: the rows the user has
      // to answer for are not in what was sent, and the entry stays queued so the
      // answer can be given — while the entries behind it are writes the user can
      // still make. The entry keeps every row it was given, so a later flush
      // re-reads them all and re-sends the rows already applied, which is
      // harmless: a field-level write of a value the row already holds changes
      // nothing.
      continue;
    }

    if (plan.gone > 0 && plan.send === null) {
      // Nothing in the entry went out at all, so the entry itself is the record:
      // dropping it quietly would report a write that cannot be made as one that
      // drained. An entry that *did* apply is not held on this — its work is
      // done, and a row that is not there cannot be written to, so it drains and
      // `gone` is the record of why part of the batch is not there.
      if (!recordGone(userId, entry.key)) {
        unsaved += 1;
        break;
      }
      continue;
    }

    if (!removeEntry(userId, entry.key)) {
      // The entry is still queued. A sent one would be replayed on every
      // reconnect and the queue would never shrink, so the flush stops and
      // reports it (outcome.unsaved) instead of looping or pretending.
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
    conflicted,
    gone,
    recreated: 0,
  };
}
