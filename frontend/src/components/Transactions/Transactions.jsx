import { useState, useEffect, useCallback, useMemo, memo, useRef } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { Search, ChevronUp, ChevronDown, Trash2, Tags, Link2, Link2Off, Check, Pencil, Plus } from 'lucide-react';
import LinkTransactionModal from './LinkTransactionModal';
import EditTransactionModal from './EditTransactionModal';
import api from '../../api/client';
import { formatCurrency, formatDate } from '../../utils/formatters';
import { useSettings } from '../../context/SettingsContext';

function EditableSelect({ value, options, onChange, placeholder, displayText, style }) {
  const isPlaceholder = !value;
  const isMissing = value && !options.some(o => o.value === value);

  return (
    <select
      className={`bg-transparent border-none text-[13px] outline-none focus:ring-0 w-full rounded px-1 py-0.5 cursor-pointer appearance-none hover:bg-slate-800/50 transition-all block truncate ${isPlaceholder ? 'text-slate-500 italic' : ''}`}
      style={{ ...style, backgroundImage: 'none' }}
      value={value || ''}
      onChange={(e) => onChange(e.target.value)}
      title="Click to edit"
    >
      <option value="" className="bg-slate-900 text-slate-500 not-italic">{placeholder}</option>
      {isMissing && (
        <option value={value} className="bg-slate-900 text-slate-200 not-italic" hidden>
          {displayText}
        </option>
      )}
      {options.map((o) => (
        <option key={o.value} value={o.value} className="bg-slate-900 text-slate-200 not-italic">{o.label}</option>
      ))}
    </select>
  );
}

const TransactionRow = memo(function TransactionRow({ t, compactLayout, selected, toggleSelect, categoryOptions, payeeOptions, handleCategoryChange, handlePayeeChange, handleDelete, handleUnlink, setLinkingTxn, setEditingTxn }) {
  if (t.isSummary) {
    const pad = compactLayout ? 'py-1.5 px-3' : 'py-3 px-4';
    return (
      <tr className="bg-cyan-500/10 border-b border-slate-800 last:border-0">
        <td className={pad}></td>
        <td className={`${pad} text-sm text-slate-400 whitespace-nowrap`}>{formatDate(t.date)}</td>
        <td className={`${pad} text-sm font-semibold text-cyan-300`}>{t.description}</td>
        <td className={pad}></td>
        <td className={`${pad} text-sm text-slate-400 whitespace-nowrap`}>{t.accountName}</td>
        <td className={pad}></td>
        <td className={`${pad} text-sm text-right font-bold text-slate-100 font-mono whitespace-nowrap`}>{formatCurrency(t.amount)}</td>
        <td className={pad}></td>
      </tr>
    );
  }
  return (
    <tr className={`transition-colors border-b border-slate-800 last:border-0 ${selected ? 'bg-cyan-500/10' : 'hover:bg-slate-800/30'}`}>
      <td className={`${compactLayout ? 'py-1.5 px-3' : 'py-3 px-4'}`}><input type="checkbox" className="w-4 h-4 rounded border-slate-700 bg-slate-950 text-cyan-500" checked={selected} onChange={() => toggleSelect(t.id)} /></td>
      <td className={`${compactLayout ? 'py-1.5 px-3' : 'py-3 px-4'} text-sm whitespace-nowrap`}>{formatDate(t.date)}</td>
      <td className={`${compactLayout ? 'py-1.5 px-3' : 'py-3 px-4'} text-sm max-w-[250px] overflow-hidden text-ellipsis whitespace-nowrap`} title={t.description}>{t.description}</td>
      <td className={`${compactLayout ? 'py-1.5 px-3' : 'py-3 px-4'} text-sm min-w-[100px]`}>
        <EditableSelect
          value={t.payeeId}
          options={payeeOptions}
          onChange={(val) => handlePayeeChange(t.id, val, t)}
          placeholder="No Payee"
          displayText={t.payee}
        />
      </td>
      <td className={`${compactLayout ? 'py-1.5 px-3' : 'py-3 px-4'} text-sm whitespace-nowrap`}>{t.accountName}</td>
      <td className={`${compactLayout ? 'py-1.5 px-3' : 'py-3 px-4'} text-sm`}>
        <EditableSelect
          value={t.categoryId}
          options={categoryOptions}
          onChange={(val) => handleCategoryChange(t.id, val, t)}
          placeholder="Uncategorized"
          displayText={t.categoryId ? t.categoryName : ''}
          style={t.categoryColor ? { color: t.categoryColor } : undefined}
        />
      </td>
      <td className={`${compactLayout ? 'py-1.5 px-3' : 'py-3 px-4'} text-sm text-right font-semibold whitespace-nowrap`}>
        <span className={t.type === 'debit' ? 'text-red-500' : 'text-emerald-500'}>
          {t.type === 'debit' ? '−' : '+'}{formatCurrency(t.amount)}
        </span>
      </td>
      <td className={`${compactLayout ? 'py-1.5 px-3' : 'py-3 px-4'}`}>
        <div className="flex items-center gap-1">
          <button
            className="p-1.5 text-slate-500 hover:text-cyan-400 hover:bg-cyan-500/10 rounded transition-colors"
            onClick={() => setEditingTxn(t)}
            title="Edit transaction"
          >
            <Pencil size={14} />
          </button>
          <button
            className="p-1.5 text-slate-500 hover:text-cyan-400 hover:bg-cyan-500/10 rounded transition-colors"
            onClick={() => setLinkingTxn(t)}
            title={t.isLinked ? 'Link more transactions' : 'Find match and link'}
          >
            <Link2 size={14} />
          </button>
          {t.linkCount > 0 && (
            <button
              className="p-1.5 text-emerald-500 hover:text-red-400 hover:bg-red-500/10 rounded transition-colors group"
              onClick={() => handleUnlink(t)}
              title={t.linkCount > 1 ? 'Manage links' : 'Unlink'}
            >
              <Check size={14} className="group-hover:hidden" />
              <Link2Off size={14} className="hidden group-hover:block" />
            </button>
          )}
          <button className="p-1.5 text-slate-500 hover:text-red-400 hover:bg-red-500/10 rounded transition-colors" onClick={() => handleDelete(t.id)}>
            <Trash2 size={14} />
          </button>
        </div>
      </td>
    </tr>
  );
});

