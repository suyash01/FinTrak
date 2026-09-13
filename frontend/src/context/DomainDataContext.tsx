import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
  type Dispatch,
  type ReactNode,
  type SetStateAction,
} from "react";
import api from "../api/client";
import type {
  Account,
  AccountType,
  Category,
  CategoryGroup,
  Payee,
  UserSettings,
} from "../types";

interface DomainDataContextValue {
  accounts: Account[];
  accountTypes: AccountType[];
  categories: Category[];
  groups: CategoryGroup[];
  payees: Payee[];
  settings: UserSettings | null;
  loading: boolean;
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

  const refreshAccounts = useCallback(async () => {
    try {
      const data = await api.getAccounts();
      setAccounts(Array.isArray(data) ? data : []);
    } catch (err) {
      console.error(err);
    }
  }, []);

  const refreshAccountTypes = useCallback(async () => {
    try {
      const data = await api.getAccountTypes();
      setAccountTypes(Array.isArray(data) ? data : []);
    } catch (err) {
      console.error(err);
    }
  }, []);

  const refreshCategories = useCallback(async () => {
    try {
      const data = await api.getCategories();
      setCategories(Array.isArray(data) ? data : []);
    } catch (err) {
      console.error(err);
    }
  }, []);

  const refreshGroups = useCallback(async () => {
    try {
      const data = await api.getGroups();
      setGroups(Array.isArray(data) ? data : []);
    } catch (err) {
      console.error(err);
    }
  }, []);

  const refreshPayees = useCallback(async () => {
    try {
      const data = await api.getPayees();
      setPayees(Array.isArray(data) ? data : []);
    } catch (err) {
      console.error(err);
    }
  }, []);

  const refreshSettings = useCallback(async () => {
    try {
      const data = await api.getPaperlessSettings();
      setSettings(data ?? null);
    } catch (err) {
      console.error(err);
    }
  }, []);

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
