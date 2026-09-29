import { describe, it, expect, vi, beforeEach } from "vitest";
import {
  render,
  screen,
  waitFor,
  fireEvent,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import Transactions from "./Transactions";
import { formatOne } from "../../lib/currency";
import type {
  Account,
  Category,
  CategoryGroup,
  Payee,
  Transaction,
} from "../../types";

if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

const { apiMock, domainMock, setSettings, toastApiError, refreshAccounts } =
  vi.hoisted(() => ({
    apiMock: {
      getTransactions: vi.fn(),
      updateTransaction: vi.fn(),
      bulkCategorize: vi.fn(),
      bulkUpdatePayee: vi.fn(),
      bulkUpdateBillingCycle: vi.fn(),
      bulkLoan: vi.fn(),
      bulkDeleteTransactions: vi.fn(),
      deleteTransaction: vi.fn(),
      getBillingCycles: vi.fn(),
      updateUserSettings: vi.fn(),
      getRecurringSeries: vi.fn(),
      attachRecurring: vi.fn(),
      detachRecurring: vi.fn(),
      getTags: vi.fn(),
      bulkUpdateTags: vi.fn(),
      exportTransactions: vi.fn(),
    },
    domainMock: { useDomainData: vi.fn() },
    setSettings: vi.fn(),
    // The page reloads the shared account list after every successful
    // transaction write so balances stay fresh.
    refreshAccounts: vi.fn(),
    toastApiError: vi.fn(),
  }));

vi.mock("../../api/client", () => ({
  default: apiMock,
  getStoredUser: () => null,
  storeUser: vi.fn(),
}));
vi.mock("../../lib/errors", () => ({ toastApiError }));
vi.mock("../../context/DomainDataContext", () => ({
  useDomainData: domainMock.useDomainData,
}));
vi.mock("../../context/SettingsContext", () => ({
  useSettings: () => ({ compactLayout: false }),
}));
// The list reloads when the offline outbox drains; a page test only needs the
// trigger value, not the provider.
vi.mock("../../context/OfflineContext", () => ({
  useOffline: () => ({ syncedAt: 0 }),
}));

const accounts: Account[] = [
  {
    id: "a1",
    name: "Checking",
    accountTypeId: "bank",
    bank: "",
    currency: "INR",
    color: "#000000",
    isDefault: true,
    closed: false,
    balance: 0,
    billingDay: null,
  },
  {
    id: "loan1",
    name: "Car Loan",
    accountTypeId: "loan",
    bank: "",
    currency: "INR",
    color: "#000000",
    isDefault: false,
    closed: false,
    balance: 0,
    billingDay: null,
  },
  {
    // A second currency, so the amount column has to resolve the row's account
    // rather than fall back to a default. Every other account here is INR, so
    // without this the column and the default agree on every row and a correct
    // label is indistinguishable from the default it happens to match.
    id: "usd1",
    name: "Dollars",
    accountTypeId: "bank",
    bank: "",
    currency: "USD",
    color: "#000000",
    isDefault: false,
    closed: false,
    balance: 0,
    billingDay: null,
  },
];

const groups = [
  { id: "g1", name: "Food", icon: "", color: "", isBase: false, isGlobal: false, sortOrder: 0 },
] as unknown as CategoryGroup[];

const categories = [
  { id: "c1", name: "Groceries", icon: "", color: "#ff0000", groupId: "g1" },
  { id: "c2", name: "Fast Food", icon: "", color: "#00ff00", groupId: "g1" },
] as unknown as Category[];

const payees = [{ id: "p1", name: "Swiggy" }] as unknown as Payee[];

const txns = [
  {
    id: "t1",
    accountId: "a1",
    date: "2024-03-15",
    description: "Coffee Shop",
    amount: 250,
    type: "debit",
    categoryId: null,
    tags: [],
    notes: "",
    payeeId: null,
    accountName: "Checking",
    isSummary: false,
    isLinked: false,
  },
  {
    id: "t2",
    accountId: "a1",
    date: "2024-03-16",
    description: "Grocery Store",
    amount: 1200,
    type: "debit",
    categoryId: null,
    tags: [],
    notes: "",
    payeeId: null,
    accountName: "Checking",
    isSummary: false,
    isLinked: false,
  },
] as unknown as Transaction[];

// A transaction in the USD account, so the amount column has a row whose correct
// currency differs from the one the column used to assume.
const usdTxn = {
  id: "t-usd",
  accountId: "usd1",
  date: "2024-03-17",
  description: "Dollar Store",
  amount: 42,
  type: "debit" as const,
  categoryId: null,
  tags: [],
  notes: "",
  payeeId: null,
  accountName: "Dollars",
  isSummary: false,
  isLinked: false,
} as unknown as Transaction;

function defaultDomain(settings: Record<string, unknown> = {}) {
  return {
    accounts,
    categories,
    groups,
    payees,
    settings,
    setSettings,
    refreshAccounts,
  };
}

function renderPage(entry = "/transactions") {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <Transactions />
    </MemoryRouter>,
  );
}

