import { useNavigate } from "react-router-dom";
import { Check } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { ImportResult, ImportTransaction } from "../../types";

interface DoneStepProps {
  importResult: ImportResult;
  parsedTransactions: ImportTransaction[];
  excludedCount: number;
  onImportAnother: () => void;
}

// Step 5: import-complete summary with the "what next" actions.
export default function DoneStep({
  importResult,
  parsedTransactions,
  excludedCount,
  onImportAnother,
}: DoneStepProps) {
  const navigate = useNavigate();

  return (
    <div className="bg-card border border-border rounded-xl p-10 max-w-125 text-center mx-auto mt-10">
      <div className="w-16 h-16 rounded-full bg-chart-3/15 flex items-center justify-center mx-auto mb-6 text-chart-3 shadow-[0_0_20px_rgba(16,185,129,0.2)]">
        <Check size={32} />
      </div>
      <h2 className="text-2xl font-bold text-foreground mb-2">
        Import Complete!
      </h2>
      <p className="text-muted-foreground mb-8 max-w-[80%] mx-auto leading-relaxed">
        <span className="font-semibold text-foreground">
          {importResult.imported}
        </span>{" "}
        of {parsedTransactions.length} transactions imported
        {excludedCount > 0 && (
          <>
            {" "}
            (
            {importResult.duplicates > 0 &&
              `${importResult.duplicates} duplicate${
                importResult.duplicates === 1 ? "" : "s"
              } skipped, `}
            {excludedCount} excluded)
          </>
        )}
        {excludedCount === 0 && importResult.duplicates > 0
          ? ` (${importResult.duplicates} duplicates skipped)`
          : null}
        .
      </p>
      <div className="flex flex-col sm:flex-row flex-wrap gap-4 justify-center">
        <Button variant="outline" className="px-6" onClick={onImportAnother}>
          Import Another
        </Button>
        <Button
          size="lg"
          className="px-6"
          onClick={() => navigate("/transactions")}
        >
          View Transactions
        </Button>
        <Button
          variant="outline"
          className="px-6"
          onClick={() => navigate("/linking")}
        >
          Transfer Suggestions
        </Button>
      </div>
    </div>
  );
}
