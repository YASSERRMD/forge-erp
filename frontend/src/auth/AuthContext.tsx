import { createContext, useContext, useState, type ReactNode } from 'react';
import { api } from '../api/client';

interface AuthState {
  token: string | null;
  login: string | null;
  signIn: (login: string, password: string) => Promise<void>;
  signOut: () => void;
}

const AuthContext = createContext<AuthState | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [token, setToken] = useState<string | null>(() => localStorage.getItem('ferp_token'));
  const [login, setLogin] = useState<string | null>(() => localStorage.getItem('ferp_login'));

  async function signIn(loginName: string, password: string) {
    const res = await api.login(loginName, password);
    localStorage.setItem('ferp_token', res.access_token);
    localStorage.setItem('ferp_login', loginName);
    setToken(res.access_token);
    setLogin(loginName);
  }

  function signOut() {
    localStorage.removeItem('ferp_token');
    localStorage.removeItem('ferp_login');
    setToken(null);
    setLogin(null);
  }

  return (
    <AuthContext.Provider value={{ token, login, signIn, signOut }}>{children}</AuthContext.Provider>
  );
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useAuth outside provider');
  return ctx;
}
