import { formatCurrency, formatDate } from "../../utils/formatters";
import type { ParsedDocument, StatementGroup } from "../../lib/bankfiles";

interface BankFileSummaryProps {
  document: ParsedDocument;
  /** The FinTrak account every row will be imported into. */
  targetAccount: string;
}

const FORMAT_LABELS: Record<string, string> = {
  "camt.052": "ISO 20022 statement (camt.052)",
  "camt.053": "ISO 20022 transaction report (camt.053)",
  ofx: "OFX / QFX",
};

/**
 * What the bank file actually contained, shown before the import is committed.
 *
 * A bank file can describe more than one account and more than one period, and
 * FinTrak imports every one of them into the single account chosen in the
 * wizard. That is a deliberate decision, so it is stated here rather than left
 * for the user to infer from a row count afterwards: the statements the file
 * described, the balances it printed, and where the rows are about to land.
 */
export default function BankFileSummary({
  document,
  targetAccount,
}: BankFileSummaryProps) {
  if (document.groups.length === 0) return null;

  const total = document.groups.reduce((sum, g) => sum + g.rows.length, 0);
  const label = document.format ? FORMAT_LABELS[document.format] : null;
  const currency = document.groups.find((g) => g.currency)?.currency;
  const statements = `${document.groups.length} statement${document.groups.length === 1 ? "" : "s"} · ${total} transaction${total === 1 ? "" : "s"} will be imported into `;

  return (
    <div className="mb-5 p-4 bg-primary/10 border border-primary/20 rounded-lg">
      <div className="text-xs font-semibold text-primary uppercase tracking-wider mb-2">
        Bank File
        {label ? ` — ${label}` : ""}
      </div>
      <p className="text-sm text-muted-foreground mb-3">
        <span>{statements}</span>
        <span className="font-medium text-foreground">{targetAccount}</span>
        <span>.</span>
      </p>

      <div className="flex flex-col gap-2">
        {document.groups.map((group, index) => (
          <GroupRow
            key={`${group.accountNumber ?? "group"}-${index}`}
            group={group}
            fallbackCurrency={currency}
          />
        ))}
      </div>
    </div>
  );
}

function GroupRow({
  group,
  fallbackCurrency,
}: {
  group: StatementGroup;
  fallbackCurrency?: string;
}) {
  const code = group.currency || fallbackCurrency || "INR";
  const name = group.accountHolder || group.accountNumber || "Unnamed account";
  const detail = `${periodLabel(group)} · ${group.rows.length} transaction${group.rows.length === 1 ? "" : "s"}`;

  return (
    <div className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 text-sm">
      <span className="font-medium text-foreground">{name}</span>
      {group.accountHolder && group.accountNumber && (
        <span className="text-muted-foreground">{group.accountNumber}</span>
      )}
      <span className="text-muted-foreground">{detail}</span>
      {group.balances?.closing !== undefined && (
        <span className="text-muted-foreground">
          {`closing ${formatCurrency(group.balances.closing, code)}`}
        </span>
      )}
      {group.balances?.opening !== undefined && (
        <span className="text-muted-foreground">
          {`opening ${formatCurrency(group.balances.opening, code)}`}
        </span>
      )}
    </div>
  );
}

function periodLabel(group: StatementGroup): string {
  if (group.periodFrom && group.periodTo) {
    return `${formatDate(group.periodFrom)} – ${formatDate(group.periodTo)}`;
  }
  if (group.periodFrom) return `from ${formatDate(group.periodFrom)}`;
  if (group.periodTo) return `to ${formatDate(group.periodTo)}`;
  return "period not stated";
}
