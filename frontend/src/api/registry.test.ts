import { describe, it, expect, beforeEach, vi } from "vitest";
import type {
  Account,
  AccountType,
  AdminCatalog,
  Category,
  CategoryGroup,
  LoanSchedule,
  LoanScheduleDetail,
  Payee,
  RecurringSeries,
  RecurringSeriesTerm,
  Rule,
  Transaction,
  UserSettings,
} from "../types";
import type { FieldPatch, FieldValue } from "./merge";
import { enqueueEdit, flushOutbox, getOutboxSnapshot, resolveConflict, type QueuedWrite, type WriteOp } from "./outbox";
import { ApiError } from "./errors";
import { OPS, applyOp, readTheirs, type ApplyShape } from "./registry";

const { apiMocks } = vi.hoisted(() => ({
  apiMocks: {
    getTransactions: vi.fn(),
    createTransaction: vi.fn(),
    updateTransaction: vi.fn(),
    getAccounts: vi.fn(),
    updateAccount: vi.fn(),
    getAccountTypes: vi.fn(),
    updateAccountType: vi.fn(),
    getGroups: vi.fn(),
    updateGroup: vi.fn(),
    getCategories: vi.fn(),
    updateCategory: vi.fn(),
    getAdminCatalog: vi.fn(),
    updateGlobalCategory: vi.fn(),
    getPayees: vi.fn(),
    updatePayee: vi.fn(),
    getRules: vi.fn(),
    updateRule: vi.fn(),
    getRecurringSeries: vi.fn(),
    updateRecurringSeries: vi.fn(),
    getRecurringTerms: vi.fn(),
    updateRecurringTerm: vi.fn(),
    getLoanSchedule: vi.fn(),
    saveLoanSchedule: vi.fn(),
    getUserSettings: vi.fn(),
    updateUserSettings: vi.fn(),
    bulkCategorize: vi.fn(),
    bulkUpdatePayee: vi.fn(),
    bulkUpdateBillingCycle: vi.fn(),
    bulkUpdateTags: vi.fn(),
    bulkLoan: vi.fn(),
    attachRecurring: vi.fn(),
    detachRecurring: vi.fn(),
    linkLoanDisbursement: vi.fn(),
  },
}));

// The registry reaches the server only through client.ts, so mocking that module
// is also the statement of which calls a queued write is allowed to make. The
// module's `newClientKey` is not mocked on purpose: nothing here may mint a key,
// so a call to it would be a TypeError rather than a silently wrong value.
vi.mock("./client", () => ({ default: apiMocks }));

const TRANSACTION: Transaction = {
  id: "t1",
  accountId: "a1",
  date: "2026-01-15",
  description: "Coffee",
  amount: 250.5,
  type: "debit",
  categoryId: null,
  tags: [],
  notes: "old",
  payeeId: null,
  billingCycleId: null,
  loanAccountId: null,
  recurringSeriesId: null,
};

const ACCOUNT: Account = {
  id: "a1",
  name: "Wallet",
  accountTypeId: "at1",
  bank: "HDFC",
  currency: "INR",
  color: "#fff",
  isDefault: true,
  closed: false,
  balance: 100,
};

const ACCOUNT_TYPE: AccountType = {
  id: "at1",
  name: "Savings",
  positiveTxnType: "credit",
};

const GROUP: CategoryGroup = {
  id: "g1",
  name: "Food",
  icon: "utensils",
  color: "#f00",
  isBase: true,
  isGlobal: false,
  sortOrder: 0,
};

const CATEGORY: Category = {
  id: "c1",
  name: "Coffee",
  icon: "cup",
  color: "#0f0",
  groupId: "g1",
};

const PAYEE: Payee = { id: "p1", name: "Cafe", accountId: null };

const RULE: Rule = {
  id: "r1",
  pattern: "coffee",
  matchType: "contains",
  categoryId: "c1",
  priority: 10,
};

const SERIES: RecurringSeries = {
  id: "s1",
  accountId: "a1",
  name: "Netflix",
  description: "",
  amount: 649,
  type: "debit",
  frequency: "monthly",
  interval: 1,
  startDate: "2026-01-01",
  endDate: null,
  categoryId: null,
  payeeId: null,
  active: true,
  notes: "",
  monthlyAmount: 649,
  attachedCount: 0,
};

const TERM: RecurringSeriesTerm = {
  id: "term-1",
  seriesId: "s1",
  startDate: "2026-01-01",
  endDate: "2026-06-01",
  amount: 649,
  accountId: "a1",
};

const TERMS: LoanSchedule = {
  id: "ls1",
  loanAccountId: "a1",
  principal: 100000,
  processingFee: 1000,
  annualRateBps: 950,
  tenureMonths: 24,
  startDate: "2026-01-01",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
};

function loanDetail(schedule: LoanSchedule | null): LoanScheduleDetail {
  return {
    schedule,
    emi: 0,
    totalInterest: 0,
    totalPayable: 0,
    entries: [],
    paidInstallments: 0,
    paidAmount: 0,
    principalPaid: 0,
    interestPaid: 0,
    outstandingPrincipal: 0,
    transfers: [],
    completed: false,
  };
}

const SETTINGS: UserSettings = {
  paperlessUrl: "https://paperless.example",
  hasToken: true,
  paperlessTag: "fintrak",
  pageSize: 50,
};

// The two optional members are narrowed rather than asserted with a bang, the
// same way outbox.ts narrows a queue entry: a spec that lost one is a bug worth
// hearing about.
async function applyMany(
  op: WriteOp,
  rows: string[],
  value: FieldValue,
): Promise<void> {
  const spec = OPS[op];
  if (!spec.applyMany) throw new Error(`${op} declares no applyMany`);
  await spec.applyMany(rows, value);
}

async function reCreate(
  op: WriteOp,
  snapshot: FieldPatch,
  diff: FieldPatch,
  key: string,
): Promise<string> {
  const spec = OPS[op];
  if (!spec.reCreate) throw new Error(`${op} declares no reCreate`);
  return spec.reCreate(snapshot, diff, key);
}

