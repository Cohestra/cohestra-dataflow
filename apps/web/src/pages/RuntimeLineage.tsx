import { useEffect, useMemo, useRef, useState } from 'react';
import ReactFlow, { Background, BackgroundVariant, Controls, type ReactFlowInstance } from 'reactflow';
import { Link } from 'react-router';
import { Activity, ExternalLink, RefreshCw, Search, X } from 'lucide-react';
import { filterWorkspaceLineage, type WorkspaceLineage } from '@dataflow/shared';
import { api } from '../api';
import { useTheme } from '../context/ThemeContext';
import { ApiError } from '../components/ApiError';
import {
  buildFlow, nodeTypes, normalizeLayer, DEFAULT_VIEWPORT, FIT_VIEW_OPTIONS, LAYER_COLOR,
  formatCount, formatDuration, type LineageLayer, type PipelineHealth,
} from './lineageFlow';

// Runtime lineage: New Relic-style aggregated execution explorer. Stable
// asset/pipeline topology decorated with windowed metrics, a cursor-paginated
// runs table, and a per-execution waterfall drawer.

const PRESETS = [
  { key: '15m', label: 'Last 15 min', ms: 15 * 60_000 },
  { key: '1h', label: 'Last 1 hour', ms: 60 * 60_000 },
  { key: '6h', label: 'Last 6 hours', ms: 6 * 60 * 60_000 },
  { key: '24h', label: 'Last 24 hours', ms: 24 * 60 * 60_000 },
  { key: '3d', label: 'Last 3 days', ms: 3 * 24 * 60 * 60_000 },
  { key: '7d', label: 'Last 7 days', ms: 7 * 24 * 60 * 60_000 },
] as const;
const MAX_WINDOW_MS = 7 * 24 * 60 * 60_000;

const PHASE_STYLE: Record<string, string> = {
  completed: 'text-emerald-700 bg-emerald-50 dark:text-emerald-300 dark:bg-emerald-500/10',
  failed: 'text-red-700 bg-red-50 dark:text-red-300 dark:bg-red-500/10',
  running: 'text-blue-700 bg-blue-50 dark:text-blue-300 dark:bg-blue-500/10',
  cancelled: 'text-gray-600 bg-gray-100 dark:text-white/50 dark:bg-white/6',
};

type RuntimeOverview = {
  from: string; to: string;
  nodes: any[]; edges: any[];
  stats: Record<string, number>;
};
type RunRow = {
  id: string; pipeline_id: string; pipeline_key: string; name: string;
  environment: string; phase: string; trigger_type: string;
  started_at: string; completed_at: string | null; duration_ms: number | null;
  run_id: string | null; trace_id: string | null; retry_of: string | null;
  records: number | null; failed_nodes: number | null;
};
type RunDetail = {
  execution: Record<string, any>;
  definition: { nodes: any[]; edges: any[] };
  nodeRuns: Array<Record<string, any>>;
  qualityResults: Array<Record<string, any>>;
};

