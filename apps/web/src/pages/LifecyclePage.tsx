import { useCallback, useEffect, useState } from 'react';
import { Link, useLocation } from 'react-router';
import { History, Milestone, RefreshCw } from 'lucide-react';
import { api } from '../api';
import { ApiError } from '../components/ApiError';
import { DateTimePicker } from '../components/DateTimePicker';
import { deriveStage, displayEnvironment, displayStage, type Stage } from '../utils/pipelineStage';

const STAGE_STYLE: Record<Stage, string> = {
  draft:      'bg-gray-100 text-gray-500 dark:bg-white/10 dark:text-white/60',
  testing:    'bg-amber-50 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300',
  production: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300',
  archived:   'bg-gray-50 text-gray-400 dark:bg-white/5 dark:text-white/35',
};

export function StageBadge({ stage }: { stage: Stage }) {
  return (
    <span className={`glass-badge ${STAGE_STYLE[stage]}`}>
      <span className="inline-block w-1.5 h-1.5 rounded-full bg-current mr-1 opacity-70" />
      {displayStage(stage)}
    </span>
  );
}

interface Pipeline {
  id: string; pipeline_key: string; version: number; name: string;
  status: string; environment?: string;
}

interface BackfillPlan {
  from: string; to: string; partitionDays: number; maxConcurrency: number;
  partitionCount: number; estimatedExecutions: number;
  partitions: { from: string; to: string }[];
}

interface BackfillJob {
  id: string; status: string; from: string; to: string; partitionDays: number; maxConcurrency: number;
  partitionCount: number; pending: number; running: number;
  completed: number; failed: number; createdAt: string;
}

