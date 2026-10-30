import { Activity, CreditCard, Plus, User, Users, X } from 'lucide-react';
import type { CatalogEntry } from '@dataflow/shared';
import { ActivityIcon } from '../../components/canvas/FlowNode';
import { ApiError } from '../../components/ApiError';

export type WorkspacePanel = 'connectors' | 'settings' | null;

export interface PipelinePolicy {
  owner: string; domain: string; tags: string;
  freshnessMinutes: string; maxFailureRatePercent: string; maxDurationSeconds: string;
  notificationConnectionId: string; minimumSeverity: string;
}

// Right-side "Connectors" / "Workspace settings" panel opened from the left
// rail. Two unrelated-looking tabs that share one slide-out shell, so they
// stay one component rather than two (splitting further would just move the
// open/close plumbing around without cutting any real coupling).
export function WorkspaceSidePanel({
  workspacePanel, setWorkspacePanel, catalog, connectorInstances, connectorsError, refreshConnectors, addNode,
  policy, setPolicy, user, usage, members, settingsError, refreshSettings,
  inviteEmail, setInviteEmail, inviteBusy, inviteMsg, inviteMember,
}: {
  workspacePanel: WorkspacePanel;
  setWorkspacePanel: (panel: WorkspacePanel) => void;
  catalog: CatalogEntry[];
  connectorInstances: any[];
  connectorsError: string | null;
  refreshConnectors: () => void;
  addNode: (entry: CatalogEntry) => void;
  policy: PipelinePolicy;
  setPolicy: (updater: (p: PipelinePolicy) => PipelinePolicy) => void;
  user: any;
  usage: any | null;
  members: any[];
  settingsError: string | null;
  refreshSettings: () => void;
  inviteEmail: string;
  setInviteEmail: (email: string) => void;
  inviteBusy: boolean;
  inviteMsg: string;
  inviteMember: (event: React.FormEvent) => void;
}) {
  if (!workspacePanel) return null;

  return (
    <aside data-canvas-sidebar className="absolute top-3 bottom-3 z-20 flex flex-col overflow-hidden border border-gray-200 bg-white/97 shadow-xl backdrop-blur-lg dark:border-white/8 dark:bg-[#0d0f17]/97
    inset-x-3 rounded-2xl
    sm:inset-x-auto sm:left-[68px] sm:right-auto sm:w-[320px] sm:rounded-r-2xl sm:rounded-l-none">
      <div className="flex h-14 items-center justify-between border-b border-gray-100 px-4 dark:border-white/[0.07]">
        <div>
          <p className="text-xs font-semibold text-gray-900 dark:text-white/90">
            {workspacePanel === 'connectors' ? 'Connectors' : 'Workspace settings'}
          </p>
          <p className="text-[10px] text-gray-400 dark:text-white/35">
            {workspacePanel === 'connectors' ? 'Add and configure pipeline integrations' : 'Pipeline policy and workspace access'}
          </p>
        </div>
        <button aria-label="Close panel" className="icon-button h-8 w-8" onClick={() => setWorkspacePanel(null)}><X size={14} /></button>
      </div>

      {workspacePanel === 'connectors' ? (
        <div className="flex-1 overflow-auto p-3">
          {connectorsError && <div className="mb-3"><ApiError message={connectorsError} onRetry={refreshConnectors} /></div>}
          <div className="mb-3 rounded-xl border border-gray-200 bg-gray-50 px-3 py-2 dark:border-white/[0.07] dark:bg-white/[0.035]">
            <p className="text-[10px] font-semibold uppercase tracking-wider text-gray-400 dark:text-white/30">Connected accounts</p>
            <p className="mt-1 text-xs text-gray-700 dark:text-white/70">{connectorInstances.length} configured</p>
          </div>
          <div className="space-y-2">
            {catalog.filter(entry => entry.nodeType === 'source' || entry.nodeType === 'sink').map(entry => (
              <div key={entry.activityType} className="rounded-xl border border-gray-200 bg-white p-3 dark:border-white/8 dark:bg-white/[0.035]">
                <div className="flex items-start gap-3">
                  <span className="flex h-9 w-9 items-center justify-center rounded-xl text-white shadow-xs" style={{ background: entry.color }}>
                    <ActivityIcon activityType={entry.activityType} nodeType={entry.nodeType} />
                  </span>
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-xs font-semibold text-gray-800 dark:text-white/80">{entry.label.replace(' (destination)', '')}</p>
                    <p className="mt-0.5 truncate text-[10px] text-gray-400 dark:text-white/30">{entry.activityType}</p>
                    <span className="mt-2 inline-flex rounded-md bg-gray-100 px-1.5 py-0.5 text-[9px] font-semibold uppercase text-gray-500 dark:bg-white/6 dark:text-white/35">{entry.nodeType}</span>
                  </div>
                  <button title={`Add ${entry.label}`} onClick={() => addNode(entry)} className="icon-button h-8 w-8"><Plus size={14} /></button>
                </div>
              </div>
            ))}
          </div>
        </div>
      ) : (
        <div className="flex-1 space-y-3 overflow-auto p-3">
          {settingsError && <ApiError message={settingsError} onRetry={refreshSettings} />}
          <section className="rounded-xl border border-gray-200 bg-white p-3 dark:border-white/8 dark:bg-white/[0.035]">
            <div className="flex items-center gap-2 text-xs font-semibold text-gray-800 dark:text-white/80"><Activity size={14} /> Ownership & SLO</div>
            <div className="mt-3 space-y-2">
              <input className="glass-input py-1.5 text-xs" placeholder="Owner (team or email)" aria-label="Owner" value={policy.owner} onChange={e => setPolicy(p => ({ ...p, owner: e.target.value }))} />
              <input className="glass-input py-1.5 text-xs" placeholder="Domain (orders, finance…)" aria-label="Domain" value={policy.domain} onChange={e => setPolicy(p => ({ ...p, domain: e.target.value }))} />
              <input className="glass-input py-1.5 text-xs" placeholder="Tags, comma separated" aria-label="Tags" value={policy.tags} onChange={e => setPolicy(p => ({ ...p, tags: e.target.value }))} />
              <div className="grid grid-cols-2 gap-2">
                <input className="glass-input py-1.5 text-xs" type="number" min="1" placeholder="Freshness min" aria-label="Freshness minutes" value={policy.freshnessMinutes} onChange={e => setPolicy(p => ({ ...p, freshnessMinutes: e.target.value }))} />
                <input className="glass-input py-1.5 text-xs" type="number" min="0" max="100" placeholder="Max failures %" aria-label="Max failure rate percent" value={policy.maxFailureRatePercent} onChange={e => setPolicy(p => ({ ...p, maxFailureRatePercent: e.target.value }))} />
              </div>
              <input className="glass-input py-1.5 text-xs" type="number" min="0.001" step="0.001" placeholder="Max duration seconds" aria-label="Max duration seconds" value={policy.maxDurationSeconds} onChange={e => setPolicy(p => ({ ...p, maxDurationSeconds: e.target.value }))} />
              <select className="glass-select py-1.5 text-xs" value={policy.notificationConnectionId} onChange={e => setPolicy(p => ({ ...p, notificationConnectionId: e.target.value }))} aria-label="Alert webhook connection">
                <option value="">No alert webhook</option>
                {connectorInstances.filter(c => c.provider === 'http').map(c => <option key={c.id} value={c.id}>{c.name ?? c.baseUrl ?? c.id}</option>)}
              </select>
              {policy.notificationConnectionId && <select className="glass-select py-1.5 text-xs" value={policy.minimumSeverity} onChange={e => setPolicy(p => ({ ...p, minimumSeverity: e.target.value }))} aria-label="Minimum alert severity">
                <option value="critical">Critical only</option><option value="warning">Warning and critical</option>
              </select>}
            </div>
            <p className="mt-2 text-[9px] text-gray-400 dark:text-white/30">Versioned with pipeline; Monitoring evaluates breaches automatically.</p>
          </section>
          <section className="rounded-xl border border-gray-200 bg-white p-3 dark:border-white/8 dark:bg-white/[0.035]">
            <div className="flex items-center gap-2 text-xs font-semibold text-gray-800 dark:text-white/80"><User size={14} /> Profile</div>
            <p className="mt-3 truncate text-xs text-gray-700 dark:text-white/70">{user?.email}</p>
            <p className="mt-1 text-[10px] capitalize text-gray-400 dark:text-white/35">{user?.role ?? 'member'} role</p>
          </section>
          <section className="rounded-xl border border-gray-200 bg-white p-3 dark:border-white/8 dark:bg-white/[0.035]">
            <div className="flex items-center gap-2 text-xs font-semibold text-gray-800 dark:text-white/80"><CreditCard size={14} /> Billing</div>
            <div className="mt-3 flex items-end justify-between">
              <div><p className="text-lg font-semibold text-gray-900 dark:text-white/90">{usage?.used ?? '—'}</p><p className="text-[10px] text-gray-400 dark:text-white/35">executions used</p></div>
              <p className="text-[10px] text-gray-400 dark:text-white/35">{usage?.metered === false ? 'no limit' : `limit ${usage?.limit ?? '—'}`}</p>
            </div>
          </section>
          <section className="rounded-xl border border-gray-200 bg-white p-3 dark:border-white/8 dark:bg-white/[0.035]">
            <div className="flex items-center justify-between text-xs font-semibold text-gray-800 dark:text-white/80"><span className="flex items-center gap-2"><Users size={14} /> Members</span><span className="glass-badge">{members.length}</span></div>
            <div className="mt-2 divide-y divide-gray-100 dark:divide-white/5">
              {members.slice(0, 6).map(member => <div key={member.id} className="flex items-center justify-between gap-2 py-2 text-[11px]"><span className="truncate text-gray-600 dark:text-white/60">{member.email}</span><span className="capitalize text-gray-400 dark:text-white/30">{member.role}</span></div>)}
              {!members.length && <p className="py-2 text-[11px] text-gray-400 dark:text-white/30">No members loaded</p>}
            </div>
            {user?.role === 'owner' && (
              <form className="mt-2 flex gap-2" onSubmit={inviteMember}>
                <input type="email" required className="glass-input min-w-0 flex-1 py-1.5 text-xs" placeholder="teammate@company.com" aria-label="Invite teammate by email" value={inviteEmail} onChange={e => setInviteEmail(e.target.value)} />
                <button className="glass-btn-primary px-2.5 py-1.5 text-xs" disabled={inviteBusy}>{inviteBusy ? 'Sending' : 'Invite'}</button>
              </form>
            )}
            {inviteMsg && <p className="mt-2 text-[10px] text-gray-400 dark:text-white/35">{inviteMsg}</p>}
          </section>
        </div>
      )}
    </aside>
  );
}
