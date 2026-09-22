import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// The module under test is loaded through a dynamic import so each case gets a
// fresh registry: the point of the prefetch is *which modules get pulled in*,
// and a cached registry would answer the question from the previous case.
async function loadPrefetch() {
  vi.resetModules();
  const seen: string[] = [];
  vi.doMock("../components/Dashboard/Dashboard", () => {
    seen.push("dashboard");
    return { default: () => null };
  });
  vi.doMock("../components/Transactions/Transactions", () => {
    seen.push("transactions");
    return { default: () => null };
  });

  const { prefetchOfflineRoutes } = await import("./prefetchRoutes");
  return { prefetchOfflineRoutes, seen };
}

const originalServiceWorker = Object.getOwnPropertyDescriptor(
  navigator,
  "serviceWorker",
);

function stubServiceWorker(options: {
  controlled: boolean;
  controller?: Record<string, unknown>;
}) {
  const listeners = new Map<string, () => void>();
  Object.defineProperty(navigator, "serviceWorker", {
    configurable: true,
    value: {
      controller: options.controlled
        ? (options.controller ?? { postMessage: () => {} })
        : null,
      ready: Promise.resolve({}),
      addEventListener: (type: string, listener: () => void) => {
        listeners.set(type, listener);
      },
    },
  });
  return listeners;
}

function setOnline(online: boolean) {
  Object.defineProperty(window.navigator, "onLine", {
    configurable: true,
    value: online,
  });
}

describe("prefetchOfflineRoutes", () => {
  beforeEach(() => {
    setOnline(true);
    vi.stubGlobal("requestIdleCallback", (callback: () => void) => callback());
  });

  afterEach(() => {
    vi.unstubAllEnvs();
    vi.unstubAllGlobals();
    vi.doUnmock("../components/Dashboard/Dashboard");
    vi.doUnmock("../components/Transactions/Transactions");
    if (originalServiceWorker) {
      Object.defineProperty(navigator, "serviceWorker", originalServiceWorker);
    } else {
      Reflect.deleteProperty(navigator, "serviceWorker");
    }
  });

  it("does nothing outside a production build", async () => {
    stubServiceWorker({ controlled: true });
    const { prefetchOfflineRoutes, seen } = await loadPrefetch();

    prefetchOfflineRoutes();
    await Promise.resolve();

    expect(seen).toEqual([]);
  });

  it("warms the pages an offline session needs", async () => {
    stubServiceWorker({ controlled: true });
    vi.stubEnv("PROD", true);
    const { prefetchOfflineRoutes, seen } = await loadPrefetch();

    prefetchOfflineRoutes();

    await vi.waitFor(() => expect(seen).toContain("dashboard"));
    expect(seen).toContain("transactions");
  });

  it("waits until the worker controls the page", async () => {
    const listeners = stubServiceWorker({ controlled: false });
    vi.stubEnv("PROD", true);
    const { prefetchOfflineRoutes, seen } = await loadPrefetch();

    prefetchOfflineRoutes();
    await vi.waitFor(() => expect(listeners.has("controllerchange")).toBe(true));
    expect(seen).toEqual([]);

    // A request only reaches the cache once the worker controls the page.
    listeners.get("controllerchange")?.();

    await vi.waitFor(() => expect(seen).toContain("dashboard"));
  });

  it("tells the worker which assets this page already loaded", async () => {
    const messages: unknown[] = [];
    stubServiceWorker({
      controlled: true,
      controller: { postMessage: (message: unknown) => messages.push(message) },
    });
    vi.stubEnv("PROD", true);
    const origin = location.origin;
    vi.spyOn(performance, "getEntriesByType").mockReturnValue([
      { name: `${origin}/assets/Dashboard-abc.js` },
      { name: `${origin}/api/v1/accounts` },
      { name: "https://cdn.example.com/assets/other.js" },
    ] as never);
    const { prefetchOfflineRoutes } = await loadPrefetch();

    prefetchOfflineRoutes();

    await vi.waitFor(() => expect(messages).toHaveLength(1));
    // Only this origin's build assets: an API response is not an asset, and a
    // cross-origin URL is not ours to cache.
    expect(messages[0]).toEqual({
      type: "cache-assets",
      urls: ["/assets/Dashboard-abc.js"],
    });
  });

  it("skips the warm-up while offline", async () => {
    stubServiceWorker({ controlled: true });
    vi.stubEnv("PROD", true);
    setOnline(false);
    const { prefetchOfflineRoutes, seen } = await loadPrefetch();

    prefetchOfflineRoutes();
    await Promise.resolve();
    await Promise.resolve();

    expect(seen).toEqual([]);
  });
});