function BackfillPanel({ pipeline, close }: { pipeline: Pipeline; close: () => void }) {
  const [form, setForm] = useState({ from: '', to: '', partitionDays: 1, maxConcurrency: 2 });
  const [plan, setPlan] = useState<BackfillPlan | null>(null);
  const [jobs, setJobs] = useState<BackfillJob[]>([]);
  const [busy, setBusy] = useState<'plan' | 'start' | null>(null);
  const [error, setError] = useState<string | null>(null);

  const loadJobs = useCallback(async () => {
    try {
      const result = await api.listBackfills(pipeline.id);
      setJobs(Array.isArray(result) ? result : result.jobs ?? []);
    } catch (e: any) { setError(e.message ?? 'Failed to load backfills'); }
  }, [pipeline.id]);
  useEffect(() => {
    let mounted = true;
    const loadJobsIfMounted = async () => {
      if (mounted) {
        await loadJobs();
      }
    };
    loadJobsIfMounted();
    const timer = setInterval(loadJobsIfMounted, 5000);
    return () => {
      mounted = false;
      clearInterval(timer);
    };
  }, [loadJobs]);

  const body = () => ({
    ...form,
    from: new Date(form.from).toISOString(),
    to: new Date(form.to).toISOString(),
  });
  const submitPlan = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.from || !form.to) { setError('Please select both From and To dates.'); return; }
    setBusy('plan'); setError(null); setPlan(null);
    try { setPlan(await api.planBackfill(pipeline.id, body())); }
    catch (err: any) { setError(err.message ?? 'Backfill plan failed'); }
    finally { setBusy(null); }
  };
  const start = async () => {
    setBusy('start'); setError(null);
    try { await api.startBackfill(pipeline.id, body()); setPlan(null); await loadJobs(); }
    catch (err: any) { setError(err.message ?? 'Backfill start failed'); }
    finally { setBusy(null); }
  };

  return (
    <div className="border-t border-gray-100 bg-gray-50/60 p-4 dark:border-white/5 dark:bg-white/2">
      <div className="mb-3 flex items-center justify-between">
        <div>
          <p className="font-medium text-gray-900 dark:text-white/90">Historical backfill · {pipeline.name}</p>
          <p className="text-xs text-gray-400 dark:text-white/40">Preview date partitions before consuming executions.</p>
        </div>
        <button type="button" className="glass-btn-ghost text-sm" onClick={close}>Close</button>
      </div>
      <form className="flex flex-wrap items-end gap-2" onSubmit={submitPlan}>
        <label className="text-xs text-gray-500 dark:text-white/50">From
          <DateTimePicker value={form.from} onChange={v => { setForm(f => ({ ...f, from: v })); setPlan(null); }}
            placeholder="Start date & time" className="mt-1" />
        </label>
        <label className="text-xs text-gray-500 dark:text-white/50">To (exclusive)
          <DateTimePicker value={form.to} onChange={v => { setForm(f => ({ ...f, to: v })); setPlan(null); }}
            placeholder="End date & time" className="mt-1" />
        </label>
        <label className="text-xs text-gray-500 dark:text-white/50">Days / partition
          <input required type="number" min="1" max="31" className="glass-input mt-1 block w-28" value={form.partitionDays}
            onChange={e => { setForm(f => ({ ...f, partitionDays: Number(e.target.value) })); setPlan(null); }} />
        </label>
        <label className="text-xs text-gray-500 dark:text-white/50">Concurrency
          <input required type="number" min="1" max="5" className="glass-input mt-1 block w-24" value={form.maxConcurrency}
            onChange={e => { setForm(f => ({ ...f, maxConcurrency: Number(e.target.value) })); setPlan(null); }} />
        </label>
        <button className="glass-btn-ghost text-sm" disabled={busy !== null}>{busy === 'plan' ? 'Planning…' : 'Preview'}</button>
      </form>
      {error && <p className="mt-3 text-xs text-red-600 dark:text-danger/90">{error}</p>}
      {plan && (
        <div className="mt-3 flex flex-wrap items-center justify-between gap-3 rounded-lg border border-gray-200 p-3 text-sm dark:border-white/10">
          <span>{plan.partitionCount} partitions · {plan.estimatedExecutions} estimated executions · up to {plan.maxConcurrency} concurrent</span>
          <button type="button" className="glass-btn-primary text-sm" onClick={start} disabled={busy !== null}>
            {busy === 'start' ? 'Starting…' : 'Start backfill'}
          </button>
        </div>
      )}
      {jobs.length > 0 && (
        <div className="mt-4 space-y-2">
          <div className="flex items-center justify-between">
            <p className="text-xs font-medium uppercase tracking-wide text-gray-400 dark:text-white/40">Recent jobs</p>
            <button type="button" className="text-xs text-gray-500 hover:text-gray-900 dark:text-white/50 dark:hover:text-white"
              onClick={loadJobs}>Refresh status</button>
          </div>
          {jobs.map(job => (
            <div key={job.id} className="flex flex-wrap items-center justify-between gap-2 text-xs text-gray-500 dark:text-white/50">
              <span>{job.from.slice(0, 10)} → {job.to.slice(0, 10)} · {job.partitionCount} partitions</span>
              <span className="font-medium text-gray-700 dark:text-white/70">
                {job.status} · {job.completed} done · {job.running} running · {job.failed} failed
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export default function LifecyclePage() {
  const location = useLocation();
  const [rows, setRows] = useState<Pipeline[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [backfillPipeline, setBackfillPipeline] = useState<string | null>(
    (location.state as any)?.openBackfillId ?? null,
  );

  const refresh = useCallback(async () => {
    setLoading(true); setError(null);
    try { setRows(await api.listAllPipelines()); }
    catch (e: any) { setError(e.message ?? 'Failed to load pipelines'); }
    finally { setLoading(false); }
  }, []);
  useEffect(() => { refresh(); }, [refresh]);

  const transition = async (row: Pipeline, to: 'testing' | 'production') => {
    setBusy(row.id); setError(null);
    try { await api.setStage(row.id, to); await refresh(); }
    catch (e: any) {
      if (to === 'production' && String(e.message).includes('breaking data contract') &&
          window.confirm(`Breaking contract detected. Promote anyway?\n\n${e.message}`)) {
        try { await api.setStage(row.id, to, true); await refresh(); }
        catch (override: any) { setError(override.message ?? 'Override failed'); }
      } else setError(e.message ?? 'Transition failed');
    }
    finally { setBusy(null); }
  };

  return (
    <div className="mx-auto max-w-5xl space-y-6 px-6 py-10">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="page-heading flex items-center gap-2"><Milestone size={20} aria-hidden="true" /> Lifecycle</h2>
          <p className="page-subtitle mt-1">
            Promote pipelines draft → Integration → production. Production requires a green run and compatible published contracts.
          </p>
        </div>
        <button className="glass-btn-ghost text-sm flex items-center gap-1.5" onClick={refresh} disabled={loading}>
          <RefreshCw size={15} className={loading ? 'animate-spin' : ''} /> Refresh
        </button>
      </div>

      {error && <ApiError message={error} onRetry={refresh} />}

      <div className="glass-card divide-y divide-gray-100 dark:divide-white/5">
        {rows.length === 0 && !loading && !error && (
          <div className="p-6 text-center">
            <p className="text-sm text-gray-400 dark:text-white/40">No pipelines yet.</p>
            <Link to="/" className="glass-btn-primary mt-3 inline-flex text-sm">Create a pipeline</Link>
          </div>
        )}
        {rows.map(row => {
          const stage = deriveStage(row.status, row.environment);
          return (
            <div key={row.id}>
            <div className="flex items-center justify-between gap-4 p-4">
              <div className="min-w-0">
                <p className="truncate font-medium text-gray-900 dark:text-white/90">{row.name}</p>
                <p className="text-xs text-gray-400 dark:text-white/40">v{row.version} · {displayEnvironment(row.environment)}</p>
              </div>
              <div className="flex items-center gap-3">
                <StageBadge stage={stage} />
                <button className="glass-btn-ghost flex items-center gap-1.5 text-sm"
                  onClick={() => setBackfillPipeline(current => current === row.id ? null : row.id)}>
                  <History size={14} /> Backfill
                </button>
                {stage === 'draft' && (
                  <button className="glass-btn-ghost text-sm" disabled={busy === row.id}
                    onClick={() => transition(row, 'testing')}>
                    {busy === row.id ? '…' : 'Promote to Integration →'}
                  </button>
                )}
                {stage === 'testing' && (
                  <button className="glass-btn-primary text-sm" disabled={busy === row.id}
                    onClick={() => transition(row, 'production')}>
                    {busy === row.id ? '…' : 'Promote to production →'}
                  </button>
                )}
              </div>
            </div>
            {backfillPipeline === row.id && <BackfillPanel pipeline={row} close={() => setBackfillPipeline(null)} />}
            </div>
          );
        })}
      </div>
    </div>
  );
}
