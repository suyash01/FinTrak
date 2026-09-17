import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import api from "./client";

const API_BASE = "/api/v1";

function ok(body: unknown = {}) {
  return {
    ok: true,
    status: 200,
    statusText: "OK",
    headers: new Headers(),
    json: async () => body,
    text: async () => JSON.stringify(body),
    blob: async () => new Blob(["x"]),
  };
}

// Each entry exercises one client method and asserts the request it builds.
type Case = {
  name: string;
  call: () => Promise<unknown>;
  url: string;
  method: string;
};

describe("api method surface", () => {
  let fetchMock: ReturnType<typeof vi.fn>;
  const originalLocation = window.location;

  beforeEach(() => {
    localStorage.clear();
    fetchMock = vi.fn().mockResolvedValue(ok({}));
    vi.stubGlobal("fetch", fetchMock);
    Object.defineProperty(window, "location", {
      configurable: true,
      value: { pathname: "/", href: "", assign: vi.fn(), replace: vi.fn(), reload: vi.fn(), toString: () => "" },
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    Object.defineProperty(window, "location", { configurable: true, value: originalLocation });
  });

  const cases: Case[] = [
    { name: "register", call: () => api.register({ email: "a", password: "b" }), url: "/auth/register", method: "POST" },
    { name: "logout", call: () => api.logout(), url: "/auth/logout", method: "POST" },
    { name: "getAccounts", call: () => api.getAccounts(), url: "/accounts", method: "GET" },
    { name: "createAccount", call: () => api.createAccount({ name: "A" } as any), url: "/accounts", method: "POST" },
    { name: "updateAccount", call: () => api.updateAccount("a1", { name: "A" } as any), url: "/accounts/a1", method: "PUT" },
    { name: "deleteAccount", call: () => api.deleteAccount("a1"), url: "/accounts/a1", method: "DELETE" },
    { name: "getBillingCycles", call: () => api.getBillingCycles("a1"), url: "/accounts/a1/billing-cycles", method: "GET" },
    { name: "getAccountTypes", call: () => api.getAccountTypes(), url: "/account-types", method: "GET" },
    { name: "createAccountType", call: () => api.createAccountType({ name: "A" } as any), url: "/account-types", method: "POST" },
    { name: "updateAccountType", call: () => api.updateAccountType("t1", { name: "A" } as any), url: "/account-types/t1", method: "PUT" },
    { name: "deleteAccountType", call: () => api.deleteAccountType("t1"), url: "/account-types/t1", method: "DELETE" },
    { name: "getCategories", call: () => api.getCategories(), url: "/categories", method: "GET" },
    { name: "createCategory", call: () => api.createCategory({ name: "C" } as any), url: "/categories", method: "POST" },
    { name: "updateCategory", call: () => api.updateCategory("c1", { name: "C" } as any), url: "/categories/c1", method: "PUT" },
    { name: "deleteCategory", call: () => api.deleteCategory("c1"), url: "/categories/c1", method: "DELETE" },
    { name: "getGroups", call: () => api.getGroups(), url: "/groups", method: "GET" },
    { name: "createGroup", call: () => api.createGroup({ id: "g", name: "G" } as any), url: "/groups", method: "POST" },
    { name: "updateGroup", call: () => api.updateGroup("g", { name: "G" } as any), url: "/groups/g", method: "PUT" },
    { name: "deleteGroup", call: () => api.deleteGroup("g"), url: "/groups/g", method: "DELETE" },
    { name: "createGlobalGroup", call: () => api.createGlobalGroup({ id: "g", name: "G" } as any), url: "/admin/groups", method: "POST" },
    { name: "createGlobalCategory", call: () => api.createGlobalCategory({ name: "C" } as any), url: "/admin/categories", method: "POST" },
    { name: "updateGlobalCategory", call: () => api.updateGlobalCategory("c1", { name: "C" } as any), url: "/admin/categories/c1", method: "PUT" },
    { name: "deleteGlobalCategory", call: () => api.deleteGlobalCategory("c1"), url: "/admin/categories/c1", method: "DELETE" },
    { name: "createTransaction", call: () => api.createTransaction({} as any), url: "/transactions", method: "POST" },
    { name: "updateTransaction", call: () => api.updateTransaction("t1", {} as any), url: "/transactions/t1", method: "PATCH" },
    { name: "deleteTransaction", call: () => api.deleteTransaction("t1"), url: "/transactions/t1", method: "DELETE" },
    { name: "importTransactions", call: () => api.importTransactions({} as any), url: "/transactions/import", method: "POST" },
    { name: "bulkCategorize", call: () => api.bulkCategorize({} as any), url: "/transactions/bulk-categorize", method: "POST" },
    { name: "bulkUpdatePayee", call: () => api.bulkUpdatePayee({} as any), url: "/transactions/bulk-payee", method: "POST" },
    { name: "bulkUpdateBillingCycle", call: () => api.bulkUpdateBillingCycle({} as any), url: "/transactions/bulk-billing-cycle", method: "POST" },
    { name: "bulkDeleteTransactions", call: () => api.bulkDeleteTransactions({} as any), url: "/transactions/bulk-delete", method: "POST" },
    { name: "bulkLoan", call: () => api.bulkLoan({} as any), url: "/transactions/bulk-loan", method: "POST" },
    { name: "getStatementExtractors", call: () => api.getStatementExtractors(), url: "/statements/extractors", method: "GET" },
    { name: "parseStatement", call: () => api.parseStatement(new FormData()), url: "/statements/parse", method: "POST" },
    { name: "getPaperlessSettings", call: () => api.getPaperlessSettings(), url: "/paperless/settings", method: "GET" },
    { name: "updatePaperlessSettings", call: () => api.updatePaperlessSettings({} as any), url: "/paperless/settings", method: "PUT" },
    { name: "getUserSettings", call: () => api.getUserSettings(), url: "/paperless/settings", method: "GET" },
    { name: "updateUserSettings", call: () => api.updateUserSettings({} as any), url: "/paperless/settings", method: "PUT" },
    { name: "importPaperlessDocument", call: () => api.importPaperlessDocument({} as any), url: "/paperless/import", method: "POST" },
    { name: "getRules", call: () => api.getRules(), url: "/rules", method: "GET" },
    { name: "createRule", call: () => api.createRule({} as any), url: "/rules", method: "POST" },
    { name: "updateRule", call: () => api.updateRule("r1", {} as any), url: "/rules/r1", method: "PUT" },
    { name: "deleteRule", call: () => api.deleteRule("r1"), url: "/rules/r1", method: "DELETE" },
    { name: "applyRules", call: () => api.applyRules(), url: "/rules/apply", method: "POST" },
    { name: "getPayees", call: () => api.getPayees(), url: "/payees", method: "GET" },
    { name: "createPayee", call: () => api.createPayee({} as any), url: "/payees", method: "POST" },
    { name: "updatePayee", call: () => api.updatePayee("p1", {} as any), url: "/payees/p1", method: "PUT" },
    { name: "deletePayee", call: () => api.deletePayee("p1"), url: "/payees/p1", method: "DELETE" },
    { name: "getCashFlowCalendar", call: () => api.getCashFlowCalendar(), url: "/dashboard/cash-flow-calendar", method: "GET" },
    { name: "createLink", call: () => api.createLink({} as any), url: "/links", method: "POST" },
    { name: "deleteLink", call: () => api.deleteLink("l1"), url: "/links/l1", method: "DELETE" },
    { name: "bulkDeleteLinks", call: () => api.bulkDeleteLinks({} as any), url: "/links/bulk-delete", method: "POST" },
  ];

  it.each(cases)("$name hits $method $url", async ({ call, url, method }) => {
    await call();
    const [calledUrl, opts] = fetchMock.mock.calls[0];
    expect(calledUrl).toBe(`${API_BASE}${url}`);
    expect(opts.method ?? "GET").toBe(method);
  });

  it("builds the transactions query string", async () => {
    await api.getTransactions({ accountId: "a1", limit: 10 });
    expect(fetchMock.mock.calls[0][0]).toBe(`${API_BASE}/transactions?accountId=a1&limit=10`);
  });

  it("builds the links query string only when params exist", async () => {
    await api.getLinks();
    expect(fetchMock.mock.calls[0][0]).toBe(`${API_BASE}/links`);

    await api.getLinks({ type: "transfer" });
    expect(fetchMock.mock.calls[1][0]).toBe(`${API_BASE}/links?type=transfer`);
  });

  it("builds the dashboard query string", async () => {
    await api.getDashboardSummary({ groupBy: "billing_cycle" });
    expect(fetchMock.mock.calls[0][0]).toBe(`${API_BASE}/dashboard/summary?groupBy=billing_cycle`);
  });

  it("builds the cash-flow calendar query string only when params exist", async () => {
    await api.getCashFlowCalendar();
    expect(fetchMock.mock.calls[0][0]).toBe(`${API_BASE}/dashboard/cash-flow-calendar`);

    await api.getCashFlowCalendar({ accountId: "a1", dateFrom: "2024-06-01" });
    expect(fetchMock.mock.calls[1][0]).toBe(
      `${API_BASE}/dashboard/cash-flow-calendar?accountId=a1&dateFrom=2024-06-01`,
    );
  });

  it("serializes every paperless documents filter", async () => {
    await api.getPaperlessDocuments({
      search: "rent",
      page: 3,
      pageSize: 50,
      correspondentInc: ["Bank"],
      correspondentExc: ["Spam"],
      documentTypeInc: ["Statement"],
      documentTypeExc: ["Receipt"],
      tagInc: ["finance"],
      tagExc: ["old"],
    });

    const called = new URL(fetchMock.mock.calls[0][0], "http://localhost");
    expect(called.pathname).toBe(`${API_BASE}/paperless/documents`);
    expect(called.searchParams.get("search")).toBe("rent");
    expect(called.searchParams.get("page")).toBe("3");
    expect(called.searchParams.get("pageSize")).toBe("50");
    expect(called.searchParams.getAll("correspondentInc")).toEqual(["Bank"]);
    expect(called.searchParams.getAll("correspondentExc")).toEqual(["Spam"]);
    expect(called.searchParams.getAll("documentTypeInc")).toEqual(["Statement"]);
    expect(called.searchParams.getAll("documentTypeExc")).toEqual(["Receipt"]);
    expect(called.searchParams.getAll("tagInc")).toEqual(["finance"]);
    expect(called.searchParams.getAll("tagExc")).toEqual(["old"]);
  });

  it("omits page=1 and empty filters from the paperless query", async () => {
    await api.getPaperlessDocuments({ page: 1 });
    expect(fetchMock.mock.calls[0][0]).toBe(`${API_BASE}/paperless/documents`);
  });

  it("loads a paperless document file as a blob", async () => {
    const res = await api.getPaperlessDocumentFile(42);
    expect(res).toBeInstanceOf(Blob);
    expect(fetchMock.mock.calls[0][0]).toBe(`${API_BASE}/paperless/documents/42/file`);
  });

  it("throws with status when the document file fails", async () => {
    fetchMock.mockResolvedValue({ ok: false, status: 502 });
    await expect(api.getPaperlessDocumentFile(42)).rejects.toMatchObject({ status: 502 });
  });

  it("extracts the first field error message", async () => {
    fetchMock.mockResolvedValue({
      ok: false,
      status: 400,
      statusText: "Bad Request",
      headers: new Headers(),
      json: async () => ({ errors: [{ message: "name is required" }] }),
      text: async () => "",
    });
    await expect(api.createAccount({} as any)).rejects.toThrow("name is required");
  });

  it("falls back to a generic message when the payload has none", async () => {
    fetchMock.mockResolvedValue({
      ok: false,
      status: 400,
      statusText: "Bad Request",
      headers: new Headers(),
      json: async () => ({ errors: [{ message: "" }] }),
      text: async () => "",
    });
    await expect(api.createAccount({} as any)).rejects.toThrow("Request failed");
  });
});
