import { useRef, useState } from "react";
import { toast } from "sonner";
import api from "../../api/client";
import { useDomainData } from "../../context/DomainDataContext";
import { Button } from "@/components/ui/button";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import type { BackupImportResult } from "../../types";

// summarize renders the restore counts as a short human phrase.
function summarize(result: BackupImportResult): string {
  const parts: [number, string][] = [
    [result.accounts, "account"],
    [result.transactions, "transaction"],
    [result.categories, "category"],
    [result.payees, "payee"],
    [result.billingCycles, "billing cycle"],
    [result.links, "link"],
    [result.recurringSeries, "recurring series"],
    [result.rules, "rule"],
  ];
  const rendered = parts
    .filter(([count]) => count > 0)
    .map(([count, label]) => `${count} ${label}${count === 1 ? "" : "s"}`);
  return rendered.length > 0 ? rendered.join(", ") : "no rows";
}

interface PendingImport {
  name: string;
  bundle: unknown;
}

export default function DataSettingsManager() {
  const { refreshAll } = useDomainData();
  const inputRef = useRef<HTMLInputElement>(null);
  const [exporting, setExporting] = useState(false);
  const [importing, setImporting] = useState(false);
  const [pending, setPending] = useState<PendingImport | null>(null);

  const handleExport = async () => {
    setExporting(true);
    try {
      await api.exportUserData();
      toast.success("Backup downloaded");
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setExporting(false);
    }
  };

  const handleFile = async (file: File | undefined) => {
    if (!file) return;
    try {
      const bundle: unknown = JSON.parse(await file.text());
      setPending({ name: file.name, bundle });
    } catch {
      toast.error("That file is not a valid JSON backup");
    }
  };

  const handleImport = async () => {
    if (!pending) return;
    setImporting(true);
    try {
      const result = await api.importUserData(pending.bundle);
      setPending(null);
      await refreshAll();
      const warnings = result.warnings?.length ?? 0;
      if (warnings > 0) {
        toast.warning(
          `Backup restored: ${summarize(result)} (${warnings} row${warnings === 1 ? "" : "s"} skipped)`,
        );
      } else {
        toast.success(`Backup restored: ${summarize(result)}`);
      }
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setImporting(false);
    }
  };

  return (
    <div className="space-y-5">
      <div className="flex items-start justify-between gap-4">
        <div>
          <div className="text-sm font-medium text-foreground">
            Export all data
          </div>
          <div className="text-[13px] text-muted-foreground">
            Download a JSON backup of every account, transaction, category,
            payee, rule, link and subscription.
          </div>
        </div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={handleExport}
          disabled={exporting}
        >
          {exporting ? "Exporting..." : "Export"}
        </Button>
      </div>

      <div className="flex items-start justify-between gap-4">
        <div>
          <div className="text-sm font-medium text-foreground">
            Import backup
          </div>
          <div className="text-[13px] text-muted-foreground">
            Restore a previously exported backup. Importing is only allowed
            while you have no accounts, so it can never merge with existing
            data.
          </div>
        </div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => inputRef.current?.click()}
          disabled={importing}
        >
          {importing ? "Importing..." : "Import"}
        </Button>
        <input
          ref={inputRef}
          type="file"
          accept="application/json,.json"
          className="hidden"
          aria-label="Backup file"
          onChange={(e) => {
            void handleFile(e.target.files?.[0]);
            e.target.value = "";
          }}
        />
      </div>

      <AlertDialog
        open={pending !== null}
        onOpenChange={(open) => !open && setPending(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Restore this backup?</AlertDialogTitle>
            <AlertDialogDescription>
              {pending?.name} will be imported into your account. This creates
              new accounts, transactions and settings, and cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={() => void handleImport()}>
              Import
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
