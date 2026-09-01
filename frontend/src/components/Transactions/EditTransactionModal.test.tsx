import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import EditTransactionModal from "./EditTransactionModal";
import type {
  Transaction,
  Account,
  BillingCycle,
  Category,
  CategoryGroup,
  Payee,
} from "../../types";

// Radix Select/Dialog (Sheet) jsdom polyfills — required before render.
if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
}
if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {};

const apiMocks = vi.hoisted(() => ({
  getBillingCycles: vi.fn(),
  createTransaction: vi.fn(),
  updateTransaction: vi.fn(),
}));

vi.mock("../../api/client", () => ({
  default: {
    getBillingCycles: apiMocks.getBillingCycles,
    createTransaction: apiMocks.createTransaction,
    updateTransaction: apiMocks.updateTransaction,
  },
  downloadCSV: vi.fn(),
}));

const account: Account = {
  id: "acct-1",
  name: "HDFC Credit Card",
  accountTypeId: "credit_card",
  accountTypeName: "Credit Card",
  bank: "HDFC",
  currency: "INR",
  color: "#000000",
  isDefault: false,
  closed: false,
  balance: 0,
  billingDay: 5,
};

const cycles: BillingCycle[] = [
  {
    id: "cycle-A",
    accountId: "acct-1",
    startDate: "2026-07-06T00:00:00Z",
    endDate: "2026-08-05T00:00:00Z",
    label: "Aug 2026",
    totalOutstanding: 0,
    transactionCount: 0,
  },
];

const baseTransaction: Transaction = {
  id: "txn-1",
  accountId: "acct-1",
  date: "2026-07-20T00:00:00Z",
  description: "Swiggy",
  amount: 450.5,
  type: "debit",
};

const noCategories: Category[] = [];
const noGroups: CategoryGroup[] = [];
const noPayees: Payee[] = [];

function billingCycleTrigger(): HTMLElement {
  const label = screen.getByText("Billing Cycle");
  const section = label.parentElement!;
  const trigger = section.querySelector<HTMLElement>('[role="combobox"]');
  if (!trigger) throw new Error("billing cycle select trigger not found");
  return trigger;
}

describe("EditTransactionModal — billing cycle dropdown", () => {
  beforeEach(() => {
    apiMocks.getBillingCycles.mockReset();
    apiMocks.createTransaction.mockReset();
    apiMocks.updateTransaction.mockReset();
  });

  it("shows the cycle label when the transaction's cycle is in the account's cycle list", async () => {
    apiMocks.getBillingCycles.mockResolvedValue({ data: cycles });
    render(
      <EditTransactionModal
        transaction={{ ...baseTransaction, billingCycleId: "cycle-A" }}
        accounts={[account]}
        categories={noCategories}
        groups={noGroups}
        payees={noPayees}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    );
    await waitFor(() => expect(apiMocks.getBillingCycles).toHaveBeenCalledTimes(1));
    await waitFor(() => {
      expect(billingCycleTrigger().textContent).toContain("Aug 2026");
    });
  });

  it("REPRO (root cause): keeps showing the attached cycle when the transaction's account is not the first account in the list", async () => {
    // Effect 2 runs once with the INITIAL form state (accountId = accounts[0])
    // before effect 1 swaps in the transaction's own account. The stale pass
    // records prevAccountRef = accounts[0].id, so when the form settles on the
    // real account the "account changed" check wipes billingCycleId and the
    // dropdown shows nothing — even though the cycle IS in the fetched list.
    const firstAccount: Account = {
      id: "acct-savings",
      name: "Savings",
      accountTypeId: "bank",
      accountTypeName: "Bank",
      bank: "",
      currency: "INR",
      color: "#000000",
      isDefault: true,
      closed: false,
      balance: 0,
      billingDay: null,
    };
    apiMocks.getBillingCycles.mockResolvedValue({ data: cycles });
    render(
      <EditTransactionModal
        transaction={{ ...baseTransaction, billingCycleId: "cycle-A" }}
        accounts={[firstAccount, account]}
        categories={noCategories}
        groups={noGroups}
        payees={noPayees}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    );
    await waitFor(() => expect(apiMocks.getBillingCycles).toHaveBeenCalled());
    await waitFor(() => {
      expect(billingCycleTrigger().textContent).toContain("Aug 2026");
    });
  });

  it("shows the attached cycle even when it is missing from the fetched cycle list (fallback item), using the label from the transaction response", async () => {
    apiMocks.getBillingCycles.mockResolvedValue({ data: cycles });
    render(
      <EditTransactionModal
        transaction={{
          ...baseTransaction,
          billingCycleId: "cycle-zz",
          billingCycleLabel: "Aug 2026",
        }}
        accounts={[account]}
        categories={noCategories}
        groups={noGroups}
        payees={noPayees}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    );
    await waitFor(() => expect(apiMocks.getBillingCycles).toHaveBeenCalledTimes(1));
    await waitFor(() => {
      expect(billingCycleTrigger().textContent).toContain("Aug 2026");
    });
  });

  it("hides the billing cycle block entirely when the account has no billing day, even if the transaction carries a billingCycleId", async () => {
    apiMocks.getBillingCycles.mockResolvedValue({ data: cycles });
    render(
      <EditTransactionModal
        transaction={{ ...baseTransaction, billingCycleId: "cycle-A" }}
        accounts={[{ ...account, billingDay: null }]}
        categories={noCategories}
        groups={noGroups}
        payees={noPayees}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    );
    // No billing day → the cycle effect returns early without fetching.
    expect(apiMocks.getBillingCycles).not.toHaveBeenCalled();
    await waitFor(() => {
      expect(screen.queryByText("Billing Cycle")).toBeNull();
    });
  });

  it("re-selects the transaction's own cycle when it is present in the list (edit save keeps the attachment)", async () => {
    apiMocks.getBillingCycles.mockResolvedValue({ data: cycles });
    apiMocks.updateTransaction.mockResolvedValue({});
    render(
      <EditTransactionModal
        transaction={{ ...baseTransaction, billingCycleId: "cycle-A" }}
        accounts={[account]}
        categories={noCategories}
        groups={noGroups}
        payees={noPayees}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    );
    await waitFor(() => expect(billingCycleTrigger().textContent).toContain("Aug 2026"));
    await userEvent.click(screen.getByRole("button", { name: "Save Changes" }));
    await waitFor(() => expect(apiMocks.updateTransaction).toHaveBeenCalledTimes(1));
    expect(apiMocks.updateTransaction.mock.calls[0][1].billingCycleId).toBe("cycle-A");
  });
});