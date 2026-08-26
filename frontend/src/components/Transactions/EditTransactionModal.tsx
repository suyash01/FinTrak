import {
  useState,
  useEffect,
  useRef,
  type FormEvent,
  type KeyboardEvent,
} from "react";
import {
  X,
  Save,
  Pencil,
  Calendar,
  DollarSign,
  FileText,
  Tag,
  User,
  Landmark,
  ArrowDownLeft,
  ArrowUpRight,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import api from "../../api/client";
import { formatDate } from "../../utils/formatters";
import { buildCategorySections } from "../../lib/categories";
import type {
  Transaction,
  Account,
  Category,
  CategoryGroup,
  Payee,
  BillingCycle,
  CreateTransactionRequest,
  TransactionType,
} from "../../types";

interface EditTransactionModalProps {
  transaction?: Transaction;
  accounts: Account[];
  categories: Category[];
  groups: CategoryGroup[];
  payees: Payee[];
  onClose: () => void;
  onSaved: () => void;
}

interface TransactionForm {
  date: string;
  description: string;
  amount: string;
  type: string;
  accountId: string;
  categoryId: string;
  payeeId: string;
  notes: string;
  tags: string[];
  billingCycleId: string;
}

export default function EditTransactionModal({
  transaction,
  accounts,
  categories,
  groups,
  payees,
  onClose,
  onSaved,
}: EditTransactionModalProps) {
  const isCreate = !transaction;
  const [form, setForm] = useState<TransactionForm>({
    date: new Date().toISOString().split("T")[0],
    description: "",
    amount: "",
    type: "debit",
    accountId: accounts?.[0]?.id || "",
    categoryId: "",
    payeeId: "",
    notes: "",
    tags: [],
    billingCycleId: "",
  });
  const selectedAccount = accounts.find((a) => a.id === form.accountId);
  const [tagInput, setTagInput] = useState("");
  const [billingCycles, setBillingCycles] = useState<BillingCycle[]>([]);
  const [loadingCycles, setLoadingCycles] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const prevAccountRef = useRef<string | null>(null);

  useEffect(() => {
    if (isCreate) {
      setForm({
        date: new Date().toISOString().split("T")[0],
        description: "",
        amount: "",
        type: "debit",
        accountId: accounts?.[0]?.id || "",
        categoryId: "",
        payeeId: "",
        notes: "",
        tags: [],
        billingCycleId: "",
      });
    } else {
      const dateStr = String(transaction.date).split("T")[0];
      setForm({
        date: dateStr,
        description: transaction.description || "",
        amount: String(transaction.amount || ""),
        type: transaction.type || "debit",
        accountId: transaction.accountId || "",
        categoryId: transaction.categoryId || "",
        payeeId: transaction.payeeId || "",
        notes: transaction.notes || "",
        tags: transaction.tags || [],
        billingCycleId: transaction.billingCycleId || "",
      });
    }
  }, [transaction, isCreate, accounts]);

  // Load billing cycles for the selected account (credit cards only). The
  // cycle is cleared when the account actually changes, but preserved on the
  // initial load so an existing attachment survives opening the modal.
  useEffect(() => {
    const acct = accounts.find((a) => a.id === form.accountId);
    const isCreditCard = acct?.accountTypeId === "credit_card";

    if (!isCreditCard) {
      setBillingCycles([]);
      setLoadingCycles(false);
      // Clear any cycle picked for a previous (credit-card) account.
      if (prevAccountRef.current && prevAccountRef.current !== form.accountId) {
        setForm((f) => ({ ...f, billingCycleId: "" }));
      }
      prevAccountRef.current = form.accountId;
      return;
    }

    let cancelled = false;
    setLoadingCycles(true);
    api
      .getBillingCycles(form.accountId)
      .then((res) => {
        if (!cancelled) setBillingCycles(res.data || []);
      })
      .catch(() => {
        if (!cancelled) setBillingCycles([]);
      })
      .finally(() => {
        if (!cancelled) setLoadingCycles(false);
      });

    if (prevAccountRef.current && prevAccountRef.current !== form.accountId) {
      setForm((f) => ({ ...f, billingCycleId: "" }));
    }
    prevAccountRef.current = form.accountId;

    return () => {
      cancelled = true;
    };
  }, [form.accountId, accounts]);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    setSaving(true);

    try {
      const payload: CreateTransactionRequest = {
        categoryId: form.categoryId || null,
        tags: form.tags,
        notes: form.notes,
        payeeId: form.payeeId || null,
        date: form.date,
        description: form.description,
        amount: parseFloat(form.amount),
        type: form.type as TransactionType,
        accountId: form.accountId,
      };

      // Only credit-card transactions carry a billing cycle; for other account
      // types omit the field so an edit never clears an existing attachment.
      if (selectedAccount?.accountTypeId === "credit_card") {
        payload.billingCycleId = form.billingCycleId || null;
      }

      if (isCreate) {
        await api.createTransaction(payload);
      } else {
        await api.updateTransaction(transaction.id, payload);
      }
      onSaved();
    } catch (err) {
      setError(
        (err as Error).message ||
          (isCreate
            ? "Failed to add transaction"
            : "Failed to update transaction"),
      );
    } finally {
      setSaving(false);
    }
  };

  const addTag = () => {
    const tag = tagInput.trim();
    if (tag && !form.tags.includes(tag)) {
      setForm((f) => ({ ...f, tags: [...f.tags, tag] }));
      setTagInput("");
    }
  };

  const removeTag = (tag: string) => {
    setForm((f) => ({ ...f, tags: f.tags.filter((t) => t !== tag) }));
  };

  const handleTagKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter") {
      e.preventDefault();
      addTag();
    }
  };

  const labelClass =
    "mb-1.5 text-xs font-semibold uppercase tracking-wider text-muted-foreground";

  return (
    <Sheet
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <SheetContent side="right" className="w-full sm:max-w-lg gap-0">
        <SheetHeader className="px-6 py-5 border-b border-border bg-card">
          <div className="flex items-center gap-3 pr-8">
            <div className="p-2 bg-primary/10 rounded-lg">
              <Pencil size={18} className="text-primary" />
            </div>
            <div>
              <SheetTitle className="text-lg font-bold">
                {isCreate ? "Add Transaction" : "Edit Transaction"}
              </SheetTitle>
              <SheetDescription className="text-xs mt-0.5">
                {isCreate
                  ? "Add a new transaction"
                  : "Modify transaction details"}
              </SheetDescription>
            </div>
          </div>
        </SheetHeader>

        {/* Form */}
        <form
          id="edit-transaction-form"
          onSubmit={handleSubmit}
          className="flex-1 overflow-y-auto"
        >
          <div className="px-6 py-5 space-y-6">
            {/* Error */}
            {error && (
              <div className="px-4 py-3 bg-destructive/10 border border-destructive/30 rounded-lg text-sm text-destructive">
                {error}
              </div>
            )}

            {/* Section: Core Details */}
            <div className="space-y-4">
              <div className="flex items-center gap-2 text-xs font-bold uppercase tracking-widest text-muted-foreground">
                <FileText size={12} />
                <span>Core Details</span>
              </div>

              {/* Description */}
              <div>
                <Label className={labelClass}>Description</Label>
                <Input
                  type="text"
                  value={form.description}
                  onChange={(e) =>
                    setForm((f) => ({ ...f, description: e.target.value }))
                  }
                  placeholder="Transaction description"
                  required
                />
              </div>

              {/* Date & Amount row */}
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <Label className={labelClass}>
                    <Calendar size={10} className="inline mr-1" />
                    Date
                  </Label>
                  <Input
                    type="date"
                    value={form.date}
                    onChange={(e) =>
                      setForm((f) => ({ ...f, date: e.target.value }))
                    }
                    required
                  />
                </div>
                <div>
                  <Label className={labelClass}>
                    <DollarSign size={10} className="inline mr-1" />
                    Amount
                  </Label>
                  <Input
                    type="number"
                    step="0.01"
                    min="0"
                    value={form.amount}
                    onChange={(e) =>
                      setForm((f) => ({ ...f, amount: e.target.value }))
                    }
                    placeholder="0.00"
                    required
                  />
                </div>
              </div>

              {/* Type & Account row */}
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <Label className={labelClass}>Type</Label>
                  <div className="flex rounded-lg border border-border overflow-hidden">
                    <Button
                      type="button"
                      variant="ghost"
                      className={`flex-1 h-auto px-3 py-2.5 rounded-none border-r ${form.type === "debit" ? "bg-destructive/10 text-destructive border-destructive/30 hover:bg-destructive/20" : "bg-background text-muted-foreground hover:text-foreground border-border"}`}
                      onClick={() => setForm((f) => ({ ...f, type: "debit" }))}
                    >
                      <ArrowDownLeft size={14} />
                      Debit
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      className={`flex-1 h-auto px-3 py-2.5 rounded-none ${form.type === "credit" ? "bg-emerald-500/20 text-emerald-400 hover:bg-emerald-500/30" : "bg-background text-muted-foreground hover:text-foreground"}`}
                      onClick={() => setForm((f) => ({ ...f, type: "credit" }))}
                    >
                      <ArrowUpRight size={14} />
                      Credit
                    </Button>
                  </div>
                </div>
                <div>
                  <Label className={labelClass}>
                    <Landmark size={10} className="inline mr-1" />
                    Account
                  </Label>
                  <Select
                    value={form.accountId || "none"}
                    onValueChange={(v) =>
                      setForm((f) => ({
                        ...f,
                        accountId: v === "none" ? "" : v,
                      }))
                    }
                  >
                    <SelectTrigger className="w-full">
                      <SelectValue placeholder="Select account" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="none">Select account</SelectItem>
                      {accounts.map((a) => (
                        <SelectItem key={a.id} value={a.id}>
                          {a.name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
              </div>

              {/* Billing Cycle (credit cards only) */}
              {selectedAccount?.accountTypeId === "credit_card" && (
                <div>
                  <Label className={labelClass}>
                    <Calendar size={10} className="inline mr-1" />
                    Billing Cycle
                  </Label>
                  <Select
                    value={form.billingCycleId || "none"}
                    onValueChange={(v) =>
                      setForm((f) => ({
                        ...f,
                        billingCycleId: v === "none" ? "" : v,
                      }))
                    }
                  >
                    <SelectTrigger className="w-full">
                      <SelectValue
                        placeholder={
                          isCreate ? "Auto (by date)" : "Unassigned"
                        }
                      />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="none">
                        {isCreate ? "Auto (by date)" : "Unassigned"}
                      </SelectItem>
                      {billingCycles.map((bc) => (
                        <SelectItem key={bc.id} value={bc.id}>
                          {bc.label} ({formatDate(bc.startDate)} –{" "}
                          {formatDate(bc.endDate)})
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  {loadingCycles && (
                    <p className="text-xs text-muted-foreground mt-1.5">
                      Loading billing cycles...
                    </p>
                  )}
                </div>
              )}
            </div>

            {/* Divider */}
            <div className="border-t border-border" />

            {/* Section: Classification */}
            <div className="space-y-4">
              <div className="flex items-center gap-2 text-xs font-bold uppercase tracking-widest text-muted-foreground">
                <Tag size={12} />
                <span>Classification</span>
              </div>

              {/* Category & Payee row */}
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <Label className={labelClass}>Category</Label>
                  <Select
                    value={form.categoryId || "none"}
                    onValueChange={(v) =>
                      setForm((f) => ({
                        ...f,
                        categoryId: v === "none" ? "" : v,
                      }))
                    }
                  >
                    <SelectTrigger className="w-full">
                      <SelectValue placeholder="Uncategorized" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="none">Uncategorized</SelectItem>
                      {buildCategorySections(groups, categories).map((s) => (
                        <SelectGroup key={s.group.id}>
                          <SelectLabel>{s.group.name}</SelectLabel>
                          {s.items.map((c) => (
                            <SelectItem key={c.id} value={c.id}>
                              {c.name}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div>
                  <Label className={labelClass}>
                    <User size={10} className="inline mr-1" />
                    Payee
                  </Label>
                  <Select
                    value={form.payeeId || "none"}
                    onValueChange={(v) =>
                      setForm((f) => ({
                        ...f,
                        payeeId: v === "none" ? "" : v,
                      }))
                    }
                  >
                    <SelectTrigger className="w-full">
                      <SelectValue placeholder="No Payee" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="none">No Payee</SelectItem>
                      {payees.map((p) => (
                        <SelectItem key={p.id} value={p.id}>
                          {p.name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
              </div>

              {/* Notes */}
              <div>
                <Label className={labelClass}>Notes</Label>
                <textarea
                  className="w-full min-h-24 resize-none rounded-lg border border-border bg-background px-2.5 py-2 text-sm text-foreground placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
                  rows={3}
                  value={form.notes}
                  onChange={(e) =>
                    setForm((f) => ({ ...f, notes: e.target.value }))
                  }
                  placeholder="Add notes..."
                />
              </div>

              {/* Tags */}
              <div>
                <Label className={labelClass}>Tags</Label>
                <div className="flex flex-wrap gap-2 mb-2">
                  {form.tags.map((tag) => (
                    <Badge
                      key={tag}
                      className="bg-primary/10 text-primary border-primary/20 rounded-full"
                    >
                      {tag}
                      <button
                        type="button"
                        className="hover:text-destructive transition-colors ml-0.5"
                        onClick={() => removeTag(tag)}
                      >
                        <X size={12} />
                      </button>
                    </Badge>
                  ))}
                </div>
                <div className="flex gap-2">
                  <Input
                    type="text"
                    className="flex-1"
                    value={tagInput}
                    onChange={(e) => setTagInput(e.target.value)}
                    onKeyDown={handleTagKeyDown}
                    placeholder="Add a tag and press Enter"
                  />
                  <Button type="button" variant="outline" onClick={addTag}>
                    Add
                  </Button>
                </div>
              </div>
            </div>
          </div>
        </form>

        {/* Footer */}
        <SheetFooter className="px-6 py-4 border-t border-border bg-card flex-row items-center justify-between gap-3">
          <div className="text-xs text-muted-foreground truncate">
            {isCreate
              ? "New transaction"
              : `ID: ${transaction.id?.slice(0, 8)}...`}
          </div>
          <div className="flex gap-3">
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button
              type="submit"
              form="edit-transaction-form"
              disabled={saving}
            >
              <Save size={14} />
              {saving
                ? "Saving..."
                : isCreate
                  ? "Add Transaction"
                  : "Save Changes"}
            </Button>
          </div>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  );
}