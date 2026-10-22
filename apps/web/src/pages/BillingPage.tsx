import { useEffect, useState } from 'react';
import { Activity, CalendarClock, CreditCard, Gauge } from 'lucide-react';
import { api } from '../api';
import { useAuth } from '../context/AuthContext';
import { useFeatures } from '../context/FeatureContext';
import type { PaidFeatureKey } from '@dataflow/shared';

type Usage = {
  used: number;
  limit: number;
  free_tier: number;
  extra_quota: number;
  daysUntilReset: number;
  /** False on self-hosted installs: usage is counted but never limited. */
  metered?: boolean;
};

type Payment = {
  id: string;
  razorpay_order_id: string;
  razorpay_payment_id: string | null;
  amount_paise: number;
  quota_units: number;
  status: 'created' | 'paid' | 'failed';
  created_at: string;
  paid_at: string | null;
};

const ADD_ON_EXECUTIONS = 10_000;
const ADD_ON_PRICE = '$10';
const PLANS = [
  { name: 'Free', price: '$0', runs: '500 workflow runs / month' },
  { name: 'Starter', price: '$10', runs: '10,000 workflow runs / month' },
  { name: 'Growth', price: '$20', runs: '50,000 workflow runs / month' },
  { name: 'Scale', price: '$200', runs: '5,000,000 workflow runs / month' },
];
const ADD_ONS: Array<{ key: PaidFeatureKey; label: string; billing: string; description: string }> = [
  { key: 'realtime', label: 'Realtime', billing: 'Usage metered', description: 'CDC and Kafka streaming connectors.' },
  { key: 'sparkSql', label: 'Spark SQL', billing: 'Paid add-on', description: 'Large batch and incremental lake processing.' },
  { key: 'flinkSql', label: 'Flink SQL', billing: 'Paid add-on', description: 'Stateful event-time stream processing.' },
  { key: 'statefulProcessing', label: 'Stateful processing', billing: 'Paid add-on', description: 'Cross-run deduplication and durable transform state.' },
  { key: 'advancedConnectors', label: 'Advanced connectors', billing: 'Paid add-on', description: 'SFTP and warehouse connector pack.' },
  { key: 'deepObservability', label: 'Deep observability', billing: 'Paid add-on', description: 'Temporal traces and extended retention.' },
  { key: 'governance', label: 'Governance', billing: 'Enterprise', description: 'Audit export and advanced controls.' },
];

function fmtINR(paise: number) {
  return `₹${(paise / 100).toLocaleString('en-IN')}`;
}

function fmtDate(iso: string) {
  return new Date(iso).toLocaleDateString(undefined,
    { year: 'numeric', month: 'short', day: 'numeric' });
}

