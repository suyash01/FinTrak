import { useState } from "react";
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import RuleDialog from "./RuleDialog";
import type { CategorySection } from "../../lib/categories";
import type { Category, CategoryGroup, Payee, Rule } from "../../types";
import { EMPTY_NEW_RULE, type NewRuleForm } from "./categoryForms";

if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

const GROUP: CategoryGroup = {
  id: "g1",
  name: "Food",
  icon: "utensils",
  color: "#ef4444",
  isBase: false,
  isGlobal: false,
  sortOrder: 0,
};

const CATEGORY: Category = {
  id: "c1",
  name: "Groceries",
  icon: "tag",
  color: "#22c55e",
  groupId: "g1",
};

const SECTIONS: CategorySection[] = [{ group: GROUP, items: [CATEGORY] }];
const PAYEES: Payee[] = [{ id: "p1", name: "Swiggy" }];

const RULE: Rule = {
  id: "r1",
  pattern: "SWIGGY",
  matchType: "contains",
  categoryId: "c1",
  payeeId: "p1",
  priority: 10,
  categoryName: "Groceries",
};

interface HarnessProps {
  editingRule?: Rule | null;
  initial?: NewRuleForm;
  onSubmit?: () => void;
  onOpenChange?: (open: boolean) => void;
}

function Harness({
  editingRule = null,
  initial = EMPTY_NEW_RULE,
  onSubmit = () => {},
  onOpenChange = () => {},
}: HarnessProps) {
  const [form, setForm] = useState<NewRuleForm>(initial);
  return (
    <RuleDialog
      open
      onOpenChange={onOpenChange}
      editingRule={editingRule}
      form={form}
      onChange={setForm}
      onSubmit={onSubmit}
      categorySections={SECTIONS}
      payees={PAYEES}
      accounts={[]}
      previewCount={null}
    />
  );
}

function renderClosed() {
  return render(
    <RuleDialog
      open={false}
      onOpenChange={() => {}}
      editingRule={null}
      form={EMPTY_NEW_RULE}
      onChange={() => {}}
      onSubmit={() => {}}
      categorySections={SECTIONS}
      payees={PAYEES}
      accounts={[]}
      previewCount={null}
    />,
  );
}

describe("RuleDialog", () => {
  it("renders nothing while closed", () => {
    renderClosed();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("shows the new-rule title and create label", () => {
    render(<Harness />);
    expect(screen.getByText("New Rule")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Create Rule" }),
    ).toBeInTheDocument();
  });

  it("shows the edit title and update label", () => {
    render(<Harness editingRule={RULE} />);
    expect(screen.getByText("Edit Rule")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Update Rule" }),
    ).toBeInTheDocument();
  });

  it("updates the pattern field through onChange", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    const pattern = screen.getByPlaceholderText("e.g. SWIGGY, AMAZON, UBER");
    await user.type(pattern, "AMAZON");
    expect(pattern).toHaveValue("AMAZON");
  });

  it("selects a match type", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    const combos = screen.getAllByRole("combobox");
    await user.click(combos[0]);
    await user.click(await screen.findByRole("option", { name: "Exact Match" }));
    expect(combos[0].textContent).toContain("Exact Match");
  });

  it("selects a category from the grouped list", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    const combos = screen.getAllByRole("combobox");
    await user.click(combos[1]);
    await user.click(await screen.findByRole("option", { name: "Groceries" }));
    expect(combos[1].textContent).toContain("Groceries");
  });

  it("selects a payee and clears it back to no payee", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    const combos = screen.getAllByRole("combobox");
    await user.click(combos[2]);
    await user.click(await screen.findByRole("option", { name: "Swiggy" }));
    expect(combos[2].textContent).toContain("Swiggy");

    await user.click(combos[2]);
    await user.click(await screen.findByRole("option", { name: "No Payee" }));
    expect(combos[2].textContent).toContain("No Payee");
  });

  it("updates the priority number field", () => {
    render(<Harness />);
    const priority = screen.getByRole("spinbutton");
    fireEvent.change(priority, { target: { value: "5" } });
    expect(priority).toHaveValue(5);
  });

  it("disables submit until pattern and category are present", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    const submit = screen.getByRole("button", { name: "Create Rule" });
    expect(submit).toBeDisabled();

    await user.type(
      screen.getByPlaceholderText("e.g. SWIGGY, AMAZON, UBER"),
      "AMAZON",
    );
    expect(submit).toBeDisabled();

    const combos = screen.getAllByRole("combobox");
    await user.click(combos[1]);
    await user.click(await screen.findByRole("option", { name: "Groceries" }));
    expect(submit).toBeEnabled();
  });

  it("submits the entered form", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn();
    render(
      <Harness
        initial={{ ...EMPTY_NEW_RULE, pattern: "AMAZON", categoryId: "c1" }}
        onSubmit={onSubmit}
      />,
    );
    await user.click(screen.getByRole("button", { name: "Create Rule" }));
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it("closes through the dialog close button", async () => {
    const user = userEvent.setup();
    const onOpenChange = vi.fn();
    render(<Harness onOpenChange={onOpenChange} />);
    await user.click(screen.getByRole("button", { name: "Close" }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});
