import { useCallback, useEffect, useMemo, useState } from "react";
import { formatCurrency } from "@/utils/formatters";
import type { CurrencyAmounts, CurrencyScope, ScopedAccount } from "@/types";

/**
 * formatOne renders a single currency's amount, and is the only place in the
 * app that calls formatCurrency with a code it did not choose itself.
 *
 * Intl.NumberFormat throws a RangeError on a code it cannot resolve, and an
 * account's currency is three letters a user typed — so "XYZ" is reachable, and
 * so is the empty string accounts.currency can hold when it is NULL. A display
 * detail must not take the dashboard down, so an unusable code falls back to a
 * plain grouped number prefixed with the raw code, which is still honest.
 */
export function formatOne(amount: number, code: string): string {
  if (!code) {
    return groupDigits(amount);
  }
  // The code travels with the figure even when a currency picker already names
  // it: reading a figure in the wrong currency is the whole failure this guards
  // against, and a symbol alone does not say which one — several currencies
  // share the dollar sign between them.
  try {
    return `${code} ${formatCurrency(amount, code)}`;
  } catch {
    // Intl cannot resolve this code, so there is no symbol to print. The code is
    // still the honest label and a grouped number is still the amount.
    return `${code} ${groupDigits(amount)}`;
  }
}

/** groupDigits renders a number with thousands separators and no currency. */
function groupDigits(amount: number): string {
  return new Intl.NumberFormat("en-IN", {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(amount);
}

/**
 * formatScoped renders one currency's share of an amount, or `fallback` when the
 * map holds no entry for that currency at all.
 *
 * A missing key is a zero, not an error and not absent data — an account that
 * only ever spends has no income key and the dashboard still has to draw — so
 * the default fallback is a plain zero, and it deliberately carries no currency
 * mark: there is no figure of that currency here, and printing one with a symbol
 * beside it would claim otherwise. A caller that has text for that case (the
 * notice's "no transactions") passes it as `fallback`.
 */
export function formatScoped(
  amounts: CurrencyAmounts | undefined,
  code: string,
  fallback = "0.00",
): string {
  if (!amounts || !(code in amounts)) return fallback;
  return formatOne(amounts[code], code);
}

/**
 * formatScopedMulti names every currency in a map and refuses to total them.
 * This is the notice's line, so it must read as a refusal rather than as a
 * figure: the digits of the two currencies must never appear adjacent.
 */
export function formatScopedMulti(
  amounts: CurrencyAmounts | undefined,
): string {
  const codes = Object.keys(amounts ?? {}).sort();
  if (codes.length === 0) return "no transactions";
  if (codes.length === 1) return formatOne(amounts![codes[0]], codes[0]);
  return `${codes.length} currencies: ${codes
    .map((code) => formatOne(amounts![code], code))
    .join(", ")} — not combined`;
}

/**
 * sumPerCurrency folds several per-currency maps into one, adding only within a
 * currency: a key a contribution does not carry counts as zero and no key is
 * invented, so the result holds exactly the currencies its operands held. It
 * exists for the notice, which reports money the report above it is not
 * showing, and is the one addition this app may make — the same one the server
 * makes folding SQL rows. A result of two keys is not a total and must not be
 * rendered as one.
 */
export function sumPerCurrency(
  ...maps: (CurrencyAmounts | undefined)[]
): CurrencyAmounts {
  const out: CurrencyAmounts = {};
  for (const amounts of maps) {
    for (const [code, value] of Object.entries(amounts ?? {})) {
      out[code] = (out[code] ?? 0) + value;
    }
  }
  return out;
}

/**
 * CurrencySign is which way a figure points, as the three answers a report can
 * support.
 *
 * "none" is the whole reason the type exists. A net negative in one currency
 * and positive in another has no sign, and a two-valued answer cannot say so:
 * the caller reads its false as either "positive" or "unknown", picks
 * "positive", and a day that spent dollars and earned rupees is drawn as a
 * green surplus.
 */
export type CurrencySign = "negative" | "positive" | "none";

/**
 * signClass is the token a sign is drawn in, and what a figure with no sign
 * falls back to. It is one table so the callers cannot each pick their own
 * treatment of "none", which is the mistake this type was made to stop.
 */
export const signClass: Record<CurrencySign, string> = {
  negative: "text-destructive",
  positive: "text-chart-3",
  // Deliberately muted rather than positive: the report does not know which way
  // this figure points, and colouring it as though it did is the claim the
  // payload refused to make.
  none: "text-muted-foreground",
};

/**
 * signOf reports which way a figure points: the selected currency's own sign,
 * or — with nothing selected — a sign only when the map agrees with itself.
 *
 * A currency on screen that the map never held is "none" whatever the other
 * entries say. That is the case that matters: a category spent only in dollars,
 * or a month with no domestic activity, is a real row with no figure in the
 * currency being looked at, and reading its projected zero as a surplus is
 * exactly the claim this change exists to remove.
 *
 * With nothing selected the map has to speak for itself, and it speaks by the
 * rule the type already states: negative only when every currency it covers is,
 * positive only when every one is, and mixed (or holding a zero) is neither.
 */
export function signOf(
  amounts: CurrencyAmounts | undefined,
  code: string,
): CurrencySign {
  if (!amounts) return "none";
  if (code) {
    if (!(code in amounts)) return "none";
    const value = amounts[code];
    if (value < 0) return "negative";
    return value > 0 ? "positive" : "none";
  }
  const values = Object.values(amounts);
  if (values.length === 0) return "none";
  if (values.every((value) => value < 0)) return "negative";
  if (values.every((value) => value > 0)) return "positive";
  return "none";
}

/**
 * ScopedAmount reads one currency's share of a per-currency amount as a plain
 * number, for the paths a chart or a bar width cannot do without one. Text goes
 * through formatScoped instead, which names the currency rather than dropping
 * it.
 */
export type ScopedAmount = (amounts: CurrencyAmounts | undefined) => number;

export interface CurrencyScopeSelection {
  /** The currency on screen, or "" before any scope has loaded. */
  code: string;
  /** Every currency the response covers, sorted. Empty before it loads. */
  codes: string[];
  setCode: (code: string) => void;
  /** The selected currency's share of an amount, as a number for a chart. */
  scoped: ScopedAmount;
  /** Every account in a currency the user is not currently looking at. */
  others: ScopedAccount[];
}

/**
 * useCurrencyScope owns which currency a report is showing.
 *
 * The selection is deliberately NOT sent to the API: the response always carries
 * every currency in the window, so switching is instant and switching back costs
 * nothing. That also means the report is never silently narrowed — the other
 * currencies are in the payload and `others` names them.
 *
 * The default is the first code, which is sorted, so the choice is stable rather
 * than depending on object key order.
 */
export function useCurrencyScope(
  scope: CurrencyScope | undefined,
): CurrencyScopeSelection {
  const codes = useMemo(() => [...(scope?.currencies ?? [])].sort(), [scope]);
  const [chosen, setChosen] = useState("");

  // A response that no longer holds the chosen currency must not leave the
  // screen on a currency it does not have.
  useEffect(() => {
    setChosen((current) => (current && codes.includes(current) ? current : ""));
  }, [codes]);

  const code = chosen || codes[0] || "";

  const scoped = useCallback<ScopedAmount>(
    (amounts) => (amounts ? (amounts[code] ?? 0) : 0),
    [code],
  );

  const others = useMemo(
    () => (scope?.accounts ?? []).filter((account) => account.currency !== code),
    [scope, code],
  );

  return { code, codes, setCode: setChosen, scoped, others };
}
