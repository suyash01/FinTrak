import { beforeEach, describe, expect, it, vi } from "vitest";
import { toast } from "sonner";
import { NetworkError } from "../api/errors";
import { toastApiError } from "./errors";

vi.mock("sonner", () => ({ toast: { error: vi.fn() } }));

describe("toastApiError", () => {
  beforeEach(() => {
    vi.mocked(toast.error).mockClear();
  });

  it("surfaces the API error message", () => {
    toastApiError(new Error("Invalid credentials"));
    expect(toast.error).toHaveBeenCalledWith("Invalid credentials");
  });

  it("falls back for an Error with an empty message", () => {
    toastApiError(new Error(""));
    expect(toast.error).toHaveBeenCalledWith(
      "Something went wrong. Please try again.",
    );
  });

  it("falls back for a non-Error throwable", () => {
    toastApiError("boom");
    expect(toast.error).toHaveBeenCalledWith(
      "Something went wrong. Please try again.",
    );
  });

  it("accepts a custom fallback", () => {
    toastApiError(null, "Import failed");
    expect(toast.error).toHaveBeenCalledWith("Import failed");
  });

  it("stays quiet about an unreachable server while offline", () => {
    const online = Object.getOwnPropertyDescriptor(window.navigator, "onLine");
    Object.defineProperty(window.navigator, "onLine", {
      configurable: true,
      value: false,
    });

    // The offline banner owns this message; a toast per failed read would be
    // noise on every page.
    toastApiError(new NetworkError());
    expect(toast.error).not.toHaveBeenCalled();

    Object.defineProperty(window.navigator, "onLine", {
      configurable: true,
      value: true,
    });
    toastApiError(new NetworkError());
    expect(toast.error).toHaveBeenCalledWith(
      "Network error: could not reach the API server",
    );

    if (online) Object.defineProperty(window.navigator, "onLine", online);
  });
});
