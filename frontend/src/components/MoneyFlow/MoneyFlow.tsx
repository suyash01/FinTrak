import {
  useState,
  useEffect,
  useCallback,
  useMemo,
  useRef,
  type ReactNode,
} from "react";
import {
  Sankey,
  Tooltip,
  ResponsiveContainer,
  type SankeyNodeProps,
  type SankeyLinkProps,
} from "recharts";
import { useNavigate, useSearchParams } from "react-router-dom";
import {
  ArrowRightLeft,
  CalendarDays,
  RefreshCw,
  Repeat,
  TrendingDown,
  TrendingUp,
  Waypoints,
} from "lucide-react";
import api from "../../api/client";
import { useRefetchOnFocus } from "../../lib/useRefetchOnFocus";
import { formatCurrency } from "../../utils/formatters";
import { useSettings } from "../../context/SettingsContext";
import { useDomainData } from "../../context/DomainDataContext";
import AccountSelect from "@/components/AccountSelect/AccountSelect";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import type {
  LinkCycleReport,
  MoneyFlowGraph,
  MoneyFlowLinkSummary,
  MoneyFlowNode,
  MoneyFlowTimeline,
  MoneyFlowTimelineGroupBy,
  MoneyFlowTimelinePeriod,
  QueryParams,
} from "../../types";

const ALL_ACCOUNTS = "all";

const PERIOD_LAST_12_MONTHS = "last_12_months";
const PERIOD_CURRENT_FY = "current_fy";
const PERIOD_CUSTOM = "custom";
const PERIOD_VALUES = [
  PERIOD_LAST_12_MONTHS,
  PERIOD_CURRENT_FY,
  PERIOD_CUSTOM,
];

const NODE_LIMIT_VALUES = ["8", "12", "20"];

const LINK_TYPE_LABELS: Record<string, string> = {
  transfer: "Transfers",
  cashback: "Cashbacks",
  refund: "Refunds",
  bill_payment: "Bill payments",
};

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

function currentFinancialYearRange(): { dateFrom: string; dateTo: string } {
  const now = new Date();
  const startYear =
    now.getMonth() >= 3 ? now.getFullYear() : now.getFullYear() - 1;
  return {
    dateFrom: toISODate(new Date(startYear, 3, 1)),
    dateTo: toISODate(new Date(startYear + 1, 2, 31)),
  };
}

function periodRange(period: string): { dateFrom: string; dateTo: string } {
  return period === PERIOD_CURRENT_FY
    ? currentFinancialYearRange()
    : lastTwelveMonthsRange();
}

// nodeDrilldownPath maps a money-flow node to the Transactions page filters
// that reproduce it. Aggregate "Other" nodes have no single underlying id, so
// they return null and are not clickable. Exported for unit testing.
export function nodeDrilldownPath(
  node: MoneyFlowNode,
  dateFrom: string,
  dateTo: string,
): string | null {
  // ":other" is an aggregate of many nodes; there is no single filter for it.
  if (node.id.endsWith(":other")) return null;

  const params = new URLSearchParams();
  if (dateFrom) params.set("dateFrom", dateFrom);
  if (dateTo) params.set("dateTo", dateTo);

  if (node.id.startsWith("account:")) {
    params.set("accountId", node.id.slice("account:".length));
  } else if (node.id.startsWith("category:")) {
    params.set("categoryId", node.id.slice("category:".length));
    params.set("type", "debit");
  } else if (node.id.startsWith("income:")) {
    params.set("categoryId", node.id.slice("income:".length));
    params.set("type", "credit");
  } else if (node.id.startsWith("payee:")) {
    params.set("payeeId", node.id.slice("payee:".length));
    params.set("type", "debit");
  } else {
    return null;
  }
  return `/transactions?${params.toString()}`;
}

