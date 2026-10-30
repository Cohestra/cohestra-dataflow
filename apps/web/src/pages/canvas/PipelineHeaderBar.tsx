import { Activity, ChevronDown, Clock, Layers3, Rocket } from 'lucide-react';
import type { Stage } from '../../utils/pipelineStage';

const STAGE_STYLES: Record<Stage, string> = {
  draft:      'bg-amber-100  text-amber-700  dark:bg-amber-500/15  dark:text-amber-300',
  testing:    'bg-blue-100   text-blue-700   dark:bg-blue-500/15   dark:text-blue-300',
  production: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300',
  archived:   'bg-gray-100   text-gray-500   dark:bg-white/5       dark:text-white/35',
};

// A fresh, unguessable signing secret for a new webhook trigger. The API
// rejects placeholders and returns secrets blank afterwards (write-only).
function newWebhookSecret(): string {
  const bytes = new Uint8Array(24);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('');
}

function newWebhookPath(): string {
  return `hook-${newWebhookSecret().slice(0, 10)}`;
}

// Top floating pill: pipeline name, lifecycle-stage dropdown, and trigger
// config (manual/cron/webhook/upstream-pipeline/asset picker). These three
// share one visual pill and one "left offset shifts when a side panel opens"
// concern, so they stay together rather than being split into a name
// component + a separate upstream-pipeline-picker component.
export function PipelineHeaderBar({
  leftOffset, name, setName,
  pipelineStage, showLifecycle, setShowLifecycle, isOwner, activate, promote,
  trigger, setTrigger, pipelineKey, upstreamPipelines, workspaceAssets,
}: {
  leftOffset: number;
  name: string;
  setName: (name: string) => void;
  pipelineStage: Stage;
  showLifecycle: boolean;
  setShowLifecycle: (updater: boolean | ((v: boolean) => boolean)) => void;
  isOwner: boolean;
  activate: () => void;
  promote: () => void;
  trigger: any;
  setTrigger: (trigger: any) => void;
  pipelineKey: string;
  upstreamPipelines: Array<{ pipeline_key: string; name: string }>;
  workspaceAssets: Array<{ urn: string; name: string; layer?: string }>;
}) {
  return (
    <div className="absolute top-4 z-10 flex items-center gap-2 pointer-events-none transition-[left] duration-200
        left-3 sm:left-(--header-left)"
      style={{ '--header-left': `${leftOffset}px` } as React.CSSProperties}>
      <div className="pointer-events-auto flex items-center gap-2 rounded-2xl border border-gray-200 dark:border-white/9
        bg-white/95 dark:bg-[#0d1018]/90 px-3 py-2 shadow-xs dark:shadow-glass backdrop-blur-xl">
        <input
          className="bg-transparent text-sm font-semibold text-gray-900 dark:text-white/90 outline-hidden w-40 placeholder-gray-400 dark:placeholder-white/30"
          value={name} onChange={e => setName(e.target.value)} aria-label="Pipeline name" />
        <div className="h-4 w-px bg-gray-200 dark:bg-white/10" />
        <div className="relative">
          <button onClick={() => setShowLifecycle(v => !v)}
            className={`flex items-center gap-1 rounded-md px-2 py-0.5 text-[10px] font-semibold transition-all ${STAGE_STYLES[pipelineStage]}`}>
            {pipelineStage} <ChevronDown size={10} />
          </button>
          {showLifecycle && (
            <div className="absolute top-7 left-0 z-50 w-52 rounded-2xl border border-gray-200 dark:border-white/10
              bg-white dark:bg-[#11141d]/95 p-2 shadow-xl dark:shadow-glass backdrop-blur-xl">
              <p className="mb-2 px-2 text-[10px] font-semibold uppercase tracking-wider text-gray-400 dark:text-white/30">Lifecycle</p>
              {pipelineStage === 'draft' && (
                <button className="glass-btn-ghost w-full justify-start text-xs"
                  onClick={() => { activate(); setShowLifecycle(false); }}>
                  <Rocket size={13} /> Activate → Integration
                </button>
              )}
              {pipelineStage === 'testing' && isOwner && (
                <button className="glass-btn-primary w-full justify-start text-xs"
                  onClick={() => { promote(); setShowLifecycle(false); }}>
                  <Layers3 size={13} /> Promote to Production
                </button>
              )}
              {pipelineStage === 'production' && (
                <p className="flex items-center gap-1.5 px-2 py-2 text-xs text-emerald-600 dark:text-emerald-400"><Activity size={13} /> Live in production</p>
              )}
            </div>
          )}
        </div>
        <div className="h-4 w-px bg-gray-200 dark:bg-white/10" />
        <div className="flex items-center gap-1.5">
          <Clock size={12} className="text-gray-400 dark:text-white/30" />
          <select className="bg-transparent text-xs text-gray-600 dark:text-white/60 outline-hidden cursor-pointer"
            value={trigger.type}
            onChange={e => setTrigger({ type: e.target.value,
              ...(e.target.value === 'cron' ? { schedule: '*/5 * * * *' } : {}),
              ...(e.target.value === 'webhook' ? { path: newWebhookPath(), secret: newWebhookSecret() } : {}),
              ...(e.target.value === 'event' ? { topic: upstreamPipelines.find(p => p.pipeline_key !== pipelineKey)
                ? `pipeline.completed.${upstreamPipelines.find(p => p.pipeline_key !== pipelineKey)!.pipeline_key}` : 'pipeline.completed.<pipeline-key>' } : {}),
              ...(e.target.value === 'asset' ? { assetUrn: workspaceAssets[0]?.urn ?? '' } : {}) })}>
            <option value="manual">Manual</option>
            <option value="cron">Cron</option>
            <option value="webhook">Webhook</option>
            <option value="event">Upstream pipeline</option>
            <option value="asset">Asset materialized</option>
          </select>
          {trigger.type === 'cron' && (
            <input className="bg-transparent text-xs outline-hidden w-28 text-gray-600 dark:text-white/60 font-mono"
              value={trigger.schedule} onChange={e => setTrigger({ ...trigger, schedule: e.target.value })} />
          )}
          {trigger.type === 'webhook' && (
            <>
              <input className="bg-transparent text-xs outline-hidden w-32 text-gray-600 dark:text-white/60 font-mono"
                aria-label="Webhook path" title="Webhook path: POST /api/hooks/<path>"
                value={trigger.path ?? ''} onChange={e => setTrigger({ ...trigger, path: e.target.value })} />
              <input className="bg-transparent text-xs outline-hidden w-40 text-gray-600 dark:text-white/60 font-mono"
                aria-label="Webhook signing secret" title="HMAC-SHA256 signing secret. Copy it now: it is not shown again after saving."
                placeholder={trigger.secretSet ? 'secret set · type to replace' : 'signing secret (16+ chars)'}
                value={trigger.secret ?? ''} onChange={e => setTrigger({ ...trigger, secret: e.target.value })} />
            </>
          )}
          {trigger.type === 'event' && (
            <select className="max-w-48 bg-transparent text-xs text-gray-600 outline-hidden dark:text-white/60"
              value={trigger.topic} onChange={e => setTrigger({ ...trigger, topic: e.target.value })} aria-label="Upstream pipeline event">
              {!upstreamPipelines.some(p => p.pipeline_key !== pipelineKey) && <option value={trigger.topic}>{trigger.topic}</option>}
              {upstreamPipelines.filter(p => p.pipeline_key !== pipelineKey).flatMap(p => [
                <option key={`${p.pipeline_key}:completed`} value={`pipeline.completed.${p.pipeline_key}`}>{p.name} completed</option>,
                <option key={`${p.pipeline_key}:failed`} value={`pipeline.failed.${p.pipeline_key}`}>{p.name} failed</option>,
                <option key={`${p.pipeline_key}:cancelled`} value={`pipeline.cancelled.${p.pipeline_key}`}>{p.name} cancelled</option>,
              ])}
            </select>
          )}
          {trigger.type === 'asset' && (
            <select className="max-w-56 bg-transparent text-xs text-gray-600 outline-hidden dark:text-white/60"
              value={trigger.assetUrn} onChange={e => setTrigger({ ...trigger, assetUrn: e.target.value })} aria-label="Materialized asset">
              {!workspaceAssets.length && <option value="">No Integration output assets</option>}
              {workspaceAssets.map(asset => <option key={asset.urn} value={asset.urn}>{asset.name}{asset.layer ? ` · ${asset.layer}` : ''}</option>)}
            </select>
          )}
        </div>
      </div>
    </div>
  );
}
