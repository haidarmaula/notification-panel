import {
  createContext,
  useContext,
  createEffect,
  createSignal,
  onCleanup,
  type ParentProps,
} from 'solid-js';
import {
  createQuery,
  useQueryClient,
  QueryCache,
  QueryClient,
  MutationCache,
} from '@tanstack/solid-query';
import { ApiError, request, sessionSchema, type Session } from '../lib/api/client';
import { confirmUnsavedChanges, discardUnsavedChanges } from './unsaved';

export function makeQueryClient() {
  return new QueryClient({
    queryCache: new QueryCache({
      onError(error) {
        if (error instanceof ApiError && error.status === 401)
          window.dispatchEvent(new Event('hello:session-expired'));
      },
    }),
    mutationCache: new MutationCache({
      onError(error) {
        if (error instanceof ApiError && error.status === 401)
          window.dispatchEvent(new Event('hello:session-expired'));
      },
    }),
    defaultOptions: {
      queries: { retry: false, staleTime: 20_000, refetchOnWindowFocus: false },
      mutations: { retry: false },
    },
  });
}
function createAuth() {
  const client = useQueryClient();
  const [csrf, setCsrf] = createSignal('');
  const [authenticated, setAuthenticated] = createSignal(false);
  const session = createQuery(() => ({
    queryKey: ['session'],
    queryFn: async () => sessionSchema.parse(await request('/bff/session')),
    staleTime: 0,
  }));
  createEffect(() => {
    if (session.data) {
      setCsrf(session.data.csrfToken);
      setAuthenticated(session.data.authenticated);
    }
  });
  const expire = () => {
    setCsrf('');
    client.clear();
    window.location.replace('/login');
  };
  window.addEventListener('hello:session-expired', expire);
  onCleanup(() => window.removeEventListener('hello:session-expired', expire));
  return {
    session,
    authenticated: () => authenticated() || session.data?.authenticated === true,
    csrf,
    async login(email: string, password: string) {
      const result = sessionSchema.parse(
        await request('/bff/login', { method: 'POST', body: { email, password }, csrf: csrf() }),
      );
      client.removeQueries({ predicate: (query) => query.queryKey[0] !== 'session' });
      setCsrf(result.csrfToken);
      setAuthenticated(true);
      client.setQueryData<Session>(['session'], result);
    },
    async logout() {
      if (!confirmUnsavedChanges()) return false;
      await request('/bff/logout', { method: 'POST', csrf: csrf(), body: {} });
      discardUnsavedChanges();
      setCsrf('');
      client.clear();
      window.location.replace('/login');
      return true;
    },
  };
}
const AuthContext = createContext<ReturnType<typeof createAuth>>();
export function AuthProvider(props: ParentProps) {
  return <AuthContext.Provider value={createAuth()}>{props.children}</AuthContext.Provider>;
}
export function useAuth() {
  const auth = useContext(AuthContext);
  if (!auth) throw new Error('Missing auth provider');
  return auth;
}