function PhasePill({ phase }: { phase: string }) {
  return <span className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] font-semibold capitalize ${PHASE_STYLE[phase] ?? PHASE_STYLE.cancelled}`}>
    {phase === 'running' && <Activity size={9} className="animate-pulse" />}{phase}
  </span>;
}

function Identifier({ label, value }: { label: string; value: string | null }) {
  if (!value) return <span className="text-gray-300 dark:text-white/25">—</span>;
  return <span className="font-mono text-[10px]" title={`${label}: ${value}`}>{value.length > 18 ? `${value.slice(0, 8)}…${value.slice(-6)}` : value}</span>;
}

// Kahn topological order over the pipeline definition so drawer steps follow
// the real DAG, not insertion order.
function orderSteps(definition: RunDetail['definition']): any[] {
  const nodes = definition?.nodes ?? [];
  const indegree = new Map<string, number>(nodes.map((node: any) => [node.id, 0]));
  const outgoing = new Map<string, string[]>();
  for (const edge of definition?.edges ?? []) {
    if (!indegree.has(edge.target) || !indegree.has(edge.source)) continue;
    indegree.set(edge.target, (indegree.get(edge.target) ?? 0) + 1);
    outgoing.set(edge.source, [...(outgoing.get(edge.source) ?? []), edge.target]);
  }
  const byId = new Map(nodes.map((node: any) => [node.id, node]));
  const queue = nodes.filter((node: any) => (indegree.get(node.id) ?? 0) === 0).map((node: any) => node.id);
  const ordered: any[] = [];
  const seen = new Set<string>();
  while (queue.length) {
    const id = queue.shift()!;
    if (seen.has(id)) continue;
    seen.add(id);
    ordered.push(byId.get(id));
    for (const next of outgoing.get(id) ?? []) {
      indegree.set(next, (indegree.get(next) ?? 1) - 1);
      if ((indegree.get(next) ?? 0) <= 0) queue.push(next);
    }
  }
  for (const node of nodes) if (!seen.has(node.id)) ordered.push(node); // cycles: append rather than drop
  return ordered;
}

function ExecutionDrawer({ executionId, onClose }: { executionId: string; onClose: () => void }) {
  const [detail, setDetail] = useState<RunDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    setDetail(null); setError(null);
    api.runtimeLineageRun(executionId).then(setDetail).catch((e: any) => setError(e.message ?? 'Failed to load execution'));
  }, [executionId]);

  const steps = useMemo(() => {
    if (!detail) return [];
    const runsByNode = new Map(detail.nodeRuns.map(run => [run.node_id, run]));
    const qualityByNode = new Map(detail.qualityResults.map(result => [result.node_id, result]));
    return orderSteps(detail.definition).map((node: any) => ({
      node, run: runsByNode.get(node.id), quality: qualityByNode.get(node.id),
      layer: node.type === 'source' || node.type === 'sink' ? normalizeLayer(node.config?.layer) : null,
    }));
  }, [detail]);

  const waterfall = useMemo(() => {
    if (!detail) return { t0: 0, total: 1 };
    const executionStart = new Date(detail.execution.started_at).getTime();
    let t0 = executionStart, end = executionStart;
    for (const step of steps) {
      if (!step.run) continue;
      const started = step.run.started_at ? new Date(step.run.started_at).getTime()
        : step.run.finished_at ? new Date(step.run.finished_at).getTime() - (step.run.duration_ms ?? 0) : null;
      const finished = step.run.finished_at ? new Date(step.run.finished_at).getTime() : null;
      if (started != null) t0 = Math.min(t0, started);
      if (finished != null) end = Math.max(end, finished);
    }
    if (detail.execution.completed_at) end = Math.max(end, new Date(detail.execution.completed_at).getTime());
    return { t0, total: Math.max(end - t0, 1) };
  }, [detail, steps]);

  const layerPath = useMemo(() => {
    const present = new Set(steps.flatMap(step => step.layer ? [step.layer] : []));
    return (['external', 'bronze', 'silver', 'gold'] as LineageLayer[]).filter(layer => present.has(layer));
  }, [steps]);

  const execution = detail?.execution;
  const durationMs = execution?.completed_at
    ? new Date(execution.completed_at).getTime() - new Date(execution.started_at).getTime()
    : execution ? Date.now() - new Date(execution.started_at).getTime() : null;

  return (
    <aside className="absolute inset-y-0 right-0 z-30 flex w-full max-w-xl flex-col border-l border-gray-200 bg-white shadow-2xl dark:border-white/10 dark:bg-[#0d1017]" role="dialog" aria-label="Execution details">
      <div className="flex items-start justify-between gap-3 border-b border-gray-100 px-4 py-3 dark:border-white/[0.07]">
        <div className="min-w-0">
          <p className="text-[10px] font-semibold uppercase tracking-wider text-brand-500">Execution</p>
          <h3 className="mt-0.5 truncate text-sm font-semibold text-gray-900 dark:text-white/90">{execution?.name ?? executionId}</h3>
          {execution && <div className="mt-1 flex flex-wrap items-center gap-2 text-[10px] text-gray-500 dark:text-white/50">
            <PhasePill phase={execution.phase} />
            <span>{execution.environment === 'prod' ? 'Production' : 'Integration'}</span>
            <span>{new Date(execution.started_at).toLocaleString()}</span>
            <span>{formatDuration(durationMs)}</span>
            {execution.retry_of && <span className="text-amber-600 dark:text-amber-400">retry of {String(execution.retry_of).slice(0, 13)}…</span>}
          </div>}
        </div>
        <button className="icon-button h-7 w-7 shrink-0" aria-label="Close execution drawer" onClick={onClose}><X size={14} /></button>
      </div>
      <div className="min-h-0 flex-1 overflow-auto px-4 py-3">
        {error && <ApiError message={error} />}
        {!detail && !error && <p className="flex items-center gap-2 text-xs text-gray-500 dark:text-white/50"><RefreshCw size={13} className="animate-spin" /> Loading execution…</p>}
        {execution && <>
          <dl className="grid grid-cols-1 gap-x-4 gap-y-1 text-[11px] sm:grid-cols-2">
            {[['Execution ID', execution.id], ['Temporal run ID', execution.run_id], ['Trace ID', execution.trace_id], ['Trigger', execution.trigger_type]].map(([label, value]) => <div key={label as string} className="flex items-baseline justify-between gap-2 sm:justify-start">
              <dt className="shrink-0 text-gray-400 dark:text-white/40">{label}</dt>
              <dd className="truncate font-mono text-gray-700 dark:text-white/70" title={String(value ?? '')}>{value ?? '—'}</dd>
            </div>)}
          </dl>
          {layerPath.length > 0 && <div className="mt-3 flex flex-wrap items-center gap-1.5" aria-label="Layer path">
            {layerPath.map((layer, index) => <span key={layer} className="flex items-center gap-1.5">
              {index > 0 && <span className="text-gray-300 dark:text-white/25">→</span>}
              <span className="rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-white" style={{ background: LAYER_COLOR[layer] }}>
                {layer === 'external' ? 'source' : layer}
              </span>
            </span>)}
          </div>}
          <h4 className="mt-4 text-[10px] font-semibold uppercase tracking-wider text-gray-400">Runtime waterfall</h4>
          <div className="mt-2 space-y-2">
            {steps.map(({ node, run, quality, layer }) => {
              const started = run?.started_at ? new Date(run.started_at).getTime()
                : run?.finished_at ? new Date(run.finished_at).getTime() - (run.duration_ms ?? 0) : null;
              const offset = started != null ? Math.max(0, Math.min(100, ((started - waterfall.t0) / waterfall.total) * 100)) : 0;
              const width = run?.duration_ms != null ? Math.max(2, Math.min(100 - offset, (run.duration_ms / waterfall.total) * 100)) : 0;
              const barColor = !run ? '#cbd5e1' : run.status === 'failed' ? '#ef4444' : '#10b981';
              return (
                <details key={node.id} className="group rounded-lg border border-gray-100 dark:border-white/[0.07]">
                  <summary className="cursor-pointer list-none px-3 py-2">
                    <div className="flex items-center justify-between gap-2">
                      <span className="flex min-w-0 items-center gap-2">
                        {layer && <span className="h-2 w-2 shrink-0 rounded-full" style={{ background: LAYER_COLOR[layer] }} />}
                        <span className="truncate text-xs font-medium text-gray-800 dark:text-white/80">{node.id}</span>
                        <span className="truncate text-[10px] text-gray-400 dark:text-white/40">{node.activityType}</span>
                        {(run?.attempt ?? 1) > 1 && <span className="shrink-0 rounded-full bg-amber-50 px-1.5 text-[9px] font-semibold text-amber-600 dark:bg-amber-500/10 dark:text-amber-400">attempt {run!.attempt}</span>}
                      </span>
                      <span className="flex shrink-0 items-center gap-2 text-[10px] text-gray-500 dark:text-white/50">
                        {run?.record_count != null && <span>{formatCount(run.record_count)} rec</span>}
                        <span>{formatDuration(run?.duration_ms)}</span>
                        {run ? <PhasePill phase={run.status === 'success' ? 'completed' : run.status} /> : <span className="text-gray-300 dark:text-white/25">not run</span>}
                      </span>
                    </div>
                    <div className="mt-1.5 h-1.5 w-full overflow-hidden rounded-full bg-gray-100 dark:bg-white/6">
                      {run && <div className="h-full rounded-full" style={{ marginLeft: `${offset}%`, width: `${width}%`, background: barColor }} />}
                    </div>
                    {quality && <p className={`mt-1 text-[10px] font-semibold ${quality.status === 'passed' ? 'text-emerald-600 dark:text-emerald-400' : quality.status === 'failed' ? 'text-red-500' : 'text-amber-600 dark:text-amber-400'}`}>
                      Quality {quality.status} · {formatCount(quality.passed_count)} passed · {formatCount(quality.failed_count)} rejected
                    </p>}
                    {run?.error && <p className="mt-1 truncate text-[10px] text-red-500" title={run.error}>{run.error}</p>}
                  </summary>
                  <div className="border-t border-gray-100 px-3 py-2 text-[10px] dark:border-white/[0.07]">
                    <p className="font-semibold uppercase tracking-wide text-gray-400">Step attributes</p>
                    <pre className="mt-1 max-h-40 overflow-auto whitespace-pre-wrap break-all text-gray-600 dark:text-white/60">{JSON.stringify({
                      type: node.type, activityType: node.activityType, config: node.config,
                      startedAt: run?.started_at ?? null, finishedAt: run?.finished_at ?? null,
                      durationMs: run?.duration_ms ?? null, records: run?.record_count ?? null,
                      attempt: run?.attempt ?? null, error: run?.error ?? null,
                    }, null, 2)}</pre>
                  </div>
                </details>
              );
            })}
          </div>
        </>}
      </div>
      <div className="border-t border-gray-100 px-4 py-2.5 dark:border-white/[0.07]">
        <Link to={`/runs/${executionId}`} className="flex items-center gap-1.5 text-xs font-medium text-brand-500">
          <ExternalLink size={13} /> Open full run page
        </Link>
      </div>
    </aside>
  );
}

export default function RuntimeLineage() {
  const { dark } = useTheme();
  const [preset, setPreset] = useState<string>('1h');
  const [customFrom, setCustomFrom] = useState('');
  const [customTo, setCustomTo] = useState('');
  const [windowError, setWindowError] = useState<string | null>(null);
  const [appliedWindow, setAppliedWindow] = useState<{ from?: string; to?: string }>({});
  const [environment, setEnvironment] = useState('');
  const [status, setStatus] = useState('');
  const [pipeline, setPipeline] = useState('');
  const [layerFilter, setLayerFilter] = useState('');
  const [activeOnly, setActiveOnly] = useState(true);
  const [search, setSearch] = useState('');
  const [debouncedSearch, setDebouncedSearch] = useState('');
  const [overview, setOverview] = useState<RuntimeOverview | null>(null);
  const [runs, setRuns] = useState<RunRow[]>([]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadingRuns, setLoadingRuns] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [selectedRunId, setSelectedRunId] = useState<string | null>(null);
  const [flowInstance, setFlowInstance] = useState<ReactFlowInstance | null>(null);
  const requestSeq = useRef(0);

  // Presets resolve at request time; the backend applies the same defaults and
  // re-validates the 7-day cap.
  const windowParams = (): { from?: string; to?: string } | null => {
    if (preset !== 'custom') {
      const spec = PRESETS.find(item => item.key === preset) ?? PRESETS[1];
      if (spec.key === '1h') return {}; // backend default — /lineage opens with a bare last-hour request
      const to = new Date();
      return { from: new Date(to.getTime() - spec.ms).toISOString(), to: to.toISOString() };
    }
    if (!customFrom || !customTo) { setWindowError('Set both start and end'); return null; }
    const from = new Date(customFrom), to = new Date(customTo);
    if (!(from < to)) { setWindowError('Start must be before end'); return null; }
    if (to.getTime() - from.getTime() > MAX_WINDOW_MS) { setWindowError('Range cannot exceed 7 days'); return null; }
    setWindowError(null);
    return { from: from.toISOString(), to: to.toISOString() };
  };

  const baseParams = (window: { from?: string; to?: string }) => ({
    ...window,
    ...(environment ? { environment } : {}),
    ...(status ? { status } : {}),
    ...(pipeline ? { pipeline } : {}),
    ...(layerFilter ? { layer: layerFilter } : {}),
  });

  const refresh = async () => {
    const window = windowParams();
    if (!window) return;
    setAppliedWindow(window);
    const seq = ++requestSeq.current;
    setLoading(true); setError(null);
    try {
      const params = baseParams(window);
      const [overviewResult, runsResult] = await Promise.all([
        api.runtimeLineageOverview(params),
        api.runtimeLineageRuns({ ...params, ...(debouncedSearch ? { query: debouncedSearch } : {}), limit: '50' }),
      ]);
      if (seq !== requestSeq.current) return;
      setOverview(overviewResult);
      setRuns(runsResult.items ?? []);
      setNextCursor(runsResult.nextCursor ?? null);
    } catch (e: any) {
      if (seq === requestSeq.current) setError(e.message ?? 'Failed to load runtime lineage');
    } finally {
      if (seq === requestSeq.current) setLoading(false);
    }
  };

  useEffect(() => { void refresh(); }, [preset, environment, status, pipeline, layerFilter, debouncedSearch]);
  useEffect(() => {
    const timer = setTimeout(() => setDebouncedSearch(search.trim()), 275);
    return () => clearTimeout(timer);
  }, [search]);

  const loadMore = async () => {
    if (!nextCursor) return;
    setLoadingRuns(true);
    try {
      const page = await api.runtimeLineageRuns({
        ...baseParams(appliedWindow), ...(debouncedSearch ? { query: debouncedSearch } : {}),
        limit: '50', cursor: nextCursor,
      });
      setRuns(existing => [...existing, ...(page.items ?? [])]);
      setNextCursor(page.nextCursor ?? null);
    } catch (e: any) { setError(e.message ?? 'Failed to load more runs'); }
    finally { setLoadingRuns(false); }
  };

  const pipelineOptions = useMemo(() => {
    const seen = new Map<string, string>();
    for (const node of overview?.nodes ?? []) if (node.kind === 'pipeline') {
      seen.set(String(node.pipeline.pipelineKey), node.pipeline.name);
    }
    return [...seen.entries()].sort((a, b) => a[1].localeCompare(b[1]));
  }, [overview]);

  const healthById = useMemo(() => {
    const map: Record<string, PipelineHealth> = {};
    for (const node of overview?.nodes ?? []) if (node.kind === 'pipeline') {
      const metrics = node.metrics ?? {};
      map[node.pipeline.rowId] = {
        id: node.pipeline.rowId,
        health: (metrics.failed ?? 0) > 0 ? 'critical' : (metrics.runs ?? 0) > 0 ? 'healthy' : 'unmonitored',
      };
    }
    return map;
  }, [overview]);

  // New Relic-style default: the map shows only entities that reported data in
  // the window. Idle pipelines stay reachable via the toggle / pipeline filter.
  const filteredGraph = useMemo(() => {
    if (!overview) return null;
    let nodes = overview.nodes, edges = overview.edges;
    if (activeOnly) {
      const activePipelines = new Set(nodes
        .filter(node => node.kind === 'pipeline' && (node.metrics?.runs ?? 0) > 0)
        .map(node => node.id));
      const activeAssets = new Set(edges
        .filter(edge => activePipelines.has(edge.source) || activePipelines.has(edge.target))
        .flatMap(edge => [edge.source, edge.target]));
      nodes = nodes.filter(node => activePipelines.has(node.id) || (node.kind === 'asset' && activeAssets.has(node.id)));
      const kept = new Set(nodes.map(node => node.id));
      edges = edges.filter(edge => kept.has(edge.source) && kept.has(edge.target));
    }
    const graph = { ...overview, nodes, edges, columnEdges: [] } as unknown as WorkspaceLineage;
    return layerFilter ? filterWorkspaceLineage(graph, { layers: [layerFilter as any] }) : graph;
  }, [overview, layerFilter, activeOnly]);
  const flow = useMemo(() => filteredGraph ? buildFlow(filteredGraph, healthById) : { nodes: [], edges: [] }, [filteredGraph, healthById]);
  const flowNodeKey = useMemo(() => flow.nodes.map(node => node.id).sort().join('\u0000'), [flow.nodes]);
  useEffect(() => {
    if (!flowInstance || !flowNodeKey) return;
    const frame = requestAnimationFrame(() => {
      void flowInstance.fitView({ nodes: flowNodeKey.split('\u0000').map(id => ({ id })), ...FIT_VIEW_OPTIONS });
    });
    return () => cancelAnimationFrame(frame);
  }, [flowInstance, flowNodeKey]);

  const stats = overview?.stats;

  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden">
      <div className="shrink-0 border-b border-gray-100 px-4 py-2.5 dark:border-white/[0.07] lg:px-6">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2" role="group" aria-label="Runtime lineage filters">
          <label className="flex shrink-0 items-center gap-1.5">
            <span className="text-[10px] font-semibold uppercase tracking-wide text-gray-400">Time</span>
            <select className="glass-input w-auto py-1.5 text-[12px]" value={preset}
              onChange={event => setPreset(event.target.value)} aria-label="Runtime time range">
              {PRESETS.map(item => <option key={item.key} value={item.key}>{item.label}</option>)}
              <option value="custom">Custom</option>
            </select>
          </label>
          {preset === 'custom' && <span className="flex items-center gap-1.5">
            <input type="datetime-local" className="glass-input w-auto py-1.5 text-[12px]" value={customFrom}
              onChange={event => setCustomFrom(event.target.value)} aria-label="Custom range start" />
            <span className="text-[10px] text-gray-400">to</span>
            <input type="datetime-local" className="glass-input w-auto py-1.5 text-[12px]" value={customTo}
              onChange={event => setCustomTo(event.target.value)} aria-label="Custom range end" />
            <button className="glass-btn-ghost px-2.5 py-1.5 text-xs" onClick={refresh}>Apply</button>
          </span>}
          <label className="flex shrink-0 items-center gap-1.5">
            <span className="text-[10px] font-semibold uppercase tracking-wide text-gray-400">Environment</span>
            <select className="glass-input w-auto py-1.5 text-[12px]" value={environment}
              onChange={event => setEnvironment(event.target.value)} aria-label="Runtime environment">
              <option value="">All</option><option value="test">Integration</option><option value="prod">Production</option>
            </select>
          </label>
          <label className="flex shrink-0 items-center gap-1.5">
            <span className="text-[10px] font-semibold uppercase tracking-wide text-gray-400">Status</span>
            <select className="glass-input w-auto py-1.5 text-[12px]" value={status}
              onChange={event => setStatus(event.target.value)} aria-label="Runtime status">
              <option value="">All</option><option value="running">Running</option><option value="completed">Completed</option>
              <option value="failed">Failed</option><option value="cancelled">Cancelled</option>
            </select>
          </label>
          <label className="flex shrink-0 items-center gap-1.5">
            <span className="text-[10px] font-semibold uppercase tracking-wide text-gray-400">Pipeline</span>
            <select className="glass-input w-auto max-w-40 py-1.5 text-[12px]" value={pipeline}
              onChange={event => setPipeline(event.target.value)} aria-label="Runtime pipeline">
              <option value="">All</option>
              {pipelineOptions.map(([key, name]) => <option key={key} value={key}>{name}</option>)}
            </select>
          </label>
          <label className="flex shrink-0 items-center gap-1.5">
            <span className="text-[10px] font-semibold uppercase tracking-wide text-gray-400">Layer</span>
            <select className="glass-input w-auto py-1.5 text-[12px]" value={layerFilter}
              onChange={event => setLayerFilter(event.target.value)} aria-label="Runtime layer">
              <option value="">All</option><option value="external">Sources</option><option value="bronze">Bronze</option>
              <option value="silver">Silver</option><option value="gold">Gold</option>
            </select>
          </label>
          <label className="flex shrink-0 items-center gap-1.5">
            <span className="relative">
              <Search size={13} className="pointer-events-none absolute left-2.5 top-2 text-gray-400" />
              <input type="search" className="glass-input w-56 py-1.5 pl-7 text-[12px]" value={search}
                onChange={event => setSearch(event.target.value)}
                placeholder="Execution, run, or trace ID" aria-label="Search runs by id" />
            </span>
          </label>
          <label className="flex shrink-0 cursor-pointer items-center gap-1.5">
            <input type="checkbox" className="h-3.5 w-3.5 accent-brand-500" checked={activeOnly}
              onChange={event => setActiveOnly(event.target.checked)} aria-label="Show only pipelines with runs in window" />
            <span className="text-[10px] font-semibold uppercase tracking-wide text-gray-400">Active only</span>
          </label>
          <div className="ml-auto flex flex-wrap items-center justify-end gap-2">
            <button className="glass-btn-ghost flex items-center gap-1.5 whitespace-nowrap px-2.5 py-1.5 text-xs"
              onClick={refresh} disabled={loading} aria-label="Refresh runtime lineage">
              <RefreshCw size={14} className={loading ? 'animate-spin' : ''} /> Refresh
            </button>
          </div>
        </div>
        {windowError && <p className="mt-1.5 text-[11px] text-red-500" role="alert">{windowError}</p>}
        {stats && <div className="mt-2.5 grid grid-cols-2 gap-2 sm:grid-cols-5" aria-live="polite">
          {[
            { label: 'Runs', value: stats.runs, tone: '' },
            { label: 'Error rate', value: stats.runs > 0 ? `${Math.round((stats.failed / stats.runs) * 1000) / 10}%` : '—', tone: stats.failed > 0 ? 'text-red-500' : 'text-emerald-600 dark:text-emerald-400' },
            { label: 'Failed', value: stats.failed, tone: stats.failed > 0 ? 'text-red-500' : '' },
            { label: 'Running', value: stats.running, tone: stats.running > 0 ? 'text-blue-500' : '' },
            { label: 'Active pipelines', value: `${stats.activePipelines}/${stats.pipelines}`, tone: '' },
          ].map(tile => (
            <div key={tile.label} className="rounded-lg border border-gray-100 bg-white/60 px-3 py-1.5 dark:border-white/[0.07] dark:bg-white/3">
              <p className="text-[9px] font-semibold uppercase tracking-wider text-gray-400">{tile.label}</p>
              <p className={`text-base font-semibold leading-tight text-gray-900 dark:text-white/90 ${tile.tone}`}>{tile.value}</p>
            </div>
          ))}
        </div>}
      </div>
      {error && <div className="m-4"><ApiError message={error} onRetry={refresh} /></div>}
      <div className="relative min-h-0 flex-1 overflow-hidden">
        <div className="flex h-full min-h-0 flex-col">
          <div className="relative min-h-0 flex-3 overflow-hidden">
            {loading && <div role="status" aria-live="polite" className={`pointer-events-none absolute z-30 flex items-center justify-center ${overview ? 'left-1/2 top-12 -translate-x-1/2' : 'inset-0 bg-white/65 dark:bg-[#080a10]/65'}`}>
              <span className="flex items-center gap-2 rounded-full border border-gray-200 bg-white/95 px-3 py-1.5 text-xs text-gray-600 shadow-xs dark:border-white/10 dark:bg-[#11141d]/95 dark:text-white/65">
                <RefreshCw size={13} className="animate-spin" /> {overview ? 'Refreshing…' : 'Loading runtime lineage…'}
              </span>
            </div>}
            {!loading && overview?.nodes.length === 0 && <p className="p-8 text-sm text-gray-400">No pipelines found for this window and filters.</p>}
            <ReactFlow nodes={flow.nodes} edges={flow.edges} nodeTypes={nodeTypes} fitView
              fitViewOptions={FIT_VIEW_OPTIONS} defaultViewport={DEFAULT_VIEWPORT} minZoom={0.05}
              onInit={setFlowInstance}
              onNodeClick={(_, node) => {
                if (node.type === 'pipeline' && node.data.pipelineKey) {
                  setPipeline(current => current === String(node.data.pipelineKey) ? '' : String(node.data.pipelineKey));
                }
              }}
              nodesDraggable={false} nodesConnectable={false} elementsSelectable={false}>
              <Background variant={BackgroundVariant.Dots} gap={24} size={1} color={dark ? 'rgba(255,255,255,.06)' : 'rgba(0,0,0,.06)'} />
              <Controls showInteractive={false} />
            </ReactFlow>
          </div>
          <div className="min-h-0 flex-2 overflow-auto border-t border-gray-100 dark:border-white/[0.07]" aria-label="Runs and traces">
            <table className="w-full min-w-[960px] text-left text-[11px]">
              <thead className="sticky top-0 z-10 bg-white text-[10px] uppercase tracking-wide text-gray-400 dark:bg-[#0b0e15]">
                <tr>{['Started', 'Pipeline', 'Env', 'Status', 'Duration', 'Execution ID', 'Run ID', 'Trace ID', 'Records / errors'].map(header =>
                  <th key={header} className="whitespace-nowrap px-3 py-2 font-semibold">{header}</th>)}</tr>
              </thead>
              <tbody>
                {runs.map(run => (
                  <tr key={run.id}
                    className={`cursor-pointer border-t border-gray-50 hover:bg-gray-50 dark:border-white/4 dark:hover:bg-white/3 ${selectedRunId === run.id ? 'bg-brand-50 dark:bg-brand-500/10' : ''}`}
                    onClick={() => setSelectedRunId(run.id)}>
                    <td className="whitespace-nowrap px-3 py-1.5 text-gray-600 dark:text-white/60">{new Date(run.started_at).toLocaleString()}</td>
                    <td className="max-w-40 truncate px-3 py-1.5 font-medium text-gray-800 dark:text-white/80" title={run.name}>{run.name}</td>
                    <td className="px-3 py-1.5">{run.environment === 'prod' ? 'Prod' : 'Test'}</td>
                    <td className="px-3 py-1.5"><PhasePill phase={run.phase} /></td>
                    <td className="whitespace-nowrap px-3 py-1.5">{formatDuration(run.duration_ms)}</td>
                    <td className="px-3 py-1.5"><Identifier label="Execution ID" value={run.id} /></td>
                    <td className="px-3 py-1.5"><Identifier label="Run ID" value={run.run_id} /></td>
                    <td className="px-3 py-1.5"><Identifier label="Trace ID" value={run.trace_id} /></td>
                    <td className="whitespace-nowrap px-3 py-1.5">
                      {formatCount(run.records)}{(run.failed_nodes ?? 0) > 0 && <span className="ml-1.5 font-semibold text-red-500">{run.failed_nodes} failed</span>}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {!loading && runs.length === 0 && <p className="p-6 text-center text-xs text-gray-400">
              {debouncedSearch ? 'No execution matches that ID inside the selected window.' : 'No runs in the selected window.'}
            </p>}
            {nextCursor && <div className="flex justify-center border-t border-gray-50 py-2 dark:border-white/4">
              <button className="glass-btn-ghost px-3 py-1.5 text-xs" onClick={loadMore} disabled={loadingRuns}>
                {loadingRuns ? 'Loading…' : 'Load more runs'}
              </button>
            </div>}
          </div>
        </div>
        {selectedRunId && <ExecutionDrawer executionId={selectedRunId} onClose={() => setSelectedRunId(null)} />}
      </div>
    </div>
  );
}
