import { test, expect, type Page } from '@playwright/test';
const app = '11111111-1111-4111-8111-111111111111';
async function login(page: Page) {
  await page.goto('/login');
  await page.getByLabel('Email').fill('staff@example.invalid');
  await page.locator('input[name="password"]').fill('fixture-only');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'Choose application' })).toBeVisible();
}
async function selectSignOut(page: Page) {
  await page.getByRole('button', { name: 'Account menu', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Sign out', exact: true }).click();
}
test('logs in through the real BFF, keeps credentials server-side, and logs out', async ({
  page,
}) => {
  await login(page);
  expect(
    await page.evaluate(() => ({ local: localStorage.length, session: sessionStorage.length })),
  ).toEqual({ local: 0, session: 0 });
  const session = await page.request.get('/bff/session');
  const text = await session.text();
  expect(text).not.toContain('access_token');
  expect(text).not.toContain('refresh_token');
  const cookies = await page.context().cookies();
  expect(cookies.find((item) => item.name === 'hello_dev_session')?.httpOnly).toBe(true);
  const sidebar = page.getByRole('navigation', { name: 'Side navigation' });
  await expect(
    sidebar
      .getByRole('button', { name: 'Account menu', exact: true })
      .getByText('Staf Pengujian', { exact: true }),
  ).toBeVisible();
  await expect(
    sidebar
      .getByRole('button', { name: 'Account menu', exact: true })
      .getByText('staff@example.invalid', { exact: true }),
  ).toBeVisible();
  await expect(sidebar.locator('.astryx-avatar')).toBeVisible();
  await page.getByRole('button', { name: 'Account menu', exact: true }).click();
  const account = page.getByRole('menuitem', { name: 'My account', exact: true });
  await expect(account).toHaveAttribute('href', '/apps/settings');
  await page.keyboard.press('Escape');
  await selectSignOut(page);
  await expect(page.getByRole('heading', { name: 'Welcome back' })).toBeVisible();
});
test('shows invalid credentials without creating an authenticated session', async ({ page }) => {
  await page.goto('/login');
  await page.getByLabel('Email').fill('staff@example.invalid');
  await page.locator('input[name="password"]').fill('wrong');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByText('Email or password is incorrect.')).toBeVisible();
});
test('creates and edits a draft using scoped recipients without sending', async ({ page }) => {
  await login(page);
  await page.getByRole('link', { name: /Aplikasi Uji/ }).click();
  await page.getByRole('link', { name: 'Create campaign', exact: true }).click();
  await page.getByLabel('Notification title').fill('Draft dari pengujian');
  await page.getByLabel('Message').fill('Isi pesan fixture tanpa provider.');
  await page.getByRole('button', { name: 'Penerima Uji #42' }).click();
  await page.getByRole('button', { name: 'Save draft' }).click();
  await expect(
    page.getByRole('heading', { name: 'Draft dari pengujian', exact: true }),
  ).toBeVisible();
  await page.getByRole('link', { name: 'Edit draft' }).click();
  await page.getByLabel('Message').fill('Pesan yang diperbarui.');
  await page.getByRole('button', { name: 'Save draft' }).click();
  await expect(page.getByText('Pesan yang diperbarui.', { exact: true }).first()).toBeVisible();
});
test('shows held acceptance and preserves the draft results state', async ({ page }) => {
  await login(page);
  await page.goto(`/apps/${app}/campaigns/9007199254740993`);
  await page.getByRole('button', { name: 'Review send' }).click();
  await expect(page.getByRole('dialog')).toContainText('Penerima Uji');
  await expect(page.getByRole('dialog')).toContainText('User #42');
  await page.getByRole('button', { name: 'Confirm send' }).click();
  await expect(page.getByRole('alert')).toContainText('Dispatch is held');
  await page.getByRole('button', { name: 'Back', exact: true }).click();
  await page.getByRole('link', { name: 'Campaign history and results' }).click();
  await expect(page.getByRole('heading', { name: 'Campaign has not been sent' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Statistics are incomplete' })).toHaveCount(0);
});
test('mobile navigation and list fit a narrow screen', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await login(page);
  await page.getByRole('link', { name: /Aplikasi Uji/ }).click();
  await expect(page.getByRole('heading', { name: 'Campaigns', exact: true })).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= document.documentElement.clientWidth,
    ),
  ).toBe(true);
  await page.getByRole('button', { name: 'Open navigation' }).click();
  await expect(page.getByRole('button', { name: 'Open navigation' })).toHaveAttribute(
    'aria-expanded',
    'true',
  );
});

test('message preview updates locally without a draft write', async ({ page }) => {
  let draftWrites = 0;
  page.on('request', (request) => {
    if (
      request.method() === 'POST' &&
      /\/bff\/api\/apps\/[^/]+\/campaigns$/.test(new URL(request.url()).pathname)
    )
      draftWrites++;
  });
  await login(page);
  await page.getByRole('link', { name: /Aplikasi Uji/ }).click();
  await expect(page.getByRole('heading', { name: 'Campaigns', exact: true })).toBeVisible();
  await page.screenshot({ path: 'test-results/dashboard-campaigns.png', fullPage: true });
  await page.getByRole('link', { name: 'Create campaign', exact: true }).click();
  await page.getByLabel('Notification title').fill('Pengingat layanan besok');
  await page
    .getByLabel('Message')
    .fill('Layanan tersedia mulai pukul 09.00. Silakan buka aplikasi untuk melihat rinciannya.');
  await expect(
    page.getByRole('heading', { name: 'Pengingat layanan besok', exact: true }),
  ).toBeVisible();
  expect(draftWrites).toBe(0);
  await page.screenshot({ path: 'test-results/dashboard-editor.png', fullPage: true });
});

test('persists appearance and desktop sidebar preference without storing account data', async ({
  page,
}) => {
  await login(page);
  await page.goto(`/apps/${app}/settings`);
  await page.getByRole('button', { name: 'Use dark mode', exact: true }).click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  const sidebar = page.getByRole('navigation', { name: 'Side navigation' });
  await expect(
    sidebar
      .getByRole('button', { name: 'Account menu', exact: true })
      .getByText('Staf Pengujian', { exact: true }),
  ).toBeVisible();
  await expect(
    sidebar
      .getByRole('button', { name: 'Account menu', exact: true })
      .getByText('staff@example.invalid', { exact: true }),
  ).toBeVisible();
  await expect(sidebar.locator('.astryx-avatar')).toBeVisible();
  await sidebar.getByRole('button', { name: 'Collapse Sidebar' }).click();
  await expect(sidebar).toBeVisible();
  await expect(sidebar.getByRole('link', { name: 'Campaigns', exact: true })).toBeVisible();
  expect((await sidebar.boundingBox())!.width).toBeLessThan(100);
  await expect(
    sidebar
      .getByRole('button', { name: 'Account menu', exact: true })
      .getByText('Staf Pengujian', { exact: true }),
  ).toHaveCount(0);
  await expect(
    sidebar
      .getByRole('button', { name: 'Account menu', exact: true })
      .getByText('staff@example.invalid', { exact: true }),
  ).toHaveCount(0);
  await expect(sidebar.locator('.astryx-avatar')).toBeVisible();
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await expect(sidebar.getByRole('button', { name: 'Expand Sidebar' })).toBeVisible();
  await expect(sidebar).toBeVisible();
  expect((await sidebar.boundingBox())!.width).toBeLessThan(100);
  await sidebar.getByRole('button', { name: 'Expand Sidebar' }).click();
  await expect(page.getByRole('navigation', { name: 'Side navigation' })).toBeVisible();
  await expect(
    sidebar
      .getByRole('button', { name: 'Account menu', exact: true })
      .getByText('Staf Pengujian', { exact: true }),
  ).toBeVisible();
  await expect(
    sidebar
      .getByRole('button', { name: 'Account menu', exact: true })
      .getByText('staff@example.invalid', { exact: true }),
  ).toBeVisible();
  await expect(sidebar.locator('.astryx-avatar')).toBeVisible();
  expect(await page.evaluate(() => Object.keys(localStorage).sort())).toEqual([
    'hello.sidebar',
    'hello.theme',
  ]);
  await page.screenshot({ path: 'test-results/dashboard-settings-dark.png', fullPage: true });
  await page.emulateMedia({ colorScheme: 'light' });
  await page.getByRole('combobox', { name: 'Appearance' }).click();
  await page.getByRole('option', { name: 'System', exact: true }).click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  await page.emulateMedia({ colorScheme: 'dark' });
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
});

test('creates, replaces and uses a template without sending', async ({ page }) => {
  await login(page);
  await page.goto(`/apps/${app}/templates`);
  await page.getByRole('link', { name: 'Create template', exact: true }).click();
  await page.getByLabel('Template name').fill('Template layanan');
  await page.getByLabel('Notification title').fill('Informasi layanan');
  await page.getByRole('textbox', { name: /^Message/ }).fill('Jadwal layanan diperbarui.');
  await page.getByRole('button', { name: 'Save template', exact: true }).click();
  await page.getByRole('link', { name: 'Template layanan', exact: true }).click();
  await page.getByRole('textbox', { name: /^Message/ }).fill('Jadwal layanan terbaru.');
  await page.getByRole('button', { name: 'Save template', exact: true }).click();
  const row = page.getByRole('row').filter({ hasText: 'Template layanan' });
  await row.getByRole('link', { name: 'Use', exact: true }).click();
  await expect(page.getByRole('textbox', { name: /^Message/ })).toHaveValue(
    'Jadwal layanan terbaru.',
  );
  await page.getByRole('button', { name: 'Penerima Uji #42' }).click();
  await page.getByRole('button', { name: 'Save draft', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Informasi layanan', exact: true })).toBeVisible();
  await expect(page.getByText('Draft', { exact: true })).toBeVisible();
});

test('manages a manual segment and its membership through the real BFF', async ({ page }) => {
  await login(page);
  await page.goto(`/apps/${app}/segments`);
  await page.getByRole('link', { name: 'Create segment', exact: true }).click();
  await page.getByLabel('Segment name').fill('Pilot internal');
  await page.getByLabel('Description', { exact: true }).fill('Pengguna uji internal');
  await page.getByRole('button', { name: 'Save segment', exact: true }).click();
  await page.getByRole('button', { name: 'Add member', exact: true }).click();
  await page.getByRole('button', { name: 'Add Penerima Uji', exact: true }).click();
  await expect(
    page.getByRole('button', { name: 'Remove Penerima Uji', exact: true }),
  ).toBeVisible();
  await page.screenshot({ path: 'test-results/dashboard-segment.png', fullPage: true });
  await page.getByLabel('Description', { exact: true }).fill('Anggota diperbarui');
  await page.getByRole('button', { name: 'Save segment', exact: true }).click();
  await expect(page.getByLabel('Description', { exact: true })).toHaveValue('Anggota diperbarui');
  page.on('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', { name: 'Remove Penerima Uji', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'No members in this segment' })).toBeVisible();
  await page.getByRole('button', { name: 'Delete segment', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Segments', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Pilot internal', exact: true })).toHaveCount(0);
});

test('opens scoped device registrations and saves the staff profile', async ({ page }) => {
  await login(page);
  await page.goto(`/apps/${app}/audience`);
  await page.getByRole('link', { name: 'Penerima Uji', exact: true }).click();
  await expect(page.getByRole('cell', { name: 'ANDROID', exact: true })).toBeVisible();
  await page.getByRole('link', { name: 'Application settings', exact: true }).click();
  await page.getByLabel('Name').fill('Staf Operasional');
  await page.getByRole('button', { name: 'Save profile', exact: true }).click();
  await expect(page.getByText('Profile updated successfully.', { exact: true })).toBeVisible();
  await expect(
    page
      .getByRole('button', { name: 'Account menu', exact: true })
      .getByText('Staf Operasional', { exact: true }),
  ).toBeVisible();
});

test('mobile navigation contains focus, closes with Escape, and restores focus', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await login(page);
  await page.goto(`/apps/${app}/segments`);
  const opener = page.getByRole('button', { name: 'Open navigation', exact: true });
  await opener.click();
  const drawer = page.getByRole('dialog');
  await expect(drawer).toBeVisible();
  await expect
    .poll(() => drawer.evaluate((node) => node.contains(document.activeElement)))
    .toBe(true);
  for (let index = 0; index < 5; index++) {
    await page.keyboard.press('Tab');
    await expect
      .poll(() => drawer.evaluate((node) => node.contains(document.activeElement)))
      .toBe(true);
  }
  await page.keyboard.press('Escape');
  await expect(opener).toBeFocused();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

test('accepted fixture campaign immediately removes draft actions and refreshes list state', async ({
  page,
}) => {
  await login(page);
  await page.goto(`/apps/${app}/campaigns/new`);
  await page.getByLabel('Notification title').fill('Fixture acceptance');
  await page.getByLabel('Message').fill('Isolated fixture, never sent to a provider.');
  await page.getByRole('button', { name: 'Penerima Uji #42' }).click();
  await page.getByRole('button', { name: 'Save draft', exact: true }).click();
  await page.getByRole('button', { name: 'Review send', exact: true }).click();
  await page.getByRole('button', { name: 'Confirm send', exact: true }).click();
  await expect(page.getByText('Queued', { exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Edit draft', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Review send', exact: true })).toHaveCount(0);
  await page
    .getByRole('region', { name: 'Page header', exact: true })
    .getByRole('link', { name: 'Back to campaigns', exact: true })
    .click();
  await expect(page.getByRole('row').filter({ hasText: 'Fixture acceptance' })).toContainText(
    'Queued',
  );
});

test('uncertain campaign acceptance blocks a second submission in the open dialog', async ({
  page,
}) => {
  await login(page);
  await page.goto(`/apps/${app}/campaigns/9007199254740993`);
  let submissions = 0;
  await page.route(`**/bff/api/apps/${app}/campaigns/9007199254740993/send`, async (route) => {
    submissions++;
    await route.abort('failed');
  });
  await page.getByRole('button', { name: 'Review send', exact: true }).click();
  const confirm = page.getByRole('button', { name: 'Confirm send', exact: true });
  await confirm.click();
  await expect(confirm).toBeDisabled();
  await page.keyboard.press('Enter');
  expect(submissions).toBe(1);
  await expect(page.getByRole('dialog')).toContainText('The outcome is unknown');
});

test('campaign polling stops after a terminal status', async ({ page }) => {
  await login(page);
  let reads = 0;
  await page.route(`**/bff/api/apps/${app}/campaigns/9007199254740993`, async (route) => {
    const response = await route.fetch();
    const payload = await response.json();
    reads++;
    payload.data.status = reads === 1 ? 'PROCESSING' : 'SENT';
    await route.fulfill({ response, json: payload });
  });
  await page.goto(`/apps/${app}/campaigns/9007199254740993`);
  await expect.poll(() => reads, { timeout: 10000 }).toBe(2);
  await page.waitForTimeout(6000);
  expect(reads).toBe(2);
});

test('a delayed template cannot overwrite an editable message', async ({ page }) => {
  await login(page);
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(`**/bff/api/apps/${app}/templates/3`, async (route) => {
    await gate;
    await route.continue();
  });
  await page.goto(`/apps/${app}/campaigns/new?template=3`);
  await expect(page.getByLabel('Notification title')).toBeDisabled();
  await expect(page.getByLabel('Message')).toBeDisabled();
  release();
  await expect(page.getByLabel('Message')).toHaveValue('Buka aplikasi untuk melihat informasi.');
  await expect(page.getByLabel('Message')).toBeEnabled();
});

test('management screens fit a narrow viewport', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await login(page);
  for (const route of [
    'overview',
    'settings',
    'templates',
    'segments/7',
    'audience',
    'audience/42',
    'logs',
  ]) {
    await page.goto(`/apps/${app}/${route}`);
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
      route,
    ).toBe(true);
  }
  await page.screenshot({ path: 'test-results/dashboard-mobile-logs.png', fullPage: true });
});

