import type { CatalogEntry } from './catalog-types';
import type { PaidFeatures } from './features';

// Canonical connector catalog — single source of truth for web UI, API AI builder, and worker.
// Colors are UI metadata; the server ignores them but they're harmless to include.
export const CATALOG: CatalogEntry[] = [
  // ── Sources ──
  {
    activityType: 'zendesk.fetch', nodeType: 'source', label: 'Zendesk',
    color: '#1D9E75', supportsIngestion: true,
    fields: [
      { key: 'connection', label: 'Zendesk account', type: 'oauth-picker',
        picker: 'zendesk', provider: 'zendesk',
        writes: ['connectionId', 'subdomain', 'resource'] },
    ],
  },
  {
    activityType: 'gsheets.fetch', nodeType: 'source', label: 'Google Sheets',
    color: '#1D9E75', supportsIngestion: true,
    fields: [
      { key: 'connection', label: 'Spreadsheet', type: 'oauth-picker',
        picker: 'gsheets', provider: 'google',
        writes: ['connectionId', 'spreadsheetId', 'range', 'sheetName'] },
      { key: 'keyColumn', label: 'Key column', type: 'text',
        help: 'Column used to detect changed rows' },
    ],
  },
  {
    activityType: 'gdrive.fetch', nodeType: 'source', label: 'Google Drive',
    color: '#1D9E75', supportsIngestion: true,
    fields: [
      { key: 'connection', label: 'Drive folder', type: 'oauth-picker',
        picker: 'gdrive', provider: 'google',
        writes: ['connectionId', 'folderId', 'query'] },
    ],
  },
  {
    activityType: 'excel.fetch', nodeType: 'source', label: 'Microsoft Excel',
    color: '#1D9E75', supportsIngestion: true,
    fields: [
      { key: 'connection', label: 'Workbook', type: 'oauth-picker',
        picker: 'excel', provider: 'microsoft',
        writes: ['connectionId', 'driveId', 'itemId', 'sheetName'] },
      { key: 'keyColumn', label: 'Key column', type: 'text',
        help: 'Column used to detect changed rows' },
    ],
  },
  {
    activityType: 'http.fetch', nodeType: 'source', label: 'Custom API',
    color: '#1D9E75', supportsIngestion: true,
    fields: [
      { key: 'url', label: 'URL', type: 'text', placeholder: 'https://api.example.com/items' },
      { key: 'recordsPath', label: 'Records path', type: 'text', placeholder: 'data.items' },
      { key: 'paginationJson', label: 'Pagination (JSON)', type: 'textarea',
        placeholder: '{"style":"cursor","cursorPath":"meta.next","param":"cursor","limitParam":"limit","limit":100}' },
      { key: 'authJson', label: 'Auth (JSON)', type: 'textarea',
        placeholder: '{"type":"bearer","token":"..."}' },
      { key: 'incrementalJson', label: 'Incremental (JSON)', type: 'textarea',
        placeholder: '{"sinceParam":"updated_after","recordTimestampPath":"updated_at","initialWatermark":"now-24h"}',
        help: 'Without initialWatermark the first run reads the full history; use an RFC 3339 time, "now" or "now-<duration>" to start later' },
    ],
  },
  {
    activityType: 'postgres.fetch', nodeType: 'source', label: 'PostgreSQL',
    color: '#336791', supportsIngestion: true,
    fields: [
      { key: 'connection', label: 'Database connection', type: 'instance-picker', provider: 'postgres', writes: ['connectionId'] },
      { key: 'table', label: 'Source table', type: 'text', placeholder: 'public.orders' },
      { key: 'syncMode', label: 'Sync mode', type: 'select', options: ['cursor', 'cdc'] },
      { key: 'columns', label: 'Columns', type: 'text', placeholder: '*', visibleWhen: { key: 'syncMode', equals: 'cursor' } },
      { key: 'cursorColumn', label: 'Incremental cursor column', type: 'text', placeholder: 'updated_at', help: 'Indexed, monotonically increasing column such as id or updated_at', visibleWhen: { key: 'syncMode', equals: 'cursor' } },
      { key: 'keyColumn', label: 'Tie-breaker key column', type: 'text', placeholder: 'id', help: 'Unique column (usually the primary key) used with the cursor so rows sharing a cursor value are never skipped', visibleWhen: { key: 'syncMode', equals: 'cursor' } },
      { key: 'cursorType', label: 'Cursor type', type: 'select', options: ['date', 'number', 'string'], visibleWhen: { key: 'syncMode', equals: 'cursor' } },
      { key: 'layer', label: 'Data layer', type: 'select', options: ['bronze', 'silver', 'gold'] },
    ],
  },
  {
    activityType: 'mysql.fetch', nodeType: 'source', label: 'MySQL', color: '#4479A1', supportsIngestion: true,
    fields: [
      { key: 'connection', label: 'Database connection', type: 'instance-picker', provider: 'mysql', writes: ['connectionId'] },
      { key: 'table', label: 'Source table', type: 'text', placeholder: 'orders' },
      { key: 'syncMode', label: 'Sync mode', type: 'select', options: ['cursor', 'cdc'] },
      { key: 'columns', label: 'Columns', type: 'text', placeholder: '*', visibleWhen: { key: 'syncMode', equals: 'cursor' } },
      { key: 'cursorColumn', label: 'Incremental cursor column', type: 'text', placeholder: 'updated_at', visibleWhen: { key: 'syncMode', equals: 'cursor' } },
      { key: 'keyColumn', label: 'Tie-breaker key column', type: 'text', placeholder: 'id', help: 'Unique column (usually the primary key) used with the cursor so rows sharing a cursor value are never skipped', visibleWhen: { key: 'syncMode', equals: 'cursor' } },
      { key: 'cursorType', label: 'Cursor type', type: 'select', options: ['date', 'number', 'string'], visibleWhen: { key: 'syncMode', equals: 'cursor' } },
      { key: 'layer', label: 'Data layer', type: 'select', options: ['bronze', 'silver', 'gold'] },
    ],
  },
  {
    activityType: 'mongodb.fetch', nodeType: 'source', label: 'MongoDB', color: '#47A248', supportsIngestion: true,
    fields: [
      { key: 'connection', label: 'Database connection', type: 'instance-picker', provider: 'mongodb', writes: ['connectionId'] },
      { key: 'collection', label: 'Collection', type: 'text', placeholder: 'orders' },
      { key: 'syncMode', label: 'Sync mode', type: 'select', options: ['cursor', 'cdc'] },
      { key: 'cursorField', label: 'Cursor field', type: 'text', placeholder: '_id', visibleWhen: { key: 'syncMode', equals: 'cursor' } },
      { key: 'cursorType', label: 'Cursor type', type: 'select', options: ['objectId', 'string', 'number', 'date'], visibleWhen: { key: 'syncMode', equals: 'cursor' } },
      { key: 'layer', label: 'Data layer', type: 'select', options: ['bronze', 'silver', 'gold'] },
    ],
  },
  {
    activityType: 's3.fetch', nodeType: 'source', label: 'Amazon S3', color: '#FF9900', supportsIngestion: true,
    fields: [
      { key: 'connection', label: 'S3 connection', type: 'instance-picker', provider: 's3', writes: ['connectionId'] },
      { key: 'bucket', label: 'Bucket', type: 'text' },
      { key: 'key', label: 'Object key', type: 'text', placeholder: 'imports/orders.jsonl' },
      { key: 'format', label: 'Format', type: 'select', options: ['jsonl', 'json'] },
      { key: 'layer', label: 'Data layer', type: 'select', options: ['bronze', 'silver', 'gold'] },
    ],
  },
  {
    activityType: 'kafka.fetch', nodeType: 'source', label: 'Kafka / Redpanda', color: '#7C3AED',
    requiredFeature: 'realtime',
    fields: [
      { key: 'connection', label: 'Kafka connection', type: 'instance-picker', provider: 'kafka', writes: ['connectionId'] },
      { key: 'topic', label: 'Topic', type: 'text', placeholder: 'orders.v1' },
      { key: 'cluster', label: 'Lineage cluster name', type: 'text', placeholder: 'events-prod', help: 'Use the same name across connections to merge topic lineage' },
      { key: 'startPosition', label: 'First run starts at', type: 'select', options: ['earliest', 'latest'] },
      { key: 'valueFormat', label: 'Value format', type: 'select', options: ['json', 'string'] },
      { key: 'includeMetadata', label: 'Include Kafka metadata', type: 'checkbox' },
      { key: 'pageSize', label: 'Messages per page', type: 'number', placeholder: '1000' },
      { key: 'layer', label: 'Data layer', type: 'select', options: ['bronze', 'silver', 'gold'] },
    ],
  },
  {
    activityType: 'sftp.fetch', nodeType: 'source', label: 'SFTP', color: '#536DFE', supportsIngestion: true,
    requiredFeature: 'advancedConnectors',
    fields: [
      { key: 'connection', label: 'SFTP connection', type: 'instance-picker', provider: 'sftp', writes: ['connectionId'] },
      { key: 'path', label: 'Remote file', type: 'text', placeholder: '/exports/orders.jsonl' },
      { key: 'format', label: 'Format', type: 'select', options: ['jsonl', 'json'] },
    ],
  },
  {
    activityType: 'snowflake.fetch', nodeType: 'source', label: 'Snowflake', color: '#29B5E8', supportsIngestion: true,
    requiredFeature: 'advancedConnectors',
    fields: [
      { key: 'connection', label: 'Snowflake connection', type: 'instance-picker', provider: 'snowflake', writes: ['connectionId'] },
      { key: 'table', label: 'Table', type: 'text', placeholder: 'RAW.ORDERS' },
      { key: 'syncMode', label: 'Sync mode', type: 'select', options: ['cursor', 'changes'] },
      { key: 'cursorColumn', label: 'Cursor column', type: 'text', placeholder: 'UPDATED_AT', visibleWhen: { key: 'syncMode', equals: 'cursor' } },
    ],
  },
  {
    activityType: 'iceberg.fetch', nodeType: 'source', label: 'Apache Iceberg', color: '#4B88A2', supportsIngestion: true,
    requiredFeature: 'advancedConnectors',
    fields: [
      { key: 'connection', label: 'Iceberg REST catalog', type: 'instance-picker', provider: 'iceberg', writes: ['connectionId'] },
      { key: 'namespace', label: 'Namespace', type: 'text', placeholder: 'analytics' },
      { key: 'table', label: 'Table', type: 'text', placeholder: 'orders' },
    ],
  },
  // ── Transforms ──
  {
    activityType: 'transform.map', nodeType: 'transform', label: 'Map', color: '#D85A30',
    fields: [{ key: 'expression', label: 'Safe projection expression', type: 'textarea',
      placeholder: '({ id: r.id, subject: r.subject, priority: r.priority })' }],
  },
  {
    activityType: 'transform.filter', nodeType: 'transform', label: 'Filter', color: '#D85A30',
    fields: [{ key: 'predicate', label: 'Safe predicate', type: 'textarea',
      placeholder: "r.status === 'open'" }],
  },
  {
    activityType: 'transform.formula', nodeType: 'transform', label: 'Formula', color: '#D85A30',
    fields: [
      { key: 'outputField', label: 'Output field', type: 'text', placeholder: 'total' },
      { key: 'expression', label: 'Formula', type: 'textarea', placeholder: 'round(r.price * r.quantity, 2)', help: 'Functions: abs, round, lower, upper, string, number, length, coalesce, concat' },
    ],
  },
  {
    activityType: 'transform.select', nodeType: 'transform', label: 'JMESPath select', color: '#D85A30',
    fields: [{ key: 'expression', label: 'JMESPath expression', type: 'textarea', placeholder: '{id: id, total: total}' }],
  },
  {
    activityType: 'transform.rename', nodeType: 'transform', label: 'Rename', color: '#D85A30',
    fields: [{ key: 'mapping', label: 'Field mapping (JSON)', type: 'textarea',
      placeholder: '{"old_name":"new_name","ticket_id":"id"}' }],
  },
  {
    activityType: 'transform.dedupe', nodeType: 'transform', label: 'Dedupe', color: '#D85A30',
    fields: [
      { key: 'key', label: 'Dedup key(s)', type: 'text', placeholder: 'id, country',
        help: 'One field, or comma-separated for a compound key' },
      { key: 'keep', label: 'Keep', type: 'select', options: ['first', 'last'] },
      { key: 'scope', label: 'Scope', type: 'select', options: ['run', 'pipeline'], help: 'Pipeline scope remembers keys across runs' },
    ],
  },
  {
    activityType: 'transform.flatten', nodeType: 'transform', label: 'Flatten', color: '#D85A30',
    fields: [
      { key: 'delimiter', label: 'Key delimiter', type: 'text', placeholder: '.' },
      { key: 'maxDepth', label: 'Max depth', type: 'number', placeholder: '10' },
      { key: 'arrayPolicy', label: 'Array policy', type: 'select', options: ['index', 'stringify', 'keep'] },
    ],
  },
  {
    activityType: 'transform.parse', nodeType: 'transform', label: 'Parse JSON', color: '#D85A30',
    fields: [
      { key: 'fields', label: 'Fields (comma-separated)', type: 'text', placeholder: 'payload,metadata' },
      { key: 'onError', label: 'On error', type: 'select', options: ['skip', 'fail', 'null'] },
    ],
  },
  {
    activityType: 'transform.contract', nodeType: 'transform', label: 'Data contract', color: '#7C3AED',
    fields: [
      { key: 'schemaJson', label: 'Field contract (JSON)', type: 'textarea', placeholder: '{"id":"number","email":"string","updated_at":"date?"}' },
      { key: 'onViolation', label: 'On violation', type: 'select', options: ['fail', 'drop', 'quarantine'] },
      { key: 'allowExtra', label: 'Allow extra fields', type: 'checkbox' },
    ],
  },
  // ── Flow ──
  { activityType: 'flow.fork', nodeType: 'fork', label: 'Fork', color: '#7F77DD', fields: [] },
  {
    activityType: 'flow.merge', nodeType: 'merge', label: 'Merge', color: '#7F77DD',
    fields: [
      { key: 'mergeStrategy', label: 'Strategy', type: 'select',
        options: ['concat', 'union', 'innerJoin', 'leftJoin', 'outerJoin', 'appendWithSourceTag'] },
      { key: 'joinKey', label: 'Join key (joins only)', type: 'text' },
    ],
  },
  // ── Sinks ──
  {
    activityType: 'sink.postgres', nodeType: 'sink', label: 'Postgres (destination)', color: '#639922',
    fields: [
      { key: 'connection', label: 'Destination', type: 'instance-picker', provider: 'postgres', writes: ['connectionId'] },
      { key: 'table', label: 'Target table', type: 'text', placeholder: 'public.orders' },
      { key: 'writeMode', label: 'Write mode', type: 'select', options: ['upsert', 'apply-cdc'] },
      { key: 'conflictKey', label: 'Upsert key(s)', type: 'text', placeholder: 'id',
        help: 'Required for apply-cdc; comma-separated for a composite key' },
      { key: 'layer', label: 'Data layer', type: 'select', options: ['bronze', 'silver', 'gold'] },
    ],
  },
  {
    activityType: 'sink.clickhouse', nodeType: 'sink', label: 'ClickHouse', color: '#F4C430',
    fields: [
      { key: 'connection', label: 'ClickHouse connection', type: 'instance-picker', provider: 'clickhouse', writes: ['connectionId'] },
      { key: 'table', label: 'Target table', type: 'text', placeholder: 'analytics.events' },
      { key: 'layer', label: 'Data layer', type: 'select', options: ['bronze', 'silver', 'gold'] },
    ],
  },
  {
    activityType: 'sink.mysql', nodeType: 'sink', label: 'MySQL', color: '#4479A1',
    fields: [
      { key: 'connection', label: 'Database connection', type: 'instance-picker', provider: 'mysql', writes: ['connectionId'] },
      { key: 'table', label: 'Target table', type: 'text', placeholder: 'orders' },
      { key: 'writeMode', label: 'Write mode', type: 'select', options: ['upsert', 'apply-cdc'] },
      { key: 'primaryKey', label: 'Primary key(s)', type: 'text', placeholder: 'id', help: 'Required for apply-cdc; comma-separated for a composite key' },
      { key: 'layer', label: 'Data layer', type: 'select', options: ['bronze', 'silver', 'gold'] },
    ],
  },
  {
    activityType: 'sink.mongodb', nodeType: 'sink', label: 'MongoDB', color: '#47A248',
    fields: [
      { key: 'connection', label: 'Database connection', type: 'instance-picker', provider: 'mongodb', writes: ['connectionId'] },
      { key: 'collection', label: 'Collection', type: 'text', placeholder: 'orders' },
      { key: 'keyField', label: 'Upsert key', type: 'text', placeholder: '_id' },
      { key: 'writeMode', label: 'Write mode', type: 'select', options: ['upsert', 'apply-cdc'] },
      { key: 'layer', label: 'Data layer', type: 'select', options: ['bronze', 'silver', 'gold'] },
    ],
  },
  {
    activityType: 'sink.s3', nodeType: 'sink', label: 'Amazon S3', color: '#FF9900',
    fields: [
      { key: 'connection', label: 'S3 connection', type: 'instance-picker', provider: 's3', writes: ['connectionId'] },
      { key: 'bucket', label: 'Bucket', type: 'text' },
      { key: 'key', label: 'Object key', type: 'text', placeholder: 'exports/{executionId}.jsonl' },
      { key: 'format', label: 'Format', type: 'select', options: ['jsonl', 'json'] },
      { key: 'layer', label: 'Data layer', type: 'select', options: ['bronze', 'silver', 'gold'] },
    ],
  },
  {
    activityType: 'sink.kafka', nodeType: 'sink', label: 'Kafka / Redpanda', color: '#7C3AED',
    requiredFeature: 'realtime',
    fields: [
      { key: 'connection', label: 'Kafka connection', type: 'instance-picker', provider: 'kafka', writes: ['connectionId'] },
      { key: 'topic', label: 'Topic', type: 'text', placeholder: 'orders.cleaned.v1' },
      { key: 'cluster', label: 'Lineage cluster name', type: 'text', placeholder: 'events-prod' },
      { key: 'keyField', label: 'Message key field', type: 'text', placeholder: 'id', help: 'Recommended for compacted topics' },
      { key: 'layer', label: 'Data layer', type: 'select', options: ['bronze', 'silver', 'gold'] },
    ],
  },
  {
    activityType: 'sink.sftp', nodeType: 'sink', label: 'SFTP (destination)', color: '#536DFE',
    requiredFeature: 'advancedConnectors',
    fields: [
      { key: 'connection', label: 'SFTP connection', type: 'instance-picker', provider: 'sftp', writes: ['connectionId'] },
      { key: 'path', label: 'Remote file', type: 'text', placeholder: '/imports/{executionId}.jsonl' },
      { key: 'format', label: 'Format', type: 'select', options: ['jsonl', 'json'] },
    ],
  },
  {
    activityType: 'sink.snowflake', nodeType: 'sink', label: 'Snowflake (destination)', color: '#29B5E8',
    requiredFeature: 'advancedConnectors',
    fields: [
      { key: 'connection', label: 'Snowflake connection', type: 'instance-picker', provider: 'snowflake', writes: ['connectionId'] },
      { key: 'table', label: 'Target table', type: 'text', placeholder: 'ANALYTICS.ORDERS' },
    ],
  },
  {
    activityType: 'sink.iceberg', nodeType: 'sink', label: 'Apache Iceberg (destination)', color: '#4B88A2',
    requiredFeature: 'advancedConnectors',
    fields: [
      { key: 'connection', label: 'Iceberg REST catalog', type: 'instance-picker', provider: 'iceberg', writes: ['connectionId'] },
      { key: 'namespace', label: 'Namespace', type: 'text', placeholder: 'analytics' },
      { key: 'table', label: 'Existing table', type: 'text', placeholder: 'orders' },
    ],
  },
  {
    activityType: 'sink.gsheets', nodeType: 'sink', label: 'Google Sheets (destination)', color: '#639922',
    fields: [
      { key: 'connection', label: 'Destination', type: 'instance-picker', provider: 'google', writes: ['connectionId'] },
      { key: 'spreadsheetId', label: 'Spreadsheet ID', type: 'text' },
      { key: 'sheetName', label: 'Sheet name', type: 'text', placeholder: 'Sheet1' },
      { key: 'writeMode', label: 'Write mode', type: 'select', options: ['replace', 'append'],
        help: 'replace = clear & rewrite each run (idempotent); append = add rows' },
      { key: 'includeHeader', label: 'Include header row', type: 'checkbox' },
    ],
  },
  {
    activityType: 'sink.webhook', nodeType: 'sink', label: 'Webhook (destination)', color: '#639922',
    fields: [
      { key: 'connection', label: 'Stored HTTP credential (optional)', type: 'instance-picker', provider: 'http', writes: ['connectionId'] },
      { key: 'url', label: 'URL', type: 'text', help: 'Used only when no stored HTTP credential is selected' },
    ],
  },
  {
    activityType: 'sink.records', nodeType: 'sink', label: 'DataFlow store (managed)', color: '#639922',
    fields: [
      { key: 'collection', label: 'Collection name', type: 'text' },
      { key: 'dedupField', label: 'Dedup field', type: 'text', placeholder: 'id' },
    ],
  },
];

export const catalogByType = Object.fromEntries(CATALOG.map(c => [c.activityType, c]));

export function catalogForFeatures(catalog: CatalogEntry[], features: PaidFeatures): CatalogEntry[] {
  return catalog
    .filter(entry => !entry.requiredFeature || features[entry.requiredFeature])
    .map(entry => ({
      ...entry,
      fields: entry.fields.map(field => {
        if (!field.options) return field;
        const options = field.options.filter(option =>
          (features.realtime || (option !== 'cdc' && option !== 'apply-cdc'))
          && (features.statefulProcessing || option !== 'pipeline'));
        return options.length === field.options.length ? field : { ...field, options };
      }),
    }));
}
