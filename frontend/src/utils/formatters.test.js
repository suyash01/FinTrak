import { describe, it, expect } from "vitest";
import {
  formatCurrency,
  parseDateOnly,
  formatDateOnly,
  formatDate,
  formatDateShort,
} from "./formatters";

describe("formatCurrency", () => {
  it("formats positive amounts in INR", () => {
    expect(formatCurrency(1234.5)).toBe("₹1,234.50");
  });

  it("formats negative amounts", () => {
    expect(formatCurrency(-99.99)).toBe("-₹99.99");
  });

  it("formats zero", () => {
    expect(formatCurrency(0)).toBe("₹0.00");
  });

  it("always shows two fraction digits", () => {
    expect(formatCurrency(5)).toBe("₹5.00");
  });

  it("supports a custom currency", () => {
    expect(formatCurrency(10, "USD")).toContain("$");
  });
});

describe("parseDateOnly", () => {
  it("parses a YYYY-MM-DD string into a local Date", () => {
    const d = parseDateOnly("2024-03-05");
    expect(d).toBeInstanceOf(Date);
    expect(d.getFullYear()).toBe(2024);
    expect(d.getMonth()).toBe(2); // 0-based
    expect(d.getDate()).toBe(5);
  });

  it("ignores any time portion", () => {
    const d = parseDateOnly("2024-03-05T10:30:00Z");
    expect(d.getFullYear()).toBe(2024);
    expect(d.getMonth()).toBe(2);
    expect(d.getDate()).toBe(5);
  });

  it("returns null for empty input", () => {
    expect(parseDateOnly("")).toBeNull();
    expect(parseDateOnly(null)).toBeNull();
    expect(parseDateOnly(undefined)).toBeNull();
  });

  it("returns null for invalid input", () => {
    expect(parseDateOnly("not-a-date")).toBeNull();
    expect(parseDateOnly("2024/03/05")).toBeNull();
    expect(parseDateOnly("05-03-2024")).toBeNull();
  });
});

describe("formatDateOnly", () => {
  it("formats a Date as YYYY-MM-DD", () => {
    expect(formatDateOnly(new Date(2024, 2, 5))).toBe("2024-03-05");
  });

  it("pads month and day with leading zeros", () => {
    expect(formatDateOnly(new Date(2024, 0, 1))).toBe("2024-01-01");
  });

  it("returns empty string for invalid dates", () => {
    expect(formatDateOnly(new Date("invalid"))).toBe("");
    expect(formatDateOnly(null)).toBe("");
    expect(formatDateOnly("2024-01-01")).toBe("");
  });
});

describe("formatDate", () => {
  it("formats a date string for display", () => {
    expect(formatDate("2024-01-15")).toBe("15 Jan 2024");
  });

  it("returns empty string for invalid input", () => {
    expect(formatDate("")).toBe("");
    expect(formatDate("garbage")).toBe("");
    expect(formatDate(null)).toBe("");
  });
});

describe("formatDateShort", () => {
  it("formats a date string without the year", () => {
    expect(formatDateShort("2024-01-15")).toBe("15 Jan");
  });

  it("returns empty string for invalid input", () => {
    expect(formatDateShort("")).toBe("");
    expect(formatDateShort("garbage")).toBe("");
  });
});