function lastTransactionParams() {
  return apiMock.getTransactions.mock.calls.at(-1)?.[0] as Record<string, unknown>;
}

// The defect, in one test. The amount column called formatCurrency without the
// currency, so it took the INR default for every row: a USD transaction in the
// table wore a rupee symbol. The assertion is on the code as well as the absence
// of the wrong one, because a fix that merely dropped the symbol would pass a
// narrower test while still mislabelling the figure.
describe("the amount column's currency", () => {
  it("labels each row in its own account's currency", async () => {
    apiMock.getTransactions.mockResolvedValue({
      data: [txns[0], usdTxn],
      total: 2,
      page: 1,
      pages: 1,
    });
    renderPage();

    const dollarRow = (await screen.findByText("Dollar Store")).closest("tr")!;
    expect(dollarRow.textContent).toContain(formatOne(42, "USD"));

    const rupeeRow = screen.getByText("Coffee Shop").closest("tr")!;
    expect(rupeeRow.textContent).toContain(formatOne(250, "INR"));

    // And the wrong currency is named rather than left to a reader: the INR
    // default on the dollar row.
    expect(dollarRow.textContent).not.toContain(formatOne(42, "INR"));
    expect(dollarRow.textContent).not.toContain("₹");
  });
});

beforeEach(() => {
  localStorage.clear();
  vi.clearAllMocks();
  apiMock.getTransactions.mockResolvedValue({
    data: txns,
    total: 2,
    page: 1,
    pages: 1,
  });
  apiMock.getBillingCycles.mockResolvedValue({ data: [] });
  apiMock.getRecurringSeries.mockResolvedValue({ data: [] });
  apiMock.getTags.mockResolvedValue({ data: [] });
  apiMock.updateTransaction.mockResolvedValue({ id: "t1", queued: false });
  apiMock.bulkCategorize.mockResolvedValue({});
  apiMock.updateUserSettings.mockResolvedValue({});
  domainMock.useDomainData.mockReturnValue(defaultDomain());
});

