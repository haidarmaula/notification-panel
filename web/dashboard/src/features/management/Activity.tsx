import { For, Show } from 'solid-js';
import { A, useParams } from '@solidjs/router';
import type { components } from '../../lib/api/generated/staff';
import { createPage, Pager, QueryState, date } from './shared';

type Upload = components['schemas']['Upload'];
type UploadRow = components['schemas']['UploadRow'];
type Audit = components['schemas']['Audit'];

export default function Uploads() {
  const params = useParams();
  const page = createPage<Upload>(() => `/apps/${params.appId}/uploads`);
  return (
    <>
      <header class="page-heading">
        <div>
          <h1>Riwayat upload</h1>
          <p class="muted">Periksa batch yang tercatat melalui integrasi aplikasi.</p>
        </div>
      </header>
      <QueryState query={page.query}>
        <Show
          when={page.query.data?.items.length}
          fallback={
            <div class="empty-state">
              <h2>Belum ada riwayat upload.</h2>
              <p>Batch akan muncul setelah tercatat melalui integrasi backend.</p>
            </div>
          }
        >
          <div class="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Batch</th>
                  <th>Total baris</th>
                  <th>Valid</th>
                  <th>Tidak valid</th>
                  <th>Dicatat</th>
                </tr>
              </thead>
              <tbody>
                <For each={page.query.data?.items}>
                  {(item) => (
                    <tr>
                      <td>
                        <A class="campaign-title" href={`/apps/${params.appId}/uploads/${item.id}`}>
                          Batch #{item.id}
                        </A>
                      </td>
                      <td>{item.total_rows}</td>
                      <td>{item.valid_rows}</td>
                      <td>{item.invalid_rows}</td>
                      <td>{date(item.created_at)}</td>
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

export function UploadRows() {
  const params = useParams();
  const page = createPage<UploadRow>(() => `/apps/${params.appId}/uploads/${params.batchId}/rows`);
  return (
    <>
      <A class="back-link" href={`/apps/${params.appId}/uploads`}>
        Kembali ke riwayat upload
      </A>
      <header class="page-heading">
        <div>
          <h1>Batch #{params.batchId}</h1>
          <p class="muted">Status validasi baris yang dicatat backend.</p>
        </div>
      </header>
      <QueryState query={page.query}>
        <Show
          when={page.query.data?.items.length}
          fallback={
            <div class="empty-state">
              <h2>Belum ada baris tercatat.</h2>
            </div>
          }
        >
          <div class="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>ID baris</th>
                  <th>Validasi</th>
                  <th>Dicatat</th>
                </tr>
              </thead>
              <tbody>
                <For each={page.query.data?.items}>
                  {(item) => (
                    <tr>
                      <td>
                        <span class="row-meta">#{item.id}</span>
                      </td>
                      <td>
                        <span class={`status ${item.is_valid ? 'status-active' : 'status-failed'}`}>
                          {item.is_valid ? 'Valid' : 'Tidak valid'}
                        </span>
                      </td>
                      <td>{date(item.created_at)}</td>
                    </tr>
                  )}
                </For>
              </tbody>
            </table>
          </div>
        </Show>
        <Pager page={page} />
      </QueryState>
      <p class="page-footnote">API saat ini tidak menyediakan isi baris atau alasan penolakan.</p>
    </>
  );
}

export function AuditLog() {
  const params = useParams();
  const page = createPage<Audit>(() => `/apps/${params.appId}/audit-logs`);
  return (
    <>
      <header class="page-heading">
        <div>
          <h1>Audit aktivitas</h1>
          <p class="muted">
            Jejak tindakan pada aplikasi ini, diurutkan berdasarkan ID pencatatan.
          </p>
        </div>
      </header>
      <QueryState query={page.query}>
        <Show
          when={page.query.data?.items.length}
          fallback={
            <div class="empty-state">
              <h2>Belum ada aktivitas tercatat.</h2>
            </div>
          }
        >
          <div class="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Aktivitas</th>
                  <th>Pelaku</th>
                  <th>Objek</th>
                  <th>Dicatat</th>
                </tr>
              </thead>
              <tbody>
                <For each={page.query.data?.items}>
                  {(item) => (
                    <tr>
                      <td>
                        <strong>{item.action}</strong>
                        <span class="row-meta">#{item.id}</span>
                      </td>
                      <td>Staff #{item.actor_id}</td>
                      <td>
                        {item.entity_type}
                        <Show when={item.entity_id}>
                          <span class="row-meta">#{item.entity_id}</span>
                        </Show>
                      </td>
                      <td>{date(item.created_at)}</td>
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
