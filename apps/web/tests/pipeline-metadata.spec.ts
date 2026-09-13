import { expect, test } from '@playwright/test';

// Browser-only regression: requests are captured, no model or workflow is run.
test('canvas save, AI Apply/Undo and Mermaid edits preserve execution metadata', async ({ page }) => {
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
        config: { url: 'https://fixture.example/orders' }, timeoutSec: 17,
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
          config: id === 'sink' ? { ...config, table: 'reviewed_orders' } : config,
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
  const save = async () => {
    const count = saves.length;
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    await expect.poll(() => saves.length).toBe(count + 1);
    await expect(page.locator('#pipeline-action-status')).toHaveText(`Saved v${count + 2}`);
    preserved(saves[count]);
    return saves[count];
  };

  await page.goto('/?pipeline=p1&ai=1');
  await expect(page.getByLabel('Pipeline name')).toHaveValue(original.name);
  await expect(page.getByRole('button', { name: 'Activate', exact: true })).toBeEnabled();
  expect((await save()).nodes).toMatchObject(original.nodes);
  await page.getByLabel('Pipeline name').fill('Renamed metadata pipeline');
  await page.getByLabel('AI pipeline request').fill('Write into reviewed_orders instead');
  await page.getByRole('button', { name: 'Refine', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Apply', exact: true })).toBeVisible();
  preserved(refinements[0].definition);
  await page.getByLabel('Pipeline name').fill('Newer draft');
  await expect(page.getByRole('button', { name: 'Apply', exact: true })).toBeDisabled();
  await expect(page.getByText('This proposal is based on an older draft. Retry to regenerate before applying.')).toBeVisible();
  await page.getByLabel('Pipeline name').fill('Renamed metadata pipeline');
  await page.getByRole('button', { name: 'Apply', exact: true }).click();
  const applied = await save();
  expect(applied.name).toBe('Renamed metadata pipeline');
  expect(applied.nodes.find((node: any) => node.id === 'sink').config.table).toBe('reviewed_orders');
  await page.getByRole('button', { name: 'Undo last apply', exact: true }).click();
  const undone = await save();
  expect(undone.name).toBe('Renamed metadata pipeline');
  expect(undone.nodes).toMatchObject(original.nodes);

  await page.getByLabel('Close AI panel').click();
  await page.getByRole('button', { name: /Mermaid/i }).click();
  const editor = page.getByLabel('Mermaid diagram source');
  await expect(editor).toBeVisible();
  await editor.fill((await editor.inputValue()).replace('Read orders', 'Read original orders'));
  await page.getByRole('button', { name: 'Apply to canvas', exact: true }).click();
  const edited = await save();
  expect(edited.nodes.find((node: any) => node.id === 'source').label).toBe('Read original orders');
});
