#!/usr/bin/env python3
"""Run tests/ai-evals/run.py against the real AI handlers (Go overlay harness) for one Ollama model."""
import argparse, importlib.util, json, os, subprocess, sys, time, urllib.request
from pathlib import Path
sys.dont_write_bytecode = True
ROOT = Path('/home/user/cohestra-dataflow')
BAKE = Path(__file__).resolve().parent.parent
p = argparse.ArgumentParser()
p.add_argument('model'); p.add_argument('--label', required=True)
p.add_argument('--cases', default=str(ROOT/'tests/ai-evals/cases/v1.json'))
p.add_argument('--timeout', default='600')
a = p.parse_args()
d = BAKE/'runs'/a.label; d.mkdir(parents=True, exist_ok=False)
env = dict(os.environ, OLLAMA_MODEL=a.model, OLLAMA_URL='http://127.0.0.1:11434', OLLAMA_THINK='false', EVAL_OLLAMA_LOG=str(d/'ollama.jsonl'))
with urllib.request.urlopen('http://127.0.0.1:11434/api/show', data=json.dumps({'model': a.model}).encode()) as r:
    show = json.load(r)
prov = {'commit': subprocess.check_output(['git','-C',str(ROOT),'rev-parse','HEAD'],text=True).strip(), 'model': a.model,
        'details': show.get('details'), 'modelInfo': {k:v for k,v in show.get('model_info',{}).items() if 'parameter_count' in k or 'context_length' in k},
        'options': {'temperature':0,'seed':42,'num_ctx':4096,'think':False}, 'cases': a.cases, 'host': '4 vCPU, 15 GiB RAM, CPU-only'}
(d/'provenance.json').write_text(json.dumps(prov, indent=2)+'\n')
with (d/'server.log').open('w') as log:
    srv = subprocess.Popen([str(BAKE/'scripts/ai-harness.test'),'-test.run=^TestEvalHTTPHarness$','-test.timeout=0','-test.v'], env=env, stdout=log, stderr=subprocess.STDOUT, cwd=str(ROOT/'apps/workflow-go/internal/api'))
    try:
        for _ in range(100):
            if srv.poll() is not None: raise RuntimeError('harness failed; see server.log')
            try:
                urllib.request.urlopen('http://127.0.0.1:14000/health', timeout=1); break
            except OSError: time.sleep(.2)
        else: raise RuntimeError('harness health timeout')
        spec = importlib.util.spec_from_file_location('ai_eval', ROOT/'tests/ai-evals/run.py')
        runner = importlib.util.module_from_spec(spec); spec.loader.exec_module(runner)
        orig = runner.call_api
        def rec(*params, **kw):
            case = params[2]
            resp, lat = orig(*params, **kw)
            with (d/'responses.jsonl').open('a') as f: f.write(json.dumps({'id':case['id'],'response':resp,'latencyMs':lat})+'\n')
            print(f"  {a.label} {case['id']}: {runner.response_status(resp)} {lat/1000:.0f}s", flush=True)
            return resp, lat
        runner.call_api = rec
        os.environ['AI_EVAL_MODEL'] = a.model
        os.environ['AI_EVAL_PROMPT_VERSION'] = 'main-'+prov['commit'][:12]
        sys.argv = ['run.py','--base-url','http://127.0.0.1:14000','--timeout',a.timeout,'--cases',a.cases,'--output',str(d/'report.json')]
        code = runner.main()
    finally:
        srv.terminate(); srv.wait(timeout=10)
sys.exit(0)
