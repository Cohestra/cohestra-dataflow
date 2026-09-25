import { expect, test, type Page, type TestInfo } from '@playwright/test';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { randomUUID } from 'node:crypto';
import { isDeepStrictEqual } from 'node:util';

const artifacts = resolve(__dirname, '../../.artifacts/local-acceptance');
const credentials = JSON.parse(readFileSync(resolve(artifacts, 'credentials.json'), 'utf8'));
const original = JSON.parse(readFileSync(resolve(artifacts, 'smoke.json'), 'utf8')).pipeline.definition;

async function seed(page: Page, info: TestInfo) {
  await page.goto('/login');
  await page.getByLabel('Email', { exact: true }).fill(credentials.email);
  await page.getByLabel('Password', { exact: true }).fill(credentials.password);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.getByLabel('Pipeline name')).toBeVisible();
  const session = await page.request.post('/api/auth/refresh');
  expect(session.ok()).toBe(true);
  const headers = { Authorization: `Bearer ${(await session.json()).accessToken}` };
  // Keep password entry out of traces. Remaining local session traces stay private.
  await page.context().tracing.start({ screenshots: true, snapshots: true });
  const definition = structuredClone(original);
  definition.id = randomUUID();
  definition.name = `Local review ${definition.id.slice(-8)}`;
  definition.version = 0;
  definition.notifications = { connectionId: randomUUID(), minimumSeverity: 'warning' };
  const asset = { urn: 'postgres://local/acceptance.records', platform: 'postgres', namespace: 'acceptance', name: 'records', type: 'table', layer: 'silver' };
  definition.nodes[0].outputAssets = [asset];
  definition.nodes[1].inputAssets = [asset];
  definition.nodes[1].outputAssets = [asset];
  definition.nodes[2].inputAssets = [asset];
  definition.nodes[0].activityType = 'http.fetch';
  definition.nodes[0].label = 'Local HTTP records';
  definition.nodes[0].config = {
    url: 'https://example.com/records',
  };
  // The notification binding is an inert local identifier; these review fixtures never run.
  const saved = await page.request.post('/api/pipelines', { headers, data: definition });
  expect(saved.ok(), 'save isolated review fixture').toBe(true);
  const row = await saved.json();
  const stored = await page.request.get(`/api/pipelines/${row.rowId}`, { headers });
  expect(stored.ok()).toBe(true);
  const base = (await stored.json()).definition;
  await page.goto(`/?pipeline=${row.rowId}&ai=1`);
  await expect(page.getByLabel('Pipeline name')).toHaveValue(base.name);
  writeFileSync(info.outputPath('fixture.json'), JSON.stringify({ rowId: row.rowId, definition: base }, null, 2), { mode: 0o600 });
  return { base, headers };
}

async function save(page: Page, headers: Record<string, string>, expected: any) {
  const saving = page.waitForResponse(r => new URL(r.url()).pathname === '/api/pipelines' && r.request().method() === 'POST');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  const response = await saving;
  expect(response.ok()).toBe(true);
  const row = await response.json();
  await expect(page.locator('#pipeline-action-status')).toHaveText(`Saved v${row.version}`);
  const stored = await page.request.get(`/api/pipelines/${row.rowId}`, { headers });
  expect(stored.ok()).toBe(true);
  const actual = (await stored.json()).definition;
  expect(actual).toEqual({ ...expected, version: actual.version });
  return row;
}

async function refine(page: Page, prompt: string, info: TestInfo, name: string) {
  await page.getByLabel('AI pipeline request').fill(prompt);
  const responding = page.waitForResponse(r => new URL(r.url()).pathname === '/api/ai/refine', { timeout: 240_000 });
  await page.getByRole('button', { name: 'Refine', exact: true }).click();
  const response = await responding;
  const body = await response.json();
  writeFileSync(info.outputPath(`${name}.json`), JSON.stringify({ httpStatus: response.status(), ...body }, null, 2), { mode: 0o600 });
  expect(response.ok(), 'real Ollama refine API success').toBe(true);
  expect(body.status, 'real model must produce a ready proposal').toBe('ready');
  await expect(page.getByRole('region', { name: 'Review changes' })).toBeVisible();
  return body;
}

