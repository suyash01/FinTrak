import { useMemo, type Dispatch, type SetStateAction } from "react";
import { AlertTriangle, ShieldCheck } from "lucide-react";
import { createColumnHelper, type ColumnDef } from "@/lib/react-table";
import {
  DATE_FORMAT_OPTIONS,
  siblingIndices,
  type CsvRow,
} from "./importHelpers";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import { Badge } from "@/components/ui/badge";
import { Spinner } from "@/components/ui/spinner";
import { DataTable } from "@/components/ui/data-table";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { formatCurrency, formatDate } from "../../utils/formatters";
import type {
  BillingCycle,
  ImportTransaction,
  Payee,
  StatementExtractor,
} from "../../types";

interface PreviewStepProps {
  parsedTransactions: ImportTransaction[];
  payees: Payee[];
  excluded: Set<number>;
  onExcludedChange: Dispatch<SetStateAction<Set<number>>>;
  includedCount: number;
  excludedCount: number;
  statementTxns: ImportTransaction[] | null;
  csvData: CsvRow[] | null;
  selectedAccountHasBillingDay: number | null | undefined;
  billingCycles: BillingCycle[];
  importBillingCycleId: string;
  onImportBillingCycleChange: (id: string) => void;
  statementSummary: Record<string, string | number> | null;
  dupCount: number;
  existingDupCount: number;
  inFileDupCount: number;
  pdfFile: File | null;
  extractor: string;
  onExtractorChange: (value: string) => void;
  extractors: StatementExtractor[];
  pdfDateFormat: string;
  onPdfDateFormatChange: (value: string) => void;
  onReparse: () => void;
  parsing: boolean;
  validating: boolean;
  importing: boolean;
  onBack: () => void;
  onValidate: () => void;
  onImport: () => void;
}

