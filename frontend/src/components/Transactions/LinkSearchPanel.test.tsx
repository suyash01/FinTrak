import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import LinkSearchPanel from "./LinkSearchPanel";
import type { Account } from "../../types";

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

function renderPanel(
  overrides: Partial<Parameters<typeof LinkSearchPanel>[0]> = {},
) {
  const props = {
    search: "",
    onSearchChange: vi.fn(),
    accountId: "",
    accounts,
    dateFrom: "",
    dateTo: "",
    onDateFromChange: vi.fn(),
    onDateToChange: vi.fn(),
    matchAmount: false,
    excludeSameAccount: false,
    onMatchAmountChange: vi.fn(),
    onExcludeSameAccountChange: vi.fn(),
    onAccountChange: vi.fn(),
    onSubmit: vi.fn(),
    ...overrides,
  };
  const view = render(<LinkSearchPanel {...props} />);
  return { ...view, props };
}

describe("LinkSearchPanel", () => {
  it("emits search input changes and submits on Enter", () => {
    const { props } = renderPanel();
    const input = screen.getByPlaceholderText("Search description...");

    fireEvent.change(input, { target: { value: "coffee" } });
    expect(props.onSearchChange).toHaveBeenCalledWith("coffee");

    fireEvent.keyDown(input, { key: "Enter" });
    expect(props.onSubmit).toHaveBeenCalledTimes(1);
  });

  it("submits from the Find Match button", async () => {
    const user = userEvent.setup();
    const { props } = renderPanel();

    await user.click(screen.getByRole("button", { name: /Find Match/ }));
    expect(props.onSubmit).toHaveBeenCalledTimes(1);
  });

  it("emits date range changes", () => {
    const { container, props } = renderPanel();
    const dateInputs = container.querySelectorAll<HTMLInputElement>(
      'input[type="date"]',
    );
    expect(dateInputs).toHaveLength(2);

    fireEvent.change(dateInputs[0], { target: { value: "2024-03-01" } });
    expect(props.onDateFromChange).toHaveBeenCalledWith("2024-03-01");

    fireEvent.change(dateInputs[1], { target: { value: "2024-03-31" } });
    expect(props.onDateToChange).toHaveBeenCalledWith("2024-03-31");
  });

  it("toggles the amount and account checkboxes", async () => {
    const user = userEvent.setup();
    const { props } = renderPanel();

    await user.click(screen.getByRole("checkbox", { name: "Match Amount" }));
    expect(props.onMatchAmountChange).toHaveBeenCalledWith(true);

    await user.click(
      screen.getByRole("checkbox", { name: "Different Account Only" }),
    );
    expect(props.onExcludeSameAccountChange).toHaveBeenCalledWith(true);
  });

  it("emits an account selection", async () => {
    const user = userEvent.setup();
    const { props } = renderPanel();

    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByRole("option", { name: "Checking" }));

    expect(props.onAccountChange).toHaveBeenCalledWith("a1");
  });

  it("emits the all-accounts sentinel when an account was selected", async () => {
    const user = userEvent.setup();
    const { props } = renderPanel({ accountId: "a1" });

    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByRole("option", { name: "All Accounts" }));

    expect(props.onAccountChange).toHaveBeenCalledWith("all");
  });
});
