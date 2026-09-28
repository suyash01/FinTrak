import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import ImportPreviewTable from "./ImportPreviewTable";
import { formatOne } from "../../lib/currency";
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
        currency="INR"
      />,
    );
    expect(screen.getByText("Coffee")).toBeInTheDocument();
    expect(screen.getByText("Salary")).toBeInTheDocument();
  });

  // Every row in a batch is committed to ONE account, so the whole preview is
  // denominated in that account's currency and one code is correct for all of
  // it. It used to call formatCurrency with no currency and get the INR default,
  // so importing into a USD account previewed every amount in rupees and the
  // user committed a file whose totals did not match the preview.
  it("labels every amount in the currency of the account it will import into", () => {
    render(
      <ImportPreviewTable
        transactions={TXNS}
        excluded={new Set()}
        onExcludedChange={vi.fn()}
        currency="USD"
      />,
    );
    // Asserted on the row's text rather than a single element: the cell renders
    // the sign and the figure as sibling text nodes, so the span's full text is
    // "−USD $250.00" and an exact-match query cannot see the amount.
    const coffee = screen.getByText("Coffee").closest("tr")!;
    const salary = screen.getByText("Salary").closest("tr")!;
    expect(coffee.textContent).toContain(formatOne(250, "USD"));
    expect(salary.textContent).toContain(formatOne(50000, "USD"));
    // The wrong currency, named rather than left to a reader.
    expect(coffee.textContent).not.toContain(formatOne(250, "INR"));
  });

  // Before the target account is chosen there is no currency to name, and the
  // honest rendering is a bare number. This is the case the removed "INR"
  // default used to paper over.
  it("names no currency when none has been chosen yet", () => {
    render(
      <ImportPreviewTable
        transactions={TXNS}
        excluded={new Set()}
        onExcludedChange={vi.fn()}
        currency=""
      />,
    );
    const coffee = screen.getByText("Coffee").closest("tr")!;
    expect(coffee.textContent).toContain(formatOne(250, ""));
    expect(coffee.textContent).not.toContain("₹");
  });

  it("shows the Payee column only when enabled", () => {
    const { rerender } = render(
      <ImportPreviewTable
        transactions={[{ ...TXNS[0], payeeId: "p1" }]}
        payees={PAYEES}
        excluded={new Set()}
        onExcludedChange={vi.fn()}
        currency="INR"
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
        currency="INR"
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
        currency="INR"
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
        currency="INR"
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
