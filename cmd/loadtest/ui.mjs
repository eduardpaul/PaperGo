// Drives the PaperGo web UI against a server seeded by `loadtest seed` and times what people do with very large
// lists and libraries. Usage: node cmd/loadtest/ui.mjs BASE_URL TOKEN STATE_JSON OUT_DIR (needs the playwright package).
import { chromium } from 'playwright';
import { readFileSync, writeFileSync } from 'node:fs';

const [base, token, statePath, out] = process.argv.slice(2);
const state = JSON.parse(readFileSync(statePath, 'utf8'));
const results = [];
const browser = await chromium.launch(process.env.CHROMIUM ? { executablePath: process.env.CHROMIUM } : {});
const context = await browser.newContext({ viewport: { width: 1400, height: 900 } });
const page = await context.newPage();
const failures = [];
const requests = { batch: 0, terms: 0, other: 0 };
page.on('request', (request) => {
  const url = request.url();
  if (url.includes('/v1/$batch')) requests.batch++;
  else if (url.includes('/v1/terms')) requests.terms++;
  else if (url.includes('/v1/')) requests.other++;
});
const resetRequests = () => Object.assign(requests, { batch: 0, terms: 0, other: 0 });
page.on('pageerror', (error) => failures.push(`pageerror: ${error.message}`));
page.on('response', (response) => {
  if (response.status() >= 500) failures.push(`${response.status()} ${response.url()}`);
});

async function step(name, fn, { shot = true } = {}) {
  const started = Date.now();
  let note = '';
  try {
    note = (await fn()) ?? '';
  } catch (error) {
    note = `FAILED: ${String(error.message ?? error).split('\n')[0]}`;
  }
  const ms = Date.now() - started;
  results.push({ name, ms, note });
  console.log(`${name.padEnd(58)} ${String(ms).padStart(7)} ms  ${note}`);
  if (shot) await page.screenshot({ path: `${out}/${String(results.length).padStart(2, '0')}-${name.replace(/[^a-z0-9]+/gi, '-').toLowerCase()}.png` });
}

const rows = () => page.locator('tbody tr');
const footer = async () => (await page.locator('text=/\\d+\\+? (of \\d+ )?entr/').first().textContent().catch(() => ''))?.trim();
const list = `/w/${state.workspace}/l/${state.list}`;
const library = `/w/${state.workspace}/l/${state.library}`;

await page.goto(`${base}/login`);
await page.locator('input').first().fill(token);
await page.keyboard.press('Enter');
await page.waitForURL((url) => !url.pathname.startsWith('/login'));

