# Local acceptance: PRs 42–49

Latest execution results and discovered fixes: [September 25 report](LOCAL_ACCEPTANCE_RESULTS_20260925.md).

This plan covers the combined local checkout. A historical PR result is not a
PASS for this checkout. Record PASS only after running the corresponding command
and retaining its output; skipped or unavailable scenarios remain NOT RUN.

## Runtime start and stop

The local setup uses native enterprise Go API/activity/workflow workers and the
built React app served by Vite preview. Infrastructure runs in the isolated
Docker Compose project `cohestra-acceptance-20260922` (override with
`COHESTRA_ACCEPTANCE_PROJECT` to run a second stack; set `ACCEPTANCE_OLLAMA_URL`
and `ACCEPTANCE_OLLAMA_MODEL` to use a different model server): PostgreSQL, Redis,
ClickHouse, a real Temporal development server with persistent SQLite, and
Temporal UI. Separate workers serve the `test` and `prod` namespaces of this
local project; neither namespace refers to a remote deployment.

The combined checkout is:
a separate local checkout (`.local-acceptance/checkout` inside the workspace),
branch `codex/local-acceptance-20260922`. It combines PR48 (including its backend
stack), PR45 (including frontend) and PR42, with CI fixes already present.
No remote branch was merged or deployed. Docker Desktop and Chrome must be
available. The installed Go toolchain and locked npm dependencies are used.

From that checkout root, keep the foreground supervisor terminal open:

```sh
./scripts/local-acceptance.sh up
```

`up` builds the native applications and frontend before starting them. To reuse
the existing builds after stopping:

```sh
./scripts/local-acceptance.sh start
```

Use another terminal for the following commands. `stop` stops applications and
infrastructure; `down` additionally removes the project's containers/network.
Both preserve its volumes and records. Do not add `--volumes` when retaining
the test environment.

```sh
./scripts/local-acceptance.sh status
./scripts/local-acceptance.sh smoke
./scripts/local-acceptance.sh browser
./scripts/local-acceptance.sh controls
./scripts/local-acceptance.sh review
./scripts/local-acceptance.sh runtime
./scripts/local-acceptance.sh responsive
./scripts/local-acceptance.sh stop
./scripts/local-acceptance.sh down
```

| Service | Local endpoint |
| --- | --- |
| Web | http://localhost:13002 |
| API | http://127.0.0.1:14000 |
| Temporal UI | http://localhost:18082 |
| PostgreSQL | 127.0.0.1:15433 |
| Redis | 127.0.0.1:16379 |
| Temporal | 127.0.0.1:17233 |
| ClickHouse HTTP | http://127.0.0.1:18123 |

Native process logs and supervisor status are retained under
`.artifacts/local-acceptance/native/`. The smoke test writes generated local
credentials to private `.artifacts/local-acceptance/credentials.json`, records
its fixture/result in `smoke.json`, and uses only synthetic records. The browser
test signs in with that account, edits a node label, saves and reopens the new
pipeline version, and compares the actual stored definition. It makes no extra
workflow run. Its `browser.json`, screenshot and failure trace remain local
artifacts; traces can contain local session headers.

Apply `db/027_execution_controls.sql` before the new API/activity worker;
register the new activities before starting the new workflow worker. Existing
database volumes do not automatically replay Compose initialization migrations.
Supervisor readiness checks the API/web endpoints and that application processes
remain alive; the smoke/browser gates establish functional acceptance separately.

The cold Docker application build exceeded its 1,200-second bound, so the local
application rollout uses this native fallback. The nginx/container application
topology and production Temporal topology are **NOT VERIFIED** by this setup.
The six offline checks, six Go checks, two database suites and all six runtime
sandbox leaves in each edition passed in the current local effort; live pipeline
and browser acceptance have now also **PASSED** (see the results ledger).

## Automated acceptance matrix

