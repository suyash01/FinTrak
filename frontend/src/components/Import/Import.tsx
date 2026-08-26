import { useState, useEffect, useRef, useMemo, type ChangeEvent } from "react";
import Papa from "papaparse";
import {
  ChevronRight,
  Check,
  ArrowRight,
  AlertCircle,
  AlertTriangle,
  FileSpreadsheet,
  FileText,
  ShieldCheck,
  CheckCircle2,
  PlusCircle,
} from "lucide-react";
import api from "../../api/client";
import {
  formatCurrency,
  formatDate,
  formatDateOnly,
} from "../../utils/formatters";
import type {
  Account,
  AccountType,
  BillingCycle,
  ImportResult,
  ImportTransaction,
  ImportTransactionsRequest,
  Payee,
  StatementExtractor,
  Transaction,
  TransactionType,
  ValidateTransactionsResponse,
} from "../../types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Spinner } from "@/components/ui/spinner";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { toast } from "sonner";

type CsvRow = Record<string, string>;

interface ColumnMapping {
  date: string | null;
  description: string | null;
  amount: string | null;
  debit: string | null;
  credit: string | null;
  payee: string | null;
}

interface TargetField {
  key: keyof ColumnMapping;
  label: string;
  required: boolean;
  mode?: string;
}

const TARGET_FIELDS: TargetField[] = [
  { key: "date", label: "Date", required: true },
  { key: "description", label: "Description", required: true },
  { key: "amount", label: "Amount", required: true, mode: "single" },
  { key: "debit", label: "Debit Amount", required: true, mode: "separate" },
  { key: "credit", label: "Credit Amount", required: true, mode: "separate" },
  { key: "payee", label: "Payee", required: false },
];

const targetFieldsFor = (amountMode: string): TargetField[] =>
  TARGET_FIELDS.filter((f) => !f.mode || f.mode === amountMode);

const pad2 = (n: string | number) => String(n).padStart(2, "0");

const MONTHS: Record<string, string> = {
  jan: "01",
  feb: "02",
  mar: "03",
  apr: "04",
  may: "05",
  jun: "06",
  jul: "07",
  aug: "08",
  sep: "09",
  oct: "10",
  nov: "11",
  dec: "12",
};

const DATE_FORMAT_OPTIONS = [
  { value: "auto", label: "Auto-detect" },
  { value: "DD/MM/YYYY", label: "DD/MM/YYYY" },
  { value: "MM/DD/YYYY", label: "MM/DD/YYYY" },
  { value: "DD/MM/YY", label: "DD/MM/YY" },
  { value: "YYYY-MM-DD", label: "YYYY-MM-DD" },
  { value: "DD Mon YYYY", label: "DD Mon YYYY" },
];

const DATE_PATTERNS: Record<string, RegExp> = {
  "DD/MM/YYYY": /^(\d{1,2})[/\-.](\d{1,2})[/\-.](\d{4})$/,
  "MM/DD/YYYY": /^(\d{1,2})[/\-.](\d{1,2})[/\-.](\d{4})$/,
  "DD/MM/YY": /^(\d{1,2})[/\-.](\d{1,2})[/\-.](\d{2})$/,
  "YYYY-MM-DD": /^(\d{4})[/\-.](\d{1,2})[/\-.](\d{1,2})$/,
  "DD Mon YYYY":
    /^(\d{1,2})\s+(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)[a-z]*\s+(\d{4})$/i,
};

function parseDateExplicit(str: string, format: string): string | null {
  const m = String(str).match(DATE_PATTERNS[format]);
  if (!m) return null;
  if (format === "DD/MM/YYYY") return `${m[3]}-${pad2(m[2])}-${pad2(m[1])}`;
  if (format === "MM/DD/YYYY") return `${m[3]}-${pad2(m[1])}-${pad2(m[2])}`;
  if (format === "YYYY-MM-DD") return `${m[1]}-${pad2(m[2])}-${pad2(m[3])}`;
  if (format === "DD Mon YYYY")
    return `${m[3]}-${MONTHS[m[2].toLowerCase().substring(0, 3)]}-${pad2(m[1])}`;
  const year = parseInt(m[3]) > 50 ? `19${m[3]}` : `20${m[3]}`;
  return `${year}-${pad2(m[2])}-${pad2(m[1])}`;
}

function parseDateAuto(str: string): string | null {
  const s = String(str);
  let m = s.match(DATE_PATTERNS["DD/MM/YYYY"]);
  if (m) return `${m[3]}-${pad2(m[2])}-${pad2(m[1])}`;
  m = s.match(DATE_PATTERNS["YYYY-MM-DD"]);
  if (m) return `${m[1]}-${pad2(m[2])}-${pad2(m[3])}`;
  m = s.match(DATE_PATTERNS["DD/MM/YY"]);
  if (m) {
    const year = parseInt(m[3]) > 50 ? `19${m[3]}` : `20${m[3]}`;
    return `${year}-${pad2(m[2])}-${pad2(m[1])}`;
  }
  m = s.match(DATE_PATTERNS["DD Mon YYYY"]);
  if (m)
    return `${m[3]}-${MONTHS[m[2].toLowerCase().substring(0, 3)]}-${pad2(m[1])}`;
  const d = new Date(s);
  if (!isNaN(d.getTime())) return formatDateOnly(d);
  return null;
}

