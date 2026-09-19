import { useCallback, useEffect, useState } from "react";
import { GitBranch } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";
import { Button } from "@/components/ui/button";
import api from "../../api/client";
import { formatCurrency, formatDate } from "../../utils/formatters";
import { linkTypeBadgeClass } from "./linkHelpers";
import type { Link, Transaction } from "../../types";

// Tracing walks the link graph outward from one transaction. The caps keep a
// pathological graph (or a maliciously dense one) from unbounded fan-out.
const MAX_DEPTH = 6;
const MAX_NODES = 60;

interface ChainNode {
  txn: Transaction;
  depth: number;
}

interface ChainGraph {
  nodes: ChainNode[];
  edges: Link[];
  truncated: boolean;
}

interface TraceChainModalProps {
  txn: Transaction;
  onClose: () => void;
}

// TraceChainModal fetches the connected component of the link graph reachable
// from `txn` (bounded by MAX_DEPTH/MAX_NODES) and renders it as a hop-by-hop
// chain: transfer -> transfer, purchase -> refund, and so on.
export default function TraceChainModal({
  txn,
  onClose,
}: TraceChainModalProps) {
  const [graph, setGraph] = useState<ChainGraph | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const nodeMap = new Map<string, ChainNode>();
      nodeMap.set(txn.id, { txn, depth: 0 });
      const edgeMap = new Map<string, Link>();
      const visited = new Set<string>([txn.id]);
      let frontier = [txn.id];
      let truncated = false;

      for (let depth = 1; depth <= MAX_DEPTH && frontier.length > 0; depth++) {
        const linksPerId = await Promise.all(
          frontier.map((id) => api.getLinks({ txnId: id })),
        );
        const next: string[] = [];
        linksPerId.forEach((links, i) => {
          const id = frontier[i];
          for (const link of links) {
            if (!edgeMap.has(link.id)) edgeMap.set(link.id, link);
            const other = link.fromTxnId === id ? link.toTxn : link.fromTxn;
            if (!other || !other.id) continue;
            if (!nodeMap.has(other.id)) {
              if (nodeMap.size >= MAX_NODES) {
                truncated = true;
                continue;
              }
              nodeMap.set(other.id, { txn: other, depth });
            }
            if (!visited.has(other.id)) {
              visited.add(other.id);
              next.push(other.id);
            }
          }
        });
        frontier = next;
      }

      const nodes = [...nodeMap.values()].sort((a, b) => {
        if (a.depth !== b.depth) return a.depth - b.depth;
        return a.txn.date.localeCompare(b.txn.date);
      });
      setGraph({ nodes, edges: [...edgeMap.values()], truncated });
    } catch (err) {
      setError((err as Error).message || "Failed to load the link chain");
    } finally {
      setLoading(false);
    }
  }, [txn]);

  useEffect(() => {
    void load();
  }, [load]);

  const byDepth = new Map<number, ChainNode[]>();
  for (const node of graph?.nodes ?? []) {
    const list = byDepth.get(node.depth) ?? [];
    list.push(node);
    byDepth.set(node.depth, list);
  }
  const depths = [...byDepth.keys()].sort((a, b) => a - b);

  const linksFor = (id: string): Link[] =>
    (graph?.edges ?? []).filter(
      (l) => l.fromTxnId === id || l.toTxnId === id,
    );

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-2xl max-h-[80vh] flex flex-col overflow-hidden p-0 gap-0 rounded-2xl">
        <DialogHeader className="px-6 py-4 border-b border-border bg-card pr-10">
          <DialogTitle className="flex items-center gap-2">
            <GitBranch size={18} className="text-primary" />
            Link chain
          </DialogTitle>
          <DialogDescription>
            Every transaction connected to “{txn.description}” through links,
            traced hop by hop.
          </DialogDescription>
        </DialogHeader>

        <div className="flex-1 overflow-y-auto px-6 py-4">
          {loading ? (
            <div className="flex justify-center py-12">
              <Spinner className="size-8 text-primary" />
            </div>
          ) : error ? (
            <div className="flex flex-col items-center gap-3 py-12 text-center">
              <p className="text-sm text-destructive">{error}</p>
              <Button onClick={load}>Retry</Button>
            </div>
          ) : graph && graph.edges.length > 0 ? (
            <div className="space-y-4">
              {depths.map((depth) => (
                <div key={depth}>
                  <div className="mb-2 text-[11px] font-semibold uppercase tracking-widest text-muted-foreground">
                    {depth === 0 ? "This transaction" : `Hop ${depth}`}
                  </div>
                  <div className="space-y-2">
                    {byDepth.get(depth)?.map((node) => {
                      const connecting = linksFor(node.txn.id);
                      return (
                        <div
                          key={node.txn.id}
                          className={`rounded-lg border px-3 py-2 ${
                            depth === 0
                              ? "border-primary/40 bg-primary/5"
                              : "border-border"
                          }`}
                        >
                          <div className="flex items-center justify-between gap-3">
                            <div className="min-w-0">
                              <div className="truncate text-sm font-medium text-foreground">
                                {node.txn.description}
                              </div>
                              <div className="text-xs text-muted-foreground">
                                {formatDate(node.txn.date)}
                                {node.txn.accountName
                                  ? ` · ${node.txn.accountName}`
                                  : ""}
                              </div>
                            </div>
                            <div
                              className={`whitespace-nowrap text-sm font-semibold ${
                                node.txn.type === "debit"
                                  ? "text-destructive"
                                  : "text-chart-3"
                              }`}
                            >
                              {node.txn.type === "debit" ? "−" : "+"}
                              {formatCurrency(node.txn.amount)}
                            </div>
                          </div>
                          {connecting.length > 0 && (
                            <div className="mt-1.5 flex flex-wrap gap-1">
                              {connecting.map((l) => (
                                <span
                                  key={l.id}
                                  className={`rounded px-1.5 py-0.5 text-[10px] font-medium capitalize ${linkTypeBadgeClass(l.type)}`}
                                >
                                  {l.type.replace("_", " ")}
                                </span>
                              ))}
                            </div>
                          )}
                        </div>
                      );
                    })}
                  </div>
                </div>
              ))}
              {graph.truncated && (
                <p className="text-center text-xs text-muted-foreground">
                  Chain truncated at {MAX_NODES} transactions.
                </p>
              )}
            </div>
          ) : (
            <div className="py-12 text-center text-sm text-muted-foreground">
              This transaction has no links to trace.
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
