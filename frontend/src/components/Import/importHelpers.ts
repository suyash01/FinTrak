import { formatDateOnly } from "../../utils/formatters";
import type {
  Account,
  AccountType,
  ImportTransaction,
  Payee,
  TransactionType,
} from "../../types";

// Pure, framework-free helpers for the CSV/statement import flow: date and
// amount parsing, column-mapping detection/validation, duplicate fingerprinting,
// and CSV-row -> ImportTransaction conversion. Extracted from Import.tsx so the
// logic is unit-testable without mounting the (very large) wizard component.

export type CsvRow = Record<string, string>;

export interface ColumnMapping {
  date: string | null;
  description: string | null;
  amount: string | null;
  debit: string | null;
  credit: string | null;
  payee: string | null;
}

export interface TargetField {
  key: keyof ColumnMapping;
  label: string;
  required: boolean;
  mode?: string;
}

export const TARGET_FIELDS: TargetField[] = [
  { key: "date", label: "Date", required: true },
  { key: "description", label: "Description", required: true },
  { key: "amount", label: "Amount", required: true, mode: "single" },
  { key: "debit", label: "Debit Amount", required: true, mode: "separate" },
  { key: "credit", label: "Credit Amount", required: true, mode: "separate" },
  { key: "payee", label: "Payee", required: false },
];

export const targetFieldsFor = (amountMode: string): TargetField[] =>
  TARGET_FIELDS.filter((f) => !f.mode || f.mode === amountMode);

export const DATE_FORMAT_OPTIONS = [
  { value: "auto", label: "Auto-detect" },
  { value: "DD/MM/YYYY", label: "DD/MM/YYYY" },
  { value: "MM/DD/YYYY", label: "MM/DD/YYYY" },
  { value: "DD/MM/YY", label: "DD/MM/YY" },
  { value: "YYYY-MM-DD", label: "YYYY-MM-DD" },
  { value: "DD Mon YYYY", label: "DD Mon YYYY" },
];

const pad2 = (n: string | number) => String(n).padStart(2, "0");

const MONTHS: Record<string, string> = {
  jan: "01",
  feb: "02",
  mar: "03",
  apr: "04",
  may: "05",
  jun: "06",
  jul: "07",
  aug: "08",
  sep: "09",
  oct: "10",
  nov: "11",
  dec: "12",
};

const DATE_PATTERNS: Record<string, RegExp> = {
  "DD/MM/YYYY": /^(\d{1,2})[/\-.](\d{1,2})[/\-.](\d{4})$/,
  "MM/DD/YYYY": /^(\d{1,2})[/\-.](\d{1,2})[/\-.](\d{4})$/,
  "DD/MM/YY": /^(\d{1,2})[/\-.](\d{1,2})[/\-.](\d{2})$/,
  "YYYY-MM-DD": /^(\d{4})[/\-.](\d{1,2})[/\-.](\d{1,2})$/,
  "DD Mon YYYY":
    /^(\d{1,2})\s+(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)[a-z]*\s+(\d{4})$/i,
};

// isValidYmd rejects out-of-range month/day values (e.g. month 15) and
// impossible calendar dates (e.g. 31 Feb), so explicit and auto parsing never
// emit a string like "2024-15-03".
function isValidYmd(year: number, month: number, day: number): boolean {
  if (month < 1 || month > 12 || day < 1 || day > 31) return false;
  const d = new Date(Date.UTC(year, month - 1, day));
  return (
    d.getUTCFullYear() === year &&
    d.getUTCMonth() === month - 1 &&
    d.getUTCDate() === day
  );
}

function makeYmd(year: number, month: number, day: number): string | null {
  if (!isValidYmd(year, month, day)) return null;
  return `${year}-${pad2(month)}-${pad2(day)}`;
}

export function parseDateExplicit(str: string, format: string): string | null {
  const m = String(str).match(DATE_PATTERNS[format]);
  if (!m) return null;
  if (format === "DD/MM/YYYY")
    return makeYmd(Number(m[3]), Number(m[2]), Number(m[1]));
  if (format === "MM/DD/YYYY")
    return makeYmd(Number(m[3]), Number(m[1]), Number(m[2]));
  if (format === "YYYY-MM-DD")
    return makeYmd(Number(m[1]), Number(m[2]), Number(m[3]));
  if (format === "DD Mon YYYY")
    return makeYmd(
      Number(m[3]),
      Number(MONTHS[m[2].toLowerCase().substring(0, 3)]),
      Number(m[1]),
    );
  const year = parseInt(m[3]) > 50 ? 1900 + parseInt(m[3]) : 2000 + parseInt(m[3]);
  return makeYmd(year, Number(m[2]), Number(m[1]));
}