export function BillingPage() {
  const { user } = useAuth();
  const { features, availability, loading: featuresLoading, setFeature } = useFeatures();
  const [usage, setUsage] = useState<Usage | null>(null);
  const [history, setHistory] = useState<Payment[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [featureBusy, setFeatureBusy] = useState<PaidFeatureKey | null>(null);

  async function refresh() {
    try {
      const [u, h] = await Promise.all([api.getUsage(), api.getBillingHistory()]);
      setUsage(u);
      setHistory(h);
    } catch (e) {
      setError((e as Error).message);
    }
  }

  useEffect(() => {
    refresh();
  }, []);

  const used = usage?.used ?? 0;
  const limit = usage?.limit ?? 0;
  const metered = usage?.metered !== false;
  const pct = metered && limit > 0 ? Math.min(100, Math.round((used / limit) * 100)) : 0;

  async function toggleFeature(feature: PaidFeatureKey, enabled: boolean) {
    setFeatureBusy(feature); setError(null);
    try { await setFeature(feature, enabled); }
    catch (e) { setError((e as Error).message); }
    finally { setFeatureBusy(null); }
  }

  async function downloadAudit() {
    try {
      const blob = await api.downloadAuditExport();
      const url = URL.createObjectURL(blob);
      const link = document.createElement('a'); link.href = url; link.download = 'audit-log.csv'; link.click();
      URL.revokeObjectURL(url);
    } catch (e) { setError((e as Error).message); }
  }

  return (
    <div className="mx-auto max-w-4xl space-y-6 px-6 py-10">
      <div>
        <h2 className="page-heading">Usage & billing</h2>
        <p className="page-subtitle mt-1">Track workflow executions and extend monthly capacity.</p>
      </div>

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4" aria-label="DataFlow plans">
        {PLANS.map(plan => (
          <div className="glass-card p-4" key={plan.name}>
            <p className="text-xs font-semibold uppercase tracking-[.12em] opacity-50">{plan.name}</p>
            <p className="mt-2 text-2xl font-semibold">{plan.price}<span className="text-xs font-normal opacity-50"> / mo</span></p>
            <p className="mt-2 text-xs opacity-60">{plan.runs}</p>
          </div>
        ))}
      </div>

      <div className="glass-panel p-6">
        <div className="flex items-baseline justify-between mb-4">
          <h3 className="text-base font-semibold">Usage this month</h3>
          <span className="glass-badge"><Activity size={12} /> live meter</span>
        </div>

        {error && (
          <div className="glass-card p-3 mb-4 text-sm text-red-300">
            {error}
          </div>
        )}

        <div className="mb-3 h-3 rounded-full bg-white/10 overflow-hidden">
          <div
            className="h-full bg-indigo-400/80 transition-all"
            style={{ width: `${pct}%` }}
          />
        </div>
        <div className="text-sm opacity-80 mb-6">
          {metered ? `${used} / ${limit} executions` : `${used} executions this month · no limit on this install`}
        </div>

        <dl className="grid grid-cols-1 sm:grid-cols-3 gap-3 text-sm">
          <div className="glass-card p-4">
            <dt className="flex items-center gap-2 text-xs text-gray-400 dark:text-white/40"><Gauge size={14} /> Free tier</dt>
            <dd className="text-lg">{usage?.free_tier ?? '—'} / month</dd>
          </div>
          <div className="glass-card p-4">
            <dt className="flex items-center gap-2 text-xs text-gray-400 dark:text-white/40"><CreditCard size={14} /> Purchased</dt>
            <dd className="text-lg">
              {usage?.extra_quota ?? 0}
            </dd>
          </div>
          <div className="glass-card p-4">
            <dt className="flex items-center gap-2 text-xs text-gray-400 dark:text-white/40"><CalendarClock size={14} /> Resets in</dt>
            <dd className="text-lg">{usage?.daysUntilReset ?? '—'} days</dd>
          </div>
        </dl>

        <div className="glass-card mt-6 flex items-center justify-between gap-4 p-4">
          <div>
            <p className="font-medium">Additional workflow runs</p>
            <p className="mt-1 text-xs opacity-60">{ADD_ON_EXECUTIONS.toLocaleString()} extra runs</p>
          </div>
          <div className="text-right">
            <p className="text-xl font-semibold">{ADD_ON_PRICE}</p>
            <p className="text-xs opacity-50">add-on</p>
          </div>
        </div>
      </div>

      <div className="glass-panel p-6">
        <h3 className="text-lg font-semibold">Feature add-ons</h3>
        <p className="mt-1 text-sm opacity-60">Workspace owners control commercial entitlements. Uninstalled capabilities cannot be enabled.</p>
        <div className="mt-4 divide-y divide-white/5">
          {ADD_ONS.map(addOn => {
            const installed = availability[addOn.key];
            const enabled = features[addOn.key];
            return <div key={addOn.key} className="flex items-center justify-between gap-4 py-4">
              <div>
                <div className="flex items-center gap-2"><span className="font-medium">{addOn.label}</span><span className="glass-badge">{installed ? addOn.billing : 'Not installed'}</span></div>
                <p className="mt-1 text-xs opacity-60">{addOn.description}</p>
              </div>
              <input type="checkbox" className="h-4 w-4" aria-label={`Enable ${addOn.label}`}
                checked={enabled} disabled={featuresLoading || user?.role !== 'owner' || !installed || featureBusy === addOn.key}
                onChange={event => toggleFeature(addOn.key, event.target.checked)} />
            </div>;
          })}
        </div>
        {user?.role !== 'owner' && <p className="mt-2 text-xs opacity-60">Only a workspace owner can change add-ons.</p>}
        {user?.role === 'owner' && features.governance && <button className="glass-btn-ghost mt-3" onClick={downloadAudit}>Download audit CSV</button>}
      </div>

      <div className="glass-panel p-6">
        <h3 className="text-lg font-semibold mb-4">Recent payments</h3>
        {history.length === 0 ? (
          <p className="opacity-60 text-sm">No payments yet.</p>
        ) : (
          <table className="w-full text-sm">
            <thead className="opacity-60 text-left">
              <tr>
                <th className="py-2">Date</th>
                <th>Amount</th>
                <th>Units</th>
                <th>Status</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {history.map(p => (
                <tr key={p.id} className="border-t border-white/5">
                  <td className="py-2">{fmtDate(p.created_at)}</td>
                  <td>{fmtINR(p.amount_paise)}</td>
                  <td>{p.quota_units} × 5</td>
                  <td>
                    <span className={
                      p.status === 'paid' ? 'text-emerald-300' :
                      p.status === 'failed' ? 'text-red-300' :
                      'opacity-70'
                    }>
                      {p.status === 'paid' ? 'Captured' :
                       p.status === 'failed' ? 'Failed' : 'Pending'}
                    </span>
                  </td>
                  <td className="text-right">
                    {p.razorpay_payment_id && (
                      <span className="opacity-50 text-xs">
                        {p.razorpay_payment_id.slice(0, 14)}…
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

export default BillingPage;
