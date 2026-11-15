import { test, expect, type Page } from '@playwright/test';

const app = '11111111-1111-4111-8111-111111111111';
const campaign = `/apps/${app}/campaigns/9007199254740993`;

async function login(page: Page) {
  await page.goto('/login');
  await page.getByLabel('Email').fill('staff@example.invalid');
  await page.getByRole('textbox', { name: 'Password' }).fill('fixture-only');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'Choose application' })).toBeVisible();
}

for (const [route, field] of [
  ['campaigns/new', 'Notification title'],
  ['templates/new', 'Template name'],
  ['segments/new', 'Segment name'],
  ['settings', 'Name'],
]) {
  test(`cancelling logout preserves the session and unsaved ${route}`, async ({ page }) => {
    await login(page);
    await page.goto(`/apps/${app}/${route}`);
    await page.getByLabel(field).fill('Unsaved audit value');
    let logouts = 0;
    page.on('request', (request) => {
      if (request.url().endsWith('/bff/logout')) logouts++;
    });
    page.once('dialog', async (dialog) => {
      expect(dialog.type()).toBe('confirm');
      await dialog.dismiss();
    });
    const logout = page.getByRole('button', { name: 'Sign out', exact: true });
    await logout.click();
    await expect(logout).toBeEnabled();
    expect(logouts).toBe(0);
    expect((await (await page.request.get('/bff/session')).json()).authenticated).toBe(true);
    await expect(page.getByLabel(field)).toHaveValue('Unsaved audit value');
    const dialogs: string[] = [];
    page.on('dialog', async (dialog) => {
      dialogs.push(dialog.type());
      await dialog.accept();
    });
    await logout.click();
    await expect(page).toHaveURL(/\/login$/);
    await expect(page.getByRole('heading', { name: 'Welcome back' })).toBeVisible();
    expect(logouts).toBe(1);
    expect(dialogs).toEqual(['confirm']);
  });
}

test('failed logout retains unsaved navigation protection', async ({ page }) => {
  await login(page);
  await page.goto(`/apps/${app}/campaigns/new`);
  await page.getByLabel('Notification title').fill('Keep this draft');
  await page.route('**/bff/logout', (route) =>
    route.fulfill({ status: 503, contentType: 'application/json', body: '{}' }),
  );
  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', { name: 'Sign out', exact: true }).click();
  await expect(page.getByText('Sign-out failed')).toBeVisible();
  page.once('dialog', (dialog) => dialog.dismiss());
  await page.getByRole('link', { name: 'All campaigns', exact: true }).click();
  await expect(page).toHaveURL(/\/campaigns\/new$/);
  await expect(page.getByLabel('Notification title')).toHaveValue('Keep this draft');
  expect((await (await page.request.get('/bff/session')).json()).authenticated).toBe(true);
});

test('schedule remains locked while pending and unlocks after held acceptance', async ({
  page,
}) => {
  await login(page);
  await page.goto(campaign);
  await page.getByRole('button', { name: 'Schedule delivery', exact: true }).click();
  const date = page.getByRole('textbox', { name: 'Delivery time Required' });
  const time = page.getByRole('textbox', { name: 'Delivery time time' });
  const future = new Date();
  const dateValue = `${future.getFullYear() + 1}-01-15`;
  const timeValue = '10:30';
  await date.fill(dateValue);
  await time.fill(timeValue);
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(`**/bff/api${campaign}/schedule`, async (route) => {
    await gate;
    await route.fulfill({ status: 503, contentType: 'application/json', body: '{}' });
  });
  const request = page.waitForRequest((item) => item.url().endsWith('/schedule'));
  await page.getByRole('button', { name: 'Confirm schedule' }).click();
  const submitted = await request;
  try {
    await expect(date).toBeDisabled();
    await expect(time).toBeDisabled();
    await expect(date).toHaveValue(dateValue);
    await expect(time).toHaveValue(timeValue);
    expect(submitted.postDataJSON().scheduled_at).toBe(
      await date.evaluate(
        (input: HTMLInputElement, timeInput) =>
          new Date(`${input.value}T${timeInput}`).toISOString(),
        timeValue,
      ),
    );
    await expect(page.getByRole('button', { name: 'Processing…' })).toBeDisabled();
  } finally {
    release();
  }
  await expect(page.getByRole('alert').filter({ hasText: 'Dispatch is held' })).toContainText(
    'Dispatch is held',
  );
  await expect(date).toBeEnabled();
  await expect(time).toBeEnabled();
  await expect(date).toHaveValue(dateValue);
  await expect(time).toHaveValue(timeValue);
});

