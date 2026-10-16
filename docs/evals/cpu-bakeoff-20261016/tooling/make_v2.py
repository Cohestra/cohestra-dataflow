#!/usr/bin/env python3
"""Generate tests/ai-evals/cases/v2-datalake.json (catalog derived from ai_grounding.go)."""
import json, re
from pathlib import Path
ROOT = Path('/home/user/cohestra-dataflow')
src = (ROOT/'apps/workflow-go/internal/api/ai_grounding.go').read_text()
block = src[src.index('var codedAIFields'):src.index('var codedAIRequiredFields')]
keys = {m.group(1): re.findall(r'field\("([A-Za-z]+)"', m.group(2))
        for m in re.finditer(r'"([a-z0-9]+\.[a-z0-9]+)":\s*\{(.*?)\},?\n', block)}
v1 = json.loads((ROOT/'tests/ai-evals/cases/v1.json').read_text())
types = v1['catalog']['nodeTypes']
assert {t for v in types.values() for t in v} == set(keys), 'catalog drift'
CONN = {f['displayName']: f'00000000-0000-4000-8000-{i+1:012d}' for i, f in enumerate(v1['fixtures']['connectors'])}

def n(id, type, act, **config): return {'id': id, 'type': type, 'activityType': act, 'config': config}
def e(s, t): return {'source': s, 'target': t}
def A(t, **cfg): return {'activityType': t, **({'config': cfg} if cfg else {})}

cases = []
def gen(id, cat, prompt, fixtures, expect, messages=None):
    req = {'prompt': prompt}
    if messages: req['messages'] = messages
    cases.append({'id': id, 'category': cat, 'endpoint': '/api/ai/generate', 'connectorFixtures': fixtures, 'request': req, 'expect': expect})
def refine(id, cat, prompt, fixtures, definition, expect, messages=None):
    req = {'prompt': prompt, 'definition': definition}
    if messages: req['messages'] = messages
    cases.append({'id': id, 'category': cat, 'endpoint': '/api/ai/refine', 'connectorFixtures': fixtures, 'request': req, 'expect': expect})

# ---- Lakehouse generation -------------------------------------------------
gen('lake-cdc-contract-iceberg', 'lake-generation',
    'Continuously capture CDC changes from PostgreSQL connection warehouse-read table public.customers (syncMode cdc), '
    'enforce a data contract with schemaJson {"type":"object","required":["id","email"]} quarantining violations, '
    'then write through Iceberg connection eval-iceberg to namespace lake table customers_silver.',
    ['warehouse-read', 'eval-iceberg'],
    {'activities': {'required': ['postgres.fetch', 'transform.contract', 'sink.iceberg'], 'forbidden': ['iceberg.fetch', 'sink.postgres']},
     'configs': [{'activityType': 'postgres.fetch', 'contains': {'table': 'public.customers', 'syncMode': 'cdc'}},
                 {'activityType': 'transform.contract', 'contains': {'onViolation': 'quarantine'}},
                 {'activityType': 'sink.iceberg', 'contains': {'namespace': 'lake', 'table': 'customers_silver'}}],
     'graph': {'requiredPaths': [[A('postgres.fetch'), A('transform.contract'), A('sink.iceberg')]]}})

gen('lake-silver-to-gold-clickhouse', 'lake-generation',
    'Read Iceberg connection eval-iceberg namespace lake table orders_silver, keep only rows where status equals completed, '
    'compute a revenue field as price * quantity, and load through ClickHouse connection clickhouse-analytics into table gold.daily_revenue as the gold layer.',
    ['eval-iceberg', 'clickhouse-analytics'],
    {'activities': {'required': ['iceberg.fetch', 'transform.filter', 'transform.formula', 'sink.clickhouse'], 'forbidden': ['sink.iceberg']},
     'configs': [{'activityType': 'iceberg.fetch', 'contains': {'namespace': 'lake', 'table': 'orders_silver'}},
                 {'activityType': 'transform.formula', 'contains': {'outputField': 'revenue'}},
                 {'activityType': 'sink.clickhouse', 'contains': {'table': 'gold.daily_revenue', 'layer': 'gold'}}],
     'graph': {'requiredPaths': [[A('iceberg.fetch'), A('transform.filter'), A('transform.formula'), A('sink.clickhouse')]]}})

