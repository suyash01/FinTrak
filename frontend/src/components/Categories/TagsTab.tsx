import { useEffect, useState } from "react";
import { Tag as TagIcon, Pencil } from "lucide-react";
import api from "../../api/client";
import { useSettings } from "../../context/SettingsContext";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { toast } from "sonner";
import type { TagCount } from "../../types";

export default function TagsTab() {
  const { compactLayout } = useSettings();
  const [tags, setTags] = useState<TagCount[]>([]);
  const [loading, setLoading] = useState(true);
  const [renaming, setRenaming] = useState<TagCount | null>(null);
  const [toName, setToName] = useState("");
  const [saving, setSaving] = useState(false);

  const loadTags = () => {
    setLoading(true);
    api
      .getTags()
      .then((res) => setTags(res.data || []))
      .catch((err) => toast.error((err as Error).message))
      .finally(() => setLoading(false));
  };

  useEffect(loadTags, []);

  const openRename = (tag: TagCount) => {
    setRenaming(tag);
    setToName(tag.name);
  };

  const handleRename = async () => {
    if (!renaming) return;
    const next = toName.trim();
    if (!next || next === renaming.name) {
      setRenaming(null);
      return;
    }
    setSaving(true);
    try {
      const res = await api.renameTag({ from: renaming.name, to: next });
      toast.success(
        `Renamed "${renaming.name}" on ${res.updated} transaction${res.updated === 1 ? "" : "s"}`,
      );
      setRenaming(null);
      loadTags();
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <div className="flex justify-between items-center mb-5 flex-wrap gap-4">
        <span className="text-sm text-muted-foreground">
          {tags.length} tag{tags.length === 1 ? "" : "s"}
        </span>
      </div>

      <div className="bg-card border border-border rounded-xl overflow-x-auto">
        <table className="w-full text-left border-collapse">
          <thead>
            <tr>
              {["Tag", "Transactions", ""].map((h, i) => (
                <th
                  key={h || "actions"}
                  className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-xs font-semibold uppercase tracking-wider text-muted-foreground bg-muted/50 border-b border-border whitespace-nowrap ${i === 2 ? "w-12.5" : ""}`}
                >
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {tags.length === 0 ? (
              <tr>
                <td
                  colSpan={3}
                  className="text-center p-10 text-muted-foreground"
                >
                  {loading
                    ? "Loading tags..."
                    : "No tags yet. Add tags when editing a transaction."}
                </td>
              </tr>
            ) : (
              tags.map((t) => (
                <tr
                  key={t.name}
                  className="hover:bg-muted/30 transition-colors border-b border-border last:border-0"
                >
                  <td
                    className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm font-medium text-foreground`}
                  >
                    <span className="flex items-center gap-2">
                      <TagIcon size={14} className="text-muted-foreground" />
                      {t.name}
                    </span>
                  </td>
                  <td
                    className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm text-muted-foreground`}
                  >
                    {t.count.toLocaleString()}
                  </td>
                  <td
                    className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-right`}
                  >
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      aria-label={`Rename tag ${t.name}`}
                      className="text-muted-foreground hover:text-primary"
                      onClick={() => openRename(t)}
                    >
                      <Pencil />
                    </Button>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      <Dialog
        open={renaming !== null}
        onOpenChange={(open) => !open && setRenaming(null)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Rename tag</DialogTitle>
            <DialogDescription>
              Rewrites every use of "{renaming?.name}" across your transactions.
            </DialogDescription>
          </DialogHeader>
          <div>
            <Label htmlFor="rename-tag-input" className="mb-1.5">
              New name
            </Label>
            <Input
              id="rename-tag-input"
              value={toName}
              onChange={(e) => setToName(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && handleRename()}
              autoFocus
            />
          </div>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setRenaming(null)}>
              Cancel
            </Button>
            <Button onClick={handleRename} disabled={saving || !toName.trim()}>
              {saving ? "Renaming..." : "Rename"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
