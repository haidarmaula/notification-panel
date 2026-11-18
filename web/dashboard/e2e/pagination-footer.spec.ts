import { expect, test, type Page } from '@playwright/test';

const app = '/apps/11111111-1111-4111-8111-111111111111';

async function login(page: Page) {
  await page.goto('/login');
  await page.getByLabel('Email').fill('staff@example.invalid');
  await page.locator('input[name="password"]').fill('fixture-only');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Choose application' })).toBeVisible();
}

for (const width of [390, 1440]) {
  test(`page list pagination stays at the bottom at ${width}px`, async ({ page }) => {
    test.setTimeout(90000);
    await page.setViewportSize({ width, height: 1000 });
    await login(page);
    for (const path of [
      '/apps',
      `${app}/campaigns`,
      `${app}/logs`,
      `${app}/audience`,
      `${app}/segments`,
      `${app}/templates`,
      `${app}/uploads`,
      `${app}/uploads/17`,
      `${app}/audit`,
      '/apps/admin/staff',
      '/apps/admin/applications',
    ]) {
      await page.goto(path);
      const footer = page.getByRole('region', { name: 'Page footer', exact: true });
      await expect(footer, path).toBeVisible();
      await expect(footer.getByRole('button', { name: 'Previous', exact: true })).toBeVisible();
      await expect(footer.getByRole('button', { name: 'Next', exact: true })).toBeVisible();
      const bounds = (await footer.boundingBox())!;
      const main = (await page.getByRole('main').boundingBox())!;
      expect(Math.abs(bounds.y + bounds.height - main.y - main.height)).toBeLessThanOrEqual(2);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
    }
    await page.goto(`${app}/audience/42`);
    await expect(
      page.getByRole('heading', { name: 'Registered devices', exact: true }),
    ).toBeVisible();
    await expect(page.getByRole('region', { name: 'Page footer', exact: true })).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Previous', exact: true })).toBeVisible();
    await page.goto(`${app}/audience`);
    await expect(page.getByRole('region', { name: 'Page footer', exact: true })).toBeVisible();
    await page.evaluate(() => document.fonts.ready);
    await page.screenshot({ path: `test-results/pagination-footer-${width}.png`, fullPage: true });
  });
}

test('footer hides on initial empty data and keeps return navigation on an empty later page', async ({
  page,
}) => {
  await login(page);
  let empty = true;
  await page.route('**/bff/api/apps/*/users?*', (route) => {
    const later = new URL(route.request().url()).searchParams.has('after_id');
    return route.fulfill({
      json: {
        success: true,
        data: {
          items:
            empty || later
              ? []
              : [
                  {
                    id: '42',
                    external_id: 'fixture-recipient',
                    name: 'Recipient',
                    status: 'ACTIVE',
                  },
                ],
          next_after_id: empty || later ? null : '42',
        },
      },
    });
  });
  await page.goto(`${app}/audience`);
  await expect(page.getByRole('heading', { name: 'No users available' })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Page footer', exact: true })).toHaveCount(0);
  empty = false;
  await page.reload();
  const footer = page.getByRole('region', { name: 'Page footer', exact: true });
  await footer.getByRole('button', { name: 'Next', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'No users available' })).toBeVisible();
  await expect(footer.getByRole('button', { name: 'Previous', exact: true })).toBeEnabled();
  await footer.getByRole('button', { name: 'Previous', exact: true }).click();
  await expect(page.getByRole('link', { name: 'Recipient', exact: true })).toBeVisible();
});
