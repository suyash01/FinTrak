import {
  useState,
  useEffect,
  useCallback,
  useMemo,
  memo,
  useRef,
  type CSSProperties,
} from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import {
  Search,
  ChevronUp,
  ChevronDown,
  Trash2,
  Tags,
  Link2,
  Link2Off,
  Check,
  Pencil,
  Plus,
  Folder,
} from "lucide-react";
import LinkTransactionModal from "./LinkTransactionModal";
import EditTransactionModal from "./EditTransactionModal";
import { Checkbox } from "@/components/ui/checkbox";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
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
import api from "../../api/client";
import { formatCurrency, formatDate } from "../../utils/formatters";
import { useSettings } from "../../context/SettingsContext";
import type {
  Transaction,
  Account,
  Category,
  CategoryGroup,
  Payee,
  BillingCycle,
  TransactionsResponse,
  QueryParams,
} from "../../types";
import { buildCategorySections } from "../../lib/categories";

interface SelectOption {
  value: string;
  label: string;
}

interface EditableSelectProps {
  value?: string | null;
  options: SelectOption[];
  onChange: (value: string) => void;
  placeholder: string;
  displayText?: string;
  style?: CSSProperties;
}

function EditableSelect({
  value,
  options,
  onChange,
  placeholder,
  displayText,
  style,
}: EditableSelectProps) {
  const isPlaceholder = !value;
  const isMissing = Boolean(value) && !options.some((o) => o.value === value);

  return (
    <select
      className={`bg-transparent border-none text-[13px] outline-none focus:ring-0 w-full rounded px-1 py-0.5 cursor-pointer appearance-none hover:bg-accent transition-all block truncate ${isPlaceholder ? "text-muted-foreground italic" : ""}`}
      style={{ ...style, backgroundImage: "none" }}
      value={value || ""}
      onChange={(e) => onChange(e.target.value)}
      title="Click to edit"
    >
      <option value="" className="bg-popover text-muted-foreground not-italic">
        {placeholder}
      </option>
      {isMissing && (
        <option
          value={value ?? ""}
          className="bg-popover text-foreground not-italic"
          hidden
        >
          {displayText}
        </option>
      )}
      {options.map((o) => (
        <option
          key={o.value}
          value={o.value}
          className="bg-popover text-foreground not-italic"
        >
          {o.label}
        </option>
      ))}
    </select>
  );
}

interface TransactionRowProps {
  t: Transaction;
  compactLayout: boolean;
  selected: boolean;
  toggleSelect: (id: string) => void;
  categoryOptions: SelectOption[];
  payeeOptions: SelectOption[];
  handleCategoryChange: (
    txnId: string,
    categoryId: string,
    txn: Transaction,
  ) => void;
  handlePayeeChange: (txnId: string, payeeId: string, txn: Transaction) => void;
  handleDelete: (id: string) => void;
  handleUnlink: (t: Transaction) => void;
  setLinkingTxn: (t: Transaction | null) => void;
  setEditingTxn: (t: Transaction | null) => void;
}

