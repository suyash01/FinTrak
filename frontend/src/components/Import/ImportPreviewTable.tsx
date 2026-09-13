import { useMemo, type Dispatch, type SetStateAction } from "react";
import { createColumnHelper, type ColumnDef } from "@/lib/react-table";
import { Checkbox } from "@/components/ui/checkbox";
import { Badge } from "@/components/ui/badge";
import { DataTable } from "@/components/ui/data-table";
import { formatCurrency } from "../../utils/formatters";
import type { ImportTransaction, Payee } from "../../types";
import { siblingIndices } from "./importHelpers";

interface ImportPreviewTableProps {
  transactions: ImportTransaction[];
  excluded: Set<number>;
  onExcludedChange: Dispatch<SetStateAction<Set<number>>>;
  payees?: Payee[];
  showPayee?: boolean;
  maxHeight?: number;
  stickyHeader?: boolean;
}

// Shared preview table for both the CSV/PDF wizard and the Paperless import:
// an inclusion checkbox column plus the parsed transaction fields. Toggling one
// occurrence toggles every identical row (same date/amount/type/description) so
// a duplicated transaction cannot sneak back in through its twin.
export default function ImportPreviewTable({
  transactions,
  excluded,
  onExcludedChange,
  payees = [],
  showPayee = true,
  maxHeight = 500,
  stickyHeader = true,
}: ImportPreviewTableProps) {
  const columns = useMemo<ColumnDef<ImportTransaction, any>[]>(() => {
    const colHelper = createColumnHelper<ImportTransaction>();
    const headBase =
      "py-3 px-4 h-auto text-xs font-semibold uppercase tracking-wider text-muted-foreground";

    const cols: ColumnDef<ImportTransaction, any>[] = [
      colHelper.display({
        id: "include",
        header: () => (
          <Checkbox
            checked={excluded.size === 0}
            aria-label="Include all parsed transactions"
            onCheckedChange={() =>
              onExcludedChange(
                excluded.size === 0
                  ? new Set(
                      Array.from(
                        { length: transactions.length },
                        (_, i) => i,
                      ),
                    )
                  : new Set(),
              )
            }
          />
        ),
        cell: ({ row }) => (
          <Checkbox
            checked={!excluded.has(row.index)}
            aria-label={
              row.original.description || `Transaction ${row.index + 1}`
            }
            onCheckedChange={(c) => {
              onExcludedChange((prev) => {
                const next = new Set(prev);
                const sibs = siblingIndices(transactions, row.index);
                if (c === true) sibs.forEach((i) => next.delete(i));
                else sibs.forEach((i) => next.add(i));
                return next;
              });
            }}
          />
        ),
        meta: {
          headerClassName: `${headBase} w-12`,
          cellClassName: "py-2.5 px-4",
        },
      }),
      colHelper.accessor("date", {
        header: () => "Date",
        cell: ({ row }) => (
          <span className="text-sm text-muted-foreground whitespace-nowrap">
            {row.original.date}
          </span>
        ),
        meta: {
          headerClassName: `${headBase} w-28`,
          cellClassName: "py-2.5 px-4 text-sm",
        },
      }),
      colHelper.accessor("description", {
        header: () => "Description",
        cell: ({ row }) => (
          <span className="text-sm text-foreground max-w-50 overflow-hidden text-ellipsis whitespace-nowrap">
            {row.original.description}
          </span>
        ),
        meta: { headerClassName: headBase, cellClassName: "py-2.5 px-4 text-sm" },
      }),
    ];

    if (showPayee) {
      cols.push(
        colHelper.accessor("payeeId", {
          header: () => "Payee",
          cell: ({ row }) =>
            row.original.payeeId ? (
              <span className="text-primary font-medium">
                {payees.find((p) => p.id === row.original.payeeId)?.name}
              </span>
            ) : (
              <span className="opacity-30 italic">Not found</span>
            ),
          meta: {
            headerClassName: headBase,
            cellClassName:
              "py-2.5 px-4 text-sm max-w-37.5 overflow-hidden text-ellipsis whitespace-nowrap",
          },
        }),
      );
    }

    cols.push(
      colHelper.accessor("type", {
        header: () => "Type",
        cell: ({ row }) => (
          <Badge
            variant="outline"
            className={`${
              row.original.type === "debit"
                ? "bg-destructive/10 text-destructive border-destructive/30"
                : "bg-emerald-500/10 text-emerald-500 border-emerald-500/20"
            }`}
          >
            {row.original.type}
          </Badge>
        ),
        meta: {
          headerClassName: `${headBase} w-24`,
          cellClassName: "py-2.5 px-4",
        },
      }),
      colHelper.accessor("amount", {
        header: () => "Amount",
        cell: ({ row }) => (
          <span
            className={`font-medium whitespace-nowrap ${
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
          headerClassName: `${headBase} text-right w-32`,
          cellClassName: "py-2.5 px-4 text-right",
        },
      }),
    );

    return cols;
  }, [
    payees,
    excluded,
    transactions,
    onExcludedChange,
    showPayee,
  ]);

  return (
    <div className="border border-border rounded-lg overflow-hidden bg-background">
      <DataTable
        columns={columns}
        data={transactions}
        containerClassName=""
        tableClassName="min-w-150"
        virtualize
        maxHeight={maxHeight}
        theadClassName={
          stickyHeader
            ? "sticky top-0 bg-card z-10 shadow-[0_1px_0_var(--tw-shadow-color)] shadow-border"
            : undefined
        }
        headerClassName=""
        cellClassName=""
      />
    </div>
  );
}