describe("Transactions", () => {
  it("loads transactions and pre-fills the default account filter", async () => {
    renderPage();

    expect(await screen.findByText("Coffee Shop")).toBeInTheDocument();
    await waitFor(() => expect(apiMock.getTransactions).toHaveBeenCalled());

    expect(lastTransactionParams()).toMatchObject({ accountId: "a1" });
    // Strict: an accountId IS a filter, so this must be the "matching your
    // filters" wording. An earlier version of this assertion accepted either
    // phrasing, which is what let a bug through - see the isFiltered test below.
    expect(screen.getByText(/2 transactions matching your filters/)).toBeInTheDocument();
  });

  it("sends a lone loan account as loanAccountId", async () => {
    renderPage("/transactions?accountId=loan1");
    await waitFor(() => expect(apiMock.getTransactions).toHaveBeenCalled());

    const params = lastTransactionParams();
    expect(params.loanAccountId).toBe("loan1");
    expect(params.accountId).toBeUndefined();
  });

  it("splits a mixed account selection across accountId and loanAccountId", async () => {
    renderPage("/transactions?accountId=a1,loan1");
    await waitFor(() => expect(apiMock.getTransactions).toHaveBeenCalled());

    expect(lastTransactionParams()).toMatchObject({
      accountId: "a1",
      loanAccountId: "loan1",
    });
  });

  it("uses 50 rows per page by default", async () => {
    renderPage();
    await waitFor(() => expect(apiMock.getTransactions).toHaveBeenCalled());
    await waitFor(() => expect(lastTransactionParams().limit).toBe(50));
  });

  it("honors a page size persisted in localStorage", async () => {
    localStorage.setItem("txPageSize", "25");
    renderPage();
    await waitFor(() => expect(lastTransactionParams().limit).toBe(25));
  });

  it("restores the page size from shared user settings", async () => {
    domainMock.useDomainData.mockReturnValue(defaultDomain({ pageSize: 100 }));
    renderPage();
    await waitFor(() => expect(lastTransactionParams().limit).toBe(100));
  });

  it("persists a page-size change locally and to the server", async () => {
    const user = userEvent.setup();
    renderPage();
    await waitFor(() => expect(apiMock.getTransactions).toHaveBeenCalled());

    const triggers = Array.from(
      document.querySelectorAll<HTMLElement>('[role="combobox"]'),
    );
    const pageSizeTrigger = triggers.find((t) => t.textContent?.trim() === "50");
    expect(pageSizeTrigger).toBeTruthy();
    await user.click(pageSizeTrigger!);
    await user.click(await screen.findByText("25"));

    await waitFor(() =>
      expect(apiMock.updateUserSettings).toHaveBeenCalledWith({ pageSize: 25 }),
    );
    expect(localStorage.getItem("txPageSize")).toBe("25");
    await waitFor(() => expect(lastTransactionParams().limit).toBe(25));
  });

  it("shows the empty state when there are no transactions", async () => {
    apiMock.getTransactions.mockResolvedValue({
      data: [],
      total: 0,
      page: 1,
      pages: 0,
    });
    renderPage();
    expect(await screen.findByText("No transactions found")).toBeInTheDocument();
  });

  it("sends the new sort parameters when a column header is toggled", async () => {
    renderPage();
    await screen.findByText("Coffee Shop");

    fireEvent.click(screen.getByRole("button", { name: "Amount" }));

    await waitFor(() =>
      expect(lastTransactionParams()).toMatchObject({
        sortBy: "amount",
        sortOrder: "DESC",
        page: 1,
      }),
    );
  });

  it("updates a transaction category from the inline cell picker", async () => {
    renderPage();
    await screen.findByText("Coffee Shop");

    // The category picker is the native select in the row whose placeholder is
    // "Uncategorized" (payee uses "No Payee").
    const categorySelect = Array.from(
      document.querySelectorAll<HTMLSelectElement>("td select"),
    ).find((s) => s.options[0]?.textContent === "Uncategorized");
    expect(categorySelect).toBeTruthy();

    fireEvent.change(categorySelect!, { target: { value: "c1" } });

    // Only the edited field is PATCHed, and it is PATCHed against the row the
    // cell was rendered with. The body used to be a snapshot of the whole row
    // (category, payee, tags, notes), which overwrote a concurrent inline edit
    // with the values the row had been rendered with; the base is what lets
    // updateTransaction reduce the payload to the one field, and what it has to
    // be made against if the request never reaches the server.
    await waitFor(() =>
      expect(apiMock.updateTransaction).toHaveBeenCalledWith(
        "t1",
        { categoryId: "c1" },
        expect.objectContaining({ base: expect.objectContaining({ id: "t1" }) }),
      ),
    );
  });

  it("sends only the payee on an inline payee edit", async () => {
    renderPage();
    await screen.findByText("Coffee Shop");

    const payeeSelect = Array.from(
      document.querySelectorAll<HTMLSelectElement>("td select"),
    ).find((s) => s.options[0]?.textContent === "No Payee");
    expect(payeeSelect).toBeTruthy();

    fireEvent.change(payeeSelect!, { target: { value: "p1" } });

    // The same rule, uniformly: the field the user changed, made against the row
    // it was rendered with. See the category edit above.
    await waitFor(() =>
      expect(apiMock.updateTransaction).toHaveBeenCalledWith(
        "t1",
        { payeeId: "p1" },
        expect.objectContaining({ base: expect.objectContaining({ id: "t1" }) }),
      ),
    );
  });

  it("refreshes the shared accounts after an inline edit", async () => {
    renderPage();
    await screen.findByText("Coffee Shop");

    const categorySelect = Array.from(
      document.querySelectorAll<HTMLSelectElement>("td select"),
    ).find((s) => s.options[0]?.textContent === "Uncategorized")!;

    fireEvent.change(categorySelect, { target: { value: "c1" } });

    await waitFor(() => expect(refreshAccounts).toHaveBeenCalled());
  });

  it("refreshes the shared accounts after a delete", async () => {
    renderPage();
    await screen.findByText("Coffee Shop");

    fireEvent.click(screen.getByRole("button", { name: "Delete Coffee Shop" }));
    await userEvent.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", {
        name: "Delete",
      }),
    );

    await waitFor(() =>
      expect(apiMock.deleteTransaction).toHaveBeenCalledWith("t1"),
    );
    await waitFor(() => expect(refreshAccounts).toHaveBeenCalled());
  });

  it("drops a deleted transaction from the selection", async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByText("Coffee Shop");

    await user.click(
      screen.getByRole("checkbox", { name: "Select Coffee Shop" }),
    );
    expect(await screen.findByText("1 selected")).toBeInTheDocument();

    // Deleting the row through its own trash button must retire its id too,
    // otherwise the bulk bar keeps advertising a transaction that is gone.
    fireEvent.click(screen.getByRole("button", { name: "Delete Coffee Shop" }));
    await user.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", {
        name: "Delete",
      }),
    );

    await waitFor(() =>
      expect(apiMock.deleteTransaction).toHaveBeenCalledWith("t1"),
    );
    await waitFor(() =>
      expect(screen.queryByText("1 selected")).not.toBeInTheDocument(),
    );
  });

  it("bulk-categorizes every selected transaction", async () => {
    const user = userEvent.setup();
    const { container } = renderPage();
    await screen.findByText("Coffee Shop");

    // First checkbox is the header select-all; the next two are the rows.
    const checkboxes = screen.getAllByRole("checkbox");
    await user.click(checkboxes[1]);

    expect(await screen.findByText("1 selected")).toBeInTheDocument();

    // BulkActionBar's categorize picker is the first native select in the DOM.
    const categorize = container.querySelector<HTMLSelectElement>("select");
    expect(categorize).toBeTruthy();
    fireEvent.change(categorize!, { target: { value: "c1" } });

    await waitFor(() =>
      expect(apiMock.bulkCategorize).toHaveBeenCalledWith({
        transactionIds: ["t1"],
        categoryId: "c1",
      }),
    );
    await waitFor(() => expect(refreshAccounts).toHaveBeenCalled());
  });

  it("keeps the newest inline edit when responses resolve out of order", async () => {
    const resolvers: Array<() => void> = [];
    apiMock.updateTransaction.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          resolvers.push(resolve);
        }),
    );
    renderPage();
    await screen.findByText("Coffee Shop");

    const categorySelect = Array.from(
      document.querySelectorAll<HTMLSelectElement>("td select"),
    ).find((s) => s.options[0]?.textContent === "Uncategorized")!;

    fireEvent.change(categorySelect, { target: { value: "c1" } });
    fireEvent.change(categorySelect, { target: { value: "c2" } });
    await waitFor(() => expect(apiMock.updateTransaction).toHaveBeenCalledTimes(2));

    // Resolve the newer request first, then let the stale one land. The stale
    // response must not clobber the newer category.
    resolvers[1]();
    await waitFor(() => expect(categorySelect.value).toBe("c2"));
    resolvers[0]();
    await new Promise((resolve) => setTimeout(resolve, 10));
    expect(categorySelect.value).toBe("c2");
  });
});

