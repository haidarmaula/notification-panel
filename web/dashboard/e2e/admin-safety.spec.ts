import { expect, test, type Page } from '@playwright/test';

const app = '11111111-1111-4111-8111-111111111111';

async function login(page: Page, waitForWorkspace = true) {
  await page.goto('/login');
  await page.getByLabel('Email', { exact: true }).fill('staff@example.invalid');
  await page.getByLabel('Kata sandi', { exact: true }).fill('fixture-only');
  await page.getByRole('button', { name: 'Masuk ke dashboard' }).click();
  if (waitForWorkspace) {
    await expect(page.getByRole('heading', { name: 'Pilih aplikasi' })).toBeVisible();
  } else {
    await expect
      .poll(async () => (await (await page.request.get('/bff/session')).json()).authenticated)
      .toBe(true);
  }
}

test('self controls stay disabled until current profile identity is verified', async ({ page }) => {
  let releaseProfile: () => void = () => {};
  let profileStarted: () => void = () => {};
  const profileGate = new Promise<void>((resolve) => {
    releaseProfile = resolve;
  });
  const profileRequest = new Promise<void>((resolve) => {
    profileStarted = resolve;
  });
  await page.route('**/bff/api/profile', async (route) => {
    const response = await route.fetch();
    profileStarted();
    await profileGate;
    await route.fulfill({ response });
  });
  let sensitiveWrites = 0;
  await page.route('**/bff/api/staff/1**', async (route) => {
    if (route.request().method() !== 'GET') {
      sensitiveWrites++;
      await route.fulfill({
        status: 403,
        contentType: 'application/json',
        body: JSON.stringify({ success: false, message: 'forbidden' }),
      });
      return;
    }
    await route.continue();
  });

  await login(page, false);
  await profileRequest;
  await page.goto('/apps/admin/staff/1');
  const enabledSensitiveControls = await page
    .locator('button, select, input[type="password"]')
    .evaluateAll((elements) =>
      elements
        .filter((element) => {
          const label = [element.id, element.getAttribute('aria-label'), element.textContent].join(
            ' ',
          );
          return (
            /staff-role|Nonaktifkan akun|Aktifkan akun|Hapus akun staf|Kata sandi|Atur kata sandi/i.test(
              label,
            ) && !element.matches(':disabled')
          );
        })
        .map((element) => element.id || element.getAttribute('aria-label') || element.textContent),
    );
  expect(enabledSensitiveControls).toEqual([]);
  expect(sensitiveWrites).toBe(0);

  releaseProfile();
  await expect(page.getByRole('heading', { name: 'Edit staf', exact: true })).toBeVisible();

  await page.goto('/apps/admin/staff');
  await expect(
    page.getByRole('button', { name: /Nonaktifkan akun Staf Pengujian, akun Anda/ }),
  ).toBeDisabled();

  await page.goto('/apps/admin/staff/1');
  await expect(page.getByLabel('Peran', { exact: true })).toBeDisabled();
  await expect(page.getByLabel('Nama', { exact: true })).toBeEnabled();
  await expect(page.getByLabel('Email masuk', { exact: true })).toBeEnabled();
  await expect(page.getByRole('button', { name: 'Nonaktifkan akun', exact: true })).toBeDisabled();
  await expect(page.getByRole('button', { name: 'Hapus akun staf', exact: true })).toBeDisabled();
  await expect(page.getByLabel('Kata sandi Anda saat ini', { exact: true })).toHaveCount(0);
  await expect(
    page.getByText('Peran akun Anda tidak dapat diubah dari halaman ini.', { exact: true }),
  ).toBeVisible();
  await expect(page.getByRole('link', { name: 'pengaturan akun' })).toHaveAttribute(
    'href',
    '/apps/settings',
  );
  await expect(page.getByLabel('Kata sandi Anda saat ini', { exact: true })).toHaveCount(0);
});

test('uncertain staff create outcome keeps inputs and blocks duplicate submission', async ({
  page,
}) => {
  await login(page);
  await page.goto('/apps/admin/staff/new');
  await page.getByLabel('Nama', { exact: true }).fill('Operator Tertahan');
  await page.getByLabel('Email masuk', { exact: true }).fill('pending@example.invalid');
  await page.getByLabel('Kata sandi awal', { exact: true }).fill('initial-pass-8');

  let submissions = 0;
  await page.route('**/bff/api/staff', async (route) => {
    if (route.request().method() === 'POST') {
      submissions++;
      await route.fulfill({
        status: 503,
        contentType: 'application/json',
        body: JSON.stringify({ success: false, message: 'temporarily unavailable' }),
      });
      return;
    }
    await route.continue();
  });

  const submit = page.getByRole('button', { name: 'Buat akun staf', exact: true });
  await submit.click();
  await expect(page.getByText(/Hasil perubahan belum dapat dipastikan/)).toBeVisible();
  await expect(submit).toBeDisabled();
  await expect(page.getByLabel('Nama', { exact: true })).toHaveValue('Operator Tertahan');
  await expect(page.getByLabel('Email masuk', { exact: true })).toHaveValue(
    'pending@example.invalid',
  );
  await expect(page.getByLabel('Kata sandi awal', { exact: true })).toHaveValue('initial-pass-8');

  await page.locator('form').first().dispatchEvent('submit', { bubbles: true, cancelable: true });
  await page.waitForTimeout(100);
  expect(submissions).toBe(1);
});

test('forbidden grant lookup does not enable a new grant', async ({ page }) => {
  await login(page);
  await page.goto(`/apps/admin/applications/${app}`);
  let grantRequests = 0;
  await page.route(`**/bff/api/admin/apps/${app}/staff-grants/1`, async (route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({
        status: 403,
        contentType: 'application/json',
        body: JSON.stringify({ success: false, message: 'forbidden' }),
      });
      return;
    }
    if (route.request().method() === 'PUT') grantRequests++;
    await route.continue();
  });

  await page.getByLabel('ID staf', { exact: true }).fill('1');
  await page.getByRole('button', { name: 'Periksa akses', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('tidak memiliki izin');
  const enableGrant = page.getByRole('button', {
    name: 'Berikan atau pulihkan akses',
    exact: true,
  });
  await expect(enableGrant).toBeDisabled();
  await expect(page.getByText('Belum ada baris akses untuk ID ini.')).toHaveCount(0);
  expect(grantRequests).toBe(0);
});
