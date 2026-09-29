import { PERIOD_CURRENT_FY, PERIOD_LAST_12_MONTHS } from "@/lib/dates";

// The field table: the single definition of every field, the parser, the
// autocomplete and the grammar sheet all read this. A field therefore cannot be
// suggested that the parser will reject, and the documented syntax cannot drift
// from the accepted syntax.
//
// It mirrors backend/internal/query/fields.go. The two are pinned against each
// other by testdata/corpus.json, which both test suites read.

// fields.ts owns QueryOp, not parse.ts: parse.ts imports FIELD_TABLE (a value)
// from here, so declaring the type in parse.ts and importing it back would make
// the two modules circular at runtime over a type that is erased anyway.
export type QueryOp = "=" | "!=" | ">" | ">=" | "<" | "<=" | "~";

export type FieldKind = "text" | "uuid" | "enum" | "amount" | "date" | "tags" | "currency";

export interface FieldDef {
  /** False for the `plain` pseudo-field, which the parser synthesises. */
  userTyped: boolean;
  kind: FieldKind;
  ops: QueryOp[];
  enum?: string[];
  /**
   * Words that stand for an absent value. Only a field whose column is actually
   * nullable may take them - mirrors backend/internal/query/fields.go, where the
   * compiler turns one into IS NULL and anything else would bind the literal
   * string against the column.
   */
  sentinels?: string[];
}

const EQ: QueryOp[] = ["=", "!="];
const ORDER: QueryOp[] = ["=", "!=", ">", ">=", "<", "<="];
const TEXT: QueryOp[] = ["=", "!=", "~"];

/** nullSentinels are the two words the existing filters use for an absent value. */
const NULL_SENTINELS = ["none", "uncategorized"];

export const FIELD_TABLE: Record<string, FieldDef> = {
  plain: { userTyped: false, kind: "text", ops: TEXT },
  desc: { userTyped: true, kind: "text", ops: TEXT },
  note: { userTyped: true, kind: "text", ops: TEXT },
  cat: { userTyped: true, kind: "uuid", ops: EQ, sentinels: NULL_SENTINELS },
  group: { userTyped: true, kind: "uuid", ops: EQ },
  acct: { userTyped: true, kind: "uuid", ops: EQ },
  id: { userTyped: true, kind: "uuid", ops: EQ },
  ccy: { userTyped: true, kind: "currency", ops: EQ },
  payee: { userTyped: true, kind: "uuid", ops: EQ, sentinels: NULL_SENTINELS },
  tag: { userTyped: true, kind: "tags", ops: EQ },
  type: { userTyped: true, kind: "enum", ops: EQ, enum: ["debit", "credit"] },
  linked: { userTyped: true, kind: "enum", ops: EQ, enum: ["true", "false"] },
  recurring: { userTyped: true, kind: "enum", ops: EQ, enum: ["linked", "unlinked"] },
  amt: { userTyped: true, kind: "amount", ops: ORDER },
  date: { userTyped: true, kind: "date", ops: ORDER },
};

/** The fields the autocomplete offers, in grammar-sheet order. */
export const USER_FIELDS = Object.entries(FIELD_TABLE)
  .filter(([, def]) => def.userTyped)
  .map(([name]) => name);

export function userField(name: string): FieldDef | undefined {
  const def = FIELD_TABLE[name];
  return def && def.userTyped ? def : undefined;
}

export function allowsOp(def: FieldDef, op: QueryOp): boolean {
  return def.ops.includes(op);
}

/**
 * The named periods `date:` accepts, and the only ones it should offer.
 *
 * PERIOD_CUSTOM is deliberately absent. periodRange("custom") falls back to the
 * rolling twelve months, so offering it would tell the user they had set a custom
 * range and hand them twelve months instead. A period that cannot mean what it
 * says should not be a suggestion.
 *
 * It lives here, not in resolve.ts, because both the parser (which must accept a
 * period as valid input) and the resolver (which expands one) need it, and
 * fields.ts is below both.
 */
export const DATE_PERIODS = [PERIOD_LAST_12_MONTHS, PERIOD_CURRENT_FY] as const;
