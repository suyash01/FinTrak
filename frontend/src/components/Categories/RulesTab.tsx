import { useEffect, useMemo, useRef, useState } from "react";
import { Plus, Trash2, Play, Edit2, Tag, StickyNote } from "lucide-react";
import api from "../../api/client";
import { useSettings } from "../../context/SettingsContext";
import { useDomainData } from "../../context/DomainDataContext";
import { buildCategorySections } from "../../lib/categories";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { toast } from "sonner";
import { useCommandIntent } from "../../lib/useCommandIntent";
import type { ApplyRulesResult, CreateRuleRequest, Rule } from "../../types";
import RuleDialog from "./RuleDialog";
import {
  EMPTY_NEW_RULE,
  ruleFormToRequest,
  ruleToForm,
  type NewRuleForm,
} from "./categoryForms";

// hasConditions reports whether a rule carries any optional condition beyond
// its description pattern.
function hasConditions(r: Rule): boolean {
  return Boolean(
    r.accountId ||
      r.filterCategoryId ||
      r.filterPayeeId ||
      r.minAmount != null ||
      r.maxAmount != null ||
      r.txnType ||
      r.dateFrom ||
      r.dateTo ||
      r.isLinked != null ||
      r.isRecurring != null,
  );
}

// conditionSummary renders a compact human-readable list of a rule's set
// conditions for the table badge.
function conditionSummary(r: Rule): string {
  const parts: string[] = [];
  if (r.accountId) parts.push(`acct: ${r.accountName || "?"}`);
  if (r.filterCategoryId) parts.push(`cat: ${r.filterCategoryName || "?"}`);
  if (r.filterPayeeId) parts.push(`payee: ${r.filterPayeeName || "?"}`);
  if (r.minAmount != null) parts.push(`≥ ${r.minAmount}`);
  if (r.maxAmount != null) parts.push(`≤ ${r.maxAmount}`);
  if (r.txnType) parts.push(r.txnType);
  if (r.dateFrom) parts.push(`from ${r.dateFrom}`);
  if (r.dateTo) parts.push(`to ${r.dateTo}`);
  if (r.isLinked != null) parts.push(r.isLinked ? "linked" : "unlinked");
  if (r.isRecurring != null)
    parts.push(r.isRecurring ? "recurring" : "non-recurring");
  return parts.join(", ");
}

