import { Show, createEffect, createSignal } from 'solid-js';
import { useParams } from '@solidjs/router';
import { createQuery, useQueryClient } from '@tanstack/solid-query';
import { data } from '../../lib/api/client';
import type { Application } from '../../lib/api/types';
import { preferences, setTheme } from '../../app/preferences';
import { QueryState, useWrite, WriteFeedback, useUnsaved } from './shared';
export default function Settings() {
  const params = useParams();
  const client = useQueryClient();
  const operation = useWrite();
  const setDirty = useUnsaved();
  const [name, setName] = createSignal('');
  const [email, setEmail] = createSignal('');
  const [saved, setSaved] = createSignal(false);
  const profile = createQuery(() => ({
    queryKey: ['profile'],
    queryFn: ({ signal }) => data<{ name: string; email: string }>('/profile', signal),
  }));
  const app = createQuery(() => ({
    queryKey: ['application', params.appId],
    queryFn: ({ signal }) => data<Application>(`/apps/${params.appId}`, signal),
  }));
  let loaded = false;
  createEffect(() => {
    if (profile.data && !loaded) {
      loaded = true;
      setName(profile.data.name);
      setEmail(profile.data.email);
    }
  });
  async function save(event: SubmitEvent) {
    event.preventDefault();
    setSaved(false);
    const result = await operation.write('/profile', 'PATCH', {
      name: name().trim(),
      email: email().trim(),
    });
    if (result) {
      setDirty(false);
      await client.invalidateQueries({ queryKey: ['profile'] });
      setSaved(true);
    }
  }
  return (
    <>
      <header class="page-heading">
        <div>
          <h1>Pengaturan</h1>
          <p class="muted">Preferensi tampilan, profil Anda, dan identitas aplikasi.</p>
        </div>
      </header>
      <div class="settings-grid">
        <section class="surface stack">
          <h2>Tampilan</h2>
          <label>
            Tema
            <select
              aria-label="Tema"
              value={preferences.theme}
              onChange={(event) =>
                setTheme(event.currentTarget.value as 'light' | 'dark' | 'system')
              }
            >
              <option value="system">Ikuti sistem</option>
              <option value="light">Terang</option>
              <option value="dark">Gelap</option>
            </select>
          </label>
          <p class="help-text">
            Preferensi tema dan sidebar disimpan di browser ini. Data akun dan campaign tidak
            disimpan di sana.
          </p>
        </section>
        <section class="surface stack">
          <h2>Aplikasi &amp; lingkungan</h2>
          <QueryState query={app}>
            <dl class="settings-details">
              <dt>Nama</dt>
              <dd>{app.data?.name}</dd>
              <dt>Kunci aplikasi</dt>
              <dd>{app.data?.key}</dd>
              <dt>ID aplikasi</dt>
              <dd class="mono">{app.data?.id}</dd>
              <dt>Status</dt>
              <dd>{app.data?.status}</dd>
              <dt>Alamat dashboard</dt>
              <dd class="mono">{window.location.origin}</dd>
            </dl>
          </QueryState>
          <p class="help-text">
            Konfigurasi provider dan kebijakan pengiriman dikelola melalui prosedur operasional yang
            terlindungi.
          </p>
        </section>
        <section class="surface stack">
          <h2>Profil saya</h2>
          <QueryState query={profile}>
            <form
              onInput={() => setDirty(true)}
              class="stack"
              onSubmit={(event) => void save(event)}
            >
              <fieldset class="stack" disabled={operation.blocked()}>
                <label>
                  Nama
                  <input
                    required
                    maxLength={255}
                    value={name()}
                    onInput={(event) => {
                      setName(event.currentTarget.value);
                      setSaved(false);
                    }}
                  />
                </label>
                <label>
                  Email
                  <input
                    required
                    type="email"
                    value={email()}
                    onInput={(event) => {
                      setEmail(event.currentTarget.value);
                      setSaved(false);
                    }}
                  />
                </label>
                <WriteFeedback operation={operation} />
                <Show when={saved()}>
                  <p role="status" class="notice notice-success">
                    Profil berhasil diperbarui.
                  </p>
                </Show>
                <button class="button primary" type="submit">
                  {operation.pending() ? 'Menyimpan…' : 'Simpan profil'}
                </button>
              </fieldset>
            </form>
          </QueryState>
        </section>
      </div>
    </>
  );
}
