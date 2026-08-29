import { BrowserRouter, Routes, Route } from "react-router-dom";
import Sidebar from "./components/Layout/Sidebar";
import Dashboard from "./components/Dashboard/Dashboard";
import Import from "./components/Import/Import";
import PaperlessImport from "./components/PaperlessImport/PaperlessImport";
import Transactions from "./components/Transactions/Transactions";
import Accounts from "./components/Accounts/Accounts";
import Categories from "./components/Categories/Categories";
import Payees from "./components/Payees/Payees";
import Linking from "./components/Linking/Linking";
import Login from "./components/Auth/Login";
import { SettingsProvider, useSettings } from "./context/SettingsContext";
import { AuthProvider, useAuth } from "./context/AuthContext";
import {
  ThemeProvider,
  useTheme,
  THEME_MODES,
  ACCENT_THEMES,
  ACCENT_COLORS,
} from "./context/ThemeContext";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { Toaster } from "@/components/ui/sonner";
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
import { toast } from "sonner";
import "./index.css";
import { useState, useEffect, type FormEvent } from "react";
import api from "./api/client";
import { Trash2, Edit2, Plus, X } from "lucide-react";
import type { AccountType } from "./types";

function Settings() {
  const { compactLayout, toggleCompactLayout } = useSettings();
  const { user } = useAuth();
  const { mode, accent, setMode, setAccent } = useTheme();

  return (
    <>
      <div className="shrink-0 px-8 pt-6">
        <h1 className="text-2xl font-bold mb-1">Settings</h1>
        <p className="text-muted-foreground text-sm">Application preferences</p>
      </div>
      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto space-y-6">
        <div className="bg-card border border-border rounded-xl p-6 max-w-125">
          <h3 className="text-base font-semibold mb-1">Theme</h3>
          <p className="text-[13px] text-muted-foreground mb-4">
            Choose between light and dark mode, and pick an accent color.
          </p>
          <div className="space-y-5">
            <div>
              <div className="text-sm font-medium mb-2">Mode</div>
              <div className="flex gap-2">
                {THEME_MODES.map((m) => (
                  <Button
                    key={m}
                    variant={mode === m ? "default" : "secondary"}
                    size="sm"
                    onClick={() => setMode(m)}
                    className="capitalize"
                  >
                    {m}
                  </Button>
                ))}
              </div>
            </div>
            <div>
              <div className="text-sm font-medium mb-2">Accent Color</div>
              <div className="flex gap-2.5">
                {ACCENT_THEMES.map((a) => (
                  <button
                    key={a}
                    onClick={() => setAccent(a)}
                    aria-label={`${a} accent`}
                    title={a}
                    className={`h-7 w-7 rounded-full transition-transform hover:scale-110 ${
                      accent === a
                        ? "ring-2 ring-ring ring-offset-2 ring-offset-background"
                        : ""
                    }`}
                    style={{ backgroundColor: ACCENT_COLORS[a] }}
                  />
                ))}
              </div>
            </div>
          </div>
        </div>

        <div className="bg-card border border-border rounded-xl p-6 max-w-125">
          <h3 className="text-base font-semibold mb-4">Display Preferences</h3>
          <div className="flex items-center justify-between">
            <div>
              <div className="text-sm font-medium text-foreground">
                Compact Layout
              </div>
              <div className="text-[13px] text-muted-foreground">
                Reduce padding and spacing to show more data
              </div>
            </div>
            <Switch
              checked={compactLayout}
              onCheckedChange={toggleCompactLayout}
            />
          </div>
        </div>

        {/* Account Types Management (admin-only) */}
        {user?.role === "admin" && (
          <div className="bg-card border border-border rounded-xl p-6 max-w-125">
            <h3 className="text-base font-semibold mb-4">Account Types</h3>
            <AccountTypesManager />
          </div>
        )}

        {/* Paperless-ngx integration */}
        <div className="bg-card border border-border rounded-xl p-6 max-w-125">
          <h3 className="text-base font-semibold mb-1">Paperless-ngx</h3>
          <p className="text-[13px] text-muted-foreground mb-4">
            Connect a Paperless-ngx instance to pull statement PDFs. The import
            UI appears once both a URL and API token are set.
          </p>
          <PaperlessSettingsManager />
        </div>

        <div className="bg-card border border-border rounded-xl p-6 max-w-125">
          <h3 className="text-base font-semibold mb-4">About FinTrak</h3>
          <p className="text-muted-foreground text-sm leading-relaxed">
            FinTrak helps you consolidate bank and credit card statements,
            categorize transactions, and track transfers, cashbacks, refunds,
            and bill payments — all in one place.
          </p>
          <div className="mt-4 text-[13px] text-muted-foreground">
            Version 0.1.0-alpha · Built with Go + React
          </div>
        </div>
      </div>
    </>
  );
}

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

