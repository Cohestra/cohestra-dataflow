import { expect, test, request as playwrightRequest } from '@playwright/test';
import { readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';

test('real canvas controls acknowledge intent, display denied access, finish and disable terminal controls', async ({ page }, testInfo) => {
  const root = resolve(__dirname, '../../.artifacts/local-acceptance');
  const out = resolve(root, 'full-controls');
  const fixture = JSON.parse(readFileSync(resolve(out, 'controls.json'), 'utf8'));
  const owner = JSON.parse(readFileSync(resolve(root, 'credentials.json'), 'utf8'));
  const editor = JSON.parse(readFileSync(resolve(out, 'editor.json'), 'utf8'));
  const evidence: any = { passed: false, realAPI: true, runs: [] };
  const save = () => writeFileSync(resolve(out, 'controls-browser.json'), JSON.stringify(evidence, null, 2), { mode: 0o600 });
  save();
  const admin = await playwrightRequest.newContext({ baseURL: 'http://127.0.0.1:14000' });
  expect((await admin.post('/api/auth/login', { data: owner })).ok()).toBeTruthy();
  const token = (await (await admin.post('/api/auth/refresh')).json()).accessToken;
  const headers = { Authorization: `Bearer ${token}` };
  const grant = async (pipeline: any, role: string) => {
    expect((await admin.post(`/api/pipelines/${pipeline.rowId}/access`, { headers,
      data: { userId: fixture.uiEditorUserId, role } })).ok()).toBeTruthy();
  };
  const running: string[] = [];
  try {
    await page.goto('/login');
    await page.getByLabel('Email', { exact: true }).fill(editor.email);
    await page.getByLabel('Password', { exact: true }).fill(editor.password);
    await page.getByRole('button', { name: 'Sign in', exact: true }).click();
    await expect(page.getByLabel('Pipeline name')).toBeVisible();
    const start = async (pipeline: any) => {
      await page.goto(`/?pipeline=${pipeline.rowId}`);
      await expect(page.getByLabel('Pipeline name')).toHaveValue(pipeline.definition.name);
      const response = page.waitForResponse(r => new URL(r.url()).pathname === `/api/pipelines/${pipeline.rowId}/run` && r.request().method() === 'POST');
      await page.getByRole('button', { name: 'Run', exact: true }).first().click();
      const result = await response;
      expect(result.ok()).toBeTruthy();
      const id = (await result.json()).executionId;
      running.push(id);
      evidence.runs.push({ executionId: id, rowId: pipeline.rowId });
      save();
      await expect(page.getByTitle('Pause', { exact: true })).toBeEnabled();
      return id;
    };
    const signal = async (id: string, action: string, code = 200) => {
      const response = page.waitForResponse(r => new URL(r.url()).pathname === `/api/executions/${id}/${action.toLowerCase()}` && r.request().method() === 'POST');
      await page.getByTitle(action, { exact: true }).click();
      expect((await response).status()).toBe(code);
    };
    const first = fixture.uiPipeline;
    const firstId = await start(first);
    await signal(firstId, 'Pause');
    await expect(page.getByRole('status').filter({ hasText: /^Pause requested$/ })).toBeVisible();
    await expect(page.getByRole('status').filter({ hasText: /^paused$/ })).toBeVisible();
    await grant(first, 'viewer');
    await signal(firstId, 'Resume', 403);
    await expect(page.getByRole('alert')).toContainText('Control failed');
    await expect(page.getByRole('alert')).toContainText('editor role');
    await page.screenshot({ path: testInfo.outputPath('viewer-denial-visible.png'), fullPage: true });
    evidence.viewer403Visible = true;
    await grant(first, 'editor');
    await signal(firstId, 'Resume');
    await expect(page.getByRole('status').filter({ hasText: /^Resume requested$/ })).toBeVisible();
    await expect(page.getByRole('alert')).toHaveCount(0);
    await expect(page.getByRole('status').filter({ hasText: /^completed$/ })).toBeVisible({ timeout: 60_000 });
    await expect(page.locator('#pipeline-action-status')).toContainText('Run completed');
    for (const title of ['Pause', 'Resume', 'Cancel']) await expect(page.getByTitle(title, { exact: true })).toBeDisabled();
    const dataset = await (await admin.get(`/api/analytics/datasets/${first.collection}/rows`, { headers })).json();
    expect(dataset.total).toBe(6);
    expect(dataset.rows.map((r: any) => r.id).sort((a: number, b: number) => a - b)).toEqual([1, 3, 5, 7, 9, 11]);
    evidence.completedControlsDisabled = true;
    await page.screenshot({ path: testInfo.outputPath('completed-disabled.png'), fullPage: true });
    await page.reload();
    await expect(page.getByLabel('Pipeline name')).toHaveValue(first.definition.name);
    evidence.reloadedMonitorVisible = await page.getByTitle('Pause', { exact: true }).count() > 0;
    save();
    const second = fixture.uiCancelPipeline;
    const secondId = await start(second);
    await signal(secondId, 'Pause');
    await expect(page.getByRole('status').filter({ hasText: /^paused$/ })).toBeVisible();
    await page.reload();
    await expect(page.getByLabel('Pipeline name')).toHaveValue(second.definition.name);
    await page.getByTitle('Open output panel', { exact: true }).click();
    const detail = page.waitForResponse(r => new URL(r.url()).pathname === `/api/executions/${secondId}` && r.request().method() === 'GET');
    await page.getByRole('row').filter({ hasText: second.definition.name }).click();
    expect((await detail).ok()).toBeTruthy();
    evidence.reopenedPausedMonitorVisible = await page.getByTitle('Resume', { exact: true }).count() > 0;
    save();
    await expect(page.getByTitle('Resume', { exact: true })).toBeVisible();
    await expect(page.getByRole('status').filter({ hasText: /^paused$/ })).toBeVisible();
    await signal(secondId, 'Cancel');
    await expect(page.getByRole('status').filter({ hasText: /^Cancellation requested$/ })).toBeVisible();
    for (const title of ['Pause', 'Resume', 'Cancel']) await expect(page.getByTitle(title, { exact: true })).toBeDisabled();
    await expect(page.getByRole('status').filter({ hasText: /^cancelled$/ })).toBeVisible({ timeout: 30_000 });
    // Reload reports the loaded pipeline version; the monitor above owns run state.
    await expect(page.locator('#pipeline-action-status')).toHaveText(`Loaded v${second.version}`);
    evidence.reopenedFinalPhase = 'cancelled';
    const cancelled = await (await admin.get(`/api/analytics/datasets/${second.collection}/rows`, { headers })).json();
    expect(cancelled.total).toBe(0);
    evidence.cancelledControlsDisabled = true;
    await page.screenshot({ path: testInfo.outputPath('cancelled-disabled.png'), fullPage: true });
    evidence.passed = true;
    save();
  } catch (error) {
    evidence.error = String(error);
    save();
    throw error;
  } finally {
    if (fixture.uiPipeline) await grant(fixture.uiPipeline, 'editor');
    for (const id of running) await admin.post(`/api/executions/${id}/cancel`, { headers });
    await admin.dispose();
  }
});
