import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import ImportSteps from "./ImportSteps";

describe("ImportSteps", () => {
  it("renders every wizard step label", () => {
    render(<ImportSteps step={1} onSelect={vi.fn()} />);
    expect(screen.getByText("Select Account")).toBeInTheDocument();
    expect(screen.getByText("Upload CSV")).toBeInTheDocument();
    expect(screen.getByText("Map Columns")).toBeInTheDocument();
    expect(screen.getByText("Preview")).toBeInTheDocument();
    expect(screen.getByText("Done")).toBeInTheDocument();
  });

  it("marks the current step with aria-current", () => {
    render(<ImportSteps step={3} onSelect={vi.fn()} />);
    expect(screen.getByText("Map Columns").closest("[aria-current]")).toHaveAttribute(
      "aria-current",
      "step",
    );
  });

  it("calls onSelect for completed steps", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<ImportSteps step={4} onSelect={onSelect} />);

    await user.click(screen.getByText("Select Account"));
    expect(onSelect).toHaveBeenCalledWith(1);
  });

  it("does not call onSelect for the current or future steps", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<ImportSteps step={2} onSelect={onSelect} />);

    await user.click(screen.getByText("Upload CSV"));
    await user.click(screen.getByText("Preview"));
    expect(onSelect).not.toHaveBeenCalled();
  });
});
