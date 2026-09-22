import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import RecurringFormDialog from "./RecurringFormDialog";
import type {
  Account,
  RecurringSeries,
  RecurringSeriesTerm,
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
    createRecurringSeries: vi.fn(),
    updateRecurringSeries: vi.fn(),
    getRecurringTerms: vi.fn(),
  },
}));

vi.mock("../../api/client", () => ({ default: apiMock }));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

const accounts = [
  { id: "a1", name: "Checking", accountTypeId: "bank" },
] as unknown as Account[];

function renderDialog(series: RecurringSeries | null) {
  const onSaved = vi.fn();
  const onOpenChange = vi.fn();
  render(
    <RecurringFormDialog
      open
      onOpenChange={onOpenChange}
      series={series}
      accounts={accounts}
      categories={[]}
      payees={[]}
      onSaved={onSaved}
    />,
  );
  return { onSaved, onOpenChange };
}

beforeEach(() => {
  vi.clearAllMocks();
  apiMock.createRecurringSeries.mockResolvedValue({ id: "new" });
  apiMock.updateRecurringSeries.mockResolvedValue({ id: "s1" });
  apiMock.getRecurringTerms.mockResolvedValue({ data: [] });
});

describe("RecurringFormDialog — default entry dates", () => {
  // A new entry starts on the local day: `toISOString()` is UTC, so east of UTC
  // (the app's +05:30 target) it would start the series a day early for the
  // first hours of every local day. The zone is pinned so the assertion holds
  // on any host.
  const hostTz = process.env.TZ;
  beforeEach(() => {
    process.env.TZ = "Asia/Kolkata";
  });
  afterEach(() => {
    vi.useRealTimers();
    process.env.TZ = hostTz;
  });

  it("starts a new entry on the local day, not the UTC day", () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    // 02:00 IST on the 16th is 2026-03-15T20:30Z: the UTC day is still the 15th.
    vi.setSystemTime(new Date("2026-03-15T20:30:00Z"));

    renderDialog(null);

    expect(screen.getByLabelText("Entry 1 start")).toHaveValue("2026-03-16");
  });

  it("starts an added entry on the local day too", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.setSystemTime(new Date("2026-03-15T20:30:00Z"));
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });

    renderDialog(null);
    await user.click(screen.getByRole("button", { name: "Add entry" }));

    expect(screen.getByLabelText("Entry 2 start")).toHaveValue("2026-03-16");
  });
});

