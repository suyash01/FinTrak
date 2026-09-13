import { createRef } from "react";
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import UploadStep from "./UploadStep";

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

function renderStep(overrides: Partial<Parameters<typeof UploadStep>[0]> = {}) {
  const props = {
    statementMode: "csv",
    onStatementModeChange: vi.fn(),
    parsing: false,
    fileInputRef: createRef<HTMLInputElement>(),
    pdfInputRef: createRef<HTMLInputElement>(),
    onCsvUpload: vi.fn(),
    onPdfUpload: vi.fn(),
    extractor: "sbi_cc",
    onExtractorChange: vi.fn(),
    extractors: [],
    pdfPassword: "",
    onPdfPasswordChange: vi.fn(),
    ...overrides,
  };
  const view = render(<UploadStep {...props} />);
  return { ...view, props };
}

describe("UploadStep", () => {
  it("defaults to CSV and switches source on request", async () => {
    const user = userEvent.setup();
    const { props } = renderStep();
    expect(screen.getByText("Drop your CSV file here")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /statement pdf/i }));
    expect(props.onStatementModeChange).toHaveBeenCalledWith("pdf");
  });

  it("invokes onCsvUpload when a CSV file is selected", async () => {
    const user = userEvent.setup();
    const { container, props } = renderStep();
    const input = container.querySelector<HTMLInputElement>('input[type="file"]');
    if (!input) throw new Error("CSV file input not found");

    await user.upload(input, new File(["a,b"], "x.csv", { type: "text/csv" }));
    expect(props.onCsvUpload).toHaveBeenCalledTimes(1);
  });

  it("shows the parser controls in PDF mode", () => {
    renderStep({ statementMode: "pdf" });
    expect(
      screen.getByText("Drop your statement PDF here"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Password (if the PDF is protected)"),
    ).toBeInTheDocument();
    expect(screen.getByText("Extractor")).toBeInTheDocument();
  });

  it("shows the parsing state in PDF mode", () => {
    renderStep({ statementMode: "pdf", parsing: true });
    expect(screen.getByText("Parsing statement...")).toBeInTheDocument();
  });
});
