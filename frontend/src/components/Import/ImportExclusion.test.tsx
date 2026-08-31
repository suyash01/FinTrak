import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import Import from "./Import";

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

// Mock the API client so the component runs fully client-side.
const apiMocks = vi.hoisted(() => ({
  getAccounts: vi.fn(),
  getAccountTypes: vi.fn(),
  getPayees: vi.fn(),
  getStatementExtractors: vi.fn(),
  getTransactions: vi.fn(),
  getBillingCycles: vi.fn(),
  validateTransactions: vi.fn(),
  importTransactions: vi.fn(),
}));

vi.mock("../../api/client", () => ({
  default: {
    getAccounts: apiMocks.getAccounts,
    getAccountTypes: apiMocks.getAccountTypes,
    getPayees: apiMocks.getPayees,
    getStatementExtractors: apiMocks.getStatementExtractors,
    getTransactions: apiMocks.getTransactions,
    getBillingCycles: apiMocks.getBillingCycles,
    validateTransactions: apiMocks.validateTransactions,
    importTransactions: apiMocks.importTransactions,
  },
  downloadCSV: vi.fn(),
}));

const ACCOUNT = {
  id: "acct-1",
  name: "Excl Test Bank",
  accountTypeId: "bank",
  bank: "TestBank",
  currency: "INR",
  color: "#06b6d4",
  isDefault: false,
  closed: false,
  balance: 0,
  billingDay: null,
};

const CSV_CONTENT = `Date,Narration,Amount
15/03/2024,Coffee Shop,250
16/03/2024,Groceries,1200
17/03/2024,Skipped Row Test,999
18/03/2024,Salary Credit,50000
`;

async function runToPreview(
  user: ReturnType<typeof userEvent.setup>,
  csvContent: string = CSV_CONTENT,
) {
  // Step 1: select the account (wait for the account dropdown, then open it)
  const trigger = await waitFor(() => {
    const el = document.querySelector<HTMLElement>('[role="combobox"]');
    if (!el) throw new Error("account select trigger not found");
    return el;
  });
  await user.click(trigger);
  await user.click(await screen.findByText("Excl Test Bank"));
  await user.click(screen.getByRole("button", { name: /continue/i }));

  // Step 2: upload the CSV
  const input = document.querySelector<HTMLInputElement>(
    'input[type="file"]',
  );
  if (!input) throw new Error("CSV file input not found");
  await user.upload(
    input,
    new File([csvContent], "test.csv", { type: "text/csv" }),
  );

  // Step 3: auto-detected mapping -> preview
  await user.click(
    await screen.findByRole("button", { name: /preview transactions/i }),
  );
}

describe("Import exclusion", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.getAccounts.mockResolvedValue([ACCOUNT]);
    apiMocks.getAccountTypes.mockResolvedValue([
      { id: "bank", name: "Bank Account", positiveTxnType: "credit" },
    ]);
    apiMocks.getPayees.mockResolvedValue([]);
    apiMocks.getStatementExtractors.mockResolvedValue({ extractors: [] });
    apiMocks.getTransactions.mockResolvedValue({ data: [], pages: 1 });
    apiMocks.importTransactions.mockResolvedValue({
      imported: 0,
      total: 0,
      duplicates: 0,
    });
  });

  it("drops an unchecked row from the import payload", async () => {
    const user = userEvent.setup();
    render(<Import />);

    await runToPreview(user);

    // Four parsed transactions are listed.
    await waitFor(() => {
      expect(screen.getByText("Skipped Row Test")).toBeTruthy();
    });
    expect(screen.getByText(/Preview — 4 transactions/)).toBeTruthy();

    // Uncheck the "Skipped Row Test" row via its checkbox (aria-label is the
    // transaction description).
    const rowCheckbox = screen.getByLabelText("Skipped Row Test");
    await user.click(rowCheckbox);
    expect(await screen.findByText(/1 transaction excluded from import/)).toBeTruthy();

    // The import button reflects the included count.
    const importButton = screen.getByRole("button", {
      name: "Import 3 Transactions",
    });
    expect(importButton).toBeTruthy();

    await user.click(importButton);

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
    expect(descriptions).toContain("Coffee Shop");
    expect(descriptions).toContain("Salary Credit");
  });

  it("keeps all rows when nothing is unchecked", async () => {
    const user = userEvent.setup();
    render(<Import />);

    await runToPreview(user);
    await waitFor(() => {
      expect(screen.getByText("Skipped Row Test")).toBeTruthy();
    });

    await user.click(
      screen.getByRole("button", { name: "Import 4 Transactions" }),
    );

    await waitFor(() => {
      expect(apiMocks.importTransactions).toHaveBeenCalledTimes(1);
    });
    const payload = apiMocks.importTransactions.mock.calls[0][0];
    expect(payload.transactions).toHaveLength(4);
  });

  it("excludes every identical twin when one occurrence is unchecked", async () => {
    const user = userEvent.setup();
    render(<Import />);

    // Same file but the "Skipped Row Test" line appears twice (identical
    // date, amount, type, description).
    const dupCsv = `Date,Narration,Amount
15/03/2024,Coffee Shop,250
17/03/2024,Skipped Row Test,999
17/03/2024,Skipped Row Test,999
18/03/2024,Salary Credit,50000
`;
    await runToPreview(user, dupCsv);

    await waitFor(() => {
      expect(screen.getByText(/Preview — 4 transactions/)).toBeTruthy();
    });

    // Two rows share the "Skipped Row Test" label; uncheck only the first.
    const [twinA] = screen.getAllByLabelText("Skipped Row Test");
    await user.click(twinA);

    // Both twins are excluded (counts jump to 2), so the import button shows 2.
    expect(
      await screen.findByText(/2 transactions excluded from import/),
    ).toBeTruthy();
    const twinStates = screen
      .getAllByLabelText("Skipped Row Test")
      .map((c) => c.getAttribute("data-state"));
    expect(twinStates).toEqual(["unchecked", "unchecked"]);

    await user.click(
      screen.getByRole("button", { name: "Import 2 Transactions" }),
    );

    await waitFor(() => {
      expect(apiMocks.importTransactions).toHaveBeenCalledTimes(1);
    });
    const payload = apiMocks.importTransactions.mock.calls[0][0];
    expect(payload.transactions).toHaveLength(2);
    const descriptions = payload.transactions.map(
      (t: { description: string }) => t.description,
    );
    expect(descriptions).not.toContain("Skipped Row Test");
  });
});