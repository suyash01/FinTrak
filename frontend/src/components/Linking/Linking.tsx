import { useState, useEffect, useCallback } from "react";
import {
  ArrowRight,
  Trash2,
  Link2,
  Square,
  CheckSquare,
  AlertCircle,
} from "lucide-react";
import api from "../../api/client";
import { formatCurrency, formatDate } from "../../utils/formatters";
import type { Link } from "../../types";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { toast } from "sonner";

export default function Linking() {
  const [links, setLinks] = useState<Link[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [unlinkId, setUnlinkId] = useState<string | null>(null);
  const [bulkUnlink, setBulkUnlink] = useState(false);

  const loadData = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const allLinks = await api.getLinks();
      setLinks(allLinks);
    } catch (err) {
      setError((err as Error).message || "Failed to load links");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadData();
  }, [loadData]);

  const handleUnlink = async (linkId: string) => {
    try {
      await api.deleteLink(linkId);
      loadData();
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const handleBulkUnlink = async () => {
    if (selected.size === 0) return;
    try {
      await api.bulkDeleteLinks({ ids: [...selected] });
      setSelected(new Set());
      loadData();
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const toggleSelect = (id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const toggleSelectAll = () => {
    if (selected.size === links.length && links.length > 0) {
      setSelected(new Set());
    } else {
      setSelected(new Set(links.map((l) => l.id)));
    }
  };

  const transferLinks = links.filter((l) => l.type === "transfer");
  const cashbackLinks = links.filter((l) => l.type === "cashback");
  const refundLinks = links.filter((l) => l.type === "refund");
  const billPaymentLinks = links.filter((l) => l.type === "bill_payment");

  return (
    <>
      <div className="shrink-0 px-8 pt-6 flex justify-between items-end">
        <div>
          <h1 className="text-2xl font-bold mb-1">Linked Transactions</h1>
          <p className="text-muted-foreground text-sm">
            Review and manage linked transactions
          </p>
        </div>
        {links.length > 0 && (
          <div className="flex items-center gap-3 mb-1">
            <Button
              variant="ghost"
              size="sm"
              className="text-xs font-medium text-muted-foreground hover:text-foreground"
              onClick={toggleSelectAll}
            >
              {selected.size === links.length ? "Deselect All" : "Select All"}
            </Button>
            {selected.size > 0 && (
              <Button
                variant="destructive"
                size="sm"
                className="border border-destructive/30"
                onClick={() => setBulkUnlink(true)}
              >
                <Trash2 size={14} />
                Remove {selected.size} Links
              </Button>
            )}
          </div>
        )}
      </div>

      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto w-full">
        {loading ? (
          <div className="flex justify-center items-center py-20">
            <Spinner className="size-8 text-primary" />
          </div>
        ) : error ? (
          <div className="flex flex-col items-center justify-center py-20 px-4 text-center">
            <AlertCircle className="w-12 h-12 text-destructive mb-4 opacity-70" />
            <p className="text-destructive text-sm mb-4">{error}</p>
            <Button onClick={loadData}>Retry</Button>
          </div>
        ) : links.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-20 px-4 text-center">
            <Link2 className="w-16 h-16 text-muted-foreground mb-4 opacity-50" />
            <h3 className="text-lg font-semibold mb-2">
              No Linked Transactions
            </h3>
            <p className="text-muted-foreground text-sm mb-6 max-w-md">
              Transactions linked manually from the Transaction list will appear
              here.
            </p>
          </div>
        ) : (
          <div className="space-y-8">
            {transferLinks.length > 0 && (
              <div>
                <h4 className="text-xs font-semibold text-muted-foreground uppercase tracking-widest mb-4 flex items-center gap-2">
                  <div className="w-1.5 h-1.5 rounded-full bg-primary"></div>
                  Transfers ({transferLinks.length})
                </h4>
                <div className="space-y-3">
                  {transferLinks.map((l) => (
                    <div
                      key={l.id}
                      className={`group bg-card border ${selected.has(l.id) ? "border-primary/50 bg-primary/5" : "border-border"} rounded-xl p-4 flex items-center gap-4 transition-all hover:border-primary/30`}
                    >
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        onClick={() => toggleSelect(l.id)}
                        className={`shrink-0 transition-colors ${selected.has(l.id) ? "text-primary" : "text-muted-foreground group-hover:text-foreground"}`}
                      >
                        {selected.has(l.id) ? (
                          <CheckSquare size={18} className="size-4.5" />
                        ) : (
                          <Square size={18} className="size-4.5" />
                        )}
                      </Button>
                      <div className="flex-1 min-w-0">
                        <div className="font-medium text-sm text-foreground truncate">
                          {l.fromTxn?.description}
                        </div>
                        <div className="text-xs text-muted-foreground mt-1">
                          {l.fromTxn?.accountName} ·{" "}
                          {formatDate(l.fromTxn?.date)} ·{" "}
                          <span className="text-destructive font-medium">
                            −{formatCurrency(l.fromTxn?.amount || 0)}
                          </span>
                        </div>
                      </div>
                      <ArrowRight
                        className="text-primary shrink-0 opacity-50"
                        size={16}
                      />
                      <div className="flex-1 min-w-0">
                        <div className="font-medium text-sm text-foreground truncate">
                          {l.toTxn?.description}
                        </div>
                        <div className="text-xs text-muted-foreground mt-1">
                          {l.toTxn?.accountName} · {formatDate(l.toTxn?.date)} ·{" "}
                          <span className="text-emerald-500 font-medium">
                            +{formatCurrency(l.toTxn?.amount || 0)}
                          </span>
                        </div>
                      </div>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        className="text-muted-foreground hover:text-destructive hover:bg-destructive/10 opacity-0 group-hover:opacity-100 transition-opacity"
                        onClick={() => setUnlinkId(l.id)}
                        title="Remove link"
                      >
                        <Trash2 size={16} />
                      </Button>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {cashbackLinks.length > 0 && (
              <div>
                <h4 className="text-xs font-semibold text-muted-foreground uppercase tracking-widest mb-4 flex items-center gap-2">
                  <div className="w-1.5 h-1.5 rounded-full bg-emerald-500"></div>
                  Cashbacks ({cashbackLinks.length})
                </h4>
                <div className="space-y-3">
                  {cashbackLinks.map((l) => (
                    <div
                      key={l.id}
                      className={`group bg-card border ${selected.has(l.id) ? "border-primary/50 bg-primary/5" : "border-border"} rounded-xl p-4 flex items-center gap-4 transition-all hover:border-primary/30`}
                    >
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        onClick={() => toggleSelect(l.id)}
                        className={`shrink-0 transition-colors ${selected.has(l.id) ? "text-primary" : "text-muted-foreground group-hover:text-foreground"}`}
                      >
                        {selected.has(l.id) ? (
                          <CheckSquare size={18} className="size-4.5" />
                        ) : (
                          <Square size={18} className="size-4.5" />
                        )}
                      </Button>
                      <div className="flex-1 min-w-0">
                        <div className="font-medium text-sm text-foreground truncate">
                          {l.fromTxn?.description}
                        </div>
                        <div className="text-xs text-muted-foreground mt-1">
                          {formatDate(l.fromTxn?.date)} ·{" "}
                          <span className="text-destructive font-medium">
                            −{formatCurrency(l.fromTxn?.amount || 0)}
                          </span>
                        </div>
                      </div>
                      <ArrowRight
                        className="text-primary shrink-0 opacity-50"
                        size={16}
                      />
                      <div className="flex-1 min-w-0">
                        <div className="font-medium text-sm text-foreground truncate">
                          {l.toTxn?.description}
                        </div>
                        <div className="text-xs text-muted-foreground mt-1">
                          {formatDate(l.toTxn?.date)} ·{" "}
                          <span className="text-emerald-500 font-medium">
                            +{formatCurrency(l.toTxn?.amount || 0)}
                          </span>
                        </div>
                      </div>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        className="text-muted-foreground hover:text-destructive hover:bg-destructive/10 opacity-0 group-hover:opacity-100 transition-opacity"
                        onClick={() => setUnlinkId(l.id)}
                        title="Remove link"
                      >
                        <Trash2 size={16} />
                      </Button>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {refundLinks.length > 0 && (
              <div>
                <h4 className="text-xs font-semibold text-muted-foreground uppercase tracking-widest mb-4 flex items-center gap-2">
                  <div className="w-1.5 h-1.5 rounded-full bg-amber-500"></div>
                  Refunds ({refundLinks.length})
                </h4>
                <div className="space-y-3">
                  {refundLinks.map((l) => (
                    <div
                      key={l.id}
                      className={`group bg-card border ${selected.has(l.id) ? "border-primary/50 bg-primary/5" : "border-border"} rounded-xl p-4 flex items-center gap-4 transition-all hover:border-primary/30`}
                    >
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        onClick={() => toggleSelect(l.id)}
                        className={`shrink-0 transition-colors ${selected.has(l.id) ? "text-primary" : "text-muted-foreground group-hover:text-foreground"}`}
                      >
                        {selected.has(l.id) ? (
                          <CheckSquare size={18} className="size-4.5" />
                        ) : (
                          <Square size={18} className="size-4.5" />
                        )}
                      </Button>
                      <div className="flex-1 min-w-0">
                        <div className="font-medium text-sm text-foreground truncate">
                          {l.fromTxn?.description}
                        </div>
                        <div className="text-xs text-muted-foreground mt-1">
                          {formatDate(l.fromTxn?.date)} ·{" "}
                          <span className="text-destructive font-medium">
                            −{formatCurrency(l.fromTxn?.amount || 0)}
                          </span>
                        </div>
                      </div>
                      <ArrowRight
                        className="text-primary shrink-0 opacity-50"
                        size={16}
                      />
                      <div className="flex-1 min-w-0">
                        <div className="font-medium text-sm text-foreground truncate">
                          {l.toTxn?.description}
                        </div>
                        <div className="text-xs text-muted-foreground mt-1">
                          {formatDate(l.toTxn?.date)} ·{" "}
                          <span className="text-emerald-500 font-medium">
                            +{formatCurrency(l.toTxn?.amount || 0)}
                          </span>
                        </div>
                      </div>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        className="text-muted-foreground hover:text-destructive hover:bg-destructive/10 opacity-0 group-hover:opacity-100 transition-opacity"
                        onClick={() => setUnlinkId(l.id)}
                        title="Remove link"
                      >
                        <Trash2 size={16} />
                      </Button>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {billPaymentLinks.length > 0 && (
              <div>
                <h4 className="text-xs font-semibold text-muted-foreground uppercase tracking-widest mb-4 flex items-center gap-2">
                  <div className="w-1.5 h-1.5 rounded-full bg-sky-500"></div>
                  Bill Payments ({billPaymentLinks.length})
                </h4>
                <div className="space-y-3">
                  {billPaymentLinks.map((l) => (
                    <div
                      key={l.id}
                      className={`group bg-card border ${selected.has(l.id) ? "border-primary/50 bg-primary/5" : "border-border"} rounded-xl p-4 flex items-center gap-4 transition-all hover:border-primary/30`}
                    >
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        onClick={() => toggleSelect(l.id)}
                        className={`shrink-0 transition-colors ${selected.has(l.id) ? "text-primary" : "text-muted-foreground group-hover:text-foreground"}`}
                      >
                        {selected.has(l.id) ? (
                          <CheckSquare size={18} className="size-4.5" />
                        ) : (
                          <Square size={18} className="size-4.5" />
                        )}
                      </Button>
                      <div className="flex-1 min-w-0">
                        <div className="font-medium text-sm text-foreground truncate">
                          {l.fromTxn?.description}
                        </div>
                        <div className="text-xs text-muted-foreground mt-1">
                          {formatDate(l.fromTxn?.date)} ·{" "}
                          <span className="text-destructive font-medium">
                            −{formatCurrency(l.fromTxn?.amount || 0)}
                          </span>
                        </div>
                      </div>
                      <ArrowRight
                        className="text-primary shrink-0 opacity-50"
                        size={16}
                      />
                      <div className="flex-1 min-w-0">
                        <div className="font-medium text-sm text-foreground truncate">
                          {l.toTxn?.description}
                        </div>
                        <div className="text-xs text-muted-foreground mt-1">
                          {formatDate(l.toTxn?.date)} ·{" "}
                          <span className="text-emerald-500 font-medium">
                            +{formatCurrency(l.toTxn?.amount || 0)}
                          </span>
                        </div>
                      </div>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        className="text-muted-foreground hover:text-destructive hover:bg-destructive/10 opacity-0 group-hover:opacity-100 transition-opacity"
                        onClick={() => setUnlinkId(l.id)}
                        title="Remove link"
                      >
                        <Trash2 size={16} />
                      </Button>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        )}
      </div>

      <AlertDialog
        open={!!unlinkId}
        onOpenChange={(open) => !open && setUnlinkId(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove this link?</AlertDialogTitle>
            <AlertDialogDescription>
              This will remove the connection and its linked transactions.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                const id = unlinkId;
                setUnlinkId(null);
                if (id) handleUnlink(id);
              }}
            >
              Remove
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog
        open={bulkUnlink}
        onOpenChange={(open) => !open && setBulkUnlink(false)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              Remove {selected.size} selected links?
            </AlertDialogTitle>
            <AlertDialogDescription>
              This will unlink the selected connections and their linked
              transactions.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                setBulkUnlink(false);
                handleBulkUnlink();
              }}
            >
              Remove
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
