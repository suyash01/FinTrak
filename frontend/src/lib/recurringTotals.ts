import { formatScopedMulti, signClass, signOf, subPerCurrency } from "@/lib/currency";
import type { Account, CurrencyAmounts, RecurringSeries } from "@/types";

/**
 * RecurringMonthlyTotals is a page's monthly forecast, per currency.
 *
 * Three maps and one gap, because the forecast spans every account the user has
 * and `monthlyAmount` is a bare number: an INR subscription added to a USD one
 * is a number that never existed, and printing it as money is the exact defect
 * the per-currency map was introduced to stop. The difference is therefore taken
 * one currency at a time too — income minus expense is defined inside a currency
 * and nowhere else — and `unplaced` names the series whose currency could not be
 * read at all, so the gap is stated rather than quietly closed under a default.
 */
export interface RecurringMonthlyTotals {
  income: CurrencyAmounts;
  expense: CurrencyAmounts;
  net: CurrencyAmounts;
  /** Account names holding active series whose currency could not be read. */
  unplaced: string[];
}

/**
 * foldRecurringMonthly totals a page's active series by the currency of the
 * account each one bills against.
 *
 * RecurringSeries carries the accountId rather than a currency, because a
 * transaction carries none and the account is what holds it — so the currency is
 * reached through the accounts the caller already has, not stored twice here.
 *
 * A series whose account is not in that list, or whose account has a currency
 * the API stored as null or "", is *not* placed in a bucket. It would be easy to
 * default it to "INR" — that is the projection the server's own SQL applies, so
 * the total would be arithmetically right — but a figure shown to a person is a
 * claim about one of their accounts, and "" is a value they never chose; printing
 * INR there states a fact the response does not carry. So the series is counted
 * in `unplaced` and the screen says so, which is a thing the user can act on
 * (fix the account, or the missing accounts list) where a plausible number is not.
 */
export function foldRecurringMonthly(
  series: RecurringSeries[],
  accounts: Account[],
): RecurringMonthlyTotals {
  const currencyOf = new Map(accounts.map((a) => [a.id, a.currency]));
  const income: CurrencyAmounts = {};
  const expense: CurrencyAmounts = {};
  const unplaced = new Set<string>();

  for (const s of series) {
    if (!s.active) continue;
    const code = currencyOf.get(s.accountId);
    if (!code) {
      unplaced.add(s.accountName || s.accountId);
      continue;
    }
    const target = s.type === "credit" ? income : expense;
    target[code] = (target[code] ?? 0) + s.monthlyAmount;
  }

  return {
    income,
    expense,
    net: subPerCurrency(income, expense),
    unplaced: [...unplaced],
  };
}

/**
 * recurringTotalText renders one of the three forecast figures.
 *
 * One key renders as that currency's amount with its code beside it, exactly as
 * the reporting screens render a single-currency figure; several keys render as
 * the refusal the rest of the app uses, which names each currency and adds the
 * "not combined" the digits of two currencies must never be allowed to contradict.
 *
 * A forecast that placed nothing is not "no transactions" — that would say the
 * user has no active series when the truth is that their currency is unreadable —
 * so the gap is named in the same place the figure would have been.
 */
export function recurringTotalText(
  amounts: CurrencyAmounts,
  unplaced: readonly string[],
): string {
  if (Object.keys(amounts).length === 0) {
    return unplaced.length > 0
      ? "not counted — an account's currency could not be read"
      : "no active series";
  }
  return formatScopedMulti(amounts);
}

/**
 * recurringNetClass is the token a net figure is drawn in, from its own tri-state
 * and nothing else.
 *
 * A net positive in one currency and negative in another has no sign, and the
 * class for that state is the muted one: colouring it as a surplus or a deficit
 * picks a currency to decide with, which is the claim the payload refused to
 * make. It is the same table every other per-currency figure in the app is drawn
 * from, so the two screens that show this figure cannot each end up with their
 * own answer.
 */
export function recurringNetClass(net: CurrencyAmounts): string {
  return signClass[signOf(net, "")];
}
