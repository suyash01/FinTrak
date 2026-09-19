import { useCallback, useEffect, useState } from "react";
import { Check, Loader2, Pencil, Trash2 } from "lucide-react";
import { toast } from "sonner";
import api from "../../api/client";
import { formatCurrency, formatDate } from "../../utils/formatters";
import { useSettings } from "../../context/SettingsContext";
import type { Account, LoanScheduleDetail } from "../../types";
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
  ratePercent: string;
  tenureMonths: string;
  startDate: string;
}

const EMPTY_FORM: ScheduleForm = {
  principal: "",
  ratePercent: "",
  tenureMonths: "",
  startDate: "",
};

function toForm(detail: LoanScheduleDetail): ScheduleForm {
  const s = detail.schedule;
  if (!s) return EMPTY_FORM;
  return {
    principal: String(s.principal),
    ratePercent: String(s.annualRateBps / 100),
    tenureMonths: String(s.tenureMonths),
    startDate: s.startDate.slice(0, 10),
  };
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

  const schedule = detail?.schedule ?? null;
  const cellPad = compactLayout ? "py-1.5 px-3" : "py-2.5 px-4";

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
          </div>
        ) : (
          <div className="space-y-4">
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
              <SummaryStat label="EMI" value={formatCurrency(detail?.emi ?? 0)} />
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
              {detail?.nextDueDate ? (
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
                  {detail?.entries.map((e) => (
                    <TableRow key={e.number}>
                      <TableCell className={cellPad}>{e.number}</TableCell>
                      <TableCell className={cellPad}>
                        {formatDate(e.dueDate)}
                      </TableCell>
                      <TableCell className={`${cellPad} text-right`}>
                        {formatCurrency(e.amount)}
                      </TableCell>
                      <TableCell className={`${cellPad} text-right`}>
                        {formatCurrency(e.principal)}
                      </TableCell>
                      <TableCell className={`${cellPad} text-right`}>
                        {formatCurrency(e.interest)}
                      </TableCell>
                      <TableCell
                        className={`${cellPad} text-right text-muted-foreground`}
                      >
                        {formatCurrency(e.balance)}
                      </TableCell>
                      <TableCell className={cellPad}>
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
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
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
