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
test('shows held acceptance and preserves draft; statistics stay explicitly incomplete', async ({
  page,
}) => {
  await login(page);
  await page.goto(`/apps/${app}/campaigns/9007199254740993`);
  await page.getByRole('button', { name: 'Tinjau pengiriman' }).click();
  await expect(page.getByRole('dialog')).toContainText('INDIVIDUAL #42');
  await page.getByRole('button', { name: 'Konfirmasi kirim' }).click();
  await expect(page.getByRole('alert')).toContainText('Pengiriman sedang ditahan');
  await page.getByRole('button', { name: 'Kembali', exact: true }).click();
  await page.getByRole('link', { name: 'Riwayat dan hasil campaign' }).click();
  await expect(page.getByRole('heading', { name: 'Statistik belum lengkap' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Belum ada catatan pengiriman.' })).toBeVisible();
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
