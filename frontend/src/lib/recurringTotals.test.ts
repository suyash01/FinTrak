import { describe, it, expect } from "vitest";
import {
  foldRecurringMonthly,
  recurringNetClass,
  recurringTotalText,
} from "./recurringTotals";
import type { Account, CurrencyAmounts, RecurringSeries } from "@/types";

function account(overrides: Partial<Account>): Account {
  return {
    id: "a1",
    name: "Checking",
    accountTypeId: "bank",
    bank: "",
    currency: "INR",
    color: "#000000",
    isDefault: true,
    closed: false,
    balance: 0,
    ...overrides,
  };
}

function series(overrides: Partial<RecurringSeries>): RecurringSeries {
  return {
    id: "s1",
    accountId: "a1",
    name: "Series",
    description: "",
    amount: 100,
    type: "debit",
    frequency: "monthly",
    interval: 1,
    startDate: "2026-01-01",
    endDate: null,
    active: true,
    notes: "",
    monthlyAmount: 100,
    attachedCount: 0,
    ...overrides,
  } as RecurringSeries;
}

describe("foldRecurringMonthly", () => {
  it("folds each currency separately and never totals two of them", () => {
    const got = foldRecurringMonthly(
      [
        series({ id: "s1", accountId: "a1", monthlyAmount: 50000 }),
        series({ id: "s2", accountId: "a2", monthlyAmount: 10 }),
        series({
          id: "s3",
          accountId: "a2",
          type: "credit",
          monthlyAmount: 25,
        }),
      ],
      [
        account({ id: "a1", currency: "INR" }),
        account({ id: "a2", name: "Travel", currency: "USD" }),
      ],
    );

    expect(got.expense).toEqual({ INR: 50000, USD: 10 });
    expect(got.income).toEqual({ USD: 25 });
    // The difference is taken inside each currency, so the rupee account's
    // spending stands as a negative of its own rather than being offset by
    // dollars the user never earned in rupees.
    expect(got.net).toEqual({ INR: -50000, USD: 15 });
    expect(got.unplaced).toEqual([]);
  });

  it("ignores an inactive series and adds within one currency", () => {
    const got = foldRecurringMonthly(
      [
        series({ id: "s1", monthlyAmount: 100 }),
        series({ id: "s2", monthlyAmount: 250 }),
        series({ id: "s3", monthlyAmount: 9999, active: false }),
      ],
      [account({ id: "a1" })],
    );
    expect(got.expense).toEqual({ INR: 350 });
  });

  // A zero-cost series is still an active series the user created, so its
  // currency has been touched. Dropping the key — which is what the server's own
  // Add does for a zero contribution — would render the figure as "no active
  // series", a false claim about the forecast rather than a tidier map.
  it("keeps a zero-amount series in its currency", () => {
    const got = foldRecurringMonthly(
      [series({ id: "s1", monthlyAmount: 0 })],
      [account({ id: "a1" })],
    );
    expect(got.expense).toEqual({ INR: 0 });
    expect(got.net).toEqual({ INR: 0 });
    // A real zero, with its code beside it, rather than the empty-map sentence.
    expect(recurringTotalText(got.expense, got.unplaced)).toBe("INR ₹0.00");
    expect(recurringTotalText({}, got.unplaced)).not.toBe(
      recurringTotalText(got.expense, got.unplaced),
    );
  });

  // accounts.currency is nullable with no NOT NULL, so a restored bundle can
  // carry "" — and a deleted account's series names an id no list holds. Both
  // are cases where the easy answer is a default, and a default here would print
  // a figure in a currency the user never chose.
  it("refuses to place a series whose account's currency cannot be read", () => {
    const got = foldRecurringMonthly(
      [
        series({ id: "s1", monthlyAmount: 100 }),
        series({
          id: "s2",
          accountId: "gone",
          accountName: "Old account",
          monthlyAmount: 250,
        }),
        series({ id: "s3", accountId: "a2", monthlyAmount: 7 }),
      ],
      [account({ id: "a1" }), account({ id: "a2", currency: "" })],
    );

    expect(got.expense).toEqual({ INR: 100 });
    // Named by the joined accountName where the API supplied one, and by the id
    // where it did not — either way a label the user can act on, never a
    // currency guessed for them.
    expect(got.unplaced).toEqual(["Old account", "a2"]);
    // And nothing was invented to make the arithmetic come out whole.
    expect(got.net).toEqual({ INR: -100 });
  });
});

describe("recurringTotalText", () => {
  it("names a multi-currency figure and refuses to total it", () => {
    const mixed: CurrencyAmounts = { INR: 50000, USD: 10 };
    const text = recurringTotalText(mixed, []);
    expect(text).toContain("INR");
    expect(text).toContain("USD");
    expect(text).toContain("not combined");
  });

  it("says a forecast it could not place is not counted, not empty", () => {
    expect(recurringTotalText({}, ["Checking"])).toBe(
      "not counted — an account's currency could not be read",
    );
    expect(recurringTotalText({}, [])).toBe("no active series");
  });
});

describe("recurringNetClass", () => {
  it("draws a net with no sign uncoloured", () => {
    // One currency down and another up: there is no sign to report, and the
    // muted token is the one that claims nothing.
    expect(recurringNetClass({ INR: -50000, USD: 15 })).toBe(
      "text-muted-foreground",
    );
    expect(recurringNetClass({ INR: 50000 })).toBe("text-chart-3");
    expect(recurringNetClass({ INR: -50000 })).toBe("text-destructive");
  });
});