// The whole-row family, and where each of its five endpoints keeps the payload it
// was given. A term is the only one addressed by two ids, so its body is the
// third argument rather than the second.
const WHOLE_ROW_BODY: Record<string, [keyof typeof apiMocks, number]> = {
  "payee.put": ["updatePayee", 1],
  "rule.put": ["updateRule", 1],
  "recurring.put": ["updateRecurringSeries", 1],
  "recurringTerm.put": ["updateRecurringTerm", 2],
  "loanSchedule.put": ["saveLoanSchedule", 1],
};

// sentBody reads back what one of the five whole-row endpoints was last given. The
// overlay is a change to the *body* and to nothing else — every one of these
// writes is a single PUT of one object — so the body is the only thing a test
// about it can look at.
function sentBody(op: WriteOp): FieldPatch {
  const at = WHOLE_ROW_BODY[op];
  if (!at) throw new Error(`${op} is not a whole-row op`);
  const [method, index] = at;
  const call = apiMocks[method].mock.calls.at(-1);
  if (!call) throw new Error(`${op} sent nothing`);
  return call[index] as FieldPatch;
}

// Every op whose entry names its rows, and the field of the transaction each one
// writes — the two things the flush needs to build the request.
const MULTI_ROW: WriteOp[] = [
  "transaction.categorize",
  "transaction.payee",
  "transaction.billingCycle",
  "transaction.tags",
  "transaction.loan",
  "transaction.recurring",
  "transaction.loanDisbursement",
];

// The eleven single-row PUTs, split by what their endpoint does with a payload
// that names some fields and omits others.
const PUT_PARTIAL: WriteOp[] = [
  "account.put",
  "accountType.put",
  "group.put",
  "category.put",
  "adminCategory.put",
  "settings.put",
];

const PUT_WHOLE: WriteOp[] = [
  "payee.put",
  "rule.put",
  "recurring.put",
  "recurringTerm.put",
  "loanSchedule.put",
];

describe("declared apply shapes", () => {
  it.each<[WriteOp, ApplyShape]>([
    ["transaction.patch", "patch"],
    ["account.put", "putPartial"],
    ["category.put", "putPartial"],
    ["settings.put", "putPartial"],
    ["payee.put", "putWhole"],
    ["recurringTerm.put", "putWhole"],
    ["loanSchedule.put", "putWhole"],
  ])("%s is %s", (op, shape) => {
    expect(OPS[op].shape).toBe(shape);
  });

  // The rest of the two PUT families, so the table above cannot be right by
  // covering only the interesting rows of each family.
  it.each<[WriteOp, ApplyShape]>([
    ...PUT_PARTIAL.map((op): [WriteOp, ApplyShape] => [op, "putPartial"]),
    ...PUT_WHOLE.map((op): [WriteOp, ApplyShape] => [op, "putWhole"]),
    ...MULTI_ROW.map((op): [WriteOp, ApplyShape] => [op, "patch"]),
  ])("%s is %s", (op, shape) => {
    expect(OPS[op].shape).toBe(shape);
  });

  it("declares every op in the union and no others", () => {
    expect(Object.keys(OPS).sort()).toEqual(
      [
        "transaction.patch",
        ...MULTI_ROW,
        ...PUT_PARTIAL,
        ...PUT_WHOLE,
      ].sort(),
    );
  });

  it("gives applyMany to the writes that name their rows, and to nothing else", () => {
    const singleRow: WriteOp[] = [...PUT_PARTIAL, ...PUT_WHOLE, "transaction.patch"];
    for (const op of MULTI_ROW) expect(typeof OPS[op].applyMany).toBe("function");
    for (const op of singleRow) expect(OPS[op].applyMany).toBeUndefined();
  });

  it("gives reCreate to the transaction op alone, the only row a user can write again", () => {
    const recreatable = (Object.keys(OPS) as WriteOp[]).filter((op) => OPS[op].reCreate);
    expect(recreatable).toEqual(["transaction.patch"]);
  });
});

