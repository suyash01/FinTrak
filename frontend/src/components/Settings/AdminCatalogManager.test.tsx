import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import AdminCatalogManager from "./AdminCatalogManager";
import type { AdminCatalog } from "../../types";

if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

const { apiMock, domainMock, refreshCategories, refreshGroups } = vi.hoisted(
  () => ({
    apiMock: {
      getAdminCatalog: vi.fn(),
      createGlobalGroup: vi.fn(),
      createGlobalCategory: vi.fn(),
      updateGlobalCategory: vi.fn(),
      deleteGlobalCategory: vi.fn(),
    },
    domainMock: { useDomainData: vi.fn() },
    refreshCategories: vi.fn(),
    refreshGroups: vi.fn(),
  }),
);

vi.mock("../../api/client", () => ({ default: apiMock }));
vi.mock("../../context/DomainDataContext", () => ({
  useDomainData: domainMock.useDomainData,
}));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

const catalog: AdminCatalog = {
  groups: [
    {
      id: "expense",
      name: "Expense",
      icon: "wallet",
      color: "#f00",
      isBase: true,
      isGlobal: true,
      sortOrder: 1,
      categoryCount: 3,
    },
  ],
  categories: [
    {
      id: "11111111-1111-1111-1111-111111111111",
      name: "Groceries",
      icon: "cart",
      color: "#0f0",
      groupId: "expense",
      isGlobal: true,
      groupName: "Expense",
      groupIsBase: true,
      transactionCount: 12,
    },
  ],
};

beforeEach(() => {
  vi.clearAllMocks();
  domainMock.useDomainData.mockReturnValue({
    refreshCategories,
    refreshGroups,
  });
  apiMock.getAdminCatalog.mockResolvedValue(catalog);
});

describe("AdminCatalogManager", () => {
  it("renders groups and categories with usage counts", async () => {
    render(<AdminCatalogManager />);
    await waitFor(() =>
      expect(screen.getByText("Groceries")).toBeInTheDocument(),
    );
    expect(screen.getAllByText("Expense").length).toBeGreaterThan(0);
    expect(screen.getByText("3 categories")).toBeInTheDocument();
    expect(screen.getByText(/12 txn/)).toBeInTheDocument();
  });

  it("deletes a global category after confirmation", async () => {
    apiMock.deleteGlobalCategory.mockResolvedValue({
      clearedTransactions: 2,
      deletedRules: 0,
    });
    render(<AdminCatalogManager />);
    await waitFor(() =>
      expect(screen.getByText("Groceries")).toBeInTheDocument(),
    );

    await userEvent.click(
      screen.getByRole("button", { name: "Delete Groceries" }),
    );
    await userEvent.click(screen.getByRole("button", { name: "Delete" }));

    await waitFor(() =>
      expect(apiMock.deleteGlobalCategory).toHaveBeenCalledWith(
        catalog.categories[0].id,
      ),
    );
    expect(refreshCategories).toHaveBeenCalled();
  });
});
