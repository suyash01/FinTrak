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
  discardFailed as discardFailedEntries,
  flushOutbox,
  getOutboxSnapshot,
  subscribeOutbox,
  type OutboxEntry,
} from "../api/outbox";
import { getOfflineSnapshot, subscribeOffline } from "../api/offlineStatus";
import { useAuth } from "./AuthContext";

interface OfflineContextValue {
  // online mirrors the browser's connectivity flag; servedFromCache says the
  // last read was answered from the offline cache rather than the server.
  online: boolean;
  servedFromCache: boolean;
  pending: OutboxEntry[];
  syncing: boolean;
  // syncedAt changes after a flush wrote something, so a page showing ledger
  // data can reload it.
  syncedAt: number;
  sync: (options?: { retryFailed?: boolean }) => Promise<void>;
  discardFailed: () => void;
}

const OfflineContext = createContext<OfflineContextValue | undefined>(undefined);

// OfflineProvider owns the one thing that must happen without the user asking:
// sending the outbox when the connection comes back. It is mounted only inside
// the authenticated tree, because a queued entry belongs to a signed-in user.
export function OfflineProvider({ children }: { children: ReactNode }) {
  const { user } = useAuth();
  const userId = user?.id ?? null;

  const { online, servedFromCache } = useSyncExternalStore(
    subscribeOffline,
    getOfflineSnapshot,
  );
  const pending = useSyncExternalStore(subscribeOutbox, () =>
    getOutboxSnapshot(userId ?? ""),
  );

  const [syncing, setSyncing] = useState(false);
  const [syncedAt, setSyncedAt] = useState(0);
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
            // queue: false — the flush owns the retry, so a transport failure
            // must propagate instead of re-queueing the entry it just took.
            await api.createTransaction(entry.request, {
              idempotencyKey: entry.key,
              queue: false,
            });
          },
          options,
        );
        if (outcome.sent > 0) {
          setSyncedAt(Date.now());
          toast.success(
            `Synced ${outcome.sent} offline transaction${outcome.sent === 1 ? "" : "s"}`,
          );
        }
        if (outcome.failed > 0) {
          const reason = getOutboxSnapshot(userId).find(
            (entry) => entry.error !== undefined,
          )?.error;
          toast.error(
            `${outcome.failed} offline transaction${outcome.failed === 1 ? "" : "s"} was rejected${reason ? `: ${reason}` : ""}`,
          );
        }
      } finally {
        syncingRef.current = false;
        setSyncing(false);
      }
    },
    [userId],
  );

  const discardFailed = useCallback(() => {
    if (!userId) return;
    const dropped = discardFailedEntries(userId);
    if (dropped > 0) {
      toast.success(
        `Discarded ${dropped} offline transaction${dropped === 1 ? "" : "s"}`,
      );
    }
  }, [userId]);

  // Send whatever is waiting as soon as there is a connection — on mount (a
  // queue left over from a previous session) and on every reconnect.
  useEffect(() => {
    if (!online || !userId || pending.length === 0) return;
    void sync();
  }, [online, userId, pending.length, sync]);

  const value = useMemo<OfflineContextValue>(
    () => ({
      online,
      servedFromCache,
      pending,
      syncing,
      syncedAt,
      sync,
      discardFailed,
    }),
    [online, servedFromCache, pending, syncing, syncedAt, sync, discardFailed],
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
