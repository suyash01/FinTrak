import type { ComponentProps } from "react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
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

// A stored row as the API actually returns one: `notes` and `tags` are never
// NULL (transaction.go binds []string{} on every write edge), so the form's
// `notes: ""` and `tags: []` for a row that has neither are the values the row
// already holds, not changes the user made. A base without them would make every
// untouched save look like it had also cleared the notes and the tags.
const storedRow: Transaction = { ...baseTransaction, notes: "", tags: [] };

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

describe("EditTransactionModal — default date", () => {
  // A ledger date is a local-calendar fact. `toISOString()` is UTC, so east of
  // UTC (the app's INR/en-IN target is +05:30) it names yesterday for the first
  // hours of every local day. The zone is pinned so this assertion means the
  // same thing on any host.
  const hostTz = process.env.TZ;
  beforeEach(() => {
    process.env.TZ = "Asia/Kolkata";
  });
  afterEach(() => {
    vi.useRealTimers();
    process.env.TZ = hostTz;
  });

  it("pre-fills a new transaction with the local day, not the UTC day", () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    // 02:00 IST on the 16th is 2026-03-15T20:30Z: the UTC day is still the 15th.
    vi.setSystemTime(new Date("2026-03-15T20:30:00Z"));
    apiMocks.getBillingCycles.mockResolvedValue({ data: [] });

    render(
      <EditTransactionModal
        accounts={[account]}
        categories={noCategories}
        groups={noGroups}
        payees={noPayees}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    );

    expect(screen.getByLabelText("Date")).toHaveValue("2026-03-16");
  });
});

describe("EditTransactionModal — zero amount", () => {
  beforeEach(() => {
    apiMocks.getBillingCycles.mockReset();
    apiMocks.getBillingCycles.mockResolvedValue({ data: [] });
  });

  it('pre-fills a stored zero amount as "0", not an empty field', () => {
    render(
      <EditTransactionModal
        transaction={{ ...baseTransaction, amount: 0 }}
        accounts={[account]}
        categories={noCategories}
        groups={noGroups}
        payees={noPayees}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    );

    expect(screen.getByLabelText("Amount")).toHaveValue(0);
  });
});

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

  it("keeps showing the attached cycle when the transaction's account is not the first account in the list", async () => {
    // Guards the fix for the initial-render ordering bug: the form-rebuild
    // effect and the cycle-loading effect both run on mount. Regression would
    // be the mount pass (using the default accounts[0]) clearing the attached
    // billingCycleId once the form settles on the transaction's real account,
    // leaving the dropdown empty even though the cycle is in the fetched list.
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
    apiMocks.updateTransaction.mockResolvedValue({ id: "txn-1", queued: false });
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
    // The payload still names the cycle. With a base in place that is redundant
    // with the diff — an unchanged field is dropped, so a null here would never
    // be sent either way — and it stays because the reasoning stands on its own.
    expect(apiMocks.updateTransaction.mock.calls[0][1].billingCycleId).toBe("cycle-A");
  });
});

describe("EditTransactionModal — the base an edit is made against", () => {
  beforeEach(() => {
    apiMocks.getBillingCycles.mockReset();
    apiMocks.getBillingCycles.mockResolvedValue({ data: [] });
    apiMocks.updateTransaction.mockReset();
    // A real row always carries notes and tags: `transactions.notes` and
    // `transactions.tags` are never NULL, so a payload's `""` and `[]` for them
    // are values the row already holds rather than changes the user made. A base
    // that omits them would make every save look like it cleared both.
    apiMocks.updateTransaction.mockResolvedValue({ id: "txn-1", queued: false });
  });

  function renderModal(over: Partial<ComponentProps<typeof EditTransactionModal>> = {}) {
    return render(
      <EditTransactionModal
        transaction={storedRow}
        accounts={[account]}
        categories={noCategories}
        groups={noGroups}
        payees={noPayees}
        onClose={() => {}}
        onSaved={() => {}}
        {...over}
      />,
    );
  }

  it("sends the row it opened with as the base, so the request is a diff", async () => {
    // The whole-row PATCH is what reverts a concurrent edit: the form still holds
    // the values it loaded, so every field the user never opened goes back and
    // overwrites whatever another writer changed there meanwhile. The base is
    // what prevents it — updateTransaction reduces the payload against the row
    // the form opened with, and the same patch is what the offline queue records.
    //
    // What is pinned here is this call site's half of that, which is all it owns:
    // the payload is still the whole form (it is the base that makes the reduction
    // possible, and the client is what does it — see client.test.ts), and the base
    // is that row rather than something rebuilt from the form.
    renderModal();
    await userEvent.type(screen.getByLabelText("Notes"), "milk");
    await userEvent.click(screen.getByRole("button", { name: /save/i }));

    await waitFor(() => expect(apiMocks.updateTransaction).toHaveBeenCalledTimes(1));
    const [id, payload, options] = apiMocks.updateTransaction.mock.calls[0];
    expect(id).toBe("txn-1");
    expect(payload).toMatchObject({ notes: "milk", description: "Swiggy" });
    expect(options).toMatchObject({ base: storedRow });
  });

  it("reports the save as done when the user changed nothing", async () => {
    // A submission whose diff is empty issues no request at all and resolves
    // `{ id, queued: false }` — the row already says what the form says. This is
    // the first caller to reach that path in normal use, and a user who opens the
    // sheet, changes their mind and saves has not done anything wrong: the modal's
    // saved path must read the answer as success, or the sheet is left showing an
    // error for a save the server was never asked about.
    const onSaved = vi.fn();
    renderModal({ onSaved });

    await userEvent.click(screen.getByRole("button", { name: /save/i }));

    await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole("alert")).toBeNull();
    // And the base is what makes that answer reachable at all: without one the
    // client has nothing to reduce the payload against, so the empty diff cannot
    // be recognised and a request would go out regardless.
    expect(apiMocks.updateTransaction.mock.calls[0][2]).toMatchObject({ base: storedRow });
  });

  it("clears a category the row held, without touching the fields it did not", async () => {
    // The payload's `categoryId: null` is the user clearing a category, and the
    // diff has to keep it. The rule that would drop it — a null is a change only
    // where the base carries the key — reads the *base*, so a row that never had
    // a category is untouched by this and the clear still goes through.
    const categoriesWithOne: Category[] = [
      { id: "c1", name: "Groceries", groupId: "g1" } as Category,
    ];
    const withCategory: Transaction = { ...storedRow, categoryId: "c1" };
    renderModal({ transaction: withCategory, categories: categoriesWithOne });

    await userEvent.click(screen.getByLabelText("Category"));
    // The option, not the trigger's text: Radix's hidden native select carries
    // the same word, so a text query matches two.
    await userEvent.click(await screen.findByRole("option", { name: "Uncategorized" }));
    await userEvent.click(screen.getByRole("button", { name: /save/i }));

    await waitFor(() => expect(apiMocks.updateTransaction).toHaveBeenCalledTimes(1));
    const [id, payload, options] = apiMocks.updateTransaction.mock.calls[0];
    expect(id).toBe("txn-1");
    expect(payload).toMatchObject({ categoryId: null });
    // The base is the row the form opened with, category and all — a base built
    // from the form's own values could not tell a clear from a no-op.
    expect(options).toMatchObject({ base: withCategory });
  });
});
