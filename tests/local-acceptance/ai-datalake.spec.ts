import { expect, test, type Page } from '@playwright/test';

// Browser-only regression for the AI builder on lakehouse pipelines: the
// planner is mocked with fixed proposals, so these checks cover the UI's
// chat context, review, Apply/Undo/Redo and graph validation — not model quality.

const lake = (id: string, type: string, activityType: string, label: string, config: Record<string, unknown> = {}) =>
  ({ id, type, activityType, label, config });

// Kafka -> fork -> bronze S3 | dedupe -> silver Iceberg | filter -> gold ClickHouse
const medallion = {
  suggestedName: 'Telemetry medallion',
  trigger: { type: 'manual' },
  nodes: [
    lake('n1', 'source', 'kafka.fetch', 'Telemetry stream', { connectionId: 'kafka-1', topic: 'telemetry.raw' }),
    lake('n2', 'fork', 'flow.fork', 'Fork zones'),
    lake('n3', 'sink', 'sink.s3', 'Bronze landing', { connectionId: 's3-1', bucket: 'lake-bronze', key: 'telemetry/raw.jsonl', format: 'jsonl', layer: 'bronze' }),
    lake('n4', 'transform', 'transform.dedupe', 'Latest per device', { key: 'device_id', keep: 'last' }),
    lake('n5', 'sink', 'sink.iceberg', 'Silver table', { connectionId: 'iceberg-1', namespace: 'lake', table: 'telemetry_silver' }),
    lake('n6', 'transform', 'transform.filter', 'Critical only', { predicate: "r.severity === 'critical'" }),
    lake('n7', 'sink', 'sink.clickhouse', 'Gold alerts', { connectionId: 'ch-1', table: 'gold.critical_alerts', layer: 'gold' }),
  ],
  edges: [
    { source: 'n1', target: 'n2' }, { source: 'n2', target: 'n3' }, { source: 'n2', target: 'n4' },
    { source: 'n4', target: 'n5' }, { source: 'n2', target: 'n6' }, { source: 'n6', target: 'n7' },
  ],
};

// Refinement: a data contract guards the silver table.
const withContract = {
  ...medallion,
  nodes: [...medallion.nodes, lake('n8', 'transform', 'transform.contract', 'Silver contract', { schemaJson: '{"required":["device_id"]}', onViolation: 'quarantine' })],
  edges: medallion.edges.filter(edge => !(edge.source === 'n4' && edge.target === 'n5'))
    .concat({ source: 'n4', target: 'n8' }, { source: 'n8', target: 'n5' }),
};

// An invalid proposal: the gold sink feeds back into the fork (cycle + sink with output).
const invalid = { ...medallion, edges: [...medallion.edges, { source: 'n7', target: 'n2' }] };

type Planner = (path: string, body: any) => unknown;

