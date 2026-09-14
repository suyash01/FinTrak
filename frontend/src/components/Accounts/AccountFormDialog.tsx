import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import type { AccountType } from "../../types";
import { parseBillingDay, type AccountForm } from "./accountHelpers";

interface AccountFormDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  form: AccountForm;
  onChange: (form: AccountForm) => void;
  onSubmit: () => void;
  accountTypes: AccountType[];
  submitLabel: string;
  billingDayHint: string;
  // The closed-account toggle is only editable once an account exists.
  showClosed?: boolean;
}

// Shared create/edit account form. The parent owns the open state and the form
// value; this component is a controlled presentation layer only.
export default function AccountFormDialog({
  open,
  onOpenChange,
  title,
  form,
  onChange,
  onSubmit,
  accountTypes,
  submitLabel,
  billingDayHint,
  showClosed = false,
}: AccountFormDialogProps) {
  const patch = (fields: Partial<AccountForm>) =>
    onChange({ ...form, ...fields });

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onOpenChange(false)}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div className="flex flex-col gap-1.5">
            <Label
              htmlFor="account-form-name"
              className="text-xs text-muted-foreground"
            >
              Name
            </Label>
            <Input
              id="account-form-name"
              className="h-10"
              placeholder="e.g. HDFC Savings"
              value={form.name}
              onChange={(e) => patch({ name: e.target.value })}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label
              htmlFor="account-form-type"
              className="text-xs text-muted-foreground"
            >
              Type
            </Label>
            <Select
              value={form.accountTypeId}
              onValueChange={(v) => patch({ accountTypeId: v })}
            >
              <SelectTrigger id="account-form-type" className="w-full h-10">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {accountTypes.map((at) => (
                  <SelectItem key={at.id} value={at.id}>
                    {at.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label
              htmlFor="account-form-bank"
              className="text-xs text-muted-foreground"
            >
              Bank
            </Label>
            <Input
              id="account-form-bank"
              className="h-10"
              placeholder="e.g. HDFC"
              value={form.bank}
              onChange={(e) => patch({ bank: e.target.value })}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label
              htmlFor="account-form-color"
              className="text-xs text-muted-foreground"
            >
              Color
            </Label>
            <input
              id="account-form-color"
              type="color"
              value={form.color}
              onChange={(e) => patch({ color: e.target.value })}
              className="w-full h-10 cursor-pointer bg-background border border-border rounded-lg p-1"
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label
              htmlFor="account-form-billing-day"
              className="text-xs text-muted-foreground"
            >
              Billing Day
            </Label>
            <Input
              id="account-form-billing-day"
              type="number"
              min={1}
              max={31}
              placeholder="None"
              className="h-10"
              value={form.billingDay ?? ""}
              onChange={(e) =>
                patch({ billingDay: parseBillingDay(e.target.value) })
              }
            />
            <span className="text-[11px] text-muted-foreground">
              {billingDayHint}
            </span>
          </div>
          {showClosed && (
            <div className="flex items-end pb-1">
              <label className="flex items-center gap-2 text-sm cursor-pointer">
                <input
                  type="checkbox"
                  checked={form.closed}
                  onChange={(e) => patch({ closed: e.target.checked })}
                  className="h-4 w-4 rounded border-border accent-primary"
                />
                Closed account
                <span className="text-[11px] text-muted-foreground">
                  (transactions become read-only; linking stays possible)
                </span>
              </label>
            </div>
          )}
        </div>
        <DialogFooter>
          <Button
            size="lg"
            className="px-4"
            onClick={onSubmit}
            disabled={!form.name}
          >
            {submitLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
