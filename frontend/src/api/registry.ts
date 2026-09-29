// The op registry: what each queued write reads, and how it sends.
//
// A queued edit is a field-level patch, and the flush has to turn it into a
// request for one of nineteen endpoints. Those endpoints do not agree on what a
// payload that names some fields and omits others means, so each op declares
// which one it is rather than the registry inferring it from the call below:
//
//   patch       PATCH /transactions/{id} builds its SET clause from the fields
//               present in the body, so an absent field is a field untouched.
//   putPartial  the account, account-type, category, group, admin-category and
//               settings PUTs build a dynamic SET (or COALESCE(NULLIF($n, ''), col)),
//               so an absent key genuinely means "leave it alone".
//   putWhole    the payee, rule, recurring and loan PUTs write every column their
//               request can carry, on every call — payee.go:118 is `UPDATE payees
//               SET name = $1, account_id = $2` and recurring.go:1183 is `SET
//               start_date = $1, end_date = $2, amount = $3, account_id = $4` — so
//               a payload that omits a field *clears* it. Their diff has to be
//               overlaid onto the server's row before it goes out, which is why
//               their read exists and why `theirs` is handed to `apply`.
//
// Getting that wrong is silent: the request succeeds and a column the user never
// opened is emptied. `shape` is therefore data on the op, and the only place it
// is written down.
//
// The read side has one rule for the same reason: the server's current row, or
// null when the server no longer has it. Every read here is issued `live`, so
// the offline read cache cannot answer it — a merge answered from this browser's
// own last belief of the row can only agree with itself, and would overwrite
// whatever changed since (see outbox.ts's TheirsReader).

import api from "./client";
import { ApiError } from "./errors";
import type { FieldPatch, FieldValue } from "./merge";
import {
  findRow,
  projectAccount,
  projectAccountType,
  projectCategory,
  projectGroup,
  projectLoanTerms,
  projectPayee,
  projectRecurringSeries,
  projectRecurringTerm,
  projectRule,
  projectSettings,
  projectTransaction,
} from "./projections";
import type { WriteOp } from "./outbox";
import type {
  CreateTransactionRequest,
  LoanScheduleRequest,
  UpdateAccountRequest,
  UpdateAccountTypeRequest,
  UpdateCategoryGroupRequest,
  UpdateCategoryRequest,
  UpdatePayeeRequest,
  UpdateRecurringSeriesRequest,
  UpdateRecurringSeriesTermRequest,
  UpdateRuleRequest,
  UpdateTransactionRequest,
  UpdateUserSettingsRequest,
} from "../types";

// ApplyShape is what an op's endpoint does with a payload that names some fields
// and omits others.
export type ApplyShape = "patch" | "putPartial" | "putWhole";

export interface OpSpec {
  op: WriteOp;
  shape: ApplyShape;
  /**
   * The mergeable projection of a row, or null when it is gone. Live, never
   * cached. `rowId` is the identifier this op addresses: a row's own id for
   * almost every op, the account for a loan schedule, the user for the settings
   * singleton, and a transaction for all seven row-naming writes.
   */
  read(rowId: string): Promise<FieldPatch | null>;
  /**
   * Send the resolved diff. `theirs` is the row the diff was merged onto, and is
   * the only source an op has for an identifier its endpoint also needs (a term's
   * series). For a `putWhole` op it is also the row the diff is overlaid onto, so
   * `diff` arrives at such an op already built into the whole row its endpoint
   * writes — see applyOp.
   */
  apply(rowId: string, diff: FieldPatch, theirs: FieldPatch | null): Promise<void>;
  /**
   * Present only for transaction rows, which can be re-created. `key` is the
   * queue entry's own key, and it is what makes a retry safe: see the note on
   * transaction.patch's reCreate.
   */
  reCreate?(snapshot: FieldPatch, diff: FieldPatch, key: string): Promise<string>;
  /** Present on ops that write one field across named rows. */
  applyMany?(rows: string[], value: FieldValue): Promise<void>;
}

