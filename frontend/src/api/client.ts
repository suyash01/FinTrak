import type {
  Account,
  AccountType,
  ApplyRulesResult,
  AuthResponse,
  BillingCycle,
  BulkCategorizeRequest,
  BulkDeleteLinksRequest,
  BulkDeleteTransactionsRequest,
  BulkBillingCycleRequest,
  BulkUpdatePayeeRequest,
  Category,
  CategoryGroup,
  CreateAccountRequest,
  CreateAccountTypeRequest,
  CreateCategoryGroupRequest,
  CreateCategoryRequest,
  CreateLinkRequest,
  CreatePayeeRequest,
  CreateRuleRequest,
  CreateTransactionRequest,
  DashboardSummary,
  DeleteCategoryResult,
  ImportResult,
  ImportTransactionsRequest,
  Link,
  LoginRequest,
  PaperlessDocumentsResponse,
  PaperlessDocumentsParams,
  PaperlessImportRequest,
  PaperlessImportResult,
  Payee,
  QueryParams,
  RegisterRequest,
  Rule,
  StatementExtractor,
  StatementParseResult,
  Transaction,
  TransactionsResponse,
  UpdateAccountRequest,
  UpdateAccountTypeRequest,
  UpdateCategoryGroupRequest,
  UpdateCategoryRequest,
  UpdatePayeeRequest,
  UpdateRuleRequest,
  UpdateTransactionRequest,
  UpdateUserSettingsRequest,
  User,
  UserSettings,
  ValidateTransactionsRequest,
  ValidateTransactionsResponse,
} from "../types";

const API_BASE = import.meta.env.VITE_API_URL || "http://localhost:8080/api/v1";

const TOKEN_KEY = "fintrak_token";
const USER_KEY = "fintrak_user";

const REQUEST_TIMEOUT = 15000;

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}

export function setToken(token: string | null): void {
  if (token) {
    localStorage.setItem(TOKEN_KEY, token);
  } else {
    localStorage.removeItem(TOKEN_KEY);
  }
}

export function getStoredUser(): User | null {
  try {
    return JSON.parse(localStorage.getItem(USER_KEY) || "null");
  } catch {
    return null;
  }
}

export function storeUser(user: User | null): void {
  if (user) {
    localStorage.setItem(USER_KEY, JSON.stringify(user));
  } else {
    localStorage.removeItem(USER_KEY);
  }
}

interface ApiError extends Error {
  status?: number;
}

// extractErrorMessage normalizes API error payloads. Handlers return the
// field-error envelope ({ errors: [{ message }] }); some paths (and network
// failures) still surface a flat { error } string.
function extractErrorMessage(payload: unknown, fallback: string): string {
  if (payload && typeof payload === "object") {
    const p = payload as Record<string, unknown>;
    if (Array.isArray(p.errors) && p.errors.length > 0) {
      const first = p.errors[0] as { message?: unknown } | undefined;
      if (first && typeof first.message === "string" && first.message) {
        return first.message;
      }
    }
    if (typeof p.error === "string" && p.error) {
      return p.error;
    }
  }
  return fallback;
}

interface RequestOptions {
  method?: string;
  body?: string;
  signal?: AbortSignal;
  headers?: Record<string, string>;
}

function buildQuery(params: QueryParams): string {
  return new URLSearchParams(
    Object.entries(params).map(([k, v]) => [k, String(v)]),
  ).toString();
}

async function request<T>(
  url: string,
  options: RequestOptions = {},
): Promise<T> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...options.headers,
  };
  const token = getToken();
  if (token) headers["Authorization"] = `Bearer ${token}`;

  // Combine a caller-provided abort signal with a request timeout
  const controller = new AbortController();
  const externalSignal = options.signal;
  let timedOut = false;

  const abort = () => controller.abort();
  if (externalSignal) {
    if (externalSignal.aborted) controller.abort();
    else externalSignal.addEventListener("abort", abort, { once: true });
  }
  const timer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, REQUEST_TIMEOUT);

  let res: Response;
  try {
    res = await fetch(`${API_BASE}${url}`, {
      ...options,
      signal: controller.signal,
      headers,
    });
  } catch (err) {
    if ((err as Error).name === "AbortError") {
      if (timedOut) throw new Error("Request timed out");
      throw err;
    }
    throw new Error("Network error: could not reach the API server");
  } finally {
    clearTimeout(timer);
    if (externalSignal) externalSignal.removeEventListener("abort", abort);
  }

  if (res.status === 401 && url !== "/auth/login" && url !== "/auth/register") {
    setToken(null);
    storeUser(null);
    if (!window.location.pathname.startsWith("/login")) {
      window.location.href = "/login";
    }
  }

  if (!res.ok) {
    const error = await res.json().catch(() => ({ error: res.statusText }));
    const err = new Error(
      extractErrorMessage(error, "Request failed"),
    ) as ApiError;
    err.status = res.status;
    throw err;
  }

  const text = await res.text();
  return text ? (JSON.parse(text) as T) : (null as T);
}

