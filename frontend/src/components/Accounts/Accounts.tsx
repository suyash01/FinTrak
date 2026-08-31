import { useMemo, useState, useEffect } from "react";
import {
  Plus,
  Trash2,
  CreditCard,
  Building2,
  Landmark,
  X,
  Pencil,
  Download,
  Star,
  Lock,
  LockOpen,
  ArrowUp,
  ArrowDown,
} from "lucide-react";
import api, { downloadCSV } from "../../api/client";
import { formatDate, formatCurrency } from "../../utils/formatters";
import { useSettings } from "../../context/SettingsContext";
import type { Account, AccountType, UpdateAccountRequest } from "../../types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { toast } from "sonner";

interface AccountForm {
  name: string;
  accountTypeId: string;
  bank: string;
  color: string;
  currency: string;
  billingDay: number | null;
  closed: boolean;
}

const EMPTY_NEW_ACCOUNT: AccountForm = {
  name: "",
  accountTypeId: "bank",
  bank: "",
  color: "#06b6d4",
  currency: "INR",
  billingDay: null,
  closed: false,
};

const ordinal = (n: number): string => {
  const suffixes = ["th", "st", "nd", "rd"];
  const v = n % 100;
  return suffixes[(v - 20) % 10] || suffixes[v] || suffixes[0];
};

// parseBillingDay maps a number-input value to a billing day. An empty value
// clears the field (null = no billing day / no summary rows).
const parseBillingDay = (v: string): number | null => {
  if (v === "") return null;
  const n = Number(v);
  if (Number.isNaN(n)) return null;
  return Math.max(1, Math.min(31, n));
};

// balanceLabel picks the display label for an account's balance value by type.
const balanceLabel = (acc: Account): string => {
  if (acc.accountTypeId === "loan") return "Repaid";
  if (acc.accountTypeId === "credit_card") return "Outstanding";
  return "Balance";
};

const getTypeIcon = (accountTypeId: string, color: string, size: number) => {
  if (accountTypeId === "credit_card")
    return <CreditCard size={size} style={{ color }} />;
  if (accountTypeId === "loan")
    return <Landmark size={size} style={{ color }} />;
  return <Building2 size={size} style={{ color }} />;
};