export function parseDateAuto(str: string): string | null {
  const s = String(str);
  let m = s.match(DATE_PATTERNS["DD/MM/YYYY"]);
  if (m) {
    const year = Number(m[3]);
    // Prefer DD/MM when it yields a real date; otherwise interpret the same
    // digits as MM/DD so US-style input like 03/15/2024 doesn't become an
    // impossible 2024-15-03.
    return makeYmd(year, Number(m[2]), Number(m[1])) ??
      makeYmd(year, Number(m[1]), Number(m[2]));
  }
  m = s.match(DATE_PATTERNS["YYYY-MM-DD"]);
  if (m) return makeYmd(Number(m[1]), Number(m[2]), Number(m[3]));
  m = s.match(DATE_PATTERNS["DD/MM/YY"]);
  if (m) {
    const year =
      parseInt(m[3]) > 50 ? 1900 + parseInt(m[3]) : 2000 + parseInt(m[3]);
    return makeYmd(year, Number(m[2]), Number(m[1])) ??
      makeYmd(year, Number(m[1]), Number(m[2]));
  }
  m = s.match(DATE_PATTERNS["DD Mon YYYY"]);
  if (m)
    return makeYmd(
      Number(m[3]),
      Number(MONTHS[m[2].toLowerCase().substring(0, 3)]),
      Number(m[1]),
    );
  const d = new Date(s);
  if (!isNaN(d.getTime())) return formatDateOnly(d);
  return null;
}

export function parseDate(
  str: string | null | undefined,
  format: string,
): string | null {
  if (!str) return null;
  const value = String(str).trim();
  if (format !== "auto") {
    const parsed = parseDateExplicit(value, format);
    if (parsed) return parsed;
  }
  return parseDateAuto(value);
}

export function parseAmount(str: string | number | null | undefined): number {
  if (str == null || str === "") return 0;
  if (typeof str === "number") return Number.isFinite(str) ? str : 0;

  const cleaned = String(str)
    .replace(/[^\d.,()+\-]/g, "")
    .trim();
  if (!cleaned) return 0;

  let negative = false;
  let body = cleaned;

  // Parenthesised negatives: (1,234.56)
  if (body.startsWith("(") && body.endsWith(")")) {
    negative = true;
    body = body.slice(1, -1);
  }
  // Trailing minus: 1,234.56-
  if (body.endsWith("-")) {
    negative = true;
    body = body.slice(0, -1);
  }

  // Leading sign: -1.234,56 / +56,78. Stripped before the separator decision so
  // the grouped-number patterns below only ever see digits.
  if (body.startsWith("-") || body.startsWith("+")) {
    if (body.startsWith("-")) negative = true;
    body = body.slice(1);
  }

  let parsed: number;
  if (/^\d{1,3}(\.\d{3})+(,\d+)?$/.test(body)) {
    // European style: 1.234.567,89
    parsed = parseFloat(body.replace(/\./g, "").replace(",", "."));
  } else if (/^\d+,\d{1,2}$/.test(body)) {
    // A lone comma followed by one or two digits is a decimal comma, not a
    // thousands separator: "56,78" is 56.78. Reading it as grouping multiplied
    // every ungrouped European amount under a thousand by 100.
    parsed = parseFloat(body.replace(",", "."));
  } else {
    // Remove thousands separators, use "." as decimal separator
    parsed = parseFloat(body.replace(/,/g, ""));
  }

  if (!Number.isFinite(parsed)) return 0;
  return negative ? -Math.abs(parsed) : parsed;
}

export const getMappingErrors = (
  columnMapping: Partial<ColumnMapping>,
  amountMode: string,
): string[] => {
  const errors: string[] = [];
  if (!columnMapping.date)
    errors.push("Date field must be mapped to a CSV column");
  if (!columnMapping.description)
    errors.push("Description field must be mapped to a CSV column");
  if (amountMode === "single" && !columnMapping.amount) {
    errors.push(
      "Amount field must be mapped (or switch to separate Debit/Credit mode)",
    );
  }
  if (amountMode === "separate") {
    if (!columnMapping.debit)
      errors.push("Debit field must be mapped in separate mode");
    if (!columnMapping.credit)
      errors.push("Credit field must be mapped in separate mode");
  }
  return errors;
};

// Duplicate detection mirrors the backend fingerprint so that the count the
// user sees matches what the import endpoint would skip.
const FINGERPRINT_SEP = "\x00";
// Upper bound on pages of existing-transaction history fetched for duplicate
// detection (100 pages × 1000 rows = 100k transactions). The loop normally
// stops at the server-reported page count; this guard only trips on a server
// reporting bogus pagination metadata.
export const MAX_EXISTING_FETCH_PAGES = 100;

export const fingerprintOf = (
  date: string,
  amount: number,
  type: string,
  description: string,
): string =>
  `${date}${FINGERPRINT_SEP}${Math.round(amount * 100)}${FINGERPRINT_SEP}${type}${FINGERPRINT_SEP}${String(
    description || "",
  )
    .trim()
    .toLowerCase()}`;

// Drop the transactions whose row indices are in `excluded`. Used by both the
// statement import (CSV/PDF) and Paperless import previews so an excluded row
// never reaches validate or import.
export const filterExcluded = <T,>(
  transactions: T[],
  excluded: Set<number>,
): T[] => transactions.filter((_, i) => !excluded.has(i));

