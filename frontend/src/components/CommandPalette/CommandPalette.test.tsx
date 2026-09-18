import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router-dom";
import CommandPalette from "./CommandPalette";

if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}
if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
}

const { apiMock, setMode, onOpenChange } = vi.hoisted(() => ({
  apiMock: { applyRules: vi.fn() },
  setMode: vi.fn(),
  onOpenChange: vi.fn(),
}));

let settings: { paperlessUrl?: string; hasToken?: boolean } = {};

vi.mock("../../api/client", () => ({ default: apiMock }));
vi.mock("../../context/DomainDataContext", () => ({
  useDomainData: () => ({ settings }),
}));
vi.mock("../../context/ThemeContext", () => ({
  useTheme: () => ({ isDark: false, setMode }),
}));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

function LocationProbe() {
  const location = useLocation();
  return (
    <div data-testid="location">
      {location.pathname + location.search + JSON.stringify(location.state)}
    </div>
  );
}

function renderPalette(open = true) {
  return render(
    <MemoryRouter initialEntries={["/"]}>
      <CommandPalette open={open} onOpenChange={onOpenChange} />
      <LocationProbe />
    </MemoryRouter>,
  );
}

describe("CommandPalette", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    settings = {};
    apiMock.applyRules.mockResolvedValue({ updated: 3 });
  });

  it("shows navigation, create and action commands", () => {
    renderPalette();
    expect(screen.getByText("Dashboard")).toBeInTheDocument();
    expect(screen.getByText("Transactions")).toBeInTheDocument();
    expect(screen.getByText("Add Transaction")).toBeInTheDocument();
    expect(
      screen.getByText("Apply Rules to Uncategorized"),
    ).toBeInTheDocument();
  });

  it("hides the Paperless command unless configured", () => {
    renderPalette();
    expect(screen.queryByText("Paperless")).toBeNull();
  });

  it("shows the Paperless command when configured", () => {
    settings = { paperlessUrl: "https://p.example", hasToken: true };
    renderPalette();
    expect(screen.getByText("Paperless")).toBeInTheDocument();
  });

  it("navigates and closes when a page command is selected", async () => {
    const user = userEvent.setup();
    renderPalette();

    await user.click(screen.getByText("Dashboard"));

    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(screen.getByTestId("location")).toHaveTextContent(/^\/null$/);
  });

  it("navigates with a create intent for record dialogs", async () => {
    const user = userEvent.setup();
    renderPalette();

    await user.click(screen.getByText("Add Transaction"));

    expect(onOpenChange).toHaveBeenCalledWith(false);
    await waitFor(() =>
      expect(screen.getByTestId("location")).toHaveTextContent(
        "/transactions",
      ),
    );
    expect(screen.getByTestId("location")).toHaveTextContent(
      '"command":"new-transaction"',
    );
  });

  it("opens the rules tab intent for a new rule", async () => {
    const user = userEvent.setup();
    renderPalette();

    await user.click(screen.getByText("Add Rule"));

    expect(screen.getByTestId("location")).toHaveTextContent(
      "/categories?tab=rules",
    );
    expect(screen.getByTestId("location")).toHaveTextContent(
      '"command":"new-rule"',
    );
  });

  it("applies rules and reports the result", async () => {
    const user = userEvent.setup();
    const { toast } = await import("sonner");
    renderPalette();

    await user.click(screen.getByText("Apply Rules to Uncategorized"));

    expect(onOpenChange).toHaveBeenCalledWith(false);
    await waitFor(() => expect(apiMock.applyRules).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith("3 transactions updated"),
    );
  });

  it("surfaces an error when applying rules fails", async () => {
    const user = userEvent.setup();
    const { toast } = await import("sonner");
    apiMock.applyRules.mockRejectedValue(new Error("boom"));
    renderPalette();

    await user.click(screen.getByText("Apply Rules to Uncategorized"));

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("boom"),
    );
  });

  it("toggles the theme from the action", async () => {
    const user = userEvent.setup();
    renderPalette();

    await user.click(screen.getByText("Switch to dark theme"));

    expect(setMode).toHaveBeenCalledWith("dark");
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("toggles open state on Ctrl+K", () => {
    renderPalette(false);

    window.dispatchEvent(
      new KeyboardEvent("keydown", { key: "k", ctrlKey: true }),
    );

    expect(onOpenChange).toHaveBeenCalledWith(true);
  });
});
