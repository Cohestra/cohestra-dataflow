import { expect, test, type Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { randomUUID } from 'node:crypto';

// Regression checks for the Sept 25 UX/accessibility review (issues #51–#58).
// Fixtures are saved through the API and never run.
const artifacts = process.env.ACCEPTANCE_ARTIFACTS ?? resolve(__dirname, '../../.artifacts/local-acceptance');
const credentials = JSON.parse(readFileSync(resolve(artifacts, 'credentials.json'), 'utf8'));
const original = JSON.parse(readFileSync(resolve(artifacts, 'smoke.json'), 'utf8')).pipeline.definition;

let headers: Record<string, string>;
let page: Page;

// Password login is rate limited and refresh tokens rotate, so the suite
// shares one signed-in page and runs serially.
test.describe.configure({ mode: 'serial' });

test.beforeAll(async ({ browser }) => {
  page = await browser.newPage({ baseURL: test.info().project.use.baseURL, viewport: { width: 1440, height: 1000 } });
  await page.goto('/login');
  await page.getByLabel('Email', { exact: true }).fill(credentials.email);
  await page.getByLabel('Password', { exact: true }).fill(credentials.password);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.getByLabel('Pipeline name')).toBeVisible();
});

test.afterAll(async () => { await page.close(); });

// The app refreshes on load (rotating the cookie); reuse the token it receives.
async function login(page: Page) {
  const refreshed = page.waitForResponse(r => new URL(r.url()).pathname === '/api/auth/refresh');
  await page.goto('/pipelines');
  const session = await refreshed;
  expect(session.ok()).toBe(true);
  headers = { Authorization: `Bearer ${(await session.json()).accessToken}` };
}

async function seed(page: Page, saves = 1) {
  const definition = structuredClone(original);
  definition.id = randomUUID();
  definition.name = `Issue check ${definition.id.slice(-8)}`;
  delete definition.notifications;
  let row: any;
  for (let i = 0; i < saves; i++) {
    const saved = await page.request.post('/api/pipelines', { headers, data: definition });
    expect(saved.ok()).toBe(true);
    row = await saved.json();
  }
  return { ...row, name: definition.name as string };
}

async function openCanvas(page: Page, rowId: string, name: string) {
  await page.goto(`/?pipeline=${rowId}`);
  await expect(page.getByLabel('Pipeline name')).toHaveValue(name);
  await expect(page.locator('#pipeline-action-status')).toHaveText(/Loaded v\d+/);
}

const node = (page: Page, id: string) => page.locator(`.react-flow__node[data-id="${id}"]`);

test.beforeEach(async () => {
  await page.unrouteAll();
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.emulateMedia({ colorScheme: 'light' });
  await login(page);
});

test('#51 leaving a dirty draft offers Stay / Discard / Save; clean drafts leave freely', async () => {
  const row = await seed(page);
  await openCanvas(page, row.rowId, row.name);
  // Clean: no prompt.
  await page.getByRole('button', { name: 'All pipelines' }).click();
  await expect(page).toHaveURL(/\/pipelines$/);

  // Reopen through the app so browser Back stays inside this document.
  await page.getByPlaceholder('Search…').fill(row.name);
  await page.getByRole('button', { name: new RegExp(row.name) }).click();
  await page.getByRole('button', { name: 'Edit', exact: true }).click();
  await expect(page.getByLabel('Pipeline name')).toHaveValue(row.name);
  await page.getByLabel('Pipeline name').fill(`${row.name} edited`);
  expect(await page.evaluate(() => {
    const event = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(event);
    return event.defaultPrevented;
  })).toBe(true);

  const dialog = page.getByRole('alertdialog', { name: 'Unsaved changes' });
  await page.getByRole('button', { name: 'All pipelines' }).click();
  await expect(dialog).toBeVisible();
  await expect(page.getByRole('button', { name: 'Stay' })).toBeFocused();
  await page.getByRole('button', { name: 'Stay' }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByLabel('Pipeline name')).toHaveValue(`${row.name} edited`);

  // Browser back is guarded too.
  await page.goBack();
  await expect(dialog).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByLabel('Pipeline name')).toHaveValue(`${row.name} edited`);

  await page.getByRole('button', { name: 'All pipelines' }).click();
  await page.getByRole('button', { name: 'Discard changes' }).click();
  await expect(page).toHaveURL(/\/pipelines$/);
  await openCanvas(page, row.rowId, row.name);

  await page.getByLabel('Pipeline name').fill(`${row.name} saved`);
  await page.getByRole('button', { name: 'All pipelines' }).click();
  const saving = page.waitForResponse(r => new URL(r.url()).pathname === '/api/pipelines' && r.request().method() === 'POST');
  await page.getByRole('button', { name: 'Save and leave' }).click();
  const saved = await (await saving).json();
  await expect(page).toHaveURL(/\/pipelines$/);
  const stored = await (await page.request.get(`/api/pipelines/${saved.rowId}`, { headers })).json();
  expect(stored.definition.name).toBe(`${row.name} saved`);
});

