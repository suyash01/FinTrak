import { formatScopedMulti, sumPerCurrency } from "@/lib/currency";
import type { ScopedAccount } from "@/types";

/**
 * MultiCurrencyNotice names the money a report is not showing.
 *
 * It is not a warning about a mistake: the API refused to total these currencies
 * on purpose. It exists so the report can say what it left out and where it is,
 * which is the difference between "my USD account is not counted" and "it is,
 * here is the number, switch to see it".
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
      shown here:{" "}
      {[...byCurrency.entries()]
        .sort(([a], [b]) => a.localeCompare(b))
        .map(([, list]) => {
          // Only ever a sum within one currency, and rendered through the
          // refusal rather than by hand, so a total that somehow held two
          // currencies would say so instead of being printed as one figure.
          const total = sumPerCurrency(
            ...list.map((account) => account.income),
          );
          return `${formatScopedMulti(total)} (${list
            .map((account) => account.name)
            .join(", ")})`;
        })
        .join("; ")}
      . These are not added to the figures above.
    </div>
  );
}
