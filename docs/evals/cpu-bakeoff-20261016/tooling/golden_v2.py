#!/usr/bin/env python3
"""Golden model outputs for v2-datalake; serve them as a fake Ollama to prove each case is passable through the real API."""
import json, sys
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
SUITE = json.loads(Path('/home/user/cohestra-dataflow/tests/ai-evals/cases/v2-datalake.json').read_text())
C = {f['displayName']: f'00000000-0000-4000-8000-{i+1:012d}' for i, f in enumerate(SUITE['fixtures']['connectors'])}
def N(id, act, label=None, ingestion=None, **config):
    d = {'id': id, 'label': label or id, 'activityType': act, 'config': config}
    if ingestion: d['ingestion'] = ingestion
    return d
def E(s, t): return {'source': s, 'target': t}
def ready(nodes, edges, trigger=None, execution=None):
    d = {'status': 'ready', 'reason': '', 'suggestedName': 'golden', 'questions': [], 'assumptions': [], 'warnings': [],
         'trigger': trigger or {'type': 'manual'}, 'nodes': nodes, 'edges': edges}
    if execution: d['execution'] = execution
    return d
def ask(q): return {'status': 'needs_input', 'reason': '', 'suggestedName': '', 'questions': [q], 'assumptions': [], 'warnings': [], 'trigger': {'type': 'manual'}, 'nodes': [], 'edges': []}
def reject(r): return {'status': 'rejected', 'reason': r, 'suggestedName': '', 'questions': [], 'assumptions': [], 'warnings': [r], 'trigger': {'type': 'manual'}, 'nodes': [], 'edges': []}
def seed(case, *extra_nodes, edges, drop=()):
    d = case['request']['definition']
    nodes = [{'id': n['id'], 'label': n['id'], 'activityType': n['activityType'], 'config': n['config']} for n in d['nodes'] if n['id'] not in drop]
    return nodes + list(extra_nodes), edges
