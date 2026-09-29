import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from "react";
import { toast } from "sonner";
import api from "../api/client";
import {
  discardConflicts as discardHeldEntries,
  discardFailed as discardFailedEntries,
  flushOutbox,
  getOutboxSnapshot,
  removeEntry,
  resolveConflict as recordResolution,
  subscribeOutbox,
  type OutboxEntry,
  type QueuedWrite,
} from "../api/outbox";
import { applyOp, OPS, readTheirs } from "../api/registry";
import { getOfflineSnapshot, markSynced, subscribeOffline } from "../api/offlineStatus";
import { useAuth } from "./AuthContext";

interface OfflineContextValue {
  // online mirrors the browser's connectivity flag; servedFromCache says the
  // last read was answered from the offline cache rather than the server.
  online: boolean;
  servedFromCache: boolean;
  pending: OutboxEntry[];
  // conflicts are the entries waiting on the *user* rather than on the server: a
  // field the merge could not attribute to a side, or a row the server no longer
  // has. They are entries and not counts because the dialog shows the two
  // competing values and the base they were edited away from, and all three
  // travel with the entry. They are also disjoint from the rejected entries in
  // `pending` — recording a hold clears any rejection it answers, outbox.ts's
  // `hold` — which is what lets the banner say which is which, and what stops
  // discardFailed from taking a held edit with the rejected ones.
  conflicts: QueuedWrite[];
  syncing: boolean;
  // syncedAt changes after a flush wrote something, so a page showing ledger
  // data can reload it.
  syncedAt: number;
  sync: (options?: { retryFailed?: boolean }) => Promise<void>;
  discardFailed: () => void;
  discardConflicts: () => void;
  // discardConflict is the one entry's version of discardConflicts, and it is a
  // separate method rather than a key on that one because the dialog offers
  // Discard on the entry the user is looking at: discardConflicts filters on
  // every held entry, so behind a per-entry button it would destroy edits the
  // user was never asked about — the silent discard this whole feature exists to
  // prevent.
  discardConflict: (key: string) => void;
  resolveConflict: (
    key: string,
    resolution: Record<string, "mine" | "theirs">,
  ) => void;
  reCreate: (key: string) => Promise<void>;
}

const OfflineContext = createContext<OfflineContextValue | undefined>(undefined);

// HANDLED_KINDS is a pin with no runtime behaviour, and it is here because the
// dispatch in `sync` cannot be checked by the compiler on its own: a fourth kind
// added to the union would simply match no `case` and fall to the `default`,
// which is a throw at runtime and a comment saying it should not happen. This
// makes it a build failure instead — add a kind to QueuedWrite and this stops
// compiling until the dispatch has a branch for it. Unused by design; the repo
// compiles with noUnusedLocals off.
const HANDLED_KINDS: Record<NonNullable<QueuedWrite["kind"]>, true> = {
  edit: true,
  bulk: true,
  create: true,
};
void HANDLED_KINDS;

