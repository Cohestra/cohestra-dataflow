import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router';
import { Activity, AlertTriangle, CheckCircle2, Clock3, RefreshCw } from 'lucide-react';
import { api } from '../api';
import { displayEnvironment } from '../utils/pipelineStage';
import { ApiError } from '../components/ApiError';

interface MonitoringData {
  days: number;
  summary: { runs: number; succeeded: number; failed: number; running: number; successRate: number | null; avgDurationMs: number };
  trend: Array<{ day: string; runs: number; succeeded: number; failed: number }>;
  pipelines: Array<{
    id: string; name: string; version: number; status: string; environment: string;
    runs: number; failed: number; avg_duration_ms: string | number;
    last_execution_id?: string; last_phase?: string; last_started_at?: string;
    health?: 'healthy' | 'warning' | 'critical' | 'unmonitored';
    metadata?: { owner?: string; domain?: string; tags?: string[] };
    breaches?: Array<{ type: string; severity: string; message: string }>;
  }>;
  recentFailures: Array<{
    id: string; name: string; environment: string; started_at: string;
    node_id?: string; error?: string;
  }>;
  quality: { checks: number; passedRows: number; failedRows: number; issues: number };
  recentQualityIssues: Array<{
    execution_id: string; node_id: string; status: 'warning' | 'failed';
    passed_count: number; failed_count: number; error_samples?: Array<{ rowIndex: number; errors: string[] }>;
    evaluated_at: string; name: string; environment: string;
  }>;
}

interface PipelineAlert {
  id: string; pipeline_name: string; kind: string; severity: 'warning' | 'critical';
  status: 'open' | 'acknowledged' | 'resolved'; message: string; last_seen_at: string;
  acknowledged_by_email?: string;
  notification_sent_at?: string; notification_attempts?: number; notification_error?: string;
}

interface NodeEvent {
  execution_id: string; node_id: string; status: string; level: 'info' | 'error'; message: string;
  finished_at: string; pipeline_id: string; name: string; environment: string;
}

const phaseColor: Record<string, string> = {
  completed: 'text-emerald-600 dark:text-emerald-300',
  failed: 'text-red-600 dark:text-danger',
  running: 'text-amber-600 dark:text-amber-300',
};

const duration = (ms: number) => ms >= 60_000 ? `${(ms / 60_000).toFixed(1)}m` : ms >= 1000 ? `${(ms / 1000).toFixed(1)}s` : `${Math.round(ms)}ms`;

