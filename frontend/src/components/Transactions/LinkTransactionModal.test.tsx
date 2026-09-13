import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import LinkTransactionModal from "./LinkTransactionModal";
import type { Account, Link, Transaction } from "../../types";

if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

const { apiMock, domainMock, toastMock } = vi.hoisted(() => ({
  apiMock: {
    getLinks: vi.fn(),
    getTransactions: vi.fn(),
    createLink: vi.fn(),
    deleteLink: vi.fn(),
  },
  domainMock: { useDomainData: vi.fn() },
  toastMock: { error: vi.fn(), success: vi.fn() },
}));

vi.mock("../../api/client", () => ({ default: apiMock }));
vi.mock("../../context/DomainDataContext", () => ({
  useDomainData: domainMock.useDomainData,
}));
vi.mock("sonner", () => ({ toast: toastMock }));

const accounts: Account[] = [
  {
    id: "a1",
    name: "Checking",
    accountTypeId: "bank",
    accountTypeName: "Bank",
    bank: "",
    currency: "INR",
    color: "#000000",
    isDefault: true,
    closed: false,
    balance: 0,
  },
  {
    id: "a2",
    name: "Savings",
    accountTypeId: "bank",
    accountTypeName: "Bank",
    bank: "",
    currency: "INR",
    color: "#000000",
    isDefault: false,
    closed: false,
    balance: 0,
  },
];

const source: Transaction = {
  id: "s1",
  accountId: "a1",
  date: "2024-03-15",
  description: "Source Txn",
  amount: 100,
  type: "debit",
  accountName: "Checking",
};

const sameAccount: Transaction = {
  id: "t2",
  accountId: "a1",
  date: "2024-03-14",
  description: "Same Account Txn",
  amount: 100,
  type: "credit",
  accountName: "Checking",
};

const differentAccount: Transaction = {
  id: "t1",
  accountId: "a2",
  date: "2024-03-16",
  description: "Target Txn",
  amount: 100,
  type: "credit",
  accountName: "Savings",
};

const existingLink: Link = {
  id: "l1",
  type: "transfer",
  fromTxnId: "s1",
  toTxnId: "t1",
  fromTxn: source,
  toTxn: differentAccount,
};

function renderModal(overrides: Record<string, unknown> = {}) {
  const props = {
    txn: source,
    onClose: vi.fn(),
    onSuccess: vi.fn(),
    ...overrides,
  };
  const view = render(<LinkTransactionModal {...props} />);
  return { ...view, props };
}

function lastParams(): Record<string, unknown> {
  return apiMock.getTransactions.mock.calls.at(-1)?.[0] as Record<
    string,
    unknown
  >;
}

beforeEach(() => {
  vi.clearAllMocks();
  domainMock.useDomainData.mockReturnValue({ accounts });
  apiMock.getLinks.mockResolvedValue([]);
  apiMock.getTransactions.mockResolvedValue({
    data: [source, sameAccount, differentAccount],
    total: 3,
    page: 1,
    pages: 1,
  });
  apiMock.createLink.mockResolvedValue({});
  apiMock.deleteLink.mockResolvedValue(null);
});

