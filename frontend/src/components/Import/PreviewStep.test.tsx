import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import PreviewStep from "./PreviewStep";
import type { ImportTransaction } from "../../types";

if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

const TXNS: ImportTransaction[] = [
  { date: "2024-03-15", description: "Coffee", amount: 250, type: "debit" },
  { date: "2024-03-16", description: "Salary", amount: 50000, type: "credit" },
];

function renderStep(overrides: Partial<Parameters<typeof PreviewStep>[0]> = {}) {
  const props = {
    parsedTransactions: TXNS,
    payees: [],
    excluded: new Set<number>(),
    onExcludedChange: vi.fn(),
    includedCount: 2,
    excludedCount: 0,
    statementTxns: null,
    csvData: null,
    selectedAccountHasBillingDay: null,
    billingCycles: [],
    importBillingCycleId: "",
    onImportBillingCycleChange: vi.fn(),
    statementSummary: null,
    dupCount: 0,
    existingDupCount: 0,
    inFileDupCount: 0,
    pdfFile: null,
    extractor: "sbi_cc",
    onExtractorChange: vi.fn(),
    extractors: [],
    pdfDateFormat: "auto",
    onPdfDateFormatChange: vi.fn(),
    onReparse: vi.fn(),
    parsing: false,
    validating: false,
    importing: false,
    onBack: vi.fn(),
    onValidate: vi.fn(),
    onImport: vi.fn(),
    ...overrides,
  };
  render(<PreviewStep {...props} />);
  return props;
}

describe("PreviewStep", () => {
  it("summarises the parsed transactions and the import count", () => {
    renderStep();
    expect(
      screen.getByText(/Preview — 2 transactions/),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Import 2 Transactions" }),
    ).toBeEnabled();
  });

  it("invokes validate and import handlers", async () => {
    const user = userEvent.setup();
    const props = renderStep();
    await user.click(screen.getByRole("button", { name: /validate/i }));
    await user.click(
      screen.getByRole("button", { name: "Import 2 Transactions" }),
    );
    expect(props.onValidate).toHaveBeenCalledTimes(1);
    expect(props.onImport).toHaveBeenCalledTimes(1);
  });

  it("disables the actions and shows the excluded banner when nothing is selected", () => {
    renderStep({ includedCount: 0, excludedCount: 2 });
    expect(
      screen.getByRole("button", { name: "Import 0 Transactions" }),
    ).toBeDisabled();
    expect(screen.getByRole("button", { name: /validate/i })).toBeDisabled();
    expect(
      screen.getByText(/2 transactions excluded from import/),
    ).toBeInTheDocument();
  });

  it("shows the duplicate warning when duplicates are detected", () => {
    renderStep({ dupCount: 1, existingDupCount: 1 });
    expect(screen.getByText(/look like duplicates/)).toBeInTheDocument();
  });
});
