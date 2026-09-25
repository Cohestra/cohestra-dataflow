#!/usr/bin/env python3
"""Compare recorded runs; identify infrastructure failures separately."""
import argparse
import collections
import json
from pathlib import Path

ROOT = Path('/private/tmp/cohestra-eval-20260909')
parser = argparse.ArgumentParser()
parser.add_argument('--candidate', default='granite42-8b-candidate')
args = parser.parse_args()
candidate = ROOT / args.candidate
baseline = ROOT / 'qwen3-8b-baseline'
def read_json(path):
 return json.loads(path.read_text())
def read_lines(path):
 return [json.loads(line) for line in path.read_text().splitlines()]
b_report, c_report = [read_json(p/'report.json') for p in (baseline,candidate)]
b_prov, c_prov = [read_json(p/'provenance.json') for p in (baseline,candidate)]
b_calls, c_calls = [read_lines(p/'ollama.jsonl') for p in (baseline,candidate)]
expected_options = {'temperature':0,'seed':42,'num_ctx':4096}
model = c_prov['model']
checks = {
 'applicationCommitEqual':b_prov['commit']==c_prov['commit'],
 'sourceHashesEqual':b_prov['sourceSha256']==c_prov['sourceSha256'],
 'binarySha256Equal':b_prov['harnessBinarySha256']==c_prov['harnessBinarySha256'],
 'allActualModelIdentitiesVerified':bool(c_calls) and all(c['request']['model']==model and c.get('response',{}).get('model')==model for c in c_calls),
 'allRequestOptionsVerified':bool(c_calls) and all(c['request']['options']==expected_options and c['request']['think'] is False for c in c_calls),
 'firstRequestApartFromModelEqual':({k:v for k,v in b_calls[0]['request'].items() if k!='model'} == {k:v for k,v in c_calls[0]['request'].items() if k!='model'}),
}
models = c_prov['ollamaTags']['models']
assert len(models)==1 and models[0]['name']==model
digest = models[0]['digest']
checks['modelRegistryDigestMatchesExpected'] = digest=='f586c02fdecdf151b656207c339aa003997345774a41768bac1fd6d2fb85913b'
checks['modelWeightMetadataMatchesExpected'] = models[0]['details'].get('parent_model','').endswith('sha256-16a9369d0805f80b7377d25d87f937a90c05dc04ad79173a52001e42c9aab311')
loaded = c_prov.get('resourcesAfter',{}).get('ollamaProcesses',{}).get('models',[])
checks['loadedDigestMatchesRegistry'] = any(m['name']==model and m['digest']==digest for m in loaded)
checks['loadedContext4096'] = any(m['name']==model and m['context_length']==4096 for m in loaded)
show_path = candidate/'model-runtime-show.json'
post_run_show = read_json(show_path) if show_path.exists() else None
if post_run_show:
 checks['postRunShowDigestMatchesRegistry'] = post_run_show['modelRegistryDigestAtCapture']==digest
infra = {item['id']:item['error'] for item in c_report['results'] if 'load tenant connector instances:' in item.get('error','')}
assert len(b_report['results'])==len(c_report['results'])==31
b_by_id = {item['id']:item for item in b_report['results']}
assert set(b_by_id)=={item['id'] for item in c_report['results']}
pairs = []
for item in c_report['results']:
 prior = b_by_id[item['id']]
 pairs.append({'id':item['id'],'category':item['category'],
   'baselinePassed':prior['passed'],'candidatePassedRaw':item['passed'],
   'candidateInfrastructureFailure':item['id'] in infra,
   'candidateError':item.get('error'),
   'candidateStatus':item.get('responseStatus')})
def token_observations(calls):
 responses = [call.get('response',{}) for call in calls]
 return {
  'maxPromptEvalCount':max((r.get('prompt_eval_count',0) for r in responses),default=0),
  'maxGeneratedTokenCount':max((r.get('eval_count',0) for r in responses),default=0),
  'maxPromptPlusGeneratedTokensPerCall':max((r.get('prompt_eval_count',0)+r.get('eval_count',0) for r in responses),default=0),
  'doneReasonCounts':dict(collections.Counter(r.get('done_reason','missing') for r in responses)),
  'explicitRuntimeContextOrTruncationErrors':[r['error'] for r in responses if isinstance(r.get('error'),str) and ('context' in r['error'].lower() or 'truncat' in r['error'].lower())],
  'interpretation':'Token counts and stop reasons are observations, not proof of input truncation. Server-side prompt-truncation logs were not captured. Identical prompt bytes tokenize differently across models.',
 }
