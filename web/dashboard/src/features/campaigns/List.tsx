import { For, Show, createSignal } from 'solid-js';
import { A, useParams } from '@solidjs/router';
import { createQuery } from '@tanstack/solid-query';
import { ArrowLeft, ArrowRight, Megaphone, Plus } from 'lucide-solid';
import { data } from '../../lib/api/client';
import type { CampaignSummary, Page } from '../../lib/api/types';
import { Failure, Loading, Status } from '../../components/ui/Feedback';
export default function Campaigns() {
  const params = useParams();
  const [cursors, setCursors] = createSignal<string[]>([]);
  const query = createQuery(() => ({
    queryKey: ['campaigns', params.appId, cursors().at(-1) ?? ''],
    queryFn: ({ signal }) =>
      data<Page<CampaignSummary>>(
        `/apps/${params.appId}/campaigns?limit=20${cursors().length ? `&after_id=${encodeURIComponent(cursors().at(-1)!)}` : ''}`,
        signal,
      ),
  }));
  return (
    <>
      <header class="page-heading">
        <div>
          <span class="eyebrow">KOMUNIKASI</span>
          <h1>Campaign</h1>
          <p class="muted">Dari sebuah pesan menjadi percakapan yang berarti.</p>
        </div>
        <A class="button primary" href={`/apps/${params.appId}/campaigns/new`}>
          <Plus size={18} />
          Buat campaign
        </A>
      </header>
      <div class="section-bar">
        <h2>Semua campaign</h2>
        <span class="muted">Status terbaru dari aplikasi ini</span>
      </div>
      <Show when={!query.isPending} fallback={<Loading />}>
        <Show
          when={!query.isError}
          fallback={<Failure error={query.error} retry={() => void query.refetch()} />}
        >
          <Show
            when={query.data?.items.length}
            fallback={
              <div class="empty-state">
                <span class="empty-icon">
                  <Megaphone size={27} />
                </span>
                <h2>Mulai dari pesan pertama.</h2>
                <p>Buat draft, pilih penerima, lalu tinjau sebelum mengirim.</p>
                <A class="button primary" href={`/apps/${params.appId}/campaigns/new`}>
                  <Plus size={16} />
                  Buat campaign pertama
                </A>
              </div>
            }
          >
            <div class="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Nama campaign</th>
                    <th>Status</th>
                    <th>
                      <span class="sr-only">Tindakan</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  <For each={query.data?.items}>
                    {(campaign) => (
                      <tr>
                        <td>
                          <A
                            class="campaign-title"
                            href={`/apps/${params.appId}/campaigns/${campaign.id}`}
                          >
                            {campaign.title}
                          </A>
                          <span class="row-meta">#{campaign.id}</span>
                        </td>
                        <td>
                          <Status value={campaign.status} />
                        </td>
                        <td>
                          <A
                            class="row-action"
                            href={`/apps/${params.appId}/campaigns/${campaign.id}`}
                            aria-label={`Buka ${campaign.title}`}
                          >
                            <ArrowRight size={18} />
                          </A>
                        </td>
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
                disabled={!query.data?.next_after_id}
                onClick={() => {
                  const cursor = query.data?.next_after_id;
                  if (cursor) setCursors([...cursors(), cursor]);
                }}
              >
                Berikutnya
                <ArrowRight size={15} />
              </button>
            </div>
          </div>
        </Show>
      </Show>
      <p class="page-footnote">
        Pesan hanya dikirim setelah Anda meninjau dan mengonfirmasi campaign.
      </p>
    </>
  );
}
