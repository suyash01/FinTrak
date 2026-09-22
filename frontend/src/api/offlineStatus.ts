// The network state the offline layer and the UI share.
//
// `online` mirrors the browser's own connectivity flag (the same signal the
// service worker and the install prompt use), and `servedFromCache` records
// that the most recent API read was answered from the offline cache rather than
// the server — which is invisible to a caller otherwise, since a cached read
// resolves like any other.
//
// A tiny external store (read through useSyncExternalStore) rather than a React
// context: the API layer must be able to report into it without importing React.

export interface OfflineSnapshot {
  online: boolean;
  servedFromCache: boolean;
}

const listeners = new Set<() => void>();

function currentOnline(): boolean {
  // jsdom and any non-browser import path have no navigator.onLine.
  return typeof navigator === "undefined" || navigator.onLine !== false;
}

let snapshot: OfflineSnapshot = {
  online: currentOnline(),
  servedFromCache: false,
};

let listening = false;

function emit(): void {
  for (const listener of listeners) listener();
}

function syncOnline(): void {
  const online = currentOnline();
  if (online === snapshot.online) return;
  snapshot = { ...snapshot, online };
  emit();
}

// Listening starts with the first subscriber so an unsubscribed import (tests,
// SSR-ish paths) installs no window listeners.
function startListening(): void {
  if (listening || typeof window === "undefined") return;
  listening = true;
  window.addEventListener("online", syncOnline);
  window.addEventListener("offline", syncOnline);
}

export function getOfflineSnapshot(): OfflineSnapshot {
  return snapshot;
}

export function subscribeOffline(listener: () => void): () => void {
  startListening();
  // A page restored from the cache can render before the browser has applied
  // its own connectivity state, so re-read the flag as the UI subscribes.
  // React re-reads the snapshot after subscribing, so a change here still
  // reaches the first render.
  syncOnline();
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

// setServedFromCache is called by the API layer: true when a read was answered
// from the cache, false as soon as a live response arrives.
export function setServedFromCache(served: boolean): void {
  if (served === snapshot.servedFromCache) return;
  snapshot = { ...snapshot, servedFromCache: served };
  emit();
}
