import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
  type Dispatch,
  type ReactNode,
  type SetStateAction,
} from "react";
import { toast } from "sonner";
import api from "../api/client";
import { getOfflineSnapshot, subscribeOffline } from "../api/offlineStatus";
import type {
  Account,
  AccountType,
  Category,
  CategoryGroup,
  Payee,
  UserSettings,
} from "../types";

// DomainResource names each independently-loaded lookup resource whose load
// state is tracked.
export type DomainResource =
  | "accounts"
  | "accountTypes"
  | "categories"
  | "groups"
  | "payees"
  | "settings";

// RESOURCE_LABELS names a resource the way the user sees it, for the toast that
// reports a failed load.
const RESOURCE_LABELS: Record<DomainResource, string> = {
  accounts: "Accounts",
  accountTypes: "Account types",
  categories: "Categories",
  groups: "Groups",
  payees: "Payees",
  settings: "Settings",
};

interface DomainDataContextValue {
  accounts: Account[];
  accountTypes: AccountType[];
  categories: Category[];
  groups: CategoryGroup[];
  payees: Payee[];
  settings: UserSettings | null;
  loading: boolean;
  // errors carries a per-resource message after a failed load. An empty array
  // plus no error means a successful (possibly empty) response; consumers must
  // not render an empty state when the matching error is set.
  errors: Partial<Record<DomainResource, string>>;
  setAccounts: Dispatch<SetStateAction<Account[]>>;
  setAccountTypes: Dispatch<SetStateAction<AccountType[]>>;
  setCategories: Dispatch<SetStateAction<Category[]>>;
  setGroups: Dispatch<SetStateAction<CategoryGroup[]>>;
  setPayees: Dispatch<SetStateAction<Payee[]>>;
  setSettings: Dispatch<SetStateAction<UserSettings | null>>;
  refreshAccounts: () => Promise<void>;
  refreshAccountTypes: () => Promise<void>;
  refreshCategories: () => Promise<void>;
  refreshGroups: () => Promise<void>;
  refreshPayees: () => Promise<void>;
  refreshSettings: () => Promise<void>;
  refreshAll: () => Promise<void>;
}

const DomainDataContext = createContext<DomainDataContextValue | undefined>(
  undefined,
);

