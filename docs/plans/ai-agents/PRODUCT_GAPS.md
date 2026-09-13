# Product gaps before AI agent implementation

Status: architecture direction approved by the user on 2026-09-14; no runtime changes. Baseline:
`857f36f51d9d58c05b32a4d2941448b1eeebbcbd`, reviewed 2026-09-09 UTC.
This reconstructs the earlier scope from current source; the original package
was not recoverable. Its reported count of 13 gaps is not a target to fill.

## Scope and release order

Extend Dataflow with durable agents and a mixed data/agent builder. Retain
versioning, tenant-owned credentials, monitoring, audit, lineage and environments.
Review this gap plan first, backend architecture second, frontend architecture
third. Implementation follows explicit review, in separate backend/frontend PRs.
No main-branch push, production rollout or model promotion is authorized by this plan.

## Existing foundations

React canvas and shared pipeline types; Go API; Temporal workflow/activity
workers; connector catalog and saved tenant connections; encrypted payloads;
pipeline monitoring, audit and lineage. AI generate/refine is request-scoped
pipeline authoring, not durable agent execution. Reuse these foundations.
The existing `docs/ROADMAP.md` also records deferred identity/role and production
infrastructure work; its historical entries do not establish deployment status.

## Verified gaps

Internal Go paths below are relative to `apps/workflow-go/`.

| ID / priority | Evidence and consequence | Planned change | Acceptance |
| --- | --- | --- | --- |
| G01 / P1 | `internal/model/types.go:69-70` exposes TimeoutSec/Retry; `internal/workflows/dynamic_dag.go:35-47` applies global activity options. Saved per-node policies are not applied. | Derive validated options at node dispatch, preserving defaults when absent. | Distinct node deadlines/attempt counts work; invalid settings rejected. |
| G02 / P1 | `internal/connectors/registry.go` accepts source/sink manifests; `internal/connectors/runtime.go` has a manifest fallback in Fetch but not Handle. Manifest-only sinks can be catalogued but fail dispatch. | Implement supported sink manifests or reject them at registration; align catalog and runtime in one PR. | A declared supported sink completes a pipeline; unsupported kinds fail before selection. |
| G03 / P1 | `internal/workflows/dynamic_dag.go` drains control signals between levels, without cancelling in-flight activity contexts. | Specify pause versus cancellation, propagate cooperative cancellation and prevent new dispatch. | Cancel during a slow node reaches terminal state without starting new effects; pause resumes without pretending effects were undone. |
| G04 / P1 | August model bake-off: no candidate met promotion gates; all failed refinement and engine/trigger categories. | Diagnose contract failures separately from model choice; controlled new-model comparison. | Preserve all failed cases and all-case denominators; clear gates before changing defaults. See MODEL_EVALUATION.md. |
| G05 / P1 | Tracked `docker-compose.override.yml` hardcodes qwen2.5vl:7b and host Ollama URL, overriding environment-selected base settings during normal Compose merging. | Convert automatic developer override to explicit opt-in example; verify effective config. | Requested model matches effective config and actual serving digest. |
| G06 / P1 | `.github/workflows/integration.yml` is schedule/manual only; `scripts/smoke-test.sh` relies on external HTTPS. | PR sandbox with deterministic model/tool fixtures, preserving production egress validation. | Relevant PRs exercise the stack without paid model keys or public fixture availability; cleanup always runs. |
| G07 / P2 | `.github/workflows/ci.yml` omits the existing Python AI evaluator self-test. | Invoke its strict offline self-test, extend fixtures with shipped contracts. | Invalid corpus/scorer expectations fail PR checks; no new framework. |
| G08 / P2 | CONTRIBUTING and PR template say Apache 2.0; root LICENSE contains AGPLv3. Settings UI also says Apache. | Maintainer confirms intended policy; align wording in docs/UI. No license change in this stack. | Statements match the approved policy. |
| G09 / P1 | `apps/web/src/utils/pipelineConvert.ts` reconstructs a whitelist and drops node timeoutSec/retry/inputAssets/outputAssets and pipeline concurrency on load/save. | Preserve supported definition fields through canvas conversion. | Round-trip test retains every supported policy/asset field and metadata while editing a different node. |

## Evaluation and CI findings during this review

- **G10 / P1 — Security checks are not green.** Exact baseline main CI already
  reported two history-scan detections and vulnerable workflow-image dependencies.
  The planning PR reports the same detection count and four unique dependency
  advisories (three high, one critical) on unchanged dependencies. Logs alone do
  not establish live secret validity or vulnerability exploitability. Triage
  using redacted evidence, remediate separately, and rerun skipped npm/Go security
  checks plus image scanning. Existing dependency PRs should be reviewed before
  opening duplicate fixes. Evidence links are in MODEL_EVALUATION.md.
