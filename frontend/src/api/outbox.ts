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
// entries behind it, and it must not be retried forever either.

import type { CreateTransactionRequest } from "../types";
import { ApiError } from "./errors";

const PREFIX = "fintrak_outbox:v1";

// A bounded queue: a phone that has been offline for weeks should not grow an
// unbounded localStorage payload.
const MAX_ENTRIES = 100;

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

function writeEntries(userId: string, entries: OutboxEntry[]): void {
  try {
    localStorage.setItem(storageKey(userId), JSON.stringify(entries));
  } catch {
    // Storage is full or unavailable; the in-memory queue still flushes, and
    // the entry stays visible to the user for this session.
  }
  emit();
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

  const entry: OutboxEntry = { key, queuedAt: Date.now(), request };
  // Keep the newest entries when the cap is reached: the oldest are the ones
  // the user is least likely to still want.
  const next = [...entries, entry].slice(-MAX_ENTRIES);
  writeEntries(userId, next);
  return entry;
}

export function removeEntry(userId: string, key: string): void {
  writeEntries(
    userId,
    readEntries(userId).filter((entry) => entry.key !== key),
  );
}

// discardFailed drops every entry a flush rejected, and returns how many went.
export function discardFailed(userId: string): number {
  const entries = readEntries(userId);
  const kept = entries.filter((entry) => !entry.error);
  if (kept.length !== entries.length) writeEntries(userId, kept);
  return entries.length - kept.length;
}

export function clearOutbox(userId: string): void {
  try {
    localStorage.removeItem(storageKey(userId));
  } catch {
    /* nothing to clear */
  }
  emit();
}

// flushOutbox sends queued creates in the order they were recorded. `send` must
// throw an error carrying the HTTP `status` for a rejected request and an error
// without one for a transport failure.
export async function flushOutbox(
  userId: string,
  send: (entry: OutboxEntry) => Promise<void>,
  options: { retryFailed?: boolean } = {},
): Promise<FlushOutcome> {
  const queued = readEntries(userId).length;
  for (const entry of readEntries(userId)) {
    if (entry.error && !options.retryFailed) continue;
    try {
      await send(entry);
      removeEntry(userId, entry.key);
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
        writeEntries(userId, entries);
      }
    }
  }

  const remaining = readEntries(userId);
  return {
    // Entries removed from the queue are the ones that were written.
    sent: queued - remaining.length,
    remaining: remaining.length,
    failed: remaining.filter((entry) => entry.error).length,
  };
}
