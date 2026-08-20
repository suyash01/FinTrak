import {
  createContext,
  useContext,
  useState,
  useCallback,
  type ReactNode,
} from "react";
import api, {
  getToken,
  setToken,
  getStoredUser,
  storeUser,
} from "../api/client";
import type { AuthResponse, User } from "../types";

interface AuthContextValue {
  isAuthenticated: boolean;
  user: User | null;
  token: string | null;
  login: (email: string, password: string) => Promise<void>;
  register: (email: string, password: string) => Promise<void>;
  logout: () => void;
}

const AuthContext = createContext<AuthContextValue | undefined>(undefined);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [token, setTokenState] = useState<string | null>(getToken);
  const [user, setUser] = useState<User | null>(getStoredUser);

  const applyAuth = useCallback((res: AuthResponse) => {
    setTokenState(res.token);
    setUser(res.user);
    setToken(res.token);
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
    setTokenState(null);
    setUser(null);
    setToken(null);
    storeUser(null);
  }, []);

  const value: AuthContextValue = {
    isAuthenticated: !!token,
    user,
    token,
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
