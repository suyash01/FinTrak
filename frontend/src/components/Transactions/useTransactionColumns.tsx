import { useMemo } from "react";
import { Pencil, Link2, Trash2 } from "lucide-react";
import { createColumnHelper, type ColumnDef } from "@/lib/react-table";
import { Checkbox } from "@/components/ui/checkbox";
import { Button } from "@/components/ui/button";
import { DataTableColumnHeader } from "@/components/ui/data-table";
import { formatCurrency, formatDate } from "../../utils/formatters";
import type { Transaction } from "../../types";
import EditableSelect, {
  type EditableSelectGroup,
  type SelectOption,
} from "./EditableSelect";

const columnHelper = createColumnHelper<Transaction>();

interface UseTransactionColumnsArgs {
  payeeOptions: SelectOption[];
  categoryOptionGroups: EditableSelectGroup[];
  closedById: Map<string, boolean>;
  onCategoryChange: (
    txnId: string,
    categoryId: string,
    txn: Transaction,
  ) => void;
  onPayeeChange: (txnId: string, payeeId: string, txn: Transaction) => void;
  onDelete: (id: string) => void;
  onLink: (txn: Transaction) => void;
  onEdit: (txn: Transaction) => void;
}

// Column definitions for the transactions table. Kept in a hook so the page
// component stays focused on state and data flow.
export function useTransactionColumns({
  payeeOptions,
  categoryOptionGroups,
  closedById,
  onCategoryChange,
  onPayeeChange,
  onDelete,
  onLink,
  onEdit,
}: UseTransactionColumnsArgs): ColumnDef<Transaction, any>[] {
  return useMemo<ColumnDef<Transaction, any>[]>(
    () => [
      columnHelper.display({
        id: "select",
        header: ({ table }) => (
          <Checkbox
            aria-label="Select all rows"
            checked={table.getIsAllPageRowsSelected()}
            onCheckedChange={(v) => table.toggleAllPageRowsSelected(!!v)}
          />
        ),
        cell: ({ row }) =>
          row.original.isSummary ? null : (
            <Checkbox
              aria-label={`Select ${row.original.description}`}
              checked={row.getIsSelected()}
              onCheckedChange={(v) => row.toggleSelected(!!v)}
            />
          ),
        meta: { headerClassName: "w-10" },
      }),
      columnHelper.accessor("date", {
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title="Date" />
        ),
        cell: ({ row }) => (
          <span
            className={`text-sm whitespace-nowrap ${
              row.original.isSummary ? "text-muted-foreground" : ""
            }`}
          >
            {formatDate(row.original.date)}
          </span>
        ),
        meta: { cellClassName: "text-sm whitespace-nowrap" },
      }),
      columnHelper.accessor("description", {
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title="Description" />
        ),
        cell: ({ row }) =>
          row.original.isSummary ? (
            <span className="text-sm font-semibold text-primary">
              {row.original.description}
            </span>
          ) : (
            <span
              className="block text-sm max-w-62.5 overflow-hidden text-ellipsis whitespace-nowrap"
              title={row.original.description}
            >
              {row.original.description}
            </span>
          ),
        meta: {
          cellClassName:
            "text-sm max-w-62.5 overflow-hidden text-ellipsis whitespace-nowrap",
        },
      }),
      columnHelper.display({
        id: "payee",
        header: () => "Payee",
        cell: ({ row }) =>
          row.original.isSummary ? null : (
            <EditableSelect
              value={row.original.payeeId}
              options={payeeOptions}
              onChange={(val) =>
                onPayeeChange(row.original.id, val, row.original)
              }
              placeholder="No Payee"
              displayText={row.original.payee}
              ariaLabel={`Payee for ${row.original.description}`}
            />
          ),
        meta: { cellClassName: "text-sm min-w-25" },
      }),
      columnHelper.accessor("accountName", {
        header: () => "Account",
        cell: ({ row }) => (
          <span
            className={`text-sm whitespace-nowrap ${
              row.original.isSummary ? "text-muted-foreground" : ""
            }`}
          >
            {row.original.accountName}
          </span>
        ),
        meta: { cellClassName: "text-sm whitespace-nowrap" },
      }),
      columnHelper.display({
        id: "category",
        header: () => "Category",
        cell: ({ row }) =>
          row.original.isSummary ? null : (
            <EditableSelect
              value={row.original.categoryId}
              optionGroups={categoryOptionGroups}
              onChange={(val) =>
                onCategoryChange(row.original.id, val, row.original)
              }
              placeholder="Uncategorized"
              displayText={
                row.original.categoryId ? row.original.categoryName : ""
              }
              ariaLabel={`Category for ${row.original.description}`}
              style={
                row.original.categoryColor
                  ? { color: row.original.categoryColor }
                  : undefined
              }
            />
          ),
        meta: { cellClassName: "text-sm" },
      }),
      columnHelper.accessor("amount", {
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title="Amount" />
        ),
        cell: ({ row }) =>
          row.original.isSummary ? (
            <span className="text-sm text-right font-bold text-foreground font-mono whitespace-nowrap">
              {formatCurrency(row.original.amount)}
            </span>
          ) : (
            <span
              className={`text-sm text-right font-semibold whitespace-nowrap ${
                row.original.type === "debit"
                  ? "text-destructive"
                  : "text-emerald-500"
              }`}
            >
              {row.original.type === "debit" ? "−" : "+"}
              {formatCurrency(row.original.amount)}
            </span>
          ),
        meta: {
          headerClassName: "text-right",
          cellClassName: "text-sm text-right whitespace-nowrap",
        },
      }),
      columnHelper.display({
        id: "actions",
        header: () => "",
        cell: ({ row }) => {
          const t = row.original;
          if (t.isSummary) return null;
          // Closed accounts are immutable: only linking stays possible.
          const accountClosed = closedById.get(t.accountId) ?? false;
          return (
            <div className="flex items-center gap-1">
              {!accountClosed && (
                <Button
                  variant="ghost"
                  size="icon-sm"
                  className="text-muted-foreground hover:text-primary hover:bg-primary/10"
                  onClick={() => onEdit(t)}
                  title="Edit transaction"
                  aria-label={`Edit ${t.description}`}
                >
                  <Pencil size={14} />
                </Button>
              )}
              <Button
                variant="ghost"
                size="icon-sm"
                className={`${
                  t.isLinked
                    ? "text-primary bg-primary/10 hover:bg-primary/20 hover:text-primary"
                    : "text-muted-foreground hover:text-primary hover:bg-primary/10"
                }`}
                onClick={() => onLink(t)}
                title={t.isLinked ? "Manage links" : "Find match and link"}
                aria-label={
                  t.isLinked
                    ? `Manage links for ${t.description}`
                    : `Find match and link ${t.description}`
                }
              >
                <Link2 size={14} />
              </Button>
              {!accountClosed && (
                <Button
                  variant="ghost"
                  size="icon-sm"
                  className="text-muted-foreground hover:text-destructive hover:bg-destructive/10"
                  onClick={() => onDelete(t.id)}
                  title="Delete transaction"
                  aria-label={`Delete ${t.description}`}
                >
                  <Trash2 size={14} />
                </Button>
              )}
            </div>
          );
        },
        meta: { headerClassName: "w-12.5" },
      }),
    ],
    [
      categoryOptionGroups,
      payeeOptions,
      onCategoryChange,
      onPayeeChange,
      onDelete,
      onLink,
      onEdit,
      closedById,
    ],
  );
}
