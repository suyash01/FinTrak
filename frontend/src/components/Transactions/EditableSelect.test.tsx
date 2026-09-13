import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import EditableSelect from "./EditableSelect";

describe("EditableSelect", () => {
  it("renders a flat option list with the placeholder", () => {
    render(
      <EditableSelect
        value="b"
        options={[
          { value: "a", label: "Alpha" },
          { value: "b", label: "Beta" },
        ]}
        onChange={() => {}}
        placeholder="No Payee"
      />,
    );
    expect(screen.getByRole("combobox")).toHaveValue("b");
    expect(screen.getByRole("option", { name: "Alpha" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "No Payee" })).toBeInTheDocument();
  });

  it("renders grouped options as optgroups", () => {
    const { container } = render(
      <EditableSelect
        value="c1"
        optionGroups={[
          {
            label: "Food",
            options: [{ value: "c1", label: "Groceries" }],
          },
        ]}
        onChange={() => {}}
        placeholder="Uncategorized"
      />,
    );
    const optgroup = container.querySelector("optgroup");
    expect(optgroup).not.toBeNull();
    expect(optgroup?.getAttribute("label")).toBe("Food");
  });

  it("shows a hidden fallback option for an unknown value", () => {
    const { container } = render(
      <EditableSelect
        value="missing"
        options={[{ value: "a", label: "Alpha" }]}
        onChange={() => {}}
        placeholder="Uncategorized"
        displayText="Legacy Category"
      />,
    );
    const fallback = container.querySelector<HTMLOptionElement>("option[hidden]");
    expect(fallback).not.toBeNull();
    expect(fallback?.textContent).toBe("Legacy Category");
    expect(fallback?.value).toBe("missing");
  });

  it("emits the selected value on change", () => {
    const onChange = vi.fn();
    render(
      <EditableSelect
        value="a"
        options={[
          { value: "a", label: "Alpha" },
          { value: "b", label: "Beta" },
        ]}
        onChange={onChange}
        placeholder="None"
      />,
    );
    fireEvent.change(screen.getByRole("combobox"), {
      target: { value: "b" },
    });
    expect(onChange).toHaveBeenCalledWith("b");
  });
});
