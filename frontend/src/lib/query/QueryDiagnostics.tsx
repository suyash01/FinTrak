import { AlertTriangle } from "lucide-react";
import type { QueryDiagnostic } from "./parse";

/**
 * The banner for terms the query language could not use.
 *
 * This is not a nicety. The parser drops an unusable term rather than failing the
 * request, because a term it refused would fail the whole search over one typo.
 * But a dropped constraint silently WIDENS the result set: `amount>>50` becomes
 * no constraint, and the user reads the entire ledger as if it were the answer.
 * This banner is the only thing telling them that happened, which is why it is a
 * persistent block rather than a toast that disappears.
 */
export function QueryDiagnosticsBanner({ diagnostics }: { diagnostics: QueryDiagnostic[] }) {
  if (diagnostics.length === 0) return null;
  return (
    <div
      role="status"
      className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm"
    >
      <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-destructive" aria-hidden />
      <div className="min-w-0">
        <p className="font-medium">
          {diagnostics.length === 1 ? "1 term was ignored" : `${diagnostics.length} terms were ignored`} — the
          results below are wider than you asked for.
        </p>
        <ul className="mt-1 space-y-0.5 text-muted-foreground">
          {diagnostics.map((d) => (
            <li key={`${d.code}:${d.term}:${d.position}`}>
              <code className="text-foreground">{d.term}</code> — {d.message}
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
