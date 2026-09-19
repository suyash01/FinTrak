import { useMemo, useState } from "react";
import { Plus, Building2, ArrowUp, ArrowDown } from "lucide-react";
import api, { downloadCSV } from "../../api/client";
import { useSettings } from "../../context/SettingsContext";
import { useDomainData } from "../../context/DomainDataContext";
import type { Account } from "../../types";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
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
import { useCommandIntent } from "../../lib/useCommandIntent";
import AccountFormDialog from "./AccountFormDialog";
import AccountRow from "./AccountRow";
import LoanScheduleDialog from "./LoanScheduleDialog";
import {
  EMPTY_NEW_ACCOUNT,
  toUpdatePayload,
  type AccountForm,
} from "./accountHelpers";

export default function Accounts() {
  const { accounts, accountTypes, setAccounts } = useDomainData();
  const [createOpen, setCreateOpen] = useState(false);
  const [newAcc, setNewAcc] = useState<AccountForm>(EMPTY_NEW_ACCOUNT);
  const [editing, setEditing] = useState<Account | null>(null);
  const [editAcc, setEditAcc] = useState<AccountForm | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Account | null>(null);
  // The loan whose amortization schedule dialog is open, if any.
  const [scheduleTarget, setScheduleTarget] = useState<Account | null>(null);
  const [typeFilter, setTypeFilter] = useState("all");
  const [statusFilter, setStatusFilter] = useState("all");
  const [sortDir, setSortDir] = useState<"asc" | "desc">("asc");
  const { compactLayout } = useSettings();

  useCommandIntent("new-account", () => setCreateOpen(true));

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
                aria-label="Filter by account type"
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
                aria-label="Filter by status"
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
                    aria-sort={sortDir === "asc" ? "ascending" : "descending"}
                    className={headerBase}
                  >
                    <button
                      type="button"
                      onClick={toggleSort}
                      className="inline-flex items-center gap-1 cursor-pointer select-none hover:text-foreground"
                    >
                      Name
                      {sortDir === "asc" ? (
                        <ArrowUp size={12} />
                      ) : (
                        <ArrowDown size={12} />
                      )}
                    </button>
                  </TableHead>
                  <TableHead className={headerBase}>Type</TableHead>
                  <TableHead className={headerBase}>Bank</TableHead>
                  <TableHead className={`${headerBase} text-right`}>
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
                  <AccountRow
                    key={acc.id}
                    account={acc}
                    cellPad={cellPad}
                    compactLayout={compactLayout}
                    onSetDefault={handleSetDefault}
                    onExport={handleExport}
                    onEdit={startEdit}
                    onSchedule={setScheduleTarget}
                    onToggleClosed={handleToggleClosed}
                    onDelete={setDeleteTarget}
                  />
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </div>

      <AccountFormDialog
        open={createOpen}
        onOpenChange={(o) => {
          setCreateOpen(o);
          if (!o) resetNew();
        }}
        title="New Account"
        form={newAcc}
        onChange={setNewAcc}
        onSubmit={handleCreate}
        accountTypes={accountTypes}
        submitLabel="Create"
        billingDayHint="Optional. Set to show monthly summary rows for this account."
      />

      {editing && editAcc && (
        <AccountFormDialog
          open
          onOpenChange={(o) => {
            if (!o) {
              setEditing(null);
              setEditAcc(null);
            }
          }}
          title="Edit Account"
          form={editAcc}
          onChange={setEditAcc}
          onSubmit={handleSave}
          accountTypes={accountTypes}
          submitLabel="Save"
          billingDayHint="Optional. Set to show monthly summary rows for this account. Leave empty to disable."
          showClosed
        />
      )}

      {scheduleTarget && (
        <LoanScheduleDialog
          account={scheduleTarget}
          onClose={() => setScheduleTarget(null)}
        />
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
