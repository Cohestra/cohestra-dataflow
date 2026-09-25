// Auth-aware fetch wrapper. Reads the access token from AuthContext
// (set via setAccessToken) and includes credentials so the refresh
// cookie travels on /api/auth/* calls.
import type { Dataset, QuerySpec, TimeRange } from './components/analytics/types';

let accessToken: string | null = null;
let onUnauthorized: () => Promise<boolean> = async () => false;

export function setAccessToken(t: string | null) { accessToken = t; }
export function setUnauthorizedHandler(fn: () => Promise<boolean>) { onUnauthorized = fn; }

async function request(path: string, init: RequestInit = {}, retry = true): Promise<Response> {
  const headers = new Headers(init.headers);
  if (accessToken) headers.set('Authorization', `Bearer ${accessToken}`);
  if (init.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json');
  const res = await fetch(path, { ...init, headers, credentials: 'include' });
  if (res.status === 401 && retry) {
    const recovered = await onUnauthorized();
    if (recovered) return request(path, init, false);
  }
  return res;
}

const j = async (r: Response) => {
  if (!r.ok) {
    const text = await r.text().catch(() => '');
    throw new Error(`${r.status} ${text}`);
  }
  return r.json();
};

type PipelineListParams = {
  limit?: string;
  cursor?: string;
  search?: string;
  stage?: string;
  trigger?: string;
  key?: string;
  view?: 'current';
};

type PipelineListPage = { rows: any[]; nextCursor: string | null };

export type AiConversationMessage = { role: 'user' | 'assistant'; content: string };

type AiPipelineContext = {
  mermaid?: string;
  messages?: AiConversationMessage[];
};

const listPipelines = (params?: PipelineListParams) => {
  const clean = Object.entries(params ?? {}).filter(([, v]) => v) as [string, string][];
  const qs = clean.length ? '?' + new URLSearchParams(Object.fromEntries(clean)) : '';
  return request(`/api/pipelines${qs}`).then(j) as Promise<PipelineListPage>;
};

async function listAllPipelines(
  params: Omit<PipelineListParams, 'limit' | 'cursor'> = {},
  pageSize = 200,
): Promise<any[]> {
  const limit = Number.isFinite(pageSize) ? Math.min(200, Math.max(1, Math.floor(pageSize))) : 200;
  const rows: any[] = [];
  const seenCursors = new Set<string>();
  let cursor: string | undefined;

  while (true) {
    const page = await listPipelines({ ...params, limit: String(limit), cursor });
    rows.push(...page.rows);
    if (!page.nextCursor) return rows;
    if (seenCursors.has(page.nextCursor)) throw new Error('Pipeline pagination returned a repeated cursor');
    seenCursors.add(page.nextCursor);
    cursor = page.nextCursor;
  }
}

export const api = {
  // Pipelines / executions
  savePipeline: (def: any) => request('/api/pipelines', { method: 'POST', body: JSON.stringify(def) }).then(j),
  activate:     (rowId: string) => request(`/api/pipelines/${rowId}/activate`, { method: 'POST' }).then(j),
  promote:      (rowId: string, allowBreakingContract = false) => request(`/api/pipelines/${rowId}/promote`, {
    method: 'POST', body: JSON.stringify({ allowBreakingContract }),
  }).then(j),
  setStage:     (rowId: string, to: 'testing' | 'production', allowBreakingContract = false) =>
    request(`/api/pipelines/${rowId}/stage`, { method: 'POST', body: JSON.stringify({ to, allowBreakingContract }) }).then(j),
  run:          (rowId: string) =>
    request(`/api/pipelines/${rowId}/run`, { method: 'POST', body: JSON.stringify({}) }).then(j),
  listPipelines,
  listAllPipelines,
  getPipeline: (rowId: string) => request(`/api/pipelines/${rowId}`).then(j),
  planBackfill: (rowId: string, body: { from: string; to: string; partitionDays: number; maxConcurrency: number }) =>
    request(`/api/pipelines/${rowId}/backfills/plan`, { method: 'POST', body: JSON.stringify(body) }).then(j),
  startBackfill: (rowId: string, body: { from: string; to: string; partitionDays: number; maxConcurrency: number }) =>
    request(`/api/pipelines/${rowId}/backfills`, { method: 'POST', body: JSON.stringify(body) }).then(j),
  listBackfills: (rowId: string) => request(`/api/pipelines/${rowId}/backfills`).then(j),
  workspaceLineage: (environment?: string) =>
    request(`/api/pipelines/lineage/workspace${environment ? `?environment=${encodeURIComponent(environment)}` : ''}`).then(j),
  lineageChanges: (environment?: string, limit = 30) => {
    const params = new URLSearchParams({ limit: String(limit) });
    if (environment) params.set('environment', environment);
    return request(`/api/pipelines/lineage/changes?${params}`).then(j);
  },

  // Runtime lineage: windowed execution metrics (7-day max, defaults to the
  // last hour when from/to are omitted).
  runtimeLineageOverview: (params: Record<string, string> = {}) => {
    const clean = Object.entries(params).filter(([, value]) => value) as [string, string][];
    const qs = clean.length ? `?${new URLSearchParams(Object.fromEntries(clean))}` : '';
    return request(`/api/lineage/runtime/overview${qs}`).then(j);
  },
  runtimeLineageRuns: (params: Record<string, string> = {}) => {
    const clean = Object.entries(params).filter(([, value]) => value) as [string, string][];
    const qs = clean.length ? `?${new URLSearchParams(Object.fromEntries(clean))}` : '';
    return request(`/api/lineage/runtime/runs${qs}`).then(j);
  },
  runtimeLineageRun: (id: string) => request(`/api/lineage/runtime/runs/${encodeURIComponent(id)}`).then(j),

  // Connector catalog (coded + manifest-driven). Returns { catalog }.
  getConnectorCatalog: () => request('/api/connectors/catalog').then(j),

  // Connector instances (A3): list, create credential instance, test-connection.
  listConnectors: () => request('/api/connectors').then(j),
  createConnector: (body: { provider: string; name: string; config?: Record<string, any>; secret?: Record<string, any> }) =>
    request('/api/connectors', { method: 'POST', body: JSON.stringify(body) }).then(j),
  testConnector: (id: string) => request(`/api/connectors/${id}/test`, { method: 'POST' }).then(j),
  deleteConnector: (id: string) => request(`/api/connectors/${id}`, { method: 'DELETE' }).then(j),
  getConnectorCdc: (id: string) => request(`/api/connectors/${id}/cdc`).then(j),
  saveConnectorCdc: (id: string, resources: string[]) =>
    request(`/api/connectors/${id}/cdc`, { method: 'PUT', body: JSON.stringify({ resources }) }).then(j),
  deleteConnectorCdc: (id: string) => request(`/api/connectors/${id}/cdc`, { method: 'DELETE' }).then(j),

  // AI builder (Ollama). Conversation and graph context are sent on both paths.
  generatePipeline: (prompt: string, context: AiPipelineContext = {}) =>
    request('/api/ai/generate', {
      method: 'POST',
      body: JSON.stringify({ prompt, mermaid: context.mermaid ?? '', messages: context.messages ?? [] }),
    }).then(j),
  refinePipeline: (definition: any, prompt: string, context: AiPipelineContext = {}) =>
    request('/api/ai/refine', {
      method: 'POST',
      body: JSON.stringify({ definition, prompt, mermaid: context.mermaid ?? '', messages: context.messages ?? [] }),
    }).then(j),

  listExecutions: (params?: Record<string, string>) => {
    const clean = Object.entries(params ?? {}).filter(([, v]) => v) as [string, string][];
    const qs = clean.length ? '?' + new URLSearchParams(Object.fromEntries(clean)) : '';
    return request(`/api/executions${qs}`).then(j);
  },
  listExecutionsPage: (params?: Record<string, string>) => {
    const clean = Object.entries({ ...params, paged: '1' }).filter(([, v]) => v) as [string, string][];
    return request(`/api/executions?${new URLSearchParams(Object.fromEntries(clean))}`).then(j);
  },
  monitoringOverview: (days = 7) => request(`/api/executions/monitoring/overview?days=${days}`).then(j),
  listExecutionLogs: (params?: { query?: string; level?: string; limit?: number; days?: number }) => {
    const clean = Object.entries(params ?? {}).filter(([, value]) => value != null && value !== '');
    const qs = clean.length ? `?${new URLSearchParams(Object.fromEntries(clean.map(([key, value]) => [key, String(value)])))}` : '';
    return request(`/api/executions/logs${qs}`).then(j);
  },
  listAlerts: (status = 'active') => request(`/api/alerts?status=${encodeURIComponent(status)}`).then(j),
  acknowledgeAlert: (id: string) => request(`/api/alerts/${id}/acknowledge`, { method: 'POST' }).then(j),
  resolveAlert: (id: string) => request(`/api/alerts/${id}/resolve`, { method: 'POST' }).then(j),
  retryAlertNotification: (id: string) => request(`/api/alerts/${id}/retry-notification`, { method: 'POST' }).then(j),
  getExecution: (id: string) => request(`/api/executions/${id}`).then(j),
  getExecutionTrace: (id: string) => request(`/api/executions/${id}/trace`).then(j),
  executionStatus: (id: string) => request(`/api/executions/${id}/status`).then(j),
  signal: (id: string, action: string) => request(`/api/executions/${id}/${action}`, { method: 'POST' }).then(j),
  retryExecution: (id: string) => request(`/api/executions/${id}/retry`, { method: 'POST' }).then(j),

  // Auth (Google SSO — sign-in happens via the /api/auth/google browser redirect)
  logout: () => request('/api/auth/logout', { method: 'POST' }).then(j),
  refresh: () => request('/api/auth/refresh', { method: 'POST' }, false).then(j),
  me: () => request('/api/auth/me').then(j),
  inviteInfo: (token: string) =>
    request(`/api/auth/accept-invite?token=${encodeURIComponent(token)}`).then(j),

  // Team
  listMembers: () => request('/api/team/members').then(j),
  listInvitations: () => request('/api/team/invitations').then(j),
  invite: (body: { email: string; role: 'owner' | 'member' }) =>
    request('/api/team/invitations', { method: 'POST', body: JSON.stringify(body) }).then(j),
  revokeInvite: (email: string) =>
    request(`/api/team/invitations/${encodeURIComponent(email)}`, { method: 'DELETE' }).then(j),

  // Analytics
  getAnalyticsDatasets: (): Promise<Dataset[]> => request('/api/analytics/datasets').then(j),
  getAnalyticsSchema: (name: string) => request(`/api/analytics/datasets/${encodeURIComponent(name)}/schema`).then(j),
  getDatasetRows: (name: string, limit = 50, offset = 0): Promise<{ rows: any[]; total: number; limit: number; offset: number }> =>
    request(`/api/analytics/datasets/${encodeURIComponent(name)}/rows?limit=${limit}&offset=${offset}`).then(j),
  queryAnalytics: (body: { dataset: string; spec: QuerySpec; timeRange?: TimeRange }) =>
    request('/api/analytics/query', { method: 'POST', body: JSON.stringify({ dataset: body.dataset, ...body.spec, timeRange: body.timeRange }) }).then(j),
  listDashboards: () => request('/api/analytics/dashboards').then(j),
  createDashboard: (body: { name: string; definition: any }) =>
    request('/api/analytics/dashboards', { method: 'POST', body: JSON.stringify(body) }).then(j),
  updateDashboard: (id: string, body: { name?: string; definition?: any }) =>
    request(`/api/analytics/dashboards/${id}`, { method: 'PUT', body: JSON.stringify(body) }).then(j),
  deleteDashboard: (id: string) => request(`/api/analytics/dashboards/${id}`, { method: 'DELETE' }).then(j),
  shareDashboard: (id: string): Promise<{ shareToken: string; expiresAt: string; shareUrl: string }> =>
    request(`/api/analytics/dashboards/${id}/share`, { method: 'POST' }).then(j),
  listDashboardShares: (id: string): Promise<Array<{ share_token_hash: string; expires_at: string; created_at: string }>> =>
    request(`/api/analytics/dashboards/${id}/shares`).then(j),
  revokeDashboardShare: (id: string, hash: string) =>
    request(`/api/analytics/dashboards/${id}/shares/${encodeURIComponent(hash)}`, { method: 'DELETE' }).then(j),

  // Connectors OAuth start
  startConnectorOAuth: (provider: 'google' | 'microsoft') =>
    request(`/api/connectors/${provider}/auth`).then(j),
  startZendeskOAuth: (subdomain: string) =>
    request('/api/connectors/zendesk/auth', { method: 'POST', body: JSON.stringify({ subdomain }) }).then(j),

  // Auth — password login/register
  registerWithPassword: (body: { email: string; password: string; tenantName?: string; inviteToken?: string }) =>
    request('/api/auth/register', { method: 'POST', body: JSON.stringify(body) }).then(j),
  loginWithPassword: (body: { email: string; password: string }) =>
    request('/api/auth/login', { method: 'POST', body: JSON.stringify(body) }).then(j),

  // Billing (Phase 3)
  getUsage: () => request('/api/billing/usage').then(j),
  createOrder: (units: number) =>
    request('/api/billing/orders', { method: 'POST', body: JSON.stringify({ units }) }).then(j),
  getBillingHistory: () => request('/api/billing/history').then(j),

  // Workspace paid-feature entitlements (owner-managed).
  getEdition: () => request('/api/edition').then(j),
  setPaidFeature: (feature: string, enabled: boolean) =>
    request(`/api/edition/features/${encodeURIComponent(feature)}`, {
      method: 'PUT', body: JSON.stringify({ enabled }),
    }).then(j),
  downloadAuditExport: () => request('/api/edition/audit-export').then(async response => {
    if (!response.ok) throw new Error(`${response.status} ${await response.text()}`);
    return response.blob();
  }),
};
