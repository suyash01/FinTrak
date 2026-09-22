// FinTrak's offline shell.
//
// Scope, deliberately: this worker caches the *app shell* so an installed app
// still boots without a network, and nothing else. API responses are NOT cached
// here — they carry the user's ledger and are cached explicitly, per path, by
// the app itself (src/api/offlineCache.ts), so that what is kept offline is a
// decision in reviewable code rather than an accident of HTTP caching.
//
// Bump VERSION when a change must invalidate the caches of an installed app:
// activate drops every cache that does not carry the new prefix.

const VERSION = "fintrak-v1";
const SHELL_CACHE = `${VERSION}-shell`;
const ASSET_CACHE = `${VERSION}-assets`;

// The shell entry point is stored under a canonical key so any navigation can
// fall back to it — the SPA router resolves the route after boot.
const SHELL_KEY = "/index.html";

const SHELL_ASSETS = [
  "/index.html",
  "/theme-init.js",
  "/manifest.webmanifest",
  "/favicon.svg",
  "/icons/icon-192.png",
  "/icons/icon-512.png",
  "/icons/icon-maskable-512.png",
  "/icons/apple-touch-icon.png",
];

self.addEventListener("install", (event) => {
  event.waitUntil(
    (async () => {
      const cache = await caches.open(SHELL_CACHE);
      await cache.addAll(SHELL_ASSETS);
      await precacheBuildAssets(cache);
      // Take over on the next load instead of waiting for every tab to close:
      // a self-hosted app has no fleet of sessions to protect.
      await self.skipWaiting();
    })(),
  );
});

// The shell document names the hashed bundles, so reading it once at install
// makes the very first controlled load work offline. Without this the shell
// would boot to a page whose scripts are not cached yet, because the assets of
// the visit that installed the worker were fetched before it took control.
async function precacheBuildAssets(cache) {
  try {
    const response = await fetch(SHELL_KEY, { cache: "reload" });
    const html = await response.text();
    const urls = new Set();
    for (const match of html.matchAll(/(?:src|href)="(\/assets\/[^"]+)"/g)) {
      urls.add(match[1]);
    }
    await Promise.all(
      [...urls].map((url) => cache.add(url).catch(() => undefined)),
    );
  } catch {
    // Best effort: the shell document is the only hard requirement, and a
    // missing bundle is fetched (and then cached) on the next online visit.
  }
}

self.addEventListener("activate", (event) => {
  event.waitUntil(
    (async () => {
      const names = await caches.keys();
      await Promise.all(
        names
          .filter((name) => !name.startsWith(VERSION))
          .map((name) => caches.delete(name)),
      );
      await self.clients.claim();
    })(),
  );
});

// The app reports the assets its current page loaded, so a chunk that was
// fetched before this worker took control (or is already in the module map and
// therefore never re-requested) still ends up in the cache.
self.addEventListener("message", (event) => {
  const data = event.data;
  if (!data || data.type !== "cache-assets" || !Array.isArray(data.urls)) return;

  event.waitUntil(
    (async () => {
      const cache = await caches.open(ASSET_CACHE);
      await Promise.all(
        data.urls
          .filter((url) => typeof url === "string" && url.startsWith("/assets/"))
          .map((url) => cache.add(url).catch(() => undefined)),
      );
    })(),
  );
});

// Vite emits content-hashed bundles, so a cached asset can never be stale;
// everything else unhashed (theme bootstrap, icons) is revalidated in the
// background. `sw.js` itself is never cached — the browser must be able to see
// a new worker.
const HASHED_ASSET = /^\/assets\//;
const STATIC_ASSET =
  /\.(?:js|css|mjs|woff2?|ttf|eot|png|svg|jpe?g|gif|ico|webmanifest)$/;

self.addEventListener("fetch", (event) => {
  const { request } = event;
  if (request.method !== "GET") return;

  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;
  if (url.pathname.startsWith("/api/")) return;
  if (url.pathname === "/sw.js") return;

  if (request.mode === "navigate") {
    event.respondWith(networkFirstShell(request));
    return;
  }
  if (HASHED_ASSET.test(url.pathname)) {
    event.respondWith(cacheFirst(request));
    return;
  }
  if (STATIC_ASSET.test(url.pathname)) {
    event.respondWith(staleWhileRevalidate(request));
    return;
  }
});

// Navigations prefer the network so a deploy is picked up on the next load, and
// fall back to the cached shell when there is no network.
async function networkFirstShell(request) {
  const cache = await caches.open(SHELL_CACHE);
  try {
    const response = await fetch(request);
    if (response.ok) await cache.put(SHELL_KEY, response.clone());
    return response;
  } catch (error) {
    const cached = await cache.match(SHELL_KEY);
    if (cached) return cached;
    throw error;
  }
}

async function cacheFirst(request) {
  const cache = await caches.open(ASSET_CACHE);
  const cached = await cache.match(request);
  if (cached) return cached;
  const response = await fetch(request);
  if (response.ok && response.type === "basic") {
    await cache.put(request, response.clone());
  }
  return response;
}

async function staleWhileRevalidate(request) {
  const cache = await caches.open(SHELL_CACHE);
  const cached = await cache.match(request);
  const network = fetch(request)
    .then(async (response) => {
      if (response.ok) await cache.put(request, response.clone());
      return response;
    })
    .catch(() => undefined);
  return cached ?? (await network) ?? Response.error();
}
