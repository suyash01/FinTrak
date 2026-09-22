import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import {
  PERIOD_CURRENT_FY,
  PERIOD_LAST_12_MONTHS,
  PERIOD_VALUES,
  currentFinancialYearRange,
  lastTwelveMonthsRange,
  periodRange,
  toLocalISODate,
  todayLocalISO,
} from "./dates";

describe("toLocalISODate", () => {
  it("names the local calendar day, never the UTC day", () => {
    // The default "today" of a new transaction is a local-calendar fact. The
    // old `new Date().toISOString().slice(0, 10)` is UTC, so east of UTC (the
    // app's +05:30 target) the first hours of a local day name yesterday, and
    // west of UTC the last hours name tomorrow. Both edges are asserted with
    // instants built from local parts, so exactly one of them catches a
    // UTC-based implementation in any host zone that is not UTC itself.
    const earlyLocal = new Date(2026, 2, 15, 0, 30);
    const lateLocal = new Date(2026, 2, 15, 23, 30);

    expect(toLocalISODate(earlyLocal)).toBe("2026-03-15");
    expect(toLocalISODate(lateLocal)).toBe("2026-03-15");
  });

  it("pads single-digit months and days", () => {
    expect(toLocalISODate(new Date(2026, 0, 4, 12, 0))).toBe("2026-01-04");
  });

  it("todayLocalISO is that formatter applied to now", () => {
    expect(todayLocalISO()).toBe(toLocalISODate(new Date()));
  });
});

describe("reporting periods", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("offers the three presets the period pages share", () => {
    expect(PERIOD_VALUES).toEqual([
      PERIOD_LAST_12_MONTHS,
      PERIOD_CURRENT_FY,
      "custom",
    ]);
  });

  it("spans the twelve months up to today", () => {
    vi.setSystemTime(new Date(2026, 4, 15, 10, 0));

    expect(lastTwelveMonthsRange()).toEqual({
      dateFrom: "2025-05-16",
      dateTo: "2026-05-15",
    });
  });

  it("runs the fiscal year from 1 April to 31 March", () => {
    vi.setSystemTime(new Date(2026, 4, 15, 10, 0));
    expect(currentFinancialYearRange()).toEqual({
      dateFrom: "2026-04-01",
      dateTo: "2027-03-31",
    });

    // Before April the fiscal year is still the one that started last April.
    vi.setSystemTime(new Date(2026, 2, 31, 10, 0));
    expect(currentFinancialYearRange()).toEqual({
      dateFrom: "2025-04-01",
      dateTo: "2026-03-31",
    });
  });

  it("resolves a preset and falls back to the rolling window", () => {
    vi.setSystemTime(new Date(2026, 7, 2, 10, 0));

    expect(periodRange(PERIOD_CURRENT_FY)).toEqual(currentFinancialYearRange());
    expect(periodRange(PERIOD_LAST_12_MONTHS)).toEqual(lastTwelveMonthsRange());
    // A custom range is supplied by the user, so there is nothing to compute.
    expect(periodRange("custom")).toEqual(lastTwelveMonthsRange());
  });
});
