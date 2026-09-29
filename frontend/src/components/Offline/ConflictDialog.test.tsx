import { describe, it, expect, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import ConflictDialog from "./ConflictDialog";
import type { EditEntry, QueuedWrite } from "@/api/outbox";

// The fixtures are hand-built rather than taken from the queue module: they are
// the shape the *dialog* reads, and building them through enqueueEdit would put
// this test at the mercy of localStorage and of the enqueue's own base
// derivation — neither of which is what any case here is about.
function conflictedEdit(overrides: Partial<EditEntry> = {}): QueuedWrite {
  return {
    key: "key-1",
    queuedAt: 1,
    kind: "edit",
    op: "transaction.patch",
    rowId: "txn-1",
    base: { notes: "a" },
    patch: { notes: "mine" },
    snapshot: { description: "Coffee", amount: 250.5, notes: "a" },
    conflict: {
      units: [{ rowId: "txn-1", field: "notes", base: "a", mine: "mine", theirs: "theirs" }],
    },
    ...overrides,
  };
}

function twoFieldConflict(): QueuedWrite {
  return conflictedEdit({
    conflict: {
      units: [
        { rowId: "txn-1", field: "notes", base: "a", mine: "mine", theirs: "theirs" },
        { rowId: "txn-1", field: "amount", base: 250.5, mine: 300, theirs: 275 },
      ],
    },
  });
}

function goneEdit(overrides: Partial<EditEntry> = {}): QueuedWrite {
  return {
    key: "key-1",
    queuedAt: 1,
    kind: "edit",
    op: "transaction.patch",
    rowId: "txn-1",
    base: { description: "Coffee" },
    patch: { description: "Tea" },
    snapshot: { description: "Coffee", amount: 250.5 },
    gone: true,
    ...overrides,
  };
}

// The props a case starts from, so a case that only wants a different queue
// says so and the rest of them all start from the same place.
function props(
  overrides: Partial<Parameters<typeof ConflictDialog>[0]> = {},
): Parameters<typeof ConflictDialog>[0] {
  return {
    open: true,
    onOpenChange: vi.fn(),
    entries: [conflictedEdit()],
    onResolve: vi.fn(),
    onReCreate: vi.fn(),
    onDiscard: vi.fn(),
    ...overrides,
  };
}

function renderDialog(
  overrides: Partial<Parameters<typeof ConflictDialog>[0]> = {},
) {
  const merged = props(overrides);
  render(<ConflictDialog {...merged} />);
  return merged;
}

describe("ConflictDialog", () => {
  it("shows both values and the base for each conflicting field", () => {
    renderDialog({ entries: [conflictedEdit()] });

    // Without the base the user cannot tell which of the two values is their own
    // change, so all three are rendered and the field is named.
    expect(screen.getByText("Notes")).toBeInTheDocument();
    expect(screen.getByText("mine")).toBeInTheDocument();
    expect(screen.getByText("theirs")).toBeInTheDocument();
    expect(screen.getByText("a")).toBeInTheDocument();
  });

  it("names the three columns, so a value is not just a string in a table", () => {
    renderDialog();
    expect(screen.getByRole("columnheader", { name: "Your value" })).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "Their value" })).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "Base" })).toBeInTheDocument();
  });

  it("reads a field name the user can recognise rather than the API's key", () => {
    renderDialog({
      entries: [
        conflictedEdit({
          conflict: {
            units: [
              { rowId: "txn-1", field: "categoryId", base: null, mine: "cat-2", theirs: "cat-3" },
            ],
          },
        }),
      ],
    });
    // camelCase and a trailing Id are both noise; the field is "category".
    expect(screen.getByText("Category")).toBeInTheDocument();
    expect(screen.queryByText("categoryId")).toBeNull();
  });

  it("renders an em-dash for a base the client never held, not the string undefined", () => {
    renderDialog({
      entries: [
        conflictedEdit({
          conflict: {
            units: [
              { rowId: "txn-1", field: "notes", base: undefined, mine: "mine", theirs: "theirs" },
            ],
          },
        }),
      ],
    });
    expect(screen.getByText("—")).toBeInTheDocument();
    expect(screen.queryByText("undefined")).toBeNull();
    expect(document.body.innerHTML).not.toContain("undefined");
  });

  it("renders a list value as the list a user would read, not a JS array", () => {
    renderDialog({
      entries: [
        conflictedEdit({
          conflict: {
            units: [
              {
                rowId: "txn-1",
                field: "tags",
                base: ["a"],
                mine: ["a", "b"],
                theirs: ["c"],
              },
            ],
          },
        }),
      ],
    });
    // `tags` is a set to the user, and `String(["a","b"])` happening to produce
    // "a,b" is not a rendering anyone chose.
    expect(screen.getByText("a, b")).toBeInTheDocument();
    expect(screen.getByText("c")).toBeInTheDocument();
  });

  it("writes nothing until the user saves", async () => {
    const user = userEvent.setup();
    const props = renderDialog({ entries: [twoFieldConflict()] });

    // The default is the user's own value, so the initial state already says
    // "keep mine" — it must still be the user's click that records it.
    await user.click(screen.getByRole("button", { name: /keep theirs for notes/i }));
    await user.click(screen.getByRole("button", { name: /keep mine for amount/i }));
    expect(props.onResolve).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: /^save$/i }));
    expect(props.onResolve).toHaveBeenCalledTimes(1);
  });

  it("resolves per field and reports the whole resolution", async () => {
    const user = userEvent.setup();
    const props = renderDialog({ entries: [twoFieldConflict()] });

    // One call for the entry, carrying an answer for every field it holds — the
    // queue keys a resolution by field and takes it as an override of the patch,
    // so a partial record would leave a field unanswered and re-held.
    await user.click(screen.getByRole("button", { name: /keep theirs for notes/i }));
    await user.click(screen.getByRole("button", { name: /keep mine for amount/i }));
    await user.click(screen.getByRole("button", { name: /^save$/i }));

    expect(props.onResolve).toHaveBeenCalledWith("key-1", { notes: "theirs", amount: "mine" });
  });

  it("keeps your own value by default and shows which side is chosen", async () => {
    const user = userEvent.setup();
    renderDialog({ entries: [conflictedEdit()] });

    const mine = screen.getByRole("button", { name: /keep mine for notes/i });
    const theirs = screen.getByRole("button", { name: /keep theirs for notes/i });
    expect(mine).toHaveAttribute("aria-pressed", "true");
    expect(theirs).toHaveAttribute("aria-pressed", "false");

    await user.click(theirs);
    expect(theirs).toHaveAttribute("aria-pressed", "true");
    expect(mine).toHaveAttribute("aria-pressed", "false");
  });

  it("takes every field of the entry with the keep-all shortcuts", async () => {
    const user = userEvent.setup();
    const props = renderDialog({ entries: [twoFieldConflict()] });

    await user.click(screen.getByRole("button", { name: /^keep all theirs$/i }));
    await user.click(screen.getByRole("button", { name: /^save$/i }));
    expect(props.onResolve).toHaveBeenCalledWith("key-1", { notes: "theirs", amount: "theirs" });

    // And back the other way: a shortcut is a setter, not a toggle, so it
    // overrides a per-field answer rather than inverting whatever came before.
    await user.click(screen.getByRole("button", { name: /keep mine for notes/i }));
    await user.click(screen.getByRole("button", { name: /^keep all mine$/i }));
    await user.click(screen.getByRole("button", { name: /^save$/i }));
    expect(props.onResolve).toHaveBeenLastCalledWith("key-1", { notes: "mine", amount: "mine" });
  });

  it("reports each held entry under its own key", async () => {
    const user = userEvent.setup();
    const props = renderDialog({
      entries: [conflictedEdit(), conflictedEdit({ key: "key-2" })],
    });

    await user.click(screen.getByRole("button", { name: /^save$/i }));

    // Several entries can be held at once and nothing selects between them, so
    // every one of them is answered and none is answered for another.
    expect(props.onResolve).toHaveBeenCalledTimes(2);
    expect(props.onResolve).toHaveBeenCalledWith("key-1", { notes: "mine" });
    expect(props.onResolve).toHaveBeenCalledWith("key-2", { notes: "mine" });
  });

  it("offers re-create for a gone row, and discard behind a confirm", async () => {
    const user = userEvent.setup();
    const props = renderDialog({ entries: [goneEdit()] });

    await user.click(screen.getByRole("button", { name: /re-create/i }));
    expect(props.onReCreate).toHaveBeenCalledWith("key-1");

    // Discard is destructive, so it is behind an AlertDialog like the banner's.
    await user.click(screen.getByRole("button", { name: /^discard$/i }));
    expect(props.onDiscard).not.toHaveBeenCalled();

    const alert = await screen.findByRole("alertdialog");
    await user.click(within(alert).getByRole("button", { name: /^discard$/i }));
    expect(props.onDiscard).toHaveBeenCalledWith("key-1");
  });

  it("names the row a gone entry lost, with what it held", () => {
    renderDialog({ entries: [goneEdit()] });

    expect(
      screen.getByRole("heading", { name: /no longer exist/i }),
    ).toBeInTheDocument();
    // The row is named by what it held, so the user can recognise it before
    // deciding whether to write it again — and formatNumber, not a raw float: a
    // queued entry carries no account, so there is no currency to name and the
    // repo's currency-free formatter is the one that applies.
    expect(screen.getByText(/Coffee/)).toHaveTextContent("Coffee · 250.50");
  });

  it("names a gone row it has no description or amount for, by its id", () => {
    // Not every op's projection carries either: a payee row is `name` and
    // `accountId`. Inventing a description for it would be a guess, so the row
    // itself is named instead.
    renderDialog({
      entries: [
        goneEdit({
          op: "payee.put",
          base: { name: "Butcher" },
          patch: { name: "Greengrocer" },
          snapshot: { name: "Butcher" },
        }),
      ],
    });
    expect(screen.getByText(/txn-1 was edited offline/)).toBeInTheDocument();
    expect(screen.queryByText(/undefined/)).toBeNull();
  });

  it("does not offer re-create for a row that is merely conflicted", () => {
    // The context refuses it, because a create against a row the server still
    // has would insert a second money row — and an action that fails is worse
    // than an action that is absent.
    renderDialog({ entries: [conflictedEdit()] });
    expect(screen.queryByRole("button", { name: /re-create/i })).toBeNull();
  });

  it("does not offer re-create for a row the op has no create endpoint for", () => {
    // The kind is not enough: only transaction.patch declares a reCreate, so a
    // gone account would answer the context's refusal with an error toast. The
    // dialog asks the registry rather than keeping its own list of which ops can
    // be re-created, so a new op's answer is the registry's too.
    renderDialog({
      entries: [goneEdit({ op: "account.put", base: { name: "Old" }, patch: { name: "New" }, snapshot: { name: "Old" } })],
    });

    expect(screen.queryByRole("button", { name: /re-create/i })).toBeNull();
    // Discard is the only way out, so it has to be there.
    expect(screen.getByRole("button", { name: /^discard$/i })).toBeInTheDocument();
  });

  it("asks for no choice it cannot be given when every hold is a gone row", async () => {
    const user = userEvent.setup();
    const props = renderDialog({ entries: [goneEdit()] });

    // Nothing is conflicted, so the dialog must not tell the user to choose a
    // version per field when no field is on screen, and it must not offer a
    // primary action that records nothing.
    expect(
      screen.queryByText(/choose which version to keep/i),
    ).toBeNull();
    expect(screen.queryByRole("button", { name: /^save$/i })).toBeNull();

    // Asserted positively as well as negatively, because a branch that rendered
    // nothing at all would satisfy every absence above and leave Radix pointing
    // `aria-describedby` at an empty paragraph. This row's op is
    // transaction.patch, so writing it again is on offer and the copy says so.
    expect(
      screen.getByText(/Write it again as a new row, or discard it\./),
    ).toBeInTheDocument();

    // Re-create and discard are the whole decision, and both still work.
    await user.click(screen.getByRole("button", { name: /re-create/i }));
    expect(props.onReCreate).toHaveBeenCalledWith("key-1");
    expect(props.onResolve).not.toHaveBeenCalled();
  });

  it("does not tell a gone row to be written again when the op cannot re-create it", () => {
    // The same branch, for an op with no create endpoint. Re-create is absent
    // there — the button condition asks the registry — so instructing the user to
    // write the row again would be a promise the section does not keep, which is
    // finding 2's own defect in new words.
    renderDialog({
      entries: [
        goneEdit({
          op: "account.put",
          base: { name: "Old" },
          patch: { name: "New" },
          snapshot: { name: "Old" },
        }),
      ],
    });

    expect(screen.queryByRole("button", { name: /re-create/i })).toBeNull();
    expect(screen.queryByText(/Write it again/i)).toBeNull();
    // And it still says something, and something true: the row is gone, and
    // discarding is the only action on offer.
    expect(
      screen.getByText(/no longer exists on the server, so none of this was written\./),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/can only be discarded from here\./),
    ).toBeInTheDocument();
  });

  it("forgets a resolved entry's picks if the same key is held again", async () => {
    const user = userEvent.setup();
    const base = props();
    const { rerender } = render(<ConflictDialog {...base} />);

    await user.click(screen.getByRole("button", { name: /keep theirs for notes/i }));
    expect(screen.getByRole("button", { name: /keep theirs for notes/i })).toHaveAttribute(
      "aria-pressed",
      "true",
    );

    // The entry is answered, so it leaves the held set — and a later flush can
    // hold the very same key again, which is a new question about a row the user
    // has not seen.
    rerender(<ConflictDialog {...base} entries={[]} />);
    rerender(<ConflictDialog {...base} />);

    // State and display have to agree: nothing on screen shows the old click, so
    // the answer starts at the default again rather than arriving pre-answered.
    expect(screen.getByRole("button", { name: /keep mine for notes/i })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(
      screen.getByRole("button", { name: /keep theirs for notes/i }),
    ).toHaveAttribute("aria-pressed", "false");
  });

  it("does not offer re-create for a bulk write, which cannot be re-created", () => {
    renderDialog({
      entries: [
        {
          key: "key-2",
          queuedAt: 1,
          kind: "bulk",
          op: "transaction.categorize",
          field: "categoryId",
          value: "cat-2",
          rows: ["txn-1", "txn-2", "txn-3"],
          bases: { "txn-1": null, "txn-2": null, "txn-3": null },
          gone: true,
        },
      ],
    });

    expect(screen.queryByRole("button", { name: /re-create/i })).toBeNull();
    // Rows, not "one change": the copy has to agree with the count of what was
    // lost, or a bulk of three reads as a single row.
    expect(
      screen.getByRole("heading", { name: "3 rows no longer exist on the server" }),
    ).toBeInTheDocument();
    // And the field is named, because a bulk write is one field over many rows
    // and "3 rows" alone does not say what the user was trying to do.
    expect(screen.getByText(/set Category to cat-2/)).toBeInTheDocument();
  });

  it("never answers a gone entry as a field resolution", async () => {
    const user = userEvent.setup();
    const props = renderDialog({ entries: [conflictedEdit(), goneEdit({ key: "key-2" })] });

    await user.click(screen.getByRole("button", { name: /^save$/i }));

    // A gone row has no fields to choose between; saving the rest must not
    // report it as resolved.
    expect(props.onResolve).toHaveBeenCalledTimes(1);
    expect(props.onResolve).toHaveBeenCalledWith("key-1", { notes: "mine" });
  });

  it("renders nothing while closed, and nothing when nothing is held", () => {
    const { unmount } = render(<ConflictDialog {...props({ open: false })} />);
    expect(screen.queryByRole("dialog")).toBeNull();
    unmount();

    render(<ConflictDialog {...props({ entries: [] })} />);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("uses semantic tokens rather than a raw palette", () => {
    renderDialog();
    // The dialog renders in a portal, so assert against the document.
    expect(document.body.innerHTML).not.toMatch(
      /\b(?:bg|text|border)-(?:red|blue|green|yellow|amber|orange|purple|pink|indigo|violet|teal|cyan|sky|lime|emerald|slate|gray|zinc|neutral|stone)-\d{2,3}\b/,
    );
  });
});
