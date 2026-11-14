import { test, expect, type Page } from '@playwright/test';

const app = '11111111-1111-4111-8111-111111111111';
const largeStaffID = '9007199254740993';

async function login(page: Page) {
  await page.goto('/login');
  await page.getByLabel('Email', { exact: true }).fill('staff@example.invalid');
  await page.getByLabel('Kata sandi', { exact: true }).fill('fixture-only');
  await page.getByRole('button', { name: 'Masuk ke dashboard' }).click();
  await expect(page.getByRole('heading', { name: 'Pilih aplikasi' })).toBeVisible();
}

test('staff admin creates, edits, changes access, resets a password, and deletes an account', async ({
  page,
}) => {
  await login(page);
  await page.goto('/apps/admin/staff');
  const largeAccount = page.getByRole('link', { name: 'Akun ID besar', exact: true });
  await expect(largeAccount).toHaveAttribute('href', `/apps/admin/staff/${largeStaffID}`);
  await expect(page.getByRole('row').filter({ has: largeAccount })).toContainText(
    `#${largeStaffID}`,
  );

  await page.getByRole('link', { name: 'Tambah staf', exact: true }).click();
  await page.getByLabel('Nama', { exact: true }).fill('Operator Uji');
  await page.getByLabel('Email masuk', { exact: true }).fill('operator-test@example.invalid');
  await page.getByLabel('Peran', { exact: true }).selectOption('OPERATOR');
  await page.getByLabel('Kata sandi awal', { exact: true }).fill('initial-pass-8');
  await page.getByRole('button', { name: 'Buat akun staf', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Edit staf', exact: true })).toBeVisible();

  await page.getByLabel('Nama', { exact: true }).fill('Operator Diperbarui');
  await page.getByLabel('Email masuk', { exact: true }).fill('operator-updated@example.invalid');
  await page.getByLabel('Peran', { exact: true }).selectOption('ADMIN');
  await page.getByRole('button', { name: 'Simpan perubahan', exact: true }).click();
  await expect(page.getByLabel('Nama', { exact: true })).toHaveValue('Operator Diperbarui');

  await page.getByRole('button', { name: 'Nonaktifkan akun', exact: true }).click();
  await expect(page.getByText('Nonaktif', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Aktifkan akun', exact: true }).click();
  await expect(page.getByText('Aktif', { exact: true })).toBeVisible();

  await page
    .getByLabel('Kata sandi Anda saat ini', { exact: true })
    .fill('incorrect-actor-password');
  await page.getByLabel('Kata sandi baru staf', { exact: true }).fill('replacement-pass-8');
  await page.getByRole('button', { name: 'Atur kata sandi', exact: true }).click();
  await expect(page.getByRole('alert')).toBeVisible();
  expect((await (await page.request.get('/bff/session')).json()).authenticated).toBe(true);
  await page.getByLabel('Kata sandi Anda saat ini', { exact: true }).fill('fixture-only');
  await page.getByRole('button', { name: 'Atur kata sandi', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Kata sandi staf berhasil diperbarui.');

  page.once('dialog', async (dialog) => {
    expect(dialog.type()).toBe('confirm');
    expect(dialog.message()).toContain('Operator Diperbarui');
    expect(dialog.message()).toContain('operator-updated@example.invalid');
    await dialog.accept();
  });
  await page.getByRole('button', { name: 'Hapus akun staf', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Kelola staf', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Operator Diperbarui', exact: true })).toHaveCount(0);
});

test('registry uses strong ETags and requires explicit review after a stale rename', async ({
  page,
}) => {
  await login(page);
  await page.goto('/apps/admin/applications');
  await expect(page.getByRole('heading', { name: 'Registry aplikasi', exact: true })).toBeVisible();
  await page.getByLabel('Kunci aplikasi', { exact: true }).fill('e2e-registry');
  await page.getByLabel('Nama aplikasi', { exact: true }).fill('Aplikasi E2E');
  await page.getByRole('button', { name: 'Daftarkan aplikasi', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Aplikasi E2E', exact: true })).toBeVisible();
  await expect(page.getByText('Disiapkan', { exact: true })).toBeVisible();
  await expect(page.getByText('Belum ada pemberian akses.', { exact: true })).toBeVisible();

  const id = new URL(page.url()).pathname.split('/').at(-1)!;
  const session = await (await page.request.get('/bff/session')).json();
  const origin = 'http://127.0.0.1:4173';
  const missingPrecondition = await page.request.patch(`/bff/api/admin/apps/${id}`, {
    headers: { Origin: origin, 'X-CSRF-Token': session.csrfToken },
    data: { name: 'Seharusnya ditolak' },
  });
  expect(missingPrecondition.status()).toBe(428);
  const stalePrecondition = await page.request.patch(`/bff/api/admin/apps/${id}`, {
    headers: {
      Origin: origin,
      'X-CSRF-Token': session.csrfToken,
      'If-Match': `"app:${id}:999"`,
    },
    data: { name: 'Seharusnya ditolak' },
  });
  expect(stalePrecondition.status()).toBe(412);
  await page.getByLabel('Nama aplikasi', { exact: true }).fill('Aplikasi E2E diperbarui');
  await page.getByRole('button', { name: 'Simpan nama', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'Aplikasi E2E diperbarui', exact: true }),
  ).toBeVisible();

  let staleAttempts = 0;
  await page.route(`**/bff/api/admin/apps/${id}`, async (route) => {
    if (route.request().method() === 'PATCH') {
      staleAttempts++;
      await route.fulfill({
        status: 412,
        contentType: 'application/json',
        body: JSON.stringify({ success: false, message: 'application changed; reload and review' }),
      });
    } else {
      await route.continue();
    }
  });
  await page.getByLabel('Nama aplikasi', { exact: true }).fill('Nama yang perlu ditinjau');
  await page.getByRole('button', { name: 'Simpan nama', exact: true }).click();
  await expect(page.getByText(/Aplikasi berubah sejak dibaca/)).toBeVisible();
  expect(staleAttempts).toBe(1);
  const saveName = page.getByRole('button', { name: 'Simpan nama', exact: true });
  await expect(saveName).toBeDisabled();
  expect(staleAttempts).toBe(1);
  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', { name: 'Muat ulang aplikasi', exact: true }).click();
  await expect(page.getByLabel('Nama aplikasi', { exact: true })).toHaveValue(
    'Aplikasi E2E diperbarui',
  );
  await page.unroute(`**/bff/api/admin/apps/${id}`);

  await page.getByLabel('ID staf', { exact: true }).fill('1');
  await page.getByRole('button', { name: 'Periksa akses', exact: true }).click();
  await expect(page.getByText(/Belum ada baris akses/)).toBeVisible();
  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', { name: 'Berikan atau pulihkan akses', exact: true }).click();
  await expect(page.getByText('Status akses:')).toBeVisible();
  await expect(page.locator('.grant-review').getByText('Aktif', { exact: true })).toBeVisible();
  const missingGrantPrecondition = await page.request.delete(
    `/bff/api/admin/apps/${id}/staff-grants/1`,
    { headers: { Origin: origin, 'X-CSRF-Token': session.csrfToken } },
  );
  expect(missingGrantPrecondition.status()).toBe(428);
  const staleGrantPrecondition = await page.request.delete(
    `/bff/api/admin/apps/${id}/staff-grants/1`,
    {
      headers: {
        Origin: origin,
        'X-CSRF-Token': session.csrfToken,
        'If-Match': `"grant:${id}:1:999"`,
      },
    },
  );
  expect(staleGrantPrecondition.status()).toBe(412);

  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', { name: 'Cabut akses', exact: true }).click();
  await expect(page.locator('.grant-review').getByText('Nonaktif', { exact: true })).toBeVisible();
  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', { name: 'Berikan atau pulihkan akses', exact: true }).click();
  await expect(page.locator('.grant-review').getByText('Aktif', { exact: true })).toBeVisible();
  await expect(page.getByRole('cell', { name: '1', exact: true })).toBeVisible();
});

test('self-password rejects an incorrect current password without clearing the session, then success ends it', async ({
  page,
}) => {
  await login(page);
  await page.goto('/apps/settings');
  await page.getByLabel('Kata sandi saat ini', { exact: true }).fill('incorrect-current');
  await page.getByLabel('Kata sandi baru', { exact: true }).fill('replacement-pass-8');
  await page.getByLabel('Konfirmasi kata sandi baru', { exact: true }).fill('replacement-pass-8');
  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', { name: 'Ganti kata sandi', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('Kata sandi saat ini tidak sesuai.');
  expect((await (await page.request.get('/bff/session')).json()).authenticated).toBe(true);

  await page.getByLabel('Kata sandi saat ini', { exact: true }).fill('fixture-only');
  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', { name: 'Ganti kata sandi', exact: true }).click();
  await expect(page).toHaveURL(/\/login$/);
  await expect(page.getByRole('heading', { name: 'Selamat datang kembali.' })).toBeVisible();
  expect((await (await page.request.get('/bff/session')).json()).authenticated).toBe(false);
});

test('an unknown grant write result blocks resubmission instead of retrying', async ({ page }) => {
  await login(page);
  await page.goto(`/apps/admin/applications/${app}`);
  await page.getByLabel('ID staf', { exact: true }).fill('1');
  await page.getByRole('button', { name: 'Periksa akses', exact: true }).click();
  await expect(page.getByText(/Belum ada baris akses/)).toBeVisible();
  let submissions = 0;
  await page.route(`**/bff/api/admin/apps/${app}/staff-grants/1`, async (route) => {
    if (route.request().method() === 'PUT') {
      submissions++;
      await route.abort('failed');
    } else {
      await route.continue();
    }
  });
  page.once('dialog', (dialog) => dialog.accept());
  const enable = page.getByRole('button', { name: 'Berikan atau pulihkan akses', exact: true });
  await enable.click();
  await expect(enable).toBeDisabled();
  await expect(page.getByText(/Hasil perubahan belum dapat dipastikan/).first()).toBeVisible();
  await page.keyboard.press('Enter');
  expect(submissions).toBe(1);
});

test('staff self controls stay guarded for the active profile', async ({ page }) => {
  await login(page);
  await page.goto('/apps/admin/staff/1');
  await expect(page.getByRole('heading', { name: 'Edit staf', exact: true })).toBeVisible();
  await expect(page.getByLabel('Peran', { exact: true })).toBeDisabled();
  await expect(page.getByRole('button', { name: /Nonaktifkan akun/ })).toBeDisabled();
  await expect(page.getByRole('button', { name: 'Hapus akun staf', exact: true })).toBeDisabled();
  await expect(
    page.getByText('Peran akun Anda tidak dapat diubah dari halaman ini.'),
  ).toBeVisible();
  await expect(page.getByText('Untuk mengubah kata sandi sendiri', { exact: false })).toBeVisible();
});

test('scoped audit and upload views show only supplied fields and preserve 64-bit row IDs', async ({
  page,
}) => {
  await login(page);
  await page.goto(`/apps/${app}/audit`);
  await expect(page.getByRole('cell', { name: 'APPLICATION_UPDATED' })).toBeVisible();
  await expect(page.getByText(`Staff #1`, { exact: true })).toBeVisible();

  await page.goto(`/apps/${app}/uploads`);
  await page.getByRole('link', { name: 'Batch #17', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Batch #17', exact: true })).toBeVisible();
  await expect(page.getByText(`#${largeStaffID}`, { exact: true })).toBeVisible();
  await expect(page.getByText('Tidak valid', { exact: true })).toBeVisible();
  await expect(page.getByText('alasan penolakan', { exact: false })).toBeVisible();

  const unsupported = await page.request.get(`/bff/api/apps/${app}/uploads/17`);
  expect(unsupported.status()).toBe(404);
});

test('forbidden admin data remains visible and admin screens fit a 320px viewport', async ({
  page,
}) => {
  await login(page);
  await page.route('**/bff/api/admin/apps*', (route) =>
    route.fulfill({
      status: 403,
      contentType: 'application/json',
      body: JSON.stringify({ success: false, message: 'forbidden' }),
    }),
  );
  await page.goto('/apps/admin/applications');
  await expect(page.getByRole('alert')).toContainText('tidak memiliki izin');
  await page.unroute('**/bff/api/admin/apps*');

  await page.setViewportSize({ width: 1440, height: 900 });
  for (const theme of ['light', 'dark']) {
    await page.evaluate((value) => localStorage.setItem('hello.theme', value), theme);
    await page.goto('/apps/admin/staff');
    await expect(page.getByRole('heading', { name: 'Kelola staf', exact: true })).toBeVisible();
    await page.screenshot({ path: `/tmp/hello-admin-ui/${theme}-staff.png`, fullPage: true });
    await page.goto(`/apps/admin/applications/${app}`);
    await expect(page.getByRole('heading', { name: 'Aplikasi Uji', exact: true })).toBeVisible();
    await page.screenshot({
      path: `/tmp/hello-admin-ui/${theme}-registry-detail.png`,
      fullPage: true,
    });
  }

  await page.setViewportSize({ width: 320, height: 740 });
  await page.goto('/apps/admin/staff');
  await page.screenshot({ path: '/tmp/hello-admin-ui/mobile-320-staff.png', fullPage: true });
  await page.goto(`/apps/admin/applications/${app}`);
  await page.screenshot({
    path: '/tmp/hello-admin-ui/mobile-320-registry-detail.png',
    fullPage: true,
  });
  for (const route of [
    '/apps/admin/staff',
    '/apps/admin/applications',
    `/apps/${app}/uploads`,
    `/apps/${app}/audit`,
  ]) {
    await page.goto(route);
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
    const overflow = await page.evaluate(() => {
      const offenders = [...document.querySelectorAll<HTMLElement>('body *')]
        .map((element) => {
          const rect = element.getBoundingClientRect();
          const style = getComputedStyle(element);
          let ancestor: HTMLElement | null = element.parentElement;
          let nearestOverflow: string | null = null;
          while (ancestor && ancestor !== document.body) {
            const ancestorStyle = getComputedStyle(ancestor);
            if (
              /(auto|scroll|hidden|clip)/.test(
                `${ancestorStyle.overflowX} ${ancestorStyle.overflowY}`,
              )
            ) {
              nearestOverflow = `${ancestor.tagName.toLowerCase()}.${String(ancestor.className)}`;
              break;
            }
            ancestor = ancestor.parentElement;
          }
          return {
            tag: element.tagName.toLowerCase(),
            className: String(element.className),
            left: Math.round(rect.left),
            right: Math.round(rect.right),
            position: style.position,
            nearestOverflow,
          };
        })
        .filter((element) => element.right > innerWidth + 1)
        .sort((left, right) => right.right - left.right)
        .slice(0, 8);
      return {
        scrollWidth: document.documentElement.scrollWidth,
        innerWidth,
        offenders,
      };
    });
    expect(
      overflow.scrollWidth <= overflow.innerWidth,
      `${route}: ${JSON.stringify(overflow)}`,
    ).toBe(true);
  }
});
