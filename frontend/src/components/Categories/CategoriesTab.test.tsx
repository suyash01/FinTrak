import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import CategoriesTab from "./CategoriesTab";
import type { Category, CategoryGroup } from "../../types";

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
    createCategory: vi.fn(),
    updateCategory: vi.fn(),
    deleteCategory: vi.fn(),
    createGlobalCategory: vi.fn(),
    updateGlobalCategory: vi.fn(),
    deleteGlobalCategory: vi.fn(),
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
    id: "g1",
    name: "Food",
    icon: "",
    color: "#ef4444",
    isBase: false,
    isGlobal: false,
    sortOrder: 0,
  },
  {
    id: "gg",
    name: "Global",
    icon: "",
    color: "#3b82f6",
    isBase: false,
    isGlobal: true,
    sortOrder: -1,
  },
];

const categories: Category[] = [
  {
    id: "c1",
    name: "Groceries",
    icon: "tag",
    color: "#22c55e",
    groupId: "g1",
  },
  {
    id: "c2",
    name: "Flights",
    icon: "tag",
    color: "#0ea5e9",
    groupId: "gg",
    isGlobal: true,
  },
];

const refreshCategories = vi.fn();

function setDomain(cats = categories, grps = groups) {
  domainMock.useDomainData.mockReturnValue({
    categories: cats,
    groups: grps,
    refreshCategories,
  });
}

async function openNewCategory(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: "Add Category" }));
}

