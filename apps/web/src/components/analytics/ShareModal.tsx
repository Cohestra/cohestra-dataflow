import { useCallback, useEffect, useState } from 'react';
import { Copy, Link2, Trash2, X } from 'lucide-react';
import { api } from '../../api';

interface Share { share_token_hash: string; expires_at: string; created_at: string }

export function ShareModal({ dashboardId, onClose }: { dashboardId: string; onClose: () => void }) {
  const [shares, setShares] = useState<Share[]>([]);
  const [newUrl, setNewUrl] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const load = useCallback(() => {
    api.listDashboardShares(dashboardId).then(setShares).catch(e => setErr(e.message));
  }, [dashboardId]);
  useEffect(load, [load]);

  const create = async () => {
    setBusy(true);
    setErr(null);
    try {
      const { shareUrl } = await api.shareDashboard(dashboardId);
      const url = `${window.location.origin}${shareUrl}`;
      setNewUrl(url);
      try { await navigator.clipboard.writeText(url); } catch { /* clipboard unavailable */ }
      load();
    } catch (e: any) {
      setErr(e.message);
    } finally {
      setBusy(false);
    }
  };

  const revoke = async (hash: string) => {
    try {
      await api.revokeDashboardShare(dashboardId, hash);
      setShares(prev => prev.filter(s => s.share_token_hash !== hash));
    } catch (e: any) {
      setErr(e.message);
    }
  };

  return (
    <div className="glass-modal-backdrop" onClick={onClose}>
      <div className="glass-modal max-w-lg" onClick={e => e.stopPropagation()}>
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-lg font-semibold">Share Dashboard</h2>
          <button className="glass-btn-ghost w-8 h-8 flex items-center justify-center" aria-label="Close" onClick={onClose}><X size={15} /></button>
        </div>

        <button className="glass-btn-primary text-sm w-full" disabled={busy} onClick={create}>
          <Link2 size={14} /> {busy ? 'Creating…' : 'Create read-only link (24h)'}
        </button>

        {newUrl && (
          <div className="mt-3 flex items-center gap-2 text-xs bg-brand-500/10 text-brand-600 dark:text-brand-300 rounded-lg px-3 py-2">
            <span className="truncate flex-1">{newUrl}</span>
            <button className="glass-btn-ghost px-2 py-1 shrink-0" title="Copy"
              onClick={() => navigator.clipboard.writeText(newUrl).catch(() => {})}><Copy size={12} /></button>
          </div>
        )}
        {err && <p className="mt-3 text-xs text-danger/90 bg-danger/10 border border-danger/30 rounded-lg px-3 py-2">{err}</p>}

        <div className="mt-5">
          <h3 className="text-xs font-semibold text-gray-500 dark:text-white/50 uppercase tracking-wide mb-2">Active links</h3>
          {shares.length === 0 ? (
            <p className="text-xs text-gray-400 dark:text-white/30">No active share links.</p>
          ) : (
            <ul className="space-y-1">
              {shares.map(s => (
                <li key={s.share_token_hash} className="flex items-center justify-between text-xs px-2 py-1.5 rounded-lg bg-gray-50 dark:bg-white/4">
                  <span className="text-gray-600 dark:text-white/60">
                    …{s.share_token_hash.slice(-8)} · expires {new Date(s.expires_at).toLocaleString()}
                  </span>
                  <button className="glass-btn-ghost px-2 py-1 hover:text-danger" title="Revoke"
                    onClick={() => revoke(s.share_token_hash)}><Trash2 size={12} /></button>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </div>
  );
}