describe("Transactions recurring badge", () => {
  it("marks transactions linked to a subscription", async () => {
    apiMock.getTransactions.mockResolvedValue({
      data: [
        {
          id: "t1",
          accountId: "a1",
          date: "2024-03-15",
          description: "Netflix",
          amount: 15.99,
          type: "debit",
          categoryId: null,
          tags: [],
          notes: "",
          payeeId: null,
          accountName: "Checking",
          isSummary: false,
          isLinked: false,
          recurringSeriesId: "s1",
          recurringSeriesName: "Netflix subscription",
        },
      ],
      total: 1,
      page: 1,
      pages: 1,
    });
    renderPage();

    expect(
      await screen.findByTitle("Linked to Netflix subscription"),
    ).toBeInTheDocument();
  });
});

describe("the query language on the page", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMock.getTransactions.mockResolvedValue({ data: [], total: 0, page: 1, pages: 1 });
    apiMock.getTags.mockResolvedValue({ data: [] });
  });

  // The CSV must not drift from the table. handleExport copies the filter set, so
  // the query travels with it; this pins that it does.
  // The CSV must not drift from the table. Both take the RESOLVED query, not the
  // text the user typed: a name sent raw would be dropped by the server, and the
  // export would quietly contain more rows than the table shows.
  it("sends the resolved query to both the list and the export", async () => {
    renderPage("/transactions?q=cat%3AFood%2FGroceries+amt%3E50");
    await waitFor(() => expect(apiMock.getTransactions).toHaveBeenCalled());
    expect(apiMock.getTransactions.mock.calls[0][0]).toMatchObject({
      q: "cat:c1 amt>50",
    });

    await userEvent.click(await screen.findByRole("button", { name: /export/i }));
    await waitFor(() => expect(apiMock.exportTransactions).toHaveBeenCalled());
    expect(apiMock.exportTransactions.mock.calls[0][0]).toMatchObject({
      q: "cat:c1 amt>50",
    });
  });

  it("shows the typed text in the box, not the resolved ids", async () => {
    renderPage("/transactions?q=cat%3AFood%2FGroceries");
    expect(await screen.findByRole("combobox", { name: /transaction query/i })).toHaveValue(
      "cat:Food/Groceries",
    );
  });

  it("surfaces the diagnostics the server returned with the rows", async () => {
    apiMock.getTransactions.mockResolvedValue({
      data: [],
      total: 0,
      page: 1,
      pages: 1,
      queryDiagnostics: [
        { term: "payee:bogus", code: "unresolved_value", message: "no payee named bogus", position: 0 },
      ],
    });
    renderPage("/transactions?q=payee%3Abogus");
    expect(await screen.findByText(/term was ignored/)).toBeInTheDocument();
    expect(screen.getByText("payee:bogus")).toBeInTheDocument();
  });
});

