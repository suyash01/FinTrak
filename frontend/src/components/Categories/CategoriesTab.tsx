import { useMemo, useRef, useState } from "react";
import { Plus, Trash2, Edit2, Globe } from "lucide-react";
import api from "../../api/client";
import { useSettings } from "../../context/SettingsContext";
import { useAuth } from "../../context/AuthContext";
import { useDomainData } from "../../context/DomainDataContext";
import { buildCategorySections } from "../../lib/categories";
import { Button } from "@/components/ui/button";
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
import { useCommandIntent } from "../../lib/useCommandIntent";
import type { Category } from "../../types";
import CategoryDialog from "./CategoryDialog";
import { EMPTY_CATEGORY_FORM, type CategoryForm } from "./categoryForms";

export default function CategoriesTab() {
  const { categories, groups, refreshCategories } = useDomainData();
  const { compactLayout } = useSettings();
  const { user } = useAuth();
  const isAdmin = user?.role === "admin";

  const [showNewCategory, setShowNewCategory] = useState(false);
  const [editingCategory, setEditingCategory] = useState<Category | null>(null);
  const [globalCategoryMode, setGlobalCategoryMode] = useState(false);
  const [catForm, setCatForm] = useState<CategoryForm>(EMPTY_CATEGORY_FORM);
  const [deleteResult, setDeleteResult] = useState<string | null>(null);
  const deleteTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const categorySections = useMemo(
    () => buildCategorySections(groups, categories),
    [groups, categories],
  );

  const flash = (msg: string) => {
    setDeleteResult(msg);
    if (deleteTimerRef.current) clearTimeout(deleteTimerRef.current);
    deleteTimerRef.current = setTimeout(() => setDeleteResult(null), 4000);
  };

  const openNewCategory = () => {
    setEditingCategory(null);
    setGlobalCategoryMode(false);
    setCatForm({
      ...EMPTY_CATEGORY_FORM,
      groupId: groups[0]?.id || "",
    });
    setShowNewCategory(true);
  };

  const openEditCategory = (cat: Category) => {
    setEditingCategory(cat);
    setGlobalCategoryMode(false);
    setCatForm({
      name: cat.name,
      icon: cat.icon || "tag",
      color: cat.color || "#06b6d4",
      groupId: cat.groupId,
    });
    setShowNewCategory(true);
  };

  useCommandIntent("new-category", openNewCategory);

  const handleSaveCategory = async () => {
    try {
      if (editingCategory) {
        if (editingCategory.isGlobal) {
          await api.updateGlobalCategory(editingCategory.id, catForm);
        } else {
          await api.updateCategory(editingCategory.id, catForm);
        }
      } else if (globalCategoryMode) {
        await api.createGlobalCategory(catForm);
      } else {
        await api.createCategory(catForm);
      }
      setShowNewCategory(false);
      setEditingCategory(null);
      setGlobalCategoryMode(false);
      refreshCategories();
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const handleDeleteCategory = async (cat: Category) => {
    try {
      const result = cat.isGlobal
        ? await api.deleteGlobalCategory(cat.id)
        : await api.deleteCategory(cat.id);
      refreshCategories();
      if (result.clearedTransactions > 0) {
        flash(
          `Deleted "${cat.name}" — ${result.clearedTransactions} transaction(s) uncategorized, ${result.deletedRules} rule(s) removed.`,
        );
      } else {
        flash(`Deleted "${cat.name}".`);
      }
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  return (
    <>
      <div className="flex justify-between items-center mb-5 flex-wrap gap-4">
        <span className="text-sm text-muted-foreground">
          {categories.length} categories
        </span>
        <div className="flex gap-3">
          {isAdmin && (
            <Button
              variant="outline"
              onClick={() => {
                setEditingCategory(null);
                setGlobalCategoryMode(true);
                setCatForm({
                  ...EMPTY_CATEGORY_FORM,
                  groupId: groups.find((g) => g.isGlobal)?.id || "",
                });
                setShowNewCategory(true);
              }}
            >
              <Globe /> Add Global Category
            </Button>
          )}
          <Button onClick={openNewCategory}>
            <Plus /> Add Category
          </Button>
        </div>
      </div>

      {deleteResult && (
        <div className="mb-4 px-4 py-2.5 bg-emerald-500/10 border border-emerald-500/20 rounded-lg text-sm text-emerald-400">
          {deleteResult}
        </div>
      )}

      <CategoryDialog
        open={showNewCategory}
        onOpenChange={(open) => {
          setShowNewCategory(open);
          if (!open) {
            setEditingCategory(null);
            setGlobalCategoryMode(false);
          }
        }}
        editingCategory={editingCategory}
        globalMode={globalCategoryMode}
        form={catForm}
        onChange={setCatForm}
        onSubmit={handleSaveCategory}
        groups={groups}
      />

      {categorySections.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No categories yet. Add one above.
        </p>
      ) : (
        categorySections.map((s) => (
          <div key={s.group.id} className="mb-8">
            <div className="flex items-center gap-2 mb-3">
              <span
                className="w-3 h-3 rounded-full"
                style={{ background: s.group.color }}
              />
              <h4 className="text-xs font-semibold text-muted-foreground uppercase tracking-widest">
                {s.group.name}
              </h4>
            </div>
            <div
              className={`grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4 ${compactLayout ? "gap-2" : "gap-3"}`}
            >
              {s.items.map((cat) => (
                <div
                  key={cat.id}
                  className={`flex items-center gap-3 ${compactLayout ? "px-3 py-1.5" : "px-4 py-3"} bg-card border border-border rounded-lg`}
                >
                  <span
                    className="w-3 h-3 rounded-full shrink-0"
                    style={{ background: cat.color }}
                  />
                  <span className="flex-1 text-sm font-medium text-foreground min-w-0">
                    <span className="block truncate">{cat.name}</span>
                    {cat.isGlobal && (
                      <span className="text-xs font-normal text-muted-foreground flex items-center gap-1">
                        <Globe size={10} /> Global
                      </span>
                    )}
                  </span>
                  {(isAdmin || !cat.isGlobal) && (
                    <>
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
                            <AlertDialogTitle>
                              Delete category?
                            </AlertDialogTitle>
                            <AlertDialogDescription>
                              Delete category "{cat.name}"? Any transactions
                              using it will be uncategorized, and any rules
                              pointing to it will be removed.
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
                    </>
                  )}
                </div>
              ))}
            </div>
          </div>
        ))
      )}
    </>
  );
}
