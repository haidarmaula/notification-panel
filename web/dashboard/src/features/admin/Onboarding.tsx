import { A } from '@solidjs/router';
import { For, Show, createEffect, createSignal, onCleanup, onMount } from 'solid-js';
import { useAuth } from '../../app/auth';
import { useUnsaved } from '../../app/unsaved';
import { ApiError, request } from '../../lib/api/client';
import type { components } from '../../lib/api/generated/onboarding';
import type { components as RegistryComponents } from '../../lib/api/generated/applications';

type Readiness = components['schemas']['Readiness'];
type Profile = components['schemas']['Profile'];
type Approval = components['schemas']['Approval'];
type Operation = components['schemas']['Operation'];
type ServiceAccount = components['schemas']['ServiceAccount'];
type EnrollmentInput = components['schemas']['EnrollmentInput'];
type FcmInput = components['schemas']['FcmInput'];
type VerifyInput = components['schemas']['VerifyInput'];
type HoldInput = components['schemas']['HoldInput'];
type Platform = EnrollmentInput['platforms'][number]['platform'];
type Environment = FcmInput['environment'];
type Application = RegistryComponents['schemas']['Application'];

type PendingMutation = {
  kind: 'enrollment' | 'credential' | 'verify' | 'hold';
  path: string;
  method: 'PUT' | 'POST';
  body: EnrollmentInput | FcmInput | VerifyInput | HoldInput;
  etag: string;
  key: string;
};

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
function validText(value: string, maxBytes: number) {
  return (
    value.length > 0 &&
    new TextEncoder().encode(value).length <= maxBytes &&
    !Array.from(value).some((character) => {
      const code = character.charCodeAt(0);
      return code < 32 || (code >= 127 && code <= 159);
    })
  );
}
const allowedAccountKeys = new Set([
  'type',
  'project_id',
  'private_key_id',
  'private_key',
  'client_email',
  'token_uri',
  'auth_uri',
  'client_id',
  'auth_provider_x509_cert_url',
  'client_x509_cert_url',
  'universe_domain',
]);

function safeError(error: unknown): string {
  const status = error instanceof ApiError ? error.status : 0;
  if (status === 400) return 'Data belum diterima. Periksa pilihan dan format yang diisi.';
  if (status === 401) return 'Sesi berakhir. Masuk kembali untuk melanjutkan.';
  if (status === 403) return 'Akses untuk menyiapkan aplikasi ini tidak tersedia.';
  if (status === 404) return 'Fitur persiapan belum tersedia untuk aplikasi ini.';
  if (status === 412)
    return 'Data setup telah berubah. Muat ulang dan tinjau versi terbaru sebelum mengirim lagi.';
  if (status === 428) return 'Versi data tidak tersedia. Muat ulang sebelum mencoba kembali.';
  if (status === 429) return 'Terlalu banyak permintaan. Tunggu sebentar sebelum mencoba lagi.';
  if (status === 503)
    return 'Layanan persiapan belum tersedia. Periksa status sebelum mencoba lagi.';
  if (status === 0)
    return 'Hasil permintaan belum diketahui. Gunakan Coba lagi untuk mengulang permintaan yang sama.';
  if (status >= 500) return 'Layanan sedang bermasalah. Periksa status terbaru sebelum mengulang.';
  return 'Permintaan belum dapat diproses.';
}

function formatDate(value: string) {
  const date = new Date(value);
  return Number.isFinite(date.getTime())
    ? new Intl.DateTimeFormat('id-ID', { dateStyle: 'medium', timeStyle: 'short' }).format(date)
    : 'Waktu tidak tersedia';
}

function blockerLabel(code: string) {
  const labels: Record<string, string> = {
    FCM_NOT_CONFIGURED: 'Kredensial Firebase belum disimpan.',
    FCM_NOT_VERIFIED: 'Kredensial Firebase belum diperiksa.',
    VERIFIER_NOT_CONFIGURED: 'Pemeriksa OAuth belum tersedia. Status akan diperiksa kembali.',
    FCM_CREDENTIAL_REJECTED: 'Kredensial service account ditolak.',
    FCM_CHECK_INCONCLUSIVE: 'Pemeriksaan OAuth belum dapat dipastikan.',
    FCM_CONFIGURATION_UNAVAILABLE: 'Konfigurasi service account tidak tersedia.',
    ENROLLMENT_NOT_CONFIGURED: 'Profil pendaftaran aplikasi belum dipilih.',
    RUNTIME_PENDING: 'Pendaftaran perangkat belum diaktifkan.',
    LEGACY_ADOPTION_REQUIRED:
      'Pengelola platform perlu meninjau konfigurasi aplikasi yang sudah ada.',
    PLATFORM_HOLD: 'Persiapan platform masih ditahan.',
    TEST_DEVICE_REQUIRED: 'Perangkat uji belum disiapkan.',
    ROUTE_REVIEW_REQUIRED: 'Rute perangkat belum ditinjau.',
    DISPATCH_APPROVAL_REQUIRED: 'Persetujuan distribusi belum tersedia.',
  };
  return labels[code] ?? 'Masih ada persiapan yang perlu diselesaikan.';
}

