import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import api, { getStoredUser, storeUser, downloadCSV } from "./client";
import { NetworkError } from "./errors";
import { readCached } from "./offlineCache";
import { getOfflineSnapshot, setServedFromCache } from "./offlineStatus";
import { getOutboxSnapshot, OutboxStorageError } from "./outbox";

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
    // A timeout is a transport failure, not a plain Error: the offline layer
    // keys on the type, so getting this wrong disables the cached read, the
    // outbox enqueue and the offline session probe at once.
    await expect(promise).rejects.toThrow(NetworkError);
  });

  // The parser can spend ~a minute on a PDF and a restore rewrites the whole
  // ledger, so those routes must not be aborted at the default timeout. Each
  // budget is the frontend half of the one the nginx location enforces (75s for
  // the parse, 310s for the restore) plus a margin, so a request the proxy still
  // considers alive is never cut off by the client first.
  async function expectTimeoutAfter(
    call: () => Promise<unknown>,
    timeout: number,
  ) {
    vi.useFakeTimers();
    fetchMock.mockImplementation(abortOnSignal);
    const promise = call();
    const settled = vi.fn();
    void promise.catch(settled);

    await vi.advanceTimersByTimeAsync(15000);
    expect(settled).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(timeout - 15000);
    await expect(promise).rejects.toThrow("Request timed out");
  }

  it("keeps statement parsing alive past the default 15s timeout", () =>
    expectTimeoutAfter(() => api.parseStatement(new FormData()), 90000));

  it("keeps the Paperless import alive past the default 15s timeout", () =>
    expectTimeoutAfter(
      () => api.importPaperlessDocument({ documentId: 1 }),
      120000,
    ));

  it("keeps a backup restore alive past the default 15s timeout", () =>
    expectTimeoutAfter(() => api.importUserData({ accounts: [] }), 320000));

  it("keeps the session when the parser rejects the upload", async () => {
    // A rejected parse (a password-protected PDF, an unextractable scan) is a
    // business failure, not an expired session: signing the user out here was
    // the bug that the backend's 422 exists to prevent.
    storeUser({ id: "u1", email: "a@b.c" });
    fetchMock.mockResolvedValue(
      jsonResponse({ error: "password required or incorrect" }, 422),
    );

    await expect(api.parseStatement(new FormData())).rejects.toThrow(
      "password required or incorrect",
    );

    expect(window.location.href).not.toBe("/login");
    expect(getStoredUser()).not.toBeNull();
  });

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

  // A refresh that could not be asked is not an answer about the session. The
  // old code mapped every failure to `false` (a refusal), so a blip while the
  // server was already answering 401 signed the user out — throwing away the
  // cached ledger that exists for exactly that situation.
  it("keeps the session when the refresh cannot be reached", async () => {
    storeUser({ id: "u1", email: "a@b.c" } as never);
    fetchMock.mockImplementation((url: string) =>
      String(url).endsWith("/auth/refresh")
        ? Promise.reject(new TypeError("Failed to fetch"))
        : Promise.resolve(jsonResponse({ error: "Unauthorized" }, 401)),
    );

    await expect(api.getAccounts()).rejects.toThrow(
      "Network error: could not reach the API server",
    );

    expect(getStoredUser()).not.toBeNull();
    expect(window.location.href).not.toBe("/login");
  });

  it("keeps the session when the refresh answers 5xx", async () => {
    storeUser({ id: "u1", email: "a@b.c" } as never);
    fetchMock.mockImplementation((url: string) =>
      Promise.resolve(
        String(url).endsWith("/auth/refresh")
          ? jsonResponse({ error: "boom" }, 503)
          : jsonResponse({ error: "Unauthorized" }, 401),
      ),
    );

    await expect(api.getAccounts()).rejects.toThrow(NetworkError);

    expect(getStoredUser()).not.toBeNull();
    expect(window.location.href).not.toBe("/login");
  });

  it("bounds the refresh with the request timeout so it cannot stall the queue", async () => {
    storeUser({ id: "u1", email: "a@b.c" } as never);
    let refreshCalls = 0;
    fetchMock.mockImplementation((url: string, opts: RequestInit) => {
      if (String(url).endsWith("/auth/refresh")) {
        refreshCalls += 1;
        return abortOnSignal(url, opts);
      }
      return Promise.resolve(jsonResponse({ error: "Unauthorized" }, 401));
    });

    vi.useFakeTimers();
    const outcome = api.getAccounts().then(
      () => null,
      (err: unknown) => err,
    );
    await vi.advanceTimersByTimeAsync(15000);

    // The refresh ran on the same clock as every other request instead of
    // hanging until the browser gave up, and its timeout is a transport
    // failure: the session is kept rather than signed out.
    const err = await outcome;
    expect(err).toBeInstanceOf(NetworkError);
    expect((err as Error).message).toBe("Request timed out");
    expect(refreshCalls).toBe(1);
    expect(getStoredUser()).not.toBeNull();
    expect(window.location.href).not.toBe("/login");
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

describe("offline behaviour", () => {
  let fetchMock: ReturnType<typeof vi.fn>;
  const originalLocation = window.location;

  const create = {
    accountId: "acct-1",
    date: "2024-01-15",
    description: "Coffee",
    amount: 250.5,
    type: "debit" as const,
  };

  beforeEach(() => {
    localStorage.clear();
    setServedFromCache(false);
    // Both the cache and the outbox are namespaced by the signed-in user, which
    // the API layer reads from the cached user object.
    storeUser({ id: "u1", email: "a@b.c" } as never);
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
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
    Object.defineProperty(window, "location", {
      configurable: true,
      value: originalLocation,
    });
  });

  it("serves the last payload for a read when the network is down", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse([{ id: "a1" }]));
    await api.getAccounts();
    expect(getOfflineSnapshot().servedFromCache).toBe(false);

    fetchMock.mockRejectedValueOnce(new TypeError("Failed to fetch"));
    await expect(api.getAccounts()).resolves.toEqual([{ id: "a1" }]);

    expect(getOfflineSnapshot().servedFromCache).toBe(true);
  });

  it("stops claiming cached data once a live response arrives", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse([{ id: "a1" }]));
    await api.getAccounts();
    fetchMock.mockRejectedValueOnce(new TypeError("Failed to fetch"));
    await api.getAccounts();
    expect(getOfflineSnapshot().servedFromCache).toBe(true);

    fetchMock.mockResolvedValueOnce(jsonResponse([{ id: "a2" }]));
    await api.getAccounts();

    expect(getOfflineSnapshot().servedFromCache).toBe(false);
  });

  it("clears the cached reads when a 401 ends the session", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse([{ id: "a1" }]));
    await api.getAccounts();
    expect(readCached("u1", "/accounts")).toEqual([{ id: "a1" }]);

    fetchMock.mockResolvedValue(jsonResponse({ error: "Unauthorized" }, 401));
    await expect(api.getAccounts()).rejects.toThrow("Unauthorized");

    // The cached ledger is namespaced by the id of a user the app has just
    // forgotten, so nothing could ever read or clear it again: the sign-out has
    // to drop it here, exactly as an explicit logout does.
    expect(readCached("u1", "/accounts")).toBeNull();
    expect(getStoredUser()).toBeNull();
  });

  it("does not keep a read that is outside the allowlist", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ nodes: [] }));
    await api.getMoneyFlow();

    fetchMock.mockRejectedValueOnce(new TypeError("Failed to fetch"));
    await expect(api.getMoneyFlow()).rejects.toThrow(
      "Network error: could not reach the API server",
    );
  });

  it("reports a failed read that was never cached", async () => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));
    await expect(api.getAccounts()).rejects.toThrow(
      "Network error: could not reach the API server",
    );
  });

  // A request that hangs until the timeout is as unreachable as one that is
  // refused at once: it must take the same offline path.
  it("serves the cached read when the request times out", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse([{ id: "a1" }]));
    await api.getAccounts();

    vi.useFakeTimers();
    fetchMock.mockImplementation(abortOnSignal);
    const promise = api.getAccounts();
    await vi.advanceTimersByTimeAsync(15000);

    await expect(promise).resolves.toEqual([{ id: "a1" }]);
    expect(getOfflineSnapshot().servedFromCache).toBe(true);
  });

  it("queues a create that times out", async () => {
    vi.useFakeTimers();
    fetchMock.mockImplementation(abortOnSignal);
    const promise = api.createTransaction(create);
    await vi.advanceTimersByTimeAsync(15000);

    await expect(promise).resolves.toEqual({ id: null, queued: true });
    expect(getOutboxSnapshot("u1")).toHaveLength(1);
  });

  it("sends a client key with every create so a retry cannot double-post", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ id: "txn-1" }, 201));

    const result = await api.createTransaction(create);

    expect(result).toEqual({ id: "txn-1", queued: false });
    const body = JSON.parse(fetchMock.mock.calls[0][1].body);
    expect(body.clientKey).toEqual(expect.any(String));
    expect(body.clientKey).not.toBe("");
    expect(getOutboxSnapshot("u1")).toHaveLength(0);
  });

  it("reuses a supplied key so a flush replays the same create", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ id: "txn-1" }, 201));

    await api.createTransaction(create, {
      idempotencyKey: "key-9",
      queue: false,
    });

    expect(JSON.parse(fetchMock.mock.calls[0][1].body).clientKey).toBe("key-9");
  });

  it("queues a create that never reached the server", async () => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));

    const result = await api.createTransaction(create);

    expect(result).toEqual({ id: null, queued: true });
    const queued = getOutboxSnapshot("u1");
    expect(queued).toHaveLength(1);
    // The queued body carries the same key, so the replay is recognised.
    expect(queued[0].request.clientKey).toBe(queued[0].key);
  });

  it("surfaces a rejected create instead of queueing it", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ error: "account is closed" }, 409));

    await expect(api.createTransaction(create)).rejects.toMatchObject({
      message: "account is closed",
      status: 409,
    });
    expect(getOutboxSnapshot("u1")).toHaveLength(0);
  });

  it("fails a flush rather than re-queueing the entry it is sending", async () => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));

    await expect(
      api.createTransaction(create, { idempotencyKey: "key-9", queue: false }),
    ).rejects.toThrow("Network error: could not reach the API server");
    expect(getOutboxSnapshot("u1")).toHaveLength(0);
  });

  it("refuses to queue without a signed-in identity", async () => {
    storeUser(null);
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));

    await expect(api.createTransaction(create)).rejects.toThrow(
      "Network error: could not reach the API server",
    );
  });

  // The identity is captured when the request is issued. Reading it again when
  // the response lands attributes the payload to whoever is signed in by then,
  // which on a shared browser writes one user's ledger into another user's
  // namespace (and serves the previous user's cache to the new session). The
  // mocks below swap the session while the request is in flight: the fetch
  // resolves only after the swap has happened.
  it("does not cache a read that lands under a different session", async () => {
    fetchMock.mockImplementation(() => {
      storeUser({ id: "u2", email: "b@c.d" } as never);
      return Promise.resolve(jsonResponse([{ id: "a1" }]));
    });

    await expect(api.getAccounts()).resolves.toEqual([{ id: "a1" }]);
    expect(readCached("u1", "/accounts")).toBeNull();
    expect(readCached("u2", "/accounts")).toBeNull();
  });

  it("does not serve the replaced session's cache to the new one", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse([{ id: "a1" }]));
    await api.getAccounts();
    expect(readCached("u1", "/accounts")).toEqual([{ id: "a1" }]);

    fetchMock.mockImplementation(() => {
      storeUser({ id: "u2", email: "b@c.d" } as never);
      return Promise.reject(new TypeError("Failed to fetch"));
    });

    await expect(api.getAccounts()).rejects.toThrow(
      "Network error: could not reach the API server",
    );
    expect(getOfflineSnapshot().servedFromCache).toBe(false);
  });

  it("refuses to queue a create under a session that replaced the issuing one", async () => {
    fetchMock.mockImplementation(() => {
      storeUser({ id: "u2", email: "b@c.d" } as never);
      return Promise.reject(new TypeError("Failed to fetch"));
    });

    // Neither queue may take it: u1 is gone from this browser and u2 never
    // recorded it, and the server would reject an u1 entry flushed under u2.
    await expect(api.createTransaction(create)).rejects.toThrow(
      "Network error: could not reach the API server",
    );
    expect(getOutboxSnapshot("u1")).toHaveLength(0);
    expect(getOutboxSnapshot("u2")).toHaveLength(0);
  });

  it("refuses to claim a save the browser would not let it queue", async () => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));
    const setItem = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("QuotaExceededError");
      });

    // The create has to fail loudly: resolving `queued: true` would show
    // "Saved offline" for a transaction that is in no queue at all.
    await expect(api.createTransaction(create)).rejects.toThrow(
      OutboxStorageError,
    );
    setItem.mockRestore();

    expect(getOutboxSnapshot("u1")).toHaveLength(0);
  });
});
