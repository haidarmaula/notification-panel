import { For, Show, createEffect, createSignal } from 'solid-js';
import { A, useNavigate, useParams } from '@solidjs/router';
import { createQuery, useQueryClient } from '@tanstack/solid-query';
import { ApiError, data, versionedData } from '../../lib/api/client';
import type { components } from '../../lib/api/generated/applications';
import { QueryState, useWrite, WriteFeedback } from '../management/shared';
import { useUnsaved } from '../../app/unsaved';
import { Failure, Status } from '../../components/ui/Feedback';

type Application = components['schemas']['Application'];
type Grant = components['schemas']['StaffGrant'];
type AppList = components['schemas']['ApplicationListResponse']['data'];
type GrantList = components['schemas']['StaffGrantListResponse']['data'];

const pageSize = 20;
const maxInt64 = 9223372036854775807n;

function validStaffID(value: string) {
  if (!/^[1-9][0-9]*$/.test(value)) return false;
  try {
    return BigInt(value) <= maxInt64;
  } catch {
    return false;
  }
}

function byteLength(value: string) {
  return new TextEncoder().encode(value).length;
}
function hasControlCharacters(value: string) {
  return Array.from(value).some(
    (character) => character.codePointAt(0)! < 32 || character.codePointAt(0) === 127,
  );
}

export default function Registry() {
  const client = useQueryClient();
  const navigate = useNavigate();
  const operation = useWrite();
  const setDirty = useUnsaved();
  const [page, setPage] = createSignal(1);
  const [keyFilter, setKeyFilter] = createSignal('');
  const [filterInput, setFilterInput] = createSignal('');
  const [key, setKey] = createSignal('');
  const [name, setName] = createSignal('');
  const [created, setCreated] = createSignal(false);
  const [formError, setFormError] = createSignal('');
  const list = createQuery(() => {
    const staticPage = page();
    const staticKey = keyFilter();
    return {
      queryKey: ['managed-applications', staticPage, staticKey],
      queryFn: ({ signal }) => {
        const query = new URLSearchParams({ page: String(staticPage), limit: String(pageSize) });
        if (staticKey) query.set('key', staticKey);
        return data<AppList>(`/admin/apps?${query.toString()}`, signal);
      },
    };
  });

  async function createApp(event: SubmitEvent) {
    event.preventDefault();
    setFormError('');
    setCreated(false);
    const cleanKey = key();
    const cleanName = name().trim();
    if (!/^[a-z][a-z0-9-]{1,61}[a-z0-9]$/.test(cleanKey)) {
      setFormError(
        'Gunakan kunci 3–63 karakter: huruf kecil di awal, lalu huruf, angka, atau tanda hubung.',
      );
      return;
    }
    if (!cleanName || byteLength(cleanName) > 128 || hasControlCharacters(cleanName)) {
      setFormError(
        'Nama wajib diisi, maksimal 128 byte UTF-8, dan tidak boleh memuat karakter kontrol.',
      );
      return;
    }
    const result = await operation.write<{ data: Application }>('/admin/apps', 'POST', {
      key: cleanKey,
      name: cleanName,
    });
    if (!result) return;
    setDirty(false);
    await client.invalidateQueries({ queryKey: ['managed-applications'] });
    setKey('');
    setName('');
    setCreated(true);
    navigate(`/apps/admin/applications/${result.data.id}`);
  }

  return (
    <>
      <header class="page-heading">
        <div>
          <h1>Registry aplikasi</h1>
          <p class="muted">
            Kelola identitas aplikasi dan akses staf yang diberikan secara eksplisit.
          </p>
        </div>
      </header>
      <section class="surface stack">
        <h2>Aplikasi terdaftar</h2>
        <form
          class="inline-form"
          onSubmit={(event) => {
            event.preventDefault();
            setKeyFilter(filterInput().trim());
            setPage(1);
          }}
        >
          <label>
            Filter kunci aplikasi
            <input
              value={filterInput()}
              onInput={(event) => setFilterInput(event.currentTarget.value)}
            />
          </label>
          <button class="button secondary" type="submit">
            Terapkan
          </button>
          <Show when={keyFilter()}>
            <button
              class="button secondary"
              type="button"
              onClick={() => {
                setKeyFilter('');
                setFilterInput('');
                setPage(1);
              }}
            >
              Hapus filter
            </button>
          </Show>
        </form>
        <QueryState query={list}>
          <Show
            when={list.data?.items.length}
            fallback={
              <div class="empty-state">
                <h3>Belum ada aplikasi.</h3>
                <p>Buat registry aplikasi di bawah untuk memulai pengelolaan.</p>
              </div>
            }
          >
            <div class="table-wrap">
              <table class="directory-table">
                <thead>
                  <tr>
                    <th>Aplikasi</th>
                    <th>Kunci</th>
                    <th>Status</th>
                    <th>ID</th>
                  </tr>
                </thead>
                <tbody>
                  <For each={list.data?.items}>
                    {(app) => (
                      <tr>
                        <td>
                          <A href={`/apps/admin/applications/${app.id}`}>{app.name}</A>
                        </td>
                        <td class="mono">{app.key}</td>
                        <td>
                          <Status value={app.status} />
                        </td>
                        <td class="mono">{app.id}</td>
                      </tr>
                    )}
                  </For>
                </tbody>
              </table>
            </div>
          </Show>
          <div class="pagination">
            <span>
              Halaman {page()} · {list.data?.total ?? 0} aplikasi
            </span>
            <div>
              <button
                class="button secondary small"
                disabled={page() <= 1 || list.isFetching}
                onClick={() => setPage(page() - 1)}
              >
                Sebelumnya
              </button>
              <button
                class="button secondary small"
                disabled={page() * pageSize >= (list.data?.total ?? 0) || list.isFetching}
                onClick={() => setPage(page() + 1)}
              >
                Berikutnya
              </button>
            </div>
          </div>
        </QueryState>
      </section>
      <section class="surface stack">
        <h2>Daftarkan aplikasi</h2>
        <p class="help-text">
          Aplikasi baru berstatus Disiapkan. Pendaftaran tidak mengaktifkan aplikasi atau memberi
          akses bisnis kepada siapa pun.
        </p>
        <form
          class="stack"
          onInput={() => setDirty(true)}
          onSubmit={(event) => void createApp(event)}
        >
          <fieldset class="stack" disabled={operation.blocked()}>
            <label>
              Kunci aplikasi
              <input
                required
                value={key()}
                onInput={(event) => setKey(event.currentTarget.value)}
                autocomplete="off"
              />
            </label>
            <p class="help-text">3–63 karakter ASCII; huruf kecil di awal, tanpa spasi.</p>
            <label>
              Nama aplikasi
              <input
                required
                value={name()}
                onInput={(event) => setName(event.currentTarget.value)}
                maxlength={128}
              />
            </label>
            <Show when={formError()}>
              <p class="notice notice-error" role="alert">
                {formError()}
              </p>
            </Show>
            <WriteFeedback operation={operation} />
            <Show when={created()}>
              <p class="notice notice-success" role="status">
                Registry aplikasi dibuat.
              </p>
            </Show>
            <button class="button primary" type="submit">
              {operation.pending() ? 'Mendaftarkan…' : 'Daftarkan aplikasi'}
            </button>
          </fieldset>
        </form>
      </section>
    </>
  );
}

