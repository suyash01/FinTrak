import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import CashFlowCalendar from "./CashFlowCalendar";
import { formatCurrency } from "../../utils/formatters";
import type { CashFlowCalendar as CashFlowCalendarData } from "../../types";

if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

const { apiMock, domainMock } = vi.hoisted(() => ({
  apiMock: { getCashFlowCalendar: vi.fn() },
  domainMock: { useDomainData: vi.fn() },
}));

vi.mock("../../api/client", () => ({ default: apiMock }));
vi.mock("../../context/DomainDataContext", () => ({
  useDomainData: domainMock.useDomainData,
}));
vi.mock("../../context/SettingsContext", () => ({
  useSettings: () => ({ compactLayout: false }),
}));

function calendar(
  overrides: Partial<CashFlowCalendarData> = {},
): CashFlowCalendarData {
  return {
    days: [
      { date: "2024-06-03", income: 5000, expense: 0, net: 5000, count: 1 },
      { date: "2024-06-04", income: 0, expense: 1500, net: -1500, count: 2 },
    ],
    markers: [
      {
        date: "2024-06-04",
        label: "Running balance",
        kind: "balance",
        amount: -1500,
      },
    ],
    cycles: [],
    totalIncome: 5000,
    totalExpense: 1500,
    net: 3500,
    maxAbsNet: 5000,
    ...overrides,
  };
}

function page(entry = "/cash-flow-calendar?dateFrom=2024-06-03&dateTo=2024-06-10") {
  return (
    <MemoryRouter initialEntries={[entry]}>
      <CashFlowCalendar />
    </MemoryRouter>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  apiMock.getCashFlowCalendar.mockResolvedValue(calendar());
  domainMock.useDomainData.mockReturnValue({ accounts: [] });
});

describe("CashFlowCalendar", () => {
  it("renders the stats and the heatmap", async () => {
    render(page());

    expect(await screen.findByText("Cash Flow Calendar")).toBeInTheDocument();
    expect(screen.getByText("Money In")).toBeInTheDocument();
    expect(screen.getByText(formatCurrency(5000))).toBeInTheDocument();
    expect(screen.getByText(formatCurrency(1500))).toBeInTheDocument();
    expect(screen.getByText(formatCurrency(3500))).toBeInTheDocument();
    expect(screen.getByText("Daily Net Flow")).toBeInTheDocument();
  });

  it("passes the account and date filters to the API", async () => {
    render(
      page(
        "/cash-flow-calendar?accountId=a1&dateFrom=2024-06-03&dateTo=2024-06-10",
      ),
    );

    await waitFor(() =>
      expect(apiMock.getCashFlowCalendar).toHaveBeenCalledWith(
        expect.objectContaining({
          accountId: "a1",
          dateFrom: "2024-06-03",
          dateTo: "2024-06-10",
        }),
      ),
    );
  });

  it("renders billing-cycle boundaries when present", async () => {
    apiMock.getCashFlowCalendar.mockResolvedValue(
      calendar({
        cycles: [
          {
            id: "c1",
            label: "Jun 2024",
            // Cycle dates arrive as RFC3339 timestamps; the start falls on a
            // Sunday to exercise the week-overlap normalization.
            startDate: "2024-06-09T00:00:00Z",
            endDate: "2024-07-09T00:00:00Z",
            outstanding: 0,
          },
        ],
      }),
    );
    render(page());

    expect(await screen.findByText("Jun 2024")).toBeInTheDocument();
    expect(screen.getByText("Billing cycle")).toBeInTheDocument();
  });

  it("shows the selected day's detail after a click", async () => {
    const user = userEvent.setup();
    render(page());
    await screen.findByText("Cash Flow Calendar");

    const cells = screen.getAllByRole("gridcell");
    expect(cells.length).toBeGreaterThan(0);
    await user.click(cells[0]);

    // The selected-day panel (and the hover tooltip) surface the day's count.
    const details = await screen.findAllByText("1 transaction");
    expect(details.length).toBeGreaterThan(0);
  });

  it("shows an error and retries on demand", async () => {
    const user = userEvent.setup();
    apiMock.getCashFlowCalendar.mockRejectedValue(new Error("load failed"));
    render(page());

    expect(await screen.findByText("load failed")).toBeInTheDocument();

    apiMock.getCashFlowCalendar.mockResolvedValue(calendar());
    await user.click(screen.getByRole("button", { name: "Retry" }));

    expect(await screen.findByText("Cash Flow Calendar")).toBeInTheDocument();
    expect(apiMock.getCashFlowCalendar.mock.calls.length).toBeGreaterThan(1);
  });

  it("renders the whole window, however long", async () => {
    // A fixed cap of 80 weeks used to truncate the grid while the header and
    // the totals above it still covered the full range, so the heatmap silently
    // disagreed with the figures.
    render(page("/cash-flow-calendar?dateFrom=2024-01-01&dateTo=2025-12-31"));

    await screen.findByText("Cash Flow Calendar");
    // Two years is ~105 weeks; the old cap stopped at 80 (560 cells).
    expect(screen.getAllByRole("gridcell").length).toBeGreaterThan(700);
  });

  it("shows an empty state when there is no activity", async () => {
    apiMock.getCashFlowCalendar.mockResolvedValue(
      calendar({
        days: [],
        markers: [],
        cycles: [],
        totalIncome: 0,
        totalExpense: 0,
        net: 0,
        maxAbsNet: 0,
      }),
    );
    render(page());

    expect(
      await screen.findByText("No transactions in this range."),
    ).toBeInTheDocument();
  });
});
