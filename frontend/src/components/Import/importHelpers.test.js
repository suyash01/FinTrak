import { describe, it, expect } from "vitest";
import {
  targetFieldsFor,
  parseDateExplicit,
  parseDateAuto,
  parseDate,
  parseAmount,
  getMappingErrors,
  fingerprintOf,
  apiDate,
  buildParsedTransactions,
  autoDetectMapping,
} from "./Import";

describe("targetFieldsFor", () => {
  it("includes the single amount field in single mode", () => {
    const fields = targetFieldsFor("single");
    expect(fields.map((f) => f.key)).toContain("amount");
    expect(fields.map((f) => f.key)).not.toContain("debit");
    expect(fields.map((f) => f.key)).not.toContain("credit");
  });

  it("includes debit/credit fields in separate mode", () => {
    const fields = targetFieldsFor("separate");
    expect(fields.map((f) => f.key)).toContain("debit");
    expect(fields.map((f) => f.key)).toContain("credit");
    expect(fields.map((f) => f.key)).not.toContain("amount");
  });
});

describe("parseAmount", () => {
  it("parses plain numbers", () => {
    expect(parseAmount("1234.56")).toBe(1234.56);
    expect(parseAmount("42")).toBe(42);
  });

  it("parses numbers with thousands separators", () => {
    expect(parseAmount("1,234.56")).toBe(1234.56);
    expect(parseAmount("12,34,567.89")).toBe(1234567.89);
  });

  it("parses European-style numbers (dot thousands, comma decimal)", () => {
    expect(parseAmount("1.234,56")).toBe(1234.56);
    expect(parseAmount("1.234.567,89")).toBe(1234567.89);
  });

  it("parses parenthesised negatives", () => {
    expect(parseAmount("(1,234.56)")).toBe(-1234.56);
  });

  it("parses trailing-minus negatives", () => {
    expect(parseAmount("1,234.56-")).toBe(-1234.56);
  });

  it("strips currency symbols and whitespace", () => {
    expect(parseAmount("₹1,234.56")).toBe(1234.56);
    expect(parseAmount(" 1,234.56 ")).toBe(1234.56);
  });

  it("handles numeric input directly", () => {
    expect(parseAmount(42)).toBe(42);
    expect(parseAmount(-7.5)).toBe(-7.5);
  });

  it("returns 0 for empty or non-numeric input", () => {
    expect(parseAmount("")).toBe(0);
    expect(parseAmount(null)).toBe(0);
    expect(parseAmount(undefined)).toBe(0);
    expect(parseAmount("abc")).toBe(0);
  });
});

describe("parseDateExplicit", () => {
  it("parses DD/MM/YYYY", () => {
    expect(parseDateExplicit("15/03/2024", "DD/MM/YYYY")).toBe("2024-03-15");
  });

  it("parses MM/DD/YYYY", () => {
    expect(parseDateExplicit("03/15/2024", "MM/DD/YYYY")).toBe("2024-03-15");
  });

  it("parses DD/MM/YY with century inference", () => {
    expect(parseDateExplicit("15/03/24", "DD/MM/YY")).toBe("2024-03-15");
    expect(parseDateExplicit("15/03/99", "DD/MM/YY")).toBe("1999-03-15");
  });

  it("parses YYYY-MM-DD", () => {
    expect(parseDateExplicit("2024-03-15", "YYYY-MM-DD")).toBe("2024-03-15");
  });

  it("parses DD Mon YYYY", () => {
    expect(parseDateExplicit("15 Mar 2024", "DD Mon YYYY")).toBe("2024-03-15");
    expect(parseDateExplicit("15 March 2024", "DD Mon YYYY")).toBe(
      "2024-03-15",
    );
  });

  it("returns null for mismatched input", () => {
    expect(parseDateExplicit("15/03/2024", "YYYY-MM-DD")).toBeNull();
    expect(parseDateExplicit("garbage", "DD/MM/YYYY")).toBeNull();
  });
});

