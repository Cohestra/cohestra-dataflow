import { memo } from 'react';
import { Handle, Position, type Edge, type Node, type NodeProps, type NodeTypes } from 'reactflow';
import { Activity, Database, Workflow } from 'lucide-react';
import type { WorkspaceLineage } from '@dataflow/shared';

// Shared between ArchitectureLineage (saved pipeline versions) and
// RuntimeLineage (windowed execution metrics). Runtime nodes carry an extra
// `metrics` payload; architecture nodes leave it undefined.

export type LineageLayer = 'external' | 'bronze' | 'silver' | 'gold';
export const LAYER_COLOR: Record<LineageLayer, string> = {
  external: '#64748b', bronze: '#b7791f', silver: '#94a3b8', gold: '#eab308',
};
// Card sizes are ENFORCED via node style (ReactFlow does not constrain the
// DOM node to the declared width/height on its own — runtime metric lines
// made cards outgrow their layout slot and overlap neighbouring lanes).
export const ASSET_WIDTH = 360;
export const ASSET_HEIGHT = 152;
export const PIPELINE_WIDTH = 200;
export const PIPELINE_HEIGHT = 128;
export const DEFAULT_VIEWPORT = { x: 0, y: 0, zoom: 0.35 };
export const FIT_VIEW_OPTIONS = { padding: 0.25, minZoom: 0.15, maxZoom: 0.4 };
export const normalizeLayer = (layer: unknown): LineageLayer =>
  typeof layer === 'string' && Object.prototype.hasOwnProperty.call(LAYER_COLOR, layer)
    ? layer as LineageLayer
    : 'external';
export const HEALTH_COLOR: Record<string, string> = {
  healthy: '#10b981', warning: '#f59e0b', critical: '#ef4444', unmonitored: '#94a3b8',
};
export type PipelineHealth = {
  id: string; health: 'healthy' | 'warning' | 'critical' | 'unmonitored';
  last_execution_id?: string; last_phase?: string;
  breaches?: Array<{ type: string; severity: string; message: string }>;
};

export type RuntimeNodeMetrics = {
  runs: number; succeeded?: number; failed: number; running?: number; cancelled?: number;
  successRate?: number; errorRate?: number; p50Ms?: number; p95Ms?: number;
  records?: number; lastRunAt?: string; lastAt?: string;
};