// SankeyNodeShape paints one graph node with its stage color and a side label:
// nodes in the first two columns label to the right, the spending/payee columns
// to the left, so labels stay outside the flow. Drillable nodes get a pointer
// cursor and an onClick that navigates to their transactions.
function SankeyNodeShape({
  onNodeClick,
  ...props
}: SankeyNodeProps & { onNodeClick?: (node: MoneyFlowNode) => void }) {
  const { x, y, width, height } = props;
  const node = props.payload as unknown as MoneyFlowNode;
  const color = node.color || "var(--muted-foreground)";
  const labelRight = node.kind === "income" || node.kind === "account";
  const clickable = Boolean(onNodeClick);

  return (
    <g
      onClick={clickable ? () => onNodeClick?.(node) : undefined}
      className={clickable ? "cursor-pointer" : undefined}
    >
      <rect x={x} y={y} width={width} height={height} fill={color} rx={2} />
      {height >= 12 && (
        <text
          x={labelRight ? x + width + 6 : x - 6}
          y={y + height / 2}
          dy="0.32em"
          textAnchor={labelRight ? "start" : "end"}
          className="fill-foreground"
          style={{ fontSize: 11 }}
        >
          {node.name}
        </text>
      )}
    </g>
  );
}

// SankeyLinkShape draws one flow, tinted by its source color.
function SankeyLinkShape(props: SankeyLinkProps) {
  const {
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourceControlX,
    targetControlX,
    linkWidth,
  } = props;
  const source = props.payload.source as unknown as MoneyFlowNode;
  const color = source.color || "var(--muted-foreground)";

  return (
    <path
      d={`M${sourceX},${sourceY} C${sourceControlX},${sourceY} ${targetControlX},${targetY} ${targetX},${targetY}`}
      fill="none"
      stroke={color}
      strokeWidth={linkWidth}
      strokeOpacity={0.16}
    />
  );
}

