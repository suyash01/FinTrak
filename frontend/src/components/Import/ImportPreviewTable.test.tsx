import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import ImportPreviewTable from "./ImportPreviewTable";
import type { ImportTransaction, Payee } from "../../types";

const TXNS: ImportTransaction[] = [
  { date: "2024-03-15", description: "Coffee", amount: 250, type: "debit" },
  { date: "2024-03-16", description: "Salary", amount: 50000, type: "credit" },
];

const PAYEES = [{ id: "p1", name: "Cafe" }] as unknown as Payee[];

describe("ImportPreviewTable", () => {
  it("renders every parsed transaction", () => {
    render(
      <ImportPreviewTable
        transactions={TXNS}
        excluded={new Set()}
        onExcludedChange={vi.fn()}
      />,
    );
    expect(screen.getByText("Coffee")).toBeInTheDocument();
    expect(screen.getByText("Salary")).toBeInTheDocument();
  });

  it("shows the Payee column only when enabled", () => {
    const { rerender } = render(
      <ImportPreviewTable
        transactions={[{ ...TXNS[0], payeeId: "p1" }]}
        payees={PAYEES}
        excluded={new Set()}
        onExcludedChange={vi.fn()}
      />,
    );
    expect(screen.getByText("Payee")).toBeInTheDocument();
    expect(screen.getByText("Cafe")).toBeInTheDocument();

    rerender(
      <ImportPreviewTable
        transactions={[{ ...TXNS[0], payeeId: "p1" }]}
        payees={PAYEES}
        showPayee={false}
        excluded={new Set()}
        onExcludedChange={vi.fn()}
      />,
    );
    expect(screen.queryByText("Payee")).toBeNull();
  });

  it("toggling a row excludes it", async () => {
    const user = userEvent.setup();
    const onExcludedChange = vi.fn();
    render(
      <ImportPreviewTable
        transactions={TXNS}
        excluded={new Set()}
        onExcludedChange={onExcludedChange}
      />,
    );

    await user.click(screen.getByRole("checkbox", { name: "Coffee" }));
    expect(onExcludedChange).toHaveBeenCalledTimes(1);

    const updater = onExcludedChange.mock.calls[0][0] as (
      prev: Set<number>,
    ) => Set<number>;
    expect(updater(new Set<number>())).toEqual(new Set([0]));
  });

  it("select-all clears the exclusion set when everything is selected", async () => {
    const user = userEvent.setup();
    const onExcludedChange = vi.fn();
    render(
      <ImportPreviewTable
        transactions={TXNS}
        excluded={new Set()}
        onExcludedChange={onExcludedChange}
      />,
    );

    await user.click(
      screen.getByRole("checkbox", {
        name: "Include all parsed transactions",
      }),
    );
    expect(onExcludedChange).toHaveBeenCalledWith(new Set([0, 1]));
  });
});
