import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import LinkedTransactionsList from "./LinkedTransactionsList";
import type { Link, Transaction } from "../../types";

function txn(
  id: string,
  description: string,
  accountName: string,
  txnType: "debit" | "credit",
): Transaction {
  return {
    id,
    accountId: `acct-${id}`,
    date: "2024-03-15",
    description,
    amount: 100,
    type: txnType,
    accountName,
  };
}

const outgoing: Link = {
  id: "l1",
  type: "transfer",
  fromTxnId: "s1",
  toTxnId: "o1",
  fromTxn: txn("s1", "Source Txn", "Checking", "debit"),
  toTxn: txn("o1", "Outgoing Target", "Savings", "credit"),
};

const incoming: Link = {
  id: "l2",
  type: "refund",
  fromTxnId: "o2",
  toTxnId: "s1",
  fromTxn: txn("o2", "Incoming Refund", "Savings", "credit"),
  toTxn: txn("s1", "Source Txn", "Checking", "debit"),
};

function renderList(
  overrides: Partial<Parameters<typeof LinkedTransactionsList>[0]> = {},
) {
  const props = {
    sourceTxnId: "s1",
    links: [outgoing, incoming],
    loading: false,
    onRequestUnlink: vi.fn(),
    ...overrides,
  };
  const view = render(<LinkedTransactionsList {...props} />);
  return { ...view, props };
}

describe("LinkedTransactionsList", () => {
  it("shows the loading state", () => {
    renderList({ links: [], loading: true });
    expect(screen.getByText("Loading links...")).toBeInTheDocument();
  });

  it("shows the empty state", () => {
    renderList({ links: [] });
    expect(screen.getByText(/No links yet/)).toBeInTheDocument();
    expect(screen.getByText("Linked Transactions (0)")).toBeInTheDocument();
  });

  it("renders the other endpoint of each link", () => {
    renderList();

    expect(screen.getByText("Linked Transactions (2)")).toBeInTheDocument();
    expect(screen.getByText("Outgoing Target")).toBeInTheDocument();
    expect(screen.getByText("Incoming Refund")).toBeInTheDocument();
    expect(screen.getByText("transfer")).toBeInTheDocument();
    expect(screen.getByText("refund")).toBeInTheDocument();
  });

  it("requests an unlink for the clicked link", async () => {
    const user = userEvent.setup();
    const { props } = renderList();

    await user.click(screen.getAllByTitle("Unlink")[0]);
    expect(props.onRequestUnlink).toHaveBeenCalledWith("l1");

    await user.click(screen.getAllByTitle("Unlink")[1]);
    expect(props.onRequestUnlink).toHaveBeenCalledWith("l2");
  });
});
