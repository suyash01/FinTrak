const currencyFormatters = new Map<string, Intl.NumberFormat>();

/**
 * formatCurrency renders an amount in a named currency.
 *
 * The currency is REQUIRED and has no default. It used to default to "INR",
 * which is the root of #49: sixty-odd call sites across five components invoked
 * this without a currency and silently got rupees, so a USD transaction wore a
 * rupee symbol. Every one of them has been migrated, and removing the default is
 * what stops the next one from being written — `tsc` now fails at any call site
 * that forgets, which is the whole point of the migration being finished rather
 * than merely started.
 *
 * A caller whose code is one the user typed should reach for formatOne in
 * lib/currency.ts instead: this throws a RangeError on a code Intl cannot
 * resolve, and an account's `currency` is VARCHAR(3) that nothing validates.
 */
export function formatCurrency(amount: number, currency: string): string {
  let nf = currencyFormatters.get(currency);
  if (!nf) {
    nf = new Intl.NumberFormat("en-IN", {
      style: "currency",
      currency,
      minimumFractionDigits: 2,
    });
    currencyFormatters.set(currency, nf);
  }
  return nf.format(amount);
}

let numberFormatter: Intl.NumberFormat | null = null;

/**
 * formatNumber renders an amount with thousands separators and no currency, for
 * the places that must not name one: a code Intl cannot resolve, and an account
 * whose currency is NULL.
 *
 * It lives beside formatCurrency and mirrors its locale and its two-decimal
 * choice so the two cannot drift into printing the same amount two ways. It
 * takes no currency at all, so unlike formatCurrency it cannot throw.
 */
export function formatNumber(amount: number): string {
  numberFormatter ??= new Intl.NumberFormat("en-IN", {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  });
  return numberFormatter.format(amount);
}

export function parseDateOnly(dateStr: string | null | undefined): Date | null {
  if (!dateStr) return null;
  const m = String(dateStr).match(/^(\d{4})-(\d{2})-(\d{2})/);
  if (!m) return null;
  return new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
}

export function formatDateOnly(date: Date | string | null | undefined): string {
  if (!(date instanceof Date) || Number.isNaN(date.getTime())) return "";
  const y = date.getFullYear();
  const m = String(date.getMonth() + 1).padStart(2, "0");
  const d = String(date.getDate()).padStart(2, "0");
  return `${y}-${m}-${d}`;
}

const dateFormatter = new Intl.DateTimeFormat("en-IN", {
  day: "2-digit",
  month: "short",
  year: "numeric",
});

const dateShortFormatter = new Intl.DateTimeFormat("en-IN", {
  day: "2-digit",
  month: "short",
});

export function formatDate(dateStr: string | null | undefined): string {
  const d = parseDateOnly(dateStr);
  if (!d) return "";
  return dateFormatter.format(d);
}

export function formatDateShort(dateStr: string | null | undefined): string {
  const d = parseDateOnly(dateStr);
  if (!d) return "";
  return dateShortFormatter.format(d);
}
