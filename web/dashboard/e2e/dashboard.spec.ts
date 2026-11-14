import { test, expect, type Page } from '@playwright/test';
const app = '11111111-1111-4111-8111-111111111111';
async function login(page: Page) {
  await page.goto('/login');
  await page.getByLabel('Email', { exact: true }).fill('staff@example.invalid');
  await page.getByLabel('Kata sandi', { exact: true }).fill('fixture-only');
  await page.getByRole('button', { name: 'Masuk ke dashboard' }).click();
  await expect(page.getByRole('heading', { name: 'Pilih aplikasi' })).toBeVisible();
}
test('logs in through the real BFF, keeps credentials server-side, and logs out', async ({
  page,
}) => {
  await login(page);
  expect(
    await page.evaluate(() => ({ local: localStorage.length, session: sessionStorage.length })),
  ).toEqual({ local: 0, session: 0 });
  const session = await page.request.get('/bff/session');
  const text = await session.text();
  expect(text).not.toContain('access_token');
  expect(text).not.toContain('refresh_token');
  const cookies = await page.context().cookies();
  expect(cookies.find((item) => item.name === 'hello_dev_session')?.httpOnly).toBe(true);
  await page.getByRole('button', { name: 'Keluar', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Selamat datang kembali.' })).toBeVisible();
});
test('shows invalid credentials without creating an authenticated session', async ({ page }) => {
  await page.goto('/login');
  await page.getByLabel('Email', { exact: true }).fill('staff@example.invalid');
  await page.getByLabel('Kata sandi', { exact: true }).fill('wrong');
  await page.getByRole('button', { name: 'Masuk ke dashboard' }).click();
  await expect(page.getByRole('alert')).toContainText('Email atau kata sandi tidak sesuai.');
});
test('creates and edits a draft using scoped recipients without sending', async ({ page }) => {
  await login(page);
  await page.getByRole('link', { name: /Aplikasi Uji fixture-app/ }).click();
  await page.getByRole('link', { name: 'Buat campaign', exact: true }).click();
  await page.getByLabel('Judul notifikasi', { exact: true }).fill('Draft dari pengujian');
  await page.getByLabel('Pesan', { exact: true }).fill('Isi pesan fixture tanpa provider.');
  await page.getByRole('button', { name: 'Penerima Uji #42' }).click();
  await page.getByRole('button', { name: 'Simpan draft' }).click();
  await expect(
    page.getByRole('heading', { name: 'Draft dari pengujian', exact: true }),
  ).toBeVisible();
  await page.getByRole('link', { name: 'Edit draft' }).click();
  await page.getByLabel('Pesan', { exact: true }).fill('Pesan yang diperbarui.');
  await page.getByRole('button', { name: 'Simpan draft' }).click();
  await expect(page.getByText('Pesan yang diperbarui.', { exact: true }).first()).toBeVisible();
});
test('shows held acceptance and preserves the draft results state', async ({ page }) => {
  await login(page);
  await page.goto(`/apps/${app}/campaigns/9007199254740993`);
  await page.getByRole('button', { name: 'Tinjau pengiriman' }).click();
  await expect(page.getByRole('dialog')).toContainText('Penerima Uji');
  await expect(page.getByRole('dialog')).toContainText('Pengguna #42');
  await page.getByRole('button', { name: 'Konfirmasi kirim' }).click();
  await expect(page.getByRole('alert')).toContainText('Pengiriman sedang ditahan');
  await page.getByRole('button', { name: 'Kembali', exact: true }).click();
  await page.getByRole('link', { name: 'Riwayat dan hasil campaign' }).click();
  await expect(page.getByRole('heading', { name: 'Campaign belum dikirim' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Statistik belum lengkap' })).toHaveCount(0);
});
test('mobile navigation and list fit a narrow screen', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await login(page);
  await page.getByRole('link', { name: /Aplikasi Uji fixture-app/ }).click();
  await expect(page.getByRole('heading', { name: 'Campaign', exact: true })).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= document.documentElement.clientWidth,
    ),
  ).toBe(true);
  await page.getByRole('button', { name: 'Buka navigasi' }).click();
  await expect(
    page.getByRole('button', { name: 'Tutup navigasi', exact: true }).last(),
  ).toBeVisible();
});

test('message preview updates locally without a draft write', async ({ page }) => {
  let draftWrites = 0;
  page.on('request', (request) => {
    if (
      request.method() === 'POST' &&
      /\/bff\/api\/apps\/[^/]+\/campaigns$/.test(new URL(request.url()).pathname)
    )
      draftWrites++;
  });
  await login(page);
  await page.getByRole('link', { name: /Aplikasi Uji fixture-app/ }).click();
  await expect(page.getByRole('heading', { name: 'Campaign', exact: true })).toBeVisible();
  await page.screenshot({ path: 'test-results/dashboard-campaigns.png', fullPage: true });
  await page.getByRole('link', { name: 'Buat campaign', exact: true }).click();
  await page.getByLabel('Judul notifikasi', { exact: true }).fill('Pengingat layanan besok');
  await page
    .getByLabel('Pesan', { exact: true })
    .fill('Layanan tersedia mulai pukul 09.00. Silakan buka aplikasi untuk melihat rinciannya.');
  await expect(
    page.getByRole('heading', { name: 'Pengingat layanan besok', exact: true }),
  ).toBeVisible();
  expect(draftWrites).toBe(0);
  await page.screenshot({ path: 'test-results/dashboard-editor.png', fullPage: true });
});

test('persists appearance and desktop sidebar preference without storing account data', async ({
  page,
}) => {
  await login(page);
  await page.goto(`/apps/${app}/settings`);
  await page.getByLabel('Tema', { exact: true }).selectOption('dark');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await page.getByRole('button', { name: 'Sembunyikan sidebar' }).click();
  await expect(page.getByRole('navigation', { name: 'Navigasi utama' })).not.toBeVisible();
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await expect(page.getByRole('button', { name: 'Tampilkan sidebar' })).toBeVisible();
  await page.getByRole('button', { name: 'Tampilkan sidebar' }).click();
  await expect(page.getByRole('navigation', { name: 'Navigasi utama' })).toBeVisible();
  expect(await page.evaluate(() => Object.keys(localStorage).sort())).toEqual([
    'hello.sidebar',
    'hello.theme',
  ]);
  await page.screenshot({ path: 'test-results/dashboard-settings-dark.png', fullPage: true });
  await page.emulateMedia({ colorScheme: 'light' });
  await page.getByLabel('Tema', { exact: true }).selectOption('system');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  await page.emulateMedia({ colorScheme: 'dark' });
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
});

test('creates, replaces and uses a template without sending', async ({ page }) => {
  await login(page);
  await page.goto(`/apps/${app}/templates`);
  await page.getByRole('link', { name: 'Buat template', exact: true }).click();
  await page.getByLabel('Nama template', { exact: true }).fill('Template layanan');
  await page.getByLabel('Judul notifikasi', { exact: true }).fill('Informasi layanan');
  await page.getByLabel('Pesan', { exact: true }).fill('Jadwal layanan diperbarui.');
  await page.getByRole('button', { name: 'Simpan template', exact: true }).click();
  await page.getByRole('link', { name: 'Template layanan', exact: true }).click();
  await page.getByLabel('Pesan', { exact: true }).fill('Jadwal layanan terbaru.');
  await page.getByRole('button', { name: 'Simpan template', exact: true }).click();
  const row = page.getByRole('row').filter({ hasText: 'Template layanan' });
  await row.getByRole('link', { name: 'Gunakan', exact: true }).click();
  await expect(page.getByLabel('Pesan', { exact: true })).toHaveValue('Jadwal layanan terbaru.');
  await page.getByRole('button', { name: 'Penerima Uji #42' }).click();
  await page.getByRole('button', { name: 'Simpan draft', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Informasi layanan', exact: true })).toBeVisible();
  await expect(page.getByText('Draft', { exact: true })).toBeVisible();
});

test('manages a manual segment and its membership through the real BFF', async ({ page }) => {
  await login(page);
  await page.goto(`/apps/${app}/segments`);
  await page.getByRole('link', { name: 'Buat segment', exact: true }).click();
  await page.getByLabel('Nama segment', { exact: true }).fill('Pilot internal');
  await page.getByLabel('Deskripsi', { exact: true }).fill('Pengguna uji internal');
  await page.getByRole('button', { name: 'Simpan segment', exact: true }).click();
  await page.getByRole('button', { name: 'Tambah anggota', exact: true }).click();
  await page.getByRole('button', { name: 'Tambahkan Penerima Uji', exact: true }).click();
  await expect(
    page.getByRole('button', { name: 'Keluarkan Penerima Uji', exact: true }),
  ).toBeVisible();
  await page.screenshot({ path: 'test-results/dashboard-segment.png', fullPage: true });
  await page.getByLabel('Deskripsi', { exact: true }).fill('Anggota diperbarui');
  await page.getByRole('button', { name: 'Simpan segment', exact: true }).click();
  await expect(page.getByLabel('Deskripsi', { exact: true })).toHaveValue('Anggota diperbarui');
  page.on('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', { name: 'Keluarkan Penerima Uji', exact: true }).click();
  await expect(page.getByText('Belum ada anggota di segment ini.')).toBeVisible();
  await page.getByRole('button', { name: 'Hapus segment', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Segment', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Pilot internal', exact: true })).toHaveCount(0);
});

test('opens scoped device registrations and saves the staff profile', async ({ page }) => {
  await login(page);
  await page.goto(`/apps/${app}/audience`);
  await page.getByRole('link', { name: 'Penerima Uji', exact: true }).click();
  await expect(page.getByRole('cell', { name: 'ANDROID', exact: true })).toBeVisible();
  await page.getByRole('link', { name: 'Pengaturan', exact: true }).click();
  await page.getByLabel('Nama', { exact: true }).fill('Staf Operasional');
  await page.getByRole('button', { name: 'Simpan profil', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Profil berhasil diperbarui.');
  await expect(page.getByText('Staf Operasional', { exact: true })).toBeVisible();
});

test('mobile navigation contains focus, closes with Escape, and restores focus', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await login(page);
  await page.goto(`/apps/${app}/segments`);
  const opener = page.getByRole('button', { name: 'Buka navigasi', exact: true });
  await opener.click();
  await expect(
    page.getByRole('button', { name: 'Tutup navigasi', exact: true }).last(),
  ).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(opener).toBeFocused();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

test('accepted fixture campaign immediately removes draft actions and refreshes list state', async ({
  page,
}) => {
  await login(page);
  await page.goto(`/apps/${app}/campaigns/new`);
  await page.getByLabel('Judul notifikasi', { exact: true }).fill('Fixture acceptance');
  await page
    .getByLabel('Pesan', { exact: true })
    .fill('Isolated fixture, never sent to a provider.');
  await page.getByRole('button', { name: 'Penerima Uji #42' }).click();
  await page.getByRole('button', { name: 'Simpan draft', exact: true }).click();
  await page.getByRole('button', { name: 'Tinjau pengiriman', exact: true }).click();
  await page.getByRole('button', { name: 'Konfirmasi kirim', exact: true }).click();
  await expect(page.getByText('Dalam antrean', { exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Edit draft', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Tinjau pengiriman', exact: true })).toHaveCount(0);
  await page.getByRole('link', { name: 'Semua campaign', exact: true }).click();
  await expect(page.getByRole('row').filter({ hasText: 'Fixture acceptance' })).toContainText(
    'Dalam antrean',
  );
});

test('uncertain campaign acceptance blocks a second submission in the open dialog', async ({
  page,
}) => {
  await login(page);
  await page.goto(`/apps/${app}/campaigns/9007199254740993`);
  let submissions = 0;
  await page.route(`**/bff/api/apps/${app}/campaigns/9007199254740993/send`, async (route) => {
    submissions++;
    await route.abort('failed');
  });
  await page.getByRole('button', { name: 'Tinjau pengiriman', exact: true }).click();
  const confirm = page.getByRole('button', { name: 'Konfirmasi kirim', exact: true });
  await confirm.click();
  await expect(confirm).toBeDisabled();
  await page.keyboard.press('Enter');
  expect(submissions).toBe(1);
  await expect(page.getByRole('dialog')).toContainText('Hasil permintaan belum diketahui');
});

test('campaign polling stops after a terminal status', async ({ page }) => {
  await login(page);
  let reads = 0;
  await page.route(`**/bff/api/apps/${app}/campaigns/9007199254740993`, async (route) => {
    const response = await route.fetch();
    const payload = await response.json();
    reads++;
    payload.data.status = reads === 1 ? 'PROCESSING' : 'SENT';
    await route.fulfill({ response, json: payload });
  });
  await page.goto(`/apps/${app}/campaigns/9007199254740993`);
  await expect.poll(() => reads, { timeout: 10000 }).toBe(2);
  await page.waitForTimeout(6000);
  expect(reads).toBe(2);
});

test('a delayed template cannot overwrite an editable message', async ({ page }) => {
  await login(page);
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(`**/bff/api/apps/${app}/templates/3`, async (route) => {
    await gate;
    await route.continue();
  });
  await page.goto(`/apps/${app}/campaigns/new?template=3`);
  await expect(page.getByLabel('Judul notifikasi', { exact: true })).toBeDisabled();
  await expect(page.getByLabel('Pesan', { exact: true })).toBeDisabled();
  release();
  await expect(page.getByLabel('Pesan', { exact: true })).toHaveValue(
    'Buka aplikasi untuk melihat informasi.',
  );
  await expect(page.getByLabel('Pesan', { exact: true })).toBeEnabled();
});

test('management screens fit a narrow viewport', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await login(page);
  for (const route of [
    'overview',
    'settings',
    'templates',
    'segments/7',
    'audience',
    'audience/42',
    'logs',
  ]) {
    await page.goto(`/apps/${app}/${route}`);
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
      route,
    ).toBe(true);
  }
  await page.screenshot({ path: 'test-results/dashboard-mobile-logs.png', fullPage: true });
});

test('closed mobile navigation is skipped by keyboard and resets on desktop resize', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await login(page);
  await page.goto(`/apps/${app}/campaigns`);
  const opener = page.getByRole('button', { name: 'Buka navigasi', exact: true });
  await expect(page.getByRole('button', { name: /sidebar/ })).toHaveCount(0);
  await page.getByRole('link', { name: 'Lewati ke konten' }).focus();
  await page.keyboard.press('Tab');
  await expect(opener).toBeFocused();
  await opener.click();
  await page.getByRole('link', { name: 'Template', exact: true }).click();
  await expect(opener).toBeFocused();
  await expect(page.getByRole('navigation', { name: 'Navigasi utama' })).toHaveCount(0);
  await opener.click();
  await page.setViewportSize({ width: 1440, height: 1000 });
  await expect(page.locator('.workspace-body')).not.toHaveAttribute('inert');
  await expect(page.getByRole('heading', { name: 'Template', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Sembunyikan sidebar' })).toBeVisible();
});

test('dark login themes the full panel without a card and supports password visibility', async ({
  page,
}) => {
  await page.addInitScript(() => localStorage.setItem('hello.theme', 'dark'));
  await page.goto('/login');
  await expect(page.getByRole('heading', { name: 'Selamat datang kembali.' })).toBeVisible();
  await expect(page.locator('.login-form-area')).toHaveCSS('background-color', 'rgb(20, 28, 24)');
  await expect(page.locator('.login-form-wrap')).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)');
  await page.getByLabel('Kata sandi', { exact: true }).fill('fixture-only');
  await page.getByRole('button', { name: 'Tampilkan kata sandi' }).click();
  await expect(page.locator('input[name="password"]')).toHaveAttribute('type', 'text');
  await page.getByRole('button', { name: 'Sembunyikan kata sandi' }).click();
  await expect(page.locator('input[name="password"]')).toHaveAttribute('type', 'password');
  await page.screenshot({ path: 'test-results/dashboard-login-dark.png', fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: 'test-results/dashboard-login-mobile-dark.png', fullPage: true });
  await page.getByLabel('Email', { exact: true }).fill('staff@example.invalid');
  await page.getByLabel('Kata sandi', { exact: true }).fill('wrong');
  await page.getByRole('button', { name: 'Masuk ke dashboard' }).click();
  await expect(page.getByRole('alert')).toContainText('Email atau kata sandi tidak sesuai.');
});

test('dark editor keeps readable enabled recipients and notification content', async ({ page }) => {
  await login(page);
  await page.goto(`/apps/${app}/settings`);
  await page.getByLabel('Tema', { exact: true }).selectOption('dark');
  await page.goto(`/apps/${app}/campaigns/new`);
  await expect(page.getByRole('button', { name: 'Penerima Uji #42' })).toBeEnabled();
  const contrast = await page.evaluate(() => {
    const rgb = (value: string) =>
      value
        .match(/[\d.]+/g)!
        .slice(0, 3)
        .map(Number);
    const luminance = (value: number[]) =>
      value
        .map((channel) => {
          channel /= 255;
          return channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4;
        })
        .reduce((sum, channel, index) => sum + channel * [0.2126, 0.7152, 0.0722][index], 0);
    return ['.audience-option', '.notification-preview > p', '.back-link'].map((selector) => {
      const element = document.querySelector<HTMLElement>(selector)!;
      let parent: HTMLElement | null = element;
      while (parent && getComputedStyle(parent).backgroundColor === 'rgba(0, 0, 0, 0)')
        parent = parent.parentElement;
      const foreground = luminance(rgb(getComputedStyle(element).color));
      const background = luminance(rgb(getComputedStyle(parent!).backgroundColor));
      return {
        selector,
        ratio:
          (Math.max(foreground, background) + 0.05) / (Math.min(foreground, background) + 0.05),
      };
    });
  });
  for (const item of contrast) expect(item.ratio, item.selector).toBeGreaterThanOrEqual(4.5);
  await page.screenshot({ path: 'test-results/dashboard-editor-dark.png', fullPage: true });
});

test('draft validation identifies fields, restores focus, and preserves field geometry', async ({
  page,
}) => {
  await login(page);
  await page.goto(`/apps/${app}/campaigns/new`);
  const title = page.getByLabel('Judul notifikasi', { exact: true });
  const body = page.getByLabel('Pesan', { exact: true });
  const before = await body.boundingBox();
  await page.getByRole('button', { name: 'Simpan draft', exact: true }).click();
  await expect(title).toBeFocused();
  await expect(title).toHaveAttribute('aria-invalid', 'true');
  await expect(title).toHaveAccessibleDescription(/Judul wajib diisi/);
  await expect(body).toHaveAttribute('aria-invalid', 'true');
  const after = await body.boundingBox();
  expect(Math.abs(after!.y + (await page.evaluate(() => scrollY)) - before!.y)).toBeLessThanOrEqual(
    2,
  );
  await title.fill('Judul valid');
  await body.fill('Pesan valid');
  await page.getByRole('button', { name: 'Simpan draft', exact: true }).click();
  const broadcast = page.getByLabel('Semua penerima dalam aplikasi', { exact: true });
  await expect(broadcast).toBeFocused();
  await expect(broadcast).toHaveAccessibleDescription('Pilih sedikitnya satu penerima.');
  await page.getByRole('button', { name: 'Penerima Uji #42' }).click();
  await expect(broadcast).toHaveAttribute('aria-invalid', 'false');
  await title.fill('é'.repeat(128));
  await page.getByRole('button', { name: 'Simpan draft', exact: true }).click();
  await expect(title).toBeFocused();
  await expect(title).toHaveAccessibleDescription(/Judul maksimal 255 byte/);
});

test('recipient names survive broadcast toggles, draft editing, and send review', async ({
  page,
}) => {
  await login(page);
  await page.goto(`/apps/${app}/campaigns/new`);
  await page.getByLabel('Judul notifikasi', { exact: true }).fill('Named recipients');
  await page.getByLabel('Pesan', { exact: true }).fill('Isolated draft with named recipients.');
  await page.getByRole('button', { name: 'Penerima Uji #42' }).click();
  await page.getByRole('combobox', { name: 'Jenis penerima', exact: true }).selectOption('SEGMENT');
  await page.getByRole('button', { name: 'Segmen Uji #7' }).click();
  const selected = page.locator('.selected-targets');
  await expect(selected).toContainText('Penerima Uji');
  await expect(selected).toContainText('Segmen Uji');
  const broadcast = page.getByLabel('Semua penerima dalam aplikasi', { exact: true });
  await broadcast.check();
  await broadcast.uncheck();
  await expect(selected).toContainText('Penerima Uji');
  await expect(selected).toContainText('Segmen Uji');
  await page.getByRole('button', { name: 'Simpan draft', exact: true }).click();
  await expect(page.locator('.target-list')).toContainText('Penerima Uji');
  await expect(page.locator('.target-list')).toContainText('Segmen Uji');
  await page.getByRole('link', { name: 'Edit draft', exact: true }).click();
  await expect(selected).toContainText('Penerima Uji');
  await expect(selected).toContainText('Segmen Uji');
  await page.getByRole('button', { name: 'Simpan draft', exact: true }).click();
  await page.getByRole('button', { name: 'Tinjau pengiriman', exact: true }).click();
  await expect(page.getByRole('dialog')).toContainText('Penerima Uji');
  await expect(page.getByRole('dialog')).toContainText('Segmen Uji');
  await expect(page.getByRole('dialog')).not.toContainText('INDIVIDUAL');
});

test('mobile editor starts with fields and keeps save reachable with a collapsible preview', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await login(page);
  await page.goto(`/apps/${app}/campaigns/new`);
  const title = page.getByLabel('Judul notifikasi', { exact: true });
  await expect(title).toBeInViewport();
  await expect(page.locator('.editor-preview')).not.toHaveAttribute('open');
  const save = page.getByRole('button', { name: 'Simpan draft', exact: true });
  await expect(save).toBeInViewport();
  await title.fill('Pesan mobile');
  await page.getByLabel('Pesan', { exact: true }).fill('Isi pratinjau mobile.');
  await page.locator('.editor-preview > summary').click();
  await expect(page.locator('.notification-preview')).toContainText('Isi pratinjau mobile.');
  await expect(save).toBeInViewport();
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: 'test-results/dashboard-editor-mobile.png', fullPage: true });
});

test('campaign loading and empty states keep the footer stable and ID lookup secondary', async ({
  page,
}) => {
  await login(page);
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(`**/bff/api/apps/${app}/campaigns?*`, async (route) => {
    await gate;
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ success: true, data: { items: [], next_after_id: null } }),
    });
  });
  await page.goto(`/apps/${app}/campaigns`);
  await expect(page.getByRole('status')).toContainText('Memuat data');
  const before = await page.locator('.page-footnote').boundingBox();
  release();
  await expect(page.getByRole('heading', { name: 'Mulai dari pesan pertama.' })).toBeVisible();
  const after = await page.locator('.page-footnote').boundingBox();
  expect(Math.abs(after!.y - before!.y)).toBeLessThanOrEqual(1);
  await expect(page.getByLabel('Buka berdasarkan ID campaign')).not.toBeVisible();
  await page.locator('.campaign-shortcut > summary').click();
  await expect(page.getByLabel('Buka berdasarkan ID campaign')).toBeVisible();
  await page.getByLabel('Buka berdasarkan ID campaign').fill('9007199254740993');
  await page.getByRole('button', { name: 'Buka', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'Pengingat jadwal layanan', level: 1, exact: true }),
  ).toBeVisible();
});
