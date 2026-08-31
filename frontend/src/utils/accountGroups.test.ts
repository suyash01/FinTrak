import { describe, expect, it } from "vitest";
import { groupAccountsByType } from "./accountGroups";
import type { Account } from "../types";

const mk = (
  id: string,
  name: string,
  accountTypeId: string,
  accountTypeName: string,
  closed = false,
): Account => ({
  id,
  name,
  accountTypeId,
  accountTypeName,
  bank: "",
  currency: "INR",
  color: "#000000",
  isDefault: false,
  closed,
  balance: 0,
});

describe("groupAccountsByType", () => {
  it("groups by type name, sorts accounts by name, orders groups alphabetically", () => {
    const groups = groupAccountsByType([
      mk("1", "HDFC Savings", "bank", "Bank Account"),
      mk("2", "Home Loan", "loan", "Loan / EMI"),
      mk("3", "Axis Savings", "bank", "Bank Account"),
      mk("4", "SBI Card", "credit_card", "Credit Card"),
    ]);
    expect(groups.map((g) => g.typeName)).toEqual([
      "Bank Account",
      "Credit Card",
      "Loan / EMI",
    ]);
    expect(groups[0].accounts.map((a) => a.name)).toEqual([
      "Axis Savings",
      "HDFC Savings",
    ]);
  });

  it("falls back to the type id when the type name is missing", () => {
    const groups = groupAccountsByType([mk("1", "X", "bank", "")]);
    expect(groups[0].typeName).toBe("bank");
  });

  it("keeps the closed flag on grouped accounts", () => {
    const groups = groupAccountsByType([
      mk("1", "Old Savings", "bank", "Bank Account", true),
      mk("2", "New Savings", "bank", "Bank Account"),
    ]);
    // Sorted by name: "New Savings" first, then the closed "Old Savings".
    expect(groups[0].accounts[0].closed).toBe(false);
    expect(groups[0].accounts[1].closed).toBe(true);
  });
});