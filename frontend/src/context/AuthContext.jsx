import { createContext, useContext, useState, useCallback } from 'react';
import api, { getToken, setToken, getStoredUser, storeUser } from '../api/client';

const AuthContext = createContext();

export function AuthProvider({ children }) {
  const [token, setTokenState] = useState(getToken);
  const [user, setUser] = useState(getStoredUser);

  const applyAuth = useCallback((res) => {
    setTokenState(res.token);
    setUser(res.user);
    setToken(res.token);
    storeUser(res.user);
  }, []);

  const login = useCallback(async (email, password) => {
    const res = await api.login({ email, password });
    applyAuth(res);
  }, [applyAuth]);

  const register = useCallback(async (email, password) => {
    const res = await api.register({ email, password });
    applyAuth(res);
  }, [applyAuth]);

  const logout = useCallback(() => {
    setTokenState(null);
    setUser(null);
    setToken(null);
    storeUser(null);
  }, []);

  const value = {
    isAuthenticated: !!token,
    user,
    token,
    login,
    register,
    logout,
  };

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const context = useContext(AuthContext);
  if (context === undefined) {
    throw new Error('useAuth must be used within an AuthProvider');
  }
  return context;
}
