import { describe, it, expect } from "vitest";
import { diffAgainstBase, type FieldPatch, type FieldValue } from "./merge";
import {
  findRow,
  LOAN_TERMS_FIELDS,
  PAYEE_FIELDS,
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
  RULE_FIELDS,
  SERIES_FIELDS,
  TERM_FIELDS,
} from "./projections";

// A projector, and a field of that family the server reports as null.
type Family = [name: string, project: (row: object) => FieldPatch, nullish: string];

// The rule is the same for every family, and it is the one the whole merge rests
// on: a field the server holds nothing in is a field the row does not carry, so
// it is left off rather than written as null. A projector that forgot it would
// put a difference into a base that the user never made — and the merge would
// then hold that field as a conflict between the user and nobody.
const families: Family[] = [
  ["projectTransaction", projectTransaction, "categoryId"],
  ["projectAccount", projectAccount, "bank"],
  ["projectAccountType", projectAccountType, "positiveTxnType"],
  ["projectGroup", projectGroup, "icon"],
  ["projectCategory", projectCategory, "groupId"],
  ["projectPayee", projectPayee, "accountId"],
  ["projectRule", projectRule, "payeeId"],
  ["projectRecurringSeries", projectRecurringSeries, "endDate"],
  ["projectRecurringTerm", projectRecurringTerm, "seriesId"],
  ["projectLoanTerms", projectLoanTerms, "startDate"],
  ["projectSettings", projectSettings, "paperlessTag"],
];

