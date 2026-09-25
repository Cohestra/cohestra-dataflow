import { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router';
import {
  ChevronRight, Clock, Play, RefreshCw, RotateCcw, Search, X,
} from 'lucide-react';
import { api } from '../api';
import { deriveStage, type Stage } from '../utils/pipelineStage';
import { ApiError } from '../components/ApiError';
import { useCatalog } from '../context/CatalogContext';
import { ActivityIcon } from '../components/canvas/FlowNode';

// ── types ─────────────────────────────────────────────────────────────────────

interface Pipeline {
  id: string;
  pipeline_key: string;
  version: number;
  name: string;
  status: string;
  environment: string;
  created_at: string;
  // ponytail: list rows are a summary projection (Gate 1 keyset pagination) —
  // no `definition` column. trigger_type is derived server-side; the drawer
  // fetches the full definition on demand via api.getPipeline().
  trigger_type: string | null;
  last_run_phase: string | null;
  last_run_at: string | null;
  last_run_id: string | null;
  last_run_version: number | null;
  // view=current rows only: one row per logical pipeline/environment.
  active_version?: number | null;
  version_count?: number;
}

interface Execution {
  id: string;
  phase: string;
  started_at: string;
  completed_at: string | null;
  error?: string;
  record_count?: number;
  pipeline_version?: number;
}

// ── helpers ───────────────────────────────────────────────────────────────────

function reltime(iso: string | null): string {
  if (!iso) return '—';
  const diff = (Date.now() - new Date(iso).getTime()) / 1000;
  if (diff < 60) return `${Math.round(diff)}s ago`;
  if (diff < 3600) return `${Math.round(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.round(diff / 3600)}h ago`;
  return `${Math.round(diff / 86400)}d ago`;
}

function duration(started: string, completed: string | null): string {
  if (!completed) return '—';
  const ms = new Date(completed).getTime() - new Date(started).getTime();
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
  return `${Math.round(ms / 60000)}m`;
}

// ponytail: activityIcon() removed — use ActivityIcon from FlowNode directly

function versionLabel(p: Pipeline): string {
  const count = (p.version_count ?? 1) > 1 ? ` of ${p.version_count}` : '';
  const active = p.active_version != null && p.active_version !== p.version ? ` · v${p.active_version} active` : '';
  return `v${p.version}${count}${active}`;
}

function stageOf(p: Pipeline): Stage { return deriveStage(p.status, p.environment); }

function pipelineName(p: Pipeline): string { return p.name?.trim() || p.pipeline_key || 'Untitled pipeline'; }

function triggerLabel(def: any): string {
  const t = def?.trigger;
  if (!t || t.type === 'manual') return 'Manual';
  if (t.type === 'cron') return `Cron · ${t.cron ?? ''}`;
  if (t.type === 'webhook') return 'Webhook';
  return t.type;
}

// Cheap label from the list summary's trigger_type (no cron expression —
// that only exists in the full definition, fetched on demand in the drawer).
function triggerTypeLabel(type: string | null): string {
  if (!type || type === 'manual') return 'Manual';
  if (type === 'cron') return 'Cron';
  if (type === 'webhook') return 'Webhook';
  return type;
}

function pipelineNodes(def: any) {
  if (!def?.nodes) return [];
  return (def.nodes as any[]).filter(n => n.activityType && n.nodeType !== 'fork' && n.nodeType !== 'merge').slice(0, 4);
}

// ── stage config ──────────────────────────────────────────────────────────────

const STAGE_CFG: Record<Stage, { bar: string; badge: string; label: string }> = {
  draft:      { bar: 'bg-amber-400/40',   badge: 'bg-amber-50 border-amber-200 text-amber-700 dark:bg-amber-500/15 dark:border-amber-500/25 dark:text-amber-300',              label: 'Draft' },
  testing:    { bar: 'bg-blue-400/60',    badge: 'bg-blue-50 border-blue-200 text-blue-700 dark:bg-blue-500/15 dark:border-blue-500/25 dark:text-blue-300',                    label: 'Integration' },
  production: { bar: 'bg-emerald-400/70', badge: 'bg-emerald-50 border-emerald-200 text-emerald-700 dark:bg-emerald-500/15 dark:border-emerald-500/25 dark:text-emerald-300',  label: 'Production' },
  archived:   { bar: 'bg-gray-300/60',    badge: 'bg-gray-50 border-gray-200 text-gray-400 dark:bg-white/5 dark:border-white/10 dark:text-white/35',                           label: 'Archived' },
};

// ── sub-components ────────────────────────────────────────────────────────────

// ponytail: per-row PipelineIcon (first node's activity icon) removed — the
// list summary projection no longer carries `definition`, and fetching it
// per row would defeat the point of the Gate 1 summary query. The drawer's
// topology section (below) still shows it once the row is selected.

// Every execution phase gets an explicit label; "Never run" only when there is no run.
// Text colours hold >= 4.5:1 on the light and dark list surfaces (WCAG 1.4.3).
const RUN_STATUS: Record<string, { dot: string; text: string; label: string }> = {
  completed:  { dot: 'bg-emerald-500 dark:bg-emerald-400', text: 'text-emerald-700 dark:text-emerald-400', label: 'Success' },
  failed:     { dot: 'bg-red-500 dark:bg-red-400', text: 'text-red-700 dark:text-red-400', label: 'Failed' },
  running:    { dot: 'bg-cyan-500 dark:bg-cyan-400 animate-pulse', text: 'text-cyan-700 dark:text-cyan-400', label: 'Running' },
  paused:     { dot: 'bg-amber-500 dark:bg-amber-400', text: 'text-amber-800 dark:text-amber-300', label: 'Paused' },
  cancelling: { dot: 'bg-gray-500 dark:bg-white/60 animate-pulse', text: 'text-gray-700 dark:text-white/75', label: 'Cancelling' },
  cancelled:  { dot: 'bg-gray-500 dark:bg-white/60', text: 'text-gray-700 dark:text-white/75', label: 'Cancelled' },
};

function runStatus(phase: string | null) {
  if (!phase) return { dot: 'bg-gray-400 dark:bg-white/40', text: 'text-gray-600 dark:text-white/70', label: 'Never run' };
  return RUN_STATUS[phase] ?? { dot: 'bg-gray-500 dark:bg-white/60', text: 'text-gray-700 dark:text-white/75', label: phase.charAt(0).toUpperCase() + phase.slice(1) };
}

function RunDot({ phase }: { phase: string | null }) {
  return <span aria-hidden className={`h-1.5 w-1.5 rounded-full ${runStatus(phase).dot}`} />;
}

function RunLabel({ phase }: { phase: string | null }) {
  const status = runStatus(phase);
  return <span className={status.text}>{status.label}</span>;
}

// ── drawer ────────────────────────────────────────────────────────────────────

function PipelineDrawer({ pipeline, onClose }: { pipeline: Pipeline; onClose: () => void }) {
  const navigate = useNavigate();
  const { byType } = useCatalog();
  const [runs, setRuns] = useState<Execution[]>([]);
  const [tab, setTab] = useState<'runs'>('runs');
  // List rows don't carry the full definition (Gate 1 summary projection) —
  // fetch it lazily here for topology + the detailed trigger label.
  const [definition, setDefinition] = useState<any | null>(null);
  const stage = stageOf(pipeline);
  const cfg = STAGE_CFG[stage];
  const nodes = pipelineNodes(definition);

  const [versions, setVersions] = useState<Pipeline[]>([]);

  // Runs and versions span every saved version of this pipeline/environment.
  useEffect(() => {
    api.listExecutions({ pipelineKey: pipeline.pipeline_key, env: pipeline.environment, limit: '30' })
      .then((d: Execution[]) => setRuns(d))
      .catch(() => {});
    api.listPipelines({ key: pipeline.pipeline_key, limit: '50' })
      .then(page => setVersions(page.rows.filter((row: Pipeline) => row.environment === pipeline.environment)))
      .catch(() => {});
  }, [pipeline.id]);

  useEffect(() => {
    setDefinition(null);
    api.getPipeline(pipeline.id)
      .then((row: any) => setDefinition(row?.definition ?? null))
      .catch(() => {});
  }, [pipeline.id]);

  const successRate = runs.length
    ? Math.round((runs.filter(r => r.phase === 'completed').length / runs.length) * 100)
    : null;

  return (
    <div className="fixed inset-y-0 right-0 z-40 flex w-[85vw] max-w-[380px] flex-none flex-col border-l border-gray-200 bg-white
      dark:border-white/[0.08] dark:bg-white/[0.04] dark:backdrop-blur-xl overflow-hidden
      shadow-[-18px_0_50px_rgba(0,0,0,.08)] dark:shadow-[-18px_0_50px_rgba(0,0,0,.28)]
      sm:static sm:z-auto sm:w-[380px] sm:max-w-none">

      {/* header */}
      <div className="px-5 pt-4 pb-3.5 border-b border-gray-100 dark:border-white/[0.07] shrink-0">
        <div className="flex items-center gap-2 mb-1 min-w-0">
          <span className="text-[13px] font-semibold tracking-tight text-gray-900 dark:text-white/90 min-w-0 truncate">{pipelineName(pipeline)}</span>
          <span className={`text-[9px] font-semibold px-1.5 py-px rounded-full border shrink-0 ${cfg.badge}`}>{cfg.label}</span>
          <button onClick={onClose} aria-label="Close pipeline details"
            className="relative ml-auto flex h-[22px] w-[22px] items-center justify-center rounded-[6px] shrink-0
              bg-gray-100 border border-gray-200 text-gray-400 hover:bg-gray-200 hover:text-gray-700
              dark:bg-white/[0.04] dark:border-white/[0.07] dark:text-white/40
              dark:hover:bg-white/[0.08] dark:hover:text-white transition-all
              before:absolute before:-inset-[11px] before:content-['']">
            <X size={13} />
          </button>
        </div>
        <div className="flex items-center gap-1.5 text-[11px] text-gray-400 dark:text-white/35">
          <Clock size={11} />
          <span>{versionLabel(pipeline)} · {definition ? triggerLabel(definition) : triggerTypeLabel(pipeline.trigger_type)}</span>
        </div>
        <div className="flex gap-1.5 mt-3">
          {[
            { label: 'Edit', icon: <ChevronRight size={12}/>, action: () => navigate('/', { state: { pipelineId: pipeline.id } }) },
            { label: 'Run now',  icon: <Play size={11}/>,         action: () => api.run(pipeline.id).catch(() => {}) },
            { label: 'Backfill', icon: <RotateCcw size={11}/>,    action: () => navigate('/lifecycle', { state: { openBackfillId: pipeline.id } }) },
          ].map(({ label, icon, action }) => (
            <button key={label} onClick={action}
              className="flex items-center gap-1 px-2.5 py-1 rounded-[8px] text-[11px] font-medium transition-all
                bg-gray-100 border border-gray-200 text-gray-600 hover:bg-gray-200 hover:text-gray-900
                dark:bg-white/[0.045] dark:border-white/[0.08] dark:text-white/65
                dark:hover:bg-white/[0.085] dark:hover:text-white">
              {icon} {label}
            </button>
          ))}
        </div>
      </div>

      {/* stats */}
      <div className="grid grid-cols-3 border-b border-gray-100 dark:border-white/[0.07] shrink-0">
        {[
          { val: successRate != null ? `${successRate}%` : '—', lbl: 'Success rate', hi: successRate != null && successRate >= 80 },
          { val: String(runs.length),                            lbl: 'Runs (recent)', hi: false },
          { val: pipeline.last_run_at ? reltime(pipeline.last_run_at) : '—', lbl: 'Last run', hi: false },
        ].map(({ val, lbl, hi }) => (
          <div key={lbl} className="py-3 text-center border-r border-gray-100 dark:border-white/[0.06] last:border-r-0">
            <div className={`text-[17px] font-semibold tracking-tight ${hi ? 'text-emerald-600 dark:text-emerald-400' : 'text-gray-900 dark:text-white/90'}`}>{val}</div>
            <div className="text-[10px] uppercase tracking-[.08em] text-gray-400 dark:text-white/40 mt-0.5">{lbl}</div>
          </div>
        ))}
      </div>

      {/* tabs */}
      <div className="flex border-b border-gray-100 dark:border-white/[0.07] px-4 shrink-0">
        {/* ponytail: lineage/config/access tabs hidden until implemented */}
        {(['runs'] as const).map(t => (
          <button key={t} onClick={() => setTab(t)}
            className={`py-2.5 px-2.5 text-[11px] font-medium capitalize border-b-2 -mb-px transition-all ${
              tab === t
                ? 'text-gray-900 border-brand-500 dark:text-white/90'
                : 'text-gray-400 border-transparent hover:text-gray-600 dark:text-white/35 dark:hover:text-white/65'
            }`}>
            {t}
          </button>
        ))}
      </div>

      {/* run list */}
      <div className="flex-1 overflow-y-auto">
        {tab === 'runs' && (
          runs.length === 0
            ? <div className="flex items-center justify-center h-32 text-[12px] text-gray-400 dark:text-white/40">No runs yet</div>
            : runs.map(run => (
              <div key={run.id}
                className="flex items-center gap-2.5 px-4 py-2.5 border-b border-gray-100 dark:border-white/[0.05]
                  hover:bg-gray-50 dark:hover:bg-white/[0.025] cursor-pointer">
                <span aria-hidden className={`flex h-[26px] w-[26px] items-center justify-center rounded-[7px] border shrink-0 text-[12px] font-semibold ${
                  run.phase === 'completed'
                    ? 'bg-emerald-50 border-emerald-200 text-emerald-600 dark:bg-emerald-500/10 dark:border-emerald-500/20 dark:text-emerald-400'
                    : run.phase === 'failed'
                    ? 'bg-red-50 border-red-200 text-red-500 dark:bg-red-500/10 dark:border-red-500/20 dark:text-red-400'
                    : run.phase === 'cancelled'
                    ? 'bg-gray-50 border-gray-200 text-gray-600 dark:bg-white/[0.05] dark:border-white/10 dark:text-white/60'
                    : 'bg-blue-50 border-blue-200 text-blue-500 dark:bg-cyan-500/10 dark:border-cyan-500/20 dark:text-cyan-400'
                }`}>
                  {run.phase === 'completed' ? '✓' : run.phase === 'failed' ? '✕' : run.phase === 'cancelled' ? '⊘' : '↻'}
                </span>
                <div className="flex-1 min-w-0">
                  <div className="text-[12px] font-medium text-gray-700 dark:text-white/80 truncate">
                    <RunLabel phase={run.phase} /> · {reltime(run.started_at)}
                    {run.pipeline_version != null && <span className="ml-1.5 text-gray-600 dark:text-white/60">v{run.pipeline_version}</span>}
                    {run.record_count != null && (
                      <span className="ml-1.5 text-gray-400 dark:text-white/45">{run.record_count.toLocaleString()} rows</span>
                    )}
                  </div>
                  {run.error && <div className="text-[10px] text-red-500 dark:text-red-400/80 truncate mt-0.5">{run.error}</div>}
                </div>
                <span className="text-[11px] text-gray-400 dark:text-white/45 shrink-0">{duration(run.started_at, run.completed_at)}</span>
              </div>
            ))
        )}
        {tab !== 'runs' && (
          <div className="flex items-center justify-center h-32 text-[12px] text-gray-400 dark:text-white/40">Coming soon</div>
        )}
      </div>

      {/* version history */}
      {versions.length > 1 && (
        <div className="max-h-40 overflow-y-auto border-t border-gray-100 px-4 py-3 dark:border-white/[0.07] shrink-0">
          <div className="text-[10px] font-semibold uppercase tracking-[.1em] text-gray-600 dark:text-white/60 mb-1.5">Versions</div>
          <ul>
            {versions.map(v => (
              <li key={v.id} className="flex items-center gap-2 py-1 text-[11px] text-gray-700 dark:text-white/75">
                <span className="font-medium">v{v.version}</span>
                <span className="text-gray-600 dark:text-white/60">{v.status === 'active' ? 'Active' : v.status === 'archived' ? 'Archived' : 'Draft'} · saved {reltime(v.created_at)}</span>
                <button className="ml-auto text-brand-600 hover:underline dark:text-brand-300" aria-label={`Open version ${v.version} in editor`}
                  onClick={() => navigate('/', { state: { pipelineId: v.id } })}>Open</button>
              </li>
            ))}
          </ul>
        </div>
      )}

      {/* mini topology */}
      {nodes.length > 0 && (
        <div className="px-4 py-3 border-t border-gray-100 dark:border-white/[0.07] shrink-0">
          <div className="text-[10px] font-semibold uppercase tracking-[.1em] text-gray-400 dark:text-white/40 mb-2">Topology</div>
          <div className="flex flex-wrap items-center gap-1.5">
            {nodes.map((n, i) => {
              const entry = byType[n.activityType];
              return (
                <div key={i} className="flex items-center gap-1.5">
                  <span className="flex items-center gap-1 px-2 py-1 rounded-[6px] border text-[11px] font-medium
                    border-gray-200 bg-gray-50 dark:border-white/[0.1] dark:bg-white/[0.05]"
                    style={{ color: entry?.color ?? '#7c6cf2' }}>
                    <ActivityIcon activityType={n.activityType} nodeType={n.nodeType} size={11} />
                    {entry?.label ?? n.activityType}
                  </span>
                  {i < nodes.length - 1 && <span className="text-[10px] text-gray-300 dark:text-white/20">→</span>}
                </div>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}

// ── main ──────────────────────────────────────────────────────────────────────

type FilterType = 'all' | 'production' | 'integration' | 'draft';
type TriggerFilter = 'all' | 'cron' | 'manual' | 'webhook';

const FILTERS: { key: FilterType; label: string; dot?: string }[] = [
  { key: 'all',         label: 'All' },
  { key: 'production',  label: 'Production',      dot: 'bg-emerald-400' },
  { key: 'integration', label: 'Integration',     dot: 'bg-blue-400' },
  { key: 'draft',       label: 'Draft',           dot: 'bg-amber-400' },
];

const TRIGGER_FILTERS: { key: TriggerFilter; label: string }[] = [
  { key: 'all', label: 'All triggers' },
  { key: 'cron', label: 'Cron' },
  { key: 'manual', label: 'Manual' },
  { key: 'webhook', label: 'Webhook' },
];

// FilterType -> the API's `stage` query param (server-side filter, no client scan).
const STAGE_PARAM: Partial<Record<FilterType, string>> = {
  production: 'production', integration: 'testing', draft: 'draft',
};

export default function PipelinesPage() {
  const navigate = useNavigate();
  const [rows, setRows] = useState<Pipeline[]>([]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState<FilterType>('all');
  const [triggerFilter, setTriggerFilter] = useState<TriggerFilter>('all');
  const [failedOnly, setFailedOnly] = useState(false);
  const [search, setSearch] = useState('');
  const [debouncedSearch, setDebouncedSearch] = useState('');
  const [selected, setSelected] = useState<Pipeline | null>(null);
  const requestGeneration = useRef(0);
  const activeQuerySignature = useRef('');

  // Debounce the search box before it hits the server-side ILIKE filter.
  useEffect(() => {
    const t = setTimeout(() => setDebouncedSearch(search.trim()), 300);
    return () => clearTimeout(t);
  }, [search]);

  const queryParams = {
    limit: '50',
    view: 'current' as const,
    search: debouncedSearch || undefined,
    stage: STAGE_PARAM[filter],
    trigger: triggerFilter === 'all' ? undefined : triggerFilter,
  };
  const querySignature = `${filter}\u0000${triggerFilter}\u0000${debouncedSearch}`;
  activeQuerySignature.current = querySignature;

  const load = () => {
    const generation = ++requestGeneration.current;
    const requestQuery = querySignature;
    setLoading(true); setLoadingMore(false); setError(null); setNextCursor(null);
    api.listPipelines(queryParams)
      .then(page => {
        if (generation !== requestGeneration.current || requestQuery !== activeQuerySignature.current) return;
        setRows(page.rows); setNextCursor(page.nextCursor);
      })
      .catch((e: Error) => {
        if (generation === requestGeneration.current && requestQuery === activeQuerySignature.current) setError(e.message);
      })
      .finally(() => {
        if (generation === requestGeneration.current && requestQuery === activeQuerySignature.current) setLoading(false);
      });
  };

  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => {
    load();
    return () => { requestGeneration.current++; };
  }, [filter, triggerFilter, debouncedSearch]);

  const loadMore = () => {
    if (!nextCursor) return;
    const generation = ++requestGeneration.current;
    const requestQuery = querySignature;
    const cursor = nextCursor;
    setLoadingMore(true);
    api.listPipelines({ ...queryParams, cursor })
      .then(page => {
        if (generation !== requestGeneration.current || requestQuery !== activeQuerySignature.current) return;
        setRows(r => [...r, ...page.rows]); setNextCursor(page.nextCursor);
      })
      .catch((e: Error) => {
        if (generation === requestGeneration.current && requestQuery === activeQuerySignature.current) setError(e.message);
      })
      .finally(() => {
        if (generation === requestGeneration.current && requestQuery === activeQuerySignature.current) setLoadingMore(false);
      });
  };

  // failedOnly narrows only the currently-loaded page — cheap client filter,
  // not a full-table scan (rows are already keyset-paginated from the server).
  const visible = failedOnly ? rows.filter(p => p.last_run_phase === 'failed') : rows;

  return (
    <div className="flex h-full">
      <div className="flex flex-1 flex-col min-w-0 min-h-0">
        {/* toolbar */}
        <div className="flex flex-col gap-2 px-6 py-2.5 border-b border-gray-100 dark:border-white/[0.06] shrink-0">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="mr-0.5 text-[10px] font-semibold uppercase tracking-[.06em] text-gray-400 dark:text-white/30">Stage</span>
            {FILTERS.map(({ key, label, dot }) => (
              <button key={key} onClick={() => setFilter(key)}
                className={`flex items-center gap-1.5 px-3 py-1 rounded-full text-[11px] font-medium border transition-all ${
                  filter === key
                    ? 'bg-gray-900 border-gray-900 text-white dark:bg-white/[0.12] dark:border-white/[0.2] dark:text-white'
                    : 'bg-transparent border-gray-200 text-gray-500 hover:bg-gray-100 hover:text-gray-800 dark:border-white/[0.07] dark:text-white/45 dark:hover:bg-white/[0.065] dark:hover:text-white/75'
                }`}>
                {dot && <span className={`h-1.5 w-1.5 rounded-full ${dot}`} />}
                {label}
              </button>
            ))}
            <div className="ml-auto flex items-center gap-2">
              <div className="relative flex items-center">
                <Search size={13} className="absolute left-2.5 text-gray-400 dark:text-white/30 pointer-events-none" />
                <input value={search} onChange={e => setSearch(e.target.value)}
                  placeholder="Search…" className="glass-input pl-7 py-1.5 text-[12px] w-40" />
              </div>
              <button onClick={load} aria-label="Refresh pipelines" className="icon-button h-8 w-8 border-transparent bg-transparent"><RefreshCw size={14} /></button>
              <button onClick={() => navigate('/')} className="glass-btn-primary text-[12px] py-1.5 px-4">+ New</button>
            </div>
          </div>
          <div className="flex items-center gap-2 flex-wrap">
            <span className="mr-0.5 text-[10px] font-semibold uppercase tracking-[.06em] text-gray-400 dark:text-white/30">Trigger</span>
            {TRIGGER_FILTERS.map(({ key, label }) => (
              <button key={key} onClick={() => setTriggerFilter(key)}
                className={`px-3 py-1 rounded-full text-[11px] font-medium border transition-all ${triggerFilter === key
                  ? 'bg-gray-900 border-gray-900 text-white dark:bg-white/[0.12] dark:border-white/[0.2] dark:text-white'
                  : 'bg-transparent border-gray-200 text-gray-500 hover:bg-gray-100 dark:border-white/[0.07] dark:text-white/45 dark:hover:bg-white/[0.065] dark:hover:text-white/75'}`}>
                {label}
              </button>
            ))}
          </div>
          <button type="button" aria-pressed={failedOnly} onClick={() => setFailedOnly(v => !v)}
            className={`flex w-fit items-center gap-2 py-0.5 text-[12px] font-medium transition-colors ${failedOnly ? 'text-red-500' : 'text-gray-500 dark:text-white/55'}`}>
            <span className={`relative h-[17px] w-[30px] rounded-full transition-colors ${failedOnly ? 'bg-red-500' : 'bg-gray-200 dark:bg-white/[0.14]'}`}>
              <span className={`absolute top-0.5 h-[13px] w-[13px] rounded-full bg-white shadow-sm transition-[left] ${failedOnly ? 'left-[15px]' : 'left-0.5'}`} />
            </span>
            Only show last-run failures
          </button>
        </div>

        {/* rows */}
        <div className="flex-1 overflow-y-auto">
          {loading && <div className="flex items-center justify-center h-32 text-[12px] text-gray-400 dark:text-white/25">Loading…</div>}
          {!loading && error && <div className="p-6"><ApiError message={error} onRetry={load} /></div>}
          {!loading && !error && visible.length === 0 && (
            <div className="flex h-32 flex-col items-center justify-center gap-2 text-[12px] text-gray-400 dark:text-white/35">
              {rows.length === 0 && filter === 'all' && triggerFilter === 'all' && !debouncedSearch ? (
                <>
                  <span>No pipelines yet. Create one to run your first data flow.</span>
                  <button className="glass-btn-primary px-3 py-1 text-xs" onClick={() => navigate('/')}>Create pipeline</button>
                </>
              ) : (
                <>
                  <span>No pipelines match these filters.</span>
                  <button className="glass-btn-ghost px-3 py-1 text-xs" onClick={() => { setFilter('all'); setTriggerFilter('all'); setFailedOnly(false); setSearch(''); }}>Clear filters</button>
                </>
              )}
            </div>
          )}
          {!error && visible.map(p => {
            const stage = stageOf(p);
            const cfg = STAGE_CFG[stage];
            const sel = selected?.id === p.id;
            return (
              <button type="button" key={p.id} onClick={() => setSelected(sel ? null : p)}
                className={`group flex w-full items-stretch border-b border-gray-100 text-left text-gray-900 dark:border-white/[0.05] dark:text-white/90 cursor-pointer transition-colors ${
                  sel ? 'bg-brand-50 dark:bg-brand-500/[0.06]' : 'hover:bg-gray-50 dark:hover:bg-white/[0.025]'
                }`}>
                <div className={`w-[3px] shrink-0 ${cfg.bar}`} />
                <div className="flex flex-1 flex-wrap items-center gap-3.5 px-5 py-3 min-w-0">
                  <div className="flex-1 min-w-0">
                    <div className="text-[13px] font-medium text-gray-900 dark:text-white/90 truncate">{pipelineName(p)}</div>
                    <div className="text-[11px] text-gray-600 dark:text-white/60 mt-0.5">{versionLabel(p)} · {triggerTypeLabel(p.trigger_type)}</div>
                  </div>
                  <div className="flex flex-none items-center gap-2.5 ml-auto">
                    <span className={`text-[10px] font-semibold px-2 py-0.5 rounded-full border ${cfg.badge}`}>{cfg.label}</span>
                    <div className="flex items-center gap-1.5 text-[11px]">
                      <RunDot phase={p.last_run_phase} />
                      <RunLabel phase={p.last_run_phase} />
                    </div>
                    <span className="text-[11px] text-gray-600 dark:text-white/60 w-20 text-right"
                      title={p.last_run_version != null ? `Last run used v${p.last_run_version}` : undefined}>
                      {reltime(p.last_run_at)}{p.last_run_version != null && p.last_run_version !== p.version ? ` · v${p.last_run_version}` : ''}
                    </span>
                  </div>
                </div>
              </button>
            );
          })}
        </div>

        {/* footer */}
        <div className="flex items-center gap-3 px-6 py-2 border-t border-gray-100 dark:border-white/[0.06] shrink-0">
          <span className="text-[11px] text-gray-600 dark:text-white/60">{visible.length} pipeline{visible.length !== 1 ? 's' : ''} loaded</span>
          {nextCursor && (
            <button onClick={loadMore} disabled={loadingMore}
              className="glass-btn-ghost px-2.5 py-1 text-[11px] disabled:opacity-50">
              {loadingMore ? 'Loading…' : 'Load more'}
            </button>
          )}
        </div>
      </div>

      {selected && <div className="fixed inset-0 z-30 bg-black/40 sm:hidden" onClick={() => setSelected(null)} />}
      {selected && <PipelineDrawer key={selected.id} pipeline={selected} onClose={() => setSelected(null)} />}
    </div>
  );
}
