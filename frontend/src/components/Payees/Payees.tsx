import { useState, useEffect, useRef, useMemo, type FormEvent } from "react";
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
  ArrowUp,
  ArrowDown,
} from "lucide-react";
import api from "../../api/client";
import { useDomainData } from "../../context/DomainDataContext";
import { useSettings } from "../../context/SettingsContext";
import type { Payee } from "../../types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Spinner } from "@/components/ui/spinner";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
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
import AccountSelect from "@/components/AccountSelect/AccountSelect";

interface PayeeForm {
  name: string;
  accountId: string;
}

const EMPTY_FORM: PayeeForm = { name: "", accountId: "" };
const NO_ACCOUNT = "none";

export default function Payees() {
  const { payees, accounts, loading, refreshPayees } = useDomainData();
  const { compactLayout } = useSettings();
  const [searchParams, setSearchParams] = useSearchParams();
  const syncedUrlRef = useRef(searchParams.toString());
  const [search, setSearch] = useState(() => searchParams.get("search") || "");
  const [showModal, setShowModal] = useState(false);
  const [editingPayee, setEditingPayee] = useState<Payee | null>(null);
  const [formData, setFormData] = useState<PayeeForm>(EMPTY_FORM);
  const [sortDir, setSortDir] = useState<"asc" | "desc">("asc");

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
    // React to external URL changes only; the setters/state read above are
    // stable and including them would re-sync on state we just wrote.
  }, [searchParams]);

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
      refreshPayees();
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const handleDelete = async (id: string) => {
    try {
      await api.deletePayee(id);
      toast.success("Payee deleted");
      refreshPayees();
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

  const accountNameById = useMemo(
    () => new Map(accounts.map((a) => [a.id, a.name])),
    [accounts],
  );

  const filteredPayees = useMemo(() => {
    const list = payees.filter((p) =>
      p.name.toLowerCase().includes(search.toLowerCase()),
    );
    return [...list].sort((x, y) =>
      sortDir === "asc"
        ? x.name.localeCompare(y.name)
        : y.name.localeCompare(x.name),
    );
  }, [payees, search, sortDir]);

  const toggleSort = () => setSortDir((d) => (d === "asc" ? "desc" : "asc"));

  const cellPad = compactLayout ? "py-1.5 px-3" : "py-2.5 px-4";
  const headerBase = `${cellPad} text-xs font-semibold uppercase tracking-wider text-muted-foreground whitespace-nowrap`;

  return (
    <div className="flex flex-col h-full">
      <div className="shrink-0 px-8 pt-6">
        <h1 className="text-2xl font-bold text-foreground mb-1 flex items-center gap-2">
          <Users className="text-primary" />
          Payees
        </h1>
        <p className="text-muted-foreground text-sm">
          Manage entities you pay or receive money from
        </p>
      </div>

      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto w-full">
        <div className="flex justify-between items-center mb-5 gap-4">
          <div className="relative group w-full max-w-md">
            <Search
              className="absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground group-focus-within:text-primary transition-colors"
              size={16}
            />
            <Input
              type="text"
              placeholder="Search payees..."
              aria-label="Search payees"
              className={`pl-9 ${compactLayout ? "h-8" : "h-10"}`}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
          <Button
            size="lg"
            className="px-4 shrink-0"
            onClick={() => openModal(null)}
          >
            <Plus />
            Add Payee
          </Button>
        </div>

        {loading ? (
          <div className="flex justify-center p-20">
            <Spinner className="size-10 text-primary" />
          </div>
        ) : filteredPayees.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-20 px-4 text-center">
            <Users className="w-16 h-16 text-muted-foreground opacity-50 mb-4" />
            <h3 className="text-lg font-semibold text-foreground mb-2">
              {payees.length === 0 ? "No Payees Yet" : "No matching payees"}
            </h3>
            <p className="text-muted-foreground text-sm mb-6 max-w-md">
              {payees.length === 0
                ? "Add a payee to track who you pay or receive money from."
                : "Try a different search term."}
            </p>
            {payees.length === 0 && (
              <Button
                size="lg"
                className="px-4"
                onClick={() => openModal(null)}
              >
                Add Payee
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
                  <TableHead className={headerBase}>Linked Account</TableHead>
                  <TableHead className={`${headerBase} text-right`}>
                    Actions
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {filteredPayees.map((payee) => (
                  <TableRow key={payee.id} className="border-border">
                    <TableCell className={cellPad}>
                      <div className="flex items-center gap-2.5">
                        {payee.accountId ? (
                          <Wallet
                            size={compactLayout ? 16 : 18}
                            className="text-violet-400 shrink-0"
                          />
                        ) : (
                          <ReceiptText
                            size={compactLayout ? 16 : 18}
                            className="text-primary shrink-0"
                          />
                        )}
                        <span className="font-medium text-foreground">
                          {payee.name}
                        </span>
                        {payee.accountId && (
                          <Badge className="bg-violet-500/20 text-violet-400 hover:bg-violet-500/20">
                            Account
                          </Badge>
                        )}
                      </div>
                    </TableCell>
                    <TableCell
                      className={`${cellPad} text-sm text-muted-foreground whitespace-nowrap`}
                    >
                      {payee.accountId ? "Linked" : "Standalone"}
                    </TableCell>
                    <TableCell
                      className={`${cellPad} text-sm text-muted-foreground whitespace-nowrap`}
                    >
                      {payee.accountId
                        ? accountNameById.get(payee.accountId) || "—"
                        : "—"}
                    </TableCell>
                    <TableCell className={`${cellPad} text-right`}>
                      <div className="flex items-center justify-end gap-0.5">
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          className="text-muted-foreground hover:text-primary hover:bg-primary/10"
                          onClick={() => openModal(payee)}
                          title="Edit payee"
                          aria-label={`Edit ${payee.name}`}
                        >
                          <Edit2 size={14} />
                        </Button>
                        {!payee.accountId && (
                          <AlertDialog>
                            <AlertDialogTrigger asChild>
                              <Button
                                variant="ghost"
                                size="icon-sm"
                                title="Delete payee"
                                aria-label={`Delete ${payee.name}`}
                                className="text-muted-foreground hover:text-destructive hover:bg-destructive/10"
                              >
                                <Trash2 size={14} />
                              </Button>
                            </AlertDialogTrigger>
                            <AlertDialogContent>
                              <AlertDialogHeader>
                                <AlertDialogTitle>
                                  Delete payee?
                                </AlertDialogTitle>
                                <AlertDialogDescription>
                                  Are you sure you want to delete "
                                  {payee.name}"? This will NOT delete
                                  transactions but will remove the link.
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
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
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
              <Label htmlFor="payee-name">Payee Name</Label>
              <Input
                id="payee-name"
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
              <Label htmlFor="payee-account">Link to Account (Optional)</Label>
              <AccountSelect
                id="payee-account"
                accounts={accounts}
                value={formData.accountId || NO_ACCOUNT}
                onValueChange={(v) =>
                  setFormData({
                    ...formData,
                    accountId: v === NO_ACCOUNT ? "" : v,
                  })
                }
                placeholder="No linked account"
                triggerClassName="w-full h-11"
                extraItems={<SelectItem value={NO_ACCOUNT}>No linked account</SelectItem>}
              />
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