test('closed mobile navigation is skipped by keyboard and resets on desktop resize', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await login(page);
  await page.goto(`/apps/${app}/campaigns`);
  const opener = page.getByRole('button', { name: 'Open navigation', exact: true });
  await expect(page.getByRole('button', { name: /sidebar/ })).toHaveCount(0);
  await page.getByRole('link', { name: 'Skip to content' }).focus();
  for (let index = 0; index < 4; index++) {
    await page.keyboard.press('Tab');
    expect(
      await page.evaluate(
        () => document.activeElement?.closest('[aria-label="Side navigation"]') !== null,
      ),
    ).toBe(false);
  }
  await opener.click();
  await page.getByRole('link', { name: 'Templates', exact: true }).click();
  await expect(opener).toBeFocused();
  await expect(page.getByRole('navigation', { name: 'Side navigation' })).toHaveCount(0);
  await opener.click();
  await page.setViewportSize({ width: 1440, height: 1000 });
  await expect(page.getByRole('main')).not.toHaveAttribute('inert');
  await expect(page.getByRole('heading', { name: 'Templates', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Collapse Sidebar' })).toBeVisible();
});

test('dark login uses the official card composition and supports password visibility', async ({
  page,
}) => {
  await page.addInitScript(() => localStorage.setItem('hello.theme', 'dark'));
  await page.goto('/login');
  await expect(page.getByRole('heading', { name: 'Welcome back' })).toBeVisible();
  await expect(page.getByLabel('Email')).toHaveAttribute('type', 'email');
  await expect(page.locator('.astryx-card')).toHaveCount(1);
  await expect(page.getByText('hello.', { exact: true })).toBeVisible();
  const photograph = page.getByRole('img', { name: 'Two colleagues working together at a laptop' });
  await expect(photograph).toBeVisible();
  await expect
    .poll(() => photograph.evaluate((image: HTMLImageElement) => image.naturalWidth))
    .toBeGreaterThan(0);
  await page.locator('input[name="password"]').fill('fixture-only');
  await page.getByRole('button', { name: 'Show password' }).click();
  await expect(page.locator('input[name="password"]')).toHaveAttribute('type', 'text');
  await page.getByRole('button', { name: 'Hide password' }).click();
  await expect(page.locator('input[name="password"]')).toHaveAttribute('type', 'password');
  await page.screenshot({ path: 'test-results/dashboard-login-dark.png', fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: 'test-results/dashboard-login-mobile-dark.png', fullPage: true });
  await page.getByLabel('Email').fill('staff@example.invalid');
  await page.locator('input[name="password"]').fill('wrong');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByText('Email or password is incorrect.')).toBeVisible();
});

test('dark editor keeps readable enabled recipients and notification content', async ({ page }) => {
  await login(page);
  await page.goto(`/apps/${app}/settings`);
  await page.getByRole('combobox', { name: 'Appearance' }).click();
  await page.getByRole('option', { name: 'Dark', exact: true }).click();
  await page.goto(`/apps/${app}/campaigns/new`);
  await expect(page.getByRole('button', { name: 'Penerima Uji #42' })).toBeEnabled();
  const contrast = await page.evaluate(() => {
    const rgb = (value: string) =>
      value
        .match(/[\d.]+/g)!
        .slice(0, 3)
        .map(Number);
    const luminance = (value: number[]) =>
      value
        .map((channel) => {
          channel /= 255;
          return channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4;
        })
        .reduce((sum, channel, index) => sum + channel * [0.2126, 0.7152, 0.0722][index], 0);
    const elements = [
      {
        selector: 'recipient option',
        element: Array.from(document.querySelectorAll<HTMLElement>('button')).find((item) =>
          item.textContent?.includes('Penerima Uji #42'),
        ),
      },
      {
        selector: 'notification preview',
        element: Array.from(document.querySelectorAll<HTMLElement>('*')).find(
          (item) =>
            item.children.length === 0 &&
            item.textContent?.trim() === 'Your message will appear here as you type.',
        ),
      },
      {
        selector: 'back link',
        element: Array.from(document.querySelectorAll<HTMLElement>('a')).find((item) =>
          item.textContent?.includes('Back to campaigns'),
        ),
      },
    ];
    return elements.map(({ selector, element }) => {
      if (!element) throw new Error(`Missing ${selector} element`);
      let parent: HTMLElement | null = element;
      while (parent && getComputedStyle(parent).backgroundColor === 'rgba(0, 0, 0, 0)')
        parent = parent.parentElement;
      const foreground = luminance(rgb(getComputedStyle(element).color));
      const background = luminance(rgb(getComputedStyle(parent!).backgroundColor));
      return {
        selector,
        ratio:
          (Math.max(foreground, background) + 0.05) / (Math.min(foreground, background) + 0.05),
      };
    });
  });
  for (const item of contrast) expect(item.ratio, item.selector).toBeGreaterThanOrEqual(4.5);
  await page.screenshot({ path: 'test-results/dashboard-editor-dark.png', fullPage: true });
});

test('draft validation identifies fields, restores focus, and preserves field geometry', async ({
  page,
}) => {
  await login(page);
  await page.goto(`/apps/${app}/campaigns/new`);
  const title = page.getByLabel('Notification title');
  const body = page.getByLabel('Message');
  const before = await body.boundingBox();
  await page.getByRole('button', { name: 'Save draft', exact: true }).click();
  await expect(title).toBeFocused();
  await expect(title).toHaveAttribute('aria-invalid', 'true');
  await expect(title).toHaveAccessibleDescription(/Title is required/);
  await expect(body).toHaveAttribute('aria-invalid', 'true');
  const after = await body.boundingBox();
  expect(Math.abs(after!.y + (await page.evaluate(() => scrollY)) - before!.y)).toBeLessThanOrEqual(
    2,
  );
  await title.fill('Judul valid');
  await body.fill('Pesan valid');
  await page.getByRole('button', { name: 'Save draft', exact: true }).click();
  const broadcast = page.getByLabel('All recipients in this application');
  await expect(broadcast).toBeFocused();
  await expect(broadcast).toHaveAccessibleDescription('Select at least one recipient.');
  await page.getByRole('button', { name: 'Penerima Uji #42' }).click();
  await expect(broadcast).not.toHaveAttribute('aria-invalid', 'true');
  await title.fill('é'.repeat(128));
  await page.getByRole('button', { name: 'Save draft', exact: true }).click();
  await expect(title).toBeFocused();
  await expect(title).toHaveAccessibleDescription(/Title must be at most 255 bytes/);
});

test('recipient names survive broadcast toggles, draft editing, and send review', async ({
  page,
}) => {
  await login(page);
  await page.goto(`/apps/${app}/campaigns/new`);
  await page.getByLabel('Notification title').fill('Named recipients');
  await page.getByLabel('Message').fill('Isolated draft with named recipients.');
  await page.getByRole('button', { name: 'Penerima Uji #42' }).click();
  await page.getByRole('main').getByRole('button', { name: 'Segments', exact: true }).click();
  await page.getByRole('button', { name: 'Segmen Uji #7' }).click();
  const selected = page.getByRole('group', { name: 'Selected recipients' });
  await expect(selected).toContainText('Penerima Uji');
  await expect(selected).toContainText('Segmen Uji');
  const broadcast = page.getByLabel('All recipients in this application');
  await broadcast.check();
  await broadcast.uncheck();
  await expect(selected).toContainText('Penerima Uji');
  await expect(selected).toContainText('Segmen Uji');
  await page.getByRole('button', { name: 'Save draft', exact: true }).click();
  const recipients = page.getByRole('group', { name: 'Recipients' });
  await expect(recipients).toContainText('Penerima Uji');
  await expect(recipients).toContainText('Segmen Uji');
  await page.getByRole('link', { name: 'Edit draft', exact: true }).click();
  await expect(selected).toContainText('Penerima Uji');
  await expect(selected).toContainText('Segmen Uji');
  await page.getByRole('button', { name: 'Save draft', exact: true }).click();
  await page.getByRole('button', { name: 'Review send', exact: true }).click();
  await expect(page.getByRole('dialog')).toContainText('Penerima Uji');
  await expect(page.getByRole('dialog')).toContainText('Segmen Uji');
  await expect(page.getByRole('dialog')).not.toContainText('INDIVIDUAL');
});

test('mobile editor starts with fields and keeps save reachable with a collapsible preview', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await login(page);
  await page.goto(`/apps/${app}/campaigns/new`);
  const title = page.getByLabel('Notification title');
  await expect(title).toBeInViewport();
  await expect(page.getByRole('button', { name: 'Notification preview' })).toHaveAttribute(
    'aria-expanded',
    'false',
  );
  const save = page.getByRole('button', { name: 'Save draft', exact: true });
  await expect(save).toBeInViewport();
  await title.fill('Pesan mobile');
  await page.getByLabel('Message').fill('Isi pratinjau mobile.');
  await page.getByRole('button', { name: 'Notification preview' }).click();
  await expect(
    page
      .getByRole('region', { name: 'Notification preview' })
      .getByText('Isi pratinjau mobile.', { exact: true }),
  ).toContainText('Isi pratinjau mobile.');
  await expect(save).toBeInViewport();
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: 'test-results/dashboard-editor-mobile.png', fullPage: true });
});

