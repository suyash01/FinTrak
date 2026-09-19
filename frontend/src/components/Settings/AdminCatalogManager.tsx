import { useEffect, useMemo, useState } from "react";
import { Plus, Globe, Edit2, Trash2, RefreshCw } from "lucide-react";
import api from "../../api/client";
import { useDomainData } from "../../context/DomainDataContext";
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
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { toast } from "sonner";
import type {
  AdminCatalog,
  AdminCatalogCategory,
  Category,
  CategoryGroup,
} from "../../types";
import CategoryDialog from "../Categories/CategoryDialog";
import GroupDialog from "../Categories/GroupDialog";
import {
  EMPTY_CATEGORY_FORM,
  EMPTY_GROUP_FORM,
  type CategoryForm,
  type GroupForm,
} from "../Categories/categoryForms";

// AdminCatalogManager is the admin console for the shared global catalog. It
// shows each global group/category with its usage counts and lets an admin
// create global groups, and create/edit/delete global categories. Base groups
// and global groups themselves are immutable through the API, so they are
// listed read-only (with their category counts) rather than editable.
export default function AdminCatalogManager() {
  const { refreshCategories, refreshGroups } = useDomainData();
  const [catalog, setCatalog] = useState<AdminCatalog | null>(null);
  const [loading, setLoading] = useState(true);

  const [showGroupDialog, setShowGroupDialog] = useState(false);
  const [groupForm, setGroupForm] = useState<GroupForm>(EMPTY_GROUP_FORM);

  const [showCategoryDialog, setShowCategoryDialog] = useState(false);
  const [editingCategory, setEditingCategory] = useState<Category | null>(null);
  const [categoryForm, setCategoryForm] =
    useState<CategoryForm>(EMPTY_CATEGORY_FORM);

  const loadCatalog = () => {
    setLoading(true);
    api
      .getAdminCatalog()
      .then(setCatalog)
      .catch((err) => toast.error((err as Error).message))
      .finally(() => setLoading(false));
  };

  useEffect(loadCatalog, []);

  const globalGroups = useMemo<CategoryGroup[]>(
    () => catalog?.groups ?? [],
    [catalog],
  );

  const handleCreateGroup = async () => {
    try {
      await api.createGlobalGroup(groupForm);
      toast.success("Global group created");
      setShowGroupDialog(false);
      setGroupForm(EMPTY_GROUP_FORM);
      loadCatalog();
      refreshGroups();
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const handleSaveCategory = async () => {
    try {
      if (editingCategory) {
        await api.updateGlobalCategory(editingCategory.id, categoryForm);
        toast.success("Global category updated");
      } else {
        await api.createGlobalCategory(categoryForm);
        toast.success("Global category created");
      }
      setShowCategoryDialog(false);
      setEditingCategory(null);
      loadCatalog();
      refreshCategories();
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const handleDeleteCategory = async (cat: AdminCatalogCategory) => {
    try {
      const result = await api.deleteGlobalCategory(cat.id);
      toast.success(
        result.clearedTransactions > 0
          ? `Deleted "${cat.name}" — ${result.clearedTransactions} transaction(s) uncategorized.`
          : `Deleted "${cat.name}".`,
      );
      loadCatalog();
      refreshCategories();
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const openNewCategory = () => {
    setEditingCategory(null);
    setCategoryForm({
      ...EMPTY_CATEGORY_FORM,
      groupId: globalGroups[0]?.id || "",
    });
    setShowCategoryDialog(true);
  };

  const openEditCategory = (cat: AdminCatalogCategory) => {
    setEditingCategory(cat);
    setCategoryForm({
      name: cat.name,
      icon: cat.icon || "tag",
      color: cat.color || "#06b6d4",
      groupId: cat.groupId,
    });
    setShowCategoryDialog(true);
  };

  if (loading && !catalog) {
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Spinner className="size-4" /> Loading catalog...
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div className="flex justify-end gap-2">
        <Button
          variant="outline"
          size="sm"
          onClick={loadCatalog}
          aria-label="Refresh catalog"
        >
          <RefreshCw /> Refresh
        </Button>
        <Button
          variant="outline"
          size="sm"
          onClick={() => {
            setGroupForm(EMPTY_GROUP_FORM);
            setShowGroupDialog(true);
          }}
        >
          <Plus /> Global Group
        </Button>
        <Button size="sm" onClick={openNewCategory}>
          <Plus /> Global Category
        </Button>
      </div>

      <div>
        <h4 className="text-xs font-semibold uppercase tracking-widest text-muted-foreground mb-2">
          Global Groups
        </h4>
        <div className="divide-y divide-border border border-border rounded-lg overflow-hidden bg-background">
          {(catalog?.groups ?? []).map((g) => (
            <div
              key={g.id}
              className="p-3 flex items-center justify-between gap-3"
            >
              <div className="flex items-center gap-2 min-w-0">
                <span
                  className="w-3 h-3 rounded-full shrink-0"
                  style={{ background: g.color }}
                />
                <span className="text-sm font-medium text-foreground truncate">
                  {g.name}
                </span>
                {g.isBase && (
                  <span className="text-[10px] uppercase tracking-wide text-muted-foreground">
                    base
                  </span>
                )}
              </div>
              <span className="text-xs text-muted-foreground shrink-0">
                {g.categoryCount} categor{g.categoryCount === 1 ? "y" : "ies"}
              </span>
            </div>
          ))}
          {(catalog?.groups ?? []).length === 0 && (
            <p className="p-3 text-sm text-muted-foreground">
              No global groups.
            </p>
          )}
        </div>
      </div>

      <div>
        <h4 className="text-xs font-semibold uppercase tracking-widest text-muted-foreground mb-2">
          Global Categories
        </h4>
        <div className="divide-y divide-border border border-border rounded-lg overflow-hidden bg-background">
          {(catalog?.categories ?? []).map((cat) => (
            <div
              key={cat.id}
              className="p-3 flex items-center justify-between gap-3 group hover:bg-accent transition-colors"
            >
              <div className="flex items-center gap-2 min-w-0">
                <span
                  className="w-3 h-3 rounded-full shrink-0"
                  style={{ background: cat.color }}
                />
                <span className="text-sm font-medium text-foreground truncate">
                  {cat.name}
                </span>
                <span className="text-xs text-muted-foreground truncate">
                  {cat.groupName}
                </span>
              </div>
              <div className="flex items-center gap-1 shrink-0">
                <span className="text-xs text-muted-foreground mr-1">
                  {cat.transactionCount.toLocaleString()} txn
                  {cat.transactionCount === 1 ? "" : "s"}
                </span>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={`Edit ${cat.name}`}
                  className="text-muted-foreground hover:text-primary"
                  onClick={() => openEditCategory(cat)}
                >
                  <Edit2 />
                </Button>
                <AlertDialog>
                  <AlertDialogTrigger asChild>
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      aria-label={`Delete ${cat.name}`}
                      className="text-muted-foreground hover:text-destructive"
                    >
                      <Trash2 />
                    </Button>
                  </AlertDialogTrigger>
                  <AlertDialogContent>
                    <AlertDialogHeader>
                      <AlertDialogTitle>Delete global category?</AlertDialogTitle>
                      <AlertDialogDescription>
                        Delete "{cat.name}"? Any transaction using it (across
                        every user) will be uncategorized, and referencing rules
                        removed.
                      </AlertDialogDescription>
                    </AlertDialogHeader>
                    <AlertDialogFooter>
                      <AlertDialogCancel>Cancel</AlertDialogCancel>
                      <AlertDialogAction
                        variant="destructive"
                        onClick={() => handleDeleteCategory(cat)}
                      >
                        Delete
                      </AlertDialogAction>
                    </AlertDialogFooter>
                  </AlertDialogContent>
                </AlertDialog>
              </div>
            </div>
          ))}
          {(catalog?.categories ?? []).length === 0 && (
            <p className="p-3 text-sm text-muted-foreground">
              No global categories.
            </p>
          )}
        </div>
      </div>

      <GroupDialog
        open={showGroupDialog}
        onOpenChange={(open) => {
          setShowGroupDialog(open);
          if (!open) setGroupForm(EMPTY_GROUP_FORM);
        }}
        editingGroup={null}
        globalGroupMode
        form={groupForm}
        onChange={setGroupForm}
        onSubmit={handleCreateGroup}
      />

      <CategoryDialog
        open={showCategoryDialog}
        onOpenChange={(open) => {
          setShowCategoryDialog(open);
          if (!open) setEditingCategory(null);
        }}
        editingCategory={editingCategory}
        globalMode
        form={categoryForm}
        onChange={setCategoryForm}
        onSubmit={handleSaveCategory}
        groups={globalGroups}
      />
    </div>
  );
}
