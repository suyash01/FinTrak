import { useCallback, useEffect, useMemo, useState } from "react";
import {
  ArrowRightLeft,
  Check,
  Loader2,
  Pencil,
  Trash2,
  Undo2,
} from "lucide-react";
import { toast } from "sonner";
import api from "../../api/client";
import { formatCurrency, formatDate } from "../../utils/formatters";
import { useDomainData } from "../../context/DomainDataContext";
import { useSettings } from "../../context/SettingsContext";
import type {
  Account,
  LoanScheduleDetail,
  LoanTransferRequest,
} from "../../types";
import { cn } from "@/lib/utils";
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

function todayIso(): string {
  return new Date().toISOString().slice(0, 10);
}

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
  const [detail, setDetail] = useState<LoanScheduleDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState(false);
  const [form, setForm] = useState<ScheduleForm>(EMPTY_FORM);
  const [saving, setSaving] = useState(false);
  const [transferOpen, setTransferOpen] = useState(false);

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

  const schedule = detail?.schedule ?? null;
  const cellPad = compactLayout ? "py-1.5 px-3" : "py-2.5 px-4";
  // The transfer that settled this loan is the last one leaving this account,
  // since transfers are returned in date order.
  const settledTransfer = detail?.settledOn
    ? detail.transfers.filter((t) => t.fromLoanAccountId === account.id).pop()
    : undefined;

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
                            {formatDate(t.transferDate)}
                          </span>
                          {outgoing && (
                            <Button
                              variant="ghost"
                              size="icon-sm"
                              title="Undo this balance transfer"
                              aria-label="Undo this balance transfer"
                              onClick={() => void handleUndoTransfer(t.id)}
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
                onClick={handleDelete}
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
          outstandingPrincipal={detail?.outstandingPrincipal ?? 0}
          onClose={() => setTransferOpen(false)}
          onTransferred={() => {
            setTransferOpen(false);
            void load();
          }}
        />
      )}
    </Dialog>
  );
}

interface TransferBalanceDialogProps {
  account: Account;
  outstandingPrincipal: number;
  onClose: () => void;
  onTransferred: () => void;
}

// TransferBalanceDialog settles this loan at its outstanding principal on the
// transfer date and recasts the chosen target loan's remaining installments to
// absorb it. A target with no schedule of its own needs its terms supplied.
function TransferBalanceDialog({
  account,
  outstandingPrincipal,
  onClose,
  onTransferred,
}: TransferBalanceDialogProps) {
  const { accounts } = useDomainData();
  const [form, setForm] = useState<TransferForm>(() => ({
    targetId: "",
    transferDate: todayIso(),
    targetRatePercent: "",
    targetTenureMonths: "",
    targetStartDate: "",
  }));
  const [targetNeedsTerms, setTargetNeedsTerms] = useState(false);
  const [loadingTarget, setLoadingTarget] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  const targets = useMemo(
    () =>
      accounts.filter(
        (a) => a.accountTypeId === "loan" && a.id !== account.id && !a.closed,
      ),
    [accounts, account.id],
  );

  const handleTargetChange = async (targetId: string) => {
    setForm((f) => ({ ...f, targetId }));
    setTargetNeedsTerms(false);
    if (!targetId) return;
    setLoadingTarget(true);
    try {
      const res = await api.getLoanSchedule(targetId);
      setTargetNeedsTerms(res.schedule === null);
    } catch (err) {
      toast.error(
        (err as Error).message || "Failed to load the target loan schedule",
      );
    } finally {
      setLoadingTarget(false);
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
    }

    setSubmitting(true);
    try {
      await api.transferLoanBalance(account.id, payload);
      toast.success("Balance transferred; both loans updated");
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
            Settle {account.name} at its outstanding principal and recast the
            target loan's remaining installments to absorb it.
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

          <SummaryStat
            label="Amount to transfer"
            value={formatCurrency(outstandingPrincipal)}
          />

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
