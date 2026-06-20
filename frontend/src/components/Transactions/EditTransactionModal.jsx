import { useState, useEffect, useRef } from 'react';
import { X, Save, Pencil, Calendar, DollarSign, FileText, Tag, User, Landmark, ArrowDownLeft, ArrowUpRight } from 'lucide-react';
import api from '../../api/client';

export default function EditTransactionModal({ transaction, accounts, categories, payees, onClose, onSaved }) {
  const [form, setForm] = useState({
    date: '',
    description: '',
    amount: '',
    type: 'debit',
    accountId: '',
    categoryId: '',
    payeeId: '',
    notes: '',
    tags: [],
  });
  const [tagInput, setTagInput] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [visible, setVisible] = useState(false);
  const backdropRef = useRef(null);

  useEffect(() => {
    if (transaction) {
      const d = new Date(transaction.date);
      const dateStr = d.toISOString().split('T')[0];
      setForm({
        date: dateStr,
        description: transaction.description || '',
        amount: String(transaction.amount || ''),
        type: transaction.type || 'debit',
        accountId: transaction.accountId || '',
        categoryId: transaction.categoryId || '',
        payeeId: transaction.payeeId || '',
        notes: transaction.notes || '',
        tags: transaction.tags || [],
      });
      // Trigger enter animation
      requestAnimationFrame(() => setVisible(true));
    }
  }, [transaction]);

  const handleClose = () => {
    setVisible(false);
    setTimeout(onClose, 200);
  };

  const handleBackdropClick = (e) => {
    if (e.target === backdropRef.current) handleClose();
  };

  const handleSubmit = async (e) => {
    e.preventDefault();
    setError('');
    setSaving(true);

    try {
      const payload = {
        categoryId: form.categoryId || null,
        tags: form.tags,
        notes: form.notes,
        payeeId: form.payeeId || null,
        date: form.date,
        description: form.description,
        amount: parseFloat(form.amount),
        type: form.type,
        accountId: form.accountId,
      };

      await api.updateTransaction(transaction.id, payload);
      onSaved();
    } catch (err) {
      setError(err.message || 'Failed to update transaction');
    } finally {
      setSaving(false);
    }
  };

  const addTag = () => {
    const tag = tagInput.trim();
    if (tag && !form.tags.includes(tag)) {
      setForm(f => ({ ...f, tags: [...f.tags, tag] }));
      setTagInput('');
    }
  };

  const removeTag = (tag) => {
    setForm(f => ({ ...f, tags: f.tags.filter(t => t !== tag) }));
  };

  const handleTagKeyDown = (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      addTag();
    }
  };

  if (!transaction) return null;

  const inputClass = "w-full px-3.5 py-2.5 bg-slate-950/80 border border-slate-700/60 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500/30 transition-all placeholder:text-slate-600";
  const labelClass = "block text-xs font-semibold uppercase tracking-wider text-slate-400 mb-1.5";

  return (
    <div
      ref={backdropRef}
      className={`fixed inset-0 z-50 flex justify-end transition-colors duration-200 ${visible ? 'bg-black/60 backdrop-blur-sm' : 'bg-transparent'}`}
      onClick={handleBackdropClick}
    >
      <div
        className={`w-full max-w-lg h-full bg-linear-to-b from-slate-900 to-slate-950 border-l border-slate-700/50 shadow-2xl shadow-black/50 flex flex-col transition-transform duration-200 ease-out ${visible ? 'translate-x-0' : 'translate-x-full'}`}
      >
        {/* Header */}
        <div className="shrink-0 px-6 py-5 border-b border-slate-800/80 bg-slate-900/50">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-3">
              <div className="p-2 bg-cyan-500/10 rounded-lg">
                <Pencil size={18} className="text-cyan-400" />
              </div>
              <div>
                <h2 className="text-lg font-bold text-slate-100">Edit Transaction</h2>
                <p className="text-xs text-slate-500 mt-0.5">Modify transaction details</p>
              </div>
            </div>
            <button
              className="p-2 text-slate-400 hover:text-slate-200 hover:bg-slate-800 rounded-lg transition-colors"
              onClick={handleClose}
            >
              <X size={20} />
            </button>
          </div>
        </div>

        {/* Form */}
        <form onSubmit={handleSubmit} className="flex-1 overflow-y-auto">
          <div className="px-6 py-5 space-y-6">
            {/* Error */}
            {error && (
              <div className="px-4 py-3 bg-red-500/10 border border-red-500/20 rounded-lg text-sm text-red-400">
                {error}
              </div>
            )}

            {/* Section: Core Details */}
            <div className="space-y-4">
              <div className="flex items-center gap-2 text-xs font-bold uppercase tracking-widest text-slate-500">
                <FileText size={12} />
                <span>Core Details</span>
              </div>

              {/* Description */}
              <div>
                <label className={labelClass}>Description</label>
                <input
                  type="text"
                  className={inputClass}
                  value={form.description}
                  onChange={e => setForm(f => ({ ...f, description: e.target.value }))}
                  placeholder="Transaction description"
                  required
                />
              </div>

              {/* Date & Amount row */}
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className={labelClass}>
                    <Calendar size={10} className="inline mr-1" />Date
                  </label>
                  <input
                    type="date"
                    className={`${inputClass} scheme-dark [&::-webkit-calendar-picker-indicator]:invert`}
                    value={form.date}
                    onChange={e => setForm(f => ({ ...f, date: e.target.value }))}
                    required
                  />
                </div>
                <div>
                  <label className={labelClass}>
                    <DollarSign size={10} className="inline mr-1" />Amount
                  </label>
                  <input
                    type="number"
                    step="0.01"
                    min="0"
                    className={inputClass}
                    value={form.amount}
                    onChange={e => setForm(f => ({ ...f, amount: e.target.value }))}
                    placeholder="0.00"
                    required
                  />
                </div>
              </div>

              {/* Type & Account row */}
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className={labelClass}>Type</label>
                  <div className="flex rounded-lg border border-slate-700/60 overflow-hidden">
                    <button
                      type="button"
                      className={`flex-1 flex items-center justify-center gap-1.5 px-3 py-2.5 text-sm font-medium transition-all ${form.type === 'debit' ? 'bg-red-500/20 text-red-400 border-r border-red-500/30' : 'bg-slate-950/80 text-slate-500 border-r border-slate-700/60 hover:text-slate-300'}`}
                      onClick={() => setForm(f => ({ ...f, type: 'debit' }))}
                    >
                      <ArrowDownLeft size={14} />
                      Debit
                    </button>
                    <button
                      type="button"
                      className={`flex-1 flex items-center justify-center gap-1.5 px-3 py-2.5 text-sm font-medium transition-all ${form.type === 'credit' ? 'bg-emerald-500/20 text-emerald-400' : 'bg-slate-950/80 text-slate-500 hover:text-slate-300'}`}
                      onClick={() => setForm(f => ({ ...f, type: 'credit' }))}
                    >
                      <ArrowUpRight size={14} />
                      Credit
                    </button>
                  </div>
                </div>
                <div>
                  <label className={labelClass}>
                    <Landmark size={10} className="inline mr-1" />Account
                  </label>
                  <select
                    className={inputClass}
                    value={form.accountId}
                    onChange={e => setForm(f => ({ ...f, accountId: e.target.value }))}
                    required
                  >
                    <option value="">Select account</option>
                    {accounts.map(a => (
                      <option key={a.id} value={a.id}>{a.name}</option>
                    ))}
                  </select>
                </div>
              </div>
            </div>

            {/* Divider */}
            <div className="border-t border-slate-800/60" />

            {/* Section: Classification */}
            <div className="space-y-4">
              <div className="flex items-center gap-2 text-xs font-bold uppercase tracking-widest text-slate-500">
                <Tag size={12} />
                <span>Classification</span>
              </div>

              {/* Category & Payee row */}
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className={labelClass}>Category</label>
                  <select
                    className={inputClass}
                    value={form.categoryId}
                    onChange={e => setForm(f => ({ ...f, categoryId: e.target.value }))}
                  >
                    <option value="">Uncategorized</option>
                    {categories.map(c => (
                      <option key={c.id} value={c.id}>{c.name}</option>
                    ))}
                  </select>
                </div>
                <div>
                  <label className={labelClass}>
                    <User size={10} className="inline mr-1" />Payee
                  </label>
                  <select
                    className={inputClass}
                    value={form.payeeId}
                    onChange={e => setForm(f => ({ ...f, payeeId: e.target.value }))}
                  >
                    <option value="">No Payee</option>
                    {payees.map(p => (
                      <option key={p.id} value={p.id}>{p.name}</option>
                    ))}
                  </select>
                </div>
              </div>

              {/* Notes */}
              <div>
                <label className={labelClass}>Notes</label>
                <textarea
                  className={`${inputClass} resize-none`}
                  rows={3}
                  value={form.notes}
                  onChange={e => setForm(f => ({ ...f, notes: e.target.value }))}
                  placeholder="Add notes..."
                />
              </div>

              {/* Tags */}
              <div>
                <label className={labelClass}>Tags</label>
                <div className="flex flex-wrap gap-2 mb-2">
                  {form.tags.map(tag => (
                    <span
                      key={tag}
                      className="inline-flex items-center gap-1 px-2.5 py-1 bg-cyan-500/10 text-cyan-400 text-xs font-medium rounded-full border border-cyan-500/20"
                    >
                      {tag}
                      <button
                        type="button"
                        className="hover:text-red-400 transition-colors ml-0.5"
                        onClick={() => removeTag(tag)}
                      >
                        <X size={12} />
                      </button>
                    </span>
                  ))}
                </div>
                <div className="flex gap-2">
                  <input
                    type="text"
                    className={`${inputClass} flex-1`}
                    value={tagInput}
                    onChange={e => setTagInput(e.target.value)}
                    onKeyDown={handleTagKeyDown}
                    placeholder="Add a tag and press Enter"
                  />
                  <button
                    type="button"
                    className="px-3 py-2 bg-slate-800 hover:bg-slate-700 text-slate-300 text-sm rounded-lg border border-slate-700/60 transition-colors"
                    onClick={addTag}
                  >
                    Add
                  </button>
                </div>
              </div>
            </div>
          </div>
        </form>

        {/* Footer */}
        <div className="shrink-0 px-6 py-4 border-t border-slate-800/80 bg-slate-900/50 flex items-center justify-between gap-3">
          <div className="text-xs text-slate-600 truncate">
            ID: {transaction.id?.slice(0, 8)}...
          </div>
          <div className="flex gap-3">
            <button
              type="button"
              className="px-4 py-2.5 text-sm font-medium text-slate-400 hover:text-slate-200 hover:bg-slate-800 rounded-lg transition-colors"
              onClick={handleClose}
            >
              Cancel
            </button>
            <button
              type="submit"
              className="flex items-center gap-2 px-5 py-2.5 bg-cyan-500 hover:bg-cyan-400 text-white text-sm font-semibold rounded-lg transition-all shadow-lg shadow-cyan-500/20 disabled:opacity-50 disabled:cursor-not-allowed"
              onClick={handleSubmit}
              disabled={saving}
            >
              <Save size={14} />
              {saving ? 'Saving...' : 'Save Changes'}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
