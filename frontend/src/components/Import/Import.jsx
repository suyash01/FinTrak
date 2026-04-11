import { useState, useEffect, useRef } from 'react';
import Papa from 'papaparse';
import { Upload, ChevronRight, Check, ArrowRight, AlertCircle, FileSpreadsheet } from 'lucide-react';
import api from '../../api/client';
import { formatCurrency } from '../../utils/formatters';

const DB_COLUMNS = [
  { key: 'skip', label: '— Skip this column —', required: false },
  { key: 'date', label: 'Date', required: true },
  { key: 'description', label: 'Description', required: true },
  { key: 'amount', label: 'Amount (single column)', required: false },
  { key: 'debit', label: 'Debit Amount', required: false },
  { key: 'credit', label: 'Credit Amount', required: false },
  { key: 'payee', label: 'Payee', required: false },
];

export default function Import() {
  const [step, setStep] = useState(1);
  const [accounts, setAccounts] = useState([]);
  const [selectedAccount, setSelectedAccount] = useState('');
  const [newAccount, setNewAccount] = useState({ name: '', type: 'bank', bank: '', color: '#06b6d4' });
  const [showNewAccount, setShowNewAccount] = useState(false);
  const [payees, setPayees] = useState([]);

  // CSV state
  const [csvData, setCsvData] = useState(null);
  const [csvHeaders, setCsvHeaders] = useState([]);
  const [columnMapping, setColumnMapping] = useState({});
  const [dateFormat, setDateFormat] = useState('auto');
  const [amountMode, setAmountMode] = useState('single'); // 'single' or 'separate'

  // Import results
  const [importing, setImporting] = useState(false);
  const [importResult, setImportResult] = useState(null);

  const fileInputRef = useRef(null);

  useEffect(() => {
    api.getAccounts().then(setAccounts).catch(console.error);
    api.getPayees().then(setPayees).catch(console.error);
  }, []);

  // ---- Step 1: Select Account ----
  const handleCreateAccount = async () => {
    try {
      const acc = await api.createAccount(newAccount);
      setAccounts((prev) => [acc, ...prev]);
      setSelectedAccount(acc.id);
      setShowNewAccount(false);
      setNewAccount({ name: '', type: 'bank', bank: '', color: '#06b6d4' });
    } catch (err) {
      alert(err.message);
    }
  };

  // ---- Step 2: Upload & Parse CSV ----
  const handleFileUpload = (e) => {
    const file = e.target.files[0];
    if (!file) return;

    Papa.parse(file, {
      header: true,
      skipEmptyLines: true,
      complete: (results) => {
        setCsvData(results.data);
        setCsvHeaders(results.meta.fields || []);
        // Auto-detect column mapping
        const mapping = autoDetectMapping(results.meta.fields || []);
        setColumnMapping(mapping);
        // Detect if separate debit/credit columns
        const hasDebit = results.meta.fields.some(h => /debit|withdrawal|dr/i.test(h));
        const hasCredit = results.meta.fields.some(h => /credit|deposit|cr/i.test(h));
        if (hasDebit && hasCredit) {
          setAmountMode('separate');
        }
        setStep(3);
      },
      error: (err) => {
        alert('Failed to parse CSV: ' + err.message);
      },
    });
  };

  const autoDetectMapping = (headers) => {
    const mapping = {};
    headers.forEach((h) => {
      const lower = h.toLowerCase().trim();
      if (/date|txn.*date|transaction.*date|value.*date/i.test(lower)) {
        mapping[h] = 'date';
      } else if (/narration|description|particulars|details|remark/i.test(lower)) {
        mapping[h] = 'description';
      } else if (/^amount$|^transaction.*amount$|^txn.*amount$/i.test(lower)) {
        mapping[h] = 'amount';
      } else if (/debit|withdrawal|dr/i.test(lower)) {
        mapping[h] = 'debit';
      } else if (/credit|deposit|cr/i.test(lower)) {
        mapping[h] = 'credit';
      } else if (/payee|beneficiary|merchant|receiver|sender/i.test(lower)) {
        mapping[h] = 'payee';
      } else {
        mapping[h] = 'skip';
      }
    });
    return mapping;
  };

  // ---- Step 3: Column Mapping ----
  const updateMapping = (csvCol, dbCol) => {
    setColumnMapping((prev) => ({ ...prev, [csvCol]: dbCol }));
  };

  const getMappingErrors = () => {
    const errors = [];
    const mapped = Object.values(columnMapping);
    if (!mapped.includes('date')) errors.push('Date column is required');
    if (!mapped.includes('description')) errors.push('Description column is required');
    if (amountMode === 'single' && !mapped.includes('amount')) {
      errors.push('Amount column is required (or switch to separate Debit/Credit mode)');
    }
    if (amountMode === 'separate') {
      if (!mapped.includes('debit')) errors.push('Debit column is required in separate mode');
      if (!mapped.includes('credit')) errors.push('Credit column is required in separate mode');
    }
    return errors;
  };

  // ---- Step 4: Preview & Import ----
  const parseTransactions = () => {
    const dateCol = Object.keys(columnMapping).find((k) => columnMapping[k] === 'date');
    const descCol = Object.keys(columnMapping).find((k) => columnMapping[k] === 'description');
    const amountCol = Object.keys(columnMapping).find((k) => columnMapping[k] === 'amount');
    const debitCol = Object.keys(columnMapping).find((k) => columnMapping[k] === 'debit');
    const creditCol = Object.keys(columnMapping).find((k) => columnMapping[k] === 'credit');
    const payeeCol = Object.keys(columnMapping).find((k) => columnMapping[k] === 'payee');

    return csvData
      .map((row) => {
        const rawDate = row[dateCol]?.trim();
        if (!rawDate) return null;

        const date = parseDate(rawDate);
        if (!date) return null;

        const description = row[descCol]?.trim() || '';
        if (!description) return null;

        let amount = 0;
        let type = 'debit';

        if (amountMode === 'single' && amountCol) {
          const raw = parseAmount(row[amountCol]);
          if (raw < 0) {
            amount = Math.abs(raw);
            type = 'debit';
          } else {
            amount = raw;
            type = 'credit';
          }
        } else if (amountMode === 'separate') {
          const debitAmt = parseAmount(row[debitCol]);
          const creditAmt = parseAmount(row[creditCol]);
          if (debitAmt > 0) {
            amount = debitAmt;
            type = 'debit';
          } else if (creditAmt > 0) {
            amount = creditAmt;
            type = 'credit';
          } else {
            return null;
          }
        }

        if (amount === 0) return null;

        let payeeId = null;
        if (payeeCol && row[payeeCol]) {
          const name = row[payeeCol].trim().toLowerCase();
          const match = payees.find(p => p.name.toLowerCase() === name);
          if (match) payeeId = match.id;
        }

        return { date, description, amount, type, payeeId };
      })
      .filter(Boolean);
  };

  const parseDate = (str) => {
    if (!str) return null;
    // Try multiple formats
    const formats = [
      // DD/MM/YYYY, DD-MM-YYYY
      /^(\d{1,2})[\/\-](\d{1,2})[\/\-](\d{4})$/,
      // YYYY-MM-DD
      /^(\d{4})[\/\-](\d{1,2})[\/\-](\d{1,2})$/,
      // DD/MM/YY
      /^(\d{1,2})[\/\-](\d{1,2})[\/\-](\d{2})$/,
      // DD Mon YYYY
      /^(\d{1,2})\s+(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)[a-z]*\s+(\d{4})$/i,
    ];

    // DD/MM/YYYY or DD-MM-YYYY
    let match = str.match(formats[0]);
    if (match) {
      return `${match[3]}-${match[2].padStart(2, '0')}-${match[1].padStart(2, '0')}`;
    }

    // YYYY-MM-DD
    match = str.match(formats[1]);
    if (match) {
      return `${match[1]}-${match[2].padStart(2, '0')}-${match[3].padStart(2, '0')}`;
    }

    // DD/MM/YY
    match = str.match(formats[2]);
    if (match) {
      const year = parseInt(match[3]) > 50 ? `19${match[3]}` : `20${match[3]}`;
      return `${year}-${match[2].padStart(2, '0')}-${match[1].padStart(2, '0')}`;
    }

    // DD Mon YYYY
    match = str.match(formats[3]);
    if (match) {
      const months = { jan: '01', feb: '02', mar: '03', apr: '04', may: '05', jun: '06', jul: '07', aug: '08', sep: '09', oct: '10', nov: '11', dec: '12' };
      const m = months[match[2].toLowerCase().substring(0, 3)];
      return `${match[3]}-${m}-${match[1].padStart(2, '0')}`;
    }

    // Fallback: try Date constructor
    const d = new Date(str);
    if (!isNaN(d.getTime())) {
      return d.toISOString().split('T')[0];
    }

    return null;
  };

  const parseAmount = (str) => {
    if (!str || typeof str !== 'string') {
      if (typeof str === 'number') return str;
      return 0;
    }
    // Remove commas, currency symbols, spaces
    const cleaned = str.replace(/[₹$,\s]/g, '').trim();
    if (!cleaned) return 0;
    return parseFloat(cleaned) || 0;
  };

  const handleImport = async () => {
    setImporting(true);
    try {
      const transactions = parseTransactions();
      if (transactions.length === 0) {
        alert('No valid transactions found. Please check your column mapping.');
        setImporting(false);
        return;
      }

      const result = await api.importTransactions({
        accountId: selectedAccount,
        transactions,
      });
      setImportResult(result);
      setStep(5);
    } catch (err) {
      alert('Import failed: ' + err.message);
    } finally {
      setImporting(false);
    }
  };

  const parsedTransactions = step >= 4 ? parseTransactions() : [];
  const mappingErrors = step >= 3 ? getMappingErrors() : [];

  const steps = [
    { num: 1, label: 'Select Account' },
    { num: 2, label: 'Upload CSV' },
    { num: 3, label: 'Map Columns' },
    { num: 4, label: 'Preview' },
    { num: 5, label: 'Done' },
  ];

  return (
    <>
      <div className="shrink-0 px-8 pt-6">
        <h1 className="text-2xl font-bold mb-1">Import Statement</h1>
        <p className="text-slate-400 text-sm">Upload and map your CSV bank or credit card statement</p>
      </div>
      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto w-full">
        {/* Steps indicator */}
        <div className="flex items-center gap-4 mb-8 overflow-x-auto pb-2 scrollbar-none">
          {steps.map((s) => (
            <div
              key={s.num}
              className={`flex items-center gap-2 text-sm font-medium whitespace-nowrap px-3 py-1.5 rounded-lg transition-colors ${step === s.num ? 'bg-cyan-500/10 text-cyan-400' : step > s.num ? 'text-emerald-500' : 'text-slate-500'}`}
              onClick={() => step > s.num && setStep(s.num)}
              style={{ cursor: step > s.num ? 'pointer' : 'default' }}
            >
              <span className={`w-6 h-6 rounded-full flex items-center justify-center text-xs font-bold shrink-0 ${step === s.num ? 'bg-cyan-500 text-slate-950' : step > s.num ? 'bg-emerald-500/20 text-emerald-500' : 'bg-slate-800 text-slate-400'}`}>
                {step > s.num ? <Check size={12} /> : s.num}
              </span>
              {s.label}
            </div>
          ))}
        </div>

        {/* Step 1: Select Account */}
        {step === 1 && (
          <div className="bg-slate-900 border border-slate-800 rounded-xl p-6" style={{ maxWidth: '600px' }}>
            <h3 className="text-lg font-semibold mb-4 text-slate-100">Select Account</h3>
            {accounts.length > 0 && !showNewAccount && (
              <div className="flex flex-col gap-1.5 mb-5">
                <label className="text-sm font-medium text-slate-400">Existing Account</label>
                <select
                  className="px-3.5 py-2.5 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all"
                  value={selectedAccount}
                  onChange={(e) => setSelectedAccount(e.target.value)}
                >
                  <option value="">Choose an account...</option>
                  {accounts.map((a) => (
                    <option key={a.id} value={a.id}>
                      {a.name} ({a.bank || a.type})
                    </option>
                  ))}
                </select>
              </div>
            )}

            {!showNewAccount && (
              <button className="inline-flex items-center gap-2 px-4 py-2 bg-slate-800 text-slate-200 border border-slate-700 rounded-lg text-sm font-medium hover:bg-slate-700 transition-all mb-4" onClick={() => setShowNewAccount(true)}>
                + Create New Account
              </button>
            )}

            {showNewAccount && (
              <div className="bg-slate-950 p-5 rounded-lg border border-slate-800 mb-4">
                <h4 className="mb-4 text-sm font-semibold text-slate-200">New Account</h4>
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 mb-4">
                  <div className="flex flex-col gap-1.5">
                    <label className="text-sm font-medium text-slate-400">Name</label>
                    <input className="px-3.5 py-2.5 bg-slate-900 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all" placeholder="e.g. HDFC Savings" value={newAccount.name} onChange={(e) => setNewAccount({ ...newAccount, name: e.target.value })} />
                  </div>
                  <div className="flex flex-col gap-1.5">
                    <label className="text-sm font-medium text-slate-400">Type</label>
                    <select className="px-3.5 py-2.5 bg-slate-900 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all" value={newAccount.type} onChange={(e) => setNewAccount({ ...newAccount, type: e.target.value })}>
                      <option value="bank">Bank Account</option>
                      <option value="credit_card">Credit Card</option>
                    </select>
                  </div>
                </div>
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 mb-5">
                  <div className="flex flex-col gap-1.5">
                    <label className="text-sm font-medium text-slate-400">Bank Name</label>
                    <input className="px-3.5 py-2.5 bg-slate-900 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all" placeholder="e.g. HDFC, ICICI, SBI" value={newAccount.bank} onChange={(e) => setNewAccount({ ...newAccount, bank: e.target.value })} />
                  </div>
                  <div className="flex flex-col gap-1.5">
                    <label className="text-sm font-medium text-slate-400">Color</label>
                    <input type="color" value={newAccount.color} onChange={(e) => setNewAccount({ ...newAccount, color: e.target.value })} className="w-full h-[42px] cursor-pointer bg-slate-900 border border-slate-800 rounded-lg p-1" />
                  </div>
                </div>
                <div className="flex gap-3">
                  <button className="inline-flex items-center gap-2 px-4 py-2 bg-linear-to-r from-cyan-500 to-blue-600 text-white rounded-lg text-sm font-medium hover:opacity-90 transition-all disabled:opacity-50 shadow-md shadow-cyan-500/20" onClick={handleCreateAccount} disabled={!newAccount.name}>Create</button>
                  <button className="inline-flex items-center gap-2 px-4 py-2 bg-slate-800 text-slate-200 border border-slate-700 rounded-lg text-sm font-medium hover:bg-slate-700 transition-all" onClick={() => setShowNewAccount(false)}>Cancel</button>
                </div>
              </div>
            )}

            <div className="pt-4 mt-2 border-t border-slate-800">
              <button className="inline-flex items-center gap-2 px-5 py-2.5 bg-linear-to-r from-cyan-500 to-blue-600 text-white rounded-lg text-sm font-medium hover:opacity-90 transition-all shadow-lg shadow-cyan-500/20 disabled:opacity-50 disabled:cursor-not-allowed" disabled={!selectedAccount} onClick={() => setStep(2)}>
                Continue <ChevronRight size={16} />
              </button>
            </div>
          </div>
        )}

        {/* Step 2: Upload CSV */}
        {step === 2 && (
          <div className="bg-slate-900 border border-slate-800 rounded-xl p-6" style={{ maxWidth: '600px' }}>
            <div
              className="border-2 border-dashed border-slate-700 bg-slate-950/50 rounded-xl p-12 flex flex-col items-center justify-center text-center cursor-pointer transition-colors hover:border-cyan-500/50 hover:bg-slate-900/50 group"
              onClick={() => fileInputRef.current?.click()}
              onDragOver={(e) => { e.preventDefault(); e.currentTarget.classList.add('border-cyan-500', 'bg-slate-900/80'); }}
              onDragLeave={(e) => e.currentTarget.classList.remove('border-cyan-500', 'bg-slate-900/80')}
              onDrop={(e) => {
                e.preventDefault();
                e.currentTarget.classList.remove('border-cyan-500', 'bg-slate-900/80');
                const file = e.dataTransfer.files[0];
                if (file) {
                  const dt = new DataTransfer();
                  dt.items.add(file);
                  fileInputRef.current.files = dt.files;
                  handleFileUpload({ target: { files: [file] } });
                }
              }}
            >
              <div className="w-16 h-16 rounded-full bg-slate-800 flex items-center justify-center mb-4 group-hover:bg-cyan-500/20 text-slate-400 group-hover:text-cyan-400 transition-colors">
                <FileSpreadsheet size={32} />
              </div>
              <h3 className="text-lg font-semibold text-slate-200 mb-2">Drop your CSV file here</h3>
              <p className="text-sm text-slate-500">or click to browse. Supports .csv files from any bank.</p>
            </div>
            <input ref={fileInputRef} type="file" accept=".csv" className="hidden" onChange={handleFileUpload} />
          </div>
        )}

        {/* Step 3: Column Mapping */}
        {step === 3 && (
          <div className="bg-slate-900 border border-slate-800 rounded-xl p-6" style={{ maxWidth: '800px' }}>
            <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-4">
              <h3 className="text-lg font-bold text-slate-100">Map CSV Columns</h3>
              <div className="flex gap-2">
                <button className={`px-3 py-1.5 text-xs font-semibold rounded-md border transition-colors ${amountMode === 'single' ? 'bg-cyan-500 border-cyan-500 text-white' : 'bg-slate-950 border-slate-800 text-slate-400 hover:text-slate-200'}`} onClick={() => setAmountMode('single')}>Single Amount</button>
                <button className={`px-3 py-1.5 text-xs font-semibold rounded-md border transition-colors ${amountMode === 'separate' ? 'bg-cyan-500 border-cyan-500 text-white' : 'bg-slate-950 border-slate-800 text-slate-400 hover:text-slate-200'}`} onClick={() => setAmountMode('separate')}>Debit / Credit</button>
              </div>
            </div>

            <p className="text-sm text-slate-400 mb-6">
              Map each CSV column to the corresponding database field. Skip columns you don't need.
            </p>

            <div className="bg-slate-950 border border-slate-800 rounded-lg overflow-hidden">
              {csvHeaders.map((header) => (
                <div key={header} className="grid grid-cols-[1fr_auto_1fr] items-center gap-6 p-4 border-b border-slate-800 last:border-0 hover:bg-slate-900/50 transition-colors">
                  <div className="flex flex-col gap-1 min-w-0">
                    <div className="text-[11px] font-semibold text-slate-500 uppercase tracking-wider">CSV Column</div>
                    <div className="font-medium text-sm text-slate-200 truncate">{header}</div>
                    <div className="text-xs text-slate-500 truncate mt-0.5">
                      e.g. "{csvData?.[0]?.[header] || '—'}"
                    </div>
                  </div>
                  <ArrowRight className="text-slate-600 shrink-0" size={20} />
                  <div className="flex flex-col gap-1 min-w-0">
                    <div className="text-[11px] font-semibold text-slate-500 uppercase tracking-wider">Maps To</div>
                    <select
                      className="mt-1 px-3 py-2 bg-slate-900 border border-slate-700 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 w-full"
                      value={columnMapping[header] || 'skip'}
                      onChange={(e) => updateMapping(header, e.target.value)}
                    >
                      {DB_COLUMNS.filter(col => {
                        if (amountMode === 'single' && (col.key === 'debit' || col.key === 'credit')) return false;
                        if (amountMode === 'separate' && col.key === 'amount') return false;
                        return true;
                      }).map((col) => (
                        <option key={col.key} value={col.key}>{col.label}</option>
                      ))}
                    </select>
                  </div>
                </div>
              ))}
            </div>

            {mappingErrors.length > 0 && (
              <div className="mt-5 p-4 bg-red-500/10 border border-red-500/20 rounded-lg">
                {mappingErrors.map((err, i) => (
                  <div key={i} className="flex gap-2 items-center text-red-500 text-sm py-0.5">
                    <AlertCircle size={14} className="shrink-0" /> {err}
                  </div>
                ))}
              </div>
            )}

            {/* Preview first 5 rows */}
            {csvData && (
              <div className="mt-6 border border-slate-800 rounded-lg overflow-x-auto bg-slate-900">
                <table className="w-full text-left border-collapse min-w-max">
                  <thead>
                    <tr>
                      {csvHeaders.map((h) => (
                        <th key={h} className="py-2.5 px-4 text-xs font-semibold text-slate-400 bg-slate-950 border-b border-slate-800 whitespace-nowrap">
                          {h}
                          {columnMapping[h] && columnMapping[h] !== 'skip' && (
                            <div className="text-[10px] font-medium text-cyan-500 mt-0.5 tracking-wide">→ {columnMapping[h].toUpperCase()}</div>
                          )}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {csvData.slice(0, 5).map((row, i) => (
                      <tr key={i} className="border-b border-slate-800 last:border-0 hover:bg-slate-800/30">
                        {csvHeaders.map((h) => (
                          <td key={h} className={`py-2 px-4 text-xs max-w-[150px] overflow-hidden text-ellipsis whitespace-nowrap ${columnMapping[h] === 'skip' ? 'opacity-40 text-slate-500' : 'text-slate-300'}`}>
                            {row[h]}
                          </td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}

            <div className="pt-5 mt-6 border-t border-slate-800 flex justify-between gap-4">
              <button className="inline-flex items-center gap-2 px-4 py-2 bg-slate-800 text-slate-200 border border-slate-700 rounded-lg text-sm font-medium hover:bg-slate-700 transition-all" onClick={() => setStep(2)}>Back</button>
              <button className="inline-flex items-center gap-2 px-5 py-2.5 bg-linear-to-r from-cyan-500 to-blue-600 text-white rounded-lg text-sm font-medium hover:opacity-90 transition-all shadow-lg shadow-cyan-500/20 disabled:opacity-50 disabled:cursor-not-allowed" disabled={mappingErrors.length > 0} onClick={() => setStep(4)}>
                Preview Transactions <ChevronRight size={16} />
              </button>
            </div>
          </div>
        )}

        {/* Step 4: Preview & Confirm Import */}
        {step === 4 && (
          <div className="bg-slate-900 border border-slate-800 rounded-xl p-6">
            <div className="flex flex-col sm:flex-row justify-between sm:items-end gap-2 mb-6">
              <h3 className="text-xl font-bold text-slate-100">Preview — {parsedTransactions.length} transactions</h3>
              <div className="text-sm text-slate-400 font-medium">{csvData.length - parsedTransactions.length} rows skipped (empty/invalid)</div>
            </div>

            <div className="border border-slate-800 rounded-lg overflow-y-auto max-h-[500px] bg-slate-950">
              <table className="w-full text-left border-collapse min-w-[600px]">
                <thead className="sticky top-0 bg-slate-900 z-10 shadow-[0_1px_0_var(--tw-shadow-color)] shadow-slate-800">
                  <tr>
                    <th className="py-3 px-4 text-xs font-semibold uppercase tracking-wider text-slate-400 w-28">Date</th>
                    <th className="py-3 px-4 text-xs font-semibold uppercase tracking-wider text-slate-400">Description</th>
                    <th className="py-3 px-4 text-xs font-semibold uppercase tracking-wider text-slate-400">Payee</th>
                    <th className="py-3 px-4 text-xs font-semibold uppercase tracking-wider text-slate-400 w-24">Type</th>
                    <th className="py-3 px-4 text-xs font-semibold uppercase tracking-wider text-slate-400 text-right w-32">Amount</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-800/50">
                  {parsedTransactions.slice(0, 100).map((t, i) => (
                    <tr key={i} className="hover:bg-slate-900/50 transition-colors">
                      <td className="py-2.5 px-4 text-sm text-slate-300 whitespace-nowrap">{t.date}</td>
                      <td className="py-2.5 px-4 text-sm text-slate-200 max-w-[200px] overflow-hidden text-ellipsis whitespace-nowrap">{t.description}</td>
                      <td className="py-2.5 px-4 text-sm text-slate-400 max-w-[150px] overflow-hidden text-ellipsis whitespace-nowrap">
                        {t.payeeId ? (
                          <span className="text-cyan-500 font-medium">{payees.find(p => p.id === t.payeeId)?.name}</span>
                        ) : (
                          <span className="opacity-30 italic">Not found</span>
                        )}
                      </td>
                      <td className="py-2.5 px-4">
                        <span className={`inline-flex items-center px-2 py-0.5 rounded textxs font-semibold tracking-wide uppercase ${t.type === 'debit' ? 'bg-red-500/10 text-red-500 border border-red-500/20' : 'bg-emerald-500/10 text-emerald-500 border border-emerald-500/20'}`}>
                          {t.type}
                        </span>
                      </td>
                      <td className="py-2.5 px-4 text-right font-medium whitespace-nowrap">
                        <span className={t.type === 'debit' ? 'text-red-500' : 'text-emerald-500'}>
                          {t.type === 'debit' ? '−' : '+'}{formatCurrency(t.amount)}
                        </span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            {parsedTransactions.length > 100 && (
              <p className="text-sm text-center text-slate-500 mt-4">Showing first 100 of {parsedTransactions.length} transactions</p>
            )}

            <div className="pt-5 mt-6 border-t border-slate-800 flex justify-between gap-4">
              <button className="inline-flex items-center gap-2 px-4 py-2 bg-slate-800 text-slate-200 border border-slate-700 rounded-lg text-sm font-medium hover:bg-slate-700 transition-all" onClick={() => setStep(3)}>Back to Mapping</button>
              <button className="inline-flex items-center gap-2 px-5 py-2.5 bg-linear-to-r from-cyan-500 to-blue-600 text-white rounded-lg text-sm font-medium hover:opacity-90 transition-all shadow-lg shadow-cyan-500/20 disabled:opacity-50 disabled:cursor-not-allowed" onClick={handleImport} disabled={importing || parsedTransactions.length === 0}>
                {importing ? <div className="flex gap-2 items-center"><div className="animate-spin rounded-full h-4 w-4 border-b-2 border-white"></div> Importing...</div> : <>Import {parsedTransactions.length} Transactions</>}
              </button>
            </div>
          </div>
        )}

        {/* Step 5: Done */}
        {step === 5 && importResult && (
          <div className="bg-slate-900 border border-slate-800 rounded-xl p-10 max-w-[500px] text-center mx-auto mt-10">
            <div className="w-16 h-16 rounded-full bg-emerald-500/15 flex items-center justify-center mx-auto mb-6 text-emerald-500 shadow-[0_0_20px_rgba(16,185,129,0.2)]">
              <Check size={32} />
            </div>
            <h2 className="text-2xl font-bold text-slate-100 mb-2">Import Complete!</h2>
            <p className="text-slate-400 mb-8 max-w-[80%] mx-auto leading-relaxed">
              <span className="font-semibold text-slate-200">{importResult.imported}</span> transactions successfully imported.<br />
              <span className="text-sm">{importResult.skipped} duplicates skipped.</span>
            </p>
            <div className="flex flex-col sm:flex-row gap-4 justify-center">
              <button className="inline-flex justify-center items-center gap-2 px-6 py-2.5 bg-slate-800 text-slate-200 border border-slate-700 rounded-lg text-sm font-medium hover:bg-slate-700 transition-all" onClick={() => { setStep(1); setCsvData(null); setImportResult(null); }}>
                Import Another
              </button>
              <button className="inline-flex justify-center items-center gap-2 px-6 py-2.5 bg-linear-to-r from-cyan-500 to-blue-600 text-white rounded-lg text-sm font-medium hover:opacity-90 transition-all shadow-lg shadow-cyan-500/20" onClick={() => window.location.href = '/transactions'}>
                View Transactions
              </button>
            </div>
          </div>
        )}
      </div>
    </>
  );
}
