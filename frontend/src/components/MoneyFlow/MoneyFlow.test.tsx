import { describe, it, expect, vi, beforeEach } from "vitest";
import type { ReactNode } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import MoneyFlow, { nodeDrilldownPath } from "./MoneyFlow";
import { formatCurrency } from "../../utils/formatters";
import type {
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

function graph(overrides: Partial<MoneyFlowGraph> = {}): MoneyFlowGraph {
  return {
    nodes: [
      {
        id: "income:i1",
        name: "Salary",
        kind: "income",
        color: "#22c55e",
        group: "income",
        total: 50000,
      },
      {
        id: "account:a1",
        name: "Checking",
        kind: "account",
        color: "#3b82f6",
        total: 50000,
      },
      {
        id: "category:c1",
        name: "Food",
        kind: "category",
        color: "#f97316",
        group: "expense",
        total: 12000,
      },
      { id: "payee:p1", name: "Zomato", kind: "payee", total: 12000 },
    ],
    links: [
      { source: "income:i1", target: "account:a1", value: 50000 },
      { source: "account:a1", target: "category:c1", value: 12000 },
      { source: "category:c1", target: "payee:p1", value: 12000 },
    ],
    totalIncome: 50000,
    totalExpense: 12000,
    linkSummary: [{ type: "transfer", count: 2, total: 30000 }],
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
        income: 5000,
        expense: 1000,
        net: 4000,
      },
      {
        key: "2024-06",
        label: "Jun 2024",
        startDate: "2024-06-01",
        endDate: "2024-06-30",
        income: 0,
        expense: 2000,
        net: -2000,
      },
    ],
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
            amount: 8000,
            count: 2,
            types: [{ type: "transfer", count: 2, total: 8000 }],
          },
          {
            fromAccountId: "a2",
            fromAccountName: "Card",
            toAccountId: "a1",
            toAccountName: "Checking",
            amount: 3000,
            count: 1,
            types: [{ type: "transfer", count: 1, total: 3000 }],
          },
        ],
        net: 3000,
        gross: 11000,
        transactions: 3,
      },
    ],
    totalCircular: 3000,
    oneSidedFlows: [
      {
        fromAccountId: "a1",
        fromAccountName: "Checking",
        toAccountId: "a3",
        toAccountName: "Savings",
        total: 1500,
        count: 1,
        types: [{ type: "bill_payment", count: 1, total: 1500 }],
      },
    ],
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
    expect(screen.getByText(formatCurrency(50000))).toBeInTheDocument();
    expect(screen.getByText(formatCurrency(12000))).toBeInTheDocument();
    expect(screen.getByText(formatCurrency(38000))).toBeInTheDocument();

    expect(screen.getByText("Transfers")).toBeInTheDocument();
    expect(screen.getByText("2 links")).toBeInTheDocument();
    expect(screen.getByText(formatCurrency(30000))).toBeInTheDocument();
  });

  it("renders the circular-money report", async () => {
    render(page());

    expect(await screen.findByText("Circular Money")).toBeInTheDocument();
    expect(screen.getByText("Cycles between your accounts")).toBeInTheDocument();
    expect(screen.getByText("Two-way pair")).toBeInTheDocument();
    expect(
      screen.getByText(`${formatCurrency(3000)} circulating`),
    ).toBeInTheDocument();
    // Both cycle legs are listed with their own flows.
    expect(
      screen.getByText(`Checking → Card: ${formatCurrency(8000)}`),
    ).toBeInTheDocument();
    expect(screen.getByText("One-way account flows")).toBeInTheDocument();
    expect(screen.getByText("Bill payments (1)")).toBeInTheDocument();
    expect(screen.getByText(formatCurrency(1500))).toBeInTheDocument();
  });

  it("shows an empty state when there is no circular money", async () => {
    apiMock.getLinkCycles.mockResolvedValue(
      cycleReport({ cycles: [], oneSidedFlows: [], totalCircular: 0 }),
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
      graph({ nodes: [], links: [], totalIncome: 0, totalExpense: 0, linkSummary: [] }),
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
});

describe("nodeDrilldownPath", () => {
  const node = (id: string, kind: MoneyFlowNode["kind"]): MoneyFlowNode => ({
    id,
    name: id,
    kind,
    total: 1,
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
