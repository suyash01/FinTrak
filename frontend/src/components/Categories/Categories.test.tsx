import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router-dom";
import Categories from "./Categories";

vi.mock("../../context/SettingsContext", () => ({
  useSettings: () => ({ compactLayout: false }),
}));
vi.mock("./GroupsTab", () => ({ default: () => <div>Groups content</div> }));
vi.mock("./CategoriesTab", () => ({
  default: () => <div>Categories content</div>,
}));
vi.mock("./RulesTab", () => ({ default: () => <div>Rules content</div> }));

function LocationProbe() {
  const location = useLocation();
  return <span data-testid="location">{location.pathname + location.search}</span>;
}

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Categories />
      <LocationProbe />
    </MemoryRouter>,
  );
}

describe("Categories", () => {
  it("defaults to the Groups tab with no tab query param", () => {
    renderAt("/categories");
    expect(screen.getByText("Groups content")).toBeInTheDocument();
    expect(screen.queryByText("Categories content")).toBeNull();
    expect(screen.getByTestId("location").textContent).toBe("/categories");
  });

  it("switches tabs and reflects the tab in the URL", async () => {
    const user = userEvent.setup();
    renderAt("/categories");

    await user.click(screen.getByRole("tab", { name: "Categories" }));

    expect(screen.getByText("Categories content")).toBeInTheDocument();
    expect(screen.queryByText("Groups content")).toBeNull();
    expect(screen.getByTestId("location").textContent).toBe(
      "/categories?tab=categories",
    );
  });

  it("honors a deep link to the Rules tab", () => {
    renderAt("/categories?tab=rules");
    expect(screen.getByText("Rules content")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Rules" })).toHaveAttribute(
      "data-state",
      "active",
    );
  });

  it("drops the tab param when returning to the default tab", async () => {
    const user = userEvent.setup();
    renderAt("/categories?tab=rules");

    await user.click(screen.getByRole("tab", { name: "Groups" }));

    expect(screen.getByText("Groups content")).toBeInTheDocument();
    expect(screen.getByTestId("location").textContent).toBe("/categories");
  });
});
