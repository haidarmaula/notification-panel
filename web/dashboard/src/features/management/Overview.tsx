import { For, Show } from 'solid-js';
import { A, useParams } from '@solidjs/router';
import { createQuery } from '@tanstack/solid-query';
import { data } from '../../lib/api/client';
import type { CampaignSummary, Page } from '../../lib/api/types';
import { Status } from '../../components/ui/Feedback';
import { QueryState } from './shared';
export default function Overview() {
  const params = useParams();
  const recent = createQuery(() => ({
    queryKey: ['recent-campaigns', params.appId],
    queryFn: ({ signal }) =>
      data<Page<CampaignSummary>>(`/apps/${params.appId}/campaigns?limit=5`, signal),
  }));
  return (
    <>
      <header class="page-heading">
        <div>
          <h1>Ringkasan</h1>
          <p class="muted">Lanjutkan pekerjaan campaign dan periksa hasil pengiriman.</p>
        </div>
        <A class="button primary" href={`/apps/${params.appId}/campaigns/new`}>
          Buat campaign
        </A>
      </header>
      <section class="surface stack">
        <div class="section-bar">
          <h2>Campaign terbaru</h2>
          <A href={`/apps/${params.appId}/campaigns`}>Lihat semua</A>
        </div>
        <QueryState query={recent}>
          <Show
            when={recent.data?.items.length}
            fallback={<p>Belum ada campaign. Mulai dengan membuat draft pertama.</p>}
          >
            <div class="table-wrap">
              <table class="directory-table">
                <thead>
                  <tr>
                    <th>Campaign</th>
                    <th>Status</th>
                    <th>Hasil</th>
                  </tr>
                </thead>
                <tbody>
                  <For each={recent.data?.items}>
                    {(item) => (
                      <tr>
                        <td>
                          <A href={`/apps/${params.appId}/campaigns/${item.id}`}>{item.title}</A>
                          <span class="row-meta">#{item.id}</span>
                        </td>
                        <td>
                          <Status value={item.status} />
                        </td>
                        <td>
                          <Show when={item.status !== 'DRAFT'} fallback="Belum dikirim">
                            <A href={`/apps/${params.appId}/campaigns/${item.id}/results`}>
                              Lihat hasil
                            </A>
                          </Show>
                        </td>
                      </tr>
                    )}
                  </For>
                </tbody>
              </table>
            </div>
          </Show>
        </QueryState>
      </section>
      <section class="management-section">
        <h2>Siapkan pengiriman</h2>
        <div class="workflow-links">
          <A class="surface" href={`/apps/${params.appId}/templates`}>
            <strong>Template pesan</strong>
            <span>Gunakan kembali isi pesan untuk draft baru.</span>
          </A>
          <A class="surface" href={`/apps/${params.appId}/segments`}>
            <strong>Segment penerima</strong>
            <span>Kelola kelompok pengguna untuk target campaign.</span>
          </A>
          <A class="surface" href={`/apps/${params.appId}/audience`}>
            <strong>Pengguna &amp; perangkat</strong>
            <span>Periksa registrasi perangkat penerima.</span>
          </A>
        </div>
      </section>
    </>
  );
}
