import type { Category, CategoryGroup } from "../types";

// The virtual "Uncategorized" sentinel: transactions with no category assigned.
// Used as a filter value and as the bulk-categorize "clear" target.
export const UNCATEGORIZED = "uncategorized";

export interface CategorySection {
  group: CategoryGroup;
  items: Category[];
}

// Groups visible categories into sections. Empty groups are hidden (the
// "Uncategorized" option is rendered separately and always shown). Base/global
// groups come first in sort-order, then the user's own custom groups.
export function buildCategorySections(
  groups: CategoryGroup[],
  categories: Category[],
): CategorySection[] {
  const sorted = [...groups].sort(
    (a, b) => (a.isGlobal === b.isGlobal ? a.sortOrder - b.sortOrder : a.isGlobal ? -1 : 1),
  );
  return sorted
    .map((group) => ({
      group,
      items: categories.filter((c) => c.groupId === group.id),
    }))
    .filter((s) => s.items.length > 0);
}