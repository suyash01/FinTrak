import type { Account, UpdateAccountRequest } from "../../types";

export interface AccountForm {
  name: string;
  accountTypeId: string;
  bank: string;
  color: string;
  currency: string;
  billingDay: number | null;
  closed: boolean;
}

export const EMPTY_NEW_ACCOUNT: AccountForm = {
  name: "",
  accountTypeId: "bank",
  bank: "",
  color: "#06b6d4",
  currency: "INR",
  billingDay: null,
  closed: false,
};

export const ordinal = (n: number): string => {
  const suffixes = ["th", "st", "nd", "rd"];
  const v = n % 100;
  return suffixes[(v - 20) % 10] || suffixes[v] || suffixes[0];
};

// parseBillingDay maps a number-input value to a billing day. An empty value
// clears the field (null = no billing day / no summary rows).
export const parseBillingDay = (v: string): number | null => {
  if (v === "") return null;
  const n = Number(v);
  if (Number.isNaN(n)) return null;
  return Math.max(1, Math.min(31, n));
};

// balanceLabel picks the display label for an account's balance value by type.
export const balanceLabel = (acc: Account): string => {
  if (acc.accountTypeId === "loan") return "Repaid";
  if (acc.accountTypeId === "credit_card") return "Outstanding";
  return "Balance";
};

// toUpdatePayload flattens an AccountForm (or Account) into the partial-update
// request shape used by PUT /accounts/:id.
export function toUpdatePayload(form: {
  name: string;
  accountTypeId: string;
  bank: string;
  color: string;
  currency: string;
  billingDay?: number | null;
  closed: boolean;
}): UpdateAccountRequest {
  return {
    name: form.name,
    accountTypeId: form.accountTypeId,
    bank: form.bank || "",
    currency: form.currency || "INR",
    color: form.color,
    billingDay: form.billingDay ?? null,
    closed: form.closed,
  };
}