test('draft results skip delivery requests and refresh into real result states', async ({
  page,
}) => {
  await login(page);
  let status = 'DRAFT';
  let resultRequests = 0;
  await page.route(`**/bff/api${campaign}`, async (route) => {
    const response = await route.fetch();
    const payload = await response.json();
    payload.data.status = status;
    await route.fulfill({ response, json: payload });
  });
  page.on('request', (request) => {
    if (/\/(statistics|deliveries|reads)(\?|$)/.test(request.url())) resultRequests++;
  });
  await page.goto(`${campaign}/results`);
  await expect(page.getByRole('heading', { name: 'Campaign has not been sent' })).toBeVisible();
  expect(resultRequests).toBe(0);
  await expect(page.getByRole('link', { name: 'Review campaign' })).toHaveAttribute(
    'href',
    campaign,
  );
  await expect(page.getByText('A message arrived, but no receipt is shown?')).toHaveCount(0);
  status = 'QUEUED';
  await page.getByRole('button', { name: 'Refresh' }).click();
  await expect(
    page.getByRole('status').filter({ hasText: 'Statistics are incomplete' }),
  ).toBeVisible();
  await expect(page.getByRole('heading', { name: 'No delivery records yet' })).toBeVisible();
  expect(resultRequests).toBeGreaterThanOrEqual(2);
  await page.getByRole('radio', { name: 'Opens', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'No open records yet' })).toBeVisible();
});

test('campaign lookup errors stay retryable without requesting delivery data', async ({ page }) => {
  await login(page);
  let unavailable = true;
  let results = 0;
  await page.route(`**/bff/api${campaign}`, async (route) => {
    if (unavailable)
      await route.fulfill({ status: 502, contentType: 'application/json', body: '{}' });
    else await route.continue();
  });
  page.on('request', (request) => {
    if (/\/(statistics|deliveries|reads)(\?|$)/.test(request.url())) results++;
  });
  await page.goto(`${campaign}/results`);
  await expect(page.getByText('Unable to complete this request')).toBeVisible();
  expect(results).toBe(0);
  unavailable = false;
  await page.getByRole('button', { name: 'Retry' }).click();
  await expect(page.getByRole('heading', { name: 'Campaign has not been sent' })).toBeVisible();
});

for (const theme of ['light', 'dark']) {
  for (const width of [1440, 390]) {
    test(`campaign actions remain reachable at ${width}px in ${theme}`, async ({ page }) => {
      await page.setViewportSize({ width, height: 844 });
      await page.addInitScript((value) => localStorage.setItem('hello.theme', value), theme);
      await login(page);
      await page.goto(`/apps/${app}/campaigns/new`);
      await expect(page.getByLabel('Notification title')).toBeVisible();
      const save = page.getByRole('button', { name: 'Save draft', exact: true });
      await expect(save).toBeInViewport({ ratio: 1 });
      await page.getByRole('button', { name: 'Penerima Uji #42', exact: true }).click();
      await expect(save).toBeInViewport({ ratio: 1 });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      await page.screenshot({ path: `test-results/polish-editor-${width}-${theme}.png` });
      page.once('dialog', (dialog) => dialog.accept());
      await page.getByRole('link', { name: 'All campaigns', exact: true }).click();
      await expect(page).toHaveURL(`/apps/${app}/campaigns`);
      await page.goto(campaign);
      await expect(
        page.getByRole('button', { name: 'Schedule delivery', exact: true }),
      ).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      await page.screenshot({
        path: `test-results/polish-detail-${width}-${theme}.png`,
        fullPage: true,
      });
    });
  }
}
