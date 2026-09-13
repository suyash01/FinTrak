import { describe, it, expect } from "vitest";
import {
  balanceLabel,
  ordinal,
  parseBillingDay,
  toUpdatePayload,
  EMPTY_NEW_ACCOUNT,
} from "./accountHelpers";
import type { Account } from "../../types";

const makeAccount = (overrides: Partial<Account> = {}): Account => ({
  id: "a1",
  name: "Checking",
  accountTypeId: "bank",
  bank: "HDFC",
  currency: "INR",
  color: "#06b6d4",
  isDefault: false,
  closed: false,
  balance: 1000,
  ...overrides,
});

describe("accountHelpers", () => {
  it("returns ordinal suffixes", () => {
    expect(ordinal(1)).toBe("st");
    expect(ordinal(2)).toBe("nd");
    expect(ordinal(3)).toBe("rd");
    expect(ordinal(4)).toBe("th");
    expect(ordinal(11)).toBe("th");
    expect(ordinal(21)).toBe("st");
  });

  it("parses and clamps billing days", () => {
    expect(parseBillingDay("")).toBeNull();
    expect(parseBillingDay("abc")).toBeNull();
    expect(parseBillingDay("0")).toBe(1);
    expect(parseBillingDay("40")).toBe(31);
    expect(parseBillingDay("15")).toBe(15);
  });

  it("labels balances by account type", () => {
    expect(balanceLabel(makeAccount({ accountTypeId: "loan" }))).toBe("Repaid");
    expect(balanceLabel(makeAccount({ accountTypeId: "credit_card" }))).toBe(
      "Outstanding",
    );
    expect(balanceLabel(makeAccount({ accountTypeId: "bank" }))).toBe("Balance");
  });

  it("maps an empty form to the update payload", () => {
    expect(toUpdatePayload(EMPTY_NEW_ACCOUNT)).toEqual({
      name: "",
      accountTypeId: "bank",
      bank: "",
      currency: "INR",
      color: "#06b6d4",
      billingDay: null,
      closed: false,
    });
  });

  it("falls back to INR when currency is blank and preserves the rest", () => {
    const payload = toUpdatePayload({
      name: "Card",
      accountTypeId: "credit_card",
      bank: "",
      color: "#ff0000",
      currency: "",
      billingDay: 5,
      closed: true,
    });
    expect(payload).toEqual({
      name: "Card",
      accountTypeId: "credit_card",
      bank: "",
      currency: "INR",
      color: "#ff0000",
      billingDay: 5,
      closed: true,
    });
  });

  it("accepts a full Account as the payload source", () => {
    const payload = toUpdatePayload(
      makeAccount({ billingDay: 20, closed: true }),
    );
    expect(payload.billingDay).toBe(20);
    expect(payload.closed).toBe(true);
    expect(payload.name).toBe("Checking");
  });
});
