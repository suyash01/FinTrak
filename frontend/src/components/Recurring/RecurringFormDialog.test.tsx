import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import RecurringFormDialog from "./RecurringFormDialog";
import type { Account, RecurringSeries } from "../../types";

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
