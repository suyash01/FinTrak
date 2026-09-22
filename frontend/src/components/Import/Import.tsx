import { useState, useEffect, useRef, useMemo } from "react";
import Papa from "papaparse";
import { ChevronRight } from "lucide-react";
import api from "../../api/client";
import { useDomainData } from "../../context/DomainDataContext";
import type {
  Account,
  BillingCycle,
  ImportResult,
  ImportTransaction,
  ImportTransactionsRequest,
  StatementExtractor,
  Transaction,
  ValidateTransactionsResponse,
} from "../../types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { toast } from "sonner";
import { toastApiError } from "../../lib/errors";
import AccountSelect from "@/components/AccountSelect/AccountSelect";
import {
  autoDetectMapping,
  apiDate,
  buildParsedTransactions,
  filterExcluded,
  fingerprintOf,
  MAX_CSV_BYTES,
  MAX_EXISTING_FETCH_PAGES,
  type ColumnMapping,
  type CsvRow,
} from "./importHelpers";
import ImportSteps from "./ImportSteps";
import MappingStep from "./MappingStep";
import UploadStep from "./UploadStep";
import PreviewStep from "./PreviewStep";
import DoneStep from "./DoneStep";
import DuplicateDialog from "./DuplicateDialog";
import ValidationDialog from "./ValidationDialog";

interface NewAccountForm {
  name: string;
  accountTypeId: string;
  bank: string;
  color: string;
}

const EMPTY_NEW_ACCOUNT: NewAccountForm = {
  name: "",
  accountTypeId: "bank",
  bank: "",
  color: "#06b6d4",
};

