import { periodRange } from "@/lib/dates";
import type { Account, Category, CategoryGroup, Payee } from "@/types";
import { FIELD_PLAIN, DIAG, type ParsedQuery, type QueryDiagnostic, type QueryTerm } from "./parse";
import { DATE_PERIODS, FIELD_TABLE, type FieldDef } from "./fields";

// Re-exported so the autocomplete and the grammar sheet read the period list from
// the one place that defines which periods are offerable.
export { DATE_PERIODS };

export interface ResolveSource {
  accounts: Account[];
  categories: Category[];
  groups: CategoryGroup[];
  payees: Payee[];
  /** Tag names, from the page's TagCount[] — there is no tag table. */
  tags: string[];
}

/**
 * resolveQuery rewrites the terms a user typed into the terms the server can act
 * on: names become ids, a named period becomes concrete date bounds, and an
 * amount becomes minor units.
 *
 * This is the whole reason the surface syntax and the wire syntax can be the
 * same syntax: the server resolves no names, so a caller — this one — has to.
 * A name that matches nothing is dropped and reported rather than passed on as
 * a literal, because the server would drop it anyway and only it could say why.
 */
export function resolveQuery(
  parsed: ParsedQuery,
  src: ResolveSource,
): { terms: QueryTerm[]; diagnostics: QueryDiagnostic[] } {
  const terms: QueryTerm[] = [];
  const diagnostics: QueryDiagnostic[] = [...parsed.diagnostics];

  for (const term of parsed.terms) {
    if (term.field === FIELD_PLAIN) {
      terms.push(term);
      continue;
    }
    const def = FIELD_TABLE[term.field];
    if (!def) {
      diagnostics.push({
        term: term.raw,
        code: DIAG.unknownField,
        message: `unknown field ${term.field}`,
        position: term.position,
      });
      continue;
    }
    switch (def.kind) {
      case "tags":
        // Already a name, and names are what a tag is.
        terms.push(term);
        continue;
      case "amount":
        // Deliberately NOT converted to minor units. The wire carries major
        // units, like the JSON boundary everywhere else in this API, and the
        // server does the single major->minor conversion with money.Parse.
        // Converting here as well made every amount filter 100x too large.
        terms.push(term);
        continue;
      case "date":
        terms.push(...expandDates(term, diagnostics));
        continue;
      case "enum":
        terms.push(term);
        continue;
      case "text":
        terms.push(term);
        continue;
      case "uuid":
        terms.push(...resolveIds(term, def, src, diagnostics));
        continue;
    }
  }

  return { terms, diagnostics };
}

/** labelFor names the thing a field points at, for a message a person reads. */
function labelFor(field: string): string {
  switch (field) {
    case "cat":
      return "category";
    case "group":
      return "category group";
    case "acct":
      return "account";
    case "payee":
      return "payee";
    default:
      return field;
  }
}

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function resolveIds(term: QueryTerm, def: FieldDef, src: ResolveSource, diagnostics: QueryDiagnostic[]): QueryTerm[] {
  const resolved: string[] = [];
  const ambiguous: string[] = [];

  for (const value of term.values) {
    // A sentinel passes only for a field whose column is actually nullable. On
    // any other uuid field the compiler would bind the literal string against the
    // column and the database would answer 500, so it is reported here instead.
    if (def.sentinels?.includes(value) || UUID_RE.test(value)) {
      resolved.push(value);
      continue;
    }
    const hits = lookup(term.field, value, src);
    if (hits.length === 0) {
      diagnostics.push({
        term: term.raw,
        code: DIAG.unresolved,
        message: `no ${labelFor(term.field)} named ${JSON.stringify(value)}`,
        position: term.position,
      });
      continue;
    }
    if (hits.length > 1) ambiguous.push(value);
    resolved.push(...hits);
  }

  if (ambiguous.length > 0) {
    // The fix-it advice has to name a spelling this field actually understands.
    // Only cat resolves `Group/Name`; telling someone to disambiguate a payee or
    // an account that way sent them to a string that resolves to nothing.
    const hint =
      term.field === "cat"
        ? ` Use ${term.field}:Group/Name to pick one.`
        : term.field === "group"
          ? ` Use the group's exact name.`
          : ` Use the exact name.`;
    diagnostics.push({
      term: term.raw,
      code: DIAG.ambiguous,
      message:
        `${ambiguous.map((v) => JSON.stringify(v)).join(", ")} matched more than one ` +
        `${labelFor(term.field)}; keeping all of them.${hint}`,
      position: term.position,
    });
  }

  // A term whose every value failed to resolve emits nothing, so the filter is
  // a no-op rather than a false predicate that would silently empty the list.
  if (resolved.length === 0) return [];
  return [{ ...term, values: resolved }];
}

