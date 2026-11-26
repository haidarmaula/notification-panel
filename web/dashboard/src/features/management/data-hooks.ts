import { useRef, useState } from 'react';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { useAuth } from '../../app/auth-context';
import { ApiError, data, errorMessage, request, type WriteConditions } from '../../lib/api/client';
import type { Page } from '../../lib/api/types';
export function usePage<T>(path: string, options: { enabled?: boolean } = {}) {
  const [cursors, setCursors] = useState<string[]>([]);
  const query = useQuery({
    queryKey: ['directory', path, cursors.at(-1) ?? ''],
    queryFn: ({ signal }) =>
      data<Page<T>>(
        `${path}?limit=20${cursors.length ? `&after_id=${encodeURIComponent(cursors.at(-1)!)}` : ''}`,
        signal,
      ),
    enabled: options.enabled ?? true,
    placeholderData: keepPreviousData,
  });
  return { query, cursors: () => cursors, setCursors };
}
export function hasPageNavigation<T>(page: ReturnType<typeof usePage<T>>) {
  return (
    !page.query.isPending &&
    !page.query.isError &&
    (!!page.query.data?.items.length || page.cursors().length > 0)
  );
}
export function useWrite() {
  const auth = useAuth();
  const [, setPending] = useState(false);
  const [, setError] = useState('');
  const [, setStatus] = useState<number | null>(null);
  const [, setUncertain] = useState(false);
  const inFlight = useRef(false);
  const latestStatus = useRef<number | null>(null);
  const latestError = useRef('');
  const uncertainResult = useRef(false);
  async function write<T>(
    path: string,
    method: string,
    body?: unknown,
    conditions: WriteConditions = {},
  ): Promise<T | undefined> {
    if (inFlight.current || uncertainResult.current) return;
    inFlight.current = true;
    latestError.current = '';
    latestStatus.current = null;
    setPending(true);
    setError('');
    setStatus(null);
    try {
      const result = await request<T>(`/bff/api${path}`, {
        method,
        body,
        csrf: auth.csrf(),
        ...conditions,
      });
      return result ?? ({} as T);
    } catch (cause) {
      latestStatus.current = cause instanceof ApiError ? cause.status : null;
      setStatus(latestStatus.current);
      if (cause instanceof ApiError && cause.status === 401)
        window.dispatchEvent(new Event('hello:session-expired'));
      if (!(cause instanceof ApiError) || cause.status === 0 || cause.status >= 500) {
        uncertainResult.current = true;
        setUncertain(true);
      }
      latestError.current =
        cause instanceof ApiError && cause.status === 503
          ? 'Service unavailable. Check the outcome before trying again.'
          : errorMessage(cause);
      setError(latestError.current);
    } finally {
      inFlight.current = false;
      setPending(false);
    }
  }
  return {
    write,
    pending: () => inFlight.current,
    error: () => latestError.current,
    status: () => latestStatus.current,
    uncertain: () => uncertainResult.current,
    blocked: () => inFlight.current || uncertainResult.current,
  };
}
const dateFormatter = new Intl.DateTimeFormat('en-GB', {
  dateStyle: 'medium',
  timeStyle: 'short',
});
export function date(value?: string | null) {
  if (!value || !Number.isFinite(new Date(value).getTime())) return '—';
  return dateFormatter.format(new Date(value));
}
