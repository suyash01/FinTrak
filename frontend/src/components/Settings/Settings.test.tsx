import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import Settings from "./Settings";

const ctx = vi.hoisted(() => ({
  user: null as { id: number; email: string; role: string } | null,
  mode: "light" as string,
  accent: "cyan" as string,
  setMode: vi.fn(),
  setAccent: vi.fn(),
  compactLayout: false,
  toggleCompactLayout: vi.fn(),
}));

vi.mock("../../context/AuthContext", () => ({
  useAuth: () => ({ user: ctx.user }),
}));
vi.mock("../../context/SettingsContext", () => ({
  useSettings: () => ({
    compactLayout: ctx.compactLayout,
    toggleCompactLayout: ctx.toggleCompactLayout,
  }),
}));
vi.mock("../../context/ThemeContext", () => ({
  useTheme: () => ({
    mode: ctx.mode,
    accent: ctx.accent,
    setMode: ctx.setMode,
    setAccent: ctx.setAccent,
  }),
  THEME_MODES: ["light", "dark", "system"],
  ACCENT_THEMES: ["cyan", "violet", "emerald", "amber", "rose", "zinc"],
  ACCENT_COLORS: {
    cyan: "#06b6d4",
    violet: "#8b5cf6",
    emerald: "#10b981",
    amber: "#f59e0b",
    rose: "#f43f5e",
    zinc: "#71717a",
  },
}));
vi.mock("./AccountTypesManager", () => ({
  default: () => <div>Account types manager</div>,
}));
vi.mock("./PaperlessSettingsManager", () => ({
  default: () => <div>Paperless manager</div>,
}));

beforeEach(() => {
  vi.clearAllMocks();
  ctx.user = { id: 1, email: "user@example.com", role: "user" };
  ctx.mode = "light";
  ctx.accent = "cyan";
  ctx.compactLayout = false;
});

describe("Settings", () => {
  it("sets the theme mode when a mode button is clicked", async () => {
    const user = userEvent.setup();
    render(<Settings />);

    await user.click(screen.getByRole("button", { name: "dark" }));
    expect(ctx.setMode).toHaveBeenCalledWith("dark");

    await user.click(screen.getByRole("button", { name: "system" }));
    expect(ctx.setMode).toHaveBeenCalledWith("system");
  });

  it("sets the accent theme from the swatches", async () => {
    const user = userEvent.setup();
    render(<Settings />);

    await user.click(screen.getByRole("button", { name: "violet accent" }));
    expect(ctx.setAccent).toHaveBeenCalledWith("violet");
  });

  it("toggles the compact layout switch", async () => {
    const user = userEvent.setup();
    render(<Settings />);

    await user.click(screen.getByRole("switch"));
    expect(ctx.toggleCompactLayout).toHaveBeenCalledTimes(1);
  });

  it("hides the Account Types manager for non-admins", () => {
    render(<Settings />);
    expect(screen.queryByText("Account types manager")).toBeNull();
    expect(screen.queryByText("Account Types")).toBeNull();
  });

  it("shows the Account Types manager for admins", () => {
    ctx.user = { id: 2, email: "admin@example.com", role: "admin" };
    render(<Settings />);
    expect(screen.getByText("Account Types")).toBeInTheDocument();
    expect(screen.getByText("Account types manager")).toBeInTheDocument();
  });

  it("renders the Paperless manager and the app version", () => {
    render(<Settings />);
    expect(screen.getByText("Paperless manager")).toBeInTheDocument();
    expect(screen.getByText(/Version .* Built with Go \+ React/)).toBeInTheDocument();
  });
});
