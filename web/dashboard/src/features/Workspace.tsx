import { For, Show, createSignal, createEffect, onCleanup, type ParentProps } from 'solid-js';
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
  LayoutDashboard,
  Users,
  Layers,
  ListChecks,
  Settings,
  PanelLeftClose,
  PanelLeftOpen,
} from 'lucide-solid';
import { preferences, setSidebarHidden } from '../app/preferences';
import { useAuth } from '../app/auth';
import { data, errorMessage } from '../lib/api/client';
import type { Application } from '../lib/api/types';
import { Failure, Loading, Status } from '../components/ui/Feedback';

export function Workspace(props: ParentProps) {
  const auth = useAuth();
  const params = useParams();
  const [menu, setMenu] = createSignal(false);
  let navigation: HTMLElement | undefined;
  let menuButton: HTMLButtonElement | undefined;
  createEffect(() => {
    if (menu()) navigation?.querySelector<HTMLButtonElement>('button')?.focus();
  });
  const closeMenu = () => {
    setMenu(false);
    menuButton?.focus();
  };
  const escape = (event: KeyboardEvent) => {
    if (event.key === 'Escape' && menu()) closeMenu();
  };
  window.addEventListener('keydown', escape);
  onCleanup(() => window.removeEventListener('keydown', escape));
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
        <div class={`workspace ${preferences.sidebarHidden ? 'sidebar-hidden' : ''}`}>
          <Show when={menu()}>
            <button class="nav-scrim" aria-label="Tutup navigasi" onClick={closeMenu} />
          </Show>
          <aside
            ref={navigation}
            onKeyDown={(event) => {
              if (!menu() || event.key !== 'Tab') return;
              const items = Array.from(
                navigation?.querySelectorAll<HTMLElement>('a[href], button:not([disabled])') ?? [],
              ).filter((node) => node.getClientRects().length);
              const first = items[0],
                last = items.at(-1);
              if (event.shiftKey && document.activeElement === first) {
                event.preventDefault();
                last?.focus();
              } else if (!event.shiftKey && document.activeElement === last) {
                event.preventDefault();
                first?.focus();
              }
            }}
            class={`sidebar ${menu() ? 'is-open' : ''}`}
          >
            <div class="sidebar-brand">
              <A href="/apps" class="wordmark">
                hello<span>.</span>
              </A>
              <button
                class="icon-button mobile-only"
                aria-label="Tutup navigasi"
                onClick={closeMenu}
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
              <Show when={params.appId}>
                <A
                  href={`/apps/${params.appId}/overview`}
                  activeClass="nav-active"
                  onClick={() => setMenu(false)}
                >
                  <LayoutDashboard size={18} />
                  Ringkasan
                </A>
              </Show>
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
              <Show when={params.appId}>
                <A
                  href={`/apps/${params.appId}/templates`}
                  activeClass="nav-active"
                  onClick={() => setMenu(false)}
                >
                  <FileText size={18} />
                  Template
                </A>
                <A
                  href={`/apps/${params.appId}/logs`}
                  activeClass="nav-active"
                  onClick={() => setMenu(false)}
                >
                  <ListChecks size={18} />
                  Log pengiriman
                </A>
                <span class="sidebar-caption nav-group">AUDIENCE</span>
                <A
                  href={`/apps/${params.appId}/audience`}
                  activeClass="nav-active"
                  onClick={() => setMenu(false)}
                >
                  <Users size={18} />
                  Pengguna &amp; perangkat
                </A>
                <A
                  href={`/apps/${params.appId}/segments`}
                  activeClass="nav-active"
                  onClick={() => setMenu(false)}
                >
                  <Layers size={18} />
                  Segment
                </A>
                <A
                  href={`/apps/${params.appId}/settings`}
                  activeClass="nav-active"
                  onClick={() => setMenu(false)}
                >
                  <Settings size={18} />
                  Pengaturan
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
          <div class="workspace-body" inert={menu()}>
            <header class="topbar">
              <button
                ref={menuButton}
                class="icon-button mobile-only"
                aria-label="Buka navigasi"
                aria-expanded={menu()}
                onClick={() => setMenu(true)}
              >
                <Menu size={21} />
              </button>
              <button
                class="icon-button desktop-only"
                aria-label={preferences.sidebarHidden ? 'Tampilkan sidebar' : 'Sembunyikan sidebar'}
                aria-expanded={!preferences.sidebarHidden}
                onClick={() => setSidebarHidden(!preferences.sidebarHidden)}
              >
                <Show when={preferences.sidebarHidden} fallback={<PanelLeftClose size={20} />}>
                  <PanelLeftOpen size={20} />
                </Show>
              </button>
              <span class="environment-badge">
                {['localhost', '127.0.0.1', '[::1]'].includes(window.location.hostname)
                  ? 'LOKAL'
                  : window.location.host}
              </span>
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
