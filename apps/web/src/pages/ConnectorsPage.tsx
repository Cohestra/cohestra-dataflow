import { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router';
import { BadgeHelp, Blocks, Cloud, Database, Globe2, Plug, Radio, Server, Sheet, Snowflake, type LucideIcon } from 'lucide-react';
import { api } from '../api';
import { useFeatures } from '../context/FeatureContext';
import { ApiError } from '../components/ApiError';

// ─── Types ────────────────────────────────────────────────────────────────────

type Provider = 'google' | 'microsoft' | 'zendesk';
type CredentialProvider = 'postgres' | 'mysql' | 'mongodb' | 'clickhouse' | 's3' | 'sftp' | 'snowflake' | 'iceberg' | 'kafka' | 'http';

interface Connection {
  id: string;
  kind?: 'oauth' | 'credential';
  provider: string;
  name?: string;
  email?: string;
  subdomain?: string;   // zendesk only
  host?: string;
  brokers?: string;
  baseUrl?: string;
  cdc?: { enabled?: boolean; resources?: string[]; state?: string; error?: string };
  connected_at?: string;
}

// ponytail: local startOAuth/startZendeskOAuth removed — use api.startConnectorOAuth / api.startZendeskOAuth

// ─── Helpers ──────────────────────────────────────────────────────────────────

function timeAgo(iso?: string): string {
  if (!iso) return '';
  const diff = Date.now() - new Date(iso).getTime();
  const mins = Math.floor(diff / 60000);
  if (mins < 1) return 'just now';
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  return `${days}d ago`;
}

// ─── Provider icon avatars ────────────────────────────────────────────────────

function ProviderIcon({ provider }: { provider: Provider }) {
  const cfg: Record<Provider, { icon: typeof Sheet; bg: string }> = {
    google:    { icon: Sheet, bg: 'from-emerald-500/80 to-green-600/80' },
    microsoft: { icon: Blocks, bg: 'from-blue-500/80 to-cyan-500/80' },
    zendesk:   { icon: BadgeHelp, bg: 'from-slate-700 to-emerald-700' },
  };
  const { icon: Icon, bg } = cfg[provider];
  return (
    <div className={`w-10 h-10 rounded-xl bg-linear-to-br ${bg} flex items-center justify-center text-white shadow-lg shrink-0`}>
      <Icon size={18} strokeWidth={1.8} />
    </div>
  );
}

const CREDENTIAL_CATALOG: Array<{ key: CredentialProvider; label: string; subtitle: string; icon: LucideIcon; color: string; feature?: 'realtime' | 'advancedConnectors' }> = [
  { key: 'postgres', label: 'PostgreSQL', subtitle: 'Database · CDC', icon: Database, color: 'from-blue-600 to-sky-500' },
  { key: 'mysql', label: 'MySQL', subtitle: 'Database · CDC', icon: Database, color: 'from-sky-500 to-amber-500' },
  { key: 'mongodb', label: 'MongoDB', subtitle: 'Document DB · CDC', icon: Database, color: 'from-emerald-600 to-green-500' },
  { key: 's3', label: 'Amazon S3', subtitle: 'Object storage', icon: Cloud, color: 'from-amber-500 to-orange-600' },
  { key: 'kafka', label: 'Kafka', subtitle: 'Event streaming', icon: Radio, color: 'from-slate-700 to-violet-600', feature: 'realtime' },
  { key: 'snowflake', label: 'Snowflake', subtitle: 'Cloud warehouse', icon: Snowflake, color: 'from-cyan-500 to-blue-600', feature: 'advancedConnectors' },
  { key: 'sftp', label: 'SFTP', subtitle: 'Secure file transfer', icon: Server, color: 'from-slate-600 to-slate-800', feature: 'advancedConnectors' },
  { key: 'iceberg', label: 'Apache Iceberg', subtitle: 'Lakehouse tables', icon: Cloud, color: 'from-indigo-500 to-cyan-500', feature: 'advancedConnectors' },
  { key: 'clickhouse', label: 'ClickHouse', subtitle: 'Analytics database', icon: Server, color: 'from-yellow-400 to-amber-600' },
  { key: 'http', label: 'HTTP API', subtitle: 'REST endpoint', icon: Globe2, color: 'from-violet-500 to-fuchsia-600' },
];

// ─── Zendesk connect modal ────────────────────────────────────────────────────

function ZendeskModal({ onClose, onConnect }: {
  onClose: () => void;
  onConnect: (subdomain: string) => Promise<void>;
}) {
  const [subdomain, setSubdomain] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose(); };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const slug = subdomain.trim().replace(/\.zendesk\.com\/?$/, '');
    if (!slug) return;
    setBusy(true);
    setErr(null);
    try {
      await onConnect(slug);
    } catch (ex: any) {
      setErr(ex.message ?? 'Failed to initiate OAuth');
      setBusy(false);
    }
  };

  return (
    <div className="glass-modal-backdrop" onClick={onClose}>
      <div className="glass-modal" role="dialog" aria-modal="true" aria-labelledby="zendesk-modal-title" onClick={e => e.stopPropagation()}>
        <div className="flex items-center justify-between mb-5">
          <h2 id="zendesk-modal-title" className="text-lg font-semibold">Connect Zendesk subdomain</h2>
          <button
            className="glass-btn-ghost w-8 h-8 flex items-center justify-center text-lg leading-none"
            onClick={onClose}
            aria-label="Close"
          >
            ×
          </button>
        </div>

        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="glass-label">Subdomain</label>
            <div className="flex items-center gap-2">
              <input
                className="glass-input flex-1"
                placeholder="acme"
                value={subdomain}
                onChange={e => setSubdomain(e.target.value)}
                autoFocus
                required
              />
              <span className="text-sm text-gray-400 dark:text-white/40 whitespace-nowrap">.zendesk.com</span>
            </div>
          </div>

          {err && (
            <p className="text-xs text-danger/90 bg-danger/10 border border-danger/30 rounded-lg px-3 py-2">
              {err}
            </p>
          )}

          <div className="flex justify-end gap-2 pt-1">
            <button type="button" className="glass-btn-ghost" onClick={onClose}>
              Cancel
            </button>
            <button type="submit" className="glass-btn-primary" disabled={busy || !subdomain.trim()}>
              {busy ? 'Redirecting…' : 'Connect →'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

// ─── Credential instance modal (A3 — non-OAuth creds) ──────────────────────────

function CredentialModal({ onClose, onSaved, realtime, advanced, initialProvider = 'postgres' }: { onClose: () => void; onSaved: () => void; realtime: boolean; advanced: boolean; initialProvider?: CredentialProvider }) {
  const [provider, setProvider] = useState<CredentialProvider>(initialProvider);
  const [name, setName] = useState('');
  const [f, setF] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const set = (k: string) => (e: React.ChangeEvent<HTMLInputElement>) => setF(s => ({ ...s, [k]: e.target.value }));

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose(); };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true); setErr(null);
    try {
      let body: Record<string, unknown>;
      if (provider === 'postgres' || provider === 'mysql') {
        body = { provider, name, config: { host: f.host, port: f.port ? +f.port : provider === 'postgres' ? 5432 : 3306, database: f.database, user: f.user, sslMode: f.sslMode ?? 'disable' }, secret: { password: f.password } };
      } else if (provider === 'mongodb') {
        const srv = f.srv === 'true';
        body = { provider, name, config: { host: f.host, ...(srv ? { srv: true } : { port: f.port ? +f.port : 27017 }), database: f.database, user: f.user, authSource: f.authSource || 'admin', tls: srv || f.tls === 'true' }, secret: { password: f.password } };
      } else if (provider === 'clickhouse') {
        body = { provider, name, config: { url: f.url, database: f.database || 'default', username: f.username || 'default' }, secret: { password: f.password } };
      } else if (provider === 's3') {
        body = { provider, name, config: { region: f.region || 'us-east-1', endpoint: f.endpoint, forcePathStyle: f.forcePathStyle === 'true' }, secret: { accessKeyId: f.accessKeyId, secretAccessKey: f.secretAccessKey } };
      } else if (provider === 'sftp') {
        body = { provider, name, config: { host: f.host, port: f.port ? +f.port : 22, user: f.user, hostKey: f.hostKey, testPath: f.testPath }, secret: { password: f.password, privateKey: f.privateKey } };
      } else if (provider === 'snowflake') {
        body = { provider, name, config: { account: f.account, user: f.user, warehouse: f.warehouse, database: f.database, schema: f.schema }, secret: { password: f.password } };
      } else if (provider === 'iceberg') {
        body = { provider, name, config: { url: f.url, warehouse: f.warehouse, region: f.region, endpoint: f.endpoint, forcePathStyle: f.forcePathStyle === 'true' }, secret: { token: f.token, accessKeyId: f.accessKeyId, secretAccessKey: f.secretAccessKey } };
      } else if (provider === 'kafka') {
        body = { provider, name, config: { brokers: f.brokers, clientId: f.clientId || 'dataflow', tls: f.tls === 'true', saslMechanism: f.saslMechanism || 'none' }, secret: { username: f.username, password: f.password } };
      } else {
        body = { provider, name, config: { baseUrl: f.baseUrl }, secret: { apiKey: f.apiKey, hmacSecret: f.hmacSecret } };
      }
      await api.createConnector(body as { provider: string; name: string; config?: Record<string, any>; secret?: Record<string, any> });
      onSaved(); onClose();
    } catch (ex: any) { setErr(ex.message ?? 'Failed to save'); setBusy(false); }
  };

  return (
    <div className="glass-modal-backdrop" onClick={onClose}>
      <div className="glass-modal" role="dialog" aria-modal="true" aria-labelledby="credential-modal-title" onClick={e => e.stopPropagation()}>
        <div className="flex items-center justify-between mb-5">
          <h2 id="credential-modal-title" className="text-lg font-semibold">Add credential</h2>
          <button className="glass-btn-ghost w-8 h-8 flex items-center justify-center text-lg leading-none" onClick={onClose} aria-label="Close">×</button>
        </div>
        <form onSubmit={submit} className="space-y-3">
          <label className="glass-label">Type
            <select className="glass-select" value={provider} onChange={e => setProvider(e.target.value as any)}>
              <option value="postgres">Postgres</option>
              <option value="mysql">MySQL</option>
              <option value="mongodb">MongoDB</option>
              <option value="clickhouse">ClickHouse</option>
              <option value="s3">Amazon S3</option>
              {advanced && <option value="sftp">SFTP</option>}
              {advanced && <option value="snowflake">Snowflake</option>}
              {advanced && <option value="iceberg">Apache Iceberg</option>}
              {realtime && <option value="kafka">Kafka / Redpanda</option>}
              <option value="http">HTTP API</option>
            </select>
          </label>
          <label className="glass-label">Name
            <input className="glass-input" value={name} onChange={e => setName(e.target.value)} placeholder="Connector name" required />
          </label>
          {provider === 'postgres' || provider === 'mysql' ? (
            <>
              <input className="glass-input" placeholder="host" aria-label="Host" value={f.host ?? ''} onChange={set('host')} />
              <input className="glass-input" placeholder={`port (${provider === 'postgres' ? 5432 : 3306})`} aria-label="Port" value={f.port ?? ''} onChange={set('port')} />
              <input className="glass-input" placeholder="database" aria-label="Database" value={f.database ?? ''} onChange={set('database')} />
              <input className="glass-input" placeholder="user" aria-label="User" value={f.user ?? ''} onChange={set('user')} />
              <input className="glass-input" type="password" placeholder="password" aria-label="Password" value={f.password ?? ''} onChange={set('password')} />
              <select className="glass-select" aria-label="SSL mode" value={f.sslMode ?? 'disable'} onChange={e => setF(s => ({ ...s, sslMode: e.target.value }))}>
                <option value="disable">SSL disabled</option>
                <option value="require">SSL required</option>
                <option value="verify-full">SSL verify certificate</option>
              </select>
            </>
          ) : provider === 'mongodb' ? (
            <>
              <input className="glass-input" placeholder="host" aria-label="Host" value={f.host ?? ''} onChange={set('host')} required />
              {f.srv !== 'true' && <input className="glass-input" placeholder="port (27017)" aria-label="Port" value={f.port ?? ''} onChange={set('port')} />}
              <input className="glass-input" placeholder="database" aria-label="Database" value={f.database ?? ''} onChange={set('database')} required />
              <input className="glass-input" placeholder="user (optional)" aria-label="User" value={f.user ?? ''} onChange={set('user')} />
              <input className="glass-input" type="password" placeholder="password" aria-label="Password" value={f.password ?? ''} onChange={set('password')} />
              <input className="glass-input" placeholder="auth source (admin)" aria-label="Auth source" value={f.authSource ?? ''} onChange={set('authSource')} />
              <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={f.srv === 'true'} onChange={e => setF(s => ({ ...s, srv: String(e.target.checked) }))} /> SRV (Atlas / DNS seedlist — no port)</label>
              {f.srv !== 'true' && <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={f.tls === 'true'} onChange={e => setF(s => ({ ...s, tls: String(e.target.checked) }))} /> TLS</label>}
            </>
          ) : provider === 'clickhouse' ? (
            <>
              <input className="glass-input" type="url" placeholder="https://clickhouse.example.com:8443" aria-label="ClickHouse URL" value={f.url ?? ''} onChange={set('url')} required />
              <input className="glass-input" placeholder="database (default)" aria-label="Database" value={f.database ?? ''} onChange={set('database')} />
              <input className="glass-input" placeholder="username (default)" aria-label="Username" value={f.username ?? ''} onChange={set('username')} />
              <input className="glass-input" type="password" placeholder="password" aria-label="Password" value={f.password ?? ''} onChange={set('password')} />
            </>
          ) : provider === 's3' ? (
            <>
              <input className="glass-input" placeholder="region (us-east-1)" aria-label="Region" value={f.region ?? ''} onChange={set('region')} />
              <input className="glass-input" placeholder="endpoint (optional)" aria-label="Endpoint" value={f.endpoint ?? ''} onChange={set('endpoint')} />
              <input className="glass-input" placeholder="access key ID" aria-label="Access key ID" value={f.accessKeyId ?? ''} onChange={set('accessKeyId')} required />
              <input className="glass-input" type="password" placeholder="secret access key" aria-label="Secret access key" value={f.secretAccessKey ?? ''} onChange={set('secretAccessKey')} required />
              <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={f.forcePathStyle === 'true'} onChange={e => setF(s => ({ ...s, forcePathStyle: String(e.target.checked) }))} /> Path-style URLs (MinIO/S3-compatible)</label>
            </>
          ) : provider === 'kafka' ? (
            <>
              <input className="glass-input" placeholder="brokers (kafka-1:9092,kafka-2:9092)" aria-label="Brokers" value={f.brokers ?? ''} onChange={set('brokers')} required />
              <input className="glass-input" placeholder="client ID (dataflow)" aria-label="Client ID" value={f.clientId ?? ''} onChange={set('clientId')} />
              <select className="glass-select" aria-label="SASL mechanism" value={f.saslMechanism ?? 'none'} onChange={e => setF(s => ({ ...s, saslMechanism: e.target.value }))}>
                <option value="none">No SASL</option><option value="plain">SASL/PLAIN</option>
                <option value="scram-sha-256">SCRAM-SHA-256</option><option value="scram-sha-512">SCRAM-SHA-512</option>
              </select>
              {f.saslMechanism && f.saslMechanism !== 'none' && <>
                <input className="glass-input" placeholder="SASL username" aria-label="SASL username" value={f.username ?? ''} onChange={set('username')} required />
                <input className="glass-input" type="password" placeholder="SASL password" aria-label="SASL password" value={f.password ?? ''} onChange={set('password')} required />
              </>}
              <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={f.tls === 'true'} onChange={e => setF(s => ({ ...s, tls: String(e.target.checked) }))} /> TLS</label>
            </>
          ) : provider === 'sftp' ? (
            <>
              <input className="glass-input" placeholder="host" aria-label="Host" value={f.host ?? ''} onChange={set('host')} required />
              <input className="glass-input" placeholder="port (22)" aria-label="Port" value={f.port ?? ''} onChange={set('port')} />
              <input className="glass-input" placeholder="username" aria-label="Username" value={f.user ?? ''} onChange={set('user')} required />
              <textarea className="glass-input" placeholder="server host public key" aria-label="Server host public key" value={f.hostKey ?? ''} onChange={e => setF(s => ({ ...s, hostKey: e.target.value }))} required />
              <input className="glass-input" type="password" placeholder="password" aria-label="Password" value={f.password ?? ''} onChange={set('password')} />
              <textarea className="glass-input" placeholder="private key (optional)" aria-label="Private key" value={f.privateKey ?? ''} onChange={e => setF(s => ({ ...s, privateKey: e.target.value }))} />
            </>
          ) : provider === 'snowflake' ? (
            <>
              <input className="glass-input" placeholder="account identifier" aria-label="Account identifier" value={f.account ?? ''} onChange={set('account')} required />
              <input className="glass-input" placeholder="username" aria-label="Username" value={f.user ?? ''} onChange={set('user')} required />
              <input className="glass-input" type="password" placeholder="password" aria-label="Password" value={f.password ?? ''} onChange={set('password')} required />
              <input className="glass-input" placeholder="warehouse" aria-label="Warehouse" value={f.warehouse ?? ''} onChange={set('warehouse')} required />
              <input className="glass-input" placeholder="database" aria-label="Database" value={f.database ?? ''} onChange={set('database')} required />
              <input className="glass-input" placeholder="schema (optional)" aria-label="Schema" value={f.schema ?? ''} onChange={set('schema')} />
            </>
          ) : provider === 'iceberg' ? (
            <>
              <input className="glass-input" type="url" placeholder="REST catalog URL" aria-label="REST catalog URL" value={f.url ?? ''} onChange={set('url')} required />
              <input className="glass-input" placeholder="warehouse (optional)" aria-label="Warehouse" value={f.warehouse ?? ''} onChange={set('warehouse')} />
              <input className="glass-input" type="password" placeholder="catalog bearer token (optional)" aria-label="Catalog bearer token" value={f.token ?? ''} onChange={set('token')} />
              <input className="glass-input" placeholder="S3 access key (optional)" aria-label="S3 access key" value={f.accessKeyId ?? ''} onChange={set('accessKeyId')} />
              <input className="glass-input" type="password" placeholder="S3 secret key (optional)" aria-label="S3 secret key" value={f.secretAccessKey ?? ''} onChange={set('secretAccessKey')} />
              <input className="glass-input" placeholder="S3 region (us-east-1)" aria-label="S3 region" value={f.region ?? ''} onChange={set('region')} />
            </>
          ) : (
            <>
              <input className="glass-input" placeholder="base URL" aria-label="Base URL" value={f.baseUrl ?? ''} onChange={set('baseUrl')} />
              <input className="glass-input" type="password" placeholder="Bearer API key (optional)" aria-label="Bearer API key" value={f.apiKey ?? ''} onChange={set('apiKey')} />
              <input className="glass-input" type="password" placeholder="Webhook HMAC secret (optional)" aria-label="Webhook HMAC secret" value={f.hmacSecret ?? ''} onChange={set('hmacSecret')} />
            </>
          )}
          {err && <p className="text-xs text-danger/90 bg-danger/10 border border-danger/30 rounded-lg px-3 py-2">{err}</p>}
          <div className="flex justify-end gap-2 pt-1">
            <button type="button" className="glass-btn-ghost" onClick={onClose}>Cancel</button>
            <button type="submit" className="glass-btn-primary" disabled={busy || !name.trim()}>{busy ? 'Saving…' : 'Save'}</button>
          </div>
        </form>
      </div>
    </div>
  );
}

