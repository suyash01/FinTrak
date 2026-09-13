import { useState } from "react";
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import AccountFormDialog from "./AccountFormDialog";
import type { AccountForm } from "./accountHelpers";
import type { AccountType } from "../../types";

// Radix needs Pointer Capture / scrollIntoView in jsdom.
if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

const ACCOUNT_TYPES: AccountType[] = [
  { id: "bank", name: "Bank", positiveTxnType: "credit" },
  { id: "credit_card", name: "Credit Card", positiveTxnType: "debit" },
];

const EMPTY: AccountForm = {
  name: "",
  accountTypeId: "bank",
  bank: "",
  color: "#06b6d4",
  currency: "INR",
  billingDay: null,
  closed: false,
};

function Harness({ onSubmit = vi.fn(), showClosed = false }) {
  const [form, setForm] = useState<AccountForm>(EMPTY);
  return (
    <AccountFormDialog
      open
      onOpenChange={() => {}}
      title="New Account"
      form={form}
      onChange={setForm}
      onSubmit={onSubmit}
      accountTypes={ACCOUNT_TYPES}
      submitLabel="Create"
      billingDayHint="Optional."
      showClosed={showClosed}
    />
  );
}

describe("AccountFormDialog", () => {
  it("disables submit until a name is entered", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn();
    render(<Harness onSubmit={onSubmit} />);

    const submit = screen.getByRole("button", { name: "Create" });
    expect(submit).toBeDisabled();

    await user.type(screen.getByPlaceholderText("e.g. HDFC Savings"), "Wallet");
    expect(submit).toBeEnabled();

    await user.click(submit);
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it("shows the closed-account toggle only in edit mode", () => {
    const { rerender } = render(<Harness />);
    expect(screen.queryByLabelText(/closed account/i)).toBeNull();

    rerender(<Harness showClosed />);
    expect(screen.getByLabelText(/closed account/i)).toBeInTheDocument();
  });
});
