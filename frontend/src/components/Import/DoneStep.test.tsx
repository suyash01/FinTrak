import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
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

  it("reports duplicate skips when nothing was excluded", () => {
    renderStep({
      importResult: { imported: 2, total: 2, duplicates: 2 },
      excludedCount: 0,
    });
    expect(screen.getByText(/2 duplicates skipped/)).toBeInTheDocument();
    expect(screen.queryByText(/excluded/)).toBeNull();
  });

  it("omits the duplicate note when none were skipped", () => {
    renderStep({
      importResult: { imported: 3, total: 3, duplicates: 0 },
      excludedCount: 0,
    });
    expect(screen.queryByText(/duplicates skipped/)).toBeNull();
    expect(screen.getByText(/of 3 transactions imported/)).toBeInTheDocument();
  });

  it("navigates to transactions and linking routes", async () => {
    const user = userEvent.setup();

    function LocationProbe() {
      const location = useLocation();
      return <div data-testid="location">{location.pathname}</div>;
    }

    render(
      <MemoryRouter initialEntries={["/import"]}>
        <Routes>
          <Route
            path="*"
            element={
              <>
                <DoneStep
                  importResult={RESULT}
                  parsedTransactions={TXNS}
                  excludedCount={1}
                  onImportAnother={vi.fn()}
                />
                <LocationProbe />
              </>
            }
          />
        </Routes>
      </MemoryRouter>,
    );

    expect(screen.getByTestId("location")).toHaveTextContent("/import");
    await user.click(
      screen.getByRole("button", { name: "View Transactions" }),
    );
    expect(screen.getByTestId("location")).toHaveTextContent("/transactions");
    await user.click(
      screen.getByRole("button", { name: "Transfer Suggestions" }),
    );
    expect(screen.getByTestId("location")).toHaveTextContent("/linking");
  });
});
