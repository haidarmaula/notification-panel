import { Show, For, createSignal } from 'solid-js';
import { A, useParams, useNavigate } from '@solidjs/router';
import { createQuery, useQueryClient } from '@tanstack/solid-query';
import { Dialog } from '@kobalte/core/dialog';
import { ArrowLeft, ArrowUpRight, CalendarClock, Pencil, Send, Trash2, X } from 'lucide-solid';
import { useAuth } from '../../app/auth';
import { data, request, errorMessage, ApiError } from '../../lib/api/client';
import type { Campaign } from '../../lib/api/types';
import { Failure, Loading, Status } from '../../components/ui/Feedback';
export default function Detail() {
  const params = useParams();
  const auth = useAuth();
  const client = useQueryClient();
  const navigate = useNavigate();
  const [action, setAction] = createSignal<'send' | 'schedule' | 'delete' | null>(null);
  const [scheduled, setScheduled] = createSignal('');
  const [pending, setPending] = createSignal(false);
  const [error, setError] = createSignal('');
  const [success, setSuccess] = createSignal('');
  const path = () => `/apps/${params.appId}/campaigns/${params.campaignId}`;
  const query = createQuery(() => ({
    queryKey: ['campaign', params.appId, params.campaignId],
    queryFn: ({ signal }) => data<Campaign>(path(), signal),
  }));
  async function confirm() {
    if (pending()) return;
    const operation = action();
    if (!operation) return;
    const appId = params.appId;
    const campaignId = params.campaignId;
    const destination = path();
    setError('');
    setPending(true);
    try {
      let body: unknown;
      if (operation === 'schedule') {
        const time = new Date(scheduled());
        if (!Number.isFinite(time.getTime()) || time.getTime() <= Date.now()) {
          setError('Pilih waktu pengiriman di masa depan.');
          return;
        }
        body = { scheduled_at: time.toISOString() };
      }
      await request(`/bff/api${destination}${operation === 'delete' ? '' : `/${operation}`}`, {
        method: operation === 'delete' ? 'DELETE' : 'POST',
        body,
        csrf: auth.csrf(),
        idempotencyKey:
          operation === 'send' ? `hello-dashboard:${appId}:${campaignId}:send` : undefined,
      });
      await client.invalidateQueries({ queryKey: ['campaigns', appId] });
      await client.invalidateQueries({ queryKey: ['campaign', appId, campaignId] });
      if (params.appId !== appId || params.campaignId !== campaignId) return;
      setAction(null);
      if (operation === 'delete') navigate(`/apps/${appId}/campaigns`);
      else
        setSuccess(
          operation === 'send'
            ? 'Permintaan kirim diterima. Hasil perangkat akan tercatat terpisah.'
            : 'Jadwal pengiriman diterima.',
        );
    } catch (error) {
      if (error instanceof ApiError && error.status === 401)
        window.dispatchEvent(new Event('hello:session-expired'));
      setError(errorMessage(error));
    } finally {
      setPending(false);
    }
  }
  function open(value: 'send' | 'schedule' | 'delete') {
    setError('');
    setScheduled('');
    setAction(value);
  }
  return (
    <>
      <A class="back-link" href={`/apps/${params.appId}/campaigns`}>
        <ArrowLeft size={16} />
        Semua campaign
      </A>
      <Show when={!query.isPending} fallback={<Loading />}>
        <Show
          when={!query.isError}
          fallback={<Failure error={query.error} retry={() => void query.refetch()} />}
        >
          <Show when={query.data}>
            {(campaign) => (
              <>
                <header class="page-heading detail-heading">
                  <div>
                    <div class="heading-meta">
                      <span class="eyebrow">CAMPAIGN #{campaign().id}</span>
                      <Status value={campaign().status} />
                    </div>
                    <h1>{campaign().title}</h1>
                  </div>
                  <div class="actions">
                    <Show when={campaign().status === 'DRAFT'}>
                      <A class="button secondary" href={`${path()}/edit`}>
                        <Pencil size={16} />
                        Edit draft
                      </A>
                      <button class="button primary" onClick={() => open('send')}>
                        <Send size={16} />
                        Tinjau pengiriman
                      </button>
                    </Show>
                    <Show when={campaign().status !== 'DRAFT'}>
                      <A class="button primary" href={`${path()}/results`}>
                        Lihat hasil
                        <ArrowUpRight size={16} />
                      </A>
                    </Show>
                  </div>
                </header>
                <Show when={success()}>
                  <div class="notice notice-success" role="status">
                    {success()}
                  </div>
                </Show>
                <div class="detail-grid">
                  <section class="surface">
                    <h2>Isi notifikasi</h2>
                    <dl class="message-detail">
                      <dt>Judul</dt>
                      <dd>{campaign().title}</dd>
                      <dt>Pesan</dt>
                      <dd class="preserve-lines">{campaign().body}</dd>
                      <Show when={campaign().template_name}>
                        <dt>Template</dt>
                        <dd>{campaign().template_name}</dd>
                      </Show>
                    </dl>
                    <div class="section-divider" />
                    <h2>Penerima</h2>
                    <ul class="target-list">
                      <For each={campaign().targets}>
                        {(target) => (
                          <li>
                            {target.type === 'BROADCAST'
                              ? 'Semua penerima dalam aplikasi'
                              : `${target.type === 'SEGMENT' ? 'Segmen' : target.type === 'INDIVIDUAL' ? 'Pengguna' : 'Target tersimpan'} #${target.id ?? '—'}`}
                          </li>
                        )}
                      </For>
                    </ul>
                    <p class="help-text">
                      Target ini bukan perkiraan jumlah perangkat. Kelayakan diperiksa oleh backend
                      saat persiapan.
                    </p>
                  </section>
                  <aside>
                    <div class="notification-preview">
                      <div class="preview-heading">
                        <span class="sample-mark">h.</span>
                        <strong>Pratinjau notifikasi</strong>
                      </div>
                      <h3>{campaign().title}</h3>
                      <p class="preserve-lines">{campaign().body}</p>
                    </div>
                    <p class="help-text preview-note">
                      Tampilan sebenarnya mengikuti pengaturan perangkat penerima.
                    </p>
                    <Show when={campaign().status === 'DRAFT'}>
                      <div class="secondary-actions">
                        <button class="button secondary" onClick={() => open('schedule')}>
                          <CalendarClock size={17} />
                          Jadwalkan pengiriman
                        </button>
                        <button class="button text-danger" onClick={() => open('delete')}>
                          <Trash2 size={16} />
                          Hapus draft
                        </button>
                      </div>
                    </Show>
                    <A class="quiet-link" href={`${path()}/results`}>
                      Riwayat dan hasil campaign
                      <ArrowUpRight size={14} />
                    </A>
                  </aside>
                </div>
              </>
            )}
          </Show>
        </Show>
      </Show>
      <Dialog
        open={action() !== null}
        onOpenChange={(value) => {
          if (!value && !pending()) setAction(null);
        }}
      >
        <Dialog.Portal>
          <Dialog.Overlay class="dialog-overlay" />
          <div class="dialog-position">
            <Dialog.Content class="dialog-content">
              <Dialog.CloseButton
                class="dialog-close icon-button"
                disabled={pending()}
                aria-label="Tutup"
              >
                <X size={18} />
              </Dialog.CloseButton>
              <Dialog.Title>
                {action() === 'delete'
                  ? 'Hapus draft ini?'
                  : action() === 'schedule'
                    ? 'Jadwalkan campaign'
                    : 'Tinjau sebelum mengirim'}
              </Dialog.Title>
              <Dialog.Description>
                {action() === 'delete'
                  ? 'Draft yang dihapus tidak dapat dikembalikan dari dashboard.'
                  : 'Tindakan ini memproses campaign nyata pada aplikasi yang Anda pilih.'}
              </Dialog.Description>
              <div class="confirmation-summary">
                <strong>{query.data?.title}</strong>
                <span>{query.data?.body}</span>
                <ul>
                  <For each={query.data?.targets}>
                    {(target) => (
                      <li>
                        {target.type === 'BROADCAST'
                          ? 'Semua penerima aplikasi'
                          : `${target.type} #${target.id}`}
                      </li>
                    )}
                  </For>
                </ul>
              </div>
              <Show when={action() === 'schedule'}>
                <label>
                  Waktu pengiriman
                  <input
                    type="datetime-local"
                    required
                    value={scheduled()}
                    onInput={(event) => setScheduled(event.currentTarget.value)}
                  />
                </label>
                <p class="help-text">
                  Zona waktu perangkat: {Intl.DateTimeFormat().resolvedOptions().timeZone}
                </p>
              </Show>
              <Show when={action() === 'send'}>
                <p class="help-text">
                  Permintaan yang diterima belum berarti pesan sampai ke perangkat. Pembatasan
                  pengiriman tetap berlaku.
                </p>
              </Show>
              <Show when={error()}>
                <p class="notice notice-error" role="alert">
                  {error()}
                </p>
              </Show>
              <div class="dialog-actions">
                <Dialog.CloseButton
                  aria-label="Kembali"
                  class="button secondary"
                  disabled={pending()}
                >
                  Kembali
                </Dialog.CloseButton>
                <button
                  class={`button ${action() === 'delete' ? 'danger' : 'primary'}`}
                  disabled={pending()}
                  onClick={() => void confirm()}
                >
                  {pending()
                    ? 'Memproses…'
                    : action() === 'delete'
                      ? 'Hapus draft'
                      : action() === 'schedule'
                        ? 'Konfirmasi jadwal'
                        : 'Konfirmasi kirim'}
                </button>
              </div>
            </Dialog.Content>
          </div>
        </Dialog.Portal>
      </Dialog>
    </>
  );
}
