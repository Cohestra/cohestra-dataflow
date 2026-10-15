import { expect, test, type Page } from '@playwright/test';

// Live AI builder check on a lakehouse pipeline: /api/ai/* is forwarded to a
// real planner (AI_LIVE_API, e.g. the isolated eval harness seeded with the
// tests/ai-evals connector fixtures) backed by a real Ollama model; all other
// app endpoints are mocked. Verdicts on model fidelity are recorded as
// annotations so UI correctness and model quality stay separable.
const live = process.env.AI_LIVE_API;
test.skip(!live, 'set AI_LIVE_API to a planner base URL');
test.setTimeout(20 * 60_000);

const catalog = [
  ['postgres.fetch', 'source'], ['iceberg.fetch', 'source'], ['kafka.fetch', 'source'], ['s3.fetch', 'source'], ['http.fetch', 'source'], ['mongodb.fetch', 'source'],
  ['transform.filter', 'transform'], ['transform.dedupe', 'transform'], ['transform.contract', 'transform'], ['transform.formula', 'transform'],
  ['transform.parse', 'transform'], ['transform.rename', 'transform'], ['transform.flatten', 'transform'], ['transform.select', 'transform'], ['transform.map', 'transform'],
  ['flow.fork', 'fork'], ['flow.merge', 'merge'],
  ['sink.iceberg', 'sink'], ['sink.s3', 'sink'], ['sink.clickhouse', 'sink'], ['sink.postgres', 'sink'], ['sink.snowflake', 'sink'], ['sink.kafka', 'sink'],
].map(([activityType, nodeType]) => ({ activityType, nodeType, label: activityType, fields: [] }));

async function wire(page: Page) {
  const proposals: any[] = [];
  await page.route('**/api/**', async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path.startsWith('/api/ai/')) {
      const response = await route.fetch({ url: live!.replace(/\/$/, '') + path, timeout: 15 * 60_000 });
      proposals.push(await response.json());
      return route.fulfill({ response });
    }
    let body: unknown;
    if (path === '/api/auth/refresh' || path === '/api/auth/me') body = { accessToken: 'fixture', user: { id: 'u1', email: 'qa@example.com', role: 'owner', tenant_id: 't1' } };
    else if (path === '/api/connectors/catalog') body = { catalog };
    else if (path === '/api/edition') body = { features: {}, availability: {} };
    else if (path === '/api/pipelines/lineage/workspace') body = { nodes: [], edges: [] };
    else if (path === '/api/pipelines' || path === '/api/connectors') body = [];
    else return route.fulfill({ status: 404, contentType: 'application/json', body: '{}' });
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) });
  });
  return proposals;
}

const activities = (proposal: any) => (proposal?.definition?.nodes ?? []).map((node: any) => node.activityType);
const button = (page: Page, name: string) => page.getByRole('button', { name, exact: true });

test('live lakehouse: generate, chat refine, Apply/Undo/Redo with a real model', async ({ page }, info) => {
  const proposals = await wire(page);
  await page.goto('/?ai=1');
  const request = page.getByLabel('AI pipeline request');

  await request.fill('Continuously capture CDC changes from PostgreSQL connection warehouse-read table public.customers (syncMode cdc), '
    + 'drop duplicate customer id keeping the last record, then write through Iceberg connection eval-iceberg to namespace lake table customers_silver.');
  await button(page, 'Generate').click();
  await expect(page.getByText(/Proposed graph · \d+ nodes|More information needed|No proposal created/)).toBeVisible({ timeout: 15 * 60_000 });
  const first = proposals.at(-1);
  info.annotations.push({ type: 'model-generate', description: `${first.status}: ${activities(first).join(' -> ')}` });
  expect(first.status, 'model must produce a lakehouse proposal for a fully specified request').toBe('ready');
  for (const wanted of ['postgres.fetch', 'transform.dedupe', 'sink.iceberg']) expect(activities(first)).toContain(wanted);

  await button(page, 'Apply').click();
  const appliedCount = first.definition.nodes.length;
  await expect(page.locator('.react-flow__node')).toHaveCount(appliedCount);
  await expect(button(page, 'Save')).toBeEnabled();

  await request.fill('Add a data contract with schemaJson {"type":"object","required":["id"]} that quarantines violations, right before the Iceberg sink. Keep every existing node unchanged.');
  await button(page, 'Refine').click();
  await expect(page.getByText(/Proposed graph · \d+ nodes|More information needed|No proposal created/)).toBeVisible({ timeout: 15 * 60_000 });
  const second = proposals.at(-1);
  info.annotations.push({ type: 'model-refine', description: `${second.status}: ${activities(second).join(' -> ')}` });
  expect(second.status).toBe('ready');
  expect(activities(second)).toContain('transform.contract');

  await button(page, 'Apply').click();
  const refinedCount = await page.locator('.react-flow__node').count();
  expect(refinedCount).toBeGreaterThan(appliedCount);
  await button(page, 'Undo last apply').click();
  await expect(page.locator('.react-flow__node')).toHaveCount(appliedCount);
  await button(page, 'Redo last apply').click();
  await expect(page.locator('.react-flow__node')).toHaveCount(refinedCount);
  await expect(button(page, 'Save')).toBeEnabled();
  await page.screenshot({ path: info.outputPath('live-lakehouse.png'), fullPage: true });
});
