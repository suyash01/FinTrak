import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import LinkTypeStep from "./LinkTypeStep";
import type { LinkType, Transaction } from "../../types";

if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

const txn: Transaction = {
  id: "s1",
  accountId: "a1",
  date: "2024-03-15",
  description: "Debit Source",
  amount: 250,
  type: "debit",
  accountName: "Checking",
};

const target: Transaction = {
  id: "t1",
  accountId: "a2",
  date: "2024-03-16",
  description: "Credit Target",
  amount: 250,
  type: "credit",
  accountName: "Savings",
};

function renderStep(overrides: Record<string, unknown> = {}) {
  const props = {
    txn,
    target,
    linkType: "transfer" as LinkType,
    onLinkTypeChange: vi.fn(),
    onBack: vi.fn(),
    onConfirm: vi.fn(),
    ...overrides,
  };
  const view = render(<LinkTypeStep {...props} />);
  return { ...view, props };
}

describe("LinkTypeStep", () => {
  it("previews both endpoints and all four link types", () => {
    renderStep();

    expect(screen.getByText("Choose Link Type")).toBeInTheDocument();
    expect(screen.getByText("Source")).toBeInTheDocument();
    expect(screen.getByText("Target")).toBeInTheDocument();
    expect(screen.getByText("Debit Source")).toBeInTheDocument();
    expect(screen.getByText("Credit Target")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Transfer/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Cashback/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Refund/ })).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /Bill Payment/ }),
    ).toBeInTheDocument();
  });

  it("emits the chosen link type", async () => {
    const user = userEvent.setup();
    const { props } = renderStep();

    await user.click(screen.getByRole("button", { name: /Cashback/ }));
    expect(props.onLinkTypeChange).toHaveBeenCalledWith("cashback");

    await user.click(screen.getByRole("button", { name: /Refund/ }));
    expect(props.onLinkTypeChange).toHaveBeenCalledWith("refund");

    await user.click(screen.getByRole("button", { name: /Bill Payment/ }));
    expect(props.onLinkTypeChange).toHaveBeenCalledWith("bill_payment");

    await user.click(screen.getByRole("button", { name: /Transfer/ }));
    expect(props.onLinkTypeChange).toHaveBeenCalledWith("transfer");
  });

  it("confirms the link", async () => {
    const user = userEvent.setup();
    const { props } = renderStep();

    await user.click(screen.getByRole("button", { name: "Confirm Link" }));
    expect(props.onConfirm).toHaveBeenCalledTimes(1);
  });

  it("goes back from the footer", async () => {
    const user = userEvent.setup();
    const { props } = renderStep();

    await user.click(screen.getByRole("button", { name: "Back to Results" }));
    expect(props.onBack).toHaveBeenCalledTimes(1);
  });

  it("disables confirm when no link type is selected", () => {
    renderStep({ linkType: "" as LinkType });
    expect(screen.getByRole("button", { name: "Confirm Link" })).toBeDisabled();
  });
});
