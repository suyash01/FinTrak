import { useState, useEffect, useCallback } from 'react';
import { ArrowRight, Trash2, Link2, Square, CheckSquare, AlertCircle } from 'lucide-react';
import api from '../../api/client';
import { formatCurrency, formatDate } from '../../utils/formatters';

export default function Linking() {
  const [links, setLinks] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [selected, setSelected] = useState(new Set());

  const loadData = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const allLinks = await api.getLinks();
      setLinks(allLinks);
    } catch (err) {
      setError(err.message || 'Failed to load links');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadData();
  }, [loadData]);

  const handleUnlink = async (linkId) => {
    if (!confirm('Remove this link?')) return;
    try {
      await api.deleteLink(linkId);
      loadData();
    } catch (err) {
      alert(err.message);
    }
  };

  const handleBulkUnlink = async () => {
    if (selected.size === 0) return;
    if (!confirm(`Remove ${selected.size} selected links?`)) return;
    try {
      await api.bulkDeleteLinks({ ids: [...selected] });
      setSelected(new Set());
      loadData();
    } catch (err) {
      alert(err.message);
    }
  };

  const toggleSelect = (id) => {
    setSelected(prev => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const toggleSelectAll = () => {
    if (selected.size === links.length && links.length > 0) {
      setSelected(new Set());
    } else {
      setSelected(new Set(links.map(l => l.id)));
    }
  };

  const transferLinks = links.filter((l) => l.type === 'transfer');
  const cashbackLinks = links.filter((l) => l.type === 'cashback');
  const refundLinks = links.filter((l) => l.type === 'refund');

  return (
    <>
      <div className="shrink-0 px-8 pt-6 flex justify-between items-end">
        <div>
          <h1 className="text-2xl font-bold mb-1">Linked Transactions</h1>
          <p className="text-slate-400 text-sm">Review and manage linked transfers and cashbacks</p>
        </div>
        {links.length > 0 && (
          <div className="flex items-center gap-3 mb-1">
            <button 
              className="text-xs font-medium text-slate-400 hover:text-slate-200 transition-colors"
              onClick={toggleSelectAll}
            >
              {selected.size === links.length ? 'Deselect All' : 'Select All'}
            </button>
            {selected.size > 0 && (
              <button 
                className="inline-flex items-center gap-2 px-3 py-1.5 bg-red-500/10 text-red-500 border border-red-500/20 rounded-lg text-xs font-semibold hover:bg-red-500 hover:text-white transition-all"
                onClick={handleBulkUnlink}
              >
                <Trash2 size={14} />
                Remove {selected.size} Links
              </button>
            )}
          </div>
        )}
      </div>

      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto w-full">
        {loading ? (
          <div className="flex justify-center items-center py-20">
            <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-cyan-500"></div>
          </div>
        ) : error ? (
          <div className="flex flex-col items-center justify-center py-20 px-4 text-center">
            <AlertCircle className="w-12 h-12 text-red-500 mb-4 opacity-70" />
            <p className="text-red-400 text-sm mb-4">{error}</p>
            <button
              className="px-4 py-2 bg-cyan-500 hover:bg-cyan-400 text-white text-sm font-semibold rounded-lg transition-colors"
              onClick={loadData}
            >
              Retry
            </button>
          </div>
        ) : links.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-20 px-4 text-center">
            <Link2 className="w-16 h-16 text-slate-600 mb-4 opacity-50" />
            <h3 className="text-lg font-semibold mb-2">No Linked Transactions</h3>
            <p className="text-slate-400 text-sm mb-6 max-w-md">
              Transactions linked manually from the Transaction list will appear here.
            </p>
          </div>
        ) : (
          <div className="space-y-8">
            {transferLinks.length > 0 && (
              <div>
                <h4 className="text-xs font-semibold text-slate-500 uppercase tracking-widest mb-4 flex items-center gap-2">
                  <div className="w-1.5 h-1.5 rounded-full bg-blue-500"></div>
                  Transfers ({transferLinks.length})
                </h4>
                <div className="space-y-3">
                  {transferLinks.map((l) => (
                    <div 
                      key={l.id} 
                      className={`group bg-slate-900 border ${selected.has(l.id) ? 'border-cyan-500/50 bg-cyan-500/5' : 'border-slate-800'} rounded-xl p-4 flex items-center gap-4 transition-all hover:border-slate-700`}
                    >
                      <button 
                        onClick={() => toggleSelect(l.id)}
                        className={`shrink-0 transition-colors ${selected.has(l.id) ? 'text-cyan-500' : 'text-slate-600 group-hover:text-slate-400'}`}
                      >
                        {selected.has(l.id) ? <CheckSquare size={18} /> : <Square size={18} />}
                      </button>
                      <div className="flex-1 min-w-0">
                        <div className="font-medium text-sm text-slate-200 truncate">{l.fromTxn?.description}</div>
                        <div className="text-xs text-slate-400 mt-1">
                          {l.fromTxn?.accountName} · {formatDate(l.fromTxn?.date)} · <span className="text-red-500 font-medium">−{formatCurrency(l.fromTxn?.amount || 0)}</span>
                        </div>
                      </div>
                      <ArrowRight className="text-cyan-500 shrink-0 opacity-50" size={16} />
                      <div className="flex-1 min-w-0">
                        <div className="font-medium text-sm text-slate-200 truncate">{l.toTxn?.description}</div>
                        <div className="text-xs text-slate-400 mt-1">
                          {l.toTxn?.accountName} · {formatDate(l.toTxn?.date)} · <span className="text-emerald-500 font-medium">+{formatCurrency(l.toTxn?.amount || 0)}</span>
                        </div>
                      </div>
                      <button 
                        className="p-2 text-slate-500 hover:text-red-400 hover:bg-red-500/10 rounded-lg transition-colors opacity-0 group-hover:opacity-100" 
                        onClick={() => handleUnlink(l.id)}
                      >
                        <Trash2 size={16} />
                      </button>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {cashbackLinks.length > 0 && (
              <div>
                <h4 className="text-xs font-semibold text-slate-500 uppercase tracking-widest mb-4 flex items-center gap-2">
                  <div className="w-1.5 h-1.5 rounded-full bg-emerald-500"></div>
                  Cashbacks ({cashbackLinks.length})
                </h4>
                <div className="space-y-3">
                  {cashbackLinks.map((l) => (
                    <div 
                      key={l.id} 
                      className={`group bg-slate-900 border ${selected.has(l.id) ? 'border-cyan-500/50 bg-cyan-500/5' : 'border-slate-800'} rounded-xl p-4 flex items-center gap-4 transition-all hover:border-slate-700`}
                    >
                      <button 
                        onClick={() => toggleSelect(l.id)}
                        className={`shrink-0 transition-colors ${selected.has(l.id) ? 'text-cyan-500' : 'text-slate-600 group-hover:text-slate-400'}`}
                      >
                        {selected.has(l.id) ? <CheckSquare size={18} /> : <Square size={18} />}
                      </button>
                      <div className="flex-1 min-w-0">
                        <div className="font-medium text-sm text-slate-200 truncate">{l.fromTxn?.description}</div>
                        <div className="text-xs text-slate-400 mt-1">
                          {formatDate(l.fromTxn?.date)} · <span className="text-red-500 font-medium">−{formatCurrency(l.fromTxn?.amount || 0)}</span>
                        </div>
                      </div>
                      <ArrowRight className="text-cyan-500 shrink-0 opacity-50" size={16} />
                      <div className="flex-1 min-w-0">
                        <div className="font-medium text-sm text-slate-200 truncate">{l.toTxn?.description}</div>
                        <div className="text-xs text-slate-400 mt-1">
                          {formatDate(l.toTxn?.date)} · <span className="text-emerald-500 font-medium">+{formatCurrency(l.toTxn?.amount || 0)}</span>
                        </div>
                      </div>
                      <button 
                        className="p-2 text-slate-500 hover:text-red-400 hover:bg-red-500/10 rounded-lg transition-colors opacity-0 group-hover:opacity-100" 
                        onClick={() => handleUnlink(l.id)}
                      >
                        <Trash2 size={16} />
                      </button>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {refundLinks.length > 0 && (
              <div>
                <h4 className="text-xs font-semibold text-slate-500 uppercase tracking-widest mb-4 flex items-center gap-2">
                  <div className="w-1.5 h-1.5 rounded-full bg-amber-500"></div>
                  Refunds ({refundLinks.length})
                </h4>
                <div className="space-y-3">
                  {refundLinks.map((l) => (
                    <div 
                      key={l.id} 
                      className={`group bg-slate-900 border ${selected.has(l.id) ? 'border-cyan-500/50 bg-cyan-500/5' : 'border-slate-800'} rounded-xl p-4 flex items-center gap-4 transition-all hover:border-slate-700`}
                    >
                      <button 
                        onClick={() => toggleSelect(l.id)}
                        className={`shrink-0 transition-colors ${selected.has(l.id) ? 'text-cyan-500' : 'text-slate-600 group-hover:text-slate-400'}`}
                      >
                        {selected.has(l.id) ? <CheckSquare size={18} /> : <Square size={18} />}
                      </button>
                      <div className="flex-1 min-w-0">
                        <div className="font-medium text-sm text-slate-200 truncate">{l.fromTxn?.description}</div>
                        <div className="text-xs text-slate-400 mt-1">
                          {formatDate(l.fromTxn?.date)} · <span className="text-red-500 font-medium">−{formatCurrency(l.fromTxn?.amount || 0)}</span>
                        </div>
                      </div>
                      <ArrowRight className="text-cyan-500 shrink-0 opacity-50" size={16} />
                      <div className="flex-1 min-w-0">
                        <div className="font-medium text-sm text-slate-200 truncate">{l.toTxn?.description}</div>
                        <div className="text-xs text-slate-400 mt-1">
                          {formatDate(l.toTxn?.date)} · <span className="text-emerald-500 font-medium">+{formatCurrency(l.toTxn?.amount || 0)}</span>
                        </div>
                      </div>
                      <button 
                        className="p-2 text-slate-500 hover:text-red-400 hover:bg-red-500/10 rounded-lg transition-colors opacity-0 group-hover:opacity-100" 
                        onClick={() => handleUnlink(l.id)}
                      >
                        <Trash2 size={16} />
                      </button>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        )}
      </div>
    </>
  );
}
