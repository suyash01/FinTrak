import { useEffect, useMemo, useRef, useState } from "react";
import { Plus, Trash2, Play, Edit2 } from "lucide-react";
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
import type { ApplyRulesResult, CreateRuleRequest, Rule } from "../../types";
import RuleDialog from "./RuleDialog";
import { EMPTY_NEW_RULE, type NewRuleForm } from "./categoryForms";

export default function RulesTab() {
  const { categories, groups, payees } = useDomainData();
  const { compactLayout } = useSettings();

  const [rules, setRules] = useState<Rule[]>([]);
  const [showNewRule, setShowNewRule] = useState(false);
  const [editingRule, setEditingRule] = useState<Rule | null>(null);
  const [newRule, setNewRule] = useState<NewRuleForm>(EMPTY_NEW_RULE);
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

  const handleUpsertRule = async () => {
    const payload: CreateRuleRequest = {
      ...newRule,
      payeeId: newRule.payeeId || null,
    };

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
      />

      <div className="bg-card border border-border rounded-xl overflow-x-auto">
        <table className="w-full text-left border-collapse">
          <thead>
            <tr>
              {["Pattern", "Match", "Category", "Payee", "Priority", ""].map(
                (h, i) => (
                  <th
                    key={h || "actions"}
                    className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-xs font-semibold uppercase tracking-wider text-muted-foreground bg-muted/50 border-b border-border whitespace-nowrap ${i === 5 ? "w-12.5" : ""}`}
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
                  colSpan={6}
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
                          setNewRule({
                            pattern: r.pattern,
                            matchType: r.matchType,
                            categoryId: r.categoryId,
                            payeeId: r.payeeId ?? null,
                            priority: r.priority,
                          });
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
