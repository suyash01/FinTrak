import {
  Trash2,
  Pencil,
  Download,
  Star,
  Lock,
  LockOpen,
  Calculator,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { TableCell, TableRow } from "@/components/ui/table";
import { formatCurrency } from "../../utils/formatters";
import type { Account } from "../../types";
import AccountTypeIcon from "./AccountTypeIcon";
import { balanceLabel, ordinal } from "./accountHelpers";

interface AccountRowProps {
  account: Account;
  cellPad: string;
  compactLayout: boolean;
  onSetDefault: (acc: Account) => void;
  onExport: (id: string) => void;
  onEdit: (acc: Account) => void;
  onSchedule: (acc: Account) => void;
  onToggleClosed: (acc: Account) => void;
  onDelete: (acc: Account) => void;
}

export default function AccountRow({
  account: acc,
  cellPad,
  compactLayout,
  onSetDefault,
  onExport,
  onEdit,
  onSchedule,
  onToggleClosed,
  onDelete,
}: AccountRowProps) {
  return (
    <TableRow className="border-border">
      <TableCell className={cellPad}>
        <div className="flex items-center gap-2.5">
          <AccountTypeIcon
            accountTypeId={acc.accountTypeId}
            color={acc.color}
            size={compactLayout ? 16 : 18}
          />
          <span
            className={`font-medium ${
              acc.closed ? "text-muted-foreground" : "text-foreground"
            }`}
          >
            {acc.name}
          </span>
          {acc.isDefault && (
            <span className="shrink-0 text-[9px] font-bold bg-amber-500/20 text-amber-700 dark:text-amber-300 px-1.5 py-0.5 rounded uppercase tracking-wider">
              Default
            </span>
          )}
          {acc.closed && (
            <span className="shrink-0 text-[9px] font-bold bg-destructive/10 text-destructive px-1.5 py-0.5 rounded uppercase tracking-wider">
              Closed
            </span>
          )}
        </div>
      </TableCell>
      <TableCell className={`${cellPad} text-sm text-muted-foreground whitespace-nowrap`}>
        {acc.accountTypeName}
      </TableCell>
      <TableCell className={`${cellPad} text-sm text-muted-foreground whitespace-nowrap`}>
        {acc.bank || "—"}
      </TableCell>
      <TableCell className={`${cellPad} text-right whitespace-nowrap`}>
        <div className="text-sm font-semibold text-foreground font-mono">
          {formatCurrency(acc.balance, acc.currency)}
        </div>
        <div className="text-[10px] uppercase tracking-wider text-muted-foreground font-medium">
          {balanceLabel(acc)}
        </div>
      </TableCell>
      <TableCell className={`${cellPad} text-sm text-muted-foreground whitespace-nowrap`}>
        {acc.billingDay ? `${acc.billingDay}${ordinal(acc.billingDay)}` : "—"}
      </TableCell>
      <TableCell className={`${cellPad} text-sm whitespace-nowrap`}>
        {acc.closed ? (
          <span className="text-destructive">Closed</span>
        ) : (
          <span className="text-chart-3">Open</span>
        )}
      </TableCell>
      <TableCell className={`${cellPad} text-right`}>
        <div className="flex items-center justify-end gap-0.5">
          <Button
            variant="ghost"
            size="icon-sm"
            className={
              acc.isDefault
                ? "text-amber-700 dark:text-amber-300 hover:bg-amber-500/10"
                : "text-muted-foreground hover:text-amber-700 dark:hover:text-amber-300 hover:bg-accent"
            }
            onClick={() => onSetDefault(acc)}
            title={
              acc.isDefault
                ? "Remove as default account"
                : "Set as default account"
            }
            aria-label={
              acc.isDefault
                ? `Remove ${acc.name} as default account`
                : `Set ${acc.name} as default account`
            }
          >
            <Star size={15} fill={acc.isDefault ? "currentColor" : "none"} />
          </Button>
          {acc.accountTypeId === "loan" && (
            <Button
              variant="ghost"
              size="icon-sm"
              className="text-muted-foreground hover:text-primary hover:bg-primary/10"
              onClick={() => onSchedule(acc)}
              title="Amortization schedule"
              aria-label={`Amortization schedule for ${acc.name}`}
            >
              <Calculator size={14} />
            </Button>
          )}
          <Button
            variant="ghost"
            size="icon-sm"
            className="text-muted-foreground hover:text-primary hover:bg-primary/10"
            onClick={() => onExport(acc.id)}
            title="Export transactions (CSV)"
            aria-label={`Export ${acc.name} transactions as CSV`}
          >
            <Download size={14} />
          </Button>
          <Button
            variant="ghost"
            size="icon-sm"
            className="text-muted-foreground hover:text-primary hover:bg-primary/10"
            onClick={() => onEdit(acc)}
            title="Edit account"
            aria-label={`Edit ${acc.name}`}
          >
            <Pencil size={14} />
          </Button>
          <Button
            variant="ghost"
            size="icon-sm"
            className="text-muted-foreground hover:text-amber-700 dark:hover:text-amber-300 hover:bg-accent"
            onClick={() => onToggleClosed(acc)}
            title={
              acc.closed
                ? "Reopen account"
                : "Close account (transactions become read-only)"
            }
            aria-label={
              acc.closed ? `Reopen ${acc.name}` : `Close ${acc.name}`
            }
          >
            {acc.closed ? <LockOpen size={14} /> : <Lock size={14} />}
          </Button>
          <Button
            variant="ghost"
            size="icon-sm"
            className="text-muted-foreground hover:text-destructive hover:bg-destructive/10"
            onClick={() => onDelete(acc)}
            title="Delete account"
            aria-label={`Delete ${acc.name}`}
          >
            <Trash2 size={14} />
          </Button>
        </div>
      </TableCell>
    </TableRow>
  );
}
