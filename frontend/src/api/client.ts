import type {
  Account,
  AccountType,
  ApplyRulesResult,
  AuthResponse,
  BackupImportResult,
  BillingCycle,
  BulkCategorizeRequest,
  BulkDeleteLinksRequest,
  BulkDeleteTransactionsRequest,
  BulkBillingCycleRequest,
  BulkLoanRequest,
  BulkUpdatePayeeRequest,
  Category,
  CategoryGroup,
  CreateAccountRequest,
  CreateAccountTypeRequest,
  CreateCategoryGroupRequest,
  CreateCategoryRequest,
  CreateLinkRequest,
  CreatePayeeRequest,
  CreateRecurringSeriesRequest,
  CreateRecurringSeriesTermRequest,
  CreateRuleRequest,
  CreateTransactionRequest,
  DashboardSummary,
  DeleteCategoryResult,
  ImportResult,
  ImportTransactionsRequest,
  Link,
  LoginRequest,
  MoneyFlowGraph,
  PaperlessDocumentsResponse,
  PaperlessDocumentsParams,
  PaperlessImportRequest,
  PaperlessImportResult,
  Payee,
  QueryParams,
  RegisterRequest,
  RecurringAttachRequest,
  RecurringDetachRequest,
  RecurringForecastItem,
  RecurringSeries,
  RecurringSeriesTerm,
  RecurringSuggestion,
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
  UpdateRecurringSeriesRequest,
  UpdateRecurringSeriesTermRequest,
  UpdateRuleRequest,
  UpdateTransactionRequest,
  UpdateUserSettingsRequest,
  User,
  UserSettings,
  ValidateTransactionsRequest,
  ValidateTransactionsResponse,
} from "../types";

const API_BASE = import.meta.env.VITE_API_URL || "/api/v1";

const USER_KEY = "fintrak_user";

const REQUEST_TIMEOUT = 15000;

// The session JWT is held in an httpOnly cookie the backend sets on login, so
// it is intentionally unreadable here. Only the non-sensitive user object is
// cached to avoid a flash of the login screen on reload; it is re-verified via
// api.me() on mount.
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
  // timeout overrides REQUEST_TIMEOUT for long-running calls (e.g. a full
  // backup restore of a large history).
  timeout?: number;
}

function buildQuery(params: QueryParams): string {
  return new URLSearchParams(
    Object.entries(params).map(([k, v]) => [k, String(v)]),
  ).toString();
}

// isAuthEndpoint reports whether a 401 should be surfaced rather than being
// treated as an expired access token. /auth/refresh must never trigger a
// refresh attempt, or an invalid refresh token would loop forever.
function isAuthEndpoint(url: string): boolean {
  return (
    url === "/auth/login" || url === "/auth/register" || url === "/auth/refresh"
  );
}

// isSessionCheck reports whether the request is the on-mount /auth/me probe.
// A 401 there means the whole session is gone; AuthContext shows the login
// screen, so we avoid a hard redirect (which would fight React Router).
function isSessionCheck(url: string): boolean {
  return url === "/auth/me";
}

function redirectToLogin(): void {
  storeUser(null);
  if (!window.location.pathname.startsWith("/login")) {
    window.location.href = "/login";
  }
}

// refreshSession trades the long-lived refresh cookie for a fresh access token.
// Concurrent 401s share one in-flight call so a burst of failing requests only
// hits /auth/refresh once.
let refreshPromise: Promise<boolean> | null = null;

function refreshSession(): Promise<boolean> {
  if (!refreshPromise) {
    refreshPromise = fetch(`${API_BASE}/auth/refresh`, {
      method: "POST",
      credentials: "include",
    })
      .then((res) => res.ok)
      .catch(() => false)
      .finally(() => {
        refreshPromise = null;
      });
  }
  return refreshPromise;
}