const cases: Array<{ name: string; passed: boolean; error?: string }> = [];
let modelScopePassed: boolean | null = null;

async function check(info: TestInfo, name: string, action: () => Promise<void>) {
  const previousErrors = info.errors.length;
  try {
    await test.step(name, action);
    cases.push({ name, passed: info.errors.length === previousErrors });
  } catch (error) {
    cases.push({ name, passed: false, error: String(error) });
    throw error;
  }
}

function reviewedDefinition(base: any, proposal: any, requested: any, info: TestInfo) {
  const reviewed = {
    ...base,
    execution: proposal.execution ?? base.execution,
    nodes: proposal.nodes.map((node: any) => {
      const old = base.nodes.find((n: any) => n.id === node.id && n.type === node.type && n.activityType === node.activityType);
      return { ...old, ...node };
    }),
    // These three-node fixtures preserve edge endpoints; stable canvas IDs survive a changed condition.
    edges: proposal.edges.map((edge: any) => ({ ...edge, id: base.edges.find((e: any) => e.source === edge.source && e.target === edge.target)?.id ?? edge.id })),
  };
  modelScopePassed = isDeepStrictEqual(reviewed, requested);
  writeFileSync(info.outputPath('model-scope.json'), JSON.stringify({
    priorStrictFailureRetained: true, passed: modelScopePassed, requested, actualReviewedDefinition: reviewed,
  }, null, 2), { mode: 0o600 });
  return reviewed;
}

test.beforeEach(() => { cases.length = 0; modelScopePassed = null; });
test.afterEach(async ({ context }, info) => {
  const outcome = { status: info.status, expectedStatus: info.expectedStatus, modelScopePassed, cases };
  writeFileSync(info.outputPath('outcome.json'), JSON.stringify(outcome, null, 2), { mode: 0o600 });
  const reportPath = resolve(artifacts, 'full-review/continuation-case-outcomes.json');
  const report = existsSync(reportPath) ? JSON.parse(readFileSync(reportPath, 'utf8')) : { priorModelScopePassed: false, promotionApproved: false, tests: {} };
  report.tests[info.title] = outcome;
  writeFileSync(reportPath, JSON.stringify(report, null, 2), { mode: 0o600 });
  await context.tracing.stop(info.status === info.expectedStatus ? {} : { path: info.outputPath('trace.zip') }).catch(() => {});
});

