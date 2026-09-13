import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import Linking from "./Linking";
import type { Link, LinkType } from "../../types";

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
    getLinks: vi.fn(),
    deleteLink: vi.fn(),
    bulkDeleteLinks: vi.fn(),
  },
}));

vi.mock("../../api/client", () => ({ default: apiMock }));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

function link(id: string, type: LinkType): Link {
  return {
    id,
    type,
    fromTxnId: `${id}-from`,
    toTxnId: `${id}-to`,
    fromTxn: {
      id: `${id}-from`,
      accountId: "a1",
      date: "2024-03-15",
      description: `${type} source`,
      amount: 100,
      type: "debit",
      accountName: "Checking",
    },
    toTxn: {
      id: `${id}-to`,
      accountId: "a2",
      date: "2024-03-16",
      description: `${type} target`,
      amount: 100,
      type: "credit",
      accountName: "Savings",
    },
  };
}

const links: Link[] = [
  link("l1", "transfer"),
  link("l2", "cashback"),
  link("l3", "refund"),
  link("l4", "bill_payment"),
];

beforeEach(() => {
  vi.clearAllMocks();
  apiMock.getLinks.mockResolvedValue(links);
  apiMock.deleteLink.mockResolvedValue(null);
  apiMock.bulkDeleteLinks.mockResolvedValue(null);
});

describe("Linking", () => {
  it("renders each link type in its own group", async () => {
    render(<Linking />);

    expect(await screen.findByText("Transfers (1)")).toBeInTheDocument();
    expect(screen.getByText("Cashbacks (1)")).toBeInTheDocument();
    expect(screen.getByText("Refunds (1)")).toBeInTheDocument();
    expect(screen.getByText("Bill Payments (1)")).toBeInTheDocument();
    expect(screen.getByText("transfer source")).toBeInTheDocument();
    expect(screen.getByText("refund target")).toBeInTheDocument();
  });

  it("shows the empty state when there are no links", async () => {
    apiMock.getLinks.mockResolvedValue([]);
    render(<Linking />);
    expect(
      await screen.findByText("No Linked Transactions"),
    ).toBeInTheDocument();
  });

  it("shows an error and retries", async () => {
    const user = userEvent.setup();
    apiMock.getLinks
      .mockRejectedValueOnce(new Error("load failed"))
      .mockResolvedValue(links);
    render(<Linking />);

    expect(await screen.findByText("load failed")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Retry" }));

    expect(await screen.findByText("Transfers (1)")).toBeInTheDocument();
  });

  it("removes a single link after confirmation", async () => {
    const user = userEvent.setup();
    render(<Linking />);
    await screen.findByText("Transfers (1)");

    await user.click(screen.getAllByTitle("Remove link")[0]);

    expect(await screen.findByText("Remove this link?")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Remove" }));

    await waitFor(() => expect(apiMock.deleteLink).toHaveBeenCalledWith("l1"));
  });

  it("bulk-unlinks every selected link", async () => {
    const user = userEvent.setup();
    render(<Linking />);
    await screen.findByText("Transfers (1)");

    await user.click(screen.getByRole("button", { name: "Select All" }));
    const removeBulk = await screen.findByRole("button", {
      name: "Remove 4 Links",
    });
    await user.click(removeBulk);

    expect(
      await screen.findByText("Remove 4 selected links?"),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Remove" }));

    await waitFor(() =>
      expect(apiMock.bulkDeleteLinks).toHaveBeenCalledWith({
        ids: ["l1", "l2", "l3", "l4"],
      }),
    );
  });
});
