import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import TransactionFilters from "./TransactionFilters";
import type { CategorySection } from "../../lib/categories";
import type { Account, Payee, TagCount } from "../../types";

if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

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

const payees = [
  { id: "p1", name: "Swiggy" },
  { id: "p2", name: "Uber" },
] as unknown as Payee[];
const tags = [
  { name: "trip", count: 2 },
  { name: "work", count: 1 },
] as unknown as TagCount[];

const categorySections = [
  {
    group: { id: "g1", name: "Food" },
    items: [{ id: "c1", name: "Groceries" }],
  },
] as unknown as CategorySection[];

const baseFilters: Record<string, string | number> = {
  search: "",
  accountId: "",
  categoryId: "",
  groupId: "",
  payeeId: "",
  type: "",
  linked: "",
  dateFrom: "",
  dateTo: "",
  tags: "",
};

function renderFilters(
  overrides: Partial<Parameters<typeof TransactionFilters>[0]> = {},
) {
  const props = {
    compactLayout: false,
    filters: baseFilters,
    onFilterChange: vi.fn(),
    accounts,
    payees,
    tags,
    categorySections,
    groupIds: new Set(["g1"]),
    preset: "50",
    customInput: "",
    onPresetChange: vi.fn(),
    onCustomInputChange: vi.fn(),
    onCommitCustom: vi.fn(),
    ...overrides,
  };
  const view = render(<TransactionFilters {...props} />);
  return { ...view, props };
}

function comboBoxWithText(text: string): HTMLElement {
  const box = screen
    .getAllByRole("combobox")
    .find((c) => c.textContent?.includes(text));
  if (!box) throw new Error(`combobox containing "${text}" not found`);
  return box;
}

