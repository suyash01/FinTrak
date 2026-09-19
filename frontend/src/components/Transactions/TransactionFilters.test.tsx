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
];

const payees = [{ id: "p1", name: "Swiggy" }] as unknown as Payee[];
const tags = [{ name: "trip", count: 2 }] as unknown as TagCount[];

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

  it("maps account selections and the all sentinel", async () => {
    const user = userEvent.setup();
    const { props } = renderFilters();

    await user.click(comboBoxWithText("All Accounts"));
    await user.click(await screen.findByRole("option", { name: "Checking" }));
    expect(props.onFilterChange).toHaveBeenCalledWith("accountId", "a1");
  });

  it("maps the all-accounts sentinel back to an empty string", async () => {
    const user = userEvent.setup();
    const { props } = renderFilters({
      filters: { ...baseFilters, accountId: "a1" },
    });

    await user.click(comboBoxWithText("Checking"));
    await user.click(await screen.findByRole("option", { name: "All Accounts" }));
    expect(props.onFilterChange).toHaveBeenCalledWith("accountId", "");
  });

  it("routes a group selection to groupId", async () => {
    const user = userEvent.setup();
    const { props } = renderFilters();

    await user.click(comboBoxWithText("All Categories"));
    await user.click(await screen.findByRole("option", { name: /Food/ }));

    expect(props.onFilterChange).toHaveBeenCalledWith("categoryId", "");
    expect(props.onFilterChange).toHaveBeenCalledWith("groupId", "g1");
  });

  it("routes a category selection to categoryId", async () => {
    const user = userEvent.setup();
    const { props } = renderFilters();

    await user.click(comboBoxWithText("All Categories"));
    await user.click(await screen.findByRole("option", { name: "Groceries" }));

    expect(props.onFilterChange).toHaveBeenCalledWith("groupId", "");
    expect(props.onFilterChange).toHaveBeenCalledWith("categoryId", "c1");
  });

  it("clears both category and group on the all sentinel", async () => {
    const user = userEvent.setup();
    const { props } = renderFilters({
      filters: { ...baseFilters, categoryId: "c1" },
    });

    await user.click(comboBoxWithText("Groceries"));
    await user.click(await screen.findByRole("option", { name: "All Categories" }));

    expect(props.onFilterChange).toHaveBeenCalledWith("categoryId", "");
    expect(props.onFilterChange).toHaveBeenCalledWith("groupId", "");
  });

  it("emits payee, type and link-status changes", async () => {
    const user = userEvent.setup();
    const { props } = renderFilters();

    await user.click(comboBoxWithText("All Payees"));
    await user.click(await screen.findByRole("option", { name: "Swiggy" }));
    expect(props.onFilterChange).toHaveBeenCalledWith("payeeId", "p1");

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
