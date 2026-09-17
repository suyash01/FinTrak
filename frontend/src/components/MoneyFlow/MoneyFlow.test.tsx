import { describe, it, expect, vi, beforeEach } from "vitest";
import type { ReactNode } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import MoneyFlow from "./MoneyFlow";
import { formatCurrency } from "../../utils/formatters";
import type { MoneyFlowGraph } from "../../types";

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
  apiMock: { getMoneyFlow: vi.fn() },
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

  it("passes the account filter and node limit to the API", async () => {
    render(page("/money-flow?accountId=a1"));

    await waitFor(() =>
      expect(apiMock.getMoneyFlow).toHaveBeenCalledWith(
        expect.objectContaining({ accountId: "a1", limit: "12" }),
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
});
