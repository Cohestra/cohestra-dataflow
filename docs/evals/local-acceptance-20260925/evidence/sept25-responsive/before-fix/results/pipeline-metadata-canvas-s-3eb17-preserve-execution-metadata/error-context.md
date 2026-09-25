# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: pipeline-metadata.spec.ts >> canvas save, AI Apply/Undo and Mermaid edits preserve execution metadata
- Location: apps/web/tests/pipeline-metadata.spec.ts:4:5

# Error details

```
Test timeout of 90000ms exceeded.
```

```
Error: locator.click: Test timeout of 90000ms exceeded.
Call log:
  - waiting for getByRole('button', { name: 'Save', exact: true })
    - locator resolved to <button aria-describedby="pipeline-action-status" class="glass-btn-ghost border-transparent bg-transparent text-xs disabled:cursor-not-allowed disabled:opacity-40">…</button>
  - attempting click action
    2 × waiting for element to be visible, enabled and stable
      - element is visible, enabled and stable
      - scrolling into view if needed
      - done scrolling
      - element is outside of the viewport
    - retrying click action
    - waiting 20ms
    2 × waiting for element to be visible, enabled and stable
      - element is visible, enabled and stable
      - scrolling into view if needed
      - done scrolling
      - element is outside of the viewport
    - retrying click action
      - waiting 100ms
    149 × waiting for element to be visible, enabled and stable
        - element is visible, enabled and stable
        - scrolling into view if needed
        - done scrolling
        - element is outside of the viewport
      - retrying click action
        - waiting 500ms

```

# Page snapshot

```yaml
- generic [ref=e3]:
  - generic [ref=e4]:
    - generic [ref=e5]:
      - generic [ref=e7]:
        - generic:
          - img:
            - button "Edge from source to sink" [ref=e8] [cursor=pointer]
          - generic:
            - button "Read orders Custom API" [ref=e11] [cursor=pointer]:
              - generic [ref=e22]:
                - generic [ref=e23]: Read orders
                - generic [ref=e24]: Custom API
            - button "Write orders Postgres (destination)" [ref=e26] [cursor=pointer]:
              - generic [ref=e36]:
                - generic [ref=e37]: Write orders
                - generic [ref=e38]: Postgres (destination)
      - generic [ref=e42]:
        - button "zoom in" [ref=e43] [cursor=pointer]
        - button "zoom out" [ref=e46] [cursor=pointer]
        - button "fit view" [ref=e49] [cursor=pointer]
        - button "toggle interactivity" [ref=e52] [cursor=pointer]
      - img "React Flow mini map" [ref=e56]
    - complementary [ref=e60]:
      - button "All pipelines" [ref=e66] [cursor=pointer]:
        - generic [ref=e70]: Pipelines
      - button "Sources" [ref=e72] [cursor=pointer]
      - button "Transforms" [ref=e78] [cursor=pointer]
      - button "Sinks" [ref=e83] [cursor=pointer]
      - button "Flow" [ref=e87] [cursor=pointer]
      - button "Quick AI add" [ref=e95] [cursor=pointer]:
        - generic [ref=e99]: Quick AI
      - button "Edit as Mermaid" [ref=e100] [cursor=pointer]:
        - generic [ref=e105]: Mermaid
      - button "Connectors" [ref=e107] [cursor=pointer]:
        - generic [ref=e112]: Connect
      - button "Pipeline runs" [ref=e113] [cursor=pointer]:
        - generic [ref=e118]: Runs
      - button "Pipeline lifecycle" [ref=e119] [cursor=pointer]:
        - generic [ref=e125]: Lifecycle
      - button "Profile and settings" [ref=e126] [cursor=pointer]:
        - generic [ref=e130]: Settings
      - button "Switch to dark mode" [ref=e131] [cursor=pointer]:
        - generic [ref=e134]: Dark
    - generic [ref=e135]:
      - textbox "Pipeline name" [ref=e136]: Metadata pipeline
      - button "draft" [ref=e139] [cursor=pointer]
      - combobox [ref=e147] [cursor=pointer]:
        - option "Manual" [selected]
        - option "Cron"
        - option "Webhook"
        - option "Upstream pipeline"
        - option "Asset materialized"
    - generic:
      - status: Loaded v1
      - generic [ref=e148]:
        - combobox "Execution engine" [ref=e149]:
          - option "Workflow" [selected]
          - option "Direct stream · locked" [disabled]
          - option "Spark SQL · locked" [disabled]
          - option "Flink SQL · locked" [disabled]
        - button "Save" [ref=e150] [cursor=pointer]
        - button "Activate" [ref=e155] [cursor=pointer]
        - button "Run" [ref=e161] [cursor=pointer]
  - complementary [ref=e164]:
    - generic [ref=e165]:
      - generic [ref=e169]: Build with AI
      - button "Close AI panel" [ref=e170] [cursor=pointer]
    - generic [ref=e176]:
      - textbox "AI pipeline request" [active] [ref=e177]:
        - /placeholder: Describe changes… e.g. Add a filter step before the Postgres sink
      - button "Refine" [disabled] [ref=e178]
  - complementary [ref=e179]
  - button "Output" [ref=e180] [cursor=pointer]
```

