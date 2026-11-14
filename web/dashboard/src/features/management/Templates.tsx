import { For, Show, createEffect, createSignal } from 'solid-js';
import { A, useNavigate, useParams } from '@solidjs/router';
import { createQuery, useQueryClient } from '@tanstack/solid-query';
import { data } from '../../lib/api/client';
import type { components } from '../../lib/api/generated/staff';
import { createPage, Pager, QueryState, useWrite, WriteFeedback } from './shared';
import { useUnsaved } from '../../app/unsaved';
import { Status } from '../../components/ui/Feedback';
type Template = components['schemas']['Template'];
export default function Templates() {
  const params = useParams();
  const page = createPage<Template>(() => `/apps/${params.appId}/templates`);
  return (
    <>
      <header class="page-heading">
        <div>
          <h1>Template</h1>
          <p class="muted">Simpan pesan yang sering digunakan untuk campaign berikutnya.</p>
        </div>
        <A class="button primary" href={`/apps/${params.appId}/templates/new`}>
          Buat template
        </A>
      </header>
      <QueryState query={page.query}>
        <Show
          when={page.query.data?.items.length}
          fallback={
            <div class="empty-state">
              <h2>Belum ada template.</h2>
              <p>Buat satu template untuk mengurangi penulisan pesan berulang.</p>
            </div>
          }
        >
          <div class="table-wrap">
            <table class="directory-table template-table">
              <thead>
                <tr>
                  <th>Template</th>
                  <th>Judul pesan</th>
                  <th>Status</th>
                  <th>Tindakan</th>
                </tr>
              </thead>
              <tbody>
                <For each={page.query.data?.items}>
                  {(item) => (
                    <tr>
                      <td>
                        <A
                          class="campaign-title"
                          href={`/apps/${params.appId}/templates/${item.id}`}
                        >
                          {item.name}
                        </A>
                        <span class="row-meta">#{item.id}</span>
                      </td>
                      <td>{item.title_template}</td>
                      <td>
                        <Status value={item.is_active ? 'ACTIVE' : 'INACTIVE'} />
                      </td>
                      <td>
                        <Show when={item.is_active}>
                          <A
                            class="button secondary small"
                            href={`/apps/${params.appId}/campaigns/new?template=${item.id}`}
                          >
                            Gunakan
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
        <Pager page={page} />
      </QueryState>
    </>
  );
}
export function TemplateEditor() {
  const params = useParams();
  const navigate = useNavigate();
  const client = useQueryClient();
  const operation = useWrite();
  const setDirty = useUnsaved();
  const [name, setName] = createSignal('');
  const [title, setTitle] = createSignal('');
  const [body, setBody] = createSignal('');
  const [active, setActive] = createSignal(true);
  const query = createQuery(() => ({
    queryKey: ['template', params.appId, params.templateId],
    queryFn: ({ signal }) =>
      data<Template>(`/apps/${params.appId}/templates/${params.templateId}`, signal),
    enabled: !!params.templateId,
  }));
  let loaded = false;
  createEffect(() => {
    if (query.data && !loaded) {
      loaded = true;
      setName(query.data.name);
      setTitle(query.data.title_template);
      setBody(query.data.body_template);
      setActive(query.data.is_active);
    }
  });
  async function save(event: SubmitEvent) {
    event.preventDefault();
    const appId = params.appId;
    const destination = `/apps/${appId}/templates`;
    const origin = window.location.pathname;
    const result = await operation.write<{ data: Template }>(
      `${destination}${params.templateId ? `/${params.templateId}` : ''}`,
      params.templateId ? 'PUT' : 'POST',
      { name: name().trim(), title_template: title(), body_template: body(), is_active: active() },
    );
    if (result) {
      setDirty(false);
      client.setQueryData(['template', appId, result.data.id], result.data);
      await client.invalidateQueries({ queryKey: ['directory', destination] });
      if (window.location.pathname === origin) navigate(destination);
    }
  }
  return (
    <>
      <A class="back-link" href={`/apps/${params.appId}/templates`}>
        ← Template
      </A>
      <header class="page-heading">
        <h1>{params.templateId ? 'Edit template' : 'Buat template'}</h1>
      </header>
      <Show when={!params.templateId || !query.isPending}>
        <Show
          when={!query.isError}
          fallback={
            <QueryState query={query}>
              <></>
            </QueryState>
          }
        >
          <form
            onInput={() => setDirty(true)}
            class="editor-grid"
            onSubmit={(event) => void save(event)}
          >
            <fieldset class="surface stack" disabled={operation.blocked()}>
              <label>
                Nama template
                <input
                  required
                  maxLength={255}
                  value={name()}
                  onInput={(event) => setName(event.currentTarget.value)}
                />
              </label>
              <label>
                Judul notifikasi
                <input
                  required
                  value={title()}
                  onInput={(event) => setTitle(event.currentTarget.value)}
                />
              </label>
              <label>
                Pesan
                <textarea
                  required
                  rows={6}
                  value={body()}
                  onInput={(event) => setBody(event.currentTarget.value)}
                />
              </label>
              <label class="check-label">
                <input
                  type="checkbox"
                  checked={active()}
                  onChange={(event) => setActive(event.currentTarget.checked)}
                />
                Aktif untuk campaign baru
              </label>
              <WriteFeedback operation={operation} />
              <div class="actions">
                <A class="button secondary" href={`/apps/${params.appId}/templates`}>
                  Kembali
                </A>
                <button class="button primary" type="submit">
                  {operation.pending() ? 'Menyimpan…' : 'Simpan template'}
                </button>
              </div>
            </fieldset>
            <aside class="surface">
              <h2>Pratinjau isi</h2>
              <h3>{title() || 'Judul notifikasi'}</h3>
              <p class="message-body">{body() || 'Isi pesan Anda.'}</p>
              <p class="help-text">
                Template mengisi draft; tidak mengirim notifikasi. Periksa kembali isi pesan sebelum
                pengiriman.
              </p>
            </aside>
          </form>
        </Show>
      </Show>
    </>
  );
}
