import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import Recurring from "./Recurring";
import type { Account, RecurringSeries } from "../../types";
import { formatCurrency } from "../../utils/formatters";

if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

const { apiMock, domainMock } = vi.hoisted(() => ({
  apiMock: {
    getRecurringSeries: vi.fn(),
    createRecurringSeries: vi.fn(),
    updateRecurringSeries: vi.fn(),
    deleteRecurringSeries: vi.fn(),
    getRecurringForecast: vi.fn(),
    getRecurringSuggestions: vi.fn(),
    getRecurringTransactions: vi.fn(),
    getRecurringTerms: vi.fn(),
    attachRecurring: vi.fn(),
    detachRecurring: vi.fn(),
    createRecurringTerm: vi.fn(),
    updateRecurringTerm: vi.fn(),
    deleteRecurringTerm: vi.fn(),
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
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

const accounts = [
  { id: "a1", name: "Checking" },
] as unknown as Account[];

const series = [
  {
    id: "s1",
    accountId: "a1",
    name: "Rent",
    description: "",
    amount: 50000,
    type: "debit",
    frequency: "monthly",
    interval: 1,
    startDate: "2026-01-01",
    endDate: null,
    categoryId: null,
    payeeId: null,
    active: true,
    notes: "",
    accountName: "Checking",
    monthlyAmount: 50000,
    attachedCount: 3,
    nextDueDate: "2026-10-01",
  },
  {
    id: "s2",
    accountId: "a1",
    name: "Salary",
    description: "",
    amount: 100000,
    type: "credit",
    frequency: "monthly",
    interval: 1,
    startDate: "2026-01-01",
    endDate: null,
    categoryId: null,
    payeeId: null,
    active: true,
    notes: "",
    accountName: "Checking",
    monthlyAmount: 100000,
    attachedCount: 0,
    nextDueDate: "2026-10-01",
  },
] as unknown as RecurringSeries[];

function setDomain() {
  domainMock.useDomainData.mockReturnValue({
    accounts,
    categories: [],
    payees: [],
  });
}

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/recurring"]}>
      <Recurring />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  setDomain();
  apiMock.getRecurringSeries.mockResolvedValue({ data: series });
  apiMock.deleteRecurringSeries.mockResolvedValue(null);
});

describe("Recurring", () => {
  it("shows the empty state when there are no series", async () => {
    apiMock.getRecurringSeries.mockResolvedValue({ data: [] });
    renderPage();
    expect(await screen.findByText("No recurring series yet")).toBeInTheDocument();
  });

  it("renders series rows and the monthly summary", async () => {
    renderPage();
    expect(await screen.findByText("Rent")).toBeInTheDocument();
    expect(screen.getByText("Salary")).toBeInTheDocument();
    expect(screen.getByText("Monthly expenses")).toBeInTheDocument();
    expect(screen.getByText("Monthly income")).toBeInTheDocument();
    // Expense summary equals the rent's normalized monthly amount.
    expect(
      screen.getAllByText(formatCurrency(50000)).length,
    ).toBeGreaterThanOrEqual(1);
  });

  it("deletes a series after confirmation", async () => {
    const user = userEvent.setup();
    renderPage();
    const row = (await screen.findByText("Rent")).closest("tr")!;
    await user.click(within(row).getByLabelText("Delete Rent"));

    expect(await screen.findByText("Delete recurring series?")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Delete" }));

    await waitFor(() =>
      expect(apiMock.deleteRecurringSeries).toHaveBeenCalledWith("s1"),
    );
  });

  it("opens the create dialog", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole("button", { name: "Add Series" }));
    expect(await screen.findByText("Add Recurring Series")).toBeInTheDocument();
  });
});
