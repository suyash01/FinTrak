import { describe, it, expect, beforeEach, vi } from "vitest";
import {
  getOfflineSnapshot,
  markSynced,
  setServedFromCache,
  subscribeOffline,
} from "./offlineStatus";

// jsdom never changes navigator.onLine on its own, so a test drives both the
// flag and the event the browser would fire with it.
function setOnline(online: boolean) {
  Object.defineProperty(window.navigator, "onLine", {
    configurable: true,
    value: online,
  });
  window.dispatchEvent(new Event(online ? "online" : "offline"));
}

describe("offline status", () => {
  beforeEach(() => {
    setServedFromCache(false);
    // The store outlives a single test file's cases, so the flush signal is
    // reset the same way the cache flag is.
    markSynced(0);
    setOnline(true);
  });

  it("tracks the browser's connectivity flag", () => {
    const seen: boolean[] = [];
    const unsubscribe = subscribeOffline(() =>
      seen.push(getOfflineSnapshot().online),
    );

    setOnline(false);
    setOnline(true);
    unsubscribe();

    expect(seen).toEqual([false, true]);
  });

  it("stops notifying once unsubscribed", () => {
    const listener = vi.fn();
    const unsubscribe = subscribeOffline(listener);
    unsubscribe();

    setOnline(false);

    expect(listener).not.toHaveBeenCalled();
  });

  it("reports a read served from the cache until a live response arrives", () => {
    expect(getOfflineSnapshot().servedFromCache).toBe(false);

    setServedFromCache(true);
    expect(getOfflineSnapshot().servedFromCache).toBe(true);

    setServedFromCache(false);
    expect(getOfflineSnapshot().servedFromCache).toBe(false);
  });

  it("re-reads the connectivity flag when the UI subscribes", () => {
    // A page restored from the cache can render before the browser applies its
    // connectivity state, so the first subscriber must not trust the value read
    // at module load.
    setServedFromCache(false);
    Object.defineProperty(window.navigator, "onLine", {
      configurable: true,
      value: false,
    });

    const unsubscribe = subscribeOffline(() => {});
    expect(getOfflineSnapshot().online).toBe(false);
    unsubscribe();

    setOnline(true);
  });

  it("records when the outbox last drained, and notifies once", () => {
    const listener = vi.fn();
    const unsubscribe = subscribeOffline(listener);

    expect(getOfflineSnapshot().syncedAt).toBe(0);
    markSynced(1_700_000_000_000);
    // The same value twice is not a change: consumers refetch on the value.
    markSynced(1_700_000_000_000);
    expect(getOfflineSnapshot().syncedAt).toBe(1_700_000_000_000);
    expect(listener).toHaveBeenCalledTimes(1);

    markSynced(0);
    expect(getOfflineSnapshot().syncedAt).toBe(0);
    unsubscribe();
  });

  it("keeps the snapshot reference stable when nothing changed", () => {
    const before = getOfflineSnapshot();
    setServedFromCache(false);
    setOnline(true);
    expect(getOfflineSnapshot()).toBe(before);
  });
});
