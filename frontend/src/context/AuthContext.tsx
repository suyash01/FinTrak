import {
  createContext,
  useContext,
  useState,
  useCallback,
  useEffect,
  useRef,
  type ReactNode,
} from "react";
import api, {
  STORED_USER_KEY,
  getStoredUser,
  storeUser,
} from "../api/client";
import { isNetworkError } from "../api/errors";
import { clearCached } from "../api/offlineCache";
import type { AuthResponse, User } from "../types";

interface AuthContextValue {
  isAuthenticated: boolean;
  // initializing is true while the session cookie is verified on mount.
  initializing: boolean;
  user: User | null;
  login: (email: string, password: string) => Promise<void>;
  register: (email: string, password: string) => Promise<void>;
  logout: () => void;
}

const AuthContext = createContext<AuthContextValue | undefined>(undefined);

// forgetUser ends this tab's session: it drops the departing user's cached reads
// — payloads namespaced by an id that nothing can reach once the stored user is
// gone, which is why every path that ends a session has to clear them here — and
// removes the cached identity. Queued creates are kept: they are unsent work,
// not a cache.
function forgetUser(): void {
  const departing = getStoredUser();
  if (departing) clearCached(departing.id);
  storeUser(null);
}

export function AuthProvider({ children }: { children: ReactNode }) {
  // Optimistically hydrate from the cached (non-sensitive) user so the app
  // doesn't flash the login screen, then verify against the server because the
  // JWT itself lives in an httpOnly cookie that JavaScript cannot read.
  const [user, setUser] = useState<User | null>(getStoredUser);
  const [initializing, setInitializing] = useState(true);
  // The storage listener runs outside React and must know which identity this
  // tab was serving when another tab replaces the stored one.
  const currentUser = useRef<User | null>(user);
  useEffect(() => {
    currentUser.current = user;
  });

  useEffect(() => {
    let cancelled = false;
    api
      .me()
      .then((current) => {
        if (cancelled) return;
        setUser(current);
        storeUser(current);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        // A probe that never reached the server must not sign the user out:
        // the cached user and the offline cache are what an installed app shows
        // until the connection returns. A rejected probe is a real sign-out, and
        // it has to clear the cached ledger exactly as logout does.
        if (isNetworkError(err) && getStoredUser()) return;
        setUser(null);
        forgetUser();
      })
      .finally(() => {
        if (!cancelled) setInitializing(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // Another tab signing in (or out) rewrites the origin-wide identity without
  // this tab's knowledge: the session cookie is shared, so this tab would keep
  // rendering the previous user's UI — and serving their offline cache — while
  // the API layer already reads the new identity. Adopt what the storage now
  // holds; a reload is how the tab re-derives everything (the session probe, the
  // offline namespace, every provider mounted under the authenticated tree) from
  // the session that actually exists.
  useEffect(() => {
    const onStorage = (event: StorageEvent) => {
      // A null key is a storage.clear(), which also drops the identity.
      if (event.key !== null && event.key !== STORED_USER_KEY) return;
      const next = getStoredUser();
      const previous = currentUser.current;
      if ((previous?.id ?? null) === (next?.id ?? null)) return;
      currentUser.current = next;
      if (previous) clearCached(previous.id);
      window.location.reload();
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, []);

  const applyAuth = useCallback((res: AuthResponse) => {
    const previous = getStoredUser();
    // Signing in as someone else on a browser that still holds the previous
    // user's cached ledger must not leave that ledger reachable: the cache is
    // namespaced by an id this tab is about to stop being.
    if (previous && previous.id !== res.user.id) clearCached(previous.id);
    setUser(res.user);
    storeUser(res.user);
  }, []);

  const login = useCallback(
    async (email: string, password: string) => {
      const res = await api.login({ email, password });
      applyAuth(res);
    },
    [applyAuth],
  );

  const register = useCallback(
    async (email: string, password: string) => {
      const res = await api.register({ email, password });
      applyAuth(res);
    },
    [applyAuth],
  );

  const logout = useCallback(() => {
    setUser(null);
    // Drops the cached ledger; queued creates are kept (see forgetUser).
    forgetUser();
    // Expire the httpOnly session cookie server-side; best-effort.
    api.logout().catch(() => {});
  }, []);

  const value: AuthContextValue = {
    isAuthenticated: !!user,
    initializing,
    user,
    login,
    register,
    logout,
  };

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const context = useContext(AuthContext);
  if (context === undefined) {
    throw new Error("useAuth must be used within an AuthProvider");
  }
  return context;
}
