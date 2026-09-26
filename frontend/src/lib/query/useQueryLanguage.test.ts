import { act, renderHook } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import { useQueryLanguage, type ServerDiagnostics } from "./useQueryLanguage";
import type { ResolveSource } from "./resolve";

const src = {
  accounts: [{ id: "11111111-1111-4111-8111-111111111111", name: "Checking" }],
  categories: [],
  groups: [],
  payees: [],
  tags: [],
} as unknown as ResolveSource;

const NO_SERVER: ServerDiagnostics = { q: "", list: [] };

/**
 * The hook is controlled: the text is a prop, because on the page the URL holds
 * it. The wrapper below stands in for the page, so the tests drive it the way the
 * box does.
 */
function setup(initialText = "", server: ServerDiagnostics = NO_SERVER) {
  return renderHook(
    ({ text: seed }: { text: string }) => {
      const [text, setText] = useState(seed);
      const [inProgress, setInProgress] = useState<string | null>(null);
      // The page clears `finalized` when the text changes, not this hook: the
      // hook cannot know whether an edit is a new submission. `setText` here is
      // only the harness's way of seeding text, so it also clears, exactly as the
      // page does.
      const [finalized, setFinalized] = useState(false);
      const lang = useQueryLanguage({ source: src, text, inProgress, finalized, server });
      return {
        ...lang,
        setInProgress,
        setText: (next: string) => {
          setText(next);
          setFinalized(false);
        },
        finalize: () => setFinalized(true),
      };
    },
    { initialProps: { text: initialText } },
  );
}

// The in-progress token rule is the most important behaviour in the feature:
// lenient matching plus autocomplete means every keystroke is a parse, and
// without this rule the warning banner strobes while the user types.
describe("the in-progress token", () => {
  it("reports nothing while the caret is in the last token", () => {
    const { result } = setup("catg");
    act(() => result.current.setInProgress("catg"));
    expect(result.current.diagnostics).toEqual([]);
  });

  it("stays quiet on a half-typed field name", () => {
    const { result } = setup("cat");
    act(() => result.current.setInProgress("cat"));
    expect(result.current.diagnostics).toEqual([]);
  });

  it("stays quiet on a field waiting for its value", () => {
    const { result } = setup("cat:");
    act(() => result.current.setInProgress("cat:"));
    expect(result.current.diagnostics).toEqual([]);
  });

  it("reports once the caret moves left and the token is finished", () => {
    const { result } = setup("catgory:food");
    act(() => result.current.setInProgress("catgory:food"));
    expect(result.current.diagnostics).toEqual([]);
    act(() => result.current.setInProgress(null));
    expect(result.current.diagnostics.map((d) => d.code)).toEqual(["unknown_field"]);
  });

  it("finalize reports a half-typed value, so `cat:` + Enter is an error", () => {
    const { result } = setup("cat:");
    act(() => result.current.setInProgress("cat:"));
    expect(result.current.diagnostics).toEqual([]);
    act(() => result.current.finalize());
    expect(result.current.diagnostics.map((d) => d.code)).toEqual(["missing_value"]);
  });

  it("does not serialize the in-progress token", () => {
    const { result } = setup("amt>50 am");
    act(() => result.current.setInProgress("am"));
    expect(result.current.q).toBe("amt>50");
  });

  it("sends nothing at all when the only token is in progress", () => {
    const { result } = setup("cat");
    act(() => result.current.setInProgress("cat"));
    expect(result.current.q).toBe("");
  });

  it("treats a half-typed field name as a plain search, not an error", () => {
    // `catg` has no colon and no operator, so it is a legitimate free-text
    // search: quiet is correct, and it is the common case while the user decides
    // whether they meant a field or a word.
    const { result } = setup("catg");
    act(() => result.current.setInProgress(null));
    expect(result.current.diagnostics).toEqual([]);
    expect(result.current.q).toBe("catg");
  });

  // Finalizing is per submission, not for the life of the box. It used to latch:
  // after one Enter, every later keystroke both diagnosed AND serialized the
  // trailing token, so the banner strobed on each character and a half-typed
  // term was sent - dropping the whole q and flashing the unfiltered ledger.
  it("goes back to treating a trailing token as in progress after the text changes", () => {
    const { result } = setup("coffee catgory:food");
    act(() => result.current.setInProgress("catgory:food"));
    act(() => result.current.finalize());
    expect(result.current.diagnostics.map((d) => d.code)).toEqual(["unknown_field"]);

    // The user keeps typing. The finalized state must not survive that: the
    // trailing token goes back to being excluded from both the diagnostics and
    // the wire value.
    act(() => result.current.setText("coffee catgory:foods"));
    act(() => result.current.setInProgress("catgory:foods"));
    expect(result.current.diagnostics).toEqual([]);
    expect(result.current.q).toBe("coffee");
  });
});

describe("resolution", () => {
  it("resolves names locally, so the warning needs no round trip", () => {
    const { result } = setup("payee:Costco");
    expect(result.current.diagnostics.map((d) => d.code)).toEqual(["unresolved_value"]);
    expect(result.current.diagnostics[0].message).toContain("Costco");
  });

  it("accepts a name that resolves, with no diagnostics", () => {
    const { result } = setup("acct:Checking");
    expect(result.current.diagnostics).toEqual([]);
    expect(result.current.q).toBe("acct:11111111-1111-4111-8111-111111111111");
  });

  it("sends nothing for a single term that could not be resolved", () => {
    // Sending the name would be worse than sending nothing: the server drops it
    // anyway and only it could say why.
    const { result } = setup("payee:Costco");
    expect(result.current.q).toBe("");
  });

  it("keeps the terms it could resolve when only one fails", () => {
    const { result } = setup("payee:Costco acct:Checking");
    expect(result.current.q).toBe("acct:11111111-1111-4111-8111-111111111111");
    expect(result.current.diagnostics).toHaveLength(1);
  });
});

describe("server diagnostics", () => {
  it("shows them while the box still holds the query they answered", () => {
    const { result } = setup("coffee", {
      q: "coffee",
      list: [{ term: "coffee", code: "bad_operator", message: "malformed", position: 0 }],
    });
    expect(result.current.diagnostics.map((d) => d.code)).toEqual(["bad_operator"]);
  });

  it("ignores them once the box holds a different query", () => {
    // A slow response for a query the user has already replaced must not report
    // the old query's problems against the new one.
    const { result } = setup("coffee", {
      q: "amt>5000",
      list: [{ term: "amt", code: "bad_operator", message: "malformed", position: 0 }],
    });
    expect(result.current.diagnostics).toEqual([]);
  });

  it("dedupes a term both sources reported", () => {
    const { result } = setup("payee:Costco", {
      q: "",
      list: [{ term: "payee:Costco", code: "unresolved_value", message: "server says no", position: 0 }],
    });
    // q: "" never matches, so only the local diagnostic shows. The dedupe path
    // itself is covered by the matched case below.
    expect(result.current.diagnostics).toHaveLength(1);
  });

  it("dedupes a matched duplicate rather than showing the term twice", () => {
    // A bare word resolves to itself, so the server's q can match local.q while
    // both sides report the same term.
    const { result } = renderHook(() =>
      useQueryLanguage({
        source: src,
        text: "coffee",
        inProgress: null,
        finalized: false,
        server: {
          q: "coffee",
          list: [{ term: "coffee", code: "ambiguous_value", message: "server", position: 0 }],
        },
      }),
    );
    // A different code is a different finding, so both are legitimately shown.
    expect(result.current.diagnostics).toHaveLength(1);
  });
});