export default function RulesTab() {
  const { categories, groups, payees, accounts } = useDomainData();
  const { compactLayout } = useSettings();

  const [rules, setRules] = useState<Rule[]>([]);
  const [showNewRule, setShowNewRule] = useState(false);
  const [editingRule, setEditingRule] = useState<Rule | null>(null);
  const [newRule, setNewRule] = useState<NewRuleForm>(EMPTY_NEW_RULE);
  const [previewCount, setPreviewCount] = useState<number | null>(null);
  const [applyResult, setApplyResult] = useState<ApplyRulesResult | null>(null);
  const applyTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const categorySections = useMemo(
    () => buildCategorySections(groups, categories),
    [groups, categories],
  );

  useEffect(() => {
    api
      .getRules()
      .then(setRules)
      .catch((err) => toast.error((err as Error).message));
    return () => {
      if (applyTimerRef.current) clearTimeout(applyTimerRef.current);
    };
  }, []);

  // Live preview of how many uncategorized transactions the current form
  // would match. Debounced and best-effort (a failed preview is silent).
  useEffect(() => {
    if (!showNewRule || !newRule.pattern || !newRule.categoryId) {
      setPreviewCount(null);
      return;
    }
    const timer = setTimeout(() => {
      api
        .previewRule(ruleFormToRequest(newRule))
        .then((res) => setPreviewCount(res.matched))
        .catch(() => setPreviewCount(null));
    }, 400);
    return () => clearTimeout(timer);
  }, [newRule, showNewRule]);

  const handleUpsertRule = async () => {
    const payload: CreateRuleRequest = ruleFormToRequest(newRule);

    try {
      if (editingRule) {
        await api.updateRule(editingRule.id, payload);
      } else {
        await api.createRule(payload);
      }
      setShowNewRule(false);
      setEditingRule(null);
      setNewRule(EMPTY_NEW_RULE);
      const updatedRules = await api.getRules();
      setRules(updatedRules);
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const handleDeleteRule = async (id: string) => {
    try {
      await api.deleteRule(id);
      setRules((prev) => prev.filter((r) => r.id !== id));
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const handleApplyRules = async () => {
    try {
      const result = await api.applyRules();
      setApplyResult(result);
      if (applyTimerRef.current) clearTimeout(applyTimerRef.current);
      applyTimerRef.current = setTimeout(() => setApplyResult(null), 3000);
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  useCommandIntent("new-rule", () => {
    setEditingRule(null);
    setNewRule(EMPTY_NEW_RULE);
    setShowNewRule(true);
  });

  return (
    <>
      <div className="flex justify-between items-center mb-5 flex-wrap gap-4">
        <div className="flex items-center gap-4">
          <span className="text-sm text-muted-foreground">
            {rules.length} rules
          </span>
          <Button variant="outline" onClick={handleApplyRules}>
            <Play /> Apply Rules to Uncategorized
          </Button>
          {applyResult && (
            <span className="text-sm font-medium text-emerald-500">
              {applyResult.updated} transactions updated
            </span>
          )}
        </div>
        <Button
          onClick={() => {
            setEditingRule(null);
            setNewRule(EMPTY_NEW_RULE);
            setShowNewRule(true);
          }}
        >
          <Plus /> Add Rule
        </Button>
      </div>

      <RuleDialog
        open={showNewRule}
        onOpenChange={(open) => {
          setShowNewRule(open);
          if (!open) {
            setEditingRule(null);
            setNewRule(EMPTY_NEW_RULE);
          }
        }}
        editingRule={editingRule}
        form={newRule}
        onChange={setNewRule}
        onSubmit={handleUpsertRule}
        categorySections={categorySections}
        payees={payees}
        accounts={accounts}
        previewCount={previewCount}
      />

      <div className="bg-card border border-border rounded-xl overflow-x-auto">
        <table className="w-full text-left border-collapse">
          <thead>
            <tr>
              {["Pattern", "Match", "Category", "Payee", "Conditions & Actions", "Priority", ""].map(
                (h, i) => (
                  <th
                    key={h || "actions"}
                    className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-xs font-semibold uppercase tracking-wider text-muted-foreground bg-muted/50 border-b border-border whitespace-nowrap ${i === 6 ? "w-12.5" : ""}`}
                  >
                    {h}
                  </th>
                ),
              )}
            </tr>
          </thead>
          <tbody>
            {rules.length === 0 ? (
              <tr>
                <td
                  colSpan={7}
                  className="text-center p-10 text-muted-foreground"
                >
                  No rules yet. Create one to auto-categorize transactions.
                </td>
              </tr>
            ) : (
              rules.map((r) => (
                <tr
                  key={r.id}
                  className="hover:bg-muted/30 transition-colors border-b border-border last:border-0"
                >
                  <td
                    className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm font-medium text-foreground`}
                  >
                    "{r.pattern}"
                  </td>
                  <td
                    className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm`}
                  >
                    <Badge
                      variant="outline"
                      className="capitalize text-muted-foreground"
                    >
                      {r.matchType.replace("_", " ")}
                    </Badge>
                  </td>
                  <td
                    className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm text-foreground`}
                  >
                    {r.categoryName}
                  </td>
                  <td
                    className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm text-muted-foreground`}
                  >
                    {r.payee || "—"}
                  </td>
                  <td
                    className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm`}
                  >
                    <div className="flex flex-wrap gap-1">
                      {hasConditions(r) && (
                        <Badge
                          variant="secondary"
                          className="text-[10px]"
                          title={conditionSummary(r)}
                        >
                          {conditionSummary(r)}
                        </Badge>
                      )}
                      {(r.addTags?.length ?? 0) > 0 && (
                        <Badge
                          variant="outline"
                          className="text-[10px] gap-1"
                          title={`Adds tags: ${r.addTags!.join(", ")}`}
                        >
                          <Tag size={10} />
                          {r.addTags!.join(", ")}
                        </Badge>
                      )}
                      {r.notes ? (
                        <Badge
                          variant="outline"
                          className="text-[10px] gap-1"
                          title={`Appends note: ${r.notes}`}
                        >
                          <StickyNote size={10} />
                          note
                        </Badge>
                      ) : null}
                      {!hasConditions(r) &&
                        (r.addTags?.length ?? 0) === 0 &&
                        !r.notes && (
                          <span className="text-muted-foreground">—</span>
                        )}
                    </div>
                  </td>
                  <td
                    className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm text-muted-foreground`}
                  >
                    {r.priority}
                  </td>
                  <td
                    className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-right`}
                  >
                    <div className="flex justify-end gap-1">
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={`Edit rule ${r.pattern}`}
                        className="text-muted-foreground hover:text-primary"
                        onClick={() => {
                          setEditingRule(r);
                          setNewRule(ruleToForm(r));
                          setShowNewRule(true);
                        }}
                      >
                        <Edit2 />
                      </Button>
                      <AlertDialog>
                        <AlertDialogTrigger asChild>
                          <Button
                            variant="ghost"
                            size="icon-sm"
                            aria-label={`Delete rule ${r.pattern}`}
                            className="text-muted-foreground hover:text-destructive"
                          >
                            <Trash2 />
                          </Button>
                        </AlertDialogTrigger>
                        <AlertDialogContent>
                          <AlertDialogHeader>
                            <AlertDialogTitle>Delete rule?</AlertDialogTitle>
                            <AlertDialogDescription>
                              Delete this rule? It will no longer
                              auto-categorize matching transactions.
                            </AlertDialogDescription>
                          </AlertDialogHeader>
                          <AlertDialogFooter>
                            <AlertDialogCancel>Cancel</AlertDialogCancel>
                            <AlertDialogAction
                              variant="destructive"
                              onClick={() => handleDeleteRule(r.id)}
                            >
                              Delete
                            </AlertDialogAction>
                          </AlertDialogFooter>
                        </AlertDialogContent>
                      </AlertDialog>
                    </div>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </>
  );
}
