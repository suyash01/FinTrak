import { describe, expect, it } from "vitest";
import { UNCATEGORIZED, buildCategorySections } from "./categories";
import type { Category, CategoryGroup } from "../types";

const group = (
  id: string,
  over: Partial<CategoryGroup> = {},
): CategoryGroup => ({
  id,
  name: id,
  icon: "",
  color: "#000000",
  isBase: false,
  isGlobal: false,
  sortOrder: 0,
  ...over,
});

const category = (id: string, groupId: string): Category => ({
  id,
  name: id,
  icon: "",
  color: "#000000",
  groupId,
});

describe("buildCategorySections", () => {
  it("orders global groups before custom ones, then by sortOrder", () => {
    const sections = buildCategorySections(
      [
        group("custom-early", { sortOrder: 0 }),
        group("global-late", { isGlobal: true, sortOrder: 5 }),
        group("global-early", { isGlobal: true, sortOrder: 1 }),
      ],
      [
        category("a", "custom-early"),
        category("b", "global-late"),
        category("c", "global-early"),
      ],
    );

    expect(sections.map((s) => s.group.id)).toEqual([
      "global-early",
      "global-late",
      "custom-early",
    ]);
  });

  it("attaches only the categories belonging to each group", () => {
    const sections = buildCategorySections(
      [group("g1"), group("g2")],
      [category("c1", "g1"), category("c2", "g2"), category("c3", "g1")],
    );

    expect(sections[0].items.map((c) => c.id)).toEqual(["c1", "c3"]);
    expect(sections[1].items.map((c) => c.id)).toEqual(["c2"]);
  });

  it("drops groups that have no categories", () => {
    const sections = buildCategorySections(
      [group("empty"), group("filled")],
      [category("c1", "filled")],
    );

    expect(sections.map((s) => s.group.id)).toEqual(["filled"]);
  });

  it("does not mutate the input groups slice", () => {
    const groups = [
      group("custom", { sortOrder: 0 }),
      group("global", { isGlobal: true, sortOrder: 0 }),
    ];
    const original = groups.map((g) => g.id);

    buildCategorySections(groups, []);

    expect(groups.map((g) => g.id)).toEqual(original);
  });

  it("exposes the uncategorized sentinel", () => {
    expect(UNCATEGORIZED).toBe("uncategorized");
  });
});
