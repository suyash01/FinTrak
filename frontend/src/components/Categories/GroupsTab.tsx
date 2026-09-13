import { useState } from "react";
import { Plus, Trash2, Edit2, Globe, Lock } from "lucide-react";
import api from "../../api/client";
import { useSettings } from "../../context/SettingsContext";
import { useAuth } from "../../context/AuthContext";
import { useDomainData } from "../../context/DomainDataContext";
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
import type { CategoryGroup } from "../../types";
import GroupDialog from "./GroupDialog";
import { EMPTY_GROUP_FORM, type GroupForm } from "./categoryForms";

export default function GroupsTab() {
  const { groups, refreshGroups } = useDomainData();
  const { compactLayout } = useSettings();
  const { user } = useAuth();
  const isAdmin = user?.role === "admin";

  const [showGroupForm, setShowGroupForm] = useState(false);
  const [editingGroup, setEditingGroup] = useState<CategoryGroup | null>(null);
  const [globalGroupMode, setGlobalGroupMode] = useState(false);
  const [groupForm, setGroupForm] = useState<GroupForm>(EMPTY_GROUP_FORM);

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
      refreshGroups();
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const handleDeleteGroup = async (g: CategoryGroup) => {
    try {
      await api.deleteGroup(g.id);
      refreshGroups();
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  return (
    <>
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

      <GroupDialog
        open={showGroupForm}
        onOpenChange={(open) => {
          setShowGroupForm(open);
          if (!open) {
            setEditingGroup(null);
            setGlobalGroupMode(false);
          }
        }}
        editingGroup={editingGroup}
        globalGroupMode={globalGroupMode}
        form={groupForm}
        onChange={setGroupForm}
        onSubmit={handleSaveGroup}
      />

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
                    aria-label={`Edit ${g.name}`}
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
                        aria-label={`Delete ${g.name}`}
                        className="text-muted-foreground hover:text-destructive"
                      >
                        <Trash2 />
                      </Button>
                    </AlertDialogTrigger>
                    <AlertDialogContent>
                      <AlertDialogHeader>
                        <AlertDialogTitle>Delete group?</AlertDialogTitle>
                        <AlertDialogDescription>
                          Delete group "{g.name}"? Only empty groups can be
                          deleted — move or remove its categories first.
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
    </>
  );
}