describe("parseDateAuto", () => {
  it("detects DD/MM/YYYY", () => {
    expect(parseDateAuto("15/03/2024")).toBe("2024-03-15");
  });

  it("detects YYYY-MM-DD", () => {
    expect(parseDateAuto("2024-03-15")).toBe("2024-03-15");
  });

  it("detects DD/MM/YY with century inference", () => {
    expect(parseDateAuto("15/03/24")).toBe("2024-03-15");
    expect(parseDateAuto("15/03/99")).toBe("1999-03-15");
  });

  it("detects DD Mon YYYY", () => {
    expect(parseDateAuto("15 Mar 2024")).toBe("2024-03-15");
  });

  it("falls back to the Date parser for ISO timestamps", () => {
    expect(parseDateAuto("2024-03-15T10:30:00Z")).toBe("2024-03-15");
  });

  it("returns null for unparseable input", () => {
    expect(parseDateAuto("not a date")).toBeNull();
  });
});

describe("parseDate", () => {
  it("uses the explicit format when provided", () => {
    expect(parseDate("15/03/2024", "DD/MM/YYYY")).toBe("2024-03-15");
  });

  it("falls back to auto-detection when the explicit format fails", () => {
    expect(parseDate("2024-03-15", "DD/MM/YYYY")).toBe("2024-03-15");
  });

  it("uses auto-detection for the auto format", () => {
    expect(parseDate("15/03/2024", "auto")).toBe("2024-03-15");
  });

  it("returns null for empty input", () => {
    expect(parseDate("", "auto")).toBeNull();
    expect(parseDate(null, "auto")).toBeNull();
  });
});

describe("getMappingErrors", () => {
  it("flags missing required fields in single mode", () => {
    const errors = getMappingErrors({}, "single");
    expect(errors).toContain("Date field must be mapped to a CSV column");
    expect(errors).toContain(
      "Description field must be mapped to a CSV column",
    );
    expect(errors).toContain(
      "Amount field must be mapped (or switch to separate Debit/Credit mode)",
    );
  });

  it("flags missing debit/credit in separate mode", () => {
    const errors = getMappingErrors({}, "separate");
    expect(errors).toContain("Debit field must be mapped in separate mode");
    expect(errors).toContain("Credit field must be mapped in separate mode");
  });

  it("returns no errors for a complete single mapping", () => {
    const errors = getMappingErrors(
      { date: "Date", description: "Desc", amount: "Amount" },
      "single",
    );
    expect(errors).toEqual([]);
  });

  it("returns no errors for a complete separate mapping", () => {
    const errors = getMappingErrors(
      { date: "Date", description: "Desc", debit: "Debit", credit: "Credit" },
      "separate",
    );
    expect(errors).toEqual([]);
  });
});

describe("fingerprintOf", () => {
  it("produces identical fingerprints for identical transactions", () => {
    expect(fingerprintOf("2024-03-15", 100, "debit", "Coffee")).toBe(
      fingerprintOf("2024-03-15", 100, "debit", "Coffee"),
    );
  });

  it("normalises description case and whitespace", () => {
    expect(fingerprintOf("2024-03-15", 100, "debit", "  Coffee  ")).toBe(
      fingerprintOf("2024-03-15", 100, "debit", "coffee"),
    );
  });

  it("distinguishes different amounts", () => {
    expect(fingerprintOf("2024-03-15", 100, "debit", "Coffee")).not.toBe(
      fingerprintOf("2024-03-15", 101, "debit", "Coffee"),
    );
  });

  it("distinguishes different types", () => {
    expect(fingerprintOf("2024-03-15", 100, "debit", "Coffee")).not.toBe(
      fingerprintOf("2024-03-15", 100, "credit", "Coffee"),
    );
  });
});

describe("apiDate", () => {
  it("extracts the date portion of an ISO timestamp", () => {
    expect(apiDate("2024-03-15T00:00:00Z")).toBe("2024-03-15");
  });

  it("passes through a plain date string", () => {
    expect(apiDate("2024-03-15")).toBe("2024-03-15");
  });

  it("returns empty string for empty input", () => {
    expect(apiDate("")).toBe("");
    expect(apiDate(null)).toBe("");
    expect(apiDate(undefined)).toBe("");
  });
});

