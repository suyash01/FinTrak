import {
  useState,
  useEffect,
  useMemo,
  useRef,
  useCallback,
  type Dispatch,
  type SetStateAction,
} from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
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
} from "lucide-react";
import { Checkbox } from "@/components/ui/checkbox";
import AccountSelect from "@/components/AccountSelect/AccountSelect";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Spinner } from "@/components/ui/spinner";
import {
  Dialog,
  DialogContent,
  DialogDescription,
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
import { toastApiError } from "../../lib/errors";
import { useDomainData } from "../../context/DomainDataContext";
import { formatDateOnly } from "../../utils/formatters";
import { filterExcluded } from "../Import/importHelpers";
import ImportPreviewTable from "../Import/ImportPreviewTable";
import ParseWarnings from "../Import/ParseWarnings";
import ValidationDialog from "../Import/ValidationDialog";
import type {
  PaperlessDocument,
  StatementExtractor,
  ImportTransaction,
  ValidateTransactionsResponse,
} from "../../types";

// Number of Paperless documents fetched and parsed in parallel. Parsing proxies
// an expensive PDF download plus extraction to the parser service, so parsing
// every selected document at once (up to 100 per page) can burst-load both
// services. Keep the client-side pool small.
const PARSE_CONCURRENCY = 3;

// mapWithConcurrency runs fn over items with at most `limit` calls in flight,
// preserving input order in the settled results (like Promise.allSettled).
async function mapWithConcurrency<T, R>(
  items: T[],
  limit: number,
  fn: (item: T) => Promise<R>,
): Promise<PromiseSettledResult<R>[]> {
  const results = new Array<PromiseSettledResult<R>>(items.length);
  let next = 0;
  const worker = async () => {
    for (;;) {
      const i = next++;
      if (i >= items.length) return;
      try {
        results[i] = { status: "fulfilled", value: await fn(items[i]) };
      } catch (reason) {
        results[i] = { status: "rejected", reason };
      }
    }
  };
  await Promise.all(
    Array.from({ length: Math.min(limit, items.length) }, () => worker()),
  );
  return results;
}

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
    <div
      ref={ref}
      className="relative"
      onKeyDown={(e) => {
        if (e.key === "Escape" && open) setOpen(false);
      }}
    >
      <Button
        type="button"
        variant="outline"
        aria-expanded={open}
        aria-label={`${label} filter`}
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
        <div
          role="group"
          aria-label={`${label} options`}
          className="absolute z-20 mt-1 w-full min-w-55 bg-popover text-popover-foreground border border-border rounded-lg shadow-xl overflow-hidden"
        >
          <div className="px-3 py-2 border-b border-border text-xs font-semibold text-muted-foreground">
            {label} — <span className="text-chart-3">+ include</span> ·{" "}
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
                        ? "bg-chart-3/10 text-chart-3"
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
                        aria-label={`Include ${opt}`}
                        aria-pressed={mode === "inc"}
                        onClick={() =>
                          onSet(opt, mode === "inc" ? null : "inc")
                        }
                        title="Include (match)"
                        className={`rounded-md ${
                          mode === "inc"
                            ? "bg-emerald-500 hover:bg-emerald-600 border-emerald-500 text-white hover:text-white"
                            : "text-muted-foreground border-border hover:bg-accent hover:text-foreground"
                        }`}
                      >
                        <Plus size={13} />
                      </Button>
                      <Button
                        type="button"
                        size="icon-xs"
                        variant="outline"
                        aria-label={`Exclude ${opt}`}
                        aria-pressed={mode === "exc"}
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
  // Parser-reported subtotal mismatches across the parsed documents.
  validationErrors: string[];
}

// Inc/exclude filter maps (correspondents, document types, tags) are serialized
// to the URL as repeated params so the page state is shareable and deep-linkable.
const SEARCH_PARAM = "search";
const PAGE_PARAM = "page";
const PAGE_SIZE_PARAM = "pageSize";
const PAGE_SIZE_DEFAULT = 25;
const PAGE_SIZE_OPTIONS = [25, 50, 100];
const MAP_PARAMS = {
  correspondent: { inc: "correspondentInc", exc: "correspondentExc" },
  documentType: { inc: "documentTypeInc", exc: "documentTypeExc" },
  tag: { inc: "tagInc", exc: "tagExc" },
} as const;

