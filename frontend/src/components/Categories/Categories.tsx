import { useState, useEffect, useRef } from "react";
import { Plus, Trash2, Play, Edit2, Globe, Lock } from "lucide-react";
import api from "../../api/client";
import { useSettings } from "../../context/SettingsContext";
import { useAuth } from "../../context/AuthContext";
import { buildCategorySections } from "../../lib/categories";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
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
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { toast } from "sonner";
import type {
  Category,
  CategoryGroup,
  Rule,
  Payee,
  ApplyRulesResult,
  CreateRuleRequest,
} from "../../types";

interface CategoryForm {
  name: string;
  icon: string;
  color: string;
  groupId: string;
}

interface GroupForm {
  id: string;
  name: string;
  icon: string;
  color: string;
}

interface NewRuleForm {
  pattern: string;
  matchType: string;
  categoryId: string;
  payeeId: string | null;
  priority: number;
}

const EMPTY_CATEGORY_FORM: CategoryForm = {
  name: "",
  icon: "tag",
  color: "#06b6d4",
  groupId: "",
};

const EMPTY_GROUP_FORM: GroupForm = {
  id: "",
  name: "",
  icon: "folder",
  color: "#64748b",
};

const EMPTY_NEW_RULE: NewRuleForm = {
  pattern: "",
  matchType: "contains",
  categoryId: "",
  payeeId: "",
  priority: 0,
};

const NO_GROUP = "none";
const NO_PAYEE = "none";
const NO_CATEGORY = "none";