diagnostics = {
 'evaluationValidity':'invalid-infrastructure-interruption' if infra else 'completed-with-known-corpus-conflicts',
 'summary':c_report['summary'],
 'httpFailures':sum('error' in x for x in c_report['results']),
 'infrastructureFailureCount':len(infra),
 'modelCalls':len(c_calls),
 'modelGeneratedTokens':sum(c.get('response',{}).get('eval_count',0) for c in c_calls),
 'modelPromptTokens':sum(c.get('response',{}).get('prompt_eval_count',0) for c in c_calls),
 'allActualModelIdentitiesVerified':checks['allActualModelIdentitiesVerified'],
 'allRequestOptionsVerified':checks['allRequestOptionsVerified'],
 'apiStatusCounts':dict(collections.Counter(item.get('responseStatus','HTTPerror') for item in c_report['results'])),
 'passedCaseIds':[item['id'] for item in c_report['results'] if item['passed']],
 'byCategory':{category:{'cases':stats['cases'],'passedRaw':stats['passed'],'infrastructureFailures':sum(item['category']==category and item['id'] in infra for item in c_report['results'])} for category,stats in c_report['byCategory'].items()},
 'modelRegistryDigest':digest,
 'initialTagsCapabilities':models[0].get('capabilities',[]),
 'postRunShowCapabilities':post_run_show.get('capabilities',[]) if post_run_show else None,
 'postRunShowTemplateSha256':post_run_show.get('templateSha256') if post_run_show else None,
 'capabilitiesInterpretation':'Capabilities are labeled by endpoint and collection time. Metadata is not a tool-calling benchmark; /api/tags may report a different list from /api/show.',
 'modelWeightSha256FromMetadata':models[0]['details'].get('parent_model','').split('sha256-')[-1],
 'checks':checks,
 'tokenAndStopObservations':token_observations(c_calls),
 'limitations':[
  'Raw per-case results include infrastructure failures; do not label these model accuracy.' if infra else 'Six corpus/contract conflicts prohibit using v1 as a promotion gate.',
  'Three passing safety rejections happen before a model call.',
  'Non-pass summary metrics omit HTTP failures; use all-case pass count only when infrastructure is healthy.',
  'Different run dates and high host swap prevent a controlled latency comparison.',
  'Exact-main AI handler harness bypasses authentication and workflow execution.',
  'This suite evaluates pipeline planning, not agent tool calling or execution of generated pipelines.',
 ],
}
comparison = {
 'candidateDirectory':str(candidate),'baselineModel':b_prov['model'],'candidateModel':model,
 'comparisonValidForAccuracyRanking':False,
 'historicalPairedResultsComparable':not infra and all(checks.values()),
 'reason':f'Fixture database was unreachable for {len(infra)} cases, which did not reach model.' if infra else 'Historical paired corpus is preserved but six contract conflicts require v2 before promotion.',
 'baselineRawPassed':b_report['summary']['passed'],'candidateRawPassed':c_report['summary']['passed'],
 'candidateInfrastructureFailures':len(infra),'provenanceChecks':checks,
 'pairedCases':pairs,
 'candidateInitialTagsCapabilities':models[0].get('capabilities',[]),
 'candidatePostRunShowCapabilities':post_run_show.get('capabilities',[]) if post_run_show else None,
 'tokenAndStopObservations':{'baseline':token_observations(b_calls),'candidate':token_observations(c_calls)},
 'pairedOutcomeCounts':dict(collections.Counter('candidate-infrastructure-failure' if p['candidateInfrastructureFailure'] else 'both-pass' if p['baselinePassed'] and p['candidatePassedRaw'] else 'candidate-only-pass' if p['candidatePassedRaw'] else 'baseline-only-pass' if p['baselinePassed'] else 'both-fail' for p in pairs)),
}
native_path = candidate/'native-db-provenance.json'
if native_path.exists():
 diagnostics['databaseRuntimeProvenance'] = read_json(native_path)
 diagnostics['limitations'].append('Baseline used Docker PostgreSQL16; candidate used isolated native PostgreSQL17.10. Database deployment and host state changed, so latency is not a controlled comparison.')
 comparison['databaseDeploymentChanged'] = True
 comparison['latencyComparisonValid'] = False
(candidate/'diagnostics.json').write_text(json.dumps(diagnostics,indent=2)+'\n')
(candidate/'comparison.json').write_text(json.dumps(comparison,indent=2)+'\n')
print(json.dumps({k:v for k,v in diagnostics.items() if k!='limitations'},indent=2))
