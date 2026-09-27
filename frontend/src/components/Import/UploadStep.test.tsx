import { createRef } from "react";
import { describe, it, expect, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
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

function fileInput(container: HTMLElement): HTMLInputElement {
  const input = container.querySelector<HTMLInputElement>('input[type="file"]');
  if (!input) throw new Error("file input not found");
  Object.defineProperty(input, "files", {
    configurable: true,
    writable: true,
    value: null,
  });
  return input;
}

function renderStep(overrides: Partial<Parameters<typeof UploadStep>[0]> = {}) {
  const props = {
    statementMode: "csv",
    onStatementModeChange: vi.fn(),
    parsing: false,
    fileInputRef: createRef<HTMLInputElement>(),
    pdfInputRef: createRef<HTMLInputElement>(),
    bankFileRef: createRef<HTMLInputElement>(),
    onCsvUpload: vi.fn(),
    onPdfUpload: vi.fn(),
    onBankFileUpload: vi.fn(),
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

  it("clears the CSV input so the same file can be re-selected after a failed parse", async () => {
    const user = userEvent.setup();
    const { container, props } = renderStep();
    const input = container.querySelector<HTMLInputElement>('input[type="file"]');
    if (!input) throw new Error("CSV file input not found");
    const file = new File(["a,b"], "x.csv", { type: "text/csv" });

    await user.upload(input, file);

    // A file input keeps its selection, so a retry with the same file fires no
    // change event at all (user-event honours that, like a browser does) and
    // the upload handler is never reached.
    expect(input.value).toBe("");
    await user.upload(input, file);
    expect(props.onCsvUpload).toHaveBeenCalledTimes(2);
  });

  it("clears the PDF input so the same statement can be re-selected after a failed parse", async () => {
    const user = userEvent.setup();
    const { container, props } = renderStep({ statementMode: "pdf" });
    const input = container.querySelector<HTMLInputElement>('input[type="file"]');
    if (!input) throw new Error("PDF file input not found");
    const file = new File(["pdf"], "s.pdf", { type: "application/pdf" });

    await user.upload(input, file);

    expect(input.value).toBe("");
    await user.upload(input, file);
    expect(props.onPdfUpload).toHaveBeenCalledTimes(2);
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

  it("opens the CSV picker on click and keyboard activation", async () => {
    const user = userEvent.setup();
    const { container } = renderStep();
    const input = fileInput(container);
    const clickSpy = vi.spyOn(input, "click").mockImplementation(() => {});

    const dropzone = screen.getByRole("button", { name: "Upload CSV file" });
    await user.click(dropzone);
    fireEvent.keyDown(dropzone, { key: "Enter" });
    fireEvent.keyDown(dropzone, { key: " " });
    fireEvent.keyDown(dropzone, { key: "Tab" });
    expect(clickSpy).toHaveBeenCalledTimes(3);
  });

  it("toggles the drag styling on the CSV dropzone", () => {
    renderStep();
    const dropzone = screen.getByRole("button", { name: "Upload CSV file" });
    fireEvent.dragOver(dropzone);
    expect(dropzone.classList.contains("border-primary")).toBe(true);
    fireEvent.dragLeave(dropzone);
    expect(dropzone.classList.contains("border-primary")).toBe(false);
  });

  it("forwards a dropped CSV file to onCsvUpload", () => {
    const { container, props } = renderStep();
    fileInput(container);
    const file = new File(["a,b"], "x.csv", { type: "text/csv" });

    fireEvent.drop(screen.getByRole("button", { name: "Upload CSV file" }), {
      dataTransfer: { files: [file] },
    });

    expect(props.onCsvUpload).toHaveBeenCalledTimes(1);
    expect(props.onCsvUpload).toHaveBeenCalledWith({
      target: { files: [file] },
    });
  });

  it("ignores a drop that carries no file", () => {
    const { props } = renderStep();
    fireEvent.drop(screen.getByRole("button", { name: "Upload CSV file" }), {
      dataTransfer: { files: [] },
    });
    expect(props.onCsvUpload).not.toHaveBeenCalled();
  });

  it("opens the PDF picker on click", async () => {
    const user = userEvent.setup();
    const { container } = renderStep({ statementMode: "pdf" });
    const input = fileInput(container);
    const clickSpy = vi.spyOn(input, "click").mockImplementation(() => {});

    await user.click(
      screen.getByRole("button", { name: "Upload statement PDF" }),
    );
    expect(clickSpy).toHaveBeenCalledTimes(1);
  });

  it("forwards a dropped PDF file to onPdfUpload", () => {
    const { container, props } = renderStep({ statementMode: "pdf" });
    fileInput(container);
    const file = new File(["pdf"], "s.pdf", { type: "application/pdf" });

    fireEvent.drop(
      screen.getByRole("button", { name: "Upload statement PDF" }),
      { dataTransfer: { files: [file] } },
    );

    expect(props.onPdfUpload).toHaveBeenCalledTimes(1);
    expect(props.onPdfUpload).toHaveBeenCalledWith({
      target: { files: [file] },
    });
  });

  it("edits the extractor and PDF password", async () => {
    const user = userEvent.setup();
    const { props } = renderStep({
      statementMode: "pdf",
      extractor: "sbi_cc",
      extractors: [
        { name: "sbi_cc", display_name: "SBI Credit Card" },
        { name: "hdfc", display_name: "HDFC Bank" },
      ],
    });

    await user.type(screen.getByPlaceholderText("Optional"), "x");
    expect(props.onPdfPasswordChange).toHaveBeenCalledWith("x");

    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByRole("option", { name: "HDFC Bank" }));
    expect(props.onExtractorChange).toHaveBeenCalledWith("hdfc");
  });

  it("falls back to the built-in extractor when none are supplied", async () => {
    const user = userEvent.setup();
    renderStep({ statementMode: "pdf", extractors: [] });
    await user.click(screen.getByRole("combobox"));
    expect(
      await screen.findByRole("option", { name: "SBI Credit Card" }),
    ).toBeInTheDocument();
  });

  // ---- Bank file (ISO 20022 / OFX) source ----

  it("offers a bank file source alongside CSV and statement PDF", () => {
    renderStep();
    expect(screen.getByRole("button", { name: /bank file/i })).toBeInTheDocument();
  });

  it("switches to the bank file source on request", async () => {
    const user = userEvent.setup();
    const { props } = renderStep();
    await user.click(screen.getByRole("button", { name: /bank file/i }));
    expect(props.onStatementModeChange).toHaveBeenCalledWith("bank");
  });

  it("shows the bank file dropzone and names the formats it reads", () => {
    renderStep({ statementMode: "bank" });
    expect(screen.getByText("Drop your bank file here")).toBeInTheDocument();
    expect(screen.getByText(/ISO 20022|OFX/)).toBeInTheDocument();
  });

  it("hides the PDF-only controls in bank file mode", () => {
    // An extractor and a PDF password mean nothing for an XML or SGML file, and
    // offering them would imply they do something.
    renderStep({ statementMode: "bank" });
    expect(screen.queryByText("Extractor")).not.toBeInTheDocument();
    expect(
      screen.queryByText("Password (if the PDF is protected)"),
    ).not.toBeInTheDocument();
  });

  it("forwards a dropped bank file to onBankFileUpload", () => {
    const { props } = renderStep({ statementMode: "bank" });
    const file = new File(["<OFX>"], "statement.xml", { type: "text/xml" });
    fireEvent.drop(screen.getByRole("button", { name: "Upload bank file" }), {
      dataTransfer: { files: [file] },
    });
    expect(props.onBankFileUpload).toHaveBeenCalledWith({
      target: { files: [file] },
    });
  });

  it("clears the bank file input so the same file can be re-selected after a failed read", async () => {
    // A retained value makes a retry with the same file fire no change event,
    // so the retry would silently do nothing. The CSV and PDF inputs already
    // clear for exactly this reason.
    const user = userEvent.setup();
    const { container, props } = renderStep({ statementMode: "bank" });
    const input = container.querySelector<HTMLInputElement>('input[type="file"]');
    if (!input) throw new Error("bank file input not found");
    const file = new File(["<OFX>"], "statement.xml", { type: "text/xml" });

    await user.upload(input, file);
    expect(input.value).toBe("");
    await user.upload(input, file);
    expect(props.onBankFileUpload).toHaveBeenCalledTimes(2);
  });

  it("shows the reading state in bank file mode", () => {
    renderStep({ statementMode: "bank", parsing: true });
    expect(screen.getByText("Reading bank file...")).toBeInTheDocument();
  });
});