describe("reading the server's row", () => {
  beforeEach(() => {
    for (const fn of Object.values(apiMocks)) fn.mockReset();
    apiMocks.getTransactions.mockResolvedValue({
      data: [TRANSACTION],
      total: 1,
      page: 1,
      pages: 1,
    });
    apiMocks.getAccounts.mockResolvedValue([ACCOUNT]);
    apiMocks.getAccountTypes.mockResolvedValue([ACCOUNT_TYPE]);
    apiMocks.getGroups.mockResolvedValue([GROUP]);
    apiMocks.getCategories.mockResolvedValue([CATEGORY]);
    apiMocks.getPayees.mockResolvedValue([PAYEE]);
    apiMocks.getRules.mockResolvedValue([RULE]);
    apiMocks.getAdminCatalog.mockResolvedValue({
      groups: [GROUP],
      categories: [CATEGORY],
    } as AdminCatalog);
    apiMocks.getRecurringSeries.mockResolvedValue({ data: [SERIES] });
    apiMocks.getRecurringTerms.mockResolvedValue({ data: [] });
    apiMocks.getLoanSchedule.mockResolvedValue(loanDetail(TERMS));
    apiMocks.getUserSettings.mockResolvedValue(SETTINGS);
  });

  it("reads one transaction by id, scoped with q alone", async () => {
    await readTheirs("transaction.patch", "t1");
    // No accountId, and never one: the list endpoint injects synthetic summary
    // rows (per-cycle "Total outstanding", month-end "Running balance") when a
    // single account is filtered and the sort is by date, and it guards on
    // accountUUID, which only an accountId parameter sets — a q= term never
    // reaches it. Scoping this read by account would make data[0] a summary row
    // that is not the row being merged.
    expect(apiMocks.getTransactions).toHaveBeenCalledWith(
      { q: "id:t1", limit: 1 },
      { live: true },
    );
  });

  it("answers null for a transaction the server no longer has", async () => {
    apiMocks.getTransactions.mockResolvedValue({
      data: [],
      total: 0,
      page: 1,
      pages: 0,
    });
    expect(await readTheirs("transaction.patch", "t1")).toBeNull();
  });

  it("projects a transaction to its mergeable fields, leaving a nullish one off", async () => {
    const row = await readTheirs("transaction.patch", "t1");
    expect(row).toEqual({
      accountId: "a1",
      date: "2026-01-15",
      description: "Coffee",
      amount: 250.5,
      type: "debit",
      // An empty list is a value, not an absence: emptying every tag is a change
      // the user made, and a projection that dropped [] would lose it.
      tags: [],
      notes: "old",
    });
    // categoryId, payeeId, billingCycleId, loanAccountId and recurringSeriesId are
    // all null here, and absent stays distinct from null (see merge.ts).
    expect("categoryId" in row!).toBe(false);
  });

  it("keeps the loan and subscription attachment in the projection", async () => {
    // The two multi-row writes that attach a transaction to a loan or a series
    // merge on exactly these fields, and a projection without them reads as "the
    // server has no attachment" — so a concurrent attachment would be overwritten
    // rather than held.
    apiMocks.getTransactions.mockResolvedValue({
      data: [{ ...TRANSACTION, loanAccountId: "a1", recurringSeriesId: "s1" }],
      total: 1,
      page: 1,
      pages: 1,
    });
    expect(await readTheirs("transaction.loan", "t1")).toEqual({
      accountId: "a1",
      date: "2026-01-15",
      description: "Coffee",
      amount: 250.5,
      type: "debit",
      tags: [],
      notes: "old",
      loanAccountId: "a1",
      recurringSeriesId: "s1",
    });
  });

  it("finds the row in its family's collection, and null once the server has not got it", async () => {
    expect(await readTheirs("account.put", "a1")).toMatchObject({
      name: "Wallet",
      bank: "HDFC",
      currency: "INR",
    });
    apiMocks.getAccounts.mockResolvedValue([]);
    expect(await readTheirs("account.put", "a1")).toBeNull();
  });

  it("reads a family that answers with a wrapper, not a bare list", async () => {
    expect(await readTheirs("recurring.put", "s1")).toMatchObject({ name: "Netflix" });
    expect(await readTheirs("adminCategory.put", "c1")).toMatchObject({ name: "Coffee" });
    expect(await readTheirs("accountType.put", "at1")).toMatchObject({
      name: "Savings",
      positiveTxnType: "credit",
    });
    expect(await readTheirs("group.put", "g1")).toMatchObject({ name: "Food" });
    expect(await readTheirs("category.put", "c1")).toMatchObject({ groupId: "g1" });
    expect(await readTheirs("payee.put", "p1")).toMatchObject({ name: "Cafe" });
    expect(await readTheirs("rule.put", "r1")).toMatchObject({ pattern: "coffee" });
  });

  it("answers the settings singleton, and never with a token it does not hold", async () => {
    // The response carries hasToken and not the token itself, so a projection
    // that named a token field would put a value on the wire the client never
    // read from anywhere.
    const row = await readTheirs("settings.put", "singleton");
    expect(row).toEqual({
      paperlessUrl: "https://paperless.example",
      paperlessTag: "fintrak",
      pageSize: 50,
    });
    expect(apiMocks.getUserSettings).toHaveBeenCalledWith({ live: true });
  });

  it("reads a loan's terms off the account, which is what its rowId addresses", async () => {
    expect(await readTheirs("loanSchedule.put", "a1")).toEqual({
      principal: 100000,
      processingFee: 1000,
      annualRateBps: 950,
      tenureMonths: 24,
      startDate: "2026-01-01",
    });
    expect(apiMocks.getLoanSchedule).toHaveBeenCalledWith("a1", { live: true });
  });

  it("reads a loan with no schedule as an empty row, not a gone one", async () => {
    // saveLoanSchedule upserts the terms, so a loan without one has nothing that
    // could have been deleted: answering null would hold the write for the user
    // over a row the endpoint is about to create.
    apiMocks.getLoanSchedule.mockResolvedValue(loanDetail(null));
    expect(await readTheirs("loanSchedule.put", "a1")).toEqual({});
  });

  it("finds a recurring term, and the series that is the only way to address it", async () => {
    apiMocks.getRecurringTerms.mockImplementation(async (id: string) => ({
      data: id === "s1" ? [TERM] : [],
    }));
    expect(await readTheirs("recurringTerm.put", "term-1")).toEqual({
      seriesId: "s1",
      startDate: "2026-01-01",
      endDate: "2026-06-01",
      amount: 649,
      accountId: "a1",
    });
  });
});

