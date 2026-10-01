import { expect, test } from '@playwright/test';
import { readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';

test('real sign-in, pipeline list and canvas edit preserve stored metadata', async ({ page, context }, testInfo) => {
  const artifacts = resolve(__dirname, '../../.artifacts/local-acceptance');
  const credentials = JSON.parse(readFileSync(resolve(artifacts, 'credentials.json'), 'utf8'));
  const fixture = JSON.parse(readFileSync(resolve(artifacts, 'smoke.json'), 'utf8')).pipeline;
  expect(fixture.rowId).toBeTruthy();
  writeFileSync(resolve(artifacts, 'browser.json'), JSON.stringify({ passed: false, status: 'started' }), { mode: 0o600 });

  await page.goto('/login');
  await page.getByLabel('Email', { exact: true }).fill(credentials.email);
  await page.getByLabel('Password', { exact: true }).fill(credentials.password);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.getByLabel('Pipeline name')).toBeVisible();

  await context.tracing.start({ screenshots: true, snapshots: true });
  try {
    await page.goto('/pipelines');
    // Search so the fixture is found regardless of how many pipelines exist.
    const listResponse = page.waitForResponse(response => {
      const url = new URL(response.url());
      return url.pathname === '/api/pipelines' && url.searchParams.get('search') === fixture.definition.name;
    });
    await page.getByPlaceholder('Search…').fill(fixture.definition.name);
    const listed = await listResponse;
    expect(listed.ok()).toBeTruthy();
    const list = await listed.json();
    // The overview lists the current version of each pipeline (#56).
    const current = list.rows.find((row: any) => row.pipeline_key === fixture.definition.id);
    expect(current).toBeTruthy();
    const row = page.getByRole('button').filter({ has: page.getByText(fixture.definition.name, { exact: true }) }).first();
    await expect(row).toBeVisible();
    await row.click();

    const originalResponse = page.waitForResponse(response =>
      new URL(response.url()).pathname === `/api/pipelines/${current.id}` && response.request().method() === 'GET');
    await page.getByRole('button', { name: 'Edit', exact: true }).click();
    const original = await originalResponse;
    expect(original.ok()).toBeTruthy();
    const before = (await original.json()).definition;
    await expect(page.getByLabel('Pipeline name')).toHaveValue(before.name);
    for (const key of ['metadata', 'slo', 'concurrency']) {
      expect(before[key], `seeded ${key}`).toEqual(fixture.definition[key]);
    }
    for (const node of before.nodes) {
      expect(node.timeoutSec).toBe(30);
      expect(node.retry).toEqual({ maximumAttempts: 1 });
    }

    const source = before.nodes.find((node: any) => node.type === 'source');
    expect(source).toBeTruthy();
    const editedLabel = `${source.label} (browser verified)`;
    await page.locator('.react-flow__node').filter({ hasText: source.label }).click();
    await page.getByLabel('Label', { exact: true }).fill(editedLabel);
    const savedResponse = page.waitForResponse(response =>
      new URL(response.url()).pathname === '/api/pipelines' && response.request().method() === 'POST');
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    const saved = await savedResponse;
    expect(saved.ok()).toBeTruthy();
    const savedRow = await saved.json();
    await expect(page.locator('#pipeline-action-status')).toHaveText(`Saved v${savedRow.version}`);

    // Save creates a version but leaves the old URL/state; reopen its returned ID.
    const readbackResponse = page.waitForResponse(response =>
      new URL(response.url()).pathname === `/api/pipelines/${savedRow.rowId}` && response.request().method() === 'GET');
    await page.goto(`/?pipeline=${encodeURIComponent(savedRow.rowId)}`);
    const readback = await readbackResponse;
    expect(readback.ok()).toBeTruthy();
    const after = (await readback.json()).definition;
    const expected = structuredClone(before);
    expected.version = after.version; // Assigned by the server for the new version.
    expected.nodes.find((node: any) => node.id === source.id).label = editedLabel;
    expect(after).toEqual(expected);
    await expect(page.locator('.react-flow__node').filter({ hasText: editedLabel })).toBeVisible();
    await expect(page.locator('#pipeline-action-status')).toHaveText(`Loaded v${savedRow.version}`);
    await page.screenshot({ path: testInfo.outputPath('canvas-saved.png'), fullPage: true });
    writeFileSync(resolve(artifacts, 'browser.json'), JSON.stringify({
      passed: true, originalRowId: fixture.rowId, savedRowId: savedRow.rowId,
      version: savedRow.version, editedNode: source.id, editedLabel,
      metadataPreserved: true, realApi: true, additionalWorkflowRuns: 0,
    }, null, 2), { mode: 0o600 });
    await context.tracing.stop();
  } catch (error) {
    // Traces can contain local session headers; keep these ignored artifacts private.
    await context.tracing.stop({ path: testInfo.outputPath('trace.zip') });
    throw error;
  }
});
