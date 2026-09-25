# Implementation status — 2026-09-14

The user approved the plan and parallel implementation using orchestrate-agents.
The implementation waves deliver bounded B1/F1/CI foundations; they do not complete
the full agent product. LangGraph and cross-run memory remain deferred.

| Lane | Draft implementation PR | Head | Delivered |
| --- | --- | --- | --- |
| CI/evaluation | [#42](https://github.com/Cohestra/cohestra-dataflow/pull/42) | `6c60721ed21f4638918e71c4d930279a4fa3832e` | Offline evaluator in CI; opt-in continuous required graph paths; malformed-contract checks; v1 unchanged. |
| Frontend F1 slice | [#43](https://github.com/Cohestra/cohestra-dataflow/pull/43) | `f04fad7391fb2722858110dec72b6f657f1d99cf` | Lossless metadata through load/save, AI Apply/Undo and Mermaid; stale-proposal guard; browser regression in PR CI. |
| Backend B1 slice | [#44](https://github.com/Cohestra/cohestra-dataflow/pull/44) | `31ce9c88fe9b0648269b97fbe1cba812ae055cdd` | Validated per-node activity timeout/retry, source pagination and sibling isolation; preserved historical workflow behavior; source-only manifest admission. |

All three target main independently at baseline
`01677b7bab4be929769aca0e3eb626189231b0ae`. None is merged. Planning PRs #38–#40
remain the architectural references. A combined private checkout at
`/private/tmp/cohestra-impl-integration-20260914` merges all three without
conflicts; combined commit `4684383b28443a881d533b75baceb43580d1cfc4` is local only.

## Verification

- Backend: full community and enterprise `go test -race`, both `go vet` modes,
  both service builds, existing historical replay and new policy/admission
  regressions pass. Fifteen policy wire cases cover defaults and invalid inputs.
- Frontend: shared/web tests and production build pass. The browser regression
  failed on untouched main because Save dropped concurrency, then passed on the
  implementation, including AI stale protection, Apply/Undo and Mermaid edits.
  GitHub's `metadata-round-trip` job also passed.
- Evaluator: strict offline self-test passes three report cases, ten path/branch
  checks and eleven invalid-contract checks. No live inference ran in this wave.
- Combined checkout: web tests, strict offline evaluator and focused Go
  model/workflow/connector/API packages pass. This is not a real API/DB/Temporal
  end-to-end sandbox claim.

[Evidence and dated GitHub check snapshot](../../evals/implementation-foundations-20260914/)
contain logs and machine-readable results. The reachable-history security scan
remains failing (one reported finding on #43), as it also failed on the main
baseline. Other remote checks may still be pending in this snapshot. No history
rewrite, credential rotation or scan exception was performed.

## Next work, in dependency order

1. Complete remaining production-connector, separate-process crash/effect recovery,
   and authenticated end-to-end acceptance. PR48 adds the deterministic real
   Temporal/PostgreSQL sandbox for current data workflows.
2. Complete F1 accessibility and integrated browser acceptance, including saved
   version readback. Proposal change review is implemented; the current browser
   job uses synthetic API responses.
3. Repair the versioned v2 evaluation corpus with symbolic connector fixture
   bindings and positive API golden-output preflight. The opt-in path matcher
   alone is not an accuracy or promotion gate; v1 retains known limitations.
4. Proceed to B2/F2 immutable definitions and tenant policy/storage, B3/F3 native
   model-only runs, B4/F4 tools and durable approvals, then B5/F5 recovery and
   operations. Do not claim the full flow before integrated acceptance.

## First-wave execution handoff

The original checkout's application edits were preserved. Implementation worktrees:
`/private/tmp/cohestra-impl-{backend,frontend,ci}-20260914`. Remote PR branches
are durable; temporary checkouts may be cleaned by the OS.

Native agents backend_plan/frontend_plan/eval_setup started in parallel and left
partial edits before an account usage limit stopped them. User-authorized Claude
Code fallback was attempted for backend/frontend but its OAuth session was expired
and could not refresh. Root completed and reviewed the edits directly, added the
missing regressions, tested, signed off and published them. No agent or Claude
Code task remains running. No model default or installed runtime was changed.

## Second wave — reviewed and published

| Lane | Draft PR and dependency | Head | Delivered |
| --- | --- | --- | --- |
| F1 proposal review | [#45](https://github.com/Cohestra/cohestra-dataflow/pull/45), targets #43 | `e77c73e44918e94c9838789d045ff3713630d49a` | Exact merged before/after review, sanitized values, keyboard focus, semantic incoming-source order, Apply/Undo/Discard regression. |
| B1 reserved contract | [#46](https://github.com/Cohestra/cohestra-dataflow/pull/46), targets #44 | `8265ebb15788cbce98674b35d4345deaa464580d` | Shared strict batch fixture, exact field keys, fail-closed API and direct Temporal admission, early webhook/backfill rejection without writes, PostgreSQL CI. |
| B1 durable controls | [#47](https://github.com/Cohestra/cohestra-dataflow/pull/47), targets #46 | `62f0c4e0908770f8ba031e05d05752ffe9ed72fb` | Atomic intent/audit, namespace delivery and revision CAS, responsive new workflow branch, per-attempt source/handler admission, bounded finalization, legacy resume and replay compatibility, migration027. |

None is merged or deployed. The backend merge order is #44 → #46 → #47;
frontend is #43 → #45. #42 remains independent. The local combined checkout
`/private/tmp/cohestra-impl-integration-20260914` at
`f34d360d2fea25faa52cce32002b8f0c2455c632` contains both waves. The contracts/controls
merge needed only a changelog resolution; both workflow version guards remain.

### Evidence and acceptance limits

[Second-wave evidence](../../evals/implementation-controls-20260914/) contains
full combined community/enterprise race tests, both vet modes and service builds,
shared/web tests, frontend build, Chrome regression, offline evaluator, PostgreSQL
logs, and dated GitHub results. All local checks passed. Database tests used an
isolated PostgreSQL17.10 cluster and the real application RLS role; CI uses
PostgreSQL16. The admission database CI job passed on #46; #45's browser CI passed.
At the saved snapshot, #45/#46 have 13 successful checks and the existing security
job failure. #47 also passed its PostgreSQL16 admission/control CI job in both
editions (3m11s); some other remote checks may remain in progress, as recorded in
the snapshot. The existing security job still fails. No security exceptions or
history changes were made.

Independent reviews found and fixed omitted join-input ordering in the preview,
noncanonical contract keys, backfill/webhook prewrite gaps, stale terminal side
effects, and revision-zero resume delivery for previously paused legacy workflows.
Ten top-level control regressions cover database and SDK workflow behavior.
Cancellation checks observe the SDK cancellation command while work is active;
they do not establish real-server heartbeat propagation or connector rollback.

Apply migration027 before the API/activity worker and register new activities
before the workflow worker. Source/handler admission orders control requests at
a short database transaction boundary; effects admitted earlier may still finish.
Merge/edge/checkpoint activity attempts have workflow gates only. Enterprise
engine workflows retain their existing responsiveness. Full Temporal/connector
sandbox acceptance, broader accessibility, saved-version readback and terminal
reconciliation remain outstanding. Agent model/tool execution is still disabled;
LangGraph and cross-run memory remain deferred.

All child agents completed. Original application edits remain untouched.
Second-wave worktrees are `/private/tmp/cohestra-{preview,contracts,controls}-20260914`.
All are clean and published. The disposable PostgreSQL cluster was stopped after
verifying zero remaining tenant fixtures; its data directory was removed after
copying logs. No test browser/server or Claude task remains running from this wave.

## Real Temporal sandbox — PR48

[Draft #48](https://github.com/Cohestra/cohestra-dataflow/pull/48) targets #47 on
`codex/impl-temporal-sandbox`, head `21b137aa3db30ec84eb0a555ddb8b2a275d040ed`.
The worktree `/private/tmp/cohestra-sandbox-20260914` is clean and published.
No production code or dependencies changed; nothing is merged or deployed.

The standalone runner creates and migrates its own native PostgreSQL cluster,
starts a pinned real Temporal server with disposable SQLite state, and runs both
editions serially. It verifies the downloaded archive before extraction, checks
real health/namespace readiness, fails missing/skipped tests, and retains
synthetic histories/JSON outcomes while cleaning child processes and runtime data.
The CI gate has a20-minute bound, cancellation of superseded runs, relevant-change
selection with a stable docs-only job, and always-uploaded evidence.

All six leaf scenarios passed twice in community and enterprise under race
detection against Temporal CLI1.8.3 / Server1.31.2, PostgreSQL17.10 and the module
Go1.25.12 toolchain: encrypted source/map/sink golden output; retries; real
start-to-close timeout; pause/resume across a cold-cache worker replacement;
actual connector-context cancellation with no downstream sink; and valid reserved
agent rejection before any activity. The control tests require matching delivered
revisions and real signal events;30-second database polling alone cannot pass.
HTTP cross-tenant control is denied.

The default download/failure path also ran with deliberately failing test
subprocesses: both editions failed, evidence remained, service ports closed,
and the temporary directory was removed. Seven result-parser regressions pass.
[Evidence](../../evals/temporal-sandbox-20260914/) records source revisions, commands,
versions, histories and cleanup results. Linux/PostgreSQL16 CI run
[34816809471](https://github.com/Cohestra/cohestra-dataflow/actions/runs/34816809471)
initially failed the community worker-restart status query: its HTTP deadline
coincided with expiry of a task still leased to the old worker. The history
confirmed the10-second task timeout. Status polling now tolerates only GET
deadline expiration under the existing bounded scenario deadline; mutations are
never retried and real paused state is still required. All six scenarios passed
again in both local editions on the corrected head. Linux rerun
[34817419742](https://github.com/Cohestra/cohestra-dataflow/actions/runs/34817419742)
passed in4m6s with PostgreSQL16.15: all six scenarios passed in both editions,
artifacts uploaded and cleanup succeeded. All14 ordinary PR checks now pass;
image signing is skipped and the existing security-job failure remains.
The initial failure and diagnosis are retained in
`linux-initial-failure/`, and corrected local evidence in `recovery-verified/`.

The real API handlers, PostgreSQL/RLS, workers, payload storage and Temporal
histories run in the tests. Authentication is an explicitly injected synthetic
tenant context; source/sink handlers are deterministic test boundaries. Worker
restart is graceful, in-process, with SDK sticky cache disabled and the same run
ID verified. This does not establish interactive sign-in, full UI/provider
integration, arbitrary process-kill/effect reconciliation, or future agent/tool
execution. LangGraph and cross-run memory remain deferred.

Independent review corrected malformed-only agent coverage and control tests
that could otherwise pass via fallback polling. Agents reached their usage limit
after writing the changes; root completed integration, runner provenance,
verification, cleanup and PR publication. All agents are finished. Both runner
clusters and the separate manual debug cluster are stopped and removed. Original
application edits remain preserved.


## CI repairs — 2026-09-14

- PR #49: https://github.com/Cohestra/cohestra-dataflow/pull/49; shared fix `29467c9`, based on current main. Integrated with normal merge commits into PRs #38–40 and #42–48; no history rewrite and nothing merged to main.
- Gitleaks falsely detected ordinary cost-state prose; exempted only its exact historical fingerprint and an identical copy in an explanatory comment. The comment is now paraphrased. Fully redacted diagnostics remain enabled. Final committed reachable-history scan passes; new-commit detection verified.
- Go scan uncovered vulnerabilities behind the failed secret gate. Upgraded Go and Docker builder to 1.26.8, x/crypto to 0.56.0, and synchronized vendor. Local govulncheck, community/enterprise race tests, vet and service builds pass.
- Integration workflow is disabled in GitHub because its Compose smoke pipeline reads the public JSONPlaceholder API. Source now has only a manual trigger. Keep disabled until this change is merged; then it may be enabled for deliberate manual runs. Isolated PostgreSQL, Temporal, and browser checks stay enabled.
- Final verification: all 17 latest workflows across PR #49 and the 10 existing PRs succeeded. Older duplicate/superseded runs were cancelled; no final-head workflow failed. Integration remains `disabled_manually`. Evidence and exact branch heads: `docs/evals/ci-fixes-20260914/`.


## Local acceptance — 2026-09-22

Combined isolated checkout: `.local-acceptance/checkout`, branch
`codex/local-acceptance-20260922`; PR48 + PR45 + PR42 and CI fixes. No remote
merge/deployment. Native enterprise API/workers + built React preview; isolated
Docker PostgreSQL/Redis/ClickHouse/Temporal. Web: http://localhost:13002.

Offline, both-edition Go/DB/Temporal, real pipeline, tenant isolation, real browser
save/reopen and stop/restart persistence checks passed. Local Qwen3:8b generation
returned a proposal but failed semantic review (invented recordsPath); no model
promotion. Manual UI coverage and deferred agent runtime remain explicitly open.
Plan, exact evidence and startup/cleanup commands: `docs/evals/local-acceptance-20260922/TEST_PLAN.md`.
Private login credentials: `.local-acceptance/checkout/.artifacts/local-acceptance/credentials.json`.


## Local acceptance completion — 2026-09-25

Executed the remaining local browser/API/worker journeys in the isolated
checkout and committed tested fixes as `10a82e337a6c392a32ecc7f73d3b9b13ef9701b6`
on `codex/local-acceptance-20260922` (clean). Both-edition race, RLS, real
Temporal and Iceberg checks passed, along with controls, timeout/retry,
monitoring, Mermaid and narrow/desktop UI checks. Fixed editor access under
RLS, AI ingestion schema, source totals, monitoring feedback and mobile Save.

Two real Qwen3:8b refinement cases still fail with HTTP 422 after repair
(missing pipeline name). Mocked UI passes do not establish model accuracy.
Initial controls failure under concurrent load and a non-secret scanner
false positive remain recorded. Deferred agent runtime and LangGraph remain
out of scope. No remote push, merge, model promotion or deployment.

Report and sanitized evidence: `docs/evals/local-acceptance-20260925/`.
The local application remains at http://localhost:13002; startup/stop and
repeatable checks are in the saved local acceptance plan.


## Product UI/UX audit — 2026-09-25

Reviewed the local app after development acceptance and filed GitHub bugs #51–58
with screenshots, reproducible steps and acceptance checks. P1: unsaved edits
are silently lost on navigation; keyboard node activation cannot open settings.
P2: field labels, validation feedback, cancelled status, version duplicates,
status contrast and real AI failure recovery. Product source unchanged.
Audit and issue links: `docs/evals/ux-audit-20260925/README.md`.