describe("CategoriesTab", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    authState.user = { id: "u1", email: "user@example.com", role: "user" };
    setDomain();
    apiMock.createCategory.mockResolvedValue({});
    apiMock.updateCategory.mockResolvedValue({});
    apiMock.createGlobalCategory.mockResolvedValue({});
    apiMock.updateGlobalCategory.mockResolvedValue({});
    apiMock.deleteCategory.mockResolvedValue({
      clearedTransactions: 0,
      deletedRules: 0,
    });
    apiMock.deleteGlobalCategory.mockResolvedValue({
      clearedTransactions: 0,
      deletedRules: 0,
    });
  });

  it("shows the category count and rendered sections", () => {
    render(<CategoriesTab />);
    expect(screen.getByText("2 categories")).toBeInTheDocument();
    expect(screen.getByText("Groceries")).toBeInTheDocument();
    expect(screen.getByText("Flights")).toBeInTheDocument();
    expect(screen.getAllByText("Global").length).toBeGreaterThan(0);
  });

  it("shows the empty state when there are no categories", () => {
    setDomain([], []);
    render(<CategoriesTab />);
    expect(screen.getByText("0 categories")).toBeInTheDocument();
    expect(
      screen.getByText("No categories yet. Add one above."),
    ).toBeInTheDocument();
  });

  it("creates a category from the dialog", async () => {
    const user = userEvent.setup();
    render(<CategoriesTab />);

    await openNewCategory(user);
    expect(screen.getByText("New Category")).toBeInTheDocument();

    await user.type(screen.getByPlaceholderText("e.g. Gym"), "Gym");
    await user.click(screen.getByRole("button", { name: "Create Category" }));

    await waitFor(() =>
      expect(apiMock.createCategory).toHaveBeenCalledWith(
        expect.objectContaining({ name: "Gym", groupId: "g1" }),
      ),
    );
    expect(refreshCategories).toHaveBeenCalled();
  });

  it("edits a user category", async () => {
    const user = userEvent.setup();
    render(<CategoriesTab />);

    await user.click(screen.getByRole("button", { name: "Edit Groceries" }));
    expect(screen.getByText("Edit Category")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Update Category" }));

    await waitFor(() =>
      expect(apiMock.updateCategory).toHaveBeenCalledWith(
        "c1",
        expect.objectContaining({ name: "Groceries", groupId: "g1" }),
      ),
    );
  });

  it("hides the global category controls from non-admins", () => {
    render(<CategoriesTab />);
    expect(
      screen.queryByRole("button", { name: "Add Global Category" }),
    ).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Edit Flights" }),
    ).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Delete Flights" }),
    ).toBeNull();
  });

  it("shows the global controls for admins", () => {
    authState.user = { id: "a1", email: "admin@example.com", role: "admin" };
    render(<CategoriesTab />);
    expect(
      screen.getByRole("button", { name: "Add Global Category" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Edit Flights" }),
    ).toBeInTheDocument();
  });

  it("creates a global category as an admin", async () => {
    authState.user = { id: "a1", email: "admin@example.com", role: "admin" };
    const user = userEvent.setup();
    render(<CategoriesTab />);

    await user.click(
      screen.getByRole("button", { name: "Add Global Category" }),
    );
    expect(screen.getByText("New Global Category")).toBeInTheDocument();

    await user.type(screen.getByPlaceholderText("e.g. Gym"), "Shared");
    await user.click(screen.getByRole("button", { name: "Create Category" }));

    await waitFor(() =>
      expect(apiMock.createGlobalCategory).toHaveBeenCalledWith(
        expect.objectContaining({ name: "Shared", groupId: "gg" }),
      ),
    );
  });

  it("edits a global category as an admin", async () => {
    authState.user = { id: "a1", email: "admin@example.com", role: "admin" };
    const user = userEvent.setup();
    render(<CategoriesTab />);

    await user.click(screen.getByRole("button", { name: "Edit Flights" }));
    expect(screen.getByText("Edit Global Category")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Update Category" }));

    await waitFor(() =>
      expect(apiMock.updateGlobalCategory).toHaveBeenCalledWith(
        "c2",
        expect.objectContaining({ name: "Flights" }),
      ),
    );
  });

  it("deletes a category and flashes the plain message", async () => {
    const user = userEvent.setup();
    render(<CategoriesTab />);

    await user.click(screen.getByRole("button", { name: "Delete Groceries" }));
    await user.click(await screen.findByRole("button", { name: "Delete" }));

    await waitFor(() =>
      expect(apiMock.deleteCategory).toHaveBeenCalledWith("c1"),
    );
    expect(
      await screen.findByText('Deleted "Groceries".'),
    ).toBeInTheDocument();
  });

  it("flashes the uncategorized message when transactions were cleared", async () => {
    apiMock.deleteCategory.mockResolvedValue({
      clearedTransactions: 3,
      deletedRules: 2,
    });
    const user = userEvent.setup();
    render(<CategoriesTab />);

    await user.click(screen.getByRole("button", { name: "Delete Groceries" }));
    await user.click(await screen.findByRole("button", { name: "Delete" }));

    expect(
      await screen.findByText(
        /3 transaction\(s\) uncategorized, 2 rule\(s\) removed/,
      ),
    ).toBeInTheDocument();
  });

  it("deletes a global category as an admin", async () => {
    authState.user = { id: "a1", email: "admin@example.com", role: "admin" };
    const user = userEvent.setup();
    render(<CategoriesTab />);

    await user.click(screen.getByRole("button", { name: "Delete Flights" }));
    await user.click(await screen.findByRole("button", { name: "Delete" }));

    await waitFor(() =>
      expect(apiMock.deleteGlobalCategory).toHaveBeenCalledWith("c2"),
    );
    expect(
      await screen.findByText('Deleted "Flights".'),
    ).toBeInTheDocument();
  });

  it("surfaces a toast error when the save fails", async () => {
    apiMock.createCategory.mockRejectedValue(new Error("boom"));
    const user = userEvent.setup();
    render(<CategoriesTab />);

    await openNewCategory(user);
    await user.type(screen.getByPlaceholderText("e.g. Gym"), "Gym");
    await user.click(screen.getByRole("button", { name: "Create Category" }));

    await waitFor(() =>
      expect(toastMock.error).toHaveBeenCalledWith("boom"),
    );
  });
});