// asRequest is the seam between the merge and each family's request type. A
// FieldPatch says "these fields changed" and nothing about which endpoint they
// are for; the op above is the declaration that pairs the two, so the cast is
// made once here rather than at every wire call.
function asRequest<R>(diff: FieldPatch): R {
  return diff as unknown as R;
}

// The projections — a row's mergeable fields — live in their own module rather
// than here: the client builds the base a user's edit is diffed from with the
// same ones, and a second copy of a field list is how a field would come to be
// mergeable on one side and not the other. This file is the ops: the shapes, the
// reads that reach the server, and the wire calls.

// readTransaction serves the PATCH and all seven row-naming writes: they address
// the same transaction and differ only in how the write travels.
//
// It scopes with `q=id:` and nothing else, and that is load-bearing. The list
// endpoint injects synthetic summary rows — a per-cycle "Total outstanding", a
// month-end "Running balance" — when a single account is filtered and the sort is
// by date, and it guards on accountUUID, which comes from an accountId parameter
// and never from a q= term. Scoping this read by account as well would make
// data[0] a summary row rather than the row being merged.
const readTransaction = async (rowId: string): Promise<FieldPatch | null> => {
  const { data } = await api.getTransactions({ q: `id:${rowId}`, limit: 1 }, { live: true });
  const [row] = data;
  return row ? projectTransaction(row) : null;
};

// readTerm finds a term by its own id. The term endpoints are addressed by series
// and there is no read that lists a user's terms across series, so the series are
// asked in turn; the read is also what supplies the series the write needs (see
// recurringTerm.put's apply).
const readTerm = async (rowId: string): Promise<FieldPatch | null> => {
  const { data: series } = await api.getRecurringSeries({ live: true });
  const terms = await Promise.all(
    series.map((one) => api.getRecurringTerms(one.id, { live: true })),
  );
  const term = terms
    .flatMap((response) => response.data ?? [])
    .find((candidate) => candidate.id === rowId);
  return term ? projectRecurringTerm(term) : null;
};

// REMOVE_TAG marks a tag the entry removes rather than adds. The bulk-tags
// endpoint takes two lists and applyMany is given one value, so the value is the
// only place the direction can live: a registry that guessed would send a
// removal back as an addition and the tag the user took off would come back on
// its own. client.ts writes the same marker when it builds the delta (its
// REMOVE_TAG), and the two are pinned against each other from both sides — one
// of them mocking the other away is the only thing that could keep them honest.
const REMOVE_TAG = "-";

function asTagList(value: FieldValue): string[] {
  // The value is a list by construction — the field this op writes is `tags` —
  // and anything else writes no tags at all, which the endpoint refuses.
  return Array.isArray(value) ? value.map((tag) => String(tag)) : [];
}

// isCleared is the one question the row-naming writes ask about a nullish value,
// and it is the question they do not agree on: it is a clear to BulkCategorize
// (as its own sentinel) and a detach to BulkLoanRequest, and nothing at all to the
// two endpoints that cannot express one.
function isCleared(value: FieldValue): boolean {
  return value === undefined || value === null;
}

// asCategory is the categorize value as the endpoint reads it: a category uuid,
// or the literal "uncategorized" that BulkCategorize clears on — the handler
// sends everything else to uuid.Parse (transaction_bulk.go:35), so an empty
// string is a 400 rather than a clear. Only a nullish value is read as the clear
// here, because "" is not this endpoint's way of saying anything and pretending
// otherwise would be the same guess, inverted.
function asCategory(value: FieldValue): string {
  return isCleared(value) ? "uncategorized" : String(value);
}

// requiredId is the value as the id an endpoint that cannot do without one takes,
// and it refuses a clear rather than sending one. Three of these endpoints bind a
// required uuid.UUID and then guard the write with an EXISTS on it, so the zero
// uuid an empty string unmarshals to matches no row and the answer is 200 with
// updated: 0: a clear the server never performed, reported as one that was.
// Only BulkLoanRequest reads a null as "detach" (its LoanAccountID is a
// *uuid.UUID), and it says so at its own call site — a helper that assumed the
// seven endpoints agreed here was the bug.
//
// An ApiError with a 4xx, for the reason applyOp's putPartial refusal gives one: a
// queued write that cannot be sent is a definite answer about the entry, so the
// flush records it, leaves it for the user, and carries on with the entries behind
// it. A plain Error would be neither recorded nor skipped — the flush rethrows
// anything that is not an ApiError or a NetworkError — so one clear the app asked
// for once would stop every sync from then on.
function requiredId(op: WriteOp, value: FieldValue): string {
  if (isCleared(value) || value === "") {
    throw new ApiError(
      `${op} cannot detach: its endpoint takes a required uuid and has no way to clear one, so a clear is refused here rather than sent as an id that matches no row (the server would answer 200 with updated: 0)`,
      422,
    );
  }
  return String(value);
}

