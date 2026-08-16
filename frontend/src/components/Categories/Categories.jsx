import { useState, useEffect, useRef } from "react";
import { Plus, Trash2, Play, X, Edit2 } from "lucide-react";
import api from "../../api/client";
import { useSettings } from "../../context/SettingsContext";

export default function Categories() {
  const [categories, setCategories] = useState([]);
  const [rules, setRules] = useState([]);
  const [payees, setPayees] = useState([]);
  const [tab, setTab] = useState("categories");
  const [showNewCategory, setShowNewCategory] = useState(false);
  const [showNewRule, setShowNewRule] = useState(false);
  const [editingRule, setEditingRule] = useState(null);
  const [newCat, setNewCat] = useState({
    name: "",
    icon: "tag",
    color: "#06b6d4",
    type: "expense",
  });
  const [newRule, setNewRule] = useState({
    pattern: "",
    matchType: "contains",
    categoryId: "",
    payeeId: "",
    priority: 0,
  });
  const [applyResult, setApplyResult] = useState(null);
  const { compactLayout } = useSettings();
  const applyTimerRef = useRef(null);

  useEffect(() => {
    api.getCategories().then(setCategories).catch(console.error);
    api.getRules().then(setRules).catch(console.error);
    api.getPayees().then(setPayees).catch(console.error);
  }, []);

  const handleCreateCategory = async () => {
    try {
      const cat = await api.createCategory(newCat);
      setCategories((prev) => [...prev, cat]);
      setShowNewCategory(false);
      setNewCat({ name: "", icon: "tag", color: "#06b6d4", type: "expense" });
    } catch (err) {
      alert(err.message);
    }
  };

  const handleUpsertRule = async () => {
    const payload = {
      ...newRule,
      payeeId: newRule.payeeId || null,
    };

    try {
      if (editingRule) {
        await api.updateRule(editingRule.id, payload);
      } else {
        await api.createRule(payload);
      }
      setShowNewRule(false);
      setEditingRule(null);
      setNewRule({
        pattern: "",
        matchType: "contains",
        categoryId: "",
        payeeId: "",
        priority: 0,
      });
      const updatedRules = await api.getRules();
      setRules(updatedRules);
    } catch (err) {
      alert(err.message);
    }
  };

  const handleDeleteRule = async (id) => {
    if (!confirm("Delete this rule?")) return;
    try {
      await api.deleteRule(id);
      setRules((prev) => prev.filter((r) => r.id !== id));
    } catch (err) {
      alert(err.message);
    }
  };

  const handleApplyRules = async () => {
    try {
      const result = await api.applyRules();
      setApplyResult(result);
      clearTimeout(applyTimerRef.current);
      applyTimerRef.current = setTimeout(() => setApplyResult(null), 3000);
    } catch (err) {
      alert(err.message);
    }
  };

  useEffect(() => () => clearTimeout(applyTimerRef.current), []);

  const expenseCategories = categories.filter((c) => c.type === "expense");
  const incomeCategories = categories.filter((c) => c.type === "income");
  const transferCategories = categories.filter((c) => c.type === "transfer");

  return (
    <>
      <div className="shrink-0 px-8 pt-6">
        <h1 className="text-2xl font-bold mb-1">Categories & Rules</h1>
        <p className="text-slate-400 text-sm">
          Manage transaction categories and auto-categorization rules
        </p>
      </div>
      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto w-full">
        <div
          className={`flex gap-4 ${compactLayout ? "mb-4" : "mb-6"} border-b border-slate-800`}
        >
          <button
            className={`${compactLayout ? "px-3 py-2" : "px-4 py-3"} text-sm font-medium border-b-2 transition-colors ${tab === "categories" ? "border-cyan-500 text-cyan-400" : "border-transparent text-slate-400 hover:text-slate-200 hover:border-slate-700"}`}
            onClick={() => setTab("categories")}
          >
            Categories
          </button>
          <button
            className={`${compactLayout ? "px-3 py-2" : "px-4 py-3"} text-sm font-medium border-b-2 transition-colors ${tab === "rules" ? "border-cyan-500 text-cyan-400" : "border-transparent text-slate-400 hover:text-slate-200 hover:border-slate-700"}`}
            onClick={() => setTab("rules")}
          >
            Auto-Categorization Rules
          </button>
        </div>

        {tab === "categories" && (
          <>
            <div className="flex justify-between items-center mb-5">
              <span className="text-sm text-slate-400">
                {categories.length} categories
              </span>
              <button
                className="inline-flex items-center gap-2 px-4 py-2 bg-linear-to-r from-cyan-500 to-blue-600 text-white rounded-lg text-sm font-medium hover:opacity-90 transition-all shadow-lg shadow-cyan-500/20"
                onClick={() => setShowNewCategory(true)}
              >
                <Plus size={16} /> Add Category
              </button>
            </div>

            {showNewCategory && (
              <div className="bg-slate-900 border border-slate-800 rounded-xl p-6 mb-6">
                <div className="flex justify-between items-center mb-4">
                  <h4 className="text-base font-semibold">New Category</h4>
                  <button
                    className="p-1.5 text-slate-400 hover:text-slate-200 hover:bg-slate-800 rounded-lg transition-colors"
                    onClick={() => setShowNewCategory(false)}
                  >
                    <X size={16} />
                  </button>
                </div>
                <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mb-5">
                  <div className="flex flex-col gap-1.5">
                    <label className="text-sm font-medium text-slate-400">
                      Name
                    </label>
                    <input
                      className="px-3.5 py-2.5 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all"
                      placeholder="e.g. Gym"
                      value={newCat.name}
                      onChange={(e) =>
                        setNewCat({ ...newCat, name: e.target.value })
                      }
                    />
                  </div>
                  <div className="flex flex-col gap-1.5">
                    <label className="text-sm font-medium text-slate-400">
                      Type
                    </label>
                    <select
                      className="px-3.5 py-2.5 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all"
                      value={newCat.type}
                      onChange={(e) =>
                        setNewCat({ ...newCat, type: e.target.value })
                      }
                    >
                      <option value="expense">Expense</option>
                      <option value="income">Income</option>
                      <option value="transfer">Transfer</option>
                    </select>
                  </div>
                  <div className="flex flex-col gap-1.5">
                    <label className="text-sm font-medium text-slate-400">
                      Color
                    </label>
                    <input
                      type="color"
                      value={newCat.color}
                      onChange={(e) =>
                        setNewCat({ ...newCat, color: e.target.value })
                      }
                      className="w-full h-10.5 cursor-pointer bg-slate-950 border border-slate-800 rounded-lg p-1"
                    />
                  </div>
                </div>
                <button
                  className="inline-flex items-center gap-2 px-4 py-2 bg-linear-to-r from-cyan-500 to-blue-600 text-white rounded-lg text-sm font-medium hover:opacity-90 transition-all shadow-lg shadow-cyan-500/20 disabled:opacity-50 disabled:cursor-not-allowed"
                  onClick={handleCreateCategory}
                  disabled={!newCat.name}
                >
                  Create Category
                </button>
              </div>
            )}

            {[
              { label: "Expenses", items: expenseCategories },
              { label: "Income", items: incomeCategories },
              { label: "Transfers", items: transferCategories },
            ].map(
              ({ label, items }) =>
                items.length > 0 && (
                  <div key={label} className="mb-8">
                    <h4 className="text-xs font-semibold text-slate-500 uppercase tracking-widest mb-3">
                      {label}
                    </h4>
                    <div
                      className={`grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4 ${compactLayout ? "gap-2" : "gap-3"}`}
                    >
                      {items.map((cat) => (
                        <div
                          key={cat.id}
                          className={`flex items-center gap-3 ${compactLayout ? "px-3 py-1.5" : "px-4 py-3"} bg-slate-900 border border-slate-800 rounded-lg`}
                        >
                          <span
                            className="w-3 h-3 rounded-full shrink-0"
                            style={{ background: cat.color }}
                          />
                          <span className="text-sm font-medium text-slate-200">
                            {cat.name}
                          </span>
                        </div>
                      ))}
                    </div>
                  </div>
                ),
            )}
          </>
        )}

        {tab === "rules" && (
          <>
            <div className="flex justify-between items-center mb-5 flex-wrap gap-4">
              <div className="flex items-center gap-4">
                <span className="text-sm text-slate-400">
                  {rules.length} rules
                </span>
                <button
                  className="inline-flex items-center gap-2 px-4 py-2 bg-slate-800 text-slate-200 border border-slate-700 rounded-lg text-sm font-medium hover:bg-slate-700 transition-all"
                  onClick={handleApplyRules}
                >
                  <Play size={14} /> Apply Rules to Uncategorized
                </button>
                {applyResult && (
                  <span className="text-sm font-medium text-emerald-500">
                    {applyResult.updated} transactions updated
                  </span>
                )}
              </div>
              <button
                className="inline-flex items-center gap-2 px-4 py-2 bg-linear-to-r from-cyan-500 to-blue-600 text-white rounded-lg text-sm font-medium hover:opacity-90 transition-all shadow-lg shadow-cyan-500/20"
                onClick={() => {
                  setEditingRule(null);
                  setNewRule({
                    pattern: "",
                    matchType: "contains",
                    categoryId: "",
                    payeeId: "",
                    priority: 0,
                  });
                  setShowNewRule(true);
                }}
              >
                <Plus size={16} /> Add Rule
              </button>
            </div>

            {showNewRule && (
              <div className="bg-slate-900 border border-slate-800 rounded-xl p-6 mb-6">
                <div className="flex justify-between items-center mb-4">
                  <h4 className="text-base font-semibold">
                    {editingRule ? "Edit Rule" : "New Rule"}
                  </h4>
                  <button
                    className="p-1.5 text-slate-400 hover:text-slate-200 hover:bg-slate-800 rounded-lg transition-colors"
                    onClick={() => {
                      setShowNewRule(false);
                      setEditingRule(null);
                    }}
                  >
                    <X size={16} />
                  </button>
                </div>
                <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mb-4">
                  <div className="flex flex-col gap-1.5">
                    <label className="text-sm font-medium text-slate-400">
                      Pattern
                    </label>
                    <input
                      className="px-3.5 py-2.5 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all"
                      placeholder="e.g. SWIGGY, AMAZON, UBER"
                      value={newRule.pattern}
                      onChange={(e) =>
                        setNewRule({ ...newRule, pattern: e.target.value })
                      }
                    />
                  </div>
                  <div className="flex flex-col gap-1.5">
                    <label className="text-sm font-medium text-slate-400">
                      Match Type
                    </label>
                    <select
                      className="px-3.5 py-2.5 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all"
                      value={newRule.matchType}
                      onChange={(e) =>
                        setNewRule({ ...newRule, matchType: e.target.value })
                      }
                    >
                      <option value="contains">Contains</option>
                      <option value="starts_with">Starts With</option>
                      <option value="exact">Exact Match</option>
                    </select>
                  </div>
                </div>
                <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mb-5">
                  <div className="flex flex-col gap-1.5">
                    <label className="text-sm font-medium text-slate-400">
                      Assign Category
                    </label>
                    <select
                      className="px-3.5 py-2.5 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all"
                      value={newRule.categoryId}
                      onChange={(e) =>
                        setNewRule({ ...newRule, categoryId: e.target.value })
                      }
                    >
                      <option value="">Choose category...</option>
                      {categories.map((c) => (
                        <option key={c.id} value={c.id}>
                          {c.name}
                        </option>
                      ))}
                    </select>
                  </div>
                  <div className="flex flex-col gap-1.5">
                    <label className="text-sm font-medium text-slate-400">
                      Assign Payee (optional)
                    </label>
                    <select
                      className="px-3.5 py-2.5 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all"
                      value={newRule.payeeId || ""}
                      onChange={(e) =>
                        setNewRule({
                          ...newRule,
                          payeeId: e.target.value || null,
                        })
                      }
                    >
                      <option value="">No Payee</option>
                      {payees.map((p) => (
                        <option key={p.id} value={p.id}>
                          {p.name}
                        </option>
                      ))}
                    </select>
                  </div>
                  <div className="flex flex-col gap-1.5">
                    <label className="text-sm font-medium text-slate-400">
                      Priority (higher = first)
                    </label>
                    <input
                      type="number"
                      className="px-3.5 py-2.5 bg-slate-950 border border-slate-800 rounded-lg text-slate-200 text-sm focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all"
                      value={newRule.priority}
                      onChange={(e) =>
                        setNewRule({
                          ...newRule,
                          priority: parseInt(e.target.value) || 0,
                        })
                      }
                    />
                  </div>
                </div>
                <button
                  className="inline-flex items-center gap-2 px-4 py-2 bg-linear-to-r from-cyan-500 to-blue-600 text-white rounded-lg text-sm font-medium hover:opacity-90 transition-all shadow-lg shadow-cyan-500/20 disabled:opacity-50 disabled:cursor-not-allowed"
                  onClick={handleUpsertRule}
                  disabled={!newRule.pattern || !newRule.categoryId}
                >
                  {editingRule ? "Update Rule" : "Create Rule"}
                </button>
              </div>
            )}

            <div className="bg-slate-900 border border-slate-800 rounded-xl overflow-x-auto">
              <table className="w-full text-left border-collapse">
                <thead>
                  <tr>
                    <th
                      className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-xs font-semibold uppercase tracking-wider text-slate-500 bg-slate-800/50 border-b border-slate-800 whitespace-nowrap`}
                    >
                      Pattern
                    </th>
                    <th
                      className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-xs font-semibold uppercase tracking-wider text-slate-500 bg-slate-800/50 border-b border-slate-800 whitespace-nowrap`}
                    >
                      Match
                    </th>
                    <th
                      className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-xs font-semibold uppercase tracking-wider text-slate-500 bg-slate-800/50 border-b border-slate-800 whitespace-nowrap`}
                    >
                      Category
                    </th>
                    <th
                      className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-xs font-semibold uppercase tracking-wider text-slate-500 bg-slate-800/50 border-b border-slate-800 whitespace-nowrap`}
                    >
                      Payee
                    </th>
                    <th
                      className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-xs font-semibold uppercase tracking-wider text-slate-500 bg-slate-800/50 border-b border-slate-800 whitespace-nowrap`}
                    >
                      Priority
                    </th>
                    <th
                      className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-xs font-semibold uppercase tracking-wider text-slate-500 bg-slate-800/50 border-b border-slate-800 w-12.5`}
                    ></th>
                  </tr>
                </thead>
                <tbody>
                  {rules.length === 0 ? (
                    <tr>
                      <td
                        colSpan={5}
                        className="text-center p-10 text-slate-500"
                      >
                        No rules yet. Create one to auto-categorize
                        transactions.
                      </td>
                    </tr>
                  ) : (
                    rules.map((r) => (
                      <tr
                        key={r.id}
                        className="hover:bg-slate-800/30 transition-colors border-b border-slate-800 last:border-0"
                      >
                        <td
                          className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm font-medium text-slate-200`}
                        >
                          "{r.pattern}"
                        </td>
                        <td
                          className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm`}
                        >
                          <span className="inline-flex items-center px-2 py-1 rounded bg-slate-950 border border-slate-800 text-xs font-medium text-slate-400 capitalize">
                            {r.matchType.replace("_", " ")}
                          </span>
                        </td>
                        <td
                          className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm text-slate-200`}
                        >
                          {r.categoryName}
                        </td>
                        <td
                          className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm text-slate-400`}
                        >
                          {r.payee || "—"}
                        </td>
                        <td
                          className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm text-slate-400`}
                        >
                          {r.priority}
                        </td>
                        <td
                          className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-right`}
                        >
                          <div className="flex justify-end gap-1">
                            <button
                              className="p-1.5 text-slate-500 hover:text-cyan-400 hover:bg-slate-800 rounded transition-colors"
                              onClick={() => {
                                setEditingRule(r);
                                setNewRule({
                                  pattern: r.pattern,
                                  matchType: r.matchType,
                                  categoryId: r.categoryId,
                                  payeeId: r.payeeId,
                                  priority: r.priority,
                                });
                                setShowNewRule(true);
                              }}
                            >
                              <Edit2 size={16} />
                            </button>
                            <button
                              className="p-1.5 text-slate-500 hover:text-red-400 hover:bg-red-500/10 rounded transition-colors"
                              onClick={() => handleDeleteRule(r.id)}
                            >
                              <Trash2 size={16} />
                            </button>
                          </div>
                        </td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>
          </>
        )}
      </div>
    </>
  );
}