function parseDate(
  str: string | null | undefined,
  format: string,
): string | null {
  if (!str) return null;
  const value = String(str).trim();
  if (format !== "auto") {
    const parsed = parseDateExplicit(value, format);
    if (parsed) return parsed;
  }
  return parseDateAuto(value);
}

function parseAmount(str: string | number | null | undefined): number {
  if (str == null || str === "") return 0;
  if (typeof str === "number") return Number.isFinite(str) ? str : 0;

  const cleaned = String(str)
    .replace(/[^\d.,()+\-]/g, "")
    .trim();
  if (!cleaned) return 0;

  let negative = false;
  let body = cleaned;

  // Parenthesised negatives: (1,234.56)
  if (body.startsWith("(") && body.endsWith(")")) {
    negative = true;
    body = body.slice(1, -1);
  }
  // Trailing minus: 1,234.56-
  if (body.endsWith("-")) {
    negative = true;
    body = body.slice(0, -1);
  }

  let parsed: number;
  if (/^\d{1,3}(\.\d{3})+(,\d+)?$/.test(body)) {
    // European style: 1.234.567,89
    parsed = parseFloat(body.replace(/\./g, "").replace(",", "."));
  } else {
    // Remove thousands separators, use "." as decimal separator
    parsed = parseFloat(body.replace(/,/g, ""));
  }

  if (!Number.isFinite(parsed)) return 0;
  return negative ? -Math.abs(parsed) : parsed;
}

const getMappingErrors = (
  columnMapping: Partial<ColumnMapping>,
  amountMode: string,
): string[] => {
  const errors: string[] = [];
  if (!columnMapping.date)
    errors.push("Date field must be mapped to a CSV column");
  if (!columnMapping.description)
    errors.push("Description field must be mapped to a CSV column");
  if (amountMode === "single" && !columnMapping.amount) {
    errors.push(
      "Amount field must be mapped (or switch to separate Debit/Credit mode)",
    );
  }
  if (amountMode === "separate") {
    if (!columnMapping.debit)
      errors.push("Debit field must be mapped in separate mode");
    if (!columnMapping.credit)
      errors.push("Credit field must be mapped in separate mode");
  }
  return errors;
};

// Duplicate detection mirrors the backend fingerprint so that the count the
// user sees matches what the import endpoint would skip.
const FINGERPRINT_SEP = "\x00";
const fingerprintOf = (
  date: string,
  amount: number,
  type: string,
  description: string,
): string =>
  `${date}${FINGERPRINT_SEP}${Math.round(amount * 100)}${FINGERPRINT_SEP}${type}${FINGERPRINT_SEP}${String(
    description || "",
  )
    .trim()
    .toLowerCase()}`;

const apiDate = (d: string | null | undefined): string => {
  const m = String(d || "").match(/^(\d{4})-(\d{2})-(\d{2})/);
  return m ? m[0] : "";
};

interface BuildParsedTransactionsArgs {
  csvData: CsvRow[] | null;
  columnMapping: Partial<ColumnMapping>;
  amountMode: string;
  dateFormat: string;
  accounts: Account[];
  accountTypes: AccountType[];
  payees: Payee[];
  selectedAccount: string;
}

function buildParsedTransactions({
  csvData,
  columnMapping,
  amountMode,
  dateFormat,
  accounts,
  accountTypes,
  payees,
  selectedAccount,
}: BuildParsedTransactionsArgs): ImportTransaction[] {
  if (!csvData) return [];

  const dateCol = columnMapping.date;
  const descCol = columnMapping.description;
  const amountCol = columnMapping.amount;
  const debitCol = columnMapping.debit;
  const creditCol = columnMapping.credit;
  const payeeCol = columnMapping.payee;

  const selAcct = accounts.find((a) => a.id === selectedAccount);
  const selType = accountTypes.find((at) => at.id === selAcct?.accountTypeId);
  const positiveTxnType = selType?.positiveTxnType || "credit";

  return csvData
    .map((row): ImportTransaction | null => {
      const rawDate = dateCol ? row[dateCol]?.trim() : undefined;
      if (!rawDate) return null;

      const date = parseDate(rawDate, dateFormat);
      if (!date) return null;

      const description = descCol ? row[descCol]?.trim() || "" : "";
      if (!description) return null;

      let amount = 0;
      let type: TransactionType = "debit";

      // Determine sign convention from account type
      if (amountMode === "single" && amountCol) {
        const raw = parseAmount(row[amountCol]);
        if (raw < 0) {
          amount = Math.abs(raw);
          type = positiveTxnType === "credit" ? "debit" : "credit";
        } else {
          amount = raw;
          type = positiveTxnType as TransactionType;
        }
      } else if (amountMode === "separate") {
        const debitAmt = parseAmount(debitCol ? row[debitCol] : undefined);
        const creditAmt = parseAmount(creditCol ? row[creditCol] : undefined);
        if (debitAmt !== 0) {
          amount = Math.abs(debitAmt);
          type = "debit";
        } else if (creditAmt !== 0) {
          amount = Math.abs(creditAmt);
          type = "credit";
        } else {
          return null;
        }
      }

      if (amount === 0) return null;

      let payeeId: string | null = null;
      if (payeeCol && row[payeeCol]) {
        const name = row[payeeCol].trim().toLowerCase();
        const match = payees.find((p) => p.name.toLowerCase() === name);
        if (match) payeeId = match.id;
      }

      return { date, description, amount, type, payeeId };
    })
    .filter((t): t is ImportTransaction => Boolean(t));
}

