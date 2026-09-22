import { describe, it, expect, vi, beforeEach } from "vitest";
import type { ReactNode } from "react";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import Dashboard from "./Dashboard";
import { formatCurrency } from "../../utils/formatters";
import type { Account, DashboardSummary, RecurringSeries } from "../../types";

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

function summary(overrides: Partial<DashboardSummary> = {}): DashboardSummary {
  return {
    totalAccounts: 1,
    totalTransactions: 12,
    totalIncome: 5000,
    totalExpense: 2000,
    byCategory: [],
    incomeByCategory: [],
    monthlyTrend: [{ month: "2024-01", income: 5000, expense: 2000 }],
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
    expect(screen.getByText(formatCurrency(5000))).toBeInTheDocument();
    expect(screen.getByText(formatCurrency(2000))).toBeInTheDocument();
    expect(screen.getByText(formatCurrency(3000))).toBeInTheDocument();
    expect(screen.getByText("12")).toBeInTheDocument();

    const recent = screen.getByText("Coffee Shop").closest("tr")!;
    expect(within(recent).getByText("Checking")).toBeInTheDocument();
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
      resolveSecond(summary({ totalIncome: 9999 }));
    });
    expect(await screen.findByText(formatCurrency(9999))).toBeInTheDocument();

    // The older request resolves last; its data must not overwrite the newer
    // result.
    await act(async () => {
      resolveFirst(summary({ totalIncome: 1111 }));
    });
    expect(screen.queryByText(formatCurrency(1111))).not.toBeInTheDocument();
    expect(screen.getByText(formatCurrency(9999))).toBeInTheDocument();
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
            income: 100,
            expense: 50,
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
    expect(
      screen.getAllByText(formatCurrency(1599)).length,
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
});
