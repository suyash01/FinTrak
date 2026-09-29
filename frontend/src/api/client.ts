import type {
  Account,
  AccountType,
  AdminCatalog,
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
  BulkUpdateTagsRequest,
  CashFlowCalendar,
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
  LinkCycleReport,
  LoanDisbursementRequest,
  LoanPayoff,
  LoanScheduleDetail,
  LoanScheduleRequest,
  LoanTransferRequest,
  LoanTransferResult,
  LoginRequest,
  MoneyFlowGraph,
  MoneyFlowTimeline,
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
  RenameTagRequest,
  Rule,
  RulePreview,
  StatementExtractor,
  StatementParseResult,
  TagCount,
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
import { ApiError, NetworkError, isNetworkError } from "./errors";
import { diffAgainstBase, type FieldPatch } from "./merge";
import { clearCached, isCacheablePath, readCached, writeCached } from "./offlineCache";
import { enqueueCreate, enqueueEdit } from "./outbox";
import {
  projectAccount,
  projectAccountType,
  projectCategory,
  projectGroup,
  projectSettings,
  projectTransaction,
} from "./projections";
import { setServedFromCache } from "./offlineStatus";

const API_BASE = import.meta.env.VITE_API_URL || "/api/v1";

// STORED_USER_KEY is the origin-wide signed-in identity. Everything the offline
// layer namespaces — the read cache, the outbox, the identity a queued create is
// attributed to — is derived from it, so it is exported for the modules that
// have to notice it being replaced under them (a second tab signing in as
// someone else writes it without this tab's knowledge).
export const STORED_USER_KEY = "fintrak_user";

const REQUEST_TIMEOUT = 15000;

// The session JWT is held in an httpOnly cookie the backend sets on login, so
// it is intentionally unreadable here. Only the non-sensitive user object is
// cached to avoid a flash of the login screen on reload; it is re-verified via
// api.me() on mount.
export function getStoredUser(): User | null {
  try {
    return JSON.parse(localStorage.getItem(STORED_USER_KEY) || "null");
  } catch {
    return null;
  }
}

export function storeUser(user: User | null): void {
  if (user) {
    localStorage.setItem(STORED_USER_KEY, JSON.stringify(user));
  } else {
    localStorage.removeItem(STORED_USER_KEY);
  }
}

// offlineUserId is the identity that namespaces the offline cache and the
// outbox. The cached user object is the only identity the API layer has;
// without it a cached read cannot be attributed and a queued write cannot be
// owned, so both are refused rather than guessed.
function offlineUserId(): string | null {
  return getStoredUser()?.id ?? null;
}

// stillOwner reports whether the identity captured when a request was issued is
// still the signed-in one. A response that lands under a different identity
// belongs to a session this browser no longer serves, so it must be neither
// written into the new user's namespace nor answered from the old one's.
function stillOwner(owner: string | null): owner is string {
  return owner !== null && owner === offlineUserId();
}

// readOffline answers a read from the last payload that came back for the same
// URL under the identity that issued the request, when the network failed.
function readOffline<T>(owner: string | null, url: string): T | null {
  if (!stillOwner(owner) || !isCacheablePath(url)) return null;
  const cached = readCached<T>(owner, url);
  if (cached !== null) setServedFromCache(true);
  return cached;
}

// isQueriedLedgerRead reports whether this GET is a transaction read carrying a
// typed query (q=), rather than the plain filtered or unfiltered list.
//
// Such a read is not cached. The cache is keyed on the full URL, so every
// distinct expression would take one of the 40 slots in offlineCache.ts and
// compete for the 2MB total cap: a user trying a few queries would evict the
// default unfiltered view, which is the one actually worth having offline. This
// is not a correctness hazard — a cached body is only ever served for the exact
// URL that produced it — it is a budget one.
function isQueriedLedgerRead(url: string): boolean {
  const [path = "", search = ""] = url.split("?");
  if (path !== "/transactions") return false;
  return new URLSearchParams(search).has("q");
}

// writeOffline records a successful read for the next offline load, attributed
// to the session that asked for it.
function writeOffline(
  owner: string | null,
  url: string,
  data: unknown,
): void {
  if (stillOwner(owner)) writeCached(owner, url, data);
}

// newClientKey identifies one create attempt. It survives the 401 refresh
// replay and every outbox retry, which is what lets the server recognise a
// repeat instead of inserting a second row.
function newClientKey(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  // crypto.randomUUID requires a secure context; a self-hosted instance reached
  // over a LAN address without TLS is not one.
  return `ck-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 12)}`;
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
  // live answers from the server or not at all. A read the offline merge takes
  // as "theirs" is one whose answer decides what gets written, and the cache
  // holds this browser's own last belief of that row — a merge answered from it
  // can only agree with itself, and would overwrite whatever changed since.
  live?: boolean;
}

// ReadOptions is what a GET takes. Narrower than RequestOptions on purpose:
// method, body and headers are honoured by request(), so a read typed as
// RequestOptions would accept api.getAccounts({ method: "DELETE" }) and quietly
// stop being a read. getTransactions keeps the wider type it already had, since
// its second argument is a general RequestOptions.
type ReadOptions = Pick<RequestOptions, "live" | "signal" | "timeout">;

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
  // The cached reads are the departing user's ledger, and they are namespaced
  // by an id that is about to be forgotten: once the stored user is gone
  // nothing can reach them again, so a session ending here has to drop them the
  // way an explicit logout does. Queued creates are kept — they are unsent
  // work, not a cache.
  const departing = getStoredUser();
  if (departing) clearCached(departing.id);
  storeUser(null);
  if (!window.location.pathname.startsWith("/login")) {
    window.location.href = "/login";
  }
}

