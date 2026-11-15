// Exercise the real local dashboard with synthetic credentials; never use staging.
import { createRequire } from 'node:module';
import { readFileSync, mkdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const require = createRequire(new URL('../../web/dashboard/package.json', import.meta.url));
const { chromium, expect } = require('@playwright/test');
const root = new URL('../../', import.meta.url);
const settings = JSON.parse(readFileSync(new URL('tmp/local-dev/config/settings.json', root)));
const fixture = JSON.parse(readFileSync(new URL('tmp/local-dev/config/generated/onboarding.json', root)));
const accountPath = fileURLToPath(new URL('tmp/local-dev/tls/service-account.json', root));
const account = JSON.parse(readFileSync(accountPath));
if (account.project_id !== 'integration-fixture' || account.client_email !== 'fixture@integration-fixture.iam.gserviceaccount.com') {
  throw new Error('Refusing non-fixture credentials');
}

const origin = 'http://127.0.0.1:5173';
const browser = await chromium.launch({ headless: true });
try {
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  const failures = [];
  page.on('pageerror', () => failures.push('browser runtime error'));
  page.on('request', (request) => {
    if (request.postData()?.includes('"private_key"') && !/^http:\/\/127\.0\.0\.1:5173\/bff\/api\/admin\/apps\/[0-9a-f-]+\/onboarding\/fcm$/.test(request.url())) {
      failures.push('unexpected credential destination');
    }
  });
  await page.goto(origin + '/login');
  await page.getByLabel('Email', { exact: true }).fill(settings.email);
  await page.getByLabel('Kata sandi', { exact: true }).fill(settings.password);
  await page.getByRole('button', { name: 'Masuk ke dashboard' }).click();
  await expect(page.getByRole('heading', { name: 'Pilih aplikasi' })).toBeVisible();
  const list = await page.request.get(origin + '/bff/api/admin/apps?key=local-browser-onboarding&limit=20');
  if (list.status() !== 200) throw new Error('Local registry unavailable');
  let appID = (await list.json()).data.items.find((item) => item.key === 'local-browser-onboarding')?.id;
  if (!appID) {
    await page.goto(origin + '/apps/admin/applications');
    await page.getByLabel('Kunci aplikasi', { exact: true }).fill('local-browser-onboarding');
    await page.getByLabel('Nama aplikasi', { exact: true }).fill('Uji onboarding dashboard');
    await page.getByRole('button', { name: 'Daftarkan aplikasi', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Uji onboarding dashboard', exact: true })).toBeVisible();
    appID = new URL(page.url()).pathname.split('/').at(-1);
  } else {
    await page.goto(origin + `/apps/admin/applications/${appID}`);
  }
  if (!/^[0-9a-f-]{36}$/.test(appID ?? '')) throw new Error('Invalid local test app');
  await page.getByRole('link', { name: 'Konfigurasi notifikasi', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Konfigurasi notifikasi', exact: true })).toBeVisible();
  const editProfile = page.getByRole('button', { name: 'Ubah profil pendaftaran', exact: true });
  await expect(page.getByRole('heading', { name: 'Status aplikasi', exact: true })).toBeVisible();
  if (await editProfile.isVisible()) await editProfile.click();
  await page.getByRole('combobox', { name: 'Profil pendaftaran', exact: true }).selectOption(fixture.profile_id);
  await page.getByLabel('Android', { exact: true }).check();
  await page.getByLabel('ID aplikasi Android', { exact: true }).fill(fixture.application_identifier);
  await page.getByRole('button', { name: 'Simpan profil', exact: true }).click();
  await expect(page.getByText('Profil pendaftaran disimpan.', { exact: true })).toBeVisible();
  const environment = page.getByRole('combobox', { name: 'Lingkungan', exact: true });
  if (await environment.isEnabled()) await environment.selectOption('staging');
  else await expect(environment).toHaveValue('staging');
  await page.getByLabel('Berkas kredensial FCM', { exact: false }).setInputFiles(accountPath);
  await page.getByRole('button', { name: 'Simpan kredensial', exact: true }).click();
  await expect(page.getByText('Kredensial tersimpan sebagai kandidat.', { exact: true })).toBeVisible();
  await expect(page.getByLabel('Berkas kredensial FCM', { exact: false })).toHaveValue('');
  const verify = page.getByRole('button', { name: 'Verifikasi FCM', exact: true });
  await expect(verify).toBeEnabled({ timeout: 30_000 });
  await verify.click();
  await expect(page.getByText('OAuth berhasil. Izin mengirim notifikasi dan penerimaan di perangkat belum diperiksa.', { exact: true })).toBeVisible({ timeout: 45_000 });
  const ready = await page.request.get(origin + `/bff/api/admin/apps/${appID}/onboarding`);
  const data = (await ready.json()).data;
  if (ready.status() !== 200 || data.application_status !== 'PROVISIONING' || data.dispatch_mode !== 'HELD' || data.current_configuration_id !== null) {
    throw new Error('Setup incorrectly activated enrollment or dispatch');
  }
  if ((await page.locator('body').innerText()).includes('BEGIN PRIVATE KEY')) throw new Error('Credential material rendered');
  const storage = await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }));
  if (storage.includes('private_key') || storage.includes('BEGIN PRIVATE KEY')) throw new Error('Credential material persisted');
  const output = fileURLToPath(new URL('tmp/local-dev/screenshots/', root));
  mkdirSync(output, { recursive: true });
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: output + '/onboarding-desktop.png', fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.evaluate(() => window.scrollTo(0, 0));
  if (!(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth))) throw new Error('Mobile page overflows');
  await page.screenshot({ path: output + '/onboarding-mobile.png', fullPage: true });
  await page.evaluate(() => localStorage.setItem('hello.theme', 'dark'));
  await page.reload();
  await expect(page.getByRole('heading', { name: 'Konfigurasi notifikasi', exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Status aplikasi', exact: true })).toBeVisible();
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: output + '/onboarding-dark-mobile.png', fullPage: true });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole('button', { name: 'Keluar', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Masuk ke dashboard' })).toBeVisible();
  if (failures.length) throw new Error('Local browser validation failed');
  console.log('Real local browser: app creation, profile, credential upload, scoped OAuth verifier and held dispatch passed.');
  console.log(`Screenshots: ${output}`);
} finally {
  await browser.close();
}
