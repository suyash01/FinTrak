export const URL_PARAMS = [
  "search",
  "accountId",
  "categoryId",
  "groupId",
  "payeeId",
  "type",
  "dateFrom",
  "dateTo",
  "linked",
  "tags",
  "sortBy",
  "sortOrder",
  "page",
];

export const DEFAULT_FILTERS: Record<string, string | number> = {
  search: "",
  accountId: "",
  categoryId: "",
  groupId: "",
  payeeId: "",
  type: "",
  dateFrom: "",
  dateTo: "",
  linked: "",
  tags: "",
  sortBy: "date",
  sortOrder: "DESC",
  page: 1,
};

export const PAGE_SIZE_OPTIONS = [25, 50, 100, 200, 500, 1000];
export const MAX_PAGE_SIZE = 1000;
export const PAGE_SIZE_LS_KEY = "txPageSize";

// The id filters (accountId, categoryId, groupId, payeeId, tags) travel as a
// comma-separated list, the grammar the API already uses for tags, so a
// multi-select writes one value and a single selection stays a one-element
// list. A tag containing a comma cannot be filtered on (API limitation).
export function parseFilterList(value: unknown): string[] {
  return String(value ?? "")
    .split(",")
    .map((entry) => entry.trim())
    .filter(Boolean);
}
