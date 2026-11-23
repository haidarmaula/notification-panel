import { test, expect, type Page } from '@playwright/test';
import { installOnboardingLifecycleFixture, lifecycleIDs } from './onboarding-lifecycle-fixture';

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
    checks_configuration_id: stored ? configuration : null,
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
  await page.getByLabel('Email', { exact: false }).fill('staff@example.invalid');
  await page.locator('input[name="password"]').fill('fixture-only');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'Choose application' })).toBeVisible();
  await page.goto(`/apps/admin/applications/${app}/onboarding`);
  await expect(page.getByRole('heading', { name: 'Notification setup' })).toBeVisible();
  return { attempts: () => attempts };
}

async function selectProfile(page: Page) {
  await page.getByRole('combobox', { name: 'Enrollment profile', exact: true }).click();
  await page.getByRole('option', { name: 'Profil Android uji', exact: true }).click();
  await page.getByLabel('Android', { exact: true }).check();
  await page.getByLabel('Android application ID', { exact: true }).fill('com.example.test');
}

async function selectCredential(page: Page) {
  await page.getByRole('combobox', { name: 'Environment', exact: true }).click();
  await page.getByRole('option', { name: 'Staging', exact: true }).click();
  await page.locator('input[type="file"]').setInputFiles({
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
  await page.getByRole('button', { name: 'Save profile', exact: true }).click();
  await expect(page.getByText('Enrollment profile saved.', { exact: true })).toBeVisible();
  await selectCredential(page);
  await page.getByRole('button', { name: 'Save credentials', exact: true }).click();
  await expect(page.getByText('Credentials saved as a candidate.', { exact: true })).toBeVisible();
  await expect(page.locator('input[type="file"]')).toHaveValue('');
  await page.getByRole('button', { name: 'Verify FCM', exact: true }).click();
  await expect(
    page.getByText(
      'OAuth succeeded. Delivery permissions and device receipt have not been checked.',
      { exact: true },
    ),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Activate enrollment', exact: true }),
  ).toBeEnabled();
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
  const save = page.getByRole('button', { name: 'Save profile', exact: true });
  await save.click();
  await expect(page.getByRole('button', { name: 'Reload and review', exact: true })).toBeVisible();
  await expect(save).toBeDisabled();
  await page.getByRole('button', { name: 'Refresh status', exact: true }).click();
  await expect(save).toBeDisabled();
  await page.getByRole('button', { name: 'Reload and review', exact: true }).click();
  await expect(save).toBeEnabled();
  await save.click();
  await expect(page.getByText('Enrollment profile saved.', { exact: true })).toBeVisible();
});

test('unknown upload outcome retries the same key and original ETag', async ({ page }) => {
  const state = await mockSetup(page, { lostUpload: true });
  await selectCredential(page);
  await page.getByRole('button', { name: 'Save credentials', exact: true }).click();
  const retry = page.getByRole('button', {
    name: 'Retry the same request',
    exact: true,
  });
  await expect(retry).toBeVisible();
  await expect(page.getByRole('button', { name: /FCM credential file/ })).toBeDisabled();
  await retry.click();
  await expect(
    page.getByText('The previous request has already been applied.', { exact: true }),
  ).toBeVisible();
  expect(state.attempts()).toBe(2);
  await expect(page.locator('input[type="file"]')).toHaveValue('');
});

async function openLifecycle(
  page: Page,
  options: Parameters<typeof installOnboardingLifecycleFixture>[1] = {},
) {
  const fixture = await installOnboardingLifecycleFixture(page, options);
  await page.goto('/login');
  await page.getByLabel('Email', { exact: false }).fill('staff@example.invalid');
  await page.locator('input[name="password"]').fill('fixture-only');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'Choose application' })).toBeVisible();
  await page.goto(`/apps/admin/applications/${lifecycleIDs.app}/onboarding`);
  await expect(page.getByRole('heading', { name: 'Notification setup' })).toBeVisible();
  return fixture;
}

test('activation queues a protected operation and keeps dispatch held', async ({ page }) => {
  const fixture = await openLifecycle(page, { initialStatus: 'PROVISIONING', pendingPolls: 2 });
  await page.getByRole('button', { name: 'Activate enrollment', exact: true }).click();
  await expect(
    page.getByText('Enrollment activation is awaiting operator action.', { exact: true }),
  ).toBeVisible();
  await expect(page.getByText('Status: Pending.', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Refresh status', exact: true })).toBeEnabled();
  await page.reload();
  await expect(page.getByText('Status: Pending.', { exact: true })).toBeVisible();
  await expect(page.getByText('Active', { exact: true })).toBeVisible();
  await expect(page.getByText('Held', { exact: true })).toBeVisible();
  const activation = fixture.requests.find((request) => request.path.endsWith('/activate'));
  expect(activation?.method).toBe('POST');
  expect(activation?.body).toEqual({ configuration_id: lifecycleIDs.configuration });
  expect(activation?.key).toMatch(/^[0-9a-f-]{36}$/);
  expect(activation?.csrf).toBeTruthy();
  expect(activation?.etag).toContain(':8"');
  expect(
    fixture.requests.some((request) =>
      request.path.endsWith(`/operations/${lifecycleIDs.activation}`),
    ),
  ).toBe(true);
});

test('device roster paginates, dry-run submits the approved selection, and target failures remain per device', async ({
  page,
}) => {
  const fixture = await openLifecycle(page);
  await page
    .getByLabel(`Select ANDROID installation ${lifecycleIDs.androidOne}`, { exact: true })
    .check();
  await page.getByRole('button', { name: 'Next', exact: true }).click();
  await page.getByLabel(`Select IOS installation ${lifecycleIDs.iosOne}`, { exact: true }).check();
  await page.getByRole('combobox', { name: 'Test cohort approval', exact: true }).click();
  await page.getByRole('option', { name: new RegExp(lifecycleIDs.approval) }).click();
  await page.getByLabel('Dry-run reason', { exact: true }).fill('Fixture cohort check');
  await page.getByRole('button', { name: 'Queue device dry-run', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'Per-device outcomes', exact: true }),
  ).toBeVisible();
  await expect(page.getByText('FAILED', { exact: true })).toBeVisible();
  await expect(page.getByText('Pending', { exact: true })).toBeVisible();
  const pages = fixture.requests.filter((request) => request.path.includes('/test-installations'));
  expect(
    pages.map((request) => new URLSearchParams(request.path.split('?')[1] ?? '').get('after')),
  ).toEqual([null, lifecycleIDs.androidTwo]);
  const dryRun = fixture.requests.find((request) => request.path.endsWith('/test-cohort'));
  expect(dryRun?.method).toBe('PUT');
  expect(dryRun?.body).toEqual({
    installation_ids: [lifecycleIDs.androidOne, lifecycleIDs.iosOne],
    reason: 'Fixture cohort check',
    evidence_id: lifecycleIDs.approval,
  });
  for (const width of [320, 390]) {
    await page.setViewportSize({ width, height: 844 });
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true);
  }
});

