import { useEffect, useRef, useState, type FormEvent } from "react";
import { Plus, Trash2 } from "lucide-react";
import api from "../../api/client";
import type {
  Account,
  Category,
  Payee,
  RecurringFrequency,
  RecurringSeries,
  RecurringSeriesRange,
  TransactionType,
} from "../../types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Spinner } from "@/components/ui/spinner";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import AccountSelect from "@/components/AccountSelect/AccountSelect";
import { toast } from "sonner";

const NONE = "none";

const FREQUENCIES: { value: RecurringFrequency; label: string }[] = [
  { value: "daily", label: "Daily" },
  { value: "weekly", label: "Weekly" },
  { value: "monthly", label: "Monthly" },
  { value: "yearly", label: "Yearly" },
];

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  series: RecurringSeries | null;
  accounts: Account[];
  categories: Category[];
  payees: Payee[];
  onSaved: () => void;
}

function todayIso(): string {
  return new Date().toISOString().slice(0, 10);
}

interface FormState {
  name: string;
  type: TransactionType;
  frequency: RecurringFrequency;
  interval: string;
  categoryId: string;
  payeeId: string;
  notes: string;
  active: boolean;
}

// RangeRow is one account/amount entry with its own optional end (exclusive).
// Entries may leave gaps, so a subscription can be discontinued and resumed.
interface RangeRow {
  startDate: string;
  endDate: string;
  amount: string;
  accountId: string;
}

const EMPTY: FormState = {
  name: "",
  type: "debit",
  frequency: "monthly",
  interval: "1",
  categoryId: NONE,
  payeeId: NONE,
  notes: "",
  active: true,
};

function emptyRange(accountId: string, startDate = todayIso()): RangeRow {
  return { startDate, endDate: "", amount: "", accountId };
}

