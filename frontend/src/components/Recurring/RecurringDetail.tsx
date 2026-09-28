import { useCallback, useEffect, useRef, useState } from "react";
import api from "../../api/client";
import type {
  Account,
  RecurringForecastItem,
  RecurringSeries,
  RecurringSeriesTerm,
  RecurringSuggestion,
  Transaction,
} from "../../types";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Spinner } from "@/components/ui/spinner";
import { formatDate } from "../../utils/formatters";
import { formatOne, useAccountCurrency } from "../../lib/currency";
import { useDomainData } from "../../context/DomainDataContext";
import { toast } from "sonner";

interface Props {
  series: RecurringSeries | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  accounts: Account[];
  // onChanged notifies the parent that links changed (so counts refresh).
  onChanged: () => void;
}

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

// RecurringDetail shows a series' forecast, suggested matches, linked
// transactions, and its account/amount date ranges. Linking is an explicit
// user action; the ranges themselves are edited from the series form.
export default function RecurringDetail({
  series,
  open,
  onOpenChange,
  onChanged,
}: Props) {
  const [forecast, setForecast] = useState<RecurringForecastItem[]>([]);
  const [suggestions, setSuggestions] = useState<RecurringSuggestion[]>([]);
  const [linked, setLinked] = useState<Transaction[]>([]);
  const [terms, setTerms] = useState<RecurringSeriesTerm[]>([]);
  const [loading, setLoading] = useState(false);
  const [busyId, setBusyId] = useState<string | null>(null);

  const { accounts } = useDomainData();
  const currencyOf = useAccountCurrency(accounts);
  // Two different questions, and the difference matters. The series, its
  // forecast and its term ranges are all denominated in the SERIES' account's
  // currency. The suggested and already-linked transactions are real rows in
  // whichever account they sit in, which is not necessarily the series' account -
  // a subscription can be attached across accounts - so those resolve their own.
  const seriesMoney = useCallback(
    // series is null until the dialog is opened on one, and the panel below is
    // guarded on that, so an unknown account here just means an unnamed figure.
    (amount: number) => formatOne(amount, currencyOf(series?.accountId ?? "")),
    [series?.accountId, currencyOf],
  );

  // The series the currently-displayed payloads belong to. A response for a
  // series the user has already navigated away from must not overwrite them:
  // closing series A and opening B used to leave A's forecast and suggestions
  // under B's title, and "Link" then posted A's transaction id with B's series
  // id — a real attachment on the wrong series.
  const loadedForRef = useRef<string | null>(null);

  const load = useCallback(async (s: RecurringSeries) => {
    loadedForRef.current = s.id;
    setLoading(true);
    try {
      const [f, sug, linkedRes, termsRes] = await Promise.all([
        api.getRecurringForecast(s.id),
        api.getRecurringSuggestions(s.id),
        api.getRecurringTransactions(s.id),
        api.getRecurringTerms(s.id),
      ]);
      // A newer load owns the panel now: drop this one, spinner included.
      if (loadedForRef.current !== s.id) return;
      setForecast(f.data || []);
      setSuggestions(sug.data || []);
      setLinked(linkedRes.data || []);
      setTerms(termsRes.data || []);
    } catch (err) {
      if (loadedForRef.current !== s.id) return;
      toast.error((err as Error).message);
    } finally {
      if (loadedForRef.current === s.id) setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (open && series) void load(series);
  }, [open, series, load]);

  const handleLink = async (txnId: string) => {
    if (!series) return;
    setBusyId(txnId);
    try {
      await api.attachRecurring({
        seriesId: series.id,
        transactionIds: [txnId],
      });
      toast.success("Transaction linked");
      await load(series);
      onChanged();
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setBusyId(null);
    }
  };

  const handleUnlink = async (txnId: string) => {
    if (!series) return;
    setBusyId(txnId);
    try {
      await api.detachRecurring({ transactionIds: [txnId] });
      toast.success("Transaction unlinked");
      await load(series);
      onChanged();
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setBusyId(null);
    }
  };

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="w-full sm:max-w-lg flex flex-col overflow-y-auto"
      >
        <SheetHeader>
          <SheetTitle>{series?.name ?? "Recurring series"}</SheetTitle>
          <SheetDescription>
            {series
              ? `${seriesMoney(series.amount)} · ${cadenceLabel(series)}`
              : ""}
          </SheetDescription>
        </SheetHeader>

        {loading ? (
          <div className="flex justify-center p-16">
            <Spinner className="size-8 text-primary" />
          </div>
        ) : (
          <Tabs defaultValue="forecast" className="px-4 pb-6">
            <TabsList>
              <TabsTrigger value="forecast">Forecast</TabsTrigger>
              <TabsTrigger value="suggestions">
                Suggestions ({suggestions.length})
              </TabsTrigger>
              <TabsTrigger value="linked">Linked ({linked.length})</TabsTrigger>
              <TabsTrigger value="ranges">Ranges ({terms.length})</TabsTrigger>
            </TabsList>

            <TabsContent value="forecast" className="mt-4">
              {forecast.length === 0 ? (
                <p className="text-sm text-muted-foreground">
                  No upcoming occurrences.
                </p>
              ) : (
                <div className="space-y-1.5">
                  {forecast.map((f) => (
                    <div
                      key={f.date}
                      className="flex items-center justify-between rounded-lg border border-border px-3 py-2"
                    >
                      <span className="text-sm text-foreground">
                        {formatDate(f.date)}
                      </span>
                      <span className="flex items-center gap-2 text-sm text-muted-foreground">
                        {seriesMoney(f.amount)}
                        {f.matched && (
                          <Badge className="bg-primary/10 text-primary hover:bg-primary/10">
                            Matched
                          </Badge>
                        )}
                      </span>
                    </div>
                  ))}
                </div>
              )}
            </TabsContent>

            <TabsContent value="suggestions" className="mt-4">
              {suggestions.length === 0 ? (
                <p className="text-sm text-muted-foreground">
                  No matching transactions found.
                </p>
              ) : (
                <div className="space-y-1.5">
                  {suggestions.map((s) => (
                    <div
                      key={s.txn.id}
                      className="flex items-center justify-between gap-3 rounded-lg border border-border px-3 py-2"
                    >
                      <div className="min-w-0">
                        <p className="truncate text-sm font-medium text-foreground">
                          {s.txn.description}
                        </p>
                        <p className="text-xs text-muted-foreground">
                          {formatDate(s.txn.date)} ·{" "}
                          {formatOne(s.txn.amount, currencyOf(s.txn.accountId))} · score{" "}
                          {Math.round(s.score)}
                        </p>
                      </div>
                      <Button
                        size="sm"
                        disabled={busyId === s.txn.id}
                        onClick={() => handleLink(s.txn.id)}
                      >
                        Link
                      </Button>
                    </div>
                  ))}
                </div>
              )}
            </TabsContent>

            <TabsContent value="linked" className="mt-4">
              {linked.length === 0 ? (
                <p className="text-sm text-muted-foreground">
                  No linked transactions yet.
                </p>
              ) : (
                <div className="space-y-1.5">
                  {linked.map((t) => (
                    <div
                      key={t.id}
                      className="flex items-center justify-between gap-3 rounded-lg border border-border px-3 py-2"
                    >
                      <div className="min-w-0">
                        <p className="truncate text-sm font-medium text-foreground">
                          {t.description}
                        </p>
                        <p className="text-xs text-muted-foreground">
                          {formatDate(t.date)} ·{" "}
                          {formatOne(t.amount, currencyOf(t.accountId))}
                        </p>
                      </div>
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={busyId === t.id}
                        onClick={() => handleUnlink(t.id)}
                      >
                        Unlink
                      </Button>
                    </div>
                  ))}
                </div>
              )}
            </TabsContent>

            <TabsContent value="ranges" className="mt-4">
              {terms.length === 0 ? (
                <p className="text-sm text-muted-foreground">
                  No ranges defined.
                </p>
              ) : (
                <div className="space-y-1.5">
                  {terms.map((t) => (
                    <div
                      key={t.id}
                      className="flex items-center justify-between rounded-lg border border-border px-3 py-2"
                    >
                      <div className="min-w-0">
                        <p className="text-sm font-medium text-foreground">
                          {seriesMoney(t.amount)}
                        </p>
                        <p className="text-xs text-muted-foreground">
                          {t.endDate
                            ? `${formatDate(t.startDate)} – ${formatDate(t.endDate)}`
                            : `${formatDate(t.startDate)} onward`}
                          {t.accountName ? ` · ${t.accountName}` : ""}
                        </p>
                      </div>
                    </div>
                  ))}
                </div>
              )}
              <p className="text-xs text-muted-foreground mt-3">
                Edit the account/amount ranges from the series' edit form.
              </p>
            </TabsContent>
          </Tabs>
        )}
      </SheetContent>
    </Sheet>
  );
}
