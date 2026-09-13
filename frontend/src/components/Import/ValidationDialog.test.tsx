import { describe, it, expect, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import ValidationDialog from "./ValidationDialog";
import type { ValidateTransactionsResponse } from "../../types";

const RESULT: ValidateTransactionsResponse = {
  total: 2,
  existingCount: 1,
  missingCount: 1,
  results: [
    {
      index: 0,
      exists: true,
      date: "2024-03-15",
      description: "Coffee",
      amount: 250,
      type: "debit",
    },
    {
      index: 1,
      exists: false,
      date: "2024-03-16",
      description: "Salary",
      amount: 50000,
      type: "credit",
    },
  ],
};

function renderDialog(
  overrides: Partial<Parameters<typeof ValidationDialog>[0]> = {},
) {
  const props = {
    result: RESULT,
    accountName: "Test Account",
    onClose: vi.fn(),
    ...overrides,
  };
  render(<ValidationDialog {...props} />);
  return props;
}

describe("ValidationDialog", () => {
  it("renders the summary and per-transaction rows", () => {
    renderDialog();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText("Validation Results")).toBeInTheDocument();
    expect(screen.getByText("Test Account")).toBeInTheDocument();
    expect(screen.getByText("Total")).toBeInTheDocument();
    expect(screen.getByText("Already exist")).toBeInTheDocument();
    expect(screen.getAllByText("New").length).toBeGreaterThan(0);
    expect(screen.getByText("Coffee")).toBeInTheDocument();
    expect(screen.getByText("Salary")).toBeInTheDocument();
    expect(screen.getByText("Already exists")).toBeInTheDocument();
  });

  it("renders signed amounts for debit and credit rows", () => {
    renderDialog();
    expect(screen.getByText(/250\.00/)).toBeInTheDocument();
    expect(screen.getByText(/50,000\.00/)).toBeInTheDocument();
  });

  it("falls back to a generic account label when none is provided", () => {
    renderDialog({ accountName: "" });
    expect(screen.getByText("this account")).toBeInTheDocument();
  });

  it("invokes onClose from the footer button", async () => {
    const user = userEvent.setup();
    const props = renderDialog();
    const closeButtons = screen.getAllByRole("button", { name: "Close" });
    await user.click(closeButtons[0]);
    expect(props.onClose).toHaveBeenCalledTimes(1);
  });

  it("invokes onClose when dismissed with Escape", async () => {
    const user = userEvent.setup();
    const props = renderDialog();
    await user.keyboard("{Escape}");
    await waitFor(() => expect(props.onClose).toHaveBeenCalled());
  });
});