| Area | Existing check | Acceptance | Coverage limit |
| --- | --- | --- | --- |
| #42 evaluator paths | Strict Python self-test | Continuous directed paths, branch handling and malformed contracts pass; no API calls required | Does not establish model accuracy or deliver the deferred v2 corpus |
| #43 lossless edits | Shared/web tests and canvas browser regression | Save, AI Apply/Undo/Discard and Mermaid edits retain supported pipeline/node metadata and identity; stale proposal cannot replace a newer draft | Browser test intercepts API requests |
| #45 proposal review | Web proposal tests and same browser regression | Displayed changes equal the merged Apply result, include input order changes, conceal sensitive/unrecognized values; keyboard review and focus return work | One Chrome journey; broader accessibility and real planner integration remain manual |
| #44 policies/manifests | Both-edition Go tests | Validate timeout/retry values before admission; actual options apply without leaking to siblings; legacy replay passes; unsupported sink manifests are excluded/rejected | Existing histories retain their old policy behavior |
| #46 agent contracts | Shared round trip, model/API/workflow tests and DB admission tests | Strict canonical fields and bounded batch bindings round-trip; unsupported agents rejected before activities or durable trigger writes | Agent execution is intentionally unavailable |
| #47 controls | Both-edition Go SDK tests and DB control tests | Atomic intent/audit, roles/RLS, delivery recovery, revision races, legacy first resume, admission, cancellation and monotonic terminality pass | SDK alone does not prove real server delivery |
| #48 real server sandbox | Runner in both editions | All six leaf scenarios below run and pass, with history/result evidence and successful cleanup | Synthetic authentication and connectors; graceful workflow-worker replacement |
| #49 security/CI | Existing security gates | Reachable history scan, production npm high-severity audit and Go vulnerability scan pass on the combined checkout | External/public smoke remains a separate deliberate manual run |

### Offline checks and builds

Run from the checkout root after installing its locked npm dependencies.

```sh
npm ci
python3 tests/ai-evals/run.py --self-test --strict
python3 scripts/temporal-sandbox.py --self-test
npm -w @dataflow/shared test
npm -w @dataflow/web test
npm run build
```

Run from `apps/workflow-go`:

```sh
go test -race ./...
go test -race -tags ee ./...
go vet ./...
go vet -tags ee ./...
go build ./cmd/...
go build -tags ee ./cmd/...
```

General Go success is insufficient for integration acceptance: the DB tests skip
without `CONTROL_TEST_DATABASE_URL`; sandbox tests skip without the DB URL and
both sandbox Temporal variables. Inspect output for skipped tests.

### Browser regression

Chrome must be available (`npx playwright install --with-deps chrome`
is the CI installation command). From the checkout root:

```sh
npm -w @dataflow/shared run build
npm -w @dataflow/web run test:e2e -- tests/pipeline-metadata.spec.ts --workers=1
```

Playwright starts its own Vite server at `127.0.0.1:3101` and refuses to reuse an
existing server. Pass means the one synthetic API journey completed; it is not a
real API/worker test. Failure traces go under `apps/web/test-results/`.

### Database controls and admission

Set `CONTROL_TEST_DATABASE_URL` to a disposable PostgreSQL database with **all**
numbered `db/*.sql` migrations applied in order, using `psql -v ON_ERROR_STOP=1`.
Never point these checks at personal or production data. From `apps/workflow-go`:

```sh
go test -race ./internal/api ./internal/workflows -run '^Test(Agent|Control)' -count=1 -v
go test -race -tags ee ./internal/api ./internal/workflows -run '^Test(Agent|Control)' -count=1 -v
```

Pass requires both editions and no skipped matching test. The API suite has four
`TestControl...` tests plus agent backfill/webhook denial; the workflow suite has
six `TestControl...` tests plus agent admission. See
`docs/EXECUTION_CONTROLS.md` for each guarantee.

### Real Temporal/PostgreSQL sandbox

From the checkout root, choose a new/empty evidence directory:

