import { test, expect, type Page } from '@playwright/test';

const app = '11111111-1111-4111-8111-111111111111';
const campaign = `/apps/${app}/campaigns/9007199254740993`;

async function login(page: Page) {
  await page.goto('/login');
  await page.getByLabel('Email', { exact: true }).fill('staff@example.invalid');
  await page.getByLabel('Kata sandi', { exact: true }).fill('fixture-only');
  await page.getByRole('button', { name: 'Masuk ke dashboard' }).click();
  await expect(page.getByRole('heading', { name: 'Pilih aplikasi' })).toBeVisible();
}

for (const [route, field] of [
  ['campaigns/new', 'Judul notifikasi'],
  ['templates/new', 'Nama template'],
  ['segments/new', 'Nama segment'],
  ['settings', 'Nama'],
]) {
  test(`cancelling logout preserves the session and unsaved ${route}`, async ({ page }) => {
    await login(page);
    await page.goto(`/apps/${app}/${route}`);
    await page.getByLabel(field, { exact: true }).fill('Unsaved audit value');
    let logouts = 0;
    page.on('request', (request) => {
      if (request.url().endsWith('/bff/logout')) logouts++;
    });
    page.once('dialog', async (dialog) => {
      expect(dialog.type()).toBe('confirm');
      await dialog.dismiss();
    });
    const logout = page.getByRole('button', { name: 'Keluar', exact: true });
    await logout.click();
    await expect(logout).toBeEnabled();
    expect(logouts).toBe(0);
    expect((await (await page.request.get('/bff/session')).json()).authenticated).toBe(true);
    await expect(page.getByLabel(field, { exact: true })).toHaveValue('Unsaved audit value');
    const dialogs: string[] = [];
    page.on('dialog', async (dialog) => {
      dialogs.push(dialog.type());
      await dialog.accept();
    });
    await logout.click();
    await expect(page).toHaveURL(/\/login$/);
    await expect(page.getByRole('heading', { name: 'Selamat datang kembali.' })).toBeVisible();
    expect(logouts).toBe(1);
    expect(dialogs).toEqual(['confirm']);
  });
}

test('failed logout retains unsaved navigation protection', async ({ page }) => {
  await login(page);
  await page.goto(`/apps/${app}/campaigns/new`);
  await page.getByLabel('Judul notifikasi', { exact: true }).fill('Keep this draft');
  await page.route('**/bff/logout', (route) =>
    route.fulfill({ status: 503, contentType: 'application/json', body: '{}' }),
  );
  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', { name: 'Keluar', exact: true }).click();
  await expect(page.getByRole('alert')).toBeVisible();
  page.once('dialog', (dialog) => dialog.dismiss());
  await page.getByRole('link', { name: 'Semua campaign', exact: true }).click();
  await expect(page).toHaveURL(/\/campaigns\/new$/);
  await expect(page.getByLabel('Judul notifikasi', { exact: true })).toHaveValue('Keep this draft');
  expect((await (await page.request.get('/bff/session')).json()).authenticated).toBe(true);
});

test('schedule remains locked while pending and unlocks after held acceptance', async ({
  page,
}) => {
  await login(page);
  await page.goto(campaign);
  await page.getByRole('button', { name: 'Jadwalkan pengiriman', exact: true }).click();
  const date = page.getByLabel('Waktu pengiriman');
  const future = new Date();
  const value = `${future.getFullYear() + 1}-01-15T10:30`;
  await date.fill(value);
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(`**/bff/api${campaign}/schedule`, async (route) => {
    await gate;
    await route.fulfill({ status: 503, contentType: 'application/json', body: '{}' });
  });
  const request = page.waitForRequest((item) => item.url().endsWith('/schedule'));
  await page.getByRole('button', { name: 'Konfirmasi jadwal' }).click();
  const submitted = await request;
  try {
    await expect(date).toBeDisabled();
    await expect(date).toHaveValue(value);
    expect(submitted.postDataJSON().scheduled_at).toBe(
      await date.evaluate((input: HTMLInputElement) => new Date(input.value).toISOString()),
    );
    await expect(page.getByRole('button', { name: 'Memproses…' })).toBeDisabled();
  } finally {
    release();
  }
  await expect(page.getByRole('alert')).toContainText('Pengiriman sedang ditahan');
  await expect(date).toBeEnabled();
  await expect(date).toHaveValue(value);
});

