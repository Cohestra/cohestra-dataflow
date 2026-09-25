// ─── Pipeline definition: the contract between UI, API, and Temporal ───────

export type NodeType = 'source' | 'transform' | 'sink' | 'fork' | 'merge' | 'agent';

// M3: a pipeline version lives in exactly one environment. Each environment is
// a separate Temporal namespace + task queue (dynamic-dag-<env>).
export type Environment = 'test' | 'prod';
export const ENVIRONMENTS: Environment[] = ['test', 'prod'];

export interface PipelineDefinition {
  id: string;
  version: number;            // immutable; editing creates a new version
  name: string;
  tenantId: string;
  trigger: TriggerConfig;     // trigger lives in the definition itself
  nodes: PipelineNode[];
  edges: PipelineEdge[];
  concurrency?: { maxParallelNodes?: number };
  metadata?: PipelineMetadata;
  slo?: PipelineSlo;
  notifications?: PipelineNotificationPolicy;
  execution?: { engine?: 'workflow' | 'stream-direct' | 'spark-sql' | 'flink-sql'; transformSql?: string };
}

export interface PipelineMetadata {
  owner?: string;
  domain?: string;
  tags?: string[];
}

export interface PipelineSlo {
  freshnessMinutes?: number;
  maxFailureRatePercent?: number;
  maxDurationMs?: number;
}

export interface PipelineNotificationPolicy {
  connectionId?: string;
  minimumSeverity?: 'warning' | 'critical';
}

export type DataAssetType = 'table' | 'file' | 'topic' | 'stream' | 'vector-index' | 'model' | 'api' | 'collection';
export type DataAssetLayer = 'bronze' | 'silver' | 'gold';

// Stable asset identity is what joins otherwise-independent pipelines into one
// workspace lineage graph. URNs must not contain credentials or secret query params.
export interface DataAssetRef {
  urn: string;
  platform: string;
  namespace: string;
  name: string;
  type: DataAssetType;
  layer?: DataAssetLayer;
  schema?: { fields: Array<{ name: string; type: string; nullable?: boolean }> };
  owner?: string;
  tags?: string[];
}

export type TriggerConfig =
  | { type: 'cron'; schedule: string }                       // Temporal Schedule
  | { type: 'webhook'; path: string; secret: string }        // HMAC verified
  | { type: 'event'; topic: string }                         // Redis pub/sub (POC)
  | { type: 'asset'; assetUrn: string }                      // durable materialization event
  | { type: 'manual' };

export interface PipelineNode {
  id: string;
  type: NodeType;
  activityType: string;       // catalog key e.g. "zendesk.fetch"
  label?: string;
  config: Record<string, unknown>;
  ingestion?: IngestionConfig; // source nodes only
  timeoutSec?: number;
  retry?: { maximumAttempts?: number };
  mergeStrategy?: 'concat' | 'union' | 'innerJoin' | 'leftJoin' | 'outerJoin' | 'appendWithSourceTag';
  joinKey?: string;
  inputAssets?: DataAssetRef[];
  outputAssets?: DataAssetRef[];
}

// Reserved wire contract; production admission stays disabled until AgentWorkflow exists.
export interface AgentNodeConfig extends Record<string, unknown> {
  agentId: string; // canonical lowercase UUID of an immutable published agent
  agentVersion: number; // positive integer
  inputBinding: { mode: 'batch'; fields: string[]; maxRecords: number }; // 1..100
}

export interface AgentPipelineNode extends PipelineNode {
  type: 'agent';
  activityType: 'agent.run';
  config: AgentNodeConfig;
}

export interface IngestionConfig {
  mode: 'incremental' | 'backfill' | 'realtime';
  backfillStart?: string;     // ISO date — page from here until caught up
  backfillEnd?: string;       // ISO date — exclusive upper bound
  stateKey?: string;          // isolates durable cursor state for a backfill partition
  pageSize?: number;
}

export type CdcOperation = 'create' | 'update' | 'delete' | 'snapshot';

export interface CdcEvent {
  op: CdcOperation;
  key: Record<string, unknown>;
  before: Record<string, unknown> | null;
  after: Record<string, unknown> | null;
  occurredAt: string;
  source: {
    provider: 'postgres' | 'mysql' | 'mongodb';
    database: string;
    schema?: string;
    table: string;
    topic: string;
    partition: number;
    offset: string;
  };
}

export interface PipelineEdge {
  id: string;
  source: string;
  target: string;
  condition?: string;         // expression evaluated against upstream output
}

// ─── DataRef: pointers flow through Temporal history, never payloads ───────

export interface DataRef {
  type: 'pg' | 'inline' | 's3';
  key: string;                // pg row id, base64 inline (<4KB), or object key
  bucket?: string;            // persisted so old refs survive default-bucket changes
  tenantId: string;
  sizeBytes: number;
  recordCount?: number;
  encrypted?: boolean;
  iv?: string;
}

export interface NodeResult {
  nodeId: string;
  status: 'success' | 'skipped' | 'failed';
  outputRef?: DataRef;
  meta: { durationMs: number; recordCount?: number };
  error?: string;
}

export interface ExecutionStatus {
  executionId: string;
  phase: 'running' | 'paused' | 'completed' | 'failed' | 'cancelled';
  nodeResults: Record<string, NodeResult>;
  startedAt: string;
  completedAt?: string;
  stream?: { batches: number; records: number; errors: number; lagRecords: number; throughputPerSec: number; lastHeartbeat: string };
}

export interface DynamicWorkflowInput {
  definition: PipelineDefinition;   // FULL frozen definition — never fetched inside workflow
  tenantId: string;
  executionId: string;
  pipelineRowId?: string;
  environment?: Environment;
  executionPrepared?: boolean;
  trigger: { type: string; payloadRef?: DataRef; firedAt: string };
  // Phase 6: DEK wrapped with worker's RSA public key. Worker decrypts once at workflow
  // start; downstream activities receive plaintext DEK in workflow context.
  encryptedDek?: string;
  dekIv?: string;
  stream?: ExecutionStatus['stream'];
}

// ─── Tenant / auth context attached to every authenticated API request ─────

export type TenantRole = 'owner' | 'member';

export interface TenantContext {
  tenantId: string;
  userId: string | null;     // null only for stub middleware before Phase 1 lands
  email: string | null;
  role: TenantRole;
  emailVerified: boolean;
}

declare global {
  // Augment Express Request so req.tenant is typed everywhere
  // eslint-disable-next-line @typescript-eslint/no-namespace
  namespace Express {
    interface Request {
      tenant: TenantContext;
    }
  }
}