export default function MoneyFlow() {
  const { accounts, groups } = useDomainData();
  const { compactLayout } = useSettings();
  const navigate = useNavigate();
  const defaultRange = useMemo(() => lastTwelveMonthsRange(), []);
  const [searchParams, setSearchParams] = useSearchParams();

  // The graph is most useful across every account, so unlike the dashboard the
  // account filter starts empty rather than on the default account.
  const [accountId, setAccountId] = useState(
    searchParams.get("accountId") || "",
  );
  const [dateFrom, setDateFrom] = useState(
    searchParams.get("dateFrom") || defaultRange.dateFrom,
  );
  const [dateTo, setDateTo] = useState(
    searchParams.get("dateTo") || defaultRange.dateTo,
  );
  const [limit, setLimit] = useState(
    NODE_LIMIT_VALUES.includes(searchParams.get("limit") || "")
      ? (searchParams.get("limit") as string)
      : "12",
  );
  const [period, setPeriod] = useState(() => {
    const p = searchParams.get("period");
    if (p && PERIOD_VALUES.includes(p)) return p;
    if (searchParams.get("dateFrom") || searchParams.get("dateTo")) {
      return PERIOD_CUSTOM;
    }
    return PERIOD_LAST_12_MONTHS;
  });
  const [groupBy, setGroupBy] = useState<MoneyFlowTimelineGroupBy>(() =>
    searchParams.get("groupBy") === "billing_cycle"
      ? "billing_cycle"
      : "month",
  );

  const [data, setData] = useState<MoneyFlowGraph | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [timeline, setTimeline] = useState<MoneyFlowTimeline | null>(null);
  const [timelineLoading, setTimelineLoading] = useState(true);

  useEffect(() => {
    const params: Record<string, string> = {};
    if (accountId) params.accountId = accountId;
    if (period) params.period = period;
    if (dateFrom) params.dateFrom = dateFrom;
    if (dateTo) params.dateTo = dateTo;
    params.limit = limit;
    if (groupBy === "billing_cycle") params.groupBy = groupBy;
    const urlParams = Object.fromEntries(searchParams.entries());
    if (JSON.stringify(params) !== JSON.stringify(urlParams)) {
      setSearchParams(params, { replace: true });
    }
  }, [
    accountId,
    dateFrom,
    dateTo,
    limit,
    period,
    groupBy,
    searchParams,
    setSearchParams,
  ]);

  useEffect(() => {
    setAccountId(searchParams.get("accountId") || "");
    setDateFrom(searchParams.get("dateFrom") || defaultRange.dateFrom);
    setDateTo(searchParams.get("dateTo") || defaultRange.dateTo);
    const lim = searchParams.get("limit");
    if (lim && NODE_LIMIT_VALUES.includes(lim)) setLimit(lim);
    const per = searchParams.get("period");
    if (per && PERIOD_VALUES.includes(per)) {
      setPeriod(per);
    } else {
      setPeriod(
        searchParams.get("dateFrom") || searchParams.get("dateTo")
          ? PERIOD_CUSTOM
          : PERIOD_LAST_12_MONTHS,
      );
    }
    setGroupBy(
      searchParams.get("groupBy") === "billing_cycle"
        ? "billing_cycle"
        : "month",
    );
  }, [searchParams, defaultRange.dateFrom, defaultRange.dateTo]);

  const applyPeriod = (value: string) => {
    setPeriod(value);
    if (value === PERIOD_LAST_12_MONTHS || value === PERIOD_CURRENT_FY) {
      const range = periodRange(value);
      setDateFrom(range.dateFrom);
      setDateTo(range.dateTo);
    }
  };

  // Billing-cycle grouping is only meaningful for a single account that has a
  // billing day; fall back to months otherwise.
  const selectedAccount = accounts.find((a) => a.id === accountId);
  const isBillingAccount = Boolean(selectedAccount?.billingDay);
  useEffect(() => {
    if (groupBy === "billing_cycle" && !isBillingAccount) setGroupBy("month");
  }, [groupBy, isBillingAccount]);

  const handleNodeClick = useCallback(
    (node: MoneyFlowNode) => {
      const path = nodeDrilldownPath(node, dateFrom, dateTo);
      if (path) navigate(path);
    },
    [navigate, dateFrom, dateTo],
  );

  const renderNode = useCallback(
    (props: SankeyNodeProps) => (
      <SankeyNodeShape {...props} onNodeClick={handleNodeClick} />
    ),
    [handleNodeClick],
  );

  const selectPeriod = (p: MoneyFlowTimelinePeriod) => {
    setDateFrom(p.startDate);
    setDateTo(p.endDate);
    setPeriod(PERIOD_CUSTOM);
  };

  // Monotonic request id so a slow earlier response can never overwrite the
  // result for the current filters.
  const requestIdRef = useRef(0);

  const load = useCallback(async () => {
    const requestId = ++requestIdRef.current;
    setLoading(true);
    setError("");
    try {
      const params: QueryParams = { limit };
      if (accountId) params.accountId = accountId;
      if (dateFrom) params.dateFrom = dateFrom;
      if (dateTo) params.dateTo = dateTo;
      const res = await api.getMoneyFlow(params);
      if (requestId !== requestIdRef.current) return;
      setData(res);
    } catch (err) {
      if (requestId !== requestIdRef.current) return;
      setError((err as Error).message || "Failed to load money flow");
    } finally {
      if (requestId === requestIdRef.current) setLoading(false);
    }
  }, [accountId, dateFrom, dateTo, limit]);

  useEffect(() => {
    void load();
  }, [load]);

  // Timeline strip is best-effort: a failure hides the strip rather than
  // failing the whole page. A monotonic id drops stale responses.
  const timelineReqRef = useRef(0);
  const loadTimeline = useCallback(async () => {
    const requestId = ++timelineReqRef.current;
    setTimelineLoading(true);
    try {
      const params: QueryParams = {};
      if (accountId) params.accountId = accountId;
      if (dateFrom) params.dateFrom = dateFrom;
      if (dateTo) params.dateTo = dateTo;
      if (groupBy === "billing_cycle") params.groupBy = groupBy;
      const res = await api.getMoneyFlowTimeline(params);
      if (requestId !== timelineReqRef.current) return;
      setTimeline(res);
    } catch {
      if (requestId === timelineReqRef.current) setTimeline(null);
    } finally {
      if (requestId === timelineReqRef.current) setTimelineLoading(false);
    }
  }, [accountId, dateFrom, dateTo, groupBy]);

  useEffect(() => {
    void loadTimeline();
  }, [loadTimeline]);

  // Circular-money report. Like the timeline it is best-effort: a failure
  // hides the panel rather than failing the page.
  const [cycles, setCycles] = useState<LinkCycleReport | null>(null);
  const cyclesReqRef = useRef(0);
  const loadCycles = useCallback(async () => {
    const requestId = ++cyclesReqRef.current;
    try {
      const params: QueryParams = {};
      if (accountId) params.accountId = accountId;
      if (dateFrom) params.dateFrom = dateFrom;
      if (dateTo) params.dateTo = dateTo;
      const res = await api.getLinkCycles(params);
      if (requestId !== cyclesReqRef.current) return;
      setCycles(res);
    } catch {
      if (requestId === cyclesReqRef.current) setCycles(null);
    }
  }, [accountId, dateFrom, dateTo]);

  useEffect(() => {
    void loadCycles();
  }, [loadCycles]);

  // Refresh the graph, timeline, and cycle report when the tab regains focus,
  // and on demand from the header button, so edits made elsewhere show up.
  const reloadAll = useCallback(() => {
    void load();
    void loadTimeline();
    void loadCycles();
  }, [load, loadTimeline, loadCycles]);
  useRefetchOnFocus(reloadAll);

  const activeTimelineKey = useMemo(() => {
    if (!timeline) return null;
    return (
      timeline.periods.find(
        (p) => p.startDate === dateFrom && p.endDate === dateTo,
      )?.key ?? null
    );
  }, [timeline, dateFrom, dateTo]);

  // Recharts Sankey wants links referencing nodes by array index.
  const sankeyData = useMemo(() => {
    if (!data) return null;
    const index = new Map(data.nodes.map((n, i) => [n.id, i]));
    const links = data.links
      .map((l) => ({
        source: index.get(l.source),
        target: index.get(l.target),
        value: l.value,
      }))
      .filter(
        (l): l is { source: number; target: number; value: number } =>
          l.source !== undefined && l.target !== undefined,
      );
    return { nodes: data.nodes, links };
  }, [data]);

  // Distinct base-group colors present among category nodes, for the legend.
  const groupLegend = useMemo(() => {
    if (!data) return [];
    const seen = new Map<string, { id: string; color: string }>();
    for (const n of data.nodes) {
      if (n.kind === "category" && n.group && !seen.has(n.group)) {
        seen.set(n.group, {
          id: n.group,
          color: n.color || "var(--muted-foreground)",
        });
      }
    }
    return [...seen.values()].map((g) => ({
      ...g,
      name: groups.find((x) => x.id === g.id)?.name ?? g.id,
    }));
  }, [data, groups]);

  if (loading && !data) {
    return (
      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto">
        <div className="flex flex-col items-center justify-center py-16 text-center">
          <Spinner className="size-8 text-primary" />
        </div>
      </div>
    );
  }

  if (error && !data) {
    return (
      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto">
        <div className="flex flex-col items-center justify-center py-20 text-center">
          <div className="px-5 py-3 bg-destructive/10 border border-destructive/30 rounded-lg text-sm text-destructive mb-4">
            {error}
          </div>
          <Button onClick={load}>Retry</Button>
        </div>
      </div>
    );
  }

  if (!data) return null;

  const net = data.totalIncome - data.totalExpense;
  const hasFlows = data.nodes.length > 0 && data.links.length > 0;

  return (
    <>
      <div className="shrink-0 px-8 pt-6 flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold mb-1">Money Flow</h1>
          <p className="text-muted-foreground text-sm">
            Where your money comes from, which accounts it passes through, and
            where it goes
          </p>
        </div>
        <Button
          variant="outline"
          size={compactLayout ? "sm" : "default"}
          onClick={reloadAll}
          disabled={loading}
          title="Refresh"
          aria-label="Refresh money flow"
        >
          <RefreshCw size={16} className={loading ? "animate-spin" : undefined} />
          {!compactLayout && "Refresh"}
        </Button>
      </div>
      <div className="shrink-0 px-8 pt-4">
        <div
          className={`flex flex-wrap items-center ${compactLayout ? "gap-2" : "gap-3"}`}
        >
          <AccountSelect
            accounts={accounts}
            value={accountId || ALL_ACCOUNTS}
            onValueChange={(v) => setAccountId(v === ALL_ACCOUNTS ? "" : v)}
            placeholder="All Accounts"
            ariaLabel="Filter by account"
            triggerClassName={`${compactLayout ? "h-8" : "h-10"} bg-background`}
            extraItems={<SelectItem value={ALL_ACCOUNTS}>All Accounts</SelectItem>}
          />
          <Select value={period} onValueChange={applyPeriod}>
            <SelectTrigger
              aria-label="Period"
              className={`${compactLayout ? "h-8" : "h-10"} bg-background w-44`}
            >
              <SelectValue placeholder="Period" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={PERIOD_LAST_12_MONTHS}>
                Last 12 months
              </SelectItem>
              <SelectItem value={PERIOD_CURRENT_FY}>
                Current financial year
              </SelectItem>
              <SelectItem value={PERIOD_CUSTOM}>Custom range</SelectItem>
            </SelectContent>
          </Select>
          <Input
            type="date"
            className={`w-auto ${compactLayout ? "h-9" : "h-10"} bg-background scheme-light dark:scheme-dark`}
            value={dateFrom}
            onChange={(e) => {
              setDateFrom(e.target.value);
              setPeriod(PERIOD_CUSTOM);
            }}
            title="From date"
            aria-label="From date"
          />
          <Input
            type="date"
            className={`w-auto ${compactLayout ? "h-9" : "h-10"} bg-background scheme-light dark:scheme-dark`}
            value={dateTo}
            onChange={(e) => {
              setDateTo(e.target.value);
              setPeriod(PERIOD_CUSTOM);
            }}
            title="To date"
            aria-label="To date"
          />
          <Select value={limit} onValueChange={setLimit}>
            <SelectTrigger
              aria-label="Nodes per stage"
              className={`${compactLayout ? "h-8" : "h-10"} bg-background w-40`}
            >
              <SelectValue placeholder="Nodes" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="8">Top 8 per stage</SelectItem>
              <SelectItem value="12">Top 12 per stage</SelectItem>
              <SelectItem value="20">Top 20 per stage</SelectItem>
            </SelectContent>
          </Select>
        </div>
      </div>

      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto w-full">
        {error && (
          <div className="mb-4 px-4 py-2 bg-destructive/10 border border-destructive/30 rounded-lg text-sm text-destructive">
            {error}
          </div>
        )}

        <div
          className={`grid grid-cols-1 md:grid-cols-3 ${compactLayout ? "gap-3 mb-4" : "gap-5 mb-6"}`}
        >
          <StatCard
            label="Money In"
            value={data.totalIncome}
            icon={<TrendingUp size={22} className="text-emerald-500" />}
            iconClass="bg-emerald-500/15"
            valueClass="text-emerald-500"
            compact={compactLayout}
          />
          <StatCard
            label="Money Out"
            value={data.totalExpense}
            icon={<TrendingDown size={22} className="text-destructive" />}
            iconClass="bg-destructive/10"
            valueClass="text-destructive"
            compact={compactLayout}
          />
          <StatCard
            label="Net Flow"
            value={net}
            icon={<Waypoints size={22} className="text-primary" />}
            iconClass="bg-primary/10"
            valueClass={net >= 0 ? "text-emerald-500" : "text-destructive"}
            compact={compactLayout}
          />
        </div>

        <Card
          size={compactLayout ? "sm" : "default"}
          className={`flex flex-col ${compactLayout ? "mb-4" : "mb-6"}`}
        >
          <CardHeader
            className={`flex flex-row items-center justify-between ${compactLayout ? "mb-3" : "mb-5"}`}
          >
            <CardTitle className="flex items-center gap-2">
              <CalendarDays className="text-primary" size={18} />
              Timeline
            </CardTitle>
            {isBillingAccount && (
              <Select
                value={groupBy}
                onValueChange={(v) =>
                  setGroupBy(v as MoneyFlowTimelineGroupBy)
                }
              >
                <SelectTrigger
                  aria-label="Timeline grouping"
                  className={`${compactLayout ? "h-8" : "h-10"} w-40 bg-background`}
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="month">By month</SelectItem>
                  <SelectItem value="billing_cycle">By billing cycle</SelectItem>
                </SelectContent>
              </Select>
            )}
          </CardHeader>
          <CardContent>
            {timelineLoading && !timeline ? (
              <div className="flex justify-center py-6">
                <Spinner className="size-6 text-primary" />
              </div>
            ) : (
              <TimelineStrip
                periods={timeline?.periods ?? []}
                activeKey={activeTimelineKey}
                onSelect={selectPeriod}
              />
            )}
          </CardContent>
        </Card>

        <div className="grid grid-cols-1 xl:grid-cols-3 gap-6">
          <Card
            size={compactLayout ? "sm" : "default"}
            className="xl:col-span-2 flex flex-col"
          >
            <CardHeader
              className={`flex flex-row items-center justify-between ${compactLayout ? "mb-3" : "mb-5"}`}
            >
              <CardTitle className="flex items-center gap-2">
                <Waypoints className="text-primary" size={18} />
                Flow
              </CardTitle>
            </CardHeader>
            <CardContent className="flex-1">
              {hasFlows && sankeyData ? (
                <>
                  <ResponsiveContainer width="100%" height={560}>
                    <Sankey
                      data={sankeyData}
                      nodePadding={18}
                      nodeWidth={12}
                      linkCurvature={0.5}
                      iterations={40}
                      node={renderNode}
                      link={SankeyLinkShape}
                      margin={{ top: 12, right: 90, bottom: 12, left: 90 }}
                    >
                      <Tooltip
                        contentStyle={{
                          background: "var(--card)",
                          border: "1px solid var(--border)",
                          borderRadius: "8px",
                          color: "var(--foreground)",
                        }}
                        formatter={(v) => formatCurrency(Number(v))}
                      />
                    </Sankey>
                  </ResponsiveContainer>
                  {groupLegend.length > 0 && (
                    <div className="flex flex-wrap items-center gap-x-4 gap-y-2 pt-3 mt-1 border-t border-border">
                      {groupLegend.map((g) => (
                        <span
                          key={g.id}
                          className="flex items-center gap-2 text-xs text-muted-foreground"
                        >
                          <span
                            className="w-2.5 h-2.5 rounded-full shrink-0"
                            style={{ background: g.color }}
                          />
                          {g.name}
                        </span>
                      ))}
                    </div>
                  )}
                </>
              ) : (
                <div className="flex flex-col items-center justify-center h-70 text-muted-foreground p-10 text-center">
                  <p>No transactions in this range. Import a statement to see the flow.</p>
                </div>
              )}
            </CardContent>
          </Card>

          <Card size={compactLayout ? "sm" : "default"} className="flex flex-col">
            <CardHeader
              className={`flex flex-row items-center justify-between ${compactLayout ? "mb-3" : "mb-5"}`}
            >
              <CardTitle className="flex items-center gap-2">
                <ArrowRightLeft className="text-primary" size={18} />
                Linked Transfers
              </CardTitle>
            </CardHeader>
            <CardContent>
              <p className="text-xs text-muted-foreground mb-4">
                Cross-account transfers, refunds, cashbacks, and bill payments
                are drawn as account-to-account flows. Select a node to view its
                transactions, or a type below to list every linked transaction.
              </p>
              <LinkSummaryPanel
                summary={data.linkSummary}
                onSelect={() =>
                  navigate(
                    `/transactions?linked=true${
                      dateFrom ? `&dateFrom=${dateFrom}` : ""
                    }${dateTo ? `&dateTo=${dateTo}` : ""}`,
                  )
                }
              />
            </CardContent>
          </Card>
        </div>

        {cycles && (
          <Card size={compactLayout ? "sm" : "default"} className="mt-6">
            <CardHeader
              className={`flex flex-row items-center justify-between ${compactLayout ? "mb-3" : "mb-5"}`}
            >
              <CardTitle className="flex items-center gap-2">
                <Repeat className="text-primary" size={18} />
                Circular Money
              </CardTitle>
            </CardHeader>
            <CardContent>
              <CircularMoneyPanel report={cycles} />
            </CardContent>
          </Card>
        )}
      </div>
    </>
  );
}

