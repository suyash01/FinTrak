import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import api, {
  getToken,
  setToken,
  getStoredUser,
  storeUser,
  downloadCSV,
} from "./client";

const API_BASE = "http://localhost:8080/api/v1";

function jsonResponse(body, status = 200, headers = {}) {
  const ok = status >= 200 && status < 300;
  return {
    ok,
    status,
    statusText: status === 401 ? "Unauthorized" : "OK",
    headers: new Headers(headers),
    json: async () => body,
    text: async () => (typeof body === "string" ? body : JSON.stringify(body)),
    blob: async () =>
      new Blob([typeof body === "string" ? body : JSON.stringify(body)]),
  };
}

describe("token storage", () => {
  beforeEach(() => localStorage.clear());

  it("setToken stores and getToken retrieves", () => {
    setToken("abc123");
    expect(getToken()).toBe("abc123");
  });

  it("setToken(null) removes the token", () => {
    setToken("abc123");
    setToken(null);
    expect(getToken()).toBeNull();
  });
});

describe("user storage", () => {
  beforeEach(() => localStorage.clear());

  it("storeUser persists a JSON object", () => {
    const user = { id: 1, email: "a@b.c" };
    storeUser(user);
    expect(getStoredUser()).toEqual(user);
  });

  it("storeUser(null) removes the stored user", () => {
    storeUser({ id: 1 });
    storeUser(null);
    expect(getStoredUser()).toBeNull();
  });

  it("getStoredUser returns null for invalid JSON", () => {
    localStorage.setItem("fintrak_user", "{not json");
    expect(getStoredUser()).toBeNull();
  });
});

describe("api request", () => {
  let fetchMock;
  const originalLocation = window.location;

  beforeEach(() => {
    localStorage.clear();
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    // jsdom throws "Not implemented: navigation" when href is assigned, so
    // replace window.location with a plain object for the 401 redirect tests.
    Object.defineProperty(window, "location", {
      configurable: true,
      value: {
        pathname: "/transactions",
        href: "",
        assign: vi.fn(),
        replace: vi.fn(),
        reload: vi.fn(),
        toString: () => "",
      },
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
    Object.defineProperty(window, "location", {
      configurable: true,
      value: originalLocation,
    });
  });

  it("sends JSON requests to the API base URL", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ token: "t", user: { id: 1 } }));
    const res = await api.login({ email: "a@b.c", password: "pw" });
    expect(res).toEqual({ token: "t", user: { id: 1 } });
    const [url, opts] = fetchMock.mock.calls[0];
    expect(url).toBe(`${API_BASE}/auth/login`);
    expect(opts.method).toBe("POST");
    expect(opts.headers["Content-Type"]).toBe("application/json");
    expect(JSON.parse(opts.body)).toEqual({ email: "a@b.c", password: "pw" });
  });

  it("attaches the bearer token when present", async () => {
    setToken("tok123");
    fetchMock.mockResolvedValue(jsonResponse([]));
    await api.getAccounts();
    const [, opts] = fetchMock.mock.calls[0];
    expect(opts.headers.Authorization).toBe("Bearer tok123");
  });

  it("builds query strings for list endpoints", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ data: [] }));
    await api.getTransactions({ accountId: "acct-1", limit: 50 });
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe(`${API_BASE}/transactions?accountId=acct-1&limit=50`);
  });

  it("returns null for empty response bodies", async () => {
    fetchMock.mockResolvedValue(jsonResponse("", 204));
    const res = await api.deleteAccount("acct-1");
    expect(res).toBeNull();
  });

  it("throws an error with status for non-ok responses", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ error: "Not found" }, 404));
    await expect(api.getAccounts()).rejects.toMatchObject({
      message: "Not found",
      status: 404,
    });
  });

  it("falls back to statusText when the error body is not JSON", async () => {
    fetchMock.mockResolvedValue({
      ok: false,
      status: 500,
      statusText: "Internal Server Error",
      json: async () => {
        throw new Error("bad json");
      },
      text: async () => "oops",
    });
    await expect(api.getAccounts()).rejects.toThrow("Internal Server Error");
  });

  it("throws a network error when fetch rejects", async () => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));
    await expect(api.getAccounts()).rejects.toThrow(
      "Network error: could not reach the API server",
    );
  });

  it("throws a timeout error when the request exceeds the timeout", async () => {
    vi.useFakeTimers();
    fetchMock.mockImplementation((_url, opts) => {
      return new Promise((_resolve, reject) => {
        opts.signal.addEventListener("abort", () => {
          const err = new Error("Aborted");
          err.name = "AbortError";
          reject(err);
        });
      });
    });
    const promise = api.getAccounts();
    vi.advanceTimersByTime(15000);
    await expect(promise).rejects.toThrow("Request timed out");
  });

  it("clears auth and redirects to /login on 401", async () => {
    setToken("tok");
    storeUser({ id: 1 });
    fetchMock.mockResolvedValue(jsonResponse({ error: "Unauthorized" }, 401));
    await expect(api.getAccounts()).rejects.toThrow("Unauthorized");
    expect(getToken()).toBeNull();
    expect(getStoredUser()).toBeNull();
    expect(window.location.href).toBe("/login");
  });

  it("does not redirect for 401 on the login endpoint", async () => {
    fetchMock.mockResolvedValue(
      jsonResponse({ error: "Bad credentials" }, 401),
    );
    await expect(api.login({ email: "a", password: "b" })).rejects.toThrow(
      "Bad credentials",
    );
    expect(window.location.href).not.toBe("/login");
  });
});

describe("downloadCSV", () => {
  let fetchMock;
  let clickSpy;

  beforeEach(() => {
    localStorage.clear();
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    clickSpy = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(() => {});
    URL.createObjectURL = vi.fn(() => "blob:mock");
    URL.revokeObjectURL = vi.fn();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    clickSpy.mockRestore();
  });

  it("downloads a CSV with the filename from Content-Disposition", async () => {
    const blob = new Blob(["a,b\n1,2"]);
    fetchMock.mockResolvedValue({
      ok: true,
      status: 200,
      headers: new Headers({
        "Content-Disposition": 'attachment; filename="transactions.csv"',
      }),
      blob: async () => blob,
    });

    await downloadCSV("/transactions/export");

    expect(fetchMock).toHaveBeenCalledWith(
      `${API_BASE}/transactions/export`,
      expect.anything(),
    );
    expect(URL.createObjectURL).toHaveBeenCalledWith(blob);
    expect(clickSpy).toHaveBeenCalled();
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:mock");
  });

  it("throws when the export request fails", async () => {
    fetchMock.mockResolvedValue({ ok: false, status: 500 });
    await expect(downloadCSV("/transactions/export")).rejects.toThrow(
      "Export failed",
    );
  });
});
