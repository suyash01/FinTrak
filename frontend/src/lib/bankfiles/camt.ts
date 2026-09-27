/**
 * A reader for ISO 20022 `camt.052` and `camt.053`.
 *
 * Unlike OFX, camt is real XML, so this uses `DOMParser` and walks the tree
 * rather than tokenising a string. Two consequences shape the code:
 *
 *  - **Lookup is by local name, not qualified name.** camt files put everything
 *    in a default namespace (`xmlns="...camt.053.001.02"`), but some exporters
 *    use a prefix (`<c53:Ntry>`). A qualified-name lookup works for one and
 *    silently returns nothing for the other, so every lookup here matches on
 *    `localName` in any namespace.
 *  - **Nothing is thrown.** Broken XML becomes an `unreadable_file`
 *    diagnostic, and an entry the reader cannot trust is dropped and counted.
 */

import {
  MAX_BANK_FILE_GROUPS,
  MAX_BANK_FILE_ROWS,
} from "./limits";
import type {
  ParseDiagnostic,
  ParseDiagnosticCode,
  ParsedDocument,
  StatementGroup,
} from "./types";
import { majorFromMinor, normalizeBankDate, parseMinorUnits } from "./values";

/** camt's credit/debit indicator. Anything else is a file we do not understand. */
const DIRECTIONS: Record<string, "debit" | "credit"> = {
  CRDT: "credit",
  DBIT: "debit",
};

// The balance codes that matter. OPBD/CLBD are the booked opening and closing
// positions; FWAV (forward available) and PRCD (previously closed) are not the
// statement's own position and are skipped.
const OPENING_BALANCE = "OPBD";
const CLOSING_BALANCE = "CLBD";

/** First direct child element with this local name. */
function child(node: Element | null, name: string): Element | null {
  if (!node) return null;
  for (const el of node.children) {
    if (el.localName === name) return el;
  }
  return null;
}

/** All direct child elements with this local name. */
function children(node: Element | null, name: string): Element[] {
  if (!node) return [];
  return [...node.children].filter((el) => el.localName === name);
}

/** All descendant elements with this local name, in document order. */
function descendants(node: Element | null, name: string): Element[] {
  if (!node) return [];
  return [...node.getElementsByTagNameNS("*", name)];
}

/** The trimmed text of the first direct child with this local name. */
function childText(node: Element | null, name: string): string | undefined {
  return textOf(child(node, name));
}

function textOf(el: Element | null): string | undefined {
  const value = el?.textContent?.trim();
  return value ? value : undefined;
}

/** Accumulates one diagnostic per code, with a count, in first-seen order. */
class Diagnostics {
  private readonly seen = new Map<ParseDiagnosticCode, ParseDiagnostic>();

  add(code: ParseDiagnosticCode, message: string, count = 1): void {
    const existing = this.seen.get(code);
    if (existing) {
      existing.count = (existing.count ?? 1) + count;
      return;
    }
    this.seen.set(code, { code, message, count: count > 1 ? count : undefined });
  }

  list(): ParseDiagnostic[] {
    return [...this.seen.values()];
  }
}

/**
 * Read an ISO 20022 camt document.
 *
 * One group per `<Stmt>`, which is the only element that identifies an account
 * and a period; a file may carry several.
 */
export function readCamt(source: string): ParsedDocument {
  const doc = new DOMParser().parseFromString(source, "application/xml");

  // jsdom and browsers both surface malformed XML as a `parsererror` element
  // rather than by throwing, so this is the check that stands in for a try/catch.
  if (hasParserError(doc)) {
    return {
      format: "camt.053",
      groups: [],
      diagnostics: [
        {
          code: "unreadable_file",
          message:
            "The file is not valid XML, so it could not be read. If your bank offers a different download format, try that.",
        },
      ],
    };
  }

  const diagnostics = new Diagnostics();
  const format = camtVersion(source);
  const groups: StatementGroup[] = [];
  let totalRows = 0;

  for (const stmt of descendants(doc.documentElement, "Stmt")) {
    if (groups.length >= MAX_BANK_FILE_GROUPS) {
      diagnostics.add(
        "groups_truncated",
        `This file describes more than ${MAX_BANK_FILE_GROUPS} statements; only the first ${MAX_BANK_FILE_GROUPS} were read.`,
      );
      break;
    }

    const group: StatementGroup = { format, rows: [] };
    readAccount(stmt, group);
    readBalances(stmt, group);
    readEntries(stmt, group, diagnostics, () => totalRows);
    totalRows += group.rows.length;

    groups.push(group);
  }

  if (totalRows === 0) {
    diagnostics.add(
      "no_transactions",
      "The file was read but contains no booked transactions to import.",
    );
  }

  return { format, groups, diagnostics: diagnostics.list() };
}

