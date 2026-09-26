import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { useQueryLanguage } from "./useQueryLanguage";
import type { ResolveSource } from "./resolve";

const src = {
  accounts: [{ id: "11111111-1111-4111-8111-111111111111", name: "Checking" }],
  categories: [],
  groups: [],
  payees: [],
  tags: [],
} as unknown as ResolveSource;

const setup = (ownerQ = "") =>
  renderHook(() => useQueryLanguage({ source: src, ownerQ }));

// The in-progress token rule is the most important behaviour in the feature:
// lenient matching plus autocomplete means every keystroke is a parse, and
// without this rule the warning banner strobes while the user types.
describe("useQueryLanguage in-progress tokens", () => {
  it("reports nothing while the caret is in the last token", () => {
    const { result } = setup();
    act(() => result.current.setText("catg"));
    act(() => result.current.setInProgress("catg"));
    expect(result.current.diagnostics).toEqual([]);
  });

  it("stays quiet on a half-typed field name", () => {
    const { result } = setup();
    act(() => result.current.setText("cat"));
    act(() => result.current.setInProgress("cat"));
    expect(result.current.diagnostics).toEqual([]);
  });

  it("stays quiet on a field waiting for its value", () => {
    const { result } = setup();
    act(() => result.current.setText("cat:"));
    act(() => result.current.setInProgress("cat:"));
    expect(result.current.diagnostics).toEqual([]);
  });

  it("reports once the caret moves left and the token is finished", () => {
    const { result } = setup();
    act(() => result.current.setText("catgory:food"));
    act(() => result.current.setInProgress("catgory:food"));
    expect(result.current.diagnostics).toEqual([]);
    act(() => result.current.setInProgress(null));
    expect(result.current.diagnostics.map((d) => d.code)).toEqual(["unknown_field"]);
  });

  it("treats a half-typed field name as a plain search, not an error", () => {
    // `catg` has no colon and no operator, so it is a legitimate free-text
    // search. Quiet is the correct answer, and it is the common case while the
    // user is still deciding whether they meant a field or a word.
    const { result } = setup();
    act(() => result.current.setText("catg"));
    act(() => result.current.setInProgress(null));
    expect(result.current.diagnostics).toEqual([]);
    expect(result.current.q).toBe("catg");
  });

  it("finalize reports a half-typed value, so `cat:` + Enter is an error", () => {
    const { result } = setup();
    act(() => result.current.setText("cat:"));
    act(() => result.current.setInProgress("cat:"));
    expect(result.current.diagnostics).toEqual([]);
    act(() => result.current.finalize());
    expect(result.current.diagnostics.map((d) => d.code)).toEqual(["missing_value"]);
  });

  it("does not serialize the in-progress token", () => {
    const { result } = setup();
    act(() => result.current.setText("amt>50 am"));
    act(() => result.current.setInProgress("am"));
    expect(result.current.q).toBe("amt>5000");
  });

  it("sends nothing at all when the only token is in progress", () => {
    const { result } = setup();
    act(() => result.current.setText("cat"));
    act(() => result.current.setInProgress("cat"));
    expect(result.current.q).toBe("");
  });
});

describe("useQueryLanguage diagnostics", () => {
  it("resolves names locally, so the warning needs no round trip", () => {
    const { result } = setup();
    act(() => result.current.setText("payee:Costco"));
    act(() => result.current.setInProgress(null));
    expect(result.current.diagnostics.map((d) => d.code)).toEqual(["unresolved_value"]);
    expect(result.current.diagnostics[0].message).toContain("Costco");
  });

  it("accepts a name that resolves, with no diagnostics", () => {
    const { result } = setup();
    act(() => result.current.setText("acct:Checking"));
    act(() => result.current.setInProgress(null));
    expect(result.current.diagnostics).toEqual([]);
    expect(result.current.q).toBe("acct:11111111-1111-4111-8111-111111111111");
  });

  // A slow response for a query the user has already replaced must not annotate
  // the new one.
  it("ignores server diagnostics that belong to a different query", () => {
    const { result } = setup("amt>5000");
    act(() =>
      result.current.setServerDiagnostics([
        { term: "cat:bogus", code: "unresolved_value", message: "no category", position: 0 },
      ]),
    );
    act(() => result.current.setText("coffee"));
    expect(result.current.diagnostics).toEqual([]);
  });

  it("shows server diagnostics that belong to the current query", () => {
    const { result } = setup("coffee");
    act(() => result.current.setText("coffee"));
    act(() =>
      result.current.setServerDiagnostics([
        { term: "coffee~", code: "bad_operator", message: "malformed operator", position: 0 },
      ]),
    );
    expect(result.current.diagnostics.map((d) => d.code)).toEqual(["bad_operator"]);
  });

  it("dedupes a term both sources reported", () => {
    const { result } = setup("payee:Costco");
    act(() => result.current.setText("payee:Costco"));
    act(() =>
      result.current.setServerDiagnostics([
        { term: "payee:Costco", code: "unresolved_value", message: "server says no", position: 0 },
      ]),
    );
    expect(result.current.diagnostics).toHaveLength(1);
  });
});

describe("useQueryLanguage clearing", () => {
  it("clear empties the text, the wire value and the diagnostics", () => {
    const { result } = setup();
    act(() => result.current.setText("payee:Costco"));
    act(() => result.current.setInProgress(null));
    expect(result.current.diagnostics.length).toBeGreaterThan(0);
    act(() => result.current.clear());
    expect(result.current.text).toBe("");
    expect(result.current.q).toBe("");
    expect(result.current.diagnostics).toEqual([]);
  });
});