// Step 4: preview the parsed transactions, let the user exclude rows, choose a
// billing cycle / reparse the PDF, then validate or import.
export default function PreviewStep({
  parsedTransactions,
  payees,
  excluded,
  onExcludedChange,
  includedCount,
  excludedCount,
  statementTxns,
  csvData,
  selectedAccountHasBillingDay,
  billingCycles,
  importBillingCycleId,
  onImportBillingCycleChange,
  statementSummary,
  dupCount,
  existingDupCount,
  inFileDupCount,
  pdfFile,
  extractor,
  onExtractorChange,
  extractors,
  pdfDateFormat,
  onPdfDateFormatChange,
  onReparse,
  parsing,
  validating,
  importing,
  onBack,
  onValidate,
  onImport,
}: PreviewStepProps) {
  const previewColumns = useMemo<ColumnDef<ImportTransaction, any>[]>(() => {
    const colHelper = createColumnHelper<ImportTransaction>();
    const headBase =
      "py-3 px-4 h-auto text-xs font-semibold uppercase tracking-wider text-muted-foreground";
    return [
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
                        { length: parsedTransactions.length },
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
              // Toggling one occurrence toggles every identical row (same
              // date/amount/type/description) in the file, so a duplicated
              // transaction cannot sneak back in through its twin.
              onExcludedChange((prev) => {
                const next = new Set(prev);
                const sibs = siblingIndices(parsedTransactions, row.index);
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
    ];
  }, [payees, excluded, parsedTransactions, onExcludedChange]);

  return (
    <div className="bg-card border border-border rounded-xl p-6">
      <div className="flex flex-col sm:flex-row justify-between sm:items-end gap-2 mb-4">
        <h3 className="text-xl font-bold text-foreground">
          Preview — {parsedTransactions.length} transactions
          {excludedCount > 0 && (
            <span className="ml-2 text-base font-semibold text-amber-500">
              ({includedCount} selected)
            </span>
          )}
        </h3>
        <div className="flex flex-col items-end gap-1">
          {!statementTxns && csvData && (
            <div className="text-sm text-muted-foreground font-medium">
              {csvData.length - parsedTransactions.length} rows skipped
              (empty/invalid)
            </div>
          )}
          {excludedCount > 0 && (
            <div className="text-sm font-medium text-amber-500/90">
              {excludedCount} transaction{excludedCount === 1 ? "" : "s"}{" "}
              excluded from import
            </div>
          )}
        </div>
      </div>

      {selectedAccountHasBillingDay && (
        <div className="mb-5 p-4 bg-background border border-border rounded-lg">
          <div className="flex flex-col gap-1.5">
            <Label className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider">
              Billing Cycle — attach all imported transactions
            </Label>
            <Select
              value={importBillingCycleId || "auto"}
              onValueChange={(v) =>
                onImportBillingCycleChange(v === "auto" ? "" : v)
              }
            >
              <SelectTrigger className="w-full bg-card">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="auto">
                  Auto (by transaction date)
                </SelectItem>
                {billingCycles.map((bc) => (
                  <SelectItem key={bc.id} value={bc.id}>
                    {bc.label} ({formatDate(bc.startDate)} –{" "}
                    {formatDate(bc.endDate)})
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <p className="text-xs text-muted-foreground">
              Leave on "Auto" to attach each transaction to the cycle matching
              its date, or pick a cycle to force every imported transaction into
              it.
            </p>
          </div>
        </div>
      )}

      {statementTxns && pdfFile && (
        <div className="mb-5 p-4 bg-background border border-border rounded-lg flex flex-wrap items-end gap-3">
          <div className="flex flex-col gap-1.5 min-w-45">
            <Label className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider">
              Reparse with extractor
            </Label>
            <Select value={extractor} onValueChange={onExtractorChange}>
              <SelectTrigger className="w-full bg-card">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {extractors.map((ex) => (
                  <SelectItem key={ex.name} value={ex.name}>
                    {ex.display_name || ex.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1.5 min-w-45">
            <Label className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider">
              Date Format
            </Label>
            <Select
              value={pdfDateFormat}
              onValueChange={onPdfDateFormatChange}
            >
              <SelectTrigger className="w-full bg-card">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {DATE_FORMAT_OPTIONS.map((f) => (
                  <SelectItem key={f.value} value={f.value}>
                    {f.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <Button
            variant="outline"
            onClick={onReparse}
            disabled={parsing}
          >
            {parsing ? (
              <>
                <Spinner className="size-4" /> Reparsing...
              </>
            ) : (
              <>Reparse PDF</>
            )}
          </Button>
          <p className="text-xs text-muted-foreground w-full">
            Parsing didn't look right? Try a different extractor or date format
            to reprocess the same file.
          </p>
        </div>
      )}

      {statementSummary && (
        <div className="mb-5 p-4 bg-primary/10 border border-primary/20 rounded-lg">
          <div className="text-xs font-semibold text-primary uppercase tracking-wider mb-2">
            Statement Summary
          </div>
          <div className="flex flex-wrap gap-x-6 gap-y-1 text-sm text-muted-foreground">
            {Object.entries(statementSummary).map(([k, v]) => (
              <div key={k}>
                <span className="text-muted-foreground capitalize">
                  {k.replace(/_/g, " ")}:{" "}
                </span>
                <span className="font-medium text-foreground">{v}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {dupCount > 0 && (
        <div className="mb-5 p-4 bg-amber-500/10 border border-amber-500/25 rounded-lg flex gap-3 items-start">
          <AlertTriangle size={18} className="text-amber-500 shrink-0 mt-0.5" />
          <div className="text-sm text-amber-200">
            <p className="font-semibold mb-1">
              {dupCount} transaction{dupCount === 1 ? "" : "s"} look like
              duplicates.
            </p>
            <p className="text-amber-200/80">
              {existingDupCount > 0 && (
                <span>
                  {existingDupCount} already exist
                  {existingDupCount === 1 ? "s" : ""} in this account
                  {inFileDupCount > 0 ? ", " : ". "}
                </span>
              )}
              {inFileDupCount > 0 && (
                <span>
                  {inFileDupCount} repeat{inFileDupCount === 1 ? "s" : ""}{" "}
                  within this file.{" "}
                </span>
              )}
              You'll be asked what to do before importing.
            </p>
          </div>
        </div>
      )}

      <p className="text-sm text-muted-foreground mb-3">
        Uncheck any row to exclude it from the import. Excluded transactions are
        not validated or imported.
      </p>

      <div className="border border-border rounded-lg overflow-hidden bg-background">
        <DataTable
          columns={previewColumns}
          data={parsedTransactions}
          containerClassName=""
          tableClassName="min-w-150"
          virtualize
          maxHeight={500}
          theadClassName="sticky top-0 bg-card z-10 shadow-[0_1px_0_var(--tw-shadow-color)] shadow-border"
          headerClassName=""
          cellClassName=""
        />
      </div>

      <div className="pt-5 mt-6 border-t border-border flex justify-between gap-4">
        <Button variant="outline" onClick={onBack}>
          {statementTxns ? "Back to Upload" : "Back to Mapping"}
        </Button>
        <div className="flex gap-3">
          <Button
            variant="outline"
            className="bg-muted text-primary border-primary/30"
            onClick={onValidate}
            disabled={validating || includedCount === 0}
            title="Check which of these transactions already exist in this account (no data is written)"
          >
            {validating ? (
              <>
                <Spinner className="size-4" /> Validating...
              </>
            ) : (
              <>
                <ShieldCheck size={15} /> Validate
              </>
            )}
          </Button>
          <Button
            size="lg"
            className="px-5"
            onClick={onImport}
            disabled={importing || includedCount === 0}
          >
            {importing ? (
              <div className="flex gap-2 items-center">
                <Spinner className="size-4" /> Importing...
              </div>
            ) : (
              <>
                Import {includedCount} Transaction
                {includedCount === 1 ? "" : "s"}
              </>
            )}
          </Button>
        </div>
      </div>
    </div>
  );
}
