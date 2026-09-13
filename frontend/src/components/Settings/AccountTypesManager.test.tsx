import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";
import AccountTypesManager from "./AccountTypesManager";
import type { AccountType } from "../../types";

if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

const { apiMock, domainMock, refreshAccountTypes } = vi.hoisted(() => ({
  apiMock: {
    createAccountType: vi.fn(),
    updateAccountType: vi.fn(),
    deleteAccountType: vi.fn(),
  },
  domainMock: { useDomainData: vi.fn() },
  refreshAccountTypes: vi.fn(),
}));

vi.mock("../../api/client", () => ({ default: apiMock }));
vi.mock("../../context/DomainDataContext", () => ({
  useDomainData: domainMock.useDomainData,
}));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

const accountTypes: AccountType[] = [
  { id: "bank", name: "Bank", positiveTxnType: "credit" },
  { id: "cash", name: "Cash", positiveTxnType: "debit" },
];

function setDomain(types: AccountType[], loading = false) {
  domainMock.useDomainData.mockReturnValue({
    accountTypes: types,
    loading,
    refreshAccountTypes,
  });
}

function renderManager() {
  return render(<AccountTypesManager />);
}

beforeEach(() => {
  vi.clearAllMocks();
  setDomain(accountTypes);
  apiMock.createAccountType.mockResolvedValue(accountTypes[0]);
  apiMock.updateAccountType.mockResolvedValue(accountTypes[0]);
  apiMock.deleteAccountType.mockResolvedValue(null);
});

describe("AccountTypesManager", () => {
  it("shows a loading indicator while domain data loads", () => {
    setDomain([], true);
    renderManager();
    expect(screen.getByText("Loading...")).toBeInTheDocument();
  });

  it("renders the configured account types", () => {
    renderManager();
    expect(screen.getByText("Bank")).toBeInTheDocument();
    expect(screen.getByText("Cash")).toBeInTheDocument();
    expect(screen.getByText(/ID: bank/)).toBeInTheDocument();
    expect(screen.getByText("credit")).toBeInTheDocument();
    expect(screen.getByText("debit")).toBeInTheDocument();
  });

  it("still offers adding a type when the list is empty", () => {
    setDomain([]);
    renderManager();
    expect(screen.getByRole("button", { name: "Add Account Type" })).toBeInTheDocument();
  });

  it("submits the add account type form and refreshes", async () => {
    const user = userEvent.setup();
    renderManager();

    await user.click(screen.getByRole("button", { name: "Add Account Type" }));
    await user.type(screen.getByPlaceholderText("e.g. wallet, cash"), "wallet");
    await user.type(
      screen.getByPlaceholderText("e.g. Mobile Wallet"),
      "Mobile Wallet",
    );
    await user.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() =>
      expect(apiMock.createAccountType).toHaveBeenCalledWith({
        id: "wallet",
        name: "Mobile Wallet",
        positiveTxnType: "credit",
      }),
    );
    expect(refreshAccountTypes).toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Add Account Type" })).toBeInTheDocument();
  });

  it("submits the add account type form with a debit sign convention", async () => {
    const user = userEvent.setup();
    renderManager();

    await user.click(screen.getByRole("button", { name: "Add Account Type" }));
    await user.type(screen.getByPlaceholderText("e.g. wallet, cash"), "wallet");
    await user.type(
      screen.getByPlaceholderText("e.g. Mobile Wallet"),
      "Mobile Wallet",
    );
    await user.click(screen.getByRole("combobox"));
    await user.click(
      await screen.findByRole("option", {
        name: "Debit amounts increase balance",
      }),
    );
    await user.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() =>
      expect(apiMock.createAccountType).toHaveBeenCalledWith({
        id: "wallet",
        name: "Mobile Wallet",
        positiveTxnType: "debit",
      }),
    );
  });

  it("cancels creating an account type", async () => {
    const user = userEvent.setup();
    renderManager();

    await user.click(screen.getByRole("button", { name: "Add Account Type" }));
    expect(screen.getByPlaceholderText("e.g. wallet, cash")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Cancel" }));

    expect(screen.queryByPlaceholderText("e.g. wallet, cash")).toBeNull();
    expect(screen.getByRole("button", { name: "Add Account Type" })).toBeInTheDocument();
    expect(apiMock.createAccountType).not.toHaveBeenCalled();
  });

  it("edits an existing account type", async () => {
    const user = userEvent.setup();
    renderManager();

    await user.click(screen.getByRole("button", { name: "Edit Bank" }));
    const input = screen.getByDisplayValue("Bank");
    await user.clear(input);
    await user.type(input, "Banking");
    await user.click(screen.getByRole("button", { name: "Save account type" }));

    await waitFor(() =>
      expect(apiMock.updateAccountType).toHaveBeenCalledWith("bank", {
        name: "Banking",
        positiveTxnType: "credit",
      }),
    );
    expect(refreshAccountTypes).toHaveBeenCalled();
  });

  it("cancels editing without saving", async () => {
    const user = userEvent.setup();
    renderManager();

    await user.click(screen.getByRole("button", { name: "Edit Bank" }));
    await user.click(screen.getByRole("button", { name: "Cancel" }));

    expect(apiMock.updateAccountType).not.toHaveBeenCalled();
    expect(screen.getByText("Bank")).toBeInTheDocument();
  });

  it("deletes an account type after confirmation", async () => {
    const user = userEvent.setup();
    renderManager();

    await user.click(screen.getByRole("button", { name: "Delete Bank" }));
    expect(await screen.findByText("Delete account type?")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Delete" }));

    await waitFor(() =>
      expect(apiMock.deleteAccountType).toHaveBeenCalledWith("bank"),
    );
    expect(toast.success).toHaveBeenCalledWith("Account type deleted");
    expect(refreshAccountTypes).toHaveBeenCalled();
  });

  it("surfaces an error toast when creating fails", async () => {
    apiMock.createAccountType.mockRejectedValueOnce(new Error("Create failed"));
    const user = userEvent.setup();
    renderManager();

    await user.click(screen.getByRole("button", { name: "Add Account Type" }));
    await user.type(screen.getByPlaceholderText("e.g. wallet, cash"), "wallet");
    await user.type(
      screen.getByPlaceholderText("e.g. Mobile Wallet"),
      "Mobile Wallet",
    );
    await user.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Create failed"),
    );
    expect(refreshAccountTypes).not.toHaveBeenCalled();
  });

  it("surfaces an error toast when updating fails", async () => {
    apiMock.updateAccountType.mockRejectedValueOnce(new Error("Update failed"));
    const user = userEvent.setup();
    renderManager();

    await user.click(screen.getByRole("button", { name: "Edit Bank" }));
    await user.click(screen.getByRole("button", { name: "Save account type" }));

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Update failed"),
    );
  });

  it("surfaces an error toast when deleting fails", async () => {
    apiMock.deleteAccountType.mockRejectedValueOnce(new Error("Delete failed"));
    const user = userEvent.setup();
    renderManager();

    await user.click(screen.getByRole("button", { name: "Delete Bank" }));
    await user.click(await screen.findByRole("button", { name: "Delete" }));

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Delete failed"),
    );
    expect(toast.success).not.toHaveBeenCalled();
  });
});