// ─── Individual connector card ────────────────────────────────────────────────

interface CardProps {
  provider: Provider;
  label: string;
  connections: Connection[];
  onDisconnect: (id: string) => Promise<void>;
  onConnect: () => void;
}

function ConnectorCard({ provider, label, connections, onDisconnect, onConnect }: CardProps) {
  const [disconnecting, setDisconnecting] = useState<string | null>(null);

  const handleDisconnect = async (id: string) => {
    setDisconnecting(id);
    try {
      await onDisconnect(id);
    } finally {
      setDisconnecting(null);
    }
  };

  const hasConnections = connections.length > 0;

  return (
    <div className="glass-card flex min-h-[158px] flex-col gap-3 p-4">
      {/* Header */}
      <div className="flex items-center gap-3">
        <ProviderIcon provider={provider} />
        <div className="flex-1 min-w-0">
          <h3 className="text-sm font-semibold">{label}</h3>
          <p className="text-xs text-gray-400 mt-0.5">
            {provider === 'zendesk' ? 'Per-subdomain OAuth' : 'OAuth 2.0'}
          </p>
        </div>
        {hasConnections ? (
          <span className="glass-badge-success px-2 py-0.5 text-[10px]">
            <span className="inline-block w-1.5 h-1.5 rounded-full bg-success mr-1" />
            Active
          </span>
        ) : (
          <span className="glass-badge px-2 py-0.5 text-[10px]">
            <span className="inline-block w-1.5 h-1.5 rounded-full bg-gray-300 dark:bg-white/30 mr-1" />
            Not connected
          </span>
        )}
      </div>

      {/* Connected accounts */}
      {hasConnections && (
        <ul className="divide-y divide-gray-100 text-xs dark:divide-white/5">
          {connections.map(conn => (
            <li key={conn.id} className="flex items-center justify-between py-1.5">
              <div className="min-w-0">
                <p className="truncate text-gray-900 dark:text-white/90">
                  {conn.email ?? conn.subdomain ?? conn.id}
                  {conn.subdomain && (
                    <span className="text-gray-400 dark:text-white/40 ml-1">.zendesk.com</span>
                  )}
                </p>
                {conn.connected_at && (
                  <p className="text-xs text-gray-400 dark:text-white/40">Connected {timeAgo(conn.connected_at)}</p>
                )}
              </div>
              <button
                className="glass-btn-danger ml-3 px-2 py-1 text-[10px] shrink-0"
                disabled={disconnecting === conn.id}
                onClick={() => handleDisconnect(conn.id)}
                title="Disconnect"
              >
                {disconnecting === conn.id ? 'Disconnecting…' : 'Disconnect'}
              </button>
            </li>
          ))}
        </ul>
      )}

      {/* Action row */}
      {!hasConnections && (
        <div className="mt-auto pt-1">
          <button className="glass-btn-primary w-full py-1.5 text-xs" onClick={onConnect}>
            Connect {label} →
          </button>
        </div>
      )}

      {/* Zendesk extra: add another subdomain */}
      {provider === 'zendesk' && hasConnections && (
        <button
          className="mt-auto flex items-center gap-1 self-start text-xs font-medium text-brand-500 hover:text-brand-400"
          onClick={onConnect}
        >
          <span className="text-brand-400">+</span>
          Connect another Zendesk subdomain
        </button>
      )}
    </div>
  );
}

