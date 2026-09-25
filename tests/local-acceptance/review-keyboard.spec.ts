import { expect, test } from '@playwright/test';

// Browser-only regression: requests are captured, no model or workflow is run.
test('430px mocked AI review keyboard Apply and Undo preserve metadata', async ({ page }, testInfo) => {
  const asset = {
    urn: 'postgres://fixture/public.orders', platform: 'postgres', namespace: 'fixture',
    name: 'orders', type: 'table', layer: 'silver',
  };
  const original = {
    id: 'metadata-pipeline', name: 'Metadata pipeline', version: 1, tenantId: 't1',
    trigger: { type: 'manual' }, concurrency: { maxParallelNodes: 3 },
    execution: { engine: 'workflow' },
    metadata: { owner: 'Fixture owner', domain: 'operations', tags: ['regression'] },
    slo: { freshnessMinutes: 12, maxFailureRatePercent: 3, maxDurationMs: 45000 },
    notifications: { connectionId: 'fixture-notifications', minimumSeverity: 'warning' },
    nodes: [
      { id: 'source', type: 'source', activityType: 'http.fetch', label: 'Read orders',
        config: { url: 'https://fixture.example/orders', headers: { Authorization: 'Bearer SECRET_HEADER_BEFORE' } }, timeoutSec: 17,
        retry: { maximumAttempts: 2 }, ingestion: { mode: 'incremental', pageSize: 25 },
        outputAssets: [asset] },
      { id: 'sink', type: 'sink', activityType: 'sink.postgres', label: 'Write orders',
        config: { connectionId: 'fixture-db', table: 'orders' }, timeoutSec: 23,
        retry: { maximumAttempts: 1 }, inputAssets: [asset] },
    ],
    edges: [{ id: 'source-sink', source: 'source', target: 'sink' }],
  };
  const saves: any[] = [];
  const refinements: any[] = [];
  await page.route('**/api/**', async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    let body: unknown;
    if (path === '/api/pipelines' && request.method() === 'POST') {
      saves.push(request.postDataJSON());
      body = { rowId: 'p1', pipelineKey: original.id, version: saves.length + 1 };
    } else if (path === '/api/ai/refine') {
      refinements.push(request.postDataJSON());
      // The planner returns graph/config fields, omitting non-graph metadata.
      body = { status: 'ready', definition: {
        nodes: original.nodes.map(({ id, type, activityType, label, config }) => ({
          id, type, activityType, label: id === 'sink' ? 'Write reviewed orders' : label,
          config: id === 'sink' ? { ...config, table: 'reviewed_orders' } : { ...config, headers: { Authorization: 'Bearer SECRET_HEADER_AFTER' } },
        })), edges: original.edges,
      }, mermaid: '', questions: [], warnings: [], assumptions: [] };
    } else if (path === '/api/pipelines/p1') {
      body = { id: 'p1', version: 1, status: 'inactive', environment: 'test', definition: original };
    } else if (path === '/api/auth/refresh' || path === '/api/auth/me') {
      body = { accessToken: 'fixture', user: { id: 'u1', email: 'qa@example.com', role: 'owner', tenant_id: 't1' } };
    } else if (path === '/api/connectors/catalog') {
      body = { catalog: original.nodes.map(node => ({
        activityType: node.activityType, nodeType: node.type, label: node.label, fields: [],
      })) };
    } else if (path === '/api/edition') {
      body = { features: {}, availability: {} };
    } else if (path === '/api/pipelines/lineage/workspace') {
      body = { nodes: [], edges: [] };
    } else if (path === '/api/pipelines' || path === '/api/connectors') {
      body = [];
    } else {
      return route.fulfill({ status: 404, contentType: 'application/json', body: JSON.stringify({ error: `Unmocked fixture route: ${path}` }) });
    }
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) });
  });

  const preserved = (definition: any) => {
    for (const key of ['trigger', 'concurrency', 'execution', 'metadata', 'slo', 'notifications'] as const) {
      expect(definition[key], key).toEqual(original[key]);
    }
    for (const node of original.nodes) {
      const actual = definition.nodes.find((candidate: any) => candidate.id === node.id);
      for (const key of ['timeoutSec', 'retry', 'ingestion', 'inputAssets', 'outputAssets'] as const) {
        expect(actual[key], `${node.id}.${key}`).toEqual((node as any)[key]);
      }
    }
  };
  await page.goto('/?pipeline=p1&ai=1');
  await expect(page.getByLabel('Pipeline name')).toHaveValue(original.name);
  await page.getByLabel('AI pipeline request').fill('Write into reviewed_orders instead');
  await page.getByRole('button', { name: 'Refine', exact: true }).click();
  const review = page.getByRole('region', { name: 'Review changes' });
  await expect(review).toBeVisible();
  preserved(refinements[0].definition);
  const sinkReview = review.locator('summary').filter({ hasText: 'Node sink' });
  await sinkReview.focus(); await page.keyboard.press('Enter');
  await expect(review.getByText('After: reviewed_orders', { exact: true })).toBeVisible();
  const sourceReview = review.locator('summary').filter({ hasText: 'Node source' });
  await sourceReview.focus(); await page.keyboard.press('Enter');
  expect(await review.textContent()).not.toMatch(/SECRET_HEADER_|Authorization|Bearer/);
  await page.screenshot({ path: testInfo.outputPath('narrow-review.png'), fullPage: true });
  const apply = page.getByRole('button', { name: 'Apply', exact: true });
  await expect(apply).toBeInViewport();
  await apply.focus(); await page.keyboard.press('Enter');
  await expect(page.getByLabel('AI pipeline request')).toBeFocused();
  await expect(page.locator('.react-flow__node').filter({ hasText: 'Write reviewed orders' })).toHaveCount(1);
  const undo = page.getByRole('button', { name: 'Undo last apply', exact: true });
  await expect(undo).toBeInViewport();
  await page.screenshot({ path: testInfo.outputPath('narrow-applied.png'), fullPage: true });
  await undo.focus(); await page.keyboard.press('Enter');
  await expect(page.locator('.react-flow__node').filter({ hasText: 'Write reviewed orders' })).toHaveCount(0);
  await expect(page.locator('.react-flow__node').filter({ hasText: 'Write orders' })).toHaveCount(1);
  await page.getByLabel('AI pipeline request').fill('Check restored metadata');
  await page.getByRole('button', { name: 'Refine', exact: true }).click();
  await expect(review).toBeVisible();
  preserved(refinements[1].definition);
  expect(refinements[1].definition.nodes).toMatchObject(original.nodes);
  expect(saves).toHaveLength(0);
  const discard = page.getByRole('button', { name: 'Discard', exact: true });
  await expect(discard).toBeInViewport();
  await discard.focus(); await page.keyboard.press('Enter');
  await expect(review).toHaveCount(0);
  await expect(page.getByLabel('AI pipeline request')).toBeFocused();
  await page.screenshot({ path: testInfo.outputPath('narrow-undone.png'), fullPage: true });
});