async function mockApi(page: Page, planner: Planner) {
  const calls: Array<{ path: string; body: any }> = [];
  const catalog = Array.from(new Map([...medallion.nodes, ...withContract.nodes].map(node => [node.activityType, node])).values())
    .map(node => ({ activityType: node.activityType, nodeType: node.type, label: node.label, fields: [] }));
  await page.route('**/api/**', async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    let body: unknown;
    if (path === '/api/ai/generate' || path === '/api/ai/refine') {
      const sent = request.postDataJSON();
      calls.push({ path, body: sent });
      body = planner(path, sent);
    } else if (path === '/api/auth/refresh' || path === '/api/auth/me') {
      body = { accessToken: 'fixture', user: { id: 'u1', email: 'qa@example.com', role: 'owner', tenant_id: 't1' } };
    } else if (path === '/api/ai/models') {
      body = { default: 'qwen3:8b', models: [
        { id: 'qwen3:8b', label: 'Qwen3 8B', description: 'Best quality' },
        { id: 'qwen3:4b-instruct-2507', label: 'Qwen3 4B', description: 'Faster' },
      ] };
    } else if (path === '/api/connectors/catalog') {
      body = { catalog };
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
  return calls;
}

const ready = (definition: unknown) => ({ status: 'ready', definition, mermaid: 'flowchart LR\n  n1 --> n2', questions: [], warnings: [], assumptions: [] });
const nodesOnCanvas = (page: Page) => page.locator('.react-flow__node');
const ask = async (page: Page, prompt: string, action: 'Generate' | 'Refine') => {
  await page.getByLabel('AI pipeline request').fill(prompt);
  await page.getByRole('button', { name: action, exact: true }).click();
};
const button = (page: Page, name: string) => page.getByRole('button', { name, exact: true });

test('lakehouse generate, chat refine, Undo/Redo and redo invalidation', async ({ page }) => {
  const calls = await mockApi(page, path => ready(path === '/api/ai/generate' ? medallion : withContract));
  await page.goto('/?ai=1');

  await ask(page, 'Build a bronze/silver/gold lakehouse for the telemetry stream', 'Generate');
  await expect(page.getByText('Proposed graph · 7 nodes')).toBeVisible();
  await button(page, 'Apply').click();
  await expect(nodesOnCanvas(page)).toHaveCount(7);
  await expect(page.getByLabel('Pipeline name')).toHaveValue('Telemetry medallion');
  await expect(button(page, 'Save')).toBeEnabled();
  await expect(button(page, 'Redo last apply')).toHaveCount(0);

  // Second chat turn: the request carries the current graph and the conversation so far.
  await ask(page, 'Quarantine silver rows without a device_id', 'Refine');
  await expect(page.getByText('Proposed graph · 8 nodes')).toBeVisible();
  const refine = calls.find(call => call.path === '/api/ai/refine')!.body;
  expect(refine.definition.nodes.map((node: any) => node.id).sort()).toEqual(['n1', 'n2', 'n3', 'n4', 'n5', 'n6', 'n7']);
  expect(refine.messages).toEqual(expect.arrayContaining([
    expect.objectContaining({ role: 'user', content: 'Build a bronze/silver/gold lakehouse for the telemetry stream' }),
    expect.objectContaining({ role: 'assistant' }),
  ]));
  expect(refine.mermaid).toContain('flowchart');
  await expect(page.getByRole('region', { name: 'Review changes' })).toContainText('Silver contract');
  await button(page, 'Apply').click();
  await expect(nodesOnCanvas(page)).toHaveCount(8);

  // Undo -> Redo -> Undo round-trips the exact graphs.
  await button(page, 'Undo last apply').click();
  await expect(nodesOnCanvas(page)).toHaveCount(7);
  await expect(nodesOnCanvas(page).filter({ hasText: 'Silver contract' })).toHaveCount(0);
  await expect(button(page, 'Undo last apply')).toHaveCount(0);
  await button(page, 'Redo last apply').click();
  await expect(nodesOnCanvas(page)).toHaveCount(8);
  await expect(nodesOnCanvas(page).filter({ hasText: 'Silver contract' })).toHaveCount(1);
  await expect(button(page, 'Redo last apply')).toHaveCount(0);
  await button(page, 'Undo last apply').click();
  await expect(nodesOnCanvas(page)).toHaveCount(7);

  // A manual edit after Undo must discard Redo so it can never overwrite user work.
  await expect(button(page, 'Redo last apply')).toBeVisible();
  await page.getByLabel('Pipeline name').fill('Telemetry medallion (edited)');
  await expect(button(page, 'Redo last apply')).toHaveCount(0);
});

test('invalid lakehouse proposal is caught by graph validation and Undo recovers', async ({ page }) => {
  let turn = 0;
  await mockApi(page, () => ready(turn++ === 0 ? medallion : invalid));
  await page.goto('/?ai=1');
  await ask(page, 'Build the telemetry lakehouse', 'Generate');
  await button(page, 'Apply').click();
  await expect(nodesOnCanvas(page)).toHaveCount(7);

  await ask(page, 'Send gold alerts back into the fork', 'Refine');
  await button(page, 'Apply').click();
  await expect(button(page, 'Save')).toBeDisabled();
  await expect(page.locator('#pipeline-action-status')).toHaveText(/cycle|cannot have outgoing edges/i);

  await button(page, 'Undo last apply').click();
  await expect(button(page, 'Save')).toBeEnabled();
  await expect(page.locator('#pipeline-action-status')).not.toHaveText(/cycle|cannot have outgoing edges/i);
});

test('clarifying and rejected planner replies never offer Apply', async ({ page }) => {
  const replies = [
    { status: 'needs_input', questions: ['Which Iceberg namespace and table should receive the silver data?'], warnings: [], assumptions: [] },
    { status: 'rejected', reason: 'Pipeline cycles are not executable.', questions: [], warnings: ['Pipeline cycles are not executable.'], assumptions: [] },
  ];
  let turn = 0;
  await mockApi(page, () => replies[turn++]);
  await page.goto('/?ai=1');

  await ask(page, 'Build me a data lake', 'Generate');
  await expect(page.getByText('More information needed')).toBeVisible();
  await expect(page.getByText('Which Iceberg namespace and table should receive the silver data?').first()).toBeVisible();
  await expect(button(page, 'Apply')).toHaveCount(0);

  await ask(page, 'Make the sink loop back into the source forever', 'Generate');
  await expect(page.getByText('No proposal created')).toBeVisible();
  await expect(page.getByText('Pipeline cycles are not executable.').first()).toBeVisible();
  await expect(button(page, 'Apply')).toHaveCount(0);
  await expect(nodesOnCanvas(page)).toHaveCount(0);
});

test('stale lakehouse proposal cannot be applied over a newer draft', async ({ page }) => {
  let turn = 0;
  await mockApi(page, () => ready(turn++ === 0 ? medallion : withContract));
  await page.goto('/?ai=1');
  await ask(page, 'Build the telemetry lakehouse', 'Generate');
  await button(page, 'Apply').click();
  await ask(page, 'Add a silver contract', 'Refine');
  await expect(button(page, 'Apply')).toBeEnabled();
  await page.getByLabel('Pipeline name').fill('Renamed while reviewing');
  await expect(button(page, 'Apply')).toBeDisabled();
  await expect(page.locator('#ai-proposal-stale')).toBeVisible();
  await button(page, 'Discard').click();
  await expect(nodesOnCanvas(page)).toHaveCount(7);
});

test('model picker offers the top two planner models and remembers the choice', async ({ page }) => {
  const calls = await mockApi(page, () => ready(medallion));
  await page.goto('/?ai=1');
  const picker = page.getByLabel('AI model');
  await expect(picker).toHaveValue('qwen3:8b');
  await expect(picker.locator('option')).toHaveText(['Qwen3 8B — Best quality', 'Qwen3 4B — Faster']);

  await ask(page, 'Build the telemetry lakehouse', 'Generate');
  await expect(page.getByText('Proposed graph · 7 nodes')).toBeVisible();
  expect(calls.at(-1)!.body.model).toBe('qwen3:8b');

  await picker.selectOption('qwen3:4b-instruct-2507');
  await button(page, 'Retry').click();
  await expect.poll(() => calls.length).toBe(2);
  expect(calls.at(-1)!.body.model).toBe('qwen3:4b-instruct-2507');

  await page.reload();
  await expect(page.getByLabel('AI model')).toHaveValue('qwen3:4b-instruct-2507');
});
