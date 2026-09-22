import { readFileSync } from "node:fs";
import { join } from "node:path";
import { URL as NodeURL } from "node:url";
import { describe, it, expect } from "vitest";

// public/sw.js is a classic worker script, not a module, so it is exercised the
// way the browser runs it: evaluated against a stubbed `self` scope with a fake
// Cache Storage. The fake keys each entry by its absolute URL (as the real API
// does) and records which cache a write landed in, so a precache that stores
// copies no request can match shows up as a failed offline read.
const SOURCE = readFileSync(
  join(import.meta.dirname, "..", "..", "public", "sw.js"),
  "utf8",
);

const ORIGIN = "http://localhost";

// The cache names are derived from the worker's VERSION rather than re-declared
// here: bumping VERSION is the documented release step for invalidating an
// installed app's caches, and a copy of the names in this file would turn that
// step into a false failure against a worker that is in fact correct.
const VERSION = /const VERSION = "([^"]+)"/.exec(SOURCE)?.[1];
if (!VERSION) throw new Error("sw.js no longer declares VERSION");
const SHELL_CACHE = `${VERSION}-shell`;
const ASSET_CACHE = `${VERSION}-assets`;

// A copy of the worker source with a different VERSION, so a deploy can be
// simulated without editing the file under test.
function sourceWithVersion(version: string): string {
  const patched = SOURCE.replace(/const VERSION = "[^"]+"/, `const VERSION = "${version}"`);
  if (patched === SOURCE) throw new Error("sw.js no longer declares VERSION");
  return patched;
}

// Cache Storage normalizes every key to an absolute URL, and the worker runs
// against Node's URL rather than jsdom's (whose base is the document URL), so
// the fake storage and the assertions resolve keys the same way.
const absolute = (path: string) => new NodeURL(path, ORIGIN).href;

const HTML = [
  "<!doctype html><html><head>",
  '<link rel="stylesheet" href="/assets/index-abc.css">',
  "</head><body>",
  '<script type="module" src="/assets/index-abc.js"></script>',
  "</body></html>",
].join("");

class FakeResponse {
  readonly ok = true;
  readonly type = "basic";

  constructor(readonly body: string) {}

  clone(): FakeResponse {
    return new FakeResponse(this.body);
  }

  text(): Promise<string> {
    return Promise.resolve(this.body);
  }

  static error(): FakeResponse {
    return new FakeResponse("");
  }
}

interface WorkerHarness {
  /** cache name → stored entries (absolute URL → body). */
  caches: Map<string, Map<string, string>>;
  /** how many times the worker itself fetched the shell document. */
  shellFetches: () => number;
  install(): Promise<unknown>;
  activate(): Promise<unknown>;
  navigate(path: string): Promise<FakeResponse | undefined>;
  asset(path: string): Promise<FakeResponse | undefined>;
}

// loadWorker evaluates the worker source with the network split in two, because
// the browser has two: `fetch` is what the worker itself calls, while `fill` is
// what the Cache API reaches when it stores a URL (cache.add), which works even
// when the worker's own fetches are failing.
function loadWorker(
  network: {
    fetch: (url: string) => Promise<FakeResponse>;
    fill: (url: string) => Promise<FakeResponse>;
  },
  source: string = SOURCE,
): WorkerHarness {
  const stores = new Map<string, Map<string, string>>();
  const listeners = new Map<string, (event: Record<string, unknown>) => void>();

  function open(name: string) {
    let store = stores.get(name);
    if (!store) stores.set(name, (store = new Map<string, string>()));
    const cache = {
      add: async (url: string) => {
        store.set(absolute(url), (await network.fill(url)).body);
      },
      addAll: async (urls: string[]) => {
        for (const url of urls) await cache.add(url);
      },
      put: async (request: string | { url: string }, response: FakeResponse) => {
        store.set(
          typeof request === "string" ? absolute(request) : request.url,
          response.body,
        );
      },
      match: async (request: string | { url: string }) => {
        const key =
          typeof request === "string" ? absolute(request) : request.url;
        const body = store.get(key);
        return body === undefined ? undefined : new FakeResponse(body);
      },
    };
    return cache;
  }

  const caches = {
    open: (name: string) => Promise.resolve(open(name)),
    keys: () => Promise.resolve([...stores.keys()]),
    delete: (name: string) => Promise.resolve(stores.delete(name)),
  };

  let shellFetches = 0;

  const self = {
    location: { origin: ORIGIN },
    addEventListener: (type: string, handler: (event: never) => void) => {
      listeners.set(type, handler as unknown as (event: Record<string, unknown>) => void);
    },
    skipWaiting: () => Promise.resolve(),
    clients: { claim: () => Promise.resolve() },
  };

  new Function("self", "caches", "fetch", "URL", "Response", source)(
    self,
    caches,
    (url: string) => {
      if (url === "/index.html") shellFetches += 1;
      return network.fetch(url);
    },
    NodeURL,
    FakeResponse,
  );

  async function dispatch(
    type: string,
    request: Record<string, unknown>,
  ): Promise<unknown> {
    let result: Promise<unknown> | undefined;
    listeners.get(type)?.({
      request,
      waitUntil: (promise: Promise<unknown>) => {
        result = promise;
      },
      respondWith: (promise: Promise<unknown>) => {
        result = promise;
      },
    });
    return result;
  }

  return {
    caches: stores,
    shellFetches: () => shellFetches,
    install: () => dispatch("install", {}),
    activate: () => dispatch("activate", {}),
    navigate: (path) =>
      dispatch("fetch", {
        method: "GET",
        mode: "navigate",
        url: absolute(path),
      }) as Promise<FakeResponse | undefined>,
    asset: (path) =>
      dispatch("fetch", {
        method: "GET",
        mode: "cors",
        url: absolute(path),
      }) as Promise<FakeResponse | undefined>,
  };
}

