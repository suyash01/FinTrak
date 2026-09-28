import { describe, it, expect, vi, beforeEach } from "vitest";
import type { ReactNode } from "react";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import MoneyFlow, { nodeDrilldownPath } from "./MoneyFlow";
import { formatOne } from "../../lib/currency";
import type {
  CurrencyScope,
  LinkCycleReport,
  MoneyFlowGraph,
  MoneyFlowNode,
  MoneyFlowTimeline,
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
  Sankey: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Tooltip: () => null,
}));

const { apiMock, domainMock } = vi.hoisted(() => ({
  apiMock: {
    getMoneyFlow: vi.fn(),
    getMoneyFlowTimeline: vi.fn(),
    getLinkCycles: vi.fn(),
  },
  domainMock: { useDomainData: vi.fn() },
}));

vi.mock("../../api/client", () => ({ default: apiMock }));
vi.mock("../../context/DomainDataContext", () => ({
  useDomainData: domainMock.useDomainData,
}));
vi.mock("../../context/SettingsContext", () => ({
  useSettings: () => ({ compactLayout: false }),
}));

// oneCurrencyScope is the common case: a window that only ever touched rupees.
function oneCurrencyScope(): CurrencyScope {
  return {
    currencies: ["INR"],
    accounts: [
      {
        id: "a1",
        name: "Checking",
        currency: "INR",
        income: { INR: 50000 },
        expense: { INR: 12000 },
      },
    ],
  };
}

function graph(overrides: Partial<MoneyFlowGraph> = {}): MoneyFlowGraph {
  return {
    nodes: [
      {
        id: "income:i1",
        name: "Salary",
        kind: "income",
        color: "#22c55e",
        group: "income",
        total: { INR: 50000 },
      },
      {
        id: "account:a1",
        name: "Checking",
        kind: "account",
        color: "#3b82f6",
        total: { INR: 50000 },
      },
      {
        id: "category:c1",
        name: "Food",
        kind: "category",
        color: "#f97316",
        group: "expense",
        total: { INR: 12000 },
      },
      { id: "payee:p1", name: "Zomato", kind: "payee", total: { INR: 12000 } },
    ],
    links: [
      { source: "income:i1", target: "account:a1", value: { INR: 50000 } },
      { source: "account:a1", target: "category:c1", value: { INR: 12000 } },
      { source: "category:c1", target: "payee:p1", value: { INR: 12000 } },
    ],
    totalIncome: { INR: 50000 },
    totalExpense: { INR: 12000 },
    totalNet: { INR: 38000 },
    linkSummary: [{ type: "transfer", count: 2, total: { INR: 30000 } }],
    suppressedCycles: [],
    currencyScope: oneCurrencyScope(),
    ...overrides,
  };
}

function timeline(): MoneyFlowTimeline {
  return {
    groupBy: "month",
    periods: [
      {
        key: "2024-05",
        label: "May 2024",
        startDate: "2024-05-01",
        endDate: "2024-05-31",
        income: { INR: 5000 },
        expense: { INR: 1000 },
        net: { INR: 4000 },
      },
      {
        key: "2024-06",
        label: "Jun 2024",
        startDate: "2024-06-01",
        endDate: "2024-06-30",
        income: { INR: 0 },
        expense: { INR: 2000 },
        net: { INR: -2000 },
      },
    ],
    currencyScope: oneCurrencyScope(),
  };
}

function cycleReport(overrides: Partial<LinkCycleReport> = {}): LinkCycleReport {
  return {
    cycles: [
      {
        kind: "reciprocal",
        accounts: [
          { id: "a1", name: "Checking", color: "#3b82f6" },
          { id: "a2", name: "Card", color: "#f97316" },
        ],
        legs: [
          {
            fromAccountId: "a1",
            fromAccountName: "Checking",
            toAccountId: "a2",
            toAccountName: "Card",
            amount: { INR: 8000 },
            count: 2,
            types: [{ type: "transfer", count: 2, total: { INR: 8000 } }],
          },
          {
            fromAccountId: "a2",
            fromAccountName: "Card",
            toAccountId: "a1",
            toAccountName: "Checking",
            amount: { INR: 3000 },
            count: 1,
            types: [{ type: "transfer", count: 1, total: { INR: 3000 } }],
          },
        ],
        net: { INR: 3000 },
        gross: { INR: 11000 },
        transactions: 3,
      },
    ],
    totalCircular: { INR: 3000 },
    oneSidedFlows: [
      {
        fromAccountId: "a1",
        fromAccountName: "Checking",
        toAccountId: "a3",
        toAccountName: "Savings",
        total: { INR: 1500 },
        count: 1,
        types: [{ type: "bill_payment", count: 1, total: { INR: 1500 } }],
      },
    ],
    currencyScope: oneCurrencyScope(),
    ...overrides,
  };
}

