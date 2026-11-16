import { expect, test, type Page } from '@playwright/test';

async function login(page: Page) {
  await page.goto('/login');
  await page.locator('input[name="email"]').fill('staff@example.invalid');
  await page.locator('input[name="password"]').fill('fixture-only');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Choose application' })).toBeVisible();
}
async function chooseTheme(page: Page, name: string) {
  await page.goto('/apps/settings');
  const selector = page.getByRole('combobox', { name: 'Design theme' });
  await selector.click();
  await page.getByRole('option', { name, exact: true }).click();
  await expect(selector).toContainText(name);
  await page.goto('/apps');
  await expect(page.getByRole('heading', { name: 'Choose application' })).toBeVisible();
}
async function chooseAppearance(page: Page, name: string) {
  await page
    .getByRole('button', {
      name: name === 'Dark' ? 'Use dark mode' : 'Use light mode',
      exact: true,
    })
    .click();
}

test('Stone is the default and explicit theme and appearance choices persist independently', async ({
  page,
}) => {
  await login(page);
  await expect(page.getByRole('button', { name: 'Account menu', exact: true })).toBeVisible();
  expect(await page.evaluate(() => localStorage.length)).toBe(0);
  await chooseTheme(page, 'Matcha');
  await chooseAppearance(page, 'Dark');
  await page.reload();
  await page.goto('/apps/settings');
  await expect(page.getByRole('combobox', { name: 'Design theme' })).toContainText('Matcha');
  await expect(page.getByRole('radio', { name: 'Matcha', exact: true })).toBeChecked();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await expect(page.getByRole('button', { name: 'Use light mode', exact: true })).toBeVisible();
  expect(await page.evaluate(() => Object.keys(localStorage).sort())).toEqual([
    'hello.design-theme',
    'hello.theme',
  ]);
  await chooseTheme(page, 'Stone');
  await chooseAppearance(page, 'Light');
  await expect(page.getByRole('radio', { name: 'Stone', exact: true })).toBeChecked();
  await expect(page.getByRole('button', { name: 'Use dark mode', exact: true })).toBeVisible();
});

test('all three production themes remain usable at 320px and fonts stay same-origin', async ({
  page,
}) => {
  const externalFonts: string[] = [];
  page.on('request', (request) => {
    if (/fonts\.(googleapis|gstatic)\.com/.test(request.url())) externalFonts.push(request.url());
  });
  await page.setViewportSize({ width: 320, height: 844 });
  await login(page);
  const fonts = new Set<string>();
  for (const name of ['Stone', 'Y2K', 'Matcha']) {
    await chooseTheme(page, name);
    fonts.add(
      await page
        .getByRole('heading', { name: 'Choose application' })
        .evaluate((element) => getComputedStyle(element).fontFamily),
    );
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
  }
  expect(fonts.size).toBeGreaterThan(1);
  expect(externalFonts).toEqual([]);
});