test('draft results skip delivery requests and refresh into real result states', async ({
  page,
}) => {
  await login(page);
  let status = 'DRAFT';
  let resultRequests = 0;
  await page.route(`**/bff/api${campaign}`, async (route) => {
    const response = await route.fetch();
    const payload = await response.json();
    payload.data.status = status;
    await route.fulfill({ response, json: payload });
  });
  page.on('request', (request) => {
    if (/\/(statistics|deliveries|reads)(\?|$)/.test(request.url())) resultRequests++;
  });
  await page.goto(`${campaign}/results`);
  await expect(page.getByRole('heading', { name: 'Campaign belum dikirim' })).toBeVisible();
  expect(resultRequests).toBe(0);
  await expect(page.getByRole('link', { name: 'Tinjau campaign' })).toHaveAttribute(
    'href',
    campaign,
  );
  await expect(page.getByText('Pesan sampai, tetapi receipt belum muncul?')).toHaveCount(0);
  status = 'QUEUED';
  await page.getByRole('button', { name: 'Perbarui' }).click();
  await expect(page.getByRole('heading', { name: 'Statistik belum lengkap' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Belum ada catatan pengiriman.' })).toBeVisible();
  expect(resultRequests).toBeGreaterThanOrEqual(2);
  await page.getByRole('button', { name: 'Pembukaan', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Belum ada catatan pembukaan.' })).toBeVisible();
});

test('campaign lookup errors stay retryable without requesting delivery data', async ({ page }) => {
  await login(page);
  let unavailable = true;
  let results = 0;
  await page.route(`**/bff/api${campaign}`, async (route) => {
    if (unavailable)
      await route.fulfill({ status: 502, contentType: 'application/json', body: '{}' });
    else await route.continue();
  });
  page.on('request', (request) => {
    if (/\/(statistics|deliveries|reads)(\?|$)/.test(request.url())) results++;
  });
  await page.goto(`${campaign}/results`);
  await expect(page.getByRole('alert')).toBeVisible();
  expect(results).toBe(0);
  unavailable = false;
  await page.getByRole('button', { name: 'Muat ulang' }).click();
  await expect(page.getByRole('heading', { name: 'Campaign belum dikirim' })).toBeVisible();
});

for (const theme of ['light', 'dark']) {
  for (const width of [1440, 390]) {
    test(`campaign actions remain reachable at ${width}px in ${theme}`, async ({ page }) => {
      await page.setViewportSize({ width, height: 844 });
      await page.addInitScript((value) => localStorage.setItem('hello.theme', value), theme);
      await login(page);
      await page.goto(`/apps/${app}/campaigns/new`);
      await expect(page.getByLabel('Judul notifikasi', { exact: true })).toBeVisible();
      const save = page.getByRole('button', { name: 'Simpan draft', exact: true });
      await expect(save).toBeInViewport({ ratio: 1 });
      await page.getByRole('button', { name: 'Penerima Uji #42', exact: true }).click();
      await expect(save).toBeInViewport({ ratio: 1 });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      await page.screenshot({ path: `test-results/polish-editor-${width}-${theme}.png` });
      page.once('dialog', (dialog) => dialog.accept());
      await page.getByRole('link', { name: 'Kembali', exact: true }).click();
      await page.goto(campaign);
      await expect(
        page.getByRole('button', { name: 'Jadwalkan pengiriman', exact: true }),
      ).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      await page.screenshot({
        path: `test-results/polish-detail-${width}-${theme}.png`,
        fullPage: true,
      });
    });
  }
}
