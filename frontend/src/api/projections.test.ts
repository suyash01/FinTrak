import { describe, it, expect } from "vitest";
import type { FieldPatch } from "./merge";
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
