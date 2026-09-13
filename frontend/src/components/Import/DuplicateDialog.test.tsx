import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import DuplicateDialog from "./DuplicateDialog";

function renderDialog(
  overrides: Partial<Parameters<typeof DuplicateDialog>[0]> = {},
) {
  const props = {
    open: true,
    onOpenChange: vi.fn(),
    dupCount: 2,
    includedCount: 5,
    existingDupCount: 1,
    inFileDupCount: 1,
    importing: false,
    onSkip: vi.fn(),
    onKeep: vi.fn(),
    ...overrides,
  };
  render(<DuplicateDialog {...props} />);
  return props;
}

describe("DuplicateDialog", () => {
  it("renders the duplicate breakdown", () => {
    renderDialog();
    expect(
      screen.getByText("Duplicate transactions found"),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/2 of the 5 transactions match/),
    ).toBeInTheDocument();
    const items = screen
      .getAllByRole("listitem")
      .map((li) => li.textContent ?? "");
    expect(items.some((t) => t.includes("already exists in this account"))).toBe(
      true,
    );
    expect(
      items.some((t) => t.includes("repeat") && t.includes("within this file")),
    ).toBe(true);
  });

  it("invokes skip and keep", async () => {
    const user = userEvent.setup();
    const props = renderDialog();
    await user.click(
      screen.getByRole("button", { name: "Skip duplicates" }),
    );
    await user.click(
      screen.getByRole("button", { name: "Keep all (import everything)" }),
    );
    expect(props.onSkip).toHaveBeenCalledTimes(1);
    expect(props.onKeep).toHaveBeenCalledTimes(1);
  });
});