const TransactionRow = memo(function TransactionRow({
  t,
  compactLayout,
  selected,
  toggleSelect,
  categoryOptions,
  payeeOptions,
  handleCategoryChange,
  handlePayeeChange,
  handleDelete,
  handleUnlink,
  setLinkingTxn,
  setEditingTxn,
}: TransactionRowProps) {
  if (t.isSummary) {
    const pad = compactLayout ? "py-1.5 px-3" : "py-3 px-4";
    return (
      <TableRow className="bg-primary/10 border-border">
        <TableCell className={pad}></TableCell>
        <TableCell className={`${pad} text-sm text-muted-foreground whitespace-nowrap`}>
          {formatDate(t.date)}
        </TableCell>
        <TableCell className={`${pad} text-sm font-semibold text-primary`}>
          {t.description}
        </TableCell>
        <TableCell className={pad}></TableCell>
        <TableCell className={`${pad} text-sm text-muted-foreground whitespace-nowrap`}>
          {t.accountName}
        </TableCell>
        <TableCell className={pad}></TableCell>
        <TableCell
          className={`${pad} text-sm text-right font-bold text-foreground font-mono whitespace-nowrap`}
        >
          {formatCurrency(t.amount)}
        </TableCell>
        <TableCell className={pad}></TableCell>
      </TableRow>
    );
  }
  return (
    <TableRow
      className={`transition-colors border-border ${selected ? "bg-primary/10" : "hover:bg-muted/50"}`}
    >
      <TableCell className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"}`}>
        <Checkbox checked={selected} onCheckedChange={() => toggleSelect(t.id)} />
      </TableCell>
      <TableCell
        className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm whitespace-nowrap`}
      >
        {formatDate(t.date)}
      </TableCell>
      <TableCell
        className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm max-w-62.5 overflow-hidden text-ellipsis whitespace-nowrap`}
        title={t.description}
      >
        {t.description}
      </TableCell>
      <TableCell
        className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm min-w-25`}
      >
        <EditableSelect
          value={t.payeeId}
          options={payeeOptions}
          onChange={(val) => handlePayeeChange(t.id, val, t)}
          placeholder="No Payee"
          displayText={t.payee}
        />
      </TableCell>
      <TableCell
        className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm whitespace-nowrap`}
      >
        {t.accountName}
      </TableCell>
      <TableCell className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm`}>
        <EditableSelect
          value={t.categoryId}
          options={categoryOptions}
          onChange={(val) => handleCategoryChange(t.id, val, t)}
          placeholder="Uncategorized"
          displayText={t.categoryId ? t.categoryName : ""}
          style={t.categoryColor ? { color: t.categoryColor } : undefined}
        />
      </TableCell>
      <TableCell
        className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm text-right font-semibold whitespace-nowrap`}
      >
        <span
          className={t.type === "debit" ? "text-destructive" : "text-emerald-500"}
        >
          {t.type === "debit" ? "−" : "+"}
          {formatCurrency(t.amount)}
        </span>
      </TableCell>
      <TableCell className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"}`}>
        <div className="flex items-center gap-1">
          <Button
            variant="ghost"
            size="icon-sm"
            className="text-muted-foreground hover:text-primary hover:bg-primary/10"
            onClick={() => setEditingTxn(t)}
            title="Edit transaction"
          >
            <Pencil size={14} />
          </Button>
          <Button
            variant="ghost"
            size="icon-sm"
            className="text-muted-foreground hover:text-primary hover:bg-primary/10"
            onClick={() => setLinkingTxn(t)}
            title={
              t.isLinked ? "Link more transactions" : "Find match and link"
            }
          >
            <Link2 size={14} />
          </Button>
          {(t.linkCount ?? 0) > 0 && (
            <Button
              variant="ghost"
              size="icon-sm"
              className="group text-emerald-500 hover:text-destructive hover:bg-destructive/10"
              onClick={() => handleUnlink(t)}
              title={(t.linkCount ?? 0) > 1 ? "Manage links" : "Unlink"}
            >
              <Check size={14} className="group-hover:hidden" />
              <Link2Off size={14} className="hidden group-hover:block" />
            </Button>
          )}
          <Button
            variant="ghost"
            size="icon-sm"
            className="text-muted-foreground hover:text-destructive hover:bg-destructive/10"
            onClick={() => handleDelete(t.id)}
          >
            <Trash2 size={14} />
          </Button>
        </div>
      </TableCell>
    </TableRow>
  );
});

const URL_PARAMS = [
  "search",
  "accountId",
  "categoryId",
  "groupId",
  "payeeId",
  "type",
  "dateFrom",
  "dateTo",
  "linked",
  "sortBy",
  "sortOrder",
  "page",
];

const DEFAULT_FILTERS: Record<string, string | number> = {
  search: "",
  accountId: "",
  categoryId: "",
  groupId: "",
  payeeId: "",
  type: "",
  dateFrom: "",
  dateTo: "",
  linked: "",
  sortBy: "date",
  sortOrder: "DESC",
  page: 1,
};

const PAGE_SIZE_OPTIONS = [25, 50, 100, 200, 500, 1000];
const MAX_PAGE_SIZE = 1000;
const PAGE_SIZE_LS_KEY = "txPageSize";

