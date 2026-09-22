import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SettingsProvider, useSettings } from "./SettingsContext";

function Harness() {
  const { compactLayout, toggleCompactLayout, setCompactLayout } =
    useSettings();
  return (
    <div>
      <span data-testid="compact">{String(compactLayout)}</span>
      <button onClick={toggleCompactLayout}>toggle</button>
      <button onClick={() => setCompactLayout(false)}>set-false</button>
    </div>
  );
}

function renderHarness() {
  return render(
    <SettingsProvider>
      <Harness />
    </SettingsProvider>,
  );
}

describe("SettingsProvider", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("defaults compactLayout to true", () => {
    renderHarness();
    expect(screen.getByTestId("compact").textContent).toBe("true");
  });

  it("reads a saved value from localStorage", () => {
    localStorage.setItem("compactLayout", "false");
    renderHarness();
    expect(screen.getByTestId("compact").textContent).toBe("false");
  });

  it("falls back to the default when the stored value is corrupt", () => {
    localStorage.setItem("compactLayout", "{not json");
    renderHarness();
    expect(screen.getByTestId("compact").textContent).toBe("true");
  });

  it("falls back to the default when the stored value is not a boolean", () => {
    localStorage.setItem("compactLayout", JSON.stringify("yes"));
    renderHarness();
    expect(screen.getByTestId("compact").textContent).toBe("true");
  });

  it("toggleCompactLayout flips the value", async () => {
    const user = userEvent.setup();
    renderHarness();
    expect(screen.getByTestId("compact").textContent).toBe("true");

    await user.click(screen.getByText("toggle"));
    expect(screen.getByTestId("compact").textContent).toBe("false");

    await user.click(screen.getByText("toggle"));
    expect(screen.getByTestId("compact").textContent).toBe("true");
  });

  it("persists the value to localStorage on change", async () => {
    const user = userEvent.setup();
    renderHarness();

    await user.click(screen.getByText("toggle"));
    expect(localStorage.getItem("compactLayout")).toBe("false");

    await user.click(screen.getByText("set-false"));
    expect(localStorage.getItem("compactLayout")).toBe("false");
  });

  it("setCompactLayout updates the value directly", async () => {
    const user = userEvent.setup();
    renderHarness();

    await user.click(screen.getByText("set-false"));
    expect(screen.getByTestId("compact").textContent).toBe("false");
    expect(localStorage.getItem("compactLayout")).toBe("false");
  });

  it("keeps the layout in memory when storage refuses the write", async () => {
    const user = userEvent.setup();
    renderHarness();
    const setItem = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("QuotaExceededError");
      });

    await user.click(screen.getByText("toggle"));
    setItem.mockRestore();

    // The reader tolerates a bad value, so the writer has to tolerate a refused
    // one: throwing during commit blanks the app for a layout toggle.
    expect(screen.getByTestId("compact").textContent).toBe("false");
  });
});

describe("useSettings", () => {
  it("throws when used outside a SettingsProvider", () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    expect(() => render(<Harness />)).toThrow(
      "useSettings must be used within a SettingsProvider",
    );
    spy.mockRestore();
  });
});
