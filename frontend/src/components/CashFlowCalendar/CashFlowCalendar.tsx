import {
  Fragment,
  useState,
  useEffect,
  useCallback,
  useMemo,
  useRef,
  type CSSProperties,
  type MouseEvent,
  type ReactNode,
} from "react";
import { useSearchParams } from "react-router-dom";
import {
  CalendarDays,
  TrendingDown,
  TrendingUp,
  Waypoints,
} from "lucide-react";
import api from "../../api/client";
import {
  formatCurrency,
  formatDate,
  formatDateOnly,
  parseDateOnly,
} from "../../utils/formatters";
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
  CashFlowCalendar as CashFlowCalendarData,
  CashFlowCalendarCycle,
  CashFlowCalendarDay,
  CashFlowCalendarMarker,
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

const WEEKDAYS = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];
const MONTHS = [
  "Jan",
  "Feb",
  "Mar",
  "Apr",
  "May",
  "Jun",
  "Jul",
  "Aug",
  "Sep",
  "Oct",
  "Nov",
  "Dec",
];

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

interface DayCell {
  date: string;
  inRange: boolean;
  income: number;
  expense: number;
  net: number;
  count: number;
  markers: CashFlowCalendarMarker[];
}

interface WeekColumn {
  cells: DayCell[];
  monthLabel: string | null;
}

// buildWeeks lays the requested range out as GitHub-style week columns
// (Monday-first). Days outside the range are kept as padding so every column
// has seven cells; days with no API aggregate read as zero.
function buildWeeks(
  dateFrom: string,
  dateTo: string,
  dayMap: Map<string, CashFlowCalendarDay>,
  markerMap: Map<string, CashFlowCalendarMarker[]>,
): WeekColumn[] {
  const from = parseDateOnly(dateFrom);
  const to = parseDateOnly(dateTo);
  if (!from || !to || from > to) return [];

  const start = new Date(from);
  const startDow = (start.getDay() + 6) % 7; // Monday = 0
  start.setDate(start.getDate() - startDow);

  const end = new Date(to);
  const endDow = (end.getDay() + 6) % 7;
  end.setDate(end.getDate() + (6 - endDow));

  const weeks: WeekColumn[] = [];
  let prevMonth = -1;
  let cursor = new Date(start);
  // Guard the loop so a bad clock/DST edge can never spin forever.
  for (let guard = 0; cursor <= end && guard < 80; guard++) {
    const cells: DayCell[] = [];
    for (let i = 0; i < 7; i++) {
      const d = new Date(cursor);
      d.setDate(cursor.getDate() + i);
      const key = formatDateOnly(d);
      const inRange = key >= dateFrom && key <= dateTo;
      const agg = inRange ? dayMap.get(key) : undefined;
      cells.push({
        date: key,
        inRange,
        income: agg?.income ?? 0,
        expense: agg?.expense ?? 0,
        net: agg?.net ?? 0,
        count: agg?.count ?? 0,
        markers: inRange ? (markerMap.get(key) ?? []) : [],
      });
    }

    const firstInRange = cells.find((c) => c.inRange);
    let monthLabel: string | null = null;
    if (firstInRange) {
      const month = parseDateOnly(firstInRange.date)?.getMonth() ?? -1;
      if (month !== prevMonth) {
        monthLabel = month >= 0 ? MONTHS[month] : null;
        prevMonth = month;
      }
    }
    weeks.push({ cells, monthLabel });
    cursor = new Date(cursor);
    cursor.setDate(cursor.getDate() + 7);
  }
  return weeks;
}

// findCycleStartColumns maps each billing cycle to the week column that holds
// its start date, so the overlay can draw a boundary line and a label there.
function findCycleStartColumns(
  weeks: WeekColumn[],
  cycles: CashFlowCalendarCycle[],
): Map<number, CashFlowCalendarCycle> {
  const map = new Map<number, CashFlowCalendarCycle>();
  if (weeks.length === 0) return map;
  for (const cycle of cycles) {
    // Cycle dates arrive as RFC3339 timestamps; normalize to YYYY-MM-DD so the
    // lexicographic overlap checks line up with the calendar's date strings.
    const start = formatDateOnly(parseDateOnly(cycle.startDate));
    const end = formatDateOnly(parseDateOnly(cycle.endDate));
    if (!start || !end) continue;
    const idx = weeks.findIndex(
      (w) => w.cells[0].date <= end && w.cells[6].date >= start,
    );
    if (idx >= 0) map.set(idx, cycle);
  }
  return map;
}

