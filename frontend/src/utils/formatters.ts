const currencyFormatters = new Map<string, Intl.NumberFormat>();
export function formatCurrency(amount: number, currency = "INR"): string {
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
