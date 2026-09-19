import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import Transactions from "./Transactions";
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

const { apiMock, domainMock, setSettings, toastApiError } = vi.hoisted(() => ({
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

function defaultDomain(settings: Record<string, unknown> = {}) {
  return {
    accounts,
    categories,
    groups,
    payees,
    settings,
    setSettings,
  };
}

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/transactions"]}>
      <Transactions />
    </MemoryRouter>,
  );
}

function lastTransactionParams() {
  return apiMock.getTransactions.mock.calls.at(-1)?.[0] as Record<string, unknown>;
}

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
  apiMock.updateTransaction.mockResolvedValue({});
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
    expect(screen.getByText(/2 transactions across all accounts|2 transactions matching your filters/)).toBeInTheDocument();
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

    await waitFor(() =>
      expect(apiMock.updateTransaction).toHaveBeenCalledWith(
        "t1",
        expect.objectContaining({ categoryId: "c1" }),
      ),
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