describe("the header's filtered wording", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMock.getTransactions.mockResolvedValue({ data: [], total: 0, page: 1, pages: 1 });
    apiMock.getTags.mockResolvedValue({ data: [] });
  });

  // isFiltered used to test `filters.search`, which no longer exists after the
  // query language replaced the free-text parameter. `undefined !== ""` is true,
  // so the header claimed "matching your filters" no matter what was set.
  //
  // The default account pre-fill is itself a filter, so this isolates isFiltered
  // by removing the default account: with no filter at all the header must say
  // "across all accounts".
  it("says across all accounts when nothing is filtered", async () => {
    domainMock.useDomainData.mockReturnValue({
      ...defaultDomain(),
      accounts: accounts.map((a) => ({ ...a, isDefault: false })),
    });
    renderPage("/transactions");
    await waitFor(() => expect(apiMock.getTransactions).toHaveBeenCalled());
    expect(lastTransactionParams().accountId).toBeUndefined();
    expect(screen.getByText(/across all accounts/)).toBeInTheDocument();
  });

  it("counts a query as a filter", async () => {
    domainMock.useDomainData.mockReturnValue({
      ...defaultDomain(),
      accounts: accounts.map((a) => ({ ...a, isDefault: false })),
    });
    renderPage("/transactions?q=coffee");
    expect(await screen.findByText(/matching your filters/)).toBeInTheDocument();
  });
});