function checkLabel(code: string) {
  const labels: Record<string, string> = {
    LOCAL_FORMAT: 'Format kredensial',
    OAUTH: 'Koneksi OAuth',
    TARGET_PERMISSION: 'Izin pengiriman',
    DEVICE_DELIVERY: 'Penerimaan perangkat',
  };
  return labels[code] ?? 'Pemeriksaan';
}

function outcomeLabel(value: string) {
  if (value === 'PASSED') return 'Lulus';
  if (value === 'FAILED') return 'Gagal';
  if (value === 'INCONCLUSIVE') return 'Belum dipastikan';
  return 'Belum diperiksa';
}

function operationMessage(operation: Operation) {
  if (operation.state === 'SUCCEEDED')
    return 'OAuth berhasil. Izin mengirim notifikasi dan penerimaan di perangkat belum diperiksa.';
  if (operation.failure_code === 'FCM_CREDENTIAL_REJECTED')
    return 'Service account ditolak oleh penyedia. Periksa kredensial sebelum mencoba lagi.';
  if (operation.failure_code === 'FCM_CHECK_INCONCLUSIVE')
    return 'Pemeriksaan OAuth belum dapat dipastikan. Anda dapat mengirim pemeriksaan baru.';
  if (operation.failure_code === 'FCM_CONFIGURATION_UNAVAILABLE')
    return 'Konfigurasi service account tidak tersedia untuk diperiksa.';
  if (operation.state === 'SUPERSEDED')
    return 'Pemeriksaan sebelumnya sudah tidak berlaku. Muat ulang status setup.';
  if (operation.state === 'FAILED')
    return 'Pemeriksaan OAuth gagal. Periksa konfigurasi sebelum mencoba lagi.';
  return 'Pemeriksaan OAuth sedang berjalan.';
}

function strongETag(value: string | null): value is string {
  return !!value && /^"[^"\r\n]+"$/.test(value);
}

function serviceAccount(value: unknown): value is ServiceAccount {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const account = value as Record<string, unknown>;
  return (
    Object.keys(account).every((key) => allowedAccountKeys.has(key)) &&
    account.type === 'service_account' &&
    typeof account.project_id === 'string' &&
    account.project_id.length > 0 &&
    account.project_id.length <= 128 &&
    typeof account.private_key_id === 'string' &&
    account.private_key_id.length > 0 &&
    typeof account.private_key === 'string' &&
    account.private_key.length > 0 &&
    typeof account.client_email === 'string' &&
    account.client_email.length > 0 &&
    account.token_uri === 'https://oauth2.googleapis.com/token' &&
    (account.universe_domain === undefined || account.universe_domain === 'googleapis.com')
  );
}

