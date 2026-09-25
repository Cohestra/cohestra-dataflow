#!/usr/bin/env python3
"""Offline proof: v1 refinement preservation conflicts with main grounding."""
import copy
import importlib.util
from io import BytesIO
import json
from pathlib import Path
import re
import sys
from unittest.mock import patch
sys.dont_write_bytecode = True

ROOT = Path('/private/tmp/cohestra-plan-20260909')
OUT = Path('/private/tmp/cohestra-eval-20260909')
spec = importlib.util.spec_from_file_location('ai_eval', ROOT/'tests/ai-evals/run.py')
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)
suite = runner.load_suite(ROOT/'tests/ai-evals/cases/v1.json')
source = (ROOT/'apps/workflow-go/internal/api/ai_grounding.go').read_text()
required_map = source.split('var codedAIRequiredFields',1)[1].split('func codedFields',1)[0]
requires_connection = {
 activity for activity, fields in re.findall(r'"([^"]+)":\s*\{([^}]+)\}', required_map)
 if '"connectionId"' in fields
}
conflicts = []
for case in suite['cases']:
 if case['endpoint'] != '/api/ai/refine':
  continue
 # Exercise the original request preparation path: no fixture binding occurs.
 with patch('urllib.request.urlopen', return_value=BytesIO(b'{}')) as opened:
  runner.call_api('http://offline.invalid',None,case,1)
 request = json.loads(opened.call_args.args[0].data)
 assert request == case['request'], 'request path now performs a transformation'
 after = copy.deepcopy(request['definition'])
 nodes = {node['id']:node for node in after['nodes']}
 invalid_nodes = []
 for expected in case['expect'].get('preserve',{}).get('nodes',[]):
  node = nodes[expected['id']]
  fields = expected.get('fields',['id','activityType','config'])
  if ('config' in fields and node['activityType'] in requires_connection
      and not node.get('config',{}).get('connectionId')):
   invalid_nodes.append({'node':node['id'],'activityType':node['activityType']})
   node['config']['connectionId'] = '00000000-0000-4000-8000-000000000001'
 if invalid_nodes:
  score = runner.preservation_score(case,{'definition':after})
  assert score < 1, 'adding mandatory connectionId unexpectedly preserves config'
  preservation_only_case = dict(case,expect={'preserve':case['expect']['preserve']})
  scored = runner.score_case(preservation_only_case,{'definition':after},1,suite['catalog'])
  assert scored['schemaValid'] and scored['structuralValid'] and scored['groundingAccuracy']==1
  assert not scored['passed'], 'scorer unexpectedly accepts preservation below one'
  conflicts.append({'case':case['id'],'missingRequiredConnection':invalid_nodes,
                    'preservationScoreAfterOnlyAddingRequiredIds':score,
                    'preservationOnlyCasePassed':scored['passed'],
                    'outgoingRequestUnmodified':True})
assert len(conflicts) == 5, conflicts
assert sum(len(item['missingRequiredConnection']) for item in conflicts) == 6

# Audit expected config keys and enum values against the coded AI catalog.
# Every catalog entry is a single Go map line in this pinned source; assert
# catalog coverage so a source formatting change cannot silently hide entries.
catalog_section = source.split('var codedAIFields',1)[1].split('var codedAIRequiredFields',1)[0]
allowed = {}
enums = {}
for activity, body in re.findall(r'^\s*"([^"]+)":\s*\{(.*)\},?$',catalog_section,re.M):
 allowed[activity] = set()
 for arguments in re.findall(r'field\(([^)]*)\)',body):
  values = re.findall(r'"([^"]*)"',arguments)
  key, kind, *options = values
  allowed[activity].add(key)
  if options: enums[(activity,key)] = options
assert set(allowed) == set(runner.activity_node_types(suite['catalog'])), 'catalog extraction incomplete'
config_collisions = []
for case in suite['cases']:
 expectations = [(e['activityType'],e.get('contains',{})) for e in case['expect'].get('configs',[])]
 expectations += [(e['activityType'],e.get('contains',{}).get('config',{})) for e in case['expect'].get('nodes',[])]
 for activity, expected in expectations:
  for key, value in expected.items():
   if key not in allowed[activity]:
    config_collisions.append({'case':case['id'],'activityType':activity,'key':key,'expected':value,'problem':'expected key forbidden by main AI grounding'})
   elif (activity,key) in enums and str(value) not in enums[(activity,key)]:
    config_collisions.append({'case':case['id'],'activityType':activity,'key':key,'expected':value,'allowed':enums[(activity,key)],'problem':'expected value forbidden by main AI grounding'})
assert any(c['case']=='gen-postgres-snowflake' and c['key']=='mode' for c in config_collisions)

# Confirm a live request has the same missing ID after the real HTTP handler.
log = OUT/'qwen3-8b-baseline/ollama.jsonl'
live_proof = None
if log.exists():
 for line in log.read_text().splitlines():
  record = json.loads(line)
  prompt = record['request']['messages'][-1]['content']
  if ('Before filtering, remove duplicate posts using id' not in prompt
      or 'Current pipeline JSON:' not in prompt):
   continue
  encoded = prompt.split('Current pipeline JSON:',1)[1].lstrip()
  definition, _ = json.JSONDecoder().raw_decode(encoded)
  node = next(n for n in definition['nodes'] if n['id']=='sink')
  assert 'connectionId' not in node['config']
  live_proof = {'case':'refine-insert-dedupe','actualOllamaModel':record['request']['model'],
                'actualRefineSeedSinkConfig':node['config']}
  break
 assert live_proof, 'expected live refinement request not captured yet'

report = {'applicationCommit':'857f36f51d9d58c05b32a4d2941448b1eeebbcbd',
 'suiteVersion':suite['version'],'conflictingCases':len(conflicts),
 'conflictingNodes':6,'proof':conflicts,'liveRequestProof':live_proof,
 'expectedConfigCollisions':config_collisions,
 'allCasesStaticallyAudited':len(suite['cases']),
 'interpretation':'Every v1 refinement case preserves a connector config lacking its mandatory connectionId. The original call_api sends that input unchanged. Adding the ID lowers exact-equality preservation; omitting it fails main grounding. No model can pass all five under these contracts.',
 'scope':'Offline request preparation and scorer checks plus one captured real model request; this proof does not execute model inference or modify the established suite.'}
(OUT/'corpus-conflicts.json').write_text(json.dumps(report,indent=2)+'\n')
print(json.dumps(report,indent=2))
