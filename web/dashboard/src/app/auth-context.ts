import { createContext, useContext } from 'react';
import type { useAuthState } from './auth-state';
export const AuthContext = createContext<ReturnType<typeof useAuthState> | undefined>(undefined);
export function useAuth() {
  const auth = useContext(AuthContext);
  if (!auth) throw new Error('Missing auth provider');
  return auth;
}