export default function Onboarding(props: { applicationID: string }) {
  const auth = useAuth();
  const setDirty = useUnsaved();
  const applicationIDValid = () => uuidPattern.test(props.applicationID);
  const base = () => `/admin/apps/${encodeURIComponent(props.applicationID)}/onboarding`;
  const [readiness, setReadiness] = createSignal<Readiness>();
  const [application, setApplication] = createSignal<Pick<Application, 'id' | 'name' | 'key'>>();
  const [applicationError, setApplicationError] = createSignal('');
  const [etag, setEtag] = createSignal('');
  const [profiles, setProfiles] = createSignal<Profile[]>([]);
  const [approvals, setApprovals] = createSignal<Approval[]>([]);
  const [loading, setLoading] = createSignal(true);
  const [refreshing, setRefreshing] = createSignal(false);
  const [loadError, setLoadError] = createSignal('');
  const [profileError, setProfileError] = createSignal('');
  const [approvalError, setApprovalError] = createSignal('');
  const [writeError, setWriteError] = createSignal('');
  const [writeNotice, setWriteNotice] = createSignal('');
  const [conflict, setConflict] = createSignal(false);
  const [busy, setBusy] = createSignal(false);
  const [pendingMutation, setPendingMutation] = createSignal<PendingMutation>();
  const [retryAfter, setRetryAfter] = createSignal('');
  const [profileID, setProfileID] = createSignal('');
  const [editingProfile, setEditingProfile] = createSignal(false);
  const [profileDirty, setProfileDirty] = createSignal(false);
  const [selectedPlatforms, setSelectedPlatforms] = createSignal<Platform[]>([]);
  const [identifiers, setIdentifiers] = createSignal<Record<Platform, string>>({
    ANDROID: '',
    IOS: '',
  });
  const [environment, setEnvironment] = createSignal<Environment | ''>('');
  const [account, setAccount] = createSignal<ServiceAccount>();
  const [fileError, setFileError] = createSignal('');
  const [holdReason, setHoldReason] = createSignal('');
  const [operation, setOperation] = createSignal<Operation>();
  const [operationError, setOperationError] = createSignal('');
  createEffect(() =>
    setDirty(
      profileDirty() || !!account() || !!pendingMutation() || busy() || !!holdReason().trim(),
    ),
  );
  let fileInput: HTMLInputElement | undefined;
  let credentialSelection = 0;
  let alive = true;
  let readinessTimer: ReturnType<typeof setTimeout> | undefined;
  let operationTimer: ReturnType<typeof setTimeout> | undefined;
  const lifetime = new AbortController();

  function clearTimers() {
    if (readinessTimer) clearTimeout(readinessTimer);
    if (operationTimer) clearTimeout(operationTimer);
    readinessTimer = undefined;
    operationTimer = undefined;
  }

  function clearCredential() {
    credentialSelection++;
    setAccount(undefined);
    setFileError('');
    if (fileInput) fileInput.value = '';
  }

  function handleUnauthorized(error: unknown) {
    if (error instanceof ApiError && error.status === 401)
      window.dispatchEvent(new Event('hello:session-expired'));
  }

  async function readReadiness(showLoading = false): Promise<Readiness | undefined> {
    if (showLoading) setRefreshing(true);
    setLoadError('');
    try {
      let responseETag: string | null = null;
      const envelope = await request<components['schemas']['ReadinessEnvelope']>(
        `/bff/api${base()}`,
        {
          signal: lifetime.signal,
          onHeaders: (headers) => {
            responseETag = headers.get('ETag');
          },
        },
      );
      if (!strongETag(responseETag) || envelope.data.application_id !== props.applicationID)
        throw new ApiError(502, 'Invalid application context');
      if (!alive) return undefined;
      setReadiness(envelope.data);
      if (envelope.data.environment) setEnvironment(envelope.data.environment);
      setEtag(responseETag);
      if (readinessTimer) clearTimeout(readinessTimer);
      readinessTimer = undefined;
      if (envelope.data.blockers.includes('VERIFIER_NOT_CONFIGURED')) {
        readinessTimer = setTimeout(() => void readReadiness(), 2000);
      }
      return envelope.data;
    } catch (error) {
      if (!alive) return undefined;
      handleUnauthorized(error);
      setLoadError(safeError(error));
      return undefined;
    } finally {
      if (alive) {
        setLoading(false);
        setRefreshing(false);
      }
    }
  }

  async function readProfiles() {
    setProfileError('');
    try {
      const response = await request<components['schemas']['ProfileListEnvelope']>(
        `/bff/api${base()}/enrollment-profiles`,
        { signal: lifetime.signal },
      );
      if (alive) setProfiles(response.data.items);
    } catch (error) {
      if (!alive) return;
      handleUnauthorized(error);
      setProfileError(
        error instanceof ApiError && error.status === 404
          ? 'Profil pendaftaran belum tersedia untuk aplikasi ini.'
          : safeError(error),
      );
    }
  }

  async function readApprovals() {
    setApprovalError('');
    try {
      const response = await request<components['schemas']['ApprovalListEnvelope']>(
        `/bff/api${base()}/approvals`,
        { signal: lifetime.signal },
      );
      if (alive) setApprovals(response.data.items);
    } catch (error) {
      if (!alive) return;
      handleUnauthorized(error);
      setApprovalError(
        error instanceof ApiError && error.status === 404
          ? 'Daftar persetujuan belum tersedia untuk aplikasi ini.'
          : safeError(error),
      );
    }
  }

  async function readApplication() {
    try {
      const result = await request<{ data: Application }>(
        `/bff/api/admin/apps/${encodeURIComponent(props.applicationID)}`,
        { signal: lifetime.signal },
      );
      if (!alive) return;
      if (result.data.id !== props.applicationID)
        throw new ApiError(502, 'Invalid application context');
      setApplication({ id: result.data.id, name: result.data.name, key: result.data.key });
      setApplicationError('');
    } catch (error) {
      if (!alive) return;
      setApplication(undefined);
      handleUnauthorized(error);
      setApplicationError(safeError(error));
    }
  }

  async function refreshAll() {
    if (!applicationIDValid()) {
      setLoadError('ID aplikasi tidak valid. Kembali ke daftar aplikasi dan pilih ulang.');
      setLoading(false);
      return;
    }
    setLoading(true);
    await Promise.all([readApplication(), readReadiness(), readProfiles(), readApprovals()]);
  }

  async function refreshReadinessForReview() {
    const result = await readReadiness(true);
    if (result) {
      setConflict(false);
      setWriteNotice('Status terbaru dimuat. Tinjau kembali revisi dan langkah berikutnya.');
    }
  }

  function makePending(
    kind: PendingMutation['kind'],
    suffix: string,
    method: PendingMutation['method'],
    body: PendingMutation['body'],
  ): PendingMutation | undefined {
    const version = etag();
    if (!version || conflict() || !application() || !uuidPattern.test(props.applicationID)) {
      setWriteError('Muat ulang status setup sebelum mengirim perubahan.');
      return undefined;
    }
    return {
      kind,
      path: `${base()}${suffix}`,
      method,
      body,
      etag: version,
      key: crypto.randomUUID(),
    };
  }

  async function runMutation(mutation: PendingMutation) {
    if (busy()) return;
    setBusy(true);
    setWriteError('');
    setWriteNotice('');
    setRetryAfter('');
    let responseETag: string | null = null;
    let replayed = false;
    let retryHeader = '';
    let responseLocation = '';
    try {
      const response = await request<unknown>(`/bff/api${mutation.path}`, {
        method: mutation.method,
        body: mutation.body,
        csrf: auth.csrf(),
        idempotencyKey: mutation.key,
        ifMatch: mutation.etag,
        signal: lifetime.signal,
        onHeaders: (headers) => {
          responseETag = headers.get('ETag');
          replayed = headers.get('Idempotency-Replayed') === 'true';
          responseLocation = headers.get('Location') ?? '';
          const retry = headers.get('Retry-After') ?? '';
          if (/^[1-9][0-9]{0,3}$/.test(retry) && Number(retry) <= 3600) retryHeader = retry;
        },
      });
      if (!alive) return;
      setPendingMutation(undefined);
      setConflict(false);
      if (strongETag(responseETag)) setEtag(responseETag);
      if (mutation.kind === 'credential') clearCredential();
      if (mutation.kind === 'enrollment') {
        setEditingProfile(false);
        setProfileDirty(false);
      }
      if (mutation.kind === 'hold') setHoldReason('');
      setWriteNotice(
        replayed
          ? 'Permintaan sebelumnya sudah diterapkan.'
          : mutation.kind === 'verify'
            ? 'Pemeriksaan OAuth diterima.'
            : mutation.kind === 'credential'
              ? 'Kredensial tersimpan sebagai kandidat.'
              : mutation.kind === 'enrollment'
                ? 'Profil pendaftaran disimpan.'
                : 'Distribusi ditahan.',
      );
      if (mutation.kind === 'verify') {
        const envelope = response as components['schemas']['OperationEnvelope'];
        const operationID = envelope?.data?.id;
        if (!operationID || !uuidPattern.test(operationID)) {
          setOperationError(
            'Status pemeriksaan belum tersedia. Muat ulang untuk melihat hasilnya.',
          );
        } else {
          setOperationError('');
          setOperation(envelope.data);
          const expectedLocation = `/bff/api${base()}/operations/${operationID}`;
          if (responseLocation && responseLocation !== expectedLocation) {
            setOperationError(
              'Lokasi status pemeriksaan tidak sesuai. Status akan dibaca dari aplikasi ini.',
            );
          }
          void pollOperation(operationID);
        }
      }
      void readReadiness();
    } catch (error) {
      if (!alive) return;
      handleUnauthorized(error);
      const status = error instanceof ApiError ? error.status : 0;
      if (status === 412) setConflict(true);
      setWriteError(safeError(error));
      if (retryHeader && (status === 429 || status === 503)) setRetryAfter(retryHeader);
      if (status === 0 || status === 429 || status === 503 || status >= 500) {
        setPendingMutation(mutation);
      } else {
        setPendingMutation(undefined);
        if (mutation.kind === 'credential') clearCredential();
      }
    } finally {
      if (alive) setBusy(false);
    }
  }

  async function pollOperation(operationID: string) {
    if (!alive || !uuidPattern.test(operationID)) return;
    try {
      const response = await request<components['schemas']['OperationEnvelope']>(
        `/bff/api${base()}/operations/${operationID}`,
        { signal: lifetime.signal },
      );
      if (!alive) return;
      setOperation(response.data);
      setOperationError('');
      if (response.data.state === 'PENDING' || response.data.state === 'RUNNING') {
        operationTimer = setTimeout(() => void pollOperation(operationID), 2000);
      } else {
        operationTimer = undefined;
        void readReadiness();
      }
    } catch (error) {
      if (!alive) return;
      handleUnauthorized(error);
      setOperationError(safeError(error));
      operationTimer = setTimeout(() => void pollOperation(operationID), 2000);
    }
  }

  function selectProfile(value: string) {
    setProfileDirty(true);
    setProfileID(value);
    const profile = profiles().find((item) => item.id === value);
    setSelectedPlatforms(profile?.platforms.slice(0, 1) ?? []);
  }

  function togglePlatform(platform: Platform, checked: boolean) {
    setSelectedPlatforms((current) => {
      if (checked) return current.includes(platform) ? current : [...current, platform];
      return current.filter((value) => value !== platform);
    });
  }

  function editIdentifier(platform: Platform, value: string) {
    setIdentifiers((current) => ({ ...current, [platform]: value }));
  }

  async function saveEnrollment(event: SubmitEvent) {
    event.preventDefault();
    setWriteError('');
    const profile = profiles().find((item) => item.id === profileID());
    const platforms =
      profile?.platforms.filter((value) => selectedPlatforms().includes(value)) ?? [];
    if (
      !profile ||
      platforms.length < 1 ||
      platforms.some((value) => {
        const identifier = identifiers()[value].trim();
        return !validText(identifier, 255);
      })
    ) {
      setWriteError('Pilih profil, minimal satu platform, dan ID paket aplikasi yang valid.');
      return;
    }
    const body: EnrollmentInput = {
      profile_id: profile.id,
      platforms: platforms.map((platform) => ({
        platform,
        application_identifier: identifiers()[platform].trim(),
      })),
    };
    const mutation = makePending('enrollment', '/enrollment', 'PUT', body);
    if (mutation) await runMutation(mutation);
  }

  async function chooseCredential(event: Event & { currentTarget: HTMLInputElement }) {
    const input = event.currentTarget;
    const file = input.files?.[0];
    const selection = ++credentialSelection;
    setAccount(undefined);
    setFileError('');
    setWriteError('');
    if (!file) return;
    if (file.size > 64 * 1024) {
      clearCredential();
      setFileError('Berkas JSON harus berukuran maksimal 64 KiB.');
      return;
    }
    try {
      const parsed: unknown = JSON.parse(await file.text());
      if (!alive || selection !== credentialSelection || input.files?.[0] !== file) return;
      if (!serviceAccount(parsed)) throw new Error('invalid account');
      setAccount(parsed);
    } catch {
      if (!alive || selection !== credentialSelection || input.files?.[0] !== file) return;
      clearCredential();
      setFileError(
        'Berkas tidak sesuai format service account yang didukung. Pilih berkas JSON lain.',
      );
    }
  }

  async function storeCredential(event: SubmitEvent) {
    event.preventDefault();
    setWriteError('');
    const value = account();
    if (!value || !environment()) {
      setFileError('Pilih lingkungan dan berkas kredensial terlebih dahulu.');
      return;
    }
    const body: FcmInput = {
      environment: environment() as Environment,
      target_project_id: value.project_id,
      service_account: value,
    };
    const mutation = makePending('credential', '/fcm', 'PUT', body);
    if (mutation) await runMutation(mutation);
  }

  async function verifyCredential() {
    const candidate = readiness()?.candidate_configuration_id;
    if (!candidate || readiness()?.blockers.includes('VERIFIER_NOT_CONFIGURED')) return;
    const body: VerifyInput = { configuration_id: candidate };
    const mutation = makePending('verify', '/fcm/verify', 'POST', body);
    if (mutation) await runMutation(mutation);
  }

  async function holdDispatch(event: SubmitEvent) {
    event.preventDefault();
    const reason = holdReason().trim();
    if (!validText(reason, 256)) {
      setWriteError('Jelaskan alasan penahanan, maksimal 256 byte UTF-8.');
      return;
    }
    const body: HoldInput = { reason };
    const mutation = makePending('hold', '/dispatch/hold', 'POST', body);
    if (mutation) await runMutation(mutation);
  }

  onMount(() => {
    if (!applicationIDValid()) {
      setLoadError('ID aplikasi tidak valid. Kembali ke daftar aplikasi dan pilih ulang.');
      setLoading(false);
      return;
    }
    void refreshAll();
  });
  onCleanup(() => {
    alive = false;
    clearTimers();
    lifetime.abort();
    setPendingMutation(undefined);
    setAccount(undefined);
  });

  const selectedProfile = () => profiles().find((item) => item.id === profileID());
  const verifierReady = () => !readiness()?.blockers.includes('VERIFIER_NOT_CONFIGURED');
  const credentialReady = () =>
    !!account() && !!environment() && !!etag() && !conflict() && !busy();

  return (
    <>
      <A class="back-link" href={`/apps/admin/applications/${props.applicationID}`}>
        ← Registry aplikasi
      </A>
      <header class="page-heading">
        <div>
          <h1>Konfigurasi notifikasi</h1>
          <Show when={application()}>
            {(target) => (
              <p style={{ 'overflow-wrap': 'anywhere' }}>
                <strong>{target().name}</strong> · {target().key}
              </p>
            )}
          </Show>
          <p class="muted">Siapkan profil pendaftaran dan periksa kredensial Firebase aplikasi.</p>
        </div>
        <button
          class="button secondary"
          type="button"
          onClick={() => void refreshAll()}
          disabled={refreshing()}
        >
          {refreshing() ? 'Memuat…' : 'Muat ulang status'}
        </button>
      </header>

      <Show when={loadError()}>
        <div class="notice notice-error" role="alert">
          <p>{loadError()}</p>
          <button class="button secondary" type="button" onClick={() => void refreshAll()}>
            Coba muat ulang
          </button>
        </div>
      </Show>
      <Show when={applicationError()}>
        <p class="notice notice-error" role="alert">
          {applicationError()}
        </p>
      </Show>
      <Show when={loading()}>
        <p class="loading-state" role="status">
          Memuat status persiapan…
        </p>
      </Show>

      <Show when={readiness()}>
        {(current) => (
          <div class="stack">
            <section class="surface stack" aria-labelledby="onboarding-status-heading">
              <h2 id="onboarding-status-heading">Status aplikasi</h2>
              <dl class="settings-details">
                <dt>ID aplikasi</dt>
                <dd class="mono">{props.applicationID}</dd>
                <dt>Status aplikasi</dt>
                <dd>{statusLabel(current().application_status)}</dd>
                <dt>Distribusi</dt>
                <dd>{dispatchLabel(current().dispatch_mode)}</dd>
                <dt>Revisi setup</dt>
                <dd class="mono">{current().revision}</dd>
                <dt>Lingkungan</dt>
                <dd>{current().environment ?? 'Belum dipilih'}</dd>
                <Show when={current().candidate_configuration_id}>
                  {(candidate) => (
                    <>
                      <dt>Kandidat kredensial</dt>
                      <dd class="mono">{candidate()}</dd>
                    </>
                  )}
                </Show>
                <Show when={current().current_configuration_id}>
                  {(configured) => (
                    <>
                      <dt>Kredensial aktif</dt>
                      <dd class="mono">{configured()}</dd>
                    </>
                  )}
                </Show>
              </dl>
              <Show when={current().blockers.length > 0}>
                <div class="notice" aria-label="Persiapan yang masih diperlukan">
                  <h3>Yang masih diperlukan</h3>
                  <ul>
                    <For each={current().blockers}>{(code) => <li>{blockerLabel(code)}</li>}</For>
                  </ul>
                </div>
              </Show>
              <Show when={current().checks.length > 0}>
                <div>
                  <h3>Pemeriksaan</h3>
                  <ul>
                    <For each={current().checks}>
                      {(check) => (
                        <li>
                          {checkLabel(check.code)}: {outcomeLabel(check.outcome)}
                        </li>
                      )}
                    </For>
                  </ul>
                </div>
              </Show>
            </section>

            <div class="settings-grid">
              <section class="surface stack" aria-labelledby="enrollment-heading">
                <h2 id="enrollment-heading">Profil pendaftaran</h2>
                <Show when={current().enrollment_profile_id}>
                  {(id) => (
                    <p class="help-text">
                      Profil tersimpan:{' '}
                      <strong>
                        {profiles().find((profile) => profile.id === id())?.name ?? id()}
                      </strong>
                      .
                    </p>
                  )}
                </Show>
                <p class="help-text">
                  Pilih profil aplikasi dan ID paket atau bundle yang akan digunakan perangkat.
                </p>
                <Show when={profileError()}>
                  <p class="notice notice-error" role="alert">
                    {profileError()}
                  </p>
                </Show>
                <Show
                  when={!current().enrollment_profile_id || editingProfile()}
                  fallback={
                    <button
                      class="button secondary admin-submit"
                      type="button"
                      onClick={() => {
                        selectProfile(current().enrollment_profile_id ?? '');
                        setEditingProfile(true);
                      }}
                      disabled={busy() || conflict() || !!pendingMutation()}
                    >
                      Ubah profil pendaftaran
                    </button>
                  }
                >
                  <p class="help-text">
                    Pilih platform dan isi ID aplikasi untuk konfigurasi yang akan disimpan.
                  </p>
                  <form class="stack" onSubmit={(event) => void saveEnrollment(event)}>
                    <fieldset
                      class="stack"
                      disabled={
                        busy() ||
                        conflict() ||
                        !!pendingMutation() ||
                        !application() ||
                        profiles().length === 0
                      }
                    >
                      <label>
                        Profil pendaftaran
                        <select
                          value={profileID()}
                          onChange={(event) => selectProfile(event.currentTarget.value)}
                        >
                          <option value="">Pilih profil</option>
                          <For each={profiles()}>
                            {(profile) => <option value={profile.id}>{profile.name}</option>}
                          </For>
                        </select>
                      </label>
                      <Show when={selectedProfile()}>
                        {(profile) => (
                          <fieldset class="stack">
                            <legend>Platform</legend>
                            <For each={profile().platforms}>
                              {(platform) => (
                                <label class="check-label">
                                  <input
                                    type="checkbox"
                                    checked={selectedPlatforms().includes(platform)}
                                    onChange={(event) =>
                                      togglePlatform(platform, event.currentTarget.checked)
                                    }
                                  />
                                  {platform === 'ANDROID' ? 'Android' : 'iOS'}
                                </label>
                              )}
                            </For>
                            <For each={selectedPlatforms()}>
                              {(platform) => (
                                <label>
                                  {platform === 'ANDROID' ? 'ID aplikasi Android' : 'ID bundle iOS'}
                                  <input
                                    value={identifiers()[platform]}
                                    maxlength={255}
                                    autocomplete="off"
                                    onInput={(event) =>
                                      editIdentifier(platform, event.currentTarget.value)
                                    }
                                    aria-label={
                                      platform === 'ANDROID'
                                        ? 'ID aplikasi Android'
                                        : 'ID bundle iOS'
                                    }
                                  />
                                </label>
                              )}
                            </For>
                          </fieldset>
                        )}
                      </Show>
                      <button class="button primary admin-submit" type="submit" disabled={!etag()}>
                        Simpan profil
                      </button>
                      <Show when={current().enrollment_profile_id}>
                        <button
                          class="button secondary"
                          type="button"
                          onClick={() => {
                            setEditingProfile(false);
                            setProfileDirty(false);
                          }}
                        >
                          Batal ubah profil
                        </button>
                      </Show>
                    </fieldset>
                  </form>
                </Show>
              </section>

              <section class="surface stack" aria-labelledby="credential-heading">
                <h2 id="credential-heading">Kredensial Firebase</h2>
                <p class="help-text">
                  Berkas dipakai untuk permintaan ini. Kunci pribadi tidak ditampilkan atau disimpan
                  ke penyimpanan browser.
                </p>
                <form class="stack" onSubmit={(event) => void storeCredential(event)}>
                  <fieldset
                    class="stack"
                    disabled={busy() || conflict() || !!pendingMutation() || !application()}
                  >
                    <label>
                      Lingkungan
                      <select
                        value={environment()}
                        disabled={!!current().environment}
                        onChange={(event) =>
                          setEnvironment(event.currentTarget.value as Environment | '')
                        }
                      >
                        <option value="">Pilih lingkungan</option>
                        <option value="staging">Staging</option>
                        <option value="production">Produksi</option>
                      </select>
                    </label>
                    <label>
                      Berkas kredensial FCM (service account JSON)
                      <input
                        ref={fileInput}
                        type="file"
                        accept="application/json,.json"
                        onChange={(event) => void chooseCredential(event)}
                        aria-describedby="credential-file-help"
                      />
                    </label>
                    <p id="credential-file-help" class="help-text">
                      Maksimal 64 KiB. Pastikan berkas berasal dari project Firebase yang dipilih.
                    </p>
                    <Show when={fileError()}>
                      <p class="field-error" role="alert">
                        {fileError()}
                      </p>
                    </Show>
                    <Show when={account()}>
                      {(value) => (
                        <p class="help-text">
                          Project terdeteksi: <span class="mono">{value().project_id}</span>
                        </p>
                      )}
                    </Show>
                    <button
                      class="button primary admin-submit"
                      type="submit"
                      disabled={!credentialReady()}
                    >
                      Simpan kredensial
                    </button>
                  </fieldset>
                </form>
              </section>
            </div>

            <section class="surface stack" aria-labelledby="verification-heading">
              <h2 id="verification-heading">Pemeriksaan OAuth</h2>
              <p class="help-text">
                Pemeriksaan ini meminta token OAuth untuk memvalidasi kredensial. Ini tidak mengirim
                notifikasi dan tidak memastikan izin pengiriman atau penerimaan perangkat.
              </p>
              <Show
                when={current().candidate_configuration_id}
                fallback={
                  <p class="help-text">
                    Simpan kandidat kredensial sebelum menjalankan pemeriksaan.
                  </p>
                }
              >
                <button
                  class="button primary admin-submit"
                  type="button"
                  disabled={
                    busy() ||
                    conflict() ||
                    !!pendingMutation() ||
                    !application() ||
                    !etag() ||
                    !verifierReady() ||
                    (!!operation() && ['PENDING', 'RUNNING'].includes(operation()!.state))
                  }
                  onClick={() => void verifyCredential()}
                >
                  Verifikasi FCM
                </button>
              </Show>
              <Show when={!verifierReady()}>
                <p class="help-text" role="status">
                  Pemeriksa belum tersedia. Status akan diperiksa kembali otomatis.
                </p>
              </Show>
              <Show when={operation()}>
                {(value) => (
                  <div class="notice" role="status" aria-live="polite">
                    <p>{operationMessage(value())}</p>
                    <p class="help-text">
                      Status: {operationState(value().state)}. Diperbarui saat pemeriksaan berjalan.
                    </p>
                    <Show when={operationError()}>
                      <p class="field-error">{operationError()}</p>
                    </Show>
                  </div>
                )}
              </Show>
              <Show when={writeError()}>
                <p class="notice notice-error" role="alert">
                  {writeError()}
                </p>
              </Show>
              <Show when={retryAfter()}>
                <p class="help-text">Tunggu {retryAfter()} detik sebelum mencoba kembali.</p>
              </Show>
              <Show when={pendingMutation()}>
                {(retry) => (
                  <button
                    class="button secondary"
                    type="button"
                    onClick={() => void runMutation(retry())}
                    disabled={busy()}
                  >
                    Coba lagi dengan permintaan yang sama
                  </button>
                )}
              </Show>
              <Show when={conflict()}>
                <div class="notice notice-error" role="alert">
                  <p>Revisi berubah. Tinjau status terbaru sebelum mengirim perubahan baru.</p>
                  <button
                    class="button secondary"
                    type="button"
                    onClick={() => void refreshReadinessForReview()}
                    disabled={refreshing()}
                  >
                    Muat ulang dan tinjau
                  </button>
                </div>
              </Show>
              <Show when={writeNotice()}>
                <p class="notice notice-success" role="status">
                  {writeNotice()}
                </p>
              </Show>
            </section>

            <Show when={current().dispatch_mode !== 'HELD'}>
              <section class="surface stack" aria-labelledby="hold-heading">
                <h2 id="hold-heading">Tahan distribusi</h2>
                <p class="help-text">
                  Tindakan ini menahan distribusi aplikasi. Persetujuan untuk membuka distribusi
                  belum tersedia di sini.
                </p>
                <form class="stack" onSubmit={(event) => void holdDispatch(event)}>
                  <fieldset
                    class="stack"
                    disabled={busy() || conflict() || !!pendingMutation() || !application()}
                  >
                    <label>
                      Alasan penahanan
                      <input
                        value={holdReason()}
                        maxlength={256}
                        onInput={(event) => setHoldReason(event.currentTarget.value)}
                      />
                    </label>
                    <button class="button secondary admin-submit" type="submit" disabled={!etag()}>
                      Tahan distribusi
                    </button>
                  </fieldset>
                </form>
              </section>
            </Show>
            <section class="surface stack" aria-labelledby="device-heading">
              <h2 id="device-heading">Penerimaan perangkat</h2>
              <p class="help-text">
                Pendaftaran perangkat belum tersedia. Pemeriksaan OAuth tidak memastikan notifikasi
                diterima perangkat.
              </p>
            </section>

            <section class="surface stack" aria-labelledby="approvals-heading">
              <h2 id="approvals-heading">Persetujuan</h2>
              <Show
                when={approvalError()}
                fallback={
                  <Show
                    when={approvals().length > 0}
                    fallback={<p class="help-text">Belum ada persetujuan untuk aplikasi ini.</p>}
                  >
                    <ul>
                      <For each={approvals()}>
                        {(approval) => (
                          <li>
                            {approvalKind(approval.kind)} · {approvalStatus(approval.status)} ·
                            berakhir {formatDate(approval.expires_at)}
                          </li>
                        )}
                      </For>
                    </ul>
                  </Show>
                }
              >
                <p class="help-text" role="status">
                  {approvalError()}
                </p>
              </Show>
            </section>
          </div>
        )}
      </Show>
    </>
  );
}