test('real Ollama Apply and Undo UI acceptance with separate model-scope verdict', async ({ page, context }, info) => {
  const { base, headers } = await seed(page, info);
  const requested = structuredClone(base);
  requested.nodes.find((n: any) => n.id === 'snk').config.collection = 'review_verified';
  let reviewed: any;
  await check(info, 'request one real proposal; record intent fidelity separately', async () => {
    const proposal = await refine(page,
      'Change ONLY node snk config.collection to review_verified. Keep all three node IDs, labels, activity types, every other config value and both edges exactly unchanged. Preserve all metadata, policies, asset bindings, notifications and ingestion settings. Return the complete graph; this is a review only and must not execute anything.', info, 'collection-proposal');
    reviewed = reviewedDefinition(base, proposal.definition, requested, info);
  });
  const review = page.getByRole('region', { name: 'Review changes' });
  await check(info, 'keyboard expansion and stale Apply protection', async () => {
    const summary = review.locator('summary').filter({ hasText: 'Node snk' });
    await summary.focus();
    await page.keyboard.press('Enter');
    await expect(review.getByText('Changed: config.collection', { exact: true })).toBeVisible();
    await expect(review.getByText('After: review_verified', { exact: true })).toBeVisible();
    await page.getByLabel('Pipeline name').fill(`${base.name} newer draft`);
    await expect(page.getByRole('button', { name: 'Apply', exact: true })).toBeDisabled();
    await expect(page.locator('#ai-proposal-stale')).toBeVisible();
    await page.getByLabel('Pipeline name').fill(base.name);
    await expect(page.getByRole('button', { name: 'Apply', exact: true })).toBeEnabled();
    await page.screenshot({ path: info.outputPath('proposal-reviewed.png'), fullPage: true });
  });
  await check(info, 'Apply saves the actual reviewed proposal and reopens in another tab', async () => {
    await page.setViewportSize({ width: 430, height: 932 });
    const applyBounds = await page.getByRole('button', { name: 'Apply', exact: true }).boundingBox();
    expect(applyBounds!.x).toBeGreaterThanOrEqual(0);
    expect(applyBounds!.x + applyBounds!.width).toBeLessThanOrEqual(430);
    await page.getByRole('button', { name: 'Apply', exact: true }).focus();
    await page.keyboard.press('Enter');
    await expect(page.getByLabel('AI pipeline request')).toBeFocused();
    await page.setViewportSize({ width: 1440, height: 1000 });
    const row = await save(page, headers, reviewed);
    const reopened = await context.newPage();
    await reopened.goto(`/?pipeline=${row.rowId}`);
    await expect(reopened.getByLabel('Pipeline name')).toHaveValue(base.name);
    await expect(reopened.locator('#pipeline-action-status')).toHaveText(`Loaded v${row.version}`);
    await reopened.locator('.react-flow__node').filter({ hasText: 'Managed records' }).click();
    await expect(reopened.locator('input[value="review_verified"]')).toHaveValue('review_verified');
    await reopened.screenshot({ path: info.outputPath('applied-reopened.png'), fullPage: true });
    await reopened.close();
  });
  await check(info, 'Undo restores original metadata, assets, notifications and config', async () => {
    await page.setViewportSize({ width: 430, height: 932 });
    const undoBounds = await page.getByRole('button', { name: 'Undo last apply', exact: true }).boundingBox();
    expect(undoBounds!.x).toBeGreaterThanOrEqual(0);
    expect(undoBounds!.x + undoBounds!.width).toBeLessThanOrEqual(430);
    await page.getByRole('button', { name: 'Undo last apply', exact: true }).focus();
    await page.keyboard.press('Enter');
    await expect(page.getByLabel('AI pipeline request')).toBeFocused();
    await page.setViewportSize({ width: 1440, height: 1000 });
    await save(page, headers, base);
    await page.screenshot({ path: info.outputPath('undo-restored.png'), fullPage: true });
  });
  await check(info, 'model obeyed the requested scope (independent of UI correctness)', async () => {
    expect(modelScopePassed, 'actual reviewed proposal must contain only the requested edit').toBe(true);
  });
});