describe("TransactionFilters", () => {
  it("emits search changes", () => {
    const { props } = renderFilters();
    fireEvent.change(screen.getByPlaceholderText("Search descriptions, notes, payees, tags..."), {
      target: { value: "coffee" },
    });
    expect(props.onFilterChange).toHaveBeenCalledWith("search", "coffee");
  });

  it("emits an account selection as a one-element list", async () => {
    const user = userEvent.setup();
    const { props } = renderFilters();

    await user.click(screen.getByRole("button", { name: "Filter by account" }));
    await user.click(await screen.findByRole("menuitemcheckbox", { name: "Checking" }));

    expect(props.onFilterChange).toHaveBeenCalledWith("accountId", "a1");
  });

  it("appends to the account selection instead of replacing it", async () => {
    const user = userEvent.setup();
    const { props } = renderFilters({
      filters: { ...baseFilters, accountId: "a1" },
    });

    // A single selection is summarised by name on the trigger.
    expect(screen.getByRole("button", { name: "Filter by account" })).toHaveTextContent(
      "Checking",
    );

    await user.click(screen.getByRole("button", { name: "Filter by account" }));
    await user.click(await screen.findByRole("menuitemcheckbox", { name: "Savings" }));

    expect(props.onFilterChange).toHaveBeenCalledWith("accountId", "a1,a2");
  });

  it("clears the account filter when its last account is unchecked", async () => {
    const user = userEvent.setup();
    const { props } = renderFilters({
      filters: { ...baseFilters, accountId: "a1" },
    });

    await user.click(screen.getByRole("button", { name: "Filter by account" }));
    await user.click(await screen.findByRole("menuitemcheckbox", { name: "Checking" }));

    expect(props.onFilterChange).toHaveBeenCalledWith("accountId", "");
  });

  it("summarises a multi selection on the trigger", () => {
    renderFilters({ filters: { ...baseFilters, accountId: "a1,a2" } });

    expect(screen.getByRole("button", { name: "Filter by account" })).toHaveTextContent(
      "2 selected",
    );
  });

  it("routes a group selection to groupId", async () => {
    const user = userEvent.setup();
    const { props } = renderFilters();

    await user.click(screen.getByRole("button", { name: "Filter by category" }));
    await user.click(await screen.findByRole("menuitemcheckbox", { name: /Food/ }));

    expect(props.onFilterChange).toHaveBeenCalledWith("categoryId", "");
    expect(props.onFilterChange).toHaveBeenCalledWith("groupId", "g1");
  });

  it("routes a category selection to categoryId", async () => {
    const user = userEvent.setup();
    const { props } = renderFilters();

    await user.click(screen.getByRole("button", { name: "Filter by category" }));
    await user.click(await screen.findByRole("menuitemcheckbox", { name: "Groceries" }));

    expect(props.onFilterChange).toHaveBeenCalledWith("groupId", "");
    expect(props.onFilterChange).toHaveBeenCalledWith("categoryId", "c1");
  });

  it("keeps a group and a category selection in their own parameters", async () => {
    const user = userEvent.setup();
    const { props } = renderFilters({
      filters: { ...baseFilters, categoryId: "c1" },
    });

    await user.click(screen.getByRole("button", { name: "Filter by category" }));
    await user.click(await screen.findByRole("menuitemcheckbox", { name: /Food/ }));

    expect(props.onFilterChange).toHaveBeenCalledWith("categoryId", "c1");
    expect(props.onFilterChange).toHaveBeenCalledWith("groupId", "g1");
  });

  it("emits payee selections as a list", async () => {
    const user = userEvent.setup();
    const { props } = renderFilters({
      filters: { ...baseFilters, payeeId: "p1" },
    });

    await user.click(screen.getByRole("button", { name: "Filter by payee" }));
    await user.click(await screen.findByRole("menuitemcheckbox", { name: "Uber" }));

    expect(props.onFilterChange).toHaveBeenCalledWith("payeeId", "p1,p2");
  });

  it("emits tag selections as a list", async () => {
    const user = userEvent.setup();
    const { props } = renderFilters({
      filters: { ...baseFilters, tags: "trip" },
    });

    await user.click(screen.getByRole("button", { name: "Filter by tag" }));
    await user.click(await screen.findByRole("menuitemcheckbox", { name: /work/ }));

    expect(props.onFilterChange).toHaveBeenCalledWith("tags", "trip,work");
  });

  it("emits type and link-status changes", async () => {
    const user = userEvent.setup();
    const { props } = renderFilters();

    await user.click(comboBoxWithText("All Types"));
    await user.click(await screen.findByRole("option", { name: "Debit" }));
    expect(props.onFilterChange).toHaveBeenCalledWith("type", "debit");

    await user.click(comboBoxWithText("All Link Status"));
    await user.click(await screen.findByRole("option", { name: "Linked Only" }));
    expect(props.onFilterChange).toHaveBeenCalledWith("linked", "true");
  });

  it("emits date range changes", () => {
    const { container, props } = renderFilters();
    const dateInputs = container.querySelectorAll<HTMLInputElement>(
      'input[type="date"]',
    );
    expect(dateInputs).toHaveLength(2);

    fireEvent.change(dateInputs[0], { target: { value: "2024-01-01" } });
    expect(props.onFilterChange).toHaveBeenCalledWith("dateFrom", "2024-01-01");

    fireEvent.change(dateInputs[1], { target: { value: "2024-12-31" } });
    expect(props.onFilterChange).toHaveBeenCalledWith("dateTo", "2024-12-31");
  });

  it("emits page-size preset changes", async () => {
    const user = userEvent.setup();
    const { props } = renderFilters();

    await user.click(comboBoxWithText("50"));
    await user.click(await screen.findByRole("option", { name: "25" }));
    expect(props.onPresetChange).toHaveBeenCalledWith("25");
  });

  it("emits the custom preset selection", async () => {
    const user = userEvent.setup();
    const { props } = renderFilters();

    await user.click(comboBoxWithText("50"));
    await user.click(await screen.findByRole("option", { name: "Custom..." }));
    expect(props.onPresetChange).toHaveBeenCalledWith("custom");
  });

  it("edits and commits a custom page size", () => {
    const { props } = renderFilters({ preset: "custom", customInput: "42" });
    const input = screen.getByPlaceholderText("Custom");
    expect(input).toHaveValue(42);

    fireEvent.change(input, { target: { value: "100" } });
    expect(props.onCustomInputChange).toHaveBeenCalledWith("100");

    fireEvent.blur(input);
    expect(props.onCommitCustom).toHaveBeenCalledTimes(1);

    fireEvent.keyDown(input, { key: "Enter" });
    expect(props.onCommitCustom).toHaveBeenCalledTimes(2);
  });

  it("hides the custom page size input unless the custom preset is chosen", () => {
    renderFilters();
    expect(screen.queryByPlaceholderText("Custom")).toBeNull();
  });
});
