import { AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

interface DuplicateDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  dupCount: number;
  includedCount: number;
  existingDupCount: number;
  inFileDupCount: number;
  importing: boolean;
  onSkip: () => void;
  onKeep: () => void;
}

// Confirmation shown when duplicate transactions are detected before import.
export default function DuplicateDialog({
  open,
  onOpenChange,
  dupCount,
  includedCount,
  existingDupCount,
  inFileDupCount,
  importing,
  onSkip,
  onKeep,
}: DuplicateDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md sm:max-w-md">
        <DialogHeader>
          <div className="flex items-center gap-2.5 text-amber-400">
            <AlertTriangle size={20} />
            <DialogTitle className="text-lg font-bold">
              Duplicate transactions found
            </DialogTitle>
          </div>
          <DialogDescription>
            {dupCount} of the {includedCount} transaction
            {includedCount === 1 ? "" : "s"} match an existing transaction or
            repeat within this file.
          </DialogDescription>
        </DialogHeader>
        <ul className="text-sm text-muted-foreground list-disc pl-5 space-y-1">
          {existingDupCount > 0 && (
            <li>
              {existingDupCount} already{" "}
              {existingDupCount === 1 ? "exists in" : "exist in"} this account
            </li>
          )}
          {inFileDupCount > 0 && (
            <li>
              {inFileDupCount} repeat{inFileDupCount === 1 ? "s" : ""} within
              this file
            </li>
          )}
        </ul>
        <p className="text-sm text-muted-foreground">
          How would you like to handle them?
        </p>
        <div className="flex flex-col gap-3">
          <Button
            size="lg"
            className="w-full"
            onClick={onSkip}
            disabled={importing}
          >
            Skip duplicates
          </Button>
          <Button
            variant="outline"
            size="lg"
            className="w-full"
            onClick={onKeep}
            disabled={importing}
          >
            Keep all (import everything)
          </Button>
          <Button
            variant="ghost"
            size="lg"
            className="w-full"
            onClick={() => onOpenChange(false)}
            disabled={importing}
          >
            Cancel
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
