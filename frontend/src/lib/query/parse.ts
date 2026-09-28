import { allowsOp, DATE_PERIODS, userField, type FieldDef, type QueryOp } from "./fields";

// The query grammar, in TypeScript. It is a mirror of
// backend/internal/query/parse.go, and the two are pinned against each other by
// testdata/corpus.json, which both test suites read: a field or operator added
// on one side without the other fails both.
//
// The parser is lenient by design and never throws. A term it cannot understand
// is dropped and reported, because a dropped constraint silently widens a result
// set and the user would read the whole ledger as if it were the answer.

// QueryOp is re-exported so callers can take the whole query surface from this
// module; the type itself is declared in fields.ts to keep the two acyclic.
export type { QueryOp };

export const MAX_QUERY_CHARS = 2000;
export const MAX_TERMS = 32;

/** The pseudo-field a bare word compiles to: a free-text search. */
export const FIELD_PLAIN = "plain";

export interface QueryTerm {
  field: string;
  op: QueryOp;
  values: string[];
  negated: boolean;
  position: number;
  raw: string;
}

export interface QueryDiagnostic {
  term: string;
  code: string;
  message: string;
  position: number;
}

export interface ParsedQuery {
  terms: QueryTerm[];
  diagnostics: QueryDiagnostic[];
}

export const DIAG = {
  unknownField: "unknown_field",
  badOperator: "bad_operator",
  missingValue: "missing_value",
  unresolved: "unresolved_value",
  malformedAmount: "malformed_amount",
  malformedDate: "malformed_date",
  ambiguous: "ambiguous_value",
  tooLong: "too_long",
  // Resolver-only, and unlike every code above it, not a thing a user can type
  // their way into. It is raised when a field reaches resolveQuery with a kind
  // its switch does not handle, which is a defect in this repository rather than
  // a bad query - the seven kinds are all handled. It exists so that defect is
  // visible instead of silent, which is what ccy: was for four commits: the
  // parser accepted it, the corpus covered it, the resolver had no case for the
  // currency kind, and the term was dropped with nothing reported. The user got
  // the unfiltered ledger and no warning.
  //
  // It is deliberately NOT mirrored into backend/internal/query/parse.go, and is
  // not part of the code list in openapi.yaml: the server resolves no names and so
  // never has to switch on a kind, and documenting a code the API cannot emit
  // would make the contract a worse description of the contract.
  unhandledKind: "unhandled_kind",
} as const;

// Longest first, so ">=" wins over ">".
const OPERATORS: Array<[string, QueryOp]> = [
  [">=", ">="],
  ["<=", "<="],
  ["!=", "!="],
  ["~", "~"],
  [">", ">"],
  ["<", "<"],
  ["=", "="],
];

const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;
// One optional sign, digits on both sides of the point, at most two fraction
// digits: the same grammar money.Parse accepts, mirrored so the box cannot
// produce a query the API will refuse.
const AMOUNT_RE = /^[+-]?\d+(\.\d{1,2})?$/;

function isOperatorChar(c: string): boolean {
  return c === ">" || c === "<" || c === "=" || c === "!" || c === "~";
}

/**
 * readWord reads a run of characters at i that cannot be part of a field name,
 * returning the word and the index just past it. An empty word means i sits on a
 * delimiter.
 *
 * It stops at ':' because that introduces a field, and at the operator
 * characters because they follow one.
 */
function readWord(q: string, i: number): { word: string; next: number } {
  let j = i;
  while (j < q.length) {
    const c = q[j];
    if (c === " " || c === "\t" || c === '"' || c === "," || c === ":") break;
    if (isOperatorChar(c)) break;
    j++;
  }
  return { word: q.slice(i, j), next: j };
}

function matchOp(q: string, i: number): { symbol: string; op: QueryOp } | null {
  const rest = q.slice(i);
  for (const [symbol, op] of OPERATORS) {
    if (rest.startsWith(symbol)) return { symbol, op };
  }
  return null;
}

/**
 * readValue reads one value at i — quoted, or a bare comma-separated run — and
 * returns the index just past it. A quoted value is a single item even if it
 * contains a comma.
 */
function readValue(q: string, i: number): { end: number; values: string[] } {
  if (q[i] === '"') {
    let out = "";
    let j = i + 1;
    while (j < q.length) {
      if (q[j] === "\\" && j + 1 < q.length) {
        out += q[j + 1];
        j += 2;
        continue;
      }
      if (q[j] === '"') {
        j++;
        break;
      }
      out += q[j];
      j++;
    }
    return { end: j, values: [out] };
  }
  let j = i;
  while (j < q.length && q[j] !== " " && q[j] !== "\t" && q[j] !== '"') j++;
  const values = q
    .slice(i, j)
    .split(",")
    .map((p) => p.trim())
    .filter((p) => p !== "");
  if (values.length === 0) {
    // A run of only separators: keep it as one literal item so the term is
    // reported as unresolvable rather than silently matching nothing.
    return { end: j, values: [q.slice(i, j)] };
  }
  return { end: j, values };
}

