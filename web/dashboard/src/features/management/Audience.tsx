import { For, Show } from 'solid-js';
import { A, useParams } from '@solidjs/router';
import { createQuery } from '@tanstack/solid-query';
import { data } from '../../lib/api/client';
import type { components } from '../../lib/api/generated/staff';
import { createPage, Pager, QueryState, date } from './shared';
import { Status } from '../../components/ui/Feedback';
type User = components['schemas']['User'];
type Device = components['schemas']['Device'];
export default function Audience() {
  const params = useParams();
  const page = createPage<User>(() => `/apps/${params.appId}/users`);
  return (
    <>
      <header class="page-heading">
        <div>
          <span class="eyebrow">AUDIENCE</span>
          <h1>Pengguna &amp; perangkat</h1>
          <p class="muted">Periksa pengguna dan perangkat di aplikasi yang dipilih.</p>
        </div>
        <A class="button secondary" href={`/apps/${params.appId}/segments`}>
          Kelola segment
        </A>
      </header>
      <QueryState query={page.query}>
        <Show
          when={page.query.data?.items.length}
          fallback={
            <div class="empty-state">
              <h2>Belum ada pengguna.</h2>
              <p>Pengguna muncul setelah tersinkronisasi melalui integrasi aplikasi.</p>
            </div>
          }
        >
          <div class="table-wrap">
            <table class="directory-table">
              <thead>
                <tr>
                  <th>Pengguna</th>
                  <th>ID eksternal</th>
                  <th>Status</th>
                </tr>
              </thead>
              <tbody>
                <For each={page.query.data?.items}>
                  {(item) => (
                    <tr>
                      <td>
                        <A href={`/apps/${params.appId}/audience/${item.id}`}>
                          {item.name || `Pengguna #${item.id}`}
                        </A>
                        <span class="row-meta">#{item.id}</span>
                      </td>
                      <td>{item.external_id}</td>
                      <td>
                        <Status value={item.status} />
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
export function UserDetail() {
  const params = useParams();
  const base = () => `/apps/${params.appId}/users/${params.userId}`;
  const user = createQuery(() => ({
    queryKey: ['user', params.appId, params.userId],
    queryFn: ({ signal }) => data<User>(base(), signal),
  }));
  const devices = createPage<Device>(() => `${base()}/devices`);
  return (
    <>
      <A class="back-link" href={`/apps/${params.appId}/audience`}>
        ← Pengguna
      </A>
      <QueryState query={user}>
        <header class="page-heading">
          <div>
            <span class="eyebrow">PENGGUNA #{params.userId}</span>
            <h1>{user.data?.name || user.data?.external_id}</h1>
            <p class="muted">{user.data?.email || 'Email tidak tersedia'}</p>
            <p>
              <Show when={user.data?.status}>{(status) => <Status value={status()} />}</Show>
            </p>
          </div>
        </header>
        <section class="surface stack">
          <h2>Perangkat terdaftar</h2>
          <p class="help-text">
            Status aktif adalah status registrasi. Ini bukan bukti perangkat sedang online atau
            pasti menerima pesan.
          </p>
          <QueryState query={devices.query}>
            <Show
              when={devices.query.data?.items.length}
              fallback={<p>Belum ada perangkat terdaftar.</p>}
            >
              <div class="table-wrap">
                <table class="device-table">
                  <thead>
                    <tr>
                      <th>Perangkat</th>
                      <th>Platform</th>
                      <th>Registrasi</th>
                      <th>Terakhir terlihat</th>
                    </tr>
                  </thead>
                  <tbody>
                    <For each={devices.query.data?.items}>
                      {(item) => (
                        <tr>
                          <td>#{item.id}</td>
                          <td>{item.platform}</td>
                          <td>
                            <Status value={item.is_active ? 'ACTIVE' : 'INACTIVE'} />
                          </td>
                          <td>{date(item.last_seen_at)}</td>
                        </tr>
                      )}
                    </For>
                  </tbody>
                </table>
              </div>
            </Show>
            <Pager page={devices} />
          </QueryState>
        </section>
      </QueryState>
    </>
  );
}
