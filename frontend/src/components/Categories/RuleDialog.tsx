import { useState, type KeyboardEvent } from "react";
import { ChevronDown, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { CategorySection } from "../../lib/categories";
import type { Account, Payee, Rule } from "../../types";
import {
  NO_CATEGORY,
  NO_PAYEE,
  type NewRuleForm,
} from "./categoryForms";

interface RuleDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  editingRule: Rule | null;
  form: NewRuleForm;
  onChange: (form: NewRuleForm) => void;
  onSubmit: () => void;
  categorySections: CategorySection[];
  payees: Payee[];
  accounts: Account[];
  // Live count of currently-uncategorized transactions the rule would match,
  // or null while it is being computed.
  previewCount: number | null;
}

const labelClass = "text-xs text-muted-foreground";

export default function RuleDialog({
  open,
  onOpenChange,
  editingRule,
  form,
  onChange,
  onSubmit,
  categorySections,
  payees,
  accounts,
  previewCount,
}: RuleDialogProps) {
  const patch = (fields: Partial<NewRuleForm>) =>
    onChange({ ...form, ...fields });
  const [showConditions, setShowConditions] = useState(false);
  const [tagInput, setTagInput] = useState("");

  const addTag = () => {
    const tag = tagInput.trim();
    if (tag && !form.addTags.includes(tag)) {
      patch({ addTags: [...form.addTags, tag] });
      setTagInput("");
    }
  };

  const handleTagKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter") {
      e.preventDefault();
      addTag();
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{editingRule ? "Edit Rule" : "New Rule"}</DialogTitle>
        </DialogHeader>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="rule-form-pattern" className={labelClass}>
              Pattern
            </Label>
            <Input
              id="rule-form-pattern"
              placeholder="e.g. SWIGGY, AMAZON, UBER"
              value={form.pattern}
              onChange={(e) => patch({ pattern: e.target.value })}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="rule-form-match-type" className={labelClass}>
              Match Type
            </Label>
            <Select
              value={form.matchType}
              onValueChange={(v) => patch({ matchType: v })}
            >
              <SelectTrigger id="rule-form-match-type" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="contains">Contains</SelectItem>
                <SelectItem value="starts_with">Starts With</SelectItem>
                <SelectItem value="exact">Exact Match</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="rule-form-category" className={labelClass}>
              Assign Category
            </Label>
            <Select
              value={form.categoryId || NO_CATEGORY}
              onValueChange={(v) =>
                patch({ categoryId: v === NO_CATEGORY ? "" : v })
              }
            >
              <SelectTrigger id="rule-form-category" className="w-full">
                <SelectValue placeholder="Choose category..." />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={NO_CATEGORY}>
                  Choose category...
                </SelectItem>
                {categorySections.map((s) => (
                  <SelectGroup key={s.group.id}>
                    <SelectLabel>{s.group.name}</SelectLabel>
                    {s.items.map((c) => (
                      <SelectItem key={c.id} value={c.id}>
                        {c.name}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="rule-form-payee" className={labelClass}>
              Assign Payee (optional)
            </Label>
            <Select
              value={form.payeeId || NO_PAYEE}
              onValueChange={(v) =>
                patch({ payeeId: v === NO_PAYEE ? null : v })
              }
            >
              <SelectTrigger id="rule-form-payee" className="w-full">
                <SelectValue placeholder="No Payee" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={NO_PAYEE}>No Payee</SelectItem>
                {payees.map((p) => (
                  <SelectItem key={p.id} value={p.id}>
                    {p.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="rule-form-priority" className={labelClass}>
              Priority (higher = first)
            </Label>
            <Input
              id="rule-form-priority"
              type="number"
              value={form.priority}
              onChange={(e) =>
                patch({ priority: parseInt(e.target.value) || 0 })
              }
            />
          </div>
        </div>

        {/* Conditions (optional, all ANDed) */}
        <button
          type="button"
          className="flex items-center gap-1.5 text-xs font-semibold uppercase tracking-widest text-muted-foreground self-start"
          onClick={() => setShowConditions((s) => !s)}
          aria-expanded={showConditions}
        >
          <ChevronDown
            size={14}
            className={`transition-transform ${showConditions ? "" : "-rotate-90"}`}
          />
          Conditions
        </button>
        {showConditions && (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="rule-form-account" className={labelClass}>
                Account
              </Label>
              <Select
                value={form.accountId || "any"}
                onValueChange={(v) =>
                  patch({ accountId: v === "any" ? "" : v })
                }
              >
                <SelectTrigger id="rule-form-account" className="w-full">
                  <SelectValue placeholder="Any account" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="any">Any account</SelectItem>
                  {accounts.map((a) => (
                    <SelectItem key={a.id} value={a.id}>
                      {a.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="rule-form-filter-category" className={labelClass}>
                Category is
              </Label>
              <Select
                value={form.filterCategoryId || "any"}
                onValueChange={(v) =>
                  patch({ filterCategoryId: v === "any" ? "" : v })
                }
              >
                <SelectTrigger id="rule-form-filter-category" className="w-full">
                  <SelectValue placeholder="Any category" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="any">Any category</SelectItem>
                  {categorySections.map((s) => (
                    <SelectGroup key={s.group.id}>
                      <SelectLabel>{s.group.name}</SelectLabel>
                      {s.items.map((c) => (
                        <SelectItem key={c.id} value={c.id}>
                          {c.name}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="rule-form-filter-payee" className={labelClass}>
                Payee is
              </Label>
              <Select
                value={form.filterPayeeId || "any"}
                onValueChange={(v) =>
                  patch({ filterPayeeId: v === "any" ? "" : v })
                }
              >
                <SelectTrigger id="rule-form-filter-payee" className="w-full">
                  <SelectValue placeholder="Any payee" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="any">Any payee</SelectItem>
                  {payees.map((p) => (
                    <SelectItem key={p.id} value={p.id}>
                      {p.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="rule-form-type" className={labelClass}>
                Type
              </Label>
              <Select
                value={form.txnType || "any"}
                onValueChange={(v) =>
                  patch({ txnType: v === "any" ? "" : v })
                }
              >
                <SelectTrigger id="rule-form-type" className="w-full">
                  <SelectValue placeholder="Any type" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="any">Any type</SelectItem>
                  <SelectItem value="debit">Debit</SelectItem>
                  <SelectItem value="credit">Credit</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="rule-form-min" className={labelClass}>
                Min amount
              </Label>
              <Input
                id="rule-form-min"
                type="number"
                step="0.01"
                placeholder="Any"
                value={form.minAmount}
                onChange={(e) => patch({ minAmount: e.target.value })}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="rule-form-max" className={labelClass}>
                Max amount
              </Label>
              <Input
                id="rule-form-max"
                type="number"
                step="0.01"
                placeholder="Any"
                value={form.maxAmount}
                onChange={(e) => patch({ maxAmount: e.target.value })}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="rule-form-date-from" className={labelClass}>
                From date
              </Label>
              <Input
                id="rule-form-date-from"
                type="date"
                value={form.dateFrom}
                onChange={(e) => patch({ dateFrom: e.target.value })}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="rule-form-date-to" className={labelClass}>
                To date
              </Label>
              <Input
                id="rule-form-date-to"
                type="date"
                value={form.dateTo}
                onChange={(e) => patch({ dateTo: e.target.value })}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="rule-form-linked" className={labelClass}>
                Link state
              </Label>
              <Select
                value={form.isLinked || "any"}
                onValueChange={(v) =>
                  patch({ isLinked: v === "any" ? "" : v })
                }
              >
                <SelectTrigger id="rule-form-linked" className="w-full">
                  <SelectValue placeholder="Either" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="any">Either</SelectItem>
                  <SelectItem value="true">Linked only</SelectItem>
                  <SelectItem value="false">Unlinked only</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="rule-form-recurring" className={labelClass}>
                Recurring state
              </Label>
              <Select
                value={form.isRecurring || "any"}
                onValueChange={(v) =>
                  patch({ isRecurring: v === "any" ? "" : v })
                }
              >
                <SelectTrigger id="rule-form-recurring" className="w-full">
                  <SelectValue placeholder="Either" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="any">Either</SelectItem>
                  <SelectItem value="true">Recurring only</SelectItem>
                  <SelectItem value="false">Non-recurring only</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>
        )}

        {/* Actions (extra) */}
        <div className="space-y-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="rule-form-add-tags" className={labelClass}>
              Add Tags (optional)
            </Label>
            <div className="flex flex-wrap gap-2 mb-1">
              {form.addTags.map((tag) => (
                <Badge
                  key={tag}
                  className="bg-primary/10 text-primary border-primary/20 rounded-full"
                >
                  {tag}
                  <button
                    type="button"
                    aria-label={`Remove tag ${tag}`}
                    className="hover:text-destructive transition-colors ml-0.5"
                    onClick={() =>
                      patch({ addTags: form.addTags.filter((t) => t !== tag) })
                    }
                  >
                    <X size={12} />
                  </button>
                </Badge>
              ))}
            </div>
            <div className="flex gap-2">
              <Input
                id="rule-form-add-tags"
                className="flex-1"
                value={tagInput}
                onChange={(e) => setTagInput(e.target.value)}
                onKeyDown={handleTagKeyDown}
                placeholder="Add a tag and press Enter"
              />
              <Button type="button" variant="outline" onClick={addTag}>
                Add
              </Button>
            </div>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="rule-form-notes" className={labelClass}>
              Append Note (optional)
            </Label>
            <Input
              id="rule-form-notes"
              value={form.notes}
              onChange={(e) => patch({ notes: e.target.value })}
              placeholder="Note text to append"
            />
          </div>
        </div>

        <div className="flex items-center justify-between gap-3">
          <span className="text-xs text-muted-foreground">
            {previewCount === null
              ? ""
              : `${previewCount} uncategorized transaction${previewCount === 1 ? "" : "s"} would match`}
          </span>
          <Button onClick={onSubmit} disabled={!form.pattern || !form.categoryId}>
            {editingRule ? "Update Rule" : "Create Rule"}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
