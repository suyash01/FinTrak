import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import DeleteTransactionDialogs from "./DeleteTransactionDialogs";

if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

function renderDialogs(
  overrides: Partial<Parameters<typeof DeleteTransactionDialogs>[0]> = {},
) {
  const props = {
    deleteTxnId: null as string | null,
    onCancelDelete: vi.fn(),
    onConfirmDelete: vi.fn(),
    bulkDeleteOpen: false,
    onBulkDeleteOpenChange: vi.fn(),
    selectedCount: 0,
    onConfirmBulkDelete: vi.fn(),
    ...overrides,
  };
  const view = render(<DeleteTransactionDialogs {...props} />);
  return { ...view, props };
}

describe("DeleteTransactionDialogs", () => {
  it("renders no dialog when nothing is pending", () => {
    renderDialogs();
    expect(screen.queryByText("Delete this transaction?")).toBeNull();
    expect(screen.queryByText(/selected transactions\?/)).toBeNull();
  });

  it("confirms a single delete through the callbacks", async () => {
    const user = userEvent.setup();
    const { props } = renderDialogs({ deleteTxnId: "t1" });

    expect(screen.getByText("Delete this transaction?")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Delete" }));

    expect(props.onCancelDelete).toHaveBeenCalled();
    expect(props.onConfirmDelete).toHaveBeenCalledWith("t1");
  });

  it("cancels a single delete", async () => {
    const user = userEvent.setup();
    const { props } = renderDialogs({ deleteTxnId: "t1" });

    await user.click(screen.getByRole("button", { name: "Cancel" }));

    expect(props.onCancelDelete).toHaveBeenCalled();
    expect(props.onConfirmDelete).not.toHaveBeenCalled();
  });

  it("confirms a bulk delete and closes the dialog", async () => {
    const user = userEvent.setup();
    const { props } = renderDialogs({
      bulkDeleteOpen: true,
      selectedCount: 3,
    });

    expect(
      screen.getByText("Delete 3 selected transactions?"),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Delete" }));

    expect(props.onBulkDeleteOpenChange).toHaveBeenCalledWith(false);
    expect(props.onConfirmBulkDelete).toHaveBeenCalled();
  });
});