/**
 * lookup resolves one written value against the user's own data, accepting the
 * `Group/Name` qualified spelling the autocomplete inserts. Matching is
 * case-insensitive because a person types "whole foods" for "Whole Foods".
 */
function lookup(field: string, raw: string, src: ResolveSource): string[] {
  const slash = raw.indexOf("/");
  const qualifier = slash > 0 ? raw.slice(0, slash).toLowerCase() : null;
  const name = (slash > 0 ? raw.slice(slash + 1) : raw).toLowerCase();

  switch (field) {
    case "cat": {
      const groupId = qualifier
        ? src.groups.find((g) => g.name.toLowerCase() === qualifier)?.id
        : null;
      return src.categories
        .filter((c) => c.name.toLowerCase() === name && (groupId ? c.groupId === groupId : true))
        .map((c) => c.id);
    }
    case "group": {
      const byId = src.groups.find((g) => g.id.toLowerCase() === raw.toLowerCase());
      if (byId) return [byId.id];
      return src.groups.filter((g) => g.name.toLowerCase() === name).map((g) => g.id);
    }
    case "acct":
      return src.accounts.filter((a) => a.name.toLowerCase() === name).map((a) => a.id);
    case "payee":
      return src.payees.filter((p) => p.name.toLowerCase() === name).map((p) => p.id);
    default:
      return [];
  }
}

/**
 * expandDates turns a named period into two concrete bounds, so a shared URL
 * means the same range it meant when it was written. This is the same choice
 * Money Flow and the calendar already make when they store resolved dates in
 * the URL.
 */
function expandDates(term: QueryTerm, diagnostics: QueryDiagnostic[]): QueryTerm[] {
  const isPeriod = (v: string) => (DATE_PERIODS as readonly string[]).includes(v);
  if (!term.values.every(isPeriod)) return [term];

  // Every value is the same period, since a CSV would mix a period with a date
  // and mean nothing.
  const range = periodRange(term.values[0]);
  if (!range) {
    diagnostics.push({
      term: term.raw,
      code: DIAG.malformedDate,
      message: `${JSON.stringify(term.values[0])} is not a known period`,
      position: term.position,
    });
    return [];
  }
  return [
    { ...term, op: ">=", values: [range.dateFrom], negated: false },
    { ...term, op: "<=", values: [range.dateTo], negated: false },
  ];
}

/** quoteIfNeeded quotes exactly when the value would otherwise parse as more than one token. */
function quoteIfNeeded(value: string): string {
  return /[\s,"]/.test(value) ? `"${value.replace(/(["\\])/g, "\\$1")}"` : value;
}

/** serializeQuery renders terms as the q= value the API takes. */
export function serializeQuery(terms: QueryTerm[]): string {
  return terms
    .map((t) => {
      // A bare word is plain search and must be written back as bare words: the
      // `plain` pseudo-field is not a field a user can type, so emitting
      // `plain~coffee` would be a query the parser rejects.
      if (t.field === FIELD_PLAIN) {
        const words = t.values.map(quoteIfNeeded).join(" ");
        return t.negated ? `not ${words}` : words;
      }
      const value = t.values.map(quoteIfNeeded).join(",");
      const body = t.op === "=" ? `${t.field}:${value}` : `${t.field}${t.op}${value}`;
      return t.negated ? `not ${body}` : body;
    })
    .join(" ");
}
