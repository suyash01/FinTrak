import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import DoneStep from "./DoneStep";
import type { ImportResult, ImportTransaction } from "../../types";

const RESULT: ImportResult = { imported: 2, total: 3, duplicates: 1 };
const TXNS: ImportTransaction[] = [
  { date: "2024-03-15", description: "Coffee", amount: 250, type: "debit" },
  { date: "2024-03-16", description: "Salary", amount: 50000, type: "credit" },
  { date: "2024-03-17", description: "Skipped", amount: 1, type: "debit" },
];

function renderStep(
  overrides: Partial<Parameters<typeof DoneStep>[0]> = {},
) {
  const props = {
    importResult: RESULT,
    parsedTransactions: TXNS,
    excludedCount: 1,
    onImportAnother: vi.fn(),
    ...overrides,
  };
  render(
    <MemoryRouter>
      <DoneStep {...props} />
    </MemoryRouter>,
  );
  return props;
}

describe("DoneStep", () => {
  it("renders the import summary", () => {
    renderStep();
    expect(screen.getByText("Import Complete!")).toBeInTheDocument();
    expect(screen.getByText(/of 3 transactions imported/)).toBeInTheDocument();
    expect(screen.getByText(/1 duplicate skipped/)).toBeInTheDocument();
    expect(screen.getByText(/1 excluded/)).toBeInTheDocument();
  });

  it("invokes onImportAnother", async () => {
    const user = userEvent.setup();
    const props = renderStep();
    await user.click(screen.getByRole("button", { name: "Import Another" }));
    expect(props.onImportAnother).toHaveBeenCalledTimes(1);
  });

  it("offers navigation to transactions and linking", () => {
    renderStep();
    expect(
      screen.getByRole("button", { name: "View Transactions" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Transfer Suggestions" }),
    ).toBeInTheDocument();
  });
});
