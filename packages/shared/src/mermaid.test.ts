// Round-trip stability test for the Mermaid mapping. No test framework —
// runs under ts-node and exits non-zero on the first failed assertion.
//
//   npm -w @dataflow/shared test
//
// For each example pipeline: definitionToMermaid → mermaidToDefinition must
// reproduce the same node ids, activityTypes, nodeTypes, and edge set. Node
// `config` is intentionally excluded (Mermaid does not carry config).

import assert from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { definitionToMermaid, mermaidToDefinition } from './mermaid';
import type { CatalogEntry } from './catalog-types';
import type { PipelineNode, PipelineEdge } from './types';

// Minimal catalog covering the activityTypes used by examples/*.json.
const catalog: CatalogEntry[] = [
  { activityType: 'zendesk.fetch',    nodeType: 'source',    label: 'Zendesk',        color: '', fields: [] },
  { activityType: 'gsheets.fetch',    nodeType: 'source',    label: 'Google Sheets',  color: '', fields: [] },
  { activityType: 'gdrive.fetch',     nodeType: 'source',    label: 'Google Drive',   color: '', fields: [] },
  { activityType: 'http.fetch',       nodeType: 'source',    label: 'Custom API',     color: '', fields: [] },
  { activityType: 'transform.map',    nodeType: 'transform', label: 'Map',            color: '', fields: [] },
  { activityType: 'transform.filter', nodeType: 'transform', label: 'Filter',         color: '', fields: [] },
  { activityType: 'transform.rename', nodeType: 'transform', label: 'Rename',         color: '', fields: [] },
  { activityType: 'transform.dedupe', nodeType: 'transform', label: 'Dedupe',         color: '', fields: [] },
  { activityType: 'flow.fork',        nodeType: 'fork',      label: 'Fork',           color: '', fields: [] },
  { activityType: 'flow.merge',       nodeType: 'merge',     label: 'Merge',          color: '', fields: [] },
  { activityType: 'sink.postgres',    nodeType: 'sink',      label: 'Postgres sink',  color: '', fields: [] },
  { activityType: 'sink.webhook',     nodeType: 'sink',      label: 'Webhook sink',   color: '', fields: [] },
];

const examplesDir = join(__dirname, '..', '..', '..', 'examples');
const files = ['zendesk-to-postgres.json', 'sheets-drive-join.json'];

let failures = 0;
function check(name: string, fn: () => void) {
  try { fn(); console.log(`  ✓ ${name}`); }
  catch (e: any) { failures++; console.error(`  ✗ ${name}\n    ${e.message}`); }
}

const edgeKey = (e: PipelineEdge) => `${e.source}->${e.target}${e.condition ? `[${e.condition}]` : ''}`;

for (const file of files) {
  const def = JSON.parse(readFileSync(join(examplesDir, file), 'utf8')) as
    { nodes: PipelineNode[]; edges: PipelineEdge[] };

  check(`${file}: round-trips node ids + activityTypes + nodeTypes`, () => {
    const mermaid = definitionToMermaid(def.nodes, def.edges);
    const parsed = mermaidToDefinition(mermaid, catalog);
    assert.strictEqual(parsed.warnings.length, 0, `unexpected warnings: ${parsed.warnings.join('; ')}`);

    const got = new Map(parsed.nodes.map(n => [n.id, n]));
    assert.strictEqual(got.size, def.nodes.length, 'node count differs');
    for (const n of def.nodes) {
      const g = got.get(n.id);
      assert.ok(g, `missing node ${n.id}`);
      assert.strictEqual(g!.activityType, n.activityType, `activityType for ${n.id}`);
      assert.strictEqual(g!.type, n.type, `nodeType for ${n.id}`);
    }
  });

  check(`${file}: round-trips edge set`, () => {
    const mermaid = definitionToMermaid(def.nodes, def.edges);
    const parsed = mermaidToDefinition(mermaid, catalog);
    const want = new Set(def.edges.map(edgeKey));
    const have = new Set(parsed.edges.map(edgeKey));
    assert.deepStrictEqual([...have].sort(), [...want].sort(), 'edge sets differ');
  });
}

// Conditional edge round-trip.
check('conditional edges survive round-trip', () => {
  const nodes: PipelineNode[] = [
    { id: 'a', type: 'source', activityType: 'http.fetch', config: {} },
    { id: 'b', type: 'sink', activityType: 'sink.webhook', config: {} },
  ];
  const edges: PipelineEdge[] = [{ id: 'e1', source: 'a', target: 'b', condition: 'r.ok' }];
  const parsed = mermaidToDefinition(definitionToMermaid(nodes, edges), catalog);
  assert.strictEqual(parsed.edges[0]?.condition, 'r.ok');
});

check('reserved agent fixture survives wire and Mermaid structure round trips', () => {
  const def = JSON.parse(readFileSync(join(__dirname, '../../../tests/contracts/agent-pipeline.json'), 'utf8')) as
    { nodes: PipelineNode[]; edges: PipelineEdge[] };
  const agent = def.nodes.find(n => n.type === 'agent')!;
  assert.strictEqual(agent.activityType, 'agent.run');
  assert.deepStrictEqual(JSON.parse(JSON.stringify(agent)), agent);
  assert.deepStrictEqual(agent.config, {
    agentId: 'cbb40a16-242b-4d78-87de-36a941635f48', agentVersion: 3,
    inputBinding: { mode: 'batch', fields: ['ticketId', 'subject', 'body'], maxRecords: 100 },
  });
  const parsed = mermaidToDefinition(definitionToMermaid(def.nodes, def.edges), catalog);
  const restored = parsed.nodes.find(n => n.id === agent.id)!;
  assert.strictEqual(restored.type, 'agent');
  assert.strictEqual(restored.activityType, 'agent.run');
  assert.deepStrictEqual(restored.config, {}); // config stays out-of-band
  assert.ok(parsed.warnings.some(w => w.includes(agent.id))); // no runnable catalog entry yet
  const collidingLabel = mermaidToDefinition('flowchart LR\n a["Custom API (agent.run)"]', catalog);
  assert.strictEqual(collidingLabel.nodes[0].activityType, 'agent.run');
  assert.strictEqual(collidingLabel.nodes[0].type, 'agent');
});

if (failures) { console.error(`\n${failures} test(s) failed`); process.exit(1); }
console.log('\nall mermaid round-trip tests passed');