/**
 * parseQuery tokenizes a query and returns the terms it understood plus a
 * diagnostic for every term it dropped.
 */
export function parseQuery(input: string): ParsedQuery {
  const terms: QueryTerm[] = [];
  const diagnostics: QueryDiagnostic[] = [];
  let q = input;

  if (q.length > MAX_QUERY_CHARS) {
    diagnostics.push({
      term: q.slice(0, MAX_QUERY_CHARS),
      code: DIAG.tooLong,
      message: "query is longer than 2000 characters; the rest was ignored",
      position: 0,
    });
    q = q.slice(0, MAX_QUERY_CHARS);
  }

  let i = 0;
  const n = q.length;
  const isSpace = (c: string) => c === " " || c === "\t";
  const skipSpaces = () => {
    while (i < n && isSpace(q[i])) i++;
  };

  for (;;) {
    skipSpaces();
    if (i >= n) break;

    if (terms.length >= MAX_TERMS) {
      diagnostics.push({
        term: q.slice(i).trim(),
        code: DIAG.tooLong,
        message: "query has more than 32 terms; the rest was ignored",
        position: i,
      });
      break;
    }

    // start is the offset of the term as the user wrote it, so a `not` prefix
    // is inside it: the UI highlights "not cat:a,b", not "cat:a,b".
    const start = i;
    let negated = false;
    const first = readWord(q, i);
    if (first.word.toLowerCase() === "not") {
      negated = true;
      i = first.next;
      skipSpaces();
    }

    const word = readWord(q, i);
    const def = userField(word.word.toLowerCase());

    // `field:value`
    if (word.word !== "" && word.next < n && q[word.next] === ":") {
      const field = word.word.toLowerCase();
      i = word.next + 1;
      skipSpaces();
      if (i >= n) {
        diagnostics.push({
          term: q.slice(start, word.next + 1),
          code: DIAG.missingValue,
          message: `${field}: needs a value`,
          position: start,
        });
        continue;
      }
      const read = readValue(q, i);
      i = read.end;
      const raw = q.slice(start, read.end);
      if (!def) {
        diagnostics.push({ term: raw, code: DIAG.unknownField, message: `unknown field ${field}`, position: start });
        continue;
      }
      const err = validate(def, field, read.values);
      if (err) {
        diagnostics.push({ term: raw, ...err, position: start });
        continue;
      }
      terms.push({ field, op: "=", values: read.values, negated, position: start, raw });
      continue;
    }

    // `field<op>value`. The operator sits just past the field name, which is
    // where readWord stopped. Matching at i would never fire, because i is the
    // start of the name, not the operator. The word must be non-empty: at a bare
    // operator character there is no field, and reporting "unknown field " for it
    // would be noise.
    const op = word.word === "" ? null : matchOp(q, word.next);
    if (op) {
      const field = word.word.toLowerCase();
      const afterOp = word.next + op.symbol.length;

      // A doubled operator (`amt>>50`) is a typo, not a comparison. The whole
      // malformed token is consumed, so nothing after the typo leaks through as a
      // stray bare word.
      if (afterOp < n && isOperatorChar(q[afterOp])) {
        let end = afterOp + 1;
        while (end < n && !isSpace(q[end])) end++;
        diagnostics.push({
          term: q.slice(start, end),
          code: DIAG.badOperator,
          message: `malformed operator after ${field}`,
          position: start,
        });
        i = end;
        continue;
      }

      let j = afterOp;
      while (j < n && isSpace(q[j])) j++;
      if (j >= n) {
        diagnostics.push({
          term: q.slice(start, afterOp),
          code: DIAG.missingValue,
          message: `${field} needs a value`,
          position: start,
        });
        i = afterOp;
        continue;
      }
      const read = readValue(q, j);
      i = read.end;
      const raw = q.slice(start, read.end);
      if (!def) {
        diagnostics.push({ term: raw, code: DIAG.unknownField, message: `unknown field ${field}`, position: start });
        continue;
      }
      if (!allowsOp(def, op.op)) {
        diagnostics.push({
          term: raw,
          code: DIAG.badOperator,
          message: `${field} does not support ${op.symbol}`,
          position: start,
        });
        continue;
      }
      const err = validate(def, field, read.values);
      if (err) {
        diagnostics.push({ term: raw, ...err, position: start });
        continue;
      }
      terms.push({ field, op: op.op, values: read.values, negated, position: start, raw });
      continue;
    }

    // A bare word: plain search. Collect words until the next `field:` or
    // `field<op>` shape, so "coffee shop" is one term.
    const words: string[] = [];
    let j = i;
    for (; j < n; ) {
      while (j < n && isSpace(q[j])) j++;
      if (j >= n) break;
      const w = readWord(q, j);
      if (w.word === "") {
        j++;
        continue;
      }
      if (w.next < n && q[w.next] === ":") break;
      if (matchOp(q, w.next)) break;
      words.push(w.word);
      j = w.next;
    }
    if (words.length === 0) {
      // A character we cannot start a token with. Skip it so one character
      // cannot loop forever.
      i++;
      continue;
    }
    terms.push({
      field: FIELD_PLAIN,
      op: "~",
      values: words,
      negated,
      position: start,
      raw: words.join(" "),
    });
    i = j;
  }

  return { terms, diagnostics };
}

