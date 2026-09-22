import type { PipelineDefinition, PipelineNode } from '@dataflow/shared';
import { applyGraphEdit } from './pipelineConvert';

export type AiDefinitionProposal = Pick<PipelineDefinition, 'nodes' | 'edges'> &
  Partial<Pick<PipelineDefinition, 'name' | 'trigger' | 'execution'>> & { suggestedName?: string };

// Both review and Apply consume this result. A refinement keeps the existing
// pipeline name/trigger and the non-graph fields omitted by the planner.
export function mergeAiProposal(base: PipelineDefinition, proposal: AiDefinitionProposal): PipelineDefinition {
  const next = applyGraphEdit(base, proposal, 'ai');
  if (proposal.execution) next.execution = structuredClone(proposal.execution);
  if (!base.nodes.length) {
    next.name = proposal.suggestedName ?? proposal.name ?? base.name;
    next.trigger = structuredClone(proposal.trigger ?? base.trigger);
  }
  return next;
}

export interface ProposalFieldChange {
  field: string;
  action: 'Added' | 'Removed' | 'Changed';
  before: string;
  after: string;
}
export interface ProposalChange {
  subject: string;
  action: 'Added' | 'Removed' | 'Changed' | 'Activity changed';
  fields: ProposalFieldChange[];
}

const same = (before: unknown, after: unknown) => JSON.stringify(before, sorted) === JSON.stringify(after, sorted);
const sorted = (_key: string, value: unknown) => value && typeof value === 'object' && !Array.isArray(value)
  ? Object.fromEntries(Object.entries(value).sort(([a], [b]) => a.localeCompare(b))) : value;
const short = (value: string) => value.length > 80 ? `${value.slice(0, 80)}… (shortened)` : value;
const identifier = /^[a-zA-Z_][a-zA-Z0-9_.-]{0,63}$/;
const privateField = /secret|token|password|credential|authorization|header|cookie|body|payload|private|api.?key|url|uri|sql|query|connection/i;
const identifierFields = new Set(['table', 'schema', 'database', 'collection', 'topic', 'platform', 'namespace', 'name', 'type', 'layer', 'engine', 'activityType', 'mode', 'mergeStrategy']);

function previewValue(path: string, value: unknown): string {
  if (value === undefined) return 'Not set';
  if (value === null) return 'Empty';
  if (privateField.test(path)) return 'Hidden';
  if (typeof value === 'boolean' || typeof value === 'number') return String(value);
  const key = path.split('.').at(-1) ?? path;
  if (typeof value === 'string') {
    // Arbitrary connector text can contain secrets even under innocuous keys.
    // Only short identifiers for known fields are shown; no URL parsing leaks.
    if (identifierFields.has(key) && identifier.test(value)) return value;
    if ((path === 'label' || path === 'name') && !/[:/=@]|\b(bearer|password|secret|token)\b/i.test(value)) return short(value);
    return 'Text hidden';
  }
  if (Array.isArray(value)) return `${value.length} item${value.length === 1 ? '' : 's'} (values hidden)`;
  return `${Object.keys(value as object).length} field${Object.keys(value as object).length === 1 ? '' : 's'} (values hidden)`;
}

function fieldChange(field: string, before: unknown, after: unknown): ProposalFieldChange[] {
  if (same(before, after)) return [];
  return [{ field: short(field), action: before === undefined ? 'Added' : after === undefined ? 'Removed' : 'Changed',
    before: previewValue(field, before), after: previewValue(field, after) }];
}

function objectChanges(prefix: string, before: unknown, after: unknown): ProposalFieldChange[] {
  const isObject = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
  if ((before !== undefined && !isObject(before)) || (after !== undefined && !isObject(after))) return fieldChange(prefix, before, after);
  const a = isObject(before) ? before : {};
  const b = isObject(after) ? after : {};
  const keys = [...new Set([...Object.keys(a), ...Object.keys(b)])].sort();
  const changes = keys.flatMap(key => fieldChange(`${prefix}.${key}`, a[key], b[key]));
  // Creating/removing an empty object is still a persisted change.
  return changes.length ? changes : fieldChange(prefix, before, after);
}