describe("sending a diff", () => {
  beforeEach(() => {
    for (const fn of Object.values(apiMocks)) fn.mockReset();
  });

  it("PATCHes a transaction with the diff and nothing else", async () => {
    await applyOp("transaction.patch", "t1", { notes: "milk" }, null);
    expect(apiMocks.updateTransaction).toHaveBeenCalledWith(
      "t1",
      { notes: "milk" },
      { queue: false },
    );
  });

  it.each<[WriteOp, keyof typeof apiMocks, string]>([
    ["account.put", "updateAccount", "a1"],
    ["accountType.put", "updateAccountType", "at1"],
    ["group.put", "updateGroup", "g1"],
    ["category.put", "updateCategory", "c1"],
    ["adminCategory.put", "updateGlobalCategory", "c1"],
  ])("%s sends its diff unchanged, because its endpoint leaves an omitted key alone", async (op, method, rowId) => {
    await applyOp(op, rowId, { name: "New" }, { name: "Old" });
    expect(apiMocks[method]).toHaveBeenCalledWith(rowId, { name: "New" });
  });

  // Review Focus #2. account.go:277-291 writes bank = COALESCE(NULLIF($3, ''),
  // bank), so an empty string means "not provided" — the server would ignore the
  // clear and the queue would remove the entry as "applied". Refuse it instead.
  it("refuses a putPartial clear to an empty string rather than reporting it applied", async () => {
    await expect(applyOp("account.put", "a1", { bank: "" }, { bank: "HDFC" })).rejects.toThrow(
      /cannot be cleared to an empty value/i,
    );
  });

  // The refusal is the family's, not one op's: it is keyed on the declared shape,
  // so every putPartial op carries it. The status is part of what it is — the
  // flush records the message on an entry for a 4xx and rethrows anything that is
  // not an ApiError or a NetworkError (see outbox.ts isRejection), so a plain
  // Error here would abort the whole sync and tell the user nothing.
  it.each<[WriteOp, keyof typeof apiMocks, string]>([
    ["account.put", "updateAccount", "a1"],
    ["accountType.put", "updateAccountType", "at1"],
    ["group.put", "updateGroup", "g1"],
    ["category.put", "updateCategory", "c1"],
    ["adminCategory.put", "updateGlobalCategory", "c1"],
  ])("%s refuses a clear to an empty string, whatever the field", async (op, method, rowId) => {
    const refusal = await applyOp(op, rowId, { name: "" }, { name: "Old" }).then(
      () => null,
      (err: unknown) => err as ApiError,
    );

    // Named for the entry it fails: the field, so the user knows which clear was
    // refused, and the op, so a queue holding several of them says which.
    expect(refusal).toBeInstanceOf(ApiError);
    expect(refusal?.message).toContain("name cannot be cleared to an empty value");
    expect(refusal?.message).toContain(op);
    expect(refusal?.status).toBeGreaterThanOrEqual(400);
    expect(refusal?.status).toBeLessThan(500);
    // And nothing went out: a request the server would answer 200 without
    // changing anything is the failure this refuses to commit.
    expect(apiMocks[method]).not.toHaveBeenCalled();
  });

  // One refusal, every field it is refusing. Naming only the first means a diff
  // with two empty strings is held once, the user fixes that one, and the same
  // entry is held again for the other — one field at a time through a queue entry
  // the user is asked to decide as a whole.
  it("names every field a putPartial diff cannot clear, not just the first", async () => {
    const refusal = await applyOp("account.put", "a1", { bank: "", color: "" }, {
      bank: "HDFC",
      color: "#fff",
    }).then(
      () => null,
      (err: unknown) => err as ApiError,
    );

    expect(refusal?.message).toContain("bank, color cannot be cleared to an empty value");
    expect(apiMocks.updateAccount).not.toHaveBeenCalled();
  });

  // The settings singleton is the sixth. Its endpoint is the odd one of the six —
  // paperless.go:478-508 builds its SET clause by clause behind `if req.X != nil`
  // rather than with COALESCE(NULLIF(...)), so a "" handed to it is a value it
  // would store — and the refusal is the family's anyway. A queued diff says
  // which fields changed, this family is declared putPartial, and over-refusing
  // is loud: the entry is marked with the reason and the user is told. Leaving it
  // to a per-endpoint judgement is the silent discard this feature exists to
  // prevent.
  it("refuses an empty-string clear on the settings singleton too", async () => {
    await expect(
      applyOp("settings.put", "singleton", { paperlessTag: "" }, { paperlessTag: "fintrak" }),
    ).rejects.toThrow(/paperlessTag cannot be cleared to an empty value by settings\.put/i);
    expect(apiMocks.updateUserSettings).not.toHaveBeenCalled();
  });

  // The guard reads the declared shape, so it must not reach across it. A PATCH
  // builds its SET from the fields present in the body and a putWhole writes every
  // column, and both write an empty string as a value — refusing there would
  // refuse a write the endpoint performs.
  it("lets a patch and a whole-row write send an empty string, which those endpoints do write", async () => {
    await applyOp("transaction.patch", "t1", { notes: "" }, null);
    expect(apiMocks.updateTransaction).toHaveBeenCalledWith(
      "t1",
      { notes: "" },
      { queue: false },
    );

    await applyOp("payee.put", "p1", { name: "" }, { name: "Old" });
    expect(apiMocks.updatePayee).toHaveBeenCalledWith("p1", { name: "" });
  });

  it("sends the settings singleton's diff, which is addressed by nothing", async () => {
    // The rowId is the singleton's own name for this op and is ignored: there is
    // one settings row per user, so the endpoint takes the body alone.
    await applyOp("settings.put", "singleton", { paperlessTag: "new" }, { paperlessTag: "fintrak" });
    expect(apiMocks.updateUserSettings).toHaveBeenCalledWith({ paperlessTag: "new" });
  });

  // Review Focus #5. payee.go:118 writes every column every time, so a bare diff
  // nulls account_id.
  it("sends the merged row for a putWhole op, never the bare diff", async () => {
    await applyOp("payee.put", "p1", { name: "New" }, { name: "Old", accountId: "a1" });
    expect(sentBody("payee.put")).toEqual({ name: "New", accountId: "a1" });
  });

  it("covers the whole-row family, and the table above is its whole content", () => {
    // Five endpoints and one overlay: a table that quietly left one of them out
    // would make the family look covered when one column wipe survives it.
    expect(Object.keys(WHOLE_ROW_BODY).sort()).toEqual([...PUT_WHOLE].sort());
  });

  // The sharpest of the five, and the reason the family is called whole-row:
  // recurring.go:1183 writes `SET start_date = $1, end_date = $2, amount = $3,
  // account_id = $4` with no COALESCE and no guard, so a body naming only the
  // amount clears the end date of a term the user was closing.
  it("carries a whole term, so a diff that omits end_date cannot clear it", async () => {
    await applyOp("recurringTerm.put", "term-1", { amount: 700 }, {
      seriesId: "s1",
      startDate: "2026-01-01",
      endDate: "2026-06-01",
      amount: 649,
      accountId: "a1",
    });
    expect(sentBody("recurringTerm.put")).toEqual({
      seriesId: "s1",
      startDate: "2026-01-01",
      endDate: "2026-06-01",
      amount: 700,
      accountId: "a1",
    });
  });

  // The loan is a whole-row upsert of *terms*: loan.go:324 writes principal,
  // processing fee, rate, tenure and both dates from the body, and the
  // amortization periods it answers with are derived from them server-side. So
  // the merge is over the terms row, and a diff naming one of them must still
  // arrive with the rest.
  it("sends the loan's terms whole, not the one field the user changed", async () => {
    await applyOp("loanSchedule.put", "a1", { tenureMonths: 36 }, {
      principal: 100000,
      processingFee: 1000,
      annualRateBps: 950,
      tenureMonths: 24,
      startDate: "2026-01-01",
    });
    expect(sentBody("loanSchedule.put")).toEqual({
      principal: 100000,
      processingFee: 1000,
      annualRateBps: 950,
      tenureMonths: 36,
      startDate: "2026-01-01",
    });
  });

  // A clear the user made is a value, and the overlay must not read it as an
  // absence to be filled in from the server's row. `accountId: null` against a
  // base that holds one is payee.go:120's `$2::uuid IS NULL` branch — the payee
  // goes back to the global pool — and an overlay that dropped it would re-attach
  // the account the user detached.
  it("keeps a clear the diff made, rather than filling it in from the server's row", async () => {
    await applyOp("payee.put", "p1", { accountId: null }, {
      name: "Cafe",
      accountId: "a1",
    });
    expect(sentBody("payee.put")).toEqual({ name: "Cafe", accountId: null });
  });

  // Review Focus #5. Merging onto a read that lacks a field must not emit
  // undefined into a NOT NULL column. Neither side of the merge manufactures one
  // today — project() drops a nullish field and diffAgainstBase skips an
  // undefined value — so this pins the one thing that could: the overlay itself.
  // The loan's start_date has no default and no COALESCE (loan.go:332), so a body
  // that carried the key at all would be a request the server answers with a
  // schedule it cannot schedule.
  it("never emits undefined into a merged putWhole row", async () => {
    await applyOp("loanSchedule.put", "a1", { tenureMonths: 36 }, {
      principal: 100000,
      startDate: undefined,
      tenureMonths: 24,
    });
    // toStrictEqual, not toEqual: an explicit `startDate: undefined` is a key, and
    // toEqual does not see one.
    expect(sentBody("loanSchedule.put")).toStrictEqual({
      principal: 100000,
      tenureMonths: 36,
    });
  });

  // A row the server no longer has is a fact about the row, and the flush records
  // it as one (outbox.ts's recordGone) rather than reaching the wire. An overlay
  // of nothing is the alternative: the diff alone, which for this family is a wipe
  // of every column the entry does not name — refused here so it cannot be sent by
  // a caller that skipped the merge.
  it.each(PUT_WHOLE)("%s refuses to write onto a row the server no longer has", async (op) => {
    // A 422 and not a plain throw, and this is the second time this class of
    // refusal has been converted: the flush rethrows anything that is neither an
    // ApiError nor a NetworkError, so a plain Error here ends the whole sync
    // rather than this entry. The condition is a race — `send` re-reads `theirs`
    // after the plan read it, and a row deleted on another device in between
    // arrives as null here — so recording it and carrying on is strictly better
    // than aborting every later entry. A retry re-plans it and finds the row
    // genuinely gone, which is the correct terminal state.
    //
    // (At plan time the same absence is not a refusal at all: it is
    // outbox.ts's recordGone, which holds the entry for the user.)
    const refusal = applyOp(op, "row-1", { name: "New" }, null);
    await expect(refusal).rejects.toMatchObject({ status: 422 });
    await expect(refusal).rejects.toThrow(/gone/i);
  });

  it("reaches each whole-row family through its own endpoint, keyed by the row's own id", async () => {
    // The payload is the overlay's business; what belongs here is that each one is
    // addressed by the identifier it is given.
    const theirs: FieldPatch = { name: "Old", accountId: "a1" };
    await applyOp("payee.put", "p1", { name: "New" }, theirs);
    expect(apiMocks.updatePayee.mock.calls[0][0]).toBe("p1");
    await applyOp("rule.put", "r1", { pattern: "tea" }, theirs);
    expect(apiMocks.updateRule.mock.calls[0][0]).toBe("r1");
    await applyOp("recurring.put", "s1", { name: "Prime" }, theirs);
    expect(apiMocks.updateRecurringSeries.mock.calls[0][0]).toBe("s1");
  });

  it("addresses a recurring term by its series, which only theirs can name", async () => {
    // The term endpoint takes a series id and a term id, and a term carries its
    // own id only — so the series comes off the server's row the diff was merged
    // onto. A row that does not name one has nothing to address, and must not be
    // written through a guess.
    await expect(
      applyOp("recurringTerm.put", "term-1", { amount: 700 }, {
        seriesId: "s1",
        amount: 649,
      }),
    ).resolves.toBeUndefined();
    expect(apiMocks.updateRecurringTerm.mock.calls[0].slice(0, 2)).toEqual([
      "s1",
      "term-1",
    ]);

    await expect(
      applyOp("recurringTerm.put", "term-1", { amount: 700 }, { amount: 649 }),
    ).rejects.toThrow(/series/i);
    expect(apiMocks.updateRecurringTerm).toHaveBeenCalledTimes(1);
  });

  it("addresses a loan schedule by its account", async () => {
    await applyOp("loanSchedule.put", "a1", { tenureMonths: 36 }, { tenureMonths: 24 });
    expect(apiMocks.saveLoanSchedule.mock.calls[0][0]).toBe("a1");
  });

  it("re-creates a gone transaction through the idempotent create path", async () => {
    apiMocks.createTransaction.mockResolvedValue({ id: "t9", queued: false });
    const id = await reCreate(
      "transaction.patch",
      { accountId: "a1", date: "2026-01-15", description: "Coffee", amount: 250.5, type: "debit" },
      { notes: "milk" },
      "e-entry-1",
    );
    expect(id).toBe("t9");
    // The entry's own key, and queue: false — the flush is sending this entry, so
    // a request that never reached the server must not put a second copy of it in
    // the queue.
    expect(apiMocks.createTransaction).toHaveBeenCalledWith(
      {
        accountId: "a1",
        date: "2026-01-15",
        description: "Coffee",
        amount: 250.5,
        type: "debit",
        notes: "milk",
      },
      { idempotencyKey: "e-entry-1", queue: false },
    );
  });

  // The guarantee a re-create exists to keep: a response that was lost (a timeout,
  // a killed tab) must not cost the user a second money row. It can only hold if
  // the key comes from the entry, because the entry is the thing a retry repeats.
  it("sends the same key on every attempt at the same entry", async () => {
    apiMocks.createTransaction.mockResolvedValue({ id: "t9", queued: false });
    const snapshot = { accountId: "a1", date: "2026-01-15", description: "Coffee", amount: 250.5, type: "debit" };
    const diff = { notes: "milk" };

    await reCreate("transaction.patch", snapshot, diff, "e-entry-1");
    await reCreate("transaction.patch", snapshot, diff, "e-entry-1");
    // A different entry carries a different key, so the key is the entry's own
    // rather than a constant the registry invented.
    await reCreate("transaction.patch", snapshot, diff, "e-entry-2");

    // Asserted on the key, not on how many times the endpoint was called: a fresh
    // mint per attempt would be three calls and three keys, and only the key says
    // which of the two it did.
    const keys = apiMocks.createTransaction.mock.calls.map(
      (call) => (call[1] as { idempotencyKey: string }).idempotencyKey,
    );
    expect(keys).toEqual(["e-entry-1", "e-entry-1", "e-entry-2"]);
  });

  it("refuses to re-create a transaction the server accepted without an id", async () => {
    apiMocks.createTransaction.mockResolvedValue({ id: null, queued: false });
    await expect(
      reCreate("transaction.patch", { accountId: "a1" }, { notes: "x" }, "e-entry-1"),
    ).rejects.toThrow(/no id/i);
  });
});

