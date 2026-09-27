/**
 * A lenient reader for OFX / QFX.
 *
 * OFX is the awkward one. Its 2.x flavour is well-formed XML, but 1.x -- still
 * what many banks ship -- is SGML with a plain-text header block and *unclosed
 * leaf tags*:
 *
 *     <STMTTRN><TRNTYPE>DEBIT<DTPOSTED>20260518<TRNAMT>-1250.50<NAME>SWIGGY
 *
 * `DOMParser` cannot read that (it is not well-formed), and a strict SGML
 * parser is not worth a dependency. So the document is tokenized into a flat
 * `tag / value` stream -- a leaf's value simply runs to the next `<` -- and the
 * blocks that matter are located by their opening markers. That handles both
 * flavours with one code path, and is the same trick the query language's
 * lenient parser uses.
 *
 * Two rules keep the read honest:
 *
 *  - A leaf tag with an empty value is a container, not a field. That is what
 *    lets the flat stream be segmented without a schema.
 *  - Nothing is thrown. A row the reader cannot trust is dropped and counted,
 *    because half a statement with a list of what was skipped is more useful to
 *    someone than a failed import.
 */

import type {
  BankFileFormat,
  ParseDiagnostic,
  ParseDiagnosticCode,
  ParsedDocument,
  StatementGroup,
} from "./types";
import { majorFromMinor, normalizeBankDate, parseMinorUnits } from "./values";

interface OfxTag {
  tag: string;
  value: string;
  closing: boolean;
}

/**
 * Split an OFX document into a flat tag stream.
 *
 * Entities are deliberately left alone. OFX 1.x has no entity syntax, so `&` is
 * an ordinary data character there, and "decoding" `AT&amp;T` would corrupt a
 * payee name rather than repair one. OFX 2.x entities in a *leaf value* are
 * rare enough that leaving them is the smaller risk.
 */
function tokenize(src: string): OfxTag[] {
  const tags: OfxTag[] = [];
  let i = 0;
  while (i < src.length) {
    const open = src.indexOf("<", i);
    if (open === -1) break;

    // `<?...?>` (the OFX 2.x prolog) and `<!-- -->` carry no fields.
    if (src.startsWith("<?", open) || src.startsWith("<!--", open)) {
      const end = src.indexOf(">", open);
      i = end === -1 ? src.length : end + 1;
      continue;
    }

    const closing = src[open + 1] === "/";
    let cursor = open + (closing ? 2 : 1);
    const nameStart = cursor;
    while (cursor < src.length && /[A-Za-z0-9_.:-]/.test(src[cursor])) cursor++;
    const tag = src.slice(nameStart, cursor).toUpperCase();
    if (!tag) {
      i = open + 1;
      continue;
    }

    // A self-closing tag is a container with no body.
    let end = src.indexOf(">", cursor);
    if (end === -1) end = src.length;
    const selfClosing = src[end - 1] === "/";
    // The value is everything between this tag's own ">" and the next "<". In
    // SGML that next "<" opens the following tag, because leaf tags are never
    // closed; in XML it is this tag's closing tag. Both reduce to one slice.
    // A tag with only whitespace between the two is a container, not a field,
    // which is what lets the flat stream be segmented without a schema.
    const nextOpen = src.indexOf("<", end + 1);
    const value = selfClosing
      ? ""
      : src.slice(end + 1, nextOpen === -1 ? end : nextOpen).trim();

    tags.push({ tag, value, closing });
    i = end + 1;
  }
  return tags;
}

/** A half-open `[start, end)` run of the token stream. */
interface Range {
  start: number;
  end: number;
}

/**
 * Locate the blocks opened by `names` within `range`.
 *
 * A block ends at its own closing tag when the file has one, and only otherwise
 * at the next block of the same name or at the end of `range`. Honouring the
 * closing tag is what keeps an OFX file's `INTERIMTRANLIST` (which follows
 * `BANKTRANLIST` as a *sibling*, not a child) out of the booked rows: without
 * it, a SGML list that is never explicitly closed would swallow everything
 * after it.
 */