test('real Ollama redaction and narrow Discard UI acceptance with separate model-scope verdict', async ({ page }, info) => {
  const { base, headers } = await seed(page, info);
  const review = page.getByRole('region', { name: 'Review changes' });
  await check(info, 'request one real URL proposal and verify redaction', async () => {
    const proposal = await refine(page,
      'Change ONLY source node src config.url from https://example.com/records to https://example.com/records?review=1. This is an inert review fixture which will never execute. Keep every node ID, label, activity type, other config field and edge exactly unchanged. Keep the original sink collection and preserve policies, bindings and pipeline metadata. Return the complete graph; do not execute it.', info, 'redaction-proposal');
    const redactedExpected = structuredClone(base);
    redactedExpected.nodes[0].config.url = 'https://example.com/records?review=1';
    reviewedDefinition(base, proposal.definition, redactedExpected, info);
    const sourceSummary = review.locator('summary').filter({ hasText: 'Node src' });
    await sourceSummary.focus();
    await page.keyboard.press('Enter');
    await expect(review.getByText('Changed: config.url', { exact: true })).toBeVisible();
    await expect(review.getByText('Before: Hidden', { exact: true })).toBeVisible();
    await expect(review.getByText('After: Hidden', { exact: true })).toBeVisible();
    expect(await review.textContent()).not.toMatch(/example\.com|https:\/\//);
    await page.screenshot({ path: info.outputPath('url-values-hidden.png'), fullPage: true });
  });
  await check(info, 'narrow review controls are visible and keyboard focusable', async () => {
    await page.setViewportSize({ width: 430, height: 932 });
    const discard = page.getByRole('button', { name: 'Discard', exact: true });
    await discard.focus();
    await expect(discard).toBeFocused();
    const bounds = await discard.boundingBox();
    expect(bounds).not.toBeNull();
    expect.soft(bounds!.x).toBeGreaterThanOrEqual(0);
    expect.soft(bounds!.x + bounds!.width).toBeLessThanOrEqual(430);
    await page.screenshot({ path: info.outputPath('narrow-review.png'), fullPage: true });
  });
  await check(info, 'Discard returns focus and leaves all persisted metadata unchanged', async () => {
    await page.keyboard.press('Enter');
    await expect(page.getByLabel('AI pipeline request')).toBeFocused();
    await expect(review).toHaveCount(0);
    await page.setViewportSize({ width: 1440, height: 1000 });
    await save(page, headers, base);
  });
  await check(info, 'model obeyed the requested scope (independent of UI correctness)', async () => {
    expect(modelScopePassed, 'actual reviewed proposal must contain only the requested edit').toBe(true);
  });
});

test('Mermaid harmless edit and structural cancellation/confirmation preserve surviving metadata', async ({ page }, info) => {
  const { base, headers } = await seed(page, info);
  await page.getByLabel('Close AI panel').click();
  await page.getByRole('button', { name: /Mermaid/i }).click();
  const editor = page.getByLabel('Mermaid diagram source');
  const safe = (await editor.inputValue()).replace('Active records', 'Reviewed active records');
  await editor.fill(safe);
  await page.getByRole('button', { name: 'Apply to canvas', exact: true }).click();
  const expected = structuredClone(base);
  expected.nodes.find((n: any) => n.id === 'fil').label = 'Reviewed active records';
  await save(page, headers, expected);

  await test.step('dismiss structural warning without changing the canvas', async () => {
    const structural = safe.split('\n').filter(line => !/\bfil\b/.test(line)).concat('  src --> snk').join('\n');
    await editor.fill(structural);
    const dialog = page.waitForEvent('dialog');
    const applying = page.getByRole('button', { name: 'Apply to canvas', exact: true }).click();
    const warning = await dialog;
    expect(warning.message()).toContain('removes settings and bindings for: fil');
    await warning.dismiss();
    await applying;
    await expect(page.locator('.react-flow__node')).toHaveCount(3);
    await save(page, headers, expected);
  });
  await test.step('confirm deliberate deletion and persist only the intended structural change', async () => {
    const dialog = page.waitForEvent('dialog');
    const applying = page.getByRole('button', { name: 'Apply to canvas', exact: true }).click();
    await (await dialog).accept();
    await applying;
    await expect(page.locator('.react-flow__node')).toHaveCount(2);
    const saving = page.waitForResponse(r => new URL(r.url()).pathname === '/api/pipelines' && r.request().method() === 'POST');
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    const response = await saving;
    expect(response.ok()).toBe(true);
    const row = await response.json();
    const actual = (await (await page.request.get(`/api/pipelines/${row.rowId}`, { headers })).json()).definition;
    const target = { ...expected, version: actual.version, nodes: expected.nodes.filter((n: any) => n.id !== 'fil'), edges: actual.edges };
    expect(actual).toEqual(target);
    expect(actual.edges).toHaveLength(1);
    expect(actual.edges[0]).toMatchObject({ source: 'src', target: 'snk' });
    await page.goto(`/?pipeline=${row.rowId}`);
    await expect(page.locator('.react-flow__node')).toHaveCount(2);
    await page.screenshot({ path: info.outputPath('mermaid-structural-reopened.png'), fullPage: true });
  });
});
