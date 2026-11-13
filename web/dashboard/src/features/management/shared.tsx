import { Show, createSignal, onCleanup, type JSX } from 'solid-js';
import { useBeforeLeave } from '@solidjs/router';
import { createQuery } from '@tanstack/solid-query';
import { useAuth } from '../../app/auth';
import { ApiError, data, errorMessage, request } from '../../lib/api/client';
import type { Page } from '../../lib/api/types';
import { Failure, Loading } from '../../components/ui/Feedback';

export function createPage<T>(path: () => string) {
  const [cursors, setCursors] = createSignal<string[]>([]);
  const query = createQuery(() => ({
    queryKey: ['directory', path(), cursors().at(-1) ?? ''],
    queryFn: ({ signal }) =>
      data<Page<T>>(
        `${path()}?limit=20${cursors().length ? `&after_id=${encodeURIComponent(cursors().at(-1)!)}` : ''}`,
        signal,
      ),
  }));
  return { query, cursors, setCursors };
}
export function Pager<T>(props: { page: ReturnType<typeof createPage<T>> }) {
  return (
    <div class="pagination">
      <span>Halaman {props.page.cursors().length + 1}</span>
      <div>
        <button
          class="button secondary small"
          disabled={!props.page.cursors().length || props.page.query.isFetching}
          onClick={() => props.page.setCursors(props.page.cursors().slice(0, -1))}
        >
          Sebelumnya
        </button>
        <button
          class="button secondary small"
          disabled={!props.page.query.data?.next_after_id || props.page.query.isFetching}
          onClick={() => {
            const cursor = props.page.query.data?.next_after_id;
            if (cursor) props.page.setCursors([...props.page.cursors(), cursor]);
          }}
        >
          Berikutnya
        </button>
      </div>
    </div>
  );
}
export function QueryState(props: {
  query: { isPending: boolean; isError: boolean; error: unknown; refetch: () => unknown };
  children: JSX.Element;
}) {
  return (
    <Show when={!props.query.isPending} fallback={<Loading />}>
      <Show
        when={!props.query.isError}
        fallback={<Failure error={props.query.error} retry={() => void props.query.refetch()} />}
      >
        {props.children}
      </Show>
    </Show>
  );
}
export function useWrite() {
  const auth = useAuth();
  const [pending, setPending] = createSignal(false);
  const [error, setError] = createSignal('');
  const [uncertain, setUncertain] = createSignal(false);
  async function write<T>(path: string, method: string, body?: unknown): Promise<T | undefined> {
    if (pending() || uncertain()) return;
    setPending(true);
    setError('');
    try {
      const result = await request<T>(`/bff/api${path}`, { method, body, csrf: auth.csrf() });
      return result ?? ({} as T);
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 401)
        window.dispatchEvent(new Event('hello:session-expired'));
      if (!(cause instanceof ApiError) || cause.status === 0 || cause.status >= 500)
        setUncertain(true);
      setError(
        cause instanceof ApiError && cause.status === 503
          ? 'Layanan belum tersedia. Periksa hasil perubahan sebelum mencoba lagi.'
          : errorMessage(cause),
      );
    } finally {
      setPending(false);
    }
  }
  return { write, pending, error, uncertain, blocked: () => pending() || uncertain() };
}
export function WriteFeedback(props: { operation: ReturnType<typeof useWrite> }) {
  return (
    <>
      <Show when={props.operation.error()}>
        <p class="notice notice-error" role="alert">
          {props.operation.error()}
        </p>
      </Show>
      <Show when={props.operation.uncertain()}>
        <p class="notice">
          Hasil perubahan belum dapat dipastikan. Periksa data terbaru sebelum mencoba lagi.
          Formulir ini diblokir untuk mencegah perubahan ganda.
        </p>
      </Show>
    </>
  );
}
export function date(value?: string | null) {
  if (!value || !Number.isFinite(new Date(value).getTime())) return '—';
  return new Intl.DateTimeFormat('id-ID', { dateStyle: 'medium', timeStyle: 'short' }).format(
    new Date(value),
  );
}
export function useUnsaved() {
  const [dirty, setDirty] = createSignal(false);
  useBeforeLeave((event) => {
    if (dirty() && !window.confirm('Perubahan belum disimpan. Tinggalkan halaman?'))
      event.preventDefault();
  });
  const unload = (event: BeforeUnloadEvent) => {
    if (dirty()) {
      event.preventDefault();
      event.returnValue = '';
    }
  };
  window.addEventListener('beforeunload', unload);
  onCleanup(() => window.removeEventListener('beforeunload', unload));
  return setDirty;
}