function blocksOf(
  tags: OfxTag[],
  names: readonly string[],
  range: Range,
): Range[] {
  const wanted = new Set(names);
  const starts: number[] = [];
  for (let i = range.start; i < range.end; i++) {
    const t = tags[i];
    if (!t.closing && wanted.has(t.tag)) starts.push(i);
  }
  return starts.map((start, index) => {
    const name = tags[start].tag;
    const limit = index + 1 < starts.length ? starts[index + 1] : range.end;
    for (let i = start + 1; i < limit; i++) {
      const t = tags[i];
      if (t.closing && t.tag === name) return { start, end: i };
    }
    return { start, end: limit };
  });
}

/** The first value of `name` within `range`, or undefined. */
function valueOf(
  tags: OfxTag[],
  name: string,
  range: Range,
): string | undefined {
  for (let i = range.start; i < range.end; i++) {
    const t = tags[i];
    if (!t.closing && t.tag === name && t.value) return t.value;
  }
  return undefined;
}

/** The first value of whichever of `names` appears, in the order given. */
function firstValueOf(
  tags: OfxTag[],
  names: readonly string[],
  range: Range,
): string | undefined {
  for (const name of names) {
    const found = valueOf(tags, name, range);
    if (found !== undefined) return found;
  }
  return undefined;
}

// A per-account block. A bank statement is `STMTTRNRS`; a credit-card one is
// `CCSTMTRS` (some banks write the `CCSTMTTRNRS` form instead).
const STATEMENT_BLOCKS = ["STMTTRNRS", "CCSTMTRS", "CCSTMTTRNRS"] as const;

// The list of booked movements. `INTERIMTRANLIST` holds *pending* entries and is
// deliberately not read -- see the diagnostic in `readOfx`.
const BOOKED_LIST = "BANKTRANLIST";
const INTERIM_LIST = "INTERIMTRANLIST";

const TRANSACTION = "STMTTRN";

/** Columns naming a counterparty, in the order a ledger would prefer them. */
const DESCRIPTION_TAGS = [
  "NAME",
  "MEMO",
  "ORIGDESCRIPTION",
  "CHECKNUM",
  "FITID",
] as const;

const DATE_TAGS = ["DTPOSTED", "DTUSER", "DTAVAIL"] as const;

const DIRECTIONS: Record<string, "debit" | "credit"> = {
  DEBIT: "debit",
  CREDIT: "credit",
  DEBITCREDIT: "debit",
};

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
 * Read an OFX document.
 *
 * Returns a document whose groups are the per-account statements the file
 * contains, in file order. A block with no `BANKTRANLIST` is not a statement at
 * all and contributes no group; a statement whose list is empty does, because a
 * quiet period is a real 0-row import and should not look like a failed read.
 */
export function readOfx(source: string): ParsedDocument {
  const format: BankFileFormat = "ofx";
  const diagnostics = new Diagnostics();
  const tags = tokenize(source);
  const whole = { start: 0, end: tags.length };
  const groups: StatementGroup[] = [];

  for (const block of blocksOf(tags, STATEMENT_BLOCKS, whole)) {
    const list = blocksOf(tags, [BOOKED_LIST], block)[0];
    if (!list) continue; // not a statement block

    const rows = readBookedRows(tags, list, diagnostics);

    // Pending movements are reported rather than imported: the ledger has no
    // pending state, so booking them would overstate the balance, and dropping
    // them silently would hide that the file had them.
    const interim = blocksOf(tags, [INTERIM_LIST], block)[0];
    if (interim) {
      const pending = blocksOf(tags, [TRANSACTION], interim).length;
      if (pending > 0) {
        diagnostics.add(
          "interim_excluded",
          `${pending} pending transaction${pending === 1 ? "" : "s"} were listed but not imported, because they are not booked yet.`,
          pending,
        );
      }
    }

    const group: StatementGroup = {
      format,
      rows,
    };
    const accountNumber = firstValueOf(tags, ["ACCTID", "ACCTNUM"], block);
    if (accountNumber) group.accountNumber = accountNumber;
    const currency = valueOf(tags, "CURDEF", block);
    if (currency) group.currency = currency;
    const periodFrom = normalizeBankDate(valueOf(tags, "DTSTART", list));
    if (periodFrom) group.periodFrom = periodFrom;
    const periodTo = normalizeBankDate(valueOf(tags, "DTEND", list));
    if (periodTo) group.periodTo = periodTo;

    // The ledger balance is the one that must agree with the statement; the
    // available balance differs by pending amounts and holds nothing to check.
    const ledger = blocksOf(tags, ["LEDGERBAL", "CCLEDGERBAL"], block)[0];
    if (ledger) {
      const closing = parseMinorUnits(valueOf(tags, "BALAMT", ledger));
      if (closing !== null) group.balances = { closing: majorFromMinor(closing) };
    }

    groups.push(group);
  }

  const totalRows = groups.reduce((sum, g) => sum + g.rows.length, 0);
  if (totalRows === 0) {
    diagnostics.add(
      "no_transactions",
      "The file was read but contains no booked transactions to import.",
    );
  }

  return { format, groups, diagnostics: diagnostics.list() };
}