// The whole feature, end to end, in the one place both halves are in hand: a
// queued edit goes into the outbox, the flush reads the server's row and merges
// against it, and the diff the merge decided is what applyOp is handed. The wire
// call is where the difference shows — this is the assertion that a payee's
// account survives an offline rename, rather than being nulled by a body that
// named only the name.
describe("a queued whole-row edit, flushed", () => {
  beforeEach(() => {
    for (const fn of Object.values(apiMocks)) fn.mockReset();
    localStorage.clear();
  });

  it("puts the server's row and the user's change on the wire together", async () => {
    enqueueEdit(
      "u1",
      "payee.put",
      "p1",
      { name: "Cafe", accountId: "a1" },
      { name: "Beans" },
      { name: "Cafe", accountId: "a1" },
    );

    // The two halves of the dispatch, wired the way the offline context wires
    // them: the reader the flush merges against, and the seam the resolved patch
    // is sent through. The server still holds "Cafe" — the edit never reached it —
    // so the merge has the user's change to make and the overlay has a row to make
    // it on.
    const server: Record<string, FieldPatch | null> = { p1: { name: "Cafe", accountId: "a1" } };
    const outcome = await flushOutbox(
      "u1",
      async (entry) => {
        if (entry.kind !== "edit") return;
        await applyOp(entry.op, entry.rowId, entry.patch, server[entry.rowId] ?? null);
      },
      { theirs: async (_op, rowId) => server[rowId] ?? null },
    );

    expect(outcome).toMatchObject({ sent: 1, remaining: 0, failed: 0, conflicted: 0, gone: 0 });
    // The user's change over the server's, and the account the payee was attached
    // to carried through beside it: payee.go:118 writes account_id on every call,
    // so a body naming only the name would have detached it and reported the save
    // as done.
    expect(apiMocks.updatePayee).toHaveBeenCalledWith("p1", {
      name: "Beans",
      accountId: "a1",
    });
  });

  // A conflict is the merge's, not the overlay's, and the two must not be
  // confused: a held entry writes nothing at all, so a body that appeared here
  // would be a second answer to a question the user is being asked.
  it("writes nothing for a whole-row edit held for the user", async () => {
    enqueueEdit(
      "u1",
      "payee.put",
      "p1",
      { name: "Cafe", accountId: "a1" },
      { name: "Beans" },
      { name: "Cafe", accountId: "a1" },
    );

    const outcome = await flushOutbox(
      "u1",
      async (entry) => {
        if (entry.kind !== "edit") return;
        await applyOp(entry.op, entry.rowId, entry.patch, { name: "Tea", accountId: "a1" });
      },
      { theirs: async () => ({ name: "Tea", accountId: "a1" }) },
    );

    expect(outcome).toMatchObject({ sent: 0, conflicted: 1, remaining: 1 });
    expect(apiMocks.updatePayee).not.toHaveBeenCalled();
    const left = getOutboxSnapshot("u1")[0];
    expect(left.conflict?.units[0]).toMatchObject({ rowId: "p1", field: "name" });
  });

  // The user answered "theirs" on a field, and the answer has to reach the wire as
  // the server's own value rather than the user's. This path only became reachable
  // when the family-wide refusal was deleted, and it is a one-line rule in two
  // places — planEdit drops the field from the decided patch, and the overlay
  // writes what is underneath it back onto the row — so the outcome is pinned
  // rather than argued for in a comment. A version that kept the field would
  // re-assert the very value the user declined to write; a version that dropped
  // the whole row would have undone the fields they did change, which is why the
  // patch has two fields here.
  //
  // What this pins is the body, not the spelling. For this family, dropping the
  // field and writing the server's own value into it produce the same merged row,
  // because the overlay fills it back in either way; what must never reach the
  // wire is the user's value, and that is what a mutation of the rule breaks.
  it("writes a field the user kept as the server's value, beside the ones they changed", async () => {
    const entry = enqueueEdit(
      "u1",
      "payee.put",
      "p1",
      { name: "Cafe", accountId: "a1" },
      { name: "Beans", accountId: "a2" },
      { name: "Cafe", accountId: "a1" },
    );
    // Somebody else renamed it while the edit was queued, so the name is held.
    const server: Record<string, FieldPatch | null> = { p1: { name: "Tea", accountId: "a1" } };
    const send = async (queued: QueuedWrite): Promise<void> => {
      if (queued.kind !== "edit") return;
      await applyOp(queued.op, queued.rowId, queued.patch, server[queued.rowId] ?? null);
    };
    const theirs = async (_op: WriteOp, rowId: string) => server[rowId] ?? null;

    const held = await flushOutbox("u1", send, { theirs });
    expect(held).toMatchObject({ sent: 0, conflicted: 1 });
    expect(apiMocks.updatePayee).not.toHaveBeenCalled();

    resolveConflict("u1", entry.key, { name: "theirs" });

    const answered = await flushOutbox("u1", send, { theirs });
    expect(answered).toMatchObject({ sent: 1, conflicted: 0, remaining: 0 });
    // "Tea" is the server's, not the "Beans" the user typed and declined; "a2" is
    // the account they did change, and it went out beside it.
    expect(apiMocks.updatePayee).toHaveBeenCalledWith("p1", {
      name: "Tea",
      accountId: "a2",
    });
    expect(getOutboxSnapshot("u1")).toHaveLength(0);
  });
});

