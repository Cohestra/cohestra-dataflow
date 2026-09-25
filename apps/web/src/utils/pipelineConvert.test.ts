import * as assert from 'node:assert/strict';
import type { PipelineDefinition } from '@dataflow/shared';
import { definitionToMermaid, mermaidToDefinition } from '@dataflow/shared';
import { flowToDefinition, definitionToFlow, applyGraphEdit, pipelineFingerprint } from './pipelineConvert';
import { deriveStage, displayEnvironment } from './pipelineStage';

const definition = flowToDefinition([
  { id: 'source', type: 'flowNode', position: { x: 0, y: 0 }, data: { nodeType: 'source', activityType: 'http.fetch', label: 'Source', config: {} } },
  { id: 'sink', type: 'flowNode', position: { x: 1, y: 0 }, data: { nodeType: 'sink', activityType: 'sink.postgres', label: 'Sink', config: {} } },
] as any, [
  { id: 'edge', source: 'source', target: 'sink', data: {} },
], {
  name: 'test',
  pipelineKey: 'pipeline-test',
  trigger: { type: 'manual' },
  notifications: { connectionId: 'connection-1', minimumSeverity: 'critical' },
});

assert.deepEqual(definition.notifications, { connectionId: 'connection-1', minimumSeverity: 'critical' });
assert.equal(deriveStage('active', 'prod'), 'production');
assert.equal(displayEnvironment('test'), 'Integration');
// JSON is the persistence boundary; optional undefined fields are not serialized.
const wire = (value: unknown) => JSON.parse(JSON.stringify(value));
const asset = { urn: 'urn:demo:tickets', platform: 'postgres', namespace: 'public', name: 'tickets', type: 'table' as const,
  schema: { fields: [{ name: 'id', type: 'string', nullable: false }] }, tags: ['synthetic'] };
const full: PipelineDefinition = {
  id: 'policies', version: 7, tenantId: 'synthetic-tenant', name: 'Policy example',
  trigger: { type: 'cron', schedule: '0 * * * *' }, concurrency: { maxParallelNodes: 3 },
  metadata: { owner: 'owner', domain: 'support', tags: ['one', 'two'] },
  slo: { freshnessMinutes: 15, maxFailureRatePercent: 0, maxDurationMs: 0 },
  notifications: { connectionId: 'notification-demo', minimumSeverity: 'warning' },
  execution: { engine: 'workflow', transformSql: 'SELECT * FROM source' },
  nodes: [
    { id: 'source', type: 'source', activityType: 'http.fetch', label: 'Source',
      config: { url: 'https://example.test/tickets', headers: { accept: 'application/json' }, columns: ['id'] },
      ingestion: { mode: 'backfill', pageSize: 25, stateKey: 'synthetic', backfillStart: '2026-01-01T00:00:00Z' },
      timeoutSec: 90, retry: { maximumAttempts: 4 }, inputAssets: [], outputAssets: [asset] },
    { id: 'merge', type: 'merge', activityType: 'flow.merge', config: {}, mergeStrategy: 'innerJoin', joinKey: 'id',
      timeoutSec: 60, retry: { maximumAttempts: 2 }, inputAssets: [asset], outputAssets: [asset] },
    { id: 'sink', type: 'sink', activityType: 'sink.postgres', config: { table: 'tickets' },
      timeoutSec: 180, retry: { maximumAttempts: 1 }, inputAssets: [asset], outputAssets: [] },
  ],
  edges: [{ id: 'stable-edge-1', source: 'source', target: 'merge', condition: 'count > 0' },
    { id: 'stable-edge-2', source: 'merge', target: 'sink' }],
};
const meta = (def: PipelineDefinition) => ({ name: def.name, trigger: def.trigger, pipelineKey: def.id });
const roundTrip = (def: PipelineDefinition) => {
  const graph = definitionToFlow(def, {});
  return flowToDefinition(graph.nodes, graph.edges, meta(def), def);
};
assert.deepEqual(wire(roundTrip(full)), full, 'unchanged full pipeline round-trips through the save payload');
const minimal: PipelineDefinition = { id: 'minimal', version: 1, tenantId: 'demo', name: 'Minimal',
  trigger: { type: 'manual' }, nodes: [{ id: 'source', type: 'source', activityType: 'http.fetch', config: {} }], edges: [] };
assert.deepEqual(wire(roundTrip(minimal)), minimal, 'absent optional metadata remains absent');

const graph = definitionToFlow(full, {});
graph.nodes[0].position = { x: 900, y: 200 };
graph.nodes[0].selected = true;
graph.nodes[0].data.status = 'success';
graph.nodes[0].data.recordCount = 100;
assert.equal(pipelineFingerprint(flowToDefinition(graph.nodes, graph.edges, meta(full), full)), pipelineFingerprint(full), 'layout/live state does not dirty the definition');
graph.nodes[0].data.config.columns.push('subject');
graph.nodes[0].data.ingestion.pageSize = 50;
assert.deepEqual(full.nodes[0].config.columns, ['id'], 'editable config does not mutate the loaded snapshot');
assert.deepEqual(graph.nodes[0].data.definition.config.columns, ['id'], 'config does not alias preserved node data');
assert.equal(full.nodes[0].ingestion?.pageSize, 25);
const edited = flowToDefinition(graph.nodes, graph.edges, { ...meta(full), name: 'Edited name' }, full);
assert.equal(edited.name, 'Edited name');
assert.deepEqual(wire(edited.nodes[0]), { ...full.nodes[0], config: { ...full.nodes[0].config, columns: ['id', 'subject'] }, ingestion: { ...full.nodes[0].ingestion, pageSize: 50 } });
assert.deepEqual(edited.concurrency, full.concurrency);
assert.deepEqual(edited.nodes.slice(1), roundTrip(full).nodes.slice(1));
assert.equal('status' in edited.nodes[0], false);
assert.equal('position' in edited.nodes[0], false);

