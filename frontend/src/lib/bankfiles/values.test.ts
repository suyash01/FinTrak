import { describe, it, expect } from "vitest";
import { majorFromMinor, parseMinorUnits, normalizeBankDate } from "./values";

// The readers must never round a bank figure. A bank's amount is an exact
// decimal in minor units, so the grammar is deliberately strict: one optional
// leading sign, digits on both sides of the point, at most two fraction digits.
// This mirrors backend/internal/money.Parse, so a client cannot submit what the
// API would refuse.
describe("parseMinorUnits", () => {
  it("parses a plain decimal into exact minor units", () => {
    expect(parseMinorUnits("1234.56")).toBe(123456);
    expect(parseMinorUnits("0.05")).toBe(5);
    expect(parseMinorUnits("0")).toBe(0);
    expect(parseMinorUnits("1000")).toBe(100000);
  });

  it("parses an explicit sign", () => {
    expect(parseMinorUnits("-42.50")).toBe(-4250);
    expect(parseMinorUnits("+42.50")).toBe(4250);
  });

  it("pads a single fraction digit", () => {
    expect(parseMinorUnits("7.5")).toBe(750);
  });

  it("rejects three fraction digits rather than rounding", () => {
    // parseFloat would silently return 1.234, and the row would land in the
    // ledger at a figure the bank never reported.
    expect(parseMinorUnits("1.234")).toBeNull();
    expect(parseMinorUnits("0.005")).toBeNull();
  });

  it("rejects a value that is missing digits on either side of the point", () => {
    expect(parseMinorUnits(".5")).toBeNull();
    expect(parseMinorUnits("5.")).toBeNull();
    expect(parseMinorUnits(".")).toBeNull();
  });

  it("rejects a bare or repeated sign", () => {
    expect(parseMinorUnits("-")).toBeNull();
    expect(parseMinorUnits("--1")).toBeNull();
    expect(parseMinorUnits("+-1")).toBeNull();
  });

  it("rejects an empty or whitespace value", () => {
    expect(parseMinorUnits("")).toBeNull();
    expect(parseMinorUnits("   ")).toBeNull();
  });

  it("rejects a grouped amount, which these formats never emit", () => {
    // leniency here would be a bug, not a kindness: a camt/OFX figure is
    // machine-written and "1,234.56" means the file is not what we think it is.
    expect(parseMinorUnits("1,234.56")).toBeNull();
  });

  it("rejects anything that is not a plain decimal", () => {
    expect(parseMinorUnits("INR 500")).toBeNull();
    expect(parseMinorUnits("₹500.00")).toBeNull();
    expect(parseMinorUnits("NaN")).toBeNull();
    expect(parseMinorUnits("Infinity")).toBeNull();
    expect(parseMinorUnits("1e3")).toBeNull();
  });

  it("rejects a value beyond what a float64 can carry exactly", () => {
    // The bound here is TIGHTER than the backend's money.MaxMinorUnits (1<<62).
    // A stored figure only has to fit a BIGINT, but this client has to carry it
    // through ImportTransaction.amount, which is a float64: above 2^53 the
    // integer minor units are no longer exactly representable, so accepting one
    // would corrupt it before the API ever saw it. 2^53-1 minor units is
    // therefore the first figure that cannot be rounded on the way through.
    expect(parseMinorUnits("90071992547409.92")).toBeNull();
    expect(parseMinorUnits("90071992547409.91")).toBe(9007199254740991);
  });
});

describe("majorFromMinor", () => {
  it("converts minor units to decimal major units", () => {
    expect(majorFromMinor(123456)).toBe(1234.56);
    expect(majorFromMinor(5)).toBe(0.05);
    expect(majorFromMinor(-4250)).toBe(-42.5);
    expect(majorFromMinor(0)).toBe(0);
  });
});

describe("normalizeBankDate", () => {
  it("passes an ISO date through", () => {
    expect(normalizeBankDate("2026-05-18")).toBe("2026-05-18");
  });

  it("keeps the date part of a full ISO timestamp", () => {
    expect(normalizeBankDate("2026-05-18T00:00:00+05:30")).toBe("2026-05-18");
    expect(normalizeBankDate("2026-05-18T10:11:12Z")).toBe("2026-05-18");
  });

  it("keeps the date part of a camt offset date with no time", () => {
    expect(normalizeBankDate("2026-05-18+05:30")).toBe("2026-05-18");
  });

  it("expands the compact YYYYMMDD form OFX uses", () => {
    expect(normalizeBankDate("20260518")).toBe("2026-05-18");
    expect(normalizeBankDate("20260518101112")).toBe("2026-05-18");
  });

  it("rejects a date that is not a real calendar day", () => {
    expect(normalizeBankDate("2026-02-30")).toBeNull();
    expect(normalizeBankDate("2026-13-01")).toBeNull();
    expect(normalizeBankDate("2026-00-10")).toBeNull();
  });

  it("rejects a value that is not a date at all", () => {
    expect(normalizeBankDate("")).toBeNull();
    expect(normalizeBankDate("   ")).toBeNull();
    expect(normalizeBankDate("18/05/2026")).toBeNull();
    expect(normalizeBankDate("2026-5-18")).toBeNull();
    expect(normalizeBankDate("00000000")).toBeNull();
  });
});
