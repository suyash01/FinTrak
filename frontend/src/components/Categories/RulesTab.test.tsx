import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import RulesTab from "./RulesTab";
import type { Category, CategoryGroup, Payee, Rule } from "../../types";

if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

const { apiMock, domainMock, toastMock } = vi.hoisted(() => ({
  apiMock: {
    getRules: vi.fn(),
    createRule: vi.fn(),
    updateRule: vi.fn(),
    deleteRule: vi.fn(),
    applyRules: vi.fn(),
  },
  domainMock: { useDomainData: vi.fn() },
  toastMock: { error: vi.fn(), success: vi.fn() },
}));

vi.mock("../../api/client", () => ({ default: apiMock }));
vi.mock("../../context/DomainDataContext", () => ({
  useDomainData: domainMock.useDomainData,
}));
vi.mock("../../context/SettingsContext", () => ({
  useSettings: () => ({ compactLayout: false }),
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
];

const categories: Category[] = [
  {
    id: "c1",
    name: "Groceries",
    icon: "tag",
    color: "#22c55e",
    groupId: "g1",
  },
];

const payees: Payee[] = [{ id: "p1", name: "Swiggy" }];

const rules: Rule[] = [
  {
    id: "r1",
    pattern: "SWIGGY",
    matchType: "contains",
    categoryId: "c1",
    payeeId: "p1",
    payee: "Swiggy",
    priority: 10,
    categoryName: "Groceries",
  },
  {
    id: "r2",
    pattern: "UBER",
    matchType: "starts_with",
    categoryId: "c1",
    payeeId: null,
    priority: 0,
    categoryName: "Groceries",
  },
];

const setDomain = () =>
  domainMock.useDomainData.mockReturnValue({ categories, groups, payees });

function comboboxes(): HTMLElement[] {
  return screen.getAllByRole("combobox");
}

describe("RulesTab", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setDomain();
    apiMock.getRules.mockResolvedValue(rules);
    apiMock.createRule.mockResolvedValue({});
    apiMock.updateRule.mockResolvedValue({});
    apiMock.deleteRule.mockResolvedValue(null);
    apiMock.applyRules.mockResolvedValue({ updated: 0 });
  });

  it("loads and renders the rule table", async () => {
    render(<MemoryRouter><RulesTab /></MemoryRouter>);

    expect(await screen.findByText('"SWIGGY"')).toBeInTheDocument();
    expect(screen.getByText("2 rules")).toBeInTheDocument();
    expect(screen.getByText("contains")).toBeInTheDocument();
    expect(screen.getByText("starts with")).toBeInTheDocument();
    expect(screen.getAllByText("Groceries")).toHaveLength(2);
    expect(screen.getByText("Swiggy")).toBeInTheDocument();
    expect(screen.getByText("—")).toBeInTheDocument();
  });

  it("shows the empty state when there are no rules", async () => {
    apiMock.getRules.mockResolvedValue([]);
    render(<MemoryRouter><RulesTab /></MemoryRouter>);

    expect(
      await screen.findByText(
        "No rules yet. Create one to auto-categorize transactions.",
      ),
    ).toBeInTheDocument();
  });

  it("surfaces a toast error when loading rules fails", async () => {
    apiMock.getRules.mockRejectedValue(new Error("load failed"));
    render(<MemoryRouter><RulesTab /></MemoryRouter>);

    await waitFor(() =>
      expect(toastMock.error).toHaveBeenCalledWith("load failed"),
    );
  });

  it("creates a rule with the selected category", async () => {
    const user = userEvent.setup();
    render(<MemoryRouter><RulesTab /></MemoryRouter>);
    await screen.findByText('"SWIGGY"');

    await user.click(screen.getByRole("button", { name: "Add Rule" }));
    expect(screen.getByText("New Rule")).toBeInTheDocument();

    await user.type(
      screen.getByPlaceholderText("e.g. SWIGGY, AMAZON, UBER"),
      "AMAZON",
    );
    await user.click(comboboxes()[1]);
    await user.click(await screen.findByRole("option", { name: "Groceries" }));
    await user.click(screen.getByRole("button", { name: "Create Rule" }));

    await waitFor(() =>
      expect(apiMock.createRule).toHaveBeenCalledWith(
        expect.objectContaining({
          pattern: "AMAZON",
          categoryId: "c1",
          payeeId: null,
        }),
      ),
    );
    await waitFor(() => expect(apiMock.getRules).toHaveBeenCalledTimes(2));
  });

  it("assigns a payee when creating a rule", async () => {
    const user = userEvent.setup();
    render(<MemoryRouter><RulesTab /></MemoryRouter>);
    await screen.findByText('"SWIGGY"');

    await user.click(screen.getByRole("button", { name: "Add Rule" }));
    await user.type(
      screen.getByPlaceholderText("e.g. SWIGGY, AMAZON, UBER"),
      "AMAZON",
    );
    await user.click(comboboxes()[1]);
    await user.click(await screen.findByRole("option", { name: "Groceries" }));
    await user.click(comboboxes()[2]);
    await user.click(await screen.findByRole("option", { name: "Swiggy" }));
    await user.click(screen.getByRole("button", { name: "Create Rule" }));

    await waitFor(() =>
      expect(apiMock.createRule).toHaveBeenCalledWith(
        expect.objectContaining({ payeeId: "p1" }),
      ),
    );
  });

  it("edits an existing rule", async () => {
    const user = userEvent.setup();
    render(<MemoryRouter><RulesTab /></MemoryRouter>);
    await screen.findByText('"SWIGGY"');

    await user.click(
      screen.getByRole("button", { name: "Edit rule SWIGGY" }),
    );
    expect(screen.getByText("Edit Rule")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Update Rule" }));

    await waitFor(() =>
      expect(apiMock.updateRule).toHaveBeenCalledWith(
        "r1",
        expect.objectContaining({ pattern: "SWIGGY", categoryId: "c1" }),
      ),
    );
  });

  it("deletes a rule after confirmation", async () => {
    const user = userEvent.setup();
    render(<MemoryRouter><RulesTab /></MemoryRouter>);
    await screen.findByText('"UBER"');

    await user.click(screen.getByRole("button", { name: "Delete rule UBER" }));
    expect(await screen.findByText("Delete rule?")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Delete" }));

    await waitFor(() => expect(apiMock.deleteRule).toHaveBeenCalledWith("r2"));
    await waitFor(() => expect(screen.queryByText('"UBER"')).toBeNull());
  });

  it("applies rules and reports the updated count", async () => {
    apiMock.applyRules.mockResolvedValue({ updated: 7 });
    const user = userEvent.setup();
    render(<MemoryRouter><RulesTab /></MemoryRouter>);
    await screen.findByText('"SWIGGY"');

    await user.click(
      screen.getByRole("button", { name: "Apply Rules to Uncategorized" }),
    );

    expect(
      await screen.findByText("7 transactions updated"),
    ).toBeInTheDocument();
  });

  it("surfaces a toast error when applying rules fails", async () => {
    apiMock.applyRules.mockRejectedValue(new Error("apply failed"));
    const user = userEvent.setup();
    render(<MemoryRouter><RulesTab /></MemoryRouter>);
    await screen.findByText('"SWIGGY"');

    await user.click(
      screen.getByRole("button", { name: "Apply Rules to Uncategorized" }),
    );

    await waitFor(() =>
      expect(toastMock.error).toHaveBeenCalledWith("apply failed"),
    );
  });

  it("surfaces a toast error when saving a rule fails", async () => {
    apiMock.createRule.mockRejectedValue(new Error("save failed"));
    const user = userEvent.setup();
    render(<MemoryRouter><RulesTab /></MemoryRouter>);
    await screen.findByText('"SWIGGY"');

    await user.click(screen.getByRole("button", { name: "Add Rule" }));
    await user.type(
      screen.getByPlaceholderText("e.g. SWIGGY, AMAZON, UBER"),
      "AMAZON",
    );
    await user.click(comboboxes()[1]);
    await user.click(await screen.findByRole("option", { name: "Groceries" }));
    await user.click(screen.getByRole("button", { name: "Create Rule" }));

    await waitFor(() =>
      expect(toastMock.error).toHaveBeenCalledWith("save failed"),
    );
  });

  it("surfaces a toast error when deleting a rule fails", async () => {
    apiMock.deleteRule.mockRejectedValue(new Error("delete failed"));
    const user = userEvent.setup();
    render(<MemoryRouter><RulesTab /></MemoryRouter>);
    await screen.findByText('"UBER"');

    await user.click(screen.getByRole("button", { name: "Delete rule UBER" }));
    await user.click(await screen.findByRole("button", { name: "Delete" }));

    await waitFor(() =>
      expect(toastMock.error).toHaveBeenCalledWith("delete failed"),
    );
  });
});
