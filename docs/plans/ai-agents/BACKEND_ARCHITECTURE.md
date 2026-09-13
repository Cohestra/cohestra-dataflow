# Durable agents: backend architecture and implementation plan

Status: architecture direction approved in the user conversation on 2026-09-14; architecture only. Reviewed against `main` commit `857f36f51d9d58c05b32a4d2941448b1eeebbcbd` on 2026-09-09. Agent execution, MCP, approvals, and model budgets described below are proposed additions. The [HLD](ARCHITECTURE_HLD.md) and [backend LLD](BACKEND_LLD.md) guide the separate implementation PRs.

## Outcome and boundary

Extend the existing Go/Temporal runtime to execute **data source → agent → approved tool action → typed output → data sink**, preserving execution identity, monitoring, audit, and lineage. Keep existing AI pipeline authoring independently useful: a better authoring model does not itself create an agent runtime.

The first implementation supports the `workflow` engine, immutable agent versions, a bounded model/tool loop, operator-configured Ollama, HTTP and remote MCP tools, human approval for mutations, and run-scoped memory. Reject agent nodes in `stream-direct`, `spark-sql`, and `flink-sql` until separate semantics are designed. Persistent cross-run memory and LangGraph follow the first complete flow. No second control plane, scheduler, generic plugin platform, or new backend language is needed for the native runtime.

## Repository findings and reuse

