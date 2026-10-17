#!/usr/bin/env python3
"""Rank runs: pass rate, then mean sub-score, then latency. Usage: rank.py <runs-glob-dir> [suffix] [--top N]"""
import json, sys, glob, os
runs, suffix = sys.argv[1], (sys.argv[2] if len(sys.argv) > 2 and not sys.argv[2].startswith('--') else '')
top = int(sys.argv[sys.argv.index('--top')+1]) if '--top' in sys.argv else None
rows = []
for rep in glob.glob(f'{runs}/*{suffix}/report.json'):
    label = os.path.basename(os.path.dirname(rep))
    if label.startswith('smoke') or (not suffix and label.startswith('v2-')): continue
    r = json.load(open(rep)); s = r['summary']; res = r['results']
    subs = [s[k] for k in ('schemaValidRate','structuralValidRate','activityAccuracyRate','groundingAccuracyRate','preservationAccuracyRate','clarificationAccuracyRate') if s.get(k) is not None]
    errors = sum(1 for c in res if c.get('error'))
    timeouts = sum(1 for c in res if 'unavailable' in str(c.get('error','')) or 'timed out' in str(c.get('error','')))
    model = json.load(open(os.path.join(os.path.dirname(rep), 'provenance.json')))['model']
    rows.append(dict(label=label, model=model, passed=s['passed'], cases=s['cases'], score=sum(subs)/len(subs) if subs else 0,
                     lat=(s['latencyMsMean'] or 0)/1000, p95=(s['latencyMsP95'] or 0)/1000, errors=errors, timeouts=timeouts, s=s))
rows.sort(key=lambda r: (-r['passed'], -r['score'], r['lat']))
if top: print('\n'.join(r['model'] for r in rows[:top])); sys.exit()
f = lambda v: '—' if v is None else f'{v*100:.0f}%'
print('| # | Model | Passed | Sub-score | Schema | Struct | Activity | Grounding | Preserve | Clarify | Errors (timeouts) | Mean / P95 latency |')
print('|---|---|---|---|---|---|---|---|---|---|---|---|')
for i, r in enumerate(rows, 1):
    s = r['s']
    print(f"| {i} | `{r['model']}` | {r['passed']}/{r['cases']} | {r['score']*100:.0f}% | {f(s['schemaValidRate'])} | {f(s['structuralValidRate'])} | {f(s['activityAccuracyRate'])} | {f(s['groundingAccuracyRate'])} | {f(s['preservationAccuracyRate'])} | {f(s['clarificationAccuracyRate'])} | {r['errors']} ({r['timeouts']}) | {r['lat']:.0f}s / {r['p95']:.0f}s |")
