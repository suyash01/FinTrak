import { BrowserRouter, Routes, Route } from 'react-router-dom';
import Sidebar from './components/Layout/Sidebar';
import Dashboard from './components/Dashboard/Dashboard';
import Import from './components/Import/Import';
import Transactions from './components/Transactions/Transactions';
import Accounts from './components/Accounts/Accounts';
import Categories from './components/Categories/Categories';
import Payees from './components/Payees/Payees';
import Linking from './components/Linking/Linking';
import { SettingsProvider, useSettings } from './context/SettingsContext';
import './index.css';

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

export default function App() {
  return (
    <BrowserRouter>
      <SettingsProvider>
        <div className="flex h-screen w-screen overflow-hidden">
          <Sidebar />
          <main className="flex-1 flex flex-col overflow-hidden min-w-0">
            <Routes>
              <Route path="/" element={<Dashboard />} />
              <Route path="/import" element={<Import />} />
              <Route path="/transactions" element={<Transactions />} />
              <Route path="/accounts" element={<Accounts />} />
              <Route path="/categories" element={<Categories />} />
              <Route path="/payees" element={<Payees />} />
              <Route path="/linking" element={<Linking />} />
              <Route path="/settings" element={<Settings />} />
            </Routes>
          </main>
        </div>
      </SettingsProvider>
    </BrowserRouter>
  );
}
