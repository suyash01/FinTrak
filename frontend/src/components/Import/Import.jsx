import { useState, useEffect, useRef, useMemo } from 'react';
import Papa from 'papaparse';
import { ChevronRight, Check, ArrowRight, AlertCircle, AlertTriangle, X, FileSpreadsheet, FileText, Loader2 } from 'lucide-react';
import api from '../../api/client';
import { formatCurrency, formatDateOnly } from '../../utils/formatters';

const TARGET_FIELDS = [
  { key: 'date', label: 'Date', required: true },
  { key: 'description', label: 'Description', required: true },
  { key: 'amount', label: 'Amount', required: true, mode: 'single' },
  { key: 'debit', label: 'Debit Amount', required: true, mode: 'separate' },
  { key: 'credit', label: 'Credit Amount', required: true, mode: 'separate' },
  { key: 'payee', label: 'Payee', required: false },
];

const targetFieldsFor = (amountMode) => TARGET_FIELDS.filter((f) => !f.mode || f.mode === amountMode);

const pad2 = (n) => String(n).padStart(2, '0');

const MONTHS = { jan: '01', feb: '02', mar: '03', apr: '04', may: '05', jun: '06', jul: '07', aug: '08', sep: '09', oct: '10', nov: '11', dec: '12' };

const DATE_FORMAT_OPTIONS = [
  { value: 'auto', label: 'Auto-detect' },
  { value: 'DD/MM/YYYY', label: 'DD/MM/YYYY' },
  { value: 'MM/DD/YYYY', label: 'MM/DD/YYYY' },
  { value: 'DD/MM/YY', label: 'DD/MM/YY' },
  { value: 'YYYY-MM-DD', label: 'YYYY-MM-DD' },
  { value: 'DD Mon YYYY', label: 'DD Mon YYYY' },
];

const DATE_PATTERNS = {
  'DD/MM/YYYY': /^(\d{1,2})[/\-.](\d{1,2})[/\-.](\d{4})$/,
  'MM/DD/YYYY': /^(\d{1,2})[/\-.](\d{1,2})[/\-.](\d{4})$/,
  'DD/MM/YY': /^(\d{1,2})[/\-.](\d{1,2})[/\-.](\d{2})$/,
  'YYYY-MM-DD': /^(\d{4})[/\-.](\d{1,2})[/\-.](\d{1,2})$/,
  'DD Mon YYYY': /^(\d{1,2})\s+(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)[a-z]*\s+(\d{4})$/i,
};

function parseDateExplicit(str, format) {
  const m = String(str).match(DATE_PATTERNS[format]);
  if (!m) return null;
  if (format === 'DD/MM/YYYY') return `${m[3]}-${pad2(m[2])}-${pad2(m[1])}`;
  if (format === 'MM/DD/YYYY') return `${m[3]}-${pad2(m[1])}-${pad2(m[2])}`;
  if (format === 'YYYY-MM-DD') return `${m[1]}-${pad2(m[2])}-${pad2(m[3])}`;
  if (format === 'DD Mon YYYY') return `${m[3]}-${MONTHS[m[2].toLowerCase().substring(0, 3)]}-${pad2(m[1])}`;
  const year = parseInt(m[3]) > 50 ? `19${m[3]}` : `20${m[3]}`;
  return `${year}-${pad2(m[2])}-${pad2(m[1])}`;
}

function parseDateAuto(str) {
  const s = String(str);
  let m = s.match(DATE_PATTERNS['DD/MM/YYYY']);
  if (m) return `${m[3]}-${pad2(m[2])}-${pad2(m[1])}`;
  m = s.match(DATE_PATTERNS['YYYY-MM-DD']);
  if (m) return `${m[1]}-${pad2(m[2])}-${pad2(m[3])}`;
  m = s.match(DATE_PATTERNS['DD/MM/YY']);
  if (m) {
    const year = parseInt(m[3]) > 50 ? `19${m[3]}` : `20${m[3]}`;
    return `${year}-${pad2(m[2])}-${pad2(m[1])}`;
  }
  m = s.match(DATE_PATTERNS['DD Mon YYYY']);
  if (m) return `${m[3]}-${MONTHS[m[2].toLowerCase().substring(0, 3)]}-${pad2(m[1])}`;
  const d = new Date(s);
  if (!isNaN(d.getTime())) return formatDateOnly(d);
  return null;
}

