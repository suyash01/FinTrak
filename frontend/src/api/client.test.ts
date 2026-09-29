import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import api, { getStoredUser, storeUser, downloadCSV } from "./client";
import { NetworkError } from "./errors";
import type { FieldValue } from "./merge";
import { readCached } from "./offlineCache";
import { getOfflineSnapshot, setServedFromCache } from "./offlineStatus";
import {
  flushOutbox,
  getOutboxSnapshot,
  OutboxStorageError,
  type BulkEntry,
} from "./outbox";
import { OPS } from "./registry";
import type {
  Account,
  AccountType,
  BulkBillingCycleRequest,
  BulkCategorizeRequest,
  BulkLoanRequest,
  BulkUpdatePayeeRequest,
  BulkUpdateTagsRequest,
  Category,
  CategoryGroup,
  LoanDisbursementRequest,
  LoanSchedule,
  LoanScheduleDetail,
  Payee,
  RecurringAttachRequest,
  RecurringDetachRequest,
  RecurringSeries,
  RecurringSeriesTerm,
  Rule,
  Transaction,
  UpdateAccountRequest,
  UpdateAccountTypeRequest,
  UpdateCategoryGroupRequest,
  UpdateCategoryRequest,
  UpdatePayeeRequest,
  UpdateRecurringSeriesRequest,
  UpdateRecurringSeriesTermRequest,
  UpdateRuleRequest,
  UpdateUserSettingsRequest,
  UserSettings,
  LoanScheduleRequest,
} from "../types";

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

  // The row a form would have opened with: an offline edit is a patch against
  // this, so it has to be a whole row the registry can project.
  const txn: Transaction = {
    id: "t1",
    accountId: "acct-1",
    date: "2026-01-15",
    description: "Coffee",
    amount: 250.5,
    type: "debit",
    categoryId: null,
    tags: [],
    notes: "old",
    payeeId: null,
    billingCycleId: null,
  };

  // The five row-named partial-update families and the settings singleton, each
  // as its form opened with. `balance`, `isBase`, `sortOrder` and `hasToken` are
  // on these rows and on none of the projections: they are what a queued base
  // must not carry, because a base holding a field the row cannot be written
  // with reads as a difference the user made.
  const account: Account = {
    id: "a1",
    name: "Wallet",
    accountTypeId: "at1",
    bank: "HDFC",
    currency: "INR",
    color: "#fff",
    isDefault: true,
    closed: false,
    balance: 100,
  };
  const accountType: AccountType = {
    id: "at1",
    name: "Savings",
    positiveTxnType: "credit",
  };
  const category: Category = {
    id: "c1",
    name: "Coffee",
    icon: "cup",
    color: "#0f0",
    groupId: "g1",
  };
  const group: CategoryGroup = {
    id: "g1",
    name: "Food",
    icon: "utensils",
    color: "#f00",
    isBase: true,
    isGlobal: false,
    sortOrder: 0,
  };
  // hasToken is what the settings endpoint answers with in place of the token
  // itself (models.go:272-277), so it is on the row from the start: a base
  // carrying it would put a value on the wire that this client never read from
  // anywhere, and a queued diff would claim the user had changed it.
  const settings: UserSettings = {
    paperlessUrl: "https://paperless.example",
    hasToken: true,
    paperlessTag: "fintrak",
    pageSize: 50,
  };

  // One pattern, six endpoints, so the shared behaviour is one table rather than
  // six near-copies. Each entry is the row the form opened with, the projection
  // of it that a queued entry has to carry, the payload the save sends, the diff
  // that payload reduces to against that base, and the save itself. `revert` is
  // the payload with the edited field back at the base's own value — the form
  // saved without changing anything — and it is the only thing that turns into a
  // second payload here, so the entry states its body once.
  interface PartialFamily {
    op: string;
    rowId: string;
    url: string;
    base: unknown;
    queuedBase: Record<string, unknown>;
    payload: Record<string, unknown>;
    revert: Record<string, unknown>;
    diff: Record<string, unknown>;
    send: (
      payload: Record<string, unknown>,
      call: { base?: unknown; queue?: boolean },
    ) => Promise<unknown>;
  }

  const PARTIAL_FAMILY: PartialFamily[] = [
    {
      op: "account.put",
      rowId: "a1",
      url: "/accounts/a1",
      base: account,
      queuedBase: {
        name: "Wallet",
        accountTypeId: "at1",
        bank: "HDFC",
        currency: "INR",
        color: "#fff",
        isDefault: true,
        closed: false,
      },
      // What Accounts.tsx's form sends: the row's fields, not the row. A payload
      // carrying id or balance would put them in the diff as well — the endpoint
      // ignores them, but the merge would carry a change the user never made.
      // billingDay: null is the form's default for an account that has none, and
      // the base carries no such key, so it is not a change.
      payload: {
        name: "Renamed",
        accountTypeId: "at1",
        bank: "HDFC",
        currency: "INR",
        color: "#fff",
        isDefault: true,
        closed: false,
        billingDay: null,
      },
      revert: { name: "Wallet" },
      diff: { name: "Renamed" },
      send: (payload, { base, queue }) =>
        api.updateAccount(
          "a1",
          payload as unknown as UpdateAccountRequest,
          base === undefined ? { queue } : { base: base as Account, queue },
        ),
    },
    {
      op: "accountType.put",
      rowId: "at1",
      url: "/account-types/at1",
      base: accountType,
      queuedBase: { name: "Savings", positiveTxnType: "credit" },
      payload: { name: "Current", positiveTxnType: "credit" },
      revert: { name: "Savings" },
      diff: { name: "Current" },
      send: (payload, { base, queue }) =>
        api.updateAccountType(
          "at1",
          payload as unknown as UpdateAccountTypeRequest,
          base === undefined ? { queue } : { base: base as AccountType, queue },
        ),
    },
    {
      op: "category.put",
      rowId: "c1",
      url: "/categories/c1",
      base: category,
      queuedBase: { name: "Coffee", icon: "cup", color: "#0f0", groupId: "g1" },
      payload: { name: "Tea", icon: "cup", color: "#0f0", groupId: "g1" },
      revert: { name: "Coffee" },
      diff: { name: "Tea" },
      send: (payload, { base, queue }) =>
        api.updateCategory(
          "c1",
          payload as unknown as UpdateCategoryRequest,
          base === undefined ? { queue } : { base: base as Category, queue },
        ),
    },
    {
      op: "adminCategory.put",
      rowId: "c1",
      url: "/admin/categories/c1",
      base: category,
      queuedBase: { name: "Coffee", icon: "cup", color: "#0f0", groupId: "g1" },
      payload: { name: "Tea", icon: "cup", color: "#0f0", groupId: "g1" },
      revert: { name: "Coffee" },
      diff: { name: "Tea" },
      send: (payload, { base, queue }) =>
        api.updateGlobalCategory(
          "c1",
          payload as unknown as UpdateCategoryRequest,
          base === undefined ? { queue } : { base: base as Category, queue },
        ),
    },
    {
      op: "group.put",
      rowId: "g1",
      url: "/groups/g1",
      base: group,
      queuedBase: { name: "Food", icon: "utensils", color: "#f00" },
      payload: { name: "Food", icon: "cup", color: "#f00" },
      revert: { icon: "utensils" },
      diff: { icon: "cup" },
      send: (payload, { base, queue }) =>
        api.updateGroup(
          "g1",
          payload as unknown as UpdateCategoryGroupRequest,
          base === undefined ? { queue } : { base: base as CategoryGroup, queue },
        ),
    },
    {
      // The singleton: no id in the path, and the row the entry is queued against
      // is the user. A settings form names the fields it means to set rather than
      // the row — the row carries hasToken, which is not a field the endpoint
      // takes and not one the base can hold — so for this one the whole payload
      // and the diff are the same object.
      op: "settings.put",
      rowId: "u1",
      url: "/paperless/settings",
      base: settings,
      queuedBase: {
        paperlessUrl: "https://paperless.example",
        paperlessTag: "fintrak",
        pageSize: 50,
      },
      payload: { paperlessTag: "receipts" },
      revert: { paperlessTag: "fintrak" },
      diff: { paperlessTag: "receipts" },
      send: (payload, { base, queue }) =>
        api.updateUserSettings(
          payload as unknown as UpdateUserSettingsRequest,
          base === undefined ? { queue } : { base: base as UserSettings, queue },
        ),
    },
  ];

  // The whole-row family's five rows, each as its form opened with. `id`,
  // `createdAt`, `monthlyAmount` and `attachedCount` are on these rows and on none
  // of the projections: they are what a queued base must not carry, because a
  // base holding a field the row cannot be written with reads as a difference the
  // user made.
  const payee: Payee = { id: "p1", name: "Cafe", accountId: "a1" };
  const rule: Rule = {
    id: "r1",
    pattern: "coffee",
    matchType: "contains",
    categoryId: "c1",
    priority: 10,
  };
  const series: RecurringSeries = {
    id: "s1",
    accountId: "a1",
    name: "Netflix",
    description: "",
    amount: 649,
    type: "debit",
    frequency: "monthly",
    interval: 1,
    startDate: "2026-01-01",
    endDate: null,
    categoryId: null,
    payeeId: null,
    active: true,
    notes: "",
    monthlyAmount: 649,
    attachedCount: 0,
  };
  const term: RecurringSeriesTerm = {
    id: "term-1",
    seriesId: "s1",
    startDate: "2026-01-01",
    endDate: "2026-06-01",
    amount: 649,
    accountId: "a1",
  };
  // The terms row alone: the amortization periods are derived server-side from
  // these, so they are not a form's business and not a queued edit's either.
  const loan: LoanSchedule = {
    id: "ls1",
    loanAccountId: "a1",
    principal: 100000,
    processingFee: 1000,
    disbursalDate: "2026-01-05",
    annualRateBps: 950,
    tenureMonths: 24,
    startDate: "2026-01-10",
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
  };
  const loanDetail: LoanScheduleDetail = {
    schedule: loan,
    emi: 4400,
    totalInterest: 5600,
    totalPayable: 105600,
    entries: [],
    paidInstallments: 0,
    paidAmount: 0,
    principalPaid: 0,
    interestPaid: 0,
    outstandingPrincipal: 100000,
    transfers: [],
    completed: false,
  };

  // The whole-row family, and it is the same table on the five endpoints whose
  // handlers write every column their request can carry. Nothing about the *call*
  // differs from the six above — a base, a diff, a queue on a transport failure —
  // so this is the same six questions asked twice. What the family needs is that
  // the diff is what is queued and what goes out, because the whole row is the
  // flush's to build by overlaying this diff on the row it re-reads (see
  // registry.ts's mergedRow).
  const WHOLE_FAMILY: PartialFamily[] = [
    {
      op: "payee.put",
      rowId: "p1",
      url: "/payees/p1",
      base: payee,
      queuedBase: { name: "Cafe", accountId: "a1" },
      payload: { name: "Beans", accountId: "a1" },
      revert: { name: "Cafe" },
      diff: { name: "Beans" },
      send: (payload, { base, queue }) =>
        api.updatePayee(
          "p1",
          payload as unknown as UpdatePayeeRequest,
          base === undefined ? { queue } : { base: base as Payee, queue },
        ),
    },
    {
      op: "rule.put",
      rowId: "r1",
      url: "/rules/r1",
      base: rule,
      queuedBase: { pattern: "coffee", matchType: "contains", categoryId: "c1", priority: 10 },
      payload: {
        pattern: "coffee",
        matchType: "contains",
        categoryId: "c1",
        priority: 20,
      },
      revert: { priority: 10 },
      diff: { priority: 20 },
      send: (payload, { base, queue }) =>
        api.updateRule(
          "r1",
          payload as unknown as UpdateRuleRequest,
          base === undefined ? { queue } : { base: base as Rule, queue },
        ),
    },
    {
      // The series carries four fields the endpoint does not write — amount,
      // accountId, startDate and endDate are derived from its terms — and two it
      // does (monthlyAmount, attachedCount). A base carrying any of them would
      // hold the field as a change the user never made, and a diff built from one
      // would send it.
      op: "recurring.put",
      rowId: "s1",
      url: "/recurring/s1",
      base: series,
      queuedBase: {
        accountId: "a1",
        name: "Netflix",
        description: "",
        amount: 649,
        type: "debit",
        frequency: "monthly",
        interval: 1,
        startDate: "2026-01-01",
        active: true,
        notes: "",
      },
      payload: {
        accountId: "a1",
        name: "Prime",
        description: "",
        amount: 649,
        type: "debit",
        frequency: "monthly",
        interval: 1,
        startDate: "2026-01-01",
        active: true,
        notes: "",
      },
      revert: { name: "Netflix" },
      diff: { name: "Prime" },
      send: (payload, { base, queue }) =>
        api.updateRecurringSeries(
          "s1",
          payload as unknown as UpdateRecurringSeriesRequest,
          base === undefined ? { queue } : { base: base as RecurringSeries, queue },
        ),
    },
    {
      // The one row addressed by two ids: the term endpoint takes the series and
      // the term, and the series is not in the body. The ids are closed over
      // rather than passed, so the table's `send` stays one shape.
      op: "recurringTerm.put",
      rowId: "term-1",
      url: "/recurring/s1/terms/term-1",
      base: term,
      queuedBase: {
        seriesId: "s1",
        startDate: "2026-01-01",
        endDate: "2026-06-01",
        amount: 649,
        accountId: "a1",
      },
      payload: {
        startDate: "2026-01-01",
        endDate: "2026-12-01",
        amount: 649,
        accountId: "a1",
      },
      revert: { endDate: "2026-06-01" },
      diff: { endDate: "2026-12-01" },
      send: (payload, { base, queue }) =>
        api.updateRecurringTerm(
          "s1",
          "term-1",
          payload as unknown as UpdateRecurringSeriesTermRequest,
          base === undefined ? { queue } : { base: base as RecurringSeriesTerm, queue },
        ),
    },
    {
      // The loan: the base is the schedule detail the form opened with, and what
      // is diffed is its *terms* — the periods in the same response are derived
      // server-side from them, so a queued edit is a change to the terms and
      // nothing else.
      op: "loanSchedule.put",
      rowId: "a1",
      url: "/accounts/a1/loan-schedule",
      base: loanDetail,
      queuedBase: {
        principal: 100000,
        processingFee: 1000,
        disbursalDate: "2026-01-05",
        annualRateBps: 950,
        tenureMonths: 24,
        startDate: "2026-01-10",
      },
      payload: {
        principal: 100000,
        processingFee: 1000,
        disbursalDate: "2026-01-05",
        annualRateBps: 950,
        tenureMonths: 36,
        startDate: "2026-01-10",
      },
      revert: { tenureMonths: 24 },
      diff: { tenureMonths: 36 },
      send: (payload, { base, queue }) =>
        api.saveLoanSchedule(
          "a1",
          payload as unknown as LoanScheduleRequest,
          base === undefined
            ? { queue }
            : { base: base as LoanScheduleDetail, queue },
        ),
    },
  ];

  // The row-naming writes: the same six questions asked of the eight calls that
  // name their own rows. One field, one value, N rows — so the base is per row
  // rather than one row's, and `settled` is the selection in which nothing changes
  // (this family's `revert`).
  //
  // t1 and t2 need the write and t3 already has it, so `rows` is what is left of
  // the caller's three. A base carrying a null is what a caller holding the
  // transaction rows has in hand, and `queuedBase` is that base as the queue has
  // to record it: the nullish entry dropped, because a base that says "no
  // category" where the server's row says the row carries no such field reads as
  // somebody having changed it (merge.ts: absent is not null) and would come back
  // from the merge as a conflict over a write the row never had.
  interface MultiRowFamily {
    label: string;
    op: string;
    field: string;
    value: FieldValue;
    method: string;
    url: string;
    rows: string[];
    base: Record<string, FieldValue>;
    queuedBase: Record<string, FieldValue>;
    // The base in which nothing changes, and there is not one for every case: a
    // write whose value is nullish cannot have one, because a base that says "on
    // no loan" is an absent key and a value the base is silent about is a change
    // — the merge says the same (mergeFields), and dropping a row on a base that
    // merely has nothing to say about it would lose the write the user made.
    settled?: Record<string, FieldValue>;
    payload: Record<string, unknown>;
    body: Record<string, unknown>;
    send: (call: {
      payload: Record<string, unknown>;
      base?: Record<string, FieldValue>;
      queue?: boolean;
    }) => Promise<unknown>;
  }

  const MULTI_ROW: MultiRowFamily[] = [
    {
      label: "transaction.categorize",
      op: "transaction.categorize",
      field: "categoryId",
      value: "c1",
      method: "POST",
      url: "/transactions/bulk-categorize",
      rows: ["t1", "t2"],
      base: { t1: null, t2: "c0", t3: "c1" },
      queuedBase: { t2: "c0", t3: "c1" },
      settled: { t1: "c1", t2: "c1", t3: "c1" },
      payload: { transactionIds: ["t1", "t2", "t3"], categoryId: "c1" },
      body: { transactionIds: ["t1", "t2"], categoryId: "c1" },
      send: ({ payload, base, queue }) =>
        api.bulkCategorize(
          payload as unknown as BulkCategorizeRequest,
          base === undefined ? { queue } : { base, queue },
        ),
    },
    {
      label: "transaction.payee",
      op: "transaction.payee",
      field: "payeeId",
      value: "p1",
      method: "POST",
      url: "/transactions/bulk-payee",
      rows: ["t1", "t2"],
      base: { t1: "p0", t2: null, t3: "p1" },
      queuedBase: { t1: "p0", t3: "p1" },
      settled: { t1: "p1", t2: "p1", t3: "p1" },
      payload: { transactionIds: ["t1", "t2", "t3"], payeeId: "p1" },
      body: { transactionIds: ["t1", "t2"], payeeId: "p1" },
      send: ({ payload, base, queue }) =>
        api.bulkUpdatePayee(
          payload as unknown as BulkUpdatePayeeRequest,
          base === undefined ? { queue } : { base, queue },
        ),
    },
    {
      label: "transaction.billingCycle",
      op: "transaction.billingCycle",
      field: "billingCycleId",
      value: "bc1",
      method: "POST",
      url: "/transactions/bulk-billing-cycle",
      rows: ["t1", "t2"],
      base: { t1: "bc0", t2: null, t3: "bc1" },
      queuedBase: { t1: "bc0", t3: "bc1" },
      settled: { t1: "bc1", t2: "bc1", t3: "bc1" },
      payload: { transactionIds: ["t1", "t2", "t3"], billingCycleId: "bc1" },
      body: { transactionIds: ["t1", "t2"], billingCycleId: "bc1" },
      send: ({ payload, base, queue }) =>
        api.bulkUpdateBillingCycle(
          payload as unknown as BulkBillingCycleRequest,
          base === undefined ? { queue } : { base, queue },
        ),
    },
    {
      label: "transaction.loan attach",
      op: "transaction.loan",
      field: "loanAccountId",
      value: "a1",
      method: "POST",
      url: "/transactions/bulk-loan",
      rows: ["t1", "t2"],
      base: { t1: "a0", t2: null, t3: "a1" },
      queuedBase: { t1: "a0", t3: "a1" },
      settled: { t1: "a1", t2: "a1", t3: "a1" },
      payload: { transactionIds: ["t1", "t2", "t3"], loanAccountId: "a1" },
      body: { transactionIds: ["t1", "t2"], loanAccountId: "a1" },
      send: ({ payload, base, queue }) =>
        api.bulkLoan(
          payload as unknown as BulkLoanRequest,
          base === undefined ? { queue } : { base, queue },
        ),
    },
    {
      label: "transaction.loan detach",
      op: "transaction.loan",
      field: "loanAccountId",
      // null, never "": BulkLoanRequest.loanAccountId is a *uuid.UUID server-side,
      // so an empty string would be sent on as an id and rejected. It is also the
      // only value the merge can read as a change away from the loan a row holds.
      value: null,
      method: "POST",
      url: "/transactions/bulk-loan",
      // Every row, including t3, which is on no loan already: a base that is
      // silent about a row is not a row that already holds the value.
      rows: ["t1", "t2", "t3"],
      base: { t1: "a0", t2: "a0", t3: null },
      queuedBase: { t1: "a0", t2: "a0" },
      payload: { transactionIds: ["t1", "t2", "t3"], loanAccountId: null },
      body: { transactionIds: ["t1", "t2", "t3"], loanAccountId: null },
      send: ({ payload, base, queue }) =>
        api.bulkLoan(
          payload as unknown as BulkLoanRequest,
          base === undefined ? { queue } : { base, queue },
        ),
    },
    {
      label: "transaction.recurring attach",
      op: "transaction.recurring",
      field: "recurringSeriesId",
      value: "s1",
      method: "POST",
      url: "/recurring/attach",
      rows: ["t1", "t2"],
      base: { t1: "s0", t2: null, t3: "s1" },
      queuedBase: { t1: "s0", t3: "s1" },
      settled: { t1: "s1", t2: "s1", t3: "s1" },
      payload: { seriesId: "s1", transactionIds: ["t1", "t2", "t3"] },
      body: { seriesId: "s1", transactionIds: ["t1", "t2"] },
      send: ({ payload, base, queue }) =>
        api.attachRecurring(
          payload as unknown as RecurringAttachRequest,
          base === undefined ? { queue } : { base, queue },
        ),
    },
    {
      label: "transaction.recurring detach",
      op: "transaction.recurring",
      field: "recurringSeriesId",
      value: null,
      method: "POST",
      url: "/recurring/detach",
      rows: ["t1", "t2", "t3"],
      base: { t1: "s0", t2: "s0", t3: null },
      queuedBase: { t1: "s0", t2: "s0" },
      payload: { transactionIds: ["t1", "t2", "t3"] },
      body: { transactionIds: ["t1", "t2", "t3"] },
      send: ({ payload, base, queue }) =>
        api.detachRecurring(
          payload as unknown as RecurringDetachRequest,
          base === undefined ? { queue } : { base, queue },
        ),
    },
    {
      // The one value in this family that is not the row's content: the endpoint
      // takes additions and removals, so what is queued is the delta, and a
      // removal the user made has to be recorded as a removal — the surviving
      // names would be added back on the flush.
      label: "transaction.tags",
      op: "transaction.tags",
      field: "tags",
      value: ["-milk"],
      method: "POST",
      url: "/transactions/bulk-tags",
      // Only t1 holds "milk". The other two are not in a removal of it, which a
      // base that cannot say so would get wrong: the value is a delta, so
      // comparing it to a row's complete list is comparing two questions.
      rows: ["t1"],
      base: { t1: ["milk", "bread"], t2: ["bread"], t3: [] },
      queuedBase: { t1: ["milk", "bread"], t2: ["bread"], t3: [] },
      settled: { t1: ["bread"], t2: ["bread"], t3: [] },
      payload: {
        transactionIds: ["t1", "t2", "t3"],
        add: [],
        remove: ["milk"],
      },
      body: { transactionIds: ["t1"], add: [], remove: ["milk"] },
      send: ({ payload, base, queue }) =>
        api.bulkUpdateTags(
          payload as unknown as BulkUpdateTagsRequest,
          base === undefined ? { queue } : { base, queue },
        ),
    },
    {
      label: "transaction.loanDisbursement",
      op: "transaction.loanDisbursement",
      field: "loanAccountId",
      value: "a1",
      method: "PUT",
      url: "/accounts/a1/loan-disbursement",
      rows: ["t1"],
      base: { t1: null },
      // Nothing at all: the credit is on no loan account, so the base has no key
      // for it rather than a null.
      queuedBase: {},
      settled: { t1: "a1" },
      payload: { transactionId: "t1" },
      body: { transactionId: "t1" },
      // The only one of the eight addressed outside the body: the loan account is
      // in the path and the transaction is the row the write names.
      send: ({ payload, base, queue }) =>
        api.linkLoanDisbursement(
          "a1",
          payload as unknown as LoanDisbursementRequest,
          base === undefined ? { queue } : { base, queue },
        ),
    },
  ];

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
    // A test that makes the browser refuse to write the queue stubs
    // Storage.prototype.setItem, and a spy that outlived a failing assertion
    // would take every test after it down with it.
    vi.restoreAllMocks();
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
    const entry = queued[0];
    if (entry.kind !== "create") throw new Error("createTransaction queued a non-create");
    expect(entry.request.clientKey).toBe(entry.key);
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

  it("reads one transaction by id, scoped with q alone", async () => {
    fetchMock.mockResolvedValue(
      jsonResponse({ data: [txn], total: 1, page: 1, pages: 1 }),
    );

    await expect(api.getTransaction("t1")).resolves.toEqual(txn);

    // No accountId, and never one: the list endpoint injects synthetic summary
    // rows (a per-cycle "Total outstanding", a month-end "Running balance") when
    // a single account is filtered and the sort is by date, and it guards on
    // accountUUID, which only an accountId parameter sets — a q= term never
    // reaches it. Scoped by account as well, this could answer a summary row
    // rather than the row an edit is about to be based on.
    expect(fetchMock.mock.calls[0][0]).toBe(
      `${API_BASE}/transactions?q=id%3At1&limit=1`,
    );
  });

  it("answers null for a transaction the server no longer has", async () => {
    fetchMock.mockResolvedValue(
      jsonResponse({ data: [], total: 0, page: 1, pages: 0 }),
    );

    await expect(api.getTransaction("t1")).resolves.toBeNull();
  });

  it("never answers a single-transaction read from the offline cache", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ data: [txn], total: 1, page: 1, pages: 1 }),
    );
    await api.getTransaction("t1");
    // isQueriedLedgerRead keeps a q= read out of the cache, so the row an edit
    // is based on cannot come back as this browser's own last belief of it: a
    // base that agreed with the client by construction would merge the edit
    // against itself and never see a conflict.
    expect(readCached("u1", "/transactions?q=id%3At1&limit=1")).toBeNull();

    fetchMock.mockRejectedValueOnce(new TypeError("Failed to fetch"));
    await expect(api.getTransaction("t1")).rejects.toThrow(NetworkError);
    expect(getOfflineSnapshot().servedFromCache).toBe(false);
  });

  it("queues an edit that never reached the server, and answers queued", async () => {
    fetchMock.mockRejectedValue(new TypeError("failed to fetch"));

    const result = await api.updateTransaction(
      "t1",
      { notes: "offline" },
      { base: txn },
    );

    expect(result).toEqual({ id: "t1", queued: true });
    const entries = getOutboxSnapshot("u1");
    expect(entries[0]).toMatchObject({
      kind: "edit",
      op: "transaction.patch",
      rowId: "t1",
      // The queued patch is the diff that would have gone out, and the base is
      // the row it was made against — the two things the flush merges.
      patch: { notes: "offline" },
      base: { notes: "old", description: "Coffee" },
    });
  });

  it("sends only the changed field when a base is supplied", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ message: "updated" }));

    await api.updateTransaction(
      "t1",
      { notes: "new", description: "Coffee" },
      { base: txn },
    );

    const body = JSON.parse(fetchMock.mock.calls[0][1].body);
    expect(body).toEqual({ notes: "new" });
  });

  it("keeps a field the user cleared in the diff", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ message: "updated" }));

    // null is the user clearing the category, not a value this side of the diff
    // may drop: absent is not null (merge.ts). A projection of the payload would
    // drop it, and the category would stay attached while the user was told the
    // edit was saved.
    await api.updateTransaction(
      "t1",
      { categoryId: null },
      { base: { ...txn, categoryId: "c1" } },
    );

    const body = JSON.parse(fetchMock.mock.calls[0][1].body);
    expect(body).toEqual({ categoryId: null });
  });

  it("sends no request at all when the diff is empty", async () => {
    // The form's row and the base agree on every field, so there is nothing to
    // write. The endpoint answers a PATCH carrying no fields 400 "no fields to
    // update", which would put a server error in front of a user who did not
    // change anything — so the answer has to come from here, and the assertion
    // is that no request was made at all.
    const result = await api.updateTransaction(
      "t1",
      { notes: "old", description: "Coffee" },
      { base: txn },
    );

    expect(fetchMock).not.toHaveBeenCalled();
    expect(result).toEqual({ id: "t1", queued: false });
    expect(getOutboxSnapshot("u1")).toHaveLength(0);
  });

  it("sends no request when the form's payload is all defaults for a row that has none", async () => {
    // The shape the app actually sends: the edit modal emits categoryId,
    // payeeId and billingCycleId from form state, so a row that has never had
    // any of them comes back as three nulls. Those are not changes — there was
    // nothing to clear — so the diff is empty and the guard fires here too,
    // rather than only for a payload that repeats the base's own values.
    const result = await api.updateTransaction(
      "t1",
      { categoryId: null, payeeId: null, billingCycleId: null },
      { base: txn },
    );

    expect(fetchMock).not.toHaveBeenCalled();
    expect(result).toEqual({ id: "t1", queued: false });
  });

  it("queues a cleared field but not the defaults beside it", async () => {
    // The other half of the same shape: a real clear still goes out, and the
    // nulls for fields the row never held stay out of it. Without this the
    // queued entry would carry a change to fields the user never touched, and
    // the merge would hold them as conflicts.
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));

    await api.updateTransaction(
      "t1",
      { categoryId: null, payeeId: null, billingCycleId: null },
      { base: { ...txn, categoryId: "c1" } },
    );

    const entries = getOutboxSnapshot("u1");
    expect(entries[0]).toMatchObject({
      rowId: "t1",
      patch: { categoryId: null },
    });
    const entry = entries[0];
    if (entry.kind !== "edit") throw new Error("updateTransaction queued a non-edit");
    expect(Object.keys(entry.patch)).toEqual(["categoryId"]);
  });

  it("sends the whole payload when no base is supplied", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ message: "updated" }));

    await api.updateTransaction("t1", { notes: "new", description: "Coffee" });

    // A caller holding no base row must behave exactly as it did before the
    // offline edit existed: its payload, whole, because it is the only thing
    // that says what the user changed.
    const body = JSON.parse(fetchMock.mock.calls[0][1].body);
    expect(body).toEqual({ notes: "new", description: "Coffee" });
  });

  it("surfaces a rejected edit rather than queueing it", async () => {
    fetchMock.mockResolvedValue(
      jsonResponse({ errors: [{ message: "amount must be positive" }] }, 400),
    );

    await expect(
      api.updateTransaction("t1", { amount: -1 }, { base: txn }),
    ).rejects.toMatchObject({ message: "amount must be positive", status: 400 });
    expect(getOutboxSnapshot("u1")).toHaveLength(0);
  });

  it("fails the flush rather than re-queueing the entry it is sending", async () => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));

    // queue: false is what a caller *sending* an already-queued edit passes: a
    // second copy of the entry behind the flush's back would replay it twice.
    await expect(
      api.updateTransaction("t1", { notes: "x" }, { base: txn, queue: false }),
    ).rejects.toThrow(NetworkError);
    expect(getOutboxSnapshot("u1")).toHaveLength(0);
  });

  it("refuses to queue an edit under a session that replaced the issuing one", async () => {
    fetchMock.mockImplementation(() => {
      storeUser({ id: "u2", email: "b@c.d" } as never);
      return Promise.reject(new TypeError("Failed to fetch"));
    });

    // Neither queue may take it: u1 is gone from this browser and u2 never
    // recorded it, and the server would reject an u1 entry flushed under u2.
    await expect(
      api.updateTransaction("t1", { notes: "x" }, { base: txn }),
    ).rejects.toThrow(NetworkError);
    expect(getOutboxSnapshot("u1")).toHaveLength(0);
    expect(getOutboxSnapshot("u2")).toHaveLength(0);
  });

  it("refuses to queue an edit the browser would not let it store", async () => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));
    const setItem = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("QuotaExceededError");
      });

    // A queue that does not exist is an edit the user believes is saved: this
    // has to fail loudly rather than resolve `queued: true`.
    await expect(
      api.updateTransaction("t1", { notes: "x" }, { base: txn }),
    ).rejects.toBeInstanceOf(OutboxStorageError);
    setItem.mockRestore();

    expect(getOutboxSnapshot("u1")).toHaveLength(0);
  });

  it("cannot queue an edit made with no base", async () => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));

    // There is nothing to be a patch *against*: an entry with an empty base
    // would merge as though the user had changed every field in it. Answering
    // `queued` here would be a lie the flush cannot act on either.
    await expect(
      api.updateTransaction("t1", { notes: "x" }),
    ).rejects.toThrow(NetworkError);
    expect(getOutboxSnapshot("u1")).toHaveLength(0);
  });

  // The partial-update families from here on. They are the same pattern as the
  // transaction above — a base, a diff, and a queue on a transport failure — on
  // the six endpoints whose handlers leave an omitted key alone.
  it.each(PARTIAL_FAMILY)("$op PUTs only the field the user changed", async ({
    url,
    diff,
    base,
    payload,
    send,
  }) => {
    fetchMock.mockResolvedValue(jsonResponse({ id: "x" }));

    await send(payload, { base });

    const [called, opts] = fetchMock.mock.calls[0];
    expect(called).toBe(`${API_BASE}${url}`);
    expect(opts.method).toBe("PUT");
    expect(JSON.parse(opts.body)).toEqual(diff);
  });

  it.each(PARTIAL_FAMILY)("$op sends the whole payload when no base is supplied", async ({
    url,
    payload,
    send,
  }) => {
    fetchMock.mockResolvedValue(jsonResponse({ id: "x" }));

    await send(payload, {});

    // A caller holding no base row must behave exactly as it did before the
    // offline edit existed: its payload, whole, because it is the only thing
    // that says what the user changed.
    const [called, opts] = fetchMock.mock.calls[0];
    expect(called).toBe(`${API_BASE}${url}`);
    expect(JSON.parse(opts.body)).toEqual(payload);
  });

  it.each(PARTIAL_FAMILY)("$op sends no request at all when the form changed nothing", async ({
    base,
    payload,
    revert,
    send,
  }) => {
    // The row already says what the form says. Sending it anyway would put a
    // request on the wire that writes nothing — and on the offline path would
    // queue an entry with no change in it.
    const result = await send({ ...payload, ...revert }, { base });

    expect(fetchMock).not.toHaveBeenCalled();
    expect(result).toEqual({ queued: false });
    expect(getOutboxSnapshot("u1")).toHaveLength(0);
  });

  it.each(PARTIAL_FAMILY)("$op queues the diff against the projected base when the server is unreachable", async ({
    op,
    rowId,
    diff,
    queuedBase,
    base,
    payload,
    send,
  }) => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));

    const result = await send(payload, { base });

    expect(result).toEqual({ queued: true });
    const entries = getOutboxSnapshot("u1");
    expect(entries).toHaveLength(1);
    // The entry is the merge's whole input: which endpoint to reach, which row,
    // the fields the user changed, and the base they were changed from.
    expect(entries[0]).toMatchObject({ kind: "edit", op, rowId, patch: diff });
    const entry = entries[0];
    if (entry.kind !== "edit") throw new Error(`${op} queued a non-edit`);
    // The projection, not the row: `balance`, `hasToken` and the rest are on the
    // row and on no endpoint, and a base carrying one of them would hold the
    // field as a change nobody made.
    expect(entry.base).toEqual(queuedBase);
  });

  it.each(PARTIAL_FAMILY)("$op fails the flush rather than re-queueing the entry it is sending", async ({
    base,
    payload,
    send,
  }) => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));

    // queue: false is what a caller *sending* an already-queued edit passes: a
    // second copy of the entry behind the flush's back would replay it twice.
    await expect(send(payload, { base, queue: false })).rejects.toThrow(NetworkError);
    expect(getOutboxSnapshot("u1")).toHaveLength(0);
  });

  it.each(PARTIAL_FAMILY)("$op cannot queue an edit made with no base", async ({
    payload,
    send,
  }) => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));

    // Nothing to be a patch *against*, so there is nothing to record: an entry
    // with an empty base would merge as though the user had changed every field
    // in it. The failure is the honest answer.
    await expect(send(payload, {})).rejects.toThrow(NetworkError);
    expect(getOutboxSnapshot("u1")).toHaveLength(0);
  });

  // The whole-row family, and it is the same six questions asked again. The
  // endpoint writes every column its body names, which is why the answer for this
  // family is a *diff* rather than a whole row: the whole row is the flush's job to
  // build, by overlaying this diff onto the row it re-reads (registry.ts's
  // mergedRow), and a save that sent the form's row instead would wipe whatever
  // moved while the form was open.
  it.each(WHOLE_FAMILY)("$op PUTs only the field the user changed", async ({
    url,
    diff,
    base,
    payload,
    send,
  }) => {
    fetchMock.mockResolvedValue(jsonResponse({ id: "x" }));

    await send(payload, { base });

    const [called, opts] = fetchMock.mock.calls[0];
    expect(called).toBe(`${API_BASE}${url}`);
    expect(opts.method).toBe("PUT");
    expect(JSON.parse(opts.body)).toEqual(diff);
  });

  it.each(WHOLE_FAMILY)("$op sends the whole payload when no base is supplied", async ({
    url,
    payload,
    send,
  }) => {
    fetchMock.mockResolvedValue(jsonResponse({ id: "x" }));

    await send(payload, {});

    // Unchanged from before the offline edit existed: a caller holding no base row
    // has only its payload, and for this family the payload *is* the whole row —
    // loan.go:324 and recurring.go:922 cannot write one term of it.
    const [called, opts] = fetchMock.mock.calls[0];
    expect(called).toBe(`${API_BASE}${url}`);
    expect(JSON.parse(opts.body)).toEqual(payload);
  });

  it.each(WHOLE_FAMILY)("$op sends no request at all when the form changed nothing", async ({
    base,
    payload,
    revert,
    send,
  }) => {
    const result = await send({ ...payload, ...revert }, { base });

    expect(fetchMock).not.toHaveBeenCalled();
    expect(result).toEqual({ queued: false });
    expect(getOutboxSnapshot("u1")).toHaveLength(0);
  });

  it.each(WHOLE_FAMILY)("$op queues the diff against the projected base when the server is unreachable", async ({
    op,
    rowId,
    diff,
    queuedBase,
    base,
    payload,
    send,
  }) => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));

    const result = await send(payload, { base });

    expect(result).toEqual({ queued: true });
    const entries = getOutboxSnapshot("u1");
    expect(entries).toHaveLength(1);
    // The op is what tells the flush which endpoint to reach and therefore that
    // the diff has to be overlaid onto the server's row rather than sent alone.
    expect(entries[0]).toMatchObject({ kind: "edit", op, rowId, patch: diff });
    const entry = entries[0];
    if (entry.kind !== "edit") throw new Error(`${op} queued a non-edit`);
    // The projection, not the row: `id`, `createdAt`, `monthlyAmount` and the rest
    // are on these rows and on no request, and a base carrying one of them would
    // hold the field as a change nobody made.
    expect(entry.base).toEqual(queuedBase);
  });

  it.each(WHOLE_FAMILY)("$op fails the flush rather than re-queueing the entry it is sending", async ({
    base,
    payload,
    send,
  }) => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));

    await expect(send(payload, { base, queue: false })).rejects.toThrow(NetworkError);
    expect(getOutboxSnapshot("u1")).toHaveLength(0);
  });

  it.each(WHOLE_FAMILY)("$op cannot queue an edit made with no base", async ({
    payload,
    send,
  }) => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));

    // Nothing to be a patch *against*: the flush needs the base to tell the user's
    // change from someone else's, and an entry without one would merge as though
    // they had changed every field in it.
    await expect(send(payload, {})).rejects.toThrow(NetworkError);
    expect(getOutboxSnapshot("u1")).toHaveLength(0);
  });

  // A clear is a change, and this family is the one where dropping it would be
  // invisible: a term with no end date is open-ended, so a body that lost the key
  // would leave the range the user just closed exactly as it was.
  it("keeps a clear in a whole-row diff, which is a value the endpoint writes", async () => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));

    await api.updateRecurringTerm(
      "s1",
      "term-1",
      { startDate: "2026-01-01", endDate: "", amount: 649, accountId: "a1" },
      { base: term },
    );

    const entry = getOutboxSnapshot("u1")[0];
    if (entry.kind !== "edit") throw new Error("updateRecurringTerm queued a non-edit");
    // "" and not an absent key: an omitted end_date in a merged whole-row request
    // is the row the server holds, not the open-ended range the user chose.
    expect(entry.patch).toEqual({ endDate: "" });
  });

  it("keeps a cleared field in a queued diff, and never the token beside it", async () => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));

    // Two things about the settings singleton in one payload. pageSize: null is
    // the user clearing the page size, and the endpoint reads a null as a clear
    // (paperless.go:504 binds page_size whenever the field is set) — a
    // projection of the user's side would have dropped it, so the queue would
    // hold a change that is not there. paperlessToken is the field the base can
    // never carry, and it is sent only because the user typed it.
    await api.updateUserSettings(
      { pageSize: null, paperlessToken: "typed-by-the-user" },
      { base: settings },
    );

    const entries = getOutboxSnapshot("u1");
    const entry = entries[0];
    if (entry.kind !== "edit") throw new Error("updateUserSettings queued a non-edit");
    expect(entry.patch).toEqual({ pageSize: null, paperlessToken: "typed-by-the-user" });
    // The base never holds a token, and never hasToken either: the response
    // reports whether one is set and never what it is (models.go:272-277), so a
    // base carrying either would put a value on the wire this client never read.
    expect(entry.base).not.toHaveProperty("paperlessToken");
    expect(entry.base).not.toHaveProperty("hasToken");
  });

  it("surfaces a rejected partial update rather than queueing it", async () => {
    fetchMock.mockResolvedValue(
      jsonResponse({ errors: [{ message: "name is required" }] }, 400),
    );

    await expect(
      api.updateCategory("c1", { name: "" }, { base: category }),
    ).rejects.toMatchObject({ message: "name is required", status: 400 });
    expect(getOutboxSnapshot("u1")).toHaveLength(0);
  });

  it("refuses to queue a partial update under a session that replaced the issuing one", async () => {
    fetchMock.mockImplementation(() => {
      storeUser({ id: "u2", email: "b@c.d" } as never);
      return Promise.reject(new TypeError("Failed to fetch"));
    });

    // Neither queue may take it: u1 is gone from this browser and u2 never
    // recorded it, and the server would reject an u1 entry flushed under u2.
    await expect(
      api.updateAccount(
        "a1",
        { name: "Renamed", accountTypeId: "at1", bank: "HDFC" },
        { base: account },
      ),
    ).rejects.toThrow(NetworkError);
    expect(getOutboxSnapshot("u1")).toHaveLength(0);
    expect(getOutboxSnapshot("u2")).toHaveLength(0);
  });

  // The row-naming family. Same six questions as the two tables above, and the
  // same two extra guards below: a write that names rows is not a smaller change
  // than one that names a row, and the reason a per-row base exists at all is
  // that a single base for the batch would merge a row nobody changed against a
  // row that did (outbox.ts's BulkEntry).
  //
  // The `queued` helper narrows the union the way outbox.ts does, rather than
  // asserting with a bang: an entry that is not a bulk write is a bug worth
  // hearing about, and the fields the assertions below read are on no other kind.
  function queued(index = 0): BulkEntry {
    const entry = getOutboxSnapshot("u1")[index];
    if (entry?.kind !== "bulk") throw new Error("expected a queued bulk write");
    return entry;
  }

  // The two detach writes have no base in which nothing changes, so they are not
  // in the table for that one question — see MULTI_ROW's `settled`.
  const SETTLEABLE = MULTI_ROW.filter((row) => row.settled);

  it.each(MULTI_ROW)("$label sends only the rows the base says change", async ({
    method,
    url,
    base,
    body,
    payload,
    send,
  }) => {
    fetchMock.mockResolvedValue(jsonResponse({}));

    await send({ payload, base });

    // The caller's own payload, with the rows the base rules out taken out. A row
    // already holding the value is not part of the write, so putting it on the
    // wire would ask the server to write what it already holds — and the queue
    // would then record a row the user never changed, which the merge would hold
    // as a conflict over their own edit. A base that cannot say (a row it is
    // silent about, as with a nullish value) leaves the row in, which is why the
    // two detaches send all three of theirs.
    const [called, opts] = fetchMock.mock.calls[0];
    expect(called).toBe(`${API_BASE}${url}`);
    expect(opts.method).toBe(method);
    expect(JSON.parse(opts.body)).toEqual(body);
  });

  it.each(MULTI_ROW)("$label sends the whole payload when no base is supplied", async ({
    url,
    payload,
    send,
  }) => {
    fetchMock.mockResolvedValue(jsonResponse({}));

    await send({ payload });

    // Unchanged from before the offline write existed: a caller holding no base
    // rows has only its payload, and the rows it names are the write.
    const [called, opts] = fetchMock.mock.calls[0];
    expect(called).toBe(`${API_BASE}${url}`);
    expect(JSON.parse(opts.body)).toEqual(payload);
  });

  it.each(SETTLEABLE)("$label sends no request at all when no row changes", async ({
    settled,
    payload,
    send,
  }) => {
    // Every row already says what the user asked for, so there is nothing to
    // write: no request on the wire, and no entry for the flush to merge.
    const result = await send({ payload, base: settled });

    expect(fetchMock).not.toHaveBeenCalled();
    expect(result).toEqual({ queued: false, queuedRows: 0 });
    expect(getOutboxSnapshot("u1")).toHaveLength(0);
  });

  it.each(MULTI_ROW)("$label queues a bulk entry with the per-row base when the server is unreachable", async ({
    op,
    field,
    value,
    rows,
    base,
    queuedBase,
    payload,
    send,
  }) => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));

    const result = await send({ payload, base });

    expect(result).toEqual({ queued: true, queuedRows: rows.length });
    // The entry is the merge's whole input: which endpoint to reach, which field
    // of each row it writes, the one value, the rows that write it, and the base
    // each of those rows held.
    const entry = queued();
    expect(entry).toMatchObject({ op, field, value, rows });
    expect(entry.bases).toEqual(queuedBase);
  });

  it.each(MULTI_ROW)("$label fails the flush rather than re-queueing the entry it is sending", async ({
    base,
    payload,
    send,
  }) => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));

    // queue: false is what a caller *sending* an already-queued write passes: a
    // second copy of the entry behind the flush's back would replay it twice.
    await expect(send({ payload, base, queue: false })).rejects.toThrow(NetworkError);
    expect(getOutboxSnapshot("u1")).toHaveLength(0);
  });

  it.each(MULTI_ROW)("$label cannot queue a write made with no base", async ({
    payload,
    send,
  }) => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));

    // There is nothing for the merge to tell the user's change from somebody
    // else's: a per-row base is what makes this write a patch, and an entry
    // without one would hold every row it names over a change nobody made. The
    // failure is the honest answer.
    await expect(send({ payload })).rejects.toThrow(NetworkError);
    expect(getOutboxSnapshot("u1")).toHaveLength(0);
  });

  it("refuses to queue a row-naming write under a session that replaced the issuing one", async () => {
    fetchMock.mockImplementation(() => {
      storeUser({ id: "u2", email: "b@c.d" } as never);
      return Promise.reject(new TypeError("Failed to fetch"));
    });

    // Neither queue may take it: u1 is gone from this browser and u2 never
    // recorded it, and the server would reject an u1 entry flushed under u2.
    await expect(
      api.bulkCategorize(
        { transactionIds: ["t1", "t2"], categoryId: "c1" },
        { base: { t1: null, t2: "c0" } },
      ),
    ).rejects.toThrow(NetworkError);
    expect(getOutboxSnapshot("u1")).toHaveLength(0);
    expect(getOutboxSnapshot("u2")).toHaveLength(0);
  });

  it("refuses to claim a queued bulk write the browser would not let it store", async () => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));
    const setItem = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("QuotaExceededError");
      });

    // A queue the browser will not write must reach the user as a failure: the
    // UI would otherwise confirm a write no flush will ever perform.
    await expect(
      api.bulkCategorize(
        { transactionIds: ["t1"], categoryId: "c1" },
        { base: { t1: null } },
      ),
    ).rejects.toThrow(OutboxStorageError);
    setItem.mockRestore();
  });

  it("surfaces a rejected row-naming write rather than queueing it", async () => {
    fetchMock.mockResolvedValue(
      jsonResponse({ errors: [{ message: "category not found" }] }, 400),
    );

    await expect(
      api.bulkCategorize(
        { transactionIds: ["t1"], categoryId: "c1" },
        { base: { t1: null } },
      ),
    ).rejects.toMatchObject({ message: "category not found", status: 400 });
    expect(getOutboxSnapshot("u1")).toHaveLength(0);
  });

  // The endpoint's own identifier rides beside the value, named after what the
  // request type calls it, because the value alone says what to write and not
  // which loan account or series the user picked. A detach records none: there is
  // no account or series being left, and recording the one the row held would
  // name a target the write is about to remove.
  it("records the series an attach names, and none for a detach", async () => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));

    await api.attachRecurring(
      { seriesId: "s1", transactionIds: ["t1"] },
      { base: { t1: null } },
    );
    await api.detachRecurring(
      { transactionIds: ["t2"] },
      { base: { t2: "s0" } },
    );

    expect(queued(0).seriesId).toBe("s1");
    expect(queued(1).seriesId).toBeUndefined();
  });

  it("records the loan an attach names, and none for a detach", async () => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));

    await api.bulkLoan(
      { transactionIds: ["t1"], loanAccountId: "a1" },
      { base: { t1: null } },
    );
    await api.bulkLoan(
      { transactionIds: ["t2"], loanAccountId: null },
      { base: { t2: "a0" } },
    );

    expect(queued(0).loanAccountId).toBe("a1");
    expect(queued(1).loanAccountId).toBeUndefined();
  });

  // The one place both halves of the tag encoding are exercised at once. The
  // client writes the delta and registry.ts reads it back, and the two agreeing is
  // the whole of a queued removal: a marker one side changed alone sends the
  // user's removal back as an addition, and the tag they took off comes back on
  // its own. The registry's own test cannot catch that — it mocks the client.
  it("sends a queued tag removal back out as a removal", async () => {
    fetchMock.mockRejectedValueOnce(new TypeError("Failed to fetch"));
    await api.bulkUpdateTags(
      { transactionIds: ["t1"], add: [], remove: ["milk"] },
      { base: { t1: ["milk", "bread"] } },
    );

    fetchMock.mockResolvedValue(jsonResponse({ updated: 1 }));
    await flushOutbox(
      "u1",
      async (entry) => {
        if (entry.kind !== "bulk") return;
        const spec = OPS[entry.op];
        if (!spec.applyMany) throw new Error(`${entry.op} declares no applyMany`);
        await spec.applyMany(entry.rows, entry.value);
      },
      { theirs: async () => ({ tags: ["milk", "bread"] }) },
    );

    const [called, opts] = fetchMock.mock.calls.at(-1) as [string, RequestInit];
    expect(called).toBe(`${API_BASE}/transactions/bulk-tags`);
    expect(JSON.parse(opts.body as string)).toEqual({
      transactionIds: ["t1"],
      add: [],
      remove: ["milk"],
    });
  });
});

