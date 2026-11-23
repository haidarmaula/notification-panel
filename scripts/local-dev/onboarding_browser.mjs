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
  await page.locator('input[name="email"]').fill(settings.email);
  await page.locator('input[name="password"]').fill(settings.password);
  const signIn = page.getByRole('button', { name: 'Sign in', exact: true });
  await signIn.click();
  await expect(page.getByRole('heading', { name: 'Choose application', exact: true })).toBeVisible();
  const list = await page.request.get(origin + '/bff/api/admin/apps?key=local-browser-onboarding&limit=20');
  if (list.status() !== 200) throw new Error('Local registry unavailable');
  let appID = (await list.json()).data.items.find((item) => item.key === 'local-browser-onboarding')?.id;
  if (!appID) {
    await page.goto(origin + '/apps/admin/applications');
    await page.getByRole('textbox', { name: /^Application key/ }).fill('local-browser-onboarding');
    await page.getByLabel('Application name', { exact: false }).fill('Uji onboarding dashboard');
    await page.getByRole('button', { name: 'Register application', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Uji onboarding dashboard', exact: true })).toBeVisible();
    appID = new URL(page.url()).pathname.split('/').at(-1);
  } else {
    await page.goto(origin + `/apps/admin/applications/${appID}`);
  }
  if (!/^[0-9a-f-]{36}$/.test(appID ?? '')) throw new Error('Invalid local test app');
  await page.getByRole('link', { name: 'Notification setup', exact: true }).click();
  const setupHeading = page.getByRole('heading', { name: 'Notification setup', exact: true });
  const statusHeading = page.getByRole('heading', { name: 'Application status', exact: true });
  await expect(setupHeading).toBeVisible();
  await expect(statusHeading).toBeVisible();
  const profile = page.getByRole('combobox', { name: 'Enrollment profile', exact: true });
  const editProfile = page.getByRole('button', { name: 'Change enrollment profile', exact: true });
  if (await editProfile.isVisible()) await editProfile.click();
  await profile.click();
  await page.getByRole('option', { name: 'Hello Local synthetic Android', exact: true }).click();
  await page.getByLabel('Android', { exact: true }).check();
  await page.getByLabel('Android application ID', { exact: true }).fill(fixture.application_identifier);
  await page.getByRole('button', { name: 'Save profile', exact: true }).click();
  await expect(page.getByText('Enrollment profile saved.', { exact: true })).toBeVisible();
  const environment = page.getByRole('combobox', { name: 'Environment', exact: true });
  if (await environment.isEnabled()) {
    await environment.click();
    await page.getByRole('option', { name: 'Staging', exact: true }).click();
  }
  await expect(environment).toContainText('Staging');
  const credential = page.locator('input[type="file"]');
  await credential.setInputFiles(accountPath);
  await page.getByRole('button', { name: 'Save credentials', exact: true }).click();
  await expect(page.getByText('Credentials saved as a candidate.', { exact: true })).toBeVisible();
  await expect(credential).toHaveValue('');
  const verify = page.getByRole('button', { name: 'Verify FCM', exact: true });
  await expect(verify).toBeEnabled({ timeout: 30_000 });
  await verify.click();
  await expect(page.getByText('OAuth succeeded. Delivery permissions and device receipt have not been checked.', { exact: true })).toBeVisible({ timeout: 45_000 });
  const ready = await page.request.get(origin + `/bff/api/admin/apps/${appID}/onboarding`);
  const data = (await ready.json()).data;
  if (ready.status() !== 200 || data.application_status !== 'PROVISIONING' || data.dispatch_mode !== 'HELD' || data.current_configuration_id !== null) {
    throw new Error('Setup incorrectly activated enrollment or dispatch');
  }
  if (data.enrollment_profile_id !== fixture.profile_id) throw new Error('Unexpected enrollment profile');
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
  await expect(setupHeading).toBeVisible();
  await expect(statusHeading).toBeVisible();
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: output + '/onboarding-dark-mobile.png', fullPage: true });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole('button', { name: 'Account menu', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Sign out', exact: true }).click();
  await expect(signIn).toBeVisible();
  if (failures.length) throw new Error('Local browser validation failed');
  console.log('Real local browser: app creation, profile, credential upload, scoped OAuth verifier and held dispatch passed.');
  console.log(`Screenshots: ${output}`);
} finally {
  await browser.close();
}
