import { describe, it, expect } from "vitest";
import { valuesEqual, diffAgainstBase, mergeFields } from "./merge";

describe("valuesEqual", () => {
  it("treats an absent key and an empty collection as different", () => {
    // Review Focus #4. Emptying every tag is a change; not touching tags is not.
    expect(valuesEqual(undefined, [])).toBe(false);
    expect(valuesEqual([], [])).toBe(true);
  });
  it("compares tag collections without regard to order", () => {
    expect(valuesEqual(["a", "b"], ["b", "a"])).toBe(true);
  });
  it("distinguishes null (a clear) from an absent key", () => {
    expect(valuesEqual(null, undefined)).toBe(false);
    expect(valuesEqual(null, null)).toBe(true);
  });
});

describe("diffAgainstBase", () => {
  it("keeps only the fields whose value differs from the base", () => {
    const base = { notes: "", tags: ["a"], amount: 250.5, categoryId: "c1" };
    const mine = { notes: "coffee", tags: ["a"], amount: 250.5, categoryId: null };
    expect(diffAgainstBase(base, mine)).toEqual({ notes: "coffee", categoryId: null });
  });
  it("treats a field absent from mine as untouched", () => {
    expect(diffAgainstBase({ notes: "a" }, {})).toEqual({});
  });
  it("omits a field whose value is undefined rather than sending it", () => {
    expect(diffAgainstBase({ notes: "a" }, { notes: undefined })).toStrictEqual({});
  });

  // A form that always emits its optional fields — the transaction editor sends
  // categoryId, payeeId and billingCycleId whether or not the row has one — says
  // "no value" for a field that has never held a value, and there is nothing to
  // clear. Left in the diff it would read as a change the user never made.
  it("drops a null the base does not carry, because nothing was there to clear", () => {
    expect(diffAgainstBase({}, { categoryId: null })).toEqual({});
  });
  it("keeps a null the base does carry, because that is a clear", () => {
    expect(diffAgainstBase({ categoryId: "c1" }, { categoryId: null })).toEqual({
      categoryId: null,
    });
  });
  it("still diffs a real value set against a field the base never held", () => {
    // Only null means "no value". An empty string and an empty list are values
    // the user set, and setting one where there was none is a change.
    expect(diffAgainstBase({}, { notes: "", tags: [] })).toEqual({
      notes: "",
      tags: [],
    });
  });
});

describe("mergeFields", () => {
  it("takes theirs when the user did not change the field", () => {
    // rule 1
    const r = mergeFields({ notes: "a" }, { notes: "a" }, { notes: "b" });
    expect(r).toEqual({ patch: { notes: "b" }, conflicts: [] });
  });
  it("omits a resolved field whose value is undefined", () => {
    // The field is in the union because base carries it; rule 1 resolves it to
    // theirs, which is undefined. toStrictEqual so a present-with-undefined
    // patch fails rather than comparing equal to {}.
    expect(mergeFields({ notes: "a" }, { notes: "a" }, {})).toStrictEqual({
      patch: {},
      conflicts: [],
    });
  });
  it("takes mine when nobody else changed the field", () => {
    // rule 2
    const r = mergeFields({ notes: "a" }, { notes: "c" }, { notes: "a" });
    expect(r).toEqual({ patch: { notes: "c" }, conflicts: [] });
  });
  it("converges when both sides agree", () => {
    // rule 3
    const r = mergeFields({ notes: "a" }, { notes: "c" }, { notes: "c" });
    expect(r).toEqual({ patch: { notes: "c" }, conflicts: [] });
  });
  it("holds a conflict when both changed the field differently", () => {
    // rule 4 — nothing is written, so the field is absent from the patch
    const r = mergeFields({ notes: "a" }, { notes: "mine" }, { notes: "theirs" });
    expect(r.patch).toEqual({});
    expect(r.conflicts).toEqual([
      { field: "notes", base: "a", mine: "mine", theirs: "theirs" },
    ]);
  });
  it("holds a conflict when the user clears a field the server also changed", () => {
    const r = mergeFields(
      { categoryId: "a" },
      { categoryId: null },
      { categoryId: "b" },
    );
    expect(r.patch).toEqual({});
    expect(r.conflicts).toEqual([
      { field: "categoryId", base: "a", mine: null, theirs: "b" },
    ]);
  });
  it("takes theirs for a field the user's patch does not carry", () => {
    const r = mergeFields({}, {}, { notes: "b" });
    expect(r).toEqual({ patch: { notes: "b" }, conflicts: [] });
  });
  it("holds a conflict when the user set a field the base never held", () => {
    // There is no shared baseline, so the engine cannot say who moved it.
    // Resolving to theirs would silently discard the user's edit.
    const r = mergeFields({}, { notes: "a" }, { notes: "b" });
    expect(r.patch).toEqual({});
    expect(r.conflicts).toEqual([
      { field: "notes", base: undefined, mine: "a", theirs: "b" },
    ]);
  });
  it("merges the clean fields and holds only the conflicting one", () => {
    const r = mergeFields(
      { notes: "a", amount: 10 },
      { notes: "mine", amount: 20 },
      { notes: "theirs", amount: 10 },
    );
    expect(r.patch).toEqual({ amount: 20 });
    expect(r.conflicts.map((c) => c.field)).toEqual(["notes"]);
  });
  it("does no arithmetic on amounts", () => {
    // 0.1 + 0.2 !== 0.3 in float64; the engine compares, it never sums.
    const r = mergeFields({ amount: 0.1 }, { amount: 0.2 }, { amount: 0.1 });
    expect(r.patch).toEqual({ amount: 0.2 });
  });
});
