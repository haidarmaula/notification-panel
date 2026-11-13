import { For, Show } from 'solid-js';
import { createQuery } from '@tanstack/solid-query';
import { data, errorMessage } from '../../lib/api/client';
export function Loading(props: { label?: string; rows?: number }) {
  return (
    <div class={`loading-state ${props.rows ? 'table-loading' : ''}`} role="status">
      <Show
        when={props.rows}
        fallback={
          <>
            <span class="spinner" />
            {props.label ?? 'Memuat data…'}
          </>
        }
      >
        <span class="sr-only">{props.label ?? 'Memuat data…'}</span>
        <div class="skeleton-table" aria-hidden="true">
          <For each={Array.from({ length: props.rows ?? 0 })}>
            {() => (
              <div class="skeleton-row">
                <span />
                <span />
                <span />
              </div>
            )}
          </For>
        </div>
      </Show>
    </div>
  );
}
export function RecipientLabel(props: {
  appId: string;
  target: { type: string; id?: string | null };
  name?: string;
}) {
  const named = () => props.target.type === 'INDIVIDUAL' || props.target.type === 'SEGMENT';
  const recipient = createQuery(() => ({
    queryKey: ['recipient-name', props.appId, props.target.type, props.target.id],
    queryFn: ({ signal }) =>
      data<{ name?: string | null; external_id?: string }>(
        `/apps/${props.appId}/${props.target.type === 'SEGMENT' ? 'segments' : 'users'}/${props.target.id}`,
        signal,
      ),
    enabled: named() && !!props.target.id && !props.name,
  }));
  const kind = () =>
    props.target.type === 'SEGMENT'
      ? 'Segmen'
      : props.target.type === 'INDIVIDUAL'
        ? 'Pengguna'
        : 'Target tersimpan';
  const name = () =>
    props.name ||
    (recipient.isSuccess ? recipient.data?.name || recipient.data?.external_id : undefined);
  return (
    <span class="recipient-label">
      <span>
        {props.target.type === 'BROADCAST' ? 'Semua penerima dalam aplikasi' : name() || kind()}
      </span>
      <Show when={props.target.id}>
        <small>
          {name() ? `${kind()} ` : ''}#{props.target.id}
        </small>
      </Show>
    </span>
  );
}
export function Failure(props: { error: unknown; retry?: () => void }) {
  return (
    <div class="notice notice-error" role="alert">
      <p>{errorMessage(props.error)}</p>
      <Show when={props.retry}>
        <button class="button secondary" onClick={() => props.retry?.()}>
          Muat ulang
        </button>
      </Show>
    </div>
  );
}
export function Status(props: { value: string }) {
  const labels: Record<string, string> = {
    DRAFT: 'Draft',
    SCHEDULED: 'Terjadwal',
    QUEUED: 'Dalam antrean',
    PROCESSING: 'Diproses',
    COMPLETED: 'Selesai',
    FAILED: 'Gagal',
    SENT: 'Diterima provider',
    DELIVERED: 'Teramati di perangkat',
    OPENED: 'Dibuka',
    PENDING: 'Menunggu',
    ACTIVE: 'Aktif',
    HELD: 'Ditahan',
    PROVISIONING: 'Disiapkan',
    RETIRED: 'Nonaktif',
  };
  return (
    <span class={`status status-${props.value.toLowerCase()}`}>
      {labels[props.value] ?? props.value}
    </span>
  );
}
