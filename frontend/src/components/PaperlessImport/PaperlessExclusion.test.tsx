import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import PaperlessImport from "./PaperlessImport";
import { DomainDataProvider } from "../../context/DomainDataContext";

if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

const apiMocks = vi.hoisted(() => ({
  getPaperlessSettings: vi.fn(),
  getAccounts: vi.fn(),
  getAccountTypes: vi.fn(),
  getCategories: vi.fn(),
  getGroups: vi.fn(),
  getPayees: vi.fn(),
  getStatementExtractors: vi.fn(),
  getPaperlessDocuments: vi.fn(),
  importPaperlessDocument: vi.fn(),
  validateTransactions: vi.fn(),
  importTransactions: vi.fn(),
}));

vi.mock("../../api/client", () => ({
  default: {
    getPaperlessSettings: apiMocks.getPaperlessSettings,
    getAccounts: apiMocks.getAccounts,
    getAccountTypes: apiMocks.getAccountTypes,
    getCategories: apiMocks.getCategories,
    getGroups: apiMocks.getGroups,
    getPayees: apiMocks.getPayees,
    getStatementExtractors: apiMocks.getStatementExtractors,
    getPaperlessDocuments: apiMocks.getPaperlessDocuments,
    importPaperlessDocument: apiMocks.importPaperlessDocument,
    validateTransactions: apiMocks.validateTransactions,
    importTransactions: apiMocks.importTransactions,
  },
  downloadCSV: vi.fn(),
}));

const ACCOUNT = {
  id: "acct-1",
  name: "Excl Test CC",
  accountTypeId: "credit_card",
  bank: "TestBank",
  currency: "INR",
  color: "#06b6d4",
  isDefault: false,
  closed: false,
  balance: 0,
  billingDay: null,
};

const TXNS = [
  { date: "2024-03-15", description: "Coffee Shop", amount: 250, type: "debit" },
  { date: "2024-03-16", description: "Groceries", amount: 1200, type: "debit" },
  { date: "2024-03-17", description: "Skipped Row Test", amount: 999, type: "debit" },
  { date: "2024-03-18", description: "Payment Received", amount: 5000, type: "credit" },
];

describe("PaperlessImport exclusion", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.getPaperlessSettings.mockResolvedValue({
      paperlessUrl: "https://paperless.example",
      hasToken: true,
      paperlessTag: "fintrak",
    });
    apiMocks.getAccounts.mockResolvedValue([ACCOUNT]);
    apiMocks.getAccountTypes.mockResolvedValue([]);
    apiMocks.getCategories.mockResolvedValue([]);
    apiMocks.getGroups.mockResolvedValue([]);
    apiMocks.getPayees.mockResolvedValue([]);
    apiMocks.getStatementExtractors.mockResolvedValue({ extractors: [] });
    apiMocks.getPaperlessDocuments.mockResolvedValue({
      documents: [
        {
          id: 1,
          title: "Statement March",
          correspondent: "",
          documentType: "",
          created: "",
          tags: [],
        },
      ],
      page: 1,
      pageSize: 25,
      totalCount: 1,
      totalPages: 1,
      correspondents: [],
      documentTypes: [],
      tags: [],
    });
    apiMocks.importPaperlessDocument.mockResolvedValue({
      transactions: TXNS,
    });
    apiMocks.importTransactions.mockResolvedValue({
      imported: 0,
      total: 0,
      duplicates: 0,
    });
  });

  it("drops an unchecked row from the Paperless import payload", async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <DomainDataProvider>
          <PaperlessImport />
        </DomainDataProvider>
      </MemoryRouter>,
    );

    // Pick the account once the config + accounts load.
    const trigger = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[role="combobox"]');
      if (!el) throw new Error("account select trigger not found");
      return el;
    });
    await user.click(trigger);
    await user.click(await screen.findByText("Excl Test CC"));

    // Select the only document, then fetch & parse it.
    await user.click(await screen.findByText("Statement March"));
    await user.click(
      screen.getByRole("button", { name: /fetch & parse selected \(1\)/i }),
    );

    // Preview appears with 4 parsed transactions.
    await waitFor(() => {
      expect(screen.getByText("Skipped Row Test")).toBeTruthy();
    });
    expect(screen.getByText(/4 transaction\(s\) parsed\./)).toBeTruthy();

    // Uncheck the "Skipped Row Test" row.
    await user.click(screen.getByLabelText("Skipped Row Test"));
    expect(
      await screen.findByText(/3 selected for import — 1 excluded/i),
    ).toBeTruthy();

    // Confirm the import.
    await user.click(
      screen.getByRole("button", { name: "Import 3 transaction(s)" }),
    );

    await waitFor(() => {
      expect(apiMocks.importTransactions).toHaveBeenCalledTimes(1);
    });
    const payload = apiMocks.importTransactions.mock.calls[0][0];
    expect(payload.accountId).toBe("acct-1");
    expect(payload.transactions).toHaveLength(3);
    const descriptions = payload.transactions.map(
      (t: { description: string }) => t.description,
    );
    expect(descriptions).not.toContain("Skipped Row Test");
  });

  it("surfaces the parser's validation warnings for the parsed documents", async () => {
    const user = userEvent.setup();
    apiMocks.importPaperlessDocument.mockResolvedValue({
      transactions: TXNS,
      validationErrors: [
        "page 1: rebuilt total 1000.00 does not match the printed 1250.00",
      ],
    });
    render(
      <MemoryRouter>
        <DomainDataProvider>
          <PaperlessImport />
        </DomainDataProvider>
      </MemoryRouter>,
    );

    const trigger = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[role="combobox"]');
      if (!el) throw new Error("account select trigger not found");
      return el;
    });
    await user.click(trigger);
    await user.click(await screen.findByText("Excl Test CC"));

    await user.click(await screen.findByText("Statement March"));
    await user.click(
      screen.getByRole("button", { name: /fetch & parse selected \(1\)/i }),
    );

    // Each message is prefixed with the document it came from.
    expect(await screen.findByText(/1 parse warning/)).toBeTruthy();
    expect(
      screen.getByText(/Statement March: page 1: rebuilt total 1000\.00/),
    ).toBeTruthy();
  });

  it("renders Paperless rows as labelled list items with a sibling Preview button", async () => {
    render(
      <MemoryRouter>
        <DomainDataProvider>
          <PaperlessImport />
        </DomainDataProvider>
      </MemoryRouter>,
    );

    const list = await screen.findByRole("list", {
      name: "Paperless documents",
    });
    const item = within(list).getByRole("listitem");

    // A real checkbox with an accessible name, not a focusable row pretending
    // to be one.
    const checkbox = within(item).getByRole("checkbox", {
      name: "Select Statement March",
    });
    expect(item.getAttribute("role")).toBe("listitem");
    expect(item.getAttribute("tabindex")).toBeNull();

    // Preview is a sibling control, never nested inside the checkbox.
    const preview = within(item).getByRole("button", { name: /preview/i });
    expect(checkbox.contains(preview)).toBe(false);
  });
});