describe("LinkTransactionModal", () => {
  it("runs an initial search and hides the source and same-account rows", async () => {
    renderModal();

    expect(await screen.findByText("Target Txn")).toBeInTheDocument();
    expect(screen.queryByText("Same Account Txn")).toBeNull();
    expect(screen.getAllByRole("button", { name: /Choose Type/ })).toHaveLength(
      1,
    );
    expect(lastParams()).toMatchObject({
      amount: 100,
      limit: 20,
      dateFrom: "2024-03-12",
      dateTo: "2024-03-18",
    });
  });

  it("shows the manage-links title when links already exist", async () => {
    apiMock.getLinks.mockResolvedValue([existingLink]);
    renderModal();

    expect(await screen.findByText("Manage Links")).toBeInTheDocument();
    expect(screen.getByText("Linked Transactions (1)")).toBeInTheDocument();
  });

  it("re-searches when the search term is submitted", async () => {
    const user = userEvent.setup();
    renderModal();
    await screen.findByText("Target Txn");

    await user.type(
      screen.getByPlaceholderText("Search description..."),
      "coffee",
    );
    await user.click(screen.getByRole("button", { name: /Find Match/ }));

    await waitFor(() => expect(lastParams().search).toBe("coffee"));
  });

  it("drops the amount filter when Match Amount is turned off", async () => {
    const user = userEvent.setup();
    renderModal();
    await screen.findByText("Target Txn");

    await user.click(screen.getByRole("checkbox", { name: "Match Amount" }));

    await waitFor(() => expect(lastParams().amount).toBeUndefined());
  });

  it("re-searches when the account filter changes", async () => {
    const user = userEvent.setup();
    renderModal();
    await screen.findByText("Target Txn");

    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByRole("option", { name: "Savings" }));

    await waitFor(() => expect(lastParams().accountId).toBe("a2"));
  });

  it("creates a transfer link for a cross-account pair", async () => {
    const user = userEvent.setup();
    const { props } = renderModal();
    await screen.findByText("Target Txn");

    await user.click(
      screen.getByRole("button", { name: /Choose Type/ }),
    );
    expect(await screen.findByText("Choose Link Type")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Confirm Link" }));

    await waitFor(() =>
      expect(apiMock.createLink).toHaveBeenCalledWith({
        type: "transfer",
        fromTxnId: "s1",
        toTxnId: "t1",
      }),
    );
    expect(props.onSuccess).toHaveBeenCalled();
  });

  it("defaults same-account pairs to cashback", async () => {
    const user = userEvent.setup();
    renderModal();
    await screen.findByText("Target Txn");

    await user.click(
      screen.getByRole("checkbox", { name: "Different Account Only" }),
    );
    const sameDesc = await screen.findByText("Same Account Txn");
    const row = sameDesc.parentElement!.parentElement!;

    await user.click(
      within(row).getByRole("button", { name: /Choose Type/ }),
    );
    await screen.findByText("Choose Link Type");
    await user.click(screen.getByRole("button", { name: "Confirm Link" }));

    await waitFor(() =>
      expect(apiMock.createLink).toHaveBeenCalledWith({
        type: "cashback",
        fromTxnId: "s1",
        toTxnId: "t2",
      }),
    );
  });

  it("surfaces a create-link failure as an error toast", async () => {
    const user = userEvent.setup();
    apiMock.createLink.mockRejectedValue(new Error("boom"));
    renderModal();
    await screen.findByText("Target Txn");

    await user.click(screen.getByRole("button", { name: /Choose Type/ }));
    await screen.findByText("Choose Link Type");
    await user.click(screen.getByRole("button", { name: "Confirm Link" }));

    await waitFor(() =>
      expect(toastMock.error).toHaveBeenCalledWith("boom"),
    );
  });

  it("removes an existing link after confirmation", async () => {
    const user = userEvent.setup();
    apiMock.getLinks.mockResolvedValue([existingLink]);
    const { props } = renderModal();
    await screen.findByText("Linked Transactions (1)");

    await user.click(screen.getByTitle("Unlink"));
    expect(await screen.findByText("Remove this link?")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Remove" }));

    await waitFor(() => expect(apiMock.deleteLink).toHaveBeenCalledWith("l1"));
    expect(apiMock.getLinks).toHaveBeenCalledTimes(2);
    expect(props.onSuccess).toHaveBeenCalled();
  });

  it("goes back from the link type step", async () => {
    const user = userEvent.setup();
    renderModal();
    await screen.findByText("Target Txn");

    await user.click(screen.getByRole("button", { name: /Choose Type/ }));
    await screen.findByText("Choose Link Type");

    await user.click(screen.getByRole("button", { name: "Back to Results" }));

    expect(await screen.findByText("Target Txn")).toBeInTheDocument();
    expect(screen.queryByText("Choose Link Type")).toBeNull();
  });
});