/** validate returns a diagnostic body, or null. */
function validate(def: FieldDef, field: string, values: string[]): { code: string; message: string } | null {
  if (values.length === 0) return { code: DIAG.missingValue, message: `${field}: needs a value` };
  for (const v of values) {
    switch (def.kind) {
      case "text":
        break;
      case "uuid":
        // Deliberately no resolvability check. The server rejects a name here
        // (backend/internal/query/parse.go, kindUUID), because on the server that
        // IS its job. This parser runs on the user's keystrokes, where a name is
        // the normal input and resolving it is resolveQuery's job. The two
        // corpus cases that pin the server's refusal are marked serverOnly.
        break;
      case "enum":
        if (!def.enum?.includes(v)) {
          return { code: DIAG.unresolved, message: `${field}: ${JSON.stringify(v)} is not one of ${def.enum}` };
        }
        break;
      case "amount":
        if (!AMOUNT_RE.test(v)) {
          return {
            code: DIAG.malformedAmount,
            message: `${field}: ${JSON.stringify(v)} is not an amount (e.g. 50 or 50.75)`,
          };
        }
        break;
      case "date":
        // A named period is legitimate input here: the box offers them, and
        // resolveQuery expands them into concrete bounds. Anything else must be
        // a real, in-window date, so a typo is reported as the user types rather
        // than at the server.
        if (DATE_PERIODS.includes(v as (typeof DATE_PERIODS)[number])) break;
        if (!DATE_RE.test(v) || !isInDateWindow(v)) {
          return {
            code: DIAG.malformedDate,
            message: `${field}: ${JSON.stringify(v)} is not a usable date (must be YYYY-MM-DD, or one of ${DATE_PERIODS.join(", ")}, between 1900-01-01 and one year from today)`,
          };
        }
        break;
      case "tags":
        // Tags are free text; the server binds them as a literal, so a quote
        // cannot be expressed. Refusing it here is what keeps that true.
        if (v.includes("'")) {
          return { code: DIAG.unresolved, message: `${field}: a tag name cannot contain a quote` };
        }
        break;
      case "currency":
        // Three ASCII letters, folded to upper case by the server. Not an enum:
        // the domain is the user's own accounts, so a fixed list would refuse a
        // currency they legitimately hold. The fold happens in the compiler
        // (backend/internal/query/compile.go, emitCurrency), so this checks the
        // shape only: the corpus keeps the lower case the user typed, and it is
        // the bound argument that comes out upper case.
        if (!/^[A-Za-z]{3}$/.test(v)) {
          return {
            code: DIAG.unresolved,
            message: `${field}: ${JSON.stringify(v)} is not a currency code (three letters, e.g. USD)`,
          };
        }
        break;
    }
  }
  return null;
}

/**
 * isInDateWindow mirrors validation.CheckTransactionDate's window on the server.
 *
 * The bounds are compared as strings, not as timestamps. That is deliberate: the
 * zero-padded ISO form sorts lexicographically in date order, and building a
 * timestamp for the minimum would hit a JavaScript trap — Date.UTC maps a year
 * in 0..99 to 1900+year, so Date.UTC(1, 0, 1) is 1901, not year 1, and the
 * out-of-window date this exists to reject would sail through.
 *
 * The bounds are duplicated rather than fetched: a shared helper would put a
 * date range in the API contract to check one string, and "today" must be the
 * local day (see todayLocalISO in src/lib/dates.ts — never toISOString, which is
 * yesterday east of UTC before 05:30).
 */
function isInDateWindow(value: string): boolean {
  if (value < "1900-01-01") return false;
  const now = new Date();
  const max = `${now.getFullYear() + 1}-${String(now.getMonth() + 1).padStart(2, "0")}-${String(now.getDate()).padStart(2, "0")}`;
  if (value > max) return false;
  // The window check already excluded years below 1900, so the components can be
  // handed to the Date constructor without the 0..99 remapping biting.
  const [y, m, d] = value.split("-").map(Number);
  const probe = new Date(y, m - 1, d);
  return probe.getFullYear() === y && probe.getMonth() === m - 1 && probe.getDate() === d;
}
