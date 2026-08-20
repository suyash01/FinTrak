const API_BASE = import.meta.env.VITE_API_URL || "http://localhost:8080/api/v1";

const TOKEN_KEY = "fintrak_token";
const USER_KEY = "fintrak_user";

const REQUEST_TIMEOUT = 15000;

export function getToken() {
  return localStorage.getItem(TOKEN_KEY);
}

export function setToken(token) {
  if (token) {
    localStorage.setItem(TOKEN_KEY, token);
  } else {
    localStorage.removeItem(TOKEN_KEY);
  }
}

export function getStoredUser() {
  try {
    return JSON.parse(localStorage.getItem(USER_KEY));
  } catch {
    return null;
  }
}

export function storeUser(user) {
  if (user) {
    localStorage.setItem(USER_KEY, JSON.stringify(user));
  } else {
    localStorage.removeItem(USER_KEY);
  }
}

async function request(url, options = {}) {
  const headers = { "Content-Type": "application/json", ...options.headers };
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

  let res;
  try {
    res = await fetch(`${API_BASE}${url}`, {
      ...options,
      signal: controller.signal,
      headers,
    });
  } catch (err) {
    if (err.name === "AbortError") {
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
    const err = new Error(error.error || "Request failed");
    err.status = res.status;
    throw err;
  }

  const text = await res.text();
  return text ? JSON.parse(text) : null;
}

// requestMultipart POSTs a FormData payload (multipart/form-data) with the auth
// header but without forcing a JSON content type, which the browser must set
// itself (including the boundary). Used for statement PDF uploads.
async function requestMultipart(url, formData) {
  const token = getToken();
  const headers = token ? { Authorization: `Bearer ${token}` } : {};

  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), REQUEST_TIMEOUT);
  let res;
  try {
    res = await fetch(`${API_BASE}${url}`, {
      method: "POST",
      body: formData,
      headers,
      signal: controller.signal,
    });
  } catch (err) {
    if (err.name === "AbortError") throw new Error("Request timed out");
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
    const err = new Error(error.error || "Request failed");
    err.status = res.status;
    throw err;
  }

  return res.json();
}

export async function downloadCSV(path) {
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
  register: (data) =>
    request("/auth/register", { method: "POST", body: JSON.stringify(data) }),
  login: (data) =>
    request("/auth/login", { method: "POST", body: JSON.stringify(data) }),

  // Accounts
  getAccounts: () => request("/accounts"),
  createAccount: (data) =>
    request("/accounts", { method: "POST", body: JSON.stringify(data) }),
  updateAccount: (id, data) =>
    request(`/accounts/${id}`, { method: "PUT", body: JSON.stringify(data) }),
  deleteAccount: (id) => request(`/accounts/${id}`, { method: "DELETE" }),
  getBillingCycles: (accountId) =>
    request(`/accounts/${accountId}/billing-cycles`),

  // Account Types
  getAccountTypes: () => request("/account-types"),
  createAccountType: (data) =>
    request("/account-types", { method: "POST", body: JSON.stringify(data) }),
  updateAccountType: (id, data) =>
    request(`/account-types/${id}`, {
      method: "PUT",
      body: JSON.stringify(data),
    }),
  deleteAccountType: (id) =>
    request(`/account-types/${id}`, { method: "DELETE" }),

  // Categories
  getCategories: () => request("/categories"),
  createCategory: (data) =>
    request("/categories", { method: "POST", body: JSON.stringify(data) }),

  // Transactions
  getTransactions: (params = {}, options = {}) => {
    const qs = new URLSearchParams(params).toString();
    return request(`/transactions?${qs}`, options);
  },
  createTransaction: (data) =>
    request("/transactions", { method: "POST", body: JSON.stringify(data) }),
  updateTransaction: (id, data) =>
    request(`/transactions/${id}`, {
      method: "PATCH",
      body: JSON.stringify(data),
    }),
  deleteTransaction: (id) =>
    request(`/transactions/${id}`, { method: "DELETE" }),
  importTransactions: (data) =>
    request("/transactions/import", {
      method: "POST",
      body: JSON.stringify(data),
    }),
  validateTransactions: (data) =>
    request("/transactions/validate", {
      method: "POST",
      body: JSON.stringify(data),
    }),
  bulkCategorize: (data) =>
    request("/transactions/bulk-categorize", {
      method: "POST",
      body: JSON.stringify(data),
    }),
  bulkUpdatePayee: (data) =>
    request("/transactions/bulk-payee", {
      method: "POST",
      body: JSON.stringify(data),
    }),
  bulkUpdateBillingCycle: (data) =>
    request("/transactions/bulk-billing-cycle", {
      method: "POST",
      body: JSON.stringify(data),
    }),
  bulkDeleteTransactions: (data) =>
    request("/transactions/bulk-delete", {
      method: "POST",
      body: JSON.stringify(data),
    }),

  // Statement parsing (PDF) — forwarded by the backend to the parser service
  parseStatement: (formData) => requestMultipart("/statements/parse", formData),
  getStatementExtractors: () => request("/statements/extractors"),

  // Paperless-ngx integration (per-user settings + manual pull)
  getPaperlessSettings: () => request("/paperless/settings"),
  updatePaperlessSettings: (data) =>
    request("/paperless/settings", {
      method: "PUT",
      body: JSON.stringify(data),
    }),

  // Generic per-user settings (the same /paperless/settings endpoint also
  // carries the transactions page-size preference).
  getUserSettings: () => request("/paperless/settings"),
  updateUserSettings: (data) =>
    request("/paperless/settings", {
      method: "PUT",
      body: JSON.stringify(data),
    }),
  getPaperlessDocuments: () => request("/paperless/documents"),
  importPaperlessDocument: (data) =>
    request("/paperless/import", {
      method: "POST",
      body: JSON.stringify(data),
    }),
  getPaperlessDocumentFile: async (id) => {
    const token = getToken();
    const res = await fetch(`${API_BASE}/paperless/documents/${id}/file`, {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    });
    if (!res.ok) {
      const err = new Error("Failed to load document file");
      err.status = res.status;
      throw err;
    }
    return res.blob();
  },

  // Rules
  getRules: () => request("/rules"),
  createRule: (data) =>
    request("/rules", { method: "POST", body: JSON.stringify(data) }),
  updateRule: (id, data) =>
    request(`/rules/${id}`, { method: "PUT", body: JSON.stringify(data) }),
  deleteRule: (id) => request(`/rules/${id}`, { method: "DELETE" }),
  applyRules: () => request("/rules/apply", { method: "POST" }),

  // Payees
  getPayees: () => request("/payees"),
  createPayee: (data) =>
    request("/payees", { method: "POST", body: JSON.stringify(data) }),
  updatePayee: (id, data) =>
    request(`/payees/${id}`, { method: "PUT", body: JSON.stringify(data) }),
  deletePayee: (id) => request(`/payees/${id}`, { method: "DELETE" }),

  // Links
  getLinks: (params = {}) => {
    const qs = new URLSearchParams(params).toString();
    return request(`/links${qs ? `?${qs}` : ""}`);
  },
  createLink: (data) =>
    request("/links", { method: "POST", body: JSON.stringify(data) }),
  deleteLink: (id) => request(`/links/${id}`, { method: "DELETE" }),
  bulkDeleteLinks: (data) =>
    request("/links/bulk-delete", {
      method: "POST",
      body: JSON.stringify(data),
    }),

  // Dashboard
  getDashboardSummary: (params = {}) => {
    const qs = new URLSearchParams(params).toString();
    return request(`/dashboard/summary?${qs}`);
  },
};

export default api;