function bindingChanges(field: 'inputAssets' | 'outputAssets', before: PipelineNode, after: PipelineNode): ProposalFieldChange[] {
  const a = before[field];
  const b = after[field];
  if (same(a, b)) return [];
  const changes = [];
  for (let i = 0; i < Math.max(a?.length ?? 0, b?.length ?? 0); i++) {
    changes.push(...objectChanges(`${field}[${i}]`, a?.[i], b?.[i]));
  }
  return changes.length ? changes : fieldChange(field, a, b);
}

function nodeChanges(before: PipelineNode, after: PipelineNode): ProposalFieldChange[] {
  return [
    ...(['type', 'activityType', 'label', 'timeoutSec', 'mergeStrategy', 'joinKey'] as const)
      .flatMap(key => fieldChange(key, before[key], after[key])),
    ...objectChanges('config', before.config, after.config),
    ...objectChanges('ingestion', before.ingestion, after.ingestion),
    ...objectChanges('retry', before.retry, after.retry),
    ...bindingChanges('inputAssets', before, after),
    ...bindingChanges('outputAssets', before, after),
  ];
}

// Only sanitized display strings leave this helper; never hand raw config values
// to the review component or place proposals in browser storage.
export function describeProposalChanges(before: PipelineDefinition, after: PipelineDefinition): ProposalChange[] {
  const changes: ProposalChange[] = [];
  const pipelineFields = [
    ...fieldChange('name', before.name, after.name),
    ...(['trigger', 'execution', 'concurrency', 'metadata', 'slo', 'notifications'] as const)
      .flatMap(key => objectChanges(key, before[key], after[key])),
  ];
  if (pipelineFields.length) changes.push({ subject: 'Pipeline settings', action: 'Changed', fields: pipelineFields });
  const oldNodes = new Map(before.nodes.map(node => [node.id, node]));
  const newNodes = new Map(after.nodes.map(node => [node.id, node]));
  for (const id of new Set([...oldNodes.keys(), ...newNodes.keys()])) {
    const a = oldNodes.get(id);
    const b = newNodes.get(id);
    const fields = nodeChanges(a ?? {} as PipelineNode, b ?? {} as PipelineNode);
    if (fields.length || !a || !b) changes.push({
      subject: `Node ${short(id)}`,
      action: !a ? 'Added' : !b ? 'Removed' : a.activityType !== b.activityType || a.type !== b.type ? 'Activity changed' : 'Changed',
      fields,
    });
  }
  const oldEdges = new Map(before.edges.map(edge => [edge.id, edge]));
  const newEdges = new Map(after.edges.map(edge => [edge.id, edge]));
  for (const id of new Set([...oldEdges.keys(), ...newEdges.keys()])) {
    const a = oldEdges.get(id);
    const b = newEdges.get(id);
    if (same(a, b)) continue;
    const edge = b ?? a!;
    changes.push({ subject: `Connection ${short(edge.source)} → ${short(edge.target)}`,
      action: !a ? 'Added' : !b ? 'Removed' : 'Changed',
      fields: (['source', 'target', 'condition'] as const).flatMap(key => fieldChange(key, a?.[key], b?.[key])),
    });
  }
  // Incoming source order determines left/right join inputs and source tags.
  // Interleaving edges belonging to different targets has no such effect.
  for (const target of new Set([...before.edges, ...after.edges].map(edge => edge.target))) {
    const a = before.edges.filter(edge => edge.target === target).map(edge => edge.source);
    const b = after.edges.filter(edge => edge.target === target).map(edge => edge.source);
    if (Math.max(a.length, b.length) < 2 || same(a, b)) continue;
    changes.push({ subject: `Inputs to node ${short(target)}`, action: 'Changed', fields: [{
      field: 'Source order', action: 'Changed',
      before: a.length ? a.map(short).join(' → ') : 'Not set',
      after: b.length ? b.map(short).join(' → ') : 'Not set',
    }] });
  }
  return changes;
}
