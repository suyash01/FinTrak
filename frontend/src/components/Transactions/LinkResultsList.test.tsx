import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import LinkResultsList from "./LinkResultsList";
import type { Transaction } from "../../types";

const results = [
  {
    id: "r1",
    accountId: "a1",
    date: "2024-03-15",
    description: "Coffee Match",
    amount: 250,
    type: "debit",
    accountName: "Checking",
    isLinked: false,
  },
  {
    id: "r2",
    accountId: "a2",
    date: "2024-03-16",
    description: "Linked Match",
    amount: 500,
    type: "credit",
    accountName: "Savings",
    isLinked: true,
  },
] as Transaction[];

function renderList(
  overrides: Partial<Parameters<typeof LinkResultsList>[0]> = {},
) {
  const props = {
    loading: false,
    results,
    sourceAccountId: "a1",
    onSelect: vi.fn(),
    ...overrides,
  };
  const view = render(<LinkResultsList {...props} />);
  return { ...view, props };
}

describe("LinkResultsList", () => {
  it("shows the searching state", () => {
    renderList({ loading: true, results: [] });
    expect(screen.getByText("Searching...")).toBeInTheDocument();
  });

  it("shows the empty state", () => {
    renderList({ results: [] });
    expect(screen.getByText("No potential matches found")).toBeInTheDocument();
  });

  it("renders results with the same-account and linked badges", () => {
    renderList();

    expect(screen.getByText("Coffee Match")).toBeInTheDocument();
    expect(screen.getByText("Linked Match")).toBeInTheDocument();
    expect(screen.getByText("Same Account")).toBeInTheDocument();
    expect(screen.getByText("Already Linked")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /Choose Type/ })).toHaveLength(
      2,
    );
  });

  it("selects a result through the callback", async () => {
    const user = userEvent.setup();
    const { props } = renderList();

    const buttons = screen.getAllByRole("button", { name: /Choose Type/ });
    await user.click(buttons[1]);

    expect(props.onSelect).toHaveBeenCalledWith(results[1]);
  });

  it("keeps the match action visible without hover", () => {
    renderList();

    // The surrounding row has no click handler, so this button is the only way
    // to pick a candidate; it used to be fully transparent until the row was
    // hovered, and `group-hover` never fires on a touch device. jsdom applies
    // no Tailwind, so the class is the only observable form of that promise.
    for (const button of screen.getAllByRole("button", {
      name: /Choose Type/,
    })) {
      expect(button.className).not.toContain("opacity-0");
    }
  });
});
