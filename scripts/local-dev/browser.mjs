// Read-only real-local-stack check. Never runs against staging or submits a send.
import { createRequire } from 'node:module';
import { readFileSync, mkdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
const require = createRequire(new URL('../../web/dashboard/package.json', import.meta.url));
const { chromium } = require('@playwright/test');
const root = new URL('../../', import.meta.url);
const settings = JSON.parse(readFileSync(new URL('tmp/local-dev/config/settings.json', root)));
const app = JSON.parse(readFileSync(new URL('tmp/local-dev/config/generated/application.json', root)));
const browser = await chromium.launch({headless: true});
try {
  const page = await browser.newPage();
  let runtimeError = false;
  page.on('pageerror', () => { runtimeError = true; });
  await page.goto('http://127.0.0.1:5173/login');
  await page.locator('input[name="email"]').fill(settings.email);
  await page.locator('input[name="password"]').fill(settings.password);
  const signIn = page.getByRole('button', {name: 'Sign in', exact: true});
  await signIn.click();
  await page.getByRole('heading', {name: 'Choose application', exact: true}).waitFor();
  const screens = [
    ['overview', 'Overview'],
    ['campaigns', 'Campaigns'],
    ['templates', 'Templates'],
    ['segments', 'Segments'],
    ['audience', 'Users and devices'],
    ['logs', 'Delivery logs'],
    ['settings', 'My account'],
  ];
  for (const [route, heading] of screens) {
    await page.goto(`http://127.0.0.1:5173/apps/${app.id}/${route}`);
    await page.getByRole('heading', {name: heading, exact: true, level: 1}).waitFor();
    const alerts = (await page.getByRole('alert').allTextContents()).filter((value) => value.trim());
    if (alerts.length) throw new Error(`Unexpected alert on ${route}: ${alerts.join(' | ')}`);
  }
  const output = fileURLToPath(new URL('tmp/local-dev/screenshots/', root));
  mkdirSync(output, {recursive: true});
  await page.screenshot({path: `${output}/settings.png`, fullPage: true});
  await page.goto(`http://127.0.0.1:5173/apps/${app.id}/campaigns`);
  await page.getByRole('heading', {name: 'Campaigns', exact: true, level: 1}).waitFor();
  await page.screenshot({path: `${output}/campaigns.png`, fullPage: true});
  await page.getByRole('button', {name: 'Account menu', exact: true}).click();
  await page.getByRole('menuitem', {name: 'Sign out', exact: true}).click();
  await signIn.waitFor();
  if (runtimeError) throw new Error('Browser reported a runtime error');
  console.log(`Real local dashboard login, seven screens and logout passed. Screenshots: ${output}`);
} finally { await browser.close(); }
