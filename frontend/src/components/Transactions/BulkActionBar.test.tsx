import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import BulkActionBar, { UNLINK_LOAN } from "./BulkActionBar";
import type { CategorySection } from "../../lib/categories";
import type { Account, BillingCycle, Payee, RecurringSeries } from "../../types";

const categorySections = [
  {
    group: { id: "g1", name: "Food" },
    items: [{ id: "c1", name: "Groceries" }],
  },
] as unknown as CategorySection[];

const payees = [{ id: "p1", name: "Swiggy" }] as unknown as Payee[];
const billingCycles = [
  { id: "bc1", label: "March", startDate: "2024-03-01", endDate: "2024-03-31" },
] as unknown as BillingCycle[];
const loanAccounts = [{ id: "l1", name: "Car Loan" }] as unknown as Account[];
const recurringSeries = [
  { id: "rs1", name: "Netflix" },
] as unknown as RecurringSeries[];

function renderBar(overrides: Partial<Parameters<typeof BulkActionBar>[0]> = {}) {
  const props = {
    selectedCount: 3,
    categorySections,
    payees,
    hasBillingDayFilter: false,
    loadingCycles: false,
    billingCycles,
    loanAccounts: [],
    recurringSeries: [],
    onCategorize: vi.fn(),
    onUpdatePayee: vi.fn(),
    onSetBillingCycle: vi.fn(),
    onLinkLoan: vi.fn(),
    onLinkRecurring: vi.fn(),
    onDelete: vi.fn(),
    onClear: vi.fn(),
    ...overrides,
  };
  const view = render(<BulkActionBar {...props} />);
  return { ...view, props };
}

describe("BulkActionBar", () => {
  it("shows the selection count and fires categorize/payee actions", () => {
    const { container, props } = renderBar();
    expect(screen.getByText("3 selected")).toBeInTheDocument();

    const [categorize, payee] = Array.from(
      container.querySelectorAll<HTMLSelectElement>("select"),
    );
    fireEvent.change(categorize, { target: { value: "c1" } });
    expect(props.onCategorize).toHaveBeenCalledWith("c1");

    fireEvent.change(payee, { target: { value: "p1" } });
    expect(props.onUpdatePayee).toHaveBeenCalledWith("p1");
  });

  it("hides billing-cycle and loan selects unless applicable", () => {
    const { container } = renderBar();
    expect(container.querySelectorAll("select")).toHaveLength(2);
    expect(screen.queryByText("Link to Loan...")).toBeNull();
  });

  it("renders billing-cycle and loan selects when applicable", () => {
    const { container, props } = renderBar({
      hasBillingDayFilter: true,
      loanAccounts,
    });
    const selects = Array.from(
      container.querySelectorAll<HTMLSelectElement>("select"),
    );
    expect(selects).toHaveLength(4);

    const bar = container;
    expect(bar.textContent).toContain("Set Billing Cycle...");
    expect(bar.textContent).toContain("Unlink from loan");

    fireEvent.change(selects[3], { target: { value: UNLINK_LOAN } });
    expect(props.onLinkLoan).toHaveBeenCalledWith(UNLINK_LOAN);
  });

  it("wires delete and clear", () => {
    const { props } = renderBar();
    fireEvent.click(screen.getByRole("button", { name: /delete/i }));
    expect(props.onDelete).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: /clear/i }));
    expect(props.onClear).toHaveBeenCalledTimes(1);
  });

  it("renders the subscription select and fires the link action", () => {
    const { container, props } = renderBar({ recurringSeries });
    const selects = Array.from(
      container.querySelectorAll<HTMLSelectElement>("select"),
    );
    // categorize, payee, subscription
    expect(selects).toHaveLength(3);
    expect(container.textContent).toContain("Link to subscription...");

    fireEvent.change(selects[2], { target: { value: "rs1" } });
    expect(props.onLinkRecurring).toHaveBeenCalledWith("rs1");
  });
});
