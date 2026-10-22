import {
  Activity, ChevronDown, ChevronUp, Code2, History, Maximize2, Minimize2, Play, Terminal,
} from 'lucide-react';
import { MermaidPreview } from '../../components/MermaidPreview';
import { MOBILE_RAIL_CLEARANCE } from './NodePalette';
import type { Stage } from '../../utils/pipelineStage';

export type BottomTab = 'runs' | 'logs' | 'lifecycle' | 'mermaid';

// IDE-style bottom drawer: runs table, execution log detail, lifecycle
// stepper, and the Mermaid tab, all sharing one resizable/expandable shell
// and tab strip. The collapsed "Output" pill is included here too since it's
// just the drawer's closed state.
export function OutputDrawer({
  drawerOpen, drawerExpanded, drawerHeight, startResize,
  bottomTab, setBottomTab, setDrawerExpanded, setDrawerOpen, openDrawer,
  runsLoading, recentRuns, run, selectRun,
  selectedRun, runDetail, executionId,
  pipelineStage,
  mermaidDraft, setMermaidDraft, mermaidValid, setMermaidValid, applyMermaid,
}: {
  drawerOpen: boolean;
  drawerExpanded: boolean;
  drawerHeight: number;
  startResize: (event: React.PointerEvent) => void;
  bottomTab: BottomTab;
  setBottomTab: (tab: BottomTab) => void;
  setDrawerExpanded: (updater: (v: boolean) => boolean) => void;
  setDrawerOpen: (open: boolean) => void;
  openDrawer: (tab?: BottomTab) => void;
  runsLoading: boolean;
  recentRuns: any[];
  run: () => void;
  selectRun: (row: any) => void;
  selectedRun: any | null;
  runDetail: any | null;
  executionId: string | null;
  pipelineStage: Stage;
  mermaidDraft: string;
  setMermaidDraft: (value: string) => void;
  mermaidValid: boolean;
  setMermaidValid: (valid: boolean) => void;
  applyMermaid: () => void;
}) {
  if (!drawerOpen) {
    return (
      <button title="Open output panel" onClick={() => openDrawer(bottomTab)}
        className={`absolute left-1/2 z-20 flex -translate-x-1/2 items-center gap-2 rounded-xl border border-gray-200 bg-white/95 px-3 py-1.5 text-[11px] text-gray-500 shadow-xs backdrop-blur-sm dark:border-white/8 dark:bg-[#11141d]/90 dark:text-white/45 ${MOBILE_RAIL_CLEARANCE}`}>
        <ChevronUp size={13} /> Output
      </button>
    );
  }

  return (
    <div className="absolute z-20 flex flex-col overflow-hidden rounded-2xl
      border border-gray-200 dark:border-white/8
      bg-white/97 dark:bg-[#0d1018]/96 backdrop-blur-xl
      shadow-[0_-4px_24px_rgba(0,0,0,.08)] dark:shadow-[0_-8px_32px_rgba(0,0,0,.4)]
      inset-x-3 bottom-24 sm:inset-x-auto sm:left-[68px] sm:right-3 sm:bottom-3"
      style={{ height: drawerExpanded ? '52vh' : drawerHeight }}>
      <button aria-label="Resize output panel" onPointerDown={startResize}
        className="absolute -top-1 left-0 right-0 h-2 cursor-row-resize bg-transparent" />
      <div className="flex h-10 flex-none items-center gap-1 border-b border-gray-100 px-3 dark:border-white/6">
        {([
          ['runs', History, 'Runs'], ['logs', Terminal, 'Logs'],
          ['lifecycle', Activity, 'Lifecycle'], ['mermaid', Code2, 'Mermaid'],
        ] as const).map(([id, Icon, label]) => (
          <button key={id} onClick={() => setBottomTab(id)}
            className={`flex h-8 items-center gap-1.5 rounded-lg px-2.5 text-[11px] font-medium transition ${bottomTab === id ? 'bg-gray-100 text-gray-800 dark:bg-white/8 dark:text-white/80' : 'text-gray-400 hover:text-gray-700 dark:text-white/35 dark:hover:text-white/65'}`}>
            <Icon size={12} /> {label}
          </button>
        ))}
        <div className="flex-1" />
        <span className="hidden text-[10px] text-gray-400 dark:text-white/30 sm:inline">Drag top edge to resize</span>
        <button className="icon-button h-7 w-7 border-transparent bg-transparent" title={drawerExpanded ? 'Restore panel' : 'Expand panel'}
          onClick={() => setDrawerExpanded(v => !v)}>{drawerExpanded ? <Minimize2 size={13} /> : <Maximize2 size={13} />}</button>
        <button className="icon-button h-7 w-7 border-transparent bg-transparent" title="Collapse panel"
          onClick={() => setDrawerOpen(false)}><ChevronDown size={14} /></button>
      </div>
      <div className="flex-1 overflow-auto">
        {bottomTab === 'runs' && runsLoading && (
          <div className="flex items-center justify-center h-full text-xs text-gray-400 dark:text-white/30">Loading…</div>
        )}
        {bottomTab === 'runs' && !runsLoading && recentRuns.length === 0 && (
          <div className="flex flex-col items-center justify-center h-full gap-2 text-center">
            <p className="text-xs text-gray-400 dark:text-white/30">No runs yet.</p>
            <button className="glass-btn-primary text-xs" onClick={run}><Play size={12} /> Run pipeline</button>
          </div>
        )}
        {bottomTab === 'runs' && !runsLoading && recentRuns.length > 0 && (
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-gray-100 dark:border-white/6">
                <th className="px-4 py-1.5 text-left font-medium text-gray-400 dark:text-white/30">Pipeline</th>
                <th className="px-4 py-1.5 text-left font-medium text-gray-400 dark:text-white/30">Status</th>
                <th className="px-4 py-1.5 text-left font-medium text-gray-400 dark:text-white/30">Started</th>
              </tr>
            </thead>
            <tbody>
              {recentRuns.slice(0, 20).map(r => {
                const statusColor =
                  r.phase === 'completed' ? 'text-emerald-600 dark:text-emerald-400'
                  : r.phase === 'failed'  ? 'text-red-500 dark:text-danger'
                  : r.phase === 'running' ? 'text-amber-600 dark:text-amber-300'
                  : 'text-gray-400 dark:text-white/40';
                return (
                  <tr key={r.id}
                    className="border-b border-gray-50 dark:border-white/4 hover:bg-gray-50 dark:hover:bg-white/3 cursor-pointer"
                    onClick={() => selectRun(r)}>
                    <td className="px-4 py-2 text-gray-700 dark:text-white/70 truncate max-w-[200px]">{r.name}</td>
                    <td className={`px-4 py-2 font-medium ${statusColor}`}>{r.phase ?? '—'}</td>
                    <td className="px-4 py-2 text-gray-400 dark:text-white/35">
                      {r.started_at ? new Date(r.started_at).toLocaleString() : '—'}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
        {bottomTab === 'logs' && (
          <div className="h-full p-3 font-mono text-[11px] text-gray-600 dark:text-white/60">
            {!selectedRun && !executionId && <p className="text-gray-400 dark:text-white/30">Select a run to inspect execution output.</p>}
            {executionId && !selectedRun && <p><span className="text-amber-500">RUNNING</span> {executionId}</p>}
            {selectedRun && <div className="mb-3 flex items-center gap-3 font-sans"><span className="font-semibold text-gray-800 dark:text-white/80">{selectedRun.name}</span><span className="glass-badge">{selectedRun.phase}</span></div>}
            {selectedRun && !runDetail && <p className="text-gray-400 dark:text-white/30">Loading execution detail…</p>}
            {runDetail && <pre className="whitespace-pre-wrap wrap-break-word leading-relaxed">{runDetail.error ?? JSON.stringify(runDetail, null, 2)}</pre>}
          </div>
        )}
        {bottomTab === 'lifecycle' && (
          <div className="grid h-full gap-3 p-4 sm:grid-cols-3">
            {(['draft', 'testing', 'production'] as Stage[]).map((stage, index) => (
              <div key={stage} className={`rounded-xl border p-3 ${pipelineStage === stage ? 'border-brand-400/50 bg-brand-500/6' : 'border-gray-200 dark:border-white/[0.07]'}`}>
                <div className="flex items-center gap-2"><span className={`flex h-6 w-6 items-center justify-center rounded-full text-[10px] font-bold ${pipelineStage === stage ? 'bg-brand-500 text-white' : 'bg-gray-100 text-gray-400 dark:bg-white/6 dark:text-white/30'}`}>{index + 1}</span><p className="text-xs font-semibold capitalize text-gray-800 dark:text-white/80">{stage === 'testing' ? 'Integration' : stage}</p></div>
                <p className="mt-2 text-[10px] text-gray-400 dark:text-white/30">{pipelineStage === stage ? 'Current pipeline stage' : stage === 'production' ? 'Promote after green Integration run' : 'Saved pipeline version'}</p>
              </div>
            ))}
          </div>
        )}
        {bottomTab === 'mermaid' && (
          <div className="grid min-h-full gap-3 p-3 md:grid-cols-2">
            <div className="flex min-h-0 flex-col">
              <textarea className="glass-input min-h-36 flex-1 font-mono text-[11px]" value={mermaidDraft} onChange={e => setMermaidDraft(e.target.value)} />
              <button className="glass-btn-primary mt-2 self-start text-xs disabled:cursor-not-allowed disabled:opacity-50" disabled={!mermaidValid} onClick={applyMermaid}><Code2 size={13} /> Apply to DAG</button>
            </div>
            <div className="min-h-36 overflow-auto rounded-xl border border-gray-200 bg-gray-50 p-2 dark:border-white/[0.07] dark:bg-black/15"><MermaidPreview source={mermaidDraft} onValidChange={setMermaidValid} /></div>
          </div>
        )}
      </div>
    </div>
  );
}
