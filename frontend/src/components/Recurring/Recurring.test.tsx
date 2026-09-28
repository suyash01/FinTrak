import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import Recurring from "./Recurring";
import type { Account, RecurringSeries } from "../../types";
import { formatOne } from "../../lib/currency";

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
  { id: "a1", name: "Checking", currency: "INR" },
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
      screen.getAllByText(formatOne(50000, "INR")).length,
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

// A series is denominated by the account it bills against, and this page spans
// every account the user has. So the monthly forecast can hold a rupee rent and
// a dollar subscription, and their sum is not a number that ever existed — the
// defect the whole per-currency change exists to remove, still live in the one
// place the app did its own arithmetic on money.
//
// The test names the wrong answer rather than only the right one: restoring the
// cross-currency sum makes the card print formatOne(51000, "INR"), and asserting
// that string is absent is the half that fails. Asserting only the refusal would
// pass just as well against a card that printed the sum and said so afterwards.
describe("Recurring's monthly forecast across two currencies", () => {
  const twoCurrencyAccounts = [
    { id: "a1", name: "Checking", currency: "INR" },
    { id: "a2", name: "Travel card", currency: "USD" },
  ] as unknown as Account[];

  const mixed = [
    {
      ...series[0],
      id: "s1",
      accountId: "a1",
      accountName: "Checking",
      type: "debit",
      monthlyAmount: 50000,
    },
    {
      ...series[0],
      id: "s3",
      accountId: "a2",
      accountName: "Travel card",
      name: "Streaming",
      type: "debit",
      monthlyAmount: 10,
    },
  ] as unknown as RecurringSeries[];

  it("refuses the total rather than adding rupees to dollars", async () => {
    domainMock.useDomainData.mockReturnValue({
      accounts: twoCurrencyAccounts,
      categories: [],
      payees: [],
    });
    apiMock.getRecurringSeries.mockResolvedValue({ data: mixed });

    renderPage();
    await screen.findByText("Streaming");

    const card = screen.getByText("Monthly expenses").closest("div")!;
    const text = card.textContent ?? "";
    // The rupee figure and the dollar figure, each with its own code, and the
    // explicit refusal — never one number standing for the two.
    expect(text).toContain("INR");
    expect(text).toContain("USD");
    expect(text).toContain("not combined");

    // The restored sum: the two figures added together and printed as one, which
    // is what the fold used to do.
    expect(screen.queryByText(formatOne(50010, "INR"))).toBeNull();
  });

  it("takes the net per currency and refuses to sign it", async () => {
    domainMock.useDomainData.mockReturnValue({
      accounts: twoCurrencyAccounts,
      categories: [],
      payees: [],
    });
    apiMock.getRecurringSeries.mockResolvedValue({
      data: [
        mixed[0],
        {
          ...mixed[1],
          id: "s4",
          type: "credit",
          name: "Payout",
          monthlyAmount: 20,
        },
      ] as unknown as RecurringSeries[],
    });

    renderPage();
    await screen.findByText("Payout");

    const card = screen.getByText("Net per month").closest("div")!;
    // A per-currency difference: USD came out 10 positive, INR 50,000 negative,
    // and the figure says both rather than one of them.
    expect(card.textContent).toContain("not combined");
    // And it is drawn in no sign's colour, because two currencies disagreeing
    // about which way the month went is exactly the state a sign cannot express.
    const figure = screen.getByText((_, el) => el?.tagName === "P" && /not combined/.test(el.textContent ?? ""))!;
    expect(figure.className).toContain("text-muted-foreground");
    // The restored difference, taken across the two sums.
    expect(screen.queryByText(formatOne(-49980, "INR"))).toBeNull();
  });
});
