import { useMemo } from "react";
import { ArrowRight, AlertCircle, ChevronRight } from "lucide-react";
import { createColumnHelper, type ColumnDef } from "@/lib/react-table";
import {
  DATE_FORMAT_OPTIONS,
  getMappingErrors,
  TARGET_FIELDS,
  targetFieldsFor,
  type ColumnMapping,
  type CsvRow,
} from "./importHelpers";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { DataTable } from "@/components/ui/data-table";
import {
  Table,
  TableBody,
  TableCell,
  TableRow,
} from "@/components/ui/table";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

interface MappingStepProps {
  csvData: CsvRow[] | null;
  csvHeaders: string[];
  columnMapping: ColumnMapping;
  amountMode: string;
  dateFormat: string;
  onMappingChange: (key: string, csvHeader: string) => void;
  onAmountModeChange: (mode: string) => void;
  onDateFormatChange: (format: string) => void;
  onBack: () => void;
  onNext: () => void;
}

// Step 3: map each target field to a CSV column and preview the raw rows.
export default function MappingStep({
  csvData,
  csvHeaders,
  columnMapping,
  amountMode,
  dateFormat,
  onMappingChange,
  onAmountModeChange,
  onDateFormatChange,
  onBack,
  onNext,
}: MappingStepProps) {
  const mappingErrors = useMemo(
    () => (csvData ? getMappingErrors(columnMapping, amountMode) : []),
    [csvData, columnMapping, amountMode],
  );

  // Reverse lookup: which field each CSV column feeds, for highlighting.
  const csvTarget = useMemo(() => {
    const map: Record<string, string> = {};
    for (const f of TARGET_FIELDS) {
      const src = columnMapping[f.key];
      if (src) map[src] = f.key;
    }
    return map;
  }, [columnMapping]);

  // Dynamic columns for the raw-CSV preview (one per header).
  const csvPreviewColumns = useMemo<ColumnDef<CsvRow, any>[]>(() => {
    const colHelper = createColumnHelper<CsvRow>();
    return csvHeaders.map((h) =>
      colHelper.accessor((row) => row[h], {
        id: h,
        header: () => (
          <>
            {h}
            {csvTarget[h] && (
              <div className="text-[10px] font-medium text-primary mt-0.5 tracking-wide">
                → {csvTarget[h].toUpperCase()}
              </div>
            )}
          </>
        ),
        cell: ({ row }) => (
          <span
            className={
              csvTarget[h] ? "text-muted-foreground" : "opacity-40 text-muted-foreground"
            }
          >
            {row.getValue(h)}
          </span>
        ),
        meta: {
          headerClassName:
            "py-2.5 px-4 h-auto text-xs font-semibold text-muted-foreground bg-background border-b border-border whitespace-nowrap",
          cellClassName:
            "py-2 px-4 text-xs max-w-37.5 overflow-hidden text-ellipsis whitespace-nowrap",
        },
      }),
    );
  }, [csvHeaders, csvTarget]);

  return (
    <div
      className="bg-card border border-border rounded-xl p-6"
      style={{ maxWidth: "800px" }}
    >
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-4">
        <h3 className="text-lg font-bold text-foreground">Map CSV Columns</h3>
        <div className="flex flex-col sm:flex-row sm:items-center gap-3">
          <div className="flex items-center gap-2">
            <Label
              htmlFor="import-date-format"
              className="text-xs font-semibold text-muted-foreground whitespace-nowrap"
            >
              Date Format
            </Label>
            <Select value={dateFormat} onValueChange={onDateFormatChange}>
              <SelectTrigger
                id="import-date-format"
                size="sm"
                className="bg-background"
              >
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
          <div className="flex gap-2">
            <Button
              size="sm"
              variant={amountMode === "single" ? "default" : "outline"}
              className={amountMode === "single" ? "" : "text-muted-foreground"}
              onClick={() => onAmountModeChange("single")}
            >
              Single Amount
            </Button>
            <Button
              size="sm"
              variant={amountMode === "separate" ? "default" : "outline"}
              className={amountMode === "separate" ? "" : "text-muted-foreground"}
              onClick={() => onAmountModeChange("separate")}
            >
              Debit / Credit
            </Button>
          </div>
        </div>
      </div>

      <p className="text-sm text-muted-foreground mb-6">
        Select which CSV column supplies each field below. Columns you don't map
        are ignored.
      </p>

      <div className="bg-background border border-border rounded-lg overflow-hidden">
        <Table>
          <TableBody>
            {targetFieldsFor(amountMode).map((f) => (
              <TableRow key={f.key} className="hover:bg-card/50">
                <TableCell className="py-4 px-4 w-2/5 align-top">
                  <div className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider">
                    Field
                  </div>
                  <div className="font-medium text-sm text-foreground mt-0.5">
                    {f.label}
                    {f.required && (
                      <span className="ml-2 text-[10px] font-bold text-destructive uppercase tracking-wide">
                        Required
                      </span>
                    )}
                  </div>
                  <div className="text-xs text-muted-foreground truncate mt-0.5">
                    {columnMapping[f.key]
                      ? `e.g. "${csvData?.[0]?.[columnMapping[f.key] ?? ""] || "—"}"`
                      : "No CSV column selected"}
                  </div>
                </TableCell>
                <TableCell className="py-4 px-2 align-middle w-12">
                  <ArrowRight
                    className="text-muted-foreground shrink-0"
                    size={20}
                  />
                </TableCell>
                <TableCell className="py-4 px-4 w-2/5 align-top">
                  <div className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider">
                    From CSV Column
                  </div>
                  <Select
                    value={columnMapping[f.key] || "none"}
                    onValueChange={(v) =>
                      onMappingChange(f.key, v === "none" ? "" : v)
                    }
                  >
                    <SelectTrigger className="mt-1 w-full h-10 bg-card">
                      <SelectValue
                        placeholder={
                          f.required ? "— Select a column —" : "— Not mapped —"
                        }
                      />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="none">
                        {f.required ? "— Select a column —" : "— Not mapped —"}
                      </SelectItem>
                      {csvHeaders.map((h) => (
                        <SelectItem key={h} value={h}>
                          {h}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      {mappingErrors.length > 0 && (
        <div className="mt-5 p-4 bg-destructive/10 border border-destructive/30 rounded-lg">
          {mappingErrors.map((err) => (
            <div
              key={err}
              className="flex gap-2 items-center text-destructive text-sm py-0.5"
            >
              <AlertCircle size={14} className="shrink-0" /> {err}
            </div>
          ))}
        </div>
      )}

      {/* Preview first 5 rows */}
      {csvData && (
        <div className="mt-6 border border-border rounded-lg bg-card">
          <DataTable
            columns={csvPreviewColumns}
            data={csvData.slice(0, 5)}
            containerClassName=""
            tableClassName="min-w-max"
            headerClassName=""
            cellClassName=""
          />
        </div>
      )}

      <div className="pt-5 mt-6 border-t border-border flex justify-between gap-4">
        <Button variant="outline" onClick={onBack}>
          Back
        </Button>
        <Button
          size="lg"
          className="px-5"
          disabled={mappingErrors.length > 0}
          onClick={onNext}
        >
          Preview Transactions <ChevronRight size={16} />
        </Button>
      </div>
    </div>
  );
}
