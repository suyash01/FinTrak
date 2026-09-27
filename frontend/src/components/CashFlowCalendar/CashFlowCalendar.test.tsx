import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import CashFlowCalendar from "./CashFlowCalendar";
import { formatOne } from "../../lib/currency";
import type {
  CashFlowCalendar as CashFlowCalendarData,
  CurrencyScope,
} from "../../types";

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

function oneCurrencyScope(): CurrencyScope {
  return {
    currencies: ["INR"],
    accounts: [
      {
        id: "a1",
        name: "Checking",
        currency: "INR",
        income: { INR: 5000 },
        expense: { INR: 1500 },
      },
    ],
  };
}

function calendar(
  overrides: Partial<CashFlowCalendarData> = {},
): CashFlowCalendarData {
  return {
    days: [
      {
        date: "2024-06-03",
        income: { INR: 5000 },
        expense: { INR: 0 },
        net: { INR: 5000 },
        count: 1,
      },
      {
        date: "2024-06-04",
        income: { INR: 0 },
        expense: { INR: 1500 },
        net: { INR: -1500 },
        count: 2,
      },
    ],
    markers: [
      {
        date: "2024-06-04",
        label: "Running balance",
        kind: "balance",
        amount: { INR: -1500 },
      },
    ],
    cycles: [],
    totalIncome: { INR: 5000 },
    totalExpense: { INR: 1500 },
    net: { INR: 3500 },
    maxAbsNet: { INR: 5000 },
    currencyScope: oneCurrencyScope(),
    ...overrides,
  };
}

// twoCurrencyCalendar is the case this change exists for: a window with a
// domestic surplus and deficit alongside a day spent entirely in dollars, on a
// much larger scale. Nothing in the response may combine the two.
function twoCurrencyCalendar(): CashFlowCalendarData {
  const dollars = {
    id: "a2",
    name: "Dollars",
    currency: "USD",
    income: { USD: 9000 },
    expense: { USD: 9060 },
  };
  return calendar({
    days: [
      {
        date: "2024-06-03",
        income: { INR: 5000 },
        expense: { INR: 0 },
        net: { INR: 5000 },
        count: 1,
      },
      {
        date: "2024-06-04",
        income: { INR: 0 },
        expense: { INR: 1500 },
        net: { INR: -1500 },
        count: 2,
      },
      {
        date: "2024-06-05",
        income: { USD: 9000 },
        expense: { USD: 9060 },
        net: { USD: -60 },
        count: 2,
      },
    ],
    totalIncome: { INR: 5000, USD: 9000 },
    totalExpense: { INR: 1500, USD: 9060 },
    net: { INR: 3500, USD: -60 },
    maxAbsNet: { INR: 5000, USD: 60 },
    currencyScope: {
      currencies: ["INR", "USD"],
      accounts: [...oneCurrencyScope().accounts, dollars],
    },
  });
}

function page(entry = "/cash-flow-calendar?dateFrom=2024-06-03&dateTo=2024-06-10") {  return (
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
    expect(screen.getByText(formatOne(5000, "INR"))).toBeInTheDocument();
    expect(screen.getByText(formatOne(1500, "INR"))).toBeInTheDocument();
    expect(screen.getByText(formatOne(3500, "INR"))).toBeInTheDocument();
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
            outstanding: { INR: 0 },
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
        totalIncome: { INR: 0 },
        totalExpense: { INR: 0 },
        net: { INR: 0 },
        maxAbsNet: { INR: 0 },
      }),
    );
    render(page());

    expect(
      await screen.findByText("No transactions in this range."),
    ).toBeInTheDocument();
  });

  it("shows one currency at a time and names the one it is not showing", async () => {
    const user = userEvent.setup();
    apiMock.getCashFlowCalendar.mockResolvedValue(twoCurrencyCalendar());
    render(page());

    expect(await screen.findByText("Cash Flow Calendar")).toBeInTheDocument();
    expect(screen.getByText(formatOne(5000, "INR"))).toBeInTheDocument();
    expect(screen.queryByText(formatOne(14000, "INR"))).not.toBeInTheDocument();
    expect(screen.getByText("1 other currency")).toBeInTheDocument();
    expect(
      screen.getByText(
        /USD .*\(Dollars\)\. These are not added to the figures above\./,
      ),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("combobox", { name: "Currency" }));
    await user.click(await screen.findByRole("option", { name: "USD" }));

    expect(await screen.findByText(formatOne(9000, "USD"))).toBeInTheDocument();
    expect(screen.queryByText(formatOne(5000, "INR"))).not.toBeInTheDocument();
  });

  it("scales the heatmap by the selected currency and leaves a foreign day flat", async () => {
    apiMock.getCashFlowCalendar.mockResolvedValue(twoCurrencyCalendar());
    render(page());

    // 3 June's rupee surplus is the largest INR net in the window, so it fills
    // the colour scale. Sizing by the dollars instead — the largest magnitude in
    // the payload — would leave it barely tinted.
    const domestic = await screen.findByRole("gridcell", {
      name: /03 Jun 2024: net INR ₹5,000\.00/,
    });
    expect(domestic.getAttribute("style")).toContain("var(--chart-3) 90%");

    // A day spent only in dollars has no figure in the currency on screen, so
    // it stays flat rather than being tinted from a currency the user is not
    // looking at.
    const foreign = screen.getByRole("gridcell", {
      name: /05 Jun 2024: net INR ₹0\.00/,
    });
    expect(foreign.getAttribute("style")).toBe(
      "background-color: var(--muted);",
    );
  });

  it("does not colour a day the selected currency never held as a surplus", async () => {
    // The same foreign day, read from the selected-day panel: a real row with
    // no figure in the currency on screen, whose net must claim no sign at all.
    apiMock.getCashFlowCalendar.mockResolvedValue(twoCurrencyCalendar());
    render(page());

    // fireEvent rather than userEvent: this is about the panel, and a real click
    // would also open the hover tooltip with a second Net row to disambiguate.
    fireEvent.click(
      await screen.findByRole("gridcell", { name: /05 Jun 2024: net INR/ }),
    );

    const net = within(await screen.findByText("Net")).getByText(
      formatOne(0, "INR"),
    );
    expect(net).toHaveClass("text-muted-foreground");
    expect(net).not.toHaveClass("text-chart-3");
  });
});