export default function MonitoringPage() {
  const [days, setDays] = useState(7);
  const [data, setData] = useState<MonitoringData | null>(null);
  const [alerts, setAlerts] = useState<PipelineAlert[]>([]);
  const [logs, setLogs] = useState<NodeEvent[]>([]);
  const [logInput, setLogInput] = useState('');
  const [logQuery, setLogQuery] = useState('');
  const [logLevel, setLogLevel] = useState('all');
  const [alertBusy, setAlertBusy] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const refresh = async () => {
    setLoading(true); setError(null);
    try {
      const [overview, incidents, activity] = await Promise.all([
        api.monitoringOverview(days),
        api.listAlerts('active').catch(() => []),
        api.listExecutionLogs({ query: logQuery, level: logLevel, limit: 100, days }).catch(() => ({ items: [] })),
      ]);
      setData(overview); setAlerts(incidents); setLogs(activity.items ?? []);
    }
    catch (e: any) { setError(e.message ?? 'Failed to load monitoring'); }
    finally { setLoading(false); }
  };
  useEffect(() => {
    void refresh();
    const timer = setInterval(() => { void refresh(); }, 15_000);
    return () => clearInterval(timer);
  }, [days, logQuery, logLevel]);

  const maxRuns = useMemo(() => Math.max(1, ...(data?.trend.map(point => Number(point.runs)) ?? [1])), [data]);
  const updateAlert = async (id: string, action: 'acknowledge' | 'resolve') => {
    setAlertBusy(id);
    try {
      if (action === 'acknowledge') await api.acknowledgeAlert(id); else await api.resolveAlert(id);
      setAlerts(current => action === 'resolve' ? current.filter(alert => alert.id !== id)
        : current.map(alert => alert.id === id ? { ...alert, status: 'acknowledged' } : alert));
    } catch (e: any) { setError(e.message ?? `Failed to ${action} alert`); }
    finally { setAlertBusy(null); }
  };
  const retryNotification = async (id: string) => {
    setAlertBusy(id);
    try {
      await api.retryAlertNotification(id);
      setAlerts(current => current.map(alert => alert.id === id ? { ...alert, notification_error: undefined, notification_attempts: 0 } : alert));
    } catch (e: any) { setError(e.message ?? 'Failed to retry notification'); }
    finally { setAlertBusy(null); }
  };
  const cards = data ? [
    { label: 'Runs', value: data.summary.runs, icon: Activity, tone: 'text-brand-500' },
    { label: 'Success rate', value: data.summary.successRate == null ? '—' : `${data.summary.successRate}%`, icon: CheckCircle2, tone: 'text-emerald-500' },
    { label: 'Failures', value: data.summary.failed, icon: AlertTriangle, tone: 'text-red-500' },
    { label: 'Average duration', value: duration(data.summary.avgDurationMs), icon: Clock3, tone: 'text-amber-500' },
    { label: 'SLO breaches', value: data.pipelines.filter(p => p.health === 'critical' || p.health === 'warning').length, icon: AlertTriangle, tone: 'text-red-500', title: 'SLO breaches include availability SLOs (e.g. pipeline-not-run-in-N-hours) evaluated independently of the run count window' },
    { label: 'Quality rejects', value: data.quality.failedRows, icon: AlertTriangle, tone: data.quality.failedRows ? 'text-red-500' : 'text-emerald-500' },
  ] : [];

  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div><h2 className="page-heading">Pipeline monitoring</h2><p className="page-subtitle mt-1">Health, reliability, latency, and recent failures across the workspace.</p></div>
        <div className="flex items-center gap-2">
          <select className="glass-input" value={days} onChange={e => setDays(Number(e.target.value))} aria-label="Time range">
            <option value={1}>24 hours</option><option value={7}>7 days</option><option value={30}>30 days</option><option value={90}>90 days</option>
          </select>
          <button className="glass-btn-ghost flex items-center gap-1.5 text-sm" onClick={refresh} disabled={loading}>
            <RefreshCw size={14} className={loading ? 'animate-spin' : ''} /> Refresh
          </button>
        </div>
      </div>
      {error && <ApiError message={error} onRetry={() => void refresh()} />}

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-6">
        {cards.map(({ label, value, icon: Icon, tone, title }: any) => <div key={label} className="glass-card p-4" title={title}>
          <div className="flex items-center justify-between"><p className="text-xs text-gray-500 dark:text-white/45">{label}</p><Icon size={16} className={tone} /></div>
          <p className="mt-2 text-2xl font-semibold text-gray-900 dark:text-white/90">{value}</p>
        </div>)}
      </div>

      <section className="glass-card overflow-hidden">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-5 py-4 dark:border-white/5">
          <div><h3 className="text-sm font-semibold text-gray-900 dark:text-white/90">Execution activity</h3><p className="mt-0.5 text-[10px] text-gray-400">Latest durable node outcomes. Sensitive values are redacted.</p></div>
          <form className="flex items-center gap-2" onSubmit={event => { event.preventDefault(); setLogQuery(logInput.trim()); }}>
            <input className="glass-input w-56" value={logInput} onChange={event => setLogInput(event.target.value)} placeholder="Pipeline, run, node, error…" aria-label="Search execution activity" />
            <select className="glass-input" value={logLevel} onChange={event => setLogLevel(event.target.value)} aria-label="Activity level">
              <option value="all">All levels</option><option value="error">Errors</option><option value="info">Info</option>
            </select>
            <button className="glass-btn-ghost text-sm">Search</button>
          </form>
        </div>
        {!logs.length ? <p className="px-5 py-6 text-sm text-gray-400">No matching activity.</p> : <div className="max-h-80 divide-y divide-gray-100 overflow-y-auto dark:divide-white/5">
          {logs.map(item => <Link key={`${item.execution_id}:${item.node_id}`} to={`/runs/${item.execution_id}`} className="grid gap-2 px-5 py-3 hover:bg-gray-50 dark:hover:bg-white/3 sm:grid-cols-[7rem_1fr_10rem]">
            <span className={`text-[10px] font-semibold uppercase ${item.level === 'error' ? 'text-red-600 dark:text-red-400' : 'text-emerald-600 dark:text-emerald-400'}`}>{item.level} · {item.node_id}</span>
            <span className="min-w-0"><span className="block truncate text-xs font-medium text-gray-900 dark:text-white/85">{item.name}</span><span className={`block truncate text-[10px] ${item.level === 'error' ? 'text-red-500' : 'text-gray-500 dark:text-white/45'}`}>{item.message}</span></span>
            <span className="text-right text-[10px] text-gray-400">{displayEnvironment(item.environment)} · {new Date(item.finished_at).toLocaleString()}</span>
          </Link>)}
        </div>}
      </section>

      <section className="glass-card overflow-hidden">
        <div className="flex items-center justify-between border-b border-gray-100 px-5 py-4 dark:border-white/5">
          <div><h3 className="text-sm font-semibold text-gray-900 dark:text-white/90">Data quality</h3><p className="mt-0.5 text-[10px] text-gray-400">{data?.quality.checks ?? 0} checks · {(data?.quality.passedRows ?? 0).toLocaleString()} rows passed</p></div>
          <span className={`glass-badge ${(data?.quality.issues ?? 0) ? 'text-amber-600 dark:text-amber-400' : 'text-emerald-600 dark:text-emerald-400'}`}>{data?.quality.issues ?? 0} issues</span>
        </div>
        {!data?.recentQualityIssues.length ? <p className="px-5 py-6 text-sm text-gray-400">No quality violations in this period.</p> : <div className="divide-y divide-gray-100 dark:divide-white/5">
          {data.recentQualityIssues.map(issue => <Link key={`${issue.execution_id}:${issue.node_id}`} to={`/runs/${issue.execution_id}`} className="flex items-center gap-3 px-5 py-3 hover:bg-gray-50 dark:hover:bg-white/3">
            <span className={`rounded-full px-2 py-1 text-[10px] font-semibold ${issue.status === 'failed' ? 'bg-red-500/10 text-red-600 dark:text-red-400' : 'bg-amber-500/10 text-amber-600 dark:text-amber-400'}`}>{issue.status}</span>
            <div className="min-w-0 flex-1"><p className="truncate text-xs font-medium text-gray-900 dark:text-white/85">{issue.name} · {issue.node_id}</p><p className="mt-0.5 truncate text-[10px] text-gray-500 dark:text-white/45">{Number(issue.failed_count).toLocaleString()} rejected · {issue.error_samples?.[0]?.errors?.join('; ') ?? 'Contract violation'}</p></div>
            <span className="text-[10px] text-gray-400">{displayEnvironment(issue.environment)}</span>
          </Link>)}
        </div>}
      </section>

      <section className="glass-card overflow-hidden">
        <div className="flex items-center justify-between border-b border-gray-100 px-5 py-4 dark:border-white/5">
          <div><h3 className="text-sm font-semibold text-gray-900 dark:text-white/90">Active incidents</h3><p className="mt-0.5 text-[10px] text-gray-400">Deduplicated until the underlying breach resolves.</p></div>
          <span className="glass-badge">{alerts.length}</span>
        </div>
        {!alerts.length ? <p className="px-5 py-6 text-sm text-gray-400">No active incidents.</p> : <div className="divide-y divide-gray-100 dark:divide-white/5">
          {alerts.map(alert => <div key={alert.id} className="flex flex-wrap items-center gap-3 px-5 py-3">
            <span className={`rounded-full px-2 py-1 text-[10px] font-semibold ${alert.severity === 'critical' ? 'bg-red-500/10 text-red-600 dark:text-red-400' : 'bg-amber-500/10 text-amber-600 dark:text-amber-400'}`}>{alert.severity}</span>
            <div className="min-w-0 flex-1"><p className="truncate text-xs font-medium text-gray-900 dark:text-white/85">{alert.pipeline_name} · {alert.kind}</p><p className="mt-0.5 truncate text-[10px] text-gray-500 dark:text-white/45">{alert.message}</p></div>
            <span className="text-[10px] text-gray-400">{alert.status}</span>
            {alert.notification_sent_at && <span className="text-[10px] text-emerald-600 dark:text-emerald-400">notified</span>}
            {alert.notification_error && <button className="glass-btn-ghost px-2.5 py-1 text-xs text-amber-600" disabled={alertBusy === alert.id} title={alert.notification_error} onClick={() => retryNotification(alert.id)}>Retry webhook</button>}
            {alert.status === 'open' && <button className="glass-btn-ghost px-2.5 py-1 text-xs" disabled={alertBusy === alert.id} onClick={() => updateAlert(alert.id, 'acknowledge')}>Acknowledge</button>}
            <button className="glass-btn-danger px-2.5 py-1 text-xs" disabled={alertBusy === alert.id} onClick={() => updateAlert(alert.id, 'resolve')}>Resolve</button>
          </div>)}
        </div>}
      </section>

      <div className="grid gap-6 xl:grid-cols-[1.6fr_1fr]">
        <section className="glass-card p-5">
          <h3 className="text-sm font-semibold text-gray-900 dark:text-white/90">Run volume and failures</h3>
          <div className="mt-5 flex h-44 items-end gap-2" aria-label="Run trend">
            {data?.trend.map(point => {
              const runs = Number(point.runs), failed = Number(point.failed);
              return <div key={point.day} className="flex min-w-0 flex-1 flex-col items-center gap-1" title={`${point.day}: ${runs} runs, ${failed} failed`}>
                <div className="relative flex h-36 w-full items-end overflow-hidden rounded-t bg-gray-100 dark:bg-white/4">
                  <div className="w-full bg-brand-400/70" style={{ height: `${Math.max(runs ? 6 : 0, (runs / maxRuns) * 100)}%` }} />
                  {failed > 0 && <div className="absolute bottom-0 left-0 w-full bg-red-500/80" style={{ height: `${Math.max(5, (failed / maxRuns) * 100)}%` }} />}
                </div>
                <span className="truncate text-[10px] text-gray-400">{new Date(point.day).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}</span>
              </div>;
            })}
          </div>
        </section>
        <section className="glass-card p-5">
          <div className="flex items-center justify-between"><h3 className="text-sm font-semibold text-gray-900 dark:text-white/90">Recent failures</h3><Link to="/runs" className="text-xs text-brand-500">All runs</Link></div>
          <div className="mt-3 divide-y divide-gray-100 dark:divide-white/5">
            {!data?.recentFailures.length && <p className="py-6 text-sm text-gray-400">No failures in this period.</p>}
            {data?.recentFailures.map(failure => <Link key={failure.id} to={`/runs/${failure.id}`} className="block py-3">
              <div className="flex items-center justify-between gap-2"><p className="truncate text-xs font-medium text-gray-900 dark:text-white/85">{failure.name}</p><span className="text-[10px] text-gray-400">{displayEnvironment(failure.environment)}</span></div>
              <p className="mt-1 line-clamp-2 text-[10px] text-red-500 dark:text-danger/80">{failure.node_id ? `${failure.node_id}: ` : ''}{failure.error ?? 'Pipeline failed'}</p>
            </Link>)}
          </div>
        </section>
      </div>

      <section className="glass-card overflow-hidden">
        <div className="border-b border-gray-100 px-5 py-4 dark:border-white/5"><h3 className="text-sm font-semibold text-gray-900 dark:text-white/90">Pipeline health</h3></div>
        <div className="overflow-x-auto"><table className="w-full text-left text-xs">
          <thead className="bg-gray-50 text-[10px] uppercase tracking-wide text-gray-400 dark:bg-white/2.5"><tr>
            <th className="px-5 py-3">Pipeline</th><th className="px-4 py-3">Health</th><th className="px-4 py-3">Last run</th><th className="px-4 py-3">Runs</th><th className="px-4 py-3">Failures</th><th className="px-4 py-3">Avg duration</th>
          </tr></thead>
          <tbody className="divide-y divide-gray-100 dark:divide-white/5">
            {data?.pipelines.map(pipeline => <tr key={`${pipeline.id}:${pipeline.environment}`}>
              <td className="px-5 py-3"><p className="font-medium text-gray-900 dark:text-white/85">{pipeline.name}</p><p className="text-[10px] text-gray-400">v{pipeline.version} · {displayEnvironment(pipeline.environment)} · {pipeline.metadata?.owner ?? 'unowned'}</p></td>
              <td className="px-4 py-3"><span className={`rounded-full px-2 py-1 text-[10px] font-semibold ${pipeline.health === 'healthy' ? 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400' : pipeline.health === 'critical' ? 'bg-red-500/10 text-red-600 dark:text-red-400' : pipeline.health === 'warning' ? 'bg-amber-500/10 text-amber-600 dark:text-amber-400' : 'bg-gray-500/10 text-gray-500 dark:text-gray-400'}`} title={pipeline.breaches?.map(item => item.message).join('\n')}>{pipeline.health ?? 'unknown'}{pipeline.breaches?.length ? ` · ${pipeline.breaches.length}` : ''}</span></td>
              <td className="px-4 py-3">{pipeline.last_execution_id ? <Link to={`/runs/${pipeline.last_execution_id}`} className={phaseColor[pipeline.last_phase ?? ''] ?? 'text-gray-500'}>{pipeline.last_phase}</Link> : <span className="text-gray-400">Never</span>}</td>
              <td className="px-4 py-3">{pipeline.runs}</td><td className="px-4 py-3 text-red-500">{pipeline.failed}</td><td className="px-4 py-3">{duration(Number(pipeline.avg_duration_ms ?? 0))}</td>
            </tr>)}
          </tbody>
        </table></div>
      </section>
    </div>
  );
}