describe("the row-naming writes", () => {
  beforeEach(() => {
    for (const fn of Object.values(apiMocks)) fn.mockReset();
  });

  it("categorizes, names a payee and attaches a cycle over the rows it is given", async () => {
    await applyMany("transaction.categorize", ["t1", "t2"], "c1");
    expect(apiMocks.bulkCategorize).toHaveBeenCalledWith({
      transactionIds: ["t1", "t2"],
      categoryId: "c1",
    });
    await applyMany("transaction.payee", ["t1"], "p1");
    expect(apiMocks.bulkUpdatePayee).toHaveBeenCalledWith({
      transactionIds: ["t1"],
      payeeId: "p1",
    });
    await applyMany("transaction.billingCycle", ["t1"], "bc1");
    expect(apiMocks.bulkUpdateBillingCycle).toHaveBeenCalledWith({
      transactionIds: ["t1"],
      billingCycleId: "bc1",
    });
  });

  // BulkCategorizeRequest.CategoryID is a required string and the handler reads
  // only the literal "uncategorized" as a clear (everything else goes to
  // uuid.Parse), so the empty string asId used to send was a 400 — and the
  // endpoint does have a real clear, under a name of its own.
  it("categorizes to uncategorized when the user cleared the category", async () => {
    await applyMany("transaction.categorize", ["t1", "t2"], null);
    expect(apiMocks.bulkCategorize).toHaveBeenCalledWith({
      transactionIds: ["t1", "t2"],
      categoryId: "uncategorized",
    });
  });

  // BulkUpdatePayeeRequest.PayeeID and BulkBillingCycleRequest.BillingCycleID are
  // required uuids behind an EXISTS guard, so the zero uuid an empty string
  // unmarshals to matches no row: a 200 carrying updated: 0. There is no way to
  // clear either through these endpoints, and a clear reported as applied is a
  // user's edit lost to a success the server never performed.
  it.each<[WriteOp, keyof typeof apiMocks]>([
    ["transaction.payee", "bulkUpdatePayee"],
    ["transaction.billingCycle", "bulkUpdateBillingCycle"],
  ])("%s refuses a clear rather than sending an id the endpoint cannot read", async (op, method) => {
    await expect(applyMany(op, ["t1"], null)).rejects.toThrow(
      new RegExp(`${op}.*cannot detach`, "s"),
    );
    expect(apiMocks[method]).not.toHaveBeenCalled();
  });

  it("passes a real id through unchanged, so a refusal above cannot be a blanket one", async () => {
    await applyMany("transaction.payee", ["t1"], "p1");
    await applyMany("transaction.billingCycle", ["t1"], "bc1");
    expect(apiMocks.bulkUpdatePayee).toHaveBeenCalledWith({
      transactionIds: ["t1"],
      payeeId: "p1",
    });
    expect(apiMocks.bulkUpdateBillingCycle).toHaveBeenCalledWith({
      transactionIds: ["t1"],
      billingCycleId: "bc1",
    });
  });

  // A queued write that cannot be sent is a definite answer about the entry, not
  // a broken dispatch, and the difference is what the flush does with it: an
  // ApiError with a 4xx is recorded on the entry and skipped, while anything that
  // is neither an ApiError nor a NetworkError is rethrown and stops the whole sync
  // (outbox.ts). So a clear the app could queue — a bulk "remove payee" the
  // endpoints cannot express — would otherwise be the one write that takes every
  // other queued write down with it.
  it("reports an unsendable clear as a rejection the flush can carry past", async () => {
    for (const op of ["transaction.payee", "transaction.billingCycle", "transaction.loanDisbursement"] as const) {
      await expect(applyMany(op, ["t1"], null)).rejects.toMatchObject({ status: 422 });
    }
    for (const method of ["bulkUpdatePayee", "bulkUpdateBillingCycle", "linkLoanDisbursement"] as const) {
      expect(apiMocks[method]).not.toHaveBeenCalled();
    }
  });

  it("adds the tags the entry names", async () => {
    await applyMany("transaction.tags", ["t1", "t2"], ["milk"]);
    expect(apiMocks.bulkUpdateTags).toHaveBeenCalledWith({
      transactionIds: ["t1", "t2"],
      add: ["milk"],
      remove: [],
    });
  });

  it("removes the tags the entry names with a leading dash", async () => {
    // The endpoint takes two lists and applyMany carries one value, so the value
    // is the only place the direction can live. A registry that guessed would
    // send a removal back as an addition and the tag would come back on its own.
    await applyMany("transaction.tags", ["t1"], ["-milk"]);
    expect(apiMocks.bulkUpdateTags).toHaveBeenCalledWith({
      transactionIds: ["t1"],
      add: [],
      remove: ["milk"],
    });
  });

  it("links a loan, and detaches on a null value", async () => {
    await applyMany("transaction.loan", ["t1", "t2"], "a1");
    expect(apiMocks.bulkLoan).toHaveBeenCalledWith({
      transactionIds: ["t1", "t2"],
      loanAccountId: "a1",
    });
    await applyMany("transaction.loan", ["t1"], null);
    expect(apiMocks.bulkLoan).toHaveBeenLastCalledWith({
      transactionIds: ["t1"],
      loanAccountId: null,
    });
  });

  it("attaches a series, and detaches on a null value", async () => {
    await applyMany("transaction.recurring", ["t1", "t2"], "s1");
    expect(apiMocks.attachRecurring).toHaveBeenCalledWith({
      seriesId: "s1",
      transactionIds: ["t1", "t2"],
    });
    await applyMany("transaction.recurring", ["t1"], null);
    expect(apiMocks.detachRecurring).toHaveBeenCalledWith({ transactionIds: ["t1"] });
  });

  it("links a disbursement credit to the loan that released it", async () => {
    // The write is one credit, so the entry names one row: the transaction. The
    // loan account rides as the value, because a disbursement is not a column on
    // the transaction and there is no other channel for it.
    await applyMany("transaction.loanDisbursement", ["t1"], "a1");
    expect(apiMocks.linkLoanDisbursement).toHaveBeenCalledWith("a1", {
      transactionId: "t1",
    });
  });

  it.each(MULTI_ROW)("%s reads the transaction row it writes", async (op) => {
    apiMocks.getTransactions.mockResolvedValue({
      data: [TRANSACTION],
      total: 1,
      page: 1,
      pages: 1,
    });
    await readTheirs(op, "t1");
    expect(apiMocks.getTransactions).toHaveBeenCalledWith(
      { q: "id:t1", limit: 1 },
      { live: true },
    );
  });

  it.each<[WriteOp, string, FieldValue, keyof typeof apiMocks, unknown]>([
    ["transaction.categorize", "categoryId", "c1", "bulkCategorize", { transactionIds: ["t1"], categoryId: "c1" }],
    ["transaction.payee", "payeeId", "p1", "bulkUpdatePayee", { transactionIds: ["t1"], payeeId: "p1" }],
    ["transaction.billingCycle", "billingCycleId", "bc1", "bulkUpdateBillingCycle", { transactionIds: ["t1"], billingCycleId: "bc1" }],
    ["transaction.loan", "loanAccountId", "a1", "bulkLoan", { transactionIds: ["t1"], loanAccountId: "a1" }],
    ["transaction.recurring", "recurringSeriesId", "s1", "attachRecurring", { seriesId: "s1", transactionIds: ["t1"] }],
  ])("%s sends one row the same way, with the field it writes", async (op, field, value, method, body) => {
    // The single-row apply is the batch of one, so the two cannot disagree about
    // which field of the diff the write is about. transaction.tags is not here and
    // the table below says what it does instead.
    await applyOp(op, "t1", { [field]: value }, null);
    expect(apiMocks[method]).toHaveBeenCalledWith(body);
  });

  // The one op of the seven whose single-row form does not exist. Its value is a
  // delta of add/remove names, not the row's tag list, so a field-level patch can
  // only be read as an addition — and a removal routed here would be added
  // instead, which is a user's edit becoming its opposite. The row's complete tag
  // list belongs to the projection, where the merge needs it; the two are
  // different questions about the same field, not the same value.
  it("refuses the single-row form of a tag change, which only the bulk endpoint can make", async () => {
    await expect(
      applyOp("transaction.tags", "t1", { tags: ["-milk"] }, null),
    ).rejects.toThrow(/transaction\.tags[\s\S]*bulk/i);
    // Nothing went out in the wrong direction either.
    expect(apiMocks.bulkUpdateTags).not.toHaveBeenCalled();
  });

  it("links a single disbursement credit the same way", async () => {
    await applyOp("transaction.loanDisbursement", "t1", { loanAccountId: "a1" }, null);
    expect(apiMocks.linkLoanDisbursement).toHaveBeenCalledWith("a1", {
      transactionId: "t1",
    });
  });
});

