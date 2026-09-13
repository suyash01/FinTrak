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
};

// Radix SelectItem values must never be empty; the "none" sentinel maps back
// to ""/null in the onValueChange handlers.
export const NO_GROUP = "none";
export const NO_PAYEE = "none";
export const NO_CATEGORY = "none";
