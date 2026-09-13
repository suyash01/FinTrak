import { useState, type FormEvent } from "react";
import { Plus, Edit2, Trash2, X } from "lucide-react";
import { toast } from "sonner";
import api from "../../api/client";
import { useDomainData } from "../../context/DomainDataContext";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Spinner } from "@/components/ui/spinner";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
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

interface AccountTypeForm {
  id: string;
  name: string;
  positiveTxnType: string;
}

const EMPTY_ACCOUNT_TYPE_FORM: AccountTypeForm = {
  id: "",
  name: "",
  positiveTxnType: "credit",
};

export default function AccountTypesManager() {
  const {
    accountTypes: types,
    loading,
    refreshAccountTypes,
  } = useDomainData();
  const [editingId, setEditingId] = useState<string | null>(null);
  const [formData, setFormData] = useState<AccountTypeForm>(
    EMPTY_ACCOUNT_TYPE_FORM,
  );

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    try {
      if (editingId) {
        await api.updateAccountType(editingId, {
          name: formData.name,
          positiveTxnType: formData.positiveTxnType,
        });
      } else {
        await api.createAccountType(formData);
      }
      setEditingId(null);
      setFormData(EMPTY_ACCOUNT_TYPE_FORM);
      refreshAccountTypes();
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const handleDelete = async (id: string) => {
    try {
      await api.deleteAccountType(id);
      toast.success("Account type deleted");
      refreshAccountTypes();
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  if (loading)
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Spinner className="size-4" /> Loading...
      </div>
    );

  return (
    <div className="space-y-4">
      <div className="divide-y divide-border border border-border rounded-lg overflow-hidden bg-background">
        {types.map((t) => (
          <div
            key={t.id}
            className="p-3 flex justify-between items-center group hover:bg-accent transition-colors"
          >
            {editingId === t.id ? (
              <form
                onSubmit={handleSubmit}
                className="flex gap-2 w-full items-center"
              >
                <Input
                  required
                  className="flex-1 h-8"
                  value={formData.name}
                  onChange={(e) =>
                    setFormData({ ...formData, name: e.target.value })
                  }
                />
                <Select
                  value={formData.positiveTxnType}
                  onValueChange={(v) =>
                    setFormData({ ...formData, positiveTxnType: v })
                  }
                >
                  <SelectTrigger size="sm" className="h-8">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="credit">Credit is positive</SelectItem>
                    <SelectItem value="debit">Debit is positive</SelectItem>
                  </SelectContent>
                </Select>
                <Button
                  type="submit"
                  variant="ghost"
                  size="icon-sm"
                  aria-label="Save account type"
                >
                  <Plus />
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  aria-label="Cancel"
                  onClick={() => setEditingId(null)}
                >
                  <X />
                </Button>
              </form>
            ) : (
              <>
                <div>
                  <div className="font-medium text-sm text-foreground">
                    {t.name}
                  </div>
                  <div className="text-xs text-muted-foreground">
                    ID: {t.id} • Positive:{" "}
                    <Badge variant="secondary" className="uppercase text-[10px]">
                      {t.positiveTxnType}
                    </Badge>
                  </div>
                </div>
                <div className="flex opacity-0 group-hover:opacity-100 transition-opacity">
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label={`Edit ${t.name}`}
                    onClick={() => {
                      setEditingId(t.id);
                      setFormData(t);
                    }}
                  >
                    <Edit2 />
                  </Button>
                  <AlertDialog>
                    <AlertDialogTrigger asChild>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={`Delete ${t.name}`}
                        className="text-muted-foreground hover:text-destructive"
                      >
                        <Trash2 />
                      </Button>
                    </AlertDialogTrigger>
                    <AlertDialogContent>
                      <AlertDialogHeader>
                        <AlertDialogTitle>
                          Delete account type?
                        </AlertDialogTitle>
                        <AlertDialogDescription>
                          Delete "{t.name}"? This will fail if accounts use it.
                        </AlertDialogDescription>
                      </AlertDialogHeader>
                      <AlertDialogFooter>
                        <AlertDialogCancel>Cancel</AlertDialogCancel>
                        <AlertDialogAction
                          variant="destructive"
                          onClick={() => handleDelete(t.id)}
                        >
                          Delete
                        </AlertDialogAction>
                      </AlertDialogFooter>
                    </AlertDialogContent>
                  </AlertDialog>
                </div>
              </>
            )}
          </div>
        ))}
      </div>

      {!editingId && editingId !== "new" && (
        <Button
          variant="ghost"
          size="sm"
          onClick={() => {
            setEditingId("new");
            setFormData(EMPTY_ACCOUNT_TYPE_FORM);
          }}
        >
          <Plus /> Add Account Type
        </Button>
      )}

      {editingId === "new" && (
        <form
          onSubmit={handleSubmit}
          className="p-3 border border-border rounded-lg bg-background space-y-3"
        >
          <div className="space-y-1.5">
            <Label className="text-xs text-muted-foreground">
              Type ID (Code)
            </Label>
            <Input
              required
              placeholder="e.g. wallet, cash"
              value={formData.id}
              onChange={(e) =>
                setFormData({ ...formData, id: e.target.value })
              }
            />
          </div>
          <div className="space-y-1.5">
            <Label className="text-xs text-muted-foreground">
              Display Name
            </Label>
            <Input
              required
              placeholder="e.g. Mobile Wallet"
              value={formData.name}
              onChange={(e) =>
                setFormData({ ...formData, name: e.target.value })
              }
            />
          </div>
          <div className="space-y-1.5">
            <Label className="text-xs text-muted-foreground">
              Sign Convention
            </Label>
            <Select
              value={formData.positiveTxnType}
              onValueChange={(v) =>
                setFormData({ ...formData, positiveTxnType: v })
              }
            >
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="credit">
                  Credit amounts increase balance
                </SelectItem>
                <SelectItem value="debit">
                  Debit amounts increase balance
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="flex gap-2 justify-end pt-1">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => setEditingId(null)}
            >
              Cancel
            </Button>
            <Button type="submit" size="sm">
              Create
            </Button>
          </div>
        </form>
      )}
    </div>
  );
}
