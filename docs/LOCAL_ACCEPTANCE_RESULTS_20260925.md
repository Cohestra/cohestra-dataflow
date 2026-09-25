# Local acceptance results — September 25, 2026

The remaining local acceptance plan was executed against the combined PR42–49
checkout, including real Chrome browser journeys and hands-on Codex browser
inspection. This is a local acceptance result, not a production readiness claim.
The application remains available at http://localhost:13002.

## Results

| Area | Observed result | Evidence under `.artifacts/local-acceptance/` |
| --- | --- | --- |
| Full Go race suites | PASS: 198 community and 200 enterprise leaf tests before the final API-only fixes | `sept25-backend/summary.json` |
| Final permission regression | PASS: 24 real application-role/RLS cases per edition; controls and monitoring regressions also pass | `sept25-backend/pipeline-access/results.json` |
| Final AI schema regression | PASS in both editions; new assertion failed before the correction | `sept25-ai-regressions.log` |
| Real Temporal/PostgreSQL sandbox | PASS: six scenarios per edition, cleanup verified | `sept25-backend/temporal/summary.json` |
| Shared/web unit tests, evaluator and sandbox self-checks | PASS | `sept25-offline/results.json` |
| Frontend production build and existing mocked browser regression | PASS | `sept25-web-build.log`, `sept25-mock-browser.log` |
| Responsive review and Save | PASS after layout fix at 430×932 and 1440×1000; supplemental narrow keyboard Apply/Undo/Discard passes with mocked APIs | `sept25-responsive/summary.json` |
| Real controls API | PASS: 32 checks, including exact six-record output, pause boundaries, duplicate intent, permissions, terminality and reserved-agent admission | `full-controls/controls.json` |
| Real browser controls | PASS: editor run, pause, visible viewer 403, resume, completed buttons disabled, refresh/reopen paused run, cancel, empty output | `full-controls/controls-browser.json`, `sept25-controls-browser.log` |
| Real browser drag/save/reopen | PASS: full definition preserved after pointer drag | `sept25-runtime-browser/runtime-browser.json` |
| UI-started timeout/retry | PASS: explicit one-second timeout, second activity attempt, exhausted retries, failed UI state, disabled controls, zero sink records | `sept25-runtime-browser/timeout-history.json` |
| Mermaid editing | PASS after final API fixes: harmless label edit, cancel deletion, confirm deletion, save/reopen surviving metadata | `full-review/continuation-results.json` |
| Real AI review UI before schema fix | UI PASS: keyboard review, stale protection, Apply/save/reopen, Undo, redaction, narrow Discard and focus return. Intent preservation FAIL | `full-review/before-schema-fix/continuation-case-outcomes.json` |
| Real AI refinement after schema fix | FAIL: both requests returned 422 after one repair, missing a required pipeline name; visible error, no valid proposal | `full-review/continuation-results.json`, `sept25-review.log` |
| Hands-on monitoring and run graph | PASS: current completed run shows 12→6→6 records; correct duration, owner and last-run links; completed controls disabled | `sept25-hands-on.json` |
| Iceberg REST/MinIO append and retry | PASS with race detection in both editions; new fixture removed successfully | `sept25-backend/iceberg/summary.json` |
| Production npm audit | PASS | `sept25-offline/4.log` |
| Go vulnerability scan | PASS | `sept25-security/govulncheck.log` |
| Reachable-history secret scan | PASS: 177 commits, zero findings | `sept25-security/gitleaks-history.json` |
| Supplemental working-files secret scan | Nonzero result retained across 21 final files: planner instruction prose triggers generic-api-key; reviewed as non-secret text, not suppressed | `sept25-security/gitleaks-working-files-final.json` |

The real provider cases remain failing tests. No assertion was removed to promote
the model or to claim overall green acceptance. Earlier real proposals also
added unconditional `true` edge conditions when only a collection/URL change
was requested. The ingestion-mode change had a separate application cause,
corrected below. This is not a model accuracy benchmark.

## Corrections made and verified

- **Granted editors could not open pipelines.** The shared permission query used
  an unscoped pool connection, so row-level security hid valid grants. It now
  runs inside the existing tenant transaction. The regression reproduced 12
  failing cases before the change and verifies tenant isolation afterward.
- **AI schema contradicted the editor contract.** It allowed ingestion `cdc`
  instead of canonical `incremental`, `backfill`, and `realtime`. The enum,
  prompt guidance and preservation fixture now match the shared contract.
  Connector CDC remains `config.syncMode=cdc`.
- **Source totals were double-counted during consolidation.** Existing local
  changes now count source pages once and replace consolidated totals. The
  database regression and real 12-record pipeline verify this behavior.
- **Execution controls and monitoring lacked reliable feedback.** Existing local
  changes expose errors/request acknowledgments, disable terminal controls,
  reopen a selected historical run, and use canonical completion timestamps,
  last-run and owner fields. Browser and database checks cover these changes.

- **Save was outside the mobile viewport with AI review open.** The mobile
  header now spans the viewport in two rows. The unchanged metadata journey
  passes on narrow and desktop screens; the separate keyboard review check
  verifies Apply, Undo, Discard, focus and redaction with mocked responses.

The browser test's reopened-run assertion was corrected: after reload, the
canvas message describes the loaded pipeline version; the execution monitor
owns the cancelled status. The test still requires visible cancellation,
disabled controls, and zero sink output.

## Coverage boundaries

All local journeys above use synthetic accounts/data. Asset bindings,
notifications and policies are checked for lossless persistence; external
notification delivery and third-party connector credentials are not exercised.
Node positions are temporary canvas layout and are not persisted. Timeout/retry
policies are seeded through the API because the canvas has no policy controls.
A refreshed canvas requires selecting its run from the output panel to reopen
the execution monitor. Health remains `unknown`; no healthy/SLO guarantee is
inferred. Agent execution, approvals, memory and LangGraph remain deferred.
Production nginx/container topology, production Temporal, external paid services,
full WCAG conformance and broad model accuracy are outside this local pass.

The initial controls run during concurrent Go/model load failed before its sink.
Its exact failure is retained in `full-controls/sept25-under-load.json`. The
unchanged controls subsequently passed twice with lower concurrency. This is
not a load-test pass; other developer workloads were left running.

## Repeat the local checks

From `.local-acceptance/checkout`, with the local application running:

```sh
rtk proxy ./scripts/local-acceptance.sh smoke
rtk proxy ./scripts/local-acceptance.sh browser
rtk proxy ./scripts/local-acceptance.sh controls
rtk proxy ./scripts/local-acceptance.sh review
rtk proxy ./scripts/local-acceptance.sh runtime
```

`review` deliberately returns nonzero while the real provider cases fail.
`controls` creates fresh cursor-based fixtures before its browser journey.
`runtime` creates its own slow local source and validates actual Temporal history.
Start/stop commands and the complete test matrix are in `LOCAL_ACCEPTANCE.md`.
Private credentials remain in `.artifacts/local-acceptance/credentials.json`;
traces and keys are excluded from published evidence. Nothing was deployed or
merged remotely.