gen('lake-join-orders-payments', 'lake-generation',
    'Read orders from PostgreSQL connection warehouse-read table public.orders and payments from MongoDB connection reporting-mongo collection payments, '
    'left join them on order_id, and write the result through Iceberg connection eval-iceberg to namespace lake table order_payments.',
    ['warehouse-read', 'reporting-mongo', 'eval-iceberg'],
    {'activities': {'required': ['postgres.fetch', 'mongodb.fetch', 'flow.merge', 'sink.iceberg']},
     'configs': [{'activityType': 'flow.merge', 'contains': {'mergeStrategy': 'leftJoin', 'joinKey': 'order_id'}},
                 {'activityType': 'mongodb.fetch', 'contains': {'collection': 'payments'}},
                 {'activityType': 'sink.iceberg', 'contains': {'namespace': 'lake', 'table': 'order_payments'}}],
     'graph': {'minNodes': 4, 'minInDegree': 2, 'requiredPaths': [[A('flow.merge'), A('sink.iceberg')]]}})

gen('lake-s3-flatten-snowflake', 'lake-generation',
    'Ingest newline-delimited JSON from S3 connection eval-s3 bucket raw-zone key events/2026/10/15.jsonl, flatten nested fields with delimiter _, '
    'rename field usr_id to user_id, and load through Snowflake connection snowflake-analytics into table ANALYTICS.EVENTS_SILVER.',
    ['eval-s3', 'snowflake-analytics'],
    {'activities': {'required': ['s3.fetch', 'transform.flatten', 'transform.rename', 'sink.snowflake'], 'forbidden': ['sink.s3']},
     'configs': [{'activityType': 's3.fetch', 'contains': {'bucket': 'raw-zone', 'key': 'events/2026/10/15.jsonl', 'format': 'jsonl'}},
                 {'activityType': 'transform.flatten', 'contains': {'delimiter': '_'}},
                 {'activityType': 'sink.snowflake', 'contains': {'table': 'ANALYTICS.EVENTS_SILVER'}}],
     'graph': {'requiredPaths': [[A('s3.fetch'), A('transform.flatten'), A('transform.rename'), A('sink.snowflake')]]}})

gen('lake-medallion-fork-zones', 'lake-generation',
    'Fetch https://api.example.test/telemetry and fork it into three zones: store the raw records through S3 connection eval-s3 in bucket lake-bronze key telemetry/raw.jsonl as bronze; '
    'deduplicate on device_id keeping the last record and write through Iceberg connection eval-iceberg to namespace lake table telemetry_silver as silver; '
    'and keep only rows where severity equals critical and send them through ClickHouse connection clickhouse-analytics to table gold.critical_alerts as gold.',
    ['eval-s3', 'eval-iceberg', 'clickhouse-analytics'],
    {'activities': {'required': ['http.fetch', 'flow.fork', 'sink.s3', 'transform.dedupe', 'sink.iceberg', 'transform.filter', 'sink.clickhouse']},
     'configs': [{'activityType': 'sink.s3', 'contains': {'bucket': 'lake-bronze', 'key': 'telemetry/raw.jsonl', 'layer': 'bronze'}},
                 {'activityType': 'transform.dedupe', 'contains': {'key': 'device_id', 'keep': 'last'}},
                 {'activityType': 'sink.iceberg', 'contains': {'namespace': 'lake', 'table': 'telemetry_silver'}},
                 {'activityType': 'sink.clickhouse', 'contains': {'table': 'gold.critical_alerts', 'layer': 'gold'}}],
     'graph': {'minNodes': 7, 'minOutDegree': 3,
               'requiredPaths': [[A('transform.dedupe'), A('sink.iceberg')], [A('transform.filter'), A('sink.clickhouse')]]}})

