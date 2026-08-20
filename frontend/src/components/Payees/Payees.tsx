import { useState, useEffect, type FormEvent } from "react";
import {
  Plus,
  Search,
  Trash2,
  Edit2,
  Users,
  ReceiptText,
  X,
  Wallet,
  AlertCircle,
} from "lucide-react";
import api from "../../api/client";
import type { Payee, Account } from "../../types";

interface PayeeForm {
  name: string;
  accountId: string;
}

const EMPTY_FORM: PayeeForm = { name: "", accountId: "" };

export default function Payees() {
  const [payees, setPayees] = useState<Payee[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [showModal, setShowModal] = useState(false);
  const [editingPayee, setEditingPayee] = useState<Payee | null>(null);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [formData, setFormData] = useState<PayeeForm>(EMPTY_FORM);

  useEffect(() => {
    fetchPayees();
    fetchAccounts();
  }, []);

  const fetchAccounts = async () => {
    try {
      const data = await api.getAccounts();
      setAccounts(data);
    } catch (err) {
      console.error(err);
    }
  };

  const fetchPayees = async () => {
    setLoading(true);
    try {
      const data = await api.getPayees();
      setPayees(data);
    } catch (err) {
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    try {
      if (editingPayee) {
        await api.updatePayee(editingPayee.id, {
          name: formData.name,
          accountId: formData.accountId || null,
        });
      } else {
        await api.createPayee({
          name: formData.name,
          accountId: formData.accountId || null,
        });
      }
      setShowModal(false);
      setEditingPayee(null);
      setFormData(EMPTY_FORM);
      fetchPayees();
    } catch (err) {
      alert((err as Error).message);
    }
  };

  const handleDelete = async (id: string) => {
    if (
      !confirm(
        "Are you sure you want to delete this payee? This will NOT delete transactions but will remove the link.",
      )
    )
      return;
    try {
      await api.deletePayee(id);
      fetchPayees();
    } catch (err) {
      alert((err as Error).message);
    }
  };

  const filteredPayees = payees.filter((p) =>
    p.name.toLowerCase().includes(search.toLowerCase()),
  );

  return (
    <div className="flex flex-col h-full">
      <div className="shrink-0 px-8 pt-6">
        <div className="flex justify-between items-center">
          <div>
            <h1 className="text-2xl font-bold text-slate-100 flex items-center gap-2">
              <Users className="text-cyan-500" />
              Payees
            </h1>
            <p className="text-slate-400 text-sm mt-1">
              Manage entities you pay or receive money from
            </p>
          </div>
          <button
            onClick={() => {
              setEditingPayee(null);
              setFormData(EMPTY_FORM);
              setShowModal(true);
            }}
            className="inline-flex items-center gap-2 px-4 py-2.5 bg-linear-to-r from-cyan-500 to-blue-600 text-white rounded-xl text-sm font-bold shadow-lg shadow-cyan-500/20 transition-all hover:scale-105 active:scale-95"
          >
            <Plus size={18} />
            Add Payee
          </button>
        </div>
      </div>

      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto w-full space-y-6">
        {/* Search Bar */}
        <div className="relative group max-w-2xl">
          <Search
            className="absolute left-4 top-1/2 -translate-y-1/2 text-slate-500 group-focus-within:text-cyan-500 transition-colors"
            size={18}
          />
          <input
            type="text"
            placeholder="Search payees..."
            className="w-full bg-slate-900 border border-slate-800 rounded-2xl pl-12 pr-4 py-3 text-slate-200 focus:outline-none focus:border-cyan-500 transition-all shadow-xl"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>

        {/* Grid */}
        {loading ? (
          <div className="flex justify-center p-20">
            <div className="animate-spin rounded-full h-10 w-10 border-b-2 border-cyan-500"></div>
          </div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
            {filteredPayees.map((payee) => (
              <div
                key={payee.id}
                className="bg-slate-900 border border-slate-800 p-5 rounded-2xl hover:border-slate-700 transition-all group relative shadow-md"
              >
                <div className="flex justify-between items-start">
                  <div className="flex items-center gap-3">
                    <div
                      className={`w-10 h-10 rounded-xl flex items-center justify-center border shadow-inner ${payee.accountId ? "bg-violet-500/10 text-violet-400 border-violet-500/20" : "bg-slate-950 text-cyan-500 border-slate-800"}`}
                    >
                      {payee.accountId ? (
                        <Wallet size={20} />
                      ) : (
                        <ReceiptText size={20} />
                      )}
                    </div>
                    <div>
                      <div className="flex items-center gap-2">
                        <h3 className="font-bold text-slate-200">
                          {payee.name}
                        </h3>
                        {payee.accountId && (
                          <span className="text-[9px] font-bold bg-violet-500/20 text-violet-400 px-1.5 py-0.5 rounded uppercase tracking-wider">
                            Account
                          </span>
                        )}
                      </div>
                      <p className="text-[10px] text-slate-500 mt-0.5 font-mono">
                        ID: {payee.id.slice(0, 8)}
                      </p>
                    </div>
                  </div>
                  <div className="flex gap-1">
                    <button
                      onClick={() => {
                        setEditingPayee(payee);
                        setFormData({
                          name: payee.name,
                          accountId: payee.accountId || "",
                        });
                        setShowModal(true);
                      }}
                      className="p-2 text-slate-500 hover:text-cyan-400 hover:bg-slate-800 rounded-lg transition-all"
                      title="Edit Payee"
                    >
                      <Edit2 size={16} />
                    </button>
                    {!payee.accountId && (
                      <button
                        onClick={() => handleDelete(payee.id)}
                        className="p-2 text-slate-500 hover:text-red-400 hover:bg-slate-800 rounded-lg transition-all"
                        title="Delete Payee"
                      >
                        <Trash2 size={16} />
                      </button>
                    )}
                  </div>
                </div>
              </div>
            ))}
            {filteredPayees.length === 0 && !loading && (
              <div className="col-span-full py-20 text-center border-2 border-dashed border-slate-800 rounded-2xl">
                <Users size={40} className="mx-auto text-slate-700 mb-4" />
                <p className="text-slate-500">No payees found.</p>
              </div>
            )}
          </div>
        )}
      </div>

      {/* Modal */}
      {showModal && (
        <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-slate-950/80 backdrop-blur-sm">
          <div className="bg-slate-900 border border-slate-800 rounded-2xl w-full max-w-md shadow-2xl overflow-hidden animate-in fade-in zoom-in duration-200">
            <div className="px-6 py-4 border-b border-slate-800 flex justify-between items-center bg-slate-900/50">
              <h3 className="text-lg font-bold">
                {editingPayee ? "Edit Payee" : "Add New Payee"}
              </h3>
              <button
                onClick={() => setShowModal(false)}
                className="text-slate-500 hover:text-white transition-colors"
              >
                <X size={20} />
              </button>
            </div>
            <form onSubmit={handleSubmit} className="p-6 space-y-4">
              <div>
                <label className="block text-xs font-bold text-slate-500 uppercase tracking-wider mb-2">
                  Payee Name
                </label>
                <input
                  type="text"
                  required
                  autoFocus
                  disabled={!!editingPayee?.accountId}
                  placeholder="e.g. Amazon, Google, etc."
                  className="w-full bg-slate-950 border border-slate-800 rounded-xl px-4 py-3 text-slate-200 focus:outline-none focus:border-cyan-500 disabled:opacity-50 disabled:cursor-not-allowed transition-all font-medium"
                  value={formData.name}
                  onChange={(e) =>
                    setFormData({ ...formData, name: e.target.value })
                  }
                />
                {editingPayee?.accountId && (
                  <p className="text-[10px] text-slate-500 mt-2 flex items-center gap-1">
                    <AlertCircle size={12} /> Account-linked payees must be
                    renamed via the Accounts page.
                  </p>
                )}
              </div>

              <div>
                <label className="block text-xs font-bold text-slate-500 uppercase tracking-wider mb-2">
                  Link to Account (Optional)
                </label>
                <select
                  className="w-full bg-slate-950 border border-slate-800 rounded-xl px-4 py-3 text-slate-200 focus:outline-none focus:border-cyan-500 transition-all font-medium"
                  value={formData.accountId}
                  onChange={(e) =>
                    setFormData({ ...formData, accountId: e.target.value })
                  }
                >
                  <option value="">No linked account</option>
                  {accounts.map((acc) => (
                    <option key={acc.id} value={acc.id}>
                      {acc.name} ({acc.bank || acc.accountTypeName})
                    </option>
                  ))}
                </select>
                <p className="text-[10px] text-slate-500 mt-2">
                  Linking to an account helps identify internal transfers.
                </p>
              </div>
              <div className="flex justify-end pt-2 gap-3">
                <button
                  type="button"
                  onClick={() => setShowModal(false)}
                  className="px-6 py-2.5 rounded-xl text-sm font-bold text-slate-400 hover:text-white transition-colors"
                >
                  Cancel
                </button>
                <button
                  type="submit"
                  className="bg-cyan-500 hover:bg-cyan-600 text-white px-8 py-2.5 rounded-xl font-bold shadow-lg shadow-cyan-500/20 transition-all hover:scale-105 active:scale-95"
                >
                  {editingPayee ? "Save Changes" : "Create Payee"}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
}
