#!/usr/bin/env python3
"""Run unchanged repository scorer against isolated exact-main AI handlers."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import urllib.request
sys.dont_write_bytecode = True

ROOT = Path('/private/tmp/cohestra-plan-20260914')
OUT = Path('/private/tmp/cohestra-eval-20260909')
parser = argparse.ArgumentParser()
parser.add_argument('model')
parser.add_argument('--label', required=True)
parser.add_argument('--limit', type=int)
args = parser.parse_args()
if not args.label.replace('-', '').replace('_', '').isalnum():
    parser.error('label must contain only letters, digits, hyphens or underscores')
directory = OUT / args.label
directory.mkdir(exist_ok=False)
env = dict(os.environ, OLLAMA_MODEL=args.model, OLLAMA_URL='http://127.0.0.1:11434', OLLAMA_THINK='false', EVAL_OLLAMA_LOG=str(directory/'ollama.jsonl'))
source_paths = [ROOT/'apps/workflow-go/internal/api/routes_ai.go', ROOT/'apps/workflow-go/internal/api/ai_grounding.go', ROOT/'apps/workflow-go/internal/api/pipeline_validation.go', ROOT/'tests/ai-evals/run.py', ROOT/'tests/ai-evals/cases/v1.json']
def resource_snapshot():
 result = {'capturedAt':time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime())}
 for key, command in [('physicalMemoryBytes',['sysctl','-n','hw.memsize']),('swapUsage',['sysctl','vm.swapusage'])]:
  result[key] = subprocess.check_output(command,text=True).strip()
 with urllib.request.urlopen('http://127.0.0.1:11434/api/ps',timeout=10) as response:
  result['ollamaProcesses'] = json.load(response)
 return result
provenance = {
 'commit': '857f36f51d9d58c05b32a4d2941448b1eeebbcbd',
 'checkoutHead': subprocess.check_output(['git','-C',str(ROOT),'rev-parse','HEAD'],text=True).strip(),
 'harnessBinarySha256': hashlib.sha256((OUT/'ai-harness.test').read_bytes()).hexdigest(),
 'buildGoVersion': 'go1.25.12 darwin/arm64',
 'model':args.model,
 'options':{'temperature':0,'seed':42,'num_ctx':4096,'think':False},
 'sourceSha256':{str(p.relative_to(ROOT)):hashlib.sha256(p.read_bytes()).hexdigest() for p in source_paths},
 'method':'Go overlay adds fixture setup and HTTP server; unmodified main AI handlers and evaluator; coded catalog; 12 synthetic connector records; injected tenant; no auth or workflow execution.',
 'resourcesBefore':resource_snapshot(),
}
for endpoint in ['version','tags']:
 with urllib.request.urlopen('http://127.0.0.1:11434/api/'+endpoint,timeout=10) as response:
  metadata = json.load(response)
  if endpoint == 'tags': metadata = {'models':[m for m in metadata['models'] if m.get('name')==args.model or m.get('model')==args.model]}
  provenance['ollama'+endpoint.title()] = metadata
(directory/'provenance.json').write_text(json.dumps(provenance,indent=2)+'\n')
with (directory/'server.log').open('w') as server_log:
 server = subprocess.Popen([str(OUT/'ai-harness.test'),'-test.run=^TestEvalHTTPHarness$','-test.timeout=0','-test.v'],env=env,stdout=server_log,stderr=subprocess.STDOUT)
 try:
  for _ in range(50):
   if server.poll() is not None: raise RuntimeError('harness failed; see server.log')
   try:
    with urllib.request.urlopen('http://127.0.0.1:14000/health',timeout=1): break
   except OSError: time.sleep(.2)
  else: raise RuntimeError('harness health timeout')
  spec = importlib.util.spec_from_file_location('ai_eval', ROOT/'tests/ai-evals/run.py')
  runner = importlib.util.module_from_spec(spec)
  spec.loader.exec_module(runner)
  original_call = runner.call_api
  def recorded_call(*params, **kwargs):
   case = params[2]
   print('START '+case['id'],flush=True)
   try:
    response, latency = original_call(*params, **kwargs)
   except Exception as error:
    print('ERROR '+case['id']+' '+str(error),flush=True)
    raise
   with (directory/'responses.jsonl').open('a') as log:
    log.write(json.dumps({'id':case['id'],'response':response,'latencyMs':latency})+'\n')
   print('DONE '+case['id']+' '+runner.response_status(response)+' '+str(round(latency))+'ms',flush=True)
   return response,latency
  runner.call_api = recorded_call
  os.environ['AI_EVAL_MODEL'] = args.model
  os.environ['AI_EVAL_PROMPT_VERSION'] = 'main-'+provenance['commit'][:12]
  sys.argv = [str(ROOT/'tests/ai-evals/run.py'),'--base-url','http://127.0.0.1:14000','--timeout','500','--output',str(directory/'report.json')]
  if args.limit: sys.argv += ['--limit',str(args.limit)]
  code = runner.main()
 finally:
  server.terminate()
  try: server.wait(timeout=10)
  except subprocess.TimeoutExpired: server.kill(); server.wait()
  try:
   provenance['resourcesAfter'] = resource_snapshot()
   (directory/'provenance.json').write_text(json.dumps(provenance,indent=2)+'\n')
  except Exception as error:
   print('Resource snapshot failed: '+str(error),flush=True)
sys.exit(code)