function autoDetectMapping(headers: string[]): ColumnMapping {
  const mapping: ColumnMapping = {
    date: null,
    description: null,
    amount: null,
    debit: null,
    credit: null,
    payee: null,
  };
  const used = new Set<string>();
  const pick = (patterns: RegExp[]): string | null => {
    for (const h of headers) {
      const lower = String(h).toLowerCase().trim();
      if (!used.has(h) && patterns.some((p) => p.test(lower))) {
        used.add(h);
        return h;
      }
    }
    return null;
  };

  mapping.date = pick([/date|txn.*date|transaction.*date|value.*date/i]);
  mapping.description = pick([
    /narration|description|particulars|details|remark/i,
  ]);
  mapping.amount = pick([/^amount$|^transaction.*amount$|^txn.*amount$/i]);
  mapping.debit = pick([/debit|withdrawal|dr/i]);
  mapping.credit = pick([/credit|deposit|cr/i]);
  mapping.payee = pick([/payee|beneficiary|merchant|receiver|sender/i]);
  return mapping;
}

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
  const [step, setStep] = useState(1);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [accountTypes, setAccountTypes] = useState<AccountType[]>([]);
  const [selectedAccount, setSelectedAccount] = useState("");
  const [newAccount, setNewAccount] =
    useState<NewAccountForm>(EMPTY_NEW_ACCOUNT);
  const [showNewAccount, setShowNewAccount] = useState(false);
  const [payees, setPayees] = useState<Payee[]>([]);

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
  const [existingRefresh, setExistingRefresh] = useState(0);
  const [dupDialogOpen, setDupDialogOpen] = useState(false);

  // Billing cycle selection (credit-card accounts only): when chosen, every
  // imported transaction is attached to that cycle instead of the date-based
  // default.
  const [billingCycles, setBillingCycles] = useState<BillingCycle[]>([]);
  const [importBillingCycleId, setImportBillingCycleId] = useState("");

  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const pdfInputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    api.getAccounts().then(setAccounts).catch(console.error);
    api.getAccountTypes().then(setAccountTypes).catch(console.error);
    api.getPayees().then(setPayees).catch(console.error);
    api
      .getStatementExtractors()
      .then((res) => {
        const list = res?.extractors || [];
        setExtractors(list);
        if (list.length > 0) setExtractor(list[0].name);
      })
      .catch(console.error);
  }, []);

  // Load the account's existing transactions so duplicates can be flagged
  // before anything is imported.
  useEffect(() => {
    if (!selectedAccount) {
      setExistingTxns([]);
      return;
    }
    api
      .getTransactions({ accountId: selectedAccount, limit: 0 })
      .then((res) => setExistingTxns(res.data || []))
      .catch(console.error);
  }, [selectedAccount, existingRefresh]);

  // Load the billing cycles for the selected account when it is a credit card,
  // so the user can attach all imported transactions to one cycle. Reset the
  // selection whenever the account changes.
  const selectedAccountType = accounts.find(
    (a) => a.id === selectedAccount,
  )?.accountTypeId;
  useEffect(() => {
    setImportBillingCycleId("");
    if (selectedAccountType !== "credit_card") {
      setBillingCycles([]);
      return;
    }
    api
      .getBillingCycles(selectedAccount)
      .then((res) => setBillingCycles(res.data || []))
      .catch(() => setBillingCycles([]));
  }, [selectedAccount, selectedAccountType]);

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

    Papa.parse<CsvRow>(file, {
      header: true,
      skipEmptyLines: true,
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
    try {
      const fd = new FormData();
      fd.append("file", file);
      if (pdfPassword) fd.append("password", pdfPassword);
      if (pdfDateFormat !== "auto") fd.append("date_format", pdfDateFormat);
      if (chosenExtractor) fd.append("extractor", chosenExtractor);
      const result = await api.parseStatement(fd);
      setStatementTxns(result.transactions || []);
      setStatementSummary(result.summary || null);
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
    setPdfFile(file);
    await parsePdf(file, extractor);
  };

  // ---- Step 3: Column Mapping ----
  const updateMapping = (key: string, csvHeader: string) => {
    setColumnMapping((prev) => ({ ...prev, [key]: csvHeader || null }));
  };

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

  const { dupCount, inFileDupCount, existingDupCount } = useMemo(() => {
    const seen = new Set<string>();
    let inFileDup = 0;
    let existingDup = 0;
    let total = 0;
    for (const t of parsedTransactions) {
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
  }, [parsedTransactions, existingSet]);

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

      const payload: ImportTransactionsRequest = {
        accountId: selectedAccount,
        transactions: parsedTransactions,
        duplicateAction: action,
      };
      // Credit-card imports can attach every transaction to a chosen billing
      // cycle (null falls back to the date-based default).
      if (selectedAccountType === "credit_card") {
        payload.billingCycleId = importBillingCycleId || null;
      }
      const result = await api.importTransactions(payload);
      setImportResult(result);
      setStep(5);
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
      const result = await api.validateTransactions({
        accountId: selectedAccount,
        transactions: parsedTransactions,
      });
      setValidationResult(result);
    } catch (err) {
      toast.error("Validation failed: " + (err as Error).message);
    } finally {
      setValidating(false);
    }
  };

  const steps = [
    { num: 1, label: "Select Account" },
    { num: 2, label: "Upload CSV" },
    { num: 3, label: "Map Columns" },
    { num: 4, label: "Preview" },
    { num: 5, label: "Done" },
  ];

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
        <div className="flex items-center gap-4 mb-8 overflow-x-auto pb-2 scrollbar-none">
          {steps.map((s) => (
            <div
              key={s.num}
              className={`flex items-center gap-2 text-sm font-medium whitespace-nowrap px-3 py-1.5 rounded-lg transition-colors ${step === s.num ? "bg-primary/10 text-primary" : step > s.num ? "text-emerald-500" : "text-muted-foreground"}`}
              onClick={() => step > s.num && setStep(s.num)}
              style={{ cursor: step > s.num ? "pointer" : "default" }}
            >
              <span
                className={`w-6 h-6 rounded-full flex items-center justify-center text-xs font-bold shrink-0 ${step === s.num ? "bg-primary text-primary-foreground" : step > s.num ? "bg-emerald-500/20 text-emerald-500" : "bg-muted text-muted-foreground"}`}
              >
                {step > s.num ? <Check size={12} /> : s.num}
              </span>
              {s.label}
            </div>
          ))}
        </div>

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
                <Label className="text-muted-foreground">
                  Existing Account
                </Label>
                <Select
                  value={selectedAccount || "none"}
                  onValueChange={(v) =>
                    setSelectedAccount(v === "none" ? "" : v)
                  }
                >
                  <SelectTrigger className="w-full h-10 bg-background">
                    <SelectValue placeholder="Choose an account..." />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="none">Choose an account...</SelectItem>
                    {accounts.map((a) => (
                      <SelectItem key={a.id} value={a.id}>
                        {a.name} ({a.accountTypeName}
                        {a.bank ? `, ${a.bank}` : ""})
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
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
                    <Label className="text-muted-foreground">Name</Label>
                    <Input
                      className="h-10 bg-card"
                      placeholder="e.g. HDFC Savings"
                      value={newAccount.name}
                      onChange={(e) =>
                        setNewAccount({ ...newAccount, name: e.target.value })
                      }
                    />
                  </div>
                  <div className="flex flex-col gap-1.5">
                    <Label className="text-muted-foreground">Type</Label>
                    <Select
                      value={newAccount.accountTypeId}
                      onValueChange={(v) =>
                        setNewAccount({
                          ...newAccount,
                          accountTypeId: v,
                        })
                      }
                    >
                      <SelectTrigger className="w-full h-10 bg-card">
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
                    <Label className="text-muted-foreground">Bank Name</Label>
                    <Input
                      className="h-10 bg-card"
                      placeholder="e.g. HDFC, ICICI, SBI"
                      value={newAccount.bank}
                      onChange={(e) =>
                        setNewAccount({ ...newAccount, bank: e.target.value })
                      }
                    />
                  </div>
                  <div className="flex flex-col gap-1.5">
                    <Label className="text-muted-foreground">Color</Label>
                    <input
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
          <div
            className="bg-card border border-border rounded-xl p-6"
            style={{ maxWidth: "600px" }}
          >
            <div className="flex gap-2 mb-6">
              <Button
                size="lg"
                className={`flex-1 ${statementMode === "csv" ? "" : "text-muted-foreground hover:text-foreground"}`}
                variant={statementMode === "csv" ? "default" : "outline"}
                onClick={() => setStatementMode("csv")}
              >
                <FileSpreadsheet size={18} /> CSV
              </Button>
              <Button
                size="lg"
                className={`flex-1 ${statementMode === "pdf" ? "" : "text-muted-foreground hover:text-foreground"}`}
                variant={statementMode === "pdf" ? "default" : "outline"}
                onClick={() => setStatementMode("pdf")}
              >
                <FileText size={18} /> Statement PDF
              </Button>
            </div>

            {statementMode === "csv" ? (
              <>
                <div
                  className="border-2 border-dashed border-border bg-background/50 rounded-xl p-12 flex flex-col items-center justify-center text-center cursor-pointer transition-colors hover:border-primary/50 hover:bg-card/50 group"
                  onClick={() => fileInputRef.current?.click()}
                  onDragOver={(e) => {
                    e.preventDefault();
                    e.currentTarget.classList.add(
                      "border-primary",
                      "bg-card/80",
                    );
                  }}
                  onDragLeave={(e) =>
                    e.currentTarget.classList.remove(
                      "border-primary",
                      "bg-card/80",
                    )
                  }
                  onDrop={(e) => {
                    e.preventDefault();
                    e.currentTarget.classList.remove(
                      "border-primary",
                      "bg-card/80",
                    );
                    const file = e.dataTransfer?.files[0];
                    if (file) {
                      const dt = new DataTransfer();
                      dt.items.add(file);
                      if (fileInputRef.current) {
                        fileInputRef.current.files = dt.files;
                      }
                      handleFileUpload({ target: { files: [file] } });
                    }
                  }}
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
                  onChange={handleFileUpload}
                />
              </>
            ) : (
              <>
                <div
                  className="border-2 border-dashed border-border bg-background/50 rounded-xl p-12 flex flex-col items-center justify-center text-center cursor-pointer transition-colors hover:border-primary/50 hover:bg-card/50 group"
                  onClick={() => pdfInputRef.current?.click()}
                  onDragOver={(e) => {
                    e.preventDefault();
                    e.currentTarget.classList.add(
                      "border-primary",
                      "bg-card/80",
                    );
                  }}
                  onDragLeave={(e) =>
                    e.currentTarget.classList.remove(
                      "border-primary",
                      "bg-card/80",
                    )
                  }
                  onDrop={(e) => {
                    e.preventDefault();
                    e.currentTarget.classList.remove(
                      "border-primary",
                      "bg-card/80",
                    );
                    const file = e.dataTransfer?.files[0];
                    if (file) {
                      const dt = new DataTransfer();
                      dt.items.add(file);
                      if (pdfInputRef.current) {
                        pdfInputRef.current.files = dt.files;
                      }
                      handlePdfUpload({ target: { files: [file] } });
                    }
                  }}
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
                  onChange={handlePdfUpload}
                />
                <div className="mt-4 flex flex-col gap-1.5">
                  <Label className="text-muted-foreground">Extractor</Label>
                  <Select value={extractor} onValueChange={setExtractor}>
                    <SelectTrigger className="w-full h-10 bg-background">
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
                  <Label className="text-muted-foreground">
                    Password (if the PDF is protected)
                  </Label>
                  <Input
                    type="password"
                    className="h-10 bg-background"
                    placeholder="Optional"
                    value={pdfPassword}
                    onChange={(e) => setPdfPassword(e.target.value)}
                  />
                </div>
              </>
            )}
          </div>
        )}

        {/* Step 3: Column Mapping */}
        {step === 3 && (
          <div
            className="bg-card border border-border rounded-xl p-6"
            style={{ maxWidth: "800px" }}
          >
            <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-4">
              <h3 className="text-lg font-bold text-foreground">
                Map CSV Columns
              </h3>
              <div className="flex flex-col sm:flex-row sm:items-center gap-3">
                <div className="flex items-center gap-2">
                  <Label
                    htmlFor="import-date-format"
                    className="text-xs font-semibold text-muted-foreground whitespace-nowrap"
                  >
                    Date Format
                  </Label>
                  <Select value={dateFormat} onValueChange={setDateFormat}>
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
                    className={
                      amountMode === "single" ? "" : "text-muted-foreground"
                    }
                    onClick={() => setAmountMode("single")}
                  >
                    Single Amount
                  </Button>
                  <Button
                    size="sm"
                    variant={amountMode === "separate" ? "default" : "outline"}
                    className={
                      amountMode === "separate" ? "" : "text-muted-foreground"
                    }
                    onClick={() => setAmountMode("separate")}
                  >
                    Debit / Credit
                  </Button>
                </div>
              </div>
            </div>

            <p className="text-sm text-muted-foreground mb-6">
              Select which CSV column supplies each field below. Columns you
              don't map are ignored.
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
                            updateMapping(f.key, v === "none" ? "" : v)
                          }
                        >
                          <SelectTrigger className="mt-1 w-full h-10 bg-card">
                            <SelectValue
                              placeholder={
                                f.required
                                  ? "— Select a column —"
                                  : "— Not mapped —"
                              }
                            />
                          </SelectTrigger>
                          <SelectContent>
                            <SelectItem value="none">
                              {f.required
                                ? "— Select a column —"
                                : "— Not mapped —"}
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
                {mappingErrors.map((err, i) => (
                  <div
                    key={i}
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
                <Table className="min-w-max">
                  <TableHeader>
                    <TableRow className="bg-background hover:bg-background">
                      {csvHeaders.map((h) => (
                        <TableHead
                          key={h}
                          className="py-2.5 px-4 h-auto text-xs font-semibold text-muted-foreground bg-background border-b border-border whitespace-nowrap"
                        >
                          {h}
                          {csvTarget[h] && (
                            <div className="text-[10px] font-medium text-primary mt-0.5 tracking-wide">
                              → {csvTarget[h].toUpperCase()}
                            </div>
                          )}
                        </TableHead>
                      ))}
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {csvData.slice(0, 5).map((row, i) => (
                      <TableRow key={i} className="hover:bg-accent/30">
                        {csvHeaders.map((h) => (
                          <TableCell
                            key={h}
                            className={`py-2 px-4 text-xs max-w-37.5 overflow-hidden text-ellipsis whitespace-nowrap ${csvTarget[h] ? "text-muted-foreground" : "opacity-40 text-muted-foreground"}`}
                          >
                            {row[h]}
                          </TableCell>
                        ))}
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}

            <div className="pt-5 mt-6 border-t border-border flex justify-between gap-4">
              <Button variant="outline" onClick={() => setStep(2)}>
                Back
              </Button>
              <Button
                size="lg"
                className="px-5"
                disabled={mappingErrors.length > 0}
                onClick={() => setStep(4)}
              >
                Preview Transactions <ChevronRight size={16} />
              </Button>
            </div>
          </div>
        )}

        {/* Step 4: Preview & Confirm Import */}
        {step === 4 && (
          <div className="bg-card border border-border rounded-xl p-6">
            <div className="flex flex-col sm:flex-row justify-between sm:items-end gap-2 mb-4">
              <h3 className="text-xl font-bold text-foreground">
                Preview — {parsedTransactions.length} transactions
              </h3>
              {!statementTxns && csvData && (
                <div className="text-sm text-muted-foreground font-medium">
                  {csvData.length - parsedTransactions.length} rows skipped
                  (empty/invalid)
                </div>
              )}
            </div>

            {selectedAccountType === "credit_card" && (
              <div className="mb-5 p-4 bg-background border border-border rounded-lg">
                <div className="flex flex-col gap-1.5">
                  <Label className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider">
                    Billing Cycle — attach all imported transactions
                  </Label>
                  <Select
                    value={importBillingCycleId || "auto"}
                    onValueChange={(v) =>
                      setImportBillingCycleId(v === "auto" ? "" : v)
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
                    Leave on "Auto" to attach each transaction to the cycle
                    matching its date, or pick a cycle to force every imported
                    transaction into it.
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
                  <Select value={extractor} onValueChange={setExtractor}>
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
                    onValueChange={setPdfDateFormat}
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
                  onClick={() => parsePdf(pdfFile, extractor)}
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
                  Parsing didn't look right? Try a different extractor or date
                  format to reprocess the same file.
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
                <AlertTriangle
                  size={18}
                  className="text-amber-500 shrink-0 mt-0.5"
                />
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

            <div className="border border-border rounded-lg overflow-y-auto max-h-125 bg-background">
              <Table className="min-w-150">
                <TableHeader className="sticky top-0 bg-card z-10 shadow-[0_1px_0_var(--tw-shadow-color)] shadow-border">
                  <TableRow className="hover:bg-card">
                    <TableHead className="py-3 px-4 h-auto text-xs font-semibold uppercase tracking-wider text-muted-foreground w-28">
                      Date
                    </TableHead>
                    <TableHead className="py-3 px-4 h-auto text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                      Description
                    </TableHead>
                    <TableHead className="py-3 px-4 h-auto text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                      Payee
                    </TableHead>
                    <TableHead className="py-3 px-4 h-auto text-xs font-semibold uppercase tracking-wider text-muted-foreground w-24">
                      Type
                    </TableHead>
                    <TableHead className="py-3 px-4 h-auto text-xs font-semibold uppercase tracking-wider text-muted-foreground text-right w-32">
                      Amount
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {parsedTransactions.slice(0, 100).map((t, i) => (
                    <TableRow key={i} className="hover:bg-card/50">
                      <TableCell className="py-2.5 px-4 text-sm text-muted-foreground whitespace-nowrap">
                        {t.date}
                      </TableCell>
                      <TableCell className="py-2.5 px-4 text-sm text-foreground max-w-50 overflow-hidden text-ellipsis whitespace-nowrap">
                        {t.description}
                      </TableCell>
                      <TableCell className="py-2.5 px-4 text-sm text-muted-foreground max-w-37.5 overflow-hidden text-ellipsis whitespace-nowrap">
                        {t.payeeId ? (
                          <span className="text-primary font-medium">
                            {payees.find((p) => p.id === t.payeeId)?.name}
                          </span>
                        ) : (
                          <span className="opacity-30 italic">Not found</span>
                        )}
                      </TableCell>
                      <TableCell className="py-2.5 px-4">
                        <Badge
                          variant="outline"
                          className={`${t.type === "debit" ? "bg-destructive/10 text-destructive border-destructive/30" : "bg-emerald-500/10 text-emerald-500 border-emerald-500/20"}`}
                        >
                          {t.type}
                        </Badge>
                      </TableCell>
                      <TableCell className="py-2.5 px-4 text-right font-medium whitespace-nowrap">
                        <span
                          className={
                            t.type === "debit"
                              ? "text-destructive"
                              : "text-emerald-500"
                          }
                        >
                          {t.type === "debit" ? "−" : "+"}
                          {formatCurrency(t.amount)}
                        </span>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>

            {parsedTransactions.length > 100 && (
              <p className="text-sm text-center text-muted-foreground mt-4">
                Showing first 100 of {parsedTransactions.length} transactions
              </p>
            )}

            <div className="pt-5 mt-6 border-t border-border flex justify-between gap-4">
              <Button
                variant="outline"
                onClick={() => setStep(statementTxns ? 2 : 3)}
              >
                {statementTxns ? "Back to Upload" : "Back to Mapping"}
              </Button>
              <div className="flex gap-3">
                <Button
                  variant="outline"
                  className="bg-muted text-primary border-primary/30"
                  onClick={runValidation}
                  disabled={validating || parsedTransactions.length === 0}
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
                  onClick={handleImport}
                  disabled={importing || parsedTransactions.length === 0}
                >
                  {importing ? (
                    <div className="flex gap-2 items-center">
                      <Spinner className="size-4" /> Importing...
                    </div>
                  ) : (
                    <>Import {parsedTransactions.length} Transactions</>
                  )}
                </Button>
              </div>
            </div>
          </div>
        )}

        {/* Step 5: Done */}
        {step === 5 && importResult && (
          <div className="bg-card border border-border rounded-xl p-10 max-w-125 text-center mx-auto mt-10">
            <div className="w-16 h-16 rounded-full bg-emerald-500/15 flex items-center justify-center mx-auto mb-6 text-emerald-500 shadow-[0_0_20px_rgba(16,185,129,0.2)]">
              <Check size={32} />
            </div>
            <h2 className="text-2xl font-bold text-foreground mb-2">
              Import Complete!
            </h2>
            <p className="text-muted-foreground mb-8 max-w-[80%] mx-auto leading-relaxed">
              <span className="font-semibold text-foreground">
                {importResult.imported}
              </span>{" "}
              of {importResult.total} transactions
              {importResult.duplicates > 0
                ? ` imported (${importResult.duplicates} duplicates skipped).`
                : " imported."}
            </p>
            <div className="flex flex-col sm:flex-row gap-4 justify-center">
              <Button
                variant="outline"
                className="px-6"
                onClick={() => {
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
                  setPdfPassword("");
                  setPdfDateFormat("auto");
                  setPdfFile(null);
                  setExistingRefresh((k) => k + 1);
                }}
              >
                Import Another
              </Button>
              <Button
                size="lg"
                className="px-6"
                onClick={() => (window.location.href = "/transactions")}
              >
                View Transactions
              </Button>
              <Button
                variant="outline"
                className="px-6"
                onClick={() => (window.location.href = "/linking")}
              >
                Transfer Suggestions
              </Button>
            </div>
          </div>
        )}

        {/* Duplicate handling dialog */}
        <Dialog open={dupDialogOpen} onOpenChange={setDupDialogOpen}>
          <DialogContent className="max-w-md sm:max-w-md">
            <DialogHeader>
              <div className="flex items-center gap-2.5 text-amber-400">
                <AlertTriangle size={20} />
                <DialogTitle className="text-lg font-bold">
                  Duplicate transactions found
                </DialogTitle>
              </div>
              <DialogDescription>
                {dupCount} of the {parsedTransactions.length} transactions
                match an existing transaction or repeat within this file.
              </DialogDescription>
            </DialogHeader>
            <ul className="text-sm text-muted-foreground list-disc pl-5 space-y-1">
              {existingDupCount > 0 && (
                <li>
                  {existingDupCount} already{" "}
                  {existingDupCount === 1 ? "exists in" : "exist in"} this
                  account
                </li>
              )}
              {inFileDupCount > 0 && (
                <li>
                  {inFileDupCount} repeat{inFileDupCount === 1 ? "s" : ""}{" "}
                  within this file
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
                onClick={() => runImport("skip")}
                disabled={importing}
              >
                Skip duplicates
              </Button>
              <Button
                variant="outline"
                size="lg"
                className="w-full"
                onClick={() => runImport("keep")}
                disabled={importing}
              >
                Keep all (import everything)
              </Button>
              <Button
                variant="ghost"
                size="lg"
                className="w-full"
                onClick={() => setDupDialogOpen(false)}
                disabled={importing}
              >
                Cancel
              </Button>
            </div>
          </DialogContent>
        </Dialog>

        {/* Validation results dialog */}
        {validationResult && (
          <Dialog
            open
            onOpenChange={(open) => {
              if (!open) setValidationResult(null);
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
                    {accounts.find((a) => a.id === selectedAccount)?.name ||
                      "this account"}
                  </span>
                  . Nothing was imported.
                </DialogDescription>
              </DialogHeader>

              {/* Summary */}
              <div className="grid grid-cols-3 gap-3">
                <div className="p-3 bg-background border border-border rounded-lg text-center">
                  <div className="text-2xl font-bold text-foreground">
                    {validationResult.total}
                  </div>
                  <div className="text-xs text-muted-foreground mt-0.5">
                    Total
                  </div>
                </div>
                <div className="p-3 bg-amber-500/10 border border-amber-500/25 rounded-lg text-center">
                  <div className="text-2xl font-bold text-amber-400">
                    {validationResult.existingCount}
                  </div>
                  <div className="text-xs text-amber-500/80 mt-0.5">
                    Already exist
                  </div>
                </div>
                <div className="p-3 bg-emerald-500/10 border border-emerald-500/25 rounded-lg text-center">
                  <div className="text-2xl font-bold text-emerald-400">
                    {validationResult.missingCount}
                  </div>
                  <div className="text-xs text-emerald-500/80 mt-0.5">New</div>
                </div>
              </div>

              {/* Per-transaction list */}
              <div className="border border-border rounded-lg overflow-y-auto flex-1 min-h-0 bg-background">
                <Table className="min-w-150">
                  <TableHeader className="sticky top-0 bg-card z-10 shadow-[0_1px_0_var(--tw-shadow-color)] shadow-border">
                    <TableRow className="hover:bg-card">
                      <TableHead className="py-3 px-4 h-auto text-xs font-semibold uppercase tracking-wider text-muted-foreground w-28">
                        Date
                      </TableHead>
                      <TableHead className="py-3 px-4 h-auto text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                        Description
                      </TableHead>
                      <TableHead className="py-3 px-4 h-auto text-xs font-semibold uppercase tracking-wider text-muted-foreground w-24">
                        Type
                      </TableHead>
                      <TableHead className="py-3 px-4 h-auto text-xs font-semibold uppercase tracking-wider text-muted-foreground text-right w-32">
                        Amount
                      </TableHead>
                      <TableHead className="py-3 px-4 h-auto text-xs font-semibold uppercase tracking-wider text-muted-foreground w-32">
                        Status
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {validationResult.results.map((r) => (
                      <TableRow key={r.index} className="hover:bg-card/50">
                        <TableCell className="py-2.5 px-4 text-sm text-muted-foreground whitespace-nowrap">
                          {r.date}
                        </TableCell>
                        <TableCell className="py-2.5 px-4 text-sm text-foreground max-w-50 overflow-hidden text-ellipsis whitespace-nowrap">
                          {r.description}
                        </TableCell>
                        <TableCell className="py-2.5 px-4">
                          <Badge
                            variant="outline"
                            className={`${r.type === "debit" ? "bg-destructive/10 text-destructive border-destructive/30" : "bg-emerald-500/10 text-emerald-500 border-emerald-500/20"}`}
                          >
                            {r.type}
                          </Badge>
                        </TableCell>
                        <TableCell className="py-2.5 px-4 text-right font-medium whitespace-nowrap">
                          <span
                            className={
                              r.type === "debit"
                                ? "text-destructive"
                                : "text-emerald-500"
                            }
                          >
                            {r.type === "debit" ? "−" : "+"}
                            {formatCurrency(r.amount)}
                          </span>
                        </TableCell>
                        <TableCell className="py-2.5 px-4">
                          {r.exists ? (
                            <Badge
                              variant="outline"
                              className="bg-amber-500/10 text-amber-400 border-amber-500/25"
                            >
                              <CheckCircle2 size={12} /> Already exists
                            </Badge>
                          ) : (
                            <Badge
                              variant="outline"
                              className="bg-emerald-500/10 text-emerald-400 border-emerald-500/25"
                            >
                              <PlusCircle size={12} /> New
                            </Badge>
                          )}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>

              <DialogFooter>
                <Button
                  variant="outline"
                  onClick={() => setValidationResult(null)}
                >
                  Close
                </Button>
              </DialogFooter>
            </DialogContent>
          </Dialog>
        )}
      </div>
    </>
  );
}

// Pure helpers exported for unit testing. They are also used internally by the
// component above (date/amount parsing, column mapping, duplicate detection).
export {
  targetFieldsFor,
  parseDateExplicit,
  parseDateAuto,
  parseDate,
  parseAmount,
  getMappingErrors,
  fingerprintOf,
  apiDate,
  buildParsedTransactions,
  autoDetectMapping,
};