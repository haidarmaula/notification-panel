import { Show, For, createSignal, createEffect, onCleanup, onMount } from 'solid-js';
import { A, useNavigate, useParams, useSearchParams } from '@solidjs/router';
import { useUnsaved } from '../../app/unsaved';
import { createQuery, useQueryClient } from '@tanstack/solid-query';
import { ArrowLeft, Check, Plus, X } from 'lucide-solid';
import { useAuth } from '../../app/auth';
import { data, request, errorMessage, ApiError } from '../../lib/api/client';
import type { Campaign, Page } from '../../lib/api/types';
import { Failure, Loading, RecipientLabel } from '../../components/ui/Feedback';
import { draftSchema } from './validation';

type Target = { type: 'BROADCAST' } | { type: 'SEGMENT' | 'INDIVIDUAL'; id: string };
export default function Editor() {
  let alive = true;
  onCleanup(() => {
    alive = false;
  });
  const params = useParams();
  const [search] = useSearchParams();
  const selectedTemplate = () =>
    !params.campaignId &&
    typeof search.template === 'string' &&
    /^[1-9][0-9]{0,18}$/.test(search.template)
      ? search.template
      : null;
  const template = createQuery(() => ({
    queryKey: ['template', params.appId, selectedTemplate()],
    queryFn: ({ signal }) =>
      data<{
        id: string;
        name: string;
        title_template: string;
        body_template: string;
        is_active: boolean;
      }>(`/apps/${params.appId}/templates/${selectedTemplate()}`, signal),
    enabled: !!selectedTemplate(),
  }));
  const templateData = () => (template.isSuccess ? template.data : undefined);
  const [templateLoaded, setTemplateLoaded] = createSignal(false);
  const auth = useAuth();
  const navigate = useNavigate();
  const client = useQueryClient();
  const [title, setTitle] = createSignal('');
  const [body, setBody] = createSignal('');
  const [targets, setTargets] = createSignal<Target[]>([]);
  const [individualTargets, setIndividualTargets] = createSignal<Target[]>([]);
  const [targetNames, setTargetNames] = createSignal<Record<string, string>>({});
  const [kind, setKind] = createSignal<'INDIVIDUAL' | 'SEGMENT'>('INDIVIDUAL');
  const [cursor, setCursor] = createSignal('');
  const [loaded, setLoaded] = createSignal('');
  const [fieldErrors, setFieldErrors] = createSignal<
    Partial<Record<'title' | 'body' | 'targets', string>>
  >({});
  const [previewOpen, setPreviewOpen] = createSignal(
    window.matchMedia('(min-width: 761px)').matches,
  );
  let titleInput: HTMLInputElement | undefined;
  let bodyInput: HTMLTextAreaElement | undefined;
  let targetsInput: HTMLInputElement | undefined;
  onMount(() => {
    const wide = window.matchMedia('(min-width: 761px)');
    const resize = () => setPreviewOpen(wide.matches);
    wide.addEventListener('change', resize);
    onCleanup(() => wide.removeEventListener('change', resize));
  });
  const [error, setError] = createSignal('');
  const [pending, setPending] = createSignal(false);
  const setDirty = useUnsaved();
  const [uncertain, setUncertain] = createSignal(false);
  const [savedTitle, setSavedTitle] = createSignal<string | null>(null);
  const editing = () => !!params.campaignId;
  createEffect(() => {
    const selected = templateData();
    if (selected?.is_active && !templateLoaded()) {
      setTemplateLoaded(true);
      setTitle(selected.title_template);
      setBody(selected.body_template);
      setDirty(true);
    }
  });
  const draft = createQuery(() => ({
    queryKey: ['campaign', params.appId, params.campaignId],
    queryFn: ({ signal }) =>
      data<Campaign>(`/apps/${params.appId}/campaigns/${params.campaignId}`, signal),
    enabled: editing(),
  }));
  const choices = createQuery(() => ({
    queryKey: ['audience', params.appId, kind(), cursor()],
    queryFn: ({ signal }) =>
      data<
        Page<{
          id: string;
          name?: string | null;
          external_id?: string;
          description?: string | null;
        }>
      >(
        `/apps/${params.appId}/${kind() === 'INDIVIDUAL' ? 'users' : 'segments'}?limit=20${cursor() ? `&after_id=${encodeURIComponent(cursor())}` : ''}`,
        signal,
      ),
  }));
  createEffect(() => {
    const campaign = draft.data;
    const identity = `${params.appId}:${params.campaignId}`;
    if (campaign && loaded() !== identity) {
      setTitle(campaign.title);
      setBody(campaign.body);
      setTargets(
        campaign.targets.flatMap<Target>((target) =>
          target.type === 'BROADCAST'
            ? [{ type: 'BROADCAST' }]
            : (target.type === 'INDIVIDUAL' || target.type === 'SEGMENT') && target.id
              ? [{ type: target.type, id: target.id }]
              : [],
        ),
      );
      setLoaded(identity);
    }
  });
  function add(item: { id: string; name?: string | null; external_id?: string }) {
    const selected: Target = { type: kind(), id: item.id };
    if (
      !targets().some(
        (target) => target.type === selected.type && 'id' in target && target.id === item.id,
      )
    ) {
      setTargets([...targets().filter((target) => target.type !== 'BROADCAST'), selected]);
      setTargetNames({
        ...targetNames(),
        [`${kind()}:${item.id}`]: item.name || item.external_id || '',
      });
      setFieldErrors({ ...fieldErrors(), targets: undefined });
      setDirty(true);
    }
  }
  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (pending() || uncertain()) return;
    setError('');
    setFieldErrors({});
    const parsed = draftSchema.safeParse({ title: title(), body: body(), targets: targets() });
    if (!parsed.success) {
      const errors = parsed.error.flatten().fieldErrors;
      setFieldErrors({
        title: errors.title?.[0],
        body: errors.body?.[0],
        targets: errors.targets?.[0],
      });
      const invalid = errors.title ? titleInput : errors.body ? bodyInput : targetsInput;
      invalid?.focus();
      invalid?.scrollIntoView({ block: 'center' });
      return;
    }
    const appId = params.appId;
    const campaignId = params.campaignId;
    setPending(true);
    try {
      const result = await request<{ data: { id: string } }>(
        `/bff/api/apps/${appId}/campaigns${campaignId ? `/${campaignId}` : ''}`,
        {
          method: editing() ? 'PUT' : 'POST',
          csrf: auth.csrf(),
          body: {
            ...parsed.data,
            template_id: draft.data?.template_id ?? (templateLoaded() ? selectedTemplate() : null),
          },
        },
      );
      if (alive) setDirty(false);
      await client.invalidateQueries({ queryKey: ['campaigns', appId] });
      await client.invalidateQueries({ queryKey: ['campaign', appId, result.data.id] });
      if (alive && params.appId === appId && params.campaignId === campaignId)
        navigate(`/apps/${appId}/campaigns/${result.data.id}`);
    } catch (error) {
      if (error instanceof ApiError && error.status === 401)
        window.dispatchEvent(new Event('hello:session-expired'));
      if (error instanceof ApiError && (error.status === 0 || error.status >= 500)) {
        setUncertain(true);
        setSavedTitle(title());
      }
      setError(errorMessage(error));
    } finally {
      setPending(false);
    }
  }
  const editable = () =>
    !editing() ||
    (draft.data?.status === 'DRAFT' &&
      !draft.data.targets.some((target) => target.type === 'UPLOAD'));
  return (
    <>
      <A class="back-link" href={`/apps/${params.appId}/campaigns`}>
        <ArrowLeft size={16} />
        Semua campaign
      </A>
      <header class="page-heading">
        <div>
          <span class="eyebrow">{editing() ? `DRAFT #${params.campaignId}` : 'CAMPAIGN BARU'}</span>
          <h1>{editing() ? 'Edit draft' : 'Buat campaign'}</h1>
          <p class="muted">Simpan sebagai draft. Pengiriman dilakukan pada langkah terpisah.</p>
        </div>
      </header>
      <Show when={selectedTemplate()}>
        <Show when={template.isError}>
          <Failure error={template.error} retry={() => void template.refetch()} />
        </Show>
        <Show when={templateData() && !templateData()?.is_active}>
          <p class="notice" role="alert">
            Template ini sudah nonaktif. Pilih template lain atau buat draft tanpa template.
          </p>
        </Show>
        <Show when={templateData()?.is_active}>
          <p class="notice">
            Dari template: {templateData()?.name}. Periksa dan sesuaikan isi pesan sebelum
            menyimpan.
          </p>
        </Show>
      </Show>
      <Show when={!editing() || !draft.isPending} fallback={<Loading />}>
        <Show
          when={!draft.isError}
          fallback={<Failure error={draft.error} retry={() => void draft.refetch()} />}
        >
          <Show
            when={editable()}
            fallback={
              <div class="notice">
                Campaign ini tidak dapat diedit dari formulir ini. Hanya draft dengan target yang
                didukung yang dapat diubah.
              </div>
            }
          >
            <form class="editor-grid" noValidate onSubmit={(event) => void save(event)}>
              <div class="stack">
                <section class="surface stack">
                  <div class="section-heading">
                    <span class="step-number">01</span>
                    <div>
                      <h2>Isi pesan</h2>
                      <p class="muted">Buat judul singkat dan pesan yang mudah dipahami.</p>
                    </div>
                  </div>
                  <label>
                    Judul notifikasi
                    <input
                      ref={titleInput}
                      aria-label="Judul notifikasi"
                      aria-describedby="title-bytes title-error"
                      aria-invalid={!!fieldErrors().title}
                      value={title()}
                      onInput={(event) => {
                        setTitle(event.currentTarget.value);
                        setFieldErrors({ ...fieldErrors(), title: undefined });
                        setDirty(true);
                      }}
                      placeholder="Apa yang ingin Anda sampaikan?"
                      required
                      disabled={
                        pending() ||
                        uncertain() ||
                        (!!selectedTemplate() && (!templateLoaded() || !templateData()?.is_active))
                      }
                    />
                    <span class="field-feedback">
                      <span id="title-error" class="field-error" aria-live="polite">
                        {fieldErrors().title}
                      </span>
                      <span id="title-bytes" class="field-hint">
                        {new TextEncoder().encode(title()).length}/255 byte
                      </span>
                    </span>
                  </label>
                  <label>
                    Pesan
                    <textarea
                      ref={bodyInput}
                      aria-label="Pesan"
                      aria-describedby="body-bytes body-error"
                      aria-invalid={!!fieldErrors().body}
                      rows={5}
                      value={body()}
                      onInput={(event) => {
                        setBody(event.currentTarget.value);
                        setFieldErrors({ ...fieldErrors(), body: undefined });
                        setDirty(true);
                      }}
                      placeholder="Tulis pesan untuk penerima Anda…"
                      required
                      disabled={
                        pending() ||
                        uncertain() ||
                        (!!selectedTemplate() && (!templateLoaded() || !templateData()?.is_active))
                      }
                    />
                    <span class="field-feedback">
                      <span id="body-error" class="field-error" aria-live="polite">
                        {fieldErrors().body}
                      </span>
                      <span id="body-bytes" class="field-hint">
                        {new TextEncoder().encode(body()).length}/4096 byte
                      </span>
                    </span>
                  </label>
                </section>
                <fieldset
                  class="surface stack"
                  aria-labelledby="recipients-heading"
                  aria-describedby="targets-error"
                  aria-invalid={!!fieldErrors().targets}
                >
                  <div class="section-heading">
                    <span class="step-number">02</span>
                    <div>
                      <h2 id="recipients-heading">Tentukan penerima</h2>
                      <p class="muted">Pilih pengguna atau segmen dalam aplikasi ini.</p>
                    </div>
                  </div>
                  <label class="checkbox-label">
                    <input
                      ref={targetsInput}
                      type="checkbox"
                      aria-describedby="targets-error"
                      aria-invalid={!!fieldErrors().targets}
                      checked={targets().some((target) => target.type === 'BROADCAST')}
                      disabled={
                        pending() ||
                        uncertain() ||
                        (!!selectedTemplate() && (!templateLoaded() || !templateData()?.is_active))
                      }
                      onChange={(event) => {
                        if (event.currentTarget.checked) {
                          setIndividualTargets(targets());
                          setTargets([{ type: 'BROADCAST' }]);
                        } else {
                          setTargets(individualTargets());
                        }
                        setFieldErrors({ ...fieldErrors(), targets: undefined });
                        setDirty(true);
                      }}
                    />
                    Semua penerima dalam aplikasi
                  </label>
                  <Show when={!targets().some((target) => target.type === 'BROADCAST')}>
                    <label>
                      Jenis penerima
                      <select
                        disabled={
                          pending() ||
                          uncertain() ||
                          (!!selectedTemplate() &&
                            (!templateLoaded() || !templateData()?.is_active))
                        }
                        value={kind()}
                        onChange={(event) => {
                          setKind(event.currentTarget.value as 'INDIVIDUAL' | 'SEGMENT');
                          setCursor('');
                        }}
                      >
                        <option value="INDIVIDUAL">Pengguna</option>
                        <option value="SEGMENT">Segmen</option>
                      </select>
                    </label>
                    <Show when={!choices.isPending} fallback={<Loading label="Memuat penerima…" />}>
                      <Show
                        when={!choices.isError}
                        fallback={
                          <Failure error={choices.error} retry={() => void choices.refetch()} />
                        }
                      >
                        <div class="audience-options">
                          <For each={choices.data?.items}>
                            {(item) => (
                              <button
                                type="button"
                                class="audience-option"
                                disabled={
                                  pending() ||
                                  uncertain() ||
                                  targets().length >= 100 ||
                                  targets().some(
                                    (target) =>
                                      target.type === kind() &&
                                      'id' in target &&
                                      target.id === item.id,
                                  )
                                }
                                onClick={() => add(item)}
                              >
                                <span>
                                  {item.name || item.external_id || `#${item.id}`}
                                  <small>#{item.id}</small>
                                </span>
                                <Plus size={16} />
                              </button>
                            )}
                          </For>
                          <Show when={!choices.data?.items.length}>
                            <p class="help-text">Belum ada penerima pada halaman ini.</p>
                          </Show>
                        </div>
                        <div class="actions">
                          <button
                            type="button"
                            class="button secondary small"
                            disabled={!cursor()}
                            onClick={() => setCursor('')}
                          >
                            Halaman pertama
                          </button>
                          <button
                            type="button"
                            class="button secondary small"
                            disabled={!choices.data?.next_after_id}
                            onClick={() => setCursor(choices.data?.next_after_id ?? '')}
                          >
                            Berikutnya
                          </button>
                        </div>
                      </Show>
                    </Show>
                    <div class="selected-targets">
                      <For each={targets()}>
                        {(target, index) => (
                          <span class="target-chip">
                            <RecipientLabel
                              appId={params.appId!}
                              target={target}
                              name={
                                'id' in target
                                  ? targetNames()[`${target.type}:${target.id}`]
                                  : undefined
                              }
                            />
                            <button
                              type="button"
                              class="icon-button"
                              disabled={
                                pending() ||
                                uncertain() ||
                                (!!selectedTemplate() &&
                                  (!templateLoaded() || !templateData()?.is_active))
                              }
                              aria-label={`Hapus target ${'id' in target ? target.id : ''}`}
                              onClick={() => {
                                setTargets(targets().filter((_, i) => i !== index()));
                                setDirty(true);
                              }}
                            >
                              <X size={13} />
                            </button>
                          </span>
                        )}
                      </For>
                    </div>
                  </Show>
                  <p id="targets-error" class="field-feedback field-error" aria-live="polite">
                    {fieldErrors().targets}
                  </p>
                </fieldset>
                <Show when={error()}>
                  <p class="notice notice-error" role="alert">
                    {error()}
                  </p>
                </Show>
                <Show when={uncertain()}>
                  <div class="notice">
                    <p>
                      Hasil simpan belum pasti untuk “{savedTitle()}”. Periksa daftar campaign
                      sebelum membuat atau menyimpan ulang.
                    </p>
                    <A class="button secondary" href={`/apps/${params.appId}/campaigns`}>
                      Periksa daftar campaign
                    </A>
                  </div>
                </Show>
              </div>
              <details
                class="editor-preview"
                open={previewOpen()}
                onToggle={(event) => setPreviewOpen(event.currentTarget.open)}
              >
                <summary>Pratinjau notifikasi</summary>
                <div class="notification-preview">
                  <div class="preview-heading">
                    <span class="sample-mark">h.</span>
                    <strong>Notifikasi aplikasi</strong>
                  </div>
                  <h3>{title() || 'Judul notifikasi Anda'}</h3>
                  <p class="preserve-lines">
                    {body() || 'Pesan akan tampil di sini saat Anda mulai menulis.'}
                  </p>
                </div>
                <p class="help-text preview-note">
                  Pratinjau membantu meninjau isi pesan. Tampilan akhir mengikuti perangkat
                  penerima.
                </p>
                <div class="editor-note">
                  <span class="step-number">!</span>
                  <p>
                    Menyimpan draft tidak mengirim notifikasi. Anda akan meninjau kembali isinya
                    sebelum mengirim.
                  </p>
                </div>
              </details>
              <div class="editor-actions">
                <A class="button secondary" href={`/apps/${params.appId}/campaigns`}>
                  Kembali
                </A>
                <button
                  type="submit"
                  class="button primary"
                  disabled={
                    pending() ||
                    uncertain() ||
                    (!!selectedTemplate() && (!templateLoaded() || !templateData()?.is_active))
                  }
                >
                  <Check size={17} />
                  {pending() ? 'Menyimpan…' : 'Simpan draft'}
                </button>
              </div>
            </form>
          </Show>
        </Show>
      </Show>
    </>
  );
}