function hasParserError(doc: Document): boolean {
  return (
    descendants(doc.documentElement, "parsererror").length > 0 ||
    doc.getElementsByTagName("parsererror").length > 0
  );
}

/**
 * Which camt message this is, from the namespace.
 *
 * The caller has already sniffed the format, so a namespace that cannot be
 * classified here is a camt.053 by elimination rather than a guess about
 * balances: a 052 read as 053 simply reports no balances.
 */
function camtVersion(source: string): "camt.052" | "camt.053" {
  return /camt\.052\./i.test(source.slice(0, 4096)) ? "camt.052" : "camt.053";
}

function readAccount(stmt: Element, group: StatementGroup): void {
  const acct = child(stmt, "Acct");
  if (!acct) return;
  // The identifier is a choice of IBAN, DomesticAcctNb, Othr/Id or Prtry/Id.
  const id = child(acct, "Id");
  const number =
    childText(id, "IBAN") ??
    childText(id, "DomesticAcctNb") ??
    childText(child(id, "Othr"), "Id") ??
    childText(id, "Prtry");
  if (number) group.accountNumber = number;
  const holder = childText(acct, "Nm");
  if (holder) group.accountHolder = holder;
  const currency = childText(acct, "Ccy");
  if (currency) group.currency = currency;
}

function readBalances(stmt: Element, group: StatementGroup): void {
  const balances: NonNullable<StatementGroup["balances"]> = {};
  let from: string | undefined;
  let to: string | undefined;

  for (const bal of children(stmt, "Bal")) {
    const code = childText(child(child(bal, "Tp"), "CdOrPrtry"), "Cd");
    if (code !== OPENING_BALANCE && code !== CLOSING_BALANCE) continue;

    const amount = parseMinorUnits(childText(bal, "Amt"));
    if (amount === null) continue;
    // The magnitude is reported, not the signed position. A liability account
    // (a credit card) states its closing balance as DBIT, because that is the
    // direction from the *account's* view, but the figure a customer checks
    // against the statement is the amount due, printed positive. FinTrak's own
    // balance convention already encodes which side is owed, so applying the
    // indicator here would double-count it and print a number the statement
    // does not show.
    if (code === OPENING_BALANCE) balances.opening = majorFromMinor(amount);
    else balances.closing = majorFromMinor(amount);

    const date = normalizeBankDate(balanceDate(bal));
    if (date) {
      if (code === OPENING_BALANCE) from = date;
      else to = date;
    }
  }

  if (balances.opening !== undefined || balances.closing !== undefined) {
    group.balances = balances;
  }
  // The balances carry the statement's own period, which is more authoritative
  // than the span of its entries.
  if (from) group.periodFrom = from;
  if (to) group.periodTo = to;
}

function balanceDate(bal: Element): string | undefined {
  const dt = child(bal, "Dt");
  return childText(dt, "Dt") ?? childText(dt, "DtTm");
}

function readEntries(
  stmt: Element,
  group: StatementGroup,
  diagnostics: Diagnostics,
  totalRows: () => number,
): void {
  let earliest: string | undefined;
  let latest: string | undefined;

  for (const entry of children(stmt, "Ntry")) {
    // A pending entry is reported rather than imported: the ledger has no
    // pending state, so booking one would overstate the balance.
    const status = (childText(entry, "Sts") ?? "").toUpperCase();
    if (status === "PDNG" || status === "INFO") {
      diagnostics.add(
        "interim_excluded",
        "A pending entry was listed but not imported, because it is not booked yet.",
      );
      continue;
    }

    if (totalRows() + group.rows.length >= MAX_BANK_FILE_ROWS) {
      diagnostics.add(
        "rows_truncated",
        `This file holds more than ${MAX_BANK_FILE_ROWS} entries; only the first ${MAX_BANK_FILE_ROWS} were read.`,
      );
      return;
    }

    const row = readEntry(entry, diagnostics);
    if (!row) continue;
    group.rows.push(row);
    if (!earliest || row.date < earliest) earliest = row.date;
    if (!latest || row.date > latest) latest = row.date;
  }

  // Only fill the period in if the balances did not already state it.
  if (!group.periodFrom && earliest) group.periodFrom = earliest;
  if (!group.periodTo && latest) group.periodTo = latest;
}