gen('lake-kafka-parse-dedupe-iceberg', 'lake-generation',
    'Consume Kafka connection eval-kafka topic payments.v2 from the earliest offset, parse the fields amount and currency skipping rows that fail, '
    'deduplicate on payment_id keeping the first record across the whole pipeline, and append through Iceberg connection eval-iceberg to namespace lake table payments_bronze.',
    ['eval-kafka', 'eval-iceberg'],
    {'activities': {'required': ['kafka.fetch', 'transform.parse', 'transform.dedupe', 'sink.iceberg'], 'forbidden': ['sink.kafka']},
     'configs': [{'activityType': 'kafka.fetch', 'contains': {'topic': 'payments.v2', 'startPosition': 'earliest'}},
                 {'activityType': 'transform.parse', 'contains': {'onError': 'skip'}},
                 {'activityType': 'transform.dedupe', 'contains': {'key': 'payment_id', 'keep': 'first', 'scope': 'pipeline'}},
                 {'activityType': 'sink.iceberg', 'contains': {'namespace': 'lake', 'table': 'payments_bronze'}}],
     'graph': {'requiredPaths': [[A('kafka.fetch'), A('transform.parse'), A('transform.dedupe'), A('sink.iceberg')]]}})

gen('lake-spark-sql-gold', 'lake-engines',
    'Using the Spark SQL engine, read Iceberg connection eval-iceberg namespace lake table events_silver, aggregate event counts per day in SQL, '
    'and write through ClickHouse connection clickhouse-analytics to table gold.daily_events as the gold layer.',
    ['eval-iceberg', 'clickhouse-analytics'],
    {'activities': {'required': ['iceberg.fetch', 'sink.clickhouse'], 'forbidden': ['transform.select', 'transform.map', 'transform.formula']},
     'configs': [{'activityType': 'sink.clickhouse', 'contains': {'table': 'gold.daily_events'}}],
     'execution': {'engine': 'spark-sql'}})

gen('lake-nightly-incremental', 'lake-engines',
    'Every night at 01:15 UTC, incrementally load MySQL connection shop-mysql table shop.orders using cursor column updated_at of type date, '
    'and upsert through PostgreSQL connection warehouse-read into table lake.orders_silver on conflict key order_id.',
    ['shop-mysql', 'warehouse-read'],
    {'activities': {'required': ['mysql.fetch', 'sink.postgres']},
     'configs': [{'activityType': 'mysql.fetch', 'contains': {'table': 'shop.orders', 'cursorColumn': 'updated_at'}},
                 {'activityType': 'sink.postgres', 'contains': {'table': 'lake.orders_silver', 'conflictKey': 'order_id'}}],
     'trigger': {'type': 'cron'}})

# ---- Multi-turn chat ------------------------------------------------------
gen('chat-answer-followup', 'chat',
    'Use PostgreSQL connection warehouse-read table public.orders as the source, keep only rows where status equals completed, '
    'and write through Iceberg connection eval-iceberg to namespace lake table orders_completed.',
    ['warehouse-read', 'eval-iceberg'],
    {'activities': {'required': ['postgres.fetch', 'transform.filter', 'sink.iceberg']},
     'configs': [{'activityType': 'postgres.fetch', 'contains': {'table': 'public.orders'}},
                 {'activityType': 'sink.iceberg', 'contains': {'namespace': 'lake', 'table': 'orders_completed'}}],
     'graph': {'requiredPaths': [[A('postgres.fetch'), A('transform.filter'), A('sink.iceberg')]]}},
    messages=[{'role': 'user', 'content': 'Load completed orders into the lake.'},
              {'role': 'assistant', 'content': 'I need more information.\nQuestion: Which saved connection and table hold the orders?\nQuestion: Which Iceberg namespace and table should receive them?'}])

