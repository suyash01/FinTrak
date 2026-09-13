import { useState } from "react";
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import CategoryDialog from "./CategoryDialog";
import type { Category, CategoryGroup } from "../../types";
import { EMPTY_CATEGORY_FORM, type CategoryForm } from "./categoryForms";

if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

const GROUPS: CategoryGroup[] = [
  {
    id: "g1",
    name: "Food",
    icon: "utensils",
    color: "#ef4444",
    isBase: false,
    isGlobal: false,
    sortOrder: 0,
  },
  {
    id: "g2",
    name: "Travel",
    icon: "plane",
    color: "#3b82f6",
    isBase: false,
    isGlobal: true,
    sortOrder: 1,
  },
];

const CATEGORY: Category = {
  id: "c1",
  name: "Groceries",
  icon: "tag",
  color: "#22c55e",
  groupId: "g1",
};

const GLOBAL_CATEGORY: Category = {
  ...CATEGORY,
  id: "c2",
  name: "Flights",
  groupId: "g2",
  isGlobal: true,
};

interface HarnessProps {
  editingCategory?: Category | null;
  globalMode?: boolean;
  initial?: CategoryForm;
  onSubmit?: () => void;
  onOpenChange?: (open: boolean) => void;
}

function Harness({
  editingCategory = null,
  globalMode = false,
  initial = EMPTY_CATEGORY_FORM,
  onSubmit = () => {},
  onOpenChange = () => {},
}: HarnessProps) {
  const [form, setForm] = useState<CategoryForm>(initial);
  return (
    <CategoryDialog
      open
      onOpenChange={onOpenChange}
      editingCategory={editingCategory}
      globalMode={globalMode}
      form={form}
      onChange={setForm}
      onSubmit={onSubmit}
      groups={GROUPS}
    />
  );
}

function renderClosed() {
  return render(
    <CategoryDialog
      open={false}
      onOpenChange={() => {}}
      editingCategory={null}
      globalMode={false}
      form={EMPTY_CATEGORY_FORM}
      onChange={() => {}}
      onSubmit={() => {}}
      groups={GROUPS}
    />,
  );
}

describe("CategoryDialog", () => {
  it("renders nothing while closed", () => {
    renderClosed();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("shows the new-category title and create label", () => {
    render(<Harness />);
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText("New Category")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Create Category" }),
    ).toBeInTheDocument();
  });

  it("shows the global-mode title", () => {
    render(<Harness globalMode />);
    expect(screen.getByText("New Global Category")).toBeInTheDocument();
  });

  it("shows the edit title for a user category", () => {
    render(<Harness editingCategory={CATEGORY} />);
    expect(screen.getByText("Edit Category")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Update Category" }),
    ).toBeInTheDocument();
  });

  it("shows the edit-global title for a global category", () => {
    render(<Harness editingCategory={GLOBAL_CATEGORY} />);
    expect(screen.getByText("Edit Global Category")).toBeInTheDocument();
  });

  it("updates the name field through onChange", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    const input = screen.getByPlaceholderText("e.g. Gym");
    await user.type(input, "Gym");
    expect(input).toHaveValue("Gym");
  });

  it("updates the icon and color fields through onChange", () => {
    render(<Harness />);
    const icon = screen.getByPlaceholderText("e.g. dumbbell");
    fireEvent.change(icon, { target: { value: "dumbbell" } });
    expect(icon).toHaveValue("dumbbell");

    const color = screen.getByDisplayValue("#06b6d4");
    fireEvent.change(color, { target: { value: "#123456" } });
    expect(color).toHaveValue("#123456");
  });

  it("selects a group from the dropdown", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByRole("option", { name: "Travel" }));
    expect(screen.getByRole("combobox").textContent).toContain("Travel");
  });

  it("keeps submit disabled until name and group are present", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    const submit = screen.getByRole("button", { name: "Create Category" });
    expect(submit).toBeDisabled();

    await user.type(screen.getByPlaceholderText("e.g. Gym"), "Gym");
    expect(submit).toBeDisabled();

    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByRole("option", { name: "Food" }));
    expect(submit).toBeEnabled();
  });

  it("submits with the entered form", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn();
    render(
      <Harness
        initial={{ ...EMPTY_CATEGORY_FORM, groupId: "g1" }}
        onSubmit={onSubmit}
      />,
    );
    await user.type(screen.getByPlaceholderText("e.g. Gym"), "Gym");
    await user.click(screen.getByRole("button", { name: "Create Category" }));
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
