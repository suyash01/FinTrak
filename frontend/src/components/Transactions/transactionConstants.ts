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
  sortBy: "date",
  sortOrder: "DESC",
  page: 1,
};

export const PAGE_SIZE_OPTIONS = [25, 50, 100, 200, 500, 1000];
export const MAX_PAGE_SIZE = 1000;
export const PAGE_SIZE_LS_KEY = "txPageSize";