// dayBackground tints a cell by the magnitude of its net flow: green for a
// surplus, red for a deficit, muted for a flat or empty day.
function dayBackground(
  net: number,
  maxAbs: number,
  hasData: boolean,
): CSSProperties {
  if (!hasData || maxAbs <= 0 || net === 0) {
    return { backgroundColor: "var(--muted)" };
  }
  const intensity = Math.min(1, Math.abs(net) / maxAbs);
  const pct = Math.round((0.18 + intensity * 0.72) * 100);
  const token = net > 0 ? "var(--chart-3)" : "var(--destructive)";
  return { backgroundColor: `color-mix(in oklch, ${token} ${pct}%, var(--muted))` };
}

function cellLabel(cell: DayCell): string {
  if (!cell.inRange) return "";
  const parts = [`${formatDate(cell.date)}: net ${formatCurrency(cell.net)}`];
  if (cell.count > 0) {
    parts.push(`${cell.count} transaction${cell.count === 1 ? "" : "s"}`);
  }
  for (const marker of cell.markers) {
    parts.push(`${marker.label} ${formatCurrency(marker.amount)}`);
  }
  return parts.join(", ");
}

export default function CashFlowCalendar() {
  const { accounts } = useDomainData();
  const { compactLayout } = useSettings();
  const defaultRange = useMemo(() => lastTwelveMonthsRange(), []);
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
  const [period, setPeriod] = useState(() => {
    const p = searchParams.get("period");
    if (p && PERIOD_VALUES.includes(p)) return p;
    if (searchParams.get("dateFrom") || searchParams.get("dateTo")) {
      return PERIOD_CUSTOM;
    }
    return PERIOD_LAST_12_MONTHS;
  });

  const [data, setData] = useState<CashFlowCalendarData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [selected, setSelected] = useState<DayCell | null>(null);
  const [hover, setHover] = useState<{
    cell: DayCell;
    left: number;
    top: number;
  } | null>(null);
  const gridRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const params: Record<string, string> = {};
    if (accountId) params.accountId = accountId;
    if (period) params.period = period;
    if (dateFrom) params.dateFrom = dateFrom;
    if (dateTo) params.dateTo = dateTo;
    const urlParams = Object.fromEntries(searchParams.entries());
    if (JSON.stringify(params) !== JSON.stringify(urlParams)) {
      setSearchParams(params, { replace: true });
    }
  }, [accountId, dateFrom, dateTo, period, searchParams, setSearchParams]);

  useEffect(() => {
    setAccountId(searchParams.get("accountId") || "");
    setDateFrom(searchParams.get("dateFrom") || defaultRange.dateFrom);
    setDateTo(searchParams.get("dateTo") || defaultRange.dateTo);
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
  }, [searchParams, defaultRange.dateFrom, defaultRange.dateTo]);

  const applyPeriod = (value: string) => {
    setPeriod(value);
    if (value === PERIOD_LAST_12_MONTHS || value === PERIOD_CURRENT_FY) {
      const range = periodRange(value);
      setDateFrom(range.dateFrom);
      setDateTo(range.dateTo);
    }
  };

  // Monotonic request id so a slow earlier response can never overwrite the
  // result for the current filters.
  const requestIdRef = useRef(0);

  const load = useCallback(async () => {
    const requestId = ++requestIdRef.current;
    setLoading(true);
    setError("");
    try {
      const params: QueryParams = {};
      if (accountId) params.accountId = accountId;
      if (dateFrom) params.dateFrom = dateFrom;
      if (dateTo) params.dateTo = dateTo;
      const res = await api.getCashFlowCalendar(params);
      if (requestId !== requestIdRef.current) return;
      setData(res);
    } catch (err) {
      if (requestId !== requestIdRef.current) return;
      setError((err as Error).message || "Failed to load the calendar");
    } finally {
      if (requestId === requestIdRef.current) setLoading(false);
    }
  }, [accountId, dateFrom, dateTo]);

  useEffect(() => {
    void load();
  }, [load]);

  const dayMap = useMemo(
    () => new Map((data?.days ?? []).map((d) => [d.date, d])),
    [data],
  );

  const markerMap = useMemo(() => {
    const map = new Map<string, CashFlowCalendarMarker[]>();
    for (const marker of data?.markers ?? []) {
      const list = map.get(marker.date) ?? [];
      list.push(marker);
      map.set(marker.date, list);
    }
    return map;
  }, [data]);

  const weeks = useMemo(
    () => buildWeeks(dateFrom, dateTo, dayMap, markerMap),
    [dateFrom, dateTo, dayMap, markerMap],
  );

  const cycleStartCols = useMemo(
    () => findCycleStartColumns(weeks, data?.cycles ?? []),
    [weeks, data],
  );

  const handleEnter = (e: MouseEvent<HTMLDivElement>, cell: DayCell) => {
    const grid = gridRef.current;
    if (!grid) return;
    const cellRect = e.currentTarget.getBoundingClientRect();
    const gridRect = grid.getBoundingClientRect();
    setHover({
      cell,
      left: cellRect.left - gridRect.left + cellRect.width / 2,
      top: cellRect.bottom - gridRect.top + 6,
    });
  };

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

  const hasOverlays = data.markers.length > 0 || data.cycles.length > 0;

  return (
    <>
      <div className="shrink-0 px-8 pt-6">
        <h1 className="text-2xl font-bold mb-1">Cash Flow Calendar</h1>
        <p className="text-muted-foreground text-sm">
          Daily net flow across the year, with billing cycles and summary rows
          overlaid
        </p>
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
            value={data.net}
            icon={<Waypoints size={22} className="text-primary" />}
            iconClass="bg-primary/10"
            valueClass={data.net >= 0 ? "text-emerald-500" : "text-destructive"}
            compact={compactLayout}
          />
        </div>

        <Card size={compactLayout ? "sm" : "default"}>
          <CardHeader
            className={`flex flex-row items-center justify-between ${compactLayout ? "mb-3" : "mb-5"}`}
          >
            <CardTitle className="flex items-center gap-2">
              <CalendarDays className="text-primary" size={18} />
              Daily Net Flow
            </CardTitle>
            <span className="text-xs text-muted-foreground">
              {formatDate(dateFrom)} – {formatDate(dateTo)}
            </span>
          </CardHeader>
          <CardContent>
            {weeks.length === 0 ? (
              <div className="flex flex-col items-center justify-center py-16 text-muted-foreground text-center">
                <p>Select a valid date range to see the calendar.</p>
              </div>
            ) : (
              <>
                <div className="relative" ref={gridRef}>
                  <div className="overflow-x-auto pb-1">
                    <div
                      className="grid min-w-max gap-1"
                      style={{
                        gridTemplateColumns: `28px repeat(${weeks.length}, 14px)`,
                      }}
                    >
                      {/* Month labels */}
                      <div />
                      {weeks.map((week, i) => (
                        <div
                          key={`month-${i}`}
                          className="h-4 whitespace-nowrap text-[10px] leading-4 text-muted-foreground"
                        >
                          {week.monthLabel ?? ""}
                        </div>
                      ))}

                      {/* Billing-cycle boundaries */}
                      <div />
                      {weeks.map((_, i) => {
                        const cycle = cycleStartCols.get(i);
                        return (
                          <div
                            key={`cycle-${i}`}
                            className={`relative h-3.5 ${cycle ? "border-l border-primary/50" : ""}`}
                          >
                            {cycle && (
                              <span className="absolute left-0.5 top-0 whitespace-nowrap text-[10px] leading-[14px] text-muted-foreground">
                                {cycle.label}
                              </span>
                            )}
                          </div>
                        );
                      })}

                      {/* Heatmap rows, Monday-first */}
                      {WEEKDAYS.map((day, row) => (
                        <Fragment key={day}>
                          <div className="h-3.5 pr-1.5 text-right text-[10px] leading-[14px] text-muted-foreground">
                            {row % 2 === 0 ? day : ""}
                          </div>
                          {weeks.map((week, i) => {
                            const cell = week.cells[row];
                            return (
                              <DayCellView
                                key={`${i}-${row}`}
                                cell={cell}
                                maxAbs={data.maxAbsNet}
                                selected={selected?.date === cell.date}
                                onEnter={handleEnter}
                                onLeave={() => setHover(null)}
                                onSelect={setSelected}
                              />
                            );
                          })}
                        </Fragment>
                      ))}
                    </div>
                  </div>

                  {hover && (
                    <div
                      role="tooltip"
                      className="pointer-events-none absolute z-20 -translate-x-1/2 rounded-md border border-border bg-popover px-3 py-2 text-xs text-popover-foreground shadow-md"
                      style={{ left: hover.left, top: hover.top }}
                    >
                      <div className="mb-1 font-medium">
                        {formatDate(hover.cell.date)}
                      </div>
                      <div className="space-y-0.5">
                        <TooltipRow
                          label="In"
                          value={
                            <span className="text-emerald-500">
                              {formatCurrency(hover.cell.income)}
                            </span>
                          }
                        />
                        <TooltipRow
                          label="Out"
                          value={
                            <span className="text-destructive">
                              {formatCurrency(hover.cell.expense)}
                            </span>
                          }
                        />
                        <TooltipRow
                          label="Net"
                          value={
                            <span
                              className={
                                hover.cell.net >= 0
                                  ? "text-emerald-500"
                                  : "text-destructive"
                              }
                            >
                              {formatCurrency(hover.cell.net)}
                            </span>
                          }
                        />
                        {hover.cell.count > 0 && (
                          <div className="pt-0.5 text-muted-foreground">
                            {hover.cell.count}{" "}
                            {hover.cell.count === 1
                              ? "transaction"
                              : "transactions"}
                          </div>
                        )}
                        {hover.cell.markers.map((marker) => (
                          <div key={marker.kind} className="text-muted-foreground">
                            {marker.label}: {formatCurrency(marker.amount)}
                          </div>
                        ))}
                      </div>
                    </div>
                  )}
                </div>

                <div className="mt-4 flex flex-wrap items-center gap-x-5 gap-y-2 text-xs text-muted-foreground">
                  <span className="flex items-center gap-1.5">
                    <span>Deficit</span>
                    <span
                      className="h-3 w-3 rounded-[2px]"
                      style={{ backgroundColor: "var(--destructive)" }}
                    />
                    <span
                      className="h-3 w-3 rounded-[2px]"
                      style={{ backgroundColor: "var(--muted)" }}
                    />
                    <span
                      className="h-3 w-3 rounded-[2px]"
                      style={{ backgroundColor: "var(--chart-3)" }}
                    />
                    <span>Surplus</span>
                  </span>
                  {hasOverlays && (
                    <span className="flex items-center gap-1.5">
                      <span className="h-3 w-3 rounded-[2px] ring-1 ring-foreground/50" />
                      Summary row
                    </span>
                  )}
                  {data.cycles.length > 0 && (
                    <span className="flex items-center gap-1.5">
                      <span className="h-3 w-3 border-l border-primary/50" />
                      Billing cycle
                    </span>
                  )}
                </div>

                {data.days.length === 0 && (
                  <p className="mt-4 text-center text-sm text-muted-foreground">
                    No transactions in this range.
                  </p>
                )}

                {selected && (
                  <div className="mt-4 flex flex-wrap items-center gap-x-6 gap-y-1 rounded-lg border border-border px-3 py-2 text-sm">
                    <span className="font-medium">{formatDate(selected.date)}</span>
                    <span className="text-muted-foreground">
                      In{" "}
                      <span className="font-medium text-emerald-500">
                        {formatCurrency(selected.income)}
                      </span>
                    </span>
                    <span className="text-muted-foreground">
                      Out{" "}
                      <span className="font-medium text-destructive">
                        {formatCurrency(selected.expense)}
                      </span>
                    </span>
                    <span className="text-muted-foreground">
                      Net{" "}
                      <span
                        className={`font-medium ${
                          selected.net >= 0
                            ? "text-emerald-500"
                            : "text-destructive"
                        }`}
                      >
                        {formatCurrency(selected.net)}
                      </span>
                    </span>
                    {selected.count > 0 && (
                      <span className="text-muted-foreground">
                        {selected.count}{" "}
                        {selected.count === 1 ? "transaction" : "transactions"}
                      </span>
                    )}
                    {selected.markers.map((marker) => (
                      <span key={marker.kind} className="text-muted-foreground">
                        {marker.label}: {formatCurrency(marker.amount)}
                      </span>
                    ))}
                  </div>
                )}
              </>
            )}
          </CardContent>
        </Card>
      </div>
    </>
  );
}

