import { describe, it, expect, vi, beforeEach } from "vitest";
import type { ReactNode } from "react";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import Dashboard from "./Dashboard";
import { formatOne } from "../../lib/currency";
import { formatCurrency, formatNumber } from "../../utils/formatters";
import type {
  Account,
  CurrencyScope,
  DashboardSummary,
  RecurringSeries,
} from "../../types";

if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

vi.mock("recharts", () => ({
  ResponsiveContainer: ({ children }: { children: ReactNode }) => (
    <div>{children}</div>
  ),
  BarChart: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Bar: () => null,
  XAxis: () => null,
  YAxis: () => null,
  Tooltip: () => null,
  CartesianGrid: () => null,
  PieChart: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Pie: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Cell: () => null,
}));

const { apiMock, domainMock } = vi.hoisted(() => ({
  apiMock: { getDashboardSummary: vi.fn(), getRecurringSeries: vi.fn() },
  domainMock: { useDomainData: vi.fn() },
}));

vi.mock("../../api/client", () => ({ default: apiMock }));
vi.mock("../../context/DomainDataContext", () => ({
  useDomainData: domainMock.useDomainData,
}));
vi.mock("../../context/SettingsContext", () => ({
  useSettings: () => ({ compactLayout: false }),
}));
// The dashboard reloads when the offline outbox drains; a page test only needs
// the trigger value, not the provider.
vi.mock("../../context/OfflineContext", () => ({
  useOffline: () => ({ syncedAt: 0 }),
}));

function account(overrides: Partial<Account> = {}): Account {
  return {
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
    ...overrides,
  };
}

// oneCurrencyScope is the common case: a window that only ever touched rupees.
function oneCurrencyScope(): CurrencyScope {
  return {
    currencies: ["INR"],
    accounts: [
      {
        id: "a1",
        name: "Checking",
        currency: "INR",
        income: { INR: 5000 },
        expense: { INR: 2000 },
      },
    ],
  };
}

// twoCurrencyScope is the case this change exists for: a window over an INR and
// a USD account, where no single figure describes it.
function twoCurrencyScope(): CurrencyScope {
  return {
    currencies: ["INR", "USD"],
    accounts: [
      ...oneCurrencyScope().accounts,
      {
        id: "a2",
        name: "Dollars",
        currency: "USD",
        income: { USD: 120 },
        expense: { USD: 80 },
      },
    ],
  };
}

// spendOnlyScope is the state that broke the notice: a foreign account that has
// only ever spent, so its income map has no key at all. A notice built from
// income alone announces the account and then says it holds nothing.
function spendOnlyScope(): CurrencyScope {
  return {
    currencies: ["INR", "USD"],
    accounts: [
      ...oneCurrencyScope().accounts,
      {
        id: "a2",
        name: "Dollars",
        currency: "USD",
        income: {},
        expense: { USD: 906 },
      },
    ],
  };
}

function summary(overrides: Partial<DashboardSummary> = {}): DashboardSummary {
  return {
    totalAccounts: 1,
    totalTransactions: 12,
    totalIncome: { INR: 5000 },
    totalExpense: { INR: 2000 },
    totalNet: { INR: 3000 },
    byCategory: [],
    incomeByCategory: [],
    monthlyTrend: [
      { month: "2024-01", income: { INR: 5000 }, expense: { INR: 2000 } },
    ],
    recentTransactions: [
      {
        id: "t1",
        accountId: "a1",
        date: "2024-03-15",
        description: "Coffee Shop",
        amount: 250,
        type: "debit",
        accountName: "Checking",
      },
    ],
    currencyScope: oneCurrencyScope(),
    ...overrides,
  };
}

function page() {
  return (
    <MemoryRouter initialEntries={["/"]}>
      <Dashboard />
    </MemoryRouter>
  );
}

function expectedFinancialYear(): { dateFrom: string; dateTo: string } {
  const now = new Date();
  const startYear =
    now.getMonth() >= 3 ? now.getFullYear() : now.getFullYear() - 1;
  return {
    dateFrom: `${startYear}-04-01`,
    dateTo: `${startYear + 1}-03-31`,
  };
}

