import { Show } from 'solid-js';
import { errorMessage } from '../../lib/api/client';
export function Loading(props: { label?: string }) {
  return (
    <div class="loading-state" role="status">
      <span class="spinner" />
      {props.label ?? 'Memuat data…'}
    </div>
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