export default function Accounts() {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [accountTypes, setAccountTypes] = useState<AccountType[]>([]);
  const [createOpen, setCreateOpen] = useState(false);
  const [newAcc, setNewAcc] = useState<AccountForm>(EMPTY_NEW_ACCOUNT);
  const [editing, setEditing] = useState<Account | null>(null);
  const [editAcc, setEditAcc] = useState<AccountForm | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Account | null>(null);
  const [typeFilter, setTypeFilter] = useState("all");
  const [statusFilter, setStatusFilter] = useState("all");
  const [sortDir, setSortDir] = useState<"asc" | "desc">("asc");
  const { compactLayout } = useSettings();

  useEffect(() => {
    api.getAccounts().then(setAccounts).catch(console.error);
    api.getAccountTypes().then(setAccountTypes).catch(console.error);
  }, []);

  const resetNew = () => setNewAcc(EMPTY_NEW_ACCOUNT);

  const handleCreate = async () => {
    try {
      const acc = await api.createAccount(newAcc);
      setAccounts((prev) => [...prev, acc]);
      setCreateOpen(false);
      resetNew();
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const handleDelete = async (acc: Account) => {
    try {
      const res = await api.deleteAccount(acc.id);
      setAccounts((prev) => prev.filter((a) => a.id !== acc.id));
      const deleted = res?.transactionsDeleted ?? 0;
      if (deleted > 0) {
        toast.success(
          `Account deleted — ${deleted} transaction(s) were removed.`,
        );
      }
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const startEdit = (acc: Account) => {
    setEditing(acc);
    setEditAcc({
      name: acc.name,
      accountTypeId: acc.accountTypeId,
      bank: acc.bank || "",
      color: acc.color,
      currency: acc.currency,
      billingDay: acc.billingDay ?? null,
      closed: acc.closed,
    });
  };

  const handleSave = async () => {
    if (!editing || !editAcc) return;
    try {
      const updated = await api.updateAccount(
        editing.id,
        toUpdatePayload(editAcc),
      );
      setAccounts((prev) => prev.map((a) => (a.id === editing.id ? updated : a)));
      setEditing(null);
      setEditAcc(null);
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const handleExport = async (id: string) => {
    try {
      await downloadCSV(`/accounts/${id}/export`);
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  // Mark/unmark an account as the user's default. The backend enforces a single
  // default per user, so when one is set the others are cleared.
  const handleSetDefault = async (acc: Account) => {
    try {
      const updated = await api.updateAccount(acc.id, {
        ...toUpdatePayload(acc),
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
      toast.error((err as Error).message);
    }
  };

  // Close/reopen an account. Closing makes its transactions immutable (only
  // linking stays possible); reopening restores normal editing.
  const handleToggleClosed = async (acc: Account) => {
    try {
      const updated = await api.updateAccount(acc.id, {
        ...toUpdatePayload(acc),
        closed: !acc.closed,
      });
      setAccounts((prev) =>
        prev.map((a) => (a.id === updated.id ? updated : a)),
      );
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  // Filtered + sorted view (client-side; the account list is small).
  const visibleAccounts = useMemo(() => {
    let list = accounts;
    if (typeFilter !== "all") {
      list = list.filter((a) => a.accountTypeId === typeFilter);
    }
    if (statusFilter === "open") {
      list = list.filter((a) => !a.closed);
    } else if (statusFilter === "closed") {
      list = list.filter((a) => a.closed);
    }
    return [...list].sort((x, y) => {
      // Closed accounts always sort last, regardless of name direction.
      if (x.closed !== y.closed) return x.closed ? 1 : -1;
      return sortDir === "asc"
        ? x.name.localeCompare(y.name)
        : y.name.localeCompare(x.name);
    });
  }, [accounts, typeFilter, statusFilter, sortDir]);

  const toggleSort = () => setSortDir((d) => (d === "asc" ? "desc" : "asc"));

  const cellPad = compactLayout ? "py-1.5 px-3" : "py-2.5 px-4";
  const headerBase = `${cellPad} text-xs font-semibold uppercase tracking-wider text-muted-foreground whitespace-nowrap`;

  return (
    <>
      <div className="shrink-0 px-8 pt-6">
        <h1 className="text-2xl font-bold text-foreground mb-1">Accounts</h1>
        <p className="text-muted-foreground text-sm">
          Manage your bank accounts, credit cards, and loans
        </p>
      </div>
      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto w-full">
        <div className="flex justify-between items-center mb-5">
          <div className="flex flex-wrap items-center gap-3">
            {/* Account-type filter */}
            <Select value={typeFilter} onValueChange={setTypeFilter}>
              <SelectTrigger
                className={`${compactLayout ? "h-8" : "h-10"} bg-background w-44`}
              >
                <SelectValue placeholder="All Types" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All Types</SelectItem>
                {accountTypes.map((at) => (
                  <SelectItem key={at.id} value={at.id}>
                    {at.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {/* Closed-status filter */}
            <Select value={statusFilter} onValueChange={setStatusFilter}>
              <SelectTrigger
                className={`${compactLayout ? "h-8" : "h-10"} bg-background w-36`}
              >
                <SelectValue placeholder="All Statuses" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All Statuses</SelectItem>
                <SelectItem value="open">Open</SelectItem>
                <SelectItem value="closed">Closed</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <Button size="lg" className="px-4" onClick={() => setCreateOpen(true)}>
            <Plus /> Add Account
          </Button>
        </div>

        {visibleAccounts.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-20 px-4 text-center">
            <Building2 className="w-16 h-16 text-muted-foreground opacity-50 mb-4" />
            <h3 className="text-lg font-semibold text-foreground mb-2">
              {accounts.length === 0 ? "No Accounts Yet" : "No matching accounts"}
            </h3>
            <p className="text-muted-foreground text-sm mb-6 max-w-md">
              {accounts.length === 0
                ? "Add a bank account, credit card, or loan to start importing statements and categorizing your transactions."
                : "Try changing the account type or status filter."}
            </p>
            {accounts.length === 0 && (
              <Button
                size="lg"
                className="px-4"
                onClick={() => setCreateOpen(true)}
              >
                Add Account
              </Button>
            )}
          </div>
        ) : (
          <div className="bg-card border border-border rounded-xl overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead
                    className={`${headerBase} cursor-pointer select-none`}
                    onClick={toggleSort}
                  >
                    <span className="flex items-center gap-1">
                      Name
                      {sortDir === "asc" ? (
                        <ArrowUp size={12} />
                      ) : (
                        <ArrowDown size={12} />
                      )}
                    </span>
                  </TableHead>
                  <TableHead className={headerBase}>Type</TableHead>
                  <TableHead className={headerBase}>Bank</TableHead>
                  <TableHead
                    className={`${headerBase} text-right`}
                  >
                    Balance
                  </TableHead>
                  <TableHead className={headerBase}>Billing Day</TableHead>
                  <TableHead className={headerBase}>Status</TableHead>
                  <TableHead className={`${headerBase} text-right`}>
                    Actions
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {visibleAccounts.map((acc) => (
                  <TableRow key={acc.id} className="border-border">
                    <TableCell className={cellPad}>
                      <div className="flex items-center gap-2.5">
                        {getTypeIcon(
                          acc.accountTypeId,
                          acc.color,
                          compactLayout ? 16 : 18,
                        )}
                        <span
                          className={`font-medium ${
                            acc.closed
                              ? "text-muted-foreground"
                              : "text-foreground"
                          }`}
                        >
                          {acc.name}
                        </span>
                        {acc.isDefault && (
                          <span className="shrink-0 text-[9px] font-bold bg-amber-500/20 text-amber-400 px-1.5 py-0.5 rounded uppercase tracking-wider">
                            Default
                          </span>
                        )}
                        {acc.closed && (
                          <span className="shrink-0 text-[9px] font-bold bg-destructive/10 text-destructive px-1.5 py-0.5 rounded uppercase tracking-wider">
                            Closed
                          </span>
                        )}
                      </div>
                    </TableCell>
                    <TableCell className={`${cellPad} text-sm text-muted-foreground whitespace-nowrap`}>
                      {acc.accountTypeName}
                    </TableCell>
                    <TableCell className={`${cellPad} text-sm text-muted-foreground whitespace-nowrap`}>
                      {acc.bank || "—"}
                    </TableCell>
                    <TableCell className={`${cellPad} text-right whitespace-nowrap`}>
                      <div className="text-sm font-semibold text-foreground font-mono">
                        {formatCurrency(acc.balance, acc.currency)}
                      </div>
                      <div className="text-[10px] uppercase tracking-wider text-muted-foreground font-medium">
                        {balanceLabel(acc)}
                      </div>
                    </TableCell>
                    <TableCell className={`${cellPad} text-sm text-muted-foreground whitespace-nowrap`}>
                      {acc.billingDay
                        ? `${acc.billingDay}${ordinal(acc.billingDay)}`
                        : "—"}
                    </TableCell>
                    <TableCell className={`${cellPad} text-sm whitespace-nowrap`}>
                      {acc.closed ? (
                        <span className="text-destructive">Closed</span>
                      ) : (
                        <span className="text-emerald-500">Open</span>
                      )}
                    </TableCell>
                    <TableCell className={`${cellPad} text-right`}>
                      <div className="flex items-center justify-end gap-0.5">
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          className={
                            acc.isDefault
                              ? "text-amber-400 hover:text-amber-300 hover:bg-amber-500/10"
                              : "text-muted-foreground hover:text-amber-400 hover:bg-accent"
                          }
                          onClick={() => handleSetDefault(acc)}
                          title={
                            acc.isDefault
                              ? "Remove as default account"
                              : "Set as default account"
                          }
                        >
                          <Star size={15} fill={acc.isDefault ? "currentColor" : "none"} />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          className="text-muted-foreground hover:text-primary hover:bg-primary/10"
                          onClick={() => handleExport(acc.id)}
                          title="Export transactions (CSV)"
                        >
                          <Download size={14} />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          className="text-muted-foreground hover:text-primary hover:bg-primary/10"
                          onClick={() => startEdit(acc)}
                          title="Edit account"
                        >
                          <Pencil size={14} />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          className="text-muted-foreground hover:text-amber-400 hover:bg-accent"
                          onClick={() => handleToggleClosed(acc)}
                          title={
                            acc.closed
                              ? "Reopen account"
                              : "Close account (transactions become read-only)"
                          }
                        >
                          {acc.closed ? <LockOpen size={14} /> : <Lock size={14} />}
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          className="text-muted-foreground hover:text-destructive hover:bg-destructive/10"
                          onClick={() => setDeleteTarget(acc)}
                          title="Delete account"
                        >
                          <Trash2 size={14} />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </div>

      {/* Create account dialog */}
      <Dialog
        open={createOpen}
        onOpenChange={(o) => {
          if (!o) {
            setCreateOpen(false);
            resetNew();
          }
        }}
      >
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>New Account</DialogTitle>
          </DialogHeader>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div className="flex flex-col gap-1.5">
              <Label className="text-xs text-muted-foreground">Name</Label>
              <Input
                className="h-10"
                placeholder="e.g. HDFC Savings"
                value={newAcc.name}
                onChange={(e) =>
                  setNewAcc({ ...newAcc, name: e.target.value })
                }
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label className="text-xs text-muted-foreground">Type</Label>
              <Select
                value={newAcc.accountTypeId}
                onValueChange={(v) =>
                  setNewAcc({ ...newAcc, accountTypeId: v })
                }
              >
                <SelectTrigger className="w-full h-10">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {accountTypes.map((at) => (
                    <SelectItem key={at.id} value={at.id}>
                      {at.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label className="text-xs text-muted-foreground">Bank</Label>
              <Input
                className="h-10"
                placeholder="e.g. HDFC"
                value={newAcc.bank}
                onChange={(e) =>
                  setNewAcc({ ...newAcc, bank: e.target.value })
                }
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label className="text-xs text-muted-foreground">Color</Label>
              <input
                type="color"
                value={newAcc.color}
                onChange={(e) =>
                  setNewAcc({ ...newAcc, color: e.target.value })
                }
                className="w-full h-10 cursor-pointer bg-background border border-border rounded-lg p-1"
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label className="text-xs text-muted-foreground">
                Billing Day
              </Label>
              <Input
                type="number"
                min={1}
                max={31}
                placeholder="None"
                className="h-10"
                value={newAcc.billingDay ?? ""}
                onChange={(e) =>
                  setNewAcc({
                    ...newAcc,
                    billingDay: parseBillingDay(e.target.value),
                  })
                }
              />
              <span className="text-[11px] text-muted-foreground">
                Optional. Set to show monthly summary rows for this account.
              </span>
            </div>
          </div>
          <DialogFooter>
            <Button
              size="lg"
              className="px-4"
              onClick={handleCreate}
              disabled={!newAcc.name}
            >
              Create
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Edit account dialog */}
      {editing && editAcc && (
        <Dialog open onOpenChange={(o) => !o && setEditing(null)}>
          <DialogContent className="sm:max-w-lg">
            <DialogHeader>
              <DialogTitle>Edit Account</DialogTitle>
            </DialogHeader>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div className="flex flex-col gap-1.5">
                <Label className="text-xs text-muted-foreground">Name</Label>
                <Input
                  className="h-10"
                  placeholder="Name"
                  value={editAcc.name}
                  onChange={(e) =>
                    setEditAcc({ ...editAcc, name: e.target.value })
                  }
                />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label className="text-xs text-muted-foreground">Type</Label>
                <Select
                  value={editAcc.accountTypeId}
                  onValueChange={(v) =>
                    setEditAcc({ ...editAcc, accountTypeId: v })
                  }
                >
                  <SelectTrigger className="w-full h-10">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {accountTypes.map((at) => (
                      <SelectItem key={at.id} value={at.id}>
                        {at.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="flex flex-col gap-1.5">
                <Label className="text-xs text-muted-foreground">Bank</Label>
                <Input
                  className="h-10"
                  placeholder="Bank"
                  value={editAcc.bank}
                  onChange={(e) =>
                    setEditAcc({ ...editAcc, bank: e.target.value })
                  }
                />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label className="text-xs text-muted-foreground">Color</Label>
                <input
                  type="color"
                  value={editAcc.color}
                  onChange={(e) =>
                    setEditAcc({ ...editAcc, color: e.target.value })
                  }
                  className="w-full h-10 cursor-pointer bg-background border border-border rounded-lg p-1"
                />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label className="text-xs text-muted-foreground">
                  Billing Day
                </Label>
                <Input
                  type="number"
                  min={1}
                  max={31}
                  placeholder="None"
                  className="h-10"
                  value={editAcc.billingDay ?? ""}
                  onChange={(e) =>
                    setEditAcc({
                      ...editAcc,
                      billingDay: parseBillingDay(e.target.value),
                    })
                  }
                />
                <span className="text-[11px] text-muted-foreground">
                  Optional. Set to show monthly summary rows for this account.
                  Leave empty to disable.
                </span>
              </div>
              <div className="flex items-end pb-1">
                <label className="flex items-center gap-2 text-sm cursor-pointer">
                  <input
                    type="checkbox"
                    checked={editAcc.closed}
                    onChange={(e) =>
                      setEditAcc({ ...editAcc, closed: e.target.checked })
                    }
                    className="h-4 w-4 rounded border-border accent-primary"
                  />
                  Closed account
                  <span className="text-[11px] text-muted-foreground">
                    (transactions become read-only; linking stays possible)
                  </span>
                </label>
              </div>
            </div>
            <DialogFooter>
              <Button
                size="lg"
                className="px-4"
                onClick={handleSave}
                disabled={!editAcc.name}
              >
                Save
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}

      {/* Delete confirmation */}
      <AlertDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              Delete {deleteTarget?.name}?
            </AlertDialogTitle>
            <AlertDialogDescription>
              This will permanently delete the account and all of its
              transactions (and remove any loan attachments on them). This
              action cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                const target = deleteTarget;
                setDeleteTarget(null);
                if (target) handleDelete(target);
              }}
            >
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}

// toUpdatePayload flattens an AccountForm (or Account) into the partial-update
// request shape used by PUT /accounts/:id.
function toUpdatePayload(
  form: {
    name: string;
    accountTypeId: string;
    bank: string;
    color: string;
    currency: string;
    billingDay?: number | null;
    closed: boolean;
  },
): UpdateAccountRequest {
  return {
    name: form.name,
    accountTypeId: form.accountTypeId,
    bank: form.bank || "",
    currency: form.currency || "INR",
    color: form.color,
    billingDay: form.billingDay ?? null,
    closed: form.closed,
  };
}