function mapFromParams(
  params: URLSearchParams,
  incKey: string,
  excKey: string,
): Record<string, string> {
  const map: Record<string, string> = {};
  params.getAll(incKey).forEach((v) => {
    if (v) map[v] = "inc";
  });
  params.getAll(excKey).forEach((v) => {
    if (v) map[v] = "exc";
  });
  return map;
}

function appendMapParams(
  params: URLSearchParams,
  map: Record<string, string>,
  incKey: string,
  excKey: string,
) {
  for (const [value, mode] of Object.entries(map)) {
    if (mode === "inc") params.append(incKey, value);
    else if (mode === "exc") params.append(excKey, value);
  }
}

export default function PaperlessImport() {
  const {
    accounts,
    settings,
    loading: loadingConfig,
  } = useDomainData();
  const configured = Boolean(settings?.paperlessUrl && settings?.hasToken);
  const tagLabel = settings?.paperlessTag || "";

  const [selectedAccount, setSelectedAccount] = useState("");

  const [extractors, setExtractors] = useState<StatementExtractor[]>([]);
  const [extractor, setExtractor] = useState("sbi_cc");
  const [password, setPassword] = useState("");
  const [dateFormat, setDateFormat] = useState("auto");
  const [tagOnImport, setTagOnImport] = useState(false);

  const [documents, setDocuments] = useState<PaperlessDocument[]>([]);
  const [loadingDocs, setLoadingDocs] = useState(false);
  const [selected, setSelected] = useState<Set<number>>(new Set());

  // Filters (initialized from URL search params so filters are shareable)
  const [searchParams, setSearchParams] = useSearchParams();
  const navigate = useNavigate();
  const syncedUrlRef = useRef(searchParams.toString());
  const [search, setSearch] = useState(
    () => searchParams.get(SEARCH_PARAM) || "",
  );
  // Server-side search is debounced so typing doesn't fire a request per key.
  const [debouncedSearch, setDebouncedSearch] = useState(() => search);
  const [correspondentMap, setCorrespondentMap] = useState<
    Record<string, string>
  >(() =>
    mapFromParams(
      searchParams,
      MAP_PARAMS.correspondent.inc,
      MAP_PARAMS.correspondent.exc,
    ),
  );
  const [documentTypeMap, setDocumentTypeMap] = useState<
    Record<string, string>
  >(() =>
    mapFromParams(
      searchParams,
      MAP_PARAMS.documentType.inc,
      MAP_PARAMS.documentType.exc,
    ),
  );

  // Pagination + filter option lists come back from the (server-side) listing.
  const [page, setPage] = useState(() =>
    Math.max(1, parseInt(searchParams.get(PAGE_PARAM) || "1", 10) || 1),
  );
  const [pageSize, setPageSize] = useState(() => {
    const v = parseInt(searchParams.get(PAGE_SIZE_PARAM) || "", 10);
    return PAGE_SIZE_OPTIONS.includes(v) ? v : PAGE_SIZE_DEFAULT;
  });
  const [totalCount, setTotalCount] = useState(0);
  const [totalPages, setTotalPages] = useState(1);
  const [correspondents, setCorrespondents] = useState<string[]>([]);
  const [documentTypes, setDocumentTypes] = useState<string[]>([]);
  const [tags, setTags] = useState<string[]>([]);
  const [tagMap, setTagMap] = useState<Record<string, string>>(() =>
    mapFromParams(searchParams, MAP_PARAMS.tag.inc, MAP_PARAMS.tag.exc),
  );

  // File preview (blob URL of the original PDF)
  const [filePreview, setFilePreview] = useState<FilePreview | null>(null);
  const [loadingFileId, setLoadingFileId] = useState<number | null>(null);
  // Aborts the previous document listing when filters/page change, so a slow
  // response can't overwrite a newer page of results.
  const docsAbortRef = useRef<AbortController | null>(null);
  // Mirrors filePreview so the unmount cleanup can revoke the current blob URL
  // without re-running when the preview changes.
  const filePreviewRef = useRef<FilePreview | null>(null);

  const [preview, setPreview] = useState<ImportPreview | null>(null);
  const [parsing, setParsing] = useState(false);
  const [parseProgress, setParseProgress] = useState({ done: 0, total: 0 });
  const [importing, setImporting] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");

  // Validation (read-only duplicate check against the selected account)
  const [validating, setValidating] = useState(false);
  const [validationResult, setValidationResult] =
    useState<ValidateTransactionsResponse | null>(null);

  // Row indices the user unchecks in the preview; excluded transactions are
  // dropped from validate + import. Reset whenever a new preview is parsed.
  const [excluded, setExcluded] = useState<Set<number>>(new Set());
  useEffect(() => {
    setExcluded(new Set());
  }, [preview]);

  useEffect(() => {
    if (!configured) return;
    api
      .getStatementExtractors()
      .then((res) => {
        const list = res?.extractors || [];
        setExtractors(list);
        if (list.length > 0) setExtractor(list[0].name);
      })
      .catch((err) => toastApiError(err));
    // Load extractors once Paperless is configured; setters are stable.
  }, [configured]);

  // Debounce the server-side search so a keystroke never fires a request.
  useEffect(() => {
    const t = setTimeout(() => setDebouncedSearch(search), 400);
    return () => clearTimeout(t);
  }, [search]);

  // Filters + pagination are forwarded to Paperless; the response also carries
  // the full lookup tables so the filter dropdowns stay complete across pages.
  const loadDocuments = useCallback(async () => {
    docsAbortRef.current?.abort();
    const controller = new AbortController();
    docsAbortRef.current = controller;

    setLoadingDocs(true);
    setError("");
    try {
      const res = await api.getPaperlessDocuments(
        {
          search: debouncedSearch,
          page,
          pageSize,
          correspondentInc: Object.keys(correspondentMap).filter(
            (k) => correspondentMap[k] === "inc",
          ),
          correspondentExc: Object.keys(correspondentMap).filter(
            (k) => correspondentMap[k] === "exc",
          ),
          documentTypeInc: Object.keys(documentTypeMap).filter(
            (k) => documentTypeMap[k] === "inc",
          ),
          documentTypeExc: Object.keys(documentTypeMap).filter(
            (k) => documentTypeMap[k] === "exc",
          ),
          tagInc: Object.keys(tagMap).filter((k) => tagMap[k] === "inc"),
          tagExc: Object.keys(tagMap).filter((k) => tagMap[k] === "exc"),
        },
        { signal: controller.signal },
      );
      if (docsAbortRef.current !== controller) return;
      setDocuments(res?.documents || []);
      setTotalCount(res?.totalCount ?? 0);
      setTotalPages(Math.max(1, res?.totalPages ?? 1));
      setCorrespondents(res?.correspondents || []);
      setDocumentTypes(res?.documentTypes || []);
      setTags(res?.tags || []);
      // Clamp to the last page if a filter change made the current page stale.
      if (res?.totalPages && page > res.totalPages) setPage(res.totalPages);
    } catch (err) {
      if (docsAbortRef.current !== controller) return;
      if ((err as Error).name !== "AbortError") {
        setError((err as Error).message);
      }
    } finally {
      if (docsAbortRef.current === controller) setLoadingDocs(false);
    }
  }, [debouncedSearch, page, pageSize, correspondentMap, documentTypeMap, tagMap]);

  useEffect(() => {
    if (!configured) return;
    loadDocuments();
  }, [configured, loadDocuments]);

  // Abort any in-flight document listing and release the preview blob URL when
  // the page unmounts.
  useEffect(
    () => () => {
      docsAbortRef.current?.abort();
      if (filePreviewRef.current) {
        URL.revokeObjectURL(filePreviewRef.current.url);
        filePreviewRef.current = null;
      }
    },
    [],
  );

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
      setPage(1);
      setter((prev) => {
        const next = { ...prev };
        if (mode) next[value] = mode;
        else delete next[value];
        return next;
      });
    };

  // Keep the URL in sync with user-driven filter changes. Only non-default
  // filters are written, so a clean URL equals the default state.
  useEffect(() => {
    const params = new URLSearchParams();
    if (search) params.set(SEARCH_PARAM, search);
    if (page > 1) params.set(PAGE_PARAM, String(page));
    if (pageSize !== PAGE_SIZE_DEFAULT)
      params.set(PAGE_SIZE_PARAM, String(pageSize));
    appendMapParams(
      params,
      correspondentMap,
      MAP_PARAMS.correspondent.inc,
      MAP_PARAMS.correspondent.exc,
    );
    appendMapParams(
      params,
      documentTypeMap,
      MAP_PARAMS.documentType.inc,
      MAP_PARAMS.documentType.exc,
    );
    appendMapParams(params, tagMap, MAP_PARAMS.tag.inc, MAP_PARAMS.tag.exc);
    const desiredQs = params.toString();
    if (desiredQs === syncedUrlRef.current) return;
    syncedUrlRef.current = desiredQs;
    setSearchParams(params, { replace: true });
  }, [
    search,
    page,
    pageSize,
    correspondentMap,
    documentTypeMap,
    tagMap,
  ]);

  // React to external URL changes (navigation, back/forward, shared links).
  useEffect(() => {
    const currentQs = searchParams.toString();
    if (currentQs === syncedUrlRef.current) return;

    const nextSearch = searchParams.get(SEARCH_PARAM) || "";
    const nextPage = Math.max(
      1,
      parseInt(searchParams.get(PAGE_PARAM) || "1", 10) || 1,
    );
    const rawPageSize = parseInt(
      searchParams.get(PAGE_SIZE_PARAM) || "",
      10,
    );
    const nextPageSize = PAGE_SIZE_OPTIONS.includes(rawPageSize)
      ? rawPageSize
      : PAGE_SIZE_DEFAULT;
    const nextCorrespondent = mapFromParams(
      searchParams,
      MAP_PARAMS.correspondent.inc,
      MAP_PARAMS.correspondent.exc,
    );
    const nextDocumentType = mapFromParams(
      searchParams,
      MAP_PARAMS.documentType.inc,
      MAP_PARAMS.documentType.exc,
    );
    const nextTags = mapFromParams(
      searchParams,
      MAP_PARAMS.tag.inc,
      MAP_PARAMS.tag.exc,
    );
    if (nextSearch !== search) setSearch(nextSearch);
    if (nextPage !== page) setPage(nextPage);
    if (nextPageSize !== pageSize) setPageSize(nextPageSize);
    if (JSON.stringify(nextCorrespondent) !== JSON.stringify(correspondentMap))
      setCorrespondentMap(nextCorrespondent);
    if (JSON.stringify(nextDocumentType) !== JSON.stringify(documentTypeMap))
      setDocumentTypeMap(nextDocumentType);
    if (JSON.stringify(nextTags) !== JSON.stringify(tagMap))
      setTagMap(nextTags);
    syncedUrlRef.current = currentQs;
    // React to external URL changes only; the setters/state read above are
    // stable and including them would re-sync on state we just wrote.
  }, [searchParams]);

  // True when any filter is active, used to distinguish "no documents" from
  // "no documents match the current filters".
  const hasActiveFilters = useMemo(
    () =>
      search.trim() !== "" ||
      Object.keys(correspondentMap).length > 0 ||
      Object.keys(documentTypeMap).length > 0 ||
      Object.keys(tagMap).length > 0,
    [search, correspondentMap, documentTypeMap, tagMap],
  );

  // Filter dropdown options come from the server response (the full lookup
  // tables), so they stay complete across pages. The loaded page is merged in
  // so names Paperless hasn't indexed yet still appear.
  const correspondentOptions = useMemo(
    () => [...new Set([...correspondents, ...documents.map((d) => d.correspondent).filter(Boolean)])].sort(),
    [correspondents, documents],
  );

  const documentTypeOptions = useMemo(
    () => [...new Set([...documentTypes, ...documents.map((d) => d.documentType).filter(Boolean)])].sort(),
    [documentTypes, documents],
  );

  const tagOptions = useMemo(
    () => [...new Set([...tags, ...documents.flatMap((d) => d.tags || [])])].sort(),
    [tags, documents],
  );

  const openFilePreview = async (doc: PaperlessDocument) => {
    setLoadingFileId(doc.id);
    setError("");
    try {
      const blob = await api.getPaperlessDocumentFile(doc.id);
      const url = URL.createObjectURL(blob);
      // Release the previous preview (if any) so its blob URL doesn't leak.
      if (filePreviewRef.current) {
        URL.revokeObjectURL(filePreviewRef.current.url);
      }
      const next = { url, title: doc.title || `Document #${doc.id}` };
      filePreviewRef.current = next;
      setFilePreview(next);
    } catch (err) {
      setError("Failed to load document preview: " + (err as Error).message);
    } finally {
      setLoadingFileId(null);
    }
  };

  const closeFilePreview = () => {
    if (filePreviewRef.current) {
      URL.revokeObjectURL(filePreviewRef.current.url);
      filePreviewRef.current = null;
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

    const ids = [...selected];
    setParseProgress({ done: 0, total: ids.length });
    try {
      // Parse the selected documents through a bounded pool (not all at once)
      // so a large selection can't burst-load the backend/parser.
      const results = await mapWithConcurrency(
        ids,
        PARSE_CONCURRENCY,
        async (id) => {
          try {
            return await api.importPaperlessDocument({
              documentId: id,
              extractor,
              password,
              dateFormat: dateFormat === "auto" ? "" : dateFormat,
            });
          } finally {
            setParseProgress((p) => ({ ...p, done: p.done + 1 }));
          }
        },
      );

      const transactions: ImportTransaction[] = [];
      const titles: string[] = [];
      const successIds: number[] = [];
      const validationErrors: string[] = [];
      const failures: string[] = [];
      results.forEach((result, i) => {
        const id = ids[i];
        const doc = documents.find((d) => d.id === id);
        const title = doc?.title || `Document #${id}`;
        if (result.status === "fulfilled") {
          titles.push(title);
          transactions.push(...(result.value.transactions || []));
          // Prefix each message with its document so a warning can be traced
          // back to the statement it came from.
          (result.value.validationErrors || []).forEach((warning) =>
            validationErrors.push(`${title}: ${warning}`),
          );
          successIds.push(id);
        } else {
          const reason = (result.reason as Error)?.message || "failed";
          failures.push(`${title}: ${reason}`);
        }
      });

      if (failures.length > 0) {
        setError(`Some documents failed to parse — ${failures.join("; ")}`);
      }
      if (successIds.length === 0) return;
      setPreview({
        title: titles.join(", "),
        transactions,
        documentIds: successIds,
        validationErrors,
      });
    } finally {
      setParsing(false);
      setParseProgress({ done: 0, total: 0 });
    }
  };

  const confirmImport = async () => {
    if (!preview || includedTransactions.length === 0) return;
    setImporting(true);
    setError("");
    setSuccess("");
    try {
      // The backend tags the source Paperless documents only after the
      // transactions have been committed, so the label is added only on a
      // successful import.
      await api.importTransactions({
        accountId: selectedAccount,
        transactions: includedTransactions,
        duplicateAction: "keep",
        paperlessDocumentIds: tagOnImport ? preview.documentIds || [] : [],
      });
      setSuccess(
        `Imported ${includedTransactions.length} of ${parsedCount} transactions${
          excludedCount > 0 ? ` (${excludedCount} excluded)` : ""
        }.`,
      );
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
    if (!preview || includedTransactions.length === 0) return;
    setValidating(true);
    setError("");
    try {
      const result = await api.validateTransactions({
        accountId: selectedAccount,
        transactions: includedTransactions,
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
  const includedTransactions = useMemo(
    () => filterExcluded(preview?.transactions || [], excluded),
    [preview, excluded],
  );
  const excludedCount = parsedCount - includedTransactions.length;

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
              onClick={() => navigate("/settings")}
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
          <div
            role="alert"
            className="bg-destructive/10 border border-destructive/30 text-destructive text-sm rounded-lg px-4 py-3"
          >
            {error}
          </div>
        )}
        {success && (
          <div
            role="status"
            className="bg-chart-3/10 border border-chart-3/30 text-chart-3 text-sm rounded-lg px-4 py-3"
          >
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
              <Label
                htmlFor="paperless-account"
                className="text-xs font-medium text-muted-foreground"
              >
                FinTrak Account
              </Label>
              <AccountSelect
                id="paperless-account"
                accounts={accounts}
                value={selectedAccount}
                onValueChange={setSelectedAccount}
                placeholder="Choose an account..."
                triggerClassName="w-full"
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label
                htmlFor="paperless-extractor"
                className="text-xs font-medium text-muted-foreground"
              >
                Extractor
              </Label>
              <Select value={extractor} onValueChange={setExtractor}>
                <SelectTrigger id="paperless-extractor" className="w-full">
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
              <Label
                htmlFor="paperless-date-format"
                className="text-xs font-medium text-muted-foreground"
              >
                Date Format
              </Label>
              <Select value={dateFormat} onValueChange={setDateFormat}>
                <SelectTrigger id="paperless-date-format" className="w-full">
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
              <Label
                htmlFor="paperless-password"
                className="text-xs font-medium text-muted-foreground"
              >
                Password (optional)
              </Label>
              <Input
                id="paperless-password"
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
              Documents ({totalCount})
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
                placeholder="Search title..."
                aria-label="Search documents by title"
                className="pl-9"
                value={search}
                onChange={(e) => {
                  setSearch(e.target.value);
                  setPage(1);
                }}
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
          ) : documents.length === 0 ? (
            <div className="text-sm text-muted-foreground py-6">
              {hasActiveFilters
                ? "No documents match the current filters."
                : "No documents found in Paperless."}
            </div>
          ) : (
            <div
              role="list"
              aria-label="Paperless documents"
              className="divide-y divide-border border border-border rounded-lg overflow-y-auto max-h-96 bg-background"
            >
              {documents.map((d) => {
                const docTitle = d.title || `Document #${d.id}`;
                const checkboxId = `paperless-doc-${d.id}`;
                return (
                  <div
                    key={d.id}
                    role="listitem"
                    className="flex items-start gap-3 p-3 transition-colors hover:bg-card"
                  >
                    <Checkbox
                      id={checkboxId}
                      checked={selected.has(d.id)}
                      onCheckedChange={() => toggle(d.id)}
                      aria-label={`Select ${docTitle}`}
                      className="mt-1"
                    />
                    {/* A real <label> is the checkbox's click target; it holds
                        only phrasing content so it stays valid. The Preview
                        button is a sibling control, not nested in the label. */}
                    <label
                      htmlFor={checkboxId}
                      className="min-w-0 flex-1 cursor-pointer"
                    >
                      <span className="flex items-center gap-2 text-sm font-medium text-foreground">
                        <FileText size={14} className="text-primary shrink-0" />
                        <span className="truncate">{docTitle}</span>
                      </span>
                      <span className="block text-xs text-muted-foreground mt-0.5 truncate">
                        #{d.id}
                        {d.correspondent ? ` · ${d.correspondent}` : ""}
                        {d.documentType ? ` · ${d.documentType}` : ""}
                        {d.created
                          ? ` · Created ${formatDateOnly(new Date(d.created))}`
                          : ""}
                      </span>
                      {d.tags?.length > 0 && (
                        <span className="flex flex-wrap gap-1 mt-1.5">
                          {d.tags.map((tag) => (
                            <span
                              key={tag}
                              className="text-[10px] uppercase bg-muted text-muted-foreground px-1.5 py-0.5 rounded"
                            >
                              {tag}
                            </span>
                          ))}
                        </span>
                      )}
                    </label>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      onClick={() => openFilePreview(d)}
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
                );
              })}
            </div>
          )}

          {/* Pagination */}
          <div className="flex items-center justify-between mt-4 mb-4 flex-wrap gap-3">
            <div className="flex items-center gap-3 text-sm text-muted-foreground">
              <span>
                Page {page} of {totalPages} ({totalCount} total)
              </span>
              <Select
                value={String(pageSize)}
                onValueChange={(v) => {
                  setPageSize(Number(v));
                  setPage(1);
                }}
              >
                <SelectTrigger
                  aria-label="Documents per page"
                  className="w-auto gap-2 px-2 py-1 h-8 text-xs"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {PAGE_SIZE_OPTIONS.map((n) => (
                    <SelectItem key={n} value={String(n)}>
                      {n} / page
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            {totalPages > 1 && (
              <div className="flex gap-1.5">
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={page <= 1}
                  onClick={() => setPage(page - 1)}
                >
                  Prev
                </Button>
                {Array.from(
                  { length: Math.min(totalPages, 7) },
                  (_, i) => {
                    let pageNum: number;
                    if (totalPages <= 7) {
                      pageNum = i + 1;
                    } else if (page <= 4) {
                      pageNum = i + 1;
                    } else if (page >= totalPages - 3) {
                      pageNum = totalPages - 6 + i;
                    } else {
                      pageNum = page - 3 + i;
                    }
                    return (
                      <Button
                        key={pageNum}
                        size="sm"
                        variant={page === pageNum ? "default" : "ghost"}
                        onClick={() => setPage(pageNum)}
                      >
                        {pageNum}
                      </Button>
                    );
                  },
                )}
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={page >= totalPages}
                  onClick={() => setPage(page + 1)}
                >
                  Next
                </Button>
              </div>
            )}
          </div>

          <div className="flex items-center gap-3">
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
                ? parseProgress.total > 0
                  ? `Parsing ${parseProgress.done}/${parseProgress.total}...`
                  : "Parsing..."
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
              {parsedCount} transaction(s) parsed.
              {excludedCount > 0 ? (
                <>
                  {" "}
                  {includedTransactions.length} selected for import —{" "}
                  {excludedCount} excluded. Unchecked rows are not validated
                  or imported.
                </>
              ) : (
                <>
                  {" "}
                  Review below — uncheck any row to exclude it from the
                  import.
                </>
              )}
            </p>
            <ParseWarnings
              warnings={preview.validationErrors}
              className="mb-3"
            />
            {preview.transactions.length === 0 ? (
              <div className="text-sm text-muted-foreground py-4">
                No transactions were parsed from these documents.
              </div>
            ) : (
              <ImportPreviewTable
                transactions={preview.transactions}
                excluded={excluded}
                onExcludedChange={setExcluded}
                showPayee={false}
                maxHeight={320}
                stickyHeader={false}
              />
            )}
            {parsedCount > 0 && (
              <div className="flex items-center gap-3 mt-4">
                <Button
                  variant="outline"
                  onClick={runValidation}
                  disabled={validating || includedTransactions.length === 0}
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
                  disabled={importing || includedTransactions.length === 0}
                  className="bg-emerald-500 hover:bg-emerald-600 text-white font-semibold"
                >
                  {importing ? (
                    <Loader2 size={16} className="animate-spin" />
                  ) : (
                    <Check size={16} />
                  )}
                  {importing
                    ? "Importing..."
                    : `Import ${includedTransactions.length} transaction(s)`}
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
        <ValidationDialog
          result={validationResult}
          accountName={
            accounts.find((a) => a.id === selectedAccount)?.name ||
            "this account"
          }
          onClose={() => setValidationResult(null)}
        />
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
                <DialogTitle className="truncate">
                  {filePreview.title}
                </DialogTitle>
              </div>
              <DialogDescription className="sr-only">
                Preview of the selected Paperless document
              </DialogDescription>
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
                  aria-label="Close preview"
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
