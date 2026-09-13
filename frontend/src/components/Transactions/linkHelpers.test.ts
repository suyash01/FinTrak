import { describe, it, expect } from "vitest";
import { linkTypeBadgeClass, orderLinkEndpoints } from "./linkHelpers";
import type { Transaction } from "../../types";

const makeTxn = (overrides: Partial<Transaction> = {}): Transaction => ({
  id: "t1",
  accountId: "a1",
  date: "2024-03-15",
  description: "Test",
  amount: 100,
  type: "debit",
  ...overrides,
});

describe("linkHelpers", () => {
  it("keeps a debit source as the from endpoint", () => {
    const { fromId, toId } = orderLinkEndpoints(
      makeTxn({ id: "debit", type: "debit" }),
      makeTxn({ id: "credit", type: "credit" }),
    );
    expect(fromId).toBe("debit");
    expect(toId).toBe("credit");
  });

  it("swaps when the opened transaction is the credit", () => {
    const { fromId, toId } = orderLinkEndpoints(
      makeTxn({ id: "credit", type: "credit" }),
      makeTxn({ id: "debit", type: "debit" }),
    );
    expect(fromId).toBe("debit");
    expect(toId).toBe("credit");
  });

  it("does not swap same-type pairs", () => {
    const { fromId, toId } = orderLinkEndpoints(
      makeTxn({ id: "a", type: "credit" }),
      makeTxn({ id: "b", type: "credit" }),
    );
    expect(fromId).toBe("a");
    expect(toId).toBe("b");
  });

  it("maps each link type to a badge class", () => {
    expect(linkTypeBadgeClass("transfer")).toContain("text-primary");
    expect(linkTypeBadgeClass("cashback")).toContain("emerald");
    expect(linkTypeBadgeClass("refund")).toContain("amber");
    expect(linkTypeBadgeClass("bill_payment")).toContain("sky");
  });
});