describe("buildParsedTransactions", () => {
  const base = {
    columnMapping: {
      date: "Date",
      description: "Description",
      amount: "Amount",
    },
    amountMode: "single",
    dateFormat: "auto",
    accounts: [{ id: "acct1", accountTypeId: "bank" }],
    accountTypes: [{ id: "bank", positiveTxnType: "credit" }],
    payees: [],
    selectedAccount: "acct1",
  };

  it("returns an empty array when there is no CSV data", () => {
    expect(buildParsedTransactions({ ...base, csvData: null })).toEqual([]);
  });

  it("maps a positive amount to the account type positive transaction type", () => {
    const result = buildParsedTransactions({
      ...base,
      csvData: [{ Date: "15/03/2024", Description: "Salary", Amount: "50000" }],
    });
    expect(result).toEqual([
      {
        date: "2024-03-15",
        description: "Salary",
        amount: 50000,
        type: "credit",
        payeeId: null,
      },
    ]);
  });

  it("maps a negative amount to a debit", () => {
    const result = buildParsedTransactions({
      ...base,
      csvData: [{ Date: "15/03/2024", Description: "Rent", Amount: "-12000" }],
    });
    expect(result[0]).toMatchObject({ amount: 12000, type: "debit" });
  });

  it("handles separate debit/credit columns", () => {
    const result = buildParsedTransactions({
      ...base,
      amountMode: "separate",
      columnMapping: {
        date: "Date",
        description: "Description",
        debit: "Debit",
        credit: "Credit",
      },
      csvData: [
        { Date: "15/03/2024", Description: "Coffee", Debit: "150", Credit: "" },
        { Date: "16/03/2024", Description: "Refund", Debit: "", Credit: "250" },
      ],
    });
    expect(result).toEqual([
      {
        date: "2024-03-15",
        description: "Coffee",
        amount: 150,
        type: "debit",
        payeeId: null,
      },
      {
        date: "2024-03-16",
        description: "Refund",
        amount: 250,
        type: "credit",
        payeeId: null,
      },
    ]);
  });

  it("matches payees case-insensitively", () => {
    const result = buildParsedTransactions({
      ...base,
      payees: [{ id: "p1", name: "Swiggy" }],
      columnMapping: {
        date: "Date",
        description: "Description",
        amount: "Amount",
        payee: "Payee",
      },
      csvData: [
        {
          Date: "15/03/2024",
          Description: "Order",
          Amount: "200",
          Payee: "swiggy",
        },
      ],
    });
    expect(result[0].payeeId).toBe("p1");
  });

  it("skips rows with missing dates, descriptions, or zero amounts", () => {
    const result = buildParsedTransactions({
      ...base,
      csvData: [
        { Date: "", Description: "No date", Amount: "100" },
        { Date: "15/03/2024", Description: "", Amount: "100" },
        { Date: "15/03/2024", Description: "Zero", Amount: "0" },
        { Date: "15/03/2024", Description: "Valid", Amount: "100" },
      ],
    });
    expect(result).toHaveLength(1);
    expect(result[0].description).toBe("Valid");
  });
});

describe("autoDetectMapping", () => {
  it("detects common bank statement headers", () => {
    const mapping = autoDetectMapping(["Date", "Narration", "Amount"]);
    expect(mapping).toEqual({
      date: "Date",
      description: "Narration",
      amount: "Amount",
      debit: null,
      credit: null,
      payee: null,
    });
  });

  it("detects separate debit/credit columns", () => {
    const mapping = autoDetectMapping([
      "Transaction Date",
      "Description",
      "Debit",
      "Credit",
    ]);
    expect(mapping.date).toBe("Transaction Date");
    expect(mapping.description).toBe("Description");
    expect(mapping.debit).toBe("Debit");
    expect(mapping.credit).toBe("Credit");
  });

  it("does not reuse a column for two fields", () => {
    const mapping = autoDetectMapping(["Date", "Amount"]);
    expect(mapping.date).toBe("Date");
    expect(mapping.amount).toBe("Amount");
    expect(mapping.description).toBeNull();
  });

  it("returns all nulls when nothing matches", () => {
    const mapping = autoDetectMapping(["Foo", "Bar"]);
    expect(mapping).toEqual({
      date: null,
      description: null,
      amount: null,
      debit: null,
      credit: null,
      payee: null,
    });
  });
});
