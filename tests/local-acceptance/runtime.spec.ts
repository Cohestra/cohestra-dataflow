import { expect, test, request as playwrightRequest } from '@playwright/test';
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { resolve } from 'node:path';
import { randomUUID } from 'node:crypto';
import { execFileSync } from 'node:child_process';

const root = resolve(__dirname, '../..');
const artifacts = resolve(root, '.artifacts/local-acceptance');
const out = resolve(artifacts, 'sept25-runtime-browser');
const project = process.env.COHESTRA_ACCEPTANCE_PROJECT ?? 'cohestra-acceptance-20260922';
const compose = ['compose', '-p', project, '-f', 'docker-compose.yml', '-f', 'docker-compose.acceptance.yml'];

test('drag preserves metadata and UI Run honors explicit timeout and retry', async ({ page }, testInfo) => {
  mkdirSync(out, { recursive: true });
  const owner = JSON.parse(readFileSync(resolve(artifacts, 'credentials.json'), 'utf8'));
  const fixture = JSON.parse(readFileSync(resolve(artifacts, 'smoke.json'), 'utf8'));
  const evidence: any = { passed: false, realAPI: true, realTemporal: true, checks: [] };
  const save = () => writeFileSync(resolve(out, 'runtime-browser.json'), JSON.stringify(evidence, null, 2), { mode: 0o600 });
  save();
  const admin = await playwrightRequest.newContext({ baseURL: 'http://127.0.0.1:14000' });
  expect((await admin.post('/api/auth/login', { data: owner })).ok()).toBeTruthy();
  const token = (await (await admin.post('/api/auth/refresh')).json()).accessToken;
  const headers = { Authorization: `Bearer ${token}` };
  let running: string | undefined;
  try {
    const definition = structuredClone(fixture.pipeline.definition);
    definition.id = randomUUID();
    definition.name = `Runtime drag ${definition.id.slice(0, 8)}`;
    definition.nodes.find((n: any) => n.type === 'sink').config.collection = `runtime_drag_${definition.id.replaceAll('-', '')}`;
    const created = await admin.post('/api/pipelines', { headers, data: definition });
    expect(created.ok()).toBeTruthy();
    const first = await created.json();
    const before = (await (await admin.get(`/api/pipelines/${first.rowId}`, { headers })).json()).definition;
    await page.goto('/login');
    await page.getByLabel('Email', { exact: true }).fill(owner.email);
    await page.getByLabel('Password', { exact: true }).fill(owner.password);
    await page.getByRole('button', { name: 'Sign in', exact: true }).click();
    await expect(page.getByLabel('Pipeline name')).toBeVisible();
    await page.goto(`/?pipeline=${first.rowId}`);
    await expect(page.getByLabel('Pipeline name')).toHaveValue(before.name);
    const source = page.locator('.react-flow__node[data-id="src"]');
    await expect(source).toBeVisible();
    // Fit-view animation settles before measuring the genuine pointer drag.
    await expect.poll(async () => await source.getAttribute('style')).toContain('transform:');
    await page.waitForTimeout(400);
    const beforePosition = await source.getAttribute('style');
    const box = (await source.boundingBox())!;
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await page.mouse.down();
    await page.mouse.move(box.x + box.width / 2 + 70, box.y + box.height / 2 + 80, { steps: 12 });
    await page.mouse.up();
    expect(await source.getAttribute('style')).not.toEqual(beforePosition);
    const savedResponse = page.waitForResponse(r => new URL(r.url()).pathname === '/api/pipelines' && r.request().method() === 'POST');
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    const saved = await savedResponse;
    expect(saved.ok()).toBeTruthy();
    const savedRow = await saved.json();
    await page.goto(`/?pipeline=${savedRow.rowId}`);
    await expect(page.locator('#pipeline-action-status')).toHaveText(`Loaded v${savedRow.version}`);
    const after = (await (await admin.get(`/api/pipelines/${savedRow.rowId}`, { headers })).json()).definition;
    expect(after).toEqual({ ...before, version: after.version });
    evidence.checks.push({ name: 'Pointer drag then Save/reopen preserves entire definition', passed: true, originalRowId: first.rowId, savedRowId: savedRow.rowId, layoutPersistence: 'Layout is a temporary canvas projection; no persisted position field exists.' });
    await page.screenshot({ path: testInfo.outputPath('drag-reopened.png'), fullPage: true });
    save();

    execFileSync('docker', [...compose, 'exec', '-T', 'postgres', 'psql', '-v', 'ON_ERROR_STOP=1', '-U', 'dataflow', '-d', 'dataflow'], {
      cwd: root, encoding: 'utf8', timeout: 30_000,
      input: "CREATE SCHEMA IF NOT EXISTS local_acceptance; CREATE OR REPLACE VIEW local_acceptance.runtime_browser_slow AS SELECT g AS id, ('Delayed ' || g || pg_sleep(3)::text) AS name, true AS active FROM generate_series(1,2) g;"
    });
    const slow = structuredClone(before);
    slow.id = randomUUID(); delete slow.version;
    slow.name = `Runtime timeout ${slow.id.slice(0, 8)}`;
    const src = slow.nodes.find((n: any) => n.type === 'source');
    src.config.table = 'local_acceptance.runtime_browser_slow';
    src.timeoutSec = 1; src.retry = { maximumAttempts: 2 };
    const collection = `runtime_timeout_${slow.id.replaceAll('-', '')}`;
    slow.nodes.find((n: any) => n.type === 'sink').config.collection = collection;
    const slowResponse = await admin.post('/api/pipelines', { headers, data: slow });
    expect(slowResponse.ok()).toBeTruthy();
    const pipeline = await slowResponse.json();
    expect((await admin.post(`/api/pipelines/${pipeline.rowId}/activate`, { headers })).ok()).toBeTruthy();
    await page.goto(`/?pipeline=${pipeline.rowId}`);
    await expect(page.getByLabel('Pipeline name')).toHaveValue(slow.name);
    const runResponse = page.waitForResponse(r => new URL(r.url()).pathname === `/api/pipelines/${pipeline.rowId}/run` && r.request().method() === 'POST');
    await page.getByRole('button', { name: 'Run', exact: true }).first().click();
    const runResult = await runResponse;
    expect(runResult.ok()).toBeTruthy();
    const run = await runResult.json(); running = run.executionId;
    evidence.timeoutRun = { ...run, rowId: pipeline.rowId, collection, timeoutSec: 1, maximumAttempts: 2 };
    save();
    await expect(page.getByRole('status').filter({ hasText: /^failed$/ })).toBeVisible({ timeout: 60_000 });
    await expect(page.locator('#pipeline-action-status')).toContainText('Run failed');
    for (const title of ['Pause', 'Resume', 'Cancel']) await expect(page.getByTitle(title, { exact: true })).toBeDisabled();
    const detail = await (await admin.get(`/api/executions/${running}`, { headers })).json();
    evidence.executionDetail = detail;
    const history = JSON.parse(execFileSync('docker', [...compose, 'exec', '-T', 'temporal', 'temporal', 'workflow', 'show', '--namespace', 'test', '--workflow-id', detail.execution.workflow_id, '--output', 'json'], { cwd: root, encoding: 'utf8', timeout: 30_000 }));
    writeFileSync(resolve(out, 'timeout-history.json'), JSON.stringify(history, null, 2), { mode: 0o600 });
    const events = history.events;
    const scheduled = events.find((e: any) => e.activityTaskScheduledEventAttributes?.activityType?.name === 'fetchSourcePage');
    expect(scheduled).toBeTruthy();
    expect(scheduled.activityTaskScheduledEventAttributes.startToCloseTimeout).toBe('1s');
    expect(scheduled.activityTaskScheduledEventAttributes.retryPolicy.maximumAttempts).toBe(2);
    const attempts = events.filter((e: any) => String(e.activityTaskStartedEventAttributes?.scheduledEventId) === String(scheduled.eventId)).map((e: any) => e.activityTaskStartedEventAttributes.attempt);
    expect(Math.max(...attempts)).toBe(2);
    const timeout = events.find((e: any) => String(e.activityTaskTimedOutEventAttributes?.scheduledEventId) === String(scheduled.eventId));
    expect(timeout).toBeTruthy();
    expect(timeout.activityTaskTimedOutEventAttributes.retryState).toBe('RETRY_STATE_MAXIMUM_ATTEMPTS_REACHED');
    const rows = await (await admin.get(`/api/analytics/datasets/${collection}/rows`, { headers })).json();
    expect(rows.total).toBe(0); expect(rows.rows).toEqual([]);
    evidence.checks.push({ name: 'UI-triggered timeout exhausts two attempts and fails with no sink records', passed: true, attempts, timeoutEvent: timeout, dataset: rows });
    await page.screenshot({ path: testInfo.outputPath('timeout-failed.png'), fullPage: true });
    evidence.passed = true; save();
  } catch (error) {
    evidence.error = String(error); save(); throw error;
  } finally {
    if (running) await admin.post(`/api/executions/${running}/cancel`, { headers });
    await admin.dispose();
  }
});