function AccountTypesManager() {
  const [types, setTypes] = useState<AccountType[]>([]);
  const [loading, setLoading] = useState(true);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [formData, setFormData] = useState<AccountTypeForm>(
    EMPTY_ACCOUNT_TYPE_FORM,
  );

  useEffect(() => {
    fetchTypes();
  }, []);

  const fetchTypes = async () => {
    try {
      const data = await api.getAccountTypes();
      setTypes(data);
    } catch (err) {
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

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
      fetchTypes();
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const handleDelete = async (id: string) => {
    try {
      await api.deleteAccountType(id);
      toast.success("Account type deleted");
      fetchTypes();
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
                <Button type="submit" variant="ghost" size="icon-sm">
                  <Plus />
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
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

function PaperlessSettingsManager() {
  const [url, setUrl] = useState("");
  const [token, setToken] = useState("");
  const [tokenSet, setTokenSet] = useState(false);
  const [tag, setTag] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    api
      .getPaperlessSettings()
      .then((s) => {
        setUrl(s.paperlessUrl || "");
        setToken("");
        setTokenSet(Boolean(s.hasToken));
        setTag(s.paperlessTag || "");
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  const handleSave = async (e: FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setSaved(false);
    try {
      const payload: {
        paperlessUrl: string;
        paperlessTag: string;
        paperlessToken?: string;
      } = {
        paperlessUrl: url,
        paperlessTag: tag,
      };
      if (token.trim() !== "") {
        payload.paperlessToken = token;
      }
      await api.updatePaperlessSettings(payload);
      setToken("");
      setTokenSet(Boolean(token.trim() !== "" || tokenSet));
      setSaved(true);
      toast.success("Paperless settings saved");
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setSaving(false);
    }
  };

  if (loading)
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Spinner className="size-4" /> Loading...
      </div>
    );

  return (
    <form onSubmit={handleSave} className="space-y-3">
      <div className="space-y-1.5">
        <Label className="text-xs text-muted-foreground">Paperless URL</Label>
        <Input
          type="text"
          placeholder="http://localhost:8000"
          value={url}
          onChange={(e) => setUrl(e.target.value)}
        />
      </div>
      <div className="space-y-1.5">
        <Label className="text-xs text-muted-foreground">API Token</Label>
        <Input
          type="password"
          placeholder={
            tokenSet
              ? "Leave blank to keep the saved token"
              : "Paperless-ngx API token"
          }
          value={token}
          onChange={(e) => setToken(e.target.value)}
        />
        <p className="text-[11px] text-muted-foreground">
          {tokenSet
            ? "An API token is saved. Enter a new one only to replace it."
            : "The token is stored encrypted and is never shown again."}
        </p>
      </div>
      <div className="space-y-1.5">
        <Label className="text-xs text-muted-foreground">
          Import Tag Label
        </Label>
        <Input
          type="text"
          placeholder="e.g. fintrak"
          value={tag}
          onChange={(e) => setTag(e.target.value)}
        />
        <p className="text-[11px] text-muted-foreground">
          When enabled during import, successfully imported documents get tagged
          with this label in Paperless-ngx.
        </p>
      </div>
      <div className="flex items-center gap-3 pt-1">
        <Button type="submit" size="sm" disabled={saving}>
          {saving ? "Saving..." : "Save"}
        </Button>
        {saved && (
          <Badge variant="secondary" className="text-emerald-500">
            Saved
          </Badge>
        )}
      </div>
    </form>
  );
}

export default function App() {
  return (
    <ThemeProvider>
      <BrowserRouter>
        <SettingsProvider>
          <AuthProvider>
            <Root />
          </AuthProvider>
        </SettingsProvider>
      </BrowserRouter>
      <Toaster />
    </ThemeProvider>
  );
}

function Root() {
  const { isAuthenticated } = useAuth();

  if (!isAuthenticated) {
    return (
      <Routes>
        <Route path="*" element={<Login />} />
      </Routes>
    );
  }

  return (
    <div className="flex h-screen w-screen overflow-hidden">
      <Sidebar />
      <main className="flex-1 flex flex-col overflow-hidden min-w-0">
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/import" element={<Import />} />
          <Route path="/paperless" element={<PaperlessImport />} />
          <Route path="/transactions" element={<Transactions />} />
          <Route path="/accounts" element={<Accounts />} />
          <Route path="/categories" element={<Categories />} />
          <Route path="/payees" element={<Payees />} />
          <Route path="/linking" element={<Linking />} />
          <Route path="/settings" element={<Settings />} />
          <Route path="*" element={<Dashboard />} />
        </Routes>
      </main>
    </div>
  );
}