// ─── Main page ────────────────────────────────────────────────────────────────

export function ConnectorsPage() {
  const { features } = useFeatures();
  const navigate = useNavigate();
  const [connections, setConnections] = useState<Connection[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [zendeskModalOpen, setZendeskModalOpen] = useState(false);
  const [credentialProvider, setCredentialProvider] = useState<CredentialProvider | null>(null);
  const [testResult, setTestResult] = useState<Record<string, string>>({});
  const [cdcResources, setCdcResources] = useState<Record<string, string>>({});
  const [cdcStatus, setCdcStatus] = useState<Record<string, any>>({});
  const [cdcBusy, setCdcBusy] = useState<string | null>(null);
  const [actionBusy, setActionBusy] = useState<string | null>(null);

  const credentials = connections.filter(c => c.kind === 'credential');
  const testConn = async (id: string) => {
    setTestResult(r => ({ ...r, [id]: 'testing…' }));
    try {
      const res = await api.testConnector(id);
      setTestResult(r => ({ ...r, [id]: `${res.ok ? 'Passed' : 'Failed'}: ${res.message}` }));
    } catch (e: any) { setTestResult(r => ({ ...r, [id]: `Failed: ${e.message ?? 'failed'}` })); }
  };
  const removeCred = async (id: string) => { await api.deleteConnector(id); refresh(); };
  const refreshCdc = async (id: string) => {
    setCdcBusy(id);
    try { const status = await api.getConnectorCdc(id); setCdcStatus(s => ({ ...s, [id]: status })); }
    catch (e: any) { setCdcStatus(s => ({ ...s, [id]: { error: e.message ?? 'status failed' } })); }
    finally { setCdcBusy(null); }
  };
  const saveCdc = async (id: string) => {
    const resources = (cdcResources[id] ?? '').split(',').map(s => s.trim()).filter(Boolean);
    setCdcBusy(id);
    try { const status = await api.saveConnectorCdc(id, resources); setCdcStatus(s => ({ ...s, [id]: status })); }
    catch (e: any) { setCdcStatus(s => ({ ...s, [id]: { error: e.message ?? 'CDC setup failed' } })); }
    finally { setCdcBusy(null); }
  };
  const disableCdc = async (id: string) => {
    setCdcBusy(id);
    try { await api.deleteConnectorCdc(id); setCdcStatus(s => ({ ...s, [id]: { enabled: false } })); }
    catch (e: any) { setCdcStatus(s => ({ ...s, [id]: { error: e.message ?? 'CDC disable failed' } })); }
    finally { setCdcBusy(null); }
  };

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const list = await api.listConnectors();
      setConnections(list);
      setCdcResources(current => Object.fromEntries(list.map((c: Connection) => [c.id, current[c.id] ?? c.cdc?.resources?.join(', ') ?? ''])));
      setCdcStatus(current => Object.fromEntries(list.map((c: Connection) => [c.id, current[c.id] ?? c.cdc ?? { enabled: false }])));
    } catch (e: any) {
      setError(e.message ?? 'Failed to load connectors');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { refresh(); }, [refresh]);

  const handleDisconnect = async (id: string) => {
    await api.deleteConnector(id);
    setConnections(prev => prev.filter(c => c.id !== id));
  };

  const handleConnectGoogle = async () => {
    setActionBusy('google');
    try { const res = await api.startConnectorOAuth('google'); window.location.href = res.url; }
    catch (e: any) { setError(e.message); setActionBusy(null); }
  };

  const handleConnectMicrosoft = async () => {
    setActionBusy('microsoft');
    try { const res = await api.startConnectorOAuth('microsoft'); window.location.href = res.url; }
    catch (e: any) { setError(e.message); setActionBusy(null); }
  };

  const handleConnectZendesk = async (subdomain: string) => {
    const res = await api.startZendeskOAuth(subdomain);
    window.location.href = res.url;
  };

  const byProvider = (p: Provider) => connections.filter(c => c.provider === p);

  const PROVIDERS: { key: Provider; label: string; onConnect: () => void }[] = [
    { key: 'google',    label: 'Google',    onConnect: handleConnectGoogle },
    { key: 'microsoft', label: 'Microsoft', onConnect: handleConnectMicrosoft },
    { key: 'zendesk',   label: 'Zendesk',   onConnect: () => setZendeskModalOpen(true) },
  ];

  return (
    <div className="mx-auto max-w-6xl space-y-5 px-6 py-7">
      {/* Page header */}
      <div>
        <div className="flex items-center justify-between">
          <div>
            <h2 className="page-heading">Connect accounts</h2>
            <p className="page-subtitle mt-1">Choose a connector. Credentials and OAuth tokens are encrypted per tenant.</p>
          </div>
        </div>

        {error && <div className="mt-4"><ApiError message={error} onRetry={refresh} /></div>}
      </div>

      {/* Connector cards */}
      {loading && connections.length === 0 ? (
        <div className="glass-panel p-10 text-center text-gray-400 dark:text-white/40 text-sm">
          Loading connectors…
        </div>
      ) : (
        <section>
          <h3 className="section-heading mb-2.5">Accounts</h3>
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {PROVIDERS.map(({ key, label, onConnect }) => (
            <ConnectorCard
              key={key}
              provider={key}
              label={label}
              connections={byProvider(key)}
              onDisconnect={handleDisconnect}
              onConnect={
                key === 'google' && actionBusy === 'google' ? () => {} :
                key === 'microsoft' && actionBusy === 'microsoft' ? () => {} :
                onConnect
              }
            />
          ))}
          </div>
        </section>
      )}

      <section>
        <h3 className="section-heading mb-2.5">Databases &amp; services</h3>
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          {CREDENTIAL_CATALOG.map(({ key, label, subtitle, icon: Icon, color, feature }) => {
            const count = credentials.filter(c => c.provider === key).length;
            const available = !feature || features[feature];
            return (
              <button key={key} type="button" aria-label={available ? `Configure ${label}` : `View plans for ${label}`}
                onClick={() => available ? setCredentialProvider(key) : navigate('/billing')}
                className="glass-card-interactive group flex min-h-[126px] flex-col p-4 text-left">
                <div className="flex items-start gap-3">
                  <span className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-linear-to-br ${color} text-white shadow-lg`}>
                    <Icon size={18} strokeWidth={1.8} />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="block text-sm font-semibold text-gray-900 dark:text-white/90">{label}</span>
                    <span className="mt-0.5 block text-[11px] text-gray-400 dark:text-white/40">{subtitle}</span>
                  </span>
                  {count > 0
                    ? <span className="glass-badge-success px-2 py-0.5 text-[10px]">{count} active</span>
                    : !available && <span className="glass-badge px-2 py-0.5 text-[10px]">Plan</span>}
                </div>
                <span className="mt-auto pt-4 text-xs font-medium text-brand-500 group-hover:text-brand-400">{available ? 'Configure →' : 'View plans →'}</span>
              </button>
            );
          })}
        </div>
      </section>

      {/* Configured credential instances retain operational controls. */}
      {credentials.length > 0 && (
        <section className="glass-card p-4">
          <div className="mb-2 flex items-center gap-2">
            <Plug size={15} className="text-brand-300" />
            <h3 className="text-sm font-semibold">Configured connections</h3>
            <span className="glass-badge ml-auto px-2 py-0.5 text-[10px]">{credentials.length}</span>
          </div>
          <ul className="divide-y divide-gray-100 dark:divide-white/5">
            {credentials.map(c => {
              const supportsCdc = ['postgres', 'mysql', 'mongodb'].includes(c.provider);
              const status = cdcStatus[c.id] ?? c.cdc ?? {};
              const prerequisite = c.provider === 'postgres' ? 'Requires logical replication and pgoutput.'
                : c.provider === 'mysql' ? 'Requires ROW binlog with a replication user.'
                : 'Requires a replica set with change streams enabled.';
              return (
                <li key={c.id} className="py-3 text-sm">
                  <div className="flex items-center justify-between">
                    <div className="min-w-0">
                      <p className="truncate text-gray-900 dark:text-white/90">{c.name ?? c.id} <span className="text-gray-400 dark:text-white/40">· {c.provider}</span></p>
                      {testResult[c.id] && <p className="text-xs text-gray-500 dark:text-white/50">{testResult[c.id]}</p>}
                    </div>
                    <div className="flex items-center gap-2">
                      <button className="glass-btn-ghost px-2.5 py-1 text-xs" onClick={() => testConn(c.id)}>Test</button>
                      <button className="glass-btn-danger px-2.5 py-1 text-xs" onClick={() => removeCred(c.id)} aria-label={`Delete ${c.name ?? c.provider} credential`}>✕</button>
                    </div>
                  </div>
                  {supportsCdc && (features.realtime || status.enabled) && (
                    <div className="mt-2 rounded-lg border border-gray-200 p-2 dark:border-white/10">
                      <label className="glass-label">CDC tables / collections
                        <input className="glass-input" value={cdcResources[c.id] ?? ''}
                          onChange={e => setCdcResources(s => ({ ...s, [c.id]: e.target.value }))}
                          placeholder={c.provider === 'mongodb' ? 'database.collection' : 'schema.table'} />
                      </label>
                      <p className="mb-2 text-[11px] text-gray-500 dark:text-white/45">Comma-separated. {prerequisite}</p>
                      <div className="flex flex-wrap items-center gap-2">
                        <button className="glass-btn-primary px-2.5 py-1 text-xs" disabled={cdcBusy === c.id || !cdcResources[c.id]?.trim()} onClick={() => saveCdc(c.id)}>
                          {status.enabled ? 'Update CDC' : 'Enable CDC'}
                        </button>
                        <button className="glass-btn-ghost px-2.5 py-1 text-xs" disabled={cdcBusy === c.id} onClick={() => refreshCdc(c.id)}>Refresh status</button>
                        {status.enabled && <button className="glass-btn-danger px-2.5 py-1 text-xs" disabled={cdcBusy === c.id} onClick={() => disableCdc(c.id)}>Disable</button>}
                        <span className="text-xs text-gray-500 dark:text-white/50" role="status">
                          {cdcBusy === c.id ? 'Working…' : status.error ? `Error: ${status.error}` : status.enabled ? status.state ?? 'Enabled' : 'Disabled'}
                        </span>
                      </div>
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
        </section>
      )}

      {/* Zendesk modal */}
      {zendeskModalOpen && (
        <ZendeskModal
          onClose={() => setZendeskModalOpen(false)}
          onConnect={handleConnectZendesk}
        />
      )}

      {/* Credential modal */}
      {credentialProvider && (
        <CredentialModal initialProvider={credentialProvider} realtime={features.realtime} advanced={features.advancedConnectors}
          onClose={() => setCredentialProvider(null)} onSaved={refresh} />
      )}
    </div>
  );
}

export default ConnectorsPage;
