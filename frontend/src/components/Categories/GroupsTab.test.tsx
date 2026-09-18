import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import GroupsTab from "./GroupsTab";
import type { CategoryGroup } from "../../types";

if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

const { apiMock, domainMock, authState, toastMock } = vi.hoisted(() => ({
  apiMock: {
    createGroup: vi.fn(),
    updateGroup: vi.fn(),
    deleteGroup: vi.fn(),
    createGlobalGroup: vi.fn(),
  },
  domainMock: { useDomainData: vi.fn() },
  authState: {
    user: { id: "u1", email: "user@example.com", role: "user" } as {
      id: string;
      email: string;
      role?: string;
    } | null,
  },
  toastMock: { error: vi.fn(), success: vi.fn() },
}));

vi.mock("../../api/client", () => ({ default: apiMock }));
vi.mock("../../context/DomainDataContext", () => ({
  useDomainData: domainMock.useDomainData,
}));
vi.mock("../../context/SettingsContext", () => ({
  useSettings: () => ({ compactLayout: false }),
}));
vi.mock("../../context/AuthContext", () => ({
  useAuth: () => ({ user: authState.user }),
}));
vi.mock("sonner", () => ({ toast: toastMock }));

const groups: CategoryGroup[] = [
  {
    id: "base",
    name: "Base",
    icon: "lock",
    color: "#111111",
    isBase: true,
    isGlobal: false,
    sortOrder: 0,
  },
  {
    id: "gg",
    name: "Global",
    icon: "globe",
    color: "#222222",
    isBase: false,
    isGlobal: true,
    sortOrder: 1,
  },
  {
    id: "custom",
    name: "Custom",
    icon: "star",
    color: "#333333",
    isBase: false,
    isGlobal: false,
    sortOrder: 2,
  },
];

const refreshGroups = vi.fn();

function setDomain(list = groups) {
  domainMock.useDomainData.mockReturnValue({
    groups: list,
    refreshGroups,
  });
}

