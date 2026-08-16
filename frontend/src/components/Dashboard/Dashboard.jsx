import { useState, useEffect, useCallback } from "react";
import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  CartesianGrid,
  PieChart,
  Pie,
  Cell,
} from "recharts";
import { TrendingUp, TrendingDown, Wallet, ArrowUpDown } from "lucide-react";
import { useSearchParams } from "react-router-dom";
import api from "../../api/client";
import { formatCurrency, formatDate } from "../../utils/formatters";
import { useSettings } from "../../context/SettingsContext";

function toISODate(d) {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

function lastTwelveMonthsRange() {
  const to = new Date();
  const from = new Date(to.getFullYear(), to.getMonth() - 12, to.getDate() + 1);
  return { dateFrom: toISODate(from), dateTo: toISODate(to) };
}

export default function Dashboard() {
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [accounts, setAccounts] = useState([]);
  const defaultRange = lastTwelveMonthsRange();
  const [searchParams, setSearchParams] = useSearchParams();
  const [accountId, setAccountId] = useState(
    searchParams.get("accountId") || "",
  );
  const [dateFrom, setDateFrom] = useState(
    searchParams.get("dateFrom") || defaultRange.dateFrom,
  );
  const [dateTo, setDateTo] = useState(
    searchParams.get("dateTo") || defaultRange.dateTo,
  );
  const { compactLayout } = useSettings();

  useEffect(() => {
    api
      .getAccounts()
      .then((res) => {
        const list = Array.isArray(res) ? res : [];
        setAccounts(list);
        // Pre-fill the account filter with the user's default account when no
        // account filter was explicitly requested.
        const def = list.find((a) => a.isDefault);
        if (def && !searchParams.get("accountId")) {
          setAccountId(def.id);
        }
      })
      .catch(() => setAccounts([]));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Keep filters in the URL for easy sharing / navigation
  useEffect(() => {
    const params = {};
    if (accountId) params.accountId = accountId;
    if (dateFrom) params.dateFrom = dateFrom;
    if (dateTo) params.dateTo = dateTo;
    const urlParams = Object.fromEntries(searchParams.entries());
    if (JSON.stringify(params) !== JSON.stringify(urlParams)) {
      setSearchParams(params, { replace: true });
    }
  }, [accountId, dateFrom, dateTo, searchParams, setSearchParams]);

  // React to URL changes from navigation / back-forward
  useEffect(() => {
    setAccountId(searchParams.get("accountId") || "");
    setDateFrom(searchParams.get("dateFrom") || defaultRange.dateFrom);
    setDateTo(searchParams.get("dateTo") || defaultRange.dateTo);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchParams]);

  const loadSummary = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const params = {};
      if (accountId) params.accountId = accountId;
      if (dateFrom) params.dateFrom = dateFrom;
      if (dateTo) params.dateTo = dateTo;
      const res = await api.getDashboardSummary(params);
      setData(res);
    } catch (err) {
      setError(err.message || "Failed to load dashboard");
    } finally {
      setLoading(false);
    }
  }, [accountId, dateFrom, dateTo]);

  useEffect(() => {
    loadSummary();
  }, [loadSummary]);

  if (loading)
    return (
      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto">
        <div className="flex flex-col items-center justify-center py-16 text-center">
          <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-cyan-500"></div>
        </div>
      </div>
    );

  if (error) {
    return (
      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto">
        <div className="flex flex-col items-center justify-center py-20 text-center">
          <div className="px-5 py-3 bg-red-500/10 border border-red-500/20 rounded-lg text-sm text-red-400 mb-4">
            {error}
          </div>
          <button
            className="px-4 py-2 bg-cyan-500 hover:bg-cyan-400 text-white text-sm font-semibold rounded-lg transition-colors"
            onClick={loadSummary}
          >
            Retry
          </button>
        </div>
      </div>
    );
  }

  if (!data) return null;

  const netSavings = data.totalIncome - data.totalExpense;

  return (
    <>
      <div className="shrink-0 px-8 pt-6">
        <h1 className="text-2xl font-bold mb-1">Dashboard</h1>
        <p className="text-slate-400 text-sm">
          Your financial overview at a glance
        </p>
      </div>
      <div className="shrink-0 px-8 pt-4">
        <div
          className={`flex flex-wrap items-center ${compactLayout ? "gap-2" : "gap-3"}`}
        >
          <select
            className={`px-3.5 ${compactLayout ? "py-1.5" : "py-2.5"} bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 transition-all`}
            value={accountId}
            onChange={(e) => setAccountId(e.target.value)}
          >
            <option value="">All Accounts</option>
            {accounts.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name}
              </option>
            ))}
          </select>
          <input
            type="date"
            className={`px-3.5 ${compactLayout ? "py-1.5" : "py-2.5"} bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 transition-all scheme-dark `}
            value={dateFrom}
            onChange={(e) => setDateFrom(e.target.value)}
            title="From date"
          />
          <input
            type="date"
            className={`px-3.5 ${compactLayout ? "py-1.5" : "py-2.5"} bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 transition-all scheme-dark `}
            value={dateTo}
            onChange={(e) => setDateTo(e.target.value)}
            title="To date"
          />
        </div>
      </div>
      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto w-full">
        {/* Stats */}
        <div
          className={`grid grid-cols-1 md:grid-cols-2 xl:grid-cols-4 ${compactLayout ? "gap-3 mb-4" : "gap-5 mb-6"}`}
        >
          <div
            className={`bg-slate-900 border border-slate-800 rounded-xl ${compactLayout ? "p-3" : "p-5"} hover:border-slate-700 transition-colors`}
          >
            <div className="w-11 h-11 rounded-lg flex items-center justify-center bg-emerald-500/15 mb-3">
              <TrendingUp size={22} className="text-emerald-500" />
            </div>
            <div className="text-xs text-slate-400 mb-1">Total Income</div>
            <div className="text-2xl font-bold text-emerald-500">
              {formatCurrency(data.totalIncome)}
            </div>
          </div>
          <div className="bg-slate-900 border border-slate-800 rounded-xl p-5 hover:border-slate-700 transition-colors">
            <div className="w-11 h-11 rounded-lg flex items-center justify-center bg-red-500/15 mb-3">
              <TrendingDown size={22} className="text-red-500" />
            </div>
            <div className="text-xs text-slate-400 mb-1">Total Expenses</div>
            <div className="text-2xl font-bold text-red-500">
              {formatCurrency(data.totalExpense)}
            </div>
          </div>
          <div className="bg-slate-900 border border-slate-800 rounded-xl p-5 hover:border-slate-700 transition-colors">
            <div className="w-11 h-11 rounded-lg flex items-center justify-center bg-cyan-500/15 mb-3">
              <Wallet size={22} className="text-cyan-500" />
            </div>
            <div className="text-xs text-slate-400 mb-1">Net Savings</div>
            <div
              className={`text-2xl font-bold ${netSavings >= 0 ? "text-emerald-500" : "text-red-500"}`}
            >
              {formatCurrency(netSavings)}
            </div>
          </div>
          <div className="bg-slate-900 border border-slate-800 rounded-xl p-5 hover:border-slate-700 transition-colors">
            <div
              className={`w-11 h-11 rounded-lg flex items-center justify-center bg-violet-500/15 ${compactLayout ? "mb-2" : "mb-3"}`}
            >
              <ArrowUpDown size={22} className="text-violet-500" />
            </div>
            <div className="text-xs text-slate-400 mb-1">
              Total Transactions
            </div>
            <div
              className={`${compactLayout ? "text-xl" : "text-2xl"} font-bold text-slate-100`}
            >
              {data.totalTransactions.toLocaleString()}
            </div>
          </div>
        </div>

        {/* Charts */}
        <div className="grid grid-cols-1 gap-6 mb-6">
          {/* Monthly Trend */}
          <div
            className={`bg-slate-900 border border-slate-800 rounded-xl ${compactLayout ? "p-4" : "p-6"} flex flex-col w-full`}
          >
            <div
              className={`flex items-center justify-between ${compactLayout ? "mb-3" : "mb-5"}`}
            >
              <h3 className="text-base font-semibold">
                Monthly Income vs Expenses
              </h3>
            </div>
            <div className="flex-1 min-h-[280px]">
              {data.monthlyTrend.length > 0 ? (
                <ResponsiveContainer width="100%" height={280}>
                  <BarChart data={data.monthlyTrend} barGap={4}>
                    <CartesianGrid strokeDasharray="3 3" stroke="#1e293b" />
                    <XAxis dataKey="month" stroke="#94a3b8" fontSize={12} />
                    <YAxis
                      stroke="#94a3b8"
                      fontSize={12}
                      tickFormatter={(v) => `₹${(v / 1000).toFixed(0)}k`}
                    />
                    <Tooltip
                      contentStyle={{
                        background: "#0f172a",
                        border: "1px solid #1e293b",
                        borderRadius: "8px",
                        color: "#f1f5f9",
                      }}
                      formatter={(v) => formatCurrency(v)}
                    />
                    <Bar
                      dataKey="income"
                      fill="#10b981"
                      radius={[4, 4, 0, 0]}
                      name="Income"
                    />
                    <Bar
                      dataKey="expense"
                      fill="#ef4444"
                      radius={[4, 4, 0, 0]}
                      name="Expense"
                    />
                  </BarChart>
                </ResponsiveContainer>
              ) : (
                <div className="flex flex-col items-center justify-center h-full text-slate-400 p-10 text-center">
                  <p>No data yet. Import some statements!</p>
                </div>
              )}
            </div>
          </div>
        </div>

        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6 mb-6">
          <CategoryPieSection
            title="Spending by Category"
            categories={data.byCategory}
            emptyMessage="No categorized expenses yet"
          />
          <CategoryPieSection
            title="Income by Category"
            categories={data.incomeByCategory}
            emptyMessage="No categorized income yet"
          />
        </div>

        {/* Recent Transactions */}
        <div
          className={`bg-slate-900 border border-slate-800 rounded-xl ${compactLayout ? "p-4" : "p-6"}`}
        >
          <div
            className={`flex items-center justify-between ${compactLayout ? "mb-3" : "mb-5"}`}
          >
            <h3 className="text-base font-semibold">Recent Transactions</h3>
          </div>
          {data.recentTransactions.length > 0 ? (
            <div className="overflow-x-auto">
              <table className="w-full text-left border-collapse">
                <thead>
                  <tr>
                    <th
                      className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-xs font-semibold uppercase tracking-wider text-slate-500 bg-slate-800/50 border-b border-slate-800 whitespace-nowrap`}
                    >
                      Date
                    </th>
                    <th
                      className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-xs font-semibold uppercase tracking-wider text-slate-500 bg-slate-800/50 border-b border-slate-800 whitespace-nowrap`}
                    >
                      Description
                    </th>
                    <th
                      className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-xs font-semibold uppercase tracking-wider text-slate-500 bg-slate-800/50 border-b border-slate-800 whitespace-nowrap`}
                    >
                      Account
                    </th>
                    <th
                      className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-xs font-semibold uppercase tracking-wider text-slate-500 bg-slate-800/50 border-b border-slate-800 whitespace-nowrap`}
                    >
                      Category
                    </th>
                    <th
                      className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-xs font-semibold uppercase tracking-wider text-slate-500 bg-slate-800/50 border-b border-slate-800 whitespace-nowrap text-right`}
                    >
                      Amount
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {data.recentTransactions.map((t) => (
                    <tr
                      key={t.id}
                      className="hover:bg-slate-800/30 transition-colors"
                    >
                      <td
                        className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm border-b border-slate-800`}
                      >
                        {formatDate(t.date)}
                      </td>
                      <td
                        className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm border-b border-slate-800 max-w-[300px]`}
                      >
                        <div className="font-medium text-slate-200 truncate">
                          {t.description}
                        </div>
                        {t.payee && (
                          <div className="text-[11px] text-slate-500 truncate">
                            {t.payee}
                          </div>
                        )}
                      </td>
                      <td
                        className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm border-b border-slate-800`}
                      >
                        {t.accountName}
                      </td>
                      <td
                        className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm border-b border-slate-800`}
                      >
                        {t.categoryName ? (
                          <span
                            className={`${compactLayout ? "px-2 py-0" : "px-2.5 py-0.5"} inline-flex items-center rounded-full text-xs font-medium border`}
                            style={{
                              color: t.categoryColor,
                              borderColor: t.categoryColor,
                              background: `${t.categoryColor}15`,
                            }}
                          >
                            {t.categoryName}
                          </span>
                        ) : (
                          <span className="text-slate-500 text-sm">—</span>
                        )}
                      </td>
                      <td
                        className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm border-b border-slate-800 text-right font-semibold`}
                      >
                        <span
                          className={
                            t.type === "debit"
                              ? "text-red-500"
                              : "text-emerald-500"
                          }
                        >
                          {t.type === "debit" ? "−" : "+"}
                          {formatCurrency(t.amount)}
                        </span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <div className="flex flex-col items-center justify-center p-10 text-center text-slate-400">
              <p>No transactions yet. Import a statement to get started.</p>
            </div>
          )}
        </div>
      </div>
    </>
  );
}