// multiRow is the shape of a write that names its rows and sets one field on
// them: one request, the rows the entry lists, and nothing else. That is the
// contract a PATCH has, which is the shape the family declares. `field` is the
// transaction column the write is about — what the merge reads, and what the
// single-row apply pulls out of the diff — and `request` is the one call the
// endpoint needs, so the two forms of the write cannot disagree about it.
// transaction.tags is not built here: its value is a delta rather than a field,
// and it says why at its own entry.
function multiRow(
  op: WriteOp,
  field: string,
  request: (rows: string[], value: FieldValue) => Promise<unknown>,
): OpSpec {
  return {
    op,
    shape: "patch",
    read: readTransaction,
    applyMany: async (rows, value) => {
      await request(rows, value);
    },
    // The single row of the same request, so an op reached one row at a time
    // takes exactly the path a batch of one takes.
    apply: async (rowId, diff) => {
      await request([rowId], diff[field]);
    },
  };
}

export const OPS: Record<WriteOp, OpSpec> = {
  "transaction.patch": {
    op: "transaction.patch",
    shape: "patch",
    read: readTransaction,
    apply: async (rowId, diff) => {
      // queue: false because the flush is sending an entry that is already in the
      // queue: a request that does not reach the server must not put a second
      // copy of it there.
      await api.updateTransaction(rowId, asRequest<UpdateTransactionRequest>(diff), {
        queue: false,
      });
    },
    reCreate: async (snapshot, diff, key) => {
      // The create body is the row's own projection with the decided diff on top:
      // every field POST /transactions requires (accountId, date, description,
      // amount, type) is in the transaction projection and none of them is
      // nullable, so a re-created row is the row the user edited.
      //
      // The idempotency key is the queue entry's own, not one minted here, and
      // that is the whole of the guarantee: a re-create whose response was lost
      // (a timeout, a killed tab) is attempted again with the same key, so the
      // server returns the row it already created instead of inserting a second
      // one. A key minted per call would be a different key every time, which is
      // a silent duplicate money row on the one path whose entire purpose is to
      // put a row back. Two entries cannot collide — the key is generated per
      // entry, and the server matches it per user.
      //
      // The entry key carries an "e-" prefix (outbox.ts's newEntryKey), which is
      // not cosmetic to strip: the server accepts any string up to
      // maxClientKeyLen (64, transaction.go:35) and `e-` plus a UUID is 38.
      const created = await api.createTransaction(
        asRequest<CreateTransactionRequest>({ ...snapshot, ...diff }),
        { idempotencyKey: key, queue: false },
      );
      if (!created.id) {
        throw new Error("The server accepted the re-created transaction but returned no id");
      }
      return created.id;
    },
  },

  "account.put": {
    op: "account.put",
    shape: "putPartial",
    read: (rowId) => findRow(() => api.getAccounts({ live: true }), rowId, projectAccount),
    apply: async (rowId, diff) => {
      await api.updateAccount(rowId, asRequest<UpdateAccountRequest>(diff));
    },
  },
  "accountType.put": {
    op: "accountType.put",
    shape: "putPartial",
    read: (rowId) =>
      findRow(() => api.getAccountTypes({ live: true }), rowId, projectAccountType),
    apply: async (rowId, diff) => {
      await api.updateAccountType(rowId, asRequest<UpdateAccountTypeRequest>(diff));
    },
  },
  "group.put": {
    op: "group.put",
    shape: "putPartial",
    read: (rowId) => findRow(() => api.getGroups({ live: true }), rowId, projectGroup),
    apply: async (rowId, diff) => {
      await api.updateGroup(rowId, asRequest<UpdateCategoryGroupRequest>(diff));
    },
  },
  "category.put": {
    op: "category.put",
    shape: "putPartial",
    read: (rowId) => findRow(() => api.getCategories({ live: true }), rowId, projectCategory),
    apply: async (rowId, diff) => {
      await api.updateCategory(rowId, asRequest<UpdateCategoryRequest>(diff));
    },
  },
  "adminCategory.put": {
    op: "adminCategory.put",
    shape: "putPartial",
    // The global catalog, not the user's own categories: this op writes the
    // shared row, and a user's copy of the catalog does not carry it.
    read: (rowId) =>
      findRow(
        async () => (await api.getAdminCatalog({ live: true })).categories,
        rowId,
        projectCategory,
      ),
    apply: async (rowId, diff) => {
      await api.updateGlobalCategory(rowId, asRequest<UpdateCategoryRequest>(diff));
    },
  },
  "settings.put": {
    op: "settings.put",
    shape: "putPartial",
    // There is one settings row per user, so the rowId is ignored rather than
    // read: the endpoint is the row.
    read: async () => projectSettings(await api.getUserSettings({ live: true })),
    apply: async (_rowId, diff) => {
      await api.updateUserSettings(asRequest<UpdateUserSettingsRequest>(diff));
    },
  },

  "payee.put": {
    op: "payee.put",
    shape: "putWhole",
    read: (rowId) => findRow(() => api.getPayees({ live: true }), rowId, projectPayee),
    apply: async (rowId, diff) => {
      await api.updatePayee(rowId, asRequest<UpdatePayeeRequest>(diff));
    },
  },
  "rule.put": {
    op: "rule.put",
    shape: "putWhole",
    read: (rowId) => findRow(() => api.getRules({ live: true }), rowId, projectRule),
    apply: async (rowId, diff) => {
      await api.updateRule(rowId, asRequest<UpdateRuleRequest>(diff));
    },
  },
  "recurring.put": {
    op: "recurring.put",
    shape: "putWhole",
    read: (rowId) =>
      findRow(
        async () => (await api.getRecurringSeries({ live: true })).data,
        rowId,
        projectRecurringSeries,
      ),
    apply: async (rowId, diff) => {
      await api.updateRecurringSeries(rowId, asRequest<UpdateRecurringSeriesRequest>(diff));
    },
  },
  "recurringTerm.put": {
    op: "recurringTerm.put",
    shape: "putWhole",
    read: readTerm,
    apply: async (rowId, diff, theirs) => {
      // The term endpoint takes a series and a term, and a term carries only its
      // own id, so the series comes off the row the diff was merged onto. There is
      // no other source for it, and applyOp has already refused a row the server
      // does not have — what is left is a row that names no series, which has
      // nothing to address and is refused rather than guessed at.
      const seriesId = theirs?.seriesId;
      if (typeof seriesId !== "string") {
        throw new Error(
          `recurringTerm.put cannot address the term ${rowId}: its row is gone, so nothing names the series it belongs to`,
        );
      }
      await api.updateRecurringTerm(
        seriesId,
        rowId,
        asRequest<UpdateRecurringSeriesTermRequest>(diff),
      );
    },
  },
  "loanSchedule.put": {
    op: "loanSchedule.put",
    shape: "putWhole",
    // The row this op addresses is the account: the loan schedule is keyed by
    // the account it belongs to and has no id of its own to name.
    read: async (rowId) => {
      const { schedule } = await api.getLoanSchedule(rowId, { live: true });
      // A loan with no schedule is not a gone row. saveLoanSchedule upserts the
      // terms, so there is nothing that could have been deleted, and answering
      // null would hold the write for the user over a row the endpoint is about
      // to create — an empty row is what it will find.
      return schedule ? projectLoanTerms(schedule) : {};
    },
    apply: async (rowId, diff) => {
      await api.saveLoanSchedule(rowId, asRequest<LoanScheduleRequest>(diff));
    },
  },

  "transaction.categorize": multiRow(
    "transaction.categorize",
    "categoryId",
    (rows, value) => api.bulkCategorize({ transactionIds: rows, categoryId: asCategory(value) }),
  ),
  "transaction.payee": multiRow("transaction.payee", "payeeId", (rows, value) =>
    api.bulkUpdatePayee({ transactionIds: rows, payeeId: requiredId("transaction.payee", value) }),
  ),
  "transaction.billingCycle": multiRow(
    "transaction.billingCycle",
    "billingCycleId",
    (rows, value) =>
      api.bulkUpdateBillingCycle({
        transactionIds: rows,
        billingCycleId: requiredId("transaction.billingCycle", value),
      }),
  ),

  // The one op of the seven that is not a multiRow, and the reason is the field.
  // "tags" is two different questions about one column: the projection answers
  // "what does the row hold" (the complete list, which is what the merge needs to
  // decide whether the tags changed at all), and the write answers "what changes"
  // (a delta of additions and removals, the only thing BulkUpdateTagsRequest can
  // express). Reading the row's list as that delta would *add* a tag the user
  // removed, so the single-row form is refused rather than guessed at.
  "transaction.tags": {
    op: "transaction.tags",
    shape: "patch",
    read: readTransaction,
    applyMany: async (rows, value) => {
      const add: string[] = [];
      const remove: string[] = [];
      for (const tag of asTagList(value)) {
        if (tag.startsWith(REMOVE_TAG)) remove.push(tag.slice(REMOVE_TAG.length));
        else add.push(tag);
      }
      await api.bulkUpdateTags({ transactionIds: rows, add, remove });
    },
    apply: async () => {
      // An ApiError with a 4xx, like the other two refusals in this file and for
      // their reason: this is an answer about the write — the endpoint cannot make
      // it — and not a broken invariant, so the flush has to record it on the entry
      // and carry on rather than rethrow it and stop every sync behind it. It is
      // unreachable today (no call site enqueues this op as a single-row edit, and
      // a bulk entry reaches applyMany), which is exactly why the class is pinned
      // by a sweep in registry.test.ts rather than left to this one call site: the
      // single-row tag edit is the obvious next thing to add, and it would arrive
      // as a plain Error by default.
      throw new ApiError(
        "transaction.tags has no single-row form: a tag change is a delta of additions and removals, which only POST /transactions/bulk-tags can express, and a field-level value here is the row's tag list — read as a delta it would add back the tag the user removed",
        422,
      );
    },
  },

  "transaction.loan": multiRow("transaction.loan", "loanAccountId", (rows, value) =>
    // The one endpoint in this file that reads a null as "detach":
    // BulkLoanRequest.LoanAccountID is a *uuid.UUID, and an absent one detaches.
    api.bulkLoan({ transactionIds: rows, loanAccountId: isCleared(value) ? null : String(value) }),
  ),
  "transaction.recurring": multiRow(
    "transaction.recurring",
    "recurringSeriesId",
    (rows, value) =>
      isCleared(value)
        ? api.detachRecurring({ transactionIds: rows })
        : api.attachRecurring({ seriesId: String(value), transactionIds: rows }),
  ),
  "transaction.loanDisbursement": multiRow(
    "transaction.loanDisbursement",
    "loanAccountId",
    // The write is one credit, so the entry names one row and the loan account
    // rides as the value: a disbursement is not a column on the transaction, so
    // there is no field of the row to carry it and no other channel to send it
    // through. The read is still the transaction's, and it is what says whether
    // the credit is still there to be linked.
    (rows, value) =>
      api.linkLoanDisbursement(
        requiredId("transaction.loanDisbursement", value),
        { transactionId: rows[0] },
      ),
  ),
};

