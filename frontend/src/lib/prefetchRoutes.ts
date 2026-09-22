// Warm the chunks of the pages an offline session needs.
//
// A hand-written worker cannot know Vite's hashed chunk names, so the only way
// to put a lazy route in the cache is to request it through the worker — which
// is what importing these modules does. Only the two pages that must work
// without a network are listed: the dashboard (the default route) and
// transactions (where an offline entry is recorded). Every other page needs one
// online visit before it can be opened offline.
//
// The paths are imported statically, so a renamed component fails the build
// instead of silently dropping that page from the cache.

async function whenControlled(): Promise<void> {
  if (navigator.serviceWorker.controller) return;
  await new Promise<void>((resolve) => {
    navigator.serviceWorker.addEventListener("controllerchange", () => resolve(), {
      once: true,
    });
  });
}

// reportLoadedAssets hands the worker the asset URLs this page has already
// loaded. A route whose module is already in the module map is not re-fetched
// by the import below, so without this its chunk would only be in the browser's
// HTTP cache — and the offline guarantee would rest on that cache not being
// evicted.
function reportLoadedAssets(): void {
  const urls = performance
    .getEntriesByType("resource")
    .map((entry) => entry.name)
    .filter((name) => name.startsWith(location.origin))
    .map((name) => new URL(name).pathname)
    .filter((path) => path.startsWith("/assets/"));

  navigator.serviceWorker.controller?.postMessage({
    type: "cache-assets",
    urls,
  });
}

export function prefetchOfflineRoutes(): void {
  if (!import.meta.env.PROD || !("serviceWorker" in navigator)) return;

  void (async () => {
    // A request only lands in the cache once the worker controls this page, so
    // wait for it rather than racing the install that runs at load.
    await navigator.serviceWorker.ready;
    await whenControlled();
    if (navigator.onLine === false) return;

    const warm = async () => {
      await Promise.allSettled([
        import("../components/Dashboard/Dashboard"),
        import("../components/Transactions/Transactions"),
      ]);
      reportLoadedAssets();
    };

    if (typeof requestIdleCallback === "function") requestIdleCallback(warm);
    else setTimeout(warm, 3000);
  })();
}
