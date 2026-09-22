// Shared date helpers for the pages that default a field to "today" and for the
// reporting period presets Dashboard, Money Flow and the Cash Flow Calendar all
// offer. One module, because the alternative — the local-day formatter and the
// April-1 fiscal-year rule copied per page — let the three pages disagree about
// the same period and about which day "today" is.

// toLocalISODate renders a Date's *local* calendar day as YYYY-MM-DD. The day a
// ledger entry belongs to is a local-calendar fact: `toISOString()` is UTC, so
// east of UTC (the app's INR/en-IN target is +05:30) it names yesterday for the
// first hours of every local day — a transaction created at 01:00 IST would be
// dated the previous day, a recurring series would start a day early and a
// balance-transfer payoff would be quoted a day short. Every default "today"
// must come from here, never from `new Date().toISOString()`.
export function toLocalISODate(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

// todayLocalISO is the local day right now, for pre-filling date inputs.
export function todayLocalISO(): string {
  return toLocalISODate(new Date());
}

export interface DateRange {
  dateFrom: string;
  dateTo: string;
}

// Period presets. The values are part of the pages' URLs (`?period=`), so they
// are stable strings rather than an enum.
export const PERIOD_LAST_12_MONTHS = "last_12_months";
export const PERIOD_CURRENT_FY = "current_fy";
export const PERIOD_CUSTOM = "custom";
export const PERIOD_VALUES = [
  PERIOD_LAST_12_MONTHS,
  PERIOD_CURRENT_FY,
  PERIOD_CUSTOM,
];

// The rolling window every period page opens on: today and the twelve months
// before it (the from-day is the day after the same date last year, so the
// window is exactly twelve months wide).
export function lastTwelveMonthsRange(): DateRange {
  const to = new Date();
  const from = new Date(to.getFullYear(), to.getMonth() - 12, to.getDate() + 1);
  return { dateFrom: toLocalISODate(from), dateTo: toLocalISODate(to) };
}

// The Indian fiscal year: 1 April to 31 March. April is month index 3, so the
// year rolls over at the start of April, not January.
export function currentFinancialYearRange(): DateRange {
  const now = new Date();
  const startYear =
    now.getMonth() >= 3 ? now.getFullYear() : now.getFullYear() - 1;
  return {
    dateFrom: toLocalISODate(new Date(startYear, 3, 1)),
    dateTo: toLocalISODate(new Date(startYear + 1, 2, 31)),
  };
}

// periodRange resolves a preset to its dates. A custom period has no computed
// range, so anything that is not the fiscal year falls back to the rolling
// twelve months.
export function periodRange(period: string): DateRange {
  return period === PERIOD_CURRENT_FY
    ? currentFinancialYearRange()
    : lastTwelveMonthsRange();
}