const mermaid = definitionToMermaid(full.nodes, full.edges).replace('Source (http.fetch)', 'Renamed source (http.fetch)');
const parsed = mermaidToDefinition(mermaid, []);
const structural = applyGraphEdit(full, parsed, 'mermaid');
assert.deepEqual(wire(structural), { ...full, nodes: full.nodes.map(node => node.id === 'source' ? { ...node, label: 'Renamed source' } : { ...node, label: node.label ?? node.activityType }) });
assert.deepEqual(wire(roundTrip(structural)), wire(structural), 'Mermaid keeps policies through subsequent save');
const changedActivity = applyGraphEdit(full, { nodes: [{ ...parsed.nodes[0], activityType: 'postgres.fetch', config: {} }], edges: [] }, 'mermaid');
assert.deepEqual(changedActivity.nodes[0].config, {});
assert.equal(changedActivity.nodes[0].ingestion, undefined);
assert.equal(changedActivity.nodes[0].outputAssets, undefined, 'new activity cannot inherit old bindings');
const renamedId = applyGraphEdit(full, { nodes: [{ ...parsed.nodes[0], id: 'new-source' }], edges: [] }, 'mermaid');
assert.equal(renamedId.nodes[0].timeoutSec, undefined, 'renamed ID is a new node');

const undo = structuredClone(edited);
const proposal = { nodes: [
  { id: 'source', type: 'source' as const, activityType: 'http.fetch', label: 'AI source', config: { url: 'https://example.test/new' } },
  full.nodes[2],
], edges: [{ id: 'stable-edge-1', source: 'source', target: 'sink' }] };
const applied = applyGraphEdit(edited, proposal, 'ai');
assert.deepEqual(applied.nodes[0], { ...edited.nodes[0], ...proposal.nodes[0] }, 'AI supplied config changes while omitted policies remain');
assert.deepEqual(applied.concurrency, edited.concurrency);
assert.deepEqual(applied.metadata, edited.metadata);
assert.notEqual(applied.edges[0].id, 'stable-edge-1', 'new edge cannot reuse an unrelated existing identity');
applied.nodes[0].retry!.maximumAttempts = 9;
applied.metadata!.tags!.push('changed');
assert.equal(undo.nodes[0].retry!.maximumAttempts, 4, 'Undo is detached from applied edits');
assert.deepEqual(wire(roundTrip(undo)), wire(edited), 'Undo restores the complete prior save payload');
assert.equal(proposal.nodes[0].label, 'AI source');
assert.equal(full.nodes[0].retry!.maximumAttempts, 4);
const edgeChanges = applyGraphEdit(full, { nodes: full.nodes, edges: [
  { id: 'e1', source: 'merge', target: 'sink' },
  { id: 'e2', source: 'source', target: 'merge', condition: 'count > 10' },
] }, 'mermaid');
assert.deepEqual(edgeChanges.edges.map(edge => edge.id), ['stable-edge-2', 'stable-edge-1'], 'edge reorder/condition edits retain stable identities');
assert.equal(pipelineFingerprint({ ...full, version: 99, metadata: { tags: ['one', 'two'], domain: 'support', owner: 'owner' } }), pipelineFingerprint(full));
assert.notEqual(pipelineFingerprint(edited), pipelineFingerprint(full), 'semantic edits make a proposal stale');
// Planner output cannot erase preserved policies/bindings with null, undefined or [].
const blanked = applyGraphEdit(full, { nodes: [{ ...full.nodes[0], config: { url: 'https://example.test/v2' },
  retry: null as any, ingestion: undefined, timeoutSec: null as any, outputAssets: [] }], edges: [] }, 'ai');
assert.deepEqual(blanked.nodes[0].retry, full.nodes[0].retry, 'null retry keeps the saved policy');
assert.deepEqual(blanked.nodes[0].ingestion, full.nodes[0].ingestion, 'undefined ingestion keeps the saved policy');
assert.equal(blanked.nodes[0].timeoutSec, full.nodes[0].timeoutSec);
assert.deepEqual(blanked.nodes[0].outputAssets, full.nodes[0].outputAssets, 'empty list keeps asset bindings');
assert.deepEqual(blanked.nodes[0].config, { url: 'https://example.test/v2' }, 'planner still owns config');
// A cleared branch condition is not persisted as an empty string.
const cleared = definitionToFlow(full, {});
cleared.edges[0].data.condition = '';
assert.equal('condition' in wire(flowToDefinition(cleared.nodes, cleared.edges, meta(full), full)).edges[0], false);
console.log('pipelineConvert.test.ts OK');
