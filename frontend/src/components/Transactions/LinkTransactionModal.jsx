import { useState, useEffect } from 'react';
import { Search, X, Link2, ArrowRight, ArrowLeft, RotateCcw, Gift } from 'lucide-react';
import api from '../../api/client';
import { formatCurrency, formatDate } from '../../utils/formatters';

export default function LinkTransactionModal({ txn, onClose, onSuccess }) {
  const [search, setSearch] = useState('');
  const [accountId, setAccountId] = useState('');
  const [accounts, setAccounts] = useState([]);
  const [results, setResults] = useState([]);
  const [loading, setLoading] = useState(false);
  const [linkType, setLinkType] = useState('');
  const [pendingTarget, setPendingTarget] = useState(null);
  const [dateFrom, setDateFrom] = useState('');
  const [dateTo, setDateTo] = useState('');
  const [matchAmount, setMatchAmount] = useState(true);
  const [excludeSameAccount, setExcludeSameAccount] = useState(true);

  useEffect(() => {
    // Initial fetch accounts
    api.getAccounts().then(setAccounts).catch(console.error);

    // Set default date range: ±14 days from txn.date
    if (txn.date) {
      const d = new Date(txn.date);
      const from = new Date(d);
      from.setDate(d.getDate() - 3);
      const to = new Date(d);
      to.setDate(d.getDate() + 3);

      const df = from.toISOString().split('T')[0];
      const dt = to.toISOString().split('T')[0];

      setDateFrom(df);
      setDateTo(dt);

      // Perform initial search with direct values to avoid stale closure
      handleSearch(df, dt);
    } else {
      handleSearch();
    }
  }, [txn.id]);

  const handleSearch = async (dFrom = dateFrom, dTo = dateTo, mAmount = matchAmount, exclAccount = excludeSameAccount) => {
    setLoading(true);
    try {
      const params = {
        search,
        accountId,
        dateFrom: dFrom,
        dateTo: dTo,
        limit: 20,
      };

      if (mAmount) {
        params.amount = txn.amount;
      }

      const res = await api.getTransactions(params);

      let filtered = res.data.filter(t => t.id !== txn.id && !t.isLinked);

      if (exclAccount) {
        filtered = filtered.filter(t => t.accountId !== txn.accountId);
      }

      setResults(filtered);
    } catch (err) {
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  const isSameAccount = (targetTxn) => {
    return targetTxn.accountId === txn.accountId;
  };

  const handleSelectTarget = (targetTxn) => {
    if (isSameAccount(targetTxn)) {
      // Same account: show confirmation step with cashback/refund choice
      setPendingTarget(targetTxn);
      setLinkType('cashback'); // default for same-account
    } else {
      // Different accounts: immediately link as transfer
      performLink(targetTxn, 'transfer');
    }
  };

  const performLink = async (targetTxn, type) => {
    try {
      let fromId = txn.id;
      let toId = targetTxn.id;

      if (txn.type === 'credit' && targetTxn.type === 'debit') {
        fromId = targetTxn.id;
        toId = txn.id;
      }

      await api.createLink({
        type: type,
        fromTxnId: fromId,
        toTxnId: toId
      });
      onSuccess();
    } catch (err) {
      alert(err.message);
    }
  };

  const handleConfirmSameAccountLink = () => {
    if (!pendingTarget || !linkType) return;
    performLink(pendingTarget, linkType);
  };

  // Pending confirmation view for same-account links
  if (pendingTarget) {
    return (
      <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-slate-950/80 backdrop-blur-sm">
        <div className="bg-slate-900 border border-slate-800 rounded-2xl w-full max-w-lg shadow-2xl overflow-hidden">
          {/* Header */}
          <div className="px-6 py-4 border-b border-slate-800 flex justify-between items-center bg-slate-900/50">
            <div className="flex items-center gap-3">
              <button onClick={() => setPendingTarget(null)} className="p-1.5 hover:bg-slate-800 rounded-full transition-colors">
                <ArrowLeft size={18} className="text-slate-400" />
              </button>
              <div>
                <h3 className="text-lg font-bold">Same Account Link</h3>
                <p className="text-xs text-slate-400 mt-0.5">Choose the link type for this connection</p>
              </div>
            </div>
            <button onClick={onClose} className="p-2 hover:bg-slate-800 rounded-full transition-colors">
              <X size={20} className="text-slate-400" />
            </button>
          </div>

          {/* Transaction pair preview */}
          <div className="px-6 py-5 border-b border-slate-800 bg-slate-800/20">
            <div className="space-y-3">
              <div className="bg-slate-950/50 border border-slate-800 rounded-xl p-3.5">
                <div className="text-[10px] font-bold text-slate-500 uppercase tracking-wider mb-1.5">Source</div>
                <div className="font-medium text-sm text-slate-200 truncate">{txn.description}</div>
                <div className="text-xs text-slate-400 mt-1">
                  {txn.accountName} · {formatDate(txn.date)} ·
                  <span className={txn.type === 'debit' ? 'text-red-500' : 'text-emerald-500'}>
                    {txn.type === 'debit' ? '−' : '+'}{formatCurrency(txn.amount)}
                  </span>
                </div>
              </div>
              <div className="flex justify-center">
                <Link2 className="text-cyan-500/50" size={18} />
              </div>
              <div className="bg-slate-950/50 border border-slate-800 rounded-xl p-3.5">
                <div className="text-[10px] font-bold text-slate-500 uppercase tracking-wider mb-1.5">Target</div>
                <div className="font-medium text-sm text-slate-200 truncate">{pendingTarget.description}</div>
                <div className="text-xs text-slate-400 mt-1">
                  {pendingTarget.accountName} · {formatDate(pendingTarget.date)} ·
                  <span className={pendingTarget.type === 'debit' ? 'text-red-500' : 'text-emerald-500'}>
                    {pendingTarget.type === 'debit' ? '−' : '+'}{formatCurrency(pendingTarget.amount)}
                  </span>
                </div>
              </div>
            </div>
          </div>

          {/* Link type selection */}
          <div className="px-6 py-5 border-b border-slate-800">
            <div className="text-[11px] font-bold text-slate-500 uppercase tracking-wider mb-3">Link Type</div>
            <div className="grid grid-cols-2 gap-3">
              <button
                onClick={() => setLinkType('cashback')}
                className={`relative flex flex-col items-center gap-2 p-4 rounded-xl border-2 transition-all ${linkType === 'cashback'
                  ? 'border-emerald-500 bg-emerald-500/10 shadow-lg shadow-emerald-500/10'
                  : 'border-slate-800 bg-slate-950/50 hover:border-slate-700'
                  }`}
              >
                <Gift size={22} className={linkType === 'cashback' ? 'text-emerald-400' : 'text-slate-500'} />
                <span className={`text-sm font-semibold ${linkType === 'cashback' ? 'text-emerald-400' : 'text-slate-400'}`}>Cashback</span>
                <span className="text-[10px] text-slate-500">Reward or cash back</span>
              </button>
              <button
                onClick={() => setLinkType('refund')}
                className={`relative flex flex-col items-center gap-2 p-4 rounded-xl border-2 transition-all ${linkType === 'refund'
                  ? 'border-amber-500 bg-amber-500/10 shadow-lg shadow-amber-500/10'
                  : 'border-slate-800 bg-slate-950/50 hover:border-slate-700'
                  }`}
              >
                <RotateCcw size={22} className={linkType === 'refund' ? 'text-amber-400' : 'text-slate-500'} />
                <span className={`text-sm font-semibold ${linkType === 'refund' ? 'text-amber-400' : 'text-slate-400'}`}>Refund</span>
                <span className="text-[10px] text-slate-500">Return or reversal</span>
              </button>
            </div>
          </div>

          {/* Actions */}
          <div className="px-6 py-4 flex items-center justify-end gap-3">
            <button
              onClick={() => setPendingTarget(null)}
              className="px-4 py-2.5 text-sm font-medium text-slate-400 hover:text-slate-200 transition-colors"
            >Back to Results</button>
            <button
              onClick={handleConfirmSameAccountLink}
              disabled={!linkType}
              className="px-6 py-2.5 bg-cyan-500 text-white rounded-lg text-sm font-bold shadow-lg shadow-cyan-500/30 transition-all hover:bg-cyan-600 hover:scale-105 active:scale-95 disabled:opacity-50 disabled:cursor-not-allowed"
            >Confirm Link</button>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-slate-950/80 backdrop-blur-sm">
      <div className="bg-slate-900 border border-slate-800 rounded-2xl w-full max-w-2xl max-h-[80vh] flex flex-col shadow-2xl overflow-hidden">
        {/* Header */}
        <div className="px-6 py-4 border-b border-slate-800 flex justify-between items-center bg-slate-900/50">
          <div>
            <h3 className="text-lg font-bold">Find Match & Link</h3>
            <p className="text-xs text-slate-400 mt-0.5">Pick a matching transaction to create a connection</p>
          </div>
          <button onClick={onClose} className="p-2 hover:bg-slate-800 rounded-full transition-colors">
            <X size={20} className="text-slate-400" />
          </button>
        </div>

        {/* Source Txn Summary */}
        <div className="px-6 py-4 bg-slate-800/20 border-b border-slate-800">
          <div className="flex items-center gap-4">
            <div className="flex-1 min-w-0">
              <div className="text-[11px] font-bold text-slate-500 uppercase tracking-wider mb-1">Source Transaction</div>
              <div className="font-medium text-slate-200 truncate">{txn.description}</div>
              <div className="text-xs text-slate-400 mt-1">
                {txn.accountName} · {formatDate(txn.date)} ·
                <span className={txn.type === 'debit' ? 'text-red-500' : 'text-emerald-500'}>
                  {txn.type === 'debit' ? '−' : '+'}{formatCurrency(txn.amount)}
                </span>
              </div>
            </div>
            <ArrowRight className="text-cyan-500 opacity-50 shrink-0" size={20} />
            <div className="flex-1 text-center py-4 border-2 border-dashed border-slate-800 rounded-xl">
              <Link2 className="w-5 h-5 text-slate-700 mx-auto mb-1" />
              <span className="text-[11px] text-slate-500">Pick match below</span>
            </div>
          </div>
        </div>

        {/* Info banner */}
        <div className="px-6 py-2.5 bg-cyan-500/5 border-b border-slate-800">
          <p className="text-[11px] text-cyan-400/70 text-center">
            <span className="font-semibold">Cross-account</span> links auto-assign as Transfer · <span className="font-semibold">Same-account</span> links let you choose Cashback or Refund
          </p>
        </div>

        {/* Search / Filters */}
        <div className="p-4 border-b border-slate-800 bg-slate-900/40">
          <div className="flex flex-col gap-4">
            <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
              <div className="relative md:col-span-3">
                <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-slate-500" />
                <input
                  className="pl-9 w-full px-3.5 py-2.5 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 transition-all placeholder:text-slate-600"
                  placeholder="Search description..."
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  onKeyDown={(e) => e.key === 'Enter' && handleSearch()}
                />
              </div>
              <div className="flex flex-row flex-wrap md:col-span-3 gap-3">
                <div className="flex items-center gap-2 bg-slate-950 border border-slate-800 rounded-lg px-3.5 py-2.5">
                  <input
                    type="checkbox"
                    id="matchAmount"
                    className="w-4 h-4 rounded border-slate-700 text-cyan-500 focus:ring-cyan-500 bg-slate-800"
                    checked={matchAmount}
                    onChange={(e) => {
                      setMatchAmount(e.target.checked);
                      handleSearch(dateFrom, dateTo, e.target.checked, excludeSameAccount);
                    }}
                  />
                  <label htmlFor="matchAmount" className="text-sm text-slate-400 cursor-pointer select-none">
                    Match Amount
                  </label>
                </div>

                <div className="flex items-center gap-2 bg-slate-950 border border-slate-800 rounded-lg px-3.5 py-2.5">
                  <input
                    type="checkbox"
                    id="excludeAccount"
                    className="w-4 h-4 rounded border-slate-700 text-cyan-500 focus:ring-cyan-500 bg-slate-800"
                    checked={excludeSameAccount}
                    onChange={(e) => {
                      setExcludeSameAccount(e.target.checked);
                      handleSearch(dateFrom, dateTo, matchAmount, e.target.checked);
                    }}
                  />
                  <label htmlFor="excludeAccount" className="text-sm text-slate-400 cursor-pointer select-none">
                    Different Account Only
                  </label>
                </div>
                <select
                  className="w-full px-3.5 py-2.5 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 transition-all"
                  value={accountId}
                  onChange={(e) => {
                    setAccountId(e.target.value);
                  }}
                >
                  <option value="">All Accounts</option>
                  {accounts.map(a => <option key={a.id} value={a.id}>{a.name}</option>)}
                </select>
              </div>
            </div>

            <div className="flex flex-col md:flex-row items-center gap-3">
              <div className="flex-1 flex items-center gap-2 p-1.5 bg-slate-950 border border-slate-800 rounded-xl w-full">
                <div className="pl-2.5 text-[10px] font-bold text-slate-600 uppercase tracking-wider shrink-0">Date Range</div>
                <input
                  type="date"
                  style={{ colorScheme: 'dark' }}
                  className="flex-1 bg-slate-900 border border-slate-800 text-slate-200 text-sm focus:outline-none p-2 rounded-lg cursor-pointer hover:border-slate-700 transition-colors"
                  value={dateFrom}
                  onChange={(e) => setDateFrom(e.target.value)}
                />
                <div className="text-slate-700 text-xs px-0.5">to</div>
                <input
                  type="date"
                  style={{ colorScheme: 'dark' }}
                  className="flex-1 bg-slate-900 border border-slate-800 text-slate-200 text-sm focus:outline-none p-2 rounded-lg cursor-pointer hover:border-slate-700 transition-colors"
                  value={dateTo}
                  onChange={(e) => setDateTo(e.target.value)}
                />
              </div>
              <button
                onClick={() => handleSearch()}
                className="w-full md:w-auto px-8 py-3 bg-cyan-500 hover:bg-cyan-600 text-white rounded-xl text-sm font-bold shadow-lg shadow-cyan-500/20 transition-all active:scale-95 shrink-0 whitespace-nowrap outline-none flex items-center justify-center gap-2"
              >
                <Search size={16} />
                Find Match
              </button>
            </div>
          </div>
        </div>

        {/* Results */}
        <div className="flex-1 overflow-y-auto p-4 custom-scrollbar">
          {loading ? (
            <div className="flex flex-col items-center justify-center p-12 text-slate-500">
              <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-cyan-500 mb-3"></div>
              <span className="text-sm">Searching...</span>
            </div>
          ) : results.length === 0 ? (
            <div className="text-center p-12 bg-slate-950/30 rounded-2xl border border-dashed border-slate-800">
              <Link2 className="w-10 h-10 text-slate-700 mx-auto mb-3 opacity-20" />
              <div className="text-slate-500 text-sm font-medium">No potential matches found</div>
              <p className="text-slate-600 text-[11px] mt-1 italic">Try adjusting your search or filters</p>
            </div>
          ) : (
            <div className="space-y-2">
              {results.map((r) => {
                const sameAccount = isSameAccount(r);
                return (
                  <div key={r.id} className="bg-slate-950/50 border border-slate-800 p-4 rounded-xl flex items-center gap-4 hover:border-cyan-500/50 hover:bg-slate-800/30 transition-all group">
                    <div className="flex-1 min-w-0">
                      <div className="font-semibold text-sm text-slate-200 truncate group-hover:text-cyan-400 transition-colors">{r.description}</div>
                      <div className="text-[12px] text-slate-500 mt-1 flex items-center gap-2 flex-wrap">
                        <span className="px-1.5 py-0.5 bg-slate-800 rounded text-slate-400">{r.accountName}</span>
                        <span>·</span>
                        <span>{formatDate(r.date)}</span>
                        <span>·</span>
                        <span className={`font-bold ${r.type === 'debit' ? 'text-red-500' : 'text-emerald-500'}`}>
                          {r.type === 'debit' ? '−' : '+'}{formatCurrency(r.amount)}
                        </span>
                        {sameAccount && (
                          <span className="px-1.5 py-0.5 bg-amber-500/10 rounded text-amber-400 text-[10px] font-semibold">Same Account</span>
                        )}
                      </div>
                    </div>
                    <button
                      onClick={() => handleSelectTarget(r)}
                      className="opacity-0 group-hover:opacity-100 px-4 py-2 bg-cyan-500 text-white rounded-lg text-xs font-bold shadow-lg shadow-cyan-500/30 transition-all hover:scale-105 active:scale-95"
                    >{sameAccount ? 'Choose Type…' : 'Link as Transfer'}</button>
                  </div>
                );
              })}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