// requestMultipart POSTs a FormData payload (multipart/form-data) with the auth
// header but without forcing a JSON content type, which the browser must set
// itself (including the boundary). Used for statement PDF uploads.
async function requestMultipart<T>(
  url: string,
  formData: FormData,
): Promise<T> {
  const token = getToken();
  const headers: Record<string, string> = token
    ? { Authorization: `Bearer ${token}` }
    : {};

  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), REQUEST_TIMEOUT);
  let res: Response;
  try {
    res = await fetch(`${API_BASE}${url}`, {
      method: "POST",
      body: formData,
      headers,
      signal: controller.signal,
    });
  } catch (err) {
    if ((err as Error).name === "AbortError")
      throw new Error("Request timed out");
    throw new Error("Network error: could not reach the API server");
  } finally {
    clearTimeout(timer);
  }

  if (res.status === 401 && url !== "/auth/login" && url !== "/auth/register") {
    setToken(null);
    storeUser(null);
    if (!window.location.pathname.startsWith("/login")) {
      window.location.href = "/login";
    }
  }

  if (!res.ok) {
    const error = await res.json().catch(() => ({ error: res.statusText }));
    const err = new Error(
      extractErrorMessage(error, "Request failed"),
    ) as ApiError;
    err.status = res.status;
    throw err;
  }

  return res.json() as Promise<T>;
}

export async function downloadCSV(path: string): Promise<void> {
  const token = getToken();
  const res = await fetch(`${API_BASE}${path}`, {
    headers: { ...(token ? { Authorization: `Bearer ${token}` } : {}) },
  });
  if (!res.ok) {
    throw new Error("Export failed");
  }

  const blob = await res.blob();
  const disposition = res.headers.get("Content-Disposition") || "";
  const match = disposition.match(/filename="?([^"]+)"?/);
  const filename = match ? match[1] : "export.csv";

  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