// DomainDataProvider centralizes the read-mostly lookup data (accounts,
// account/category groups and payees plus the user's Paperless/page-size
// settings) that most pages need on mount. Without it every route re-fetched
// the same lists and the Sidebar re-requested settings on each navigation.
// Data is fetched once per authenticated session and refreshed explicitly after
// mutations via the exposed refresh functions.
export function DomainDataProvider({ children }: { children: ReactNode }) {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [accountTypes, setAccountTypes] = useState<AccountType[]>([]);
  const [categories, setCategories] = useState<Category[]>([]);
  const [groups, setGroups] = useState<CategoryGroup[]>([]);
  const [payees, setPayees] = useState<Payee[]>([]);
  const [settings, setSettings] = useState<UserSettings | null>(null);
  const [loading, setLoading] = useState(true);
  const [errors, setErrors] = useState<
    Partial<Record<DomainResource, string>>
  >({});

  const failResource = useCallback((resource: DomainResource, err: unknown) => {
    const message = (err as Error)?.message || "Failed to load data";
    setErrors((prev) => ({
      ...prev,
      [resource]: message,
    }));
    // AGENTS.md: errors reach the user through a toast. The message also stays
    // in `errors` for a page that renders the resource's own state (an empty
    // payee list must not read as "you have none").
    toast.error(`${RESOURCE_LABELS[resource]} could not be loaded: ${message}`);
  }, []);

  const clearResourceError = useCallback((resource: DomainResource) => {
    setErrors((prev) => {
      if (!(resource in prev)) return prev;
      const next = { ...prev };
      delete next[resource];
      return next;
    });
  }, []);

  const refreshAccounts = useCallback(async () => {
    try {
      const data = await api.getAccounts();
      setAccounts(Array.isArray(data) ? data : []);
      clearResourceError("accounts");
    } catch (err) {
      console.error(err);
      failResource("accounts", err);
    }
  }, [clearResourceError, failResource]);

  const refreshAccountTypes = useCallback(async () => {
    try {
      const data = await api.getAccountTypes();
      setAccountTypes(Array.isArray(data) ? data : []);
      clearResourceError("accountTypes");
    } catch (err) {
      console.error(err);
      failResource("accountTypes", err);
    }
  }, [clearResourceError, failResource]);

  const refreshCategories = useCallback(async () => {
    try {
      const data = await api.getCategories();
      setCategories(Array.isArray(data) ? data : []);
      clearResourceError("categories");
    } catch (err) {
      console.error(err);
      failResource("categories", err);
    }
  }, [clearResourceError, failResource]);

  const refreshGroups = useCallback(async () => {
    try {
      const data = await api.getGroups();
      setGroups(Array.isArray(data) ? data : []);
      clearResourceError("groups");
    } catch (err) {
      console.error(err);
      failResource("groups", err);
    }
  }, [clearResourceError, failResource]);

  const refreshPayees = useCallback(async () => {
    try {
      const data = await api.getPayees();
      setPayees(Array.isArray(data) ? data : []);
      clearResourceError("payees");
    } catch (err) {
      console.error(err);
      failResource("payees", err);
    }
  }, [clearResourceError, failResource]);

  const refreshSettings = useCallback(async () => {
    try {
      const data = await api.getPaperlessSettings();
      setSettings(data ?? null);
      clearResourceError("settings");
    } catch (err) {
      console.error(err);
      failResource("settings", err);
    }
  }, [clearResourceError, failResource]);

  const refreshAll = useCallback(async () => {
    await Promise.all([
      refreshAccounts(),
      refreshAccountTypes(),
      refreshCategories(),
      refreshGroups(),
      refreshPayees(),
      refreshSettings(),
    ]);
  }, [
    refreshAccounts,
    refreshAccountTypes,
    refreshCategories,
    refreshGroups,
    refreshPayees,
    refreshSettings,
  ]);

  useEffect(() => {
    let cancelled = false;
    void refreshAll().finally(() => {
      if (!cancelled) setLoading(false);
    });
    return () => {
      cancelled = true;
    };
  }, [refreshAll]);

  // A reconnect or a completed outbox flush means the server's copy of these
  // lookups has moved on without this session noticing: an offline boot shows
  // the last snapshot for the whole session otherwise, and an account's balance
  // (computed from its transactions on every read) goes stale the moment a
  // transaction is written anywhere else. The offline layer's store is read
  // directly rather than through OfflineContext because this provider is mounted
  // above it in App.tsx.
  const offline = useSyncExternalStore(subscribeOffline, getOfflineSnapshot);
  const lastOffline = useRef({
    online: offline.online,
    syncedAt: offline.syncedAt,
  });

  useEffect(() => {
    const previous = lastOffline.current;
    const current = { online: offline.online, syncedAt: offline.syncedAt };
    if (
      current.online === previous.online &&
      current.syncedAt === previous.syncedAt
    ) {
      return;
    }
    lastOffline.current = current;
    // Losing the connection changes nothing about what the server holds.
    if (!current.online && current.syncedAt === previous.syncedAt) return;
    void refreshAll();
  }, [offline.online, offline.syncedAt, refreshAll]);

  return (
    <DomainDataContext.Provider
      value={{
        accounts,
        accountTypes,
        categories,
        groups,
        payees,
        settings,
        loading,
        errors,
        setAccounts,
        setAccountTypes,
        setCategories,
        setGroups,
        setPayees,
        setSettings,
        refreshAccounts,
        refreshAccountTypes,
        refreshCategories,
        refreshGroups,
        refreshPayees,
        refreshSettings,
        refreshAll,
      }}
    >
      {children}
    </DomainDataContext.Provider>
  );
}

export function useDomainData(): DomainDataContextValue {
  const context = useContext(DomainDataContext);
  if (context === undefined) {
    throw new Error("useDomainData must be used within a DomainDataProvider");
  }
  return context;
}
