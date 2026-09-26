import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { QueryInput } from "./QueryInput";
import type { ResolveSource } from "./resolve";

const src = {
  accounts: [{ id: "11111111-1111-4111-8111-111111111111", name: "Checking" }],
  categories: [
    { id: "22222222-2222-4222-8222-222222222222", name: "Groceries", groupId: "33333333-3333-4333-8333-333333333333" },
    { id: "44444444-4444-4444-8444-444444444444", name: "Groceries", groupId: "55555555-5555-4555-8555-555555555555" },
  ],
  groups: [
    { id: "33333333-3333-4333-8333-333333333333", name: "Food" },
    { id: "55555555-5555-4555-8555-555555555555", name: "Home" },
  ],
  payees: [{ id: "66666666-6666-4666-8666-666666666666", name: "Whole Foods" }],
  tags: ["vacation"],
} as unknown as ResolveSource;

const EMPTY = { accounts: [], categories: [], groups: [], payees: [], tags: [] } as unknown as ResolveSource;

// The box is controlled: the page owns the text (the URL does), so the harness
// holds it in state exactly as TransactionFilters does.
function setup(source: ResolveSource = src, initial = "") {
  const onSubmit = vi.fn();
  const Wrapper = () => {
    const [text, setText] = useState(initial);
    return (
      <QueryInput
        source={source}
        value={text}
        onValueChange={setText}
        onSubmit={onSubmit}
        diagnostics={[]}
      />
    );
  };
  render(<Wrapper />);
  return { onSubmit, input: screen.getByRole("combobox") as HTMLInputElement };
}

const value = (input: HTMLInputElement) => input.value;

describe("QueryInput autocomplete", () => {
  it("suggests field names on a fresh token", async () => {
    const user = userEvent.setup();
    const { input } = setup();
    await user.type(input, "ca");
    expect(await screen.findByText("cat")).toBeTruthy();
  });

  it("suggests an enum for a field with a fixed domain", async () => {
    const user = userEvent.setup();
    const { input } = setup();
    await user.type(input, "type:");
    expect(await screen.findByText("debit")).toBeTruthy();
    expect(await screen.findByText("credit")).toBeTruthy();
  });

  // Insertion must quote, or the suggestion produces a query that parses as two
  // tokens and silently searches for the wrong thing.
  it("quotes an inserted value containing a space", async () => {
    const user = userEvent.setup();
    const { input } = setup();
    await user.type(input, "payee:Whole");
    const option = await screen.findByText("Whole Foods");
    await user.click(option);
    expect(value(input)).toBe('payee:"Whole Foods"');
  });

  it("disambiguates a duplicated category name by its group", async () => {
    const user = userEvent.setup();
    const { input } = setup();
    await user.type(input, "cat:Grocer");
    const option = await screen.findByText("Food/Groceries");
    await user.click(option);
    expect(value(input)).toBe("cat:Food/Groceries");
  });

  it("suggests a tag name unquoted when it has no space", async () => {
    const user = userEvent.setup();
    const { input } = setup();
    await user.type(input, "tag:vac");
    const option = await screen.findByText("vacation");
    await user.click(option);
    expect(value(input)).toBe("tag:vacation");
  });

  it("suggests only the periods that mean what they say", async () => {
    const user = userEvent.setup();
    const { input } = setup();
    await user.type(input, "date:");
    expect(await screen.findByText("last_12_months")).toBeTruthy();
    expect(screen.queryByText("custom")).toBeNull();
  });

  it("offers nothing for a field it has no data for", async () => {
    const user = userEvent.setup();
    const { input } = setup(EMPTY);
    await user.type(input, "payee:");
    await waitFor(() => expect(screen.queryByRole("option")).toBeNull());
  });
});

describe("QueryInput keys", () => {
  // Enter must run the query, never accept a suggestion: the box this replaced
  // ran on Enter, and that reflex is worth more than autocomplete convenience.
  // It submits rather than writing a value, because the resolved query is derived
  // from the text continuously and writing it back would replace what was typed.
  it("submits on Enter without accepting the highlighted suggestion", async () => {
    const user = userEvent.setup();
    const { input, onSubmit } = setup();
    await user.type(input, "cat:Food/Groceries{Enter}");
    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(value(input)).toBe("cat:Food/Groceries");
  });

  it("leaves a bare word in the box rather than rewriting it", async () => {
    const user = userEvent.setup();
    const { input, onSubmit } = setup();
    await user.type(input, "coffee{Enter}");
    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(value(input)).toBe("coffee");
  });

  it("leaves a typed name in the box rather than replacing it with an id", async () => {
    // The user must keep seeing what they wrote; the id goes to the server only.
    const user = userEvent.setup();
    const { input } = setup();
    await user.type(input, "acct:Checking{Enter}");
    expect(value(input)).toBe("acct:Checking");
  });

  it("accepts the highlighted suggestion on Tab", async () => {
    const user = userEvent.setup();
    const { input, onSubmit } = setup();
    await user.type(input, "cat:Food/Groceries ");
    await user.keyboard("{Tab}");
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("closes the suggestions on Escape", async () => {
    const user = userEvent.setup();
    const { input } = setup();
    await user.type(input, "ca");
    expect(await screen.findByText("cat")).toBeTruthy();
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByText("cat")).toBeNull());
  });
});

describe("QueryInput diagnostics", () => {
  it("shows each ignored term and says the results are wider than asked", async () => {
    render(
      <QueryInput
        source={src}
        value=""
        onValueChange={vi.fn()}
        onSubmit={vi.fn()}
        diagnostics={[
          { term: "payee:Costco", code: "unresolved_value", message: 'no payee named "Costco"', position: 0 },
        ]}
      />,
    );
    expect(await screen.findByText(/1 term was ignored/)).toBeTruthy();
    expect(screen.getByText("payee:Costco")).toBeTruthy();
    expect(screen.getByText(/wider than you asked for/)).toBeTruthy();
  });

  it("shows no banner at all when nothing was ignored", () => {
    const { container } = render(<QueryInput source={src} value="" onValueChange={vi.fn()} diagnostics={[]} />);
    expect(container.querySelector('[role="status"]')).toBeNull();
  });

  it("pluralises the count", async () => {
    render(
      <QueryInput
        source={src}
        value=""
        onValueChange={vi.fn()}
        onSubmit={vi.fn()}
        diagnostics={[
          { term: "a:1", code: "unknown_field", message: "x", position: 0 },
          { term: "b:2", code: "unknown_field", message: "y", position: 4 },
        ]}
      />,
    );
    expect(await screen.findByText(/2 terms were ignored/)).toBeTruthy();
  });
});

describe("QueryInput grammar sheet", () => {
  it("lists the fields from the same table the suggestions come from", async () => {
    const user = userEvent.setup();
    setup();
    await user.click(screen.getByRole("button", { name: /query syntax/i }));
    // Both a field with a fixed domain and a plain text field must be described,
    // so the documented syntax cannot drift from the accepted one.
    expect(await screen.findByText("debit | credit")).toBeTruthy();
    expect(screen.getByText("a decimal in major units; supports > >= < <= = !=")).toBeTruthy();
  });
});