// DomainDataContext returns stable state references across renders; the mock
// mirrors that (a fixed object) so prefill only re-runs when accounts change.
function setDomain(accounts: Account[]) {
  domainMock.useDomainData.mockReturnValue({ accounts });
}

// Render with accounts loading asynchronously (empty at mount, then loaded),
// matching DomainDataProvider so Dashboard's default-account prefill wins over
// its URL-to-state sync.
function renderLoaded(accounts: Account[]) {
  setDomain([]);
  const view = render(page());
  setDomain(accounts);
  view.rerender(page());
  return view;
}

beforeEach(() => {
  vi.clearAllMocks();
  apiMock.getDashboardSummary.mockResolvedValue(summary());
  apiMock.getRecurringSeries.mockResolvedValue({ data: [] });
  setDomain([]);
});

describe("Dashboard", () => {
  it("renders the summary stats and recent transactions", async () => {
    renderLoaded([account()]);

    expect(await screen.findByText("Total Income")).toBeInTheDocument();
    expect(screen.getByText(formatOne(5000, "INR"))).toBeInTheDocument();
    expect(screen.getByText(formatOne(2000, "INR"))).toBeInTheDocument();
    expect(screen.getByText(formatOne(3000, "INR"))).toBeInTheDocument();
    expect(screen.getByText("12")).toBeInTheDocument();

    const recent = screen.getByText("Coffee Shop").closest("tr")!;
    expect(within(recent).getByText("Checking")).toBeInTheDocument();
    // A single-currency window pays for nothing: the notice renders nothing.
    expect(screen.queryByText(/other currencies?/)).toBeNull();
  });

  // A transaction carries no currency of its own — its account's is the one it is
  // denominated in — so the amount cell has to look the account up. It did not:
  // it called formatCurrency(amount) and took the "INR" default, which rendered a
  // USD transaction with a rupee symbol. The window-wide figures beside it were
  // already per currency, so the table contradicted the stat cards directly above
  // it, and a mixed-currency window is exactly when a reader is least able to
  // catch a wrong symbol.
  it("renders a recent transaction in its own account's currency", async () => {
    apiMock.getDashboardSummary.mockResolvedValue(
      summary({
        recentTransactions: [
          {
            id: "t2",
            accountId: "a2",
            date: "2024-03-16",
            description: "Dollar Store",
            amount: 42,
            type: "debit",
            accountName: "Dollars",
          },
        ],
        currencyScope: twoCurrencyScope(),
      }),
    );
    renderLoaded([
      account({ isDefault: false }),
      account({ id: "a2", name: "Dollars", currency: "USD", isDefault: false }),
    ]);

    const row = (await screen.findByText("Dollar Store")).closest("tr")!;
    // The amount cell renders a sign and the figure as sibling text nodes, so the
    // assertion is on the row's text rather than on a single element.
    //
    // The dollar figure, named — and the same treatment the stat cards above get
    // through formatOne, so the table and the cards agree.
    expect(row.textContent).toContain(formatOne(42, "USD"));
    // The wrong-currency form this replaces, asserted so the fix cannot be
    // "unfamiliar" and reverted: a rupee symbol on a dollar transaction.
    expect(row.textContent).not.toContain(formatCurrency(42, "INR"));
  });

  // The lookup can miss: the accounts context holds the user's accounts, and a
  // transaction whose account is not among them — a closed-and-hidden account, or
  // a reference data set that has not finished loading — must not fall back to
  // claiming INR. An amount with no currency named is honest; a rupee symbol on an
  // account of unknown currency is the same defect this test file is fixing, one
  // step removed.
  it("names no currency when the row's account is not in scope", async () => {
    apiMock.getDashboardSummary.mockResolvedValue(
      summary({
        recentTransactions: [
          {
            id: "t3",
            accountId: "a9",
            date: "2024-03-17",
            description: "Unknown Account",
            amount: 7,
            type: "debit",
            accountName: "Mystery",
          },
        ],
      }),
    );
    renderLoaded([account()]);

    const row = (await screen.findByText("Unknown Account")).closest("tr")!;
    expect(row.textContent).toContain(formatNumber(7));
    expect(row.textContent).not.toContain(formatCurrency(7, "INR"));
  });

  it("pre-fills the default account and passes it to the API", async () => {
    renderLoaded([account()]);
    await waitFor(() =>
      expect(apiMock.getDashboardSummary).toHaveBeenCalledWith(
        expect.objectContaining({ accountId: "a1" }),
      ),
    );
  });

  it("switches the range to the current financial year", async () => {
    const user = userEvent.setup();
    renderLoaded([account()]);
    await screen.findByText("Total Income");

    const trigger = screen.getByRole("combobox", { name: "Period" });
    expect(trigger).toHaveTextContent("Last 12 months");

    await user.click(trigger);
    await user.click(
      await screen.findByRole("option", { name: "Current financial year" }),
    );

    const { dateFrom, dateTo } = expectedFinancialYear();
    await waitFor(() =>
      expect(apiMock.getDashboardSummary).toHaveBeenCalledWith(
        expect.objectContaining({ dateFrom, dateTo }),
      ),
    );
    expect(screen.getByRole("combobox", { name: "Period" })).toHaveTextContent(
      "Current financial year",
    );
  });

  it("shows an error and retries on demand", async () => {
    const user = userEvent.setup();
    apiMock.getDashboardSummary.mockRejectedValue(new Error("load failed"));
    renderLoaded([account()]);

    expect(await screen.findByText("load failed")).toBeInTheDocument();

    apiMock.getDashboardSummary.mockResolvedValue(summary());
    await user.click(screen.getByRole("button", { name: "Retry" }));

    expect(await screen.findByText("Total Income")).toBeInTheDocument();
    expect(apiMock.getDashboardSummary.mock.calls.length).toBeGreaterThan(1);
  });

  it("ignores a stale summary response that resolves after a newer one", async () => {
    let resolveFirst: (v: DashboardSummary) => void = () => {};
    let resolveSecond: (v: DashboardSummary) => void = () => {};
    const first = new Promise<DashboardSummary>((res) => {
      resolveFirst = res;
    });
    const second = new Promise<DashboardSummary>((res) => {
      resolveSecond = res;
    });

    let calls = 0;
    apiMock.getDashboardSummary.mockImplementation(() => {
      calls += 1;
      if (calls === 1) return first;
      if (calls === 2) return second;
      return Promise.resolve(summary());
    });

    renderLoaded([account()]);

    // The newer (account-scoped) request resolves first.
    await act(async () => {
      resolveSecond(summary({ totalIncome: { INR: 9999 } }));
    });
    expect(await screen.findByText(formatOne(9999, "INR"))).toBeInTheDocument();

    // The older request resolves last; its data must not overwrite the newer
    // result.
    await act(async () => {
      resolveFirst(summary({ totalIncome: { INR: 1111 } }));
    });
    expect(screen.queryByText(formatOne(1111, "INR"))).not.toBeInTheDocument();
    expect(screen.getByText(formatOne(9999, "INR"))).toBeInTheDocument();
  });

  it("keeps the loaded dashboard while a refetch is in flight and after it fails", async () => {
    const user = userEvent.setup();
    renderLoaded([account({ billingDay: 15 })]);
    await screen.findByText("Total Income");

    // A filter change starts a refetch. The page — including the control being
    // used — must stay on screen rather than becoming a bare spinner, and a
    // failure must be reported inline instead of discarding the data.
    // tsconfig targets ES2022, so Promise.withResolvers is not available here.
    let failRefetch: (err: Error) => void = () => {};
    apiMock.getDashboardSummary.mockImplementation(
      () =>
        new Promise<DashboardSummary>((_resolve, reject) => {
          failRefetch = reject;
        }),
    );
    await user.click(screen.getByRole("switch"));

    expect(screen.getByText("Total Income")).toBeInTheDocument();

    failRefetch(new Error("refetch failed"));
    expect(await screen.findByText("refetch failed")).toBeInTheDocument();
    expect(screen.getByText("Total Income")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
  });

  it("switches to the billing-cycle view for an account with a billing day", async () => {
    const user = userEvent.setup();
    apiMock.getDashboardSummary.mockResolvedValue(
      summary({
        monthlyTrend: [],
        billingCycleTrend: [
          {
            label: "Mar 2024",
            startDate: "2024-03-01",
            endDate: "2024-03-31",
            income: { INR: 100 },
            expense: { INR: 50 },
          },
        ],
      }),
    );
    renderLoaded([account({ billingDay: 15 })]);
    await screen.findByText("Total Income");

    await user.click(screen.getByRole("switch"));

    await waitFor(() =>
      expect(apiMock.getDashboardSummary).toHaveBeenCalledWith(
        expect.objectContaining({ groupBy: "billing_cycle", cycles: "12" }),
      ),
    );
    expect(
      await screen.findByText("Statement Income vs Expenses"),
    ).toBeInTheDocument();
    expect(screen.getByText("Last 12 cycles")).toBeInTheDocument();
  });

  it("hides the billing-cycle toggle for accounts without a billing day", async () => {
    renderLoaded([account()]);
    await screen.findByText("Total Income");
    expect(screen.queryByRole("switch")).toBeNull();
  });

  it("shows the empty trend and recent-transactions messages", async () => {
    apiMock.getDashboardSummary.mockResolvedValue(
      summary({ monthlyTrend: [], recentTransactions: [] }),
    );
    renderLoaded([account()]);

    expect(
      await screen.findByText("No data yet. Import some statements!"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("No transactions yet. Import a statement to get started."),
    ).toBeInTheDocument();
  });

  it("renders the recurring section for the selected account", async () => {
    apiMock.getRecurringSeries.mockResolvedValue({
      data: [
        {
          id: "r1",
          accountId: "a1",
          name: "Netflix",
          description: "",
          amount: 1599,
          type: "debit",
          frequency: "monthly",
          interval: 1,
          startDate: "2024-01-01",
          endDate: null,
          categoryId: null,
          payeeId: null,
          active: true,
          notes: "",
          accountName: "Checking",
          categoryColor: "#06b6d4",
          monthlyAmount: 1599,
          attachedCount: 0,
          nextDueDate: "2099-01-15",
        },
      ] as RecurringSeries[],
    });
    renderLoaded([account()]);

    expect(
      await screen.findByText("Recurring & Subscriptions"),
    ).toBeInTheDocument();
    expect(screen.getByText("Netflix")).toBeInTheDocument();
    // The upcoming list below the card is one account's own rows, so those render
    // a transaction-style amount in the SERIES' account's currency — the card's
    // three headline figures are per currency, which is what the other test on
    // this file pins.
    expect(
      screen.getAllByText(formatOne(1599, "INR")).length,
    ).toBeGreaterThanOrEqual(1);
    expect(screen.getByRole("link", { name: "Manage" })).toHaveAttribute(
      "href",
      "/recurring",
    );
  });

  it("hides the recurring section when there are no series", async () => {
    renderLoaded([account()]);
    await screen.findByText("Total Income");
    expect(screen.queryByText("Recurring & Subscriptions")).toBeNull();
  });

  // The dashboard's recurring card forecast over every account the page is
  // showing, and `monthlyAmount` is a bare number, so a rupee subscription and a
  // dollar one were added together and the result printed as money. The
  // dashboard is the surface that already refuses this arithmetic everywhere
  // else, so the one place that still did it was the contradiction — and the
  // branch's claim that the SPA no longer adds across currencies was false for
  // this card and for the Recurring page it duplicates.
  it("refuses a recurring total that spans two currencies", async () => {
    // Neither account is the default, so the page shows all of them and the card
    // is not narrowed to one account's currency.
    apiMock.getRecurringSeries.mockResolvedValue({
      data: [
        {
          id: "r1",
          accountId: "a1",
          name: "Netflix",
          description: "",
          amount: 1599,
          type: "debit",
          frequency: "monthly",
          interval: 1,
          startDate: "2024-01-01",
          endDate: null,
          categoryId: null,
          payeeId: null,
          active: true,
          notes: "",
          accountName: "Checking",
          monthlyAmount: 1599,
          attachedCount: 0,
          nextDueDate: "2099-01-15",
        },
        {
          id: "r2",
          accountId: "a2",
          name: "Gym",
          description: "",
          amount: 40,
          type: "debit",
          frequency: "monthly",
          interval: 1,
          startDate: "2024-01-01",
          endDate: null,
          categoryId: null,
          payeeId: null,
          active: true,
          notes: "",
          accountName: "Dollars",
          monthlyAmount: 40,
          attachedCount: 0,
          nextDueDate: "2099-02-15",
        },
      ] as RecurringSeries[],
    });
    renderLoaded([
      account({ isDefault: false }),
      account({ id: "a2", name: "Dollars", currency: "USD", isDefault: false }),
    ]);

    expect(await screen.findByText("Netflix")).toBeInTheDocument();
    const card = screen.getByText("Monthly recurring expenses").parentElement!;
    expect(card.textContent).toContain("not combined");
    expect(card.textContent).toContain(formatOne(1599, "INR"));
    expect(card.textContent).toContain(formatOne(40, "USD"));

    // The restored sum. Asserting the refusal alone would pass just as well
    // against a card that printed 1,639 and said "not combined" underneath it.
    expect(screen.queryByText(formatOne(1639, "INR"))).not.toBeInTheDocument();
  });

  it("shows one currency at a time and names the one it is not showing", async () => {
    const user = userEvent.setup();
    apiMock.getDashboardSummary.mockResolvedValue(
      summary({
        totalIncome: { INR: 5000, USD: 120 },
        totalExpense: { INR: 2000, USD: 80 },
        totalNet: { INR: 3000, USD: 40 },
        currencyScope: twoCurrencyScope(),
      }),
    );
    renderLoaded([
      account(),
      account({ id: "a2", name: "Dollars", currency: "USD", isDefault: false }),
    ]);

    // Figures above are the selected currency's own, never a sum of the two.
    expect(await screen.findByText(formatOne(5000, "INR"))).toBeInTheDocument();
    expect(screen.queryByText(formatOne(5120, "INR"))).not.toBeInTheDocument();

    // The notice says what is left out, and where it is — naming the currency in
    // the group label, because the code is otherwise only ever printed inside a
    // figure, and a group whose figures are both "nothing" names no code at all.
    expect(screen.getByText("1 other currency")).toBeInTheDocument();
    expect(
      screen.getByText(
        /USD — Dollars: in USD .*120\.00, out USD .*80\.00\. These are not added to the figures above\./,
      ),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("combobox", { name: "Currency" }));
    await user.click(await screen.findByRole("option", { name: "USD" }));

    expect(await screen.findByText(formatOne(120, "USD"))).toBeInTheDocument();
    expect(screen.queryByText(formatOne(5000, "INR"))).not.toBeInTheDocument();
    expect(screen.getByText("1 other currency")).toBeInTheDocument();
    expect(
      screen.getByText(
        /INR — Checking: in INR .*5,000\.00, out INR .*2,000\.00\. These are not added/,
      ),
    ).toBeInTheDocument();
  });

  it("discloses a foreign account that only ever spent, with what it spent", async () => {
    // The notice has to report both sides. An income-only notice would read
    // "no transactions (Dollars)" beside a Money Out figure that has already left
    // that account's spending out.
    apiMock.getDashboardSummary.mockResolvedValue(
      summary({
        totalIncome: { INR: 5000 },
        totalExpense: { INR: 2000, USD: 906 },
        totalNet: { INR: 3000, USD: -906 },
        currencyScope: spendOnlyScope(),
      }),
    );
    renderLoaded([
      account(),
      account({ id: "a2", name: "Dollars", currency: "USD", isDefault: false }),
    ]);

    expect(await screen.findByText("1 other currency")).toBeInTheDocument();
    // The whole sentence a user reads, apart from the count above it, which is a
    // nested element: the currency and the account are named, the side with no
    // money says so, and the side that has money carries the figure. The code is
    // in the label precisely because this is the case where one half of it is
    // "nothing" — a quiet account whose currency would otherwise never be named.
    expect(
      screen.getByText(
        /^in 1 account not shown here — USD — Dollars: in nothing, out USD \$906\.00\. These are not added to the figures above\.$/,
      ),
    ).toBeInTheDocument();
    // And the account's spending is not claimed to be absent from the payload.
    expect(screen.queryByText(/no transactions/)).toBeNull();
    expect(screen.queryByText(/Dollars: in USD/)).toBeNull();
  });

  // The designed quiet account: it is in currencyScope because the plan requires
  // accounts with no transactions in the window to appear, and it holds no money
  // at all in this one. The notice must still say which currency it is, or the
  // user is told about an account they cannot find and given nothing to search
  // for. Both existing fixtures have money on at least one side, so neither
  // covers it.
  it("names the currency of a quiet account, whose figures are both nothing", async () => {
    apiMock.getDashboardSummary.mockResolvedValue(
      summary({
        totalIncome: { INR: 5000 },
        totalExpense: { INR: 2000 },
        totalNet: { INR: 3000 },
        currencyScope: {
          currencies: ["INR", "USD"],
          accounts: [
            ...oneCurrencyScope().accounts,
            {
              id: "a2",
              name: "Travel card",
              currency: "USD",
              income: {},
              expense: {},
            },
          ],
        },
      }),
    );
    renderLoaded([
      account(),
      account({
        id: "a2",
        name: "Travel card",
        currency: "USD",
        isDefault: false,
      }),
    ]);

    expect(await screen.findByText("1 other currency")).toBeInTheDocument();
    const group = screen.getByText(
      /^in 1 account not shown here — USD — Travel card: in nothing, out nothing\. These are not added to the figures above\.$/,
    );
    expect(group).toBeInTheDocument();
    // The one thing a reader needs and the old wording left out: the code.
    expect(group.textContent).toContain("USD");
  });

  it("leaves a category with no figure in the selected currency at zero", async () => {
    // A category spent only in dollars, in a window the rupee selection is
    // looking at. The row is real, so the pie beside it draws a real (empty)
    // slice — but it has no rupee figure to report, and the net card above it
    // takes its colour from the server's net rather than from `net >= 0`.
    apiMock.getDashboardSummary.mockResolvedValue(
      summary({
        totalIncome: { INR: 5000, USD: 900 },
        totalExpense: { INR: 2000, USD: 1200 },
        totalNet: { INR: 3000, USD: -300 },
        byCategory: [
          {
            categoryId: "c1",
            categoryName: "Rent",
            categoryColor: "#f97316",
            categoryIcon: "🏠",
            total: { INR: 2000 },
            count: 1,
          },
          {
            categoryId: "c2",
            categoryName: "Latte",
            categoryColor: "#a855f7",
            categoryIcon: "☕",
            total: { USD: 900 },
            count: 1,
          },
        ],
        currencyScope: twoCurrencyScope(),
      }),
    );
    renderLoaded([
      account(),
      account({ id: "a2", name: "Dollars", currency: "USD", isDefault: false }),
    ]);

    // The net is the server's figure, read in the selected currency.
    const card = (await screen.findByText("Net Savings")).parentElement!;
    const net = within(card).getByText(formatOne(3000, "INR"));
    expect(net).toHaveClass("text-chart-3");

    // A category spent only in dollars has no rupee figure at all, and the row
    // says which currency is missing rather than printing a bare zero beside
    // "INR 2,000.00" that could be read as a real one.
    const row = screen.getByText("Latte").parentElement!;
    expect(within(row).getByText("no INR in this report")).toBeInTheDocument();
    expect(within(row).queryByText("0.00")).toBeNull();
    expect(within(row).queryByText(formatOne(900, "USD"))).toBeNull();
  });
});
