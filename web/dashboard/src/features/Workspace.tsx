import { For, Show, createSignal, type ParentProps } from 'solid-js';
import { A, Navigate, useParams } from '@solidjs/router';
import { createQuery } from '@tanstack/solid-query';
import {
  ArrowLeft,
  ArrowRight,
  Boxes,
  ChevronDown,
  FileText,
  LogOut,
  Megaphone,
  Menu,
  X,
} from 'lucide-solid';
import { useAuth } from '../app/auth';
import { data, errorMessage } from '../lib/api/client';
import type { Application } from '../lib/api/types';
import { Failure, Loading, Status } from '../components/ui/Feedback';

export function Workspace(props: ParentProps) {
  const auth = useAuth();
  const params = useParams();
  const [menu, setMenu] = createSignal(false);
  const [logoutError, setLogoutError] = createSignal('');
  const [leaving, setLeaving] = createSignal(false);
  const profile = createQuery(() => ({
    queryKey: ['profile'],
    queryFn: ({ signal }) => data<{ name: string; email: string }>('/profile', signal),
    enabled: !!auth.authenticated(),
  }));
  const application = createQuery(() => ({
    queryKey: ['application', params.appId],
    queryFn: ({ signal }) => data<Application>(`/apps/${params.appId}`, signal),
    enabled: !!auth.authenticated() && !!params.appId,
  }));
  async function logout() {
    if (leaving()) return;
    setLeaving(true);
    try {
      await auth.logout();
    } catch (error) {
      setLogoutError(errorMessage(error));
      setLeaving(false);
    }
  }
  return (
    <Show when={!auth.session.isPending} fallback={<Loading />}>
      <Show
        when={auth.authenticated()}
        fallback={
          <Show when={auth.session.isError} fallback={<Navigate href="/login" />}>
            <Failure error={auth.session.error} retry={() => void auth.session.refetch()} />
          </Show>
        }
      >
        <a href="#workspace-main" class="skip-link">
          Lewati ke konten
        </a>
        <div class="workspace">
          <Show when={menu()}>
            <button class="nav-scrim" aria-label="Tutup navigasi" onClick={() => setMenu(false)} />
          </Show>
          <aside class={`sidebar ${menu() ? 'is-open' : ''}`}>
            <div class="sidebar-brand">
              <A href="/apps" class="wordmark">
                hello<span>.</span>
              </A>
              <button
                class="icon-button mobile-only"
                aria-label="Tutup navigasi"
                onClick={() => setMenu(false)}
              >
                <X size={20} />
              </button>
            </div>
            <span class="sidebar-caption">RUANG KERJA</span>
            <A href="/apps" class="workspace-switch" onClick={() => setMenu(false)}>
              <span class="app-monogram">{(application.data?.name ?? 'H').slice(0, 1)}</span>
              <span>
                {application.data?.name ?? 'Pilih aplikasi'}
                <small>{application.data?.key ?? 'Aplikasi Anda'}</small>
              </span>
              <ChevronDown size={15} />
            </A>
            <nav aria-label="Navigasi utama">
              <A href="/apps" end activeClass="nav-active" onClick={() => setMenu(false)}>
                <Boxes size={18} />
                Aplikasi
              </A>
              <Show when={params.appId}>
                <A
                  href={`/apps/${params.appId}/campaigns`}
                  activeClass="nav-active"
                  onClick={() => setMenu(false)}
                >
                  <Megaphone size={18} />
                  Campaign
                </A>
              </Show>
              <a href="/docs/" target="_blank" rel="noreferrer">
                <FileText size={18} />
                Referensi API
                <ArrowRight size={14} class="nav-external" />
              </a>
            </nav>
            <div class="sidebar-bottom">
              <div class="profile">
                <span class="avatar">{(profile.data?.name ?? 'S').slice(0, 1)}</span>
                <div>
                  <strong>{profile.data?.name ?? 'Akun staf'}</strong>
                  <small>{profile.data?.email ?? 'Terautentikasi'}</small>
                </div>
              </div>
              <button class="logout-button" onClick={() => void logout()} disabled={leaving()}>
                <LogOut size={17} />
                {leaving() ? 'Keluar…' : 'Keluar'}
              </button>
              <Show when={logoutError()}>
                <p class="sidebar-error" role="alert">
                  {logoutError()}
                </p>
              </Show>
            </div>
          </aside>
          <div class="workspace-body">
            <header class="topbar">
              <button
                class="icon-button mobile-only"
                aria-label="Buka navigasi"
                aria-expanded={menu()}
                onClick={() => setMenu(true)}
              >
                <Menu size={21} />
              </button>
              <span>
                Ruang kerja <span class="breadcrumb-separator">/</span>{' '}
                {application.data?.name ?? 'Aplikasi'}
              </span>
              <span class="context-label">Dashboard notifikasi</span>
            </header>
            <main id="workspace-main" class="main-content">
              <Show when={!params.appId || !application.isPending} fallback={<Loading />}>
                <Show
                  when={!application.isError}
                  fallback={
                    <Failure error={application.error} retry={() => void application.refetch()} />
                  }
                >
                  <Show
                    when={!params.appId || application.data?.status === 'ACTIVE'}
                    fallback={
                      <div class="empty-state">
                        <h1>Aplikasi belum tersedia</h1>
                        <p>Campaign dapat dibuka setelah aplikasi berstatus aktif.</p>
                        <Show when={application.data}>
                          <Status value={application.data!.status} />
                        </Show>
                        <A class="button secondary" href="/apps">
                          Kembali ke aplikasi
                        </A>
                      </div>
                    }
                  >
                    {props.children}
                  </Show>
                </Show>
              </Show>
            </main>
          </div>
        </div>
      </Show>
    </Show>
  );
}
export default function Applications() {
  const [page, setPage] = createSignal(1);
  const applications = createQuery(() => ({
    queryKey: ['applications', page()],
    queryFn: ({ signal }) =>
      data<{ items: Application[]; total: number }>(`/apps?page=${page()}&limit=20`, signal),
  }));
  return (
    <>
      <header class="page-heading">
        <div>
          <span class="eyebrow">RUANG KERJA ANDA</span>
          <h1>Pilih aplikasi</h1>
          <p class="muted">Setiap aplikasi memiliki campaign dan penerimanya sendiri.</p>
        </div>
      </header>
      <Show when={!applications.isPending} fallback={<Loading />}>
        <Show
          when={!applications.isError}
          fallback={
            <Failure error={applications.error} retry={() => void applications.refetch()} />
          }
        >
          <Show
            when={applications.data?.items.length}
            fallback={
              <div class="empty-state">
                <Boxes size={30} />
                <h2>Belum ada aplikasi</h2>
                <p>Minta administrator memberikan akses aplikasi ke akun Anda.</p>
              </div>
            }
          >
            <div class="application-list">
              <For each={applications.data?.items}>
                {(app) => (
                  <A href={`/apps/${app.id}/campaigns`} class="application-row">
                    <span class="application-icon">{app.name.slice(0, 1)}</span>
                    <div>
                      <h2>{app.name}</h2>
                      <p>{app.key}</p>
                    </div>
                    <Status value={app.status} />
                    <ArrowRight size={19} />
                  </A>
                )}
              </For>
            </div>
          </Show>
          <div class="pagination">
            <span>Halaman {page()}</span>
            <div>
              <button
                class="button secondary small"
                disabled={page() === 1}
                onClick={() => setPage(page() - 1)}
              >
                <ArrowLeft size={15} />
                Sebelumnya
              </button>
              <button
                class="button secondary small"
                disabled={page() * 20 >= (applications.data?.total ?? 0)}
                onClick={() => setPage(page() + 1)}
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