describe("the query language and the offline cache", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    localStorage.clear();
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    storeUser({ id: "u1", email: "a@b.c" } as any);
  });

  afterEach(() => vi.unstubAllGlobals());

  it("keeps a queried read out of the cache, so exploring queries cannot evict the default view", async () => {
    // The cache is keyed on the full URL, so every distinct q would take one of
    // the 40 slots and compete for the 2MB cap. A user trying a few queries
    // would push out the unfiltered view they actually want offline.
    fetchMock.mockResolvedValueOnce(jsonResponse({ data: [], total: 0, page: 1, pages: 1 }));
    await api.getTransactions({ q: "amt>50" });

    expect(readCached("u1", "/transactions?q=amt>50")).toBeNull();
  });

  it("still caches the unfiltered ledger read", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ data: [{ id: "t1" }], total: 1, page: 1, pages: 1 }));
    await api.getTransactions({});

    // The key is the full request URL, which getTransactions builds as
    // `/transactions?${qs}` — so it keeps the trailing "?" even with no params.
    expect(readCached("u1", "/transactions?")).toEqual({
      data: [{ id: "t1" }],
      total: 1,
      page: 1,
      pages: 1,
    });
  });

  it("leaves the dashboard reads alone: they carry no q", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ days: [], markers: [] }));
    await api.getCashFlowCalendar({ accountId: "a1" });

    expect(readCached("u1", "/dashboard/cash-flow-calendar?accountId=a1")).not.toBeNull();
  });
});