```sh
python3 scripts/temporal-sandbox.py --artifacts /tmp/cohestra-local-acceptance-evidence
```

The runner provisions isolated services, applies migrations, runs both editions
under race detection, and removes its runtime while retaining evidence. Its
internal Go selector is `-run '^TestTemporalSandbox' -count=1 -timeout=6m -json`.
Pass requires all six leaves **in each edition**, not merely exit code zero:

1. `TestTemporalSandboxStoredGoldenOutput`: source → map → sink yields exactly
   ALPHA/BETA records, three encrypted PostgreSQL payloads and one sink call.
2. `TestTemporalSandboxRetryAndTimeout/retry`: real server attempts are `[1,2,3]`.
3. `TestTemporalSandboxRetryAndTimeout/timeout`: one attempt, real start-to-close
   timeout history event, cancelled handler context, failed stored phase.
4. `TestTemporalSandboxPausePagesAndRestartWorkflowWorker`: pause blocks page two
   and the sink across worker replacement; resume completes the same run with two
   pages and one sink. Both controls have DB delivered revisions and matching
   Temporal history signals.
5. `TestTemporalSandboxHeartbeatCancelPreventsSink`: cross-tenant control is 404,
   cancellation reaches the real activity context, stored phase is cancelled and
   no sink runs.
6. `TestTemporalSandboxRejectsUnsupportedAgentAdmission`: a valid reserved agent
   receives `AgentAdmissionRejected` before any activity is scheduled.

Retain `summary.json`, edition Go JSON logs, and per-scenario history/summary
files. The runner rejects zero tests, skipped/failed/incomplete tests and cleanup
errors. See `docs/TEMPORAL_SANDBOX.md` for prerequisites and bounded timeouts.

### Security checks

From the checkout root, run the production dependency gate:

```sh
npm audit --omit=dev --audit-level=high
```

From `apps/workflow-go`, run the pinned Go vulnerability scanner:

```sh
go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...
```

Use the pinned Gitleaks 8.30.1 reachable-history command in
`.github/workflows/ci.yml` with full history and redacted output. Do not replace
this with a working-tree-only scan. Preserve remote CI results separately for
CodeQL, image/IaC scanning and other platform jobs not reproduced locally.

## Manual integrated UI acceptance

The September 25 report records the executed results for these journeys, including
real browser and API evidence. Read that ledger for passes, failures and scope limits. Use disposable fixture records and retain screenshots,
saved definitions, run IDs and output comparisons.

1. **Sign in and persist:** use a real local account; open a pipeline with node
   timeout/retry, concurrency, asset bindings, metadata, SLO and notifications.
   Save, reload and compare the stored definition. Rename/reposition a node and
   repeat. Unsupported fields must not silently disappear.
2. **Review a real AI response:** request one config change, expand the exact
   before/after review with keyboard controls and verify sensitive values remain
   hidden. Preview must not modify/save the canvas. Apply then Save/reload;
   Undo must restore the previous graph while retaining metadata. Repeat with
   Discard and a newer intervening draft; stale Apply must stay disabled. Record
   model/provider unavailability as BLOCKED, never as a successful AI test.
3. **Mermaid edits:** make a harmless label edit and verify metadata preservation.
   Attempt a structural deletion; cancel confirmation and verify no loss, then
   confirm deliberately and verify only the intended structure changed.
4. **Execute what was saved:** run a local source → transform → supported sink
   fixture. Match output to a golden result and inspect run/node status. Exercise
   a deliberately retrying and timing-out fixture using explicit node policies.
   This connects UI persistence to runtime policy behavior.
5. **Control a real run:** pause a multi-page run, refresh/reopen the page and
   confirm no next page or downstream sink starts; resume to completion. Cancel
   another run during a context-aware connector call and confirm no downstream
   sink. Acknowledged intent is not terminal completion: inspect final status and
   history. Repeat during a delivery outage and after worker restart if the local
   harness supports it. Already-admitted effects may finish; cancellation is not
   rollback.
