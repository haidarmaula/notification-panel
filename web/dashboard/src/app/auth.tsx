import type { PropsWithChildren } from 'react';
import { AuthContext } from './auth-context';
import { useAuthState } from './auth-state';
export function AuthProvider({ children }: PropsWithChildren) {
  const auth = useAuthState();
  return <AuthContext.Provider value={auth}>{children}</AuthContext.Provider>;
}
