import { createSignal, onCleanup, Show } from 'solid-js';
import { useQueryClient } from '@tanstack/solid-query';
import { discardUnsavedChanges, useUnsaved } from '../../app/unsaved';
import { useWrite, WriteFeedback } from './shared';

export default function PasswordForm() {
  const operation = useWrite();
  const client = useQueryClient();
  const setDirty = useUnsaved();
  const [current, setCurrent] = createSignal('');
  const [password, setPassword] = createSignal('');
  const [confirmation, setConfirmation] = createSignal('');
  const [error, setError] = createSignal('');
  let replacement: HTMLInputElement | undefined;
  onCleanup(() => {
    setCurrent('');
    setPassword('');
    setConfirmation('');
  });
  async function save(event: SubmitEvent) {
    event.preventDefault();
    setError('');
    if (new TextEncoder().encode(password()).length > 72 || password() !== confirmation()) {
      setError(
        password() !== confirmation()
          ? 'Konfirmasi kata sandi belum sama.'
          : 'Kata sandi baru maksimal 72 byte.',
      );
      replacement?.focus();
      return;
    }
    if (
      !window.confirm(
        'Ganti kata sandi dan keluar dari seluruh sesi akun? Perubahan lain yang belum disimpan akan hilang.',
      )
    )
      return;
    const result = await operation.write('/profile/password', 'PATCH', {
      current_password: current(),
      new_password: password(),
    });
    if (result) {
      setCurrent('');
      setPassword('');
      setConfirmation('');
      discardUnsavedChanges();
      client.clear();
      window.location.replace('/login');
    }
  }
  return (
    <section class="surface stack">
      <h2>Ganti kata sandi</h2>
      <p class="muted">
        Setelah berhasil, seluruh sesi akun dibatalkan dan Anda perlu masuk kembali.
      </p>
      <form class="stack" onInput={() => setDirty(true)} onSubmit={(event) => void save(event)}>
        <fieldset class="stack" disabled={operation.blocked()}>
          <label>
            Kata sandi saat ini
            <input
              type="password"
              required
              autocomplete="current-password"
              value={current()}
              onInput={(event) => setCurrent(event.currentTarget.value)}
            />
          </label>
          <label>
            Kata sandi baru
            <input
              ref={replacement}
              type="password"
              required
              autocomplete="new-password"
              value={password()}
              onInput={(event) => setPassword(event.currentTarget.value)}
              aria-invalid={!!error()}
              aria-describedby={error() ? 'password-error' : undefined}
            />
          </label>
          <label>
            Konfirmasi kata sandi baru
            <input
              type="password"
              required
              autocomplete="new-password"
              value={confirmation()}
              onInput={(event) => setConfirmation(event.currentTarget.value)}
            />
          </label>
          <Show when={error()}>
            <p id="password-error" class="notice notice-error" role="alert">
              {error()}
            </p>
          </Show>
          <button class="button primary" type="submit">
            {operation.pending() ? 'Mengganti…' : 'Ganti kata sandi'}
          </button>
        </fieldset>
        <WriteFeedback operation={operation} />
      </form>
    </section>
  );
}
