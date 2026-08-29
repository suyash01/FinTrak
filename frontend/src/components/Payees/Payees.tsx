import { useState, useEffect, useRef, type FormEvent } from "react";
import { useSearchParams } from "react-router-dom";
import {
  Plus,
  Search,
  Trash2,
  Edit2,
  Users,
  ReceiptText,
  Wallet,
  AlertCircle,
} from "lucide-react";
import api from "../../api/client";
import type { Payee, Account } from "../../types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Spinner } from "@/components/ui/spinner";
import {
  Dialog,
  DialogContent,
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
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { toast } from "sonner";

interface PayeeForm {
  name: string;
  accountId: string;
}

const EMPTY_FORM: PayeeForm = { name: "", accountId: "" };
const NO_ACCOUNT = "none";

export default function Payees() {
  const [payees, setPayees] = useState<Payee[]>([]);
  const [loading, setLoading] = useState(true);
  const [searchParams, setSearchParams] = useSearchParams();
  const syncedUrlRef = useRef(searchParams.toString());
  const [search, setSearch] = useState(() => searchParams.get("search") || "");
  const [showModal, setShowModal] = useState(false);
  const [editingPayee, setEditingPayee] = useState<Payee | null>(null);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [formData, setFormData] = useState<PayeeForm>(EMPTY_FORM);

  useEffect(() => {
    fetchPayees();
    fetchAccounts();
  }, []);

  // Keep the search filter in sync with the URL so it is shareable and
  // back/forward friendly.
  useEffect(() => {
    const params: Record<string, string> = {};
    if (search) params.search = search;
    const desiredQs = new URLSearchParams(params).toString();
    if (desiredQs === syncedUrlRef.current) return;
    syncedUrlRef.current = desiredQs;
    setSearchParams(params, { replace: true });
  }, [search]);

  useEffect(() => {
    const currentQs = searchParams.toString();
    if (currentQs === syncedUrlRef.current) return;
    const next = searchParams.get("search") || "";
    if (next !== search) setSearch(next);
    syncedUrlRef.current = currentQs;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchParams]);

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
      toast.error((err as Error).message);
    }
  };

  const handleDelete = async (id: string) => {
    try {
      await api.deletePayee(id);
      toast.success("Payee deleted");
      fetchPayees();
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const openModal = (payee: Payee | null) => {
    setEditingPayee(payee);
    setFormData(
      payee
        ? { name: payee.name, accountId: payee.accountId || "" }
        : EMPTY_FORM,
    );
    setShowModal(true);
  };

  const filteredPayees = payees.filter((p) =>
    p.name.toLowerCase().includes(search.toLowerCase()),
  );

  return (
    <div className="flex flex-col h-full">
      <div className="shrink-0 px-8 pt-6">
        <div className="flex justify-between items-center">
          <div>
            <h1 className="text-2xl font-bold text-foreground flex items-center gap-2">
              <Users className="text-primary" />
              Payees
            </h1>
            <p className="text-muted-foreground text-sm mt-1">
              Manage entities you pay or receive money from
            </p>
          </div>
          <Button onClick={() => openModal(null)}>
            <Plus />
            Add Payee
          </Button>
        </div>
      </div>

      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto w-full space-y-6">
        {/* Search Bar */}
        <div className="relative group max-w-2xl">
          <Search
            className="absolute left-4 top-1/2 -translate-y-1/2 text-muted-foreground group-focus-within:text-primary transition-colors"
            size={18}
          />
          <Input
            type="text"
            placeholder="Search payees..."
            className="pl-12 h-11 rounded-2xl"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>

        {/* Grid */}
        {loading ? (
          <div className="flex justify-center p-20">
            <Spinner className="size-10 text-primary" />
          </div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
            {filteredPayees.map((payee) => (
              <div
                key={payee.id}
                className="bg-card border border-border p-5 rounded-2xl hover:border-primary/30 transition-all group relative shadow-md"
              >
                <div className="flex justify-between items-start">
                  <div className="flex items-center gap-3">
                    <div
                      className={`w-10 h-10 rounded-xl flex items-center justify-center border shadow-inner ${payee.accountId ? "bg-violet-500/10 text-violet-400 border-violet-500/20" : "bg-background text-primary border-border"}`}
                    >
                      {payee.accountId ? (
                        <Wallet size={20} />
                      ) : (
                        <ReceiptText size={20} />
                      )}
                    </div>
                    <div>
                      <div className="flex items-center gap-2">
                        <h3 className="font-bold text-foreground">
                          {payee.name}
                        </h3>
                        {payee.accountId && (
                          <Badge className="bg-violet-500/20 text-violet-400 hover:bg-violet-500/20">
                            Account
                          </Badge>
                        )}
                      </div>
                      <p className="text-[10px] text-muted-foreground mt-0.5 font-mono">
                        ID: {payee.id.slice(0, 8)}
                      </p>
                    </div>
                  </div>
                  <div className="flex gap-1">
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      onClick={() => openModal(payee)}
                      title="Edit Payee"
                    >
                      <Edit2 />
                    </Button>
                    {!payee.accountId && (
                      <AlertDialog>
                        <AlertDialogTrigger asChild>
                          <Button
                            variant="ghost"
                            size="icon-sm"
                            title="Delete Payee"
                            className="text-muted-foreground hover:text-destructive"
                          >
                            <Trash2 />
                          </Button>
                        </AlertDialogTrigger>
                        <AlertDialogContent>
                          <AlertDialogHeader>
                            <AlertDialogTitle>Delete payee?</AlertDialogTitle>
                            <AlertDialogDescription>
                              Are you sure you want to delete "{payee.name}"?
                              This will NOT delete transactions but will remove
                              the link.
                            </AlertDialogDescription>
                          </AlertDialogHeader>
                          <AlertDialogFooter>
                            <AlertDialogCancel>Cancel</AlertDialogCancel>
                            <AlertDialogAction
                              variant="destructive"
                              onClick={() => handleDelete(payee.id)}
                            >
                              Delete
                            </AlertDialogAction>
                          </AlertDialogFooter>
                        </AlertDialogContent>
                      </AlertDialog>
                    )}
                  </div>
                </div>
              </div>
            ))}
            {filteredPayees.length === 0 && !loading && (
              <div className="col-span-full py-20 text-center border-2 border-dashed border-border rounded-2xl">
                <Users size={40} className="mx-auto text-muted-foreground/50 mb-4" />
                <p className="text-muted-foreground">No payees found.</p>
              </div>
            )}
          </div>
        )}
      </div>

      {/* Add/Edit Modal */}
      <Dialog
        open={showModal}
        onOpenChange={(open) => {
          setShowModal(open);
          if (!open) {
            setEditingPayee(null);
            setFormData(EMPTY_FORM);
          }
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>
              {editingPayee ? "Edit Payee" : "Add New Payee"}
            </DialogTitle>
          </DialogHeader>
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-2">
              <Label>Payee Name</Label>
              <Input
                type="text"
                required
                autoFocus
                disabled={!!editingPayee?.accountId}
                placeholder="e.g. Amazon, Google, etc."
                className="h-11 font-medium"
                value={formData.name}
                onChange={(e) =>
                  setFormData({ ...formData, name: e.target.value })
                }
              />
              {editingPayee?.accountId && (
                <p className="text-xs text-muted-foreground flex items-center gap-1">
                  <AlertCircle size={12} /> Account-linked payees must be
                  renamed via the Accounts page.
                </p>
              )}
            </div>

            <div className="space-y-2">
              <Label>Link to Account (Optional)</Label>
              <Select
                value={formData.accountId || NO_ACCOUNT}
                onValueChange={(v) =>
                  setFormData({
                    ...formData,
                    accountId: v === NO_ACCOUNT ? "" : v,
                  })
                }
              >
                <SelectTrigger className="w-full h-11">
                  <SelectValue placeholder="No linked account" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={NO_ACCOUNT}>No linked account</SelectItem>
                  {accounts.map((acc) => (
                    <SelectItem key={acc.id} value={acc.id}>
                      {acc.name} ({acc.bank || acc.accountTypeName})
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <p className="text-xs text-muted-foreground">
                Linking to an account helps identify internal transfers.
              </p>
            </div>
            <div className="flex justify-end pt-2 gap-3">
              <Button
                type="button"
                variant="ghost"
                onClick={() => setShowModal(false)}
              >
                Cancel
              </Button>
              <Button type="submit">
                {editingPayee ? "Save Changes" : "Create Payee"}
              </Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  );
}