gen('chat-vague-lake', 'chat', 'Build me a data lake for all our company data.', [], {'status': 'needs_input'})

# ---- Refinement (seeds carry real fixture connectionIds; G11-safe) -------
kafka_bronze = {'trigger': {'type': 'manual'}, 'nodes': [
    n('src', 'source', 'kafka.fetch', connectionId=CONN['eval-kafka'], topic='clicks.raw', layer='bronze'),
    n('bronze', 'sink', 'sink.s3', connectionId=CONN['eval-s3'], bucket='lake-bronze', key='clicks/raw.jsonl', format='jsonl', layer='bronze')],
    'edges': [e('src', 'bronze')]}
refine('refine-lake-insert-dedupe', 'lake-refinement',
    'Insert a dedupe on event_id keeping the last record between the Kafka source and the S3 sink. Leave all existing node settings unchanged.',
    ['eval-kafka', 'eval-s3'], kafka_bronze,
    {'activities': {'required': ['kafka.fetch', 'transform.dedupe', 'sink.s3']},
     'configs': [{'activityType': 'transform.dedupe', 'contains': {'key': 'event_id', 'keep': 'last'}}],
     'preserve': {'nodes': [{'id': 'src'}, {'id': 'bronze'}]},
     'graph': {'requiredPaths': [[{'id': 'src'}, A('transform.dedupe'), {'id': 'bronze'}]]}})

pg_s3 = {'trigger': {'type': 'manual'}, 'nodes': [
    n('orders', 'source', 'postgres.fetch', connectionId=CONN['warehouse-read'], table='public.orders'),
    n('completed', 'transform', 'transform.filter', predicate="r.status === 'completed'"),
    n('landing', 'sink', 'sink.s3', connectionId=CONN['eval-s3'], bucket='lake-bronze', key='orders.jsonl', format='jsonl')],
    'edges': [e('orders', 'completed'), e('completed', 'landing')]}
refine('refine-lake-swap-sink-iceberg', 'lake-refinement',
    'Replace the S3 sink with an Iceberg sink through connection eval-iceberg to namespace lake table orders_silver. Keep the source and the filter exactly as they are.',
    ['warehouse-read', 'eval-iceberg'], pg_s3,
    {'activities': {'required': ['postgres.fetch', 'transform.filter', 'sink.iceberg'], 'forbidden': ['sink.s3']},
     'configs': [{'activityType': 'sink.iceberg', 'contains': {'namespace': 'lake', 'table': 'orders_silver'}}],
     'preserve': {'nodes': [{'id': 'orders'}, {'id': 'completed'}]},
     'graph': {'requiredPaths': [[{'id': 'orders'}, {'id': 'completed'}, A('sink.iceberg')]]}})

silver = {'trigger': {'type': 'manual'}, 'nodes': [
    n('silver_in', 'source', 'iceberg.fetch', connectionId=CONN['eval-iceberg'], namespace='lake', table='orders_silver'),
    n('big', 'transform', 'transform.filter', predicate='r.total > 100'),
    n('silver_out', 'sink', 'sink.iceberg', connectionId=CONN['eval-iceberg'], namespace='lake', table='big_orders')],
    'edges': [e('silver_in', 'big'), e('big', 'silver_out')]}
refine('refine-lake-add-gold-branch', 'lake-refinement',
    'Also send the filtered rows through ClickHouse connection clickhouse-analytics to table gold.big_orders as the gold layer, keeping the existing Iceberg sink unchanged.',
    ['eval-iceberg', 'clickhouse-analytics'], silver,
    {'activities': {'required': ['iceberg.fetch', 'transform.filter', 'sink.iceberg', 'sink.clickhouse']},
     'configs': [{'activityType': 'sink.clickhouse', 'contains': {'table': 'gold.big_orders', 'layer': 'gold'}}],
     'preserve': {'nodes': [{'id': 'silver_in'}, {'id': 'big'}, {'id': 'silver_out'}]},
     'graph': {'minOutDegree': 2, 'requiredPaths': [[{'id': 'big'}, {'id': 'silver_out'}], [{'id': 'big'}, A('sink.clickhouse')]]}})

