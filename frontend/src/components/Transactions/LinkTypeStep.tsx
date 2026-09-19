import {
  Link2,
  ArrowLeft,
  RotateCcw,
  Gift,
  ArrowLeftRight,
  Receipt,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { formatCurrency, formatDate } from "../../utils/formatters";
import type { LinkType, Transaction } from "../../types";

interface LinkTypeStepProps {
  txn: Transaction;
  target: Transaction;
  linkType: LinkType;
  onLinkTypeChange: (type: LinkType) => void;
  onBack: () => void;
  onConfirm: () => void;
}

// Confirmation view shown after a target is picked: preview the pair and let the
// user choose the link type.
export default function LinkTypeStep({
  txn,
  target,
  linkType,
  onLinkTypeChange,
  onBack,
  onConfirm,
}: LinkTypeStepProps) {
  const renderEndpoint = (label: string, t: Transaction) => (
    <div className="bg-background/50 border border-border rounded-xl p-3.5">
      <div className="text-[10px] font-bold text-muted-foreground uppercase tracking-wider mb-1.5">
        {label}
      </div>
      <div className="font-medium text-sm text-foreground truncate">
        {t.description}
      </div>
      <div className="text-xs text-muted-foreground mt-1">
        {t.accountName} · {formatDate(t.date)} ·
        <span
          className={t.type === "debit" ? "text-destructive" : "text-chart-3"}
        >
          {t.type === "debit" ? "−" : "+"}
          {formatCurrency(t.amount)}
        </span>
      </div>
    </div>
  );

  return (
    <Dialog open onOpenChange={(open) => !open && onBack()}>
      <DialogContent className="sm:max-w-lg max-h-[80vh] flex flex-col overflow-hidden p-0 gap-0 rounded-2xl">
        <DialogHeader className="px-6 py-4 border-b border-border bg-card">
          <div className="flex items-center gap-3 pr-8">
            <Button
              variant="ghost"
              size="icon-sm"
              className="-ml-1.5"
              onClick={onBack}
              aria-label="Back to results"
            >
              <ArrowLeft size={18} />
            </Button>
            <div>
              <DialogTitle>Choose Link Type</DialogTitle>
              <DialogDescription className="text-xs mt-0.5">
                Choose the link type for this connection
              </DialogDescription>
            </div>
          </div>
        </DialogHeader>

        <div className="px-6 py-5 border-b border-border bg-accent/20">
          <div className="space-y-3">
            {renderEndpoint("Source", txn)}
            <div className="flex justify-center">
              <Link2 className="text-primary/50" size={18} />
            </div>
            {renderEndpoint("Target", target)}
          </div>
        </div>

        <div className="px-6 py-5 border-b border-border">
          <div className="text-[11px] font-bold text-muted-foreground uppercase tracking-wider mb-3">
            Link Type
          </div>
          <div className="grid grid-cols-2 gap-3">
            <Button
              type="button"
              variant="ghost"
              onClick={() => onLinkTypeChange("transfer")}
              className={`relative flex flex-col items-center gap-2 p-4 h-auto rounded-xl border-2 transition-all ${
                linkType === "transfer"
                  ? "border-primary bg-primary/10 shadow-lg shadow-primary/10"
                  : "border-border bg-background/50 hover:border-muted-foreground"
              }`}
            >
              <ArrowLeftRight
                size={22}
                className={
                  linkType === "transfer" ? "text-primary" : "text-muted-foreground"
                }
              />
              <span
                className={`text-sm font-semibold ${linkType === "transfer" ? "text-primary" : "text-muted-foreground"}`}
              >
                Transfer
              </span>
              <span className="text-[10px] text-muted-foreground">
                Money between accounts
              </span>
            </Button>
            <Button
              type="button"
              variant="ghost"
              onClick={() => onLinkTypeChange("cashback")}
              className={`relative flex flex-col items-center gap-2 p-4 h-auto rounded-xl border-2 transition-all ${
                linkType === "cashback"
                  ? "border-chart-3 bg-chart-3/10 shadow-lg shadow-chart-3/10"
                  : "border-border bg-background/50 hover:border-muted-foreground"
              }`}
            >
              <Gift
                size={22}
                className={
                  linkType === "cashback"
                    ? "text-chart-3"
                    : "text-muted-foreground"
                }
              />
              <span
                className={`text-sm font-semibold ${linkType === "cashback" ? "text-chart-3" : "text-muted-foreground"}`}
              >
                Cashback
              </span>
              <span className="text-[10px] text-muted-foreground">
                Reward or cash back
              </span>
            </Button>
            <Button
              type="button"
              variant="ghost"
              onClick={() => onLinkTypeChange("refund")}
              className={`relative flex flex-col items-center gap-2 p-4 h-auto rounded-xl border-2 transition-all ${
                linkType === "refund"
                  ? "border-chart-4 bg-chart-4/10 shadow-lg shadow-chart-4/10"
                  : "border-border bg-background/50 hover:border-muted-foreground"
              }`}
            >
              <RotateCcw
                size={22}
                className={
                  linkType === "refund" ? "text-amber-700 dark:text-amber-300" : "text-muted-foreground"
                }
              />
              <span
                className={`text-sm font-semibold ${linkType === "refund" ? "text-amber-700 dark:text-amber-300" : "text-muted-foreground"}`}
              >
                Refund
              </span>
              <span className="text-[10px] text-muted-foreground">
                Return or reversal
              </span>
            </Button>
            <Button
              type="button"
              variant="ghost"
              onClick={() => onLinkTypeChange("bill_payment")}
              className={`relative flex flex-col items-center gap-2 p-4 h-auto rounded-xl border-2 transition-all ${
                linkType === "bill_payment"
                  ? "border-chart-2 bg-chart-2/10 shadow-lg shadow-chart-2/10"
                  : "border-border bg-background/50 hover:border-muted-foreground"
              }`}
            >
              <Receipt
                size={22}
                className={
                  linkType === "bill_payment"
                    ? "text-chart-2"
                    : "text-muted-foreground"
                }
              />
              <span
                className={`text-sm font-semibold ${linkType === "bill_payment" ? "text-chart-2" : "text-muted-foreground"}`}
              >
                Bill Payment
              </span>
              <span className="text-[10px] text-muted-foreground">
                Paying a bill or EMI
              </span>
            </Button>
          </div>
        </div>

        <div className="px-6 py-4 flex items-center justify-end gap-3">
          <Button variant="ghost" onClick={onBack}>
            Back to Results
          </Button>
          <Button
            onClick={onConfirm}
            disabled={!linkType}
            className="px-6 py-2.5 h-auto text-sm font-bold"
          >
            Confirm Link
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
