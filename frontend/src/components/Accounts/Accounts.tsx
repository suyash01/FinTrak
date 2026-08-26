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
import type { Account, AccountType } from "../../types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent } from "@/components/ui/card";
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
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { toast } from "sonner";

interface AccountForm {
  name: string;
  accountTypeId: string;
  bank: string;
  color: string;
  currency: string;
}

const EMPTY_NEW_ACCOUNT = {
  name: "",
  accountTypeId: "bank",
  bank: "",
  color: "#06b6d4",
};

export default function Accounts() {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [accountTypes, setAccountTypes] = useState<AccountType[]>([]);
  const [showNew, setShowNew] = useState(false);
  const [newAcc, setNewAcc] = useState(EMPTY_NEW_ACCOUNT);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editAcc, setEditAcc] = useState<AccountForm | null>(null);
  const { compactLayout } = useSettings();

  useEffect(() => {
    api.getAccounts().then(setAccounts).catch(console.error);
    api.getAccountTypes().then(setAccountTypes).catch(console.error);
  }, []);

  const handleCreate = async () => {
    try {
      const acc = await api.createAccount(newAcc);
      setAccounts((prev) => [acc, ...prev]);
      setShowNew(false);
      setNewAcc(EMPTY_NEW_ACCOUNT);
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const handleDelete = async (id: string) => {
    try {
      await api.deleteAccount(id);
      setAccounts((prev) => prev.filter((a) => a.id !== id));
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const handleUpdate = async () => {
    if (!editingId || !editAcc) return;
    try {
      const updated = await api.updateAccount(editingId, editAcc);
      setAccounts((prev) =>
        prev.map((a) => (a.id === editingId ? updated : a)),
      );
      setEditingId(null);
      setEditAcc(null);
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const startEdit = (acc: Account) => {
    setEditingId(acc.id);
    setEditAcc({
      name: acc.name,
      accountTypeId: acc.accountTypeId,
      bank: acc.bank || "",
      color: acc.color,
      currency: acc.currency,
    });
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
        name: acc.name,
        accountTypeId: acc.accountTypeId,
        bank: acc.bank || "",
        currency: acc.currency,
        color: acc.color,
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

  const getTypeIcon = (accountTypeId: string, color: string, size: number) => {
    return accountTypeId === "credit_card" ? (
      <CreditCard size={size} style={{ color }} />
    ) : (
      <Building2 size={size} style={{ color }} />
    );
  };

  return (
    <>
      <div className="shrink-0 px-8 pt-6">
        <h1 className="text-2xl font-bold text-foreground mb-1">Accounts</h1>
        <p className="text-muted-foreground text-sm">
          Manage your bank accounts and credit cards
        </p>
      </div>
      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto w-full">
        <div className="flex justify-end mb-5">
          <Button size="lg" className="px-4" onClick={() => setShowNew(true)}>
            <Plus /> Add Account
          </Button>
        </div>

        {showNew && (
          <div className="bg-card border border-border rounded-xl p-6 mb-5">
            <div className="flex justify-between items-center mb-4">
              <h4 className="text-base font-semibold text-foreground">
                New Account
              </h4>
              <Button
                variant="ghost"
                size="icon-sm"
                className="text-muted-foreground hover:text-foreground"
                onClick={() => setShowNew(false)}
              >
                <X />
              </Button>
            </div>
            <div className="grid grid-cols-1 md:grid-cols-4 gap-4 mb-5">
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
                  className="w-full h-10.5 cursor-pointer bg-background border border-border rounded-lg p-1"
                />
              </div>
            </div>
            <Button
              size="lg"
              className="px-4"
              onClick={handleCreate}
              disabled={!newAcc.name}
            >
              Create
            </Button>
          </div>
        )}

        {accounts.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-20 px-4 text-center">
            <Building2 className="w-16 h-16 text-muted-foreground opacity-50 mb-4" />
            <h3 className="text-lg font-semibold text-foreground mb-2">
              No Accounts Yet
            </h3>
            <p className="text-muted-foreground text-sm mb-6 max-w-md">
              Add a bank account or credit card to start importing statements
              and categorizing your transactions.
            </p>
            <Button size="lg" className="px-4" onClick={() => setShowNew(true)}>
              Add Account
            </Button>
          </div>
        ) : (
          <div
            className={`grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 ${compactLayout ? "gap-3" : "gap-5"}`}
          >
            {accounts.map((acc) => (
              <Card
                key={acc.id}
                size={compactLayout ? "sm" : "default"}
                className={`border-l-border hover:ring-border transition-colors ${compactLayout ? "" : "[--card-spacing:--spacing(5)]"}`}
                style={{
                  borderLeftColor:
                    editingId === acc.id ? editAcc?.color : acc.color,
                  borderLeftWidth: "3px",
                }}
              >
                {editingId === acc.id && editAcc ? (
                  <CardContent className="flex flex-col flex-1">
                    <div className="flex justify-between items-center mb-4">
                      <h4 className="text-sm font-semibold text-foreground">
                        Edit Account
                      </h4>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        className="text-muted-foreground hover:text-foreground"
                        onClick={() => setEditingId(null)}
                      >
                        <X />
                      </Button>
                    </div>
                    <div className="flex flex-col gap-3 mb-5 flex-1">
                      <Input
                        className="h-10"
                        placeholder="Name"
                        value={editAcc.name}
                        onChange={(e) =>
                          setEditAcc({ ...editAcc, name: e.target.value })
                        }
                      />
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
                      <Input
                        className="h-10"
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
                        className="w-full h-10.5 cursor-pointer bg-background border border-border rounded-lg p-1"
                      />
                    </div>
                    <Button
                      size="lg"
                      className="w-full"
                      onClick={handleUpdate}
                      disabled={!editAcc.name}
                    >
                      Save
                    </Button>
                  </CardContent>
                ) : (
                  <CardContent className="flex flex-1">
                    <div className="flex justify-between items-start flex-1">
                      <div className="flex flex-col flex-1">
                        <div
                          className={`flex items-center gap-3 ${compactLayout ? "mb-0.5" : "mb-1"}`}
                        >
                          {getTypeIcon(
                            acc.accountTypeId,
                            acc.color,
                            compactLayout ? 18 : 20,
                          )}
                          <h3
                            className={`${compactLayout ? "text-sm" : "text-base"} font-semibold text-foreground truncate`}
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
                          className={`${compactLayout ? "text-[12px] mb-2" : "text-[13px] mb-4"} text-muted-foreground flex items-center gap-3`}
                        >
                          <span>{acc.accountTypeName}</span>
                          {acc.bank && (
                            <>
                              <span className="w-1 h-1 rounded-full bg-muted-foreground"></span>
                              <span>{acc.bank}</span>
                            </>
                          )}
                        </div>

                        <div className={compactLayout ? "mb-2" : "mb-4"}>
                          <div className="text-[11px] uppercase tracking-wider text-muted-foreground font-medium mb-1">
                            {acc.accountTypeId === "credit_card"
                              ? "Outstanding"
                              : "Balance"}
                          </div>
                          <div
                            className={`${compactLayout ? "text-xl" : "text-2xl"} font-bold text-foreground font-mono`}
                          >
                            {formatCurrency(acc.balance, acc.currency)}
                          </div>
                        </div>

                        <div className="text-xs text-muted-foreground mt-auto pt-4 border-t border-border/50">
                          Added {formatDate(acc.createdAt)}
                        </div>
                      </div>
                      <div className="flex gap-1 -mr-2 -mt-2">
                        <Button
                          variant="ghost"
                          size="icon"
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
                          <Star
                            size={16}
                            fill={acc.isDefault ? "currentColor" : "none"}
                          />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="text-muted-foreground hover:text-primary hover:bg-accent"
                          onClick={() => handleExport(acc.id)}
                          title="Export CSV"
                        >
                          <Download size={16} />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="text-muted-foreground hover:text-foreground hover:bg-accent"
                          onClick={() => startEdit(acc)}
                          title="Edit Account"
                        >
                          <Edit2 size={16} />
                        </Button>
                        <AlertDialog>
                          <AlertDialogTrigger asChild>
                            <Button
                              variant="ghost"
                              size="icon"
                              className="text-muted-foreground hover:text-destructive hover:bg-destructive/10"
                              title="Delete Account"
                            >
                              <Trash2 size={16} />
                            </Button>
                          </AlertDialogTrigger>
                          <AlertDialogContent>
                            <AlertDialogHeader>
                              <AlertDialogTitle>
                                Delete account?
                              </AlertDialogTitle>
                              <AlertDialogDescription>
                                Are you sure you want to delete "{acc.name}"
                                and all its transactions? This cannot be undone.
                              </AlertDialogDescription>
                            </AlertDialogHeader>
                            <AlertDialogFooter>
                              <AlertDialogCancel>Cancel</AlertDialogCancel>
                              <AlertDialogAction
                                variant="destructive"
                                onClick={() => handleDelete(acc.id)}
                              >
                                Delete
                              </AlertDialogAction>
                            </AlertDialogFooter>
                          </AlertDialogContent>
                        </AlertDialog>
                      </div>
                    </div>
                  </CardContent>
                )}
              </Card>
            ))}
          </div>
        )}
      </div>
    </>
  );
}