schema = '{"type":"object","required":["id","email"]}'
G = {
 'lake-cdc-contract-iceberg': ready([N('n1','postgres.fetch',connectionId=C['warehouse-read'],table='public.customers',syncMode='cdc'),
    N('n2','transform.contract',schemaJson=schema,onViolation='quarantine'),
    N('n3','sink.iceberg',connectionId=C['eval-iceberg'],namespace='lake',table='customers_silver')],[E('n1','n2'),E('n2','n3')]),
 'lake-silver-to-gold-clickhouse': ready([N('n1','iceberg.fetch',connectionId=C['eval-iceberg'],namespace='lake',table='orders_silver'),
    N('n2','transform.filter',predicate="r.status === 'completed'"),N('n3','transform.formula',outputField='revenue',expression='r.price * r.quantity'),
    N('n4','sink.clickhouse',connectionId=C['clickhouse-analytics'],table='gold.daily_revenue',layer='gold')],[E('n1','n2'),E('n2','n3'),E('n3','n4')]),
 'lake-join-orders-payments': ready([N('n1','postgres.fetch',connectionId=C['warehouse-read'],table='public.orders'),
    N('n2','mongodb.fetch',connectionId=C['reporting-mongo'],collection='payments'),N('n3','flow.merge',mergeStrategy='leftJoin',joinKey='order_id'),
    N('n4','sink.iceberg',connectionId=C['eval-iceberg'],namespace='lake',table='order_payments')],[E('n1','n3'),E('n2','n3'),E('n3','n4')]),
 'lake-s3-flatten-snowflake': ready([N('n1','s3.fetch',connectionId=C['eval-s3'],bucket='raw-zone',key='events/2026/10/15.jsonl',format='jsonl'),
    N('n2','transform.flatten',delimiter='_'),N('n3','transform.rename',mapping={'usr_id':'user_id'}),
    N('n4','sink.snowflake',connectionId=C['snowflake-analytics'],table='ANALYTICS.EVENTS_SILVER')],[E('n1','n2'),E('n2','n3'),E('n3','n4')]),
 'lake-medallion-fork-zones': ready([N('n1','http.fetch',url='https://api.example.test/telemetry'),N('n2','flow.fork'),
    N('n3','sink.s3',connectionId=C['eval-s3'],bucket='lake-bronze',key='telemetry/raw.jsonl',format='jsonl',layer='bronze'),
    N('n4','transform.dedupe',key='device_id',keep='last'),N('n5','sink.iceberg',connectionId=C['eval-iceberg'],namespace='lake',table='telemetry_silver'),
    N('n6','transform.filter',predicate="r.severity === 'critical'"),N('n7','sink.clickhouse',connectionId=C['clickhouse-analytics'],table='gold.critical_alerts',layer='gold')],
    [E('n1','n2'),E('n2','n3'),E('n2','n4'),E('n4','n5'),E('n2','n6'),E('n6','n7')]),
 'lake-kafka-parse-dedupe-iceberg': ready([N('n1','kafka.fetch',connectionId=C['eval-kafka'],topic='payments.v2',startPosition='earliest'),
    N('n2','transform.parse',fields='amount,currency',onError='skip'),N('n3','transform.dedupe',key='payment_id',keep='first',scope='pipeline'),
    N('n4','sink.iceberg',connectionId=C['eval-iceberg'],namespace='lake',table='payments_bronze')],[E('n1','n2'),E('n2','n3'),E('n3','n4')]),
 'lake-spark-sql-gold': ready([N('n1','iceberg.fetch',connectionId=C['eval-iceberg'],namespace='lake',table='events_silver'),
    N('n2','sink.clickhouse',connectionId=C['clickhouse-analytics'],table='gold.daily_events',layer='gold')],[E('n1','n2')],
    execution={'engine':'spark-sql','transformSql':'SELECT date(ts) AS day, count(*) AS events FROM input GROUP BY date(ts)'}),
 'lake-nightly-incremental': ready([N('n1','mysql.fetch',connectionId=C['shop-mysql'],table='shop.orders',cursorColumn='updated_at',cursorType='date'),
    N('n2','sink.postgres',connectionId=C['warehouse-read'],table='lake.orders_silver',writeMode='upsert',conflictKey='order_id')],[E('n1','n2')],
    trigger={'type':'cron','schedule':'15 1 * * *'}),
 'chat-answer-followup': ready([N('n1','postgres.fetch',connectionId=C['warehouse-read'],table='public.orders'),N('n2','transform.filter',predicate="r.status === 'completed'"),
    N('n3','sink.iceberg',connectionId=C['eval-iceberg'],namespace='lake',table='orders_completed')],[E('n1','n2'),E('n2','n3')]),
 'chat-vague-lake': ask('Which source connections and destination lake tables should this pipeline use?'),
 'lake-unknown-connection': ask('No saved S3 connection named prod-datalake-s3 exists. Which saved S3 connection should be used?'),
 'lake-refine-cycle': reject('Pipeline cycles are not executable.'),
}
cases = {c['id']: c for c in SUITE['cases']}
c = cases['refine-lake-insert-dedupe']; n, _ = seed(c, N('dd','transform.dedupe',key='event_id',keep='last'), edges=[]); G[c['id']] = ready(n, [E('src','dd'), E('dd','bronze')])
c = cases['refine-lake-swap-sink-iceberg']; n, _ = seed(c, N('silver','sink.iceberg',connectionId=C['eval-iceberg'],namespace='lake',table='orders_silver'), edges=[], drop=('landing',)); G[c['id']] = ready(n, [E('orders','completed'), E('completed','silver')])
c = cases['refine-lake-add-gold-branch']; n, _ = seed(c, N('gold','sink.clickhouse',connectionId=C['clickhouse-analytics'],table='gold.big_orders',layer='gold'), edges=[]); G[c['id']] = ready(n, [E('silver_in','big'), E('big','silver_out'), E('big','gold')])
c = cases['refine-chat-revert-filter']; n, _ = seed(c, edges=[], drop=('amt',)); G[c['id']] = ready(n, [E('src','dst')])
c = cases['refine-lake-promote-layer']; n, _ = seed(c, edges=[]); n[1]['config'] = dict(n[1]['config'], layer='gold'); G[c['id']] = ready(n, [E('events','mart')])
assert set(G) == set(cases), set(cases) ^ set(G)

class H(BaseHTTPRequestHandler):
    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        user = body['messages'][-1]['content']
        hits = [cid for cid, case in cases.items() if case['request']['prompt'] in user]
        out = G[hits[0]] if len(hits) == 1 else {'status': 'bogus'}
        data = json.dumps({'message': {'content': json.dumps(out)}}).encode()
        self.send_response(200); self.send_header('Content-Type', 'application/json'); self.send_header('Content-Length', str(len(data))); self.end_headers(); self.wfile.write(data)
    def log_message(self, *a): pass
HTTPServer(('127.0.0.1', int(sys.argv[1])), H).serve_forever()