await step('Home', async () => {
  await page.goto(`${base}/`);
  await page.getByText('Workspaces').first().waitFor();
});
await step('Workspace overview', async () => {
  await page.goto(`${base}/w/${state.workspace}`);
  await page.getByText('Invoices').first().waitFor();
});
await step('Open the list (default view)', async () => {
  await page.goto(`${base}${list}?view=none`);
  await rows().first().waitFor({ timeout: 60000 });
  return `${await rows().count()} rows, footer "${await footer()}"`;
});
await step('Load more x10', async () => {
  resetRequests();
  for (let i = 0; i < 10; i++) {
    const before = await rows().count();
    await page.getByRole('button', { name: /Load more/ }).click();
    await page.waitForFunction((n) => document.querySelectorAll('tbody tr').length > n, before, { timeout: 60000 });
  }
  await page.waitForTimeout(2000);
  return `${await rows().count()} rows in the DOM; requests: ${requests.batch} $batch name lookups, ${requests.other} other`;
});
await step('Sort by Amount (descending)', async () => {
  await page.goto(`${base}${list}?view=none`);
  await rows().first().waitFor();
  await page.getByRole('button', { name: 'Amount' }).click();
  await page.waitForURL(/sort=amount/);
  await page.getByRole('button', { name: 'Amount' }).click();
  await page.waitForURL(/sort=-amount/);
  await page.waitForResponse((r) => r.url().includes('/query'), { timeout: 60000 }).catch(() => {});
  await rows().first().waitFor();
  return `first row: ${(await rows().first().textContent())?.slice(0, 80)}; footer "${await footer()}"`;
});
await step('Search as you type "Acm" (word start)', async () => {
  await page.goto(`${base}${list}?view=none`);
  await rows().first().waitFor();
  await page.getByLabel('Search this list').fill('Acm');
  await page.waitForURL(/q=Acm/, { timeout: 10000 });
  await page.waitForTimeout(1500);
  return `${await rows().count()} rows`;
});
await step('Search "Acme" (whole word)', async () => {
  await page.getByLabel('Search this list').fill('Acme');
  await page.waitForURL(/q=Acme/, { timeout: 10000 });
  await rows().first().waitFor({ timeout: 60000 });
  return `${await rows().count()} rows, footer "${await footer()}"`;
});
await step('Saved view "Open invoices" (filter + sort + total)', async () => {
  await page.goto(`${base}${list}?view=none`);
  await rows().first().waitFor();
  await page.getByRole('tab', { name: 'Open invoices' }).click();
  await page.waitForResponse((r) => r.url().includes('/query'), { timeout: 60000 });
  await rows().first().waitFor({ timeout: 60000 });
  return `footer "${await footer()}"`;
});
await step('Board view "By status"', async () => {
  await page.getByRole('tab', { name: 'By status' }).click();
  await page.locator('section[aria-label]').first().waitFor({ timeout: 60000 });
  const columns = await page.locator('main section[aria-label] h3').allTextContents();
  return columns.join(' | ');
});
await step('Calendar view "Due calendar"', async () => {
  await page.getByRole('tab', { name: 'Due calendar' }).click();
  await page.getByRole('button', { name: 'Today' }).waitFor({ timeout: 60000 });
  const shown = await page.locator('main .grid-cols-7 li').count();
  return `${shown} items shown in the visible month`;
});
await step('Select all and open Edit a field', async () => {
  await page.goto(`${base}${list}?view=none`);
  await rows().first().waitFor();
  await page.getByLabel('Select all').check();
  const selected = await page.getByText(/\d+ selected/).textContent();
  await page.getByRole('button', { name: 'Edit a field' }).click();
  const title = await page.getByRole('dialog').getByRole('heading').first().textContent();
  await page.keyboard.press('Escape');
  return `${selected}; dialog: ${title}`;
});
await step('Bulk edit 100 selected items', async () => {
  await page.goto(`${base}${list}?view=none`);
  await rows().first().waitFor();
  await page.getByRole('button', { name: /Load more/ }).click();
  await page.waitForFunction(() => document.querySelectorAll('tbody tr').length > 50, null, { timeout: 30000 });
  await page.getByLabel('Select all').check();
  await page.getByRole('button', { name: 'Edit a field' }).click();
  await page.getByRole('dialog').getByRole('button', { name: /Apply/ }).click();
  const toast = await page.locator('[data-sonner-toast]').first().textContent({ timeout: 15000 }).catch(() => 'no message');
  await page.keyboard.press('Escape');
  return `result: ${toast}`;
});
await step('Open an item', async () => {
  await rows().nth(1).click();
  await page.getByRole('dialog').getByLabel('Name').waitFor({ timeout: 30000 });
  const supplier = page.getByRole('dialog').getByLabel('Supplier');
  await page.waitForFunction(() => (document.querySelector('[role=dialog] select#field-supplier')?.options.length ?? 0) > 2, null, { timeout: 15000 }).catch(() => {});
  const options = await supplier.locator('option').count();
  const chosen = await supplier.evaluate((el) => el.options[el.selectedIndex]?.textContent ?? '');
  return `Supplier picker offers ${options - 1} of ${state.supplier_ids.length} suppliers; shows "${chosen}"`;
});
await page.keyboard.press('Escape');
await step('Column picker: show Supplier', async () => {
  await page.goto(`${base}${list}?view=none`);
  await rows().first().waitFor();
  const before = (await page.locator('thead th').allTextContents()).filter(Boolean).join(', ');
  await page.getByRole('button', { name: /Columns/ }).click();
  await page.getByRole('checkbox', { name: 'Supplier' }).check();
  await page.keyboard.press('Escape');
  await page.locator('thead th', { hasText: 'Supplier' }).waitFor({ timeout: 30000 });
  return `default columns: ${before}`;
});
await step('Open the library (root: folders)', async () => {
  await page.goto(`${base}${library}?view=none`);
  await rows().first().waitFor({ timeout: 60000 });
  return `${await rows().count()} rows, footer "${await footer()}"`;
});
await step('Open a folder, then a subfolder', async () => {
  await rows().first().click();
  await page.getByRole('navigation', { name: 'Folders' }).waitFor();
  await rows().first().waitFor();
  await rows().first().click();
  await page.waitForFunction(() => document.querySelectorAll('nav[aria-label="Folders"] [aria-current]').length > 0);
  await page.waitForTimeout(500);
  await rows().first().waitFor({ timeout: 60000 });
  return `${await rows().count()} rows, footer "${await footer()}"`;
});
await step('Library: open root again (no saved view)', async () => {
  await page.goto(`${base}${library}`);
  await rows().first().waitFor({ timeout: 60000 });
  return `footer "${await footer()}"`;
});
await step('Move dialog: list root folders', async () => {
  await page.goto(`${base}${library}?item=${state.sample_docs[0]}`);
  await page.getByRole('button', { name: 'Item actions' }).click();
  await page.getByRole('menuitem', { name: 'Move…' }).click();
  await page.getByRole('dialog', { name: /Move/ }).getByRole('button', { name: /\d{4} / }).first().waitFor({ timeout: 60000 });
  const shown = await page.getByRole('dialog', { name: /Move/ }).getByRole('button', { name: /\d{4} / }).count();
  return `${shown} folders shown`;
});
await page.keyboard.press('Escape');
await step('File tab: versions', async () => {
  await page.goto(`${base}${library}?item=${state.sample_docs[0]}&tab=content`);
  await page.getByText('Version 1').waitFor({ timeout: 30000 });
});
await step('Recycle bin', async () => {
  await page.goto(`${base}${library}/recycle-bin`);
  await page.getByRole('heading', { name: 'Recycle bin' }).waitFor();
  await page.waitForTimeout(1000);
  return `${await page.locator('main li').count()} entries shown`;
});
await step('Workspace tags page', async () => {
  await page.goto(`${base}/w/${state.workspace}/tags`);
  await page.getByRole('button', { name: /priority/ }).waitFor({ timeout: 60000 });
  await page.getByRole('button', { name: /priority/ }).click();
  await page.locator('main ul li').first().waitFor({ timeout: 60000 });
  return `${await page.locator('main ul li').count()} resources listed for "priority"`;
});
await step('Global search "Acme"', async () => {
  await page.goto(`${base}/search?q=Acme`);
  await page.waitForTimeout(3000);
});

writeFileSync(`${out}/ui-results.json`, JSON.stringify({ results, failures }, null, 2));
console.log(failures.length ? `Failures:\n${failures.join('\n')}` : 'No page errors or server errors.');
await browser.close();
