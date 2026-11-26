import { expect, test } from '@playwright/test';

for (const width of [1440, 390]) {
  test(`header appearance controls and account menu work at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 1000 });
    await page.goto('/login');
    await page.getByLabel('Email').fill('staff@example.invalid');
    await page.locator('input[name="password"]').fill('fixture-only');
    await page.getByRole('button', { name: 'Sign in', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Choose application' })).toBeVisible();
    if (width < 768) await page.getByRole('button', { name: 'Open navigation' }).click();
    const sidebar = page.getByRole('navigation', { name: 'Side navigation' });
    const account = page.getByRole('button', { name: 'Account menu', exact: true });
    await expect(account).toHaveAttribute('aria-expanded', 'false');
    expect(await account.getAttribute('aria-controls')).toBeNull();
    await expect(account).toContainText('staff@example.invalid');
    await expect(account.getByRole('img')).toBeVisible();
    if (width < 768) await page.keyboard.press('Escape');
    await page.getByRole('button', { name: 'Use dark mode', exact: true }).click();
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
    await expect(page.getByRole('button', { name: 'Use light mode', exact: true })).toBeVisible();
    if (width >= 768) {
      await page.getByRole('radio', { name: 'Y2K', exact: true }).click();
      await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
      expect(await page.evaluate(() => localStorage.getItem('hello.design-theme'))).toBe('y2k');
    }
    if (width >= 768) {
      await sidebar.getByRole('button', { name: 'Collapse Sidebar' }).click();
      expect((await sidebar.boundingBox())!.width).toBeLessThan(100);
      await expect(account).toBeVisible();
      await expect(page.getByRole('button', { name: 'Use light mode', exact: true })).toBeVisible();
      await sidebar.getByRole('button', { name: 'Expand Sidebar' }).click();
    }
    if (width < 768) await page.getByRole('button', { name: 'Open navigation' }).click();
    await account.click();
    await expect(page.getByRole('menuitem', { name: 'Sign out', exact: true })).toBeVisible();
    await expect(account).toHaveAttribute('aria-expanded', 'true');
    expect(
      await account.evaluate((button) =>
        Boolean(document.getElementById(button.getAttribute('aria-controls') ?? '')),
      ),
    ).toBe(true);
    await page.getByRole('menuitem', { name: 'My account', exact: true }).click();
    await expect(page).toHaveURL('/apps/settings');
    if (width < 768) await expect(sidebar).not.toBeVisible();
  });
}
