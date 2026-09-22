import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ArrowRightLeft,
  Check,
  Loader2,
  Pencil,
  Trash2,
  Undo2,
  Unlink,
} from "lucide-react";
import { toast } from "sonner";
import api from "../../api/client";
import { formatCurrency, formatDate } from "../../utils/formatters";
import { todayLocalISO } from "../../lib/dates";
import { useDomainData } from "../../context/DomainDataContext";
import { useSettings } from "../../context/SettingsContext";
import type {
  Account,
  LoanPayoff,
  LoanSchedule,
  LoanScheduleDetail,
  LoanTransferMode,
  LoanTransferRequest,
  Transaction,
} from "../../types";
import { cn } from "@/lib/utils";
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
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

interface LoanScheduleDialogProps {
  account: Account;
  onClose: () => void;
}

// The dialog's own form state. The rate is edited as a percentage and stored as
// basis points, so 9.5% round-trips as 950.
interface ScheduleForm {
  principal: string;
  processingFee: string;
  disbursalDate: string;
  ratePercent: string;
  tenureMonths: string;
  startDate: string;
}

const EMPTY_FORM: ScheduleForm = {
  principal: "",
  processingFee: "",
  disbursalDate: "",
  ratePercent: "",
  tenureMonths: "",
  startDate: "",
};

function toForm(detail: LoanScheduleDetail): ScheduleForm {
  const s = detail.schedule;
  if (!s) return EMPTY_FORM;
  return {
    principal: String(s.principal),
    processingFee: s.processingFee > 0 ? String(s.processingFee) : "",
    disbursalDate: s.disbursalDate ? s.disbursalDate.slice(0, 10) : "",
    ratePercent: String(s.annualRateBps / 100),
    tenureMonths: String(s.tenureMonths),
    startDate: s.startDate.slice(0, 10),
  };
}

// A bank credit never lands long after the disbursal it funded, so candidates
// are windowed ±45 days around the disbursal date. When that date is unknown
// the anchor is the first installment, and the money was released months before
// the first EMI fell due, so the window reaches further back.
const CREDIT_WINDOW_DAYS = 45;
const DISBURSEMENT_LOOKBACK_DAYS = 240;
// Both candidate queries are capped; the dialog ranks whatever comes back, so
// pulling the whole page of matches beats paging through them.
const CREDIT_QUERY_LIMIT = 100;

function shiftDays(iso: string, days: number): string {
  const d = new Date(`${iso.slice(0, 10)}T00:00:00Z`);
  d.setUTCDate(d.getUTCDate() + days);
  return d.toISOString().slice(0, 10);
}

function creditWindow(schedule: LoanSchedule): {
  dateFrom: string;
  dateTo: string;
} {
  const anchor = schedule.disbursalDate || schedule.startDate;
  return {
    dateFrom: shiftDays(
      anchor,
      schedule.disbursalDate
        ? -CREDIT_WINDOW_DAYS
        : -DISBURSEMENT_LOOKBACK_DAYS,
    ),
    dateTo: shiftDays(anchor, CREDIT_WINDOW_DAYS),
  };
}

// The balance-transfer form. Target terms are only collected when the chosen
// target loan has no schedule of its own yet.
interface TransferForm {
  targetId: string;
  transferDate: string;
  targetRatePercent: string;
  targetTenureMonths: string;
  targetStartDate: string;
}