// readTheirs is the reader the flush merges against. It is this function rather
// than OPS[op].read at the call site, so "the server's row, never the offline
// cache" has exactly one implementation to get wrong.
export function readTheirs(op: WriteOp, rowId: string): Promise<FieldPatch | null> {
  return OPS[op].read(rowId);
}

// mergedRow is the payload a putWhole op is sent: the diff overlaid onto the
// server's own row, because these endpoints write every column their request can
// carry whether the body names it or not. Sending the diff alone would therefore
// write a row the form never showed — the account a payee was attached to, the
// end date of a term, the rate on a loan — and the write would succeed, so the
// only evidence of it would be a column the user never opened.
//
// The overlay is field by field rather than a spread so an `undefined` can be
// dropped. `FieldValue` admits one and `theirs` is a patch rather than a parsed
// row, so a key carrying no value is representable; JSON.stringify would drop it
// on the way out, leaving a body quietly narrower than the merge that built it.
// The two inputs cannot produce one today — project() leaves a nullish field off
// and diffAgainstBase skips an undefined value — so this is the overlay declining
// to be the thing that introduces one, and it is where the rule is cheapest to
// keep.
//
// `theirs === null` is refused rather than overlaid. A row the server no longer
// has is a fact about the row, and the flush records it as one (outbox.ts's
// recordGone) so the user can be told; an overlay of nothing is the bare diff
// again, which is the wipe this function exists to prevent.
function mergedRow(
  op: WriteOp,
  rowId: string,
  diff: FieldPatch,
  theirs: FieldPatch | null,
): FieldPatch {
  if (theirs === null) {
    throw new Error(
      `cannot send a ${op} edit to ${rowId}: the row is gone, so there is no copy of it to overlay the diff onto, and this endpoint writes every column the body names or not`,
    );
  }
  const row: FieldPatch = {};
  for (const [field, value] of Object.entries({ ...theirs, ...diff })) {
    if (value !== undefined) row[field] = value;
  }
  return row;
}