// TimelineStrip is the "small multiples" scrubber: one compact column per
// period (month or billing cycle) showing income vs expense, with the current
// window highlighted. Clicking a period re-queries the Sankey for it.
function TimelineStrip({
  periods,
  activeKey,
  onSelect,
}: {
  periods: MoneyFlowTimelinePeriod[];
  activeKey: string | null;
  onSelect: (period: MoneyFlowTimelinePeriod) => void;
}) {
  if (periods.length === 0) {
    return (
      <div className="py-8 text-center text-sm text-muted-foreground">
        No periods in this range.
      </div>
    );
  }

  const max = periods.reduce(
    (m, p) => Math.max(m, p.income, p.expense),
    0,
  );

  return (
    <div className="flex gap-2 overflow-x-auto pb-1">
      {periods.map((p) => {
        const active = p.key === activeKey;
        const incomePct = max > 0 ? Math.max(2, (p.income / max) * 100) : 0;
        const expensePct = max > 0 ? Math.max(2, (p.expense / max) * 100) : 0;
        return (
          <button
            key={p.key}
            type="button"
            onClick={() => onSelect(p)}
            title={`${p.label}: in ${formatCurrency(p.income)}, out ${formatCurrency(p.expense)}`}
            className={`w-24 shrink-0 rounded-lg border px-2 py-2 text-left transition-colors ${
              active
                ? "border-primary bg-primary/10"
                : "border-border hover:border-primary/50 hover:bg-accent"
            }`}
          >
            <div className="truncate text-[11px] text-muted-foreground">
              {p.label}
            </div>
            <div className="mt-1 flex h-16 items-end justify-center gap-1">
              <span
                className="w-2.5 rounded-t bg-emerald-500/70"
                style={{ height: `${incomePct}%` }}
              />
              <span
                className="w-2.5 rounded-t bg-destructive/70"
                style={{ height: `${expensePct}%` }}
              />
            </div>
            <div
              className={`mt-1 truncate text-[11px] font-medium ${
                p.net >= 0 ? "text-emerald-500" : "text-destructive"
              }`}
            >
              {formatCurrency(p.net)}
            </div>
          </button>
        );
      })}
    </div>
  );
}

