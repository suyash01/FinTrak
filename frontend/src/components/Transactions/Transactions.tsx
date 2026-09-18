import { useState, useEffect, useCallback, useMemo, useRef } from "react";
import { useSearchParams } from "react-router-dom";
import {
  type OnChangeFn,
  type PaginationState,
  type RowSelectionState,
  type SortingState,
} from "@/lib/react-table";
import { Plus } from "lucide-react";
import LinkTransactionModal from "./LinkTransactionModal";
import EditTransactionModal from "./EditTransactionModal";
import TraceChainModal from "./TraceChainModal";
import { DataTable, DataTablePagination } from "@/components/ui/data-table";
import { Button } from "@/components/ui/button";
import { toastApiError } from "../../lib/errors";
import api from "../../api/client";
import { useSettings } from "../../context/SettingsContext";
import { useDomainData } from "../../context/DomainDataContext";
import type {
  Transaction,
  BillingCycle,
  RecurringSeries,
  TransactionsResponse,
  QueryParams,
} from "../../types";
import { buildCategorySections } from "../../lib/categories";
import { useTransactionColumns } from "./useTransactionColumns";
import BulkActionBar, {
  UNLINK_LOAN,
  UNLINK_RECURRING,
} from "./BulkActionBar";
import TransactionFilters from "./TransactionFilters";
import DeleteTransactionDialogs from "./DeleteTransactionDialogs";
import {
  URL_PARAMS,
  DEFAULT_FILTERS,
  PAGE_SIZE_OPTIONS,
  MAX_PAGE_SIZE,
  PAGE_SIZE_LS_KEY,
} from "./transactionConstants";