test('PILOT and FULL require their own approvals and queue separate operations', async ({
  page,
}) => {
  const fixture = await openLifecycle(page, { allPassedTargets: true, pendingPolls: 1 });
  await page
    .getByLabel(`Select ANDROID installation ${lifecycleIDs.androidOne}`, { exact: true })
    .check();
  await page
    .getByLabel(`Select ANDROID installation ${lifecycleIDs.androidTwo}`, { exact: true })
    .check();
  await page.getByRole('button', { name: 'Next', exact: true }).click();
  await page.getByLabel(`Select IOS installation ${lifecycleIDs.iosOne}`, { exact: true }).check();
  await page.getByRole('combobox', { name: 'Test cohort approval', exact: true }).click();
  await page
    .getByRole('option', { name: new RegExp(lifecycleIDs.approval) })
    .last()
    .click();
  await page.getByLabel('Dry-run reason', { exact: true }).fill('All target checks passed');
  await page.getByRole('button', { name: 'Queue device dry-run', exact: true }).click();
  await page.getByRole('combobox', { name: 'PILOT approval', exact: true }).click();
  await page.getByRole('option', { name: new RegExp(lifecycleIDs.pilotApproval) }).click();
  await expect(
    page.getByRole('button', { name: 'Request PILOT release', exact: true }),
  ).toBeEnabled();
  await page.getByRole('button', { name: 'Request PILOT release', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'FULL request', exact: true })).toBeVisible();
  await page.getByRole('combobox', { name: 'FULL approval', exact: true }).click();
  await page.getByRole('option', { name: new RegExp(lifecycleIDs.fullApproval) }).click();
  await page.getByRole('button', { name: 'Request FULL release', exact: true }).click();
  await expect(
    page.getByText('FULL release is awaiting operator action.', { exact: true }),
  ).toBeVisible();
  await expect(page.getByText('FULL', { exact: true })).toHaveCount(0);
  await expect(page.getByText('FULL', { exact: true })).toBeVisible();
  const releases = fixture.requests.filter((request) => request.path.endsWith('/dispatch/release'));
  expect(releases.map((request) => (request.body as { mode: string }).mode)).toEqual([
    'PILOT',
    'FULL',
  ]);
  expect(releases[0].body).toEqual({
    mode: 'PILOT',
    cohort_id: lifecycleIDs.cohort,
    evidence_id: lifecycleIDs.pilotApproval,
  });
  expect(releases[1].body).toEqual({
    mode: 'FULL',
    cohort_id: lifecycleIDs.cohort,
    evidence_id: lifecycleIDs.fullApproval,
  });
});

test('stale activation requires review, then failed operation is visible', async ({ page }) => {
  const fixture = await openLifecycle(page, {
    initialStatus: 'PROVISIONING',
    activation: 'FAILED',
    staleOnce: '/activate',
  });
  await page.getByRole('button', { name: 'Activate enrollment', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Reload and review', exact: true })).toBeVisible();
  const activationBeforeReview = fixture.requests.filter((request) =>
    request.path.endsWith('/activate'),
  )[0];
  await page.getByRole('button', { name: 'Reload and review', exact: true }).click();
  await page.getByRole('button', { name: 'Activate enrollment', exact: true }).click();
  await expect(page.getByText('Enrollment activation failed.', { exact: true })).toBeVisible();
  const activationRequests = fixture.requests.filter((request) =>
    request.path.endsWith('/activate'),
  );
  expect(activationRequests).toHaveLength(2);
  expect(activationBeforeReview?.key).not.toBe(activationRequests[1].key);
});

test('unknown activation outcome retries the same key and replays the original operation', async ({
  page,
}) => {
  const replayFixture = await openLifecycle(page, {
    initialStatus: 'PROVISIONING',
    replayOnce: '/activate',
  });
  await page.getByRole('button', { name: 'Activate enrollment', exact: true }).click();
  const retry = page.getByRole('button', { name: 'Retry the same request', exact: true });
  await expect(retry).toBeVisible();
  await retry.click();
  await expect(page.getByText('Active', { exact: true })).toBeVisible();
  const retries = replayFixture.requests.filter((request) => request.path.endsWith('/activate'));
  expect(retries).toHaveLength(2);
  expect(retries[0].key).toBe(retries[1].key);
  expect(retries[0].etag).toBe(retries[1].etag);
  expect(retries[0].body).toEqual(retries[1].body);
});
