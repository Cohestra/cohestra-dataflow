import { Play, Rocket, Save } from 'lucide-react';

// Top-right action bar: live status text (the smoke test's
// #pipeline-action-status), execution-engine picker, and Save/Activate/Run.
// Kept as one component because the buttons' disabled state and the status
// text are derived from the same graphReady/hasUnsavedChanges pair.
export function PipelineActionBar({
  msg, graphReady, firstValidationError, hasUnsavedChanges, isDirty,
  execution, setExecution, features,
  savedRowId, save, activate, run,
}: {
  msg: string;
  graphReady: boolean;
  firstValidationError?: string;
  hasUnsavedChanges: boolean;
  isDirty: boolean;
  execution: any;
  setExecution: (execution: any) => void;
  features: { realtime: boolean; sparkSql: boolean; flinkSql: boolean };
  savedRowId: string | null;
  save: () => void;
  activate: () => void;
  run: () => void;
}) {
  // Current state outranks history: a validation error, then unsaved edits,
  // beat an earlier "Loaded/Saved" message. A failure message still shows.
  const failed = /fail/i.test(msg);
  const problem = !graphReady ? firstValidationError : undefined;
  const statusText = problem
    ?? (failed ? msg : undefined)
    ?? (hasUnsavedChanges ? 'Unsaved changes. Save before activating or running.' : undefined)
    ?? (msg || (isDirty && !savedRowId ? 'Not saved yet. Save to enable Activate and Run.' : ''));
  const alert = Boolean(problem) || failed;
  return (
    <>
      <div className="absolute top-4 right-4 z-10 flex flex-col items-end gap-2 pointer-events-none xl:flex-row xl:items-center">
        <span id="pipeline-action-status" role="status" aria-live="polite" className="sr-only">
          {statusText}
        </span>
        {statusText && (
          <span title={statusText} className={`pointer-events-auto order-last xl:order-none rounded-xl border bg-white/95 dark:bg-[#0d1018]/90 px-3 py-1.5 text-[11px] backdrop-blur-xl max-w-[min(320px,calc(100vw-2rem))] truncate shadow-sm ${
            alert ? 'border-red-200 text-red-700 dark:border-red-400/30 dark:text-red-300' : 'border-gray-200 text-gray-600 dark:border-white/[0.09] dark:text-white/70'}`}>
            {statusText}
          </span>
        )}
        <div className="pointer-events-auto flex items-center gap-1 rounded-2xl border border-gray-200 dark:border-white/[0.09]
          bg-white/95 dark:bg-[#0d1018]/90 px-2 py-2 shadow-sm dark:shadow-glass backdrop-blur-xl">
          <select className="max-w-32 bg-transparent text-xs text-gray-600 outline-none dark:text-white/60" aria-label="Execution engine" value={execution?.engine ?? 'workflow'} onChange={e => setExecution({ ...execution, engine: e.target.value })}>
            <option value="workflow">Workflow</option>
            <option value="stream-direct" disabled={!features.realtime}>Direct stream{!features.realtime ? ' · locked' : ''}</option>
            <option value="spark-sql" disabled={!features.sparkSql}>Spark SQL{!features.sparkSql ? ' · locked' : ''}</option>
            <option value="flink-sql" disabled={!features.realtime || !features.flinkSql}>Flink SQL{!features.realtime || !features.flinkSql ? ' · locked' : ''}</option>
          </select>
          <button aria-describedby="pipeline-action-status" className="glass-btn-ghost border-transparent bg-transparent text-xs disabled:cursor-not-allowed disabled:opacity-40" disabled={!graphReady} onClick={save}><Save size={14} /> Save</button>
          <button aria-describedby="pipeline-action-status" className="glass-btn-ghost border-transparent bg-transparent text-xs disabled:cursor-not-allowed disabled:opacity-40" disabled={!savedRowId || !graphReady || hasUnsavedChanges} onClick={activate}><Rocket size={14} /> Activate</button>
          <button aria-describedby="pipeline-action-status" className="glass-btn-primary text-xs disabled:cursor-not-allowed disabled:opacity-40" disabled={!savedRowId || !graphReady || hasUnsavedChanges} onClick={run}><Play size={13} fill="currentColor" /> Run</button>
        </div>
      </div>
      {(execution?.engine === 'spark-sql' || execution?.engine === 'flink-sql') && (
        <div className="pointer-events-auto absolute right-4 top-16 z-10 w-[min(520px,calc(100vw-2rem))] rounded-xl border border-gray-200 bg-white/95 p-2 shadow-sm dark:border-white/10 dark:bg-[#0d1018]/95">
          <input className="glass-input w-full font-mono text-xs" aria-label={`${execution.engine} SELECT`} placeholder="SELECT ... FROM source" value={execution?.transformSql ?? ''} onChange={e => setExecution({ ...execution, transformSql: e.target.value })} />
        </div>
      )}
    </>
  );
}