describe("a row's mergeable projection", () => {
  it.each(families)(
    "%s leaves a nullish field off the object",
    (_name, project, nullish) => {
      const row = project({ [nullish]: null });
      expect(nullish in row).toBe(false);
      expect(row).toEqual({});
    },
  );

  it("leaves a field the row does not carry at all off too", () => {
    // absent and null are the same statement here: the server's row reports
    // neither "empty" and "never set" differently, and the merge reads a missing
    // key as untouched (merge.ts).
    expect("loanAccountId" in projectTransaction({ accountId: "a1" })).toBe(false);
  });

  it("carries an empty list, which is a value and not an absence", () => {
    // Emptying every tag is a change the user made, and a projection that
    // dropped [] would lose it.
    expect(projectTransaction({ tags: [] })).toEqual({ tags: [] });
  });

  it("carries a field set to an empty string", () => {
    expect(projectTransaction({ notes: "" })).toEqual({ notes: "" });
  });

  it("leaves off what no queued write could change", () => {
    // Ids, joined display names and anything the server derives are not
    // mergeable: putting one in a base would invent a difference the user never
    // made, which the merge would then hold as somebody else's change.
    expect(
      projectAccount({
        id: "a1",
        name: "Wallet",
        balance: 100,
        accountTypeName: "Savings",
        createdAt: "2026-01-01T00:00:00Z",
      }),
    ).toEqual({ name: "Wallet" });
  });

  it("keeps the loan and subscription attachments a transaction carries", () => {
    // The two multi-row writes that attach a transaction to a loan or a series
    // merge on exactly these fields, and a projection without them reads as "the
    // server has no attachment" — so a concurrent attachment would be
    // overwritten rather than held.
    expect(
      projectTransaction({ loanAccountId: "a1", recurringSeriesId: "s1" }),
    ).toEqual({ loanAccountId: "a1", recurringSeriesId: "s1" });
  });

  it("never names a token the settings response does not carry", () => {
    // The response says whether a token is set and never sends it, so a
    // projection naming a token field would put a value on the wire this client
    // has never read from anywhere.
    expect(
      projectSettings({
        paperlessUrl: "https://paperless.example",
        paperlessTag: "fintrak",
        pageSize: 50,
        hasPaperlessToken: true,
      }),
    ).toEqual({
      paperlessUrl: "https://paperless.example",
      paperlessTag: "fintrak",
      pageSize: 50,
    });
  });

  it("names the series a term belongs to, which is the only source its write has", () => {
    // The term endpoints are addressed by series, and no read lists a user's
    // terms across series — so this field is what lets the term be written at
    // all.
    expect(projectRecurringTerm({ seriesId: "s1", amount: 9.99 })).toEqual({
      seriesId: "s1",
      amount: 9.99,
    });
  });

  it("projects the loan's terms rather than its derived schedule", () => {
    // The amortization periods are derived server-side from the terms, so a
    // queued edit is a change to the terms and nothing else.
    expect(
      projectLoanTerms({
        principal: 100000,
        processingFee: 500,
        disbursalDate: "2026-01-01",
        annualRateBps: 1200,
        tenureMonths: 24,
        startDate: "2026-02-01",
        periodCount: 24,
        monthlyPayment: 4339,
      }),
    ).toEqual({
      principal: 100000,
      processingFee: 500,
      disbursalDate: "2026-01-01",
      annualRateBps: 1200,
      tenureMonths: 24,
      startDate: "2026-02-01",
    });
  });

  it.each([
    [
      "projectAccountType",
      projectAccountType,
      { id: "at1", name: "Savings", positiveTxnType: "credit" },
      { name: "Savings", positiveTxnType: "credit" },
    ],
    ["projectGroup", projectGroup, { id: "g1", name: "Food", icon: "utensils", color: "#f00" }, { name: "Food", icon: "utensils", color: "#f00" }],
    ["projectCategory", projectCategory, { id: "c1", name: "Coffee", icon: "cup", color: "#0f0", groupId: "g1" }, { name: "Coffee", icon: "cup", color: "#0f0", groupId: "g1" }],
    ["projectPayee", projectPayee, { id: "p1", name: "Cafe", accountId: "a1" }, { name: "Cafe", accountId: "a1" }],
  ])("%s carries the mergeable fields and nothing else", (_name, project, row, expected) => {
    // The id is the address of the row, not a field of it: a base carrying it
    // would compare a row against itself on the one value that can never differ.
    expect(project(row)).toEqual(expected);
    expect("id" in project(row)).toBe(false);
  });

  it("projects a recurring series' own fields", () => {
    expect(
      projectRecurringSeries({
        id: "s1",
        name: "Netflix",
        amount: 649,
        active: true,
        nextOccurrence: "2026-03-01",
      }),
    ).toEqual({ name: "Netflix", amount: 649, active: true });
  });

  it("projects a rule's filters, which a rule write can set", () => {
    expect(
      projectRule({
        pattern: "coffee",
        categoryId: null,
        minAmount: 100,
        matchCount: 42,
      }),
    ).toEqual({ pattern: "coffee", minAmount: 100 });
  });
});

// The correspondence a whole-row write stands or falls on, and the one link in
// the chain the other guards do not cover.
//
// A `putWhole` op's request is the diff overlaid on this projection
// (registry.ts's mergedRow), so a column the endpoint writes and the projection
// omits is absent from the body — and the handler writes its zero value. Adding a
// column to one of these endpoints would therefore blank it, silently, on every
// offline edit to that row. Nothing else in the offline layer can see that: the
// overlay tests pin the merge, and the registry's completeness tables pin the op
// list, but neither knows what the handler binds.
//
// The authority is the Go handler's own `SET` / `INSERT` columns, cited per family
// below. These arrays are that citation made checkable, and they are the pin a
// handler change has to meet. Nothing compares a TypeScript interface to a runtime
// value — an interface is erased — so the only durable form is the key set written
// down here; it is updated by reading the handler, not by running the test, which
// is why each one names its line.
//
// The assertion is on the field *list*, not on a fixture's output. A list checked
// through one row is checked only where that row reaches, so a field added to a
// list the fixture does not carry would pass — which is the addition that breaks
// the family, since an added field is one the merge would read and no `SET` writes.
type WholeRowFamily = {
  name: string;
  handler: string;
  fields: readonly string[];
  project: (row: object) => FieldPatch;
  expected: string[];
};

