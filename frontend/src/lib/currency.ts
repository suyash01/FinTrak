import { useCallback, useEffect, useMemo, useState } from "react";
import { formatCurrency, formatNumber } from "@/utils/formatters";
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
    return formatNumber(amount);
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
    return `${code} ${formatNumber(amount)}`;
  }
}

/**
 * formatAxis renders one chart axis tick: the currency's code and a magnitude,
 * abbreviated, with no symbol.
 *
 * A symbol is the first thing to go and the thousands abbreviation with nothing
 * to stop it — a tick is four characters of budget in a chart column, and
 * Recharts neither abbreviates nor drops the ticks that would collide, so the
 * full form clips. The code stays, because the axis is the one place on a report
 * that states its scale without naming a currency anywhere near it.
 *
 * It formats nothing through Intl, so it cannot throw on a code the user typed.
 */
export function formatAxis(amount: number, code: string): string {
  const value =
    Math.abs(amount) >= 1000 ? `${round(amount / 1000)}k` : `${round(amount)}`;
  return code ? `${code} ${value}` : value;
}

/** round trims a magnitude to one decimal, so 1.5k does not read as 1.50k. */
function round(value: number): number {
  return Math.round(value * 10) / 10;
}

/**
 * formatScoped renders one currency's share of an amount, or the refusal when
 * the map holds no entry for that currency at all.
 *
 * A missing key is not an error and not absent data, so this never blanks a
 * figure — but it is not a zero either, and rendering it as one puts a bare
 * "0.00" in a column of "INR 2,000.00" where the reader cannot tell a genuine
 * zero from money that was left out. So the default says which currency is
 * missing, the way the terminal client does. A caller that really does mean a
 * zero — nothing in this response at all, rather than nothing in this currency —
 * passes "0.00" as `fallback`.
 */
export function formatScoped(
  amounts: CurrencyAmounts | undefined,
  code: string,
  fallback = absentText(code),
): string {
  if (!amounts || !Object.hasOwn(amounts, code)) return fallback;
  return formatOne(amounts[code], code);
}

/** absentText is what a report says where a currency has no figure at all. */
function absentText(code: string): string {
  return code ? `no ${code} in this report` : "no transactions";
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
 * subPerCurrency takes one difference per currency, so a net is a subtraction
 * inside a currency and never across two. A key either operand lacks counts as
 * zero, so a currency that only ever spends still yields a well-defined negative
 * net rather than disappearing — the same union the server's own Sub returns, and
 * the same reason that one is not a subset of the other.
 *
 * It exists for the one figure the app computes rather than reads: the recurring
 * forecast's net per month, which has no server-side counterpart. Every other net
 * on a reporting screen is the payload's own field, and a caller that reaches
 * for this where one exists has rebuilt something the server already stated.
 */
export function subPerCurrency(
  minuend: CurrencyAmounts | undefined,
  subtrahend: CurrencyAmounts | undefined,
): CurrencyAmounts {
  const out: CurrencyAmounts = {};
  for (const code of new Set([
    ...Object.keys(minuend ?? {}),
    ...Object.keys(subtrahend ?? {}),
  ])) {
    out[code] = (minuend?.[code] ?? 0) - (subtrahend?.[code] ?? 0);
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
    if (!Object.hasOwn(amounts, code)) return "none";
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
  /** Every named currency the response covers, sorted. Empty before it loads. */
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
  // accounts.currency is nullable, so an empty code is representable and it
  // sorts first — which would make it the default selection and render the whole
  // report through the no-code path, as unnamed numbers. It is not a currency a
  // reader can pick, so it is not offered as one; what such an account is
  // *shown* as is the notice's problem, and it names the account by name.
  const codes = useMemo(
    () => [...(scope?.currencies ?? [])].filter((c) => c !== "").sort(),
    [scope],
  );
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