test('campaign loading and empty states keep the footer stable and ID lookup secondary', async ({
  page,
}) => {
  await login(page);
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(`**/bff/api/apps/${app}/campaigns?*`, async (route) => {
    await gate;
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ success: true, data: { items: [], next_after_id: null } }),
    });
  });
  await page.goto(`/apps/${app}/campaigns`);
  await expect(page.getByRole('status')).toContainText('Loading data');
  const before = await page
    .getByText('Messages are sent only after you review and confirm the campaign.', { exact: true })
    .boundingBox();
  release();
  await expect(page.getByRole('heading', { name: 'Start with your first message' })).toBeVisible();
  const after = await page
    .getByText('Messages are sent only after you review and confirm the campaign.', { exact: true })
    .boundingBox();
  expect(Math.abs(after!.y - before!.y)).toBeLessThanOrEqual(1);
  await expect(page.getByLabel('Open by campaign ID')).not.toBeVisible();
  await page.getByRole('button', { name: 'Open by campaign ID' }).click();
  await expect(page.getByLabel('Open by campaign ID')).toBeVisible();
  await page.getByLabel('Open by campaign ID').fill('9007199254740993');
  await page.getByRole('button', { name: 'Open', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'Pengingat jadwal layanan', level: 1, exact: true }),
  ).toBeVisible();
});