export function RegistryDetail() {
  const params = useParams();
  const client = useQueryClient();
  const operation = useWrite();
  const [writeTarget, setWriteTarget] = createSignal<'name' | 'grant'>('name');
  const [renameError, setRenameError] = createSignal('');
  const setDirty = useUnsaved();
  const [name, setName] = createSignal('');
  const [nameDirty, setNameDirty] = createSignal(false);
  const [saved, setSaved] = createSignal(false);
  const [conflict, setConflict] = createSignal(false);
  const [staffID, setStaffID] = createSignal('');
  const [lookupID, setLookupID] = createSignal('');
  const [grantNotice, setGrantNotice] = createSignal('');
  const [grantError, setGrantError] = createSignal('');
  const [grantConflict, setGrantConflict] = createSignal(false);
  const [grantPage, setGrantPage] = createSignal(1);
  const app = createQuery(() => ({
    queryKey: ['managed-application', params.managedAppId],
    queryFn: ({ signal }) =>
      versionedData<Application>(`/admin/apps/${params.managedAppId}`, signal),
    enabled: !!params.managedAppId,
  }));
  const grants = createQuery(() => ({
    queryKey: ['application-staff-grants', params.managedAppId, grantPage()],
    queryFn: ({ signal }) =>
      data<GrantList>(
        `/admin/apps/${params.managedAppId}/staff-grants?page=${grantPage()}&limit=${pageSize}`,
        signal,
      ),
    enabled: !!params.managedAppId,
  }));
  const grantLookup = createQuery(() => ({
    queryKey: ['application-staff-grant', params.managedAppId, lookupID()],
    queryFn: ({ signal }) =>
      versionedData<Grant>(`/admin/apps/${params.managedAppId}/staff-grants/${lookupID()}`, signal),
    enabled: !!lookupID(),
    retry: false,
  }));
  createEffect(() => {
    const current = app.data?.data;
    if (current && !nameDirty()) setName(current.name);
  });

  async function rename(event: SubmitEvent) {
    event.preventDefault();
    if (operation.blocked() || conflict() || !app.data?.etag) return;
    setSaved(false);
    setConflict(false);
    setRenameError('');
    const cleanName = name().trim();
    if (!cleanName || byteLength(cleanName) > 128 || hasControlCharacters(cleanName)) {
      setRenameError('Nama wajib diisi, maksimal 128 byte UTF-8, tanpa karakter kontrol.');
      return;
    }
    setWriteTarget('name');
    const result = await operation.write<{ data: Application }>(
      `/admin/apps/${params.managedAppId}`,
      'PATCH',
      { name: cleanName },
      { ifMatch: app.data?.etag },
    );
    if (!result) {
      if (operation.status() === 412) setConflict(true);
      return;
    }
    setDirty(false);
    setNameDirty(false);
    setName(result.data.name);
    setSaved(true);
    await Promise.all([
      client.invalidateQueries({ queryKey: ['managed-application', params.managedAppId] }),
      client.invalidateQueries({ queryKey: ['managed-applications'] }),
    ]);
    await app.refetch();
  }

  async function refreshApp() {
    if (nameDirty() && !window.confirm('Muat ulang dan ganti perubahan nama yang belum disimpan?'))
      return;
    const result = await app.refetch();
    if (result.data) {
      setName(result.data.data.name);
      setConflict(false);
      setDirty(false);
      setNameDirty(false);
    }
  }

  async function inspectGrant(event: SubmitEvent) {
    event.preventDefault();
    setGrantError('');
    setGrantNotice('');
    setGrantConflict(false);
    const id = staffID();
    if (!validStaffID(id)) {
      setGrantError('Masukkan ID staf berupa bilangan bulat positif int64 tanpa nol di depan.');
      return;
    }
    setLookupID(id);
    await client.invalidateQueries({
      queryKey: ['application-staff-grant', params.managedAppId, id],
    });
  }

  async function refreshGrant() {
    const result = await grantLookup.refetch();
    if (result.isSuccess || (result.error instanceof ApiError && result.error.status === 404))
      setGrantConflict(false);
  }

  async function enableGrant() {
    if (
      operation.blocked() ||
      grantConflict() ||
      grantLookup.isFetching ||
      (!grantLookup.isSuccess && !lookupIsMissing())
    )
      return;
    const existing = grantLookup.data;
    const id = lookupID();
    if (!id || !window.confirm(`Berikan akses aplikasi kepada staf #${id}?`)) return;
    setGrantError('');
    setGrantNotice('');
    setGrantConflict(false);
    setWriteTarget('grant');
    const conditions = existing ? { ifMatch: existing.etag } : { ifNoneMatch: '*' as const };
    const result = await operation.write<{ data: Grant }>(
      `/admin/apps/${params.managedAppId}/staff-grants/${id}`,
      'PUT',
      { is_active: true },
      conditions,
    );
    if (!result) {
      if (operation.status() === 412) setGrantConflict(true);
      setGrantError(operation.error());
      return;
    }
    setGrantNotice(`Akses staf #${id} aktif.`);
    await Promise.all([
      client.invalidateQueries({ queryKey: ['application-staff-grants', params.managedAppId] }),
      client.invalidateQueries({ queryKey: ['application-staff-grant', params.managedAppId, id] }),
    ]);
    await grantLookup.refetch();
  }

  async function revokeGrant(grant: { data: Grant; etag: string }) {
    if (operation.blocked() || grantConflict() || grantLookup.isFetching) return;
    const id = grant.data.staff_id;
    if (!window.confirm(`Cabut akses aplikasi dari staf #${id}?`)) return;
    setGrantError('');
    setGrantNotice('');
    setGrantConflict(false);
    setWriteTarget('grant');
    const result = await operation.write<void>(
      `/admin/apps/${params.managedAppId}/staff-grants/${id}`,
      'DELETE',
      undefined,
      { ifMatch: grant.etag },
    );
    if (result === undefined && operation.error()) {
      if (operation.status() === 412) setGrantConflict(true);
      setGrantError(operation.error());
      return;
    }
    setGrantNotice(`Akses staf #${id} dicabut.`);
    await Promise.all([
      client.invalidateQueries({ queryKey: ['application-staff-grants', params.managedAppId] }),
      client.invalidateQueries({ queryKey: ['application-staff-grant', params.managedAppId, id] }),
    ]);
    if (lookupID() === id) await grantLookup.refetch();
  }

  const lookupIsMissing = () =>
    grantLookup.error instanceof ApiError && grantLookup.error.status === 404;
  return (
    <>
      <A class="back-link" href="/apps/admin/applications">
        ← Registry aplikasi
      </A>
      <QueryState query={app}>
        <>
          <header class="page-heading">
            <div>
              <h1>{app.data?.data.name}</h1>
              <p class="muted">Identitas aplikasi dan daftar pemberian akses staf.</p>
            </div>
          </header>
          <div class="settings-grid">
            <section class="surface stack">
              <h2>Identitas</h2>
              <dl class="settings-details">
                <dt>Kunci aplikasi</dt>
                <dd class="mono">{app.data?.data.key}</dd>
                <dt>ID aplikasi</dt>
                <dd class="mono">{app.data?.data.id}</dd>
                <dt>Status</dt>
                <dd>
                  <Status value={app.data!.data.status} />
                </dd>
                <dt>Revisi</dt>
                <dd class="mono">{app.data?.data.revision}</dd>
              </dl>
              <p class="help-text">Registry tidak menyediakan aktivasi atau perubahan status.</p>
              <form
                class="stack"
                onInput={() => {
                  setDirty(true);
                  setNameDirty(true);
                  setSaved(false);
                }}
                onSubmit={(event) => void rename(event)}
              >
                <fieldset class="stack" disabled={operation.blocked()}>
                  <label>
                    Nama aplikasi
                    <input
                      required
                      value={name()}
                      maxlength={128}
                      onInput={(event) => setName(event.currentTarget.value)}
                    />
                  </label>
                  <p class="help-text">
                    Maksimal 128 byte UTF-8. Perubahan nama memakai revisi yang dibaca terakhir.
                  </p>
                  <Show when={renameError()}>
                    <p class="notice notice-error" role="alert">
                      {renameError()}
                    </p>
                  </Show>
                  <Show when={writeTarget() === 'name'}>
                    <WriteFeedback operation={operation} />
                  </Show>
                  <Show when={saved()}>
                    <p class="notice notice-success" role="status">
                      Nama aplikasi diperbarui.
                    </p>
                  </Show>
                  <Show when={conflict()}>
                    <div class="notice notice-error" role="alert">
                      <p>
                        Aplikasi berubah sejak dibaca. Muat ulang untuk meninjau nama terbaru
                        sebelum mencoba kembali.
                      </p>
                      <button
                        class="button secondary"
                        type="button"
                        onClick={() => void refreshApp()}
                      >
                        Muat ulang aplikasi
                      </button>
                    </div>
                  </Show>
                  <button
                    class="button primary admin-submit"
                    type="submit"
                    disabled={conflict() || !app.data?.etag}
                  >
                    Simpan nama
                  </button>
                </fieldset>
              </form>
            </section>
            <section class="surface stack">
              <h2>Akses staf</h2>
              <p class="help-text">
                Cari staf berdasarkan ID, lalu berikan atau cabut akses setelah meninjau
                identitasnya.
              </p>
              <form class="inline-form" onSubmit={(event) => void inspectGrant(event)}>
                <label>
                  ID staf
                  <input
                    inputmode="numeric"
                    value={staffID()}
                    onInput={(event) => setStaffID(event.currentTarget.value)}
                  />
                </label>
                <button class="button secondary" type="submit">
                  Periksa akses
                </button>
              </form>
              <Show when={grantError()}>
                <p class="notice notice-error" role="alert">
                  {grantError()}
                </p>
              </Show>
              <Show when={grantNotice()}>
                <p class="notice notice-success" role="status">
                  {grantNotice()}
                </p>
              </Show>
              <Show when={lookupID()}>
                <div class="grant-review">
                  <h3>Staf #{lookupID()}</h3>
                  <A class="quiet-link" href={`/apps/admin/staff/${lookupID()}`}>
                    Lihat akun staf
                  </A>
                  <Show when={grantLookup.isPending}>
                    <p role="status">Memeriksa akses…</p>
                  </Show>
                  <Show when={grantLookup.isError && !lookupIsMissing()}>
                    <Failure error={grantLookup.error} retry={() => void refreshGrant()} />
                  </Show>
                  <Show when={grantLookup.isSuccess}>
                    <p>
                      Status akses:{' '}
                      <Status value={grantLookup.data!.data.is_active ? 'ACTIVE' : 'INACTIVE'} /> ·
                      revisi <span class="mono">{grantLookup.data!.data.revision}</span>
                    </p>
                  </Show>
                  <Show when={lookupIsMissing()}>
                    <p>
                      Belum ada baris akses untuk ID ini. Akses baru akan dibuat jika staf masih
                      aktif.
                    </p>
                  </Show>
                  <Show when={grantConflict()}>
                    <div class="notice notice-error" role="alert">
                      <p>
                        Baris akses berubah sejak diperiksa. Muat ulang untuk meninjau status
                        terbaru, lalu konfirmasikan lagi.
                      </p>
                      <button
                        class="button secondary"
                        type="button"
                        onClick={() => void refreshGrant()}
                      >
                        Muat ulang akses
                      </button>
                    </div>
                  </Show>
                  <div class="button-row">
                    <button
                      class="button primary"
                      disabled={
                        (!lookupIsMissing() && !grantLookup.isSuccess) ||
                        operation.blocked() ||
                        grantConflict() ||
                        grantLookup.isFetching
                      }
                      onClick={() => void enableGrant()}
                    >
                      Berikan atau pulihkan akses
                    </button>
                    <Show when={grantLookup.data?.data.is_active}>
                      <button
                        class="button secondary"
                        disabled={operation.blocked() || grantConflict() || grantLookup.isFetching}
                        onClick={() => void revokeGrant(grantLookup.data!)}
                      >
                        Cabut akses
                      </button>
                    </Show>
                  </div>
                </div>
              </Show>
              <Show when={writeTarget() === 'grant'}>
                <WriteFeedback operation={operation} />
              </Show>
              <h3>Semua pemberian akses</h3>
              <QueryState query={grants}>
                <Show
                  when={grants.data?.items.length}
                  fallback={<p class="muted">Belum ada pemberian akses.</p>}
                >
                  <div class="table-wrap">
                    <table class="directory-table">
                      <thead>
                        <tr>
                          <th>ID staf</th>
                          <th>Status akses</th>
                          <th>Revisi</th>
                          <th>
                            <span class="sr-only">Tindakan</span>
                          </th>
                        </tr>
                      </thead>
                      <tbody>
                        <For each={grants.data?.items}>
                          {(grant) => (
                            <tr>
                              <td class="mono">{grant.staff_id}</td>
                              <td>
                                <Status value={grant.is_active ? 'ACTIVE' : 'INACTIVE'} />
                              </td>
                              <td class="mono">{grant.revision}</td>
                              <td>
                                <button
                                  class="button secondary small"
                                  onClick={() => {
                                    setStaffID(grant.staff_id);
                                    setLookupID(grant.staff_id);
                                  }}
                                >
                                  Periksa
                                </button>
                              </td>
                            </tr>
                          )}
                        </For>
                      </tbody>
                    </table>
                  </div>
                </Show>
                <div class="pagination">
                  <span>
                    Halaman {grantPage()} · {grants.data?.total ?? 0} baris
                  </span>
                  <div>
                    <button
                      class="button secondary small"
                      disabled={grantPage() <= 1 || grants.isFetching}
                      onClick={() => setGrantPage(grantPage() - 1)}
                    >
                      Sebelumnya
                    </button>
                    <button
                      class="button secondary small"
                      disabled={
                        grantPage() * pageSize >= (grants.data?.total ?? 0) || grants.isFetching
                      }
                      onClick={() => setGrantPage(grantPage() + 1)}
                    >
                      Berikutnya
                    </button>
                  </div>
                </div>
              </QueryState>
            </section>
          </div>
        </>
      </QueryState>
    </>
  );
}