export default function Transactions() {
  const {
    accounts,
    categories,
    groups,
    payees,
    settings,
    setSettings,
  } = useDomainData();
  const [searchParams, setSearchParams] = useSearchParams();
  const [data, setData] = useState<TransactionsResponse>({
    data: [],
    total: 0,
    page: 1,
    pages: 0,
  });
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [linkingTxn, setLinkingTxn] = useState<Transaction | null>(null);
  const [editingTxn, setEditingTxn] = useState<Transaction | null>(null);
  const [tracingTxn, setTracingTxn] = useState<Transaction | null>(null);
  const [creating, setCreating] = useState(false);
  const [deleteTxnId, setDeleteTxnId] = useState<string | null>(null);
  const [bulkDeleteOpen, setBulkDeleteOpen] = useState(false);
  const [billingCycles, setBillingCycles] = useState<BillingCycle[]>([]);
  const [loadingCycles, setLoadingCycles] = useState(false);
  const { compactLayout } = useSettings();

  // Page size: remembered locally and persisted against the user's account.
  const savedPageSize = () => {
    const raw = localStorage.getItem(PAGE_SIZE_LS_KEY);
    if (raw === null) return 50;
    const v = Number(raw);
    if (Number.isNaN(v)) return 50;
    return Math.min(Math.max(v, 1), MAX_PAGE_SIZE);
  };
  const initialPageSize = useMemo(savedPageSize, []);
  const [pageSize, setPageSize] = useState(initialPageSize);
  const [preset, setPreset] = useState(() =>
    PAGE_SIZE_OPTIONS.includes(initialPageSize) ? String(initialPageSize) : "custom",
  );
  const [customInput, setCustomInput] = useState(() => String(initialPageSize));

  // Refs to avoid closures in callbacks
  const categoriesRef = useRef(categories);
  const payeesRef = useRef(payees);
  const abortRef = useRef<AbortController | null>(null);
  // Per-transaction edit sequence. Each inline save bumps the transaction's
  // counter and only applies its optimistic update if it is still the newest
  // request for that row, so a slow earlier response can't clobber a later edit.
  const editSeqRef = useRef<Map<string, number>>(new Map());
  const nextEditSeq = (txnId: string): number => {
    const seq = (editSeqRef.current.get(txnId) ?? 0) + 1;
    editSeqRef.current.set(txnId, seq);
    return seq;
  };
  const isLatestEdit = (txnId: string, seq: number): boolean =>
    editSeqRef.current.get(txnId) === seq;
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
  // Grouped options for the inline row picker. Group headings are plain
  // optgroup labels and are not selectable.
  const categoryOptionGroups = useMemo(
    () =>
      categorySections.map((s) => ({
        label: s.group.name,
        options: s.items.map((c) => ({ value: c.id, label: c.name })),
      })),
    [categorySections],
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
    // React to external URL changes only; the setters/state read above are
    // stable and including them would re-sync on state we just wrote.
  }, [searchParams]);

  // Sync filter limit when pageSize changes in settings
  useEffect(() => {
    setFilters((f) => ({ ...f, limit: pageSize || 0, page: 1 }));
    setSelected(new Set());
  }, [pageSize]);

  // Restore the persisted page size from the shared user settings.
  useEffect(() => {
    if (!settings || typeof settings.pageSize !== "number") return;
    const clamped = Math.min(Math.max(settings.pageSize, 1), MAX_PAGE_SIZE);
    setPageSize(clamped);
    if (PAGE_SIZE_OPTIONS.includes(clamped)) {
      setPreset(String(clamped));
    } else {
      setPreset("custom");
      setCustomInput(String(clamped));
    }
  }, [settings]);

  const applyPageSize = (size: number) => {
    const n = Number(size);
    if (!Number.isFinite(n)) return;
    const clamped = Math.min(Math.max(n, 1), MAX_PAGE_SIZE);
    setPageSize(clamped);
    localStorage.setItem(PAGE_SIZE_LS_KEY, String(clamped));
    setSettings((prev) => ({ ...(prev ?? {}), pageSize: clamped }));
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
      // A loan account owns no transactions: selecting one in the account
      // filter lists its attached EMI payments instead (loanAccountId).
      if (params.accountId) {
        const loanAcc = accounts.find((a) => a.id === params.accountId);
        if (loanAcc?.accountTypeId === "loan") {
          params.loanAccountId = params.accountId;
          delete params.accountId;
        }
      }
      const res = await api.getTransactions(params, {
        signal: controller.signal,
      });
      if (abortRef.current === controller) setData(res);
    } catch (err) {
      if ((err as Error).name !== "AbortError") toastApiError(err);
    } finally {
      if (abortRef.current === controller) setLoading(false);
    }
  }, [filters, accounts]);

  useEffect(() => {
    const timer = setTimeout(loadTransactions, 300);
    return () => clearTimeout(timer);
  }, [loadTransactions]);

  // Pre-fill the account filter with the user's default account once the shared
  // account list is available and no account filter was explicitly requested.
  useEffect(() => {
    const def = accounts.find((a) => a.isDefault);
    if (def && !searchParams.get("accountId")) {
      setFilters((f) => ({ ...f, accountId: def.id, page: 1 }));
      setSelected(new Set());
    }
    // Run once the shared account list arrives; reading searchParams here is
    // only to detect an explicit filter, so it isn't a dependency.
  }, [accounts]);

  // Bulk billing-cycle assignment is only offered when the account filter is a
  // single account with a billing day (cycles are per-account, so this
  // guarantees all selected transactions belong to the same account).
  const selectedAccount = accounts.find((a) => a.id === filters.accountId);
  const hasBillingDayFilter = Boolean(selectedAccount?.billingDay);

  // Loan/EMI targets for the bulk "Link to Loan" action.
  const loanAccounts = useMemo(
    () => accounts.filter((a) => a.accountTypeId === "loan"),
    [accounts],
  );
  // Recurring subscriptions for the bulk "Link to subscription" action.
  const [recurringSeries, setRecurringSeries] = useState<RecurringSeries[]>([]);
  // Account id -> closed flag, so row actions can hide editing/deleting on
  // closed accounts (only linking stays possible).
  const closedById = useMemo(() => {
    const m = new Map<string, boolean>();
    for (const a of accounts) m.set(a.id, a.closed);
    return m;
  }, [accounts]);

  // Load subscriptions once for the bulk link action; non-critical if it fails.
  useEffect(() => {
    let cancelled = false;
    api
      .getRecurringSeries()
      .then((res) => {
        if (!cancelled) setRecurringSeries(res.data || []);
      })
      .catch(() => {
        /* the bulk action is simply not offered */
      });
    return () => {
      cancelled = true;
    };
  }, []);

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

  // Derived TanStack state (server-side sorting/pagination, client-side
  // selection) plus the handlers that write back into `filters`/`selected`.
  const sorting = useMemo<SortingState>(
    () => [
      {
        id: String(filters.sortBy || "date"),
        desc: filters.sortOrder === "ASC" ? false : true,
      },
    ],
    [filters.sortBy, filters.sortOrder],
  );
  const sortingRef = useRef(sorting);
  useEffect(() => {
    sortingRef.current = sorting;
  }, [sorting]);

  const pagination = useMemo<PaginationState>(
    () => ({ pageIndex: Number(filters.page) - 1, pageSize }),
    [filters.page, pageSize],
  );
  const paginationRef = useRef(pagination);
  useEffect(() => {
    paginationRef.current = pagination;
  }, [pagination]);

  const rowSelection = useMemo<RowSelectionState>(() => {
    const obj: RowSelectionState = {};
    selected.forEach((id) => (obj[id] = true));
    return obj;
  }, [selected]);
  const rowSelectionRef = useRef(rowSelection);
  useEffect(() => {
    rowSelectionRef.current = rowSelection;
  }, [rowSelection]);

  const onSortingChange: OnChangeFn<SortingState> = useCallback((updater) => {
    const next =
      typeof updater === "function" ? updater(sortingRef.current) : updater;
    const col = next[0];
    setFilters((f) => ({
      ...f,
      sortBy: col?.id ?? "date",
      sortOrder: col?.desc ? "DESC" : "ASC",
      page: 1,
    }));
    setSelected(new Set());
  }, []);

  const onPaginationChange: OnChangeFn<PaginationState> = useCallback(
    (updater) => {
      const next =
        typeof updater === "function"
          ? updater(paginationRef.current)
          : updater;
      if (next.pageSize !== paginationRef.current.pageSize) {
        applyPageSize(next.pageSize);
      }
      setFilters((f) => ({ ...f, page: next.pageIndex + 1 }));
      setSelected(new Set());
    },
    [],
  );

  const onRowSelectionChange: OnChangeFn<RowSelectionState> = useCallback(
    (updater) => {
      const next =
        typeof updater === "function"
          ? updater(rowSelectionRef.current)
          : updater;
      setSelected(new Set(Object.keys(next).filter((k) => next[k])));
    },
    [],
  );

  const handleCategoryChange = useCallback(
    async (txnId: string, categoryId: string, txn: Transaction) => {
      const seq = nextEditSeq(txnId);
      try {
        await api.updateTransaction(txnId, {
          categoryId: categoryId || null,
          tags: txn.tags || [],
          notes: txn.notes || "",
          payeeId: txn.payeeId || null,
        });
        // Ignore a response that a newer edit for the same row superseded.
        if (!isLatestEdit(txnId, seq)) return;
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
        if (!isLatestEdit(txnId, seq)) return;
        // Revert the optimistic cell change and tell the user.
        setData((prev) => ({
          ...prev,
          data: prev.data.map((t) =>
            t.id === txnId
              ? {
                  ...t,
                  categoryId: txn.categoryId,
                  categoryName: txn.categoryName,
                  categoryColor: txn.categoryColor,
                  categoryIcon: txn.categoryIcon,
                }
              : t,
          ),
        }));
        toastApiError(err);
      }
    },
    [],
  );

  const handlePayeeChange = useCallback(
    async (txnId: string, payeeId: string, txn: Transaction) => {
      if (txn.payeeId === payeeId) return;
      const seq = nextEditSeq(txnId);
      try {
        await api.updateTransaction(txnId, {
          categoryId: txn.categoryId,
          tags: txn.tags || [],
          notes: txn.notes || "",
          payeeId: payeeId || null,
        });
        if (!isLatestEdit(txnId, seq)) return;
        setData((prev) => ({
          ...prev,
          data: prev.data.map((t) => {
            if (t.id !== txnId) return t;
            const p = payeesRef.current.find((p) => p.id === payeeId);
            return { ...t, payeeId, payee: p?.name || "" };
          }),
        }));
      } catch (err) {
        if (!isLatestEdit(txnId, seq)) return;
        setData((prev) => ({
          ...prev,
          data: prev.data.map((t) =>
            t.id === txnId ? { ...t, payeeId: txn.payeeId, payee: txn.payee } : t,
          ),
        }));
        toastApiError(err);
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
      toastApiError(err);
    }
  };

  const handleBulkUpdatePayee = async (payeeId: string) => {
    if (selected.size === 0) return;
    try {
      await api.bulkUpdatePayee({ transactionIds: [...selected], payeeId });
      loadTransactions();
      setSelected(new Set());
    } catch (err) {
      toastApiError(err);
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
      toastApiError(err);
    }
  };

  const handleBulkLinkLoan = async (value: string) => {
    if (selected.size === 0) return;
    try {
      await api.bulkLoan({
        transactionIds: [...selected],
        loanAccountId: value === UNLINK_LOAN ? null : value,
      });
      loadTransactions();
      setSelected(new Set());
    } catch (err) {
      toastApiError(err);
    }
  };

  const handleBulkLinkRecurring = async (value: string) => {
    if (selected.size === 0) return;
    try {
      if (value === UNLINK_RECURRING) {
        await api.detachRecurring({ transactionIds: [...selected] });
      } else {
        await api.attachRecurring({
          seriesId: value,
          transactionIds: [...selected],
        });
      }
      loadTransactions();
      setSelected(new Set());
    } catch (err) {
      toastApiError(err);
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
      toastApiError(err);
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
        toastApiError(err);
      }
    },
    [loadTransactions],
  );

  const pad = compactLayout ? "py-1.5 px-3" : "py-3 px-4";
  const headerBase = `${pad} h-auto text-xs font-semibold uppercase tracking-wider text-muted-foreground whitespace-nowrap`;

  const columns = useTransactionColumns({
    payeeOptions,
    categoryOptionGroups,
    closedById,
    onCategoryChange: handleCategoryChange,
    onPayeeChange: handlePayeeChange,
    onDelete: handleDelete,
    onLink: setLinkingTxn,
    onEdit: setEditingTxn,
    onTrace: setTracingTxn,
  });

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
        <TransactionFilters
          compactLayout={compactLayout}
          filters={filters}
          onFilterChange={updateFilter}
          accounts={accounts}
          payees={payees}
          categorySections={categorySections}
          groupIds={groupIds}
          preset={preset}
          customInput={customInput}
          onPresetChange={handlePresetChange}
          onCustomInputChange={setCustomInput}
          onCommitCustom={commitCustom}
        />

        {selected.size > 0 && (
          <BulkActionBar
            selectedCount={selected.size}
            categorySections={categorySections}
            payees={payees}
            hasBillingDayFilter={hasBillingDayFilter}
            loadingCycles={loadingCycles}
            billingCycles={billingCycles}
            loanAccounts={loanAccounts}
            recurringSeries={recurringSeries}
            onCategorize={handleBulkCategorize}
            onUpdatePayee={handleBulkUpdatePayee}
            onSetBillingCycle={handleBulkSetBillingCycle}
            onLinkLoan={handleBulkLinkLoan}
            onLinkRecurring={handleBulkLinkRecurring}
            onDelete={handleBulkDelete}
            onClear={() => setSelected(new Set())}
          />
        )}

        {/* Table */}
        <DataTable
          columns={columns}
          data={data.data}
          loading={loading}
          emptyMessage="No transactions found"
          getRowId={(row) => row.id}
          enableRowSelection={(row) => !row.original.isSummary}
          getRowClassName={(row) => {
            if (row.original.isSummary) return "bg-primary/10 border-border";
            return row.getIsSelected()
              ? "bg-primary/10 border-border"
              : "border-border";
          }}
          sorting={sorting}
          onSortingChange={onSortingChange}
          manualSorting
          pagination={pagination}
          onPaginationChange={onPaginationChange}
          manualPagination
          rowCount={data.total}
          pageCount={data.pages}
          rowSelection={rowSelection}
          onRowSelectionChange={onRowSelectionChange}
          containerClassName="bg-card border border-border rounded-xl overflow-x-auto"
          headerClassName={headerBase}
          cellClassName={pad}
          virtualize
          maxHeight={
            compactLayout ? "calc(100vh - 190px)" : "calc(100vh - 240px)"
          }
          estimateRowSize={compactLayout ? 33 : 45}
          footer={(table) =>
            pageSize > 0 && data.pages > 1 ? (
              <DataTablePagination table={table} />
            ) : null
          }
        />
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
      {tracingTxn && (
        <TraceChainModal
          txn={tracingTxn}
          onClose={() => setTracingTxn(null)}
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
      <DeleteTransactionDialogs
        deleteTxnId={deleteTxnId}
        onCancelDelete={() => setDeleteTxnId(null)}
        onConfirmDelete={confirmDelete}
        bulkDeleteOpen={bulkDeleteOpen}
        onBulkDeleteOpenChange={setBulkDeleteOpen}
        selectedCount={selected.size}
        onConfirmBulkDelete={confirmBulkDelete}
      />
    </>
  );
}