6. **Permissions and terminality:** owner/editor can control an accessible run;
   viewer gets 403, another tenant gets 404, terminal controls get 409 and
   pause/resume after cancellation get 409. Check UI errors are understandable
   and do not imply success.
7. **Reserved agent denial:** submit the shared valid agent fixture through the
   relevant save/run/activation/scheduling paths. Expect explicit unavailability
   and no execution/tool effects. Agent configuration is not runnable capability.
8. **Accessibility:** keyboard-only review/Apply/Discard/Undo, visible focus,
   expanded details and status/error announcements; repeat the core flow at a
   narrow viewport. This complements the single automated Chrome journey.

## Explicitly not delivered or proven

LangGraph orchestration, agent model/tool execution, agent memory/storage and
approval flows are deferred. Also unproven by these gates: live-provider accuracy,
external connector effects/recovery, abrupt process-kill recovery, exactly-once
effects, terminal reconciliation, complete browser/backend end-to-end coverage,
production Temporal operation and production rollout readiness. Separate
enterprise engine workflows retain their prior control responsiveness.

## Results ledger — 2026-09-22

Evidence lives under the git-ignored `.artifacts/local-acceptance/` and is not
committed. Private credentials, keys, session traces and full native logs never
leave that directory.

| Gate | Result | Evidence |
| --- | --- | --- |
| Evaluator/parser/shared/web/build/mock browser | PASS: all six commands | `offline/results.json` and logs |
| Go race tests, vet and builds, both editions | PASS: all six commands | `go/results.json` and logs |
| DB controls/agent admission, both editions | PASS, no matching skips | `database-verified/results.json` |
| Real Temporal sandbox | PASS: six leaves per edition; cleanup successful | `temporal-verified/summary.json` |
| Real local pipeline + tenant isolation | PASS: all eight checks; 3 input rows, exactly Alpha/Gamma in managed output | `smoke.json` |
| Stop/restart and persisted data | PASS: saved v2 and two output rows survived | `lifecycle.json` |
| Real browser sign-in/edit/save/reopen | PASS: new version preserves stored definition except intended label | `browser.json`, browser screenshot |
| Production npm dependency gate | PASS | `security/npm.log` |
| Reachable-history Gitleaks 8.30.1 | PASS | `security/gitleaks.log` |
| Go vulnerability gate | PASS, zero called vulnerabilities; scanner reports two additional non-called package/module findings | `security/govulncheck-verified.log` |
| Qwen3:8b local generation | API transport PASS in 31.85s; semantic FAIL: invented `/users` recordsPath for the array source | `ai-smoke.json` |
| Remaining manual UI journeys | NOT RUN; use the checklist above | Do not infer them from automated passes |

The generated AI proposal was neither saved nor executed. This single case is
not a model comparison or accuracy score. The model stays a local test setting;
there is no production model promotion.

Cold attempts retained separately: the Docker app build hit its 1,200s bound;
the first community sandbox compilation exceeded its bound, then passed on
retry; the first Go vulnerability scan timed out, then passed on retry. The
first secondary DB migration attempt collided with the cluster-wide
`dataflow_app` role. The successful DB suites used an isolated cloned database
`acceptance_controls_v2` with all 27 migrations present. These failed setup
attempts are not product-test passes.

For repeatable golden output use `smoke` again: it creates a fresh pipeline and
collection, while reusing the two private local accounts. Rerunning the same
cursor-based pipeline in the UI does not reset its source cursor. The browser
saved version remains a draft for further editing.

## Published review package

[Integration review and evidence](evals/README.md) includes the plans, dated test results,
publication manifest, and GitHub UX issue links. Raw private runtime artifacts remain
ignored. `responsive` runs desktop/mobile metadata and narrow keyboard review checks
against the running local web app with mocked APIs; it does not evaluate the model.
