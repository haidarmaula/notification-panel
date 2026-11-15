import { expect, test, type Page } from '@playwright/test';

const app = '11111111-1111-4111-8111-111111111111';

async function login(page: Page, waitForWorkspace = true) {
  await page.goto('/login');
  await page.getByRole('textbox', { name: /^Email/ }).fill('staff@example.invalid');
  await page.getByRole('textbox', { name: /^Password/ }).fill('fixture-only');
  const signIn = page.getByRole('button', { name: 'Sign in' });
  await expect(signIn).toBeEnabled();
  const loginResponse = page.waitForResponse('**/bff/login');
  await signIn.click();
  const response = await loginResponse;
  expect(response.status()).toBe(200);
  await response.finished();
  if (waitForWorkspace) {
    await expect(page.getByRole('heading', { name: 'Choose application' })).toBeVisible();
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
    .locator('button, [role="combobox"], input[type="password"]')
    .evaluateAll((elements) =>
      elements
        .filter((element) => {
          const label = [element.id, element.getAttribute('aria-label'), element.textContent].join(
            ' ',
          );
          return (
            (/Role|Deactivate|Activate|Delete staff account|password|Set password/i.test(label) ||
              element.getAttribute('role') === 'combobox') &&
            !element.matches(':disabled') &&
            element.getAttribute('aria-disabled') !== 'true'
          );
        })
        .map((element) => element.id || element.getAttribute('aria-label') || element.textContent),
    );
  expect(enabledSensitiveControls).toEqual([]);
  expect(sensitiveWrites).toBe(0);

  releaseProfile();
  await expect(
    page.getByRole('heading', { name: 'Edit staff account', exact: true }),
  ).toBeVisible();

  await page.goto('/apps/admin/staff');
  await expect(
    page.getByRole('button', { name: /Deactivate Staf Pengujian, your account/ }),
  ).toBeDisabled();

  await page.goto('/apps/admin/staff/1');
  await expect(page.getByRole('combobox', { name: 'Role', exact: false })).toBeDisabled();
  await expect(page.getByLabel('Name', { exact: false })).toBeEnabled();
  await expect(page.getByLabel('Sign-in email', { exact: false })).toBeEnabled();
  await expect(
    page.getByRole('button', { name: 'Deactivate account', exact: true }),
  ).toBeDisabled();
  await expect(
    page.getByRole('button', { name: 'Delete staff account', exact: true }),
  ).toBeDisabled();
  await expect(page.getByLabel('Your current password', { exact: false })).toHaveCount(0);
  await expect(
    page.getByText('Your account role cannot be changed here.', { exact: true }),
  ).toBeVisible();
  await expect(page.getByRole('link', { name: 'account settings' })).toHaveAttribute(
    'href',
    '/apps/settings',
  );
  await expect(page.getByLabel('Your current password', { exact: false })).toHaveCount(0);
});

test('uncertain staff create outcome keeps inputs and blocks duplicate submission', async ({
  page,
}) => {
  await login(page);
  await page.goto('/apps/admin/staff/new');
  await page.getByLabel('Name', { exact: false }).fill('Operator Tertahan');
  await page.getByLabel('Sign-in email', { exact: false }).fill('pending@example.invalid');
  await page.getByLabel('Initial password', { exact: false }).fill('initial-pass-8');

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

  const submit = page.getByRole('button', { name: 'Create staff account', exact: true });
  await submit.click();
  await expect(page.getByText(/outcome is uncertain/i)).toBeVisible();
  await expect(submit).toBeDisabled();
  await expect(page.getByLabel('Name', { exact: false })).toHaveValue('Operator Tertahan');
  await expect(page.getByLabel('Sign-in email', { exact: false })).toHaveValue(
    'pending@example.invalid',
  );
  await expect(page.getByLabel('Initial password', { exact: false })).toHaveValue('initial-pass-8');

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

  await page.getByLabel('Staff ID', { exact: false }).fill('1');
  await page.getByRole('button', { name: 'Inspect access', exact: true }).click();
  await expect(page.getByText('You do not have permission for this action.')).toBeVisible();
  const enableGrant = page.getByRole('button', {
    name: 'Grant or restore access',
    exact: true,
  });
  await expect(enableGrant).toBeDisabled();
  await expect(page.getByText('No access row exists for this ID.')).toHaveCount(0);
  expect(grantRequests).toBe(0);
});