| Current behavior | Evidence | Consequence |
| --- | --- | --- |
| One Go module owns API, workflow, and activity processes. | [ADR-002](../../ADR-002-GO-BACKEND.md), [compatibility contract](../../BACKEND_CONTRACTS.md) | Extend those processes and preserve current workflow/activity names and JSON fixtures. |
| `fireExecution` consumes execution quota, starts a Temporal workflow, and persists execution identity. | [temporal.go:72](../../../apps/workflow-go/internal/api/temporal.go#L72) | An agent is a child of an existing execution, not a separately billed pipeline run. Resolve immutable versions before execution. |
| The DAG runs acyclic levels in bounded groups, pages sources separately, and exchanges `DataRef`s. | [dynamic_dag.go:23](../../../apps/workflow-go/internal/workflows/dynamic_dag.go#L23), [types.go:108](../../../apps/workflow-go/internal/model/types.go#L108) | Add an explicit agent-node branch and child workflow; preserve payload references. |
| Node timeout/retry fields are serialized but have no runtime reads. Global options set ten minutes and five attempts. | [types.go:62](../../../apps/workflow-go/internal/model/types.go#L62), [dynamic_dag.go:35](../../../apps/workflow-go/internal/workflows/dynamic_dag.go#L35) | **Verified gap:** honor validated per-node settings. Existing retries are present; per-node overrides are missing. |
| The registry accepts and advertises source/sink manifests. `Fetch` has manifest fallback; `Handle` only checks coded handlers. | [registry.go:16](../../../apps/workflow-go/internal/connectors/registry.go#L16), [runtime.go:71](../../../apps/workflow-go/internal/connectors/runtime.go#L71) | **Verified gap:** a manifest-only sink is advertised but fails `Unknown activity`. Implement direction-aware dispatch or reject unsupported sink admission consistently. |
| Pause/resume/cancel are signals drained at DAG level boundaries; active activities are not cancelled by that drain. | [routes_executions.go:353](../../../apps/workflow-go/internal/api/routes_executions.go#L353), [dynamic_dag.go:102](../../../apps/workflow-go/internal/workflows/dynamic_dag.go#L102) | **Verified limitation:** active model/tool calls and approval waits need responsive cancellation and a parent DAG integration change. |
| AI endpoints generate/refine a catalog DAG, validate it, and allow one repair. Ollama calls are request-scoped. | [routes_ai.go:147](../../../apps/workflow-go/internal/api/routes_ai.go#L147), [routes_ai.go:419](../../../apps/workflow-go/internal/api/routes_ai.go#L419) | Reuse suitable request construction, adding an activity-safe model call. No durable agent loop or application MCP client was found in the reviewed runtime. |
| Auth derives tenant context; pipeline ACLs distinguish viewer/editor/admin; tenant transactions set RLS context. | [auth.go:54](../../../apps/workflow-go/internal/api/auth.go#L54), [auth.go:110](../../../apps/workflow-go/internal/api/auth.go#L110), [database.go:29](../../../apps/workflow-go/internal/database/database.go#L29), [003_rls.sql](../../../db/003_rls.sql) | Reuse auth/transactions and explicitly authorize new resources. Workers also use direct pool access, so carrying tenant ID alone does not prove isolation. |
| Activities verify saved connector ownership. Payload storage supports inline, PostgreSQL, and encrypted objects. | [activities.go:104](../../../apps/workflow-go/internal/activities/activities.go#L104), [activities.go:286](../../../apps/workflow-go/internal/activities/activities.go#L286), [payloads.go:25](../../../apps/workflow-go/internal/activities/payloads.go#L25) | Reuse storage/crypto, adding trusted reference ownership checks. Persist agent content as encrypted opaque references even for small bodies; inline references may contain the body. |
| User-supplied HTTP destinations use a safe outbound client. | [runtime.go:43](../../../apps/workflow-go/internal/connectors/runtime.go#L43), [http.go:71](../../../apps/workflow-go/internal/connectors/http.go#L71) | Reuse transport checks for HTTP/MCP tools. The model cannot choose arbitrary destinations, credentials, or subprocesses. |
| `node_runs` stores one aggregate row per node with an attempt counter; executions have trace IDs and runtime lineage endpoints. | [activities.go:595](../../../apps/workflow-go/internal/activities/activities.go#L595), [026_runtime_lineage.sql](../../../db/026_runtime_lineage.sql), [routes_lineage_runtime.go:25](../../../apps/workflow-go/internal/api/routes_lineage_runtime.go#L25) | Keep node aggregates and add ordered agent steps beneath them. |
| Audit inserts ignore errors; PostgreSQL outbox delivery patterns already exist. | [auth.go:144](../../../apps/workflow-go/internal/api/auth.go#L144), [dispatchers.go:85](../../../apps/workflow-go/internal/dispatchers/dispatchers.go#L85) | Approval decisions/tool intents need atomic audit. Reuse delivery conventions instead of one best-effort API signal. |

These are code-path findings, not an exhaustive security audit. Runtime regression and two-tenant integration tests remain implementation gates.

## Execution design

1. Save/activation validates `type: "agent"`, `activityType: "agent.run"`, explicit `agentId` and `agentVersion`, schemas, available tool versions, model profile, limits, and engine compatibility. Saved pipelines never reference mutable `latest` agent/tool versions.
2. Before work begins, resolve an immutable tenant-owned run snapshot: agent/tool/schema/policy versions, model identity, and non-secret connection references. Persist sensitive bodies outside Temporal history. Record model tag plus resolved digest when available.
3. `runNode` starts `AgentWorkflow` as a child with stable identity derived from tenant, execution, and node. Input includes trusted identities, version identifiers, input references, and limits. Configure parent-close cancellation and wait for acknowledgement.
4. Deterministically sequence input validation → model budget reservation → model activity → structured-decision validation → tool/schema/policy validation → required approval → tool allowance reservation → tool activity → result append → next model decision. Network, SQL, secrets, and wall-clock access occur only in activities. V1 executes one tool at a time; multiple proposed calls use a recorded stable order.
5. Stop on a validated final answer, cancellation, rejection/expiry, deadline, budget/step exhaustion, unrecoverable error, or ambiguous tool outcome. Validate final output against the versioned schema, write a `DataRef`, and return the existing `NodeResult` with `meta.agentRunId`. Failed agent nodes use existing downstream skip behavior.
6. Preserve outer execution phases. Parent remains `running` during child approval with an additive pending-approval summary. Agent runs have a finer phase. Version history-changing branches and maintain old data-only workflow replay fixtures.

Use bounded `maxSteps` and payload/token ceilings in v1; count every model invocation, including repairs. Introduce Continue-As-New only if tested bounds cannot fit comfortably in history, preserving logical run/approval identity. A final answer is structured output, never permission for another action.

### Data boundaries

Edges still move `DataRef`s. Input bindings select explicit fields or bounded batches from one incoming reference. Multiple incoming references require the existing merge node. No implicit entire-dataset model upload or per-record fan-out. Excess input is rejected; sampling must be explicit and visible in run metadata.

Output is JSON validated against a versioned schema. Agent-to-sink flows produce the record array expected by that sink or use existing transforms. Text is a field in a typed record. Never convert model prose into arbitrary connector configuration.

## Proposed storage and API contracts

All new tables include tenant ID, timestamps, ownership where relevant, and tenant-aware foreign keys. Use RLS and explicit worker tenant predicates. Existing routes and fixtures remain compatible.

| Record | Minimum fields and invariants |
| --- | --- |
| Agent / immutable version | `id`, `version`, `name`, `instructionsRef`, `modelRef`, `inputSchema`, `outputSchema`, `toolVersionRefs`, `limits`, `memoryPolicy`, `createdBy`. Edits create versions; disabling blocks new runs. |
| Immutable tool version | `id`, `version`, `name`, `transport` (`http`/`mcp`), approved endpoint reference, connection reference, HTTP method/MCP tool name, input/output schemas, `effect` (`read`/`write`), approval policy, timeout/response-size cap, declared remote idempotency/reconciliation capability. Disabling blocks later calls in active runs. |
| Agent run | `id`, parent `executionId`, `nodeId`, immutable snapshot references, `phase`, `stopReason`, input/output references, usage, workflow identity, monotonic `revision`. One logical child run per parent node in v1. |
| Agent step / tool-call ledger | `runId`, ordered `sequence`, logical `stepId`, `kind` (`model`/`tool`/`approval`), status, attempt records, provider request ID, argument/output references and hash, timings, usage, sanitized error. Logical tool-call ID survives retries. |
| Approval | `id`, `runId`, `toolCallId`, bound tool version/argument hash/policy, expiry, status/version, actor/reason, decision request ID, delivery/application status. One effective decision. |
| Usage reservation | Run, logical operation ID plus attempt, reserved and actual counters/amount, currency/price snapshot, reconciliation state. Unique attempt reservation; atomic updates under a run-budget lock. |

| Proposed route | Contract / permission |
| --- | --- |
| `GET/POST /api/agents`; `GET /api/agents/{id}`; `POST /api/agents/{id}/versions` | Read permitted agents; workspace owner manages reusable definitions in v1. Return immutable version after validation. |
| `GET/POST /api/agent-tools`; `POST /api/agent-tools/{id}/versions` | List permitted tools; owner registers endpoint/credential/policy references. No secret values in responses. |
| `GET /api/agent-models` | Read configured model profiles: ID/provider/tag/resolved digest/status/observed-at/capabilities/context ceiling/price-known. Operator configuration is authoritative; claimed tool capability requires evaluation evidence. |
| `GET /api/executions/{id}/agent-runs` | Existing execution access plus tenant scope; child summaries. |
| `GET /api/agent-runs/{id}`; `GET /api/agent-runs/{id}/steps?cursor=...`; `GET /api/agent-runs/{id}/usage` | Parent pipeline authorization, bounded pagination, sanitized summaries, separate authorization before stored-body disclosure. |
| `GET /api/approvals?status=pending&cursor=...`; `GET /api/approvals/{id}` | Read pending or decided history within tenant scope as workspace owner or parent-pipeline admin in v1. Pending/decided filters and bounded pagination; decision permission is separately checked against current pending state, expiry and authority. |
| `POST /api/approvals/{id}/decision` | `{requestId, expectedVersion, decision: "approve" or "reject", reason}`. Same request ID/body returns prior result; conflict/stale/expired returns 409. Human identities only in v1. |
| Existing `POST /api/executions/{id}/cancel` | Cancel parent/children; distinguish request acknowledgement from terminal completion. |

Agent phase: `running`, `awaiting_approval`, `completed`, `failed`, `cancelled`. `stopReason`: `budget_exhausted`, `max_steps`, `deadline`, `approval_rejected`, `approval_expired`, `unknown_outcome`, or typed errors. Step status: `pending`, `running`, `succeeded`, `failed`, `cancelled`, `unknown_outcome`. Approval status: `pending`, `approved`, `rejected`, `expired`, `cancelled`; delivery/application separately show whether the durable run consumed the decision.

Use existing error envelopes `{error, code, details?}` with stable invalid-definition, forbidden-tool, invalid-argument, version-conflict, model-unavailable, and budget codes. Server-derived capabilities describe available actions. Reuse pipeline permissions for authoring/running agent nodes; do not invent a separate role system. Tenant/actor/workflow identity comes from auth and database records, never model output or caller-supplied workflow IDs. There is no browser-facing arbitrary tool-execute endpoint.

## Required durability and security semantics

### Tools and credentials

Models select only allowlisted tool versions and propose arguments. Workers validate schema, current authorization, and destination policy immediately before dispatch. MCP descriptions, returned content, retrieved data, and prompts cannot expand permissions, waive approvals, change budgets, or select credentials. Effect classification comes from server policy, not model output or MCP annotations.

Bind HTTP destination, method, and argument placement in the tool definition. Reuse the safe outbound client, including redirect/DNS checks and cancellation; cap request/response size and duration. Operator-approved internal destinations need an explicit trusted configuration path, never a tenant-controlled SSRF bypass. Resolve secrets inside activities from authorized references; exclude them from prompts, workflow history, metadata, and error text.

Initially support approved remote MCP Streamable HTTP servers. Pin tool name/schema hash in a version; material discovery changes require a new reviewed version. Protocol request IDs do not imply remote idempotency. Select a supported MCP implementation after a narrow compatibility check rather than hand-building a protocol framework. Local stdio and arbitrary process execution are outside v1.

### Approvals, expiry, and delivery

Persist approvals bound to tool version, connection reference, destination, normalized argument hash, and policy. Display a sanitized but decision-sufficient preview, retaining encrypted immutable full arguments. Approval cannot authorize altered arguments or subsequent calls. All mutations require approval in v1; policy may also gate reads. Check the current human's authority at decision time and current tool permission at dispatch.

In one transaction, compare `expectedVersion`, verify pending/unexpired state using database time, record actor/decision/audit, and mark pending delivery. A bounded dispatcher sends the decision ID to the child and retries. Reuse the dispatcher pattern; approval rows can carry delivery fields rather than introducing a generic bus. The child re-reads the durable decision, verifies the binding, deduplicates it, and records application. An accepted approval is not a completed tool action.

Expiry uses a Temporal timer and atomic pending→expired database transition. First valid committed decision/expiry transition wins; approval committed before expiry remains valid if delivery is delayed. Cancellation blocks dispatch regardless of approved state. Rejection/expiry ends the run with its reason in v1; another attempt requires a new run/proposal.

### Idempotency and unknown outcomes

Activities can be delivered more than once. Before dispatch, insert/claim a call ledger keyed by tenant/run/logical call ID with immutable argument hash. Completed duplicates return stored output; hash mismatch is a hard conflict. Record every remote attempt.

For documented remote idempotency, send a stable call key, retain it across worker failure, and ensure the remote deduplication window covers the retry window. Reads may retry within limits. For writes lacking idempotency or safe reconciliation, set automatic attempts to one; a timeout/crash after dispatch is `unknown_outcome`. A unique local row cannot prove the remote mutation did not happen. Stop further actions, show reconciliation evidence, and require a deliberate new decision instead of blind retry.

Existing execution retry starts a new execution. Disable that shortcut for runs with successful/ambiguous mutations until an explicit rerun flow presents affected actions and obtains new approvals. Replay/resume preserves logical call IDs; a new run never inherits old approval.

### Budgets, timeout, and cancellation

Retain monthly execution quota. Add per-run input/output token, step, tool-call, wall-time, and optional monetary limits with pinned price/currency. Before each model attempt atomically reserve bounded input plus maximum output; pass an output cap to the provider. Settle once using reported usage. Missing usage is unknown, never zero: retain the conservative reservation and expose uncertainty. Billable retries reserve separately. If a provider cannot enforce a usable cap, do not advertise an exact hard cost guarantee.

Ollama may have no provider charge; report tokens/time and `cost: null` unless a compute price is configured. Execution quota is not model spend. Exhaustion prevents the next activity. Choose defaults from the separate model evaluation rather than marketing benchmarks.

Document `timeoutSec` as the per-node activity start-to-close override; agent runs additionally have an overall deadline covering model/tool calls and approval waits. `maximumAttempts: 1` disables retry; omission retains defaults; reject negative/unbounded unsafe settings. Tool effect policy can only tighten retry limits.

Handle cancel while work is active; propagate child/activity/HTTP cancellation, heartbeat long activities, and close pending approvals. Accepted remote work may still finish; report actual/unknown outcome without promising rollback. Finalize state/reservations through bounded disconnected cleanup; unexpected failures also need durable terminal status. Parent cancellation must not start downstream sinks.

### Memory, payloads, isolation, and retention

Default to run-scoped memory. Persist prompts/results/tool bodies using encrypted opaque references, including small bodies that existing payload storage would inline. History/signals carry IDs/hashes/counters/sanitized summaries. The inspector records observable decisions, calls, and final results; hidden model reasoning is not required.

Every read verifies trusted tenant/run/resource ownership. A supplied `DataRef` is not authority to read its bucket/key. Existing `Payloads.Read` has no tenant parameter; enforce ownership at the new boundary and verify database ownership/object prefixes. Byte limits must not rely only on supplied size metadata.

One run retention policy covers prompts, tool/approval bodies, PostgreSQL payloads, and object lifecycle. Retain non-sensitive audit hashes/identity for the chosen audit duration. Expired content is visibly unavailable. Introduce scoped conversation IDs and access checks only when cross-run memory is implemented; no automatic workspace-wide vector store. Define deletion and key lifecycle together so deleted memory is not readable through retained history/storage copies.

## Monitoring, audit, and lineage

Extend execution detail with child summaries and paginated steps; keep `node_runs` as an aggregate. Record model identity, durations, tool/approval timing, tokens/usage completeness, attempts, stop reasons, and sanitized errors. Distinguish pending approval from a slow model. Minimum run/approval evidence must be available in every edition offering agents; existing enterprise deep-trace gating must not hide OSS operational essentials.

Reuse trace IDs. Audit version publication, policy change, approval, call intent/completion/unknown outcome, cancellation, and memory deletion. Approval/tool-intent audit commits with state transitions; transaction failure prevents dispatch. Metrics use bounded labels and no prompt/secret content.

Connect input assets → agent node/version → output assets, attaching model/tool versions as execution metadata. Tool effects are operations with acknowledged/unknown status, not proof of dataset mutation. Reuse existing lineage/event/OpenLineage delivery where semantics fit and add fixture-tested fields/facets rather than a second graph. Refer to bodies instead of embedding them.

## Ordered backend implementation PRs

These are future implementation PRs, distinct from this architecture PR. Each additive endpoint updates wire fixtures and frontend examples; data-only community behavior remains supported.

| PR | Scope and dependencies | Acceptance gate |
| --- | --- | --- |
| **B1 — Runtime foundations and agent contract fixtures** | Fix validated timeout/retry and manifest direction/dispatch gaps; responsive parent cancellation; additive agent node/schema/engine fixtures; preserve old histories with versioned branches. Coordinate frontend metadata round-trip preservation. | Source/sink contract; per-node override; active-activity cancellation; data-only regression/replay; invalid settings/engines rejected before scheduling. |
| **B2 — Versioned definitions, policy, and tenant storage** | B1 dependency. Immutable agent/tool versions, configured model profiles, owner APIs, RLS/tenant keys, credential references, encrypted reference/retention boundary. No tool execution exposed. | Distinct immutable edit versions; old pipeline version remains pinned; unauthorized/cross-tenant denial; no secrets in public JSON/history. |
| **B3 — Bounded native child workflow** | B2 dependency. Model activity, typed input/output, run/step/usage reads, budget reservations, deadlines/cancel, output DataRef. Tools disabled until B4. | Stub model returns valid output; malformed/oversized output rejected; usage settles once; exhaustion blocks next call; restart preserves identity. |
| **B4 — HTTP/MCP tools and durable approvals** | B3 dependency. Allowlisted tools/schemas, approval decisions and delivery, ledger, remote idempotency/unknown outcomes. | Full source → agent → proposal → approval → tool → output → sink sandbox; reject/expire/conflict; duplicates/crash do not repeat a proven idempotent mutation; ambiguous writes stop for reconciliation. |
| **B5 — Recovery, evidence, and retention** | B4 dependency. Finish monitoring/audit/lineage integration, revocation, cleanup, migration/replay rollout evidence, model configuration docs. Mandatory B3/B4 safety checks are required in those PRs, not postponed here. | Kill worker during model/approval/remote acceptance; cancel each boundary; two-tenant isolation; concurrent/unknown usage; retained/expired payload behavior; OSS example without paid APIs. |
| **B6 — LangGraph adapter and optional cross-run memory** | Separate follow-up after B5 evidence/review. One supported adapter preserves Cohestra identity/tool/approval/budget authority. Memory only with explicit scope/retention. | Same native contract/failure scenarios; graph retries cannot bypass ledger/approval/budget. No general adapter framework before a second working implementation. |

B1 contains gap work packages GAP-BE-01/02, with GAP-CI-01 providing the sandbox; these are not duplicate implementation stacks.

First complete functional flow: B4/F4. Release completion requires B5/F5 recovery and operations evidence. A happy-path demo alone is not production readiness.

## Validation and review gates

PR CI uses a deterministic scripted model, read tool, idempotent write tool, ambiguous write fixture, remote MCP fixture, PostgreSQL, and Temporal. It requires no paid provider or changing public model. Reuse repository test frameworks and compatibility/replay checks.

Minimum matrix: correct typed output; invalid schema; missing/forbidden tools; timeout; token/step/deadline exhaustion; concurrent/duplicate usage settlement; approve/reject/expire/stale/double-submit; missed delivery; restart before/after remote acceptance; cancellation during approval/tool call; permission revocation; cross-tenant identifier/reference attempts; redaction/retention; and data-only regression. Live Ollama evaluation is a separate accuracy/latency/tool-use gate with digest, settings, sample count, hardware, and raw result provenance.

Review accepts or changes workflow-only scope, owner-managed definitions and human pipeline-admin decisions, mandatory mutation approvals, the bounded loop, unknown-outcome policy, retention, and cost semantics. The user approved this architecture direction on 2026-09-14; detailed contracts are in the companion HLD/LLD. This planning PR changes no runtime behavior or default model.
