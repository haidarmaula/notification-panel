import { createSignal, Show } from 'solid-js';
import { Navigate, useNavigate } from '@solidjs/router';
import { ArrowRight, BellRing, Check, Eye, EyeOff } from 'lucide-solid';
import { useAuth } from '../app/auth';
import { ApiError } from '../lib/api/client';
import { Failure, Loading } from '../components/ui/Feedback';
export default function Login() {
  const auth = useAuth();
  const navigate = useNavigate();
  const [pending, setPending] = createSignal(false);
  const [error, setError] = createSignal('');
  const [visible, setVisible] = createSignal(false);
  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (pending()) return;
    const form = event.currentTarget as HTMLFormElement;
    const fields = new FormData(form);
    setPending(true);
    setError('');
    try {
      await auth.login(String(fields.get('email')), String(fields.get('password')));
      form.reset();
      navigate('/apps', { replace: true });
    } catch (error) {
      setError(
        error instanceof ApiError && error.status === 401
          ? 'Email atau kata sandi tidak sesuai.'
          : 'Belum bisa masuk. Periksa koneksi lalu coba kembali.',
      );
    } finally {
      setPending(false);
    }
  }
  return (
    <Show when={!auth.session.isPending} fallback={<Loading />}>
      <Show when={!auth.authenticated()} fallback={<Navigate href="/apps" />}>
        <div class="login-page">
          <aside class="login-story">
            <a class="wordmark" href="/">
              hello<span>.</span>
            </a>
            <div class="login-story-content">
              <div class="story-label">
                <BellRing size={18} /> NOTIFIKASI, DENGAN ARAH.
              </div>
              <h1>
                Pesan yang tepat.
                <br />
                Sampai dengan jelas.
              </h1>
              <p>
                Siapkan campaign, tentukan penerimanya, dan ikuti perjalanan setiap pesan dalam satu
                tempat.
              </p>
              <div class="message-sample" aria-hidden="true">
                <div class="sample-mark">h.</div>
                <div>
                  <strong>Setiap pesan punya tujuan.</strong>
                  <span>Dari draft pertama sampai respons penerima.</span>
                </div>
                <Check size={18} />
              </div>
            </div>
            <span class="login-footnote">Hello · Ruang kerja notifikasi</span>
          </aside>
          <main class="login-form-area">
            <div class="login-form-wrap">
              <span class="eyebrow">DASHBOARD HELLO</span>
              <h2>Selamat datang kembali.</h2>
              <p class="muted">Masuk dengan akun staf Anda.</p>
              <Show when={auth.session.isError}>
                <Failure error={auth.session.error} retry={() => void auth.session.refetch()} />
              </Show>
              <form onSubmit={(event) => void submit(event)} class="stack">
                <label>
                  Email
                  <input
                    name="email"
                    type="email"
                    autocomplete="username"
                    placeholder="nama@perusahaan.com"
                    required
                    disabled={pending()}
                  />
                </label>
                <label>
                  Kata sandi
                  <div class="password-field">
                    <input
                      name="password"
                      type={visible() ? 'text' : 'password'}
                      autocomplete="current-password"
                      required
                      disabled={pending()}
                    />
                    <button
                      type="button"
                      class="icon-button"
                      aria-label={visible() ? 'Sembunyikan kata sandi' : 'Tampilkan kata sandi'}
                      onClick={() => setVisible(!visible())}
                    >
                      {visible() ? <EyeOff size={18} /> : <Eye size={18} />}
                    </button>
                  </div>
                </label>
                <Show when={error()}>
                  <p class="field-error" role="alert">
                    {error()}
                  </p>
                </Show>
                <button class="button primary login-submit" disabled={pending() || !auth.csrf()}>
                  {pending() ? 'Memeriksa akun…' : 'Masuk ke dashboard'}
                  <ArrowRight size={17} />
                </button>
              </form>
              <p class="login-assistance">Belum punya akses? Hubungi administrator Anda.</p>
            </div>
          </main>
        </div>
      </Show>
    </Show>
  );
}