function CategoryPieSection({ title, categories, emptyMessage }) {
  const { compactLayout } = useSettings();
  return (
    <div
      className={`bg-slate-900 border border-slate-800 rounded-xl ${compactLayout ? "p-4" : "p-6"} flex flex-col`}
    >
      <div
        className={`flex items-center justify-between ${compactLayout ? "mb-3" : "mb-5"}`}
      >
        <h3 className="text-base font-semibold">{title}</h3>
      </div>
      <div className="flex-1">
        {categories && categories.length > 0 ? (
          <div className="flex flex-col md:flex-row gap-4 items-center h-full">
            <div className="w-full md:w-1/2 min-h-[280px]">
              <ResponsiveContainer width="100%" height={280}>
                <PieChart>
                  <Pie
                    data={categories}
                    dataKey="total"
                    nameKey="categoryName"
                    cx="50%"
                    cy="50%"
                    outerRadius={100}
                    innerRadius={55}
                    strokeWidth={2}
                    stroke="#0f172a"
                  >
                    {categories.map((entry, i) => (
                      <Cell key={i} fill={entry.categoryColor || "#64748b"} />
                    ))}
                  </Pie>
                  <Tooltip
                    contentStyle={{
                      background: "#0f172a",
                      border: "1px solid #1e293b",
                      borderRadius: "8px",
                      color: "#f1f5f9",
                    }}
                    formatter={(v) => formatCurrency(v)}
                  />
                </PieChart>
              </ResponsiveContainer>
            </div>
            <div className="w-full md:w-1/2 text-[13px] max-h-[280px] overflow-y-auto pr-2">
              {categories.map((cat, i) => (
                <div
                  key={i}
                  className="flex items-center gap-2 py-1.5 border-b border-slate-800 last:border-0"
                >
                  <span
                    className="w-2.5 h-2.5 rounded-full shrink-0"
                    style={{ background: cat.categoryColor }}
                  />
                  <span className="flex-1 truncate">{cat.categoryName}</span>
                  <span className="font-medium text-slate-200">
                    {formatCurrency(cat.total)}
                  </span>
                </div>
              ))}
            </div>
          </div>
        ) : (
          <div className="flex flex-col items-center justify-center h-full text-slate-400 p-10 text-center">
            <p>{emptyMessage}</p>
          </div>
        )}
      </div>
    </div>
  );
}
