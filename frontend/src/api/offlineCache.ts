// Last-known-good copies of the reads a phone needs when the network is gone.
//
// Only GET payloads the app can render read-only are kept, and only for an
// explicit allowlist of paths — the ledger's aggregates are all server-computed,
// so the useful offline experience is "show me what I last looked at", not a
// local copy of the database. Writes never go here: an unsent create belongs in
// the outbox (src/api/outbox.ts), which has different retry semantics.
//
// Entries are namespaced per user so one account's data is never served to
// another after a sign-out on a shared browser, and are cleared on sign-out.

const PREFIX = "fintrak_offline:v1";

// Entry count, per-entry and total size caps keep a large ledger from filling
// the ~5MB localStorage quota that the theme and session cache also live in.
const MAX_ENTRIES = 40;
const MAX_ENTRY_CHARS = 200_000;
const MAX_TOTAL_CHARS = 2_000_000;

// CACHEABLE_PATHS is the allowlist, matched against the path without its query.
// Deliberately absent: /dashboard/money-flow and its timeline (a graph payload
// per period is the largest read in the app for the least offline value),
// /paperless/documents (an upstream proxy, and a document list without the
// documents is useless), and every preview/validate POST.
const CACHEABLE_PATHS: Record<string, true> = {
  "/accounts": true,
  "/account-types": true,
  "/categories": true,
  "/groups": true,
  "/payees": true,
  "/tags": true,
  "/recurring": true,
  "/paperless/settings": true,
  "/dashboard/summary": true,
  "/dashboard/cash-flow-calendar": true,
  "/transactions": true,
};

interface CacheEntry {
  // seq is a monotonic counter, not a clock: several reads land in the same
  // millisecond, and eviction must still be deterministic (oldest first) and
  // survive a reload, so the next value continues from the highest stored one.
  seq: number;
  data: unknown;
}

type CacheBlob = Record<string, CacheEntry>;

export function isCacheablePath(path: string): boolean {
  return CACHEABLE_PATHS[path.split("?")[0]] === true;
}

function storageKey(userId: string): string {
  return `${PREFIX}:${userId}`;
}

function readBlob(userId: string): CacheBlob {
  try {
    const raw = localStorage.getItem(storageKey(userId));
    if (!raw) return {};
    const parsed: unknown = JSON.parse(raw);
    // Unchecked cast: our own persisted blob, and a stale shape only costs a
    // cache miss (the app treats a miss as "no offline copy").
    return parsed && typeof parsed === "object" ? (parsed as CacheBlob) : {};
  } catch {
    return {};
  }
}

// evict trims a blob to the entry and size caps, oldest first.
function evict(blob: CacheBlob): CacheBlob {
  const entries = Object.entries(blob).sort((a, b) => b[1].seq - a[1].seq);
  const kept: CacheBlob = {};
  let total = 0;
  for (const [path, entry] of entries) {
    if (Object.keys(kept).length >= MAX_ENTRIES) break;
    const size = JSON.stringify(entry).length;
    if (size > MAX_ENTRY_CHARS) continue;
    if (total + size > MAX_TOTAL_CHARS) break;
    kept[path] = entry;
    total += size;
  }
  return kept;
}

export function readCached<T>(userId: string, path: string): T | null {
  const entry = readBlob(userId)[path];
  return entry ? (entry.data as T) : null;
}

export function writeCached(userId: string, path: string, data: unknown): void {
  if (!isCacheablePath(path)) return;
  const blob = readBlob(userId);
  let next = 1;
  for (const entry of Object.values(blob)) {
    next = Math.max(next, entry.seq + 1);
  }
  blob[path] = { seq: next, data };
  try {
    localStorage.setItem(storageKey(userId), JSON.stringify(evict(blob)));
  } catch {
    // A full quota must never break a page that is otherwise working: drop the
    // cache and let the next successful read rebuild it.
    try {
      localStorage.removeItem(storageKey(userId));
    } catch {
      /* nothing else to try */
    }
  }
}

// clearCached drops one user's cached reads. Called on sign-out: the cached
// payloads are the user's ledger and must not outlive the session.
export function clearCached(userId: string): void {
  try {
    localStorage.removeItem(storageKey(userId));
  } catch {
    /* nothing to clear */
  }
}