describe("GroupsTab", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    authState.user = { id: "u1", email: "user@example.com", role: "user" };
    setDomain();
    apiMock.createGroup.mockResolvedValue({});
    apiMock.updateGroup.mockResolvedValue({});
    apiMock.deleteGroup.mockResolvedValue(null);
    apiMock.createGlobalGroup.mockResolvedValue({});
  });

  it("renders the group count and metadata labels", () => {
    render(<MemoryRouter><GroupsTab /></MemoryRouter>);
    expect(screen.getByText("3 groups")).toBeInTheDocument();
    expect(screen.getAllByText("Custom")).toHaveLength(2);
    expect(screen.getAllByText("Base")).toHaveLength(2);
    expect(screen.getAllByText("Global")).toHaveLength(2);
  });

  it("only exposes edit/delete for editable groups", () => {
    render(<MemoryRouter><GroupsTab /></MemoryRouter>);
    expect(
      screen.getByRole("button", { name: "Edit Custom" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Delete Custom" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit Base" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Edit Global" })).toBeNull();
  });

  it("creates a group", async () => {
    const user = userEvent.setup();
    render(<MemoryRouter><GroupsTab /></MemoryRouter>);

    await user.click(screen.getByRole("button", { name: "Add Group" }));
    expect(screen.getByText("New Group")).toBeInTheDocument();

    await user.type(screen.getByPlaceholderText("e.g. Vacation"), "Vacation");
    await user.type(screen.getByPlaceholderText("e.g. vacation"), "Vacation");
    await user.click(screen.getByRole("button", { name: "Create Group" }));

    await waitFor(() =>
      expect(apiMock.createGroup).toHaveBeenCalledWith(
        expect.objectContaining({ id: "vacation", name: "Vacation" }),
      ),
    );
    expect(refreshGroups).toHaveBeenCalled();
  });

  it("keeps create disabled until name and id are present", async () => {
    const user = userEvent.setup();
    render(<MemoryRouter><GroupsTab /></MemoryRouter>);

    await user.click(screen.getByRole("button", { name: "Add Group" }));
    const submit = screen.getByRole("button", { name: "Create Group" });
    expect(submit).toBeDisabled();

    await user.type(screen.getByPlaceholderText("e.g. Vacation"), "Vacation");
    expect(submit).toBeDisabled();

    await user.type(screen.getByPlaceholderText("e.g. vacation"), "vacation");
    expect(submit).toBeEnabled();
  });

  it("updates a custom group", async () => {
    const user = userEvent.setup();
    render(<MemoryRouter><GroupsTab /></MemoryRouter>);

    await user.click(screen.getByRole("button", { name: "Edit Custom" }));
    expect(screen.getByText("Edit Group")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Update Group" }));

    await waitFor(() =>
      expect(apiMock.updateGroup).toHaveBeenCalledWith(
        "custom",
        expect.objectContaining({ name: "Custom" }),
      ),
    );
  });

  it("deletes a custom group after confirmation", async () => {
    const user = userEvent.setup();
    render(<MemoryRouter><GroupsTab /></MemoryRouter>);

    await user.click(screen.getByRole("button", { name: "Delete Custom" }));
    expect(await screen.findByText("Delete group?")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Delete" }));

    await waitFor(() =>
      expect(apiMock.deleteGroup).toHaveBeenCalledWith("custom"),
    );
    expect(refreshGroups).toHaveBeenCalled();
  });

  it("cancels the deletion", async () => {
    const user = userEvent.setup();
    render(<MemoryRouter><GroupsTab /></MemoryRouter>);

    await user.click(screen.getByRole("button", { name: "Delete Custom" }));
    await user.click(await screen.findByRole("button", { name: "Cancel" }));

    await waitFor(() =>
      expect(screen.queryByText("Delete group?")).toBeNull(),
    );
    expect(apiMock.deleteGroup).not.toHaveBeenCalled();
  });

  it("hides the global controls from non-admins", () => {
    render(<MemoryRouter><GroupsTab /></MemoryRouter>);
    expect(
      screen.queryByRole("button", { name: "Add Global Group" }),
    ).toBeNull();
  });

  it("creates a global group as an admin", async () => {
    authState.user = { id: "a1", email: "admin@example.com", role: "admin" };
    const user = userEvent.setup();
    render(<MemoryRouter><GroupsTab /></MemoryRouter>);

    await user.click(screen.getByRole("button", { name: "Add Global Group" }));
    expect(screen.getByText("New Global Group")).toBeInTheDocument();

    await user.type(screen.getByPlaceholderText("e.g. Vacation"), "Shared");
    await user.type(screen.getByPlaceholderText("e.g. vacation"), "shared");
    await user.click(screen.getByRole("button", { name: "Create Group" }));

    await waitFor(() =>
      expect(apiMock.createGlobalGroup).toHaveBeenCalledWith(
        expect.objectContaining({ id: "shared", name: "Shared" }),
      ),
    );
  });

  it("surfaces a toast error when saving fails", async () => {
    apiMock.createGroup.mockRejectedValue(new Error("nope"));
    const user = userEvent.setup();
    render(<MemoryRouter><GroupsTab /></MemoryRouter>);

    await user.click(screen.getByRole("button", { name: "Add Group" }));
    await user.type(screen.getByPlaceholderText("e.g. Vacation"), "Vacation");
    await user.type(screen.getByPlaceholderText("e.g. vacation"), "vacation");
    await user.click(screen.getByRole("button", { name: "Create Group" }));

    await waitFor(() => expect(toastMock.error).toHaveBeenCalledWith("nope"));
  });

  it("surfaces a toast error when deletion fails", async () => {
    apiMock.deleteGroup.mockRejectedValue(new Error("conflict"));
    const user = userEvent.setup();
    render(<MemoryRouter><GroupsTab /></MemoryRouter>);

    await user.click(screen.getByRole("button", { name: "Delete Custom" }));
    await user.click(await screen.findByRole("button", { name: "Delete" }));

    await waitFor(() =>
      expect(toastMock.error).toHaveBeenCalledWith("conflict"),
    );
  });
});
