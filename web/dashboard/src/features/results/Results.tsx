import { Show, For, createSignal } from 'solid-js';
import { A, useParams } from '@solidjs/router';
import { createQuery } from '@tanstack/solid-query';
import { ArrowLeft, ArrowRight, RefreshCw } from 'lucide-solid';
import { format } from 'date-fns';
import { data } from '../../lib/api/client';
import type { Page } from '../../lib/api/types';
import { Failure, Loading, Status } from '../../components/ui/Feedback';
type Statistics = {
  schema_version: number;
  snapshot_complete: boolean;
  observation_coverage: string;
  counts: null | Record<string, number>;
};
type Delivery = {
  id: string;
  user_id: string;
  installation_id: string | null;
  status: string;
  sent_at: string | null;
  delivered_at: string | null;
  opened_at: string | null;
};
type Read = { id: string; user_id: string; read_at: string };
function timestamp(value: string | null | undefined) {
  if (!value) return '—';
  const date = new Date(value);
  return Number.isFinite(date.getTime()) ? format(date, 'dd MMM yyyy, HH:mm') : '—';
}
function count(value: number | undefined) {
  return value !== undefined && Number.isSafeInteger(value) && value >= 0
    ? value.toLocaleString('id-ID')
    : '—';
}
export default function Results() {
  const params = useParams();
  const [tab, setTab] = createSignal<'deliveries' | 'reads'>('deliveries');
  const [cursors, setCursors] = createSignal<string[]>([]);
  const path = () => `/apps/${params.appId}/campaigns/${params.campaignId}`;
  const statistics = createQuery(() => ({
    queryKey: ['statistics', params.appId, params.campaignId],
    queryFn: ({ signal }) => data<Statistics>(`${path()}/statistics`, signal),
  }));
  const history = createQuery(() => ({
    queryKey: ['history', params.appId, params.campaignId, tab(), cursors().at(-1) ?? ''],
    queryFn: ({ signal }) =>
      data<Page<Delivery | Read>>(
        `${path()}/${tab()}?limit=20${cursors().length ? `&after_id=${encodeURIComponent(cursors().at(-1)!)}` : ''}`,
        signal,
      ),
  }));
  const metrics = [
    { key: 'targeted_users', label: 'Pengguna yang ditargetkan', note: 'Snapshot saat persiapan' },
    {
      key: 'provider_accepted_devices',
      label: 'Diterima provider',
      note: 'Bukan bukti sampai di perangkat',
    },
    {
      key: 'observed_devices',
      label: 'Perangkat melaporkan',
      note: 'Hanya perangkat yang mengirim receipt',
    },
    { key: 'opened_users', label: 'Pengguna membuka', note: 'Pengguna unik yang teramati' },
  ];
  return (
    <>
      <A class="back-link" href={path()}>
        <ArrowLeft size={16} />
        Detail campaign
      </A>
      <header class="page-heading">
        <div>
          <span class="eyebrow">CAMPAIGN #{params.campaignId}</span>
          <h1>Ikuti perjalanan pesan.</h1>
          <p class="muted">
            Penerimaan provider, laporan perangkat, dan pembukaan dicatat terpisah.
          </p>
        </div>
        <button
          class="button secondary"
          disabled={statistics.isFetching || history.isFetching}
          onClick={() => {
            void statistics.refetch();
            void history.refetch();
          }}
        >
          <RefreshCw size={16} />
          Perbarui
        </button>
      </header>
      <Show when={!statistics.isPending} fallback={<Loading />}>
        <Show
          when={!statistics.isError}
          fallback={<Failure error={statistics.error} retry={() => void statistics.refetch()} />}
        >
          <Show
            when={
              statistics.data?.schema_version === 1 &&
              statistics.data.snapshot_complete &&
              statistics.data.counts !== null
            }
            fallback={
              <div class="notice">
                <h2>Statistik belum lengkap</h2>
                <p>
                  Snapshot audience belum tersedia atau campaign memakai format sebelumnya. Kondisi
                  ini bukan berarti hasilnya nol.
                </p>
              </div>
            }
          >
            <dl class="metrics-strip">
              <For each={metrics}>
                {(metric) => (
                  <div>
                    <dt>{metric.label}</dt>
                    <dd>{count(statistics.data?.counts?.[metric.key])}</dd>
                    <span>{metric.note}</span>
                  </div>
                )}
              </For>
            </dl>
            <p class="help-text">
              Hitungan memiliki satuan dan cakupan berbeda, sehingga tidak dapat dijumlahkan menjadi
              satu total pengiriman.
            </p>
          </Show>
        </Show>
      </Show>
      <div class="history-heading">
        <div class="tabs" role="group" aria-label="Jenis riwayat">
          <button
            class={tab() === 'deliveries' ? 'selected' : ''}
            aria-pressed={tab() === 'deliveries'}
            onClick={() => {
              setTab('deliveries');
              setCursors([]);
            }}
          >
            Pengiriman
          </button>
          <button
            class={tab() === 'reads' ? 'selected' : ''}
            aria-pressed={tab() === 'reads'}
            onClick={() => {
              setTab('reads');
              setCursors([]);
            }}
          >
            Pembukaan
          </button>
        </div>
        <span class="help-text">Waktu: {Intl.DateTimeFormat().resolvedOptions().timeZone}</span>
      </div>
      <Show when={!history.isPending} fallback={<Loading />}>
        <Show
          when={!history.isError}
          fallback={<Failure error={history.error} retry={() => void history.refetch()} />}
        >
          <Show
            when={history.data?.items.length}
            fallback={
              <div class="empty-state">
                <h2>Belum ada catatan {tab() === 'reads' ? 'pembukaan' : 'pengiriman'}.</h2>
                <p>Catatan akan muncul setelah data diproses dan diterima backend.</p>
              </div>
            }
          >
            <div class="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Pengguna</th>
                    <th>{tab() === 'deliveries' ? 'Status perangkat' : 'Waktu pembukaan'}</th>
                    <Show when={tab() === 'deliveries'}>
                      <th>Waktu provider</th>
                      <th>Dibuka</th>
                    </Show>
                  </tr>
                </thead>
                <tbody>
                  <For each={history.data?.items}>
                    {(item) => (
                      <tr>
                        <td>
                          #{item.user_id}
                          <span class="row-meta">Catatan #{item.id}</span>
                        </td>
                        <Show
                          when={'status' in item}
                          fallback={<td>{timestamp((item as Read).read_at)}</td>}
                        >
                          <td>
                            <Status value={(item as Delivery).status} />
                          </td>
                          <td>{timestamp((item as Delivery).sent_at)}</td>
                          <td>{timestamp((item as Delivery).opened_at)}</td>
                        </Show>
                      </tr>
                    )}
                  </For>
                </tbody>
              </table>
            </div>
          </Show>
          <div class="pagination">
            <span>Halaman {cursors().length + 1}</span>
            <div>
              <button
                class="button secondary small"
                disabled={!cursors().length}
                onClick={() => setCursors(cursors().slice(0, -1))}
              >
                <ArrowLeft size={15} />
                Sebelumnya
              </button>
              <button
                class="button secondary small"
                disabled={!history.data?.next_after_id}
                onClick={() => {
                  const next = history.data?.next_after_id;
                  if (next) setCursors([...cursors(), next]);
                }}
              >
                Berikutnya
                <ArrowRight size={15} />
              </button>
            </div>
          </div>
        </Show>
      </Show>
    </>
  );
}