test('#51 a failed save keeps the user on the draft', async () => {
  const row = await seed(page);
  await openCanvas(page, row.rowId, row.name);
  await page.getByLabel('Pipeline name').fill(`${row.name} will fail`);
  await page.route('**/api/pipelines', route => route.request().method() === 'POST'
    ? route.fulfill({ status: 500, body: '{"error":"boom"}' }) : route.continue());
  await page.getByRole('button', { name: 'All pipelines' }).click();
  await page.getByRole('button', { name: 'Save and leave' }).click();
  await expect(page.getByRole('alertdialog')).toBeHidden();
  await expect(page).toHaveURL(/\/\?pipeline=/);
  await expect(page.getByLabel('Pipeline name')).toHaveValue(`${row.name} will fail`);
  await expect(page.locator('#pipeline-action-status')).toContainText('Save failed');
});

test('#52 Enter and Space open focused node settings; Escape returns focus to the node', async () => {
  const row = await seed(page);
  await openCanvas(page, row.rowId, row.name);
  const inspector = page.getByRole('complementary', { name: 'Inspector' });
  for (const [id, key] of [['src', 'Enter'], ['fil', ' '], ['snk', 'Enter']] as const) {
    await node(page, id).focus();
    await page.keyboard.press(key === ' ' ? 'Space' : key);
    await expect(inspector.getByText('Node settings')).toBeVisible();
    await expect(inspector.getByLabel('Label', { exact: true })).toBeFocused();
    await page.keyboard.type(' (kbd)');
    await page.keyboard.press('Escape');
    await expect(inspector.getByText('Node settings')).toBeHidden();
    await expect(node(page, id)).toBeFocused();
    await expect(node(page, id)).toContainText('(kbd)');
  }
  // Pointer still works.
  await node(page, 'src').click();
  await expect(inspector.getByText('Node settings')).toBeVisible();
});

test('#53 every node setting control has a programmatic name', async () => {
  const row = await seed(page);
  await openCanvas(page, row.rowId, row.name);
  const inspector = page.getByRole('complementary', { name: 'Inspector' });
  for (const id of ['src', 'fil', 'snk']) {
    await node(page, id).click();
    await expect(inspector.getByText('Node settings')).toBeVisible();
    const unnamed = await inspector.evaluate(root => [...root.querySelectorAll('input, select, textarea')]
      .filter(el => !(el as HTMLInputElement).labels?.length && !el.getAttribute('aria-label') && !el.getAttribute('aria-labelledby'))
      .map(el => el.outerHTML.slice(0, 80)));
    expect(unnamed, `unnamed controls in ${id}`).toEqual([]);
  }
  await node(page, 'src').click();
  for (const name of ['Database connection', 'Source table', 'Sync mode', 'Incremental cursor column', 'Cursor type', 'Ingestion mode']) {
    await expect(inspector.getByLabel(name, { exact: true })).toBeVisible();
  }
  await inspector.getByText('Source table', { exact: true }).click();
  await expect(inspector.getByLabel('Source table', { exact: true })).toBeFocused();
});

for (const width of [1280, 800]) {
  test(`#54 current invalid/dirty state is visible at ${width}px`, async () => {
    await page.setViewportSize({ width, height: 900 });
    const row = await seed(page);
    await openCanvas(page, row.rowId, row.name);
    await node(page, 'snk').click();
    await page.getByRole('button', { name: 'Delete node' }).click();
    const visibleStatus = page.getByText('Pipeline needs at least one sink node.', { exact: true }).last();
    await expect(visibleStatus).toBeVisible();
    await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeDisabled();
  });
}

test('#54 edits after load or save report unsaved changes', async () => {
  const row = await seed(page);
  await openCanvas(page, row.rowId, row.name);
  await page.getByLabel('Pipeline name').fill(`${row.name} renamed`);
  await expect(page.locator('#pipeline-action-status')).toHaveText(/Unsaved changes/);
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.locator('#pipeline-action-status')).toHaveText(/Saved v\d+/);
  await page.getByLabel('Pipeline name').fill(`${row.name} renamed again`);
  await expect(page.locator('#pipeline-action-status')).toHaveText(/Unsaved changes/);
});

