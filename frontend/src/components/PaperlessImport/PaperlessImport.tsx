import {
  useState,
  useEffect,
  useMemo,
  useRef,
  type Dispatch,
  type SetStateAction,
} from "react";
import {
  RefreshCw,
  Loader2,
  FileText,
  Check,
  AlertCircle,
  Search,
  Eye,
  X,
  ChevronDown,
  Plus,
  Minus,
  ShieldCheck,
  CheckCircle2,
  PlusCircle,
} from "lucide-react";
import { Checkbox } from "@/components/ui/checkbox";
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
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import api from "../../api/client";
import { formatCurrency, formatDateOnly } from "../../utils/formatters";
import type {
  Account,
  PaperlessDocument,
  StatementExtractor,
  ImportTransaction,
  ValidateTransactionsResponse,
} from "../../types";

interface MultiFilterProps {
  label: string;
  options: string[];
  map: Record<string, string>;
  onSet: (value: string, mode: string | null) => void;
}

function MultiFilter({ label, options, map, onSet }: MultiFilterProps) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const onClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node))
        setOpen(false);
    };
    document.addEventListener("mousedown", onClick);
    return () => document.removeEventListener("mousedown", onClick);
  }, []);

  const entries = Object.entries(map);
  const includeCount = entries.filter(([, m]) => m === "inc").length;
  const excludeCount = entries.filter(([, m]) => m === "exc").length;

  let selectedLabel;
  if (includeCount === 0 && excludeCount === 0) {
    selectedLabel = `All ${label.toLowerCase()}`;
  } else if (excludeCount === 0) {
    selectedLabel = [...entries]
      .filter(([, m]) => m === "inc")
      .map(([k]) => k)
      .sort()
      .join(", ");
  } else if (includeCount === 0) {
    selectedLabel = `Not: ${[...entries]
      .filter(([, m]) => m === "exc")
      .map(([k]) => k)
      .sort()
      .join(", ")}`;
  } else {
    selectedLabel = `+${includeCount} / -${excludeCount}`;
  }

  return (
    <div ref={ref} className="relative">
      <Button
        type="button"
        variant="outline"
        onClick={() => setOpen((o) => !o)}
        className={`w-full justify-between gap-2 px-3 py-2 h-auto rounded-lg text-sm ${
          entries.length > 0
            ? "border-primary/60 dark:border-primary/60"
            : "border-border"
        }`}
      >
        <span className="truncate text-left">{selectedLabel}</span>
        <ChevronDown
          size={14}
          className={`shrink-0 text-muted-foreground transition-transform ${open ? "rotate-180" : ""}`}
        />
      </Button>

      {open && (
        <div className="absolute z-20 mt-1 w-full min-w-55 bg-popover text-popover-foreground border border-border rounded-lg shadow-xl overflow-hidden">
          <div className="px-3 py-2 border-b border-border text-xs font-semibold text-muted-foreground">
            {label} — <span className="text-primary">+ include</span> ·{" "}
            <span className="text-destructive">− exclude</span>
          </div>
          <div className="max-h-52 overflow-y-auto">
            {options.length === 0 ? (
              <div className="px-3 py-2 text-xs text-muted-foreground">
                No options
              </div>
            ) : (
              options.map((opt) => {
                const mode = map[opt];
                return (
                  <div
                    key={opt}
                    className={`flex items-center justify-between gap-2 px-3 py-1.5 text-sm transition-colors ${
                      mode === "inc"
                        ? "bg-primary/10 text-primary"
                        : mode === "exc"
                          ? "bg-destructive/10 text-destructive"
                          : "text-foreground hover:bg-accent"
                    }`}
                  >
                    <span className="truncate flex-1">{opt}</span>
                    <div className="flex items-center gap-1 shrink-0">
                      <Button
                        type="button"
                        size="icon-xs"
                        variant="outline"
                        onClick={() =>
                          onSet(opt, mode === "inc" ? null : "inc")
                        }
                        title="Include (match)"
                        className={`rounded-md ${
                          mode === "inc"
                            ? "bg-primary hover:bg-primary border-primary text-primary-foreground hover:text-primary-foreground"
                            : "text-muted-foreground border-border hover:bg-accent hover:text-foreground"
                        }`}
                      >
                        <Plus size={13} />
                      </Button>
                      <Button
                        type="button"
                        size="icon-xs"
                        variant="outline"
                        onClick={() =>
                          onSet(opt, mode === "exc" ? null : "exc")
                        }
                        title="Exclude (skip)"
                        className={`rounded-md ${
                          mode === "exc"
                            ? "bg-destructive hover:bg-destructive border-destructive text-destructive-foreground hover:text-destructive-foreground"
                            : "text-muted-foreground border-border hover:bg-accent hover:text-foreground"
                        }`}
                      >
                        <Minus size={13} />
                      </Button>
                    </div>
                  </div>
                );
              })
            )}
          </div>
        </div>
      )}
    </div>
  );
}