// OfflineProvider owns the one thing that must happen without the user asking:
// sending the outbox when the connection comes back. It is mounted only inside
// the authenticated tree, because a queued entry belongs to a signed-in user.
export function OfflineProvider({ children }: { children: ReactNode }) {
  const { user } = useAuth();
  const userId = user?.id ?? null;

  const { online, servedFromCache, syncedAt } = useSyncExternalStore(
    subscribeOffline,
    getOfflineSnapshot,
  );
  const pending = useSyncExternalStore(subscribeOutbox, () =>
    getOutboxSnapshot(userId ?? ""),
  );

  // Derived rather than counted: an entry can be held for a conflict and later
  // be discarded, and a tally kept in state would have to survive every write to
  // the queue. The queue is the only place this is recorded, and it outlives the
  // tab, so the count is read off it rather than mirrored.
  const conflicts = useMemo(
    () =>
      pending.filter(
        (entry) => entry.conflict !== undefined || entry.gone === true,
      ),
    [pending],
  );

  const [syncing, setSyncing] = useState(false);
  // A ref, not the state, guards re-entry: it keeps `sync` stable so the
  // reconnect effect cannot re-trigger itself through a changed dependency.
  const syncingRef = useRef(false);

  const sync = useCallback(
    async (options: { retryFailed?: boolean } = {}) => {
      if (!userId || syncingRef.current) return;
      syncingRef.current = true;
      setSyncing(true);
      try {
        const outcome = await flushOutbox(
          userId,
          async (entry) => {
            // A three-way switch with no fall-through, and the default below it
            // rather than a create as the leftover case. A fourth kind added to
            // the union without a branch here would otherwise land in the create
            // arm with `request` undefined and post a body of nothing — and the
            // whole point of the loud paths in this function is that an unwired
            // write fails visibly instead.
            switch (entry.kind) {
              case "edit": {
                // applyOp, not OPS[op].apply directly: the shape is what decides
                // whether a diff goes out as-is or is overlaid onto the server's
                // own row, and it is the one place that decision is made.
                // `theirs` is read here rather than carried from the merge
                // because the flush hands `send` the decided entry alone, and a
                // `putWhole` op needs the row to overlay onto — a diff on its own
                // would clear every column the body did not name.
                await applyOp(
                  entry.op,
                  entry.rowId,
                  entry.patch,
                  await readTheirs(entry.op, entry.rowId),
                );
                return;
              }
              case "bulk": {
                const spec = OPS[entry.op];
                // Loud rather than skipped. `applyMany` is the whole of a bulk
                // write, so an op without one is a write this seam cannot make,
                // and a flush that swallowed it would remove the entry as
                // applied and report a batch sent that never left the browser.
                // The catch below is what turns that loudness into something the
                // user sees.
                if (!spec.applyMany) {
                  throw new Error(
                    `cannot post a ${entry.op} bulk write: the op has no batch endpoint`,
                  );
                }
                // The rows are the ones the flush narrowed the entry to, so a
                // batch that partly conflicted sends the rows that did not.
                await spec.applyMany(entry.rows, entry.value);
                return;
              }
              case "create": {
                // Nothing to merge: a create is a whole new row, so there is
                // nothing on the server it could collide with.
                //
                // queue: false — the flush owns the retry, so a transport
                // failure must propagate instead of re-queueing the entry it
                // just took.
                await api.createTransaction(entry.request, {
                  idempotencyKey: entry.key,
                  queue: false,
                });
                return;
              }
              // A v1 entry carries no kind and is already a create's shape;
              // outbox.ts's adoptKind reads it as one before it is ever handed
              // here, so this arm is the belt to that function's braces rather
              // than a case production can reach.
              case undefined: {
                await api.createTransaction(entry.request, {
                  idempotencyKey: entry.key,
                  queue: false,
                });
                return;
              }
              default: {
                // The *runtime* half of the exhaustiveness check, and only that:
                // an unmatched `case` is not a type error, so this throw is what
                // catches a kind the switch does not name. The compile-time half
                // is HANDLED_KINDS above, which stops the build instead — so
                // neither half replaces the other, and "simplifying" this switch
                // into a chain of `if`s would silently delete the only one that
                // runs. Thrown rather than narrowed to `never` so the entry's
                // kind is in the message.
                throw new Error(
                  `cannot post a queued write of kind ${JSON.stringify((entry as QueuedWrite).kind)}: no branch sends it`,
                );
              }
            }
          },
          // The reader is what makes the merge happen at all. Without it
          // planWrite throws, so an edit would be unsendable rather than
          // unsafely applied — which is the intended state for a caller with no
          // registry, and not this one. It is registry's readTheirs rather than
          // a reader written here so that "the server's row, never the offline
          // read cache" has exactly one implementation to get wrong: a merge
          // answered from the cache is a merge against this browser's own belief
          // of the row, which agrees with itself and can never find a conflict.
          { ...options, theirs: readTheirs },
        );
        // Only a write revalidates the pages holding ledger data. A held
        // conflict and a gone row are questions for the user, and neither put
        // anything on the server — a page reloading on them would show the same
        // rows it already has and report a sync that did not happen. The
        // `recreated` term is the count the flush cannot yet produce: reCreate
        // below is the only path that writes a gone row and it marks its own
        // sync, and the disjunct is here so a flush that starts counting it does
        // not need this rule re-derived.
        if (outcome.sent > 0 || outcome.recreated > 0) {
          markSynced(Date.now());
        }
        if (outcome.sent > 0) {
          // Entries, not rows, and the unit is stated rather than implied: a
          // bulk write of 199 rows is one entry and one request, so both the
          // number and the noun have to be the ones the user can reconcile with
          // what they watched happen. The noun changed with the dispatch — a
          // create is the only thing this said "transaction" about, and it is no
          // longer the only thing that reaches it.
          toast.success(
            `Synced ${outcome.sent} offline write${outcome.sent === 1 ? "" : "s"}`,
          );
        }
        if (outcome.conflicted > 0) {
          // Fields, not entries, and this flush's count rather than a running
          // total: a still-held entry is re-read and re-merged on the next
          // flush, so it is counted again there. A caller that accumulated it
          // would double-count every conflict the user has not answered yet;
          // the banner displays it, which is the shape that stays honest.
          toast.error(
            `${outcome.conflicted} offline change${outcome.conflicted === 1 ? " needs" : "s need"} your decision: someone else edited the same field${outcome.conflicted === 1 ? "" : "s"}`,
          );
        }
        if (outcome.gone > 0) {
          // Rows, not entries: a batch of two hundred that lost one row is not
          // two hundred failures, and saying so is what tells the user the
          // write mostly landed.
          toast.error(
            `${outcome.gone} offline row${outcome.gone === 1 ? "" : "s"} no longer ${outcome.gone === 1 ? "exists" : "exist"} on the server and ${outcome.gone === 1 ? "was" : "were"} not applied`,
          );
        }
        if (outcome.failed > 0) {
          // "write" for the same reason as the success toast above, and the
          // reason is taken off the first rejected entry rather than counted
          // per kind: the server's message is what tells the user what to change,
          // and it names its own row.
          const reason = getOutboxSnapshot(userId).find(
            (entry) => entry.error !== undefined,
          )?.error;
          toast.error(
            `${outcome.failed} offline write${outcome.failed === 1 ? "" : "s"} was rejected${reason ? `: ${reason}` : ""}`,
          );
        }
        if (outcome.unsaved > 0) {
          // The queue could not be rewritten, so an entry the server accepted is
          // still queued and will be replayed: the user has to know the sync is
          // stuck rather than watch the pending count never drop.
          toast.error(
            "Could not update the offline queue: browser storage is unavailable or full. Nothing was lost — reconnect and sync again.",
          );
        }
      } catch (err) {
        // The flush rethrows what it cannot classify — a dispatch this seam
        // cannot make, a reader that is not the registry's — and both callers
        // write `void sync()`, so without this the queue is intact, nothing is
        // mis-applied, and the user is told nothing at all. They would be left
        // watching a pending count that never drops and reading it as "still
        // syncing", which is the worst available answer: the writes are all still
        // there, so this is a report, not a loss.
        toast.error(
          `The offline sync stopped before the queue could drain: ${(err as Error).message}. Nothing was lost — the remaining writes are still queued.`,
        );
      } finally {
        syncingRef.current = false;
        setSyncing(false);
      }
    },
    [userId],
  );

  const discardFailed = useCallback(() => {
    if (!userId) return;
    let dropped: number;
    try {
      dropped = discardFailedEntries(userId);
    } catch (err) {
      // The entries are still queued: saying nothing would leave the user
      // clicking a button that does not do what it says.
      toast.error((err as Error).message);
      return;
    }
    if (dropped > 0) {
      // "write" rather than "transaction", as above — a rejected entry is
      // whatever the server declined, and an edit can be declined too.
      toast.success(
        `Discarded ${dropped} offline write${dropped === 1 ? "" : "s"}`,
      );
    }
  }, [userId]);

  // The explicit counterpart to discardFailed, and separate from it because the
  // two answer different questions: a rejected entry is a payload the server
  // would not take, while a held one is an edit nobody has decided about yet.
  // Folding them into one button would drop the user's own work behind a
  // decision about the server's.
  const discardConflicts = useCallback(() => {
    if (!userId) return;
    let dropped: number;
    try {
      dropped = discardHeldEntries(userId);
    } catch (err) {
      // The entries are still queued, for discardFailed's reason: saying nothing
      // would leave the user clicking a button that does not do what it says,
      // over edits of their own.
      toast.error((err as Error).message);
      return;
    }
    if (dropped > 0) {
      toast.success(
        `Discarded ${dropped} offline write${dropped === 1 ? "" : "s"}`,
      );
    }
  }, [userId]);

  // The one-entry counterpart to discardConflicts, and the one a dialog calls:
  // its Discard button sits on the entry the user is reading, so discarding
  // every held entry behind it would take edits they were never shown. It is
  // removeEntry rather than a narrowed discardConflicts because the two
  // questions are already answered before it gets here — the queue only shows
  // held entries, and the caller passes the key of the one it means.
  const discardConflict = useCallback(
    (key: string) => {
      if (!userId) return;
      // A key that is no longer queued is a discard that already happened, so
      // it is not a storage failure and nothing is said about it — the same
      // rule outbox's mutators follow.
      if (!getOutboxSnapshot(userId).some((entry) => entry.key === key)) return;
      if (!removeEntry(userId, key)) {
        // The entry is still queued: saying nothing would leave the user
        // clicking a button that does not do what it says, over edits of their
        // own.
        toast.error(
          "Could not discard the change: browser storage is unavailable or full. Nothing was lost — it is still queued, so try again.",
        );
        return;
      }
      toast.success("Discarded the offline change");
    },
    [userId],
  );

  const resolveConflict = useCallback(
    (key: string, resolution: Record<string, "mine" | "theirs">) => {
      if (!userId) return;
      // A `false` here is not a no-op to be ignored: the entry still carries its
      // conflict, so the next flush re-reads the server's row and holds the same
      // question again — and a dialog that closed over a decision the queue
      // never learned about is the silent discard this feature exists to
      // prevent.
      if (!recordResolution(userId, key, resolution)) {
        toast.error(
          "Could not record your answer: browser storage is unavailable or full. Nothing was lost — the change is still queued, so try again.",
        );
      }
    },
    [userId],
  );

  const reCreate = useCallback(
    async (key: string) => {
      if (!userId) return;
      // Read from the queue rather than from `conflicts`: the entry is the only
      // place the snapshot and the patch both exist, and a key that is no longer
      // queued is a re-create that already happened.
      const entry = getOutboxSnapshot(userId).find(
        (candidate) => candidate.key === key,
      );
      if (!entry) return;
      if (entry.kind !== "edit") {
        toast.error("Only a single-row change can be re-created as a new row.");
        return;
      }
      // `gone` and not merely `conflict`, and this guard is the one that matters
      // most on this function: the server's row has to be *absent* for a create
      // to be the right answer. A conflicted entry's row is still there — that is
      // what a conflict means, one field of it moved — so a create would insert a
      // second money row and then remove the entry and report the conflict
      // resolved. The dialog is expected to offer Re-create only for a gone row,
      // and it should, but this is a public method on a context value and the
      // precondition belongs to the seam that owns the effect, not to whichever
      // surface happens to call it.
      if (!entry.gone) {
        toast.error(
          "That change cannot be re-created: its row is still on the server, so creating it again would duplicate it. Resolve the conflicting fields instead.",
        );
        return;
      }
      // Present only on transaction rows, so an op without one is a row this
      // path cannot rebuild — the server's create endpoint makes a transaction
      // and nothing else. Refusing is better than a row that is not the one the
      // user was editing.
      const reCreateRow = OPS[entry.op].reCreate;
      if (!reCreateRow) {
        toast.error(
          `This kind of change cannot be re-created as a new row: ${entry.op} has no create endpoint.`,
        );
        return;
      }
      // The entry's own key, so a re-create whose response was lost (a timeout,
      // a killed tab) is recognised on the next attempt rather than inserting a
      // second money row. It is the same key the entry was stored under, and the
      // server matches it per user.
      let id: string;
      try {
        id = await reCreateRow(entry.snapshot, entry.patch, entry.key);
      } catch (err) {
        // Caught rather than propagated, and the entry deliberately stays
        // queued. Both ways this can fail leave the row unwritten — a transport
        // failure reached nobody, and a rejection is the server declining a
        // payload — so re-creating again is the right answer either way, and the
        // message is the server's own where there is one. Letting the rejection
        // escape would leave a click with nothing to show and no way back to the
        // dialog, since the caller cannot act on a promise it was handed as a
        // plain handler.
        toast.error((err as Error).message);
        return;
      }
      // Only now: the row exists on the server, and an entry left queued would be
      // replayed on the next flush against a row the user has already been told
      // about. A removal the browser refuses is reported rather than swallowed,
      // for the sync's reason — the entry is still there, and the user watching
      // the pending count is the only way they would learn the queue is stuck.
      if (!removeEntry(userId, key)) {
        toast.error(
          "The transaction was re-created, but this browser could not update the offline queue. Nothing was lost — the re-created row will not be created twice.",
        );
        return;
      }
      // A new row is a change to the server's copy of the ledger, so the pages
      // showing ledger data are stale until they reload.
      markSynced(Date.now());
      // The new id is named because the row that came back is not the row the
      // user was editing: they are still holding the old one, and a message
      // without it would leave them looking for an edit that moved.
      toast.success(`Re-created the transaction as a new one (${id})`);
    },
    [userId],
  );

  // Send whatever is waiting as soon as there is a connection — on mount (a
  // queue left over from a previous session) and on every reconnect.
  //
  // `decided` is in the dependency list because giving an answer is itself a
  // reason to send. Recording a resolution clears the hold and adds the answer
  // to an entry that stays queued, so `pending.length` does not move and
  // nothing else would ever flush it: on a connected device the user's
  // decision would sit there until the next reconnect, a relaunch, or a manual
  // "Sync now" — after a dialog they had just answered. It is a count rather
  // than the entries themselves because the queue hands out a fresh array on
  // every read, and an array as the dependency would re-fire the flush on any
  // write to the queue, including the one the flush is making.
  const decided = useMemo(
    () => pending.filter((entry) => entry.resolution !== undefined).length,
    [pending],
  );

  useEffect(() => {
    if (!online || !userId || pending.length === 0) return;
    void sync();
  }, [online, userId, pending.length, decided, sync]);

  const value = useMemo<OfflineContextValue>(
    () => ({
      online,
      servedFromCache,
      pending,
      conflicts,
      syncing,
      syncedAt,
      sync,
      discardFailed,
      discardConflicts,
      discardConflict,
      resolveConflict,
      reCreate,
    }),
    [
      online,
      servedFromCache,
      pending,
      conflicts,
      syncing,
      syncedAt,
      sync,
      discardFailed,
      discardConflicts,
      discardConflict,
      resolveConflict,
      reCreate,
    ],
  );

  return (
    <OfflineContext.Provider value={value}>{children}</OfflineContext.Provider>
  );
}

export function useOffline(): OfflineContextValue {
  const context = useContext(OfflineContext);
  if (context === undefined) {
    throw new Error("useOffline must be used within an OfflineProvider");
  }
  return context;
}
