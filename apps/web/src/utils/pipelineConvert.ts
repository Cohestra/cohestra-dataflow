import type { Node } from 'reactflow';
import type { CatalogEntry, PipelineDefinition, PipelineNode, PipelineEdge } from '@dataflow/shared';

type GraphDefinition = Pick<PipelineDefinition, 'nodes' | 'edges'>;
type DefinitionMeta = Pick<PipelineDefinition, 'name' | 'trigger'> &
  Partial<Pick<PipelineDefinition, 'metadata' | 'slo' | 'notifications' | 'execution' | 'concurrency'>> & { pipelineKey?: string };

export function definitionToFlow(def: Partial<PipelineDefinition>, byType: Record<string, CatalogEntry>): { nodes: Node[]; edges: any[] } {
  const nodes: Node[] = (def.nodes ?? []).map((pn, i) => ({
    id: pn.id,
    type: 'flowNode',
    position: { x: 80 + i * 60, y: 80 + (i % 5) * 100 },
    data: {
      // The graph is a projection. Keep policies/assets that the inspector does not edit.
      definition: structuredClone(pn),
      activityType: pn.activityType,
      nodeType: pn.type ?? byType[pn.activityType]?.nodeType,
      label: pn.label,
      ingestion: structuredClone(pn.ingestion),
      config: structuredClone({
        ...pn.config,
        ...(pn.mergeStrategy !== undefined ? { mergeStrategy: pn.mergeStrategy } : {}),
        ...(pn.joinKey !== undefined ? { joinKey: pn.joinKey } : {}),
      }),
    },
  }));
  const edges = (def.edges ?? []).map((e, i) => ({
    id: e.id ?? `edge-${i + 1}-${e.source}-${e.target}`,
    source: e.source, target: e.target,
    data: { condition: e.condition },
    label: e.condition || undefined,
    animated: !!e.condition,
    style: e.condition ? { stroke: '#f5b342' } : undefined,
    labelStyle: e.condition ? { fill: '#f5b342', fontSize: 10 } : undefined,
  }));
  return { nodes, edges };
}

export function flowToDefinition(
  nodes: Node[], edges: any[], meta: DefinitionMeta,
  base: Partial<PipelineDefinition> = {},
): PipelineDefinition {
  const { pipelineKey, ...fields } = meta;
  return structuredClone({
    version: 0, tenantId: 'default', ...base, ...fields,
    id: pipelineKey ?? base.id ?? '',
    nodes: nodes.map(n => {
      const original: PipelineNode | undefined = n.data.definition;
      const compatible = original?.activityType === n.data.activityType && original?.type === n.data.nodeType;
      const preserved = compatible ? original : undefined;
      const config = { ...n.data.config };
      const mergeStrategy = preserved && config.mergeStrategy === (preserved.mergeStrategy ?? preserved.config.mergeStrategy)
        ? preserved.mergeStrategy : config.mergeStrategy;
      const joinKey = preserved && config.joinKey === (preserved.joinKey ?? preserved.config.joinKey)
        ? preserved.joinKey : config.joinKey;
      // Do not persist the convenience copies injected for the merge inspector.
      for (const key of ['mergeStrategy', 'joinKey'] as const) {
        if (preserved?.[key] !== undefined) {
          if (key in preserved.config) config[key] = preserved.config[key];
          else delete config[key];
        }
      }
      return {
        ...preserved,
        id: n.id, type: n.data.nodeType, activityType: n.data.activityType,
        label: n.data.label, config, ingestion: n.data.ingestion,
        mergeStrategy, joinKey,
      };
    }),
    edges: edges.map(e => ({
      id: e.id, source: e.source, target: e.target,
      condition: e.data?.condition,
    })),
  });
}

// Mermaid cannot carry settings. AI may replace config, but omitted policies remain
// attached to the same activity and stable node ID. A different activity starts fresh.
export function applyGraphEdit(
  base: PipelineDefinition, graph: GraphDefinition, source: 'mermaid' | 'ai',
): PipelineDefinition {
  const previous = new Map(base.nodes.map(node => [node.id, node]));
  const used = new Set<string>();
  const reserved = new Set(base.edges.map(edge => edge.id));
  const edges: PipelineEdge[] = graph.edges.map((edge, i) => {
    const candidates = base.edges.filter(old => !used.has(old.id) && old.source === edge.source && old.target === edge.target);
    const old = candidates.find(candidate => candidate.condition === edge.condition) ?? candidates[0];
    let id = old?.id ?? edge.id ?? `edge-${i + 1}`;
    if (!old) {
      const prefix = id;
      let suffix = 1;
      while (used.has(id) || reserved.has(id)) id = `${prefix}-${suffix++}`;
    }
    used.add(id);
    return { ...edge, id };
  });
  return structuredClone({
    ...base,
    nodes: graph.nodes.map(node => {
      const old = previous.get(node.id);
      if (!old || old.activityType !== node.activityType || old.type !== node.type) return node;
      return source === 'mermaid'
        ? { ...old, id: node.id, type: node.type, activityType: node.activityType, label: node.label }
        : { ...old, ...node };
    }),
    edges,
  });
}

// Object key insertion order and server-assigned version do not make an edit.
export function pipelineFingerprint(definition: PipelineDefinition): string {
  const { version: _version, ...editable } = definition;
  return JSON.stringify(editable, (_key, value) => value && typeof value === 'object' && !Array.isArray(value)
    ? Object.fromEntries(Object.keys(value).sort().map(key => [key, value[key]]))
    : value);
}
