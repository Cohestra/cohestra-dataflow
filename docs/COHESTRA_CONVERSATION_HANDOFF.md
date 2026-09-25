# Cohestra AI agent planning — conversation handoff

Imported on 2026-09-09 from [Assess Cohestra Features](https://chatgpt.com/share/6aa19e6e-3364-83ee-9aa7-2770cf45e568).

## Objective and constraints

Extend Cohestra Dataflow into a full AI agent and data pipeline builder, retaining monitoring, audit, and lineage. Support plug-and-play open-source agent projects.

The user's prior direction was to plan first: raise current-product gap plans before agent architecture plans, keep frontend and backend PRs separate, and include sandbox integration tests in CI. Publish planning and architecture PRs for review before implementing. Do not push to main.

## Prior investigation (reported, not revalidated in this task)

- Repository: `Cohestra/cohestra-dataflow`.
- Existing foundations: visual pipeline builder, Temporal execution, connectors, AI-assisted pipeline drafting, conditional branches, durable retries, encrypted payloads, and saved credentials.
- AI currently assists pipeline authoring; the investigation found no native agent execution loop.
- Missing agent capabilities included tool calling, conversation memory, durable human approval, and model cost limits.
- Some connectors and capabilities are edition-gated; account for the OSS experience.
- Reported product issues: custom connector source/sink dispatch mismatch; node retry/timeout settings apparently unused by the DAG executor; default Compose command not starting Ollama; inconsistent license wording in contribution documentation.
- PR CI reportedly runs frontend and Go checks; Compose integration tests run on schedule/manual dispatch rather than on PRs.

## Proposed architecture and delivery

Reuse Temporal. Run dedicated agents as child workflows and connect their runs to existing monitoring, audit, and lineage.

First release: one complete agent flow with model calls, MCP/HTTP tools, durable human approval, cost limits, and a run inspector. Follow with a LangGraph adapter.

| Planning PR | Scope |
| --- | --- |
| 1 | Current-product gaps, priorities, sandbox and CI requirements; prior package reportedly enumerated 13 gaps. |
| 2 | Backend architecture: durable agents, tools/MCP, OSS adapters, memory, approvals, budgets, audit, lineage. |
| 3 | Frontend architecture: agent management, mixed agent/data pipeline builder, approvals, run inspection, monitoring and reporting. |

Each plan should define its later implementation PR sequence, with separate frontend/backend tracks. Integration strategy: PR-triggered sandbox tests with deterministic model/tool fixtures alongside existing deployed connector tests.

## Prior completed work and publication blocker

The previous assistant reported preparing seven documents, three PR descriptions, patches, separate implementation sequences, and 18 integration scenarios, packaged as `Cohestra-PR-Plan-Package.zip`. A separate AI agent feature review was also reported created.

It reported passing documentation checks and three offline AI evaluator self-tests. Application tests were not run because Go and Docker were unavailable in that prior environment. These are historical claims, not checks performed here.

GitHub publication failed with `403: Resource not accessible by integration`. The connection reportedly exposed an installation for `as791`, none for `Cohestra`, and no accessible fork. No remote branches or PRs were created, no implementation changes were made, and nothing was pushed to main in that session. Current local GitHub access has not yet been checked; do not assume the old connector limitation applies here.

## Recovery limits

The shared conversation's visible messages and expanded activity summaries were read. The ZIP is displayed only as a filename, with no download link. Expanded patch activity exposes a command label, not patch contents. Consequently the exact seven documents, PR bodies, patches, complete list of 13 gaps, and complete list of 18 scenarios have not been recovered. Do not represent this handoff as their original contents.

A shallow search of Downloads did not find the named package. A semantic documentation search did not locate that planning package in the checkout; it returned existing architecture and MVP execution documents instead.

## Local state verified during migration

- Workspace: `/Users/aryaman.sinha/Downloads/dataflow-poc`.
- Origin: `https://github.com/Cohestra/cohestra-dataflow.git`.
- Current branch: `feat/ai-pipeline-builder`, tracking `origin/feat/ai-pipeline-builder`.
- Existing uncommitted work includes AI API routes/tests, Compose, AI builder/model evaluation documentation, evaluation cases/runner, training/export scripts, and website changes. Preserve it; it is not evidence that the prior planning package exists here.
- Migration added this handoff only. No application changes, commits, pushes, or PR publication were performed.

## Resume point

Planning remains the active phase; implementation awaits review. Recover the original ZIP from the source task if exact artifacts are required. Otherwise reconstruct the planning documents from the recovered scope, explicitly label them as reconstructed, and validate each finding against the current repository before proposing it. Inspect existing PRs and local publishing access before creating any duplicates. Use an isolated checkout for planning PR work so existing uncommitted changes remain intact.


## Continuation — 2026-09-10 IST

The user authorized repository re-review, publication of the three planning PRs,
Notion accuracy research and new Ollama candidate testing. GitHub CLI access here
is ADMIN; the old connector permission blocker does not apply.

Clean baseline: `857f36f51d9d58c05b32a4d2941448b1eeebbcbd`, isolated checkout
`/private/tmp/cohestra-plan-20260909`. Draft stack published:

1. [PR38: product gaps and sandbox gates](https://github.com/Cohestra/cohestra-dataflow/pull/38), `codex/plan-product-gaps` → main.
2. [PR39: backend architecture](https://github.com/Cohestra/cohestra-dataflow/pull/39), `codex/plan-agent-backend` → gap branch.
3. [PR40: frontend architecture](https://github.com/Cohestra/cohestra-dataflow/pull/40), `codex/plan-agent-frontend` → backend branch.

These contain six newly reconstructed planning documents, not recovered originals.
B1–B6 and F1–F6 are future separate implementation tracks; B4/F4 demonstrates the
functional flow and B5/F5 recovery evidence gates release. No implementation or
production default change has been made.

Verified additions include lossy canvas conversion, a tracked automatic model
override, incompatible v1 benchmark expectations (six impossible cases), and
existing CI history/dependency findings. Remote CI builds/race/vet/config checks
passed; security checks are not green and are documented separately.

Latest relevant Notion evidence located: “DataFlow AI Pipeline Builder — M0–M4 &
Ollama Bake-off”, edited August12, reporting August7 full31-case GCP measurements.
No model promoted. New local benchmark compares qwen3:8b against granite4.2:8b
using exact-main handlers and unchanged v1 scorer, with synthetic fixture-only DB.
It does not test auth/RLS or workflow execution. Artifacts and reproducible harness
currently live in `/private/tmp/cohestra-eval-20260909`; final measurements and
experiment completion status are to be recorded below after the run.

## Continuation — 2026-09-14 IST

The user approved the agent planning PR direction in this conversation and
requested explicit architecture HLD and LLD plans in parallel. The existing
PR stack remains the publication target; this approval is not represented as
a GitHub review or merge. Work now adds a system HLD, backend LLD and frontend
LLD, keeping the original source-to-agent-to-approved-tool-to-sink objective.

Temporary cleanup removed the old scratch checkout working files; its Git
objects and retained evaluation artifacts survived. The recovered isolated
checkout is `/private/tmp/cohestra-plan-20260914`. Remote main remains the same
857f36f baseline. Existing original-workspace edits remain preserved.

Notion was rechecked September14: the latest located Ollama bake-off remains
the August12-edited page and its August7 results. The native Granite pull had
failed with a connection reset. A bounded IPv4 range download is reusing
completed model chunks and will verify the official full SHA-256 before use.
The Qwen baseline remains 9/31; Granite accuracy is not yet measured.

Published the explicit system HLD and backend LLD in PR39, and frontend LLD in
PR40. Durable local copies now live at docs/plans/ai-agents/ in this workspace.
Validated56relative links,8JSON examples and cross-stack DTOs; changes remain
documentation-only. The user-approved direction is recorded in all three plans.

Granite weights verified and installed. First attempt was infrastructure-invalid:
Docker disappeared after one inference;27requests failed their synthetic DB
connection and three deterministic guards passed. Do not call raw3/31 accuracy.
Native PostgreSQL17.10 runtime was installed, with post-install default cluster
and login service skipped; only a temporary loopback15432cluster was started.
The fresh full retry is granite42-8b-native-db-retry. The original binary, source,
corpus and prompt remain unchanged; database deployment and host state differ
from the Qwen run, so latency remains diagnostic rather than a controlled claim.

The user then explicitly requested deferring LangGraph. All three PRs and local
plans now use B1–B5/F1–F5 as the native Go/Temporal core sequence. README's
Deferred options list holds LangGraph and optional cross-run memory. Adapter
scenario S17 is deferred;17core scenarios are S01–S16 plus S18. This supersedes
the earlier B6/F6 roadmap wording. All five architecture diagrams parsed using
a temporary browser profile; links/JSON checks passed.

## Completed model comparison — September14 IST

The recovered Granite run completed all 31 cases with zero infrastructure failures:
9/31 passes, matching Qwen's 9/31. Granite generation was 0/8 versus Qwen's 2/8;
ambiguity was 4/4 versus 2/4; adversarial was 5/5 for both. Neither passed
refinement, branching or engine/trigger cases. All 49 Granite calls and 11
provenance checks passed. No model default changed.

The invalid Docker-interrupted attempt is retained separately. A second scorer
defect is proved offline: changing only a copied edges field from null to an
empty array makes a disconnected source/filter/sink graph pass. Captured outputs
and reports were not changed. V2 needs consistent fixture binding, supported
expectations and case-specific connectivity assertions before promotion decisions.

Curated final reports are published in PR38 and inherited by PR39/40. Raw
synthetic attempts, hashes, metadata and runnable proof/comparison scripts are
retained under docs/evals/local-main-857f36f-20260909. HLD/LLD copies and the
model assessment are under docs/plans/ai-agents. The native fixture database was
stopped and its temporary data removed; Granite was unloaded but remains
installed. PostgreSQL runtime remains installed without a default cluster or
login service. Docker remained unavailable, so its old stopped fixture container
was not removed. No application implementation or main-branch change was made.


## Implementation wave — 2026-09-14

The approved parallel foundation work produced draft implementation PRs #42 (offline evaluator/CI), #43 (lossless canvas edits and browser regression), and #44 (node activity policies and manifest direction). All target main independently. Root completed partial agent edits after native usage limits and expired Claude Code OAuth prevented delegation from finishing. Both backend editions pass race tests, vet and builds; frontend tests/build/browser and the combined checkout checks pass. GitHub #42/#43 checks pass except the reachable-history security scan; #44 still has checks pending in the dated evidence snapshot. No merge, deployment or model promotion occurred. LangGraph remains deferred.

See [implementation status and next work](plans/ai-agents/IMPLEMENTATION_STATUS.md) for exact heads, worktrees, evidence, limitations and remaining B1/F1 work.


## 2026-09-14 implementation continuation

Published draft PR45 (exact AI change review, depends43), PR46 (strict reserved
agent contracts and closed admission, depends44), and PR47 (durable controls,
depends46). All combined local edition tests/builds/vet, browser and evaluator
checks passed. PostgreSQL fixture checks exercised real RLS; the disposable
cluster is stopped and removed. PR47 PostgreSQL16 admission/control CI passed in both editions; other remote
checks are recorded in the saved snapshot. The baseline security-job failure remains. No merge/deployment.
See docs/plans/ai-agents/IMPLEMENTATION_STATUS.md and dated evidence for exact
heads, dependencies, remaining real Temporal sandbox/F1 acceptance, and B2–B5
work. LangGraph and cross-run memory remain deferred; agent execution remains
disabled. Original dirty application edits were preserved. All agents completed.


## Sandbox continuation — 2026-09-14

Draft PR48 targets47; head21b137aa3db30ec84eb0a555ddb8b2a275d040ed. Six real
Temporal/PostgreSQL leaf scenarios pass in both editions with race detection,
including delivered signals, real connector cancellation and cold-cache worker
restart. Native runner/CI added, default-download failure cleanup tested. All
local owned services/data removed. Linux/PostgreSQL16 initial run34816809471 exposed a transient status-query
timeout during old worker-task lease expiry. Bounded GET polling fixed it; final
run34817419742 passed both editions in4m6s. All14 ordinary PR checks pass; the
existing security-job failure remains. Consult dated evidence for the history. No merge or
deployment; original edits preserved. See IMPLEMENTATION_STATUS.md for scope,
explicit injected auth/connectors and remaining production recovery/UI gates.


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
