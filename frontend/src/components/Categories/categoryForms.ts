import type { CreateRuleRequest, Rule } from "../../types";

export interface CategoryForm {
  name: string;
  icon: string;
  color: string;
  groupId: string;
}

export interface GroupForm {
  id: string;
  name: string;
  icon: string;
  color: string;
}

export interface NewRuleForm {
  pattern: string;
  matchType: string;
  categoryId: string;
  payeeId: string | null;
  priority: number;
  accountId: string | null;
  filterCategoryId: string | null;
  filterPayeeId: string | null;
  minAmount: string;
  maxAmount: string;
  txnType: string;
  dateFrom: string;
  dateTo: string;
  isLinked: string;
  isRecurring: string;
  addTags: string[];
  notes: string;
}

export const EMPTY_CATEGORY_FORM: CategoryForm = {
  name: "",
  icon: "tag",
  color: "#06b6d4",
  groupId: "",
};

export const EMPTY_GROUP_FORM: GroupForm = {
  id: "",
  name: "",
  icon: "folder",
  color: "#64748b",
};

export const EMPTY_NEW_RULE: NewRuleForm = {
  pattern: "",
  matchType: "contains",
  categoryId: "",
  payeeId: "",
  priority: 0,
  accountId: "",
  filterCategoryId: "",
  filterPayeeId: "",
  minAmount: "",
  maxAmount: "",
  txnType: "",
  dateFrom: "",
  dateTo: "",
  isLinked: "",
  isRecurring: "",
  addTags: [],
  notes: "",
};

// Radix SelectItem values must never be empty; the "none" sentinel maps back
// to ""/null in the onValueChange handlers.
export const NO_GROUP = "none";
export const NO_PAYEE = "none";
export const NO_CATEGORY = "none";

// ruleFormToRequest converts the editor form (string-based, for inputs) into the
// CreateRuleRequest the API expects: empty strings become null/omitted, numeric
// amount fields become numbers, and the tri-state link/recurring selects map to
// true/false/undefined.
export function ruleFormToRequest(form: NewRuleForm): CreateRuleRequest {
  return {
    pattern: form.pattern,
    matchType: form.matchType,
    categoryId: form.categoryId,
    payeeId: form.payeeId || null,
    priority: form.priority,
    accountId: form.accountId || null,
    filterCategoryId: form.filterCategoryId || null,
    filterPayeeId: form.filterPayeeId || null,
    minAmount: form.minAmount === "" ? null : Number(form.minAmount),
    maxAmount: form.maxAmount === "" ? null : Number(form.maxAmount),
    txnType: form.txnType || undefined,
    dateFrom: form.dateFrom || null,
    dateTo: form.dateTo || null,
    isLinked:
      form.isLinked === "" ? undefined : form.isLinked === "true",
    isRecurring:
      form.isRecurring === "" ? undefined : form.isRecurring === "true",
    addTags: form.addTags,
    notes: form.notes,
  };
}

// ruleToForm maps a persisted Rule back into the editor form.
export function ruleToForm(rule: Rule): NewRuleForm {
  const tri = (v: boolean | null | undefined) =>
    v === null || v === undefined ? "" : v ? "true" : "false";
  return {
    pattern: rule.pattern,
    matchType: rule.matchType,
    categoryId: rule.categoryId,
    payeeId: rule.payeeId ?? null,
    priority: rule.priority,
    accountId: rule.accountId ?? "",
    filterCategoryId: rule.filterCategoryId ?? "",
    filterPayeeId: rule.filterPayeeId ?? "",
    minAmount: rule.minAmount == null ? "" : String(rule.minAmount),
    maxAmount: rule.maxAmount == null ? "" : String(rule.maxAmount),
    txnType: rule.txnType ?? "",
    dateFrom: rule.dateFrom ?? "",
    dateTo: rule.dateTo ?? "",
    isLinked: tri(rule.isLinked),
    isRecurring: tri(rule.isRecurring),
    addTags: rule.addTags ?? [],
    notes: rule.notes ?? "",
  };
}
