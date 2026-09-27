/**
 * Strict value parsing for bank-supplied files (camt.052/053, OFX/QFX).
 *
 * Both rules here mirror the backend on purpose. A bank's figure and a bank's
 * date are machine-written, so the grammar is narrow and a value that does not
 * fit is *dropped with a diagnostic* rather than coerced:
 *
 * - `parseMinorUnits` mirrors `internal/money.Parse`. It returns exact integer
 *   minor units, never a float, so no amount is ever rounded on the way in.
 * - `normalizeBankDate` accepts only what ISO 20022 and OFX actually emit.
 *
 * This is deliberately NOT `importHelpers.parseAmount`, which is the right
 * choice for a hand-edited CSV (tolerant of "(1,234.56)", "1.234,56", a
 * trailing minus) and the wrong one here: those heuristics would silently
 * invent a figure the bank never reported.
 */

// The largest amount this client can carry without rounding.
//
// Deliberately tighter than the backend's money.MaxMinorUnits (1<<62). A
// *stored* figure only has to fit a BIGINT, but this one also has to survive
// `ImportTransaction.amount`, which is a float64: above 2^53 the integer minor
// units stop being exactly representable, so accepting one would corrupt it
// before the API ever saw it.
const MAX_MINOR_UNITS = Number.MAX_SAFE_INTEGER;

// One optional leading sign, digits on both sides of the point, at most two
// fraction digits. No grouping separators, no exponents, no currency marks.
const DECIMAL = /^(-|\+)?(\d+)(?:\.(\d{1,2}))?$/;

// ISO 8601, optionally followed by a time and/or a zone offset, neither of
// which the date part depends on. camt writes "2026-05-18", "2026-05-18+05:30"
// (an offset with no time) and "2026-05-18T00:00:00+05:30"; OFX writes
// "20260518" and "20260518101112".
const ISO_DATE = /^(\d{4})-(\d{2})-(\d{2})(?:[T ].*|[+-]\d{2}:?\d{2})?$/;
const COMPACT_DATE = /^(\d{4})(\d{2})(\d{2})(?:\d{0,6})?$/;

/**
 * Parse a bank-supplied decimal into exact integer minor units, or null when
 * the text is not one. Null means "drop this row and say so", never "zero" and
 * never a rounded value.
 */
export function parseMinorUnits(text: string | null | undefined): number | null {
  if (text == null) return null;
  const match = DECIMAL.exec(String(text).trim());
  if (!match) return null;

  const [, sign, whole, fraction = ""] = match;
  // `fraction` is at most two digits, so padding is a slice, not a numeric
  // divide: "5" must become 50 paise, not 5 paise.
  const minor =
    Number(whole) * 100 + Number(fraction.padEnd(2, "0"));
  const signed = sign === "-" ? -minor : minor;

  // A figure past the bound would wrap on the way into the BIGINT column and
  // then fail to marshal, taking every response containing that row with it.
  if (!Number.isSafeInteger(signed)) return null;
  if (Math.abs(signed) > MAX_MINOR_UNITS) return null;
  return signed;
}

/** Minor units to the decimal major units the API speaks. */
export function majorFromMinor(minor: number): number {
  return minor / 100;
}

/**
 * Parse a bank-supplied date into `YYYY-MM-DD`, or null when it is not one.
 * The result is a local calendar day with no zone, so a date never shifts by a
 * day the way `new Date("YYYY-MM-DD")` (UTC midnight) can.
 */
export function normalizeBankDate(text: string | null | undefined): string | null {
  if (text == null) return null;
  const value = String(text).trim();
  if (!value) return null;

  const match = ISO_DATE.exec(value) || COMPACT_DATE.exec(value);
  if (!match) return null;

  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  return isRealDate(year, month, day) ? formatDateOnly(year, month, day) : null;
}

// Days in each month; February is 29 only in a leap year, which is checked
// below rather than assumed.
const MONTH_LENGTHS = [31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];

function isLeapYear(year: number): boolean {
  return (year % 4 === 0 && year % 100 !== 0) || year % 400 === 0;
}

function isRealDate(year: number, month: number, day: number): boolean {
  if (month < 1 || month > 12 || day < 1) return false;
  const length =
    month === 2 && isLeapYear(year) ? 29 : MONTH_LENGTHS[month - 1];
  return day <= length;
}

function formatDateOnly(year: number, month: number, day: number): string {
  return `${String(year).padStart(4, "0")}-${String(month).padStart(2, "0")}-${String(day).padStart(2, "0")}`;
}
