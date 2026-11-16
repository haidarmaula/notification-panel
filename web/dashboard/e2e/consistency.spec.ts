import { expect, test } from '@playwright/test';

const app = '/apps/11111111-1111-4111-8111-111111111111';

for (const width of [390, 1920]) {
  test(`shared page and notification patterns stay consistent at ${width}px`, async ({ page }) => {
    test.setTimeout(90000);
    page.on('dialog', async (dialog) => {
      if (dialog.type() === 'beforeunload') await dialog.accept();
      else await dialog.dismiss();
    });
    await page.setViewportSize({ width, height: 1000 });
    await page.goto('/login');
    await page.getByLabel('Email').fill('staff@example.invalid');
    await page.locator('input[name="password"]').fill('fixture-only');
    await page.getByRole('button', { name: 'Sign in', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Choose application' })).toBeVisible();
    for (const theme of ['stone', 'y2k', 'matcha']) {
      await page.evaluate((theme) => {
        localStorage.setItem('hello.design-theme', theme);
        localStorage.setItem('hello.theme', 'dark');
      }, theme);
      let headingX: number | undefined;
      for (const path of [
        'settings',
        'segments/new',
        'campaigns/new',
        'templates/new',
        'campaigns/9007199254740993',
      ]) {
        await page.goto(`${app}/${path}`);
        const heading = page.getByRole('heading', { level: 1 });
        await expect(heading).toBeVisible();
        await page.evaluate(() => document.fonts.ready);
        const box = (await heading.boundingBox())!;
        headingX ??= box.x;
        expect(Math.abs(box.x - headingX)).toBeLessThanOrEqual(1);
        if (path.includes('campaigns') || path === 'templates/new') {
          if (path === 'campaigns/new' && width < 761) {
            await page.getByRole('button', { name: 'Notification preview', exact: true }).click();
          }
          const preview = page.getByRole('region', { name: 'Android notification preview' });
          await expect(preview).toBeVisible();
          await expect(preview).toContainText('Aplikasi Uji');
          if (path.endsWith('/new')) {
            await page.getByLabel('Notification title').fill('Long preview title');
            await page.getByLabel('Message', { exact: false }).fill('LongMessage'.repeat(80));
            await expect(preview.getByRole('heading')).toHaveText('Long preview title');
            await page.screenshot({
              path: `test-results/consistency-${theme}-${width}-${path.split('/')[0]}.png`,
              fullPage: true,
            });
          }
        }
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
      }
    }
  });
}
