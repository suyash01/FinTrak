import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ThemeProvider, useTheme } from "./ThemeContext";

const STORAGE_KEY = "fintrak_theme";
const originalMatchMedia = window.matchMedia;

function mockMatchMedia(matches: boolean) {
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    matches,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })) as unknown as typeof window.matchMedia;
}

function Harness() {
  const { mode, accent, isDark, setMode, setAccent } = useTheme();
  return (
    <div>
      <span data-testid="mode">{mode}</span>
      <span data-testid="accent">{accent}</span>
      <span data-testid="dark">{String(isDark)}</span>
      <button onClick={() => setMode("dark")}>set-dark</button>
      <button onClick={() => setAccent("violet")}>set-violet</button>
    </div>
  );
}

function renderHarness() {
  return render(
    <ThemeProvider>
      <Harness />
    </ThemeProvider>,
  );
}

describe("ThemeProvider", () => {
  beforeEach(() => {
    localStorage.clear();
    window.matchMedia = originalMatchMedia;
    document.documentElement.className = "";
    document.documentElement.removeAttribute("data-theme");
  });

  it("defaults to system mode and cyan accent", () => {
    renderHarness();
    expect(screen.getByTestId("mode").textContent).toBe("system");
    expect(screen.getByTestId("accent").textContent).toBe("cyan");
    expect(screen.getByTestId("dark").textContent).toBe("false");
  });

  it("reads persisted mode and accent from localStorage", () => {
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({ mode: "dark", accent: "emerald" }),
    );
    renderHarness();
    expect(screen.getByTestId("mode").textContent).toBe("dark");
    expect(screen.getByTestId("accent").textContent).toBe("emerald");
    expect(screen.getByTestId("dark").textContent).toBe("true");
  });

  it("falls back to defaults when stored values are invalid", () => {
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({ mode: "neon", accent: "neon" }),
    );
    renderHarness();
    expect(screen.getByTestId("mode").textContent).toBe("system");
    expect(screen.getByTestId("accent").textContent).toBe("cyan");
  });

  it("falls back to defaults when stored JSON is corrupt", () => {
    localStorage.setItem(STORAGE_KEY, "{not json");
    renderHarness();
    expect(screen.getByTestId("mode").textContent).toBe("system");
    expect(screen.getByTestId("accent").textContent).toBe("cyan");
  });

  it("applies the dark class and persists mode changes", async () => {
    const user = userEvent.setup();
    renderHarness();

    await user.click(screen.getByText("set-dark"));

    expect(screen.getByTestId("dark").textContent).toBe("true");
    expect(document.documentElement.classList.contains("dark")).toBe(true);
    expect(JSON.parse(localStorage.getItem(STORAGE_KEY)!)).toEqual({
      mode: "dark",
      accent: "cyan",
    });
  });

  it("keeps the installed app's toolbar color in step with the mode", async () => {
    const user = userEvent.setup();
    const meta = document.createElement("meta");
    meta.setAttribute("name", "theme-color");
    meta.setAttribute("content", "#f8fafc");
    document.head.appendChild(meta);

    renderHarness();
    expect(meta.getAttribute("content")).toBe("#f8fafc");

    await user.click(screen.getByText("set-dark"));

    expect(meta.getAttribute("content")).toBe("#020618");
    meta.remove();
  });

  it("sets data-theme and persists accent changes", async () => {
    const user = userEvent.setup();
    renderHarness();

    await user.click(screen.getByText("set-violet"));

    expect(document.documentElement.dataset.theme).toBe("violet");
    expect(JSON.parse(localStorage.getItem(STORAGE_KEY)!)).toEqual({
      mode: "system",
      accent: "violet",
    });
  });

  it("follows the OS preference in system mode", () => {
    mockMatchMedia(true);
    renderHarness();
    expect(screen.getByTestId("mode").textContent).toBe("system");
    expect(screen.getByTestId("dark").textContent).toBe("true");
    expect(document.documentElement.classList.contains("dark")).toBe(true);
  });
});

describe("useTheme", () => {
  it("throws when used outside a ThemeProvider", () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    expect(() => render(<Harness />)).toThrow(
      "useTheme must be used within a ThemeProvider",
    );
    spy.mockRestore();
  });
});
