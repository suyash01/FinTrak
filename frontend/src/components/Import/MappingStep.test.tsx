import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import MappingStep from "./MappingStep";
import type { ColumnMapping } from "./importHelpers";

// jsdom lacks the Pointer Capture API that Radix (Select etc.) calls on
// pointer events; without these no-ops, pointer interactions crash.
if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

const EMPTY_MAPPING: ColumnMapping = {
  date: null,
  description: null,
  amount: null,
  debit: null,
  credit: null,
  payee: null,
};

const CSV_ROW = { Date: "15/03/2024", Narration: "Coffee Shop", Amount: "250" };
const HEADERS = ["Date", "Narration", "Amount"];

function renderStep(overrides: Partial<Parameters<typeof MappingStep>[0]> = {}) {
  const props = {
    csvData: [CSV_ROW],
    csvHeaders: HEADERS,
    columnMapping: EMPTY_MAPPING,
    amountMode: "single",
    dateFormat: "auto",
    onMappingChange: vi.fn(),
    onAmountModeChange: vi.fn(),
    onDateFormatChange: vi.fn(),
    onBack: vi.fn(),
    onNext: vi.fn(),
    ...overrides,
  };
  render(<MappingStep {...props} />);
  return props;
}

describe("MappingStep", () => {
  it("renders the field mapper, date format and amount-mode controls", () => {
    renderStep();
    expect(screen.getByText("Map CSV Columns")).toBeInTheDocument();
    expect(screen.getByText("Single Amount")).toBeInTheDocument();
    expect(screen.getByText("Debit / Credit")).toBeInTheDocument();
    expect(screen.getAllByText("Date").length).toBeGreaterThan(0);
  });

  it("lists mapping errors and disables next while required fields are unmapped", () => {
    renderStep();
    expect(
      screen.getByText("Date field must be mapped to a CSV column"),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /preview transactions/i }),
    ).toBeDisabled();
  });

  it("enables next and invokes onNext once the mapping is complete", async () => {
    const user = userEvent.setup();
    const props = renderStep({
      columnMapping: {
        ...EMPTY_MAPPING,
        date: "Date",
        description: "Narration",
        amount: "Amount",
      },
    });
    const next = screen.getByRole("button", { name: /preview transactions/i });
    expect(next).toBeEnabled();
    await user.click(next);
    expect(props.onNext).toHaveBeenCalledTimes(1);
  });

  it("invokes onBack from the Back button", async () => {
    const user = userEvent.setup();
    const props = renderStep();
    await user.click(screen.getByRole("button", { name: "Back" }));
    expect(props.onBack).toHaveBeenCalledTimes(1);
  });
});