export default function Import() {
  const { accounts, accountTypes, payees, setAccounts, refreshAccounts } =
    useDomainData();
  const [step, setStep] = useState(1);
  const [selectedAccount, setSelectedAccount] = useState("");
  const [newAccount, setNewAccount] =
    useState<NewAccountForm>(EMPTY_NEW_ACCOUNT);
  const [showNewAccount, setShowNewAccount] = useState(false);

  // CSV state
  const [csvData, setCsvData] = useState<CsvRow[] | null>(null);
  const [csvHeaders, setCsvHeaders] = useState<string[]>([]);
  const [columnMapping, setColumnMapping] = useState<ColumnMapping>({
    date: null,
    description: null,
    amount: null,
    debit: null,
    credit: null,
    payee: null,
  });
  const [dateFormat, setDateFormat] = useState("auto");
  const [amountMode, setAmountMode] = useState("single"); // 'single' or 'separate'

  // Statement (PDF) state
  const [statementMode, setStatementMode] = useState("csv"); // 'csv' | 'pdf'
  const [parsing, setParsing] = useState(false);
  const [pdfPassword, setPdfPassword] = useState("");
  const [pdfDateFormat, setPdfDateFormat] = useState("auto");
  const [statementSummary, setStatementSummary] = useState<Record<
    string,
    string | number
  > | null>(null);
  // Parser-reported subtotal mismatches for the current statement rows.
  const [validationErrors, setValidationErrors] = useState<string[]>([]);
  const [statementTxns, setStatementTxns] = useState<
    ImportTransaction[] | null
  >(null);
  const [pdfFile, setPdfFile] = useState<File | null>(null);
  const [extractors, setExtractors] = useState<StatementExtractor[]>([]);
  const [extractor, setExtractor] = useState("sbi_cc");

  // Import results
  const [importing, setImporting] = useState(false);
  const [importResult, setImportResult] = useState<ImportResult | null>(null);

  // Validation (read-only duplicate check against the selected account)
  const [validating, setValidating] = useState(false);
  const [validationResult, setValidationResult] =
    useState<ValidateTransactionsResponse | null>(null);

  // Duplicate detection
  const [existingTxns, setExistingTxns] = useState<Transaction[]>([]);
  // True when the account has more history than MAX_EXISTING_FETCH_PAGES pages,
  // so the loaded duplicate snapshot is incomplete and its counts are partial.
  const [existingPartial, setExistingPartial] = useState(false);
  const [existingRefresh, setExistingRefresh] = useState(0);
  const [dupDialogOpen, setDupDialogOpen] = useState(false);

  // Transactions the user unchecks in the preview are excluded from the
  // import. Keyed by row index into `parsedTransactions`, and reset whenever
  // the parsed set is re-derived (new file, remap, reparse).
  const [excluded, setExcluded] = useState<Set<number>>(new Set());

  useEffect(() => {
    setExcluded(new Set());
  }, [csvData, columnMapping, amountMode, dateFormat, statementTxns]);

  // Billing cycle selection (accounts with a billing day): when chosen, every
  // imported transaction is attached to that cycle instead of the date-based
  // default.
  const [billingCycles, setBillingCycles] = useState<BillingCycle[]>([]);
  const [importBillingCycleId, setImportBillingCycleId] = useState("");

  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const pdfInputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    api
      .getStatementExtractors()
      .then((res) => {
        const list = res?.extractors || [];
        setExtractors(list);
        if (list.length > 0) setExtractor(list[0].name);
      })
      .catch((err) => toastApiError(err));
  }, []);

  // Load the account's existing transactions so duplicates can be flagged
  // before anything is imported. The backend clamps a page's LIMIT to
  // maxPageSize (1000) and reports the total page count, so every page is
  // fetched — an old `limit: 0` here silently returned only the 50 newest
  // rows, making "N already exist in this account" counts (and the
  // skip-duplicates prompt) wrong for anything older, and letting genuine
  // duplicates slip through an import with duplicateAction "keep".
  useEffect(() => {
    if (!selectedAccount) {
      setExistingTxns([]);
      setExistingPartial(false);
      return;
    }
    let cancelled = false;
    (async () => {
      const all: Transaction[] = [];
      let partial = false;
      try {
        for (let page = 1; page <= MAX_EXISTING_FETCH_PAGES; page++) {
          const res = await api.getTransactions({
            accountId: selectedAccount,
            limit: 1000,
            page,
          });
          // Synthetic billing-cycle summary rows are not real transactions
          // and must never count as duplicate candidates.
          all.push(...(res.data || []).filter((t) => !t.isSummary));
          const totalPages = res.pages || 1;
          if (page >= totalPages) break;
          if (page === MAX_EXISTING_FETCH_PAGES) {
            // More pages remain but the safety cap is reached: the snapshot
            // (and therefore the duplicate counts) is incomplete.
            partial = true;
            break;
          }
        }
        if (!cancelled) {
          setExistingTxns(all);
          setExistingPartial(partial);
        }
      } catch (err) {
        // Keep the previously loaded set on failure (e.g. a transient
        // network error mid-pagination) rather than wiping the counts.
        if (!cancelled) toastApiError(err);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [selectedAccount, existingRefresh]);

  // Load the billing cycles for the selected account when it has a billing day,
  // so the user can attach all imported transactions to one cycle. Reset the
  // selection whenever the account changes.
  const selectedAccountHasBillingDay = accounts.find(
    (a) => a.id === selectedAccount,
  )?.billingDay;
  useEffect(() => {
    setImportBillingCycleId("");
    if (!selectedAccountHasBillingDay) {
      setBillingCycles([]);
      return;
    }
    api
      .getBillingCycles(selectedAccount)
      .then((res) => setBillingCycles(res.data || []))
      .catch(() => setBillingCycles([]));
  }, [selectedAccount, selectedAccountHasBillingDay]);

  // ---- Step 1: Select Account ----
  const handleCreateAccount = async () => {
    try {
      const acc = await api.createAccount(newAccount);
      setAccounts((prev) => [acc, ...prev]);
      setSelectedAccount(acc.id);
      setShowNewAccount(false);
      setNewAccount(EMPTY_NEW_ACCOUNT);
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  // ---- Step 2: Upload & Parse CSV ----
  const handleFileUpload = (e: {
    target: { files: FileList | File[] | null };
  }) => {
    const file = e.target.files?.[0];
    if (!file) return;
    if (file.size > MAX_CSV_BYTES) {
      toast.error(
        `CSV is too large (max ${Math.round(MAX_CSV_BYTES / (1024 * 1024))} MB).`,
      );
      return;
    }

    // Clear any previously parsed PDF so its rows can't leak into the CSV
    // preview (parsedTransactions prefers statementTxns when non-null).
    setStatementTxns(null);
    setStatementSummary(null);
    setValidationErrors([]);
    setPdfFile(null);

    Papa.parse<CsvRow>(file, {
      header: true,
      skipEmptyLines: true,
      // Parse off the main thread where Web Workers are available so a large
      // file doesn't freeze the UI. jsdom (tests) has no Worker, so fall back
      // to the synchronous parser there.
      worker: typeof Worker !== "undefined",
      complete: (results) => {
        setCsvData(results.data);
        setCsvHeaders(results.meta.fields || []);
        // Auto-detect column mapping
        const mapping = autoDetectMapping(results.meta.fields || []);
        setColumnMapping(mapping);
        // Detect if separate debit/credit columns
        const hasDebit = (results.meta.fields || []).some((h) =>
          /debit|withdrawal|dr/i.test(h),
        );
        const hasCredit = (results.meta.fields || []).some((h) =>
          /credit|deposit|cr/i.test(h),
        );
        if (hasDebit && hasCredit) {
          setAmountMode("separate");
        }
        setStep(3);
      },
      error: (err) => {
        toast.error("Failed to parse CSV: " + err.message);
      },
    });
  };

  const parsePdf = async (file: File, chosenExtractor: string) => {
    setParsing(true);
    setStatementTxns(null);
    setStatementSummary(null);
    setValidationErrors([]);
    try {
      const fd = new FormData();
      fd.append("file", file);
      if (pdfPassword) fd.append("password", pdfPassword);
      if (pdfDateFormat !== "auto") fd.append("date_format", pdfDateFormat);
      if (chosenExtractor) fd.append("extractor", chosenExtractor);
      const result = await api.parseStatement(fd);
      setStatementTxns(result.transactions || []);
      setStatementSummary(result.summary || null);
      setValidationErrors(result.validationErrors || []);
      setStep(4);
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setParsing(false);
    }
  };

  const handlePdfUpload = async (e: {
    target: { files: FileList | File[] | null };
  }) => {
    const file = e.target.files?.[0];
    if (!file) return;
    // Clear any previously parsed CSV so its rows can't leak into the PDF
    // preview if parsing fails.
    setCsvData(null);
    setCsvHeaders([]);
    setPdfFile(file);
    await parsePdf(file, extractor);
  };

  // ---- Step 3: Column Mapping ----
  const updateMapping = (key: string, csvHeader: string) => {
    setColumnMapping((prev) => ({ ...prev, [key]: csvHeader || null }));
  };

  // ---- Step 4: Preview & Import ----
  const parsedTransactions = useMemo(() => {
    if (statementTxns) return statementTxns;
    return buildParsedTransactions({
      csvData,
      columnMapping,
      amountMode,
      dateFormat,
      accounts,
      accountTypes,
      payees,
      selectedAccount,
    });
  }, [
    statementTxns,
    csvData,
    columnMapping,
    amountMode,
    dateFormat,
    accounts,
    accountTypes,
    payees,
    selectedAccount,
  ]);

  // Fingerprints of transactions already stored for the selected account. The
  // fingerprint formula mirrors the backend so counts match between preview and
  // the import endpoint.
  const existingSet = useMemo(
    () =>
      new Set(
        existingTxns.map((t) =>
          fingerprintOf(apiDate(t.date), t.amount, t.type, t.description),
        ),
      ),
    [existingTxns],
  );

  // Only transactions still checked are candidates for import, validation,
  // and duplicate detection; excluded rows never reach the backend.
  const includedTransactions = useMemo(
    () => filterExcluded(parsedTransactions, excluded),
    [parsedTransactions, excluded],
  );
  const includedCount = includedTransactions.length;
  const excludedCount = parsedTransactions.length - includedCount;

  const { dupCount, inFileDupCount, existingDupCount } = useMemo(() => {
    const seen = new Set<string>();
    let inFileDup = 0;
    let existingDup = 0;
    let total = 0;
    for (const t of includedTransactions) {
      const fp = fingerprintOf(t.date, t.amount, t.type, t.description);
      const matchesExisting = existingSet.has(fp);
      const repeatsInFile = seen.has(fp);
      if (matchesExisting) existingDup++;
      if (repeatsInFile) inFileDup++;
      if (matchesExisting || repeatsInFile) total++;
      seen.add(fp);
    }
    return {
      dupCount: total,
      inFileDupCount: inFileDup,
      existingDupCount: existingDup,
    };
  }, [includedTransactions, existingSet]);

  const runImport = async (action: "skip" | "keep") => {
    setDupDialogOpen(false);
    setImporting(true);
    try {
      if (parsedTransactions.length === 0) {
        toast.error(
          "No valid transactions found. Please check your column mapping.",
        );
        return;
      }
      if (includedTransactions.length === 0) {
        toast.error(
          "All transactions are excluded. Tick at least one row to import.",
        );
        return;
      }

      const payload: ImportTransactionsRequest = {
        accountId: selectedAccount,
        transactions: includedTransactions,
        duplicateAction: action,
      };
      // Accounts with a billing day can attach every transaction to a chosen
      // billing cycle (null falls back to the date-based default).
      if (selectedAccountHasBillingDay) {
        payload.billingCycleId = importBillingCycleId || null;
      }
      const result = await api.importTransactions(payload);
      setImportResult(result);
      setStep(5);
      // The imported rows move the account's balance, so the shared account
      // list is reloaded for every consumer of it.
      void refreshAccounts();
    } catch (err) {
      toast.error("Import failed: " + (err as Error).message);
    } finally {
      setImporting(false);
    }
  };

  const handleImport = () => {
    if (dupCount > 0) {
      setDupDialogOpen(true);
      return;
    }
    runImport("keep");
  };

  // Read-only check: ask the backend which of the parsed transactions already
  // exist in the selected account. Nothing is written.
  const runValidation = async () => {
    setValidating(true);
    try {
      if (parsedTransactions.length === 0) {
        toast.error(
          "No valid transactions found. Please check your column mapping.",
        );
        return;
      }
      if (includedTransactions.length === 0) {
        toast.error(
          "All transactions are excluded. Tick at least one row to validate.",
        );
        return;
      }
      const result = await api.validateTransactions({
        accountId: selectedAccount,
        transactions: includedTransactions,
      });
      setValidationResult(result);
    } catch (err) {
      toast.error("Validation failed: " + (err as Error).message);
    } finally {
      setValidating(false);
    }
  };

  return (
    <>
      <div className="shrink-0 px-8 pt-6">
        <h1 className="text-2xl font-bold mb-1">Import Statement</h1>
        <p className="text-muted-foreground text-sm">
          Upload and map your CSV bank or credit card statement
        </p>
      </div>
      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto w-full">
        {/* Steps indicator */}
        <ImportSteps step={step} onSelect={setStep} />

        {/* Step 1: Select Account */}
        {step === 1 && (
          <div
            className="bg-card border border-border rounded-xl p-6"
            style={{ maxWidth: "600px" }}
          >
            <h3 className="text-lg font-semibold mb-4 text-foreground">
              Select Account
            </h3>
            {accounts.length > 0 && !showNewAccount && (
              <div className="flex flex-col gap-1.5 mb-5">
                <Label
                  htmlFor="import-account"
                  className="text-muted-foreground"
                >
                  Existing Account
                </Label>
                <AccountSelect
                  id="import-account"
                  accounts={accounts.filter((a) => !a.closed)}
                  value={selectedAccount || "none"}
                  onValueChange={(v) =>
                    setSelectedAccount(v === "none" ? "" : v)
                  }
                  placeholder="Choose an account..."
                  triggerClassName="w-full h-10 bg-background"
                  extraItems={
                    <SelectItem value="none">Choose an account...</SelectItem>
                  }
                />
              </div>
            )}

            {!showNewAccount && (
              <Button
                variant="outline"
                className="mb-4"
                onClick={() => setShowNewAccount(true)}
              >
                + Create New Account
              </Button>
            )}

            {showNewAccount && (
              <div className="bg-background p-5 rounded-lg border border-border mb-4">
                <h4 className="mb-4 text-sm font-semibold text-foreground">
                  New Account
                </h4>
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 mb-4">
                  <div className="flex flex-col gap-1.5">
                    <Label
                      htmlFor="import-new-name"
                      className="text-muted-foreground"
                    >
                      Name
                    </Label>
                    <Input
                      id="import-new-name"
                      className="h-10 bg-card"
                      placeholder="e.g. HDFC Savings"
                      value={newAccount.name}
                      onChange={(e) =>
                        setNewAccount({ ...newAccount, name: e.target.value })
                      }
                    />
                  </div>
                  <div className="flex flex-col gap-1.5">
                    <Label
                      htmlFor="import-new-type"
                      className="text-muted-foreground"
                    >
                      Type
                    </Label>
                    <Select
                      value={newAccount.accountTypeId}
                      onValueChange={(v) =>
                        setNewAccount({
                          ...newAccount,
                          accountTypeId: v,
                        })
                      }
                    >
                      <SelectTrigger
                        id="import-new-type"
                        className="w-full h-10 bg-card"
                      >
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {accountTypes.map((at) => (
                          <SelectItem key={at.id} value={at.id}>
                            {at.name}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>
                </div>
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 mb-5">
                  <div className="flex flex-col gap-1.5">
                    <Label
                      htmlFor="import-new-bank"
                      className="text-muted-foreground"
                    >
                      Bank Name
                    </Label>
                    <Input
                      id="import-new-bank"
                      className="h-10 bg-card"
                      placeholder="e.g. HDFC, ICICI, SBI"
                      value={newAccount.bank}
                      onChange={(e) =>
                        setNewAccount({ ...newAccount, bank: e.target.value })
                      }
                    />
                  </div>
                  <div className="flex flex-col gap-1.5">
                    <Label
                      htmlFor="import-new-color"
                      className="text-muted-foreground"
                    >
                      Color
                    </Label>
                    <input
                      id="import-new-color"
                      type="color"
                      value={newAccount.color}
                      onChange={(e) =>
                        setNewAccount({ ...newAccount, color: e.target.value })
                      }
                      className="w-full h-10.5 cursor-pointer bg-card border border-border rounded-lg p-1"
                    />
                  </div>
                </div>
                <div className="flex gap-3">
                  <Button
                    size="lg"
                    className="px-5"
                    onClick={handleCreateAccount}
                    disabled={!newAccount.name}
                  >
                    Create
                  </Button>
                  <Button
                    variant="outline"
                    onClick={() => setShowNewAccount(false)}
                  >
                    Cancel
                  </Button>
                </div>
              </div>
            )}

            <div className="pt-4 mt-2 border-t border-border">
              <Button
                size="lg"
                className="px-5"
                disabled={!selectedAccount}
                onClick={() => setStep(2)}
              >
                Continue <ChevronRight size={16} />
              </Button>
            </div>
          </div>
        )}

        {/* Step 2: Upload */}
        {step === 2 && (
          <UploadStep
            statementMode={statementMode}
            onStatementModeChange={setStatementMode}
            parsing={parsing}
            fileInputRef={fileInputRef}
            pdfInputRef={pdfInputRef}
            onCsvUpload={handleFileUpload}
            onPdfUpload={handlePdfUpload}
            extractor={extractor}
            onExtractorChange={setExtractor}
            extractors={extractors}
            pdfPassword={pdfPassword}
            onPdfPasswordChange={setPdfPassword}
          />
        )}

        {/* Step 3: Column Mapping */}
        {step === 3 && (
          <MappingStep
            csvData={csvData}
            csvHeaders={csvHeaders}
            columnMapping={columnMapping}
            amountMode={amountMode}
            dateFormat={dateFormat}
            onMappingChange={updateMapping}
            onAmountModeChange={setAmountMode}
            onDateFormatChange={setDateFormat}
            onBack={() => setStep(2)}
            onNext={() => setStep(4)}
          />
        )}

        {/* Step 4: Preview & Confirm Import */}
        {step === 4 && (
          <PreviewStep
            parsedTransactions={parsedTransactions}
            payees={payees}
            excluded={excluded}
            onExcludedChange={setExcluded}
            includedCount={includedCount}
            excludedCount={excludedCount}
            statementTxns={statementTxns}
            csvData={csvData}
            selectedAccountHasBillingDay={selectedAccountHasBillingDay}
            billingCycles={billingCycles}
            importBillingCycleId={importBillingCycleId}
            onImportBillingCycleChange={setImportBillingCycleId}
            statementSummary={statementSummary}
            validationErrors={validationErrors}
            dupCount={dupCount}
            existingDupCount={existingDupCount}
            inFileDupCount={inFileDupCount}
            pdfFile={pdfFile}
            extractor={extractor}
            onExtractorChange={setExtractor}
            extractors={extractors}
            pdfDateFormat={pdfDateFormat}
            onPdfDateFormatChange={setPdfDateFormat}
            onReparse={() => {
              if (pdfFile) parsePdf(pdfFile, extractor);
            }}
            parsing={parsing}
            validating={validating}
            importing={importing}
            onBack={() => setStep(statementTxns ? 2 : 3)}
            onValidate={runValidation}
            onImport={handleImport}
          />
        )}

        {/* Step 5: Done */}
        {step === 5 && importResult && (
          <DoneStep
            importResult={importResult}
            parsedTransactions={parsedTransactions}
            excludedCount={excludedCount}
            onImportAnother={() => {
              setStep(1);
              setCsvData(null);
              setCsvHeaders([]);
              setColumnMapping({
                date: null,
                description: null,
                amount: null,
                debit: null,
                credit: null,
                payee: null,
              });
              setImportResult(null);
              setStatementTxns(null);
              setStatementSummary(null);
              setValidationErrors([]);
              setPdfPassword("");
              setPdfDateFormat("auto");
              setPdfFile(null);
              setExistingRefresh((k) => k + 1);
            }}
          />
        )}

        {/* Duplicate handling dialog */}
        <DuplicateDialog
          open={dupDialogOpen}
          onOpenChange={setDupDialogOpen}
          dupCount={dupCount}
          includedCount={includedCount}
          existingDupCount={existingDupCount}
          inFileDupCount={inFileDupCount}
          partialExisting={existingPartial}
          importing={importing}
          onSkip={() => runImport("skip")}
          onKeep={() => runImport("keep")}
        />

        {/* Validation results dialog */}
        {validationResult && (
          <ValidationDialog
            result={validationResult}
            accountName={
              accounts.find((a) => a.id === selectedAccount)?.name || ""
            }
            onClose={() => setValidationResult(null)}
          />
        )}
      </div>
    </>
  );
}