// fetchWithTimeout runs a single fetch attempt with the caller's abort signal
// combined with a request timeout.
async function fetchWithTimeout(
  url: string,
  init: RequestInit,
  timeout: number,
): Promise<Response> {
  const controller = new AbortController();
  const externalSignal = init.signal ?? undefined;
  let timedOut = false;

  const abort = () => controller.abort();
  if (externalSignal) {
    if (externalSignal.aborted) controller.abort();
    else externalSignal.addEventListener("abort", abort, { once: true });
  }
  const timer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, timeout);

  try {
    return await fetch(`${API_BASE}${url}`, {
      ...init,
      signal: controller.signal,
      credentials: "include",
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
}

// sendWithAuthRetry sends a request and, if it comes back 401 because the
// short-lived access token expired, refreshes the session and retries once.
async function sendWithAuthRetry(
  url: string,
  init: RequestInit,
  timeout: number,
): Promise<Response> {
  let res = await fetchWithTimeout(url, init, timeout);
  if (res.status === 401 && !isAuthEndpoint(url) && (await refreshSession())) {
    res = await fetchWithTimeout(url, init, timeout);
  }
  return res;
}

async function request<T>(
  url: string,
  options: RequestOptions = {},
): Promise<T> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...options.headers,
  };

  const res = await sendWithAuthRetry(
    url,
    {
      method: options.method,
      body: options.body,
      signal: options.signal,
      headers,
    },
    options.timeout ?? REQUEST_TIMEOUT,
  );

  if (res.status === 401 && !isAuthEndpoint(url) && !isSessionCheck(url)) {
    redirectToLogin();
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

// requestMultipart POSTs a FormData payload (multipart/form-data) without
// forcing a JSON content type, which the browser must set itself (including the
// boundary). Used for statement PDF uploads. Auth rides on the session cookie.
async function requestMultipart<T>(
  url: string,
  formData: FormData,
): Promise<T> {
  const res = await sendWithAuthRetry(
    url,
    { method: "POST", body: formData },
    REQUEST_TIMEOUT,
  );

  if (res.status === 401 && !isAuthEndpoint(url)) {
    redirectToLogin();
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

// downloadFile fetches a binary/text attachment and saves it using the
// server-provided filename (falling back to fallbackName).
export async function downloadFile(
  path: string,
  fallbackName = "export",
): Promise<void> {
  const res = await sendWithAuthRetry(path, { method: "GET" }, REQUEST_TIMEOUT);
  if (res.status === 401) {
    redirectToLogin();
  }
  if (!res.ok) {
    throw new Error("Export failed");
  }

  const blob = await res.blob();
  const disposition = res.headers.get("Content-Disposition") || "";
  const match = disposition.match(/filename="?([^"]+)"?/);
  const filename = match ? match[1] : fallbackName;

  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

export async function downloadCSV(path: string): Promise<void> {
  return downloadFile(path, "export.csv");
}

const api = {
  // Auth
  register: (data: RegisterRequest): Promise<AuthResponse> =>
    request("/auth/register", { method: "POST", body: JSON.stringify(data) }),
  login: (data: LoginRequest): Promise<AuthResponse> =>
    request("/auth/login", { method: "POST", body: JSON.stringify(data) }),
  logout: (): Promise<null> =>
    request("/auth/logout", { method: "POST" }),
  me: (): Promise<User> => request("/auth/me"),

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
  bulkLoan: (data: BulkLoanRequest): Promise<null> =>
    request("/transactions/bulk-loan", {
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
    options: RequestOptions = {},
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
    return request(
      query ? `/paperless/documents?${query}` : "/paperless/documents",
      options,
    );
  },
  importPaperlessDocument: (
    data: PaperlessImportRequest,
  ): Promise<PaperlessImportResult> =>
    request("/paperless/import", {
      method: "POST",
      body: JSON.stringify(data),
    }),
  getPaperlessDocumentFile: async (id: number): Promise<Blob> => {
    const res = await sendWithAuthRetry(
      `/paperless/documents/${id}/file`,
      { method: "GET" },
      REQUEST_TIMEOUT,
    );
    if (res.status === 401) {
      redirectToLogin();
    }
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

  // Recurring series & subscriptions (forecast + manual linking only)
  getRecurringSeries: (): Promise<{ data: RecurringSeries[] }> =>
    request("/recurring"),
  createRecurringSeries: (
    data: CreateRecurringSeriesRequest,
  ): Promise<RecurringSeries> =>
    request("/recurring", { method: "POST", body: JSON.stringify(data) }),
  updateRecurringSeries: (
    id: string,
    data: UpdateRecurringSeriesRequest,
  ): Promise<RecurringSeries> =>
    request(`/recurring/${id}`, { method: "PUT", body: JSON.stringify(data) }),
  deleteRecurringSeries: (id: string): Promise<null> =>
    request(`/recurring/${id}`, { method: "DELETE" }),
  getRecurringForecast: (
    id: string,
    count = 12,
  ): Promise<{ data: RecurringForecastItem[] }> =>
    request(`/recurring/${id}/forecast?count=${count}`),
  getRecurringSuggestions: (
    id: string,
    limit = 100,
  ): Promise<{ data: RecurringSuggestion[] }> =>
    request(`/recurring/${id}/suggestions?limit=${limit}`),
  getRecurringTransactions: (
    id: string,
  ): Promise<{ data: Transaction[] }> =>
    request(`/recurring/${id}/transactions`),
  getRecurringTerms: (
    id: string,
  ): Promise<{ data: RecurringSeriesTerm[] }> =>
    request(`/recurring/${id}/terms`),
  createRecurringTerm: (
    id: string,
    data: CreateRecurringSeriesTermRequest,
  ): Promise<RecurringSeriesTerm> =>
    request(`/recurring/${id}/terms`, {
      method: "PUT",
      body: JSON.stringify(data),
    }),
  updateRecurringTerm: (
    id: string,
    termId: string,
    data: UpdateRecurringSeriesTermRequest,
  ): Promise<RecurringSeriesTerm> =>
    request(`/recurring/${id}/terms/${termId}`, {
      method: "PUT",
      body: JSON.stringify(data),
    }),
  deleteRecurringTerm: (id: string, termId: string): Promise<null> =>
    request(`/recurring/${id}/terms/${termId}`, { method: "DELETE" }),
  attachRecurring: (
    data: RecurringAttachRequest,
  ): Promise<{ attached: number }> =>
    request("/recurring/attach", { method: "POST", body: JSON.stringify(data) }),
  detachRecurring: (
    data: RecurringDetachRequest,
  ): Promise<{ detached: number }> =>
    request("/recurring/detach", { method: "POST", body: JSON.stringify(data) }),

  // User-level backup & restore (whole account, not per-account)
  exportUserData: (): Promise<void> =>
    downloadFile("/export", "fintrak-backup.json"),
  importUserData: (data: unknown): Promise<BackupImportResult> =>
    request("/import", {
      method: "POST",
      body: JSON.stringify(data),
      timeout: 120000,
    }),

  // Dashboard
  getDashboardSummary: (
    params: QueryParams = {},
  ): Promise<DashboardSummary> => {
    const qs = buildQuery(params);
    return request(`/dashboard/summary?${qs}`);
  },
  // Money-flow Sankey (sources -> accounts -> categories -> payees)
  getMoneyFlow: (params: QueryParams = {}): Promise<MoneyFlowGraph> => {
    const qs = buildQuery(params);
    return request(`/dashboard/money-flow${qs ? `?${qs}` : ""}`);
  },
};

export default api;