function statusLabel(value: Readiness['application_status']) {
  const labels: Record<Readiness['application_status'], string> = {
    PROVISIONING: 'Disiapkan',
    ACTIVE: 'Aktif',
    HELD: 'Ditahan',
    RETIRED: 'Dinonaktifkan',
  };
  return labels[value];
}

function dispatchLabel(value: Readiness['dispatch_mode']) {
  const labels: Record<Readiness['dispatch_mode'], string> = {
    HELD: 'Ditahan',
    PILOT: 'Uji terbatas',
    FULL: 'Penuh',
  };
  return labels[value];
}

function operationState(value: Operation['state']) {
  const labels: Record<Operation['state'], string> = {
    PENDING: 'menunggu',
    RUNNING: 'berjalan',
    SUCCEEDED: 'selesai',
    FAILED: 'gagal',
    SUPERSEDED: 'tidak berlaku',
  };
  return labels[value];
}

function approvalKind(value: Approval['kind']) {
  const labels: Record<Approval['kind'], string> = {
    TEST_COHORT: 'Perangkat uji',
    PILOT: 'Uji terbatas',
    FULL: 'Distribusi penuh',
  };
  return labels[value];
}

function approvalStatus(value: Approval['status']) {
  const labels: Record<Approval['status'], string> = {
    VALID: 'Berlaku',
    EXPIRED: 'Kedaluwarsa',
    REVOKED: 'Dicabut',
  };
  return labels[value];
}
