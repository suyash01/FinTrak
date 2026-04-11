const API_BASE = import.meta.env.VITE_API_URL || 'http://localhost:8080/api/v1';

async function request(url, options = {}) {
  const res = await fetch(`${API_BASE}${url}`, {
    headers: { 'Content-Type': 'application/json', ...options.headers },
    ...options,
  });
  if (!res.ok) {
    const error = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(error.error || 'Request failed');
  }
  return res.json();
}

const api = {
  // Accounts
  getAccounts: () => request('/accounts'),
  createAccount: (data) => request('/accounts', { method: 'POST', body: JSON.stringify(data) }),
  updateAccount: (id, data) => request(`/accounts/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
  deleteAccount: (id) => request(`/accounts/${id}`, { method: 'DELETE' }),

  // Categories
  getCategories: () => request('/categories'),
  createCategory: (data) => request('/categories', { method: 'POST', body: JSON.stringify(data) }),

  // Transactions
  getTransactions: (params = {}) => {
    const qs = new URLSearchParams(params).toString();
    return request(`/transactions?${qs}`);
  },
  updateTransaction: (id, data) =>
    request(`/transactions/${id}`, { method: 'PATCH', body: JSON.stringify(data) }),
  deleteTransaction: (id) => request(`/transactions/${id}`, { method: 'DELETE' }),
  importTransactions: (data) =>
    request('/transactions/import', { method: 'POST', body: JSON.stringify(data) }),
  bulkCategorize: (data) =>
    request('/transactions/bulk-categorize', { method: 'POST', body: JSON.stringify(data) }),
  bulkUpdatePayee: (data) =>
    request('/transactions/bulk-payee', { method: 'POST', body: JSON.stringify(data) }),
  bulkDeleteTransactions: (data) =>
    request('/transactions/bulk-delete', { method: 'POST', body: JSON.stringify(data) }),

  // Rules
  getRules: () => request('/rules'),
  createRule: (data) => request('/rules', { method: 'POST', body: JSON.stringify(data) }),
  updateRule: (id, data) => request(`/rules/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
  deleteRule: (id) => request(`/rules/${id}`, { method: 'DELETE' }),
  applyRules: () => request('/rules/apply', { method: 'POST' }),

  // Payees
  getPayees: () => request('/payees'),
  createPayee: (data) => request('/payees', { method: 'POST', body: JSON.stringify(data) }),
  updatePayee: (id, data) => request(`/payees/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
  deletePayee: (id) => request(`/payees/${id}`, { method: 'DELETE' }),

  // Links
  getLinks: (type) => request(`/links${type ? `?type=${type}` : ''}`),
  createLink: (data) => request('/links', { method: 'POST', body: JSON.stringify(data) }),
  bulkCreateLinks: (data) => request('/links/bulk', { method: 'POST', body: JSON.stringify(data) }),
  deleteLink: (id) => request(`/links/${id}`, { method: 'DELETE' }),
  bulkDeleteLinks: (data) =>
    request('/links/bulk-delete', { method: 'POST', body: JSON.stringify(data) }),
  getTransferSuggestions: () => request('/links/transfer-suggestions'),
  getCashbackSuggestions: () => request('/links/cashback-suggestions'),

  // Dashboard
  getDashboardSummary: (params = {}) => {
    const qs = new URLSearchParams(params).toString();
    return request(`/dashboard/summary?${qs}`);
  },
};

export default api;
