import { Tags, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { formatDate } from "../../utils/formatters";
import type { CategorySection } from "../../lib/categories";
import type { Account, BillingCycle, Payee } from "../../types";

// Sentinel value for the bulk "Link to Loan" action: detach instead of attach.
export const UNLINK_LOAN = "__unlink__";

interface BulkActionBarProps {
  selectedCount: number;
  categorySections: CategorySection[];
  payees: Payee[];
  hasBillingDayFilter: boolean;
  loadingCycles: boolean;
  billingCycles: BillingCycle[];
  loanAccounts: Account[];
  onCategorize: (categoryId: string) => void;
  onUpdatePayee: (payeeId: string) => void;
  onSetBillingCycle: (billingCycleId: string) => void;
  onLinkLoan: (value: string) => void;
  onDelete: () => void;
  onClear: () => void;
}

const selectClass =
  "px-3 py-1.5 bg-background border border-border rounded text-foreground text-[13px] focus:outline-none focus:border-primary transition-all ml-2";

// Bulk-action toolbar shown when one or more transactions are selected. The
// native <select>s are a deliberate exception to the shadcn Select convention
// (see AGENTS.md) — they are dense action triggers, not form fields.
export default function BulkActionBar({
  selectedCount,
  categorySections,
  payees,
  hasBillingDayFilter,
  loadingCycles,
  billingCycles,
  loanAccounts,
  onCategorize,
  onUpdatePayee,
  onSetBillingCycle,
  onLinkLoan,
  onDelete,
  onClear,
}: BulkActionBarProps) {
  return (
    <div className="flex items-center gap-3 px-4 py-3 bg-primary/10 border border-primary/20 rounded-lg mb-4">
      <Tags size={16} className="text-primary" />
      <span className="text-sm font-medium text-foreground">
        {selectedCount} selected
      </span>
      <select
        className={selectClass}
        onChange={(e) => {
          if (e.target.value) onCategorize(e.target.value);
          e.target.value = "";
        }}
      >
        <option value="">Categorize as...</option>
        <option
          value="uncategorized"
          className="bg-popover text-muted-foreground font-semibold"
        >
          Uncategorized
        </option>
        {categorySections.map((s) => (
          <optgroup
            key={s.group.id}
            label={s.group.name}
            className="bg-popover text-muted-foreground"
          >
            {s.items.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </optgroup>
        ))}
      </select>
      <select
        className={selectClass}
        onChange={(e) => {
          if (e.target.value) onUpdatePayee(e.target.value);
          e.target.value = "";
        }}
      >
        <option value="">Set Payee...</option>
        {payees.map((p) => (
          <option key={p.id} value={p.id}>
            {p.name}
          </option>
        ))}
      </select>
      {hasBillingDayFilter && (
        <select
          className={selectClass}
          onChange={(e) => {
            if (e.target.value) onSetBillingCycle(e.target.value);
            e.target.value = "";
          }}
        >
          <option value="">
            {loadingCycles ? "Loading billing cycles..." : "Set Billing Cycle..."}
          </option>
          {billingCycles.map((bc) => (
            <option key={bc.id} value={bc.id}>
              {bc.label} ({formatDate(bc.startDate)} – {formatDate(bc.endDate)})
            </option>
          ))}
        </select>
      )}
      {loanAccounts.length > 0 && (
        <select
          className={selectClass}
          onChange={(e) => {
            if (e.target.value) onLinkLoan(e.target.value);
            e.target.value = "";
          }}
        >
          <option value="">Link to Loan...</option>
          <option value={UNLINK_LOAN}>Unlink from loan</option>
          {loanAccounts.map((la) => (
            <option key={la.id} value={la.id}>
              {la.name}
            </option>
          ))}
        </select>
      )}

      <Button
        variant="ghost"
        size="sm"
        className="text-destructive hover:text-destructive ml-2"
        onClick={onDelete}
      >
        <Trash2 size={14} />
        Delete
      </Button>
      <Button variant="ghost" size="sm" className="ml-auto" onClick={onClear}>
        Clear
      </Button>
    </div>
  );
}
