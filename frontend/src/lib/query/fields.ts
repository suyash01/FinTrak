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

export type FieldKind = "text" | "uuid" | "enum" | "amount" | "date" | "tags";

export interface FieldDef {
  /** False for the `plain` pseudo-field, which the parser synthesises. */
  userTyped: boolean;
  kind: FieldKind;
  ops: QueryOp[];
  enum?: string[];
}

const EQ: QueryOp[] = ["=", "!="];
const ORDER: QueryOp[] = ["=", "!=", ">", ">=", "<", "<="];
const TEXT: QueryOp[] = ["=", "!=", "~"];

export const FIELD_TABLE: Record<string, FieldDef> = {
  plain: { userTyped: false, kind: "text", ops: TEXT },
  desc: { userTyped: true, kind: "text", ops: TEXT },
  note: { userTyped: true, kind: "text", ops: TEXT },
  cat: { userTyped: true, kind: "uuid", ops: EQ },
  group: { userTyped: true, kind: "uuid", ops: EQ },
  acct: { userTyped: true, kind: "uuid", ops: EQ },
  payee: { userTyped: true, kind: "uuid", ops: EQ },
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
