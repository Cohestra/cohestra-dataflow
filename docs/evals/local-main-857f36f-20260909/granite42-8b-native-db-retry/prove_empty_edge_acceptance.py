#!/usr/bin/env python3
"""Offline counterexample using the original evaluator and retained API output."""
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import sys

sys.dont_write_bytecode = True
SOURCE = Path('/private/tmp/cohestra-plan-20260914/tests/ai-evals')
HERE = Path(__file__).resolve().parent
provenance = json.loads((HERE/'provenance.json').read_text())
source_hashes = {}
for relative, path in [('tests/ai-evals/run.py',SOURCE/'run.py'),('tests/ai-evals/cases/v1.json',SOURCE/'cases/v1.json')]:
 source_hashes[relative] = hashlib.sha256(path.read_bytes()).hexdigest()
 assert source_hashes[relative]==provenance['sourceSha256'][relative], 'source changed since recorded run'
spec = importlib.util.spec_from_file_location('original_eval', SOURCE/'run.py')
evaluator = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evaluator)
suite = evaluator.load_suite(SOURCE/'cases/v1.json')
case = next(c for c in suite['cases'] if c['id']=='gen-http-filter-s3')
record = next(json.loads(line) for line in (HERE/'responses.jsonl').read_text().splitlines()
              if json.loads(line)['id']==case['id'])
assert record['response']['definition']['edges'] is None
assert len(record['response']['definition']['nodes'])==3
response = copy.deepcopy(record['response'])
response['definition']['edges'] = []
scored = evaluator.score_case(case,response,record['latencyMs'],suite['catalog'])
assert scored['passed'] and scored['structuralValid'], scored
proof = json.loads((HERE/'empty-edge-scorer-proof.json').read_text())
proof['sourceSha256'] = source_hashes
proof['actualResponseUnchanged'] = True
proof['runnableCheck'] = str(Path(__file__).resolve())
report_path = HERE/'report.json'
if report_path.exists():
 original_case = next(c for c in json.loads(report_path.read_text())['results'] if c['id']==case['id'])
 assert original_case['passed'] is False
 proof['actualReportCasePassed'] = original_case['passed']
 proof['actualReportSha256'] = hashlib.sha256(report_path.read_bytes()).hexdigest()
proof['actualResponseJournalSha256'] = hashlib.sha256((HERE/'responses.jsonl').read_bytes()).hexdigest()
(HERE/'empty-edge-scorer-proof.json').write_text(json.dumps(proof,indent=2)+'\n')
print('Confirmed: disconnected three-node graph passes v1 after changing only edges:null to edges:[].')