const WHOLE_ROW_FAMILIES: WholeRowFamily[] = [
  {
    name: "projectPayee",
    handler: "payee.go:118",
    fields: PAYEE_FIELDS,
    project: projectPayee,
    // `UPDATE payees SET name = $1, account_id = $2, updated_at = NOW()`. The
    // timestamp is the server's own and on no request.
    expected: ["accountId", "name"],
  },
  {
    name: "projectRule",
    handler: "rule.go:287",
    fields: RULE_FIELDS,
    project: projectRule,
    // Seventeen columns, $1 through $17, and every one of them a column a rule
    // form can set.
    expected: [
      "accountId",
      "addTags",
      "categoryId",
      "dateFrom",
      "dateTo",
      "filterCategoryId",
      "filterPayeeId",
      "isLinked",
      "isRecurring",
      "matchType",
      "maxAmount",
      "minAmount",
      "notes",
      "pattern",
      "payeeId",
      "priority",
      "txnType",
    ],
  },
  {
    name: "projectRecurringTerm",
    handler: "recurring.go:1183",
    fields: TERM_FIELDS,
    project: projectRecurringTerm,
    // `SET start_date = $1, end_date = $2, amount = $3, account_id = $4` — four
    // columns. The fifth key is seriesId, and it is the row's *address* rather
    // than a column: the term endpoints are reached through the series and it is
    // the only read that can find it again, so it has to be in the projection even
    // though no `SET` writes it. The one entry below that is not a body column.
    expected: ["accountId", "amount", "endDate", "seriesId", "startDate"],
  },
  {
    name: "projectLoanTerms",
    handler: "loan.go:324",
    fields: LOAN_TERMS_FIELDS,
    project: projectLoanTerms,
    // The six `INSERT` columns, which are the same six as its `DO UPDATE SET`
    // list: principal, processing fee, rate, tenure and both dates. The periods
    // the same response carries are derived from them, which is why they are not
    // here.
    expected: [
      "annualRateBps",
      "disbursalDate",
      "principal",
      "processingFee",
      "startDate",
      "tenureMonths",
    ],
  },
];

describe("a whole-row family's projection is the body its endpoint writes", () => {
  it.each(WHOLE_ROW_FAMILIES)(
    "$name is exactly the columns $handler binds",
    ({ fields, project, expected }) => {
      // Exact, in both directions. A column the projection drops is one the
      // merged request leaves out and the handler zeroes; a column it adds is one
      // the merge can read and no `SET` writes, so a concurrent change to it would
      // be held as a conflict the user never caused and could never be sent.
      expect([...fields].sort()).toEqual(expected);
      // And the projector reads the list it is paired with, rather than a list of
      // its own that the assertion above cannot see. The row is built from the
      // pinned set, so this cannot pass by accident on a fixture that happens to
      // carry the right keys.
      const row = Object.fromEntries(expected.map((field) => [field, "x"]));
      expect(Object.keys(project(row)).sort()).toEqual(expected);
    },
  );

  // The odd one of the five, and it needs containment as well as equality:
  // recurring.go:922 writes nine of the thirteen projected fields, and the four it
  // does not are deliberate. deriveRecurringSeries (recurring.go:318) fills a
  // series' start date, end date, account and amount in from its terms, so the
  // merge has to be able to see a term's new amount as somebody else's change
  // even though the series endpoint never writes it. Two properties, two
  // assertions: every column the handler binds must be present, and the projection
  // is allowed to carry four more.
  it("projectRecurringSeries carries all nine columns recurring.go:922 writes, and the four it derives", () => {
    // The nine the `UPDATE` names, one for one.
    expect([...SERIES_FIELDS]).toEqual(
      expect.arrayContaining([
        "name",
        "description",
        "type",
        "frequency",
        "interval",
        "categoryId",
        "payeeId",
        "active",
        "notes",
      ]),
    );
    // And all thirteen, pinned, so a derived field cannot leave the projection
    // quietly either: a concurrent change to one of them would stop being seen.
    expect([...SERIES_FIELDS].sort()).toEqual([
      "accountId",
      "active",
      "amount",
      "categoryId",
      "description",
      "endDate",
      "frequency",
      "interval",
      "name",
      "notes",
      "payeeId",
      "startDate",
      "type",
    ]);
  });
});