// RecurringFormDialog defines a subscription as a list of account/amount
// entries, each with its own date range. Entries must not overlap; gaps are
// allowed (a discontinued subscription can resume later). The subscription's
// overall period is derived from the entries.
export default function RecurringFormDialog({
  open,
  onOpenChange,
  series,
  accounts,
  categories,
  payees,
  onSaved,
}: Props) {
  const [form, setForm] = useState<FormState>(EMPTY);
  const [ranges, setRanges] = useState<RangeRow[]>([]);
  const [loadingRanges, setLoadingRanges] = useState(false);
  const [saving, setSaving] = useState(false);

  // The create branch needs one account id for its first range row, but the
  // accounts array is replaced on every reference-data refresh: depending on it
  // re-ran this effect and discarded whatever the user had already typed, so the
  // default is read from a ref instead.
  const accountsRef = useRef(accounts);
  useEffect(() => {
    accountsRef.current = accounts;
  }, [accounts]);

  useEffect(() => {
    if (!open) return;
    if (series) {
      setForm({
        name: series.name,
        type: series.type,
        frequency: series.frequency,
        interval: String(series.interval),
        categoryId: series.categoryId || NONE,
        payeeId: series.payeeId || NONE,
        notes: series.notes || "",
        active: series.active,
      });
      setLoadingRanges(true);
      setRanges([]);
      // The terms belong to the series that was open when the fetch started: a
      // response for a series the user has moved past used to fill this form
      // with another series' ranges, and saving then rewrote its history (the
      // update replaces the whole range list).
      let cancelled = false;
      api
        .getRecurringTerms(series.id)
        .then((res) => {
          if (cancelled) return;
          setRanges(
            (res.data || []).map((t) => ({
              startDate: t.startDate.slice(0, 10),
              endDate: t.endDate ? t.endDate.slice(0, 10) : "",
              amount: String(t.amount),
              accountId: t.accountId,
            })),
          );
        })
        .catch((err) => {
          if (!cancelled) toast.error((err as Error).message);
        })
        .finally(() => {
          if (!cancelled) setLoadingRanges(false);
        });
      return () => {
        cancelled = true;
      };
    }
    setForm({ ...EMPTY });
    setRanges([emptyRange(accountsRef.current[0]?.id || "")]);
  }, [open, series]);

  const updateRange = (index: number, patch: Partial<RangeRow>) => {
    setRanges((rows) =>
      rows.map((r, i) => (i === index ? { ...r, ...patch } : r)),
    );
  };

  const addRange = () => {
    setRanges((rows) => [...rows, emptyRange(accounts[0]?.id || "")]);
  };

  const removeRange = (index: number) => {
    setRanges((rows) => rows.filter((_, i) => i !== index));
  };

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!form.name.trim()) {
      toast.error("Name is required");
      return;
    }
    if (ranges.length === 0) {
      toast.error("Add at least one account/amount entry");
      return;
    }
    const payloadRanges: RecurringSeriesRange[] = [];
    for (const r of ranges) {
      const value = Number(r.amount);
      if (!r.accountId || !r.startDate) {
        toast.error("Every entry needs an account and a start date");
        return;
      }
      if (!value || value <= 0) {
        toast.error("Every entry needs a positive amount");
        return;
      }
      payloadRanges.push({
        startDate: r.startDate,
        endDate: r.endDate,
        amount: value,
        accountId: r.accountId,
      });
    }
    const payload = {
      name: form.name.trim(),
      type: form.type,
      frequency: form.frequency,
      interval: Number(form.interval) || 1,
      categoryId: form.categoryId === NONE ? null : form.categoryId,
      payeeId: form.payeeId === NONE ? null : form.payeeId,
      notes: form.notes,
      active: form.active,
      ranges: payloadRanges,
    };
    setSaving(true);
    try {
      if (series) {
        await api.updateRecurringSeries(series.id, payload);
        toast.success("Recurring series updated");
      } else {
        await api.createRecurringSeries(payload);
        toast.success("Recurring series created");
      }
      onOpenChange(false);
      onSaved();
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>
            {series ? "Edit Recurring Series" : "Add Recurring Series"}
          </DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="recurring-name">Name</Label>
            <Input
              id="recurring-name"
              required
              autoFocus
              placeholder="e.g. Netflix, Rent, Salary"
              className="h-11"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          </div>

          <div className="grid grid-cols-3 gap-4">
            <div className="space-y-2">
              <Label htmlFor="recurring-type">Type</Label>
              <Select
                value={form.type}
                onValueChange={(v) =>
                  setForm({ ...form, type: v as TransactionType })
                }
              >
                <SelectTrigger id="recurring-type" className="w-full h-11">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="debit">Expense (debit)</SelectItem>
                  <SelectItem value="credit">Income (credit)</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="recurring-frequency">Frequency</Label>
              <Select
                value={form.frequency}
                onValueChange={(v) =>
                  setForm({ ...form, frequency: v as RecurringFrequency })
                }
              >
                <SelectTrigger id="recurring-frequency" className="w-full h-11">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {FREQUENCIES.map((f) => (
                    <SelectItem key={f.value} value={f.value}>
                      {f.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="recurring-interval">Every</Label>
              <Input
                id="recurring-interval"
                type="number"
                min="1"
                max="365"
                className="h-11"
                value={form.interval}
                onChange={(e) => setForm({ ...form, interval: e.target.value })}
              />
            </div>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label htmlFor="recurring-category">Category (optional)</Label>
              <Select
                value={form.categoryId}
                onValueChange={(v) => setForm({ ...form, categoryId: v })}
              >
                <SelectTrigger id="recurring-category" className="w-full h-11">
                  <SelectValue placeholder="No category" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={NONE}>No category</SelectItem>
                  {categories.map((c) => (
                    <SelectItem key={c.id} value={c.id}>
                      {c.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="recurring-payee">Payee (optional)</Label>
              <Select
                value={form.payeeId}
                onValueChange={(v) => setForm({ ...form, payeeId: v })}
              >
                <SelectTrigger id="recurring-payee" className="w-full h-11">
                  <SelectValue placeholder="No payee" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={NONE}>No payee</SelectItem>
                  {payees.map((p) => (
                    <SelectItem key={p.id} value={p.id}>
                      {p.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>

          <div className="space-y-3">
            <div className="flex items-center justify-between">
              <div>
                <Label>Account &amp; amount history</Label>
                <p className="text-xs text-muted-foreground mt-0.5">
                  Each entry covers its date range (the end is exclusive; leave
                  it blank to keep it open). Entries must not overlap; leave a
                  gap to discontinue the subscription and resume it later.
                </p>
              </div>
              <Button type="button" variant="outline" size="sm" onClick={addRange}>
                <Plus />
                Add entry
              </Button>
            </div>

            {loadingRanges ? (
              <div className="flex justify-center py-6">
                <Spinner className="size-6 text-primary" />
              </div>
            ) : (
              <div className="space-y-2">
                {ranges.map((r, i) => (
                  <div
                    key={i}
                    className="grid grid-cols-[1.1fr_1.1fr_1fr_1.2fr_auto] items-end gap-2 rounded-lg border border-border p-2"
                  >
                    <div className="space-y-1">
                      <Label className="text-xs">From</Label>
                      <Input
                        type="date"
                        aria-label={`Entry ${i + 1} start`}
                        className="h-9"
                        value={r.startDate}
                        onChange={(e) =>
                          updateRange(i, { startDate: e.target.value })
                        }
                      />
                    </div>
                    <div className="space-y-1">
                      <Label className="text-xs">To (excl.)</Label>
                      <Input
                        type="date"
                        aria-label={`Entry ${i + 1} end`}
                        className="h-9"
                        value={r.endDate}
                        onChange={(e) =>
                          updateRange(i, { endDate: e.target.value })
                        }
                      />
                    </div>
                    <div className="space-y-1">
                      <Label className="text-xs">Amount</Label>
                      <Input
                        type="number"
                        min="0"
                        step="0.01"
                        aria-label={`Entry ${i + 1} amount`}
                        placeholder="0.00"
                        className="h-9"
                        value={r.amount}
                        onChange={(e) =>
                          updateRange(i, { amount: e.target.value })
                        }
                      />
                    </div>
                    <div className="space-y-1">
                      <Label className="text-xs">Account</Label>
                      <AccountSelect
                        accounts={accounts}
                        value={r.accountId}
                        onValueChange={(v) => updateRange(i, { accountId: v })}
                        placeholder="Account"
                        ariaLabel={`Entry ${i + 1} account`}
                        triggerClassName="w-full h-9"
                      />
                    </div>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      title="Remove entry"
                      aria-label={`Remove entry ${i + 1}`}
                      disabled={ranges.length <= 1}
                      className="text-muted-foreground hover:text-destructive hover:bg-destructive/10"
                      onClick={() => removeRange(i)}
                    >
                      <Trash2 size={14} />
                    </Button>
                  </div>
                ))}
              </div>
            )}
          </div>

          <div className="space-y-2">
            <Label htmlFor="recurring-notes">Notes</Label>
            <Input
              id="recurring-notes"
              placeholder="Optional notes"
              className="h-11"
              value={form.notes}
              onChange={(e) => setForm({ ...form, notes: e.target.value })}
            />
          </div>

          <div className="flex items-center gap-2">
            <Switch
              id="recurring-active"
              checked={form.active}
              onCheckedChange={(v) => setForm({ ...form, active: v })}
            />
            <Label htmlFor="recurring-active">Active</Label>
          </div>

          <div className="flex justify-end pt-2 gap-3">
            <Button
              type="button"
              variant="ghost"
              onClick={() => onOpenChange(false)}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={saving || loadingRanges}>
              {series ? "Save Changes" : "Create Series"}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
