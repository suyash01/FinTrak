import { beforeEach, describe, expect, it, vi } from "vitest";
import { toast } from "sonner";
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
});