# Test source

```ts
  1   | import { expect, test } from '@playwright/test';
  2   |
  3   | // Browser-only regression: requests are captured, no model or workflow is run.
  4   | test('canvas save, AI Apply/Undo and Mermaid edits preserve execution metadata', async ({ page }) => {
  5   |   const asset = {
  6   |     urn: 'postgres://fixture/public.orders', platform: 'postgres', namespace: 'fixture',
  7   |     name: 'orders', type: 'table', layer: 'silver',
  8   |   };
  9   |   const original = {
  10  |     id: 'metadata-pipeline', name: 'Metadata pipeline', version: 1, tenantId: 't1',
  11  |     trigger: { type: 'manual' }, concurrency: { maxParallelNodes: 3 },
  12  |     execution: { engine: 'workflow' },
  13  |     metadata: { owner: 'Fixture owner', domain: 'operations', tags: ['regression'] },
  14  |     slo: { freshnessMinutes: 12, maxFailureRatePercent: 3, maxDurationMs: 45000 },
  15  |     notifications: { connectionId: 'fixture-notifications', minimumSeverity: 'warning' },
  16  |     nodes: [
  17  |       { id: 'source', type: 'source', activityType: 'http.fetch', label: 'Read orders',
  18  |         config: { url: 'https://fixture.example/orders', headers: { Authorization: 'Bearer [REDACTED]' } }, timeoutSec: 17,
  19  |         retry: { maximumAttempts: 2 }, ingestion: { mode: 'incremental', pageSize: 25 },
  20  |         outputAssets: [asset] },
  21  |       { id: 'sink', type: 'sink', activityType: 'sink.postgres', label: 'Write orders',
  22  |         config: { connectionId: 'fixture-db', table: 'orders' }, timeoutSec: 23,
  23  |         retry: { maximumAttempts: 1 }, inputAssets: [asset] },
  24  |     ],
  25  |     edges: [{ id: 'source-sink', source: 'source', target: 'sink' }],
  26  |   };
  27  |   const saves: any[] = [];
  28  |   const refinements: any[] = [];
  29  |   await page.route('**/api/**', async route => {
  30  |     const request = route.request();
  31  |     const path = new URL(request.url()).pathname;
  32  |     let body: unknown;
  33  |     if (path === '/api/pipelines' && request.method() === 'POST') {
  34  |       saves.push(request.postDataJSON());
  35  |       body = { rowId: 'p1', pipelineKey: original.id, version: saves.length + 1 };
  36  |     } else if (path === '/api/ai/refine') {
  37  |       refinements.push(request.postDataJSON());
  38  |       // The planner returns graph/config fields, omitting non-graph metadata.
  39  |       body = { status: 'ready', definition: {
  40  |         nodes: original.nodes.map(({ id, type, activityType, label, config }) => ({
  41  |           id, type, activityType, label: id === 'sink' ? 'Write reviewed orders' : label,
  42  |           config: id === 'sink' ? { ...config, table: 'reviewed_orders' } : { ...config, headers: { Authorization: 'Bearer SECRET_HEADER_AFTER' } },
  43  |         })), edges: original.edges,
  44  |       }, mermaid: '', questions: [], warnings: [], assumptions: [] };
  45  |     } else if (path === '/api/pipelines/p1') {
  46  |       body = { id: 'p1', version: 1, status: 'inactive', environment: 'test', definition: original };
  47  |     } else if (path === '/api/auth/refresh' || path === '/api/auth/me') {
  48  |       body = { accessToken: 'fixture', user: { id: 'u1', email: 'qa@example.com', role: 'owner', tenant_id: 't1' } };
  49  |     } else if (path === '/api/connectors/catalog') {
  50  |       body = { catalog: original.nodes.map(node => ({
  51  |         activityType: node.activityType, nodeType: node.type, label: node.label, fields: [],
  52  |       })) };
  53  |     } else if (path === '/api/edition') {
  54  |       body = { features: {}, availability: {} };
  55  |     } else if (path === '/api/pipelines/lineage/workspace') {
  56  |       body = { nodes: [], edges: [] };
  57  |     } else if (path === '/api/pipelines' || path === '/api/connectors') {
  58  |       body = [];
  59  |     } else {
  60  |       return route.fulfill({ status: 404, contentType: 'application/json', body: JSON.stringify({ error: `Unmocked fixture route: ${path}` }) });
  61  |     }
  62  |     return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) });
  63  |   });
  64  |
  65  |   const preserved = (definition: any) => {
  66  |     for (const key of ['trigger', 'concurrency', 'execution', 'metadata', 'slo', 'notifications'] as const) {
  67  |       expect(definition[key], key).toEqual(original[key]);
  68  |     }
  69  |     for (const node of original.nodes) {
  70  |       const actual = definition.nodes.find((candidate: any) => candidate.id === node.id);
  71  |       for (const key of ['timeoutSec', 'retry', 'ingestion', 'inputAssets', 'outputAssets'] as const) {
  72  |         expect(actual[key], `${node.id}.${key}`).toEqual((node as any)[key]);
  73  |       }
  74  |     }
  75  |   };
  76  |   const save = async () => {
  77  |     const count = saves.length;
> 78  |     await page.getByRole('button', { name: 'Save', exact: true }).click();
      |                                                                   ^ Error: locator.click: Test timeout of 90000ms exceeded.
  79  |     await expect.poll(() => saves.length).toBe(count + 1);
  80  |     await expect(page.locator('#pipeline-action-status')).toHaveText(`Saved v${count + 2}`);
  81  |     preserved(saves[count]);
  82  |     return saves[count];
  83  |   };
  84  |
  85  |   await page.goto('/?pipeline=p1&ai=1');
  86  |   await expect(page.getByLabel('Pipeline name')).toHaveValue(original.name);
  87  |   await expect(page.getByRole('button', { name: 'Activate', exact: true })).toBeEnabled();
  88  |   expect((await save()).nodes).toMatchObject(original.nodes);
  89  |   await page.getByLabel('Pipeline name').fill('Renamed metadata pipeline');
  90  |   await page.getByLabel('AI pipeline request').fill('Write into reviewed_orders instead');
  91  |   await page.getByRole('button', { name: 'Refine', exact: true }).click();
  92  |   await expect(page.getByRole('button', { name: 'Apply', exact: true })).toBeVisible();
  93  |   preserved(refinements[0].definition);
  94  |   const review = page.getByRole('region', { name: 'Review changes' });
  95  |   await expect(review).toBeVisible();
  96  |   const sinkReview = review.locator('summary').filter({ hasText: 'Node sink' });
  97  |   await sinkReview.focus();
  98  |   await page.keyboard.press('Enter');
  99  |   await expect(review.getByText('Changed: config.table', { exact: true })).toBeVisible();
  100 |   await expect(review.getByText('Before: orders', { exact: true })).toBeVisible();
  101 |   await expect(review.getByText('After: reviewed_orders', { exact: true })).toBeVisible();
  102 |   const sourceReview = review.locator('summary').filter({ hasText: 'Node source' });
  103 |   await sourceReview.focus();
  104 |   await page.keyboard.press('Enter');
  105 |   await expect(review.getByText('Changed: config.headers', { exact: true })).toBeVisible();
  106 |   await expect(review.getByText('Before: Hidden', { exact: true })).toBeVisible();
  107 |   await expect(review.getByText('After: Hidden', { exact: true })).toBeVisible();
  108 |   expect(await review.textContent()).not.toMatch(/SECRET_HEADER_|Authorization|Bearer/);
  109 |   await expect(page.locator('.react-flow__node').filter({ hasText: 'Write orders' })).toBeVisible();
  110 |   await expect(page.locator('.react-flow__node').filter({ hasText: 'Write reviewed orders' })).toHaveCount(0);
  111 |   expect(saves).toHaveLength(1); // Reading the proposal does not save or apply it.
  112 |   await page.getByLabel('Pipeline name').fill('Newer draft');
  113 |   await expect(page.getByRole('button', { name: 'Apply', exact: true })).toBeDisabled();
  114 |   await expect(page.getByText('This proposal is based on an older draft. Retry to regenerate before applying.')).toBeVisible();
  115 |   await page.getByLabel('Pipeline name').fill('Renamed metadata pipeline');
  116 |   await page.getByRole('button', { name: 'Apply', exact: true }).focus();
  117 |   await page.keyboard.press('Enter');
  118 |   await expect(page.getByLabel('AI pipeline request')).toBeFocused();
  119 |   const applied = await save();
  120 |   expect(applied.name).toBe('Renamed metadata pipeline');
  121 |   expect(applied.nodes.find((node: any) => node.id === 'sink').config.table).toBe('reviewed_orders');
  122 |   await page.getByRole('button', { name: 'Undo last apply', exact: true }).click();
  123 |   const undone = await save();
  124 |   expect(undone.name).toBe('Renamed metadata pipeline');
  125 |   expect(undone.nodes).toMatchObject(original.nodes);
  126 |
  127 |   // Discarding a second preview leaves the restored draft unchanged.
  128 |   await page.getByLabel('AI pipeline request').fill('Review another proposal');
  129 |   await page.getByRole('button', { name: 'Refine', exact: true }).click();
  130 |   await expect(review).toBeVisible();
  131 |   await page.getByRole('button', { name: 'Discard', exact: true }).focus();
  132 |   await page.keyboard.press('Enter');
  133 |   await expect(review).toHaveCount(0);
  134 |   await expect(page.getByLabel('AI pipeline request')).toBeFocused();
  135 |   const discarded = await save();
  136 |   expect(discarded.nodes).toMatchObject(original.nodes);
  137 |   await page.getByLabel('Close AI panel').click();
  138 |   await page.getByRole('button', { name: /Mermaid/i }).click();
  139 |   const editor = page.getByLabel('Mermaid diagram source');
  140 |   await expect(editor).toBeVisible();
  141 |   await editor.fill((await editor.inputValue()).replace('Read orders', 'Read original orders'));
  142 |   await page.getByRole('button', { name: 'Apply to canvas', exact: true }).click();
  143 |   const edited = await save();
  144 |   expect(edited.nodes.find((node: any) => node.id === 'source').label).toBe('Read original orders');
  145 | });
  146 |
```
