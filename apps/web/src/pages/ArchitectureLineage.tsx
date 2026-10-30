import { useEffect, useMemo, useState } from 'react';
import ReactFlow, {
  Background, BackgroundVariant, Controls, MiniMap,
  type ReactFlowInstance,
} from 'reactflow';
import { Link } from 'react-router';
import { AlertTriangle, RefreshCw, Search } from 'lucide-react';
import { filterWorkspaceLineage, type WorkspaceLineage } from '@dataflow/shared';
import { api } from '../api';
import { useTheme } from '../context/ThemeContext';
import { ApiError } from '../components/ApiError';
import {
  buildFlow, nodeTypes, normalizeLayer, DEFAULT_VIEWPORT, FIT_VIEW_OPTIONS,
  HEALTH_COLOR, LAYER_COLOR, type PipelineHealth,
} from './lineageFlow';

// Architecture lineage: the saved pipeline/version graph (design-time view).
// Runtime metrics live in RuntimeLineage; version history in the Changes tab.
export default function ArchitectureLineage() {
  const { dark } = useTheme();
  const [graph, setGraph] = useState<WorkspaceLineage | null>(null);
  const [healthById, setHealthById] = useState<Record<string, PipelineHealth>>({});
  const [environment, setEnvironment] = useState('');
  const [healthFilter, setHealthFilter] = useState('');
  const [domainFilter, setDomainFilter] = useState('');
  const [search, setSearch] = useState('');
  const [debouncedSearch, setDebouncedSearch] = useState('');
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [focusDepth, setFocusDepth] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [flowInstance, setFlowInstance] = useState<ReactFlowInstance | null>(null);

  const refresh = async () => {
    setLoading(true); setError(null);
    try {
      const [lineage, monitoring] = await Promise.all([
        api.workspaceLineage(environment || undefined),
        api.monitoringOverview(7).catch(() => ({ pipelines: [] })),
      ]);
      setGraph(lineage);
      setHealthById(Object.fromEntries((monitoring.pipelines as PipelineHealth[]).map(pipeline => [pipeline.id, pipeline])));
    }
    catch (e: any) { setError(e.message ?? 'Failed to load lineage'); }
    finally { setLoading(false); }
  };
  useEffect(() => {
    void refresh();
    const timer = setInterval(() => { void refresh(); }, 30_000);
    return () => clearInterval(timer);
  }, [environment]);
  useEffect(() => {
    const timer = setTimeout(() => setDebouncedSearch(search.trim()), 275);
    return () => clearTimeout(timer);
  }, [search]);

  const domains = useMemo(() => [...new Set((graph?.nodes ?? []).flatMap(node =>
    node.kind === 'pipeline' ? [node.pipeline.metadata?.domain ?? 'Unassigned'] : []))].sort(), [graph]);
  const filteredGraph = useMemo(() => graph ? filterWorkspaceLineage(graph, {
    query: debouncedSearch, domains: domainFilter ? [domainFilter] : undefined,
    focusId: focusDepth && selectedId ? selectedId : undefined, depth: focusDepth,
  }) : null, [graph, debouncedSearch, domainFilter, focusDepth, selectedId]);
  const flow = useMemo(() => filteredGraph ? buildFlow(filteredGraph, healthById) : { nodes: [], edges: [] }, [filteredGraph, healthById]);
  const flowNodeById = useMemo(() => new Map(flow.nodes.map(node => [node.id, node])), [flow.nodes]);
  const visible = useMemo(() => {
    if (!healthFilter) return flow;
    const matched = new Set(flow.nodes.filter(node =>
      node.type === 'pipeline' && node.data.health === healthFilter,
    ).map(node => node.id));
    for (const edge of flow.edges) if (matched.has(edge.source) || matched.has(edge.target)) {
      matched.add(edge.source); matched.add(edge.target);
    }
    return { nodes: flow.nodes.filter(node => matched.has(node.id)), edges: flow.edges.filter(edge => matched.has(edge.source) && matched.has(edge.target)) };
  }, [flow, healthFilter]);
  const visibleNodeKey = useMemo(() => visible.nodes.map(node => node.id).sort().join('\u0000'), [visible.nodes]);
  useEffect(() => {
    if (!flowInstance || !visibleNodeKey) return;
    const frame = requestAnimationFrame(() => {
      void flowInstance.fitView({
        nodes: visibleNodeKey.split('\u0000').map(id => ({ id })),
        ...FIT_VIEW_OPTIONS,
      });
    });
    return () => cancelAnimationFrame(frame);
  }, [flowInstance, visibleNodeKey]);
  const impact = useMemo(() => {
    if (!selectedId || !visible.nodes.some(node => node.id === selectedId)) {
      return { ...visible, upstream: new Set<string>(), downstream: new Set<string>() };
    }
    const rootId = selectedId;
    const incoming = new Map<string, string[]>(), outgoing = new Map<string, string[]>();
    for (const edge of visible.edges) {
      const sources = incoming.get(edge.target);
      if (sources) sources.push(edge.source);
      else incoming.set(edge.target, [edge.source]);
      const targets = outgoing.get(edge.source);
      if (targets) targets.push(edge.target);
      else outgoing.set(edge.source, [edge.target]);
    }
    const walk = (reverse: boolean) => {
      const found = new Set<string>([rootId]), queue = [rootId];
      for (let index = 0; index < queue.length; index++) {
        const id = queue[index];
        for (const next of (reverse ? incoming : outgoing).get(id) ?? []) if (!found.has(next)) {
          found.add(next); queue.push(next);
        }
      }
      found.delete(rootId); return found;
    };
    const upstream = walk(true), downstream = walk(false), active = new Set([rootId, ...upstream, ...downstream]);
    return {
      upstream, downstream,
      nodes: visible.nodes.map(node => ({ ...node, style: { ...node.style, opacity: active.has(node.id) ? 1 : 0.18 } })),
      edges: visible.edges.map(edge => ({ ...edge, style: { ...edge.style, opacity: active.has(edge.source) && active.has(edge.target) ? 1 : 0.12, strokeWidth: active.has(edge.source) && active.has(edge.target) ? 2.25 : 1 } })),
    };
  }, [visible, selectedId]);
  const selected = visible.nodes.find(node => node.id === selectedId);
  const selectedColumns = selected?.type === 'asset' ? (filteredGraph?.columnEdges ?? []).filter(edge =>
    edge.sourceAssetUrn === selected.data.urn || edge.targetAssetUrn === selected.data.urn) : [];
  const atRisk = [...impact.downstream].filter(id => flowNodeById.get(id)?.data.health === 'critical').length;
  const activeFilterCount = [
    environment, healthFilter, domainFilter, search.trim(), focusDepth && selectedId ? 'focus' : '',
  ].filter(Boolean).length;
  const clearFilters = () => {
    setEnvironment(''); setHealthFilter(''); setDomainFilter('');
    setSearch(''); setDebouncedSearch(''); setFocusDepth(0); setSelectedId(null);
  };

  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden">
      <div className="shrink-0 border-b border-gray-100 px-4 py-2.5 dark:border-white/[0.07] lg:px-6">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2" role="group" aria-label="Lineage filters">
          <label className="flex shrink-0 items-center gap-1.5">
            <span className="text-[10px] font-semibold uppercase tracking-wide text-gray-400">Environment</span>
            <select className="glass-input w-auto py-1.5 text-[12px]" value={environment}
              onChange={event => setEnvironment(event.target.value)} aria-label="Lineage environment">
              <option value="">All</option><option value="test">Integration</option><option value="prod">Production</option>
            </select>
          </label>
          <label className="flex shrink-0 items-center gap-1.5">
            <span className="text-[10px] font-semibold uppercase tracking-wide text-gray-400">Health</span>
            <select className="glass-input w-auto py-1.5 text-[12px]" value={healthFilter}
              onChange={event => { setHealthFilter(event.target.value); setSelectedId(null); }} aria-label="Lineage health">
              <option value="">All</option><option value="critical">Critical</option><option value="warning">Warning</option>
              <option value="healthy">Healthy</option><option value="unmonitored">Unmonitored</option>
            </select>
          </label>
          <label className="flex shrink-0 items-center gap-1.5">
            <span className="text-[10px] font-semibold uppercase tracking-wide text-gray-400">Domain</span>
            <select className="glass-input w-auto max-w-36 py-1.5 text-[12px]" value={domainFilter}
              onChange={event => { setDomainFilter(event.target.value); setSelectedId(null); }} aria-label="Lineage domain">
              <option value="">All</option>{domains.map(domain => <option key={domain} value={domain}>{domain}</option>)}
            </select>
          </label>
          <label className="flex shrink-0 items-center gap-1.5">
            <span className="text-[10px] font-semibold uppercase tracking-wide text-gray-400">Focus</span>
            <select className="glass-input w-auto py-1.5 text-[12px]" value={focusDepth}
              onChange={event => setFocusDepth(Number(event.target.value))} disabled={!selected} aria-label="Lineage graph focus">
              <option value={0}>Full graph</option><option value={1}>Selected + 1 hop</option><option value={2}>Selected + 2 hops</option>
            </select>
          </label>
          <label className="flex shrink-0 items-center gap-1.5">
            <span className="text-[10px] font-semibold uppercase tracking-wide text-gray-400">Search</span>
            <span className="relative">
              <Search size={13} className="pointer-events-none absolute left-2.5 top-2 text-gray-400" />
              <input type="search" className="glass-input w-44 py-1.5 pl-7 text-[12px]" value={search}
                onChange={event => { setSearch(event.target.value); setSelectedId(null); }}
                placeholder="Pipeline or asset" aria-label="Search lineage" />
            </span>
          </label>
          {activeFilterCount > 0 && <button className="glass-btn-ghost whitespace-nowrap px-2.5 py-1.5 text-xs"
            onClick={clearFilters} aria-label="Clear all lineage filters">Clear {activeFilterCount}</button>}
          <div className="ml-auto flex flex-wrap items-center justify-end gap-2">
            {graph && <span className="whitespace-nowrap text-[11px] text-gray-500 dark:text-white/50" aria-live="polite">
              {visible.nodes.length}/{graph.nodes.length} nodes · {visible.edges.length} links
            </span>}
            <button className="glass-btn-ghost flex items-center gap-1.5 whitespace-nowrap px-2.5 py-1.5 text-xs"
              onClick={refresh} disabled={loading} aria-label="Refresh lineage">
              <RefreshCw size={14} className={loading ? 'animate-spin' : ''} /> Refresh
            </button>
          </div>
        </div>
      </div>
      {error && <div className="m-4"><ApiError message={error} onRetry={refresh} /></div>}
      <div className="relative min-h-0 flex-1 overflow-hidden">
        {loading && <div role="status" aria-live="polite" className={`pointer-events-none absolute z-30 flex items-center justify-center ${graph ? 'left-1/2 top-12 -translate-x-1/2' : 'inset-0 bg-white/65 dark:bg-[#080a10]/65'}`}>
          <span className="flex items-center gap-2 rounded-full border border-gray-200 bg-white/95 px-3 py-1.5 text-xs text-gray-600 shadow-xs dark:border-white/10 dark:bg-[#11141d]/95 dark:text-white/65">
            <RefreshCw size={13} className="animate-spin" /> {graph ? 'Refreshing lineage…' : 'Loading lineage…'}
          </span>
        </div>}
        {!loading && graph?.nodes.length === 0 && <p className="p-8 text-sm text-gray-400">No pipeline assets found. Configure source and sink assets, then save a pipeline.</p>}
        {selected && <aside className="absolute right-4 top-14 z-20 w-72 rounded-xl border border-gray-200 bg-white/95 p-4 shadow-xl backdrop-blur-sm dark:border-white/10 dark:bg-[#11141d]/95">
          <div className="flex items-start justify-between gap-2"><div><p className="text-[10px] font-semibold uppercase tracking-wider text-brand-500">Impact analysis</p><h3 className="mt-1 text-sm font-semibold text-gray-900 dark:text-white/90">{selected.data.name}</h3></div><button className="icon-button h-7 w-7" aria-label="Close impact analysis" onClick={() => setSelectedId(null)}>×</button></div>
          <p className="mt-1 text-[10px] text-gray-400">{selected.type === 'pipeline' ? `${selected.data.external ? 'External job' : 'Pipeline'} · ${selected.data.environment}` : `${selected.data.platform} · ${selected.data.namespace}`}</p>
          <div className="mt-3 grid grid-cols-3 gap-2 text-center"><div className="rounded-lg bg-gray-50 p-2 dark:bg-white/4"><p className="text-lg font-semibold">{impact.upstream.size}</p><p className="text-[9px] uppercase text-gray-400">Upstream</p></div><div className="rounded-lg bg-gray-50 p-2 dark:bg-white/4"><p className="text-lg font-semibold">{impact.downstream.size}</p><p className="text-[9px] uppercase text-gray-400">Downstream</p></div><div className="rounded-lg bg-red-50 p-2 dark:bg-red-500/10"><p className="text-lg font-semibold text-red-500">{atRisk}</p><p className="text-[9px] uppercase text-gray-400">At risk</p></div></div>
          {selected.data.metadata?.owner && <p className="mt-3 text-xs text-gray-600 dark:text-white/60">Owner · {selected.data.metadata.owner}</p>}
          {selected.type === 'asset' && selected.data.materialization && <div className="mt-3 rounded-lg bg-emerald-50 p-2 text-[10px] dark:bg-emerald-500/10"><p className="font-semibold text-emerald-700 dark:text-emerald-300">Last materialization</p><p className="mt-1 text-gray-600 dark:text-white/55">{new Date(selected.data.materialization.materializedAt).toLocaleString()}{selected.data.materialization.recordCount != null ? ` · ${selected.data.materialization.recordCount.toLocaleString()} rows` : ''}</p><Link to={`/runs/${selected.data.materialization.executionId}`} className="mt-1 block text-brand-500">Open producing run</Link></div>}
          {selected.type === 'asset' && selected.data.quality && <div className={`mt-3 rounded-lg p-2 text-[10px] ${selected.data.quality.status === 'passed' ? 'bg-emerald-50 dark:bg-emerald-500/10' : 'bg-amber-50 dark:bg-amber-500/10'}`}><p className="font-semibold">Data quality · {selected.data.quality.status}</p><p className="mt-1 text-gray-600 dark:text-white/55">{selected.data.quality.passedCount.toLocaleString()} passed · {selected.data.quality.failedCount.toLocaleString()} rejected{selected.data.quality.quarantineAvailable ? ' · quarantined' : ''}</p><Link to={`/runs/${selected.data.quality.executionId}`} className="mt-1 block text-brand-500">Open quality run</Link></div>}
          {selected.type === 'pipeline' && !selected.data.external && <div className="mt-3 rounded-lg border p-2" style={{ borderColor: HEALTH_COLOR[selected.data.health ?? 'unmonitored'] }}><p className="text-[10px] font-semibold capitalize" style={{ color: HEALTH_COLOR[selected.data.health ?? 'unmonitored'] }}>{selected.data.health ?? 'unmonitored'}</p>{selected.data.breaches?.map((breach: any) => <p key={breach.type} className="mt-1 flex gap-1 text-[10px] text-gray-600 dark:text-white/55"><AlertTriangle size={11} className="mt-0.5 shrink-0" />{breach.message}</p>)}{selected.data.last_execution_id && <Link to={`/runs/${selected.data.last_execution_id}`} className="mt-2 block text-[10px] text-brand-500">Open latest run · {selected.data.last_phase}</Link>}</div>}
          {selected.type === 'pipeline' && selected.data.external && <p className="mt-3 text-[10px] text-gray-600 dark:text-white/55">Last observed {new Date(selected.data.eventTime).toLocaleString()}</p>}
          {selected.data.schema?.fields?.length > 0 && <div className="mt-3"><p className="text-[10px] font-semibold uppercase text-gray-400">Contract fields</p><p className="mt-1 text-[10px] text-gray-600 dark:text-white/55">{selected.data.schema.fields.slice(0, 8).map((field: any) => `${field.name}: ${field.type}${field.nullable ? '?' : ''}`).join(' · ')}</p></div>}
          {selectedColumns.length > 0 && <div className="mt-3"><p className="text-[10px] font-semibold uppercase text-gray-400">Column lineage</p><div className="mt-1 max-h-32 space-y-1 overflow-auto">{selectedColumns.slice(0, 20).map(edge => <p key={edge.id} className="truncate text-[10px] text-gray-600 dark:text-white/55" title={`${edge.sourceAssetUrn}.${edge.sourceField} → ${edge.targetAssetUrn}.${edge.targetField}`}>{edge.sourceField} → {edge.targetField}</p>)}</div></div>}
        </aside>}
        <ReactFlow nodes={impact.nodes} edges={impact.edges} nodeTypes={nodeTypes} fitView
          fitViewOptions={FIT_VIEW_OPTIONS} defaultViewport={DEFAULT_VIEWPORT} minZoom={0.05}
          onlyRenderVisibleElements
          onInit={setFlowInstance}
          nodesDraggable={false} nodesConnectable={false} elementsSelectable
          onNodeClick={(_, node) => setSelectedId(node.id)} onPaneClick={() => setSelectedId(null)}>
          <Background variant={BackgroundVariant.Dots} gap={24} size={1} color={dark ? 'rgba(255,255,255,.06)' : 'rgba(0,0,0,.06)'} />
          <Controls showInteractive={false} />
          <MiniMap className="hidden md:block" pannable zoomable
            nodeColor={node => node.type === 'pipeline' ? '#7c6cf2' : LAYER_COLOR[normalizeLayer(node.data.layer)]}
            nodeStrokeColor={dark ? 'rgba(255,255,255,0.5)' : 'rgba(0,0,0,0.3)'}
            nodeStrokeWidth={2}
            maskColor={dark ? 'rgba(8,10,18,0.78)' : 'rgba(235,237,245,0.78)'}
          />
        </ReactFlow>
      </div>
    </div>
  );
}
