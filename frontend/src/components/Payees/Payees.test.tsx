import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import Payees from "./Payees";
import type { Account, Payee } from "../../types";

if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

const { apiMock, domainMock, refreshPayees } = vi.hoisted(() => ({
  apiMock: {
    createPayee: vi.fn(),
    updatePayee: vi.fn(),
    deletePayee: vi.fn(),
  },
  domainMock: { useDomainData: vi.fn() },
  refreshPayees: vi.fn(),
}));

vi.mock("../../api/client", () => ({ default: apiMock }));
vi.mock("../../context/DomainDataContext", () => ({
  useDomainData: domainMock.useDomainData,
}));
vi.mock("../../context/SettingsContext", () => ({
  useSettings: () => ({ compactLayout: false }),
}));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

const payees = [
  { id: "p1", name: "Zomato", accountId: null },
  { id: "p2", name: "Amazon", accountId: "a1" },
] as unknown as Payee[];

const accounts = [
  { id: "a1", name: "Checking" },
] as unknown as Account[];

function setDomain(list: Payee[], loading = false) {
  domainMock.useDomainData.mockReturnValue({
    payees: list,
    accounts,
    loading,
    refreshPayees,
  });
}

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/payees"]}>
      <Payees />
    </MemoryRouter>,
  );
}

function renderedNames(): string[] {
  return screen
    .getAllByRole("row")
    .slice(1)
    .map((row) => {
      const cell = within(row).getAllByRole("cell")[0];
      return cell.querySelector("span.font-medium")?.textContent ?? "";
    });
}

beforeEach(() => {
  vi.clearAllMocks();
  setDomain(payees);
  apiMock.createPayee.mockResolvedValue({ id: "p3", name: "New Payee" });
  apiMock.deletePayee.mockResolvedValue(null);
});

describe("Payees", () => {
  it("shows the empty state when there are no payees", () => {
    setDomain([]);
    renderPage();
    expect(screen.getByText("No Payees Yet")).toBeInTheDocument();
  });

  it("sorts by name and can reverse the order", async () => {
    const user = userEvent.setup();
    renderPage();
    expect(renderedNames()).toEqual(["Amazon", "Zomato"]);

    await user.click(screen.getByText("Name"));
    expect(renderedNames()).toEqual(["Zomato", "Amazon"]);
  });

  it("filters payees by search", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.type(screen.getByPlaceholderText("Search payees..."), "zom");

    await waitFor(() => expect(renderedNames()).toEqual(["Zomato"]));
  });

  it("creates a payee through the modal", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(screen.getByRole("button", { name: "Add Payee" }));
    await user.type(
      screen.getByPlaceholderText("e.g. Amazon, Google, etc."),
      "Swiggy",
    );
    await user.click(screen.getByRole("button", { name: "Create Payee" }));

    await waitFor(() =>
      expect(apiMock.createPayee).toHaveBeenCalledWith({
        name: "Swiggy",
        accountId: null,
      }),
    );
    expect(refreshPayees).toHaveBeenCalled();
  });

  it("deletes a standalone payee after confirmation", async () => {
    const user = userEvent.setup();
    renderPage();

    const zomatoRow = screen.getByText("Zomato").closest("tr")!;
    await user.click(within(zomatoRow).getByTitle("Delete payee"));

    expect(await screen.findByText("Delete payee?")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Delete" }));

    await waitFor(() => expect(apiMock.deletePayee).toHaveBeenCalledWith("p1"));
  });

  it("does not offer delete for account-linked payees", () => {
    renderPage();
    const amazonRow = screen.getByText("Amazon").closest("tr")!;
    expect(within(amazonRow).queryByTitle("Delete payee")).toBeNull();
  });
});
