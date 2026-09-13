import { useState, useEffect, useRef } from "react";
import { Link2, ArrowRight } from "lucide-react";
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
import { toastApiError } from "../../lib/errors";
import api from "../../api/client";
import { useDomainData } from "../../context/DomainDataContext";
import {
  formatCurrency,
  formatDate,
  formatDateOnly,
  parseDateOnly,
} from "../../utils/formatters";
import type { Transaction, Link, LinkType, QueryParams } from "../../types";
import LinkTypeStep from "./LinkTypeStep";
import LinkedTransactionsList from "./LinkedTransactionsList";
import LinkSearchPanel from "./LinkSearchPanel";
import LinkResultsList from "./LinkResultsList";
import { orderLinkEndpoints } from "./linkHelpers";

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
  const { accounts } = useDomainData();
  const [results, setResults] = useState<Transaction[]>([]);
  const [loading, setLoading] = useState(false);
  const [linkType, setLinkType] = useState<LinkType>("transfer");
  const [pendingTarget, setPendingTarget] = useState<Transaction | null>(null);
  const [dateFrom, setDateFrom] = useState("");
  const [dateTo, setDateTo] = useState("");
  const [matchAmount, setMatchAmount] = useState(true);
  const [excludeSameAccount, setExcludeSameAccount] = useState(true);
  const [existingLinks, setExistingLinks] = useState<Link[]>([]);
  const [linksLoading, setLinksLoading] = useState(true);
  const [unlinkTarget, setUnlinkTarget] = useState<string | null>(null);
  // Aborts the in-flight candidate search when a newer one starts (or the modal
  // unmounts), so a slow response can't overwrite fresher results.
  const searchAbortRef = useRef<AbortController | null>(null);

  // Abort any in-flight candidate search when the modal unmounts.
  useEffect(() => () => searchAbortRef.current?.abort(), []);

  const loadLinks = async () => {
    try {
      setLinksLoading(true);
      setExistingLinks(await api.getLinks({ txnId: txn.id }));
    } catch (err) {
      toastApiError(err);
    } finally {
      setLinksLoading(false);
    }
  };

  useEffect(() => {
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
    // Re-run the initial search only when the transaction itself changes; the
    // search inputs are seeded from txn above and edited by the user after.
  }, [txn.id, txn.date]);

  const handleSearch = async (
    dFrom: string = dateFrom,
    dTo: string = dateTo,
    mAmount: boolean = matchAmount,
    exclAccount: boolean = excludeSameAccount,
    acctId: string = accountId,
  ) => {
    searchAbortRef.current?.abort();
    const controller = new AbortController();
    searchAbortRef.current = controller;

    setLoading(true);
    try {
      const params: QueryParams = {
        search,
        accountId: acctId,
        dateFrom: dFrom,
        dateTo: dTo,
        limit: 20,
      };

      if (mAmount) {
        params.amount = txn.amount;
      }

      const res = await api.getTransactions(params, {
        signal: controller.signal,
      });
      if (searchAbortRef.current !== controller) return;

      let filtered = res.data.filter((t) => t.id !== txn.id);

      if (exclAccount) {
        filtered = filtered.filter((t) => t.accountId !== txn.accountId);
      }

      setResults(filtered);
    } catch (err) {
      if ((err as Error).name !== "AbortError") toastApiError(err);
    } finally {
      if (searchAbortRef.current === controller) setLoading(false);
    }
  };

  const handleSelectTarget = (targetTxn: Transaction) => {
    // Always ask the user to pick a link type — cross-account pairs are no
    // longer assumed to be transfers.
    setPendingTarget(targetTxn);
    setLinkType(
      targetTxn.accountId === txn.accountId ? "cashback" : "transfer",
    );
  };

  const performLink = async (targetTxn: Transaction, type: LinkType) => {
    try {
      const { fromId, toId } = orderLinkEndpoints(txn, targetTxn);
      await api.createLink({ type: type, fromTxnId: fromId, toTxnId: toId });
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
      toastApiError(err);
    }
  };

  const handleConfirmLink = () => {
    if (!pendingTarget || !linkType) return;
    performLink(pendingTarget, linkType);
  };

  if (pendingTarget) {
    return (
      <LinkTypeStep
        txn={txn}
        target={pendingTarget}
        linkType={linkType}
        onLinkTypeChange={setLinkType}
        onBack={() => setPendingTarget(null)}
        onConfirm={handleConfirmLink}
      />
    );
  }

  return (
    <>
      <Dialog open onOpenChange={(open) => !open && onClose()}>
        <DialogContent className="sm:max-w-2xl max-h-[80vh] flex flex-col overflow-hidden p-0 gap-0 rounded-2xl">
          <DialogHeader className="px-6 py-4 border-b border-border bg-card pr-10">
            <DialogTitle>
              {existingLinks.length > 0 ? "Manage Links" : "Find Match & Link"}
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
                      txn.type === "debit"
                        ? "text-destructive"
                        : "text-emerald-500"
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

          <LinkedTransactionsList
            sourceTxnId={txn.id}
            links={existingLinks}
            loading={linksLoading}
            onRequestUnlink={setUnlinkTarget}
          />

          {/* Info banner */}
          <div className="px-6 py-2.5 bg-primary/5 border-b border-border">
            <p className="text-[11px] text-primary/70 text-center">
              Choose a link type for any pair — Transfer, Cashback, Refund, or
              Bill Payment · A transaction can be linked to many others
            </p>
          </div>

          <LinkSearchPanel
            search={search}
            onSearchChange={setSearch}
            accountId={accountId}
            accounts={accounts}
            dateFrom={dateFrom}
            dateTo={dateTo}
            onDateFromChange={setDateFrom}
            onDateToChange={setDateTo}
            matchAmount={matchAmount}
            excludeSameAccount={excludeSameAccount}
            onMatchAmountChange={(checked) => {
              setMatchAmount(checked);
              handleSearch(dateFrom, dateTo, checked, excludeSameAccount);
            }}
            onExcludeSameAccountChange={(checked) => {
              setExcludeSameAccount(checked);
              handleSearch(dateFrom, dateTo, matchAmount, checked);
            }}
            onAccountChange={(v) => {
              const acctId = v === "all" ? "" : v;
              setAccountId(acctId);
              handleSearch(
                dateFrom,
                dateTo,
                matchAmount,
                excludeSameAccount,
                acctId,
              );
            }}
            onSubmit={() => handleSearch()}
          />

          <LinkResultsList
            loading={loading}
            results={results}
            sourceAccountId={txn.accountId}
            onSelect={handleSelectTarget}
          />
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
