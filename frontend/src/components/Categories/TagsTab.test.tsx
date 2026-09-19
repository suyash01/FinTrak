import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import TagsTab from "./TagsTab";

if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

const { apiMock, toastMock } = vi.hoisted(() => ({
  apiMock: { getTags: vi.fn(), renameTag: vi.fn() },
  toastMock: { error: vi.fn(), success: vi.fn() },
}));

vi.mock("../../api/client", () => ({ default: apiMock }));
vi.mock("../../context/SettingsContext", () => ({
  useSettings: () => ({ compactLayout: false }),
}));
vi.mock("sonner", () => ({ toast: toastMock }));

const tags = [
  { name: "trip", count: 3 },
  { name: "reimbursable", count: 1 },
];

beforeEach(() => {
  vi.clearAllMocks();
  apiMock.getTags.mockResolvedValue({ data: tags });
  apiMock.renameTag.mockResolvedValue({ updated: 2 });
});

describe("TagsTab", () => {
  it("lists tags with usage counts", async () => {
    render(<TagsTab />);
    expect(await screen.findByText("trip")).toBeInTheDocument();
    expect(screen.getByText("reimbursable")).toBeInTheDocument();
    expect(screen.getByText("3")).toBeInTheDocument();
    expect(screen.getByText("2 tags")).toBeInTheDocument();
  });

  it("shows the empty state when there are no tags", async () => {
    apiMock.getTags.mockResolvedValue({ data: [] });
    render(<TagsTab />);
    expect(
      await screen.findByText(/No tags yet/),
    ).toBeInTheDocument();
  });

  it("renames a tag through the dialog", async () => {
    const user = userEvent.setup();
    render(<TagsTab />);
    await screen.findByText("trip");

    await user.click(screen.getByRole("button", { name: "Rename tag trip" }));
    const input = screen.getByLabelText("New name");
    await user.clear(input);
    await user.type(input, "travel");
    await user.click(screen.getByRole("button", { name: "Rename" }));

    await waitFor(() =>
      expect(apiMock.renameTag).toHaveBeenCalledWith({
        from: "trip",
        to: "travel",
      }),
    );
    expect(toastMock.success).toHaveBeenCalled();
  });

  it("surfaces a toast error when loading fails", async () => {
    apiMock.getTags.mockRejectedValue(new Error("boom"));
    render(<TagsTab />);
    await waitFor(() => expect(toastMock.error).toHaveBeenCalledWith("boom"));
  });
});