// A date the API returns and a date every write edge takes are two spellings of
// one calendar day, and they are not equal as strings.
//
// The read side is a `time.Time`, so encoding/json writes RFC3339
// ("2026-07-20T00:00:00Z" — models.Transaction.Date, models.LoanSchedule's
// StartDate and DisbursalDate, models.RecurringSeriesTerm's StartDate and
// EndDate). The write side is a `string` every one of those families parses as
// "2006-01-02": validation.CheckTransactionDate for a transaction
// (transaction.go:580, :805) and parseRecurringDate, which calls it, for a
// series, a term and a loan (recurring.go:89, loan.go:293).
//
// So a form holds "2026-07-20" where the row says "2026-07-20T00:00:00Z", and a
// projection that passes the read side through unchanged makes every save look
// like a date change: the diff would carry the row's own day back onto the wire
// and revert whatever another writer moved the date to. The projection is where
// the two are reconciled, because it is the one place that describes "a row's
// mergeable representation" — and it is applied to the base and to `theirs`
// alike, so the two sides of every comparison are like-for-like rather than
// merely shifted.
//
// The authority for the format is CheckTransactionDate's grammar, not the Go
// type: the pattern to look for is a `time.Time` on the model and a `string` on
// the request. A date that is a string on both sides (a rule's dateFrom/dateTo,
// which rule.go:74 formats as "YYYY-MM-DD" on read) is already one spelling and
// is left alone.
const CALENDAR_DAY_FAMILIES: Array<[
  name: string,
  project: (row: object) => FieldPatch,
  row: Record<string, FieldValue>,
  expected: FieldPatch,
]> = [
  [
    "projectTransaction",
    projectTransaction,
    { date: "2026-07-20T00:00:00Z", description: "Swiggy" },
    { date: "2026-07-20", description: "Swiggy" },
  ],
  [
    "projectRecurringSeries",
    projectRecurringSeries,
    { name: "Netflix", startDate: "2026-01-01T00:00:00Z", endDate: "2026-06-01T00:00:00Z" },
    { name: "Netflix", startDate: "2026-01-01", endDate: "2026-06-01" },
  ],
  [
    "projectRecurringTerm",
    projectRecurringTerm,
    { seriesId: "s1", startDate: "2026-01-01T00:00:00Z", endDate: "2026-06-01T00:00:00Z" },
    { seriesId: "s1", startDate: "2026-01-01", endDate: "2026-06-01" },
  ],
  [
    "projectLoanTerms",
    projectLoanTerms,
    { principal: 100000, startDate: "2026-02-01T00:00:00Z", disbursalDate: "2026-01-01T00:00:00Z" },
    { principal: 100000, startDate: "2026-02-01", disbursalDate: "2026-01-01" },
  ],
];

