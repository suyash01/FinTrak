import { BrowserRouter, Routes, Route } from 'react-router-dom';
import Sidebar from './components/Layout/Sidebar';
import Dashboard from './components/Dashboard/Dashboard';
import Import from './components/Import/Import';
import PaperlessImport from './components/PaperlessImport/PaperlessImport';
import Transactions from './components/Transactions/Transactions';
import Accounts from './components/Accounts/Accounts';
import Categories from './components/Categories/Categories';
import Payees from './components/Payees/Payees';
import Linking from './components/Linking/Linking';
import Login from './components/Auth/Login';
import { SettingsProvider, useSettings } from './context/SettingsContext';
import { AuthProvider, useAuth } from './context/AuthContext';
import './index.css';
import { useState, useEffect } from 'react';
import api from './api/client';
import { Trash2, Edit2, Plus, X } from 'lucide-react';

function Settings() {
  const { compactLayout, toggleCompactLayout, pageSize, setPageSize } = useSettings();

  const pageSizeOptions = [
    { value: 25, label: '25' },
    { value: 50, label: '50' },
    { value: 100, label: '100' },
    { value: 200, label: '200' },
    { value: 0, label: 'No Pagination' },
  ];

  return (
    <>
      <div className="shrink-0 px-8 pt-6">
        <h1 className="text-2xl font-bold mb-1">Settings</h1>
        <p className="text-slate-400 text-sm">Application preferences</p>
      </div>
      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto space-y-6">
        <div className="bg-slate-900 border border-slate-800 rounded-xl p-6 transition-colors hover:border-slate-700 max-w-[500px]">
          <h3 className="text-base font-semibold mb-4">Display Preferences</h3>
          <div className="flex items-center justify-between">
            <div>
              <div className="text-sm font-medium text-slate-200">Compact Layout</div>
              <div className="text-[13px] text-slate-500">Reduce padding and spacing to show more data</div>
            </div>
            <button
              onClick={toggleCompactLayout}
              className={`relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none ring-offset-slate-900 focus:ring-2 focus:ring-cyan-500 focus:ring-offset-2 ${compactLayout ? 'bg-cyan-500' : 'bg-slate-700'}`}
            >
              <span
                className={`pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow-sm ring-0 transition duration-200 ease-in-out ${compactLayout ? 'translate-x-5' : 'translate-x-0'}`}
              />
            </button>
          </div>
          <div className="flex items-center justify-between mt-5 pt-5 border-t border-slate-800">
            <div>
              <div className="text-sm font-medium text-slate-200">Page Size</div>
              <div className="text-[13px] text-slate-500">Number of transactions per page (0 = show all)</div>
            </div>
            <select
              value={pageSize}
              onChange={(e) => setPageSize(Number(e.target.value))}
              className="px-3 py-1.5 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all cursor-pointer"
            >
              {pageSizeOptions.map((opt) => (
                <option key={opt.value} value={opt.value}>{opt.label}</option>
              ))}
            </select>
          </div>
        </div>

        {/* Account Types Management */}
        <div className="bg-slate-900 border border-slate-800 rounded-xl p-6 transition-colors hover:border-slate-700 max-w-[500px]">
          <h3 className="text-base font-semibold mb-4">Account Types</h3>
          <AccountTypesManager />
        </div>

        {/* Paperless-ngx integration */}
        <div className="bg-slate-900 border border-slate-800 rounded-xl p-6 transition-colors hover:border-slate-700 max-w-[500px]">
          <h3 className="text-base font-semibold mb-1">Paperless-ngx</h3>
          <p className="text-[13px] text-slate-500 mb-4">
            Connect a Paperless-ngx instance to pull statement PDFs. The import UI appears once both a URL and API token are set.
          </p>
          <PaperlessSettingsManager />
        </div>

        <div className="bg-slate-900 border border-slate-800 rounded-xl p-6 transition-colors hover:border-slate-700 max-w-[500px]">
          <h3 className="text-base font-semibold mb-4">About FinTrak</h3>
          <p className="text-slate-400 text-sm leading-relaxed">
            FinTrak helps you consolidate bank and credit card statements,
            categorize transactions, and track transfers and cashbacks — all in one place.
          </p>
          <div className="mt-4 text-[13px] text-slate-500">
            Version 0.1.0-alpha · Built with Go + React
          </div>
        </div>
      </div>
    </>
  );
}

