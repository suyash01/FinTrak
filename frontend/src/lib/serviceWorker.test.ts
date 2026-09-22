import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import {
  registerServiceWorker,
  shouldRegisterServiceWorker,
} from "./serviceWorker";

// The test environment is a development build, so the production-only paths are
// exercised by stubbing import.meta.env.
function stubServiceWorker(implementation: () => Promise<unknown> = async () => ({})) {
  const register = vi.fn(implementation);
  Object.defineProperty(navigator, "serviceWorker", {
    configurable: true,
    value: { register },
  });
  return register;
}

describe("service worker registration", () => {
  const originalServiceWorker = Object.getOwnPropertyDescriptor(
    navigator,
    "serviceWorker",
  );

  afterEach(() => {
    vi.unstubAllEnvs();
    if (originalServiceWorker) {
      Object.defineProperty(navigator, "serviceWorker", originalServiceWorker);
    } else {
      Reflect.deleteProperty(navigator, "serviceWorker");
    }
    // readyState lives on the prototype, so a stubbed own property has to be
    // removed rather than reassigned.
    Reflect.deleteProperty(document, "readyState");
  });

  it("stays off outside a production build", () => {
    stubServiceWorker();
    // A worker installed against the dev server would outlive HMR and keep
    // serving stale code, so registration is production-only.
    expect(shouldRegisterServiceWorker()).toBe(false);
  });

  it("registers the shell worker in a production build", () => {
    const register = stubServiceWorker();
    vi.stubEnv("PROD", true);

    expect(shouldRegisterServiceWorker()).toBe(true);
    registerServiceWorker();

    expect(register).toHaveBeenCalledWith("/sw.js");
  });

  it("waits for load while the document is still loading", () => {
    const register = stubServiceWorker();
    vi.stubEnv("PROD", true);
    const readyState = Object.getOwnPropertyDescriptor(document, "readyState");
    Object.defineProperty(document, "readyState", {
      configurable: true,
      value: "loading",
    });

    registerServiceWorker();
    expect(register).not.toHaveBeenCalled();

    window.dispatchEvent(new Event("load"));
    expect(register).toHaveBeenCalledWith("/sw.js");

    if (readyState) Object.defineProperty(document, "readyState", readyState);
  });

  it("does not surface a blocked registration", async () => {
    const register = stubServiceWorker(async () => {
      throw new Error("service workers are blocked in this context");
    });
    vi.stubEnv("PROD", true);

    registerServiceWorker();
    await Promise.resolve();

    expect(register).toHaveBeenCalledWith("/sw.js");
  });

  // A tab left open on the previous build asks for a chunk that the deploy
  // removed; Vite reports it here and the only way out is a reload, which
  // fetches the new build's shell and chunks.
  it("reloads when a lazy chunk can no longer be fetched", () => {
    const reload = vi.fn();
    const originalLocation = window.location;
    Object.defineProperty(window, "location", {
      configurable: true,
      value: { pathname: "/transactions", href: "", reload },
    });
    Object.defineProperty(window.navigator, "onLine", {
      configurable: true,
      value: true,
    });

    registerServiceWorker();
    window.dispatchEvent(new Event("vite:preloadError"));

    expect(reload).toHaveBeenCalled();

    Object.defineProperty(window, "location", {
      configurable: true,
      value: originalLocation,
    });
  });

  it("does not reload for a chunk it cannot fetch offline", () => {
    const reload = vi.fn();
    const originalLocation = window.location;
    Object.defineProperty(window, "location", {
      configurable: true,
      value: { pathname: "/transactions", href: "", reload },
    });
    Object.defineProperty(window.navigator, "onLine", {
      configurable: true,
      value: false,
    });

    registerServiceWorker();
    window.dispatchEvent(new Event("vite:preloadError"));

    // Reloading with no network repeats the same failed import forever instead
    // of healing anything.
    expect(reload).not.toHaveBeenCalled();

    Object.defineProperty(window, "location", {
      configurable: true,
      value: originalLocation,
    });
    Object.defineProperty(window.navigator, "onLine", {
      configurable: true,
      value: true,
    });
  });
});
