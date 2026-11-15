import { expect, test, type Page } from '@playwright/test';

const id = '11111111-1111-4111-8111-111111111111';
const app = `/apps/${id}`;
const campaign = `${app}/campaigns/9007199254740993`;

async function login(page: Page) {
  await page.goto('/login');
  await page.getByLabel('Email').fill('staff@example.invalid');
  await page.locator('input[name="password"]').fill('fixture-only');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Choose application' })).toBeVisible();
}

async function expectFullHeaders(page: Page) {
  await expect(page.getByRole('table').first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
  const clipped = await page.getByRole('columnheader').evaluateAll((headers) =>
    headers.flatMap((header) => {
      const style = getComputedStyle(header);
      const range = document.createRange();
      range.selectNodeContents(header);
      const labelWidth = range.getBoundingClientRect().width;
      const available =
        header.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight);
      return labelWidth > available + 1
        ? [{ label: header.textContent, labelWidth, available }]
        : [];
    }),
  );
  expect(clipped, `Clipped headers at ${page.url()}`).toEqual([]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
}

for (const width of [320, 1440]) {
  test(`table labels fit their columns across themes at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 1000 });
    await login(page);
    const respond = (data: unknown) => ({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ success: true, data }),
    });
    await page.route(`**/bff/api${campaign}`, (route) =>
      route.fulfill(
        respond({
          id: '9007199254740993',
          title: 'Header layout fixture',
          status: 'COMPLETED',
        }),
      ),
    );
    await page.route(`**/bff/api${campaign}/statistics`, (route) =>
      route.fulfill(
        respond({
          schema_version: 1,
          snapshot_complete: false,
          counts: null,
        }),
      ),
    );
    await page.route(`**/bff/api${campaign}/deliveries?*`, (route) =>
      route.fulfill(
        respond({
          items: [{ id: '81', user_id: '42', status: 'DELIVERED', sent_at: null, opened_at: null }],
          next_after_id: null,
        }),
      ),
    );
    await page.route(`**/bff/api${campaign}/reads?*`, (route) =>
      route.fulfill(
        respond({
          items: [{ id: '82', user_id: '42', read_at: '2026-01-01T00:00:00Z' }],
          next_after_id: null,
        }),
      ),
    );
    await page.route(`**/bff/api/admin/apps/${id}/staff-grants?*`, (route) =>
      route.fulfill(
        respond({
          items: [{ staff_id: '1', is_active: true, revision: '1' }],
          total: 1,
        }),
      ),
    );
    for (const theme of ['Stone', 'Y2K', 'Matcha']) {
      await page.goto('/apps/settings');
      await page.getByRole('combobox', { name: 'Design theme' }).click();
      await page.getByRole('option', { name: theme, exact: true }).click();
      for (const route of [
        '/apps',
        `${app}/campaigns`,
        `${app}/logs`,
        '/apps/admin/staff',
        '/apps/admin/applications',
        `/apps/admin/applications/${id}`,
        `${campaign}/results`,
      ]) {
        await page.goto(route);
        await expectFullHeaders(page);
      }
      await page.getByRole('radio', { name: 'Opens', exact: true }).click();
      await expectFullHeaders(page);
    }
  });
}
