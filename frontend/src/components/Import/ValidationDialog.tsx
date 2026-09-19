import { useMemo } from "react";
import { ShieldCheck, CheckCircle2, PlusCircle } from "lucide-react";
import { createColumnHelper, type ColumnDef } from "@/lib/react-table";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { DataTable } from "@/components/ui/data-table";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { formatCurrency } from "../../utils/formatters";
import type {
  ValidateTransactionResult,
  ValidateTransactionsResponse,
} from "../../types";

interface ValidationDialogProps {
  result: ValidateTransactionsResponse;
  accountName: string;
  onClose: () => void;
}

// Read-only results of the duplicate check against the selected account.
export default function ValidationDialog({
  result,
  accountName,
  onClose,
}: ValidationDialogProps) {
  const validationColumns = useMemo<
    ColumnDef<ValidateTransactionResult, any>[]
  >(() => {
    const colHelper = createColumnHelper<ValidateTransactionResult>();
    const headBase =
      "py-3 px-4 h-auto text-xs font-semibold uppercase tracking-wider text-muted-foreground";
    return [
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
      colHelper.accessor("type", {
        header: () => "Type",
        cell: ({ row }) => (
          <Badge
            variant="outline"
            className={`${
              row.original.type === "debit"
                ? "bg-destructive/10 text-destructive border-destructive/30"
                : "bg-chart-3/10 text-chart-3 border-chart-3/20"
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
                : "text-chart-3"
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
      colHelper.display({
        id: "status",
        header: () => "Status",
        cell: ({ row }) =>
          row.original.exists ? (
            <Badge
              variant="outline"
              className="bg-amber-500/10 border-amber-500/25"
            >
              <CheckCircle2 size={12} /> Already exists
            </Badge>
          ) : (
            <Badge
              variant="outline"
              className="bg-chart-3/10 text-chart-3 border-chart-3/25"
            >
              <PlusCircle size={12} /> New
            </Badge>
          ),
        meta: {
          headerClassName: `${headBase} w-32`,
          cellClassName: "py-2.5 px-4",
        },
      }),
    ];
  }, []);

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent className="max-w-2xl sm:max-w-2xl max-h-[85vh] flex flex-col overflow-hidden">
        <DialogHeader>
          <div className="flex items-center gap-2.5">
            <ShieldCheck size={20} className="text-primary" />
            <DialogTitle className="text-lg font-bold">
              Validation Results
            </DialogTitle>
          </div>
          <DialogDescription>
            Checked against{" "}
            <span className="font-medium text-foreground">
              {accountName || "this account"}
            </span>
            . Nothing was imported.
          </DialogDescription>
        </DialogHeader>

        {/* Summary */}
        <div className="grid grid-cols-3 gap-3">
          <div className="p-3 bg-background border border-border rounded-lg text-center">
            <div className="text-2xl font-bold text-foreground">
              {result.total}
            </div>
            <div className="text-xs text-muted-foreground mt-0.5">Total</div>
          </div>
          <div className="p-3 bg-amber-500/10 border border-amber-500/25 rounded-lg text-center">
            <div className="text-2xl font-bold text-foreground">
              {result.existingCount}
            </div>
            <div className="text-xs text-muted-foreground mt-0.5">Already exist</div>
          </div>
          <div className="p-3 bg-chart-3/10 border border-chart-3/25 rounded-lg text-center">
            <div className="text-2xl font-bold text-chart-3">
              {result.missingCount}
            </div>
            <div className="text-xs text-muted-foreground mt-0.5">New</div>
          </div>
        </div>

        {/* Per-transaction list */}
        <div className="border border-border rounded-lg overflow-auto flex-1 min-h-0 bg-background">
          <DataTable
            columns={validationColumns}
            data={result.results}
            getRowId={(row) => String(row.index)}
            containerClassName=""
            tableClassName="min-w-150"
            theadClassName="sticky top-0 bg-card z-10 shadow-[0_1px_0_var(--tw-shadow-color)] shadow-border"
            headerClassName=""
            cellClassName=""
          />
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
