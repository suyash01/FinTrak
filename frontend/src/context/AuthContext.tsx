import {
  createContext,
  useContext,
  useState,
  useCallback,
  useEffect,
  type ReactNode,
} from "react";
import api, { getStoredUser, storeUser } from "../api/client";
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

export function AuthProvider({ children }: { children: ReactNode }) {
  // Optimistically hydrate from the cached (non-sensitive) user so the app
  // doesn't flash the login screen, then verify against the server because the
  // JWT itself lives in an httpOnly cookie that JavaScript cannot read.
  const [user, setUser] = useState<User | null>(getStoredUser);
  const [initializing, setInitializing] = useState(true);

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
        // until the connection returns. A rejected probe is a real sign-out.
        if (isNetworkError(err) && getStoredUser()) return;
        setUser(null);
        storeUser(null);
      })
      .finally(() => {
        if (!cancelled) setInitializing(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const applyAuth = useCallback((res: AuthResponse) => {
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
    const current = user;
    setUser(null);
    storeUser(null);
    // The cached reads are the user's ledger and must not outlive the session.
    // Queued creates are kept: they are unsent work, not a cache.
    if (current) clearCached(current.id);
    // Expire the httpOnly session cookie server-side; best-effort.
    api.logout().catch(() => {});
  }, [user]);

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
