import { describe, it, expect } from "vitest";
import {
  EMPTY_NEW_RULE,
  ruleFormToRequest,
  ruleToForm,
} from "./categoryForms";
import type { Rule } from "../../types";

describe("ruleFormToRequest", () => {
  it("maps an empty form to nulls and undefined conditions", () => {
    const req = ruleFormToRequest(EMPTY_NEW_RULE);
    expect(req.pattern).toBe("");
    expect(req.categoryId).toBe("");
    expect(req.payeeId).toBeNull();
    expect(req.accountId).toBeNull();
    expect(req.filterCategoryId).toBeNull();
    expect(req.filterPayeeId).toBeNull();
    expect(req.minAmount).toBeNull();
    expect(req.maxAmount).toBeNull();
    expect(req.txnType).toBeUndefined();
    expect(req.dateFrom).toBeNull();
    expect(req.dateTo).toBeNull();
    expect(req.isLinked).toBeUndefined();
    expect(req.isRecurring).toBeUndefined();
  });

  it("maps a populated form to concrete values", () => {
    const req = ruleFormToRequest({
      ...EMPTY_NEW_RULE,
      pattern: "Zomato",
      categoryId: "c1",
      accountId: "a1",
      minAmount: "100",
      maxAmount: "500",
      txnType: "debit",
      dateFrom: "2024-01-01",
      isLinked: "true",
      isRecurring: "false",
      addTags: ["food"],
      notes: "note",
    });
    expect(req.accountId).toBe("a1");
    expect(req.minAmount).toBe(100);
    expect(req.maxAmount).toBe(500);
    expect(req.txnType).toBe("debit");
    expect(req.dateFrom).toBe("2024-01-01");
    expect(req.isLinked).toBe(true);
    expect(req.isRecurring).toBe(false);
    expect(req.addTags).toEqual(["food"]);
    expect(req.notes).toBe("note");
  });
});

describe("ruleToForm", () => {
  it("round-trips a persisted rule", () => {
    const rule: Rule = {
      id: "r1",
      pattern: "Zomato",
      matchType: "contains",
      categoryId: "c1",
      payeeId: "p1",
      priority: 5,
      accountId: "a1",
      minAmount: 100,
      isLinked: true,
      isRecurring: false,
      addTags: ["food"],
      notes: "note",
    };
    const form = ruleToForm(rule);
    expect(form.accountId).toBe("a1");
    expect(form.minAmount).toBe("100");
    expect(form.isLinked).toBe("true");
    expect(form.isRecurring).toBe("false");
    expect(form.addTags).toEqual(["food"]);
    expect(form.notes).toBe("note");
    expect(form.maxAmount).toBe("");
    expect(form.txnType).toBe("");
  });

  it("maps null conditions to empty strings", () => {
    const form = ruleToForm({
      id: "r2",
      pattern: "Uber",
      matchType: "exact",
      categoryId: "c2",
      priority: 0,
      accountId: null,
      isLinked: null,
      isRecurring: null,
      addTags: undefined,
      notes: undefined,
    });
    expect(form.accountId).toBe("");
    expect(form.isLinked).toBe("");
    expect(form.isRecurring).toBe("");
    expect(form.addTags).toEqual([]);
    expect(form.notes).toBe("");
  });
});