export function formatDuration(ms: number | null | undefined): string {
  if (ms == null) return '—';
  if (ms < 1000) return `${Math.round(ms)}ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  const minutes = Math.floor(ms / 60_000);
  if (minutes < 60) return `${minutes}m ${Math.round((ms % 60_000) / 1000)}s`;
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

export function formatCount(value: number | null | undefined): string {
  if (value == null) return '—';
  return Number(value).toLocaleString();
}

const AssetNode = memo(function AssetNode({ data }: NodeProps) {
  const layer = normalizeLayer(data.layer);
  const metrics: RuntimeNodeMetrics | undefined = data.metrics;
  return (
    <div className="h-full w-full overflow-hidden rounded-xl border border-gray-200 bg-white px-3 py-2 shadow-xs dark:border-white/10 dark:bg-[#12151f]/95">
      <Handle type="target" position={Position.Left} />
      <div className="flex items-center gap-2">
        <Database size={14} style={{ color: LAYER_COLOR[layer] }} />
        <span className="truncate text-xs font-semibold text-gray-900 dark:text-white/90">{data.name}</span>
      </div>
      <p className="mt-1 truncate text-[10px] text-gray-400 dark:text-white/45">{data.platform} · {data.namespace}</p>
      {metrics && <p className="mt-1 text-[10px] font-medium text-gray-600 dark:text-white/60">
        {formatCount(metrics.records)} records · last write {metrics.lastAt ? new Date(metrics.lastAt).toLocaleTimeString() : '—'}
      </p>}
      {!metrics && data.schema?.fields?.length > 0 && <p className="mt-1 text-[10px] text-violet-500">Contract · {data.schema.fields.length} fields</p>}
      {!metrics && data.materialization && <p className="mt-1 text-[10px] text-emerald-600 dark:text-emerald-400">
        Updated {new Date(data.materialization.materializedAt).toLocaleString()}
        {data.materialization.recordCount != null ? ` · ${data.materialization.recordCount.toLocaleString()} rows` : ''}
      </p>}
      {!metrics && data.quality && <p className={`mt-1 text-[10px] font-semibold ${data.quality.status === 'passed' ? 'text-emerald-600 dark:text-emerald-400' : data.quality.status === 'failed' ? 'text-red-500 dark:text-red-400' : 'text-amber-600 dark:text-amber-400'}`}>
        Quality {data.quality.status} · {data.quality.failedCount.toLocaleString()} rejected
      </p>}
      <Handle type="source" position={Position.Right} />
    </div>
  );
});

const PipelineNode = memo(function PipelineNode({ data }: NodeProps) {
  const health = data.health ?? 'unmonitored';
  const metrics: RuntimeNodeMetrics | undefined = data.metrics;
  return (
    <div className="h-full w-full overflow-hidden rounded-xl border bg-brand-50 px-3 py-2 shadow-md dark:bg-brand-500/10"
      style={{ borderColor: HEALTH_COLOR[health] }}>
      <Handle type="target" position={Position.Left} />
      <div className="flex items-center gap-2">
        <Workflow size={14} className="text-brand-500" />
        <span className="truncate text-xs font-semibold text-gray-900 dark:text-white/90">{data.name}</span>
      </div>
      <p className="mt-1 text-[10px] text-gray-500 dark:text-white/50">
        {data.external ? `${data.namespace} · external` : `v${data.version} · ${data.environment === 'prod' ? 'Production' : 'Integration'} · ${data.status}`}
      </p>
      {metrics && <>
        <p className="mt-1 flex items-center gap-1.5 text-[10px] font-semibold">
          <span className={metrics.failed > 0 ? 'text-red-500' : 'text-emerald-600 dark:text-emerald-400'}>
            {metrics.runs} run{metrics.runs === 1 ? '' : 's'}{metrics.errorRate != null ? ` · ${metrics.errorRate}% err` : ''}
          </span>
          {(metrics.running ?? 0) > 0 && <span className="flex items-center gap-1 text-blue-500"><Activity size={10} className="animate-pulse" />{metrics.running}</span>}
        </p>
        <p className="text-[10px] text-gray-500 dark:text-white/50">
          {metrics.runs > 0 ? `p50 ${formatDuration(metrics.p50Ms)} · p95 ${formatDuration(metrics.p95Ms)}` : 'no runs in window'}
        </p>
      </>}
      {!metrics && !data.external && <p className="mt-1 flex items-center gap-1 text-[10px] font-semibold capitalize" style={{ color: HEALTH_COLOR[health] }}>
        <span className="h-1.5 w-1.5 rounded-full" style={{ background: HEALTH_COLOR[health] }} />
        {health}{data.breaches?.length ? ` · ${data.breaches.length} breach${data.breaches.length === 1 ? '' : 'es'}` : ''}
      </p>}
      {(data.metadata?.domain || data.metadata?.owner) && <p className="mt-1 truncate text-[10px] text-gray-400 dark:text-white/45">
        {data.metadata?.domain ?? 'Unassigned'}{data.metadata?.owner ? ` · ${data.metadata.owner}` : ''}
      </p>}
      <Handle type="source" position={Position.Right} />
    </div>
  );
});

export const nodeTypes = { asset: AssetNode, pipeline: PipelineNode } as NodeTypes;

const COL_GAP = 160;
const ROW_GAP = 32;

// Layered layout: columns from longest-path rank (left → right along data flow),
// row order from barycenter sweeps to minimise edge crossings. No dagre/elk dep.
export function buildFlow(graph: WorkspaceLineage, healthById: Record<string, PipelineHealth>): { nodes: Node[]; edges: Edge[] } {
  const outgoing = new Map<string, string[]>(), incoming = new Map<string, string[]>();
  const push = (map: Map<string, string[]>, key: string, value: string) => {
    const list = map.get(key);
    if (list) list.push(value); else map.set(key, [value]);
  };
  for (const edge of graph.edges) { push(outgoing, edge.source, edge.target); push(incoming, edge.target, edge.source); }

  // Longest-path rank via Kahn; nodes on a cycle keep the rank reached before it closed.
  const rank = new Map(graph.nodes.map(node => [node.id, 0]));
  const indegree = new Map(graph.nodes.map(node => [node.id, incoming.get(node.id)?.length ?? 0]));
  const queue = graph.nodes.filter(node => !indegree.get(node.id)).map(node => node.id);
  for (let index = 0; index < queue.length; index++) {
    const id = queue[index];
    for (const next of outgoing.get(id) ?? []) {
      rank.set(next, Math.max(rank.get(next)!, rank.get(id)! + 1));
      const remaining = indegree.get(next)! - 1;
      indegree.set(next, remaining);
      if (!remaining) queue.push(next);
    }
  }

  const columns: string[][] = [];
  for (const node of graph.nodes) (columns[rank.get(node.id)!] ??= []).push(node.id);
  const orderIndex = new Map<string, number>();
  for (const column of columns) column?.forEach((id, index) => orderIndex.set(id, index));
  const barycenter = (id: string, neighbours: Map<string, string[]>) => {
    const positions = (neighbours.get(id) ?? []).map(neighbour => orderIndex.get(neighbour)!);
    return positions.length ? positions.reduce((sum, value) => sum + value, 0) / positions.length : orderIndex.get(id)!;
  };
  for (let sweep = 0; sweep < 4; sweep++) {
    const neighbours = sweep % 2 === 0 ? incoming : outgoing;
    for (const column of sweep % 2 === 0 ? columns : [...columns].reverse()) {
      if (!column) continue;
      column.sort((a, b) => barycenter(a, neighbours) - barycenter(b, neighbours));
      column.forEach((id, index) => orderIndex.set(id, index));
    }
  }

  const kindById = new Map(graph.nodes.map(node => [node.id, node.kind]));
  const sizeOf = (id: string) => kindById.get(id) === 'asset'
    ? { width: ASSET_WIDTH, height: ASSET_HEIGHT }
    : { width: PIPELINE_WIDTH, height: PIPELINE_HEIGHT };
  const position = new Map<string, { x: number; y: number }>();
  let x = 0;
  for (const column of columns) {
    if (!column?.length) continue;
    const columnWidth = Math.max(...column.map(id => sizeOf(id).width));
    const columnHeight = column.reduce((sum, id) => sum + sizeOf(id).height + ROW_GAP, -ROW_GAP);
    let y = -columnHeight / 2;
    for (const id of column) {
      const size = sizeOf(id);
      position.set(id, { x: x + (columnWidth - size.width) / 2, y });
      y += size.height + ROW_GAP;
    }
    x += columnWidth + COL_GAP;
  }

  const nodes = graph.nodes.map<Node>(node => {
    const metrics = (node as any).metrics;
    const { width, height } = sizeOf(node.id);
    const base = { id: node.id, position: position.get(node.id)!, width, height, style: { width, height }, draggable: false };
    if (node.kind === 'asset') return {
      ...base, type: 'asset',
      data: { ...node.asset, layer: normalizeLayer(node.asset.layer), materialization: node.materialization, quality: node.quality, metrics },
    };
    const data = node.kind === 'external-job'
      ? { ...node.externalJob, external: true, status: 'external', health: 'unmonitored', metrics }
      : { ...node.pipeline, ...(healthById[node.pipeline.rowId] ?? { health: 'unmonitored', breaches: [] }), metrics };
    return { ...base, type: 'pipeline', data };
  });
  const edges = graph.edges.map<Edge>(edge => ({
    id: edge.id, source: edge.source, target: edge.target,
    style: { stroke: HEALTH_COLOR[healthById[edge.pipelineRowId]?.health] ?? '#8b86f8', strokeWidth: 1.5 },
  }));
  return { nodes, edges };
}
