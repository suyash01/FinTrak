import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import PaperlessImport from "./PaperlessImport";

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
    apiMocks.getStatementExtractors.mockResolvedValue({ extractors: [] });
    apiMocks.getPaperlessDocuments.mockResolvedValue({
      documents: [
        {
          id: 1,
          title: "Statement March",
          correspondent: null,
          documentType: null,
          tags: [],
          created: null,
          thumbnail: null,
        },
      ],
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
        <PaperlessImport />
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
});