function page(entry = "/money-flow") {
  return (
    <MemoryRouter initialEntries={[entry]}>
      <MoneyFlow />
    </MemoryRouter>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  apiMock.getMoneyFlow.mockResolvedValue(graph());
  apiMock.getMoneyFlowTimeline.mockResolvedValue(timeline());
  apiMock.getLinkCycles.mockResolvedValue(cycleReport());
  domainMock.useDomainData.mockReturnValue({
    accounts: [],
    groups: [{ id: "expense", name: "Expense" }],
  });
});

describe("MoneyFlow", () => {
  it("renders the flow stats and the link summary", async () => {
    render(page());

    expect(await screen.findByText("Money Flow")).toBeInTheDocument();
    expect(screen.getByText("Money In")).toBeInTheDocument();
    expect(screen.getByText(formatOne(50000, "INR"))).toBeInTheDocument();
    expect(screen.getByText(formatOne(12000, "INR"))).toBeInTheDocument();
    // The net is the server's figure, not a subtraction of the two above.
    expect(screen.getByText(formatOne(38000, "INR"))).toBeInTheDocument();

    expect(screen.getByText("Transfers")).toBeInTheDocument();
    expect(screen.getByText("2 links")).toBeInTheDocument();
    expect(screen.getByText(formatOne(30000, "INR"))).toBeInTheDocument();
  });

  it("renders the circular-money report", async () => {
    render(page());

    expect(await screen.findByText("Circular Money")).toBeInTheDocument();
    expect(screen.getByText("Cycles between your accounts")).toBeInTheDocument();
    expect(screen.getByText("Two-way pair")).toBeInTheDocument();
    expect(
      screen.getByText(`${formatOne(3000, "INR")} circulating`),
    ).toBeInTheDocument();
    // Both cycle legs are listed with their own flows.
    expect(
      screen.getByText(`Checking → Card: ${formatOne(8000, "INR")}`),
    ).toBeInTheDocument();
    expect(screen.getByText("One-way account flows")).toBeInTheDocument();
    expect(screen.getByText("Bill payments (1)")).toBeInTheDocument();
    expect(screen.getByText(formatOne(1500, "INR"))).toBeInTheDocument();
  });

  it("shows an empty state when there is no circular money", async () => {
    apiMock.getLinkCycles.mockResolvedValue(
      cycleReport({
        cycles: [],
        oneSidedFlows: [],
        totalCircular: { INR: 0 },
      }),
    );
    render(page());

    expect(
      await screen.findByText(
        "No circular or one-way account flows in this range.",
      ),
    ).toBeInTheDocument();
  });

  it("hides the circular-money panel when the report fails to load", async () => {
    apiMock.getLinkCycles.mockRejectedValue(new Error("load failed"));
    render(page());

    await screen.findByText("Money Flow");
    await waitFor(() => expect(apiMock.getLinkCycles).toHaveBeenCalled());
    expect(screen.queryByText("Circular Money")).not.toBeInTheDocument();
  });

  // The graph and the linked-transfers rollup are two totals for one sum, and
  // the rollup counts every link whatever the graph did with it. suppressedCycles
  // is the difference, per currency — and it travels in the graph response, not
  // in the /links/cycles one, so a failure to load the per-cycle detail must not
  // take the reconciliation with it: that is exactly the state where a reader is
  // looking at two totals with nothing to reconcile them.
  describe("the withheld cycles the graph could not draw", () => {
    function suppressedGraph() {
      return graph({
        suppressedCycles: [
          {
            kind: "reciprocal",
            accounts: ["a1", "a2"],
            legs: [
              {
                from: "a1",
                to: "a2",
                gross: { INR: 8000 },
                discarded: { INR: 5000 },
              },
              {
                from: "a2",
                to: "a1",
                // Netting cannot cancel a rupee against a dollar, so a leg in a
                // second currency loses all of it — which is the case a client
                // asks about and the reason the amounts are per currency.
                gross: { USD: 40 },
                discarded: { USD: 40 },
              },
            ],
          },
        ],
        currencyScope: {
          currencies: ["INR", "USD"],
          accounts: [
            ...oneCurrencyScope().accounts,
            {
              id: "a2",
              name: "Card",
              currency: "USD",
              income: {},
              expense: { USD: 40 },
            },
          ],
        },
      });
    }

    it("reports the per-currency amounts withheld, and says the list is not the test", async () => {
      apiMock.getMoneyFlow.mockResolvedValue(suppressedGraph());
      render(page());

      expect(await screen.findByText("Withheld from the graph")).toBeInTheDocument();
      // Both currencies named with their own withheld amounts, and no sum of
      // them: the graph is short of two different figures, not one.
      expect(
        screen.getByText(`Checking → Card: ${formatOne(8000, "INR")} flowed`),
      ).toBeInTheDocument();
      expect(
        screen.getByText(`${formatOne(5000, "INR")} not in the graph`),
      ).toBeInTheDocument();
      expect(
        screen.getByText(`Card → Checking: ${formatOne(40, "USD")} flowed`),
      ).toBeInTheDocument();
      expect(
        screen.getByText(`${formatOne(40, "USD")} not in the graph`),
      ).toBeInTheDocument();

      // The constraint, in the words a reader would act on: emptiness answers
      // "was a cycle netted", not "did a currency go missing".
      expect(
        screen.getByText(/never from whether this list has anything/i),
      ).toBeInTheDocument();
    });

    it("survives the per-cycle report failing to load", async () => {
      apiMock.getMoneyFlow.mockResolvedValue(suppressedGraph());
      apiMock.getLinkCycles.mockRejectedValue(new Error("load failed"));
      render(page());

      // No cycle detail, but the reconciliation between the two totals is still
      // on screen — it came from the graph, not from the request that failed.
      expect(await screen.findByText("Withheld from the graph")).toBeInTheDocument();
      expect(
        screen.getByText(`${formatOne(5000, "INR")} not in the graph`),
      ).toBeInTheDocument();
      expect(screen.queryByText("Cycles between your accounts")).toBeNull();
    });

    // The plan's constraint on a cycle's net: it is a per-currency local
    // minimum, so it is a circulation figure only while the loop holds one
    // currency. The MCP states this on the same data, so the SPA printing
    // "INR 3,000.00 circulating" for a two-currency rollup would be two surfaces
    // disagreeing about the sentence this branch wrote for it.
    it("refuses a circulation figure for a multi-currency total", async () => {
      apiMock.getLinkCycles.mockResolvedValue(
        cycleReport({ totalCircular: { INR: 3000, USD: 40 } }),
      );
      render(page());

      await screen.findByText("Cycles between your accounts");
      expect(
        screen.queryByText(`${formatOne(3000, "INR")} circulating`),
      ).toBeNull();
      expect(
        screen.getByText(/2 currencies: .* — not combined circulating/),
      ).toBeInTheDocument();
    });
  });

  it("passes the account filter and node limit to the API", async () => {
    render(page("/money-flow?accountId=a1"));

    await waitFor(() =>
      expect(apiMock.getMoneyFlow).toHaveBeenCalledWith(
        expect.objectContaining({ accountId: "a1", limit: "12" }),
      ),
    );
    // The circular-money report follows the same window as the graph.
    await waitFor(() =>
      expect(apiMock.getLinkCycles).toHaveBeenCalledWith(
        expect.objectContaining({ accountId: "a1" }),
      ),
    );
  });

  it("changes the node limit from the filter", async () => {
    const user = userEvent.setup();
    render(page());
    await screen.findByText("Money Flow");

    await user.click(screen.getByRole("combobox", { name: "Nodes per stage" }));
    await user.click(
      await screen.findByRole("option", { name: "Top 8 per stage" }),
    );

    await waitFor(() =>
      expect(apiMock.getMoneyFlow).toHaveBeenCalledWith(
        expect.objectContaining({ limit: "8" }),
      ),
    );
  });

  it("keeps the billing-cycle grouping while the account list loads", async () => {
    // Billing-cycle grouping is only valid for an account with a billing day,
    // but the guard must not fire before the account list arrives: a bookmarked
    // groupBy=billing_cycle used to be reset to months on every load, and the
    // URL writer then dropped the parameter for good.
    domainMock.useDomainData.mockReturnValue({ accounts: [], groups: [] });
    const view = render(
      page("/money-flow?accountId=a1&groupBy=billing_cycle"),
    );
    domainMock.useDomainData.mockReturnValue({
      accounts: [
        {
          id: "a1",
          name: "Card",
          accountTypeId: "credit_card",
          billingDay: 15,
        },
      ],
      groups: [],
    });
    view.rerender(page("/money-flow?accountId=a1&groupBy=billing_cycle"));

    await waitFor(() =>
      expect(apiMock.getMoneyFlowTimeline).toHaveBeenCalledWith(
        expect.objectContaining({
          accountId: "a1",
          groupBy: "billing_cycle",
        }),
      ),
    );
    expect(
      await screen.findByRole("combobox", { name: "Timeline grouping" }),
    ).toHaveTextContent("By billing cycle");
  });

  it("shows an error and retries on demand", async () => {
    const user = userEvent.setup();
    apiMock.getMoneyFlow.mockRejectedValue(new Error("load failed"));
    render(page());

    expect(await screen.findByText("load failed")).toBeInTheDocument();

    apiMock.getMoneyFlow.mockResolvedValue(graph());
    await user.click(screen.getByRole("button", { name: "Retry" }));

    expect(await screen.findByText("Money Flow")).toBeInTheDocument();
    expect(apiMock.getMoneyFlow.mock.calls.length).toBeGreaterThan(1);
  });

  it("shows empty states when there are no flows", async () => {
    apiMock.getMoneyFlow.mockResolvedValue(
      graph({
        nodes: [],
        links: [],
        totalIncome: { INR: 0 },
        totalExpense: { INR: 0 },
        totalNet: { INR: 0 },
        linkSummary: [],
      }),
    );
    render(page());

    expect(
      await screen.findByText(
        "No transactions in this range. Import a statement to see the flow.",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText("No linked transactions in this range."),
    ).toBeInTheDocument();
  });

  it("renders the timeline and scrubs the graph window to a period", async () => {
    const user = userEvent.setup();
    render(page());
    await screen.findByText("Money Flow");

    const period = await screen.findByRole("button", { name: /May 2024/ });
    await user.click(period);

    await waitFor(() =>
      expect(apiMock.getMoneyFlow).toHaveBeenCalledWith(
        expect.objectContaining({
          dateFrom: "2024-05-01",
          dateTo: "2024-05-31",
        }),
      ),
    );
  });

  it("shows one currency at a time and names the one it is not showing", async () => {
    const user = userEvent.setup();
    const twoCurrencies: CurrencyScope = {
      currencies: ["INR", "USD"],
      accounts: [
        ...oneCurrencyScope().accounts,
        {
          id: "a2",
          name: "Dollars",
          currency: "USD",
          income: { USD: 900 },
          expense: { USD: 300 },
        },
      ],
    };
    apiMock.getMoneyFlow.mockResolvedValue(
      graph({
        totalIncome: { INR: 50000, USD: 900 },
        totalExpense: { INR: 12000, USD: 300 },
        totalNet: { INR: 38000, USD: 600 },
        linkSummary: [{ type: "transfer", count: 2, total: { INR: 30000 } }],
        currencyScope: twoCurrencies,
      }),
    );
    render(page());

    expect(await screen.findByText("Money Flow")).toBeInTheDocument();
    expect(screen.getByText(formatOne(50000, "INR"))).toBeInTheDocument();
    expect(screen.queryByText(formatOne(50900, "INR"))).not.toBeInTheDocument();
    expect(screen.getByText("1 other currency")).toBeInTheDocument();
    expect(
      screen.getByText(
        /Dollars: in USD .*900\.00, out USD .*300\.00\. These are not added/,
      ),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("combobox", { name: "Currency" }));
    await user.click(await screen.findByRole("option", { name: "USD" }));

    expect(await screen.findByText(formatOne(900, "USD"))).toBeInTheDocument();
    expect(screen.queryByText(formatOne(50000, "INR"))).not.toBeInTheDocument();
  });

  it("sizes the timeline bars by the selected currency only", async () => {
    // June earned and spent in dollars on a larger scale than May earned and
    // spent in rupees. Its INR figures are genuinely absent: the bars must fall
    // to the floor rather than be sized by the foreign magnitudes, and May must
    // still fill the strip. Taking the largest currency as the denominator
    // would shrink May to a little over half height.
    apiMock.getMoneyFlow.mockResolvedValue(
      graph({
        currencyScope: {
          currencies: ["INR", "USD"],
          accounts: [
            ...oneCurrencyScope().accounts,
            {
              id: "a2",
              name: "Dollars",
              currency: "USD",
              income: { USD: 9000 },
              expense: { USD: 3000 },
            },
          ],
        },
      }),
    );
    apiMock.getMoneyFlowTimeline.mockResolvedValue({
      groupBy: "month",
      periods: [
        {
          key: "2024-05",
          label: "May 2024",
          startDate: "2024-05-01",
          endDate: "2024-05-31",
          income: { INR: 5000 },
          expense: { INR: 1000 },
          net: { INR: 4000 },
        },
        {
          key: "2024-06",
          label: "Jun 2024",
          startDate: "2024-06-01",
          endDate: "2024-06-30",
          income: { USD: 9000 },
          expense: { USD: 3000 },
          net: { USD: 6000 },
        },
      ],
      currencyScope: { currencies: ["INR", "USD"], accounts: [] },
    });
    render(page());

    const barHeights = (button: HTMLElement) =>
      [...button.querySelectorAll("span[style]")].map((bar) =>
        bar.getAttribute("style"),
      );

    const domestic = await screen.findByRole("button", { name: /May 2024/ });
    expect(barHeights(domestic)).toEqual(["height: 100%;", "height: 20%;"]);
    // A period with nothing in the selected currency draws flat rather than
    // being sized by the currency the user is not looking at.
    const foreign = screen.getByRole("button", { name: /Jun 2024/ });
    expect(barHeights(foreign)).toEqual(["height: 2%;", "height: 2%;"]);

    // And its net claims no sign and prints no dollar of rupees: a period with
    // nothing in the selected currency says so rather than showing a zero that
    // could be read as a figure.
    const net = within(foreign).getByText("no INR in this report");
    expect(net).toHaveClass("text-muted-foreground");
    expect(net).not.toHaveClass("text-chart-3");
    expect(within(foreign).queryByText(formatOne(0, "INR"))).toBeNull();
  });
});

describe("nodeDrilldownPath", () => {
  const node = (id: string, kind: MoneyFlowNode["kind"]): MoneyFlowNode => ({
    id,
    name: id,
    kind,
    total: { INR: 1 },
  });

  it("maps each node kind to Transactions filters", () => {
    expect(
      nodeDrilldownPath(node("account:a1", "account"), "2024-01-01", "2024-01-31"),
    ).toBe(
      "/transactions?dateFrom=2024-01-01&dateTo=2024-01-31&accountId=a1",
    );
    expect(
      nodeDrilldownPath(node("category:c1", "category"), "2024-01-01", "2024-01-31"),
    ).toBe(
      "/transactions?dateFrom=2024-01-01&dateTo=2024-01-31&categoryId=c1&type=debit",
    );
    expect(
      nodeDrilldownPath(node("income:i1", "income"), "2024-01-01", "2024-01-31"),
    ).toBe(
      "/transactions?dateFrom=2024-01-01&dateTo=2024-01-31&categoryId=i1&type=credit",
    );
    expect(
      nodeDrilldownPath(node("payee:p1", "payee"), "2024-01-01", "2024-01-31"),
    ).toBe(
      "/transactions?dateFrom=2024-01-01&dateTo=2024-01-31&payeeId=p1&type=debit",
    );
  });

  it("handles the none/uncategorized sentinels and rejects rollups", () => {
    expect(nodeDrilldownPath(node("payee:none", "payee"), "", "")).toBe(
      "/transactions?payeeId=none&type=debit",
    );
    expect(
      nodeDrilldownPath(node("category:uncategorized", "category"), "", ""),
    ).toBe("/transactions?categoryId=uncategorized&type=debit");
    expect(nodeDrilldownPath(node("payee:other", "payee"), "", "")).toBeNull();
    expect(nodeDrilldownPath(node("category:other", "category"), "", "")).toBeNull();
  });
});