const DATE_FORMAT_OPTIONS = [
  { value: "auto", label: "Auto-detect" },
  { value: "DD/MM/YYYY", label: "DD/MM/YYYY" },
  { value: "MM/DD/YYYY", label: "MM/DD/YYYY" },
  { value: "DD/MM/YY", label: "DD/MM/YY" },
  { value: "YYYY-MM-DD", label: "YYYY-MM-DD" },
  { value: "DD Mon YYYY", label: "DD Mon YYYY" },
];

interface FilePreview {
  url: string;
  title: string;
}

interface ImportPreview {
  title: string;
  transactions: ImportTransaction[];
  documentIds: number[];
}

export default function PaperlessImport() {
  const [configured, setConfigured] = useState(false);
  const [loadingConfig, setLoadingConfig] = useState(true);

  const [accounts, setAccounts] = useState<Account[]>([]);
  const [selectedAccount, setSelectedAccount] = useState("");

  const [extractors, setExtractors] = useState<StatementExtractor[]>([]);
  const [extractor, setExtractor] = useState("sbi_cc");
  const [password, setPassword] = useState("");
  const [dateFormat, setDateFormat] = useState("auto");
  const [tagOnImport, setTagOnImport] = useState(false);
  const [tagLabel, setTagLabel] = useState("");

  const [documents, setDocuments] = useState<PaperlessDocument[]>([]);
  const [loadingDocs, setLoadingDocs] = useState(false);
  const [selected, setSelected] = useState<Set<number>>(new Set());

  // Filters
  const [search, setSearch] = useState("");
  const [correspondentMap, setCorrespondentMap] = useState<
    Record<string, string>
  >({});
  const [documentTypeMap, setDocumentTypeMap] = useState<
    Record<string, string>
  >({});
  const [tagMap, setTagMap] = useState<Record<string, string>>({});

  // File preview (blob URL of the original PDF)
  const [filePreview, setFilePreview] = useState<FilePreview | null>(null);
  const [loadingFileId, setLoadingFileId] = useState<number | null>(null);

  const [preview, setPreview] = useState<ImportPreview | null>(null);
  const [parsing, setParsing] = useState(false);
  const [importing, setImporting] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");

  // Validation (read-only duplicate check against the selected account)
  const [validating, setValidating] = useState(false);
  const [validationResult, setValidationResult] =
    useState<ValidateTransactionsResponse | null>(null);

  useEffect(() => {
    api
      .getPaperlessSettings()
      .then((s) => {
        setConfigured(Boolean(s.paperlessUrl && s.hasToken));
        setTagLabel(s.paperlessTag || "");
      })
      .catch(() => setConfigured(false))
      .finally(() => setLoadingConfig(false));
  }, []);

  useEffect(() => {
    if (!configured) return;
    api.getAccounts().then(setAccounts).catch(console.error);
    api
      .getStatementExtractors()
      .then((res) => {
        const list = res?.extractors || [];
        setExtractors(list);
        if (list.length > 0) setExtractor(list[0].name);
      })
      .catch(console.error);
    loadDocuments();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [configured]);

  const loadDocuments = async () => {
    setLoadingDocs(true);
    setError("");
    try {
      const res = await api.getPaperlessDocuments();
      setDocuments(res?.documents || []);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setLoadingDocs(false);
    }
  };

  const toggle = (id: number) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const setFilter =
    (setter: Dispatch<SetStateAction<Record<string, string>>>) =>
    (value: string, mode: string | null) => {
      setter((prev) => {
        const next = { ...prev };
        if (mode) next[value] = mode;
        else delete next[value];
        return next;
      });
    };

  // Build filter dropdown options from the loaded documents.
  const correspondentOptions = useMemo(() => {
    const set = new Set(documents.map((d) => d.correspondent).filter(Boolean));
    return [...set].sort();
  }, [documents]);

  const documentTypeOptions = useMemo(() => {
    const set = new Set(documents.map((d) => d.documentType).filter(Boolean));
    return [...set].sort();
  }, [documents]);

  const tagOptions = useMemo(() => {
    const set = new Set(documents.flatMap((d) => d.tags || []));
    return [...set].sort();
  }, [documents]);

  const applyFilter = (
    matchValue: string | string[],
    map: Record<string, string>,
  ) => {
    const inc = Object.keys(map).filter((k) => map[k] === "inc");
    const exc = Object.keys(map).filter((k) => map[k] === "exc");
    const docValues = Array.isArray(matchValue) ? matchValue : [matchValue];
    if (exc.length > 0 && docValues.some((v) => exc.includes(v))) return false;
    if (inc.length > 0) return docValues.some((v) => inc.includes(v));
    return true;
  };

  const filteredDocuments = useMemo(() => {
    const q = search.trim().toLowerCase();
    return documents.filter((d) => {
      const docTags = d.tags || [];

      if (!applyFilter(d.correspondent, correspondentMap)) return false;
      if (!applyFilter(d.documentType, documentTypeMap)) return false;
      if (!applyFilter(docTags, tagMap)) return false;

      if (!q) return true;
      const haystack = [
        d.title,
        d.correspondent,
        d.documentType,
        `#${d.id}`,
        ...docTags,
      ]
        .filter(Boolean)
        .join(" ")
        .toLowerCase();
      return haystack.includes(q);
    });
  }, [documents, search, correspondentMap, documentTypeMap, tagMap]);

  const openFilePreview = async (doc: PaperlessDocument) => {
    setLoadingFileId(doc.id);
    setError("");
    try {
      const blob = await api.getPaperlessDocumentFile(doc.id);
      const url = URL.createObjectURL(blob);
      setFilePreview({ url, title: doc.title || `Document #${doc.id}` });
    } catch (err) {
      setError("Failed to load document preview: " + (err as Error).message);
    } finally {
      setLoadingFileId(null);
    }
  };

  const closeFilePreview = () => {
    if (filePreview) {
      URL.revokeObjectURL(filePreview.url);
    }
    setFilePreview(null);
  };

  const importSelected = async () => {
    if (!selectedAccount) {
      setError("Please select a FinTrak account first.");
      return;
    }
    setParsing(true);
    setError("");
    setSuccess("");
    setPreview(null);

    const transactions: ImportTransaction[] = [];
    const titles: string[] = [];
    try {
      for (const id of selected) {
        const res = await api.importPaperlessDocument({
          documentId: id,
          extractor,
          password,
          dateFormat: dateFormat === "auto" ? "" : dateFormat,
        });
        const doc = documents.find((d) => d.id === id);
        titles.push(doc?.title || `Document #${id}`);
        transactions.push(...(res.transactions || []));
      }
      setPreview({
        title: titles.join(", "),
        transactions,
        documentIds: [...selected],
      });
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setParsing(false);
    }
  };

  const confirmImport = async () => {
    if (!preview || preview.transactions.length === 0) return;
    setImporting(true);
    setError("");
    setSuccess("");
    try {
      // The backend tags the source Paperless documents only after the
      // transactions have been committed, so the label is added only on a
      // successful import.
      await api.importTransactions({
        accountId: selectedAccount,
        transactions: preview.transactions,
        duplicateAction: "keep",
        paperlessDocumentIds: tagOnImport ? preview.documentIds || [] : [],
      });
      setSuccess(`Imported ${preview.transactions.length} transactions.`);
      setPreview(null);
      setSelected(new Set());
      loadDocuments();
    } catch (err) {
      setError("Import failed: " + (err as Error).message);
    } finally {
      setImporting(false);
    }
  };

  // Read-only check: ask the backend which of the parsed transactions already
  // exist in the selected account. Nothing is written.
  const runValidation = async () => {
    if (!preview || preview.transactions.length === 0) return;
    setValidating(true);
    setError("");
    try {
      const result = await api.validateTransactions({
        accountId: selectedAccount,
        transactions: preview.transactions,
      });
      setValidationResult(result);
    } catch (err) {
      setError("Validation failed: " + (err as Error).message);
    } finally {
      setValidating(false);
    }
  };

  const parsedCount = useMemo(
    () => preview?.transactions.length || 0,
    [preview],
  );

  if (loadingConfig) {
    return (
      <div className="flex-1 px-8 pt-6">
        <div className="text-sm text-muted-foreground">Loading...</div>
      </div>
    );
  }

  if (!configured) {
    return (
      <>
        <div className="shrink-0 px-8 pt-6">
          <h1 className="text-2xl font-bold mb-1">Paperless Import</h1>
          <p className="text-muted-foreground text-sm">
            Pull statements from Paperless-ngx
          </p>
        </div>
        <div className="flex-1 px-8 pb-8 pt-6">
          <div className="bg-card border border-border rounded-xl p-6 max-w-125">
            <div className="flex items-center gap-2 text-muted-foreground mb-2">
              <AlertCircle size={18} className="text-amber-500" />
              <h3 className="text-base font-semibold text-foreground">
                Paperless not configured
              </h3>
            </div>
            <p className="text-sm text-muted-foreground mb-4">
              Set a Paperless-ngx URL and API token in{" "}
              <span className="text-foreground">Settings</span> to enable
              pulling statements here.
            </p>
            <Button
              size="sm"
              onClick={() => (window.location.hash = "#/settings")}
            >
              Go to Settings
            </Button>
          </div>
        </div>
      </>
    );
  }

  return (
    <>
      <div className="shrink-0 px-8 pt-6">
        <h1 className="text-2xl font-bold mb-1">Paperless Import</h1>
        <p className="text-muted-foreground text-sm">
          Select statement documents to pull and import
        </p>
      </div>
      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto w-full space-y-6">
        {error && (
          <div className="bg-destructive/10 border border-destructive/30 text-destructive text-sm rounded-lg px-4 py-3">
            {error}
          </div>
        )}
        {success && (
          <div className="bg-emerald-500/10 border border-emerald-500/30 text-emerald-400 text-sm rounded-lg px-4 py-3">
            {success}
          </div>
        )}

        {/* Pull settings */}
        <div className="bg-card border border-border rounded-xl p-5">
          <h3 className="text-sm font-semibold text-foreground mb-4">
            Pull Configuration
          </h3>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
            <div className="flex flex-col gap-1.5">
              <Label className="text-xs font-medium text-muted-foreground">
                FinTrak Account
              </Label>
              <Select
                value={selectedAccount}
                onValueChange={setSelectedAccount}
              >
                <SelectTrigger className="w-full">
                  <SelectValue placeholder="Choose an account..." />
                </SelectTrigger>
                <SelectContent>
                  {accounts.map((a) => (
                    <SelectItem key={a.id} value={a.id}>
                      {a.name} ({a.accountTypeName}
                      {a.bank ? `, ${a.bank}` : ""})
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label className="text-xs font-medium text-muted-foreground">
                Extractor
              </Label>
              <Select value={extractor} onValueChange={setExtractor}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {extractors.map((ex) => (
                    <SelectItem key={ex.name} value={ex.name}>
                      {ex.display_name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label className="text-xs font-medium text-muted-foreground">
                Date Format
              </Label>
              <Select value={dateFormat} onValueChange={setDateFormat}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {DATE_FORMAT_OPTIONS.map((o) => (
                    <SelectItem key={o.value} value={o.value}>
                      {o.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label className="text-xs font-medium text-muted-foreground">
                Password (optional)
              </Label>
              <Input
                type="password"
                placeholder="Statement password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </div>
            <div className="flex items-end">
              <Label className="flex items-center gap-2 text-sm text-foreground cursor-pointer">
                <Checkbox
                  checked={tagOnImport}
                  onCheckedChange={(checked) =>
                    setTagOnImport(checked === true)
                  }
                />
                Tag imported docs as “{tagLabel || "fintrak"}”
              </Label>
            </div>
          </div>
        </div>

        {/* Documents */}
        <div className="bg-card border border-border rounded-xl p-5">
          <div className="flex items-center justify-between mb-4">
            <h3 className="text-sm font-semibold text-foreground">
              Documents ({filteredDocuments.length})
            </h3>
            <Button
              variant="outline"
              size="sm"
              onClick={loadDocuments}
              className="bg-muted text-foreground hover:bg-accent"
            >
              <RefreshCw size={14} /> Refresh
            </Button>
          </div>

          {/* Filters */}
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3 mb-4">
            <div className="relative">
              <Search
                size={14}
                className="absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground"
              />
              <Input
                type="text"
                placeholder="Search title, correspondent, tag..."
                className="pl-9"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
              />
            </div>
            <MultiFilter
              label="Correspondents"
              options={correspondentOptions}
              map={correspondentMap}
              onSet={setFilter(setCorrespondentMap)}
            />
            <MultiFilter
              label="Document Types"
              options={documentTypeOptions}
              map={documentTypeMap}
              onSet={setFilter(setDocumentTypeMap)}
            />
            <MultiFilter
              label="Tags"
              options={tagOptions}
              map={tagMap}
              onSet={setFilter(setTagMap)}
            />
          </div>

          {loadingDocs ? (
            <div className="flex items-center gap-2 text-sm text-muted-foreground py-6">
              <Spinner className="size-4" /> Loading documents...
            </div>
          ) : filteredDocuments.length === 0 ? (
            <div className="text-sm text-muted-foreground py-6">
              {documents.length === 0
                ? "No documents found in Paperless."
                : "No documents match the current filters."}
            </div>
          ) : (
            <div className="divide-y divide-border border border-border rounded-lg overflow-y-auto max-h-96 bg-background">
              {filteredDocuments.map((d) => (
                <div
                  key={d.id}
                  className="flex items-start gap-3 p-3 cursor-pointer hover:bg-card transition-colors"
                  onClick={() => toggle(d.id)}
                >
                  <Checkbox
                    checked={selected.has(d.id)}
                    className="mt-1 pointer-events-none"
                  />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2 text-sm font-medium text-foreground">
                      <FileText size={14} className="text-primary shrink-0" />
                      <span className="truncate">
                        {d.title || `Document #${d.id}`}
                      </span>
                    </div>
                    <div className="text-xs text-muted-foreground mt-0.5 truncate">
                      #{d.id}
                      {d.correspondent ? ` · ${d.correspondent}` : ""}
                      {d.documentType ? ` · ${d.documentType}` : ""}
                      {d.created
                        ? ` · Created ${formatDateOnly(new Date(d.created))}`
                        : ""}
                    </div>
                    {d.tags?.length > 0 && (
                      <div className="flex flex-wrap gap-1 mt-1.5">
                        {d.tags.map((tag) => (
                          <span
                            key={tag}
                            className="text-[10px] uppercase bg-muted text-muted-foreground px-1.5 py-0.5 rounded"
                          >
                            {tag}
                          </span>
                        ))}
                      </div>
                    )}
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={(e) => {
                      e.stopPropagation();
                      openFilePreview(d);
                    }}
                    disabled={loadingFileId === d.id}
                    className="shrink-0 gap-1.5 text-xs bg-muted text-muted-foreground hover:bg-accent hover:text-foreground"
                    title="Preview document"
                  >
                    {loadingFileId === d.id ? (
                      <Loader2 size={13} className="animate-spin" />
                    ) : (
                      <Eye size={13} />
                    )}
                    Preview
                  </Button>
                </div>
              ))}
            </div>
          )}

          <div className="flex items-center gap-3 mt-4">
            <Button
              onClick={importSelected}
              disabled={parsing || selected.size === 0}
              className="font-semibold"
            >
              {parsing ? (
                <Loader2 size={16} className="animate-spin" />
              ) : (
                <Check size={16} />
              )}
              {parsing
                ? "Parsing..."
                : `Fetch & Parse Selected (${selected.size})`}
            </Button>
          </div>
        </div>

        {/* Preview */}
        {preview && (
          <div className="bg-card border border-border rounded-xl p-5">
            <h3 className="text-sm font-semibold text-foreground mb-1">
              Preview — {preview.title}
            </h3>
            <p className="text-xs text-muted-foreground mb-3">
              {parsedCount} transaction(s) parsed. Review below before
              importing.
            </p>
            {preview.transactions.length === 0 ? (
              <div className="text-sm text-muted-foreground py-4">
                No transactions were parsed from these documents.
              </div>
            ) : (
              <div className="max-h-80 overflow-y-auto border border-border rounded-lg bg-background">
                <Table>
                  <TableHeader className="sticky top-0 bg-card text-left text-xs text-muted-foreground">
                    <TableRow className="hover:bg-transparent">
                      <TableHead className="h-auto px-4 py-2 font-medium">
                        Date
                      </TableHead>
                      <TableHead className="h-auto px-4 py-2 font-medium">
                        Description
                      </TableHead>
                      <TableHead className="h-auto px-4 py-2 font-medium">
                        Type
                      </TableHead>
                      <TableHead className="h-auto px-4 py-2 font-medium text-right">
                        Amount
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {preview.transactions.slice(0, 500).map((t, i) => (
                      <TableRow key={i}>
                        <TableCell className="px-4 py-2 text-muted-foreground whitespace-nowrap">
                          {t.date}
                        </TableCell>
                        <TableCell className="px-4 py-2 text-foreground whitespace-normal">
                          {t.description}
                        </TableCell>
                        <TableCell className="px-4 py-2 whitespace-nowrap">
                          <Badge
                            className={`uppercase text-[10px] ${t.type === "credit" ? "bg-emerald-500/10 text-emerald-400" : "bg-destructive/10 text-destructive"}`}
                          >
                            {t.type}
                          </Badge>
                        </TableCell>
                        <TableCell
                          className={`px-4 py-2 text-right font-medium whitespace-nowrap ${t.type === "credit" ? "text-emerald-400" : "text-destructive"}`}
                        >
                          {t.type === "credit" ? "+" : "−"}
                          {formatCurrency(t.amount)}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
            {parsedCount > 0 && (
              <div className="flex items-center gap-3 mt-4">
                <Button
                  variant="outline"
                  onClick={runValidation}
                  disabled={validating}
                  title="Check which of these transactions already exist in this account (no data is written)"
                  className="bg-muted text-primary border-primary/30 dark:border-primary/30 hover:bg-accent font-semibold"
                >
                  {validating ? (
                    <Loader2 size={16} className="animate-spin" />
                  ) : (
                    <ShieldCheck size={16} />
                  )}
                  {validating ? "Validating..." : "Validate"}
                </Button>
                <Button
                  onClick={confirmImport}
                  disabled={importing}
                  className="bg-emerald-500 hover:bg-emerald-600 text-white font-semibold"
                >
                  {importing ? (
                    <Loader2 size={16} className="animate-spin" />
                  ) : (
                    <Check size={16} />
                  )}
                  {importing
                    ? "Importing..."
                    : `Import ${parsedCount} transaction(s)`}
                </Button>
                <Button
                  variant="ghost"
                  onClick={() => setPreview(null)}
                  className="text-muted-foreground hover:text-foreground"
                >
                  Cancel
                </Button>
              </div>
            )}
          </div>
        )}
      </div>

      {/* Validation results dialog */}
      {validationResult && (
        <Dialog
          open={!!validationResult}
          onOpenChange={(open) => {
            if (!open) setValidationResult(null);
          }}
        >
          <DialogContent className="sm:max-w-2xl max-h-[85vh] flex flex-col">
            <DialogHeader className="flex flex-row items-center gap-2.5">
              <ShieldCheck size={20} className="text-primary" />
              <DialogTitle className="text-lg font-bold">
                Validation Results
              </DialogTitle>
            </DialogHeader>
            <DialogDescription>
              Checked against{" "}
              <span className="font-medium text-foreground">
                {accounts.find((a) => a.id === selectedAccount)?.name ||
                  "this account"}
              </span>
              . Nothing was imported.
            </DialogDescription>

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
            <div className="border border-border rounded-lg overflow-y-auto flex-1 bg-background">
              <Table>
                <TableHeader className="sticky top-0 bg-card text-left text-xs text-muted-foreground">
                  <TableRow className="hover:bg-transparent">
                    <TableHead className="h-auto px-4 py-2 font-medium w-28">
                      Date
                    </TableHead>
                    <TableHead className="h-auto px-4 py-2 font-medium">
                      Description
                    </TableHead>
                    <TableHead className="h-auto px-4 py-2 font-medium w-24">
                      Type
                    </TableHead>
                    <TableHead className="h-auto px-4 py-2 font-medium text-right w-32">
                      Amount
                    </TableHead>
                    <TableHead className="h-auto px-4 py-2 font-medium w-32">
                      Status
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {validationResult.results.map((r) => (
                    <TableRow key={r.index}>
                      <TableCell className="px-4 py-2 text-muted-foreground whitespace-nowrap">
                        {r.date}
                      </TableCell>
                      <TableCell className="px-4 py-2 text-foreground max-w-50 overflow-hidden text-ellipsis whitespace-nowrap">
                        {r.description}
                      </TableCell>
                      <TableCell className="px-4 py-2 whitespace-nowrap">
                        <Badge
                          className={`uppercase text-[10px] ${r.type === "credit" ? "bg-emerald-500/10 text-emerald-400" : "bg-destructive/10 text-destructive"}`}
                        >
                          {r.type}
                        </Badge>
                      </TableCell>
                      <TableCell
                        className={`px-4 py-2 text-right font-medium whitespace-nowrap ${r.type === "credit" ? "text-emerald-400" : "text-destructive"}`}
                      >
                        {r.type === "credit" ? "+" : "−"}
                        {formatCurrency(r.amount)}
                      </TableCell>
                      <TableCell className="px-4 py-2 whitespace-nowrap">
                        {r.exists ? (
                          <Badge className="inline-flex items-center gap-1 bg-amber-500/10 text-amber-400 border border-amber-500/25">
                            <CheckCircle2 size={12} /> Already exists
                          </Badge>
                        ) : (
                          <Badge className="inline-flex items-center gap-1 bg-emerald-500/10 text-emerald-400 border border-emerald-500/25">
                            <PlusCircle size={12} /> New
                          </Badge>
                        )}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>

            <DialogFooter showCloseButton className="border-border" />
          </DialogContent>
        </Dialog>
      )}

      {/* File preview modal */}
      {filePreview && (
        <Dialog
          open={!!filePreview}
          onOpenChange={(open) => {
            if (!open) closeFilePreview();
          }}
        >
          <DialogContent
            showCloseButton={false}
            className="sm:max-w-4xl h-[85vh] flex flex-col gap-0 p-0 overflow-hidden"
          >
            <div className="flex items-center justify-between px-5 py-3 border-b border-border shrink-0">
              <div className="flex items-center gap-2 text-sm font-medium text-foreground min-w-0">
                <FileText size={16} className="text-primary shrink-0" />
                <span className="truncate">{filePreview.title}</span>
              </div>
              <div className="flex items-center gap-3 shrink-0">
                <Button
                  variant="outline"
                  size="sm"
                  asChild
                  className="bg-muted text-foreground hover:bg-accent"
                >
                  <a
                    href={filePreview.url}
                    download={
                      filePreview.title.replace(/[^\w.-]+/g, "_") + ".pdf"
                    }
                  >
                    Download
                  </a>
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  onClick={closeFilePreview}
                  className="text-muted-foreground hover:text-foreground"
                >
                  <X size={18} />
                </Button>
              </div>
            </div>
            <iframe
              src={filePreview.url}
              title="Document preview"
              className="flex-1 w-full bg-white"
            />
          </DialogContent>
        </Dialog>
      )}
    </>
  );
}