// refreshSession trades the long-lived refresh cookie for a fresh access token.
// Concurrent 401s share one in-flight call so a burst of failing requests only
// hits /auth/refresh once.
//
// Rejection and unreachability are not the same answer: only a *rejected*
// refresh (401/403 — the refresh cookie is gone or invalid) ends the session. A
// transport failure or a 5xx means the server could not be asked, so it throws a
// NetworkError: the caller keeps the session and takes the offline path rather
// than signing the user out over a blip. The call runs through fetchWithTimeout
// like every other request, so a refresh that hangs cannot stall the queue.
let refreshPromise: Promise<boolean> | null = null;

function refreshSession(): Promise<boolean> {
  if (!refreshPromise) {
    refreshPromise = fetchWithTimeout(
      "/auth/refresh",
      { method: "POST" },
      REQUEST_TIMEOUT,
    )
      .then((res) => {
        if (res.ok) return true;
        if (res.status === 401 || res.status === 403) return false;
        throw new NetworkError("Could not refresh the session");
      })
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
      // A timeout is a transport failure, not a caller cancellation: the
      // request may or may not have reached the server, which is exactly what
      // NetworkError means. Anything that keys on it — the cached read, the
      // outbox enqueue, AuthContext keeping the cached session — must see it as
      // one; a plain Error would silently skip all three.
      if (timedOut) throw new NetworkError("Request timed out");
      throw err;
    }
    throw new NetworkError();
  } finally {
    clearTimeout(timer);
    if (externalSignal) externalSignal.removeEventListener("abort", abort);
  }
}

// sendWithAuthRetry sends a request and, if it comes back 401 because the
// short-lived access token expired, refreshes the session and retries once. A
// refresh that could not be asked throws (NetworkError) instead of being treated
// as a refusal, so the caller decides between signing out and going offline.
async function sendWithAuthRetry(
  url: string,
  init: RequestInit,
  timeout: number,
): Promise<Response> {
  let res = await fetchWithTimeout(url, init, timeout);
  // Any response at all means the server was reachable, so the UI stops
  // claiming it is showing saved data.
  setServedFromCache(false);
  if (res.status === 401 && !isAuthEndpoint(url)) {
    if (await refreshSession()) {
      res = await fetchWithTimeout(url, init, timeout);
    }
  }
  return res;
}

async function request<T>(
  url: string,
  options: RequestOptions = {},
): Promise<T> {
  const method = options.method ?? "GET";
  // The offline namespace belongs to the session that issues the request, not to
  // whoever is signed in when the response lands: this browser can swap users
  // while a request is in flight (another tab, the login form), and reading the
  // identity at completion writes one user's ledger into another user's
  // namespace and serves the previous user's cache to the new session.
  const owner = offlineUserId();
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...options.headers,
  };

  let res: Response;
  try {
    res = await sendWithAuthRetry(
      url,
      {
        method: options.method,
        body: options.body,
        signal: options.signal,
        headers,
      },
      options.timeout ?? REQUEST_TIMEOUT,
    );
  } catch (err) {
    // A read that never reached the server is answered from the offline cache,
    // so the shell shows the state the user last saw instead of an error page.
    // A live read is the exception: it exists precisely because its answer must
    // be the server's, so a transport failure propagates and the caller decides
    // (outbox's flush stops and keeps the queue rather than merging against a
    // stale row).
    if (method === "GET" && isNetworkError(err) && !options.live) {
      const cached = readOffline<T>(owner, url);
      if (cached !== null) return cached;
    }
    throw err;
  }

  if (res.status === 401 && !isAuthEndpoint(url) && !isSessionCheck(url)) {
    redirectToLogin();
  }

  if (!res.ok) {
    const error = await res.json().catch(() => ({ error: res.statusText }));
    throw new ApiError(extractErrorMessage(error, "Request failed"), res.status);
  }

  const text = await res.text();
  const data = text ? (JSON.parse(text) as T) : (null as T);
  if (method === "GET" && !isQueriedLedgerRead(url)) writeOffline(owner, url, data);
  return data;
}

