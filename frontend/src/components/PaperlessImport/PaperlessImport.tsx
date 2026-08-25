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
import Checkbox from "../Checkbox/Checkbox";
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
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className={`w-full flex items-center justify-between gap-2 px-3 py-2 bg-slate-950 border rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 ${
          entries.length > 0 ? "border-cyan-500/60" : "border-slate-800"
        }`}
      >
        <span className="truncate text-left">{selectedLabel}</span>
        <ChevronDown
          size={14}
          className={`shrink-0 text-slate-500 transition-transform ${open ? "rotate-180" : ""}`}
        />
      </button>

      {open && (
        <div className="absolute z-20 mt-1 w-full min-w-55 bg-slate-900 border border-slate-700 rounded-lg shadow-xl overflow-hidden">
          <div className="px-3 py-2 border-b border-slate-800 text-xs font-semibold text-slate-400">
            {label} — <span className="text-cyan-400">+ include</span> ·{" "}
            <span className="text-red-400">− exclude</span>
          </div>
          <div className="max-h-52 overflow-y-auto">
            {options.length === 0 ? (
              <div className="px-3 py-2 text-xs text-slate-500">No options</div>
            ) : (
              options.map((opt) => {
                const mode = map[opt];
                return (
                  <div
                    key={opt}
                    className={`flex items-center justify-between gap-2 px-3 py-1.5 text-sm transition-colors ${
                      mode === "inc"
                        ? "bg-cyan-500/10 text-cyan-300"
                        : mode === "exc"
                          ? "bg-red-500/10 text-red-300"
                          : "text-slate-200 hover:bg-slate-800"
                    }`}
                  >
                    <span className="truncate flex-1">{opt}</span>
                    <div className="flex items-center gap-1 shrink-0">
                      <button
                        type="button"
                        onClick={() =>
                          onSet(opt, mode === "inc" ? null : "inc")
                        }
                        title="Include (match)"
                        className={`w-6 h-6 inline-flex items-center justify-center rounded-md border transition-colors ${
                          mode === "inc"
                            ? "bg-cyan-500 text-slate-950 border-cyan-500"
                            : "text-slate-400 border-slate-700 hover:bg-slate-700 hover:text-white"
                        }`}
                      >
                        <Plus size={13} />
                      </button>
                      <button
                        type="button"
                        onClick={() =>
                          onSet(opt, mode === "exc" ? null : "exc")
                        }
                        title="Exclude (skip)"
                        className={`w-6 h-6 inline-flex items-center justify-center rounded-md border transition-colors ${
                          mode === "exc"
                            ? "bg-red-500 text-white border-red-500"
                            : "text-slate-400 border-slate-700 hover:bg-slate-700 hover:text-white"
                        }`}
                      >
                        <Minus size={13} />
                      </button>
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
        <div className="text-sm text-slate-500">Loading...</div>
      </div>
    );
  }

  if (!configured) {
    return (
      <>
        <div className="shrink-0 px-8 pt-6">
          <h1 className="text-2xl font-bold mb-1">Paperless Import</h1>
          <p className="text-slate-400 text-sm">
            Pull statements from Paperless-ngx
          </p>
        </div>
        <div className="flex-1 px-8 pb-8 pt-6">
          <div className="bg-slate-900 border border-slate-800 rounded-xl p-6 max-w-125">
            <div className="flex items-center gap-2 text-slate-400 mb-2">
              <AlertCircle size={18} className="text-amber-500" />
              <h3 className="text-base font-semibold text-slate-200">
                Paperless not configured
              </h3>
            </div>
            <p className="text-sm text-slate-500 mb-4">
              Set a Paperless-ngx URL and API token in{" "}
              <span className="text-slate-300">Settings</span> to enable pulling
              statements here.
            </p>
            <button
              onClick={() => (window.location.hash = "#/settings")}
              className="px-3 py-1.5 text-xs font-medium bg-cyan-500 text-white rounded hover:bg-cyan-600"
            >
              Go to Settings
            </button>
          </div>
        </div>
      </>
    );
  }

  return (
    <>
      <div className="shrink-0 px-8 pt-6">
        <h1 className="text-2xl font-bold mb-1">Paperless Import</h1>
        <p className="text-slate-400 text-sm">
          Select statement documents to pull and import
        </p>
      </div>
      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto w-full space-y-6">
        {error && (
          <div className="bg-red-500/10 border border-red-500/30 text-red-400 text-sm rounded-lg px-4 py-3">
            {error}
          </div>
        )}
        {success && (
          <div className="bg-emerald-500/10 border border-emerald-500/30 text-emerald-400 text-sm rounded-lg px-4 py-3">
            {success}
          </div>
        )}

        {/* Pull settings */}
        <div className="bg-slate-900 border border-slate-800 rounded-xl p-5">
          <h3 className="text-sm font-semibold text-slate-200 mb-4">
            Pull Configuration
          </h3>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
            <div className="flex flex-col gap-1.5">
              <label className="text-xs font-medium text-slate-400">
                FinTrak Account
              </label>
              <select
                className="px-3 py-2 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500"
                value={selectedAccount}
                onChange={(e) => setSelectedAccount(e.target.value)}
              >
                <option value="">Choose an account...</option>
                {accounts.map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name} ({a.accountTypeName}
                    {a.bank ? `, ${a.bank}` : ""})
                  </option>
                ))}
              </select>
            </div>
            <div className="flex flex-col gap-1.5">
              <label className="text-xs font-medium text-slate-400">
                Extractor
              </label>
              <select
                className="px-3 py-2 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500"
                value={extractor}
                onChange={(e) => setExtractor(e.target.value)}
              >
                {extractors.map((ex) => (
                  <option key={ex.name} value={ex.name}>
                    {ex.display_name}
                  </option>
                ))}
              </select>
            </div>
            <div className="flex flex-col gap-1.5">
              <label className="text-xs font-medium text-slate-400">
                Date Format
              </label>
              <select
                className="px-3 py-2 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500"
                value={dateFormat}
                onChange={(e) => setDateFormat(e.target.value)}
              >
                {DATE_FORMAT_OPTIONS.map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </select>
            </div>
            <div className="flex flex-col gap-1.5">
              <label className="text-xs font-medium text-slate-400">
                Password (optional)
              </label>
              <input
                type="password"
                className="px-3 py-2 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500"
                placeholder="Statement password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </div>
            <div className="flex items-end">
              <label className="flex items-center gap-2 text-sm text-slate-300 cursor-pointer">
                <Checkbox
                  checked={tagOnImport}
                  onChange={(checked) => setTagOnImport(checked)}
                />
                Tag imported docs as “{tagLabel || "fintrak"}”
              </label>
            </div>
          </div>
        </div>

        {/* Documents */}
        <div className="bg-slate-900 border border-slate-800 rounded-xl p-5">
          <div className="flex items-center justify-between mb-4">
            <h3 className="text-sm font-semibold text-slate-200">
              Documents ({filteredDocuments.length})
            </h3>
            <button
              onClick={loadDocuments}
              className="inline-flex items-center gap-2 px-3 py-1.5 bg-slate-800 text-slate-200 border border-slate-700 rounded-lg text-xs font-medium hover:bg-slate-700 transition-all"
            >
              <RefreshCw size={14} /> Refresh
            </button>
          </div>

          {/* Filters */}
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3 mb-4">
            <div className="relative">
              <Search
                size={14}
                className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-500"
              />
              <input
                type="text"
                placeholder="Search title, correspondent, tag..."
                className="w-full pl-9 pr-3 py-2 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500"
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
            <div className="flex items-center gap-2 text-sm text-slate-500 py-6">
              <Loader2 size={16} className="animate-spin" /> Loading
              documents...
            </div>
          ) : filteredDocuments.length === 0 ? (
            <div className="text-sm text-slate-500 py-6">
              {documents.length === 0
                ? "No documents found in Paperless."
                : "No documents match the current filters."}
            </div>
          ) : (
            <div className="divide-y divide-slate-800 border border-slate-800 rounded-lg overflow-y-auto max-h-96 bg-slate-950">
              {filteredDocuments.map((d) => (
                <div
                  key={d.id}
                  className="flex items-start gap-3 p-3 cursor-pointer hover:bg-slate-900 transition-colors"
                  onClick={() => toggle(d.id)}
                >
                  <Checkbox
                    checked={selected.has(d.id)}
                    className="mt-1 pointer-events-none"
                  />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2 text-sm font-medium text-slate-200">
                      <FileText size={14} className="text-cyan-500 shrink-0" />
                      <span className="truncate">
                        {d.title || `Document #${d.id}`}
                      </span>
                    </div>
                    <div className="text-xs text-slate-500 mt-0.5 truncate">
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
                            className="text-[10px] uppercase bg-slate-800 text-slate-400 px-1.5 py-0.5 rounded"
                          >
                            {tag}
                          </span>
                        ))}
                      </div>
                    )}
                  </div>
                  <button
                    type="button"
                    onClick={(e) => {
                      e.stopPropagation();
                      openFilePreview(d);
                    }}
                    disabled={loadingFileId === d.id}
                    className="shrink-0 inline-flex items-center gap-1.5 px-2.5 py-1.5 bg-slate-800 text-slate-300 border border-slate-700 rounded-lg text-xs font-medium hover:bg-slate-700 hover:text-white transition-all"
                    title="Preview document"
                  >
                    {loadingFileId === d.id ? (
                      <Loader2 size={13} className="animate-spin" />
                    ) : (
                      <Eye size={13} />
                    )}
                    Preview
                  </button>
                </div>
              ))}
            </div>
          )}

          <div className="flex items-center gap-3 mt-4">
            <button
              onClick={importSelected}
              disabled={parsing || selected.size === 0}
              className="inline-flex items-center gap-2 px-4 py-2 bg-cyan-500 text-slate-950 rounded-lg text-sm font-semibold hover:bg-cyan-600 disabled:opacity-50 transition-all"
            >
              {parsing ? (
                <Loader2 size={16} className="animate-spin" />
              ) : (
                <Check size={16} />
              )}
              {parsing
                ? "Parsing..."
                : `Fetch & Parse Selected (${selected.size})`}
            </button>
          </div>
        </div>

        {/* Preview */}
        {preview && (
          <div className="bg-slate-900 border border-slate-800 rounded-xl p-5">
            <h3 className="text-sm font-semibold text-slate-200 mb-1">
              Preview — {preview.title}
            </h3>
            <p className="text-xs text-slate-500 mb-3">
              {parsedCount} transaction(s) parsed. Review below before
              importing.
            </p>
            {preview.transactions.length === 0 ? (
              <div className="text-sm text-slate-500 py-4">
                No transactions were parsed from these documents.
              </div>
            ) : (
              <div className="max-h-80 overflow-y-auto border border-slate-800 rounded-lg bg-slate-950">
                <table className="w-full text-sm">
                  <thead className="sticky top-0 bg-slate-900 text-left text-xs text-slate-500">
                    <tr>
                      <th className="px-4 py-2 font-medium">Date</th>
                      <th className="px-4 py-2 font-medium">Description</th>
                      <th className="px-4 py-2 font-medium">Type</th>
                      <th className="px-4 py-2 font-medium text-right">
                        Amount
                      </th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-slate-800">
                    {preview.transactions.slice(0, 500).map((t, i) => (
                      <tr key={i} className="hover:bg-slate-900">
                        <td className="px-4 py-2 text-slate-400">{t.date}</td>
                        <td className="px-4 py-2 text-slate-200">
                          {t.description}
                        </td>
                        <td className="px-4 py-2">
                          <span
                            className={`uppercase text-[10px] px-1.5 py-0.5 rounded ${t.type === "credit" ? "bg-emerald-500/10 text-emerald-400" : "bg-red-500/10 text-red-400"}`}
                          >
                            {t.type}
                          </span>
                        </td>
                        <td
                          className={`px-4 py-2 text-right font-medium ${t.type === "credit" ? "text-emerald-400" : "text-red-400"}`}
                        >
                          {t.type === "credit" ? "+" : "−"}
                          {formatCurrency(t.amount)}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            {parsedCount > 0 && (
              <div className="flex items-center gap-3 mt-4">
                <button
                  onClick={runValidation}
                  disabled={validating}
                  title="Check which of these transactions already exist in this account (no data is written)"
                  className="inline-flex items-center gap-2 px-4 py-2 bg-slate-800 text-cyan-400 border border-cyan-500/30 rounded-lg text-sm font-semibold hover:bg-slate-700 disabled:opacity-50 transition-all"
                >
                  {validating ? (
                    <Loader2 size={16} className="animate-spin" />
                  ) : (
                    <ShieldCheck size={16} />
                  )}
                  {validating ? "Validating..." : "Validate"}
                </button>
                <button
                  onClick={confirmImport}
                  disabled={importing}
                  className="inline-flex items-center gap-2 px-4 py-2 bg-emerald-500 text-slate-950 rounded-lg text-sm font-semibold hover:bg-emerald-600 disabled:opacity-50 transition-all"
                >
                  {importing ? (
                    <Loader2 size={16} className="animate-spin" />
                  ) : (
                    <Check size={16} />
                  )}
                  {importing
                    ? "Importing..."
                    : `Import ${parsedCount} transaction(s)`}
                </button>
                <button
                  onClick={() => setPreview(null)}
                  className="px-4 py-2 text-sm font-medium text-slate-400 hover:text-white"
                >
                  Cancel
                </button>
              </div>
            )}
          </div>
        )}
      </div>

      {/* Validation results dialog */}
      {validationResult && (
        <div
          className="fixed inset-0 z-100 bg-black/70 backdrop-blur-sm flex items-center justify-center p-4 sm:p-6"
          onClick={() => setValidationResult(null)}
        >
          <div
            className="bg-slate-900 border border-slate-800 rounded-xl p-6 w-full max-w-2xl max-h-[85vh] flex flex-col"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex items-start justify-between mb-4">
              <div className="flex items-center gap-2.5">
                <ShieldCheck size={20} className="text-cyan-400" />
                <h3 className="text-lg font-bold text-slate-100">
                  Validation Results
                </h3>
              </div>
              <button
                className="text-slate-500 hover:text-slate-300 transition-colors"
                onClick={() => setValidationResult(null)}
                aria-label="Close"
              >
                <X size={18} />
              </button>
            </div>
            <p className="text-sm text-slate-400 mb-4">
              Checked against{" "}
              <span className="font-medium text-slate-200">
                {accounts.find((a) => a.id === selectedAccount)?.name ||
                  "this account"}
              </span>
              . Nothing was imported.
            </p>

            {/* Summary */}
            <div className="grid grid-cols-3 gap-3 mb-5">
              <div className="p-3 bg-slate-950 border border-slate-800 rounded-lg text-center">
                <div className="text-2xl font-bold text-slate-200">
                  {validationResult.total}
                </div>
                <div className="text-xs text-slate-500 mt-0.5">Total</div>
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
            <div className="border border-slate-800 rounded-lg overflow-y-auto flex-1 bg-slate-950">
              <table className="w-full text-sm">
                <thead className="sticky top-0 bg-slate-900 text-left text-xs text-slate-500">
                  <tr>
                    <th className="px-4 py-2 font-medium w-28">Date</th>
                    <th className="px-4 py-2 font-medium">Description</th>
                    <th className="px-4 py-2 font-medium w-24">Type</th>
                    <th className="px-4 py-2 font-medium text-right w-32">
                      Amount
                    </th>
                    <th className="px-4 py-2 font-medium w-32">Status</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-800">
                  {validationResult.results.map((r) => (
                    <tr key={r.index} className="hover:bg-slate-900">
                      <td className="px-4 py-2 text-slate-400 whitespace-nowrap">
                        {r.date}
                      </td>
                      <td className="px-4 py-2 text-slate-200 max-w-50 overflow-hidden text-ellipsis whitespace-nowrap">
                        {r.description}
                      </td>
                      <td className="px-4 py-2">
                        <span
                          className={`uppercase text-[10px] px-1.5 py-0.5 rounded ${r.type === "credit" ? "bg-emerald-500/10 text-emerald-400" : "bg-red-500/10 text-red-400"}`}
                        >
                          {r.type}
                        </span>
                      </td>
                      <td
                        className={`px-4 py-2 text-right font-medium whitespace-nowrap ${r.type === "credit" ? "text-emerald-400" : "text-red-400"}`}
                      >
                        {r.type === "credit" ? "+" : "−"}
                        {formatCurrency(r.amount)}
                      </td>
                      <td className="px-4 py-2">
                        {r.exists ? (
                          <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-semibold bg-amber-500/10 text-amber-400 border border-amber-500/25">
                            <CheckCircle2 size={12} /> Already exists
                          </span>
                        ) : (
                          <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-semibold bg-emerald-500/10 text-emerald-400 border border-emerald-500/25">
                            <PlusCircle size={12} /> New
                          </span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            <div className="pt-5 mt-5 border-t border-slate-800 flex justify-end gap-3">
              <button
                className="inline-flex justify-center items-center gap-2 px-5 py-2.5 bg-slate-800 text-slate-200 border border-slate-700 rounded-lg text-sm font-medium hover:bg-slate-700 transition-all"
                onClick={() => setValidationResult(null)}
              >
                Close
              </button>
            </div>
          </div>
        </div>
      )}

      {/* File preview modal */}
      {filePreview && (
        <div
          className="fixed inset-0 z-100 bg-black/70 backdrop-blur-sm flex items-center justify-center p-4 sm:p-6"
          onClick={closeFilePreview}
        >
          <div
            className="bg-slate-900 border border-slate-800 rounded-xl w-full max-w-4xl h-[85vh] flex flex-col overflow-hidden"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex items-center justify-between px-5 py-3 border-b border-slate-800">
              <div className="flex items-center gap-2 text-sm font-medium text-slate-200 min-w-0">
                <FileText size={16} className="text-cyan-500 shrink-0" />
                <span className="truncate">{filePreview.title}</span>
              </div>
              <div className="flex items-center gap-3 shrink-0">
                <a
                  href={filePreview.url}
                  download={
                    filePreview.title.replace(/[^\w.-]+/g, "_") + ".pdf"
                  }
                  className="inline-flex items-center gap-1.5 px-3 py-1.5 bg-slate-800 text-slate-200 border border-slate-700 rounded-lg text-xs font-medium hover:bg-slate-700 transition-all"
                >
                  Download
                </a>
                <button
                  onClick={closeFilePreview}
                  className="p-1.5 rounded-lg text-slate-400 hover:text-white hover:bg-slate-800 transition-colors"
                >
                  <X size={18} />
                </button>
              </div>
            </div>
            <iframe
              src={filePreview.url}
              title="Document preview"
              className="flex-1 w-full bg-white"
            />
          </div>
        </div>
      )}
    </>
  );
}
