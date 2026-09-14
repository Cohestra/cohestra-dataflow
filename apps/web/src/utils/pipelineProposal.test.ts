import * as assert from 'node:assert/strict';
import type { PipelineDefinition } from '@dataflow/shared';
import { mergeAiProposal, describeProposalChanges, type ProposalChange } from './pipelineProposal';
import { definitionToFlow, flowToDefinition } from './pipelineConvert';

const base: PipelineDefinition = {
  id: 'review', version: 1, tenantId: 'fixture', name: 'Review pipeline', trigger: { type: 'manual' },
  concurrency: { maxParallelNodes: 2 }, execution: { engine: 'workflow' },
  nodes: [
    { id: 'source', type: 'source', activityType: 'http.fetch', label: 'Source', timeoutSec: 20,
      retry: { maximumAttempts: 2 }, ingestion: { mode: 'incremental', pageSize: 50 },
      config: { url: 'https://fixture.test?token=SECRET_URL_OLD', headers: { Authorization: 'SECRET_HEADER_OLD' },
        body: { customer: 'PRIVATE_CUSTOMER' }, table: 'orders', password: 'SECRET_PASSWORD_OLD', arbitrary: 'PRIVATE_TEXT_OLD' },
      outputAssets: [{ urn: 'private://SECRET_ASSET_OLD', name: 'orders', namespace: 'fixture', platform: 'postgres', type: 'table' }] },
    { id: 'sink', type: 'sink', activityType: 'sink.postgres', config: { table: 'orders', connectionId: 'PRIVATE_CONNECTION_OLD' } },
  ],
  edges: [{ id: 'original-edge', source: 'source', target: 'sink', condition: 'PRIVATE_EXPRESSION_OLD' }],
};
const proposal = {
  nodes: [
    { id: 'source', type: 'source' as const, activityType: 'http.fetch', label: 'Source',
      config: { url: 'https://fixture.test?token=SECRET_URL_NEW', headers: { Authorization: 'SECRET_HEADER_NEW' },
        body: { customer: 'PRIVATE_CUSTOMER_NEW' }, table: 'reviewed_orders', password: 'SECRET_PASSWORD_NEW', arbitrary: 'PRIVATE_TEXT_NEW' } },
    { id: 'sink', type: 'sink' as const, activityType: 'sink.postgres', config: { table: 'reviewed_orders', connectionId: 'PRIVATE_CONNECTION_NEW' } },
  ],
  edges: [{ id: 'generated', source: 'source', target: 'sink', condition: 'PRIVATE_EXPRESSION_NEW' }],
  name: 'Ignored rename', trigger: { type: 'event' as const, topic: 'ignored' },
  execution: { engine: 'spark-sql' as const, transformSql: 'SELECT PRIVATE_SQL' },
};
const next = mergeAiProposal(base, proposal);
const changes = describeProposalChanges(base, next);
const field = (items: ProposalChange[], subject: string, key: string) => items.find(item => item.subject === subject)?.fields.find(item => item.field === key);
assert.deepEqual(field(changes, 'Node source', 'config.table'), { field: 'config.table', action: 'Changed', before: 'orders', after: 'reviewed_orders' });
assert.deepEqual(field(changes, 'Node source', 'config.headers'), { field: 'config.headers', action: 'Changed', before: 'Hidden', after: 'Hidden' });
assert.equal(field(changes, 'Pipeline settings', 'execution.engine')?.after, 'spark-sql');
assert.equal(field(changes, 'Node sink', 'config.connectionId')?.after, 'Hidden');
assert.equal(field(changes, 'Connection source → sink', 'condition')?.after, 'Text hidden');
const display = JSON.stringify(changes);
assert.equal(/SECRET_|PRIVATE_|https:\/\//.test(display), false, 'review strings never contain raw credentials, bodies, SQL, URLs or arbitrary text');
assert.equal(field(changes, 'Node source', 'timeoutSec'), undefined, 'omitted policies preserved by merge are not described as removals');
assert.equal(field(changes, 'Node source', 'outputAssets'), undefined);
assert.equal(next.name, base.name);
assert.deepEqual(next.trigger, base.trigger);
assert.equal(field(changes, 'Pipeline settings', 'name'), undefined, 'ignored planner fields are not advertised as changes');
assert.deepEqual(next.concurrency, base.concurrency);
assert.equal(next.edges[0].id, base.edges[0].id, 'preview uses Apply edge identity');
const graph = definitionToFlow(next, {});
const saved = flowToDefinition(graph.nodes, graph.edges, { name: next.name, trigger: next.trigger, pipelineKey: next.id }, next);
assert.deepEqual(describeProposalChanges(base, saved), changes, 'preview matches subsequent canvas save semantics');
assert.deepEqual(base.nodes[0].config.table, 'orders', 'review never mutates the draft');

const changedPolicy = structuredClone(base);
changedPolicy.nodes[0].timeoutSec = 90;
changedPolicy.nodes[0].retry = { maximumAttempts: 0 };
changedPolicy.nodes[0].ingestion!.pageSize = 100;
changedPolicy.nodes[0].outputAssets![0].name = 'reviewed_orders';
changedPolicy.nodes[0].outputAssets![0].urn = 'private://SECRET_ASSET_NEW';
const policyChanges = describeProposalChanges(base, changedPolicy);
assert.equal(field(policyChanges, 'Node source', 'timeoutSec')?.after, '90');
assert.equal(field(policyChanges, 'Node source', 'retry.maximumAttempts')?.after, '0');
assert.equal(field(policyChanges, 'Node source', 'ingestion.pageSize')?.before, '50');
assert.equal(field(policyChanges, 'Node source', 'outputAssets[0].name')?.after, 'reviewed_orders');
assert.equal(JSON.stringify(policyChanges).includes('SECRET_ASSET'), false);

const replacement = mergeAiProposal(base, { nodes: [
  { id: 'source', type: 'source', activityType: 'postgres.fetch', config: { table: 'new_source' } },
  { id: 'new_sink', type: 'sink', activityType: 'sink.postgres', config: { table: 'new_table' } },
], edges: [{ id: 'generated', source: 'source', target: 'new_sink' }] });
const replacementChanges = describeProposalChanges(base, replacement);
assert.equal(replacementChanges.find(item => item.subject === 'Node source')?.action, 'Activity changed');
assert.equal(field(replacementChanges, 'Node source', 'activityType')?.before, 'http.fetch');
assert.equal(field(replacementChanges, 'Node source', 'timeoutSec')?.action, 'Removed');
assert.equal(replacementChanges.find(item => item.subject === 'Node sink')?.action, 'Removed');
assert.equal(replacementChanges.find(item => item.subject === 'Node new_sink')?.action, 'Added');
assert.equal(replacementChanges.find(item => item.subject === 'Connection source → sink')?.action, 'Removed');
assert.equal(replacementChanges.find(item => item.subject === 'Connection source → new_sink')?.action, 'Added');

const empty = { ...base, name: 'My pipeline', nodes: [], edges: [] };
const generated = mergeAiProposal(empty, { ...proposal, suggestedName: 'Generated pipeline', trigger: { type: 'event', topic: 'orders' } });
assert.equal(generated.name, 'Generated pipeline');
assert.equal(field(describeProposalChanges(empty, generated), 'Pipeline settings', 'trigger.topic')?.after, 'orders');
assert.deepEqual(describeProposalChanges(base, structuredClone(base)), [], 'unchanged proposals have no changes');
const reordered = { ...base, nodes: base.nodes.map(node => ({ ...node, config: Object.fromEntries(Object.entries(node.config).reverse()) })) };
assert.deepEqual(describeProposalChanges(base, reordered), [], 'key order does not create false changes');
const removedField = structuredClone(base);
delete removedField.nodes[0].config.table;
assert.equal(field(describeProposalChanges(base, removedField), 'Node source', 'config.table')?.action, 'Removed');
const unsafeIdentifier = structuredClone(base);
unsafeIdentifier.nodes[0].config.table = 'https://SECRET_IN_TABLE';
assert.equal(field(describeProposalChanges(base, unsafeIdentifier), 'Node source', 'config.table')?.after, 'Text hidden');
console.log('pipelineProposal.test.ts OK');