// All row indices whose transaction is identical (same date, amount, type and
// description) to the given row. Exclusions are applied to the whole set so
// that unchecking one occurrence of a duplicated transaction also unchecks its
// twins — otherwise an identical row remaining in the file would still import,
// looking like the exclusion failed.
export const siblingIndices = (
  transactions: ImportTransaction[],
  index: number,
): number[] => {
  const t = transactions[index];
  if (!t) return [];
  const fp = fingerprintOf(t.date, t.amount, t.type, t.description);
  const out: number[] = [];
  transactions.forEach((tx, i) => {
    if (fingerprintOf(tx.date, tx.amount, tx.type, tx.description) === fp) {
      out.push(i);
    }
  });
  return out;
};

export const apiDate = (d: string | null | undefined): string => {
  const m = String(d || "").match(/^(\d{4})-(\d{2})-(\d{2})/);
  return m ? m[0] : "";
};

interface BuildParsedTransactionsArgs {
  csvData: CsvRow[] | null;
  columnMapping: Partial<ColumnMapping>;
  amountMode: string;
  dateFormat: string;
  accounts: Account[];
  accountTypes: AccountType[];
  payees: Payee[];
  selectedAccount: string;
}

export function buildParsedTransactions({
  csvData,
  columnMapping,
  amountMode,
  dateFormat,
  accounts,
  accountTypes,
  payees,
  selectedAccount,
}: BuildParsedTransactionsArgs): ImportTransaction[] {
  if (!csvData) return [];

  const dateCol = columnMapping.date;
  const descCol = columnMapping.description;
  const amountCol = columnMapping.amount;
  const debitCol = columnMapping.debit;
  const creditCol = columnMapping.credit;
  const payeeCol = columnMapping.payee;

  const selAcct = accounts.find((a) => a.id === selectedAccount);
  const selType = accountTypes.find((at) => at.id === selAcct?.accountTypeId);
  const positiveTxnType = selType?.positiveTxnType || "credit";

  return csvData
    .map((row): ImportTransaction | null => {
      const rawDate = dateCol ? row[dateCol]?.trim() : undefined;
      if (!rawDate) return null;

      const date = parseDate(rawDate, dateFormat);
      if (!date) return null;

      const description = descCol ? row[descCol]?.trim() || "" : "";
      if (!description) return null;

      let amount = 0;
      let type: TransactionType = "debit";

      // Determine sign convention from account type
      if (amountMode === "single" && amountCol) {
        const raw = parseAmount(row[amountCol]);
        if (raw < 0) {
          amount = Math.abs(raw);
          type = positiveTxnType === "credit" ? "debit" : "credit";
        } else {
          amount = raw;
          type = positiveTxnType as TransactionType;
        }
      } else if (amountMode === "separate") {
        const debitAmt = parseAmount(debitCol ? row[debitCol] : undefined);
        const creditAmt = parseAmount(creditCol ? row[creditCol] : undefined);
        if (debitAmt !== 0) {
          amount = Math.abs(debitAmt);
          type = "debit";
        } else if (creditAmt !== 0) {
          amount = Math.abs(creditAmt);
          type = "credit";
        } else {
          return null;
        }
      }

      if (amount === 0) return null;

      let payeeId: string | null = null;
      if (payeeCol && row[payeeCol]) {
        const name = row[payeeCol].trim().toLowerCase();
        const match = payees.find((p) => p.name.toLowerCase() === name);
        if (match) payeeId = match.id;
      }

      return { date, description, amount, type, payeeId };
    })
    .filter((t): t is ImportTransaction => Boolean(t));
}

export function autoDetectMapping(headers: string[]): ColumnMapping {
  const mapping: ColumnMapping = {
    date: null,
    description: null,
    amount: null,
    debit: null,
    credit: null,
    payee: null,
  };
  const used = new Set<string>();
  const pick = (patterns: RegExp[]): string | null => {
    for (const h of headers) {
      const lower = String(h).toLowerCase().trim();
      if (!used.has(h) && patterns.some((p) => p.test(lower))) {
        used.add(h);
        return h;
      }
    }
    return null;
  };

  mapping.date = pick([/date|txn.*date|transaction.*date|value.*date/i]);
  mapping.description = pick([
    /narration|description|particulars|details|remark/i,
  ]);
  mapping.amount = pick([/^amount$|^transaction.*amount$|^txn.*amount$/i]);
  mapping.debit = pick([/debit|withdrawal|dr/i]);
  mapping.credit = pick([/credit|deposit|cr/i]);
  mapping.payee = pick([/payee|beneficiary|merchant|receiver|sender/i]);
  return mapping;
}

// Cap the file size that PapaParse will read; larger files are rejected before
// parsing to avoid exhausting memory and locking up the tab.
export const MAX_CSV_BYTES = 20 * 1024 * 1024;
