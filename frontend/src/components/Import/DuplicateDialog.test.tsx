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

  it("keeps the warning text legible on the light theme", () => {
    renderDialog({ partialExisting: true });
    // The dialog renders in a portal, so assert against the document.
    expect(document.body.innerHTML).not.toMatch(/text-amber-(200|300|400)/);
    expect(
      screen.getByText("Duplicate transactions found").className,
    ).toContain("text-foreground");
    expect(
      screen.getByText(/existing-duplicate check may be incomplete/).className,
    ).toContain("text-muted-foreground");
    // Amber stays on the icon as the warning affordance.
    expect(document.querySelector("svg.text-amber-500")).not.toBeNull();
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

  it("renders nothing while closed", () => {
    renderDialog({ open: false });
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("closes through onOpenChange when cancelled", async () => {
    const user = userEvent.setup();
    const props = renderDialog();
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(props.onOpenChange).toHaveBeenCalledWith(false);
  });

  it("disables every action while importing", () => {
    renderDialog({ importing: true });
    expect(
      screen.getByRole("button", { name: "Skip duplicates" }),
    ).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "Keep all (import everything)" }),
    ).toBeDisabled();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
  });

  it("hides the in-file line when there are no within-file repeats", () => {
    renderDialog({ dupCount: 2, existingDupCount: 2, inFileDupCount: 0 });
    const items = screen
      .getAllByRole("listitem")
      .map((li) => li.textContent ?? "");
    expect(
      items.some((t) => t.includes("already exist in this account")),
    ).toBe(true);
    expect(items.some((t) => t.includes("within this file"))).toBe(false);
  });

  it("hides the existing-account line when no duplicates already exist", () => {
    renderDialog({ dupCount: 2, existingDupCount: 0, inFileDupCount: 2 });
    const items = screen
      .getAllByRole("listitem")
      .map((li) => li.textContent ?? "");
    expect(
      items.some((t) => t.includes("already exist in this account")),
    ).toBe(false);
    expect(
      items.some((t) => t.includes("repeat") && t.includes("within this file")),
    ).toBe(true);
  });

  it("uses singular wording for a single duplicated transaction", () => {
    renderDialog({
      dupCount: 1,
      includedCount: 1,
      existingDupCount: 1,
      inFileDupCount: 0,
    });
    expect(
      screen.getByText(/1 of the 1 transaction match/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/1 already exists in this account/),
    ).toBeInTheDocument();
  });
});
