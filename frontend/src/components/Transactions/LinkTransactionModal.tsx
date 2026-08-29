import { useState, useEffect } from "react";
import {
  Search,
  Link2,
  ArrowRight,
  ArrowLeft,
  RotateCcw,
  Gift,
  Trash2,
} from "lucide-react";
import { Checkbox } from "@/components/ui/checkbox";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Spinner } from "@/components/ui/spinner";
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
  DialogDescription,
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
import api from "../../api/client";
import {
  formatCurrency,
  formatDate,
  formatDateOnly,
  parseDateOnly,
} from "../../utils/formatters";
import type { Transaction, Account, Link, QueryParams } from "../../types";

interface LinkTransactionModalProps {
  txn: Transaction;
  onClose: () => void;
  onSuccess: () => void;
}

export default function LinkTransactionModal({
  txn,
  onClose,
  onSuccess,
}: LinkTransactionModalProps) {
  const [search, setSearch] = useState("");
  const [accountId, setAccountId] = useState("");
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [results, setResults] = useState<Transaction[]>([]);
  const [loading, setLoading] = useState(false);
  const [linkType, setLinkType] = useState("");
  const [pendingTarget, setPendingTarget] = useState<Transaction | null>(null);
  const [dateFrom, setDateFrom] = useState("");
  const [dateTo, setDateTo] = useState("");
  const [matchAmount, setMatchAmount] = useState(true);
  const [excludeSameAccount, setExcludeSameAccount] = useState(true);
  const [existingLinks, setExistingLinks] = useState<Link[]>([]);
  const [linksLoading, setLinksLoading] = useState(true);
  const [unlinkTarget, setUnlinkTarget] = useState<string | null>(null);

  const loadLinks = async () => {
    try {
      setLinksLoading(true);
      setExistingLinks(await api.getLinks({ txnId: txn.id }));
    } catch (err) {
      console.error(err);
    } finally {
      setLinksLoading(false);
    }
  };

  useEffect(() => {
    // Initial fetch accounts
    api.getAccounts().then(setAccounts).catch(console.error);
    loadLinks();

    // Set default date range: ±14 days from txn.date
    if (txn.date) {
      const from = parseDateOnly(txn.date);
      const to = parseDateOnly(txn.date);
      if (from) from.setDate(from.getDate() - 3);
      if (to) to.setDate(to.getDate() + 3);

      const df = formatDateOnly(from);
      const dt = formatDateOnly(to);

      setDateFrom(df);
      setDateTo(dt);

      // Perform initial search with direct values to avoid stale closure
      handleSearch(df, dt);
    } else {
      handleSearch();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [txn.id, txn.date]);

  const handleSearch = async (
    dFrom: string = dateFrom,
    dTo: string = dateTo,
    mAmount: boolean = matchAmount,
    exclAccount: boolean = excludeSameAccount,
  ) => {
    setLoading(true);
    try {
      const params: QueryParams = {
        search,
        accountId,
        dateFrom: dFrom,
        dateTo: dTo,
        limit: 20,
      };

      if (mAmount) {
        params.amount = txn.amount;
      }

      const res = await api.getTransactions(params);

      let filtered = res.data.filter((t) => t.id !== txn.id);

      if (exclAccount) {
        filtered = filtered.filter((t) => t.accountId !== txn.accountId);
      }

      setResults(filtered);
    } catch (err) {
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  const isSameAccount = (targetTxn: Transaction) => {
    return targetTxn.accountId === txn.accountId;
  };

  const handleSelectTarget = (targetTxn: Transaction) => {
    if (isSameAccount(targetTxn)) {
      // Same account: show confirmation step with cashback/refund choice
      setPendingTarget(targetTxn);
      setLinkType("cashback"); // default for same-account
    } else {
      // Different accounts: immediately link as transfer
      performLink(targetTxn, "transfer");
    }
  };

  const performLink = async (targetTxn: Transaction, type: string) => {
    try {
      let fromId = txn.id;
      let toId = targetTxn.id;

      if (txn.type === "credit" && targetTxn.type === "debit") {
        fromId = targetTxn.id;
        toId = txn.id;
      }

      await api.createLink({
        type: type,
        fromTxnId: fromId,
        toTxnId: toId,
      });
      onSuccess();
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const handleUnlinkLink = async (linkId: string) => {
    try {
      await api.deleteLink(linkId);
      await loadLinks();
      onSuccess();
    } catch (err) {
      console.error(err);
    }
  };

  const handleConfirmSameAccountLink = () => {
    if (!pendingTarget || !linkType) return;
    performLink(pendingTarget, linkType);
  };

  // Pending confirmation view for same-account links
  if (pendingTarget) {
    return (
      <Dialog
        open
        onOpenChange={(open) => {
          if (!open) onClose();
        }}
      >
        <DialogContent className="sm:max-w-lg max-h-[80vh] flex flex-col overflow-hidden p-0 gap-0 rounded-2xl">
          <DialogHeader className="px-6 py-4 border-b border-border bg-card">
            <div className="flex items-center gap-3 pr-8">
              <Button
                variant="ghost"
                size="icon-sm"
                className="-ml-1.5"
                onClick={() => setPendingTarget(null)}
              >
                <ArrowLeft size={18} />
              </Button>
              <div>
                <DialogTitle>Same Account Link</DialogTitle>
                <DialogDescription className="text-xs mt-0.5">
                  Choose the link type for this connection
                </DialogDescription>
              </div>
            </div>
          </DialogHeader>

          {/* Transaction pair preview */}
          <div className="px-6 py-5 border-b border-border bg-accent/20">
            <div className="space-y-3">
              <div className="bg-background/50 border border-border rounded-xl p-3.5">
                <div className="text-[10px] font-bold text-muted-foreground uppercase tracking-wider mb-1.5">
                  Source
                </div>
                <div className="font-medium text-sm text-foreground truncate">
                  {txn.description}
                </div>
                <div className="text-xs text-muted-foreground mt-1">
                  {txn.accountName} · {formatDate(txn.date)} ·
                  <span
                    className={
                      txn.type === "debit" ? "text-destructive" : "text-emerald-500"
                    }
                  >
                    {txn.type === "debit" ? "−" : "+"}
                    {formatCurrency(txn.amount)}
                  </span>
                </div>
              </div>
              <div className="flex justify-center">
                <Link2 className="text-primary/50" size={18} />
              </div>
              <div className="bg-background/50 border border-border rounded-xl p-3.5">
                <div className="text-[10px] font-bold text-muted-foreground uppercase tracking-wider mb-1.5">
                  Target
                </div>
                <div className="font-medium text-sm text-foreground truncate">
                  {pendingTarget.description}
                </div>
                <div className="text-xs text-muted-foreground mt-1">
                  {pendingTarget.accountName} · {formatDate(pendingTarget.date)}{" "}
                  ·
                  <span
                    className={
                      pendingTarget.type === "debit"
                        ? "text-destructive"
                        : "text-emerald-500"
                    }
                  >
                    {pendingTarget.type === "debit" ? "−" : "+"}
                    {formatCurrency(pendingTarget.amount)}
                  </span>
                </div>
              </div>
            </div>
          </div>

          {/* Link type selection */}
          <div className="px-6 py-5 border-b border-border">
            <div className="text-[11px] font-bold text-muted-foreground uppercase tracking-wider mb-3">
              Link Type
            </div>
            <div className="grid grid-cols-2 gap-3">
              <Button
                type="button"
                variant="ghost"
                onClick={() => setLinkType("cashback")}
                className={`relative flex flex-col items-center gap-2 p-4 h-auto rounded-xl border-2 transition-all ${
                  linkType === "cashback"
                    ? "border-emerald-500 bg-emerald-500/10 shadow-lg shadow-emerald-500/10"
                    : "border-border bg-background/50 hover:border-muted-foreground"
                }`}
              >
                <Gift
                  size={22}
                  className={
                    linkType === "cashback"
                      ? "text-emerald-400"
                      : "text-muted-foreground"
                  }
                />
                <span
                  className={`text-sm font-semibold ${linkType === "cashback" ? "text-emerald-400" : "text-muted-foreground"}`}
                >
                  Cashback
                </span>
                <span className="text-[10px] text-muted-foreground">
                  Reward or cash back
                </span>
              </Button>
              <Button
                type="button"
                variant="ghost"
                onClick={() => setLinkType("refund")}
                className={`relative flex flex-col items-center gap-2 p-4 h-auto rounded-xl border-2 transition-all ${
                  linkType === "refund"
                    ? "border-amber-500 bg-amber-500/10 shadow-lg shadow-amber-500/10"
                    : "border-border bg-background/50 hover:border-muted-foreground"
                }`}
              >
                <RotateCcw
                  size={22}
                  className={
                    linkType === "refund" ? "text-amber-400" : "text-muted-foreground"
                  }
                />
                <span
                  className={`text-sm font-semibold ${linkType === "refund" ? "text-amber-400" : "text-muted-foreground"}`}
                >
                  Refund
                </span>
                <span className="text-[10px] text-muted-foreground">
                  Return or reversal
                </span>
              </Button>
            </div>
          </div>

          {/* Actions */}
          <div className="px-6 py-4 flex items-center justify-end gap-3">
            <Button variant="ghost" onClick={() => setPendingTarget(null)}>
              Back to Results
            </Button>
            <Button
              onClick={handleConfirmSameAccountLink}
              disabled={!linkType}
              className="px-6 py-2.5 h-auto text-sm font-bold"
            >
              Confirm Link
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    );
  }

  return (
    <>
      <Dialog
        open
        onOpenChange={(open) => {
          if (!open) onClose();
        }}
      >
        <DialogContent className="sm:max-w-2xl max-h-[80vh] flex flex-col overflow-hidden p-0 gap-0 rounded-2xl">
          <DialogHeader className="px-6 py-4 border-b border-border bg-card pr-10">
            <DialogTitle>
              {existingLinks.length > 0
                ? "Manage Links"
                : "Find Match & Link"}
            </DialogTitle>
            <DialogDescription>
              {existingLinks.length > 0
                ? "View or remove existing links, or link more transactions"
                : "Pick a matching transaction to create a connection"}
            </DialogDescription>
          </DialogHeader>

          {/* Source Txn Summary */}
          <div className="px-6 py-4 bg-muted border-b border-border">
            <div className="flex items-center gap-4">
              <div className="flex-1 min-w-0">
                <div className="text-[11px] font-bold text-muted-foreground uppercase tracking-wider mb-1">
                  Source Transaction
                </div>
                <div className="font-medium text-foreground truncate">
                  {txn.description}
                </div>
                <div className="text-xs text-muted-foreground mt-1">
                  {txn.accountName} · {formatDate(txn.date)} ·
                  <span
                    className={
                      txn.type === "debit" ? "text-destructive" : "text-emerald-500"
                    }
                  >
                    {txn.type === "debit" ? "−" : "+"}
                    {formatCurrency(txn.amount)}
                  </span>
                </div>
              </div>
              <ArrowRight className="text-primary opacity-50 shrink-0" size={20} />
              <div className="flex-1 text-center py-4 border-2 border-dashed border-border rounded-xl">
                <Link2 className="w-5 h-5 text-muted-foreground mx-auto mb-1" />
                <span className="text-[11px] text-muted-foreground">
                  Pick match below
                </span>
              </div>
            </div>
          </div>

          {/* Existing links */}
          <div className="px-6 py-3 border-b border-border bg-muted/50">
            <div className="text-[11px] font-bold text-muted-foreground uppercase tracking-wider mb-2">
              Linked Transactions ({existingLinks.length})
            </div>
            {linksLoading ? (
              <div className="flex items-center gap-2 text-xs text-muted-foreground py-1">
                <Spinner className="h-4 w-4" />
                Loading links...
              </div>
            ) : existingLinks.length === 0 ? (
              <div className="text-[11px] text-muted-foreground italic py-1">
                No links yet. Find a match below to create one.
              </div>
            ) : (
              <div className="space-y-1.5">
                {existingLinks.map((l) => {
                  const other = l.fromTxnId === txn.id ? l.toTxn : l.fromTxn;
                  const typeClass =
                    l.type === "transfer"
                      ? "bg-primary/10 text-primary"
                      : l.type === "cashback"
                        ? "bg-emerald-500/10 text-emerald-400"
                        : "bg-amber-500/10 text-amber-400";
                  return (
                    <div
                      key={l.id}
                      className="flex items-center gap-2.5 bg-card border border-border rounded-lg px-3 py-2"
                    >
                      <Badge
                        className={`h-auto px-1.5 py-0.5 rounded text-[10px] font-bold uppercase ${typeClass}`}
                      >
                        {l.type}
                      </Badge>
                      <div className="flex-1 min-w-0">
                        <div className="text-xs text-foreground truncate">
                          {other?.description}
                        </div>
                        <div className="text-[10px] text-muted-foreground">
                          {other?.accountName} · {formatDate(other?.date)} ·
                          <span
                            className={
                              other?.type === "debit"
                                ? "text-destructive"
                                : "text-emerald-500"
                            }
                          >
                            {other?.type === "debit" ? "−" : "+"}
                            {formatCurrency(other?.amount || 0)}
                          </span>
                        </div>
                      </div>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        className="text-muted-foreground hover:text-destructive hover:bg-destructive/10"
                        title="Unlink"
                        onClick={() => setUnlinkTarget(l.id)}
                      >
                        <Trash2 size={14} />
                      </Button>
                    </div>
                  );
                })}
              </div>
            )}
          </div>

          {/* Info banner */}
          <div className="px-6 py-2.5 bg-primary/5 border-b border-border">
            <p className="text-[11px] text-primary/70 text-center">
              <span className="font-semibold">Cross-account</span> links
              auto-assign as Transfer ·{" "}
              <span className="font-semibold">Same-account</span> links let you
              choose Cashback or Refund · A transaction can be linked to many
              others
            </p>
          </div>

          {/* Search / Filters */}
          <div className="p-4 border-b border-border bg-card">
            <div className="flex flex-col gap-4">
              <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
                <div className="relative md:col-span-3">
                  <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
                  <Input
                    className="pl-9"
                    placeholder="Search description..."
                    value={search}
                    onChange={(e) => setSearch(e.target.value)}
                    onKeyDown={(e) => e.key === "Enter" && handleSearch()}
                  />
                </div>
                <div className="flex flex-row flex-wrap md:col-span-3 gap-3">
                  <div className="flex items-center gap-2 bg-background border border-border rounded-lg px-3.5 py-2.5">
                    <Checkbox
                      id="matchAmount"
                      checked={matchAmount}
                      onCheckedChange={(checked) => {
                        const isChecked = checked === true;
                        setMatchAmount(isChecked);
                        handleSearch(
                          dateFrom,
                          dateTo,
                          isChecked,
                          excludeSameAccount,
                        );
                      }}
                    />
                    <Label
                      htmlFor="matchAmount"
                      className="text-sm text-muted-foreground cursor-pointer select-none"
                    >
                      Match Amount
                    </Label>
                  </div>

                  <div className="flex items-center gap-2 bg-background border border-border rounded-lg px-3.5 py-2.5">
                    <Checkbox
                      id="excludeAccount"
                      checked={excludeSameAccount}
                      onCheckedChange={(checked) => {
                        const isChecked = checked === true;
                        setExcludeSameAccount(isChecked);
                        handleSearch(dateFrom, dateTo, matchAmount, isChecked);
                      }}
                    />
                    <Label
                      htmlFor="excludeAccount"
                      className="text-sm text-muted-foreground cursor-pointer select-none"
                    >
                      Different Account Only
                    </Label>
                  </div>
                  <Select
                    value={accountId || "all"}
                    onValueChange={(v) => setAccountId(v === "all" ? "" : v)}
                  >
                    <SelectTrigger className="w-full">
                      <SelectValue placeholder="All Accounts" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="all">All Accounts</SelectItem>
                      {accounts.map((a) => (
                        <SelectItem key={a.id} value={a.id}>
                          {a.name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
              </div>

              <div className="flex flex-col md:flex-row items-center gap-3">
                <div className="flex-1 flex items-center gap-2 p-1.5 bg-background border border-border rounded-xl w-full">
                  <div className="pl-2.5 text-[10px] font-bold text-muted-foreground uppercase tracking-wider shrink-0">
                    Date Range
                  </div>
                  <Input
                    type="date"
                    className="flex-1"
                    value={dateFrom}
                    onChange={(e) => setDateFrom(e.target.value)}
                  />
                  <div className="text-muted-foreground text-xs px-0.5">to</div>
                  <Input
                    type="date"
                    className="flex-1"
                    value={dateTo}
                    onChange={(e) => setDateTo(e.target.value)}
                  />
                </div>
                <Button
                  onClick={() => handleSearch()}
                  className="w-full md:w-auto px-8 py-3 h-auto text-sm font-bold rounded-xl"
                >
                  <Search size={16} />
                  Find Match
                </Button>
              </div>
            </div>
          </div>

          {/* Results */}
          <div className="flex-1 overflow-y-auto p-4 custom-scrollbar">
            {loading ? (
              <div className="flex flex-col items-center justify-center p-12 text-muted-foreground">
                <Spinner className="h-8 w-8 mb-3 text-primary" />
                <span className="text-sm">Searching...</span>
              </div>
            ) : results.length === 0 ? (
              <div className="text-center p-12 bg-background/30 rounded-2xl border border-dashed border-border">
                <Link2 className="w-10 h-10 text-muted-foreground mx-auto mb-3 opacity-20" />
                <div className="text-muted-foreground text-sm font-medium">
                  No potential matches found
                </div>
                <p className="text-muted-foreground text-[11px] mt-1 italic">
                  Try adjusting your search or filters
                </p>
              </div>
            ) : (
              <div className="space-y-2">
                {results.map((r) => {
                  const sameAccount = isSameAccount(r);
                  return (
                    <div
                      key={r.id}
                      className="bg-background/50 border border-border p-4 rounded-xl flex items-center gap-4 hover:border-primary/50 hover:bg-accent/30 transition-all group"
                    >
                      <div className="flex-1 min-w-0">
                        <div className="font-semibold text-sm text-foreground truncate group-hover:text-primary transition-colors">
                          {r.description}
                        </div>
                        <div className="text-[12px] text-muted-foreground mt-1 flex items-center gap-2 flex-wrap">
                          <Badge
                            variant="secondary"
                            className="h-auto px-1.5 py-0.5 rounded"
                          >
                            {r.accountName}
                          </Badge>
                          <span>·</span>
                          <span>{formatDate(r.date)}</span>
                          <span>·</span>
                          <span
                            className={`font-bold ${r.type === "debit" ? "text-destructive" : "text-emerald-500"}`}
                          >
                            {r.type === "debit" ? "−" : "+"}
                            {formatCurrency(r.amount)}
                          </span>
                          {sameAccount && (
                            <Badge className="h-auto px-1.5 py-0.5 bg-amber-500/10 text-amber-400 text-[10px] font-semibold rounded">
                              Same Account
                            </Badge>
                          )}
                          {r.isLinked && (
                            <Badge className="h-auto px-1.5 py-0.5 bg-primary/10 text-primary text-[10px] font-semibold rounded">
                              Already Linked
                            </Badge>
                          )}
                        </div>
                      </div>
                      <Button
                        onClick={() => handleSelectTarget(r)}
                        className="opacity-0 group-hover:opacity-100 px-4 py-2 h-auto text-xs font-bold"
                      >
                        {sameAccount ? "Choose Type…" : "Link as Transfer"}
                      </Button>
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        </DialogContent>
      </Dialog>

      <AlertDialog
        open={!!unlinkTarget}
        onOpenChange={(open) => {
          if (!open) setUnlinkTarget(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove this link?</AlertDialogTitle>
            <AlertDialogDescription>
              This will remove the connection between the two transactions.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                if (unlinkTarget) handleUnlinkLink(unlinkTarget);
              }}
            >
              Remove
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}