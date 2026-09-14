import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import type { CategoryGroup } from "../../types";
import type { GroupForm } from "./categoryForms";

interface GroupDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  editingGroup: CategoryGroup | null;
  globalGroupMode: boolean;
  form: GroupForm;
  onChange: (form: GroupForm) => void;
  onSubmit: () => void;
}

export default function GroupDialog({
  open,
  onOpenChange,
  editingGroup,
  globalGroupMode,
  form,
  onChange,
  onSubmit,
}: GroupDialogProps) {
  const patch = (fields: Partial<GroupForm>) => onChange({ ...form, ...fields });

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>
            {editingGroup
              ? "Edit Group"
              : globalGroupMode
                ? "New Global Group"
                : "New Group"}
          </DialogTitle>
        </DialogHeader>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div className="flex flex-col gap-1.5">
            <Label
              htmlFor="group-form-name"
              className="text-xs text-muted-foreground"
            >
              Name
            </Label>
            <Input
              id="group-form-name"
              placeholder="e.g. Vacation"
              value={form.name}
              onChange={(e) => patch({ name: e.target.value })}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label
              htmlFor="group-form-icon"
              className="text-xs text-muted-foreground"
            >
              Icon
            </Label>
            <Input
              id="group-form-icon"
              placeholder="e.g. plane"
              value={form.icon}
              onChange={(e) => patch({ icon: e.target.value })}
            />
          </div>
          {!editingGroup && (
            <div className="flex flex-col gap-1.5">
              <Label
                htmlFor="group-form-id"
                className="text-xs text-muted-foreground"
              >
                ID (slug)
              </Label>
              <Input
                id="group-form-id"
                placeholder="e.g. vacation"
                value={form.id}
                onChange={(e) =>
                  patch({
                    id: e.target.value.toLowerCase().replace(/\s+/g, "_"),
                  })
                }
              />
            </div>
          )}
          <div className="flex flex-col gap-1.5">
            <Label
              htmlFor="group-form-color"
              className="text-xs text-muted-foreground"
            >
              Color
            </Label>
            <input
              id="group-form-color"
              type="color"
              value={form.color}
              onChange={(e) => patch({ color: e.target.value })}
              className="w-full h-10.5 cursor-pointer bg-background border border-border rounded-lg p-1"
            />
          </div>
        </div>
        <div className="flex justify-end">
          <Button
            onClick={onSubmit}
            disabled={!form.name || (!editingGroup && !form.id)}
          >
            {editingGroup ? "Update Group" : "Create Group"}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