function AccountTypesManager() {
  const [types, setTypes] = useState([]);
  const [loading, setLoading] = useState(true);
  const [editingId, setEditingId] = useState(null);
  const [formData, setFormData] = useState({ id: '', name: '', positiveTxnType: 'credit' });

  useEffect(() => {
    fetchTypes();
  }, []);

  const fetchTypes = async () => {
    try {
      const data = await api.getAccountTypes();
      setTypes(data);
    } catch (err) {
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  const handleSubmit = async (e) => {
    e.preventDefault();
    try {
      if (editingId) {
        await api.updateAccountType(editingId, { name: formData.name, positiveTxnType: formData.positiveTxnType });
      } else {
        await api.createAccountType(formData);
      }
      setEditingId(null);
      setFormData({ id: '', name: '', positiveTxnType: 'credit' });
      fetchTypes();
    } catch (err) {
      alert(err.message);
    }
  };

  const handleDelete = async (id) => {
    if (!confirm('Delete this account type? This will fail if accounts use it.')) return;
    try {
      await api.deleteAccountType(id);
      fetchTypes();
    } catch (err) {
      alert(err.message);
    }
  };

  if (loading) return <div className="text-sm text-slate-500">Loading...</div>;

  return (
    <div className="space-y-4">
      <div className="divide-y divide-slate-800 border border-slate-800 rounded-lg overflow-hidden bg-slate-950">
        {types.map((t) => (
          <div key={t.id} className="p-3 flex justify-between items-center group hover:bg-slate-900 transition-colors">
            {editingId === t.id ? (
              <form onSubmit={handleSubmit} className="flex gap-2 w-full items-center">
                <input required className="flex-1 px-2 py-1 bg-slate-900 border border-slate-700 rounded text-sm text-white" value={formData.name} onChange={e => setFormData({ ...formData, name: e.target.value })} />
                <select className="px-2 py-1 bg-slate-900 border border-slate-700 rounded text-sm text-white" value={formData.positiveTxnType} onChange={e => setFormData({ ...formData, positiveTxnType: e.target.value })}>
                  <option value="credit">Credit is positive</option>
                  <option value="debit">Debit is positive</option>
                </select>
                <button type="submit" className="text-cyan-500 hover:text-cyan-400 p-1"><Plus size={16} /></button>
                <button type="button" onClick={() => setEditingId(null)} className="text-slate-500 hover:text-slate-300 p-1"><X size={16} /></button>
              </form>
            ) : (
              <>
                <div>
                  <div className="font-medium text-sm text-slate-200">{t.name}</div>
                  <div className="text-xs text-slate-500">ID: {t.id} • Positive: <span className="uppercase text-[10px] bg-slate-800 px-1 rounded">{t.positiveTxnType}</span></div>
                </div>
                <div className="flex opacity-0 group-hover:opacity-100 transition-opacity">
                  <button onClick={() => { setEditingId(t.id); setFormData(t); }} className="p-1.5 text-slate-400 hover:text-cyan-400"><Edit2 size={14} /></button>
                  <button onClick={() => handleDelete(t.id)} className="p-1.5 text-slate-400 hover:text-red-400"><Trash2 size={14} /></button>
                </div>
              </>
            )}
          </div>
        ))}
      </div>

      {!editingId && editingId !== 'new' && (
        <button onClick={() => { setEditingId('new'); setFormData({ id: '', name: '', positiveTxnType: 'credit' }); }} className="text-sm text-cyan-500 hover:text-cyan-400 flex items-center gap-1 font-medium">
          <Plus size={14} /> Add Account Type
        </button>
      )}

      {editingId === 'new' && (
        <form onSubmit={handleSubmit} className="p-3 border border-slate-800 rounded-lg bg-slate-950 space-y-3">
          <div>
            <label className="block text-xs font-medium text-slate-400 mb-1">Type ID (Code)</label>
            <input required placeholder="e.g. wallet, cash" className="w-full px-3 py-1.5 bg-slate-900 border border-slate-700 rounded text-sm text-white" value={formData.id} onChange={e => setFormData({ ...formData, id: e.target.value })} />
          </div>
          <div>
            <label className="block text-xs font-medium text-slate-400 mb-1">Display Name</label>
            <input required placeholder="e.g. Mobile Wallet" className="w-full px-3 py-1.5 bg-slate-900 border border-slate-700 rounded text-sm text-white" value={formData.name} onChange={e => setFormData({ ...formData, name: e.target.value })} />
          </div>
          <div>
            <label className="block text-xs font-medium text-slate-400 mb-1">Sign Convention</label>
            <select className="w-full px-3 py-1.5 bg-slate-900 border border-slate-700 rounded text-sm text-white" value={formData.positiveTxnType} onChange={e => setFormData({ ...formData, positiveTxnType: e.target.value })}>
              <option value="credit">Credit amounts increase balance</option>
              <option value="debit">Debit amounts increase balance</option>
            </select>
          </div>
          <div className="flex gap-2 justify-end pt-1">
            <button type="button" onClick={() => setEditingId(null)} className="px-3 py-1 text-xs font-medium text-slate-400 hover:text-white">Cancel</button>
            <button type="submit" className="px-3 py-1 text-xs font-medium bg-cyan-500 text-white rounded hover:bg-cyan-600">Create</button>
          </div>
        </form>
      )}
    </div>
  );
}

function PaperlessSettingsManager() {
  const [url, setUrl] = useState('');
  const [token, setToken] = useState('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    api
      .getPaperlessSettings()
      .then((s) => {
        setUrl(s.paperlessUrl || '');
        setToken(s.paperlessToken || '');
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  const handleSave = async (e) => {
    e.preventDefault();
    setSaving(true);
    setSaved(false);
    try {
      await api.updatePaperlessSettings({ paperlessUrl: url, paperlessToken: token });
      setSaved(true);
    } catch (err) {
      alert(err.message);
    } finally {
      setSaving(false);
    }
  };

  if (loading) return <div className="text-sm text-slate-500">Loading...</div>;

  return (
    <form onSubmit={handleSave} className="space-y-3">
      <div>
        <label className="block text-xs font-medium text-slate-400 mb-1">Paperless URL</label>
        <input
          type="text"
          placeholder="http://localhost:8000"
          className="w-full px-3 py-1.5 bg-slate-900 border border-slate-800 rounded text-sm text-white focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500"
          value={url}
          onChange={(e) => setUrl(e.target.value)}
        />
      </div>
      <div>
        <label className="block text-xs font-medium text-slate-400 mb-1">API Token</label>
        <input
          type="password"
          placeholder="Paperless-ngx API token"
          className="w-full px-3 py-1.5 bg-slate-900 border border-slate-800 rounded text-sm text-white focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500"
          value={token}
          onChange={(e) => setToken(e.target.value)}
        />
      </div>
      <div className="flex items-center gap-3 pt-1">
        <button
          type="submit"
          disabled={saving}
          className="px-3 py-1.5 text-xs font-medium bg-cyan-500 text-white rounded hover:bg-cyan-600 disabled:opacity-50"
        >
          {saving ? 'Saving...' : 'Save'}
        </button>
        {saved && <span className="text-xs text-emerald-400">Saved</span>}
      </div>
    </form>
  );
}

export default function App() {
  return (
    <BrowserRouter>
      <SettingsProvider>
        <AuthProvider>
          <Root />
        </AuthProvider>
      </SettingsProvider>
    </BrowserRouter>
  );
}

function Root() {
  const { isAuthenticated } = useAuth();

  if (!isAuthenticated) {
    return (
      <Routes>
        <Route path="*" element={<Login />} />
      </Routes>
    );
  }

  return (
    <div className="flex h-screen w-screen overflow-hidden">
      <Sidebar />
      <main className="flex-1 flex flex-col overflow-hidden min-w-0">
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/import" element={<Import />} />
          <Route path="/paperless" element={<PaperlessImport />} />
          <Route path="/transactions" element={<Transactions />} />
          <Route path="/accounts" element={<Accounts />} />
          <Route path="/categories" element={<Categories />} />
          <Route path="/payees" element={<Payees />} />
          <Route path="/linking" element={<Linking />} />
          <Route path="/settings" element={<Settings />} />
          <Route path="*" element={<Dashboard />} />
        </Routes>
      </main>
    </div>
  );
}
