import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import api, { getStoredUser, storeUser, downloadCSV } from "./client";

const API_BASE = "/api/v1";

function jsonResponse(
  body: unknown,
  status = 200,
  headers: Record<string, string> = {},
) {
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

// A fetch that only settles once the caller aborts, so fake timers can tell a
// timed-out request from one that is still in flight.
function abortOnSignal(_url: unknown, opts: RequestInit): Promise<Response> {
  return new Promise<Response>((_resolve, reject) => {
    opts.signal?.addEventListener("abort", () => {
      const err = new Error("Aborted");
      err.name = "AbortError";
      reject(err);
    });
  });
}

describe("user storage", () => {
  beforeEach(() => localStorage.clear());

  it("storeUser persists a JSON object", () => {
    const user = { id: 1, email: "a@b.c" };
    storeUser(user as any);
    expect(getStoredUser()).toEqual(user);
  });

  it("storeUser(null) removes the stored user", () => {
    storeUser({ id: 1 } as any);
    storeUser(null);
    expect(getStoredUser()).toBeNull();
  });

  it("getStoredUser returns null for invalid JSON", () => {
    localStorage.setItem("fintrak_user", "{not json");
    expect(getStoredUser()).toBeNull();
  });
});

describe("api request", () => {
  let fetchMock: ReturnType<typeof vi.fn>;
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
    fetchMock.mockResolvedValue(jsonResponse({ user: { id: 1 } }));
    const res = await api.login({ email: "a@b.c", password: "pw" });
    expect(res).toEqual({ user: { id: 1 } });
    const [url, opts] = fetchMock.mock.calls[0];
    expect(url).toBe(`${API_BASE}/auth/login`);
    expect(opts.method).toBe("POST");
    expect(opts.headers["Content-Type"]).toBe("application/json");
    expect(opts.credentials).toBe("include");
    expect(JSON.parse(opts.body)).toEqual({ email: "a@b.c", password: "pw" });
  });

  it("never attaches an Authorization header", async () => {
    fetchMock.mockResolvedValue(jsonResponse([]));
    await api.getAccounts();
    const [, opts] = fetchMock.mock.calls[0];
    expect(opts.headers.Authorization).toBeUndefined();
    expect(opts.credentials).toBe("include");
  });

  it("builds query strings for list endpoints", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ data: [] }));
    await api.getTransactions({ accountId: "acct-1", limit: 50 });
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe(`${API_BASE}/transactions?accountId=acct-1&limit=50`);
  });

  it("posts transactions to the validate endpoint", async () => {
    fetchMock.mockResolvedValue(
      jsonResponse({
        total: 1,
        existingCount: 1,
        missingCount: 0,
        results: [{ index: 0, exists: true }],
      }),
    );
    const payload = {
      accountId: "acct-1",
      transactions: [
        {
          date: "2024-01-15",
          description: "Coffee",
          amount: 250.5,
          type: "debit" as const,
        },
      ],
    };
    const res = await api.validateTransactions(payload);
    expect(res).toEqual({
      total: 1,
      existingCount: 1,
      missingCount: 0,
      results: [{ index: 0, exists: true }],
    });
    const [url, opts] = fetchMock.mock.calls[0];
    expect(url).toBe(`${API_BASE}/transactions/validate`);
    expect(opts.method).toBe("POST");
    expect(JSON.parse(opts.body)).toEqual(payload);
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
    fetchMock.mockImplementation(abortOnSignal);
    const promise = api.getAccounts();
    vi.advanceTimersByTime(15000);
    await expect(promise).rejects.toThrow("Request timed out");
  });

  // The parser can spend ~a minute on a PDF, so the parse routes must not be
  // aborted at the default timeout.
  async function expectNotAbortedAtDefaultTimeout(
    call: () => Promise<unknown>,
  ) {
    vi.useFakeTimers();
    fetchMock.mockImplementation(abortOnSignal);
    const promise = call();
    const settled = vi.fn();
    void promise.catch(settled);

    await vi.advanceTimersByTimeAsync(15000);
    expect(settled).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(105000);
    await expect(promise).rejects.toThrow("Request timed out");
  }

  it("keeps statement parsing alive past the default 15s timeout", () =>
    expectNotAbortedAtDefaultTimeout(() =>
      api.parseStatement(new FormData()),
    ));

  it("keeps the Paperless import alive past the default 15s timeout", () =>
    expectNotAbortedAtDefaultTimeout(() =>
      api.importPaperlessDocument({ documentId: 1 }),
    ));

  it("clears the stored user and redirects to /login on 401", async () => {
    storeUser({ id: 1 } as any);
    fetchMock.mockResolvedValue(jsonResponse({ error: "Unauthorized" }, 401));
    await expect(api.getAccounts()).rejects.toThrow("Unauthorized");
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

  it("does not redirect for 401 on the session check", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ error: "Unauthorized" }, 401));
    await expect(api.me()).rejects.toThrow("Unauthorized");
    expect(window.location.href).not.toBe("/login");
  });

  it("refreshes the session and retries the request once on 401", async () => {
    let protectedCalls = 0;
    fetchMock.mockImplementation((url: string) => {
      if (url.endsWith("/auth/refresh")) {
        return Promise.resolve(jsonResponse({ message: "token refreshed" }));
      }
      protectedCalls += 1;
      return Promise.resolve(
        protectedCalls === 1
          ? jsonResponse({ error: "Unauthorized" }, 401)
          : jsonResponse([]),
      );
    });

    await expect(api.getAccounts()).resolves.toEqual([]);
    expect(window.location.href).not.toBe("/login");
    const refreshCalls = fetchMock.mock.calls.filter((c) =>
      String(c[0]).endsWith("/auth/refresh"),
    );
    expect(refreshCalls).toHaveLength(1);
  });

  it("gives up after one retry when the refreshed request still returns 401", async () => {
    fetchMock.mockImplementation((url: string) =>
      Promise.resolve(
        url.endsWith("/auth/refresh")
          ? jsonResponse({ message: "token refreshed" })
          : jsonResponse({ error: "Unauthorized" }, 401),
      ),
    );

    await expect(api.getAccounts()).rejects.toThrow("Unauthorized");
    expect(window.location.href).toBe("/login");
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("dedupes concurrent refreshes into a single call", async () => {
    let protectedCalls = 0;
    fetchMock.mockImplementation((url: string) => {
      if (url.endsWith("/auth/refresh")) {
        return Promise.resolve(jsonResponse({ message: "token refreshed" }));
      }
      protectedCalls += 1;
      return Promise.resolve(
        protectedCalls <= 2
          ? jsonResponse({ error: "Unauthorized" }, 401)
          : jsonResponse([]),
      );
    });

    await Promise.all([api.getAccounts(), api.getPayees()]);

    const refreshCalls = fetchMock.mock.calls.filter((c) =>
      String(c[0]).endsWith("/auth/refresh"),
    );
    expect(refreshCalls).toHaveLength(1);
  });

  it("does not attempt a refresh when the refresh endpoint itself 401s", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ error: "Unauthorized" }, 401));

    await expect(api.getAccounts()).rejects.toThrow("Unauthorized");

    const refreshCalls = fetchMock.mock.calls.filter((c) =>
      String(c[0]).endsWith("/auth/refresh"),
    );
    expect(refreshCalls).toHaveLength(1);
  });
});

describe("downloadCSV", () => {
  let fetchMock: ReturnType<typeof vi.fn>;
  let clickSpy: ReturnType<typeof vi.spyOn>;

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
