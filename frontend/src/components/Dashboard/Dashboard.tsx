import { useState, useEffect, useCallback, useMemo } from "react";
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
import { createColumnHelper, type ColumnDef } from "@tanstack/react-table";
import api from "../../api/client";
import { formatCurrency, formatDate } from "../../utils/formatters";
import { useSettings } from "../../context/SettingsContext";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { DataTable } from "@/components/ui/data-table";
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} from "@/components/ui/select";
import { Badge } from "@/components/ui/badge";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import type {
  DashboardSummary,
  Account,
  CategorySpend,
  QueryParams,
  Transaction,
} from "../../types";

const ALL_ACCOUNTS = "all";

function toISODate(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

function lastTwelveMonthsRange(): { dateFrom: string; dateTo: string } {
  const to = new Date();
  const from = new Date(to.getFullYear(), to.getMonth() - 12, to.getDate() + 1);
  return { dateFrom: toISODate(from), dateTo: toISODate(to) };
}

export default function Dashboard() {
  const [data, setData] = useState<DashboardSummary | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [accounts, setAccounts] = useState<Account[]>([]);
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
  const [groupBy, setGroupBy] = useState(
    searchParams.get("groupBy") === "billing_cycle" ? "billing_cycle" : "",
  );
  const [cycles, setCycles] = useState(searchParams.get("cycles") || "12");
  const { compactLayout } = useSettings();

  const selectedAccount = accounts.find((a) => a.id === accountId);
  const isBillingCycleMode = groupBy === "billing_cycle";
  const isBillingAccount = Boolean(selectedAccount?.billingDay);

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

  useEffect(() => {
    const params: Record<string, string> = {};
    if (accountId) params.accountId = accountId;
    if (isBillingCycleMode) {
      params.groupBy = "billing_cycle";
      params.cycles = cycles;
    } else {
      if (dateFrom) params.dateFrom = dateFrom;
      if (dateTo) params.dateTo = dateTo;
    }
    const urlParams = Object.fromEntries(searchParams.entries());
    if (JSON.stringify(params) !== JSON.stringify(urlParams)) {
      setSearchParams(params, { replace: true });
    }
  }, [accountId, dateFrom, dateTo, isBillingCycleMode, cycles, searchParams, setSearchParams]);

  useEffect(() => {
    setAccountId(searchParams.get("accountId") || "");
    setDateFrom(searchParams.get("dateFrom") || defaultRange.dateFrom);
    setDateTo(searchParams.get("dateTo") || defaultRange.dateTo);
    setGroupBy(searchParams.get("groupBy") === "billing_cycle" ? "billing_cycle" : "");
    const cyc = searchParams.get("cycles");
    if (cyc && ["6", "12", "24"].includes(cyc)) {
      setCycles(cyc);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchParams]);

  // Billing-cycle view only makes sense for an account that has a billing day;
  // fall back to the calendar view otherwise. Do not force it off while the
  // account list is still loading (selectedAccount is undefined) so the mode
  // survives a refresh.
  useEffect(() => {
    if (
      isBillingCycleMode &&
      (!accountId || (selectedAccount && !selectedAccount.billingDay))
    ) {
      setGroupBy("");
    }
  }, [isBillingCycleMode, accountId, selectedAccount]);

  const loadSummary = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const params: QueryParams = {};
      if (accountId) params.accountId = accountId;
      if (isBillingCycleMode) {
        params.groupBy = "billing_cycle";
        params.cycles = cycles;
      } else {
        if (dateFrom) params.dateFrom = dateFrom;
        if (dateTo) params.dateTo = dateTo;
      }
      const res = await api.getDashboardSummary(params);
      setData(res);
    } catch (err) {
      setError((err as Error).message || "Failed to load dashboard");
    } finally {
      setLoading(false);
    }
  }, [accountId, dateFrom, dateTo, isBillingCycleMode, cycles]);

  useEffect(() => {
    loadSummary();
  }, [loadSummary]);

  const recentColumnHelper = createColumnHelper<Transaction>();
  const recentColumns = useMemo<ColumnDef<Transaction>[]>(() => {
    const pad = compactLayout ? "py-1.5 px-3" : "py-3 px-4";
    const headBase = `${pad} h-auto text-xs font-semibold uppercase tracking-wider text-muted-foreground bg-muted/50 whitespace-nowrap`;
    return [
      recentColumnHelper.accessor("date", {
        header: () => "Date",
        cell: ({ row }) => (
          <span className="text-sm">{formatDate(row.original.date)}</span>
        ),
        meta: { headerClassName: headBase, cellClassName: pad },
      }),
      recentColumnHelper.accessor("description", {
        header: () => "Description",
        cell: ({ row }) => (
          <div className="max-w-75">
            <div className="text-sm font-medium text-foreground truncate">
              {row.original.description}
            </div>
            {row.original.payee && (
              <div className="text-[11px] text-muted-foreground truncate">
                {row.original.payee}
              </div>
            )}
          </div>
        ),
        meta: { headerClassName: headBase, cellClassName: `${pad} max-w-75` },
      }),
      recentColumnHelper.accessor("accountName", {
        header: () => "Account",
        cell: ({ row }) => (
          <span className="text-sm">{row.original.accountName}</span>
        ),
        meta: { headerClassName: headBase, cellClassName: pad },
      }),
      recentColumnHelper.display({
        id: "category",
        header: () => "Category",
        cell: ({ row }) =>
          row.original.categoryName ? (
            <Badge
              variant="outline"
              className={`h-auto rounded-full ${compactLayout ? "px-2 py-0" : "px-2.5 py-0.5"} inline-flex items-center text-xs font-medium`}
              style={{
                color: row.original.categoryColor,
                borderColor: row.original.categoryColor,
                background: `${row.original.categoryColor}15`,
              }}
            >
              {row.original.categoryName}
            </Badge>
          ) : (
            <span className="text-muted-foreground text-sm">—</span>
          ),
        meta: { headerClassName: headBase, cellClassName: pad },
      }),
      recentColumnHelper.accessor("amount", {
        header: () => "Amount",
        cell: ({ row }) => (
          <span
            className={`text-sm text-right font-semibold ${
              row.original.type === "debit"
                ? "text-destructive"
                : "text-emerald-500"
            }`}
          >
            {row.original.type === "debit" ? "−" : "+"}
            {formatCurrency(row.original.amount)}
          </span>
        ),
        meta: {
          headerClassName: `${headBase} text-right`,
          cellClassName: `${pad} text-right`,
        },
      }),
    ];
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [compactLayout]);

  if (loading)
    return (
      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto">
        <div className="flex flex-col items-center justify-center py-16 text-center">
          <Spinner className="size-8 text-primary" />
        </div>
      </div>
    );

  if (error) {
    return (
      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto">
        <div className="flex flex-col items-center justify-center py-20 text-center">
          <div className="px-5 py-3 bg-destructive/10 border border-destructive/30 rounded-lg text-sm text-destructive mb-4">
            {error}
          </div>
          <Button onClick={loadSummary}>Retry</Button>
        </div>
      </div>
    );
  }

  if (!data) return null;

  const netSavings = data.totalIncome - data.totalExpense;
  const trendData = (isBillingCycleMode
    ? data.billingCycleTrend ?? []
    : data.monthlyTrend ?? []) as unknown as Array<{
    income: number;
    expense: number;
    [key: string]: unknown;
  }>;
  const trendXKey = isBillingCycleMode ? "label" : "month";
  const hasTrend = trendData.length > 0;

  return (
    <>
      <div className="shrink-0 px-8 pt-6">
        <h1 className="text-2xl font-bold mb-1">Dashboard</h1>
        <p className="text-muted-foreground text-sm">
          Your financial overview at a glance
        </p>
      </div>
      <div className="shrink-0 px-8 pt-4">
        <div
          className={`flex flex-wrap items-center ${compactLayout ? "gap-2" : "gap-3"}`}
        >
          <Select
            value={accountId || ALL_ACCOUNTS}
            onValueChange={(v) => setAccountId(v === ALL_ACCOUNTS ? "" : v)}
          >
            <SelectTrigger
              className={`${compactLayout ? "h-8" : "h-10"} bg-background`}
            >
              <SelectValue placeholder="All Accounts" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL_ACCOUNTS}>All Accounts</SelectItem>
              {accounts.map((a) => (
                <SelectItem key={a.id} value={a.id}>
                  {a.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {isBillingCycleMode ? (
            <Select value={cycles} onValueChange={setCycles}>
              <SelectTrigger
                className={`${compactLayout ? "h-8" : "h-10"} bg-background w-40`}
              >
                <SelectValue placeholder="Cycles" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="6">Last 6 cycles</SelectItem>
                <SelectItem value="12">Last 12 cycles</SelectItem>
                <SelectItem value="24">Last 24 cycles</SelectItem>
              </SelectContent>
            </Select>
          ) : (
            <>
              <Input
                type="date"
                className={`w-auto ${compactLayout ? "h-9" : "h-10"} bg-background scheme-light dark:scheme-dark`}
                value={dateFrom}
                onChange={(e) => setDateFrom(e.target.value)}
                title="From date"
              />
              <Input
                type="date"
                className={`w-auto ${compactLayout ? "h-9" : "h-10"} bg-background scheme-light dark:scheme-dark`}
                value={dateTo}
                onChange={(e) => setDateTo(e.target.value)}
                title="To date"
              />
            </>
          )}
          {isBillingAccount && (
            <label
              className={`flex items-center gap-2 cursor-pointer select-none ${compactLayout ? "h-8" : "h-10"}`}
            >
              <Switch
                checked={isBillingCycleMode}
                onCheckedChange={(v) => setGroupBy(v ? "billing_cycle" : "")}
                size="sm"
              />
              <span className="text-sm text-muted-foreground">
                Billing cycle view
              </span>
            </label>
          )}
        </div>
      </div>
      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto w-full">
        {/* Current statement period (billing-cycle view) */}
        {data.currentCycle && (
          <p className="text-muted-foreground text-[13px] mb-3">
            Current statement:{" "}
            <span className="font-medium text-foreground">
              {data.currentCycle.label}
            </span>
            {" · "}
            {formatDate(data.currentCycle.startDate)} –{" "}
            {formatDate(data.currentCycle.endDate)}
          </p>
        )}
        {/* Stats */}
        <div
          className={`grid grid-cols-1 md:grid-cols-2 xl:grid-cols-4 ${compactLayout ? "gap-3 mb-4" : "gap-5 mb-6"}`}
        >
          <Card
            size={compactLayout ? "sm" : "default"}
            className="hover:ring-foreground/20 transition-colors"
          >
            <CardContent className="flex flex-col">
              <div className="w-11 h-11 rounded-lg flex items-center justify-center bg-emerald-500/15 mb-3">
                <TrendingUp size={22} className="text-emerald-500" />
              </div>
              <div className="text-xs text-muted-foreground mb-1">
                Total Income
              </div>
              <div className="text-2xl font-bold text-emerald-500">
                {formatCurrency(data.totalIncome)}
              </div>
            </CardContent>
          </Card>
          <Card className="hover:ring-foreground/20 transition-colors">
            <CardContent className="flex flex-col">
              <div className="w-11 h-11 rounded-lg flex items-center justify-center bg-destructive/10 mb-3">
                <TrendingDown size={22} className="text-destructive" />
              </div>
              <div className="text-xs text-muted-foreground mb-1">
                Total Expenses
              </div>
              <div className="text-2xl font-bold text-destructive">
                {formatCurrency(data.totalExpense)}
              </div>
            </CardContent>
          </Card>
          <Card className="hover:ring-foreground/20 transition-colors">
            <CardContent className="flex flex-col">
              <div className="w-11 h-11 rounded-lg flex items-center justify-center bg-primary/10 mb-3">
                <Wallet size={22} className="text-primary" />
              </div>
              <div className="text-xs text-muted-foreground mb-1">
                Net Savings
              </div>
              <div
                className={`text-2xl font-bold ${netSavings >= 0 ? "text-emerald-500" : "text-destructive"}`}
              >
                {formatCurrency(netSavings)}
              </div>
            </CardContent>
          </Card>
          <Card className="hover:ring-foreground/20 transition-colors">
            <CardContent className="flex flex-col">
              <div
                className={`w-11 h-11 rounded-lg flex items-center justify-center bg-primary/10 ${compactLayout ? "mb-2" : "mb-3"}`}
              >
                <ArrowUpDown size={22} className="text-primary" />
              </div>
              <div className="text-xs text-muted-foreground mb-1">
                Total Transactions
              </div>
              <div
                className={`${compactLayout ? "text-xl" : "text-2xl"} font-bold text-foreground`}
              >
                {data.totalTransactions.toLocaleString()}
              </div>
            </CardContent>
          </Card>
        </div>

        {/* Charts */}
        <div className="grid grid-cols-1 gap-6 mb-6">
          {/* Monthly Trend */}
          <Card
            size={compactLayout ? "sm" : "default"}
            className="flex flex-col w-full"
          >
            <CardHeader
              className={`flex flex-row items-center justify-between ${compactLayout ? "mb-3" : "mb-5"}`}
            >
              <CardTitle>
                {isBillingCycleMode
                  ? "Statement Income vs Expenses"
                  : "Monthly Income vs Expenses"}
              </CardTitle>
            </CardHeader>
            <CardContent className="flex-1 min-h-70">
              {hasTrend ? (
                <ResponsiveContainer width="100%" height={280}>
                  <BarChart data={trendData} barGap={4}>
                    <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                    <XAxis
                      dataKey={trendXKey}
                      stroke="var(--muted-foreground)"
                      fontSize={12}
                    />
                    <YAxis
                      stroke="var(--muted-foreground)"
                      fontSize={12}
                      tickFormatter={(v) => `₹${(v / 1000).toFixed(0)}k`}
                    />
                    <Tooltip
                      contentStyle={{
                        background: "var(--card)",
                        border: "1px solid var(--border)",
                        borderRadius: "8px",
                        color: "var(--foreground)",
                      }}
                      formatter={(v) => formatCurrency(Number(v))}
                    />
                    <Bar
                      dataKey="income"
                      fill="var(--chart-3)"
                      radius={[4, 4, 0, 0]}
                      name="Income"
                    />
                    <Bar
                      dataKey="expense"
                      fill="var(--chart-5)"
                      radius={[4, 4, 0, 0]}
                      name="Expense"
                    />
                  </BarChart>
                </ResponsiveContainer>
              ) : (
                <div className="flex flex-col items-center justify-center h-full text-muted-foreground p-10 text-center">
                  <p>No data yet. Import some statements!</p>
                </div>
              )}
            </CardContent>
          </Card>
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
        <Card size={compactLayout ? "sm" : "default"}>
          <CardHeader
            className={`flex flex-row items-center justify-between ${compactLayout ? "mb-3" : "mb-5"}`}
          >
            <CardTitle>Recent Transactions</CardTitle>
          </CardHeader>
          {data.recentTransactions.length > 0 ? (
            <CardContent>
              <DataTable
                columns={recentColumns}
                data={data.recentTransactions}
                getRowId={(row) => row.id}
                containerClassName="overflow-x-auto"
                headerClassName=""
                cellClassName=""
              />
            </CardContent>
          ) : (
            <div className="flex flex-col items-center justify-center p-10 text-center text-muted-foreground">
              <p>No transactions yet. Import a statement to get started.</p>
            </div>
          )}
        </Card>
      </div>
    </>
  );
}

function CategoryPieSection({
  title,
  categories,
  emptyMessage,
}: {
  title: string;
  categories: CategorySpend[];
  emptyMessage: string;
}) {
  const { compactLayout } = useSettings();
  return (
    <Card
      size={compactLayout ? "sm" : "default"}
      className="flex flex-col"
    >
      <CardHeader
        className={`flex flex-row items-center justify-between ${compactLayout ? "mb-3" : "mb-5"}`}
      >
        <CardTitle>{title}</CardTitle>
      </CardHeader>
      <CardContent className="flex-1">
        {categories && categories.length > 0 ? (
          <div className="flex flex-col md:flex-row gap-4 items-center h-full">
            <div className="w-full md:w-1/2 min-h-70">
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
                    stroke="var(--card)"
                  >
                    {categories.map((entry, i) => (
                      <Cell
                        key={i}
                        fill={entry.categoryColor || "var(--muted-foreground)"}
                      />
                    ))}
                  </Pie>
                  <Tooltip
                    contentStyle={{
                      background: "var(--card)",
                      border: "1px solid var(--border)",
                      borderRadius: "8px",
                      color: "var(--foreground)",
                    }}
                    formatter={(v) => formatCurrency(Number(v))}
                  />
                </PieChart>
              </ResponsiveContainer>
            </div>
            <div className="w-full md:w-1/2 text-[13px] max-h-70 overflow-y-auto pr-2">
              {categories.map((cat, i) => (
                <div
                  key={i}
                  className="flex items-center gap-2 py-1.5 border-b border-border last:border-0"
                >
                  <span
                    className="w-2.5 h-2.5 rounded-full shrink-0"
                    style={{ background: cat.categoryColor }}
                  />
                  <span className="flex-1 truncate">{cat.categoryName}</span>
                  <span className="font-medium text-foreground">
                    {formatCurrency(cat.total)}
                  </span>
                </div>
              ))}
            </div>
          </div>
        ) : (
          <div className="flex flex-col items-center justify-center h-full text-muted-foreground p-10 text-center">
            <p>{emptyMessage}</p>
          </div>
        )}
      </CardContent>
    </Card>
  );
}