// applyOp is the one seam a resolved diff is sent through, and the declared shape
// is what it acts on: a putPartial op is refused a clear to "", which its
// endpoints read as "not provided", and a putWhole op is sent the diff overlaid
// onto theirs, since its endpoint writes every column. Both are silent failures
// without this — a request the server accepts and a column it empties — so both
// are decided here, where `theirs` and the diff are already in hand.
export async function applyOp(
  op: WriteOp,
  rowId: string,
  diff: FieldPatch,
  theirs: FieldPatch | null,
): Promise<void> {
  const spec = OPS[op];
  if (spec.shape === "putPartial") {
    // The one thing a putPartial diff can say that the endpoint cannot do. These
    // handlers write `col = COALESCE(NULLIF($n, ''), col)` — account.go:277-291,
    // account_type.go:136, category.go:133 and :326, category_group.go:122 — and
    // paperless.go:478-508 builds the same update clause by clause behind
    // `if req.X != nil`. So an empty string is how those endpoints say "not
    // provided": the write succeeds, the column keeps the value it had, and a
    // flush that removed the entry as applied would have reported a clear the
    // server never performed. That is the silent discard this whole feature
    // exists to prevent, one layer below the obvious one. A clear is expressed by
    // omitting the key, so there is nothing to send and the edit is refused here
    // instead — a `patch` or `putWhole` op is not touched, because an empty string
    // is a value those endpoints do write.
    //
    // All of them, not the first: the entry is one thing the user is asked to
    // decide as a whole, and a message naming one field would have them fix that
    // one and be held again for the next.
    const cleared = Object.keys(diff).filter((field) => diff[field] === "");
    if (cleared.length > 0) {
      // An ApiError with a 4xx, because that is the branch the flush treats as a
      // definite answer about the entry: it records the message on it, leaves it
      // queued for the user, and carries on with the entries behind it (see
      // outbox.ts isRejection). A plain Error here would be neither recorded nor
      // skipped — the flush rethrows anything that is not an ApiError or a
      // NetworkError, so the refusal would take the whole sync down with it and
      // the user would be told nothing at all.
      throw new ApiError(
        `${cleared.join(", ")} cannot be cleared to an empty value by ${op}: this endpoint reads "" as "not provided" and leaves the field as it was, so the request would succeed without doing anything. Clear the field by removing it instead.`,
        422,
      );
    }
  }
  if (spec.shape === "putWhole") {
    return spec.apply(rowId, mergedRow(op, rowId, diff, theirs), theirs);
  }
  return spec.apply(rowId, diff, theirs);
}
