// A row's mergeable fields — the projection, one per resource family.
//
// This is not registry-specific. Two places have to describe the same row the
// same way, and neither can do it alone: the op registry reads the server's
// current row to merge a queued write against, and client.ts builds the base a
// user's edit is diffed from. A field in one description and not the other would
// be one the merge cannot reason about — the base could not account for a change
// to it, and the row could not be compared against that change — so the field
// lists live here, once, and both import them.
//
// Three things about representation rather than policy, all of which the merge
// rests on:
//
//   A nullish field is left OFF the object, not set to null. The server's row
//   reports null for a column it holds nothing in, and that is not the same
//   statement as the user clearing the field (merge.ts: absent is not null). A
//   field the server has never held is a field the row does not carry, so
//   comparing a base and a payload that both describe the row this way reads a
//   never-set field as untouched on both sides rather than as a value somebody
//   changed to nothing.
//
//   An empty collection and an empty string are values, not absences. Emptying
//   every tag or clearing a note is a change the user made.
//
//   Only fields a queued write can set are named. Ids, joined display names,
//   counts and anything the server derives are not mergeable: there is no queued
//   edit that would change them, and putting one in a base would invent a
//   difference the user never made, which the merge would then hold as somebody
//   else's change.
//
// This describes the *server's* row. It is deliberately not applied to the user's
// side of an edit: there a null is the user clearing a field, and projecting it
// with this rule would drop the clear.
//
// The module is pure — it never reaches the server — so it is the one file in the
// offline layer that can be pinned by a table test.

import type { FieldPatch, FieldValue } from "./merge";

// project is the rule every projection below applies: read the named fields, and
// leave a nullish one off. It is private so a caller cannot invent a second
// field list, which is the failure this module exists to prevent.
function project(row: object, fields: readonly string[]): FieldPatch {
  const record = row as Record<string, FieldValue>;
  const patch: FieldPatch = {};
  for (const field of fields) {
    const value = record[field];
    if (value === undefined || value === null) continue;
    patch[field] = value;
  }
  return patch;
}

// Five of the field lists below are exported, and only for the guard that pins
// them to the columns their whole-row handlers bind (see projections.test.ts).
// The list is the thing that guard has to read: checked through a fixture's
// output, it is checked only where that fixture reaches, so a field added to a
// list the fixture happens not to carry would slip through — and an added field is
// exactly what breaks a whole-row family. They are `as const`, so exporting them
// does not make one writable, and `project` stays private, so nothing here has
// become a second way to say what a projection reads.

const TRANSACTION_FIELDS = [
  "accountId",
  "date",
  "description",
  "amount",
  "type",
  "categoryId",
  // What the row holds, complete. transaction.tags writes the same column as a
  // delta, which is a different question about it and lives at that op's entry.
  "tags",
  "notes",
  "payeeId",
  "billingCycleId",
  // The two attachments the row-naming writes set, and the only evidence on the
  // row that somebody else moved one: a projection without them reads as "no
  // attachment", and a merge against that overwrites a concurrent attachment
  // instead of holding it.
  "loanAccountId",
  "recurringSeriesId",
] as const;

const ACCOUNT_FIELDS = [
  "name",
  "accountTypeId",
  "bank",
  "currency",
  "color",
  "isDefault",
  "closed",
  "billingDay",
] as const;

const ACCOUNT_TYPE_FIELDS = ["name", "positiveTxnType"] as const;

const GROUP_FIELDS = ["name", "icon", "color"] as const;

const CATEGORY_FIELDS = ["name", "icon", "color", "groupId"] as const;

export const PAYEE_FIELDS = ["name", "accountId"] as const;

export const RULE_FIELDS = [
  "pattern",
  "matchType",
  "categoryId",
  "payeeId",
  "priority",
  "accountId",
  "filterCategoryId",
  "filterPayeeId",
  "minAmount",
  "maxAmount",
  "txnType",
  "dateFrom",
  "dateTo",
  "isLinked",
  "isRecurring",
  "addTags",
  "notes",
] as const;

export const SERIES_FIELDS = [
  "accountId",
  "name",
  "description",
  "amount",
  "type",
  "frequency",
  "interval",
  "startDate",
  "endDate",
  "categoryId",
  "payeeId",
  "active",
  "notes",
] as const;

export const TERM_FIELDS = [
  // The series is part of the term's row rather than of its own address: it is
  // what the write endpoint needs, and no other read can supply it.
  "seriesId",
  "startDate",
  "endDate",
  "amount",
  "accountId",
] as const;

// The loan's *terms*, not its schedule: the amortization periods are derived
// server-side from these, so a queued edit is a change to the terms and nothing
// else.
export const LOAN_TERMS_FIELDS = [
  "principal",
  "processingFee",
  "disbursalDate",
  "annualRateBps",
  "tenureMonths",
  "startDate",
] as const;

// hasToken is deliberately absent: the settings response reports whether a token
// is set and never carries it, so a projection naming a token field would put on
// the wire a value this client has never read from anywhere.
const SETTINGS_FIELDS = ["paperlessUrl", "paperlessTag", "pageSize"] as const;

export function projectTransaction(row: object): FieldPatch {
  return project(row, TRANSACTION_FIELDS);
}

export function projectAccount(row: object): FieldPatch {
  return project(row, ACCOUNT_FIELDS);
}

export function projectAccountType(row: object): FieldPatch {
  return project(row, ACCOUNT_TYPE_FIELDS);
}

export function projectGroup(row: object): FieldPatch {
  return project(row, GROUP_FIELDS);
}

export function projectCategory(row: object): FieldPatch {
  return project(row, CATEGORY_FIELDS);
}

export function projectPayee(row: object): FieldPatch {
  return project(row, PAYEE_FIELDS);
}

export function projectRule(row: object): FieldPatch {
  return project(row, RULE_FIELDS);
}

export function projectRecurringSeries(row: object): FieldPatch {
  return project(row, SERIES_FIELDS);
}

export function projectRecurringTerm(row: object): FieldPatch {
  return project(row, TERM_FIELDS);
}

export function projectLoanTerms(row: object): FieldPatch {
  return project(row, LOAN_TERMS_FIELDS);
}

export function projectSettings(row: object): FieldPatch {
  return project(row, SETTINGS_FIELDS);
}

// findRow is a family's read: the collection plus a find by id, because these
// families have no single-row GET to ask. A row the collection does not carry is
// a row the server no longer has, which is what null has to mean.
//
// The read is injected rather than reached for here, so that this module never
// calls the server: the wire call belongs to the op that owns it, and the
// projector is the one thing the op and the client have to agree about.
export async function findRow<Row extends { id: string }>(
  read: () => Promise<Row[]>,
  rowId: string,
  project: (row: object) => FieldPatch,
): Promise<FieldPatch | null> {
  const row = (await read()).find((candidate) => candidate.id === rowId);
  return row ? project(row) : null;
}