const api = {
  // Auth
  register: (data: RegisterRequest): Promise<AuthResponse> =>
    request("/auth/register", { method: "POST", body: JSON.stringify(data) }),
  login: (data: LoginRequest): Promise<AuthResponse> =>
    request("/auth/login", { method: "POST", body: JSON.stringify(data) }),

  // Accounts
  getAccounts: (): Promise<Account[]> => request("/accounts"),
  createAccount: (data: CreateAccountRequest): Promise<Account> =>
    request("/accounts", { method: "POST", body: JSON.stringify(data) }),
  updateAccount: (id: string, data: UpdateAccountRequest): Promise<Account> =>
    request(`/accounts/${id}`, { method: "PUT", body: JSON.stringify(data) }),
  deleteAccount: (id: string): Promise<{ message?: string; transactionsDeleted?: number }> =>
      request(`/accounts/${id}`, { method: "DELETE" }),
  getBillingCycles: (accountId: string): Promise<{ data: BillingCycle[] }> =>
    request(`/accounts/${accountId}/billing-cycles`),

  // Account Types
  getAccountTypes: (): Promise<AccountType[]> => request("/account-types"),
  createAccountType: (data: CreateAccountTypeRequest): Promise<AccountType> =>
    request("/account-types", { method: "POST", body: JSON.stringify(data) }),
  updateAccountType: (
    id: string,
    data: UpdateAccountTypeRequest,
  ): Promise<AccountType> =>
    request(`/account-types/${id}`, {
      method: "PUT",
      body: JSON.stringify(data),
    }),
  deleteAccountType: (id: string): Promise<null> =>
    request(`/account-types/${id}`, { method: "DELETE" }),

  // Categories & groups
  getCategories: (): Promise<Category[]> => request("/categories"),
  createCategory: (data: CreateCategoryRequest): Promise<Category> =>
    request("/categories", { method: "POST", body: JSON.stringify(data) }),
  updateCategory: (id: string, data: UpdateCategoryRequest): Promise<Category> =>
    request(`/categories/${id}`, { method: "PUT", body: JSON.stringify(data) }),
  deleteCategory: (id: string): Promise<DeleteCategoryResult> =>
    request(`/categories/${id}`, { method: "DELETE" }),
  getGroups: (): Promise<CategoryGroup[]> => request("/groups"),
  createGroup: (data: CreateCategoryGroupRequest): Promise<CategoryGroup> =>
    request("/groups", { method: "POST", body: JSON.stringify(data) }),
  updateGroup: (
    id: string,
    data: UpdateCategoryGroupRequest,
  ): Promise<CategoryGroup> =>
    request(`/groups/${id}`, { method: "PUT", body: JSON.stringify(data) }),
  deleteGroup: (id: string): Promise<null> =>
    request(`/groups/${id}`, { method: "DELETE" }),

  // Admin: global groups & categories shared by every user
  createGlobalGroup: (data: CreateCategoryGroupRequest): Promise<CategoryGroup> =>
    request("/admin/groups", { method: "POST", body: JSON.stringify(data) }),
  createGlobalCategory: (data: CreateCategoryRequest): Promise<Category> =>
    request("/admin/categories", { method: "POST", body: JSON.stringify(data) }),
  updateGlobalCategory: (
    id: string,
    data: UpdateCategoryRequest,
  ): Promise<Category> =>
    request(`/admin/categories/${id}`, {
      method: "PUT",
      body: JSON.stringify(data),
    }),
  deleteGlobalCategory: (id: string): Promise<DeleteCategoryResult> =>
    request(`/admin/categories/${id}`, { method: "DELETE" }),

  // Transactions
  getTransactions: (
    params: QueryParams = {},
    options: RequestOptions = {},
  ): Promise<TransactionsResponse> => {
    const qs = buildQuery(params);
    return request(`/transactions?${qs}`, options);
  },
  createTransaction: (data: CreateTransactionRequest): Promise<Transaction> =>
    request("/transactions", { method: "POST", body: JSON.stringify(data) }),
  updateTransaction: (
    id: string,
    data: UpdateTransactionRequest,
  ): Promise<Transaction> =>
    request(`/transactions/${id}`, {
      method: "PATCH",
      body: JSON.stringify(data),
    }),
  deleteTransaction: (id: string): Promise<null> =>
    request(`/transactions/${id}`, { method: "DELETE" }),
  importTransactions: (
    data: ImportTransactionsRequest,
  ): Promise<ImportResult> =>
    request("/transactions/import", {
      method: "POST",
      body: JSON.stringify(data),
    }),
  validateTransactions: (
    data: ValidateTransactionsRequest,
  ): Promise<ValidateTransactionsResponse> =>
    request("/transactions/validate", {
      method: "POST",
      body: JSON.stringify(data),
    }),
  bulkCategorize: (data: BulkCategorizeRequest): Promise<null> =>
    request("/transactions/bulk-categorize", {
      method: "POST",
      body: JSON.stringify(data),
    }),
  bulkUpdatePayee: (data: BulkUpdatePayeeRequest): Promise<null> =>
    request("/transactions/bulk-payee", {
      method: "POST",
      body: JSON.stringify(data),
    }),
  bulkUpdateBillingCycle: (data: BulkBillingCycleRequest): Promise<null> =>
    request("/transactions/bulk-billing-cycle", {
      method: "POST",
      body: JSON.stringify(data),
    }),
  bulkDeleteTransactions: (
    data: BulkDeleteTransactionsRequest,
  ): Promise<null> =>
    request("/transactions/bulk-delete", {
      method: "POST",
      body: JSON.stringify(data),
    }),

  // Statement parsing (PDF) — forwarded by the backend to the parser service
  parseStatement: (formData: FormData): Promise<StatementParseResult> =>
    requestMultipart("/statements/parse", formData),
  getStatementExtractors: (): Promise<{ extractors: StatementExtractor[] }> =>
    request("/statements/extractors"),

  // Paperless-ngx integration (per-user settings + manual pull)
  getPaperlessSettings: (): Promise<UserSettings> =>
    request("/paperless/settings"),
  updatePaperlessSettings: (data: UpdateUserSettingsRequest): Promise<null> =>
    request("/paperless/settings", {
      method: "PUT",
      body: JSON.stringify(data),
    }),

  // Generic per-user settings (the same /paperless/settings endpoint also
  // carries the transactions page-size preference).
  getUserSettings: (): Promise<UserSettings> => request("/paperless/settings"),
  updateUserSettings: (data: UpdateUserSettingsRequest): Promise<null> =>
    request("/paperless/settings", {
      method: "PUT",
      body: JSON.stringify(data),
    }),
  getPaperlessDocuments: (
    params?: PaperlessDocumentsParams,
  ): Promise<PaperlessDocumentsResponse> => {
    const qs = new URLSearchParams();
    if (params?.search) qs.set("search", params.search);
    if (params?.page && params.page > 1) qs.set("page", String(params.page));
    if (params?.pageSize) qs.set("pageSize", String(params.pageSize));
    for (const key of [
      "correspondentInc",
      "correspondentExc",
      "documentTypeInc",
      "documentTypeExc",
      "tagInc",
      "tagExc",
    ] as const) {
      (params?.[key] || []).forEach((value) => qs.append(key, value));
    }
    const query = qs.toString();
    return request(query ? `/paperless/documents?${query}` : "/paperless/documents");
  },
  importPaperlessDocument: (
    data: PaperlessImportRequest,
  ): Promise<PaperlessImportResult> =>
    request("/paperless/import", {
      method: "POST",
      body: JSON.stringify(data),
    }),
  getPaperlessDocumentFile: async (id: number): Promise<Blob> => {
    const token = getToken();
    const res = await fetch(`${API_BASE}/paperless/documents/${id}/file`, {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    });
    if (!res.ok) {
      const err = new Error("Failed to load document file") as ApiError;
      err.status = res.status;
      throw err;
    }
    return res.blob();
  },

  // Rules
  getRules: (): Promise<Rule[]> => request("/rules"),
  createRule: (data: CreateRuleRequest): Promise<Rule> =>
    request("/rules", { method: "POST", body: JSON.stringify(data) }),
  updateRule: (id: string, data: UpdateRuleRequest): Promise<Rule> =>
    request(`/rules/${id}`, { method: "PUT", body: JSON.stringify(data) }),
  deleteRule: (id: string): Promise<null> =>
    request(`/rules/${id}`, { method: "DELETE" }),
  applyRules: (): Promise<ApplyRulesResult> =>
    request("/rules/apply", { method: "POST" }),

  // Payees
  getPayees: (): Promise<Payee[]> => request("/payees"),
  createPayee: (data: CreatePayeeRequest): Promise<Payee> =>
    request("/payees", { method: "POST", body: JSON.stringify(data) }),
  updatePayee: (id: string, data: UpdatePayeeRequest): Promise<Payee> =>
    request(`/payees/${id}`, { method: "PUT", body: JSON.stringify(data) }),
  deletePayee: (id: string): Promise<null> =>
    request(`/payees/${id}`, { method: "DELETE" }),

  // Links
  getLinks: (params: QueryParams = {}): Promise<Link[]> => {
    const qs = buildQuery(params);
    return request(`/links${qs ? `?${qs}` : ""}`);
  },
  createLink: (data: CreateLinkRequest): Promise<Link> =>
    request("/links", { method: "POST", body: JSON.stringify(data) }),
  deleteLink: (id: string): Promise<null> =>
    request(`/links/${id}`, { method: "DELETE" }),
  bulkDeleteLinks: (data: BulkDeleteLinksRequest): Promise<null> =>
    request("/links/bulk-delete", {
      method: "POST",
      body: JSON.stringify(data),
    }),

  // Dashboard
  getDashboardSummary: (
    params: QueryParams = {},
  ): Promise<DashboardSummary> => {
    const qs = buildQuery(params);
    return request(`/dashboard/summary?${qs}`);
  },
};

export default api;