describe("RecurringFormDialog", () => {
  it("creates a series with a range", async () => {
    const user = userEvent.setup();
    const { onSaved, onOpenChange } = renderDialog(null);

    await user.type(
      screen.getByPlaceholderText("e.g. Netflix, Rent, Salary"),
      "Netflix",
    );
    await user.type(screen.getByLabelText("Entry 1 amount"), "15.99");
    await user.click(screen.getByRole("button", { name: "Create Series" }));

    await waitFor(() =>
      expect(apiMock.createRecurringSeries).toHaveBeenCalledWith(
        expect.objectContaining({
          name: "Netflix",
          type: "debit",
          frequency: "monthly",
          ranges: [
            expect.objectContaining({ amount: 15.99, accountId: "a1" }),
          ],
        }),
      ),
    );
    expect(onSaved).toHaveBeenCalled();
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("submits the whole range list", async () => {
    const user = userEvent.setup();
    renderDialog(null);

    await user.type(
      screen.getByPlaceholderText("e.g. Netflix, Rent, Salary"),
      "Rent",
    );
    await user.type(screen.getByLabelText("Entry 1 amount"), "10");
    await user.click(screen.getByRole("button", { name: "Add entry" }));
    await user.type(screen.getByLabelText("Entry 2 amount"), "12");
    await user.click(screen.getByRole("button", { name: "Create Series" }));

    await waitFor(() =>
      expect(apiMock.createRecurringSeries).toHaveBeenCalledWith(
        expect.objectContaining({
          ranges: [
            expect.objectContaining({ amount: 10 }),
            expect.objectContaining({ amount: 12 }),
          ],
        }),
      ),
    );
  });

  it("ignores a superseded series' terms", async () => {
    const seriesA = {
      id: "s1",
      accountId: "a1",
      name: "Rent",
      description: "",
      amount: 1000,
      type: "debit",
      frequency: "monthly",
      interval: 1,
      startDate: "2099-01-15",
      endDate: null,
      categoryId: null,
      payeeId: null,
      active: true,
      notes: "",
      monthlyAmount: 1000,
      attachedCount: 0,
    } as unknown as RecurringSeries;
    const seriesB = { ...seriesA, id: "s2", name: "Spotify" } as RecurringSeries;

    // tsconfig targets ES2022, so Promise.withResolvers is not available here.
    let landFirst: (value: { data: RecurringSeriesTerm[] }) => void = () => {};
    const slowFirst = new Promise<{ data: RecurringSeriesTerm[] }>((resolve) => {
      landFirst = resolve;
    });
    apiMock.getRecurringTerms.mockImplementation((id: string) =>
      id === "s1"
        ? slowFirst
        : Promise.resolve({
            data: [
              {
                id: "t2",
                seriesId: "s2",
                startDate: "2099-04-01",
                endDate: null,
                amount: 777,
                accountId: "a1",
                accountName: "Checking",
              },
            ],
          }),
    );

    const view = (s: RecurringSeries) => (
      <RecurringFormDialog
        open
        onOpenChange={vi.fn()}
        series={s}
        accounts={accounts}
        categories={[]}
        payees={[]}
        onSaved={vi.fn()}
      />
    );
    const { rerender } = render(view(seriesA));
    rerender(view(seriesB));

    const firstAmount = () =>
      (screen.getByLabelText("Entry 1 amount") as HTMLInputElement).value;
    await waitFor(() => expect(firstAmount()).toBe("777"));

    // The first series' ranges land after the user moved on: filling this form
    // with them would rewrite the newly opened series' history on save, because
    // the update replaces the whole range list. Flush the response before
    // asserting, so a late write cannot slip in after the test ends.
    landFirst({
      data: [
        {
          id: "t1",
          seriesId: "s1",
          startDate: "2099-01-01",
          endDate: null,
          amount: 1599,
          accountId: "a1",
          accountName: "Checking",
        },
      ],
    });
    await act(async () => {});
    expect(firstAmount()).toBe("777");
  });

  it("prefills ranges from the series and updates", async () => {
    const user = userEvent.setup();
    const existing = {
      id: "s1",
      accountId: "a1",
      name: "Rent",
      description: "",
      amount: 1000,
      type: "debit",
      frequency: "monthly",
      interval: 1,
      startDate: "2099-01-15",
      endDate: null,
      categoryId: null,
      payeeId: null,
      active: true,
      notes: "",
      monthlyAmount: 1000,
      attachedCount: 0,
    } as unknown as RecurringSeries;
    apiMock.getRecurringTerms.mockResolvedValue({
      data: [
        {
          id: "t1",
          seriesId: "s1",
          startDate: "2099-01-15",
          endDate: null,
          amount: 1000,
          accountId: "a1",
          accountName: "Checking",
        },
      ],
    });

    renderDialog(existing);
    expect(screen.getByText("Edit Recurring Series")).toBeInTheDocument();

    await waitFor(() =>
      expect(
        (screen.getByLabelText("Entry 1 amount") as HTMLInputElement).value,
      ).toBe("1000"),
    );

    const nameInput = screen.getByPlaceholderText(
      "e.g. Netflix, Rent, Salary",
    ) as HTMLInputElement;
    await user.clear(nameInput);
    await user.type(nameInput, "Rent Updated");
    await user.click(screen.getByRole("button", { name: "Save Changes" }));

    await waitFor(() =>
      expect(apiMock.updateRecurringSeries).toHaveBeenCalledWith(
        "s1",
        expect.objectContaining({
          name: "Rent Updated",
          ranges: [expect.objectContaining({ amount: 1000, accountId: "a1" })],
        }),
      ),
    );
  });
});
