import { formatScopedMulti, sumPerCurrency } from "@/lib/currency";
import type { CurrencyAmounts, ScopedAccount } from "@/types";

/**
 * MultiCurrencyNotice names the money a report is not showing.
 *
 * It is not a warning about a mistake: the API refused to total these currencies
 * on purpose. It exists so the report can say what it left out and where it is,
 * which is the difference between "my USD account is not counted" and "it is,
 * here is the number, switch to see it".
 *
 * Both sides are reported. Income alone is not enough: an account that only ever
 * spends has no income key at all, so an income-only notice would announce the
 * account and then say it holds nothing — on a page whose "Money Out" figure has
 * already left that money out.
 *
 * Renders nothing for a single currency — the common case must not pay for the
 * edge case with a permanent line of text.
 */
export default function MultiCurrencyNotice({
  accounts,
  selected,
}: {
  accounts: ScopedAccount[];
  selected: string;
}) {
  // Filtered here as well as by the caller, so the component cannot be handed
  // the whole scope and then name the currency that is already on screen.
  const others = accounts.filter((account) => account.currency !== selected);
  if (others.length === 0) return null;

  const byCurrency = new Map<string, ScopedAccount[]>();
  for (const account of others) {
    const list = byCurrency.get(account.currency) ?? [];
    list.push(account);
    byCurrency.set(account.currency, list);
  }

  return (
    <div className="mb-4 px-4 py-2 bg-muted border border-border rounded-lg text-[13px] text-muted-foreground">
      <span className="font-medium text-foreground">
        {byCurrency.size === 1
          ? "1 other currency"
          : `${byCurrency.size} other currencies`}
      </span>{" "}
      in {others.length} {others.length === 1 ? "account" : "accounts"} not
      shown here —{" "}
      {[...byCurrency.entries()]
        .sort(([a], [b]) => a.localeCompare(b))
        .map(([, list]) => {
          // Only ever a sum within one currency, and rendered through the
          // refusal rather than by hand, so a total that somehow held two
          // currencies would say so instead of being printed as one figure.
          const income = sumPerCurrency(...list.map((a) => a.income));
          const expense = sumPerCurrency(...list.map((a) => a.expense));
          return `${list.map((a) => a.name).join(", ")}: in ${side(
            income,
          )}, out ${side(expense)}`;
        })
        .join("; ")}
      . These are not added to the figures above.
    </div>
  );
}

/**
 * side renders one half of a currency's total, or "nothing" when that half has
 * no key at all. It is deliberately not formatScopedMulti on its own: that is the
 * context-free refusal for a figure with nothing to total, and this notice knows
 * which half it is describing — so an account that only spends says "in nothing,
 * out USD $906.00" here instead of the whole line claiming nothing is there.
 */
function side(amounts: CurrencyAmounts): string {
  if (Object.keys(amounts).length === 0) return "nothing";
  return formatScopedMulti(amounts);
}
