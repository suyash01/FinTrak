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
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { Category, CategoryGroup } from "../../types";
import { NO_GROUP, type CategoryForm } from "./categoryForms";

interface CategoryDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  editingCategory: Category | null;
  globalMode: boolean;
  form: CategoryForm;
  onChange: (form: CategoryForm) => void;
  onSubmit: () => void;
  groups: CategoryGroup[];
}

export default function CategoryDialog({
  open,
  onOpenChange,
  editingCategory,
  globalMode,
  form,
  onChange,
  onSubmit,
  groups,
}: CategoryDialogProps) {
  const patch = (fields: Partial<CategoryForm>) =>
    onChange({ ...form, ...fields });

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>
            {editingCategory
              ? editingCategory.isGlobal
                ? "Edit Global Category"
                : "Edit Category"
              : globalMode
                ? "New Global Category"
                : "New Category"}
          </DialogTitle>
        </DialogHeader>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <div className="flex flex-col gap-1.5">
            <Label className="text-xs text-muted-foreground">Name</Label>
            <Input
              placeholder="e.g. Gym"
              value={form.name}
              onChange={(e) => patch({ name: e.target.value })}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label className="text-xs text-muted-foreground">Group</Label>
            <Select
              value={form.groupId || NO_GROUP}
              onValueChange={(v) =>
                patch({ groupId: v === NO_GROUP ? "" : v })
              }
            >
              <SelectTrigger className="w-full">
                <SelectValue placeholder="Select group" />
              </SelectTrigger>
              <SelectContent>
                {groups.map((g) => (
                  <SelectItem key={g.id} value={g.id}>
                    {g.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label className="text-xs text-muted-foreground">Color</Label>
            <input
              type="color"
              value={form.color}
              onChange={(e) => patch({ color: e.target.value })}
              className="w-full h-10.5 cursor-pointer bg-background border border-border rounded-lg p-1"
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label className="text-xs text-muted-foreground">Icon</Label>
            <Input
              placeholder="e.g. dumbbell"
              value={form.icon}
              onChange={(e) => patch({ icon: e.target.value })}
            />
          </div>
        </div>
        <div className="flex justify-end">
          <Button onClick={onSubmit} disabled={!form.name || !form.groupId}>
            {editingCategory ? "Update Category" : "Create Category"}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