test('#55 rows with a run never say Never run; cancelled runs are labelled', async () => {
  await page.goto('/pipelines');
  await expect(page.getByText(/pipelines? loaded/)).toBeVisible();
  const rows = await page.locator('button:has(> div.w-\\[3px\\])').evaluateAll(buttons => buttons.map(b => b.textContent ?? ''));
  expect(rows.length).toBeGreaterThan(0);
  for (const text of rows) {
    if (text.includes('Never run')) expect(text, text).toMatch(/Never run—/);
  }
  const listed = await page.request.get('/api/pipelines?view=current&limit=200', { headers });
  const cancelled = (await listed.json()).rows.find((r: any) => r.last_run_phase === 'cancelled');
  test.skip(!cancelled, 'no cancelled execution in this dataset');
  await page.getByPlaceholder('Search…').fill(cancelled.name);
  await expect(page.getByRole('button', { name: new RegExp(cancelled.name) }).first()).toContainText('Cancelled');
});

test('#56 repeated saves list as one current pipeline with version history', async () => {
  const row = await seed(page, 3);
  await page.goto('/pipelines');
  await page.getByPlaceholder('Search…').fill(row.name);
  const rows = page.getByRole('button', { name: new RegExp(row.name) });
  await expect(rows).toHaveCount(1);
  await expect(rows).toContainText('v3 of 3');
  await rows.click();
  await expect(page.getByText('Versions')).toBeVisible();
  for (const v of [1, 2, 3]) await expect(page.getByRole('button', { name: `Open version ${v} in editor` })).toBeVisible();
  await page.getByRole('button', { name: 'Open version 1 in editor' }).click();
  await expect(page.locator('#pipeline-action-status')).toHaveText('Loaded v1');
});

test('#57 pipeline status and timestamp text meets 4.5:1 in dark and light themes', async () => {
  const row = await seed(page);
  for (const scheme of ['dark', 'light'] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.goto('/pipelines');
    await page.getByPlaceholder('Search…').fill(row.name);
    const target = page.getByRole('button', { name: new RegExp(row.name) });
    await expect(target).toContainText('Never run');
    const ratios = await target.evaluate(button => {
      const parse = (c: string) => (c.match(/[\d.]+/g) ?? []).map(Number);
      const bg = (el: Element | null): number[] => {
        const layers: number[][] = [];
        for (let e = el; e; e = e.parentElement) {
          const [r, g, b, a = 1] = parse(getComputedStyle(e).backgroundColor);
          if (a > 0) layers.unshift([r, g, b, a]);
          if (a === 1) break;
        }
        return layers.reduce((acc, [r, g, b, a]) => [r * a + acc[0] * (1 - a), g * a + acc[1] * (1 - a), b * a + acc[2] * (1 - a)], [255, 255, 255]);
      };
      const lum = ([r, g, b]: number[]) => [r, g, b].map(v => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; })
        .reduce((s, v, i) => s + v * [0.2126, 0.7152, 0.0722][i], 0);
      return [...button.querySelectorAll('span, div')].filter(el => el.children.length === 0 && el.textContent?.trim()).map(el => {
        const back = bg(el);
        const [r, g, b, a = 1] = parse(getComputedStyle(el).color);
        const fore = [r * a + back[0] * (1 - a), g * a + back[1] * (1 - a), b * a + back[2] * (1 - a)];
        const [l1, l2] = [lum(fore), lum(back)].sort((x, y) => y - x);
        return { text: el.textContent!.trim(), ratio: Math.round(((l1 + 0.05) / (l2 + 0.05)) * 100) / 100 };
      });
    });
    for (const { text, ratio } of ratios) expect(ratio, `${scheme}: "${text}"`).toBeGreaterThanOrEqual(4.5);
  }
});

test('#58 a failed AI refinement clears loading and shows recovery copy', async () => {
  const row = await seed(page);
  await openCanvas(page, row.rowId, row.name);
  await page.route('**/api/ai/refine', route => route.fulfill({
    status: 422, contentType: 'application/json',
    body: '{"error":"could not refine pipeline: model response invalid after one repair: pipeline name is required"}',
  }));
  await page.getByRole('button', { name: 'Quick AI add' }).click();
  await page.getByLabel('AI pipeline request').fill('Change the sink collection to archive');
  await page.getByRole('button', { name: 'Refine' }).click();
  await expect(page.getByText(/could not produce a valid proposal.*Your pipeline is unchanged/)).toBeVisible();
  await expect(page.getByText(/\{"error"|422/)).toHaveCount(0);
  await expect(page.locator('#pipeline-action-status')).not.toHaveText(/Refining/);
  await expect(page.getByLabel('AI pipeline request')).toHaveValue('Change the sink collection to archive');
  await expect(page.getByLabel('Pipeline name')).toHaveValue(row.name);
});