export default function Categories() {
  const [categories, setCategories] = useState<Category[]>([]);
  const [groups, setGroups] = useState<CategoryGroup[]>([]);
  const [rules, setRules] = useState<Rule[]>([]);
  const [payees, setPayees] = useState<Payee[]>([]);
  const [tab, setTab] = useState<"groups" | "categories" | "rules">("groups");
  const [showNewCategory, setShowNewCategory] = useState(false);
  const [editingCategory, setEditingCategory] = useState<Category | null>(null);
  const [globalCategoryMode, setGlobalCategoryMode] = useState(false);
  const [showGroupForm, setShowGroupForm] = useState(false);
  const [editingGroup, setEditingGroup] = useState<CategoryGroup | null>(null);
  const [globalGroupMode, setGlobalGroupMode] = useState(false);
  const [showNewRule, setShowNewRule] = useState(false);
  const [editingRule, setEditingRule] = useState<Rule | null>(null);
  const [catForm, setCatForm] = useState<CategoryForm>(EMPTY_CATEGORY_FORM);
  const [groupForm, setGroupForm] = useState<GroupForm>(EMPTY_GROUP_FORM);
  const [newRule, setNewRule] = useState<NewRuleForm>(EMPTY_NEW_RULE);
  const [applyResult, setApplyResult] = useState<ApplyRulesResult | null>(null);
  const [deleteResult, setDeleteResult] = useState<string | null>(null);
  const { compactLayout } = useSettings();
  const { user } = useAuth();
  const isAdmin = user?.role === "admin";
  const applyTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const deleteTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const loadCategories = () =>
    api.getCategories().then(setCategories).catch(console.error);
  const loadGroups = () => api.getGroups().then(setGroups).catch(console.error);

  useEffect(() => {
    loadCategories();
    loadGroups();
    api.getRules().then(setRules).catch(console.error);
    api.getPayees().then(setPayees).catch(console.error);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    return () => {
      if (applyTimerRef.current) clearTimeout(applyTimerRef.current);
      if (deleteTimerRef.current) clearTimeout(deleteTimerRef.current);
    };
  }, []);

  const flash = (msg: string) => {
    setDeleteResult(msg);
    if (deleteTimerRef.current) clearTimeout(deleteTimerRef.current);
    deleteTimerRef.current = setTimeout(() => setDeleteResult(null), 4000);
  };

  // ---- Categories CRUD ----

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
      loadCategories();
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const handleDeleteCategory = async (cat: Category) => {
    try {
      const result = cat.isGlobal
        ? await api.deleteGlobalCategory(cat.id)
        : await api.deleteCategory(cat.id);
      loadCategories();
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

  // ---- Groups CRUD ----

  const openNewGroup = (globalMode: boolean) => {
    setEditingGroup(null);
    setGlobalGroupMode(globalMode);
    setGroupForm(EMPTY_GROUP_FORM);
    setShowGroupForm(true);
  };

  const openEditGroup = (g: CategoryGroup) => {
    setEditingGroup(g);
    setGlobalGroupMode(false);
    setGroupForm({
      id: g.id,
      name: g.name,
      icon: g.icon,
      color: g.color,
    });
    setShowGroupForm(true);
  };

  const handleSaveGroup = async () => {
    try {
      if (editingGroup) {
        await api.updateGroup(editingGroup.id, {
          name: groupForm.name,
          icon: groupForm.icon,
          color: groupForm.color,
        });
      } else if (globalGroupMode) {
        await api.createGlobalGroup(groupForm);
      } else {
        await api.createGroup(groupForm);
      }
      setShowGroupForm(false);
      setEditingGroup(null);
      loadGroups();
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const handleDeleteGroup = async (g: CategoryGroup) => {
    try {
      await api.deleteGroup(g.id);
      loadGroups();
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  // ---- Rules ----

  const handleUpsertRule = async () => {
    const payload: CreateRuleRequest = {
      ...newRule,
      payeeId: newRule.payeeId || null,
    };

    try {
      if (editingRule) {
        await api.updateRule(editingRule.id, payload);
      } else {
        await api.createRule(payload);
      }
      setShowNewRule(false);
      setEditingRule(null);
      setNewRule(EMPTY_NEW_RULE);
      const updatedRules = await api.getRules();
      setRules(updatedRules);
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const handleDeleteRule = async (id: string) => {
    try {
      await api.deleteRule(id);
      setRules((prev) => prev.filter((r) => r.id !== id));
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const handleApplyRules = async () => {
    try {
      const result = await api.applyRules();
      setApplyResult(result);
      if (applyTimerRef.current) clearTimeout(applyTimerRef.current);
      applyTimerRef.current = setTimeout(() => setApplyResult(null), 3000);
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const categorySections = buildCategorySections(groups, categories);

  return (
    <>
      <div className="shrink-0 px-8 pt-6">
        <h1 className="text-2xl font-bold mb-1 text-foreground">
          Categories & Rules
        </h1>
        <p className="text-muted-foreground text-sm">
          Manage transaction groups, categories and auto-categorization rules
        </p>
      </div>
      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto w-full">
        <Tabs
          className="gap-0"
          value={tab}
          onValueChange={(v) => setTab(v as typeof tab)}
        >
          <div
            className={`${compactLayout ? "mb-4" : "mb-6"} border-b border-border`}
          >
            <TabsList
              variant="line"
              className={`${compactLayout ? "gap-1" : "gap-2"}`}
            >
              <TabsTrigger value="groups">Groups</TabsTrigger>
              <TabsTrigger value="categories">Categories</TabsTrigger>
              <TabsTrigger value="rules">Rules</TabsTrigger>
            </TabsList>
          </div>

          {deleteResult && (
            <div className="mb-4 px-4 py-2.5 bg-emerald-500/10 border border-emerald-500/20 rounded-lg text-sm text-emerald-400">
              {deleteResult}
            </div>
          )}

          <TabsContent value="groups">
            <div className="flex justify-between items-center mb-5 flex-wrap gap-4">
              <span className="text-sm text-muted-foreground">
                {groups.length} groups
              </span>
              <div className="flex gap-3">
                {isAdmin && (
                  <Button variant="outline" onClick={() => openNewGroup(true)}>
                    <Globe /> Add Global Group
                  </Button>
                )}
                <Button onClick={() => openNewGroup(false)}>
                  <Plus /> Add Group
                </Button>
              </div>
            </div>

            <Dialog
              open={showGroupForm}
              onOpenChange={(open) => {
                setShowGroupForm(open);
                if (!open) {
                  setEditingGroup(null);
                  setGlobalGroupMode(false);
                }
              }}
            >
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
                    <Label className="text-xs text-muted-foreground">
                      Name
                    </Label>
                    <Input
                      placeholder="e.g. Vacation"
                      value={groupForm.name}
                      onChange={(e) =>
                        setGroupForm({ ...groupForm, name: e.target.value })
                      }
                    />
                  </div>
                  <div className="flex flex-col gap-1.5">
                    <Label className="text-xs text-muted-foreground">
                      Icon
                    </Label>
                    <Input
                      placeholder="e.g. plane"
                      value={groupForm.icon}
                      onChange={(e) =>
                        setGroupForm({ ...groupForm, icon: e.target.value })
                      }
                    />
                  </div>
                  {!editingGroup && (
                    <div className="flex flex-col gap-1.5">
                      <Label className="text-xs text-muted-foreground">
                        ID (slug)
                      </Label>
                      <Input
                        placeholder="e.g. vacation"
                        value={groupForm.id}
                        onChange={(e) =>
                          setGroupForm({
                            ...groupForm,
                            id: e.target.value
                              .toLowerCase()
                              .replace(/\s+/g, "_"),
                          })
                        }
                      />
                    </div>
                  )}
                  <div className="flex flex-col gap-1.5">
                    <Label className="text-xs text-muted-foreground">
                      Color
                    </Label>
                    <input
                      type="color"
                      value={groupForm.color}
                      onChange={(e) =>
                        setGroupForm({ ...groupForm, color: e.target.value })
                      }
                      className="w-full h-10.5 cursor-pointer bg-background border border-border rounded-lg p-1"
                    />
                  </div>
                </div>
                <div className="flex justify-end">
                  <Button
                    onClick={handleSaveGroup}
                    disabled={
                      !groupForm.name || (!editingGroup && !groupForm.id)
                    }
                  >
                    {editingGroup ? "Update Group" : "Create Group"}
                  </Button>
                </div>
              </DialogContent>
            </Dialog>

            <div
              className={`grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4 ${compactLayout ? "gap-2" : "gap-3"}`}
            >
              {groups.map((g) => {
                const editable = !g.isBase && !g.isGlobal;
                return (
                  <div
                    key={g.id}
                    className={`flex items-center gap-3 ${compactLayout ? "px-3 py-1.5" : "px-4 py-3"} bg-card border border-border rounded-lg`}
                  >
                    <span
                      className="w-3 h-3 rounded-full shrink-0"
                      style={{ background: g.color }}
                    />
                    <span className="flex-1 text-sm font-medium text-foreground min-w-0">
                      <span className="block truncate">{g.name}</span>
                      <span className="text-xs font-normal text-muted-foreground flex items-center gap-1">
                        {g.isBase ? (
                          <>
                            <Lock size={10} /> Base
                          </>
                        ) : g.isGlobal ? (
                          <>
                            <Globe size={10} /> Global
                          </>
                        ) : (
                          "Custom"
                        )}
                      </span>
                    </span>
                    {editable && (
                      <>
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          className="text-muted-foreground hover:text-primary"
                          onClick={() => openEditGroup(g)}
                        >
                          <Edit2 />
                        </Button>
                        <AlertDialog>
                          <AlertDialogTrigger asChild>
                            <Button
                              variant="ghost"
                              size="icon-sm"
                              className="text-muted-foreground hover:text-destructive"
                            >
                              <Trash2 />
                            </Button>
                          </AlertDialogTrigger>
                          <AlertDialogContent>
                            <AlertDialogHeader>
                              <AlertDialogTitle>Delete group?</AlertDialogTitle>
                              <AlertDialogDescription>
                                Delete group "{g.name}"? Only empty groups can
                                be deleted — move or remove its categories
                                first.
                              </AlertDialogDescription>
                            </AlertDialogHeader>
                            <AlertDialogFooter>
                              <AlertDialogCancel>Cancel</AlertDialogCancel>
                              <AlertDialogAction
                                variant="destructive"
                                onClick={() => handleDeleteGroup(g)}
                              >
                                Delete
                              </AlertDialogAction>
                            </AlertDialogFooter>
                          </AlertDialogContent>
                        </AlertDialog>
                      </>
                    )}
                  </div>
                );
              })}
            </div>
          </TabsContent>

          <TabsContent value="categories">
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

            <Dialog
              open={showNewCategory}
              onOpenChange={(open) => {
                setShowNewCategory(open);
                if (!open) {
                  setEditingCategory(null);
                  setGlobalCategoryMode(false);
                }
              }}
            >
              <DialogContent className="sm:max-w-2xl">
                <DialogHeader>
                  <DialogTitle>
                    {editingCategory
                      ? editingCategory.isGlobal
                        ? "Edit Global Category"
                        : "Edit Category"
                      : globalCategoryMode
                        ? "New Global Category"
                        : "New Category"}
                  </DialogTitle>
                </DialogHeader>
                <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                  <div className="flex flex-col gap-1.5">
                    <Label className="text-xs text-muted-foreground">
                      Name
                    </Label>
                    <Input
                      placeholder="e.g. Gym"
                      value={catForm.name}
                      onChange={(e) =>
                        setCatForm({ ...catForm, name: e.target.value })
                      }
                    />
                  </div>
                  <div className="flex flex-col gap-1.5">
                    <Label className="text-xs text-muted-foreground">
                      Group
                    </Label>
                    <Select
                      value={catForm.groupId || NO_GROUP}
                      onValueChange={(v) =>
                        setCatForm({
                          ...catForm,
                          groupId: v === NO_GROUP ? "" : v,
                        })
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
                    <Label className="text-xs text-muted-foreground">
                      Color
                    </Label>
                    <input
                      type="color"
                      value={catForm.color}
                      onChange={(e) =>
                        setCatForm({ ...catForm, color: e.target.value })
                      }
                      className="w-full h-10.5 cursor-pointer bg-background border border-border rounded-lg p-1"
                    />
                  </div>
                  <div className="flex flex-col gap-1.5">
                    <Label className="text-xs text-muted-foreground">
                      Icon
                    </Label>
                    <Input
                      placeholder="e.g. dumbbell"
                      value={catForm.icon}
                      onChange={(e) =>
                        setCatForm({ ...catForm, icon: e.target.value })
                      }
                    />
                  </div>
                </div>
                <div className="flex justify-end">
                  <Button
                    onClick={handleSaveCategory}
                    disabled={!catForm.name || !catForm.groupId}
                  >
                    {editingCategory ? "Update Category" : "Create Category"}
                  </Button>
                </div>
              </DialogContent>
            </Dialog>

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
                                    Delete category "{cat.name}"? Any
                                    transactions using it will be uncategorized,
                                    and any rules pointing to it will be
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
                          </>
                        )}
                      </div>
                    ))}
                  </div>
                </div>
              ))
            )}
          </TabsContent>

          <TabsContent value="rules">
            <div className="flex justify-between items-center mb-5 flex-wrap gap-4">
              <div className="flex items-center gap-4">
                <span className="text-sm text-muted-foreground">
                  {rules.length} rules
                </span>
                <Button variant="outline" onClick={handleApplyRules}>
                  <Play /> Apply Rules to Uncategorized
                </Button>
                {applyResult && (
                  <span className="text-sm font-medium text-emerald-500">
                    {applyResult.updated} transactions updated
                  </span>
                )}
              </div>
              <Button
                onClick={() => {
                  setEditingRule(null);
                  setNewRule(EMPTY_NEW_RULE);
                  setShowNewRule(true);
                }}
              >
                <Plus /> Add Rule
              </Button>
            </div>

            <Dialog
              open={showNewRule}
              onOpenChange={(open) => {
                setShowNewRule(open);
                if (!open) {
                  setEditingRule(null);
                  setNewRule(EMPTY_NEW_RULE);
                }
              }}
            >
              <DialogContent className="sm:max-w-2xl">
                <DialogHeader>
                  <DialogTitle>
                    {editingRule ? "Edit Rule" : "New Rule"}
                  </DialogTitle>
                </DialogHeader>
                <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                  <div className="flex flex-col gap-1.5">
                    <Label className="text-xs text-muted-foreground">
                      Pattern
                    </Label>
                    <Input
                      placeholder="e.g. SWIGGY, AMAZON, UBER"
                      value={newRule.pattern}
                      onChange={(e) =>
                        setNewRule({ ...newRule, pattern: e.target.value })
                      }
                    />
                  </div>
                  <div className="flex flex-col gap-1.5">
                    <Label className="text-xs text-muted-foreground">
                      Match Type
                    </Label>
                    <Select
                      value={newRule.matchType}
                      onValueChange={(v) =>
                        setNewRule({ ...newRule, matchType: v })
                      }
                    >
                      <SelectTrigger className="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="contains">Contains</SelectItem>
                        <SelectItem value="starts_with">
                          Starts With
                        </SelectItem>
                        <SelectItem value="exact">Exact Match</SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                  <div className="flex flex-col gap-1.5">
                    <Label className="text-xs text-muted-foreground">
                      Assign Category
                    </Label>
                    <Select
                      value={newRule.categoryId || NO_CATEGORY}
                      onValueChange={(v) =>
                        setNewRule({
                          ...newRule,
                          categoryId: v === NO_CATEGORY ? "" : v,
                        })
                      }
                    >
                      <SelectTrigger className="w-full">
                        <SelectValue placeholder="Choose category..." />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value={NO_CATEGORY}>
                          Choose category...
                        </SelectItem>
                        {buildCategorySections(groups, categories).map((s) => (
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
                      value={newRule.payeeId || NO_PAYEE}
                      onValueChange={(v) =>
                        setNewRule({
                          ...newRule,
                          payeeId: v === NO_PAYEE ? null : v,
                        })
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
                      value={newRule.priority}
                      onChange={(e) =>
                        setNewRule({
                          ...newRule,
                          priority: parseInt(e.target.value) || 0,
                        })
                      }
                    />
                  </div>
                </div>
                <div className="flex justify-end">
                  <Button
                    onClick={handleUpsertRule}
                    disabled={!newRule.pattern || !newRule.categoryId}
                  >
                    {editingRule ? "Update Rule" : "Create Rule"}
                  </Button>
                </div>
              </DialogContent>
            </Dialog>

            <div className="bg-card border border-border rounded-xl overflow-x-auto">
              <table className="w-full text-left border-collapse">
                <thead>
                  <tr>
                    {["Pattern", "Match", "Category", "Payee", "Priority", ""].map(
                      (h, i) => (
                        <th
                          key={i}
                          className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-xs font-semibold uppercase tracking-wider text-muted-foreground bg-muted/50 border-b border-border whitespace-nowrap ${i === 5 ? "w-12.5" : ""}`}
                        >
                          {h}
                        </th>
                      ),
                    )}
                  </tr>
                </thead>
                <tbody>
                  {rules.length === 0 ? (
                    <tr>
                      <td
                        colSpan={6}
                        className="text-center p-10 text-muted-foreground"
                      >
                        No rules yet. Create one to auto-categorize
                        transactions.
                      </td>
                    </tr>
                  ) : (
                    rules.map((r) => (
                      <tr
                        key={r.id}
                        className="hover:bg-muted/30 transition-colors border-b border-border last:border-0"
                      >
                        <td
                          className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm font-medium text-foreground`}
                        >
                          "{r.pattern}"
                        </td>
                        <td
                          className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm`}
                        >
                          <Badge
                            variant="outline"
                            className="capitalize text-muted-foreground"
                          >
                            {r.matchType.replace("_", " ")}
                          </Badge>
                        </td>
                        <td
                          className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm text-foreground`}
                        >
                          {r.categoryName}
                        </td>
                        <td
                          className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm text-muted-foreground`}
                        >
                          {r.payee || "—"}
                        </td>
                        <td
                          className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-sm text-muted-foreground`}
                        >
                          {r.priority}
                        </td>
                        <td
                          className={`${compactLayout ? "py-1.5 px-3" : "py-3 px-4"} text-right`}
                        >
                          <div className="flex justify-end gap-1">
                            <Button
                              variant="ghost"
                              size="icon-sm"
                              className="text-muted-foreground hover:text-primary"
                              onClick={() => {
                                setEditingRule(r);
                                setNewRule({
                                  pattern: r.pattern,
                                  matchType: r.matchType,
                                  categoryId: r.categoryId,
                                  payeeId: r.payeeId ?? null,
                                  priority: r.priority,
                                });
                                setShowNewRule(true);
                              }}
                            >
                              <Edit2 />
                            </Button>
                            <AlertDialog>
                              <AlertDialogTrigger asChild>
                                <Button
                                  variant="ghost"
                                  size="icon-sm"
                                  className="text-muted-foreground hover:text-destructive"
                                >
                                  <Trash2 />
                                </Button>
                              </AlertDialogTrigger>
                              <AlertDialogContent>
                                <AlertDialogHeader>
                                  <AlertDialogTitle>
                                    Delete rule?
                                  </AlertDialogTitle>
                                  <AlertDialogDescription>
                                    Delete this rule? It will no longer
                                    auto-categorize matching transactions.
                                  </AlertDialogDescription>
                                </AlertDialogHeader>
                                <AlertDialogFooter>
                                  <AlertDialogCancel>Cancel</AlertDialogCancel>
                                  <AlertDialogAction
                                    variant="destructive"
                                    onClick={() => handleDeleteRule(r.id)}
                                  >
                                    Delete
                                  </AlertDialogAction>
                                </AlertDialogFooter>
                              </AlertDialogContent>
                            </AlertDialog>
                          </div>
                        </td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>
          </TabsContent>
        </Tabs>
      </div>
    </>
  );
}