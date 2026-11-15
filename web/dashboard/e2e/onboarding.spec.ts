import { test, expect, type Page } from '@playwright/test';

// Credential forms must not enter Playwright request traces, even in fixtures.
test.use({ trace: 'off' });

const app = '11111111-1111-4111-8111-111111111111';
const profile = '22222222-2222-4222-8222-222222222222';
const configuration = '33333333-3333-4333-8333-333333333333';
const operation = '44444444-4444-4444-8444-444444444444';
const api = `/bff/api/admin/apps/${app}/onboarding`;
const secret = 'fixture-private-material-never-render';
const account = {
  type: 'service_account',
  project_id: 'integration-fixture',
  private_key_id: 'fixture',
  private_key: secret,
  client_email: 'fixture@integration-fixture.iam.gserviceaccount.com',
  token_uri: 'https://oauth2.googleapis.com/token',
};

async function mockSetup(page: Page, options: { stale?: boolean; lostUpload?: boolean } = {}) {
  await page.route(`**/bff/api/admin/apps/${app}`, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ data: { id: app, name: 'N'.repeat(128), key: 'a'.repeat(63) } }),
    }),
  );
  let revision = 1;
  let configured = false;
  let stored = false;
  let verified = false;
  let firstUpload: { key: string; etag: string } | undefined;
  let attempts = 0;
  const etag = () => `"onboarding:${app}:${revision}"`;
  const readiness = () => ({
    application_id: app,
    application_status: 'PROVISIONING',
    dispatch_mode: 'HELD',
    revision: String(revision),
    environment: stored ? 'staging' : null,
    current_configuration_id: null,
    candidate_configuration_id: stored ? configuration : null,
    enrollment_profile_id: configured ? profile : null,
    cohort_id: null,
    checks: verified
      ? [
          {
            code: 'OAUTH',
            outcome: 'PASSED',
            checked_at: '2026-01-01T00:00:00Z',
            expires_at: '2099-01-01T00:00:00Z',
          },
        ]
      : [],
    blockers: ['RUNTIME_PENDING', 'DISPATCH_APPROVAL_REQUIRED'],
    next_actions: [],
    last_operation: null,
  });
  await page.route(`**${api}**`, async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const headers = request.headers();
    const respond = (status: number, data: unknown, extra: Record<string, string> = {}) =>
      route.fulfill({
        status,
        contentType: 'application/json',
        headers: { 'Cache-Control': 'no-store', ...extra },
        body: JSON.stringify({
          success: status < 400,
          message: status < 400 ? 'ok' : 'conflict',
          data,
        }),
      });
    if (path === api && request.method() === 'GET')
      return respond(200, readiness(), { ETag: etag() });
    if (path.endsWith('/enrollment-profiles'))
      return respond(200, {
        items: [{ id: profile, name: 'Profil Android uji', platforms: ['ANDROID'] }],
      });
    if (path.endsWith('/approvals')) return respond(200, { items: [] });
    if (path.endsWith('/operations/' + operation)) {
      verified = true;
      return respond(200, {
        id: operation,
        kind: 'VERIFY',
        state: 'SUCCEEDED',
        created_at: '2026-01-01T00:00:00Z',
        completed_at: '2026-01-01T00:00:01Z',
        failure_code: null,
        resulting_revision: String(revision),
      });
    }
    if (path.endsWith('/fcm') && firstUpload && headers['idempotency-key'] === firstUpload.key) {
      expect(headers['if-match']).toBe(firstUpload.etag);
      attempts++;
      return respond(
        201,
        {
          id: configuration,
          state: 'STORED',
          target_project_id: account.project_id,
          service_account_email: account.client_email,
          revision: String(revision),
        },
        { ETag: etag(), 'Idempotency-Replayed': 'true' },
      );
    }
    expect(headers['idempotency-key']).toMatch(/^[0-9a-f-]{36}$/);
    expect(headers['if-match']).toBe(etag());
    if (options.stale && path.endsWith('/enrollment')) {
      options.stale = false;
      revision++;
      return respond(412, null);
    }
    if (path.endsWith('/enrollment')) {
      configured = true;
      revision++;
      return respond(200, readiness(), { ETag: etag() });
    }
    if (path.endsWith('/fcm')) {
      expect(request.postDataJSON().service_account.private_key).toBe(secret);
      firstUpload = { key: headers['idempotency-key'], etag: headers['if-match'] };
      stored = true;
      revision++;
      attempts++;
      if (options.lostUpload) return route.abort('failed');
      return respond(
        201,
        {
          id: configuration,
          state: 'STORED',
          target_project_id: account.project_id,
          service_account_email: account.client_email,
          revision: String(revision),
        },
        { ETag: etag() },
      );
    }
    if (path.endsWith('/fcm/verify')) {
      return respond(
        202,
        {
          id: operation,
          kind: 'VERIFY',
          state: 'PENDING',
          created_at: '2026-01-01T00:00:00Z',
          completed_at: null,
          failure_code: null,
          resulting_revision: null,
        },
        { Location: `${api}/operations/${operation}` },
      );
    }
    throw new Error('Unexpected onboarding fixture request');
  });
  await page.goto('/login');
  await page.getByLabel('Email', { exact: true }).fill('staff@example.invalid');
  await page.getByLabel('Kata sandi', { exact: true }).fill('fixture-only');
  await page.getByRole('button', { name: 'Masuk ke dashboard' }).click();
  await expect(page.getByRole('heading', { name: 'Pilih aplikasi' })).toBeVisible();
  await page.goto(`/apps/admin/applications/${app}/onboarding`);
  await expect(page.getByRole('heading', { name: 'Konfigurasi notifikasi' })).toBeVisible();
  return { attempts: () => attempts };
}

