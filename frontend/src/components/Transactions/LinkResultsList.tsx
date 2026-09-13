import { Link2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Spinner } from "@/components/ui/spinner";
import { formatCurrency, formatDate } from "../../utils/formatters";
import type { Transaction } from "../../types";

interface LinkResultsListProps {
  loading: boolean;
  results: Transaction[];
  sourceAccountId: string;
  onSelect: (txn: Transaction) => void;
}

// Candidate matches for the source transaction.
export default function LinkResultsList({
  loading,
  results,
  sourceAccountId,
  onSelect,
}: LinkResultsListProps) {
  return (
    <div className="flex-1 overflow-y-auto p-4 custom-scrollbar">
      {loading ? (
        <div className="flex flex-col items-center justify-center p-12 text-muted-foreground">
          <Spinner className="h-8 w-8 mb-3 text-primary" />
          <span className="text-sm">Searching...</span>
        </div>
      ) : results.length === 0 ? (
        <div className="text-center p-12 bg-background/30 rounded-2xl border border-dashed border-border">
          <Link2 className="w-10 h-10 text-muted-foreground mx-auto mb-3 opacity-20" />
          <div className="text-muted-foreground text-sm font-medium">
            No potential matches found
          </div>
          <p className="text-muted-foreground text-[11px] mt-1 italic">
            Try adjusting your search or filters
          </p>
        </div>
      ) : (
        <div className="space-y-2">
          {results.map((r) => {
            const sameAccount = r.accountId === sourceAccountId;
            return (
              <div
                key={r.id}
                className="bg-background/50 border border-border p-4 rounded-xl flex items-center gap-4 hover:border-primary/50 hover:bg-accent/30 transition-all group"
              >
                <div className="flex-1 min-w-0">
                  <div className="font-semibold text-sm text-foreground truncate group-hover:text-primary transition-colors">
                    {r.description}
                  </div>
                  <div className="text-[12px] text-muted-foreground mt-1 flex items-center gap-2 flex-wrap">
                    <Badge
                      variant="secondary"
                      className="h-auto px-1.5 py-0.5 rounded"
                    >
                      {r.accountName}
                    </Badge>
                    <span>·</span>
                    <span>{formatDate(r.date)}</span>
                    <span>·</span>
                    <span
                      className={`font-bold ${r.type === "debit" ? "text-destructive" : "text-emerald-500"}`}
                    >
                      {r.type === "debit" ? "−" : "+"}
                      {formatCurrency(r.amount)}
                    </span>
                    {sameAccount && (
                      <Badge className="h-auto px-1.5 py-0.5 bg-amber-500/10 text-amber-400 text-[10px] font-semibold rounded">
                        Same Account
                      </Badge>
                    )}
                    {r.isLinked && (
                      <Badge className="h-auto px-1.5 py-0.5 bg-primary/10 text-primary text-[10px] font-semibold rounded">
                        Already Linked
                      </Badge>
                    )}
                  </div>
                </div>
                <Button
                  onClick={() => onSelect(r)}
                  className="opacity-0 group-hover:opacity-100 px-4 py-2 h-auto text-xs font-bold"
                >
                  Choose Type…
                </Button>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