// Every op a queued entry can carry, from the registry itself rather than from a
// list kept beside it. A list copied out of the registry would only catch a new
// op on the day someone compared the two by hand; this sweep runs whatever OPS
// declares, so an op that refuses with a plain throw fails on the day it is added.
const ALL_OPS = Object.keys(OPS) as WriteOp[];

// A row that exists and names whatever its endpoint addresses, so a refusal sweep
// reaches the wire instead of tripping an invariant throw first: a term's series
// is the only identifier its write cannot do without, and it comes off the row the
// diff was merged onto (see recurringTerm.put).
const THEIRS: FieldPatch = { seriesId: "s1" };

describe("the shape of a refusal", () => {
  beforeEach(() => {
    for (const fn of Object.values(apiMocks)) fn.mockReset();
  });

  // What a queued write can ask for that an endpoint may not be able to express is
  // a clear, so that is what every op is asked here. Either answer is fine — the
  // write goes out, or it is refused — but the shape of the refusal is not the
  // registry's to choose: flushOutbox rethrows anything that is neither an ApiError
  // nor a NetworkError, so a refusal thrown as a plain Error stops every sync and
  // records nothing on the entry. One clear the app asked for once, and nothing
  // behind it in the queue can ever be sent.
  it.each(ALL_OPS)("%s answers a clear with an ApiError, or not at all", async (op) => {
    const asked: [string, () => unknown][] = [
      ["applyMany with a null", () => OPS[op].applyMany?.(["row-1"], null)],
      ["applyMany with an empty string", () => OPS[op].applyMany?.(["row-1"], "")],
      ["applyOp with a null in the diff", () => applyOp(op, "row-1", { notes: null }, THEIRS)],
      [
        "applyOp with an empty string in the diff",
        () => applyOp(op, "row-1", { notes: "" }, THEIRS),
      ],
      // A row the server no longer has is the fifth thing a queued write can ask
      // for, and it reaches applyOp as a null `theirs` on every flush: `send`
      // reads the row again after the plan read it, so a row deleted elsewhere
      // in between arrives here. For a whole-row family that is the refusal
      // below, and it is the one most likely to be written as a plain throw,
      // because at plan time the same absence is not a refusal at all — it is
      // outbox.ts's recordGone.
      ["applyOp onto a row that is gone", () => applyOp(op, "row-1", { notes: "milk" }, null)],
    ];

    for (const [what, ask] of asked) {
      try {
        await ask();
      } catch (err) {
        if (!(err instanceof ApiError)) {
          throw new Error(
            `${op} refused ${what} with a ${(err as Error).constructor?.name ?? "throw"} rather than an ApiError: the flush rethrows anything else, so this one clear would stop every sync and record nothing on the entry (${(err as Error).message})`,
          );
        }
      }
    }
  });

  // The same question asked of a refusal that is not a clear: this op has no
  // single-row form at all, and saying so is an answer about the write rather than
  // a broken invariant (see transaction.tags in registry.ts).
  it("refuses a single-row tag change as an ApiError too", async () => {
    await expect(
      applyOp("transaction.tags", "row-1", { tags: ["-milk"] }, THEIRS),
    ).rejects.toMatchObject({ status: 422 });
    expect(apiMocks.bulkUpdateTags).not.toHaveBeenCalled();
  });
});