function parseDate(str, format) {
  if (!str) return null;
  const value = String(str).trim();
  if (format !== 'auto') {
    const parsed = parseDateExplicit(value, format);
    if (parsed) return parsed;
  }
  return parseDateAuto(value);
}

function parseAmount(str) {
  if (str == null || str === '') return 0;
  if (typeof str === 'number') return Number.isFinite(str) ? str : 0;

  const cleaned = String(str).replace(/[^\d.,()+\-]/g, '').trim();
  if (!cleaned) return 0;

  let negative = false;
  let body = cleaned;

  // Parenthesised negatives: (1,234.56)
  if (body.startsWith('(') && body.endsWith(')')) {
    negative = true;
    body = body.slice(1, -1);
  }
  // Trailing minus: 1,234.56-
  if (body.endsWith('-')) {
    negative = true;
    body = body.slice(0, -1);
  }

  let parsed;
  if (/^\d{1,3}(\.\d{3})+(,\d+)?$/.test(body)) {
    // European style: 1.234.567,89
    parsed = parseFloat(body.replace(/\./g, '').replace(',', '.'));
  } else {
    // Remove thousands separators, use "." as decimal separator
    parsed = parseFloat(body.replace(/,/g, ''));
  }

  if (!Number.isFinite(parsed)) return 0;
  return negative ? -Math.abs(parsed) : parsed;
}

const getMappingErrors = (columnMapping, amountMode) => {
  const errors = [];
  if (!columnMapping.date) errors.push('Date field must be mapped to a CSV column');
  if (!columnMapping.description) errors.push('Description field must be mapped to a CSV column');
  if (amountMode === 'single' && !columnMapping.amount) {
    errors.push('Amount field must be mapped (or switch to separate Debit/Credit mode)');
  }
  if (amountMode === 'separate') {
    if (!columnMapping.debit) errors.push('Debit field must be mapped in separate mode');
    if (!columnMapping.credit) errors.push('Credit field must be mapped in separate mode');
  }
  return errors;
};

// Duplicate detection mirrors the backend fingerprint so that the count the
// user sees matches what the import endpoint would skip.
const FINGERPRINT_SEP = '\x00';
const fingerprintOf = (date, amount, type, description) =>
  `${date}${FINGERPRINT_SEP}${Math.round(amount * 100)}${FINGERPRINT_SEP}${type}${FINGERPRINT_SEP}${String(description || '').trim().toLowerCase()}`;

const apiDate = (d) => {
  const m = String(d || '').match(/^(\d{4})-(\d{2})-(\d{2})/);
  return m ? m[0] : '';
};

function buildParsedTransactions({ csvData, columnMapping, amountMode, dateFormat, accounts, accountTypes, payees, selectedAccount }) {
  if (!csvData) return [];

  const dateCol = columnMapping.date;
  const descCol = columnMapping.description;
  const amountCol = columnMapping.amount;
  const debitCol = columnMapping.debit;
  const creditCol = columnMapping.credit;
  const payeeCol = columnMapping.payee;

  const selAcct = accounts.find((a) => a.id === selectedAccount);
  const selType = accountTypes.find((at) => at.id === selAcct?.accountTypeId);
  const positiveTxnType = selType?.positiveTxnType || 'credit';

  return csvData
    .map((row) => {
      const rawDate = row[dateCol]?.trim();
      if (!rawDate) return null;

      const date = parseDate(rawDate, dateFormat);
      if (!date) return null;

      const description = row[descCol]?.trim() || '';
      if (!description) return null;

      let amount = 0;
      let type = 'debit';

      // Determine sign convention from account type
      if (amountMode === 'single' && amountCol) {
        const raw = parseAmount(row[amountCol]);
        if (raw < 0) {
          amount = Math.abs(raw);
          type = positiveTxnType === 'credit' ? 'debit' : 'credit';
        } else {
          amount = raw;
          type = positiveTxnType;
        }
      } else if (amountMode === 'separate') {
        const debitAmt = parseAmount(row[debitCol]);
        const creditAmt = parseAmount(row[creditCol]);
        if (debitAmt !== 0) {
          amount = Math.abs(debitAmt);
          type = 'debit';
        } else if (creditAmt !== 0) {
          amount = Math.abs(creditAmt);
          type = 'credit';
        } else {
          return null;
        }
      }

      if (amount === 0) return null;

      let payeeId = null;
      if (payeeCol && row[payeeCol]) {
        const name = row[payeeCol].trim().toLowerCase();
        const match = payees.find((p) => p.name.toLowerCase() === name);
        if (match) payeeId = match.id;
      }

      return { date, description, amount, type, payeeId };
    })
    .filter(Boolean);
}

