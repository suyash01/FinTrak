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

  it("reports CSV rows skipped as empty or invalid", () => {
    renderStep({
      csvData: [
        { Date: "1", Narration: "a", Amount: "1" },
        { Date: "2", Narration: "b", Amount: "2" },
        { Date: "bad", Narration: "c", Amount: "x" },
        { Date: "3", Narration: "d", Amount: "3" },
      ],
    });
    expect(screen.getByText(/2 rows skipped/)).toBeInTheDocument();
  });

  it("uses singular copy for one excluded transaction", () => {
    renderStep({ includedCount: 1, excludedCount: 1 });
    expect(
      screen.getByText(/1 transaction excluded from import/),
    ).toBeInTheDocument();
    expect(screen.getByText("(1 selected)")).toBeInTheDocument();
  });

  it("breaks down existing and in-file duplicates", () => {
    renderStep({ dupCount: 3, existingDupCount: 2, inFileDupCount: 1 });
    expect(
      screen.getByText(/2 already exist in this account/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/repeats? within this file/),
    ).toBeInTheDocument();
  });

  it("renders the statement summary entries", () => {
    renderStep({ statementSummary: { total_amount: "1250", entries: 4 } });
    expect(screen.getByText("Statement Summary")).toBeInTheDocument();
    expect(screen.getByText("total amount:")).toBeInTheDocument();
    expect(screen.getByText("1250")).toBeInTheDocument();
  });

  it("renders an empty preview with disabled actions", () => {
    renderStep({ parsedTransactions: [], includedCount: 0 });
    expect(screen.getByText(/Preview — 0 transactions/)).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Import 0 Transactions" }),
    ).toBeDisabled();
  });

  it("shows validating and importing progress states", () => {
    renderStep({ validating: true, importing: true });
    expect(
      screen.getByRole("button", { name: /Validating/ }),
    ).toBeDisabled();
    expect(screen.getByRole("button", { name: /Importing/ })).toBeDisabled();
  });

  it("makes the back button return to upload for a PDF import", async () => {
    const user = userEvent.setup();
    const props = renderStep({
      statementTxns: TXNS,
      pdfFile: new File(["pdf"], "s.pdf", { type: "application/pdf" }),
    });
    await user.click(screen.getByRole("button", { name: "Back to Upload" }));
    expect(props.onBack).toHaveBeenCalledTimes(1);
  });

  it("shows the reparse controls and invokes reparse", async () => {
    const user = userEvent.setup();
    const props = renderStep({
      statementTxns: TXNS,
      pdfFile: new File(["pdf"], "s.pdf", { type: "application/pdf" }),
      extractors: [{ name: "sbi_cc", display_name: "SBI Credit Card" }],
    });
    expect(screen.getByText("Reparse with extractor")).toBeInTheDocument();
    expect(screen.getByText("Date Format")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Reparse PDF" }));
    expect(props.onReparse).toHaveBeenCalledTimes(1);
  });

  it("disables reparse and relabels it while parsing", () => {
    renderStep({
      statementTxns: TXNS,
      pdfFile: new File(["pdf"], "s.pdf", { type: "application/pdf" }),
      parsing: true,
    });
    expect(screen.getByRole("button", { name: /Reparsing/ })).toBeDisabled();
  });

  it("renders and updates the billing cycle selection", async () => {
    const user = userEvent.setup();
    const props = renderStep({
      selectedAccountHasBillingDay: 15,
      billingCycles: [
        {
          id: "bc1",
          accountId: "acct-1",
          startDate: "2024-03-01",
          endDate: "2024-03-31",
          label: "March",
          totalOutstanding: 0,
          transactionCount: 0,
        },
      ],
      importBillingCycleId: "",
    });
    expect(
      screen.getByText(/attach all imported transactions/i),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByRole("option", { name: /March/ }));
    expect(props.onImportBillingCycleChange).toHaveBeenCalledWith("bc1");
  });

  it("maps the billing cycle Auto option back to an empty id", async () => {
    const user = userEvent.setup();
    const props = renderStep({
      selectedAccountHasBillingDay: 15,
      billingCycles: [],
      importBillingCycleId: "bc1",
    });
    await user.click(screen.getByRole("combobox"));
    await user.click(
      await screen.findByRole("option", { name: /Auto \(by transaction date\)/ }),
    );
    expect(props.onImportBillingCycleChange).toHaveBeenCalledWith("");
  });
});
