import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Plus,
  Repeat,
  Trash2,
  Edit2,
  CalendarClock,
  AlertCircle,
} from "lucide-react";
import api from "../../api/client";
import { useDomainData } from "../../context/DomainDataContext";
import type { RecurringSeries } from "../../types";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Spinner } from "@/components/ui/spinner";
import { Card } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
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
import RecurringFormDialog from "./RecurringFormDialog";
import RecurringDetail from "./RecurringDetail";
import { useCommandIntent } from "../../lib/useCommandIntent";
import { formatDate } from "../../utils/formatters";
import { formatOne, useAccountCurrency } from "../../lib/currency";
import {
  foldRecurringMonthly,
  recurringNetClass,
  recurringTotalText,
} from "../../lib/recurringTotals";
import { toast } from "sonner";

function cadenceLabel(s: RecurringSeries): string {
  const nouns: Record<string, [string, string]> = {
    daily: ["day", "days"],
    weekly: ["week", "weeks"],
    monthly: ["month", "months"],
    yearly: ["year", "years"],
  };
  const [one, many] = nouns[s.frequency] ?? ["period", "periods"];
  return s.interval === 1 ? `Every ${one}` : `Every ${s.interval} ${many}`;
}

// Recurring tracks user-defined repeating charges/income. The page forecasts
// each series and surfaces candidate transactions, but never creates or links
// anything on its own — the detail drawer performs explicit user-confirmed
// links.
export default function Recurring() {
  const { accounts, categories, payees } = useDomainData();
  // The series amount is denominated in the series' own account's currency, and
  // the table spans every account, so a row can be in a currency the page is not
  // currently showing elsewhere. The card's folded total already refuses to add
  // across currencies (see foldRecurringMonthly); this is the per-row figure
  // that has to agree with it.
  const currencyOf = useAccountCurrency(accounts);
  const [series, setSeries] = useState<RecurringSeries[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<RecurringSeries | null>(null);
  const [detail, setDetail] = useState<RecurringSeries | null>(null);

  const load = useCallback(async () => {
    try {
      const res = await api.getRecurringSeries();
      setSeries(res.data || []);
      setError(null);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  // A series is denominated by the account it bills against, and the page spans
  // every account the user has — so the fold is per currency, and the three
  // figures below render the refusal when it holds more than one rather than a
  // total of rupees and dollars.
  const totals = useMemo(
    () => foldRecurringMonthly(series, accounts),
    [series, accounts],
  );

  const handleDelete = async (id: string) => {
    try {
      await api.deleteRecurringSeries(id);
      toast.success("Recurring series deleted");
      await load();
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const openCreate = () => {
    setEditing(null);
    setFormOpen(true);
  };

  const openEdit = (s: RecurringSeries) => {
    setEditing(s);
    setFormOpen(true);
  };

  useCommandIntent("new-recurring", openCreate);

  const headerBase = "text-xs font-semibold uppercase tracking-wider text-muted-foreground whitespace-nowrap";

  return (
    <div className="flex flex-col h-full">
      <div className="shrink-0 px-8 pt-6">
        <h1 className="text-2xl font-bold text-foreground mb-1 flex items-center gap-2">
          <Repeat className="text-primary" />
          Recurring & Subscriptions
        </h1>
        <p className="text-muted-foreground text-sm">
          Track repeating charges and income. FinTrak forecasts them and suggests
          matching transactions — you confirm every link.
        </p>
      </div>

      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto w-full">
        {series.length > 0 && (
          <div className="grid gap-4 sm:grid-cols-3 mb-6">
            <Card className="p-4">
              <p className="text-xs uppercase tracking-wider text-muted-foreground">
                Monthly expenses
              </p>
              <p className="text-xl font-semibold text-foreground mt-1">
                {recurringTotalText(totals.expense, totals.unplaced)}
              </p>
            </Card>
            <Card className="p-4">
              <p className="text-xs uppercase tracking-wider text-muted-foreground">
                Monthly income
              </p>
              <p className="text-xl font-semibold text-foreground mt-1">
                {recurringTotalText(totals.income, totals.unplaced)}
              </p>
            </Card>
            <Card className="p-4">
              <p className="text-xs uppercase tracking-wider text-muted-foreground">
                Net per month
              </p>
              <p
                className={`text-xl font-semibold mt-1 ${recurringNetClass(
                  totals.net,
                )}`}
              >
                {recurringTotalText(totals.net, totals.unplaced)}
              </p>
            </Card>
          </div>
        )}
        {totals.unplaced.length > 0 && (
          <p className="-mt-3 mb-6 text-[13px] text-muted-foreground">
            Not counted:{" "}
            {totals.unplaced.join(", ")} — the account this series bills against
            has no readable currency.
          </p>
        )}

        <div className="flex justify-end mb-5">
          <Button
            size="lg"
            className="px-4 shrink-0"
            onClick={openCreate}
            disabled={accounts.length === 0}
          >
            <Plus />
            Add Series
          </Button>
        </div>

        {loading ? (
          <div className="flex justify-center p-20">
            <Spinner className="size-10 text-primary" />
          </div>
        ) : error ? (
          <div className="flex flex-col items-center justify-center py-20 text-center text-destructive">
            <AlertCircle className="w-10 h-10 mb-3" />
            <p className="text-sm">{error}</p>
          </div>
        ) : series.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-20 px-4 text-center">
            <Repeat className="w-16 h-16 text-muted-foreground opacity-50 mb-4" />
            <h3 className="text-lg font-semibold text-foreground mb-2">
              No recurring series yet
            </h3>
            <p className="text-muted-foreground text-sm mb-6 max-w-md">
              Add a subscription, rent, or salary to forecast it and match it
              against your transactions.
            </p>
            <Button
              size="lg"
              className="px-4"
              onClick={openCreate}
              disabled={accounts.length === 0}
            >
              Add Series
            </Button>
          </div>
        ) : (
          <div className="bg-card border border-border rounded-xl overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead className={headerBase}>Name</TableHead>
                  <TableHead className={headerBase}>Account</TableHead>
                  <TableHead className={`${headerBase} text-right`}>
                    Amount
                  </TableHead>
                  <TableHead className={headerBase}>Cadence</TableHead>
                  <TableHead className={headerBase}>Next due</TableHead>
                  <TableHead className={`${headerBase} text-right`}>
                    Linked
                  </TableHead>
                  <TableHead className={`${headerBase} text-right`}>
                    Actions
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {series.map((s) => (
                  <TableRow
                    key={s.id}
                    className="border-border cursor-pointer"
                    onClick={() => setDetail(s)}
                  >
                    <TableCell>
                      <div className="flex items-center gap-2.5">
                        {s.categoryColor && (
                          <span
                            className="w-2.5 h-2.5 rounded-full shrink-0"
                            style={{ backgroundColor: s.categoryColor }}
                          />
                        )}
                        <span className="font-medium text-foreground">
                          {s.name}
                        </span>
                        {!s.active && (
                          <Badge variant="secondary">Inactive</Badge>
                        )}
                      </div>
                    </TableCell>
                    <TableCell className="text-sm text-muted-foreground">
                      {s.accountName || "—"}
                    </TableCell>
                    <TableCell
                      className="text-sm text-right text-foreground whitespace-nowrap"
                    >
                      {formatOne(s.amount, currencyOf(s.accountId))}
                    </TableCell>
                    <TableCell className="text-sm text-muted-foreground whitespace-nowrap">
                      {cadenceLabel(s)}
                    </TableCell>
                    <TableCell className="text-sm text-muted-foreground whitespace-nowrap">
                      {s.nextDueDate ? (
                        <span className="inline-flex items-center gap-1.5">
                          <CalendarClock size={14} />
                          {formatDate(s.nextDueDate)}
                        </span>
                      ) : (
                        "—"
                      )}
                    </TableCell>
                    <TableCell className="text-sm text-right text-muted-foreground">
                      {s.attachedCount}
                    </TableCell>
                    <TableCell className="text-right">
                      <div
                        className="flex items-center justify-end gap-0.5"
                        onClick={(e) => e.stopPropagation()}
                      >
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          className="text-muted-foreground hover:text-primary hover:bg-primary/10"
                          onClick={() => openEdit(s)}
                          title="Edit series"
                          aria-label={`Edit ${s.name}`}
                        >
                          <Edit2 size={14} />
                        </Button>
                        <AlertDialog>
                          <AlertDialogTrigger asChild>
                            <Button
                              variant="ghost"
                              size="icon-sm"
                              title="Delete series"
                              aria-label={`Delete ${s.name}`}
                              className="text-muted-foreground hover:text-destructive hover:bg-destructive/10"
                            >
                              <Trash2 size={14} />
                            </Button>
                          </AlertDialogTrigger>
                          <AlertDialogContent>
                            <AlertDialogHeader>
                              <AlertDialogTitle>
                                Delete recurring series?
                              </AlertDialogTitle>
                              <AlertDialogDescription>
                                Delete "{s.name}"? Its links are removed, but the
                                transactions themselves are kept.
                              </AlertDialogDescription>
                            </AlertDialogHeader>
                            <AlertDialogFooter>
                              <AlertDialogCancel>Cancel</AlertDialogCancel>
                              <AlertDialogAction
                                variant="destructive"
                                onClick={() => handleDelete(s.id)}
                              >
                                Delete
                              </AlertDialogAction>
                            </AlertDialogFooter>
                          </AlertDialogContent>
                        </AlertDialog>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </div>

      <RecurringFormDialog
        open={formOpen}
        onOpenChange={setFormOpen}
        series={editing}
        accounts={accounts}
        categories={categories}
        payees={payees}
        onSaved={load}
      />
      <RecurringDetail
        series={detail}
        open={detail !== null}
        accounts={accounts}
        onOpenChange={(open) => {
          if (!open) setDetail(null);
        }}
        onChanged={load}
      />
    </div>
  );
}
