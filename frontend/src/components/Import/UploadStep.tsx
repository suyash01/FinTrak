import type { DragEvent, ReactNode, RefObject } from "react";
import { FileCode2, FileSpreadsheet, FileText } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Spinner } from "@/components/ui/spinner";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { StatementExtractor } from "../../types";

type FileInputEvent = { target: { files: FileList | File[] | null } };

/**
 * Which upload the user picked. `csv` and `pdf` need a column mapping and a
 * parser extractor respectively; `bank` needs neither, because an ISO 20022 or
 * OFX file already says what each field means.
 */
type Source = "csv" | "pdf" | "bank";

interface UploadStepProps {
  statementMode: string;
  onStatementModeChange: (mode: string) => void;
  parsing: boolean;
  fileInputRef: RefObject<HTMLInputElement | null>;
  pdfInputRef: RefObject<HTMLInputElement | null>;
  bankFileRef: RefObject<HTMLInputElement | null>;
  onCsvUpload: (e: FileInputEvent) => void;
  onPdfUpload: (e: FileInputEvent) => void;
  onBankFileUpload: (e: FileInputEvent) => void;
  extractor: string;
  onExtractorChange: (value: string) => void;
  extractors: StatementExtractor[];
  pdfPassword: string;
  onPdfPasswordChange: (value: string) => void;
}

interface SourceConfig {
  id: Source;
  label: string;
  icon: typeof FileSpreadsheet;
  accept: string;
  dropzoneLabel: string;
  idleHint: string;
  /** Accessible name of the dropzone, which is also the file-picker trigger. */
  pickerLabel: string;
}

const SOURCES: SourceConfig[] = [
  {
    id: "csv",
    label: "CSV",
    icon: FileSpreadsheet,
    accept: ".csv",
    dropzoneLabel: "Drop your CSV file here",
    idleHint: "or click to browse. Supports .csv files from any bank.",
    pickerLabel: "Upload CSV file",
  },
  {
    id: "pdf",
    label: "Statement PDF",
    icon: FileText,
    accept: ".pdf",
    dropzoneLabel: "Drop your statement PDF here",
    idleHint:
      "or click to browse. The extracted transactions will be shown for review.",
    pickerLabel: "Upload statement PDF",
  },
  {
    id: "bank",
    label: "Bank File",
    icon: FileCode2,
    accept: ".xml,.ofx,.qfx,.txt",
    dropzoneLabel: "Drop your bank file here",
    idleHint:
      "or click to browse. Reads ISO 20022 (camt.052, camt.053) and OFX/QFX exports.",
    pickerLabel: "Upload bank file",
  },
];

// Step 2: choose the source (CSV, statement PDF, or a bank-supplied ISO 20022 /
// OFX file) and upload it. Owns the dropzones; the parsing callbacks live in
// Import.
export default function UploadStep({
  statementMode,
  onStatementModeChange,
  parsing,
  fileInputRef,
  pdfInputRef,
  bankFileRef,
  onCsvUpload,
  onPdfUpload,
  onBankFileUpload,
  extractor,
  onExtractorChange,
  extractors,
  pdfPassword,
  onPdfPasswordChange,
}: UploadStepProps) {
  const source = SOURCES.find((s) => s.id === statementMode) ?? SOURCES[0];
  const ref =
    source.id === "csv"
      ? fileInputRef
      : source.id === "pdf"
        ? pdfInputRef
        : bankFileRef;
  const upload =
    source.id === "csv"
      ? onCsvUpload
      : source.id === "pdf"
        ? onPdfUpload
        : onBankFileUpload;
  const busy = parsing && source.id !== "csv";

  const handleDrop = (e: DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    e.currentTarget.classList.remove("border-primary", "bg-card/80");
    const file = e.dataTransfer?.files[0];
    if (!file) return;
    // Nothing reads the input's value afterwards, so a dropped file is handed
    // straight to the upload handler and the input is left empty — the same
    // state the picker leaves it in, so the same file can be dropped or picked
    // again after a failed parse.
    if (ref.current) ref.current.value = "";
    upload({ target: { files: [file] } });
  };

  const Icon = source.icon;

  return (
    <div
      className="bg-card border border-border rounded-xl p-6"
      style={{ maxWidth: "600px" }}
    >
      <div className="flex gap-2 mb-6">
        {SOURCES.map((s) => (
          <Button
            key={s.id}
            size="lg"
            className={`flex-1 ${source.id === s.id ? "" : "text-muted-foreground hover:text-foreground"}`}
            variant={source.id === s.id ? "default" : "outline"}
            onClick={() => onStatementModeChange(s.id)}
          >
            <s.icon size={18} /> {s.label}
          </Button>
        ))}
      </div>

      <div
        role="button"
        tabIndex={0}
        aria-label={source.pickerLabel}
        className="border-2 border-dashed border-border bg-background/50 rounded-xl p-12 flex flex-col items-center justify-content text-center cursor-pointer transition-colors hover:border-primary/50 hover:bg-card/50 group"
        onClick={() => ref.current?.click()}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            ref.current?.click();
          }
        }}
        onDragOver={(e) => {
          e.preventDefault();
          e.currentTarget.classList.add("border-primary", "bg-card/80");
        }}
        onDragLeave={(e) =>
          e.currentTarget.classList.remove("border-primary", "bg-card/80")
        }
        onDrop={handleDrop}
      >
        <div className="w-16 h-16 rounded-full bg-muted flex items-center justify-center mb-4 group-hover:bg-primary/20 text-muted-foreground group-hover:text-primary transition-colors">
          {busy ? <Spinner className="size-8 text-primary" /> : <Icon size={32} />}
        </div>
        <h3 className="text-lg font-semibold text-foreground mb-2">
          {busy
            ? source.id === "pdf"
              ? "Parsing statement..."
              : "Reading bank file..."
            : source.dropzoneLabel}
        </h3>
        <p className="text-sm text-muted-foreground">
          {busy
            ? source.id === "pdf"
              ? "Extracting transactions from your statement."
              : "Working out which format this file is."
            : source.idleHint}
        </p>
      </div>
      <input
        ref={ref}
        type="file"
        accept={source.accept}
        className="hidden"
        onChange={(e) => {
          upload(e);
          // Clear the chosen file once the handler owns it: an input keeps its
          // value, so re-selecting the same file after a failed parse would
          // fire no change event and the retry would silently do nothing. Same
          // reason as DataSettingsManager's backup input.
          e.currentTarget.value = "";
        }}
      />

      {/* The parser's own controls, which mean nothing for a file that already
          states what each field is. */}
      {source.id === "pdf" && (
        <>
          <div className="mt-4 flex flex-col gap-1.5">
            <Label htmlFor="import-extractor" className="text-muted-foreground">
              Extractor
            </Label>
            <Select value={extractor} onValueChange={onExtractorChange}>
              <SelectTrigger
                id="import-extractor"
                className="w-full h-10 bg-background"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {extractors.length === 0 && (
                  <SelectItem value="sbi_cc">SBI Credit Card</SelectItem>
                )}
                {extractors.map((ex) => (
                  <SelectItem key={ex.name} value={ex.name}>
                    {ex.display_name || ex.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="mt-4 flex flex-col gap-1.5">
            <Label
              htmlFor="import-pdf-password"
              className="text-muted-foreground"
            >
              Password (if the PDF is protected)
            </Label>
            <Input
              id="import-pdf-password"
              type="password"
              className="h-10 bg-background"
              placeholder="Optional"
              value={pdfPassword}
              onChange={(e) => onPdfPasswordChange(e.target.value)}
            />
          </div>
        </>
      )}
    </div>
  );
}
