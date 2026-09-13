import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { CategorySection } from "../../lib/categories";
import type { Payee, Rule } from "../../types";
import {
  NO_CATEGORY,
  NO_PAYEE,
  type NewRuleForm,
} from "./categoryForms";

interface RuleDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  editingRule: Rule | null;
  form: NewRuleForm;
  onChange: (form: NewRuleForm) => void;
  onSubmit: () => void;
  categorySections: CategorySection[];
  payees: Payee[];
}

export default function RuleDialog({
  open,
  onOpenChange,
  editingRule,
  form,
  onChange,
  onSubmit,
  categorySections,
  payees,
}: RuleDialogProps) {
  const patch = (fields: Partial<NewRuleForm>) =>
    onChange({ ...form, ...fields });

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{editingRule ? "Edit Rule" : "New Rule"}</DialogTitle>
        </DialogHeader>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div className="flex flex-col gap-1.5">
            <Label className="text-xs text-muted-foreground">Pattern</Label>
            <Input
              placeholder="e.g. SWIGGY, AMAZON, UBER"
              value={form.pattern}
              onChange={(e) => patch({ pattern: e.target.value })}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label className="text-xs text-muted-foreground">Match Type</Label>
            <Select
              value={form.matchType}
              onValueChange={(v) => patch({ matchType: v })}
            >
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="contains">Contains</SelectItem>
                <SelectItem value="starts_with">Starts With</SelectItem>
                <SelectItem value="exact">Exact Match</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label className="text-xs text-muted-foreground">
              Assign Category
            </Label>
            <Select
              value={form.categoryId || NO_CATEGORY}
              onValueChange={(v) =>
                patch({ categoryId: v === NO_CATEGORY ? "" : v })
              }
            >
              <SelectTrigger className="w-full">
                <SelectValue placeholder="Choose category..." />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={NO_CATEGORY}>
                  Choose category...
                </SelectItem>
                {categorySections.map((s) => (
                  <SelectGroup key={s.group.id}>
                    <SelectLabel>{s.group.name}</SelectLabel>
                    {s.items.map((c) => (
                      <SelectItem key={c.id} value={c.id}>
                        {c.name}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label className="text-xs text-muted-foreground">
              Assign Payee (optional)
            </Label>
            <Select
              value={form.payeeId || NO_PAYEE}
              onValueChange={(v) =>
                patch({ payeeId: v === NO_PAYEE ? null : v })
              }
            >
              <SelectTrigger className="w-full">
                <SelectValue placeholder="No Payee" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={NO_PAYEE}>No Payee</SelectItem>
                {payees.map((p) => (
                  <SelectItem key={p.id} value={p.id}>
                    {p.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label className="text-xs text-muted-foreground">
              Priority (higher = first)
            </Label>
            <Input
              type="number"
              value={form.priority}
              onChange={(e) =>
                patch({ priority: parseInt(e.target.value) || 0 })
              }
            />
          </div>
        </div>
        <div className="flex justify-end">
          <Button onClick={onSubmit} disabled={!form.pattern || !form.categoryId}>
            {editingRule ? "Update Rule" : "Create Rule"}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
