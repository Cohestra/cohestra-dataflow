import { useEffect, useMemo, useRef, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router';
import ReactFlow, { Background, BackgroundVariant, Handle, Position, type NodeProps } from 'reactflow';
import { ArrowLeft, Pause, Play, RotateCcw, X } from 'lucide-react';
import { api } from '../api';
import { ApiError } from '../components/ApiError';
import { useCatalog } from '../context/CatalogContext';
import { buildRunGraph, type NodeRun } from './runGraph';
import { displayEnvironment } from '../utils/pipelineStage';
import { useTheme } from '../context/ThemeContext';
import { useFeatures } from '../context/FeatureContext';

const TERMINAL = ['completed', 'failed', 'cancelled'];
const STATUS_RING: Record<string, string> = {
  success:   'border-emerald-400/60',
  completed: 'border-emerald-400/60',
  failed:    'border-red-400/70',
  running:   'border-amber-400/60',
};

function RunFlowNode({ data }: NodeProps) {
  const { byType } = useCatalog();
  const entry = byType[data.activityType];
  const ring = STATUS_RING[data.status] ?? 'border-gray-200 dark:border-white/10';
  return (
    <div className={`rounded-xl border bg-white dark:bg-[#12151f]/95 px-3 py-2 text-xs shadow-xs dark:shadow-lg ${ring}`}
      style={{ minWidth: 150 }}>
      <Handle type="target" position={Position.Left} />
      <div className="flex items-center gap-2">
        <span className="h-2.5 w-2.5 rounded-full" style={{ background: entry?.color ?? '#7F77DD' }} />
        <span className="font-medium text-gray-900 dark:text-white/90">{data.label ?? entry?.label ?? data.activityType}</span>
      </div>
      <div className="mt-1 text-[10px] text-gray-400 dark:text-white/50">
        {data.status ?? 'pending'}
        {data.recordCount != null && ` · ${data.recordCount.toLocaleString()} rec`}
        {data.durationMs != null && ` · ${data.durationMs}ms`}
      </div>
      {data.error && <div className="mt-1 text-[10px] text-red-500 dark:text-danger/90 wrap-break-word">{String(data.error).slice(0, 120)}</div>}
      <Handle type="source" position={Position.Right} />
    </div>
  );
}
const nodeTypes = { runNode: RunFlowNode };

export default function RunDetailPage() {
  const { id } = useParams();
  const navigate = useNavigate();
  const { dark } = useTheme();
  const { features } = useFeatures();
  const [data, setData] = useState<{ execution: any; definition: any; nodeRuns: NodeRun[]; qualityResults?: any[] } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [retrying, setRetrying] = useState(false);
  const [reloadKey, setReloadKey] = useState(0);
  const [trace, setTrace] = useState<any[] | null>(null);
	const [live, setLive] = useState<any>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  useEffect(() => {
    let alive = true;
    const poll = async () => {
      try {
        const d = await api.getExecution(id!);
		const status = await api.executionStatus(id!).catch(() => null);
        if (!alive) return;
        setData(d);
		setLive(status);
        if (!TERMINAL.includes(d.execution?.phase)) timer.current = setTimeout(poll, 1500);
      } catch (e: any) { if (alive) setError(e.message ?? 'Failed to load run'); }
    };
    poll();
    return () => { alive = false; clearTimeout(timer.current); };
  }, [id, reloadKey]);

  const graph = useMemo(
    () => data ? buildRunGraph(data.definition, data.nodeRuns) : { nodes: [], edges: [] },
    [data],
  );

  const retry = async () => {
    setRetrying(true); setError(null);
    try { const next = await api.retryExecution(id!); navigate(`/runs/${next.executionId}`); }
    catch (e: any) { setError(e.message ?? 'Retry failed'); setRetrying(false); }
  };

  const loadTrace = async () => {
    try { setTrace((await api.getExecutionTrace(id!)).events ?? []); }
    catch (e: any) { setError(e.message ?? 'Trace failed'); }
  };
	const signal = async (action: 'pause' | 'resume' | 'cancel') => { await api.signal(id!, action); setLive((value: any) => ({ ...value, phase: action === 'resume' ? 'running' : action === 'pause' ? 'paused' : 'cancelled' })); };

  return (
    <div className="flex h-[calc(100vh-4rem)] flex-col">
      <div className="flex items-center gap-3 border-b border-gray-100 dark:border-white/[0.07] px-6 py-4">
        <Link to="/runs" className="glass-btn-ghost flex items-center gap-1 text-sm"><ArrowLeft size={15} /> Runs</Link>
        <div>
          <h2 className="text-sm font-semibold text-gray-900 dark:text-white/90">{data?.execution?.name ?? 'Run'}</h2>
          <p className="text-xs text-gray-400 dark:text-white/40">
            {displayEnvironment(data?.execution?.environment)} · {data?.execution?.phase ?? '…'}
          </p>
        </div>
        {data?.execution?.phase === 'failed' && (
          <button className="glass-btn-ghost ml-auto flex items-center gap-1 text-sm" onClick={retry} disabled={retrying}>
            <RotateCcw size={15} /> {retrying ? 'Retrying…' : 'Retry run'}
          </button>
        )}
        {features.deepObservability && <button className="glass-btn-ghost ml-auto text-sm" onClick={loadTrace}>Temporal trace</button>}
		{(live?.stream || live?.cohestraId) && <div className="ml-auto flex items-center gap-2">
			{live.phase === 'paused' ? <button className="glass-btn-ghost text-sm" onClick={() => signal('resume')}><Play size={14} /> Resume</button> : <button className="glass-btn-ghost text-sm" onClick={() => signal('pause')}><Pause size={14} /> Pause</button>}
			<button className="glass-btn-danger text-sm" onClick={() => signal('cancel')}><X size={14} /> Cancel</button>
			{live?.cohestraId && <button className="glass-btn-ghost text-sm" onClick={() => api.signal(id!, 'rollback')}>Rollback</button>}
		</div>}
      </div>
	  {live?.stream && <div className="grid grid-cols-2 gap-3 border-b border-gray-100 px-6 py-3 text-xs dark:border-white/[0.07] sm:grid-cols-5">
		{[['Records', live.stream.records], ['Batches', live.stream.batches], ['Lag', live.stream.lagRecords], ['Throughput/s', Number(live.stream.throughputPerSec).toFixed(1)], ['Errors', live.stream.errors]].map(([label, value]) => <div key={String(label)}><span className="text-gray-400">{label}</span><strong className="ml-2 text-gray-900 dark:text-white/90">{value}</strong></div>)}
	  </div>}
      {error && <div className="mx-6 mt-4"><ApiError message={error} onRetry={() => { setError(null); setData(null); setReloadKey(key => key + 1); }} /></div>}
      {!!data?.qualityResults?.length && <div className="flex flex-wrap gap-2 border-b border-gray-100 px-6 py-3 dark:border-white/[0.07]">
        {data.qualityResults.map(result => <div key={result.node_id} className={`rounded-lg border px-3 py-2 text-xs ${result.status === 'passed' ? 'border-emerald-300 bg-emerald-50 dark:bg-emerald-500/10' : result.status === 'failed' ? 'border-red-300 bg-red-50 dark:bg-red-500/10' : 'border-amber-300 bg-amber-50 dark:bg-amber-500/10'}`}>
          <p className="font-semibold">{result.node_id} · quality {result.status}</p>
          <p className="mt-0.5 text-[10px] text-gray-500 dark:text-white/50">{Number(result.passed_count).toLocaleString()} passed · {Number(result.failed_count).toLocaleString()} rejected{result.quarantine_available ? ' · quarantined' : ''}</p>
          {result.error_samples?.[0] && <p className="mt-1 max-w-md truncate text-[10px] text-red-500">Row {Number(result.error_samples[0].rowIndex) + 1}: {result.error_samples[0].errors.join('; ')}</p>}
        </div>)}
      </div>}
      {trace && <div className="max-h-56 overflow-auto border-b border-gray-100 px-6 py-3 text-xs dark:border-white/[0.07]">
        <div className="mb-2 flex items-center justify-between"><strong>Temporal history</strong><button className="glass-btn-ghost" onClick={() => setTrace(null)}>Close</button></div>
        {trace.map(event => <details key={event.eventId} className="border-t border-gray-100 py-2 dark:border-white/6"><summary>#{event.eventId} · event {event.eventType}</summary><pre className="mt-2 overflow-auto whitespace-pre-wrap text-[10px] opacity-70">{JSON.stringify(event.attributes, null, 2)}</pre></details>)}
        {!trace.length && <p className="opacity-60">No history events.</p>}
      </div>}
      <div className="flex-1">
        {!data && !error ? <div className="flex h-full items-center justify-center text-sm text-gray-400">Loading run…</div> : data && (
        <ReactFlow nodes={graph.nodes} edges={graph.edges} nodeTypes={nodeTypes}
          fitView nodesDraggable={false} nodesConnectable={false} elementsSelectable={false}>
          <Background variant={BackgroundVariant.Dots} gap={24} size={1}
            color={dark ? 'rgba(255,255,255,0.06)' : 'rgba(0,0,0,0.06)'} />
        </ReactFlow>
        )}
      </div>
    </div>
  );
}