function readBookedRows(
  tags: OfxTag[],
  list: Range,
  diagnostics: Diagnostics,
) {
  const rows: StatementGroup["rows"] = [];
  for (const entry of blocksOf(tags, [TRANSACTION], list)) {
    const row = readRow(tags, entry, diagnostics);
    if (row) rows.push(row);
  }
  return rows;
}

function readRow(
  tags: OfxTag[],
  entry: Range,
  diagnostics: Diagnostics,
): StatementGroup["rows"][number] | null {
  const description = firstValueOf(tags, DESCRIPTION_TAGS, entry);
  if (!description) {
    diagnostics.add(
      "entry_no_details",
      "A statement entry names no payee, memo or reference, so it was not imported.",
    );
    return null;
  }

  const rawAmount = valueOf(tags, "TRNAMT", entry);
  if (rawAmount === undefined) {
    diagnostics.add(
      "row_no_amount",
      `"${description}" carries no amount, so it was not imported.`,
    );
    return null;
  }
  const minor = parseMinorUnits(rawAmount);
  if (minor === null) {
    // Never rounded into the ledger: a three-decimal or out-of-range figure is
    // a file we do not understand, not an amount to approximate.
    diagnostics.add(
      "row_bad_amount",
      `"${description}" has an amount FinTrak cannot read exactly (${rawAmount}), so it was not imported.`,
    );
    return null;
  }

  const rawDate = firstValueOf(tags, DATE_TAGS, entry);
  if (rawDate === undefined) {
    diagnostics.add(
      "row_no_date",
      `"${description}" carries no date, so it was not imported.`,
    );
    return null;
  }
  const date = normalizeBankDate(rawDate);
  if (date === null) {
    diagnostics.add(
      "row_bad_date",
      `"${description}" has a date FinTrak cannot read (${rawDate}), so it was not imported.`,
    );
    return null;
  }

  return {
    date,
    description,
    amount: majorFromMinor(Math.abs(minor)),
    type: directionOf(tags, minor, entry, diagnostics),
  };
}

/**
 * Decide whether an entry is money out or money in.
 *
 * An explicit `TRNTYPE` of DEBIT or CREDIT wins: it is the bank stating its
 * intent, and old 1.x files do emit a positive amount for a debit. Anything else
 * (XFER, INT, DIV, ...) is a movement rather than a direction, so the sign of
 * the amount decides. A disagreement between the two is reported, because a file
 * that contradicts itself is worth knowing about.
 */
function directionOf(
  tags: OfxTag[],
  minor: number,
  entry: Range,
  diagnostics: Diagnostics,
): "debit" | "credit" {
  const declared = DIRECTIONS[(valueOf(tags, "TRNTYPE", entry) ?? "").toUpperCase()];
  const bySign = minor < 0 ? "debit" : "credit";
  if (declared && declared !== bySign) {
    diagnostics.add(
      "row_no_direction",
      "Some entries state a transaction type that disagrees with the sign of their amount; the stated type was used.",
    );
  }
  return declared ?? bySign;
}