- **G11 / P1 — The v1 corpus contradicts the current planner contract.** Five
  refinement inputs omit required connectionIds but demand exact config
  preservation; one generation case requires Snowflake config.mode=upsert which
  main rejects. At least six of 31 cannot pass this API and scorer simultaneously.
  Keep v1 historical reports intact. Publish a versioned corpus with symbolic
  fixture binding applied consistently to requests and preservation expectations,
  and reconcile desired versus supported Snowflake behavior explicitly.
  Acceptance: a golden ready answer passes each positive case, invalid/mismatched
  fixture references fail preflight, and the original failing cases remain
  documented rather than quietly removed. The offline proof uses the actual
  evaluator request path and scorer and confirms one captured model request.

GAP-EVAL-01 owns G11 and is a dependency of accuracy claims in GAP-AI-01 and model
promotion. GAP-SEC-01 owns G10 with a maintainer; no credential rotation, history
rewrite or dependency update is performed in this documentation stack.

For G04, the current prompt lists field names/required markers but omits allowed
values/types, while config JSON schema remains generic. Retained model output
shows rejected enum/type guesses. Plan explicit field constraints before using
larger weights or fine-tuning as the presumed fix. Prompt/schema changes and
model changes must be evaluated independently on the corrected corpus. The first
Granite inference also exposed a concrete readiness failure: it returned three
disconnected nodes with no edges, and the API labelled the draft ready with zero
repairs. The unchanged scorer rejected it. Add positive/negative connected-flow
fixtures for source-transform-sink intent; validate intended connectivity before
ready while retaining explicitly supported independent branches. This one response
is evidence of a contract failure, not a complete Granite accuracy result.

## Corrected assumptions

- Base Compose excluding Ollama is intentional: the AI profile is opt-in.
  Improve setup/preflight instead of making an LLM mandatory for data pipelines.
- AI proposal review already has ready/needs_input/rejected, Apply/Discard/Undo
  and contextual refinement. Extend those flows rather than rebuild them.
- Existing monitoring/audit/lineage does not imply a model/tool step store or
  durable approval protocol. No application MCP client was found in this review;
  developer-tool MCP connections are not a shipped outbound tool runtime.
- Separate uncommitted AI experiments in the original checkout are not assumed shipped.

## Implementation PR sequence after plan approval

| PR | Track | Dependencies | Deliverable |
| --- | --- | --- | --- |
| GAP-BE-01 | Backend | Approved execution semantics | G01 node policy and G03 controls; split if review size warrants. |
| GAP-BE-02 | Backend/connectors | Approved support-or-reject decision | G02 catalog/runtime consistency. |
| GAP-CI-01 | CI | Plan approval | G06 deterministic sandbox baseline and G07 offline evaluator. |
| GAP-DOC-01 | Dev experience/docs | Maintainer decision for G08 | G05 explicit override/preflight and G08 wording. |
| GAP-AI-01 | Backend/AI | Failure classification, GAP-CI-01 | G04 contract corrections with unchanged-model evidence. |
| GAP-FE-01 / F1 | Frontend | Plan approval | G09 lossless conversion; reuse and improve existing AI review/error states. |

GAP-* are work packages, not a second implementation stack. B1 contains
GAP-BE-01 (G01/G03) and GAP-BE-02 (G02), plus contract/replay fixtures; it may
split along those review boundaries. GAP-CI-01 supplies the sandbox before
B1/F1 integration acceptance. F1 is GAP-FE-01 (G09). GAP-DOC-01 owns G05/G08;
GAP-AI-01 addresses planner quality separately from agent B2–B6.

The first executing agent slice needs G01–G03, G06 and G09. Non-executing agent
management planning can proceed while model promotion is unresolved. Automatic
acceptance/execution of generated drafts is out of scope.

## Approved direction and detailed design

The user approved the architecture direction on 2026-09-14. B1 rejects unsupported
manifest-only sinks until a write contract exists; native Temporal execution,
durable control and OSS operational essentials are specified in the HLD/LLD. B4/F4 demonstrate the complete functional flow; release completion requires
B5/F5 recovery and operations evidence. Mandatory B3/B4 safety checks cannot
be deferred to B5. Use the backend and frontend LLD contracts for their separate implementation PRs. Refer to SANDBOX_AND_CI.md for the 18 acceptance scenarios.