function StatCard({
  label,
  value,
  icon,
  iconClass,
  valueClass,
  compact,
}: {
  label: string;
  value: number;
  icon: ReactNode;
  iconClass: string;
  valueClass: string;
  compact: boolean;
}) {
  return (
    <Card
      size={compact ? "sm" : "default"}
      className="hover:ring-foreground/20 transition-colors"
    >
      <CardContent className="flex flex-col">
        <div
          className={`w-11 h-11 rounded-lg flex items-center justify-center ${iconClass} ${compact ? "mb-2" : "mb-3"}`}
        >
          {icon}
        </div>
        <div className="text-xs text-muted-foreground mb-1">{label}</div>
        <div className={`text-2xl font-bold ${valueClass}`}>
          {formatCurrency(value)}
        </div>
      </CardContent>
    </Card>
  );
}

function LinkSummaryPanel({
  summary,
  onSelect,
}: {
  summary: MoneyFlowLinkSummary[];
  onSelect?: (type: string) => void;
}) {
  if (summary.length === 0) {
    return (
      <div className="text-sm text-muted-foreground py-6 text-center">
        No linked transactions in this range.
      </div>
    );
  }

  return (
    <div className="space-y-2">
      {summary.map((s) => (
        <button
          key={s.type}
          type="button"
          onClick={onSelect ? () => onSelect(s.type) : undefined}
          title={onSelect ? "View these transactions" : undefined}
          className={`flex w-full items-center justify-between gap-3 rounded-lg border border-border px-3 py-2 text-left transition-colors ${
            onSelect ? "hover:border-primary/50 hover:bg-accent" : ""
          }`}
        >
          <div className="min-w-0">
            <div className="text-sm font-medium text-foreground truncate">
              {LINK_TYPE_LABELS[s.type] ?? s.type}
            </div>
            <div className="text-xs text-muted-foreground">
              {s.count} {s.count === 1 ? "link" : "links"}
            </div>
          </div>
          <div className="text-sm font-semibold text-foreground whitespace-nowrap">
            {formatCurrency(s.total)}
          </div>
        </button>
      ))}
    </div>
  );
}