describe("a projection reconciles a date the API timestamps with the day a write edge takes", () => {
  it.each(CALENDAR_DAY_FAMILIES)(
    "%s reads a date as the calendar day, not the timestamp",
    (_name, project, row, expected) => {
      expect(project(row)).toEqual(expected);
    },
  );

  it.each(CALENDAR_DAY_FAMILIES)(
    "%s leaves a value that is already a day alone",
    (_name, project, row, expected) => {
      // The write edge's own spelling has to survive the projection unchanged:
      // normalising is not permission to reshape a value, and a projection that
      // mangled a day would break the grammar the handler validates against.
      const asWritten = Object.fromEntries(
        Object.entries(row).map(([field, value]) =>
          typeof value === "string" && value.includes("T")
            ? [field, value.slice(0, 10)]
            : [field, value],
        ),
      );
      expect(project(asWritten)).toEqual(expected);
    },
  );

  it("reads a rule's dates as they are, because the API already sends them as days", () => {
    // The other half of the rule, and the one that keeps this from being a
    // blanket slice: rule.go's formatDatePtr writes "YYYY-MM-DD" on read, so a
    // rule's dateFrom/dateTo are the same spelling on both sides and must come
    // through as themselves.
    expect(
      projectRule({ pattern: "coffee", dateFrom: "2026-01-01", dateTo: "2026-06-01" }),
    ).toEqual({ pattern: "coffee", dateFrom: "2026-01-01", dateTo: "2026-06-01" });
  });

  it("leaves a field that is not a date untouched, timestamp or not", () => {
    // `createdAt` and a joined name are not on any projection, and the guard
    // against a field that is only *named* like a date is that the family, not
    // the value's shape, decides: notes is a field the transaction projection
    // carries and a timestamp in it is a note the user wrote.
    expect(projectTransaction({ notes: "2026-07-20T00:00:00Z was the due date" })).toEqual({
      notes: "2026-07-20T00:00:00Z was the due date",
    });
  });

  it("leaves a non-string value alone, because only a timestamp is a date here", () => {
    // A rule's bounds are numbers and a type is a word; the normalisation is
    // keyed on the field, so a value that cannot be a date is passed through
    // rather than coerced.
    expect(projectRule({ pattern: "coffee", priority: 10 })).toEqual({
      pattern: "coffee",
      priority: 10,
    });
  });

  it("makes an untouched transaction form diff to nothing against its own row", () => {
    // The consequence, and the reason this is a correctness rule rather than a
    // tidiness one. This is the payload the transaction editor builds: a whole
    // row read off a form whose date field is a plain day (formFromTransaction
    // splits the row's timestamp on "T"). With the two spellings reconciled, a
    // user who opens the sheet, changes nothing and saves has an empty diff —
    // which is what lets updateTransaction answer `{ queued: false }` without a
    // request. Left unreconciled, `date` is in every diff and that answer is
    // unreachable, so the save the user asked for is a PATCH carrying the day
    // the row already had.
    const row = {
      id: "txn-1",
      accountId: "acct-1",
      date: "2026-07-20T00:00:00Z",
      description: "Swiggy",
      amount: 450.5,
      type: "debit",
      categoryId: null,
      tags: [],
      notes: "",
      payeeId: null,
    };
    const form = {
      date: "2026-07-20",
      description: "Swiggy",
      amount: 450.5,
      type: "debit",
      accountId: "acct-1",
      categoryId: null,
      tags: [],
      notes: "",
      payeeId: null,
    };
    expect(diffAgainstBase(projectTransaction(row), form)).toEqual({});
    // And a change to one field is that field alone — the whole point of the
    // diff, and the reason the date is worth reconciling rather than tolerating.
    expect(
      diffAgainstBase(projectTransaction(row), { ...form, notes: "milk" }),
    ).toEqual({ notes: "milk" });
  });
});

describe("findRow", () => {
  const payees = [
    { id: "p1", name: "Cafe" },
    { id: "p2", name: "Market" },
  ];

  it("projects the row the collection carries under that id", async () => {
    expect(await findRow(async () => payees, "p2", projectPayee)).toEqual({
      name: "Market",
    });
  });

  it("answers null for a row the collection does not carry", async () => {
    // null is the row, not a field: the server does not have it, which is what
    // the flush records as gone rather than merging against nothing.
    expect(await findRow(async () => payees, "p3", projectPayee)).toBeNull();
    expect(await findRow(async () => [], "p1", projectPayee)).toBeNull();
  });

  it("projects with the projector it is given, not a list of its own", async () => {
    // The read is injected, so this module never calls the server; the wire call
    // belongs to the op that owns it, and the projection is the one thing the
    // two must agree about.
    const accounts = [
      { id: "a1", name: "Wallet", bank: null },
      { id: "a2", name: "Savings", bank: "HDFC" },
    ];
    expect(await findRow(async () => accounts, "a1", projectAccount)).toEqual({
      name: "Wallet",
    });
  });
});