describe("offline shell worker", () => {
  // One response per URL, so the network the worker talks to and the network the
  // Cache API fills from agree on what each URL returns.
  const respond = (url: string) =>
    new FakeResponse(url === "/index.html" ? HTML : `bundle ${url}`);

  function boot(source: string = SOURCE) {
    let online = true;
    const worker = loadWorker(
      {
        fetch: (url) =>
          online
            ? Promise.resolve(respond(url))
            : Promise.reject(new TypeError("Failed to fetch")),
        fill: (url) => Promise.resolve(respond(url)),
      },
      source,
    );
    return {
      worker,
      goOffline: () => {
        online = false;
      },
    };
  }

  it("precaches the shell document's bundles into the cache they are read from", async () => {
    const { worker } = boot();
    await worker.install();

    // /assets/* is dispatched to cacheFirst, which opens the asset cache, so the
    // install precache has to write there or the copies are unreachable.
    expect([...(worker.caches.get(ASSET_CACHE) ?? new Map()).keys()]).toEqual(
      expect.arrayContaining([
        absolute("/assets/index-abc.js"),
        absolute("/assets/index-abc.css"),
      ]),
    );

    const shellKeys = [...(worker.caches.get(SHELL_CACHE) ?? new Map()).keys()];
    expect(shellKeys).toContain(absolute("/index.html"));
    expect(shellKeys.some((key) => key.includes("/assets/"))).toBe(false);
  });

  // The shell document names the bundles, so the cached shell and the bundle
  // list have to come from the same response: a second read can land after a
  // deploy and pair one build's shell with another build's chunks.
  it("reads the shell document once, for both the shell and the bundle list", async () => {
    const { worker } = boot();

    await worker.install();

    expect(worker.shellFetches()).toBe(1);
  });

  // Keeping the generation before the current one is what lets a tab left open
  // on the previous build load the rest of its route chunks: those requests are
  // answered by cacheFirst from that build's asset cache.
  it("keeps the previous build's caches and drops older generations", async () => {
    const { worker } = boot(sourceWithVersion("fintrak-v3"));
    for (const name of [
      "fintrak-v3-assets",
      "fintrak-v2-assets",
      "fintrak-v1-assets",
      "fintrak-v1-shell",
      "workbox-precache",
    ]) {
      worker.caches.set(name, new Map<string, string>());
    }

    await worker.activate();

    expect([...worker.caches.keys()]).toEqual([
      "fintrak-v3-assets",
      "fintrak-v2-assets",
    ]);
  });

  it("boots the shell and its bundles from cache with no network", async () => {
    const { worker, goOffline } = boot();
    await worker.install();
    goOffline();

    // The navigation falls back to the cached shell document, and the bundle
    // requests that document triggers are answered from the install precache: a
    // shell whose scripts cannot be fetched is a blank page.
    const shell = await worker.navigate("/transactions");
    expect(await shell?.text()).toBe(HTML);

    const bundle = await worker.asset("/assets/index-abc.js");
    expect(await bundle?.text()).toBe("bundle /assets/index-abc.js");
  });
});
