import { For, Show, createEffect, createSignal } from 'solid-js';
import { A, useNavigate, useParams } from '@solidjs/router';
import { createQuery, useQueryClient } from '@tanstack/solid-query';
import { data } from '../../lib/api/client';
import type { components } from '../../lib/api/generated/staff';
import { createPage, Pager, QueryState, useWrite, WriteFeedback, useUnsaved } from './shared';
type Segment = components['schemas']['Segment'];
type Member = components['schemas']['Member'];
type User = components['schemas']['User'];
export default function Segments() {
  const params = useParams();
  const page = createPage<Segment>(() => `/apps/${params.appId}/segments`);
  return (
    <>
      <header class="page-heading">
        <div>
          <span class="eyebrow">AUDIENCE</span>
          <h1>Segment</h1>
          <p class="muted">Kelompok penerima dengan anggota yang Anda kelola secara manual.</p>
        </div>
        <A class="button primary" href={`/apps/${params.appId}/segments/new`}>
          Buat segment
        </A>
      </header>
      <QueryState query={page.query}>
        <Show
          when={page.query.data?.items.length}
          fallback={
            <div class="empty-state">
              <h2>Belum ada segment.</h2>
              <p>Buat kelompok lalu tambahkan pengguna sebagai anggota.</p>
            </div>
          }
        >
          <div class="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Segment</th>
                  <th>Deskripsi</th>
                  <th>Jenis</th>
                </tr>
              </thead>
              <tbody>
                <For each={page.query.data?.items}>
                  {(item) => (
                    <tr>
                      <td>
                        <A href={`/apps/${params.appId}/segments/${item.id}`}>{item.name}</A>
                        <span class="row-meta">#{item.id}</span>
                      </td>
                      <td>{item.description || '—'}</td>
                      <td>Manual</td>
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
export function SegmentEditor() {
  const params = useParams();
  const navigate = useNavigate();
  const client = useQueryClient();
  const operation = useWrite();
  const setDirty = useUnsaved();
  const [name, setName] = createSignal('');
  const [description, setDescription] = createSignal('');
  const query = createQuery(() => ({
    queryKey: ['segment', params.appId, params.segmentId],
    queryFn: ({ signal }) =>
      data<Segment>(`/apps/${params.appId}/segments/${params.segmentId}`, signal),
    enabled: !!params.segmentId,
  }));
  let loaded = false;
  createEffect(() => {
    if (query.data && !loaded) {
      loaded = true;
      setName(query.data.name);
      setDescription(query.data.description ?? '');
    }
  });
  const base = () => `/apps/${params.appId}/segments`;
  async function save(event: SubmitEvent) {
    event.preventDefault();
    const origin = window.location.pathname;
    const destination = base();
    const result = await operation.write<{ data: Segment }>(
      `${destination}${params.segmentId ? `/${params.segmentId}` : ''}`,
      params.segmentId ? 'PUT' : 'POST',
      { name: name().trim(), description: description().trim() || null },
    );
    if (result) {
      setDirty(false);
      await client.invalidateQueries({ queryKey: ['directory', destination] });
      if (params.segmentId) await query.refetch();
      if (window.location.pathname === origin) navigate(`${destination}/${result.data.id}`);
    }
  }
  async function remove() {
    if (
      !window.confirm(
        `Hapus segment “${name()}”? Tindakan ini menghapus kelompok dan keanggotaannya.`,
      )
    )
      return;
    const origin = window.location.pathname;
    const destination = base();
    const result = await operation.write<{ success: boolean }>(
      `${destination}/${params.segmentId}`,
      'DELETE',
    );
    if (result) {
      setDirty(false);
      await client.invalidateQueries({ queryKey: ['directory', destination] });
      if (window.location.pathname === origin) navigate(destination);
    }
  }
  return (
    <>
      <A class="back-link" href={base()}>
        ← Segment
      </A>
      <header class="page-heading">
        <div>
          <span class="eyebrow">AUDIENCE / SEGMENT MANUAL</span>
          <h1>{params.segmentId ? name() || 'Detail segment' : 'Buat segment'}</h1>
        </div>
      </header>
      <Show when={!params.segmentId || !query.isPending}>
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
            class="surface stack"
            onSubmit={(event) => void save(event)}
          >
            <fieldset class="stack" disabled={operation.blocked()}>
              <label>
                Nama segment
                <input
                  required
                  maxLength={255}
                  value={name()}
                  onInput={(event) => setName(event.currentTarget.value)}
                />
              </label>
              <label>
                Deskripsi
                <textarea
                  rows={3}
                  value={description()}
                  onInput={(event) => setDescription(event.currentTarget.value)}
                />
              </label>
              <WriteFeedback operation={operation} />
              <div class="actions">
                <button class="button primary" type="submit">
                  {operation.pending() ? 'Menyimpan…' : 'Simpan segment'}
                </button>
                <Show when={params.segmentId}>
                  <button class="button text-danger" type="button" onClick={() => void remove()}>
                    Hapus segment
                  </button>
                </Show>
              </div>
            </fieldset>
          </form>
          <Show when={params.segmentId}>
            <SegmentMembers />
          </Show>
        </Show>
      </Show>
    </>
  );
}
function SegmentMembers() {
  const params = useParams();
  const operation = useWrite();
  const client = useQueryClient();
  const [adding, setAdding] = createSignal(false);
  const path = () => `/apps/${params.appId}/segments/${params.segmentId}/members`;
  const members = createPage<Member>(path);
  const users = createPage<User>(() => `/apps/${params.appId}/users`);
  async function change(id: string, add: boolean) {
    if (!add && !window.confirm('Keluarkan pengguna ini dari segment?')) return;
    const destination = path();
    const result = await operation.write<{ success: boolean }>(
      `${destination}/${id}`,
      add ? 'PUT' : 'DELETE',
    );
    if (result) await client.invalidateQueries({ queryKey: ['directory', destination] });
  }
  return (
    <section class="surface stack management-section">
      <div class="section-bar">
        <h2>Anggota segment</h2>
        <button
          class="button secondary small"
          aria-expanded={adding()}
          onClick={() => setAdding(!adding())}
        >
          {adding() ? 'Tutup pemilih' : 'Tambah anggota'}
        </button>
      </div>
      <p class="help-text">
        Anggota segment tidak sama dengan perangkat yang memenuhi syarat saat pengiriman. Satu
        pengguna dapat memiliki beberapa perangkat.
      </p>
      <WriteFeedback operation={operation} />
      <QueryState query={members.query}>
        <Show
          when={members.query.data?.items.length}
          fallback={<p>Belum ada anggota di segment ini.</p>}
        >
          <div class="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Pengguna</th>
                  <th>ID eksternal</th>
                  <th>Tindakan</th>
                </tr>
              </thead>
              <tbody>
                <For each={members.query.data?.items}>
                  {(item) => (
                    <tr>
                      <td>{item.name || `Pengguna #${item.user_id}`}</td>
                      <td>{item.external_id}</td>
                      <td>
                        <button
                          class="button text-danger small"
                          disabled={operation.blocked()}
                          onClick={() => void change(item.user_id, false)}
                          aria-label={`Keluarkan ${item.name || item.user_id}`}
                        >
                          Keluarkan
                        </button>
                      </td>
                    </tr>
                  )}
                </For>
              </tbody>
            </table>
          </div>
        </Show>
        <Pager page={members} />
      </QueryState>
      <Show when={adding()}>
        <div class="audience-picker">
          <h3>Pilih pengguna dari aplikasi ini</h3>
          <QueryState query={users.query}>
            <For each={users.query.data?.items}>
              {(user) => (
                <div class="member-choice">
                  <span>
                    {user.name || user.external_id} <small>#{user.id}</small>
                  </span>
                  <button
                    class="button secondary small"
                    disabled={
                      operation.blocked() ||
                      members.query.data?.items.some((member) => member.user_id === user.id)
                    }
                    onClick={() => void change(user.id, true)}
                    aria-label={`Tambahkan ${user.name || user.external_id}`}
                  >
                    Tambahkan
                  </button>
                </div>
              )}
            </For>
            <Show when={!users.query.data?.items.length}>
              <p>Belum ada pengguna.</p>
            </Show>
            <Pager page={users} />
          </QueryState>
        </div>
      </Show>
    </section>
  );
}