function DayCellView({
  cell,
  maxAbs,
  selected,
  onEnter,
  onLeave,
  onSelect,
}: {
  cell: DayCell;
  maxAbs: number;
  selected: boolean;
  onEnter: (e: MouseEvent<HTMLDivElement>, cell: DayCell) => void;
  onLeave: () => void;
  onSelect: (cell: DayCell) => void;
}) {
  if (!cell.inRange) {
    return <div className="h-3.5 rounded-[2px]" aria-hidden />;
  }

  const hasData = cell.count > 0;
  const hasMarker = cell.markers.length > 0;
  return (
    <div
      role="gridcell"
      aria-label={cellLabel(cell)}
      className={`h-3.5 rounded-[2px] ${
        hasData ? "cursor-pointer hover:ring-1 hover:ring-foreground/40" : ""
      } ${hasMarker ? "ring-1 ring-foreground/50" : ""} ${
        selected ? "outline outline-2 outline-foreground" : ""
      }`}
      style={dayBackground(cell.net, maxAbs, hasData)}
      onMouseEnter={(e) => onEnter(e, cell)}
      onMouseLeave={onLeave}
      onClick={() => onSelect(cell)}
    />
  );
}

function TooltipRow({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-4">
      <span className="text-muted-foreground">{label}</span>
      <span>{value}</span>
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