with_filter = {'trigger': {'type': 'manual'}, 'nodes': [
    n('src', 'source', 'mongodb.fetch', connectionId=CONN['reporting-mongo'], collection='invoices'),
    n('amt', 'transform', 'transform.filter', predicate='r.amount > 100'),
    n('dst', 'sink', 'sink.iceberg', connectionId=CONN['eval-iceberg'], namespace='lake', table='invoices')],
    'edges': [e('src', 'amt'), e('amt', 'dst')]}
refine('refine-chat-revert-filter', 'chat',
    'Actually, remove that amount filter again and connect the MongoDB source straight to the Iceberg sink. Do not change anything else.',
    ['reporting-mongo', 'eval-iceberg'], with_filter,
    {'activities': {'required': ['mongodb.fetch', 'sink.iceberg'], 'forbidden': ['transform.filter']},
     'preserve': {'nodes': [{'id': 'src'}, {'id': 'dst'}]},
     'graph': {'requiredPaths': [[{'id': 'src'}, {'id': 'dst'}]]}},
    messages=[{'role': 'user', 'content': 'Only keep invoices with amount over 100.'},
              {'role': 'assistant', 'content': 'Proposal ready'}])

ch = {'trigger': {'type': 'manual'}, 'nodes': [
    n('events', 'source', 'iceberg.fetch', connectionId=CONN['eval-iceberg'], namespace='lake', table='events_silver'),
    n('mart', 'sink', 'sink.clickhouse', connectionId=CONN['clickhouse-analytics'], table='marts.events', layer='silver')],
    'edges': [e('events', 'mart')]}
refine('refine-lake-promote-layer', 'lake-refinement',
    'Mark the ClickHouse sink as the gold layer. Change nothing else.',
    ['eval-iceberg', 'clickhouse-analytics'], ch,
    {'configs': [{'activityType': 'sink.clickhouse', 'contains': {'table': 'marts.events', 'layer': 'gold'}}],
     'preserve': {'nodes': [{'id': 'events'}, {'id': 'mart', 'fields': ['id', 'activityType']}], 'edges': True}})

# ---- Validation / safety -------------------------------------------------
gen('lake-unknown-connection', 'lake-validation',
    'Read bucket raw-zone key orders.jsonl using S3 connection prod-datalake-s3 and write it to Iceberg connection eval-iceberg namespace lake table orders.',
    ['eval-iceberg'], {'status': 'needs_input'})
refine('lake-refine-cycle', 'lake-validation',
    'Make the Iceberg sink feed back into the Postgres source so the pipeline loops forever.',
    ['warehouse-read', 'eval-s3'], pg_s3, {'status': 'rejected'})

suite = {'version': '2.0.0-datalake',
         'description': 'Lakehouse/medallion generation, multi-turn chat, G11-safe refinement and validation cases. '
                        'Refinement seeds use the connector ids the isolated eval harness seeds for fixtures.connectors '
                        '(00000000-0000-4000-8000-<index+1>); remap them for a live tenant. Catalog configKeys are derived from codedAIFields.',
         'catalog': {'version': 'coded-catalog-v2', 'nodeTypes': types, 'commonConfigKeys': [], 'configKeys': keys},
         'fixtures': v1['fixtures'], 'cases': cases}
out = ROOT/'tests/ai-evals/cases/v2-datalake.json'
out.write_text(json.dumps(suite, indent=2) + '\n')
print(len(cases), 'cases ->', out)