function readEntry(
  entry: Element,
  diagnostics: Diagnostics,
): StatementGroup["rows"][number] | null {
  // Ntry is the booking; its details may restate the amount and direction, and
  // the booking's own values are the ones the ledger is reconciling against.
  const details = child(child(entry, "NtryDtls"), "TxDtls");

  const rawAmount = childText(entry, "Amt") ?? childText(details, "Amt");
  const rawDirection =
    childText(entry, "CdtDbtInd") ?? childText(details, "CdtDbtInd");
  const date = readEntryDate(entry);
  const description = readDescription(entry, details, rawDirection);

  if (!description) {
    diagnostics.add(
      "entry_no_details",
      "A statement entry names no party or remittance information, so it was not imported.",
    );
    return null;
  }
  if (rawAmount === undefined) {
    diagnostics.add(
      "row_no_amount",
      `"${description}" carries no amount, so it was not imported.`,
    );
    return null;
  }
  const minor = parseMinorUnits(rawAmount);
  if (minor === null) {
    // Never rounded into the ledger: a three-decimal or out-of-range figure is a
    // file we do not understand, not an amount to approximate.
    diagnostics.add(
      "row_bad_amount",
      `"${description}" has an amount FinTrak cannot read exactly (${rawAmount}), so it was not imported.`,
    );
    return null;
  }

  const type = DIRECTIONS[(rawDirection ?? "").toUpperCase()];
  if (!type) {
    diagnostics.add(
      "row_no_direction",
      `"${description}" has a credit/debit indicator FinTrak does not recognise${rawDirection ? ` (${rawDirection})` : ""}, so it was not imported.`,
    );
    return null;
  }
  // A missing date and an unreadable one are different faults and get
  // different diagnostics, but neither may reach the ledger.
  if (date == null) {
    diagnostics.add(
      date === undefined ? "row_no_date" : "row_bad_date",
      `"${description}" ${date === undefined ? "carries no date" : "has a date FinTrak cannot read"}, so it was not imported.`,
    );
    return null;
  }

  return { date, description, amount: majorFromMinor(Math.abs(minor)), type };
}

/**
 * The booking date, falling back to the value date.
 *
 * The ledger records when a movement posted, not when it took effect, so
 * `BookgDt` wins -- but a file that omits it still has an answer available.
 */
function readEntryDate(entry: Element): string | null | undefined {
  const raw =
    bookingDate(child(entry, "BookgDt")) ?? bookingDate(child(entry, "ValDt"));
  if (raw === undefined) return undefined;
  return normalizeBankDate(raw);
}

function bookingDate(holder: Element | null): string | undefined {
  if (!holder) return undefined;
  return childText(holder, "Dt") ?? childText(holder, "DtTm");
}

/**
 * The party on the other side of the payment, falling back to the remittance
 * information the bank wrote.
 *
 * A money-out entry names its creditor and a money-in entry names its debtor, so
 * the indicator decides which of the two to prefer. Either is better than the
 * account holder, which is the same string on every row and would make the
 * ledger unreadable.
 */
function readDescription(
  entry: Element,
  details: Element | null,
  rawDirection: string | undefined,
): string | undefined {
  const parties = child(details, "RltdPties");
  if (parties) {
    const outgoing = (rawDirection ?? "").toUpperCase() === "DBIT";
    const preferred = outgoing ? "Cdtr" : "Dbtr";
    const other = outgoing ? "Dbtr" : "Cdtr";
    const name =
      childText(child(parties, preferred), "Nm") ??
      childText(child(parties, other), "Nm");
    if (name) return name;
  }
  return (
    childText(child(details, "RmtInf"), "Ustrd") ??
    childText(entry, "AddtlNtryInf")
  );
}
