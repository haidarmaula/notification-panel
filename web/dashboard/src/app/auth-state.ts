import { useEffect } from 'react';
import {
  useQuery,
  useQueryClient,
  QueryCache,
  QueryClient,
  MutationCache,
} from '@tanstack/react-query';
import { sessionSchema, ApiError, request, type Session } from '../lib/api/client';
import { confirmUnsavedChanges, discardUnsavedChanges } from './unsaved';
function handleSessionError(error: Error) {
  if (error instanceof ApiError && error.status === 401)
    window.dispatchEvent(new Event('hello:session-expired'));
}
export function makeQueryClient() {
  return new QueryClient({
    queryCache: new QueryCache({ onError: handleSessionError }),
    mutationCache: new MutationCache({ onError: handleSessionError }),
    defaultOptions: {
      queries: { retry: false, staleTime: 20_000, refetchOnWindowFocus: false },
      mutations: { retry: false },
    },
  });
}
export function useAuthState() {
  const client = useQueryClient();
  const session = useQuery({
    queryKey: ['session'],
    queryFn: async () => sessionSchema.parse(await request('/bff/session')),
    staleTime: 0,
  });
  useEffect(() => {
    const expire = () => {
      client.clear();
      window.location.replace('/login');
    };
    window.addEventListener('hello:session-expired', expire);
    return () => window.removeEventListener('hello:session-expired', expire);
  }, [client]);
  return {
    session,
    authenticated: () => session.data?.authenticated === true,
    csrf: () => session.data?.csrfToken ?? '',
    async login(email: string, password: string) {
      const result = sessionSchema.parse(
        await request('/bff/login', {
          method: 'POST',
          body: { email, password },
          csrf: session.data?.csrfToken ?? '',
        }),
      );
      client.removeQueries({ predicate: (query) => query.queryKey[0] !== 'session' });
      client.setQueryData<Session>(['session'], result);
    },
    async logout() {
      if (!confirmUnsavedChanges()) return false;
      await request('/bff/logout', {
        method: 'POST',
        csrf: session.data?.csrfToken ?? '',
        body: {},
      });
      discardUnsavedChanges();
      client.clear();
      window.location.replace('/login');
      return true;
    },
  };
}