export default function Transactions() {
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const [data, setData] = useState<TransactionsResponse>({
    data: [],
    total: 0,
    page: 1,
    pages: 0,
  });
  const [loading, setLoading] = useState(true);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [categories, setCategories] = useState<Category[]>([]);
  const [groups, setGroups] = useState<CategoryGroup[]>([]);
  const [payees, setPayees] = useState<Payee[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [linkingTxn, setLinkingTxn] = useState<Transaction | null>(null);
  const [editingTxn, setEditingTxn] = useState<Transaction | null>(null);
  const [creating, setCreating] = useState(false);
  const [deleteTxnId, setDeleteTxnId] = useState<string | null>(null);
  const [bulkDeleteOpen, setBulkDeleteOpen] = useState(false);
  const [unlinkTxn, setUnlinkTxn] = useState<Transaction | null>(null);
  const [billingCycles, setBillingCycles] = useState<BillingCycle[]>([]);
  const [loadingCycles, setLoadingCycles] = useState(false);
  const { compactLayout } = useSettings();

  // Page size: remembered locally and persisted against the user's account.
  const savedPageSize = () => {
    const v = Number(localStorage.getItem(PAGE_SIZE_LS_KEY));
    if (Number.isNaN(v)) return 50;
    return Math.min(Math.max(v, 1), MAX_PAGE_SIZE);
  };
  const [pageSize, setPageSize] = useState(savedPageSize);
  const [preset, setPreset] = useState(() => {
    const v = savedPageSize();
    return PAGE_SIZE_OPTIONS.includes(v) ? String(v) : "custom";
  });
  const [customInput, setCustomInput] = useState(() => String(savedPageSize()));

  // Refs to avoid closures in callbacks
  const categoriesRef = useRef(categories);
  const payeesRef = useRef(payees);
  const abortRef = useRef<AbortController | null>(null);
  // URL that the current filters already correspond to. Used by the two URL
  // sync effects below to tell "URL change we caused" apart from external
  // navigation, which is what keeps them from fighting each other in a loop.
  const syncedUrlRef = useRef<string>(searchParams.toString());
  useEffect(() => {
    categoriesRef.current = categories;
  }, [categories]);
  useEffect(() => {
    payeesRef.current = payees;
  }, [payees]);

  // Pre-compute select options so they're stable references
  const categoryOptions = useMemo(
    () => categories.map((c) => ({ value: c.id, label: c.name })),
    [categories],
  );
  const payeeOptions = useMemo(
    () => payees.map((p) => ({ value: p.id, label: p.name })),
    [payees],
  );
  // Categories grouped by their group, in a stable order, so the filter and
  // pickers render each group with its categories. Groups without any categories
  // are hidden; the "Uncategorized" option is always shown separately.
  const categorySections = useMemo(
    () => buildCategorySections(groups, categories),
    [groups, categories],
  );
  // Lookup of every group id so the filter can tell a group selection apart
  // from a category selection (both live in the same dropdown).
  const groupIds = useMemo(() => new Set(groups.map((g) => g.id)), [groups]);

  // Filters (initialized from URL search params)
  const [filters, setFilters] = useState<Record<string, string | number>>(
    () => {
      const urlToFilters: Record<string, string | number> = {
        ...DEFAULT_FILTERS,
      };
      URL_PARAMS.forEach((k) => {
        const v = searchParams.get(k);
        if (v !== null && v !== "") urlToFilters[k] = v;
      });
      return { ...urlToFilters, limit: pageSize || 0 };
    },
  );

  // Keep the URL in sync with user-driven filter changes (browser back/forward
  // friendly). Only non-default filters are written, so an empty URL and the
  // default filter state stay equivalent. The syncedUrlRef comparison makes
  // this effect a no-op when the URL already reflects the filters, so it never
  // overwrites an external navigation that lands on the current state.
  useEffect(() => {
    const params: Record<string, string> = {};
    URL_PARAMS.forEach((k) => {
      const v = filters[k];
      if (v === "" || v === null || v === undefined) return;
      if (String(v) === String(DEFAULT_FILTERS[k])) return;
      params[k] = String(v);
    });
    const desiredQs = new URLSearchParams(params).toString();
    if (desiredQs === syncedUrlRef.current) return;
    syncedUrlRef.current = desiredQs;
    setSearchParams(params, { replace: true });
  }, [filters]);

  // React to external URL changes (navigation, back/forward, shared links).
  // URLs this component wrote itself (tracked in syncedUrlRef) are ignored so
  // user-driven filter edits are never clobbered. A param missing from the URL
  // resets its filter back to the default.
  useEffect(() => {
    const currentQs = searchParams.toString();
    if (currentQs === syncedUrlRef.current) return;

    const urlToFilters: Record<string, string> = {};
    let changed = false;
    URL_PARAMS.forEach((k) => {
      const v = searchParams.get(k);
      const current = filters[k];
      const defaultValue = DEFAULT_FILTERS[k];
      if (v !== null && v !== "") {
        if (String(current) !== v) {
          urlToFilters[k] = v;
          changed = true;
        }
      } else if (String(current) !== String(defaultValue)) {
        urlToFilters[k] = String(defaultValue);
        changed = true;
      }
    });
    if (changed) {
      setFilters((f) => ({
        ...f,
        ...urlToFilters,
        page: urlToFilters.page || DEFAULT_FILTERS.page,
      }));
      setSelected(new Set());
    }
    syncedUrlRef.current = currentQs;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchParams]);

  // Sync filter limit when pageSize changes in settings
  useEffect(() => {
    setFilters((f) => ({ ...f, limit: pageSize || 0, page: 1 }));
    setSelected(new Set());
  }, [pageSize]);

  // Restore the persisted page size from the server.
  useEffect(() => {
    api
      .getUserSettings()
      .then((s) => {
        if (typeof s.pageSize !== "number") return;
        const clamped = Math.min(Math.max(s.pageSize, 1), MAX_PAGE_SIZE);
        setPageSize(clamped);
        if (PAGE_SIZE_OPTIONS.includes(clamped)) {
          setPreset(String(clamped));
        } else {
          setPreset("custom");
          setCustomInput(String(clamped));
        }
      })
      .catch(() => {});
  }, []);

  const applyPageSize = (size: number) => {
    const n = Number(size);
    if (!Number.isFinite(n)) return;
    const clamped = Math.min(Math.max(n, 1), MAX_PAGE_SIZE);
    setPageSize(clamped);
    localStorage.setItem(PAGE_SIZE_LS_KEY, String(clamped));
    api.updateUserSettings({ pageSize: clamped }).catch(() => {});
  };

  const handlePresetChange = (val: string) => {
    if (val === "custom") {
      setCustomInput(String(pageSize));
      setPreset("custom");
    } else {
      setPreset(val);
      setCustomInput("");
      applyPageSize(Number(val));
    }
  };

  const commitCustom = () => {
    const n = Number(customInput);
    if (!Number.isFinite(n) || n < 1) return;
    applyPageSize(n);
  };

  const loadTransactions = useCallback(async () => {
    const controller = new AbortController();
    abortRef.current?.abort();
    abortRef.current = controller;

    setLoading(true);
    try {
      const params: QueryParams = {};
      Object.entries(filters).forEach(([k, v]) => {
        if (v !== "" && v !== null && v !== undefined) params[k] = v;
      });
      const res = await api.getTransactions(params, {
        signal: controller.signal,
      });
      if (abortRef.current === controller) setData(res);
    } catch (err) {
      if ((err as Error).name !== "AbortError") console.error(err);
    } finally {
      if (abortRef.current === controller) setLoading(false);
    }
  }, [filters]);

  useEffect(() => {
    const timer = setTimeout(loadTransactions, 300);
    return () => clearTimeout(timer);
  }, [loadTransactions]);
  useEffect(() => {
    api
      .getAccounts()
      .then((list) => {
        setAccounts(list);
        // Pre-fill the account filter with the user's default account when no
        // account filter was explicitly requested.
        const def = list.find((a) => a.isDefault);
        if (def && !searchParams.get("accountId")) {
          setFilters((f) => ({ ...f, accountId: def.id, page: 1 }));
          setSelected(new Set());
        }
      })
      .catch(console.error);
    api.getCategories().then(setCategories).catch(console.error);
    api.getGroups().then(setGroups).catch(console.error);
    api.getPayees().then(setPayees).catch(console.error);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Bulk billing-cycle assignment is only offered when the account filter is a
  // single account with a billing day (cycles are per-account, so this
  // guarantees all selected transactions belong to the same account).
  const selectedAccount = accounts.find((a) => a.id === filters.accountId);
  const hasBillingDayFilter = Boolean(selectedAccount?.billingDay);

  // True when any filter deviates from the defaults, so the header can tell a
  // filtered count apart from the unfiltered "all accounts" total.
  const isFiltered = useMemo(
    () =>
      filters.accountId !== "" ||
      filters.categoryId !== "" ||
      filters.groupId !== "" ||
      filters.payeeId !== "" ||
      filters.search !== "" ||
      filters.type !== "" ||
      filters.dateFrom !== "" ||
      filters.dateTo !== "" ||
      filters.linked !== "",
    [filters],
  );

  useEffect(() => {
    if (!hasBillingDayFilter) {
      setBillingCycles([]);
      setLoadingCycles(false);
      return;
    }
    let cancelled = false;
    setLoadingCycles(true);
    api
      .getBillingCycles(String(filters.accountId))
      .then((res) => {
        if (!cancelled) setBillingCycles(res.data || []);
      })
      .catch(() => {
        if (!cancelled) setBillingCycles([]);
      })
      .finally(() => {
        if (!cancelled) setLoadingCycles(false);
      });
    return () => {
      cancelled = true;
    };
  }, [filters.accountId, hasBillingDayFilter]);

  const updateFilter = (key: string, value: string) => {
    setFilters((f) => ({ ...f, [key]: value, page: 1 }));
    setSelected(new Set());
  };

  const toggleSort = (col: string) => {
    setFilters((f) => ({
      ...f,
      sortBy: col,
      sortOrder: f.sortBy === col && f.sortOrder === "DESC" ? "ASC" : "DESC",
    }));
    setSelected(new Set());
  };

  const goToPage = (page: number) => {
    setFilters((f) => ({ ...f, page }));
    setSelected(new Set());
  };

  const SortIcon = ({ col }: { col: string }) => {
    if (filters.sortBy !== col) return null;
    return filters.sortOrder === "ASC" ? (
      <ChevronUp size={14} />
    ) : (
      <ChevronDown size={14} />
    );
  };

  const handleCategoryChange = useCallback(
    async (txnId: string, categoryId: string, txn: Transaction) => {
      try {
        await api.updateTransaction(txnId, {
          categoryId: categoryId || null,
          tags: txn.tags || [],
          notes: txn.notes || "",
          payeeId: txn.payeeId || null,
        });
        setData((prev) => ({
          ...prev,
          data: prev.data.map((t) => {
            if (t.id !== txnId) return t;
            const cat = categoriesRef.current.find((c) => c.id === categoryId);
            return {
              ...t,
              categoryId,
              categoryName: cat?.name || "",
              categoryColor: cat?.color || "",
              categoryIcon: cat?.icon || "",
            };
          }),
        }));
      } catch (err) {
        console.error(err);
      }
    },
    [],
  );

  const handlePayeeChange = useCallback(
    async (txnId: string, payeeId: string, txn: Transaction) => {
      try {
        if (txn.payeeId === payeeId) return;

        await api.updateTransaction(txnId, {
          categoryId: txn.categoryId,
          tags: txn.tags || [],
          notes: txn.notes || "",
          payeeId: payeeId || null,
        });
        setData((prev) => ({
          ...prev,
          data: prev.data.map((t) => {
            if (t.id !== txnId) return t;
            const p = payeesRef.current.find((p) => p.id === payeeId);
            return { ...t, payeeId, payee: p?.name || "" };
          }),
        }));
      } catch (err) {
        console.error(err);
      }
    },
    [],
  );

  const handleBulkCategorize = async (categoryId: string) => {
    if (selected.size === 0) return;
    try {
      await api.bulkCategorize({ transactionIds: [...selected], categoryId });
      loadTransactions();
      setSelected(new Set());
    } catch (err) {
      console.error(err);
    }
  };

  const handleBulkUpdatePayee = async (payeeId: string) => {
    if (selected.size === 0) return;
    try {
      await api.bulkUpdatePayee({ transactionIds: [...selected], payeeId });
      loadTransactions();
      setSelected(new Set());
    } catch (err) {
      console.error(err);
    }
  };

  const handleBulkSetBillingCycle = async (billingCycleId: string) => {
    if (selected.size === 0) return;
    try {
      await api.bulkUpdateBillingCycle({
        transactionIds: [...selected],
        billingCycleId,
      });
      loadTransactions();
      setSelected(new Set());
    } catch (err) {
      console.error(err);
    }
  };

  const handleBulkDelete = () => {
    if (selected.size === 0) return;
    setBulkDeleteOpen(true);
  };

  const confirmBulkDelete = async () => {
    try {
      await api.bulkDeleteTransactions({ transactionIds: [...selected] });
      loadTransactions();
      setSelected(new Set());
    } catch (err) {
      console.error(err);
    }
  };

  const handleDelete = useCallback((id: string) => {
    setDeleteTxnId(id);
  }, []);

  const confirmDelete = useCallback(
    async (id: string) => {
      try {
        await api.deleteTransaction(id);
        loadTransactions();
      } catch (err) {
        console.error(err);
      }
    },
    [loadTransactions],
  );

  const handleUnlink = useCallback(
    (t: Transaction) => {
      if ((t.linkCount ?? 0) > 1 || !t.linkId) {
        navigate("/linking");
        return;
      }
      setUnlinkTxn(t);
    },
    [navigate],
  );

  const confirmUnlink = useCallback(
    async (t: Transaction) => {
      if (!t.linkId) return;
      try {
        await api.deleteLink(t.linkId);
        loadTransactions();
      } catch (err) {
        console.error(err);
      }
    },
    [loadTransactions],
  );

  const toggleSelect = useCallback((id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  }, []);

  const toggleSelectAll = useCallback(() => {
    setSelected((prev) => {
      if (prev.size === data.data.length && data.data.length > 0) {
        return new Set();
      } else {
        return new Set(data.data.map((t) => t.id));
      }
    });
  }, [data.data]);

  return (
    <>
      <div className="shrink-0 px-8 pt-6">
        <div className="flex items-start justify-between">
          <div>
            <h1 className="text-2xl font-bold mb-1">Transactions</h1>
            <p className="text-muted-foreground text-sm">
              {data.total.toLocaleString()} transaction
              {data.total === 1 ? "" : "s"}
              {isFiltered ? " matching your filters" : " across all accounts"}
            </p>
          </div>
          <Button
            className="px-4 shadow-lg shadow-primary/20"
            onClick={() => setCreating(true)}
          >
            <Plus size={16} />
            Add Transaction
          </Button>
        </div>
      </div>
      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto w-full">
        {/* Filters */}
        <div className={`relative w-full ${compactLayout ? "mb-3" : "mb-5"}`}>
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
          <Input
            className={`pl-9 ${compactLayout ? "h-8" : "h-10"} bg-background`}
            placeholder="Search descriptions..."
            value={filters.search}
            onChange={(e) => updateFilter("search", e.target.value)}
          />
        </div>
        <div
          className={`flex flex-wrap items-center ${compactLayout ? "gap-2 mb-3" : "gap-3 mb-5"}`}
        >
          <Select
            value={String(filters.accountId || "all")}
            onValueChange={(v) => updateFilter("accountId", v === "all" ? "" : v)}
          >
            <SelectTrigger className={`${compactLayout ? "h-8" : "h-10"} bg-background`}>
              <SelectValue placeholder="All Accounts" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All Accounts</SelectItem>
              {accounts.map((a) => (
                <SelectItem key={a.id} value={a.id}>
                  {a.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Select
            value={String(filters.groupId || filters.categoryId || "all")}
            onValueChange={(v) => {
              if (v === "all") {
                updateFilter("categoryId", "");
                updateFilter("groupId", "");
              } else if (groupIds.has(v)) {
                updateFilter("categoryId", "");
                updateFilter("groupId", v);
              } else {
                updateFilter("groupId", "");
                updateFilter("categoryId", v);
              }
            }}
          >
            <SelectTrigger className={`${compactLayout ? "h-8" : "h-10"} bg-background`}>
              <SelectValue placeholder="All Categories" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All Categories</SelectItem>
              <SelectItem value="uncategorized" className="font-semibold">
                Uncategorized
              </SelectItem>
              {categorySections.map((s) => (
                <SelectGroup key={s.group.id}>
                  <SelectItem value={s.group.id} className="font-semibold">
                    <span className="flex items-center gap-2">
                      <Folder size={12} className="text-muted-foreground" />
                      {s.group.name}
                    </span>
                  </SelectItem>
                  {s.items.map((c) => (
                    <SelectItem key={c.id} value={c.id}>
                      {c.name}
                    </SelectItem>
                  ))}
                </SelectGroup>
              ))}
            </SelectContent>
          </Select>
          <Select
            value={String(filters.payeeId || "all")}
            onValueChange={(v) => updateFilter("payeeId", v === "all" ? "" : v)}
          >
            <SelectTrigger className={`${compactLayout ? "h-8" : "h-10"} bg-background`}>
              <SelectValue placeholder="All Payees" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All Payees</SelectItem>
              {payees.map((p) => (
                <SelectItem key={p.id} value={p.id}>
                  {p.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Select
            value={String(filters.type || "all")}
            onValueChange={(v) => updateFilter("type", v === "all" ? "" : v)}
          >
            <SelectTrigger className={`${compactLayout ? "h-8" : "h-10"} bg-background`}>
              <SelectValue placeholder="All Types" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All Types</SelectItem>
              <SelectItem value="debit">Debit</SelectItem>
              <SelectItem value="credit">Credit</SelectItem>
            </SelectContent>
          </Select>
          <Select
            value={String(filters.linked || "all")}
            onValueChange={(v) => updateFilter("linked", v === "all" ? "" : v)}
          >
            <SelectTrigger className={`${compactLayout ? "h-8" : "h-10"} bg-background`}>
              <SelectValue placeholder="All Link Status" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All Link Status</SelectItem>
              <SelectItem value="true">Linked Only</SelectItem>
              <SelectItem value="false">Not Linked Only</SelectItem>
            </SelectContent>
          </Select>
          <Input
            type="date"
            className={`w-auto ${compactLayout ? "h-9" : "h-10"} bg-background`}
            value={filters.dateFrom}
            onChange={(e) => updateFilter("dateFrom", e.target.value)}
            title="From date"
          />
          <Input
            type="date"
            className={`w-auto ${compactLayout ? "h-9" : "h-10"} bg-background`}
            value={filters.dateTo}
            onChange={(e) => updateFilter("dateTo", e.target.value)}
            title="To date"
          />

          {/* Page size control, floated right to stay visually separate */}
          <div className={`ml-auto flex items-center gap-2`}>
            <label className="text-sm text-muted-foreground">Rows per page</label>
            <Select value={preset} onValueChange={handlePresetChange}>
              <SelectTrigger className={`${compactLayout ? "h-8" : "h-9"} bg-background cursor-pointer`}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {PAGE_SIZE_OPTIONS.map((o) => (
                  <SelectItem key={o} value={String(o)}>
                    {o}
                  </SelectItem>
                ))}
                <SelectItem value="custom">Custom...</SelectItem>
              </SelectContent>
            </Select>
            {preset === "custom" && (
              <Input
                type="number"
                min="1"
                max="1000"
                value={customInput}
                onChange={(e) => setCustomInput(e.target.value)}
                onBlur={commitCustom}
                onKeyDown={(e) => e.key === "Enter" && commitCustom()}
                placeholder="Custom"
                className={`w-24 ${compactLayout ? "h-8" : "h-9"} bg-background`}
              />
            )}
          </div>
        </div>

        {/* Bulk actions */}
        {selected.size > 0 && (
          <div className="flex items-center gap-3 px-4 py-3 bg-primary/10 border border-primary/20 rounded-lg mb-4">
            <Tags size={16} className="text-primary" />
            <span className="text-sm font-medium text-foreground">
              {selected.size} selected
            </span>
            <select
              className="px-3 py-1.5 bg-background border border-border rounded text-foreground text-[13px] focus:outline-none focus:border-primary transition-all ml-2"
              onChange={(e) => {
                if (e.target.value) handleBulkCategorize(e.target.value);
                e.target.value = "";
              }}
            >
              <option value="">Categorize as...</option>
              <option
                value="uncategorized"
                className="bg-popover text-muted-foreground font-semibold"
              >
                Uncategorized
              </option>
              {categorySections.map((s) => (
                <optgroup
                  key={s.group.id}
                  label={s.group.name}
                  className="bg-popover text-muted-foreground"
                >
                  {s.items.map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.name}
                    </option>
                  ))}
                </optgroup>
              ))}
            </select>
            <select
              className="px-3 py-1.5 bg-background border border-border rounded text-foreground text-[13px] focus:outline-none focus:border-primary transition-all ml-2"
              onChange={(e) => {
                if (e.target.value) handleBulkUpdatePayee(e.target.value);
                e.target.value = "";
              }}
            >
              <option value="">Set Payee...</option>
              {payees.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
            {hasBillingDayFilter && (
              <select
                className="px-3 py-1.5 bg-background border border-border rounded text-foreground text-[13px] focus:outline-none focus:border-primary transition-all ml-2"
                onChange={(e) => {
                  if (e.target.value) handleBulkSetBillingCycle(e.target.value);
                  e.target.value = "";
                }}
              >
                <option value="">
                  {loadingCycles
                    ? "Loading billing cycles..."
                    : "Set Billing Cycle..."}
                </option>
                {billingCycles.map((bc) => (
                  <option key={bc.id} value={bc.id}>
                    {bc.label} ({formatDate(bc.startDate)} –{" "}
                    {formatDate(bc.endDate)})
                  </option>
                ))}
              </select>
            )}
            <Button
              variant="ghost"
              size="sm"
              className="text-destructive hover:text-destructive ml-2"
              onClick={handleBulkDelete}
            >
              <Trash2 size={14} />
              Delete
            </Button>
            <Button
              variant="ghost"
              size="sm"
              className="ml-auto"
              onClick={() => setSelected(new Set())}
            >
              Clear
            </Button>
          </div>
        )}

{/* Table */}
        <div className="bg-card border border-border rounded-xl overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead
                  className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} h-auto text-xs font-semibold uppercase tracking-wider text-muted-foreground bg-muted/50 w-10`}
                >
                  <Checkbox
                    checked={
                      data.data.length > 0 && selected.size === data.data.length
                    }
                    onCheckedChange={() => toggleSelectAll()}
                  />
                </TableHead>
                <TableHead
                  className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} h-auto text-xs font-semibold uppercase tracking-wider text-muted-foreground bg-muted/50 whitespace-nowrap cursor-pointer hover:text-foreground select-none`}
                  onClick={() => toggleSort("date")}
                >
                  Date <SortIcon col="date" />
                </TableHead>
                <TableHead
                  className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} h-auto text-xs font-semibold uppercase tracking-wider text-muted-foreground bg-muted/50 whitespace-nowrap cursor-pointer hover:text-foreground select-none`}
                  onClick={() => toggleSort("description")}
                >
                  Description <SortIcon col="description" />
                </TableHead>
                <TableHead
                  className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} h-auto text-xs font-semibold uppercase tracking-wider text-muted-foreground bg-muted/50 whitespace-nowrap`}
                >
                  Payee
                </TableHead>
                <TableHead
                  className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} h-auto text-xs font-semibold uppercase tracking-wider text-muted-foreground bg-muted/50 whitespace-nowrap`}
                >
                  Account
                </TableHead>
                <TableHead
                  className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} h-auto text-xs font-semibold uppercase tracking-wider text-muted-foreground bg-muted/50 whitespace-nowrap`}
                >
                  Category
                </TableHead>
                <TableHead
                  className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} h-auto text-xs font-semibold uppercase tracking-wider text-muted-foreground bg-muted/50 whitespace-nowrap cursor-pointer hover:text-foreground select-none text-right`}
                  onClick={() => toggleSort("amount")}
                >
                  Amount <SortIcon col="amount" />
                </TableHead>
                <TableHead
                  className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} h-auto text-xs font-semibold uppercase tracking-wider text-muted-foreground bg-muted/50 w-12.5`}
                ></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {loading ? (
                <TableRow>
                  <TableCell colSpan={8} className="text-center p-10">
                    <Spinner className="mx-auto size-8 text-primary" />
                  </TableCell>
                </TableRow>
              ) : data.data.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={8} className="text-center p-10 text-muted-foreground">
                    No transactions found
                  </TableCell>
                </TableRow>
              ) : (
                data.data.map((t) => (
                  <TransactionRow
                    key={t.id}
                    t={t}
                    compactLayout={compactLayout}
                    selected={selected.has(t.id)}
                    toggleSelect={toggleSelect}
                    categoryOptions={categoryOptions}
                    payeeOptions={payeeOptions}
                    handleCategoryChange={handleCategoryChange}
                    handlePayeeChange={handlePayeeChange}
                    handleDelete={handleDelete}
                    handleUnlink={handleUnlink}
                    setLinkingTxn={setLinkingTxn}
                    setEditingTxn={setEditingTxn}
                  />
                ))
              )}
            </TableBody>
          </Table>
        </div>

        {/* Pagination */}
        {pageSize > 0 && data.pages > 1 && (
          <div className="flex items-center justify-between mt-4 mb-4">
            <div className="text-sm text-muted-foreground">
              Page {data.page} of {data.pages} ({data.total} total)
            </div>
            <div className="flex gap-1.5">
              <Button
                size="sm"
                variant="ghost"
                disabled={data.page <= 1}
                onClick={() => goToPage(data.page - 1)}
              >
                Prev
              </Button>
              {Array.from({ length: Math.min(data.pages, 7) }, (_, i) => {
                let pageNum: number;
                if (data.pages <= 7) {
                  pageNum = i + 1;
                } else if (data.page <= 4) {
                  pageNum = i + 1;
                } else if (data.page >= data.pages - 3) {
                  pageNum = data.pages - 6 + i;
                } else {
                  pageNum = data.page - 3 + i;
                }
                return (
                  <Button
                    key={pageNum}
                    size="sm"
                    variant={data.page === pageNum ? "default" : "ghost"}
                    onClick={() => goToPage(pageNum)}
                  >
                    {pageNum}
                  </Button>
                );
              })}
              <Button
                size="sm"
                variant="ghost"
                disabled={data.page >= data.pages}
                onClick={() => goToPage(data.page + 1)}
              >
                Next
              </Button>
            </div>
          </div>
        )}
      </div>
      {linkingTxn && (
        <LinkTransactionModal
          txn={linkingTxn}
          onClose={() => setLinkingTxn(null)}
          onSuccess={() => {
            setLinkingTxn(null);
            loadTransactions();
          }}
        />
      )}
      {creating && (
        <EditTransactionModal
          accounts={accounts}
          categories={categories}
          groups={groups}
          payees={payees}
          onClose={() => setCreating(false)}
          onSaved={() => {
            setCreating(false);
            loadTransactions();
          }}
        />
      )}
      {editingTxn && (
        <EditTransactionModal
          transaction={editingTxn}
          accounts={accounts}
          categories={categories}
          groups={groups}
          payees={payees}
          onClose={() => setEditingTxn(null)}
          onSaved={() => {
            setEditingTxn(null);
            loadTransactions();
          }}
        />
      )}
      <AlertDialog
        open={deleteTxnId !== null}
        onOpenChange={(open) => !open && setDeleteTxnId(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this transaction?</AlertDialogTitle>
            <AlertDialogDescription>
              This will permanently delete the transaction. This action cannot
              be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                const id = deleteTxnId;
                setDeleteTxnId(null);
                if (id) confirmDelete(id);
              }}
            >
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      <AlertDialog open={bulkDeleteOpen} onOpenChange={setBulkDeleteOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              Delete {selected.size} selected transactions?
            </AlertDialogTitle>
            <AlertDialogDescription>
              This will permanently delete the selected transactions. This
              action cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                setBulkDeleteOpen(false);
                confirmBulkDelete();
              }}
            >
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      <AlertDialog
        open={unlinkTxn !== null}
        onOpenChange={(open) => !open && setUnlinkTxn(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Unlink these transactions?</AlertDialogTitle>
            <AlertDialogDescription>
              This will remove the link between these transactions.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                const t = unlinkTxn;
                setUnlinkTxn(null);
                if (t) confirmUnlink(t);
              }}
            >
              Unlink
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
