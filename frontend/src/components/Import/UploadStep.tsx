import type { DragEvent, RefObject } from "react";
import { FileSpreadsheet, FileText } from "lucide-react";
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

interface UploadStepProps {
  statementMode: string;
  onStatementModeChange: (mode: string) => void;
  parsing: boolean;
  fileInputRef: RefObject<HTMLInputElement | null>;
  pdfInputRef: RefObject<HTMLInputElement | null>;
  onCsvUpload: (e: FileInputEvent) => void;
  onPdfUpload: (e: FileInputEvent) => void;
  extractor: string;
  onExtractorChange: (value: string) => void;
  extractors: StatementExtractor[];
  pdfPassword: string;
  onPdfPasswordChange: (value: string) => void;
}

// Step 2: choose the source (CSV or statement PDF) and upload a file. Owns the
// two drag-and-drop dropzones; the actual parsing callbacks live in Import.
export default function UploadStep({
  statementMode,
  onStatementModeChange,
  parsing,
  fileInputRef,
  pdfInputRef,
  onCsvUpload,
  onPdfUpload,
  extractor,
  onExtractorChange,
  extractors,
  pdfPassword,
  onPdfPasswordChange,
}: UploadStepProps) {
  const handleDrop = (
    e: DragEvent<HTMLDivElement>,
    ref: RefObject<HTMLInputElement | null>,
    upload: (event: FileInputEvent) => void,
  ) => {
    e.preventDefault();
    e.currentTarget.classList.remove("border-primary", "bg-card/80");
    const file = e.dataTransfer?.files[0];
    if (!file) return;
    const dt = new DataTransfer();
    dt.items.add(file);
    if (ref.current) {
      ref.current.files = dt.files;
    }
    upload({ target: { files: [file] } });
  };

  return (
    <div
      className="bg-card border border-border rounded-xl p-6"
      style={{ maxWidth: "600px" }}
    >
      <div className="flex gap-2 mb-6">
        <Button
          size="lg"
          className={`flex-1 ${statementMode === "csv" ? "" : "text-muted-foreground hover:text-foreground"}`}
          variant={statementMode === "csv" ? "default" : "outline"}
          onClick={() => onStatementModeChange("csv")}
        >
          <FileSpreadsheet size={18} /> CSV
        </Button>
        <Button
          size="lg"
          className={`flex-1 ${statementMode === "pdf" ? "" : "text-muted-foreground hover:text-foreground"}`}
          variant={statementMode === "pdf" ? "default" : "outline"}
          onClick={() => onStatementModeChange("pdf")}
        >
          <FileText size={18} /> Statement PDF
        </Button>
      </div>

      {statementMode === "csv" ? (
        <>
          <div
            role="button"
            tabIndex={0}
            aria-label="Upload CSV file"
            className="border-2 border-dashed border-border bg-background/50 rounded-xl p-12 flex flex-col items-center justify-center text-center cursor-pointer transition-colors hover:border-primary/50 hover:bg-card/50 group"
            onClick={() => fileInputRef.current?.click()}
            onKeyDown={(e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                fileInputRef.current?.click();
              }
            }}
            onDragOver={(e) => {
              e.preventDefault();
              e.currentTarget.classList.add("border-primary", "bg-card/80");
            }}
            onDragLeave={(e) =>
              e.currentTarget.classList.remove("border-primary", "bg-card/80")
            }
            onDrop={(e) => handleDrop(e, fileInputRef, onCsvUpload)}
          >
            <div className="w-16 h-16 rounded-full bg-muted flex items-center justify-center mb-4 group-hover:bg-primary/20 text-muted-foreground group-hover:text-primary transition-colors">
              <FileSpreadsheet size={32} />
            </div>
            <h3 className="text-lg font-semibold text-foreground mb-2">
              Drop your CSV file here
            </h3>
            <p className="text-sm text-muted-foreground">
              or click to browse. Supports .csv files from any bank.
            </p>
          </div>
          <input
            ref={fileInputRef}
            type="file"
            accept=".csv"
            className="hidden"
            onChange={onCsvUpload}
          />
        </>
      ) : (
        <>
          <div
            role="button"
            tabIndex={0}
            aria-label="Upload statement PDF"
            className="border-2 border-dashed border-border bg-background/50 rounded-xl p-12 flex flex-col items-center justify-center text-center cursor-pointer transition-colors hover:border-primary/50 hover:bg-card/50 group"
            onClick={() => pdfInputRef.current?.click()}
            onKeyDown={(e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                pdfInputRef.current?.click();
              }
            }}
            onDragOver={(e) => {
              e.preventDefault();
              e.currentTarget.classList.add("border-primary", "bg-card/80");
            }}
            onDragLeave={(e) =>
              e.currentTarget.classList.remove("border-primary", "bg-card/80")
            }
            onDrop={(e) => handleDrop(e, pdfInputRef, onPdfUpload)}
          >
            <div className="w-16 h-16 rounded-full bg-muted flex items-center justify-center mb-4 group-hover:bg-primary/20 text-muted-foreground group-hover:text-primary transition-colors">
              {parsing ? (
                <Spinner className="size-8 text-primary" />
              ) : (
                <FileText size={32} />
              )}
            </div>
            <h3 className="text-lg font-semibold text-foreground mb-2">
              {parsing
                ? "Parsing statement..."
                : "Drop your statement PDF here"}
            </h3>
            <p className="text-sm text-muted-foreground">
              {parsing
                ? "Extracting transactions from your statement."
                : "or click to browse. The extracted transactions will be shown for review."}
            </p>
          </div>
          <input
            ref={pdfInputRef}
            type="file"
            accept=".pdf"
            className="hidden"
            onChange={onPdfUpload}
          />
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
