# Sandbox and CI acceptance plan

Status: proposed. Future agent tests have not run in this documentation PR.
Baseline: `857f36f51d9d58c05b32a4d2941448b1eeebbcbd`.

## Reuse and tiers

Keep Go community/enterprise, shared Mermaid, web run-graph, builds, Compose and
security checks. Reuse `tests/ai-evals/run.py` for planner scoring. Its offline
`--self-test --strict` belongs in PR CI; model scores do not test execution.

1. PR fast: existing unit/type/build checks plus offline evaluator. Add focused
   regressions with each behavior change, using existing test tools.
2. PR integration: isolated Postgres, Temporal, API/workers and deterministic
   model/tool fixtures. Relevant-path triggers, superseded-run cancellation,
   20-minute job bound and unambiguous required-check behavior on docs-only PRs.
3. Nightly/manual: full Compose/deployed connector tests and optional actual
   Ollama/provider characterization. No paid keys or weight downloads on untrusted PRs.

Use unique project names, networks, volumes and synthetic tenants. Bind local
ports to loopback. Never reuse developer/production databases. Clean up under
always()/shell traps; retain redacted logs, synthetic workflow histories and
machine-readable summaries on failure.

The current smoke source is external HTTPS because tenant URL validation rejects
private HTTP. Preserve that policy: use an injected test transport at the
connector boundary or isolated test-only endpoint configuration with explicit
production deny-by-default regression tests. Do not globally disable SSRF
validation. Separate fixture contract tests from real-network egress tests.

## Integration matrix

Implement each scenario alongside its feature. These are acceptance criteria,
not claims of existing coverage. B/F milestones live in the separate architecture plans.

| ID | Scenario | Required observation | Tier / owner |
| --- | --- | --- | --- |
| S01 | Existing source-transform-sink | Same stored records and terminal status as baseline. | PR / BE |
| S02 | Manifest source/sink | Catalog matches runtime; unsupported kinds fail before a run. | PR / BE |
| S03 | Per-node retry/timeout | Actual attempts/deadline match validated settings. | PR / BE |
| S04 | Generate mixed draft | Valid schema/graph and explicit user review before save/run. | PR / BE+FE |
| S05 | Missing resource or unsafe prompt | Clarify/reject; never invent credentials or execute tools. | PR / BE+FE |
| S06 | Refine one node | Preserve unrelated nodes, edges, policies and assets; show diff. | PR / BE+FE |
| S07 | Native agent/read-only tool | Versioned contract, validated input/output and recorded step. | PR / BE |
| S08 | Approval-required tool | No effect before authorized approval of exact argument digest. | PR / BE+FE |
| S09 | Reject/expire/stale/duplicate approval | Correct state, no bypass or repeated dispatch. | PR / BE+FE |
| S10 | Exhaust budget | No further calls; record usage and clear terminal reason. | PR / BE+FE |
| S11 | Worker restart mid-run/wait | State survives; never blindly replay external effects. | PR / BE |
| S12 | Effect succeeds, acknowledgement lost | Reconcile same idempotency key or enter explicit unknown-result/manual-recovery state. | PR / BE |
| S13 | Pause/cancel active work | Pause prevents future dispatch; cancellation propagates and prevents new calls. | PR / BE+FE |
| S14 | Cross-tenant access/approval/credentials | API, RLS and worker boundaries reject mismatch; no secret leakage. | PR / BE |
| S15 | Agent and data nodes together | Typed refs feed downstream; coherent parent/child failure and cancellation. | PR / BE+FE |
| S16 | Audit/monitoring/lineage | Correlate execution, agent, tool and approval IDs with authorized/redacted records. | PR / BE+FE |
| S18 | Keyboard/mobile/role journey | Accessible create/review/approve/inspect; denied UI actions also fail direct API calls. | PR browser / BE+FE |

### Deferred acceptance

S17 (OSS adapter lifecycle) is excluded from the native release gate. If the
[deferred LangGraph option](README.md#deferred-options) is selected later, require
the pinned adapter to obey the same resume/cancel, allowlist, approval, budget
and failure contracts, using a deterministic fixture. No adapter job or runtime
is required for the 17 core scenarios (S01–S16 and S18).

## Model experiments

First run positive golden-output checks against the API contract to detect
unsatisfiable cases (G11); freeze a corrected version without rewriting v1.
Pin source, corpus, prompt, schema and binary hashes; seed the synthetic connector
manifest. Record actual serving digest, context, sampling, thinking, repair limit
and timeout. Run serially, retain cold/warm distinction and every failed case.
Compare all-case passes before applicable-only rates. Repeat full suite twice,
then expanded promotion corpus on intended hardware before changing defaults.

An API-only model benchmark does not cover workflow execution, real connectors,
auth/RLS, browser behavior or durable tools/approvals. Label any injected test
seam or synthetic catalog. A small smoke sample establishes compatibility only.

## Implementation merge gate

Assigned scenarios pass in the sandbox; existing data-only pipelines remain
compatible; both editions compile. Check migrations/rollback, API compatibility,
resource bounds and redaction when applicable. Link dependent frontend/backend
PRs. Mocked UI checks alone cannot establish integration success.
