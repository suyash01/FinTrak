import { useState, useEffect } from "react";
import {
  Plus,
  Trash2,
  CreditCard,
  Building2,
  X,
  Edit2,
  Download,
  Star,
} from "lucide-react";
import api, { downloadCSV } from "../../api/client";
import { formatDate, formatCurrency } from "../../utils/formatters";
import { useSettings } from "../../context/SettingsContext";

export default function Accounts() {
  const [accounts, setAccounts] = useState([]);
  const [accountTypes, setAccountTypes] = useState([]);
  const [showNew, setShowNew] = useState(false);
  const [newAcc, setNewAcc] = useState({
    name: "",
    accountTypeId: "bank",
    bank: "",
    color: "#06b6d4",
    billingDay: "",
  });
  const [editingId, setEditingId] = useState(null);
  const [editAcc, setEditAcc] = useState(null);
  const { compactLayout } = useSettings();

  useEffect(() => {
    api.getAccounts().then(setAccounts).catch(console.error);
    api.getAccountTypes().then(setAccountTypes).catch(console.error);
  }, []);

  const handleCreate = async () => {
    try {
      const acc = await api.createAccount({
        ...newAcc,
        billingDay: newAcc.billingDay === "" ? null : Number(newAcc.billingDay),
      });
      setAccounts((prev) => [acc, ...prev]);
      setShowNew(false);
      setNewAcc({
        name: "",
        accountTypeId: "bank",
        bank: "",
        color: "#06b6d4",
        billingDay: "",
      });
    } catch (err) {
      alert(err.message);
    }
  };

  const handleDelete = async (id) => {
    if (!confirm("Delete this account and all its transactions?")) return;
    try {
      await api.deleteAccount(id);
      setAccounts((prev) => prev.filter((a) => a.id !== id));
    } catch (err) {
      alert(err.message);
    }
  };

  const handleUpdate = async () => {
    try {
      const updated = await api.updateAccount(editingId, {
        ...editAcc,
        billingDay:
          editAcc.billingDay === "" ? null : Number(editAcc.billingDay),
      });
      setAccounts((prev) =>
        prev.map((a) => (a.id === editingId ? updated : a)),
      );
      setEditingId(null);
      setEditAcc(null);
    } catch (err) {
      alert(err.message);
    }
  };

  const startEdit = (acc) => {
    setEditingId(acc.id);
    setEditAcc({
      name: acc.name,
      accountTypeId: acc.accountTypeId,
      bank: acc.bank || "",
      color: acc.color,
      currency: acc.currency,
      billingDay: acc.billingDay ?? "",
    });
  };

  const handleExport = async (id) => {
    try {
      await downloadCSV(`/accounts/${id}/export`);
    } catch (err) {
      alert(err.message);
    }
  };

  // Mark/unmark an account as the user's default. The backend enforces a single
  // default per user, so when one is set the others are cleared.
  const handleSetDefault = async (acc) => {
    try {
      const updated = await api.updateAccount(acc.id, {
        name: acc.name,
        accountTypeId: acc.accountTypeId,
        bank: acc.bank || "",
        currency: acc.currency,
        color: acc.color,
        billingDay: acc.billingDay ?? null,
        isDefault: !acc.isDefault,
      });
      setAccounts((prev) =>
        prev.map((a) => {
          if (a.id === updated.id) return updated;
          if (updated.isDefault) return { ...a, isDefault: false };
          return a;
        }),
      );
    } catch (err) {
      alert(err.message);
    }
  };

  const getTypeIcon = (accountTypeId, color, size) => {
    return accountTypeId === "credit_card" ? (
      <CreditCard size={size} style={{ color }} />
    ) : (
      <Building2 size={size} style={{ color }} />
    );
  };

  return (
    <>
      <div className="shrink-0 px-8 pt-6">
        <h1 className="text-2xl font-bold mb-1">Accounts</h1>
        <p className="text-slate-400 text-sm">
          Manage your bank accounts and credit cards
        </p>
      </div>
      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto w-full">
        <div className="flex justify-end mb-5">
          <button
            className="inline-flex items-center gap-2 px-4 py-2 bg-linear-to-r from-cyan-500 to-blue-600 text-white rounded-lg text-sm font-medium hover:opacity-90 transition-all shadow-lg shadow-cyan-500/20"
            onClick={() => setShowNew(true)}
          >
            <Plus size={16} /> Add Account
          </button>
        </div>

        {showNew && (
          <div className="bg-slate-900 border border-slate-800 rounded-xl p-6 mb-5">
            <div className="flex justify-between items-center mb-4">
              <h4 className="text-base font-semibold">New Account</h4>
              <button
                className="p-1.5 text-slate-400 hover:text-slate-200 hover:bg-slate-800 rounded-lg transition-colors"
                onClick={() => setShowNew(false)}
              >
                <X size={16} />
              </button>
            </div>
            <div className="grid grid-cols-1 md:grid-cols-4 gap-4 mb-5">
              <div className="flex flex-col gap-1.5">
                <label className="text-sm font-medium text-slate-400">
                  Name
                </label>
                <input
                  className="px-3.5 py-2.5 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all"
                  placeholder="e.g. HDFC Savings"
                  value={newAcc.name}
                  onChange={(e) =>
                    setNewAcc({ ...newAcc, name: e.target.value })
                  }
                />
              </div>
              <div className="flex flex-col gap-1.5">
                <label className="text-sm font-medium text-slate-400">
                  Type
                </label>
                <select
                  className="px-3.5 py-2.5 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all"
                  value={newAcc.accountTypeId}
                  onChange={(e) =>
                    setNewAcc({ ...newAcc, accountTypeId: e.target.value })
                  }
                >
                  {accountTypes.map((at) => (
                    <option key={at.id} value={at.id}>
                      {at.name}
                    </option>
                  ))}
                </select>
              </div>
              <div className="flex flex-col gap-1.5">
                <label className="text-sm font-medium text-slate-400">
                  Bank
                </label>
                <input
                  className="px-3.5 py-2.5 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all"
                  placeholder="e.g. HDFC"
                  value={newAcc.bank}
                  onChange={(e) =>
                    setNewAcc({ ...newAcc, bank: e.target.value })
                  }
                />
              </div>
              <div className="flex flex-col gap-1.5">
                <label className="text-sm font-medium text-slate-400">
                  Color
                </label>
                <input
                  type="color"
                  value={newAcc.color}
                  onChange={(e) =>
                    setNewAcc({ ...newAcc, color: e.target.value })
                  }
                  className="w-full h-[42px] cursor-pointer bg-slate-950 border border-slate-800 rounded-lg p-1"
                />
              </div>
            </div>
            {newAcc.accountTypeId === "credit_card" && (
              <div className="flex flex-col gap-1.5 mb-5 max-w-xs">
                <label className="text-sm font-medium text-slate-400">
                  Billing Day (1-31)
                </label>
                <input
                  type="number"
                  min="1"
                  max="31"
                  className="px-3.5 py-2.5 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all"
                  placeholder="e.g. 5"
                  value={newAcc.billingDay}
                  onChange={(e) =>
                    setNewAcc({ ...newAcc, billingDay: e.target.value })
                  }
                />
              </div>
            )}
            <button
              className="inline-flex items-center gap-2 px-4 py-2 bg-linear-to-r from-cyan-500 to-blue-600 text-white rounded-lg text-sm font-medium hover:opacity-90 transition-all shadow-lg shadow-cyan-500/20 disabled:opacity-50 disabled:cursor-not-allowed"
              onClick={handleCreate}
              disabled={!newAcc.name}
            >
              Create
            </button>
          </div>
        )}

        {accounts.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-20 px-4 text-center">
            <Building2 className="w-16 h-16 text-slate-600 mb-4 opacity-50" />
            <h3 className="text-lg font-semibold mb-2">No Accounts Yet</h3>
            <p className="text-slate-400 text-sm mb-6 max-w-md">
              Add a bank account or credit card to start importing statements
              and categorizing your transactions.
            </p>
            <button
              className="inline-flex items-center gap-2 px-4 py-2 bg-linear-to-r from-cyan-500 to-blue-600 text-white rounded-lg text-sm font-medium hover:opacity-90 transition-all shadow-lg shadow-cyan-500/20"
              onClick={() => setShowNew(true)}
            >
              Add Account
            </button>
          </div>
        ) : (
          <div
            className={`grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 ${compactLayout ? "gap-3" : "gap-5"}`}
          >
            {accounts.map((acc) => (
              <div
                key={acc.id}
                className={`bg-slate-900 border border-slate-800 rounded-xl ${compactLayout ? "p-3" : "p-5"} hover:border-slate-700 transition-colors flex flex-col`}
                style={{
                  borderLeftColor:
                    editingId === acc.id ? editAcc.color : acc.color,
                  borderLeftWidth: "3px",
                }}
              >
                {editingId === acc.id ? (
                  <div className="flex flex-col h-full">
                    <div className="flex justify-between items-center mb-4">
                      <h4 className="text-sm font-semibold">Edit Account</h4>
                      <button
                        className="p-1.5 text-slate-400 hover:text-slate-200 hover:bg-slate-800 rounded-lg transition-colors"
                        onClick={() => setEditingId(null)}
                      >
                        <X size={14} />
                      </button>
                    </div>
                    <div className="flex flex-col gap-3 mb-5 flex-1">
                      <input
                        className="px-3.5 py-2.5 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all"
                        placeholder="Name"
                        value={editAcc.name}
                        onChange={(e) =>
                          setEditAcc({ ...editAcc, name: e.target.value })
                        }
                      />
                      <select
                        className="px-3.5 py-2.5 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all"
                        value={editAcc.accountTypeId}
                        onChange={(e) =>
                          setEditAcc({
                            ...editAcc,
                            accountTypeId: e.target.value,
                          })
                        }
                      >
                        {accountTypes.map((at) => (
                          <option key={at.id} value={at.id}>
                            {at.name}
                          </option>
                        ))}
                      </select>
                      <input
                        className="px-3.5 py-2.5 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all"
                        placeholder="Bank"
                        value={editAcc.bank}
                        onChange={(e) =>
                          setEditAcc({ ...editAcc, bank: e.target.value })
                        }
                      />
                      <input
                        type="color"
                        value={editAcc.color}
                        onChange={(e) =>
                          setEditAcc({ ...editAcc, color: e.target.value })
                        }
                        className="w-full h-[42px] cursor-pointer bg-slate-950 border border-slate-800 rounded-lg p-1"
                      />
                      {editAcc.accountTypeId === "credit_card" && (
                        <input
                          type="number"
                          min="1"
                          max="31"
                          className="px-3.5 py-2.5 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all"
                          placeholder="Billing Day (1-31)"
                          value={editAcc.billingDay}
                          onChange={(e) =>
                            setEditAcc({
                              ...editAcc,
                              billingDay: e.target.value,
                            })
                          }
                        />
                      )}
                    </div>
                    <button
                      className="inline-flex items-center justify-center w-full gap-2 px-4 py-2 bg-linear-to-r from-cyan-500 to-blue-600 text-white rounded-lg text-sm font-medium hover:opacity-90 transition-all disabled:opacity-50"
                      onClick={handleUpdate}
                      disabled={!editAcc.name}
                    >
                      Save
                    </button>
                  </div>
                ) : (
                  <div className="flex justify-between items-start h-full">
                    <div className="flex flex-col h-full flex-1">
                      <div
                        className={`flex items-center gap-3 ${compactLayout ? "mb-0.5" : "mb-1"}`}
                      >
                        {getTypeIcon(
                          acc.accountTypeId,
                          acc.color,
                          compactLayout ? 18 : 20,
                        )}
                        <h3
                          className={`${compactLayout ? "text-sm" : "text-base"} font-semibold text-slate-100 truncate`}
                        >
                          {acc.name}
                        </h3>
                        {acc.isDefault && (
                          <span className="shrink-0 text-[9px] font-bold bg-amber-500/20 text-amber-400 px-1.5 py-0.5 rounded uppercase tracking-wider">
                            Default
                          </span>
                        )}
                      </div>
                      <div
                        className={`${compactLayout ? "text-[12px] mb-2" : "text-[13px] mb-4"} text-slate-400 flex items-center gap-3`}
                      >
                        <span>{acc.accountTypeName}</span>
                        {acc.bank && (
                          <>
                            <span className="w-1 h-1 rounded-full bg-slate-700"></span>
                            <span>{acc.bank}</span>
                          </>
                        )}
                      </div>

                      <div className={compactLayout ? "mb-2" : "mb-4"}>
                        <div className="text-[11px] uppercase tracking-wider text-slate-500 font-medium mb-1">
                          {acc.accountTypeId === "credit_card"
                            ? "Outstanding"
                            : "Balance"}
                        </div>
                        <div
                          className={`${compactLayout ? "text-xl" : "text-2xl"} font-bold text-slate-100 font-mono`}
                        >
                          {formatCurrency(acc.balance, acc.currency)}
                        </div>
                      </div>

                      <div className="text-xs text-slate-500 mt-auto pt-4 border-t border-slate-800/50">
                        Added {formatDate(acc.createdAt)}
                      </div>
                    </div>
                    <div className="flex gap-1 -mr-2 -mt-2">
                      <button
                        className={`p-2 rounded-lg transition-colors ${acc.isDefault ? "text-amber-400 hover:text-amber-300 hover:bg-amber-500/10" : "text-slate-500 hover:text-amber-400 hover:bg-slate-800"}`}
                        onClick={() => handleSetDefault(acc)}
                        title={
                          acc.isDefault
                            ? "Remove as default account"
                            : "Set as default account"
                        }
                      >
                        <Star
                          size={16}
                          fill={acc.isDefault ? "currentColor" : "none"}
                        />
                      </button>
                      <button
                        className="p-2 text-slate-400 hover:text-cyan-400 hover:bg-slate-800 rounded-lg transition-colors"
                        onClick={() => handleExport(acc.id)}
                        title="Export CSV"
                      >
                        <Download size={16} />
                      </button>
                      <button
                        className="p-2 text-slate-400 hover:text-white hover:bg-slate-800 rounded-lg transition-colors"
                        onClick={() => startEdit(acc)}
                        title="Edit Account"
                      >
                        <Edit2 size={16} />
                      </button>
                      <button
                        className="p-2 text-slate-400 hover:text-red-400 hover:bg-red-500/10 rounded-lg transition-colors"
                        onClick={() => handleDelete(acc.id)}
                        title="Delete Account"
                      >
                        <Trash2 size={16} />
                      </button>
                    </div>
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
      </div>
    </>
  );
}
