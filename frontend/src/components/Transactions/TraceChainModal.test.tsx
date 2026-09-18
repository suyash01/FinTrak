import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import TraceChainModal from "./TraceChainModal";
import type { Link, Transaction } from "../../types";

const { apiMock } = vi.hoisted(() => ({
  apiMock: { getLinks: vi.fn() },
}));

vi.mock("../../api/client", () => ({ default: apiMock }));

function txn(overrides: Partial<Transaction>): Transaction {
  return {
    id: "t1",
    accountId: "a1",
    date: "2024-06-01",
    description: "Transfer out",
    amount: 500,
    type: "debit",
    ...overrides,
  };
}

const t1 = txn({ id: "t1", description: "Transfer out" });
const t2 = txn({
  id: "t2",
  date: "2024-06-02",
  description: "Transfer in",
  type: "credit",
  accountName: "Savings",
});
const t3 = txn({
  id: "t3",
  date: "2024-06-03",
  description: "Cashback reward",
  type: "credit",
});

const link1: Link = {
  id: "l1",
  type: "transfer",
  fromTxnId: "t1",
  toTxnId: "t2",
  fromTxn: t1,
  toTxn: t2,
};
const link2: Link = {
  id: "l2",
  type: "cashback",
  fromTxnId: "t2",
  toTxnId: "t3",
  fromTxn: t2,
  toTxn: t3,
};

beforeEach(() => {
  vi.clearAllMocks();
  const all = [link1, link2];
  apiMock.getLinks.mockImplementation(({ txnId }: { txnId: string }) =>
    Promise.resolve(
      all.filter((l) => l.fromTxnId === txnId || l.toTxnId === txnId),
    ),
  );
});

describe("TraceChainModal", () => {
  it("traces the link chain hop by hop", async () => {
    render(<TraceChainModal txn={t1} onClose={vi.fn()} />);

    expect(await screen.findByText("Hop 1")).toBeInTheDocument();
    expect(screen.getByText("Hop 2")).toBeInTheDocument();
    expect(screen.getByText("Transfer in")).toBeInTheDocument();
    expect(screen.getByText("Cashback reward")).toBeInTheDocument();
    expect(screen.getAllByText("transfer").length).toBeGreaterThan(0);
    expect(screen.getAllByText("cashback").length).toBeGreaterThan(0);
  });

  it("shows an empty state when nothing is linked", async () => {
    apiMock.getLinks.mockResolvedValue([]);
    render(<TraceChainModal txn={t1} onClose={vi.fn()} />);

    expect(
      await screen.findByText("This transaction has no links to trace."),
    ).toBeInTheDocument();
  });
});
