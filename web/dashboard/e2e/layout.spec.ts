import { expect, test, type Page } from '@playwright/test';

const app = '/apps/11111111-1111-4111-8111-111111111111';

async function login(page: Page) {
  await page.goto('/login');
  await page.getByLabel('Email').fill('staff@example.invalid');
  await page.locator('input[name="password"]').fill('fixture-only');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Choose application' })).toBeVisible();
}

test('template form and preview retain usable independent regions on desktop', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await login(page);
  await page.goto(`${app}/templates/new`);
  const field = page.getByLabel('Template name');
  await expect(field).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
  const input = await field.boundingBox();
  const preview = await page.getByRole('heading', { name: 'Notification preview' }).boundingBox();
  expect(input!.width).toBeGreaterThanOrEqual(320);
  expect(input!.width).toBeLessThanOrEqual(720);
  expect(preview!.x).toBeGreaterThan(input!.x + input!.width);
  await page.getByLabel('Notification title').fill('A preview without a write');
  await expect(page.getByRole('heading', { name: 'A preview without a write' })).toBeVisible();
});

test('settings sections align with their page heading and keep bounded fields', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await login(page);
  await page.goto(`${app}/settings`);
  const title = page.getByRole('heading', { name: 'My account', exact: true });
  await expect(title).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
  const heading = await title.boundingBox();
  const section = await page
    .getByRole('heading', { name: 'Appearance', exact: true })
    .boundingBox();
  expect(Math.abs(heading!.x - section!.x)).toBeLessThanOrEqual(1);
  const input = await page.getByLabel('Name', { exact: false }).boundingBox();
  expect(input!.width).toBeGreaterThanOrEqual(320);
  expect(input!.width).toBeLessThanOrEqual(720);
  const save = await page.getByRole('button', { name: 'Save profile', exact: true }).boundingBox();
  expect(save!.width).toBeLessThan(300);
});

for (const width of [320, 390]) {
  test(`operational pages retain their shell and avoid page overflow at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 844 });
    await login(page);
    for (const path of ['overview', 'campaigns', 'campaigns/new', 'templates/new', 'settings']) {
      await page.goto(`${app}/${path}`);
      await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
      await page.evaluate(() => document.fonts.ready);
      await expect(page.getByRole('link', { name: 'hello.', exact: true })).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
    }
  });
}
