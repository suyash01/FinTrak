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
});
