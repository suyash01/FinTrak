import { useState } from "react";
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import GroupDialog from "./GroupDialog";
import type { CategoryGroup } from "../../types";
import { EMPTY_GROUP_FORM, type GroupForm } from "./categoryForms";

const GROUP: CategoryGroup = {
  id: "custom",
  name: "Custom",
  icon: "star",
  color: "#334155",
  isBase: false,
  isGlobal: false,
  sortOrder: 2,
};

interface HarnessProps {
  editingGroup?: CategoryGroup | null;
  globalGroupMode?: boolean;
  initial?: GroupForm;
  onSubmit?: () => void;
  onOpenChange?: (open: boolean) => void;
}

function Harness({
  editingGroup = null,
  globalGroupMode = false,
  initial = EMPTY_GROUP_FORM,
  onSubmit = () => {},
  onOpenChange = () => {},
}: HarnessProps) {
  const [form, setForm] = useState<GroupForm>(initial);
  return (
    <GroupDialog
      open
      onOpenChange={onOpenChange}
      editingGroup={editingGroup}
      globalGroupMode={globalGroupMode}
      form={form}
      onChange={setForm}
      onSubmit={onSubmit}
    />
  );
}

function renderClosed() {
  return render(
    <GroupDialog
      open={false}
      onOpenChange={() => {}}
      editingGroup={null}
      globalGroupMode={false}
      form={EMPTY_GROUP_FORM}
      onChange={() => {}}
      onSubmit={() => {}}
    />,
  );
}

describe("GroupDialog", () => {
  it("renders nothing while closed", () => {
    renderClosed();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("shows the new-group title and create label", () => {
    render(<Harness />);
    expect(screen.getByText("New Group")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Create Group" }),
    ).toBeInTheDocument();
  });

  it("shows the global-mode title", () => {
    render(<Harness globalGroupMode />);
    expect(screen.getByText("New Global Group")).toBeInTheDocument();
  });

  it("shows the edit title and hides the id field when editing", () => {
    render(<Harness editingGroup={GROUP} />);
    expect(screen.getByText("Edit Group")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Update Group" }),
    ).toBeInTheDocument();
    expect(screen.queryByPlaceholderText("e.g. vacation")).toBeNull();
  });

  it("updates the name and icon fields through onChange", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    const name = screen.getByPlaceholderText("e.g. Vacation");
    await user.type(name, "Vacation");
    expect(name).toHaveValue("Vacation");

    const icon = screen.getByPlaceholderText("e.g. plane");
    await user.clear(icon);
    await user.type(icon, "plane");
    expect(icon).toHaveValue("plane");
  });

  it("slugifies the id field", () => {
    render(<Harness />);
    const id = screen.getByPlaceholderText("e.g. vacation");
    fireEvent.change(id, { target: { value: "My Group" } });
    expect(id).toHaveValue("my_group");
  });

  it("updates the color field through onChange", () => {
    render(<Harness />);
    const color = screen.getByDisplayValue("#64748b");
    fireEvent.change(color, { target: { value: "#111111" } });
    expect(color).toHaveValue("#111111");
  });

  it("disables create until both name and id are present", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    const submit = screen.getByRole("button", { name: "Create Group" });
    expect(submit).toBeDisabled();

    await user.type(screen.getByPlaceholderText("e.g. Vacation"), "Vacation");
    expect(submit).toBeDisabled();

    await user.type(screen.getByPlaceholderText("e.g. vacation"), "vacation");
    expect(submit).toBeEnabled();
  });

  it("submits the entered form", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn();
    render(
      <Harness
        initial={{ ...EMPTY_GROUP_FORM, id: "g", name: "Group" }}
        onSubmit={onSubmit}
      />,
    );
    await user.click(screen.getByRole("button", { name: "Create Group" }));
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
