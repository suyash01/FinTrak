import { useState, useEffect, useMemo } from 'react';
import { RefreshCw, Loader2, FileText, Check, AlertCircle, Search, Eye, X } from 'lucide-react';
import api from '../../api/client';
import { formatCurrency, formatDateOnly } from '../../utils/formatters';

const DATE_FORMAT_OPTIONS = [
  { value: 'auto', label: 'Auto-detect' },
  { value: 'DD/MM/YYYY', label: 'DD/MM/YYYY' },
  { value: 'MM/DD/YYYY', label: 'MM/DD/YYYY' },
  { value: 'DD/MM/YY', label: 'DD/MM/YY' },
  { value: 'YYYY-MM-DD', label: 'YYYY-MM-DD' },
  { value: 'DD Mon YYYY', label: 'DD Mon YYYY' },
];

export default function PaperlessImport() {
  const [configured, setConfigured] = useState(false);
  const [loadingConfig, setLoadingConfig] = useState(true);

  const [accounts, setAccounts] = useState([]);
  const [selectedAccount, setSelectedAccount] = useState('');

  const [extractors, setExtractors] = useState([]);
  const [extractor, setExtractor] = useState('sbi_cc');
  const [password, setPassword] = useState('');
  const [dateFormat, setDateFormat] = useState('auto');

  const [documents, setDocuments] = useState([]);
  const [loadingDocs, setLoadingDocs] = useState(false);
  const [selected, setSelected] = useState(new Set());

  // Filters
  const [search, setSearch] = useState('');
  const [correspondentFilter, setCorrespondentFilter] = useState('');
  const [documentTypeFilter, setDocumentTypeFilter] = useState('');
  const [tagFilter, setTagFilter] = useState('');

  // File preview (blob URL of the original PDF)
  const [filePreview, setFilePreview] = useState(null); // { url, title }
  const [loadingFileId, setLoadingFileId] = useState(null);

  const [preview, setPreview] = useState(null); // { title, transactions }
  const [parsing, setParsing] = useState(false);
  const [importing, setImporting] = useState(false);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');

  useEffect(() => {
    api
      .getPaperlessSettings()
      .then((s) => setConfigured(Boolean(s.paperlessUrl && s.paperlessToken)))
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
    setError('');
    try {
      const res = await api.getPaperlessDocuments();
      setDocuments(res?.documents || []);
    } catch (err) {
      setError(err.message);
    } finally {
      setLoadingDocs(false);
    }
  };

  const toggle = (id) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
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

  const filteredDocuments = useMemo(() => {
    const q = search.trim().toLowerCase();
    return documents.filter((d) => {
      if (correspondentFilter && d.correspondent !== correspondentFilter) return false;
      if (documentTypeFilter && d.documentType !== documentTypeFilter) return false;
      if (tagFilter && !(d.tags || []).includes(tagFilter)) return false;
      if (!q) return true;
      const haystack = [
        d.title,
        d.correspondent,
        d.documentType,
        `#${d.id}`,
        ...(d.tags || []),
      ]
        .filter(Boolean)
        .join(' ')
        .toLowerCase();
      return haystack.includes(q);
    });
  }, [documents, search, correspondentFilter, documentTypeFilter, tagFilter]);

  const openFilePreview = async (doc) => {
    setLoadingFileId(doc.id);
    setError('');
    try {
      const blob = await api.getPaperlessDocumentFile(doc.id);
      const url = URL.createObjectURL(blob);
      setFilePreview({ url, title: doc.title || `Document #${doc.id}` });
    } catch (err) {
      setError('Failed to load document preview: ' + err.message);
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
      setError('Please select a FinTrak account first.');
      return;
    }
    setParsing(true);
    setError('');
    setSuccess('');
    setPreview(null);

    const transactions = [];
    const titles = [];
    try {
      for (const id of selected) {
        const res = await api.importPaperlessDocument({
          documentId: id,
          extractor,
          password,
          dateFormat: dateFormat === 'auto' ? '' : dateFormat,
        });
        const doc = documents.find((d) => d.id === id);
        titles.push(doc?.title || `Document #${id}`);
        transactions.push(...(res.transactions || []));
      }
      setPreview({ title: titles.join(', '), transactions });
    } catch (err) {
      setError(err.message);
    } finally {
      setParsing(false);
    }
  };

  const confirmImport = async () => {
    if (!preview || preview.transactions.length === 0) return;
    setImporting(true);
    setError('');
    setSuccess('');
    try {
      await api.importTransactions({
        accountId: selectedAccount,
        transactions: preview.transactions,
        duplicateAction: 'keep',
      });
      setSuccess(`Imported ${preview.transactions.length} transactions.`);
      setPreview(null);
      setSelected(new Set());
      loadDocuments();
    } catch (err) {
      setError('Import failed: ' + err.message);
    } finally {
      setImporting(false);
    }
  };

  const parsedCount = useMemo(() => preview?.transactions.length || 0, [preview]);

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
          <p className="text-slate-400 text-sm">Pull statements from Paperless-ngx</p>
        </div>
        <div className="flex-1 px-8 pb-8 pt-6">
          <div className="bg-slate-900 border border-slate-800 rounded-xl p-6 max-w-[500px]">
            <div className="flex items-center gap-2 text-slate-400 mb-2">
              <AlertCircle size={18} className="text-amber-500" />
              <h3 className="text-base font-semibold text-slate-200">Paperless not configured</h3>
            </div>
            <p className="text-sm text-slate-500 mb-4">
              Set a Paperless-ngx URL and API token in <span className="text-slate-300">Settings</span> to enable pulling statements here.
            </p>
            <button
              onClick={() => (window.location.hash = '#/settings')}
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
        <p className="text-slate-400 text-sm">Select statement documents to pull and import</p>
      </div>
      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto w-full space-y-6">
        {error && (
          <div className="bg-red-500/10 border border-red-500/30 text-red-400 text-sm rounded-lg px-4 py-3">{error}</div>
        )}
        {success && (
          <div className="bg-emerald-500/10 border border-emerald-500/30 text-emerald-400 text-sm rounded-lg px-4 py-3">
            {success}
          </div>
        )}

        {/* Pull settings */}
        <div className="bg-slate-900 border border-slate-800 rounded-xl p-5">
          <h3 className="text-sm font-semibold text-slate-200 mb-4">Pull Configuration</h3>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
            <div className="flex flex-col gap-1.5">
              <label className="text-xs font-medium text-slate-400">FinTrak Account</label>
              <select
                className="px-3 py-2 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500"
                value={selectedAccount}
                onChange={(e) => setSelectedAccount(e.target.value)}
              >
                <option value="">Choose an account...</option>
                {accounts.map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name} ({a.accountTypeName}{a.bank ? `, ${a.bank}` : ''})
                  </option>
                ))}
              </select>
            </div>
            <div className="flex flex-col gap-1.5">
              <label className="text-xs font-medium text-slate-400">Extractor</label>
              <select
                className="px-3 py-2 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500"
                value={extractor}
                onChange={(e) => setExtractor(e.target.value)}
              >
                {extractors.map((ex) => (
                  <option key={ex.name} value={ex.name}>{ex.display_name}</option>
                ))}
              </select>
            </div>
            <div className="flex flex-col gap-1.5">
              <label className="text-xs font-medium text-slate-400">Date Format</label>
              <select
                className="px-3 py-2 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500"
                value={dateFormat}
                onChange={(e) => setDateFormat(e.target.value)}
              >
                {DATE_FORMAT_OPTIONS.map((o) => (
                  <option key={o.value} value={o.value}>{o.label}</option>
                ))}
              </select>
            </div>
            <div className="flex flex-col gap-1.5">
              <label className="text-xs font-medium text-slate-400">Password (optional)</label>
              <input
                type="password"
                className="px-3 py-2 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500"
                placeholder="Statement password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
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
              <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-500" />
              <input
                type="text"
                placeholder="Search title, correspondent, tag..."
                className="w-full pl-9 pr-3 py-2 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
              />
            </div>
            <select
              className="px-3 py-2 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500"
              value={correspondentFilter}
              onChange={(e) => setCorrespondentFilter(e.target.value)}
            >
              <option value="">All correspondents</option>
              {correspondentOptions.map((c) => (
                <option key={c} value={c}>{c}</option>
              ))}
            </select>
            <select
              className="px-3 py-2 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500"
              value={documentTypeFilter}
              onChange={(e) => setDocumentTypeFilter(e.target.value)}
            >
              <option value="">All document types</option>
              {documentTypeOptions.map((t) => (
                <option key={t} value={t}>{t}</option>
              ))}
            </select>
            <select
              className="px-3 py-2 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500"
              value={tagFilter}
              onChange={(e) => setTagFilter(e.target.value)}
            >
              <option value="">All tags</option>
              {tagOptions.map((t) => (
                <option key={t} value={t}>{t}</option>
              ))}
            </select>
          </div>

          {loadingDocs ? (
            <div className="flex items-center gap-2 text-sm text-slate-500 py-6">
              <Loader2 size={16} className="animate-spin" /> Loading documents...
            </div>
          ) : filteredDocuments.length === 0 ? (
            <div className="text-sm text-slate-500 py-6">
              {documents.length === 0 ? 'No documents found in Paperless.' : 'No documents match the current filters.'}
            </div>
          ) : (
            <div className="divide-y divide-slate-800 border border-slate-800 rounded-lg overflow-hidden bg-slate-950">
              {filteredDocuments.map((d) => (
                <div key={d.id} className="flex items-start gap-3 p-3 cursor-pointer hover:bg-slate-900 transition-colors" onClick={() => toggle(d.id)}>
                  <input
                    type="checkbox"
                    readOnly
                    checked={selected.has(d.id)}
                    className="mt-1 accent-cyan-500 pointer-events-none"
                  />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2 text-sm font-medium text-slate-200">
                      <FileText size={14} className="text-cyan-500 shrink-0" />
                      <span className="truncate">{d.title || `Document #${d.id}`}</span>
                    </div>
                    <div className="text-xs text-slate-500 mt-0.5 truncate">
                      #{d.id}
                      {d.correspondent ? ` · ${d.correspondent}` : ''}
                      {d.documentType ? ` · ${d.documentType}` : ''}
                      {d.added ? ` · Added ${formatDateOnly(new Date(d.added))}` : ''}
                    </div>
                    {d.tags?.length > 0 && (
                      <div className="flex flex-wrap gap-1 mt-1.5">
                        {d.tags.map((tag) => (
                          <span key={tag} className="text-[10px] uppercase bg-slate-800 text-slate-400 px-1.5 py-0.5 rounded">
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
                    {loadingFileId === d.id ? <Loader2 size={13} className="animate-spin" /> : <Eye size={13} />}
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
              {parsing ? <Loader2 size={16} className="animate-spin" /> : <Check size={16} />}
              {parsing ? 'Parsing...' : `Fetch & Parse Selected (${selected.size})`}
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
              {parsedCount} transaction(s) parsed. Review below before importing.
            </p>
            {preview.transactions.length === 0 ? (
              <div className="text-sm text-slate-500 py-4">No transactions were parsed from these documents.</div>
            ) : (
              <div className="max-h-80 overflow-y-auto border border-slate-800 rounded-lg bg-slate-950">
                <table className="w-full text-sm">
                  <thead className="sticky top-0 bg-slate-900 text-left text-xs text-slate-500">
                    <tr>
                      <th className="px-4 py-2 font-medium">Date</th>
                      <th className="px-4 py-2 font-medium">Description</th>
                      <th className="px-4 py-2 font-medium">Type</th>
                      <th className="px-4 py-2 font-medium text-right">Amount</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-slate-800">
                    {preview.transactions.slice(0, 500).map((t, i) => (
                      <tr key={i} className="hover:bg-slate-900">
                        <td className="px-4 py-2 text-slate-400">{t.date}</td>
                        <td className="px-4 py-2 text-slate-200">{t.description}</td>
                        <td className="px-4 py-2">
                          <span className={`uppercase text-[10px] px-1.5 py-0.5 rounded ${t.type === 'credit' ? 'bg-emerald-500/10 text-emerald-400' : 'bg-red-500/10 text-red-400'}`}>
                            {t.type}
                          </span>
                        </td>
                        <td className={`px-4 py-2 text-right font-medium ${t.type === 'credit' ? 'text-emerald-400' : 'text-slate-200'}`}>
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
                  onClick={confirmImport}
                  disabled={importing}
                  className="inline-flex items-center gap-2 px-4 py-2 bg-emerald-500 text-slate-950 rounded-lg text-sm font-semibold hover:bg-emerald-600 disabled:opacity-50 transition-all"
                >
                  {importing ? <Loader2 size={16} className="animate-spin" /> : <Check size={16} />}
                  {importing ? 'Importing...' : `Import ${parsedCount} transaction(s)`}
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

      {/* File preview modal */}
      {filePreview && (
        <div
          className="fixed inset-0 z-[100] bg-black/70 backdrop-blur-sm flex items-center justify-center p-4 sm:p-6"
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
                  download={filePreview.title.replace(/[^\w.-]+/g, '_') + '.pdf'}
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