export default function Import() {
  const [step, setStep] = useState(1);
  const [accounts, setAccounts] = useState([]);
  const [accountTypes, setAccountTypes] = useState([]);
  const [selectedAccount, setSelectedAccount] = useState('');
  const [newAccount, setNewAccount] = useState({ name: '', accountTypeId: 'bank', bank: '', color: '#06b6d4' });
  const [showNewAccount, setShowNewAccount] = useState(false);
  const [payees, setPayees] = useState([]);

  // CSV state
  const [csvData, setCsvData] = useState(null);
  const [csvHeaders, setCsvHeaders] = useState([]);
  const [columnMapping, setColumnMapping] = useState({});
  const [dateFormat, setDateFormat] = useState('auto');
  const [amountMode, setAmountMode] = useState('single'); // 'single' or 'separate'

  // Statement (PDF) state
  const [statementMode, setStatementMode] = useState('csv'); // 'csv' | 'pdf'
  const [parsing, setParsing] = useState(false);
  const [pdfPassword, setPdfPassword] = useState('');
  const [pdfDateFormat, setPdfDateFormat] = useState('auto');
  const [statementSummary, setStatementSummary] = useState(null);
  const [statementTxns, setStatementTxns] = useState(null);
  const [pdfFile, setPdfFile] = useState(null);
  const [extractors, setExtractors] = useState([]);
  const [extractor, setExtractor] = useState('sbi_cc');

  // Import results
  const [importing, setImporting] = useState(false);
  const [importResult, setImportResult] = useState(null);

  // Duplicate detection
  const [existingTxns, setExistingTxns] = useState([]);
  const [existingRefresh, setExistingRefresh] = useState(0);
  const [dupDialogOpen, setDupDialogOpen] = useState(false);

  const fileInputRef = useRef(null);
  const pdfInputRef = useRef(null);

  useEffect(() => {
    api.getAccounts().then(setAccounts).catch(console.error);
    api.getAccountTypes().then(setAccountTypes).catch(console.error);
    api.getPayees().then(setPayees).catch(console.error);
    api.getStatementExtractors()
      .then((res) => {
        const list = res?.extractors || [];
        setExtractors(list);
        if (list.length > 0) setExtractor(list[0].name);
      })
      .catch(console.error);
  }, []);

  // Load the account's existing transactions so duplicates can be flagged
  // before anything is imported.
  useEffect(() => {
    if (!selectedAccount) {
      setExistingTxns([]);
      return;
    }
    api
      .getTransactions({ accountId: selectedAccount, limit: 0 })
      .then((res) => setExistingTxns(res.data || []))
      .catch(console.error);
  }, [selectedAccount, existingRefresh]);

  // ---- Step 1: Select Account ----
  const handleCreateAccount = async () => {
    try {
      const acc = await api.createAccount(newAccount);
      setAccounts((prev) => [acc, ...prev]);
      setSelectedAccount(acc.id);
      setShowNewAccount(false);
      setNewAccount({ name: '', accountTypeId: 'bank', bank: '', color: '#06b6d4' });
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

  const parsePdf = async (file, chosenExtractor) => {
    setParsing(true);
    setStatementTxns(null);
    setStatementSummary(null);
    try {
      const fd = new FormData();
      fd.append('file', file);
      if (pdfPassword) fd.append('password', pdfPassword);
      if (pdfDateFormat !== 'auto') fd.append('date_format', pdfDateFormat);
      if (chosenExtractor) fd.append('extractor', chosenExtractor);
      const result = await api.parseStatement(fd);
      setStatementTxns(result.transactions || []);
      setStatementSummary(result.summary || null);
      setStep(4);
    } catch (err) {
      alert(err.message);
    } finally {
      setParsing(false);
    }
  };

  const handlePdfUpload = async (e) => {
    const file = e.target.files[0];
    if (!file) return;
    setPdfFile(file);
    await parsePdf(file, extractor);
  };

  const autoDetectMapping = (headers) => {
    const mapping = { date: null, description: null, amount: null, debit: null, credit: null, payee: null };
    const used = new Set();
    const pick = (patterns) => {
      for (const h of headers) {
        const lower = String(h).toLowerCase().trim();
        if (!used.has(h) && patterns.some((p) => p.test(lower))) {
          used.add(h);
          return h;
        }
      }
      return null;
    };

    mapping.date = pick([/date|txn.*date|transaction.*date|value.*date/i]);
    mapping.description = pick([/narration|description|particulars|details|remark/i]);
    mapping.amount = pick([/^amount$|^transaction.*amount$|^txn.*amount$/i]);
    mapping.debit = pick([/debit|withdrawal|dr/i]);
    mapping.credit = pick([/credit|deposit|cr/i]);
    mapping.payee = pick([/payee|beneficiary|merchant|receiver|sender/i]);
    return mapping;
  };

  // ---- Step 3: Column Mapping ----
  const updateMapping = (key, csvHeader) => {
    setColumnMapping((prev) => ({ ...prev, [key]: csvHeader || null }));
  };

  const mappingErrors = useMemo(
    () => (csvData ? getMappingErrors(columnMapping, amountMode) : []),
    [csvData, columnMapping, amountMode]
  );

  // Reverse lookup: which field each CSV column feeds, for highlighting.
  const csvTarget = useMemo(() => {
    const map = {};
    for (const f of TARGET_FIELDS) {
      const src = columnMapping[f.key];
      if (src) map[src] = f.key;
    }
    return map;
  }, [columnMapping]);

  // ---- Step 4: Preview & Import ----
  const parsedTransactions = useMemo(() => {
    if (statementTxns) return statementTxns;
    return buildParsedTransactions({ csvData, columnMapping, amountMode, dateFormat, accounts, accountTypes, payees, selectedAccount });
  }, [statementTxns, csvData, columnMapping, amountMode, dateFormat, accounts, accountTypes, payees, selectedAccount]);

  // Fingerprints of transactions already stored for the selected account. The
  // fingerprint formula mirrors the backend so counts match between preview and
  // the import endpoint.
  const existingSet = useMemo(
    () =>
      new Set(
        existingTxns.map((t) => fingerprintOf(apiDate(t.date), t.amount, t.type, t.description))
      ),
    [existingTxns]
  );

  const { dupCount, inFileDupCount, existingDupCount } = useMemo(() => {
    const seen = new Set();
    let inFileDup = 0;
    let existingDup = 0;
    let total = 0;
    for (const t of parsedTransactions) {
      const fp = fingerprintOf(t.date, t.amount, t.type, t.description);
      const matchesExisting = existingSet.has(fp);
      const repeatsInFile = seen.has(fp);
      if (matchesExisting) existingDup++;
      if (repeatsInFile) inFileDup++;
      if (matchesExisting || repeatsInFile) total++;
      seen.add(fp);
    }
    return { dupCount: total, inFileDupCount: inFileDup, existingDupCount: existingDup };
  }, [parsedTransactions, existingSet]);

  const runImport = async (action) => {
    setDupDialogOpen(false);
    setImporting(true);
    try {
      if (parsedTransactions.length === 0) {
        alert('No valid transactions found. Please check your column mapping.');
        return;
      }

      const result = await api.importTransactions({
        accountId: selectedAccount,
        transactions: parsedTransactions,
        duplicateAction: action,
      });
      setImportResult(result);
      setStep(5);
    } catch (err) {
      alert('Import failed: ' + err.message);
    } finally {
      setImporting(false);
    }
  };

  const handleImport = () => {
    if (dupCount > 0) {
      setDupDialogOpen(true);
      return;
    }
    runImport('keep');
  };

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
                      {a.name} ({a.accountTypeName}{a.bank ? `, ${a.bank}` : ''})
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
                    <select className="px-3.5 py-2.5 bg-slate-900 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all" value={newAccount.accountTypeId} onChange={(e) => setNewAccount({ ...newAccount, accountTypeId: e.target.value })}>
                      {accountTypes.map((at) => (
                        <option key={at.id} value={at.id}>{at.name}</option>
                      ))}
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

        {/* Step 2: Upload */}
        {step === 2 && (
          <div className="bg-slate-900 border border-slate-800 rounded-xl p-6" style={{ maxWidth: '600px' }}>
            <div className="flex gap-2 mb-6">
              <button
                className={`flex-1 inline-flex items-center justify-center gap-2 px-4 py-2.5 rounded-lg text-sm font-medium border transition-all ${statementMode === 'csv' ? 'bg-cyan-500 border-cyan-500 text-white' : 'bg-slate-950 border-slate-800 text-slate-400 hover:text-slate-200'}`}
                onClick={() => setStatementMode('csv')}
              >
                <FileSpreadsheet size={18} /> CSV
              </button>
              <button
                className={`flex-1 inline-flex items-center justify-center gap-2 px-4 py-2.5 rounded-lg text-sm font-medium border transition-all ${statementMode === 'pdf' ? 'bg-cyan-500 border-cyan-500 text-white' : 'bg-slate-950 border-slate-800 text-slate-400 hover:text-slate-200'}`}
                onClick={() => setStatementMode('pdf')}
              >
                <FileText size={18} /> Statement PDF
              </button>
            </div>

            {statementMode === 'csv' ? (
              <>
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
              </>
            ) : (
              <>
                <div
                  className="border-2 border-dashed border-slate-700 bg-slate-950/50 rounded-xl p-12 flex flex-col items-center justify-center text-center cursor-pointer transition-colors hover:border-cyan-500/50 hover:bg-slate-900/50 group"
                  onClick={() => pdfInputRef.current?.click()}
                  onDragOver={(e) => { e.preventDefault(); e.currentTarget.classList.add('border-cyan-500', 'bg-slate-900/80'); }}
                  onDragLeave={(e) => e.currentTarget.classList.remove('border-cyan-500', 'bg-slate-900/80')}
                  onDrop={(e) => {
                    e.preventDefault();
                    e.currentTarget.classList.remove('border-cyan-500', 'bg-slate-900/80');
                    const file = e.dataTransfer.files[0];
                    if (file) {
                      const dt = new DataTransfer();
                      dt.items.add(file);
                      pdfInputRef.current.files = dt.files;
                      handlePdfUpload({ target: { files: [file] } });
                    }
                  }}
                >
                  <div className="w-16 h-16 rounded-full bg-slate-800 flex items-center justify-center mb-4 group-hover:bg-cyan-500/20 text-slate-400 group-hover:text-cyan-400 transition-colors">
                    {parsing ? <Loader2 size={32} className="animate-spin" /> : <FileText size={32} />}
                  </div>
                  <h3 className="text-lg font-semibold text-slate-200 mb-2">{parsing ? 'Parsing statement...' : 'Drop your statement PDF here'}</h3>
                  <p className="text-sm text-slate-500">{parsing ? 'Extracting transactions from your statement.' : 'or click to browse. The extracted transactions will be shown for review.'}</p>
                </div>
                <input ref={pdfInputRef} type="file" accept=".pdf" className="hidden" onChange={handlePdfUpload} />
                <div className="mt-4 flex flex-col gap-1.5">
                  <label className="text-sm font-medium text-slate-400">Extractor</label>
                  <select
                    className="px-3.5 py-2.5 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all"
                    value={extractor}
                    onChange={(e) => setExtractor(e.target.value)}
                  >
                    {extractors.length === 0 && <option value="sbi_cc">SBI Credit Card</option>}
                    {extractors.map((ex) => (
                      <option key={ex.name} value={ex.name}>{ex.display_name || ex.name}</option>
                    ))}
                  </select>
                </div>
                <div className="mt-4 flex flex-col gap-1.5">
                  <label className="text-sm font-medium text-slate-400">Password (if the PDF is protected)</label>
                  <input
                    type="password"
                    className="px-3.5 py-2.5 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all"
                    placeholder="Optional"
                    value={pdfPassword}
                    onChange={(e) => setPdfPassword(e.target.value)}
                  />
                </div>
              </>
            )}
          </div>
        )}

        {/* Step 3: Column Mapping */}
        {step === 3 && (
          <div className="bg-slate-900 border border-slate-800 rounded-xl p-6" style={{ maxWidth: '800px' }}>
            <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-4">
              <h3 className="text-lg font-bold text-slate-100">Map CSV Columns</h3>
              <div className="flex flex-col sm:flex-row sm:items-center gap-3">
                <div className="flex items-center gap-2">
                  <label htmlFor="import-date-format" className="text-xs font-semibold text-slate-500 whitespace-nowrap">Date Format</label>
                  <select
                    id="import-date-format"
                    className="px-2.5 py-1.5 bg-slate-950 border border-slate-800 rounded-md text-slate-200 text-xs focus:outline-none focus:border-cyan-500 transition-all"
                    value={dateFormat}
                    onChange={(e) => setDateFormat(e.target.value)}
                  >
                    {DATE_FORMAT_OPTIONS.map((f) => (
                      <option key={f.value} value={f.value}>{f.label}</option>
                    ))}
                  </select>
                </div>
                <div className="flex gap-2">
                  <button className={`px-3 py-1.5 text-xs font-semibold rounded-md border transition-colors ${amountMode === 'single' ? 'bg-cyan-500 border-cyan-500 text-white' : 'bg-slate-950 border-slate-800 text-slate-400 hover:text-slate-200'}`} onClick={() => setAmountMode('single')}>Single Amount</button>
                  <button className={`px-3 py-1.5 text-xs font-semibold rounded-md border transition-colors ${amountMode === 'separate' ? 'bg-cyan-500 border-cyan-500 text-white' : 'bg-slate-950 border-slate-800 text-slate-400 hover:text-slate-200'}`} onClick={() => setAmountMode('separate')}>Debit / Credit</button>
                </div>
              </div>
            </div>

            <p className="text-sm text-slate-400 mb-6">
              Select which CSV column supplies each field below. Columns you don't map are ignored.
            </p>

            <div className="bg-slate-950 border border-slate-800 rounded-lg overflow-hidden">
              {targetFieldsFor(amountMode).map((f) => (
                <div key={f.key} className="grid grid-cols-[1fr_auto_1fr] items-center gap-6 p-4 border-b border-slate-800 last:border-0 hover:bg-slate-900/50 transition-colors">
                  <div className="flex flex-col gap-1 min-w-0">
                    <div className="text-[11px] font-semibold text-slate-500 uppercase tracking-wider">Field</div>
                    <div className="font-medium text-sm text-slate-200">
                      {f.label}
                      {f.required && <span className="ml-2 text-[10px] font-bold text-red-400 uppercase tracking-wide">Required</span>}
                    </div>
                    <div className="text-xs text-slate-500 truncate mt-0.5">
                      {columnMapping[f.key]
                        ? `e.g. "${csvData?.[0]?.[columnMapping[f.key]] || '—'}"`
                        : 'No CSV column selected'}
                    </div>
                  </div>
                  <ArrowRight className="text-slate-600 shrink-0" size={20} />
                  <div className="flex flex-col gap-1 min-w-0">
                    <div className="text-[11px] font-semibold text-slate-500 uppercase tracking-wider">From CSV Column</div>
                    <select
                      className="mt-1 px-3 py-2 bg-slate-900 border border-slate-700 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 w-full"
                      value={columnMapping[f.key] || ''}
                      onChange={(e) => updateMapping(f.key, e.target.value)}
                    >
                      <option value="">{f.required ? '— Select a column —' : '— Not mapped —'}</option>
                      {csvHeaders.map((h) => (
                        <option key={h} value={h}>{h}</option>
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
                          {csvTarget[h] && (
                            <div className="text-[10px] font-medium text-cyan-500 mt-0.5 tracking-wide">→ {csvTarget[h].toUpperCase()}</div>
                          )}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {csvData.slice(0, 5).map((row, i) => (
                      <tr key={i} className="border-b border-slate-800 last:border-0 hover:bg-slate-800/30">
                        {csvHeaders.map((h) => (
                          <td key={h} className={`py-2 px-4 text-xs max-w-[150px] overflow-hidden text-ellipsis whitespace-nowrap ${csvTarget[h] ? 'text-slate-300' : 'opacity-40 text-slate-500'}`}>
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
            <div className="flex flex-col sm:flex-row justify-between sm:items-end gap-2 mb-4">
              <h3 className="text-xl font-bold text-slate-100">Preview — {parsedTransactions.length} transactions</h3>
              {!statementTxns && csvData && (
                <div className="text-sm text-slate-400 font-medium">{csvData.length - parsedTransactions.length} rows skipped (empty/invalid)</div>
              )}
            </div>

            {statementTxns && pdfFile && (
              <div className="mb-5 p-4 bg-slate-950 border border-slate-800 rounded-lg flex flex-wrap items-end gap-3">
                <div className="flex flex-col gap-1.5 min-w-[180px]">
                  <label className="text-[11px] font-semibold text-slate-500 uppercase tracking-wider">Reparse with extractor</label>
                  <select
                    className="px-3 py-2 bg-slate-900 border border-slate-700 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all"
                    value={extractor}
                    onChange={(e) => setExtractor(e.target.value)}
                  >
                    {extractors.map((ex) => (
                      <option key={ex.name} value={ex.name}>{ex.display_name || ex.name}</option>
                    ))}
                  </select>
                </div>
                <div className="flex flex-col gap-1.5 min-w-[180px]">
                  <label className="text-[11px] font-semibold text-slate-500 uppercase tracking-wider">Date Format</label>
                  <select
                    className="px-3 py-2 bg-slate-900 border border-slate-700 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all"
                    value={pdfDateFormat}
                    onChange={(e) => setPdfDateFormat(e.target.value)}
                  >
                    {DATE_FORMAT_OPTIONS.map((f) => (
                      <option key={f.value} value={f.value}>{f.label}</option>
                    ))}
                  </select>
                </div>
                <button
                  className="inline-flex items-center gap-2 px-4 py-2 bg-slate-800 text-slate-200 border border-slate-700 rounded-lg text-sm font-medium hover:bg-slate-700 transition-all disabled:opacity-50 disabled:cursor-not-allowed"
                  onClick={() => parsePdf(pdfFile, extractor)}
                  disabled={parsing}
                >
                  {parsing ? <><Loader2 size={15} className="animate-spin" /> Reparsing...</> : <>Reparse PDF</>}
                </button>
                <p className="text-xs text-slate-500 w-full">Parsing didn't look right? Try a different extractor or date format to reprocess the same file.</p>
              </div>
            )}

            {statementSummary && (
              <div className="mb-5 p-4 bg-cyan-500/5 border border-cyan-500/20 rounded-lg">
                <div className="text-xs font-semibold text-cyan-400 uppercase tracking-wider mb-2">Statement Summary</div>
                <div className="flex flex-wrap gap-x-6 gap-y-1 text-sm text-slate-300">
                  {Object.entries(statementSummary).map(([k, v]) => (
                    <div key={k}>
                      <span className="text-slate-500 capitalize">{k.replace(/_/g, ' ')}: </span>
                      <span className="font-medium">{v}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {dupCount > 0 && (
              <div className="mb-5 p-4 bg-amber-500/10 border border-amber-500/25 rounded-lg flex gap-3 items-start">
                <AlertTriangle size={18} className="text-amber-500 shrink-0 mt-0.5" />
                <div className="text-sm text-amber-200">
                  <p className="font-semibold mb-1">{dupCount} transaction{dupCount === 1 ? '' : 's'} look like duplicates.</p>
                  <p className="text-amber-200/80">
                    {existingDupCount > 0 && <span>{existingDupCount} already exist{existingDupCount === 1 ? 's' : ''} in this account{inFileDupCount > 0 ? ', ' : '. '}</span>}
                    {inFileDupCount > 0 && <span>{inFileDupCount} repeat{inFileDupCount === 1 ? 's' : ''} within this file. </span>}
                    You'll be asked what to do before importing.
                  </p>
                </div>
              </div>
            )}

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
                        <span className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-semibold tracking-wide uppercase ${t.type === 'debit' ? 'bg-red-500/10 text-red-500 border border-red-500/20' : 'bg-emerald-500/10 text-emerald-500 border border-emerald-500/20'}`}>
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
              <button className="inline-flex items-center gap-2 px-4 py-2 bg-slate-800 text-slate-200 border border-slate-700 rounded-lg text-sm font-medium hover:bg-slate-700 transition-all" onClick={() => setStep(statementTxns ? 2 : 3)}>{statementTxns ? 'Back to Upload' : 'Back to Mapping'}</button>
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
              <span className="font-semibold text-slate-200">{importResult.imported}</span> of {importResult.total} transactions
              {importResult.duplicates > 0
                ? ` imported (${importResult.duplicates} duplicates skipped).`
                : ' imported.'}
            </p>
            <div className="flex flex-col sm:flex-row gap-4 justify-center">
              <button className="inline-flex justify-center items-center gap-2 px-6 py-2.5 bg-slate-800 text-slate-200 border border-slate-700 rounded-lg text-sm font-medium hover:bg-slate-700 transition-all" onClick={() => { setStep(1); setCsvData(null); setCsvHeaders([]); setColumnMapping({}); setImportResult(null); setStatementTxns(null); setStatementSummary(null); setPdfPassword(''); setPdfDateFormat('auto'); setPdfFile(null); setExistingRefresh(k => k + 1); }}>
                Import Another
              </button>
              <button className="inline-flex justify-center items-center gap-2 px-6 py-2.5 bg-linear-to-r from-cyan-500 to-blue-600 text-white rounded-lg text-sm font-medium hover:opacity-90 transition-all shadow-lg shadow-cyan-500/20" onClick={() => window.location.href = '/transactions'}>
                View Transactions
              </button>
              <button className="inline-flex justify-center items-center gap-2 px-6 py-2.5 bg-slate-800 text-slate-200 border border-slate-700 rounded-lg text-sm font-medium hover:bg-slate-700 transition-all" onClick={() => window.location.href = '/linking'}>
                Transfer Suggestions
              </button>
            </div>
          </div>
        )}

        {/* Duplicate handling dialog */}
        {dupDialogOpen && (
          <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/60" onClick={() => setDupDialogOpen(false)}>
            <div className="bg-slate-900 border border-slate-800 rounded-xl p-6 w-full max-w-md" onClick={(e) => e.stopPropagation()}>
              <div className="flex items-start justify-between mb-4">
                <div className="flex items-center gap-2.5 text-amber-400">
                  <AlertTriangle size={20} />
                  <h3 className="text-lg font-bold text-slate-100">Duplicate transactions found</h3>
                </div>
                <button className="text-slate-500 hover:text-slate-300 transition-colors" onClick={() => setDupDialogOpen(false)} aria-label="Close">
                  <X size={18} />
                </button>
              </div>
              <p className="text-sm text-slate-300 mb-4">
                {dupCount} of the {parsedTransactions.length} transactions match an existing transaction or repeat within this file.
              </p>
              <ul className="text-sm text-slate-400 mb-6 list-disc pl-5 space-y-1">
                {existingDupCount > 0 && <li>{existingDupCount} already {existingDupCount === 1 ? 'exists in' : 'exist in'} this account</li>}
                {inFileDupCount > 0 && <li>{inFileDupCount} repeat{inFileDupCount === 1 ? 's' : ''} within this file</li>}
              </ul>
              <p className="text-sm text-slate-500 mb-5">How would you like to handle them?</p>
              <div className="flex flex-col gap-3">
                <button className="inline-flex justify-center items-center gap-2 px-4 py-2.5 bg-linear-to-r from-cyan-500 to-blue-600 text-white rounded-lg text-sm font-medium hover:opacity-90 transition-all shadow-md shadow-cyan-500/20" onClick={() => runImport('skip')} disabled={importing}>
                  Skip duplicates
                </button>
                <button className="inline-flex justify-center items-center gap-2 px-4 py-2.5 bg-slate-800 text-slate-200 border border-slate-700 rounded-lg text-sm font-medium hover:bg-slate-700 transition-all" onClick={() => runImport('keep')} disabled={importing}>
                  Keep all (import everything)
                </button>
                <button className="inline-flex justify-center items-center gap-2 px-4 py-2.5 text-slate-400 text-sm font-medium hover:text-slate-200 transition-colors" onClick={() => setDupDialogOpen(false)} disabled={importing}>
                  Cancel
                </button>
              </div>
            </div>
          </div>
        )}
      </div>
    </>
  );
}