// requestMultipart POSTs a FormData payload (multipart/form-data) without
// forcing a JSON content type, which the browser must set itself (including the
// boundary). Used for statement PDF uploads. Auth rides on the session cookie.
// Parsing an upload proxies to an extractor that can spend a minute on a large
// PDF, so callers may pass a longer timeout than REQUEST_TIMEOUT.
async function requestMultipart<T>(
  url: string,
  formData: FormData,
  timeout: number = REQUEST_TIMEOUT,
): Promise<T> {
  const res = await sendWithAuthRetry(
    url,
    { method: "POST", body: formData },
    timeout,
  );

  if (res.status === 401 && !isAuthEndpoint(url)) {
    redirectToLogin();
  }

  if (!res.ok) {
    const error = await res.json().catch(() => ({ error: res.statusText }));
    throw new ApiError(extractErrorMessage(error, "Request failed"), res.status);
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

// CreateTransactionResult is what a manual create resolves to: the server's id
// for a transaction that was written, or `queued` for one the offline outbox
// holds until the network returns.
export interface CreateTransactionResult {
  id: string | null;
  queued: boolean;
}

// UpdateTransactionResult is what an update resolves to, and `queued` is the only
// thing in it a caller can learn: whether the server holds the write or the
// offline outbox is holding it. The id is the row the caller addressed — PATCH
// /transactions/{id} answers a message, not a row — so it is the same either way,
// and a queued edit is named by the row the flush will merge it into.
export interface UpdateTransactionResult {
  id: string;
  queued: boolean;
}

// QueuedEdit is what a write that never became a request resolves to in place of
// a row: `queued: true` when the offline outbox is holding the edit, `queued:
// false` when there was nothing to send. Only a caller that passed a base can see
// one, and that is a fact about the write rather than about the type — see
// EditWrite.
export interface QueuedEdit {
  queued: boolean;
}

// EditOptions is the offline half of a family's write. `base` is the row the
// caller's form opened with, and it is what makes the write a patch: only the
// fields the user changed go on the wire, so a concurrent change to a field the
// form never opened cannot be reverted by this save, and the same patch is what
// the queue records, so an edit that reached the server and one that did not
// cannot disagree about what the user changed.
//
// The payload stays the caller's own, reduced field by field against the projected
// base, so it names the fields it means to set rather than the whole row: a
// payload carrying a key the endpoint does not take (an `id`, a computed
// `balance`) puts that key in the diff too, where the server ignores it and the
// merge carries a change nobody made. Every field these six request types can
// carry is a mergeable one, so projecting the user's side would buy nothing and
// cost the clears — a null it drops is a clear the user made.
//
// `queue: false` is what a caller that is *sending* an already-queued edit
// passes — the outbox flush, which must not put a second copy of an entry back in
// the queue when the request does not reach the server.
export interface EditOptions<Row> {
  base?: Row;
  queue?: boolean;
}

// EditWrite is the call shape of a family whose edit can be queued, and the two
// signatures are one implementation read from two angles rather than two
// behaviours: a write with no base has no patch to record, so a request that
// never reached the server is raised as a failure (updateTransaction refuses to
// queue on exactly these grounds) and the caller always gets back the server's
// row. Pass a base and the result may be a QueuedEdit instead, which the caller
// has to narrow before it reads a row off it — that is the cost of a write that
// can be held, and it is paid only by the callers that can be held.
export interface EditWrite<Row, Data> {
  (id: string, data: Data): Promise<Row>;
  (id: string, data: Data, options: EditOptions<Row>): Promise<Row | QueuedEdit>;
}

// SettingsWrite is EditWrite for the one row this API has that is addressed by
// no id: there is a single settings row per user, so the endpoint takes the body
// alone.
export interface SettingsWrite<Row, Data> {
  (data: Data): Promise<null>;
  (data: Data, options: EditOptions<Row>): Promise<null | QueuedEdit>;
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
  getAccounts: (options: ReadOptions = {}): Promise<Account[]> =>
    request("/accounts", options),
  createAccount: (data: CreateAccountRequest): Promise<Account> =>
    request("/accounts", { method: "POST", body: JSON.stringify(data) }),
  updateAccount: (async (
    id: string,
    data: UpdateAccountRequest,
    options: EditOptions<Account> = {},
  ) => {
    // updateTransaction's rules, one for one: project the base, take the user's
    // side as given, put only the diff on the wire, and record that same diff
    // when the request never reached the server. They are spelled out there, once
    // — what follows is what is particular to an account.
    //
    // The payload is not projected: billingDay is nullable, and a null there is
    // the user clearing it, which account.go:286 does perform (it binds
    // billing_day whenever the field is set, null included). A projection of the
    // user's side would have dropped that clear, and a form that always sends
    // `billingDay: null` for an account with none would have queued a change to a
    // field nobody touched.
    const base = options.base ? projectAccount(options.base) : null;
    const edit = base
      ? { base, patch: diffAgainstBase(base, data as unknown as FieldPatch) }
      : null;
    // An empty diff is not a write: the row already says what the form says, so
    // there is nothing to send and nothing to queue.
    if (edit && Object.keys(edit.patch).length === 0) {
      return { queued: false };
    }
    // The queue entry belongs to the session that issued the edit.
    const owner = offlineUserId();
    return request<Account>(`/accounts/${id}`, {
      method: "PUT",
      body: JSON.stringify(edit ? edit.patch : data),
    }).then(
      (row) => row,
      (err: unknown) => {
        // Only a request that never reached the server may be replayed later; a
        // rejected one is the server refusing this payload, and the user has to
        // see that.
        if (options.queue === false || !isNetworkError(err)) throw err;
        if (!stillOwner(owner)) throw err;
        // No base is no queue: a patch with nothing to be a patch *against* would
        // merge as though the user had changed every field in it.
        if (!edit) throw err;
        enqueueEdit(owner, "account.put", id, edit.base, edit.patch, edit.base);
        return { queued: true };
      },
    );
  }) as EditWrite<Account, UpdateAccountRequest>,
  deleteAccount: (id: string): Promise<{ message?: string; transactionsDeleted?: number }> =>
      request(`/accounts/${id}`, { method: "DELETE" }),
  getBillingCycles: (accountId: string): Promise<{ data: BillingCycle[] }> =>
    request(`/accounts/${accountId}/billing-cycles`),

  // Account Types
  getAccountTypes: (options: ReadOptions = {}): Promise<AccountType[]> =>
    request("/account-types", options),
  createAccountType: (data: CreateAccountTypeRequest): Promise<AccountType> =>
    request("/account-types", { method: "POST", body: JSON.stringify(data) }),
  updateAccountType: (async (
    id: string,
    data: UpdateAccountTypeRequest,
    options: EditOptions<AccountType> = {},
  ) => {
    // Name and positiveTxnType, both non-null strings, so the diff is a set of
    // values and never a clear. An empty string is still refused at the queue
    // (see registry.ts's applyOp): the user emptying this form is a clear
    // account_type.go:136 cannot perform, and reporting it as saved would be
    // reporting a write that never happened.
    const base = options.base ? projectAccountType(options.base) : null;
    const edit = base
      ? { base, patch: diffAgainstBase(base, data as unknown as FieldPatch) }
      : null;
    if (edit && Object.keys(edit.patch).length === 0) {
      return { queued: false };
    }
    const owner = offlineUserId();
    return request<AccountType>(`/account-types/${id}`, {
      method: "PUT",
      body: JSON.stringify(edit ? edit.patch : data),
    }).then(
      (row) => row,
      (err: unknown) => {
        if (options.queue === false || !isNetworkError(err)) throw err;
        if (!stillOwner(owner)) throw err;
        if (!edit) throw err;
        enqueueEdit(owner, "accountType.put", id, edit.base, edit.patch, edit.base);
        return { queued: true };
      },
    );
  }) as EditWrite<AccountType, UpdateAccountTypeRequest>,
  deleteAccountType: (id: string): Promise<null> =>
    request(`/account-types/${id}`, { method: "DELETE" }),

  // Categories & groups
  getCategories: (options: ReadOptions = {}): Promise<Category[]> =>
    request("/categories", options),
  createCategory: (data: CreateCategoryRequest): Promise<Category> =>
    request("/categories", { method: "POST", body: JSON.stringify(data) }),
  updateCategory: (async (
    id: string,
    data: UpdateCategoryRequest,
    options: EditOptions<Category> = {},
  ) => {
    // A user's own category: name, icon, colour and group, all non-null strings,
    // so the diff is a set of values and never a clear. category.go:133 reads an
    // empty string as "not provided", which is what the queue refuses to record
    // as one (see registry.ts's applyOp).
    const base = options.base ? projectCategory(options.base) : null;
    const edit = base
      ? { base, patch: diffAgainstBase(base, data as unknown as FieldPatch) }
      : null;
    if (edit && Object.keys(edit.patch).length === 0) {
      return { queued: false };
    }
    const owner = offlineUserId();
    return request<Category>(`/categories/${id}`, {
      method: "PUT",
      body: JSON.stringify(edit ? edit.patch : data),
    }).then(
      (row) => row,
      (err: unknown) => {
        if (options.queue === false || !isNetworkError(err)) throw err;
        if (!stillOwner(owner)) throw err;
        if (!edit) throw err;
        enqueueEdit(owner, "category.put", id, edit.base, edit.patch, edit.base);
        return { queued: true };
      },
    );
  }) as EditWrite<Category, UpdateCategoryRequest>,
  deleteCategory: (id: string): Promise<DeleteCategoryResult> =>
    request(`/categories/${id}`, { method: "DELETE" }),
  getGroups: (options: ReadOptions = {}): Promise<CategoryGroup[]> =>
    request("/groups", options),
  createGroup: (data: CreateCategoryGroupRequest): Promise<CategoryGroup> =>
    request("/groups", { method: "POST", body: JSON.stringify(data) }),
  updateGroup: (async (
    id: string,
    data: UpdateCategoryGroupRequest,
    options: EditOptions<CategoryGroup> = {},
  ) => {
    // Name, icon and colour, and nothing nullable: the diff is the set of them
    // that changed. isBase, isGlobal and sortOrder are not fields this endpoint
    // writes, so they are not in the projection either — a base carrying one
    // would read as a change the user made.
    const base = options.base ? projectGroup(options.base) : null;
    const edit = base
      ? { base, patch: diffAgainstBase(base, data as unknown as FieldPatch) }
      : null;
    if (edit && Object.keys(edit.patch).length === 0) {
      return { queued: false };
    }
    const owner = offlineUserId();
    return request<CategoryGroup>(`/groups/${id}`, {
      method: "PUT",
      body: JSON.stringify(edit ? edit.patch : data),
    }).then(
      (row) => row,
      (err: unknown) => {
        if (options.queue === false || !isNetworkError(err)) throw err;
        if (!stillOwner(owner)) throw err;
        if (!edit) throw err;
        enqueueEdit(owner, "group.put", id, edit.base, edit.patch, edit.base);
        return { queued: true };
      },
    );
  }) as EditWrite<CategoryGroup, UpdateCategoryGroupRequest>,
  deleteGroup: (id: string): Promise<null> =>
    request(`/groups/${id}`, { method: "DELETE" }),

  // Admin: global groups & categories shared by every user
  getAdminCatalog: (options: ReadOptions = {}): Promise<AdminCatalog> =>
    request("/admin/catalog", options),
  createGlobalGroup: (data: CreateCategoryGroupRequest): Promise<CategoryGroup> =>
    request("/admin/groups", { method: "POST", body: JSON.stringify(data) }),
  createGlobalCategory: (data: CreateCategoryRequest): Promise<Category> =>
    request("/admin/categories", { method: "POST", body: JSON.stringify(data) }),
  updateGlobalCategory: (async (
    id: string,
    data: UpdateCategoryRequest,
    options: EditOptions<Category> = {},
  ) => {
    // The shared catalog, not the user's copy of it: the same fields and the same
    // partial-update endpoint as updateCategory (category.go:326), on a row every
    // user of the instance sees. The diff is the user's own either way; what the
    // flush merges it against is the server's current catalog row, not this
    // user's copy of the catalog (see registry.ts's adminCategory.put).
    const base = options.base ? projectCategory(options.base) : null;
    const edit = base
      ? { base, patch: diffAgainstBase(base, data as unknown as FieldPatch) }
      : null;
    if (edit && Object.keys(edit.patch).length === 0) {
      return { queued: false };
    }
    const owner = offlineUserId();
    return request<Category>(`/admin/categories/${id}`, {
      method: "PUT",
      body: JSON.stringify(edit ? edit.patch : data),
    }).then(
      (row) => row,
      (err: unknown) => {
        if (options.queue === false || !isNetworkError(err)) throw err;
        if (!stillOwner(owner)) throw err;
        if (!edit) throw err;
        enqueueEdit(
          owner,
          "adminCategory.put",
          id,
          edit.base,
          edit.patch,
          edit.base,
        );
        return { queued: true };
      },
    );
  }) as EditWrite<Category, UpdateCategoryRequest>,
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
  createTransaction: (
    data: CreateTransactionRequest,
    options: { idempotencyKey?: string; queue?: boolean } = {},
  ): Promise<CreateTransactionResult> => {
    const clientKey = options.idempotencyKey ?? newClientKey();
    // The queue entry belongs to the session that issued the create: an entry
    // attributed to whoever is signed in later would be flushed against that
    // account, which the server rejects as not the entry's own — and the user
    // would never see where it went.
    const owner = offlineUserId();
    return request<{ id: string }>("/transactions", {
      method: "POST",
      body: JSON.stringify({ ...data, clientKey }),
    }).then(
      (res): CreateTransactionResult => ({ id: res.id, queued: false }),
      (err: unknown): CreateTransactionResult => {
        // Only a request that never reached the server may be replayed later; a
        // rejected one is surfaced to the caller as it always was.
        if (options.queue === false || !isNetworkError(err)) throw err;
        if (!stillOwner(owner)) throw err;
        // Never answer `queued` unless the entry is really in the queue: a
        // refused write (or a full queue) has to reach the user as a failure,
        // or the UI confirms a save that no flush will ever perform.
        enqueueCreate(owner, { ...data, clientKey }, clientKey);
        return { id: null, queued: true };
      },
    );
  },
  // The one transaction the app needs by id: the row a form is about to edit, read
  // so the edit has a base to be made against if the server turns out to be
  // unreachable.
  //
  // It scopes with `q=` alone, and never with an accountId: the list endpoint
  // injects synthetic summary rows (a per-cycle "Total outstanding", a month-end
  // "Running balance") when a single account is filtered and the sort is by date,
  // and it guards on accountUUID, which only an accountId parameter sets — so an
  // accountId here could answer a summary row rather than the row the caller
  // asked for.
  //
  // live, so this read is answered by the server or not at all. It is the base an
  // edit is diffed against, and the offline cache holds this browser's own last
  // belief of the row — a base from there would merge an edit against itself and
  // never see a conflict. (isQueriedLedgerRead separately keeps a q= URL out of
  // the cache; that is a budget rule, this is the correctness one, and neither is
  // left to imply the other.)
  getTransaction: (id: string): Promise<Transaction | null> =>
    api
      .getTransactions({ q: `id:${id}`, limit: 1 }, { live: true })
      .then((r) => r.data[0] ?? null),
  updateTransaction: (
    id: string,
    data: UpdateTransactionRequest,
    // `base` is the row the caller's form opened with. With it the request
    // carries only the fields the user actually changed, so a PATCH cannot revert
    // a column another writer moved while the form was open — and the same patch
    // is what gets queued, so an edit that never reached the server and one that
    // did cannot disagree about what the user changed.
    //
    // `queue: false` is what a caller that is *sending* an already-queued edit
    // passes — the outbox flush, which must not put a second copy of an entry
    // back in the queue when the request does not reach the server.
    options: { base?: Transaction; queue?: boolean } = {},
  ): Promise<UpdateTransactionResult> => {
    // One diff, both paths: the payload on the wire and the patch recorded in the
    // queue are reduced from the same base by the same projection the flush will
    // merge with. Without a base there is nothing to reduce against, so the
    // payload goes out as it always has.
    const base = options.base ? projectTransaction(options.base) : null;
    // The user's side is the payload as given rather than a projection of it, and
    // the whole difference is the nulls: project() drops a nullish value because
    // the *server's* row reports a null for a column it holds nothing in, while
    // here a null is the user clearing the field (merge.ts: absent is not null).
    // Every field UpdateTransactionRequest can carry is a mergeable one, so
    // projecting it would drop the clears and nothing else. Unchecked cast: an
    // interface carries no implicit index signature, the same seam registry.ts's
    // asRequest crosses in the other direction.
    const mine = data as unknown as FieldPatch;
    // The base and the patch are one value because a patch is never anything on
    // its own: it is a diff *against* a base, and an entry without one has
    // nothing to be merged against. Deriving the second from the first is also
    // what makes the empty-diff check below and the no-base refusal at the
    // enqueue the same question.
    const edit = base ? { base, patch: diffAgainstBase(base, mine) } : null;
    // An empty diff is not a write. The row already says what the form says, so
    // there is nothing to apply — and PATCH /transactions/{id} answers a body
    // with no fields 400 "no fields to update" (transaction.go:919), which would
    // put a server error in front of a user who changed nothing, and on the
    // flush path would mark a queued entry rejected for a write that was never
    // needed. Answering here is what planEdit already does with a decided entry
    // that has nothing left to write: the edit is made, so saying so is true
    // rather than a request that could only fail.
    if (edit && Object.keys(edit.patch).length === 0) {
      return Promise.resolve<UpdateTransactionResult>({ id, queued: false });
    }
    // The queue entry belongs to the session that issued the edit: an entry
    // attributed to whoever is signed in later would be flushed against that
    // account, which the server rejects as not the entry's own.
    const owner = offlineUserId();
    return request(`/transactions/${id}`, {
      method: "PATCH",
      body: JSON.stringify(edit ? edit.patch : data),
    }).then(
      (): UpdateTransactionResult => ({ id, queued: false }),
      (err: unknown): UpdateTransactionResult => {
        // Only a request that never reached the server may be replayed later; a
        // rejected one is the server refusing this payload, and the user has to
        // see that.
        if (options.queue === false || !isNetworkError(err)) throw err;
        if (!stillOwner(owner)) throw err;
        // No base is no queue: a patch with nothing to be a patch *against* would
        // merge as though the user had changed every field in it. Failing is the
        // honest answer — the caller has a base to pass.
        if (!edit) throw err;
        // Never answer `queued` unless the entry is really in the queue: a
        // refused write (or a full queue) has to reach the user as a failure, or
        // the UI confirms a save that no flush will ever perform.
        //
        // The base is passed twice on purpose: as the base, which the flush
        // merges against (enqueueEdit advances it by anything already queued for
        // this row), and as the snapshot, which is the row the form opened with
        // and is the only whole row the re-create path can rebuild a transaction
        // from.
        enqueueEdit(
          owner,
          "transaction.patch",
          id,
          edit.base,
          edit.patch,
          edit.base,
        );
        return { id, queued: true };
      },
    );
  },
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
  // Optional amortization schedule of a Loan / EMI account. GET answers with
  // schedule: null when the loan has no schedule yet.
  getLoanSchedule: (
    accountId: string,
    options: ReadOptions = {},
  ): Promise<LoanScheduleDetail> =>
    request(`/accounts/${accountId}/loan-schedule`, options),
  // What settling this loan on `date` ("YYYY-MM-DD") costs: its outstanding
  // principal plus the interest accrued since its last EMI payment. The
  // transfer endpoint runs the identical computation, so a preview and the
  // transfer it precedes cannot disagree.
  getLoanPayoff: (accountId: string, date: string): Promise<LoanPayoff> =>
    request(`/accounts/${accountId}/loan-payoff?date=${date}`),
  saveLoanSchedule: (
    accountId: string,
    data: LoanScheduleRequest,
  ): Promise<LoanScheduleDetail> =>
    request(`/accounts/${accountId}/loan-schedule`, {
      method: "PUT",
      body: JSON.stringify(data),
    }),
  deleteLoanSchedule: (accountId: string): Promise<{ deleted: number }> =>
    request(`/accounts/${accountId}/loan-schedule`, { method: "DELETE" }),
  // The bank credit that released this loan. Linking replaces any previous
  // credit; both endpoints answer with the refreshed schedule detail.
  linkLoanDisbursement: (
    accountId: string,
    data: LoanDisbursementRequest,
  ): Promise<LoanScheduleDetail> =>
    request(`/accounts/${accountId}/loan-disbursement`, {
      method: "PUT",
      body: JSON.stringify(data),
    }),
  unlinkLoanDisbursement: (accountId: string): Promise<{ deleted: number }> =>
    request(`/accounts/${accountId}/loan-disbursement`, { method: "DELETE" }),
  // Balance transfer: settles `accountId` at its outstanding balance and
  // reshapes the target loan by `mode` (recast / opens / takeover). The target
  // terms are required only when the target loan has no schedule yet.
  transferLoanBalance: (
    accountId: string,
    data: LoanTransferRequest,
  ): Promise<LoanTransferResult> =>
    request(`/accounts/${accountId}/loan-transfer`, {
      method: "POST",
      body: JSON.stringify(data),
    }),
  // Reverts both loans touched by a transfer. `accountId` must be the source
  // loan account, or the transfer is not found.
  deleteLoanTransfer: (
    accountId: string,
    transferId: string,
  ): Promise<{ deleted: number }> =>
    request(`/accounts/${accountId}/loan-transfer/${transferId}`, {
      method: "DELETE",
    }),
  bulkUpdateTags: (data: BulkUpdateTagsRequest): Promise<{ updated: number }> =>
    request("/transactions/bulk-tags", {
      method: "POST",
      body: JSON.stringify(data),
    }),
  // Filter-aware report export: honors the same params as getTransactions.
  exportTransactions: (params: QueryParams = {}): Promise<void> => {
    const qs = buildQuery(params);
    return downloadFile(
      `/transactions/export${qs ? `?${qs}` : ""}`,
      "fintrak_transactions.csv",
    );
  },

  // Statement parsing (PDF) — forwarded by the backend to the parser service.
  // The budget is the nginx location's (75s read/send) plus its own margin; the
  // backend's forward to the parser is capped at 60s.
  parseStatement: (formData: FormData): Promise<StatementParseResult> =>
    requestMultipart("/statements/parse", formData, 90000),
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
  getUserSettings: (options: ReadOptions = {}): Promise<UserSettings> =>
    request("/paperless/settings", options),
  updateUserSettings: (async (
    data: UpdateUserSettingsRequest,
    options: EditOptions<UserSettings> = {},
  ) => {
    // The singleton: there is no id to address, so the row an edit is queued
    // against is the user, and the endpoint takes the body alone. The base is the
    // UserSettings the settings form opened with.
    //
    // projectSettings is what keeps a token out of this. The response carries
    // hasToken and never the token (models.go:272-277), so the base cannot hold
    // one, and the diff is reduced against it field by field — a token the user
    // typed is a change the base never carried, and goes out as itself, while
    // nothing the client has not read can be written back to the row.
    const base = options.base ? projectSettings(options.base) : null;
    const edit = base
      ? { base, patch: diffAgainstBase(base, data as unknown as FieldPatch) }
      : null;
    if (edit && Object.keys(edit.patch).length === 0) {
      return { queued: false };
    }
    const owner = offlineUserId();
    return request<null>("/paperless/settings", {
      method: "PUT",
      body: JSON.stringify(edit ? edit.patch : data),
    }).then(
      (row) => row,
      (err: unknown) => {
        if (options.queue === false || !isNetworkError(err)) throw err;
        if (!stillOwner(owner)) throw err;
        if (!edit) throw err;
        // The user is the row: registry.ts's settings.put reads the singleton
        // rather than the id, so this is the only identifier the entry can carry.
        enqueueEdit(owner, "settings.put", owner, edit.base, edit.patch, edit.base);
        return { queued: true };
      },
    );
  }) as SettingsWrite<UserSettings, UpdateUserSettingsRequest>,
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
      // Same parser round-trip as /statements/parse.
      timeout: 120000,
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
      throw new ApiError("Failed to load document file", res.status);
    }
    return res.blob();
  },

  // Rules
  getRules: (options: ReadOptions = {}): Promise<Rule[]> =>
    request("/rules", options),
  createRule: (data: CreateRuleRequest): Promise<Rule> =>
    request("/rules", { method: "POST", body: JSON.stringify(data) }),
  updateRule: (id: string, data: UpdateRuleRequest): Promise<Rule> =>
    request(`/rules/${id}`, { method: "PUT", body: JSON.stringify(data) }),
  deleteRule: (id: string): Promise<null> =>
    request(`/rules/${id}`, { method: "DELETE" }),
  applyRules: (): Promise<ApplyRulesResult> =>
    request("/rules/apply", { method: "POST" }),
  previewRule: (data: CreateRuleRequest): Promise<RulePreview> =>
    request("/rules/preview", { method: "POST", body: JSON.stringify(data) }),

  // Payees
  getPayees: (options: ReadOptions = {}): Promise<Payee[]> =>
    request("/payees", options),
  createPayee: (data: CreatePayeeRequest): Promise<Payee> =>
    request("/payees", { method: "POST", body: JSON.stringify(data) }),
  updatePayee: (id: string, data: UpdatePayeeRequest): Promise<Payee> =>
    request(`/payees/${id}`, { method: "PUT", body: JSON.stringify(data) }),
  deletePayee: (id: string): Promise<null> =>
    request(`/payees/${id}`, { method: "DELETE" }),

  // Tags (derived from transactions.tags; no tag table)
  getTags: (): Promise<{ data: TagCount[] }> => request("/tags"),
  renameTag: (data: RenameTagRequest): Promise<{ updated: number }> =>
    request("/tags/rename", { method: "POST", body: JSON.stringify(data) }),

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
  getRecurringSeries: (options: ReadOptions = {}): Promise<{ data: RecurringSeries[] }> =>
    request("/recurring", options),
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
    options: ReadOptions = {},
  ): Promise<{ data: RecurringSeriesTerm[] }> =>
    request(`/recurring/${id}/terms`, options),
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

  // User-level backup & restore (whole account, not per-account). A restore
  // rewrites the whole ledger inside one transaction, so its budget is the
  // nginx location's (310s) plus the client's own margin.
  exportUserData: (): Promise<void> =>
    downloadFile("/export", "fintrak-backup.json"),
  importUserData: (data: unknown): Promise<BackupImportResult> =>
    request("/import", {
      method: "POST",
      body: JSON.stringify(data),
      timeout: 320000,
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
  // Per-period flow totals for the Money Flow timeline strip
  getMoneyFlowTimeline: (
    params: QueryParams = {},
  ): Promise<MoneyFlowTimeline> => {
    const qs = buildQuery(params);
    return request(`/dashboard/money-flow/timeline${qs ? `?${qs}` : ""}`);
  },
  // Circular-money report: the account cycles the Sankey cannot draw plus
  // one-directional account flows.
  getLinkCycles: (params: QueryParams = {}): Promise<LinkCycleReport> => {
    const qs = buildQuery(params);
    return request(`/links/cycles${qs ? `?${qs}` : ""}`);
  },
  // Cash-flow calendar heatmap (daily net flow + billing-cycle/summary overlays)
  getCashFlowCalendar: (
    params: QueryParams = {},
  ): Promise<CashFlowCalendar> => {
    const qs = buildQuery(params);
    return request(`/dashboard/cash-flow-calendar${qs ? `?${qs}` : ""}`);
  },
};

export default api;