// LoanScheduleDialog manages one Loan / EMI account's optional amortization
// schedule: the terms, the generated principal/interest table, and the progress
// derived from the EMI payments attached to the loan.
export default function LoanScheduleDialog({
  account,
  onClose,
}: LoanScheduleDialogProps) {
  const { compactLayout } = useSettings();
  const { accounts } = useDomainData();
  const [detail, setDetail] = useState<LoanScheduleDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState(false);
  const [form, setForm] = useState<ScheduleForm>(EMPTY_FORM);
  const [saving, setSaving] = useState(false);
  const [transferOpen, setTransferOpen] = useState(false);
  const [credits, setCredits] = useState<Transaction[]>([]);
  const [loadingCredits, setLoadingCredits] = useState(false);
  // Destructive actions are confirmed first: the schedule delete drops the
  // amortization table with its installment matches, and undoing a balance
  // transfer reverts a settlement. Both are irreversible.
  const [confirmDeleteOpen, setConfirmDeleteOpen] = useState(false);
  const [undoTransferId, setUndoTransferId] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const res = await api.getLoanSchedule(account.id);
      setDetail(res);
      setForm(toForm(res));
      setEditing(res.schedule === null);
    } catch (err) {
      setError((err as Error).message || "Failed to load the loan schedule");
    } finally {
      setLoading(false);
    }
  }, [account.id]);

  useEffect(() => {
    void load();
  }, [load]);

  const schedule = detail?.schedule ?? null;
  const disbursement = detail?.disbursement;
  // The span both candidate queries cover, named in the empty state so the
  // user knows where the picker looked.
  const creditRange = useMemo(
    () => (schedule ? creditWindow(schedule) : null),
    [schedule],
  );

  // Candidate bank credits for the loan's disbursement: an exact-amount match
  // finds the credit wherever it landed, and the date window catches one whose
  // amount drifted from the net released. Fetched once the schedule and its net
  // are known and no credit is linked yet. A credit sitting on a loan account
  // can never have funded another loan, so those are dropped.
  const loadCredits = useCallback(async () => {
    if (!schedule || !disbursement) return;
    const net = disbursement.net;
    const { dateFrom, dateTo } = creditWindow(schedule);
    setLoadingCredits(true);
    try {
      const [byAmount, byWindow] = await Promise.all([
        api.getTransactions({
          type: "credit",
          amount: net,
          limit: CREDIT_QUERY_LIMIT,
        }),
        api.getTransactions({
          type: "credit",
          dateFrom,
          dateTo,
          limit: CREDIT_QUERY_LIMIT,
        }),
      ]);
      const loanIds = new Set(
        accounts.filter((a) => a.accountTypeId === "loan").map((a) => a.id),
      );
      const byId = new Map<string, Transaction>();
      for (const t of [...byAmount.data, ...byWindow.data]) {
        if (loanIds.has(t.accountId)) continue;
        byId.set(t.id, t);
      }
      // The credit that released the net amount comes first, then the rest by
      // how far off they are; ties break on the most recent credit.
      setCredits(
        [...byId.values()].sort((a, b) => {
          const delta = Math.abs(a.amount - net) - Math.abs(b.amount - net);
          return delta !== 0 ? delta : b.date.localeCompare(a.date);
        }),
      );
    } catch (err) {
      toast.error((err as Error).message || "Failed to load candidate credits");
    } finally {
      setLoadingCredits(false);
    }
  }, [schedule, disbursement, accounts]);

  useEffect(() => {
    if (disbursement && !disbursement.creditTransactionId) void loadCredits();
  }, [disbursement, loadCredits]);

  const handleSave = async () => {
    const principal = Number(form.principal);
    const fee = form.processingFee === "" ? 0 : Number(form.processingFee);
    const ratePercent = form.ratePercent === "" ? 0 : Number(form.ratePercent);
    const tenure = Number(form.tenureMonths);
    if (!form.startDate) {
      toast.error("A first installment date is required");
      return;
    }
    if (!Number.isFinite(principal) || principal <= 0) {
      toast.error("Principal must be positive");
      return;
    }
    if (!Number.isFinite(fee) || fee < 0) {
      toast.error("Processing fee must not be negative");
      return;
    }
    if (form.disbursalDate && form.disbursalDate >= form.startDate) {
      toast.error("Disbursal date must be before the first installment date");
      return;
    }
    if (!Number.isFinite(ratePercent) || ratePercent < 0) {
      toast.error("Interest rate must not be negative");
      return;
    }
    if (!Number.isInteger(tenure) || tenure < 1) {
      toast.error("Tenure must be at least one month");
      return;
    }

    setSaving(true);
    try {
      const res = await api.saveLoanSchedule(account.id, {
        principal,
        processingFee: fee,
        disbursalDate: form.disbursalDate,
        annualRateBps: Math.round(ratePercent * 100),
        tenureMonths: tenure,
        startDate: form.startDate,
      });
      setDetail(res);
      setForm(toForm(res));
      setEditing(false);
      toast.success("Loan schedule saved");
    } catch (err) {
      toast.error((err as Error).message || "Failed to save the loan schedule");
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async () => {
    setSaving(true);
    try {
      await api.deleteLoanSchedule(account.id);
      setDetail({ ...(detail as LoanScheduleDetail), schedule: null, entries: [] });
      setForm(EMPTY_FORM);
      setEditing(true);
      toast.success("Loan schedule removed");
    } catch (err) {
      toast.error((err as Error).message || "Failed to remove the loan schedule");
    } finally {
      setSaving(false);
    }
  };

  const handleUndoTransfer = async (transferId: string) => {
    setSaving(true);
    try {
      await api.deleteLoanTransfer(account.id, transferId);
      toast.success("Balance transfer undone");
      await load();
    } catch (err) {
      toast.error((err as Error).message || "Failed to undo the balance transfer");
    } finally {
      setSaving(false);
    }
  };

  const handleLinkCredit = async (transactionId: string) => {
    setSaving(true);
    try {
      const res = await api.linkLoanDisbursement(account.id, { transactionId });
      setDetail(res);
      setForm(toForm(res));
      toast.success("Disbursement credit linked");
    } catch (err) {
      toast.error((err as Error).message || "Failed to link the disbursement credit");
    } finally {
      setSaving(false);
    }
  };

  const handleUnlinkCredit = async () => {
    setSaving(true);
    try {
      await api.unlinkLoanDisbursement(account.id);
      toast.success("Disbursement credit unlinked");
      await load();
    } catch (err) {
      toast.error((err as Error).message || "Failed to unlink the disbursement credit");
    } finally {
      setSaving(false);
    }
  };

  // Detaching an EMI payment from the loan. Payments cover installments in
  // order, so removing one shifts every later installment's match up by one —
  // that is the existing semantics, which the reload below makes visible.
  const handleUnlinkPayment = async (transactionId: string) => {
    setSaving(true);
    try {
      await api.bulkLoan({
        transactionIds: [transactionId],
        loanAccountId: null,
      });
      toast.success("Payment unlinked from this loan");
      await load();
    } catch (err) {
      toast.error((err as Error).message || "Failed to unlink the payment");
    } finally {
      setSaving(false);
    }
  };

  const cellPad = compactLayout ? "py-1.5 px-3" : "py-2.5 px-4";
  // The transfer that settled this loan is the last one leaving this account,
  // since transfers are returned in date order.
  const settledTransfer = detail?.settledOn
    ? detail.transfers.filter((t) => t.fromLoanAccountId === account.id).pop()
    : undefined;
  // The transfer the undo confirmation is describing, so the dialog can name
  // the amount and date it is about to revert.
  const undoTransfer = detail?.transfers.find((t) => t.id === undoTransferId);

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{account.name} — Amortization</DialogTitle>
          <DialogDescription>
            Split every EMI into principal and interest. Installments are
            matched to the EMI payments attached to this loan, in date order.
          </DialogDescription>
        </DialogHeader>

        {loading ? (
          <div className="flex justify-center py-10">
            <Spinner className="size-6 text-primary" />
          </div>
        ) : error ? (
          <div className="flex flex-col items-center gap-3 py-8">
            <div className="rounded-lg border border-destructive/30 bg-destructive/10 px-4 py-2 text-sm text-destructive">
              {error}
            </div>
            <Button variant="outline" onClick={load}>
              Retry
            </Button>
          </div>
        ) : editing || !schedule ? (
          <div className="grid grid-cols-2 gap-4">
            <div className="col-span-2 sm:col-span-1">
              <Label htmlFor="loan-principal">Principal</Label>
              <Input
                id="loan-principal"
                type="number"
                min="0"
                step="0.01"
                value={form.principal}
                onChange={(e) =>
                  setForm({ ...form, principal: e.target.value })
                }
                placeholder="1000000"
              />
            </div>
            <div className="col-span-2 sm:col-span-1">
              <Label htmlFor="loan-fee">Processing fee</Label>
              <Input
                id="loan-fee"
                type="number"
                min="0"
                step="0.01"
                value={form.processingFee}
                onChange={(e) =>
                  setForm({ ...form, processingFee: e.target.value })
                }
                placeholder="0"
              />
              <p className="mt-1 text-[11px] text-muted-foreground">
                Recorded for reference. It does not change the EMI or the
                table, which repay the principal in full.
              </p>
            </div>
            <div className="col-span-2 sm:col-span-1">
              <Label htmlFor="loan-rate">Annual interest rate (%)</Label>
              <Input
                id="loan-rate"
                type="number"
                min="0"
                step="0.01"
                value={form.ratePercent}
                onChange={(e) =>
                  setForm({ ...form, ratePercent: e.target.value })
                }
                placeholder="9.5"
              />
            </div>
            <div className="col-span-2 sm:col-span-1">
              <Label htmlFor="loan-tenure">Tenure (months)</Label>
              <Input
                id="loan-tenure"
                type="number"
                min="1"
                step="1"
                value={form.tenureMonths}
                onChange={(e) =>
                  setForm({ ...form, tenureMonths: e.target.value })
                }
                placeholder="60"
              />
            </div>
            <div className="col-span-2 sm:col-span-1">
              <Label htmlFor="loan-start">First installment date</Label>
              <Input
                id="loan-start"
                type="date"
                className="scheme-light dark:scheme-dark"
                value={form.startDate}
                onChange={(e) =>
                  setForm({ ...form, startDate: e.target.value })
                }
              />
            </div>
            <div className="col-span-2 sm:col-span-1">
              <Label htmlFor="loan-disbursal">Disbursal date</Label>
              <Input
                id="loan-disbursal"
                type="date"
                className="scheme-light dark:scheme-dark"
                value={form.disbursalDate}
                onChange={(e) =>
                  setForm({ ...form, disbursalDate: e.target.value })
                }
              />
            </div>
          </div>
        ) : (
          <div className="space-y-4">
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
              <SummaryStat label="EMI" value={formatCurrency(detail?.emi ?? 0)} />
              <SummaryStat
                label="Processing fee"
                value={formatCurrency(schedule.processingFee)}
              />
              <SummaryStat
                label="Outstanding principal"
                value={formatCurrency(detail?.outstandingPrincipal ?? 0)}
              />
              <SummaryStat
                label="Interest paid"
                value={formatCurrency(detail?.interestPaid ?? 0)}
              />
              <SummaryStat
                label="Total interest"
                value={formatCurrency(detail?.totalInterest ?? 0)}
              />
            </div>
            <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
              <span>
                {detail?.paidInstallments ?? 0} of {schedule.tenureMonths}{" "}
                installments paid
              </span>
              <span>Total payable {formatCurrency(detail?.totalPayable ?? 0)}</span>
              {detail?.settledOn ? (
                <span>
                  Settled by balance transfer on {formatDate(detail.settledOn)}
                  {settledTransfer
                    ? ` (${formatCurrency(settledTransfer.amount)})`
                    : ""}
                </span>
              ) : detail?.nextDueDate ? (
                <span>Next due {formatDate(detail.nextDueDate)}</span>
              ) : (
                <span>All installments covered</span>
              )}
            </div>

            {disbursement && (
              <div className="space-y-2">
                <div className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
                  Disbursement
                </div>
                <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
                  <SummaryStat
                    label="Sanctioned"
                    value={formatCurrency(disbursement.sanctioned)}
                  />
                  <SummaryStat
                    label="Processing fee"
                    value={formatCurrency(disbursement.processingFee)}
                  />
                  <SummaryStat
                    label="Paid out"
                    value={formatCurrency(disbursement.paidOut)}
                  />
                  <SummaryStat
                    label="Net released"
                    value={formatCurrency(disbursement.net)}
                  />
                </div>
                <div className="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-md border border-border px-3 py-2 text-xs">
                  {disbursement.creditTransactionId ? (
                    <>
                      <span className="text-muted-foreground">Bank credit</span>
                      <span className="text-sm font-medium text-foreground">
                        {formatCurrency(disbursement.creditAmount ?? 0)}
                      </span>
                      {disbursement.verified ? (
                        <Badge variant="secondary" className="text-primary">
                          <Check size={12} className="mr-1" />
                          Matched
                        </Badge>
                      ) : (
                        <span className="text-destructive">
                          Off by{" "}
                          {formatCurrency(Math.abs(disbursement.difference))}
                        </span>
                      )}
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        title="Unlink this credit"
                        aria-label="Unlink this credit"
                        onClick={() => void handleUnlinkCredit()}
                        disabled={saving}
                      >
                        <Unlink size={14} />
                      </Button>
                    </>
                  ) : (
                    <>
                      <span className="text-muted-foreground">
                        No bank credit is linked against this loan yet.
                      </span>
                      <Select
                        value=""
                        onValueChange={(v) => void handleLinkCredit(v)}
                        disabled={loadingCredits || saving}
                      >
                        <SelectTrigger
                          className="h-8 w-auto min-w-56"
                          aria-label="Link a bank credit"
                        >
                          <SelectValue
                            placeholder={
                              loadingCredits
                                ? "Loading credits…"
                                : "Link a bank credit"
                            }
                          />
                        </SelectTrigger>
                        <SelectContent>
                          {credits.map((t) => (
                            // A credit already attached to a loan is shown so
                            // the user can see the record exists, but the API
                            // rejects linking it (409), so it is not offered.
                            <SelectItem
                              key={t.id}
                              value={t.id}
                              disabled={Boolean(t.loanAccountId)}
                            >
                              {formatCurrency(t.amount)} · {formatDate(t.date)} ·{" "}
                              {t.accountName}
                              {t.loanAccountId
                                ? ` — already linked to ${t.loanAccountName ?? "another loan"}`
                                : ""}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                      {!loadingCredits && credits.length === 0 && creditRange && (
                        <span className="text-muted-foreground">
                          No credit of {formatCurrency(disbursement.net)} between{" "}
                          {formatDate(creditRange.dateFrom)} and{" "}
                          {formatDate(creditRange.dateTo)}. Import the bank
                          statement covering the disbursement, or set this
                          loan's disbursal date.
                        </span>
                      )}
                    </>
                  )}
                </div>
              </div>
            )}

            <div className="max-h-80 overflow-y-auto rounded-md border border-border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className={cellPad}>#</TableHead>
                    <TableHead className={cellPad}>Due</TableHead>
                    <TableHead className={`${cellPad} text-right`}>EMI</TableHead>
                    <TableHead className={`${cellPad} text-right`}>
                      Principal
                    </TableHead>
                    <TableHead className={`${cellPad} text-right`}>
                      Interest
                    </TableHead>
                    <TableHead className={`${cellPad} text-right`}>
                      Balance
                    </TableHead>
                    <TableHead className={cellPad} />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {detail?.entries.map((e) => {
                    // A cancelled installment was voided by a transfer, so it is
                    // dimmed and struck through — except its badges, which stay
                    // legible.
                    const dim = e.cancelled;
                    const struck = dim ? "line-through" : undefined;
                    // The payment the schedule matched to this installment, if
                    // any; only then can the match be detached.
                    const paymentId = e.paid ? e.transactionId : undefined;
                    return (
                      <TableRow
                        key={e.number}
                        className={dim ? "text-muted-foreground" : undefined}
                      >
                        <TableCell className={cn(cellPad, struck)}>
                          {e.number}
                        </TableCell>
                        <TableCell className={cn(cellPad, struck)}>
                          {formatDate(e.dueDate)}
                        </TableCell>
                        <TableCell
                          className={cn(cellPad, "text-right", struck)}
                        >
                          {formatCurrency(e.amount)}
                        </TableCell>
                        <TableCell
                          className={cn(cellPad, "text-right", struck)}
                        >
                          {formatCurrency(e.principal)}
                        </TableCell>
                        <TableCell
                          className={cn(cellPad, "text-right", struck)}
                        >
                          {formatCurrency(e.interest)}
                        </TableCell>
                        <TableCell
                          className={cn(
                            cellPad,
                            "text-right",
                            dim ? struck : "text-muted-foreground",
                          )}
                        >
                          {formatCurrency(e.balance)}
                        </TableCell>
                        <TableCell className={cellPad}>
                          <div className="flex flex-wrap items-center gap-1">
                            {e.paid && (
                              <Badge
                                variant="secondary"
                                title={
                                  e.transactionId
                                    ? `Paid by transaction ${e.transactionId}`
                                    : "Paid"
                                }
                              >
                                <Check size={12} className="mr-1" />
                                Paid
                              </Badge>
                            )}
                            {paymentId && (
                              <Button
                                variant="ghost"
                                size="icon-sm"
                                title="Unlink the payment covering this installment — payments match installments in order, so the later matches shift up"
                                aria-label="Unlink the payment covering this installment"
                                onClick={() => void handleUnlinkPayment(paymentId)}
                                disabled={saving}
                              >
                                <Unlink size={14} />
                              </Button>
                            )}
                            {e.recast && (
                              <Badge
                                variant="outline"
                                title="Regenerated by a balance transfer"
                              >
                                Recast
                              </Badge>
                            )}
                            {e.cancelled && (
                              <Badge
                                variant="outline"
                                title="Voided when a balance transfer settled this loan"
                              >
                                Settled
                              </Badge>
                            )}
                          </div>
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>

            {detail && detail.transfers.length > 0 && (
              <div className="space-y-2">
                <div className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
                  Balance transfers
                </div>
                <div className="divide-y divide-border overflow-hidden rounded-md border border-border">
                  {detail.transfers.map((t) => {
                    const outgoing = t.fromLoanAccountId === account.id;
                    const counterparty = outgoing
                      ? t.toLoanAccountName
                      : t.fromLoanAccountName;
                    return (
                      <div
                        key={t.id}
                        className="flex items-center justify-between gap-3 px-3 py-2"
                      >
                        <div className="flex min-w-0 items-center gap-2">
                          <Badge
                            variant={outgoing ? "destructive" : "secondary"}
                          >
                            {outgoing ? "Out" : "In"}
                          </Badge>
                          <span className="truncate text-sm">
                            {outgoing ? "To" : "From"}{" "}
                            {counterparty || "another loan"}
                          </span>
                        </div>
                        <div className="flex shrink-0 items-center gap-3">
                          <span className="text-sm font-medium">
                            {formatCurrency(t.amount)}
                          </span>
                          <span className="text-xs text-muted-foreground">
                            {formatCurrency(t.principal)} principal ·{" "}
                            {formatCurrency(t.accruedInterest)} accrued interest
                          </span>
                          <span className="text-xs text-muted-foreground">
                            {formatDate(t.transferDate)}
                          </span>
                          {outgoing && (
                            <Button
                              variant="ghost"
                              size="icon-sm"
                              title="Undo this balance transfer"
                              aria-label="Undo this balance transfer"
                              onClick={() => setUndoTransferId(t.id)}
                              disabled={saving}
                            >
                              <Undo2 size={14} />
                            </Button>
                          )}
                        </div>
                      </div>
                    );
                  })}
                </div>
              </div>
            )}
          </div>
        )}

        <DialogFooter className="gap-2">
          {!loading && !error && (editing ? (
            <>
              {schedule && (
                <Button
                  variant="ghost"
                  onClick={() => {
                    setForm(toForm(detail as LoanScheduleDetail));
                    setEditing(false);
                  }}
                >
                  Cancel
                </Button>
              )}
              <Button onClick={handleSave} disabled={saving}>
                {saving && <Loader2 size={16} className="animate-spin" />}
                Save schedule
              </Button>
            </>
          ) : (
            <>
              <Button
                variant="outline"
                onClick={() => setConfirmDeleteOpen(true)}
                disabled={saving}
                className="text-destructive"
              >
                <Trash2 size={14} className="mr-1" />
                Remove schedule
              </Button>
              {!detail?.settledOn && (
                <Button
                  variant="outline"
                  onClick={() => setTransferOpen(true)}
                  disabled={saving}
                >
                  <ArrowRightLeft size={14} className="mr-1" />
                  Transfer balance
                </Button>
              )}
              <Button
                variant="outline"
                onClick={() => setEditing(true)}
                disabled={saving}
              >
                <Pencil size={14} className="mr-1" />
                Edit terms
              </Button>
            </>
          ))}
        </DialogFooter>
      </DialogContent>

      {transferOpen && (
        <TransferBalanceDialog
          account={account}
          onClose={() => setTransferOpen(false)}
          onTransferred={() => {
            setTransferOpen(false);
            void load();
          }}
        />
      )}

      <AlertDialog
        open={confirmDeleteOpen}
        onOpenChange={(open) => !open && setConfirmDeleteOpen(false)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove this schedule?</AlertDialogTitle>
            <AlertDialogDescription>
              This deletes {account.name}'s amortization table with its
              installment matches. The loan account, its transactions and any
              balance-transfer history are kept. This action cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                setConfirmDeleteOpen(false);
                void handleDelete();
              }}
            >
              Remove
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog
        open={undoTransferId !== null}
        onOpenChange={(open) => !open && setUndoTransferId(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Undo this balance transfer?</AlertDialogTitle>
            <AlertDialogDescription>
              {undoTransfer
                ? `This reverts the ${formatCurrency(undoTransfer.amount)} settlement of ${formatDate(undoTransfer.transferDate)} and restores both loans' schedules to what they were. This action cannot be undone.`
                : "This reverts the settlement and restores both loans' schedules. This action cannot be undone."}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                const id = undoTransferId;
                setUndoTransferId(null);
                if (id) void handleUndoTransfer(id);
              }}
            >
              Undo transfer
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Dialog>
  );
}

interface TransferBalanceDialogProps {
  account: Account;
  onClose: () => void;
  onTransferred: () => void;
}

// TransferBalanceDialog settles this loan at its payoff on the transfer date —
// its outstanding principal plus the interest accrued since the last EMI
// payment, quoted by the API for the chosen date — and reshapes the chosen
// target loan. A target with a schedule either absorbs the amount into its
// remaining installments (recast) or pays it out of its own disbursement
// (takeover); a target with no schedule needs its terms supplied so the
// transfer can open one.
function TransferBalanceDialog({
  account,
  onClose,
  onTransferred,
}: TransferBalanceDialogProps) {
  const { accounts } = useDomainData();
  const [form, setForm] = useState<TransferForm>(() => ({
    targetId: "",
    transferDate: todayLocalISO(),
    targetRatePercent: "",
    targetTenureMonths: "",
    targetStartDate: "",
  }));
  const [targetDetail, setTargetDetail] = useState<LoanScheduleDetail | null>(
    null,
  );
  // The target whose schedule the dialog is showing, so a response for a
  // superseded one can be dropped (see handleTargetChange).
  const requestedTargetRef = useRef("");
  const [mode, setMode] = useState<LoanTransferMode>("recast");
  const [loadingTarget, setLoadingTarget] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  // The payoff quoted for the chosen transfer date. Every figure shown comes
  // from this quote — nothing about the amount is computed here — and a failed
  // quote is reported without disabling the form, since the transfer endpoint
  // runs the same computation server-side.
  const [payoff, setPayoff] = useState<LoanPayoff | null>(null);
  const [loadingPayoff, setLoadingPayoff] = useState(false);

  const targets = useMemo(
    () =>
      accounts.filter(
        (a) => a.accountTypeId === "loan" && a.id !== account.id && !a.closed,
      ),
    [accounts, account.id],
  );

  // A target without a schedule is not yet a loan being repaid, so it opens one
  // from the transferred amount and takes no mode.
  const targetNeedsTerms = targetDetail !== null && targetDetail.schedule === null;
  const targetSchedule = targetDetail?.schedule ?? null;

  // Re-quote whenever the transfer date moves, so the amount the dialog shows
  // is the one the transfer will settle for. A response for a superseded date
  // is dropped.
  useEffect(() => {
    const date = form.transferDate;
    if (!date) {
      setPayoff(null);
      return;
    }
    let cancelled = false;
    setLoadingPayoff(true);
    api
      .getLoanPayoff(account.id, date)
      .then((res) => {
        if (!cancelled) setPayoff(res);
      })
      .catch((err: Error) => {
        if (cancelled) return;
        setPayoff(null);
        toast.error(err.message || "Failed to quote the payoff for this date");
      })
      .finally(() => {
        if (!cancelled) setLoadingPayoff(false);
      });
    return () => {
      cancelled = true;
    };
  }, [account.id, form.transferDate]);

  const handleTargetChange = async (targetId: string) => {
    setForm((f) => ({ ...f, targetId }));
    setTargetDetail(null);
    setMode("recast");
    if (!targetId) return;
    // The schedule decides the mode and the terms the transfer is submitted
    // with, so a response for a target the user has already moved past must not
    // describe the current one (picking A then B quickly used to submit A's
    // mode/terms for B).
    requestedTargetRef.current = targetId;
    setLoadingTarget(true);
    try {
      const res = await api.getLoanSchedule(targetId);
      if (requestedTargetRef.current !== targetId) return;
      setTargetDetail(res);
    } catch (err) {
      if (requestedTargetRef.current !== targetId) return;
      toast.error(
        (err as Error).message || "Failed to load the target loan schedule",
      );
    } finally {
      if (requestedTargetRef.current === targetId) setLoadingTarget(false);
    }
  };

  const handleSubmit = async () => {
    const { targetId, transferDate } = form;
    if (!targetId) {
      toast.error("Select a target loan account");
      return;
    }
    if (!transferDate) {
      toast.error("A transfer date is required");
      return;
    }

    const payload: LoanTransferRequest = {
      toLoanAccountId: targetId,
      transferDate,
    };
    if (targetNeedsTerms) {
      const ratePercent =
        form.targetRatePercent === "" ? 0 : Number(form.targetRatePercent);
      const tenure = Number(form.targetTenureMonths);
      if (!Number.isFinite(ratePercent) || ratePercent < 0) {
        toast.error("Target interest rate must not be negative");
        return;
      }
      if (!Number.isInteger(tenure) || tenure < 1) {
        toast.error("Target tenure must be at least one month");
        return;
      }
      if (!form.targetStartDate) {
        toast.error("A target first installment date is required");
        return;
      }
      payload.targetAnnualRateBps = Math.round(ratePercent * 100);
      payload.targetTenureMonths = tenure;
      payload.targetStartDate = form.targetStartDate;
    } else if (targetSchedule) {
      payload.mode = mode;
    }

    setSubmitting(true);
    try {
      await api.transferLoanBalance(account.id, payload);
      toast.success(
        targetNeedsTerms
          ? "Balance transferred; the target's schedule was opened"
          : mode === "takeover"
            ? "Balance settled out of the target's disbursement"
            : "Balance absorbed by the target's remaining installments",
      );
      onTransferred();
    } catch (err) {
      toast.error((err as Error).message || "Failed to transfer the balance");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Transfer balance</DialogTitle>
          <DialogDescription>
            Settle {account.name} at its payoff on the transfer date — its
            outstanding principal plus the interest accrued since its last EMI
            payment — and reshape the target loan to take it on.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="transfer-target">Target loan account</Label>
            <Select
              value={form.targetId}
              onValueChange={(v) => void handleTargetChange(v)}
            >
              <SelectTrigger id="transfer-target" className="w-full">
                <SelectValue placeholder="Select a loan account" />
              </SelectTrigger>
              <SelectContent>
                {targets.map((a) => (
                  <SelectItem key={a.id} value={a.id}>
                    {a.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {targets.length === 0 && (
              <p className="text-xs text-muted-foreground">
                No other open loan account to transfer to.
              </p>
            )}
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="transfer-date">Transfer date</Label>
            <Input
              id="transfer-date"
              type="date"
              className="scheme-light dark:scheme-dark"
              value={form.transferDate}
              onChange={(e) =>
                setForm({ ...form, transferDate: e.target.value })
              }
            />
          </div>

          <div className="space-y-2">
            <div className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
              Payoff on the transfer date
            </div>
            {loadingPayoff ? (
              <div className="flex items-center gap-2 rounded-lg border border-border px-3 py-2 text-sm text-muted-foreground">
                <Spinner className="size-4 text-primary" />
                Quoting the payoff…
              </div>
            ) : payoff ? (
              <div className="space-y-1.5">
                <div className="grid grid-cols-3 gap-3">
                  <SummaryStat
                    label="Principal"
                    value={formatCurrency(payoff.outstandingPrincipal)}
                  />
                  <SummaryStat
                    label="Accrued interest"
                    value={formatCurrency(payoff.accruedInterest)}
                  />
                  <SummaryStat
                    label="Payoff"
                    value={formatCurrency(payoff.payoff)}
                  />
                </div>
                <p className="text-[11px] text-muted-foreground">
                  {formatCurrency(payoff.payoff)} moves: the principal plus{" "}
                  {formatCurrency(payoff.accruedInterest)} of interest accrued
                  over {payoff.days} {payoff.days === 1 ? "day" : "days"} from{" "}
                  {formatDate(payoff.fromDate)} to {formatDate(payoff.asOf)}.
                </p>
              </div>
            ) : form.transferDate ? (
              <div className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
                No payoff quote for this date. The transfer still settles this
                loan at its payoff on the transfer date.
              </div>
            ) : (
              <p className="text-xs text-muted-foreground">
                Pick a transfer date to quote the payoff.
              </p>
            )}
          </div>

          {targetSchedule && (
            <div className="space-y-1.5">
              <Label htmlFor="transfer-mode">Transfer mode</Label>
              <Select
                value={mode}
                onValueChange={(v) => setMode(v as LoanTransferMode)}
              >
                <SelectTrigger
                  id="transfer-mode"
                  aria-label="Transfer mode"
                  className="w-full"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="recast">
                    Absorb the balance (recast)
                  </SelectItem>
                  <SelectItem value="takeover">
                    Take over (settle it out of the target's disbursement)
                  </SelectItem>
                </SelectContent>
              </Select>
              {mode === "takeover" && targetDetail?.disbursement && (
                <div className="grid grid-cols-2 gap-3 pt-1">
                  <SummaryStat
                    label="Target net disbursement"
                    value={formatCurrency(targetDetail.disbursement.net)}
                  />
                  {payoff && (
                    <SummaryStat
                      label="Left after takeover"
                      value={formatCurrency(
                        targetDetail.disbursement.net - payoff.payoff,
                      )}
                    />
                  )}
                </div>
              )}
            </div>
          )}

          {targetNeedsTerms && (
            <div className="grid grid-cols-2 gap-4">
              <div className="col-span-2 sm:col-span-1">
                <Label htmlFor="target-rate">Annual interest rate (%)</Label>
                <Input
                  id="target-rate"
                  type="number"
                  min="0"
                  step="0.01"
                  value={form.targetRatePercent}
                  onChange={(e) =>
                    setForm({ ...form, targetRatePercent: e.target.value })
                  }
                  placeholder="9.5"
                />
              </div>
              <div className="col-span-2 sm:col-span-1">
                <Label htmlFor="target-tenure">Tenure (months)</Label>
                <Input
                  id="target-tenure"
                  type="number"
                  min="1"
                  step="1"
                  value={form.targetTenureMonths}
                  onChange={(e) =>
                    setForm({ ...form, targetTenureMonths: e.target.value })
                  }
                  placeholder="60"
                />
              </div>
              <div className="col-span-2">
                <Label htmlFor="target-start">First installment date</Label>
                <Input
                  id="target-start"
                  type="date"
                  className="scheme-light dark:scheme-dark"
                  value={form.targetStartDate}
                  onChange={(e) =>
                    setForm({ ...form, targetStartDate: e.target.value })
                  }
                />
              </div>
            </div>
          )}
        </div>

        <DialogFooter className="gap-2">
          <Button variant="ghost" onClick={onClose} disabled={submitting}>
            Cancel
          </Button>
          <Button
            onClick={handleSubmit}
            disabled={submitting || loadingTarget}
          >
            {submitting && <Loader2 size={16} className="animate-spin" />}
            Confirm transfer
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function SummaryStat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border border-border px-3 py-2">
      <div className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
        {label}
      </div>
      <div className="text-sm font-semibold text-foreground">{value}</div>
    </div>
  );
}