async function selectProfile(page: Page) {
  await page
    .getByRole('combobox', { name: 'Profil pendaftaran', exact: true })
    .selectOption(profile);
  await page.getByLabel('Android', { exact: true }).check();
  await page.getByLabel('ID aplikasi Android', { exact: true }).fill('com.example.test');
}

async function selectCredential(page: Page) {
  await page.getByRole('combobox', { name: 'Lingkungan', exact: true }).selectOption('staging');
  await page.getByLabel('Berkas kredensial FCM', { exact: false }).setInputFiles({
    name: 'fixture-account.json',
    mimeType: 'application/json',
    buffer: Buffer.from(JSON.stringify(account)),
  });
}

test('setup keeps credentials out of storage and leaves enrollment and dispatch held after OAuth', async ({
  page,
}) => {
  await mockSetup(page);
  await selectProfile(page);
  await page.getByRole('button', { name: 'Simpan profil', exact: true }).click();
  await expect(page.getByText('Profil pendaftaran disimpan.', { exact: true })).toBeVisible();
  await selectCredential(page);
  await page.getByRole('button', { name: 'Simpan kredensial', exact: true }).click();
  await expect(
    page.getByText('Kredensial tersimpan sebagai kandidat.', { exact: true }),
  ).toBeVisible();
  await expect(page.getByLabel('Berkas kredensial FCM', { exact: false })).toHaveValue('');
  await page.getByRole('button', { name: 'Verifikasi FCM', exact: true }).click();
  await expect(
    page.getByText(
      'OAuth berhasil. Izin mengirim notifikasi dan penerimaan di perangkat belum diperiksa.',
      { exact: true },
    ),
  ).toBeVisible();
  await expect(
    page.getByText('Pendaftaran perangkat belum tersedia.', { exact: false }),
  ).toBeVisible();
  expect(await page.locator('body').innerText()).not.toContain(secret);
  const storage = await page.evaluate(() =>
    JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }),
  );
  expect(storage).not.toContain(secret);
  expect(storage).not.toContain('private_key');
  await page.setViewportSize({ width: 320, height: 800 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
});

test('stale setup requires explicit review before another write', async ({ page }) => {
  await mockSetup(page, { stale: true });
  await selectProfile(page);
  const save = page.getByRole('button', { name: 'Simpan profil', exact: true });
  await save.click();
  await expect(
    page.getByRole('button', { name: 'Muat ulang dan tinjau', exact: true }),
  ).toBeVisible();
  await expect(save).toBeDisabled();
  await page.getByRole('button', { name: 'Muat ulang status', exact: true }).click();
  await expect(save).toBeDisabled();
  await page.getByRole('button', { name: 'Muat ulang dan tinjau', exact: true }).click();
  await expect(save).toBeEnabled();
  await save.click();
  await expect(page.getByText('Profil pendaftaran disimpan.', { exact: true })).toBeVisible();
});

test('unknown upload outcome retries the same key and original ETag', async ({ page }) => {
  const state = await mockSetup(page, { lostUpload: true });
  await selectCredential(page);
  await page.getByRole('button', { name: 'Simpan kredensial', exact: true }).click();
  const retry = page.getByRole('button', {
    name: 'Coba lagi dengan permintaan yang sama',
    exact: true,
  });
  await expect(retry).toBeVisible();
  await expect(page.getByLabel('Berkas kredensial FCM', { exact: false })).toBeDisabled();
  await retry.click();
  await expect(
    page.getByText('Permintaan sebelumnya sudah diterapkan.', { exact: true }),
  ).toBeVisible();
  expect(state.attempts()).toBe(2);
  await expect(page.getByLabel('Berkas kredensial FCM', { exact: false })).toHaveValue('');
});
