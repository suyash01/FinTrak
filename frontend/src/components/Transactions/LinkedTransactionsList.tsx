import { Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Spinner } from "@/components/ui/spinner";
import { formatCurrency, formatDate } from "../../utils/formatters";
import type { Link } from "../../types";
import { linkTypeBadgeClass } from "./linkHelpers";

interface LinkedTransactionsListProps {
  sourceTxnId: string;
  links: Link[];
  loading: boolean;
  onRequestUnlink: (linkId: string) => void;
}

// The "Linked Transactions" block at the top of the match dialog.
export default function LinkedTransactionsList({
  sourceTxnId,
  links,
  loading,
  onRequestUnlink,
}: LinkedTransactionsListProps) {
  return (
    <div className="px-6 py-3 border-b border-border bg-muted/50">
      <div className="text-[11px] font-bold text-muted-foreground uppercase tracking-wider mb-2">
        Linked Transactions ({links.length})
      </div>
      {loading ? (
        <div className="flex items-center gap-2 text-xs text-muted-foreground py-1">
          <Spinner className="h-4 w-4" />
          Loading links...
        </div>
      ) : links.length === 0 ? (
        <div className="text-[11px] text-muted-foreground italic py-1">
          No links yet. Find a match below to create one.
        </div>
      ) : (
        <div className="space-y-1.5">
          {links.map((l) => {
            const other = l.fromTxnId === sourceTxnId ? l.toTxn : l.fromTxn;
            return (
              <div
                key={l.id}
                className="flex items-center gap-2.5 bg-card border border-border rounded-lg px-3 py-2"
              >
                <Badge
                  className={`h-auto px-1.5 py-0.5 rounded text-[10px] font-bold uppercase ${linkTypeBadgeClass(l.type)}`}
                >
                  {l.type}
                </Badge>
                <div className="flex-1 min-w-0">
                  <div className="text-xs text-foreground truncate">
                    {other?.description}
                  </div>
                  <div className="text-[10px] text-muted-foreground">
                    {other?.accountName} · {formatDate(other?.date)} ·
                    <span
                      className={
                        other?.type === "debit"
                          ? "text-destructive"
                          : "text-chart-3"
                      }
                    >
                      {other?.type === "debit" ? "−" : "+"}
                      {formatCurrency(other?.amount || 0)}
                    </span>
                  </div>
                </div>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  className="text-muted-foreground hover:text-destructive hover:bg-destructive/10"
                  title="Unlink"
                  aria-label="Unlink transaction"
                  onClick={() => onRequestUnlink(l.id)}
                >
                  <Trash2 size={14} />
                </Button>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