const URL_PARAMS = ['search', 'accountId', 'categoryId', 'payeeId', 'type', 'dateFrom', 'dateTo', 'uncategorized', 'linked', 'sortBy', 'sortOrder', 'page'];

const PAGE_SIZE_OPTIONS = [25, 50, 100, 200, 0]; // 0 = show all
const PAGE_SIZE_LS_KEY = 'txPageSize';

export default function Transactions() {
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const [data, setData] = useState({ data: [], total: 0, page: 1, pages: 0 });
  const [loading, setLoading] = useState(true);
  const [accounts, setAccounts] = useState([]);
  const [categories, setCategories] = useState([]);
  const [payees, setPayees] = useState([]);
  const [selected, setSelected] = useState(new Set());
  const [linkingTxn, setLinkingTxn] = useState(null);
  const [editingTxn, setEditingTxn] = useState(null);
  const [creating, setCreating] = useState(false);
  const { compactLayout } = useSettings();

  // Page size: remembered locally, with an opt-in to persist against the user.
  const savedPageSize = () => {
    const v = Number(localStorage.getItem(PAGE_SIZE_LS_KEY));
    return Number.isNaN(v) ? 50 : v;
  };
  const [pageSize, setPageSize] = useState(savedPageSize);
  const [preset, setPreset] = useState(() => {
    const v = savedPageSize();
    return PAGE_SIZE_OPTIONS.includes(v) ? String(v) : 'custom';
  });
  const [customInput, setCustomInput] = useState(() => String(savedPageSize()));
  const [persistPageSize, setPersistPageSize] = useState(false);

  // Refs to avoid closures in callbacks
  const categoriesRef = useRef(categories);
  const payeesRef = useRef(payees);
  const abortRef = useRef(null);
  useEffect(() => { categoriesRef.current = categories; }, [categories]);
  useEffect(() => { payeesRef.current = payees; }, [payees]);

  // Pre-compute select options so they're stable references
  const categoryOptions = useMemo(() =>
    categories.map(c => ({ value: c.id, label: c.name })),
    [categories]
  );
  const payeeOptions = useMemo(() =>
    payees.map(p => ({ value: p.id, label: p.name })),
    [payees]
  );

  // Filters (initialized from URL search params)
  const [filters, setFilters] = useState(() => {
    const urlToFilters = {
      search: '', accountId: '', categoryId: '', payeeId: '', type: '', dateFrom: '', dateTo: '', uncategorized: '', linked: '',
      sortBy: 'date', sortOrder: 'DESC', page: 1,
    };
    URL_PARAMS.forEach((k) => {
      const v = searchParams.get(k);
      if (v !== null && v !== '') urlToFilters[k] = v;
    });
    return { ...urlToFilters, limit: pageSize || 0 };
  });

  // Keep the URL in sync with the current filters (browser back/forward friendly)
  useEffect(() => {
    const params = {};
    URL_PARAMS.forEach((k) => {
      const v = filters[k];
      if (v !== '' && v !== null && v !== undefined) params[k] = v;
    });
    const urlParams = Object.fromEntries(searchParams.entries());
    if (JSON.stringify(params) !== JSON.stringify(urlParams)) {
      setSearchParams(params, { replace: true });
    }
  }, [filters, searchParams, setSearchParams]);

  // React to external URL changes (navigation, back/forward, shared links)
  useEffect(() => {
    const urlToFilters = {};
    let changed = false;
    URL_PARAMS.forEach((k) => {
      const v = searchParams.get(k);
      const current = filters[k];
      if (v !== null && v !== '') {
        if (String(current) !== v) { urlToFilters[k] = v; changed = true; }
      } else if (current !== '' && current !== undefined && current !== null) {
        urlToFilters[k] = '';
        changed = true;
      }
    });
    if (changed) {
      setFilters((f) => ({ ...f, ...urlToFilters, page: urlToFilters.page || '1' }));
      setSelected(new Set());
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchParams]);

  // Sync filter limit when pageSize changes in settings
  useEffect(() => {
    setFilters(f => ({ ...f, limit: pageSize || 0, page: 1 }));
    setSelected(new Set());
  }, [pageSize]);

  // Restore a persisted page size from the server if the user opted in.
  useEffect(() => {
    api.getUserSettings()
      .then((s) => {
        if (typeof s.pageSize !== 'number') return;
        setPageSize(s.pageSize);
        setPersistPageSize(true);
        if (PAGE_SIZE_OPTIONS.includes(s.pageSize)) {
          setPreset(String(s.pageSize));
        } else {
          setPreset('custom');
          setCustomInput(String(s.pageSize));
        }
      })
      .catch(() => {});
  }, []);

  const applyPageSize = (size) => {
    const n = Number(size);
    if (!Number.isFinite(n) || n < 0) return;
    setPageSize(n);
    localStorage.setItem(PAGE_SIZE_LS_KEY, String(n));
    if (persistPageSize) api.updateUserSettings({ pageSize: n }).catch(() => {});
  };

  const handlePresetChange = (val) => {
    if (val === 'custom') {
      setCustomInput(String(pageSize));
      setPreset('custom');
    } else {
      setPreset(val);
      setCustomInput('');
      applyPageSize(Number(val));
    }
  };

  const commitCustom = () => {
    const n = Number(customInput);
    if (!Number.isFinite(n) || n < 0) return;
    applyPageSize(n);
  };

  const togglePersist = (checked) => {
    setPersistPageSize(checked);
    api.updateUserSettings({ pageSize: checked ? pageSize : null }).catch(() => {});
  };

  const loadTransactions = useCallback(async () => {
    const controller = new AbortController();
    abortRef.current?.abort();
    abortRef.current = controller;

    setLoading(true);
    try {
      const params = {};
      Object.entries(filters).forEach(([k, v]) => { if (v !== '' && v !== null && v !== undefined) params[k] = v; });
      const res = await api.getTransactions(params, { signal: controller.signal });
      if (abortRef.current === controller) setData(res);
    } catch (err) {
      if (err.name !== 'AbortError') console.error(err);
    } finally {
      if (abortRef.current === controller) setLoading(false);
    }
  }, [filters]);

  useEffect(() => {
    const timer = setTimeout(loadTransactions, 300);
    return () => clearTimeout(timer);
  }, [loadTransactions]);
  useEffect(() => {
    api.getAccounts().then(setAccounts).catch(console.error);
    api.getCategories().then(setCategories).catch(console.error);
    api.getPayees().then(setPayees).catch(console.error);
  }, []);

  const updateFilter = (key, value) => {
    setFilters((f) => ({ ...f, [key]: value, page: 1 }));
    setSelected(new Set());
  };

  const toggleSort = (col) => {
    setFilters((f) => ({
      ...f,
      sortBy: col,
      sortOrder: f.sortBy === col && f.sortOrder === 'DESC' ? 'ASC' : 'DESC',
    }));
    setSelected(new Set());
  };

  const goToPage = (page) => {
    setFilters((f) => ({ ...f, page }));
    setSelected(new Set());
  };

  const SortIcon = ({ col }) => {
    if (filters.sortBy !== col) return null;
    return filters.sortOrder === 'ASC' ? <ChevronUp size={14} /> : <ChevronDown size={14} />;
  };

  const handleCategoryChange = useCallback(async (txnId, categoryId, txn) => {
    try {
      await api.updateTransaction(txnId, {
        categoryId: categoryId || null,
        tags: txn.tags || [],
        notes: txn.notes || '',
        payeeId: txn.payeeId || null,
      });
      setData((prev) => ({
        ...prev,
        data: prev.data.map((t) => {
          if (t.id !== txnId) return t;
          const cat = categoriesRef.current.find((c) => c.id === categoryId);
          return { ...t, categoryId, categoryName: cat?.name || '', categoryColor: cat?.color || '', categoryIcon: cat?.icon || '' };
        }),
      }));
    } catch (err) {
      console.error(err);
    }
  }, []);

  const handlePayeeChange = useCallback(async (txnId, payeeId, txn) => {
    try {
      if (txn.payeeId === payeeId) return;

      await api.updateTransaction(txnId, {
        categoryId: txn.categoryId,
        tags: txn.tags || [],
        notes: txn.notes || '',
        payeeId: payeeId || null,
      });
      setData((prev) => ({
        ...prev,
        data: prev.data.map((t) => {
          if (t.id !== txnId) return t;
          const p = payeesRef.current.find(p => p.id === payeeId);
          return { ...t, payeeId, payee: p?.name || '' };
        }),
      }));
    } catch (err) {
      console.error(err);
    }
  }, []);

  const handleBulkCategorize = async (categoryId) => {
    if (selected.size === 0) return;
    try {
      await api.bulkCategorize({ transactionIds: [...selected], categoryId });
      loadTransactions();
      setSelected(new Set());
    } catch (err) {
      console.error(err);
    }
  };

  const handleBulkUpdatePayee = async (payeeId) => {
    if (selected.size === 0) return;
    try {
      await api.bulkUpdatePayee({ transactionIds: [...selected], payeeId });
      loadTransactions();
      setSelected(new Set());
    } catch (err) {
      console.error(err);
    }
  };

  const handleBulkDelete = async () => {
    if (selected.size === 0) return;
    if (!confirm(`Delete ${selected.size} selected transactions?`)) return;
    try {
      await api.bulkDeleteTransactions({ transactionIds: [...selected] });
      loadTransactions();
      setSelected(new Set());
    } catch (err) {
      console.error(err);
    }
  };

  const handleDelete = useCallback(async (id) => {
    if (!confirm('Delete this transaction?')) return;
    try {
      await api.deleteTransaction(id);
      loadTransactions();
    } catch (err) {
      console.error(err);
    }
  }, [loadTransactions]);

  const handleUnlink = useCallback(async (t) => {
    if (t.linkCount > 1 || !t.linkId) {
      navigate('/linking');
      return;
    }
    if (!confirm('Unlink these transactions?')) return;
    try {
      await api.deleteLink(t.linkId);
      loadTransactions();
    } catch (err) {
      console.error(err);
    }
  }, [loadTransactions, navigate]);

  const toggleSelect = useCallback((id) => {
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
            <p className="text-slate-400 text-sm">{data.total.toLocaleString()} transactions across all accounts</p>
          </div>
          <button
            className="flex items-center gap-2 px-4 py-2.5 bg-cyan-500 hover:bg-cyan-400 text-white text-sm font-semibold rounded-lg transition-all shadow-lg shadow-cyan-500/20"
            onClick={() => setCreating(true)}
          >
            <Plus size={16} />
            Add Transaction
          </button>
        </div>
      </div>
      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto w-full">
        {/* Filters */}
        <div className={`relative w-full ${compactLayout ? 'mb-3' : 'mb-5'}`}>
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-slate-500" />
          <input className={`pl-9 w-full px-3.5 ${compactLayout ? 'py-1.5' : 'py-2.5'} bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all`} placeholder="Search descriptions..." value={filters.search} onChange={(e) => updateFilter('search', e.target.value)} />
        </div>
        <div className={`flex flex-wrap items-center ${compactLayout ? 'gap-2 mb-3' : 'gap-3 mb-5'}`}>
          <select className={`px-3.5 ${compactLayout ? 'py-1.5' : 'py-2.5'} bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 transition-all`} value={filters.accountId} onChange={(e) => updateFilter('accountId', e.target.value)}>
            <option value="">All Accounts</option>
            {accounts.map((a) => <option key={a.id} value={a.id}>{a.name}</option>)}
          </select>
          <select className={`px-3.5 ${compactLayout ? 'py-1.5' : 'py-2.5'} bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 transition-all`} value={filters.categoryId} onChange={(e) => updateFilter('categoryId', e.target.value)}>
            <option value="">All Categories</option>
            {categories.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
          </select>
          <select className={`px-3.5 ${compactLayout ? 'py-1.5' : 'py-2.5'} bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 transition-all`} value={filters.payeeId} onChange={(e) => updateFilter('payeeId', e.target.value)}>
            <option value="">All Payees</option>
            {payees.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
          </select>
          <select className={`px-3.5 ${compactLayout ? 'py-1.5' : 'py-2.5'} bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 transition-all`} value={filters.type} onChange={(e) => updateFilter('type', e.target.value)}>
            <option value="">All Types</option>
            <option value="debit">Debit</option>
            <option value="credit">Credit</option>
          </select>
          <select className={`px-3.5 ${compactLayout ? 'py-1.5' : 'py-2.5'} bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 transition-all`} value={filters.uncategorized} onChange={(e) => updateFilter('uncategorized', e.target.value)}>
            <option value="">All Categories Status</option>
            <option value="true">Uncategorized Only</option>
          </select>
          <select className={`px-3.5 ${compactLayout ? 'py-1.5' : 'py-2.5'} bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 transition-all`} value={filters.linked} onChange={(e) => updateFilter('linked', e.target.value)}>
            <option value="">All Link Status</option>
            <option value="true">Linked Only</option>
            <option value="false">Not Linked Only</option>
          </select>
          <input type="date" className={`px-3.5 ${compactLayout ? 'py-1.5' : 'py-2.5'} bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 transition-all scheme-dark`} value={filters.dateFrom} onChange={(e) => updateFilter('dateFrom', e.target.value)} title="From date" />
          <input type="date" className={`px-3.5 ${compactLayout ? 'py-1.5' : 'py-2.5'} bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 transition-all scheme-dark`} value={filters.dateTo} onChange={(e) => updateFilter('dateTo', e.target.value)} title="To date" />
        </div>

        {/* Page size control */}
        <div className="flex flex-wrap items-center justify-end gap-3 mb-4">
          <div className="flex items-center gap-2">
            <label className="text-sm text-slate-400">Rows per page</label>
            <select
              value={preset}
              onChange={(e) => handlePresetChange(e.target.value)}
              className={`px-3 ${compactLayout ? 'py-1.5' : 'py-2'} bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 transition-all cursor-pointer`}
            >
              {PAGE_SIZE_OPTIONS.map((o) => (
                <option key={o} value={o}>{o === 0 ? 'Show all' : o}</option>
              ))}
              <option value="custom">Custom...</option>
            </select>
            {preset === 'custom' && (
              <input
                type="number"
                min="0"
                value={customInput}
                onChange={(e) => setCustomInput(e.target.value)}
                onBlur={commitCustom}
                onKeyDown={(e) => e.key === 'Enter' && commitCustom()}
                placeholder="Custom"
                className={`w-24 px-3 ${compactLayout ? 'py-1.5' : 'py-2'} bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 transition-all`}
              />
            )}
            <label className="flex items-center gap-1.5 text-sm text-slate-400 cursor-pointer select-none">
              <input
                type="checkbox"
                checked={persistPageSize}
                onChange={(e) => togglePersist(e.target.checked)}
                className="w-4 h-4 rounded border-slate-700 bg-slate-950 text-cyan-500 cursor-pointer"
              />
              Remember for my account
            </label>
          </div>
        </div>

        {/* Bulk actions */}
        {selected.size > 0 && (
          <div className="flex items-center gap-3 px-4 py-3 bg-cyan-500/10 border border-cyan-500/20 rounded-lg mb-4">
            <Tags size={16} className="text-cyan-500" />
            <span className="text-sm font-medium text-slate-200">{selected.size} selected</span>
            <select className="px-3 py-1.5 bg-slate-950 border border-slate-800 rounded text-slate-200 text-[13px] focus:outline-none focus:border-cyan-500 transition-all ml-2" onChange={(e) => { if (e.target.value) handleBulkCategorize(e.target.value); e.target.value = ''; }}>
              <option value="">Categorize as...</option>
              {categories.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
            </select>
            <select className="px-3 py-1.5 bg-slate-950 border border-slate-800 rounded text-slate-200 text-[13px] focus:outline-none focus:border-cyan-500 transition-all ml-2" onChange={(e) => { if (e.target.value) handleBulkUpdatePayee(e.target.value); e.target.value = ''; }}>
              <option value="">Set Payee...</option>
              {payees.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
            </select>
            <button
              className="flex items-center gap-1.5 px-3 py-1.5 text-sm text-red-500 hover:text-red-400 hover:bg-red-500/10 rounded transition-colors ml-2"
              onClick={handleBulkDelete}
            >
              <Trash2 size={14} />
              Delete
            </button>
            <button className="px-3 py-1.5 text-sm text-slate-400 hover:text-slate-200 hover:bg-slate-800 rounded transition-colors ml-auto" onClick={() => setSelected(new Set())}>Clear</button>
          </div>
        )}

        {/* Table */}
        <div className="bg-slate-900 border border-slate-800 rounded-xl overflow-x-auto">
          <table className="w-full text-left border-collapse">
            <thead>
              <tr>
                <th className={`${compactLayout ? 'py-1.5 px-3' : 'py-3 px-4'} text-xs font-semibold uppercase tracking-wider text-slate-500 bg-slate-800/50 border-b border-slate-800 w-10`}>
                  <input type="checkbox" className="w-4 h-4 rounded border-slate-700 bg-slate-950 text-cyan-500" checked={data.data.length > 0 && selected.size === data.data.length} onChange={toggleSelectAll} />
                </th>
                <th className={`${compactLayout ? 'py-1.5 px-3' : 'py-3 px-4'} text-xs font-semibold uppercase tracking-wider text-slate-500 bg-slate-800/50 border-b border-slate-800 whitespace-nowrap cursor-pointer hover:text-slate-400 select-none`} onClick={() => toggleSort('date')}>Date <SortIcon col="date" /></th>
                <th className={`${compactLayout ? 'py-1.5 px-3' : 'py-3 px-4'} text-xs font-semibold uppercase tracking-wider text-slate-500 bg-slate-800/50 border-b border-slate-800 whitespace-nowrap cursor-pointer hover:text-slate-400 select-none`} onClick={() => toggleSort('description')}>Description <SortIcon col="description" /></th>
                <th className={`${compactLayout ? 'py-1.5 px-3' : 'py-3 px-4'} text-xs font-semibold uppercase tracking-wider text-slate-500 bg-slate-800/50 border-b border-slate-800 whitespace-nowrap`}>Payee</th>
                <th className={`${compactLayout ? 'py-1.5 px-3' : 'py-3 px-4'} text-xs font-semibold uppercase tracking-wider text-slate-500 bg-slate-800/50 border-b border-slate-800 whitespace-nowrap`}>Account</th>
                <th className={`${compactLayout ? 'py-1.5 px-3' : 'py-3 px-4'} text-xs font-semibold uppercase tracking-wider text-slate-500 bg-slate-800/50 border-b border-slate-800 whitespace-nowrap`}>Category</th>
                <th className={`${compactLayout ? 'py-1.5 px-3' : 'py-3 px-4'} text-xs font-semibold uppercase tracking-wider text-slate-500 bg-slate-800/50 border-b border-slate-800 whitespace-nowrap cursor-pointer hover:text-slate-400 select-none text-right`} onClick={() => toggleSort('amount')}>Amount <SortIcon col="amount" /></th>
                <th className={`${compactLayout ? 'py-1.5 px-3' : 'py-3 px-4'} text-xs font-semibold uppercase tracking-wider text-slate-500 bg-slate-800/50 border-b border-slate-800 w-[50px]`}></th>
              </tr>
            </thead>
            <tbody>
              {loading ? (
                <tr><td colSpan={8} className="text-center p-10"><div className="animate-spin rounded-full h-8 w-8 border-b-2 border-cyan-500 mx-auto"></div></td></tr>
              ) : data.data.length === 0 ? (
                <tr><td colSpan={8} className="text-center p-10 text-slate-500">No transactions found</td></tr>
              ) : data.data.map((t) => (
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
              ))}
            </tbody>
          </table>
        </div>

        {/* Pagination */}
        {pageSize > 0 && data.pages > 1 && (
          <div className="flex items-center justify-between mt-4 mb-4">
            <div className="text-sm text-slate-400">
              Page {data.page} of {data.pages} ({data.total} total)
            </div>
            <div className="flex gap-1.5">
              <button className="px-3 py-1.5 rounded-lg text-sm font-medium transition-colors disabled:opacity-50 disabled:cursor-not-allowed hover:bg-slate-800 hover:text-slate-200 text-slate-400" disabled={data.page <= 1} onClick={() => goToPage(data.page - 1)}>Prev</button>
              {Array.from({ length: Math.min(data.pages, 7) }, (_, i) => {
                let pageNum;
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
                  <button key={pageNum} className={`px-3 py-1.5 rounded-lg text-sm font-medium transition-colors ${data.page === pageNum ? 'bg-cyan-500 text-white' : 'hover:bg-slate-800 hover:text-slate-200 text-slate-400'}`} onClick={() => goToPage(pageNum)}>
                    {pageNum}
                  </button>
                );
              })}
              <button className="px-3 py-1.5 rounded-lg text-sm font-medium transition-colors disabled:opacity-50 disabled:cursor-not-allowed hover:bg-slate-800 hover:text-slate-200 text-slate-400" disabled={data.page >= data.pages} onClick={() => goToPage(data.page + 1)}>Next</button>
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
          payees={payees}
          onClose={() => setEditingTxn(null)}
          onSaved={() => {
            setEditingTxn(null);
            loadTransactions();
          }}
        />
      )}
    </>
  );
}
