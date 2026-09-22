import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import RecurringDetail from "./RecurringDetail";
import type {
  Account,
  RecurringForecastItem,
  RecurringSeries,
} from "../../types";

if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

const { apiMock } = vi.hoisted(() => ({
  apiMock: {
    getRecurringForecast: vi.fn(),
    getRecurringSuggestions: vi.fn(),
    getRecurringTransactions: vi.fn(),
    getRecurringTerms: vi.fn(),
    attachRecurring: vi.fn(),
    detachRecurring: vi.fn(),
  },
}));

vi.mock("../../api/client", () => ({ default: apiMock }));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

const accounts = [
  { id: "a1", name: "Checking", accountTypeId: "bank" },
] as unknown as Account[];

const series = {
  id: "s1",
  accountId: "a1",
  name: "Netflix",
  description: "",
  amount: 1599,
  type: "debit",
  frequency: "monthly",
  interval: 1,
  startDate: "2099-01-15",
  endDate: null,
  categoryId: null,
  payeeId: null,
  active: true,
  notes: "",
  monthlyAmount: 1599,
  attachedCount: 1,
} as unknown as RecurringSeries;

function renderDetail() {
  const onChanged = vi.fn();
  render(
    <RecurringDetail
      series={series}
      open
      onOpenChange={vi.fn()}
      accounts={accounts}
      onChanged={onChanged}
    />,
  );
  return { onChanged };
}

beforeEach(() => {
  vi.clearAllMocks();
  apiMock.getRecurringForecast.mockResolvedValue({
    data: [
      { date: "2099-01-15", amount: 1599, type: "debit", matched: true },
      { date: "2099-02-15", amount: 1599, type: "debit", matched: false },
    ],
  });
  apiMock.getRecurringSuggestions.mockResolvedValue({
    data: [
      {
        txn: {
          id: "t1",
          accountId: "a1",
          description: "NETFLIX SUB",
          date: "2099-01-14",
          amount: 1599,
          type: "debit",
        },
        score: 95,
        occurrenceDate: "2099-01-15",
        daysOff: 1,
      },
    ],
  });
  apiMock.getRecurringTransactions.mockResolvedValue({
    data: [
      {
        id: "t2",
        accountId: "a1",
        description: "OLD NETFLIX",
        date: "2098-12-15",
        amount: 1599,
        type: "debit",
      },
    ],
  });
  apiMock.getRecurringTerms.mockResolvedValue({
    data: [
      {
        id: "term1",
        seriesId: "s1",
        startDate: "2099-01-15",
        endDate: "2099-06-01",
        amount: 1599,
        accountId: "a1",
        accountName: "Checking",
      },
      {
        id: "term2",
        seriesId: "s1",
        startDate: "2099-06-01",
        endDate: null,
        amount: 1999,
        accountId: "a1",
        accountName: "Checking",
      },
    ],
  });
  apiMock.attachRecurring.mockResolvedValue({ attached: 1 });
  apiMock.detachRecurring.mockResolvedValue({ detached: 1 });
});

describe("RecurringDetail", () => {
  it("shows the forecast with matched occurrences", async () => {
    renderDetail();
    expect(await screen.findByText("Matched")).toBeInTheDocument();
  });

  it("ignores a superseded series' late response", async () => {
    const other = { ...series, id: "s2", name: "Spotify" };
    // tsconfig targets ES2022, so Promise.withResolvers is not available here.
    let landFirst: (value: { data: RecurringForecastItem[] }) => void = () => {};
    const firstResponse = new Promise<{ data: RecurringForecastItem[] }>(
      (resolve) => {
        landFirst = resolve;
      },
    );
    apiMock.getRecurringForecast.mockImplementation((id: string) =>
      id === series.id
        ? firstResponse
        : Promise.resolve({
            data: [
              { date: "2099-04-15", amount: 999, type: "debit", matched: false },
            ],
          }),
    );

    const view = (s: RecurringSeries) => (
      <RecurringDetail
        series={s}
        open
        onOpenChange={vi.fn()}
        accounts={accounts}
        onChanged={vi.fn()}
      />
    );
    const { rerender } = render(view(series));
    rerender(view(other as RecurringSeries));

    expect(await screen.findByText("15 Apr 2099")).toBeInTheDocument();

    // The first series' forecast lands after the user moved on: it must not
    // replace what is on screen, or "Link" would post its transaction id with
    // the newly opened series' id.
    landFirst({
      data: [{ date: "2099-03-15", amount: 1599, type: "debit", matched: true }],
    });
    await waitFor(() =>
      expect(screen.queryByText("15 Mar 2099")).not.toBeInTheDocument(),
    );
    expect(screen.getByText("15 Apr 2099")).toBeInTheDocument();
  });

  it("links a suggested transaction", async () => {
    const user = userEvent.setup();
    const { onChanged } = renderDetail();

    await user.click(await screen.findByRole("tab", { name: /Suggestions/ }));
    expect(await screen.findByText("NETFLIX SUB")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Link" }));

    await waitFor(() =>
      expect(apiMock.attachRecurring).toHaveBeenCalledWith({
        seriesId: "s1",
        transactionIds: ["t1"],
      }),
    );
    expect(onChanged).toHaveBeenCalled();
  });

  it("unlinks an attached transaction", async () => {
    const user = userEvent.setup();
    const { onChanged } = renderDetail();

    await user.click(await screen.findByRole("tab", { name: /Linked/ }));
    expect(await screen.findByText("OLD NETFLIX")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Unlink" }));

    await waitFor(() =>
      expect(apiMock.detachRecurring).toHaveBeenCalledWith({
        transactionIds: ["t2"],
      }),
    );
    expect(onChanged).toHaveBeenCalled();
  });

  it("shows the account/amount date ranges", async () => {
    const user = userEvent.setup();
    renderDetail();

    await user.click(await screen.findByRole("tab", { name: /Ranges/ }));
    expect(
      await screen.findByText("15 Jan 2099 – 01 Jun 2099 · Checking"),
    ).toBeInTheDocument();
    expect(screen.getByText("01 Jun 2099 onward · Checking")).toBeInTheDocument();
  });
});
