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
  const failures = [];
  page.on('pageerror', error => failures.push(error.message));
  await page.goto('http://127.0.0.1:5173/login');
  await page.getByLabel('Email', {exact: true}).fill(settings.email);
  await page.getByLabel('Kata sandi', {exact: true}).fill(settings.password);
  await page.getByRole('button', {name: 'Masuk ke dashboard'}).click();
  await page.getByRole('heading', {name: 'Pilih aplikasi'}).waitFor();
  for (const route of ['overview', 'campaigns', 'templates', 'segments', 'audience', 'logs', 'settings']) {
    await page.goto(`http://127.0.0.1:5173/apps/${app.id}/${route}`);
    await page.getByRole('heading', {level: 1}).waitFor();
    if (await page.getByRole('alert').count()) throw new Error(`Unexpected error on ${route}`);
  }
  const output = fileURLToPath(new URL('tmp/local-dev/screenshots/', root));
  mkdirSync(output, {recursive: true});
  await page.screenshot({path: `${output}/settings.png`, fullPage: true});
  await page.goto(`http://127.0.0.1:5173/apps/${app.id}/campaigns`);
  await page.getByRole('heading', {name: 'Campaign', exact: true}).waitFor();
  await page.screenshot({path: `${output}/campaigns.png`, fullPage: true});
  await page.getByRole('button', {name: 'Keluar', exact: true}).click();
  await page.getByRole('button', {name: 'Masuk ke dashboard'}).waitFor();
  if (failures.length) throw new Error('Browser reported a runtime error');
  console.log(`Real local dashboard login, seven screens and logout passed. Screenshots: ${output}`);
} finally { await browser.close(); }