// CircularMoneyPanel surfaces what the Sankey cannot draw: the account cycles
// its cycle-breaking nets away or drops, and the account-to-account flows with
// no counterpart in the opposite direction.
function CircularMoneyPanel({ report }: { report: LinkCycleReport }) {
  if (report.cycles.length === 0 && report.oneSidedFlows.length === 0) {
    return (
      <div className="text-sm text-muted-foreground py-6 text-center">
        No circular or one-way account flows in this range.
      </div>
    );
  }

  return (
    <div className="space-y-5">
      {report.cycles.length > 0 && (
        <div>
          <div className="mb-2 flex items-baseline justify-between gap-3">
            <h3 className="text-sm font-medium text-foreground">
              Cycles between your accounts
            </h3>
            <span className="text-xs text-muted-foreground">
              {formatCurrency(report.totalCircular)} circulating
            </span>
          </div>
          <div className="space-y-2">
            {report.cycles.map((c, i) => (
              <div
                key={`${c.kind}-${i}`}
                className="rounded-lg border border-border px-3 py-2"
              >
                <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1">
                  <div className="flex min-w-0 flex-wrap items-center gap-x-1.5 text-sm text-foreground">
                    {c.accounts.map((a, idx) => (
                      <span key={a.id} className="flex items-center gap-1.5">
                        {idx > 0 && (
                          <span className="text-muted-foreground">→</span>
                        )}
                        <span className="flex items-center gap-1.5">
                          {a.color && (
                            <span
                              className="h-2 w-2 shrink-0 rounded-full"
                              style={{ background: a.color }}
                            />
                          )}
                          <span className="truncate font-medium">{a.name}</span>
                        </span>
                      </span>
                    ))}
                    <span className="text-muted-foreground">→</span>
                    <span className="truncate text-muted-foreground">
                      {c.accounts[0]?.name}
                    </span>
                  </div>
                  <div className="text-sm font-semibold text-foreground whitespace-nowrap">
                    {formatCurrency(c.net)}
                  </div>
                </div>
                <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
                  <span>
                    {c.kind === "reciprocal"
                      ? "Two-way pair"
                      : `${c.accounts.length}-account loop`}
                  </span>
                  <span>
                    {c.transactions}{" "}
                    {c.transactions === 1 ? "link" : "links"}
                  </span>
                  {c.legs.map((leg) => (
                    <span key={`${leg.fromAccountId}-${leg.toAccountId}`}>
                      {leg.fromAccountName} → {leg.toAccountName}:{" "}
                      {formatCurrency(leg.amount)}
                    </span>
                  ))}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {report.oneSidedFlows.length > 0 && (
        <div>
          <h3 className="mb-2 text-sm font-medium text-foreground">
            One-way account flows
          </h3>
          <p className="mb-2 text-xs text-muted-foreground">
            No link moves money back the other way. A card bill paid from a bank
            account looks the same as a half-entered transfer.
          </p>
          <div className="space-y-2">
            {report.oneSidedFlows.map((f) => (
              <div
                key={`${f.fromAccountId}-${f.toAccountId}`}
                className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 rounded-lg border border-border px-3 py-2"
              >
                <div className="flex min-w-0 items-center gap-1.5 text-sm text-foreground">
                  {f.fromAccountColor && (
                    <span
                      className="h-2 w-2 shrink-0 rounded-full"
                      style={{ background: f.fromAccountColor }}
                    />
                  )}
                  <span className="truncate font-medium">
                    {f.fromAccountName}
                  </span>
                  <span className="text-muted-foreground">→</span>
                  {f.toAccountColor && (
                    <span
                      className="h-2 w-2 shrink-0 rounded-full"
                      style={{ background: f.toAccountColor }}
                    />
                  )}
                  <span className="truncate font-medium">{f.toAccountName}</span>
                </div>
                <div className="flex items-center gap-3">
                  <span className="text-xs text-muted-foreground">
                    {f.types
                      .map(
                        (t) =>
                          `${LINK_TYPE_LABELS[t.type] ?? t.type} (${t.count})`,
                      )
                      .join(", ")}
                  </span>
                  <span className="text-sm font-semibold text-foreground whitespace-nowrap">
                    {formatCurrency(f.total)}
                  </span>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
