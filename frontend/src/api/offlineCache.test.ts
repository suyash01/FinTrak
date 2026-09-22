import { describe, it, expect, beforeEach, vi } from "vitest";
import {
  clearCached,
  isCacheablePath,
  readCached,
  writeCached,
} from "./offlineCache";

const USER = "user-1";

describe("offline cache", () => {
  beforeEach(() => localStorage.clear());

  it("caches only the reads worth having offline", () => {
    expect(isCacheablePath("/transactions?page=1&limit=50")).toBe(true);
    expect(isCacheablePath("/dashboard/summary?groupBy=month")).toBe(true);
    // The money-flow graph is the largest read for the least offline value, and
    // a Paperless document list is an upstream proxy.
    expect(isCacheablePath("/dashboard/money-flow")).toBe(false);
    expect(isCacheablePath("/paperless/documents?page=1")).toBe(false);
  });

  it("round-trips a payload per user and request URL", () => {
    writeCached(USER, "/accounts", [{ id: "a1" }]);

    expect(readCached(USER, "/accounts")).toEqual([{ id: "a1" }]);
    expect(readCached(USER, "/accounts?x=1")).toBeNull();
    expect(readCached("other-user", "/accounts")).toBeNull();
  });

  it("ignores a path outside the allowlist", () => {
    writeCached(USER, "/dashboard/money-flow", { nodes: [] });
    expect(readCached(USER, "/dashboard/money-flow")).toBeNull();
  });

  it("keeps the newest entries when the cap is reached", () => {
    for (let page = 1; page <= 45; page++) {
      writeCached(USER, `/transactions?page=${page}`, { page });
    }

    expect(readCached(USER, "/transactions?page=45")).toEqual({ page: 45 });
    expect(readCached(USER, "/transactions?page=1")).toBeNull();
  });

  it("drops a single entry larger than the per-entry cap", () => {
    writeCached(USER, "/accounts", { blob: "x".repeat(300_000) });
    expect(readCached(USER, "/accounts")).toBeNull();
  });

  it("survives a full quota instead of breaking the page", () => {
    const setItem = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("QuotaExceededError");
      });

    expect(() => writeCached(USER, "/accounts", [])).not.toThrow();
    setItem.mockRestore();

    expect(readCached(USER, "/accounts")).toBeNull();
  });

  it("clears one user's cache without touching another's", () => {
    writeCached(USER, "/accounts", []);
    writeCached("other-user", "/accounts", [{ id: "a2" }]);

    clearCached(USER);

    expect(readCached(USER, "/accounts")).toBeNull();
    expect(readCached("other-user", "/accounts")).toEqual([{ id: "a2" }]);
  });
});
