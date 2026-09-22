import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import Accounts from "./Accounts";
import type { Account, AccountType } from "../../types";

if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

const { apiMock, downloadCSV, domainMock, refreshPayees } = vi.hoisted(() => ({
  apiMock: {
    createAccount: vi.fn(),
    updateAccount: vi.fn(),
    deleteAccount: vi.fn(),
  },
  downloadCSV: vi.fn(),
  // The account handlers refresh the shared payee list, because the backend
  // keeps an account-linked payee in step with its account.
  refreshPayees: vi.fn(),
  domainMock: { useDomainData: vi.fn() },
}));

vi.mock("../../api/client", () => ({
  default: apiMock,
  downloadCSV,
}));
vi.mock("../../context/DomainDataContext", () => ({
  useDomainData: domainMock.useDomainData,
}));
vi.mock("../../context/SettingsContext", () => ({
  useSettings: () => ({ compactLayout: false }),
}));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

function account(overrides: Partial<Account> = {}): Account {
  return {
    id: "a1",
    name: "Alpha",
    accountTypeId: "bank",
    accountTypeName: "Bank",
    bank: "",
    currency: "INR",
    color: "#000000",
    isDefault: false,
    closed: false,
    balance: 0,
    billingDay: null,
    ...overrides,
  };
}

const accounts: Account[] = [
  account({ id: "a1", name: "Zeta", closed: true }),
  account({ id: "a2", name: "Alpha" }),
  account({ id: "a3", name: "Beta" }),
];

const accountTypes = [
  { id: "bank", name: "Bank", positiveTxnType: "credit" },
] as unknown as AccountType[];

const setAccounts = vi.fn();

function setDomain(list: Account[]) {
  domainMock.useDomainData.mockReturnValue({
    accounts: list,
    accountTypes,
    setAccounts,
    refreshPayees,
  });
}

function renderedNames(): string[] {
  return screen
    .getAllByRole("row")
    .slice(1)
    .map((row) => {
      const cell = within(row).getAllByRole("cell")[0];
      return cell.querySelector("span.font-medium")?.textContent ?? "";
    });
}

beforeEach(() => {
  vi.clearAllMocks();
  setDomain(accounts);
  apiMock.updateAccount.mockImplementation(async (id: string, payload: Partial<Account>) => ({
    ...accounts.find((a) => a.id === id),
    ...payload,
  }));
  apiMock.deleteAccount.mockResolvedValue({ transactionsDeleted: 0 });
});

describe("Accounts", () => {
  it("shows the empty state when there are no accounts", () => {
    setDomain([]);
    render(<MemoryRouter><Accounts /></MemoryRouter>);
    expect(screen.getByText("No Accounts Yet")).toBeInTheDocument();
  });

  it("sorts by name and puts closed accounts last, then reverses", async () => {
    const user = userEvent.setup();
    render(<MemoryRouter><Accounts /></MemoryRouter>);

    expect(renderedNames()).toEqual(["Alpha", "Beta", "Zeta"]);

    await user.click(screen.getByText("Name"));
    expect(renderedNames()).toEqual(["Beta", "Alpha", "Zeta"]);
  });

  it("filters to closed accounts only", async () => {
    const user = userEvent.setup();
    render(<MemoryRouter><Accounts /></MemoryRouter>);

    const statusTrigger = Array.from(
      document.querySelectorAll<HTMLElement>('[role="combobox"]'),
    ).find((t) => t.textContent?.includes("All Statuses"));
    expect(statusTrigger).toBeTruthy();
    await user.click(statusTrigger!);
    await user.click(await screen.findByRole("option", { name: "Closed" }));

    await waitFor(() => expect(renderedNames()).toEqual(["Zeta"]));
  });

  it("marks an account as the default", async () => {
    const user = userEvent.setup();
    render(<MemoryRouter><Accounts /></MemoryRouter>);

    const betaRow = screen.getByText("Beta").closest("tr")!;
    await user.click(
      within(betaRow).getByTitle("Set as default account"),
    );

    await waitFor(() =>
      expect(apiMock.updateAccount).toHaveBeenCalledWith(
        "a3",
        expect.objectContaining({ isDefault: true }),
      ),
    );
  });

  it("deletes an account after confirmation", async () => {
    const user = userEvent.setup();
    render(<MemoryRouter><Accounts /></MemoryRouter>);

    const betaRow = screen.getByText("Beta").closest("tr")!;
    await user.click(within(betaRow).getByTitle("Delete account"));

    expect(
      await screen.findByText("Delete Beta?"),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Delete" }));

    await waitFor(() =>
      expect(apiMock.deleteAccount).toHaveBeenCalledWith("a3"),
    );
    // The account's linked payee went with it, so the payee list is refetched
    // instead of keeping a phantom row for the rest of the session.
    await waitFor(() => expect(refreshPayees).toHaveBeenCalled());
  });
});
