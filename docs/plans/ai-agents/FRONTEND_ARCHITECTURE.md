# Agent and mixed-pipeline frontend plan

Status: architecture direction approved in the user conversation on 2026-09-14.
The [frontend LLD](FRONTEND_LLD.md) guides the separate implementation PRs.
Baseline: repository main at `857f36f51d9d58c05b32a4d2941448b1eeebbcbd`, inspected 2026-09-09.
Companion: [backend architecture](BACKEND_ARCHITECTURE.md). Backend milestone IDs B1–B6 below refer to that plan.

## Outcome and boundary

A user can define a versioned agent, insert it into an existing data pipeline, select permitted tools and a model, set execution limits, run in Integration, approve a specific tool action, and inspect the resulting data and operational history. The first complete flow is **data source → agent → approved HTTP/MCP action → data sink**. A browser reload or worker restart must not lose the approval or create another action.

Keep the existing React application, React Flow canvas, connector credentials, pipeline lifecycle, run routes, and lineage surfaces. Add a small set of agent views and extend existing views. The current “Build with AI” assistant drafts pipeline definitions; the new agent node executes inside a pipeline. The UI must name these two jobs clearly.

This is a full product delivery plan in increments, not a proposal for a second application. Standalone conversational chat, a plugin marketplace, an additional graph editor, and arbitrary user-written agent code are outside the initial delivery. A LangGraph adapter remains a later explicit increment.

## What the current repository provides

Paths below are repository-relative source evidence. Abbreviated paths within a row share its first directory prefix.

| Existing surface | Evidence | Reuse and implication |
| --- | --- | --- |
| Authenticated app and navigation | `apps/web/src/App.tsx`, `apps/web/src/components/AppShell.tsx`, `apps/web/src/context/AuthContext.tsx` | Canvas is full-screen `/`; operational pages use AppShell. Workspace roles are owner/member. Add agent/approval links to both navigation contexts. |
| Existing pipeline permissions | `apps/workflow-go/internal/api/auth.go:110` | `pipelineAccess` already has viewer/editor/admin ranks, owner and creator handling. Reuse effective permissions rather than build a custom role designer. |
| Catalog-driven canvas | `apps/web/src/pages/PipelineCanvasPage.tsx`, `apps/web/src/pages/canvas/NodePalette.tsx`, `apps/web/src/components/canvas/ConfigPanel.tsx` | Reuse catalog discovery, add-node behavior, config inspection, save/run and Integration/Production lifecycle. Current shared node kinds are source, transform, sink, fork and merge. |
| AI proposal review | `apps/web/src/hooks/useAiGenerate.ts`, `apps/web/src/pages/canvas/AiBuilderPanel.tsx`, `apps/web/src/pages/PipelineCanvasPage.tsx` | Generate/refine already send graph and conversation context. Results use ready/needs_input/rejected; ready proposals require Apply, can be discarded, and support undo. Preserve this review boundary. |
| Pipeline graph serialization | `apps/web/src/utils/pipelineConvert.ts`, `packages/shared/src/types.ts`, `packages/shared/src/mermaid.ts` | Extend the existing conversion contract. Mermaid carries graph structure, not full configuration; stable node IDs preserve config out of band. |
| Connections and credentials | `apps/web/src/pages/ConnectorsPage.tsx`, `apps/web/src/components/canvas/ConfigPanel.tsx`, `apps/web/src/api.ts` | Connection list, credential creation, tests and OAuth exist. Nodes select connectionId; extend that reference pattern for permitted model/tool credentials. |
| Run monitoring | `apps/web/src/pages/RunDetailPage.tsx`, `apps/web/src/components/canvas/ExecutionMonitor.tsx`, `apps/web/src/pages/canvas/OutputDrawer.tsx`, `apps/web/src/pages/MonitoringPage.tsx` | Reuse execution IDs, run route, canvas status and polling. Detail is graph/node state, records, quality results and optional Temporal trace. Model/tool step inspection is additional work. |
| Lineage | `apps/web/src/pages/LineagePage.tsx`, `apps/web/src/pages/RuntimeLineage.tsx`, `apps/web/src/pages/ArchitectureLineage.tsx` | Existing tabs cover runtime metrics, saved architecture and version changes. Extend their entities and links. |
| Entitlements and audit export | `apps/web/src/context/FeatureContext.tsx`, `apps/web/src/api.ts` | Edition response supplies availability/features; downloadAuditExport exists. App.tsx has no audit browse route. An export is not an approval inbox. |
| Existing checks | `apps/web/package.json`, `apps/web/tests/release-a.spec.ts`, `apps/web/playwright.config.ts`, `apps/web/playwright.deployed.config.ts`, `.github/workflows/ci.yml`, `.github/workflows/integration.yml` | Reuse TypeScript checks, existing assert tests and Playwright. PR CI runs build/unit checks; Compose integration is scheduled/manual, and these workflows do not invoke browser e2e. |

## Verified gaps before extending the canvas

1. **Load/save loses supported definition fields.** definitionToFlow and flowToDefinition reconstruct nodes without timeoutSec, retry, inputAssets or outputAssets; page/build conversion also omits pipeline concurrency. Those fields are defined in packages/shared/src/types.ts. Opening a definition carrying them and saving can remove them. Fix the round trip once and preserve future pinned agent/version and mapping fields through the same path. The existing conversion test covers notifications, not these fields.
2. **Model/tool operations have no product contract yet.** App routes and api.ts expose planner generate/refine, connections and pipeline executions, but no agent definitions, approval decisions, model/tool steps, memory or agent budgets. These are planned capabilities, not regressions of an existing agent product.
3. **Runtime feedback is insufficient for approval actions.** ExecutionMonitor.tsx silently catches polling failures and sends signal requests without displaying their failures. Durable approvals need visible disconnected, submitting, accepted and applied states. HTTP success must not imply that the worker completed the approved action.
4. **Shared form accessibility needs attention in the changed path.** Catalog field captions in ConfigPanel.tsx are spans inside divs and are not programmatically associated with their inputs; the connection picker also lacks a label. Give changed controls stable IDs, labels and error associations before reusing them for model, tool and budget settings.
5. **License text is inconsistent.** SettingsPage.tsx says Apache 2.0; root LICENSE contains AGPLv3. Correct product copy to the reviewed repository licensing policy in foundation work; this observation does not settle new model or adapter licensing.

These findings are source inspection, not a claim that deployed browser journeys or the full test suite passed in this planning PR.

## Screens and user flows

### Agents

Add `/agents` and `/agents/:id` under AppShell. Start with a searchable list and one editor with sections for instructions, model, typed input/output, tools, limits and memory. Show immutable version, draft/saved state, validation and referenced pipelines. Saving creates a new immutable version; “Use in pipeline” inserts that exact version. A newer version is an explicit change the pipeline author reviews, not a silent update to saved pipelines.

Select from server-approved model profiles. Show provider, exact tag/resolved digest, supported structured-output/tool capabilities and availability. Keep deployment health separate from task evaluation: installed does not mean accurate. If an evaluation summary exists, show its date, corpus/sample count, outcomes and latency, with a report link. Do not show a universal accuracy percentage. The Notion/model investigation informs the default profile without hard-coding a winning model into the UI.

Limits cover maximum steps, input/output tokens, elapsed time and optional monetary cap, with explicit units. Local inference still has token/time limits; unknown monetary cost displays Unavailable, not zero. Memory initially offers explicit run-local state, retention information and permitted prior-context references. Persisted reuse is enabled only when the tenant-scoped backend policy exists; vector search and autonomous long-term learning are not implied.

### Tools and credentials

Extend Connections with a Tools section/tab instead of adding a credentials store. An HTTP/MCP tool definition shows schema, destination/server, read/write classification, credential reference, allowed operations, timeout, approval requirement and connection status. Server discovery may populate a draft; registration and policy approval remain deliberate actions. Credentials never appear in pipeline JSON, proposal text, run previews, DOM attributes or exports.

Reuse credential create/test where the backend provider supports it. MCP/model-specific support must land in B2/B4 before those options appear. Tests must not execute side-effecting production actions. Owners manage reusable agents, tools, destinations and approval policy. Authoring/running agent nodes inherits pipeline permissions; human owners or existing pipeline admins decide approvals under the server policy. Render server-supplied effective capabilities and enforce permissions in the API.

### Mixed pipeline builder

Add an Agent catalog category and agent.run entry using the agreed shared node contract. The inspector selects a saved agent version and maps upstream data to its input schema; expose the output schema to downstream nodes. Initially support a bounded batch of records with explicit input limits. Per-record fan-out requires backend concurrency and aggregate budget semantics before the UI offers it.

Pin the agent version and configuration snapshot through save/load, conversion, AI Apply/Undo and Mermaid edits. Show source data and resulting artifact references with bounded authorized previews rather than loading large datasets into browser state. Reject missing input fields/incompatible output mappings before Run; display authoritative server validation at the affected node/field.

Agent execution initially uses the workflow engine. Disable incompatible stream/Spark/Flink choices for mixed graphs with an explanation; do not silently change the engine. Preserve save-before-run and Integration-before-promotion behavior. Generated proposals may suggest permitted agent versions/tools but cannot introduce credentials, grant permissions or approve their own actions.

### Run inspector and approvals

Extend `/runs/:id` with an accessible node list alongside the graph. Selecting an agent opens chronological model, tool and approval steps with timestamps, attempt, state, output preview, usage and stop reason. Show pinned agent version/model, parent execution/node and referenced inputs/outputs. Exclude hidden chain-of-thought; display observable messages, structured requests/results and concise permitted summaries.

Use awaiting_approval for the agent state and a linked Review action entry. Add `/approvals` with pending/decided filters and durable detail. Show the exact tool, destination, sanitized arguments, expected effect, requesting agent/run, expiry and eligible decision-makers. Bind the decision to the immutable call and argument hash. Editing arguments creates a new request; generic Resume is not approval.

Retain one request ID across decision retries, disable duplicate submission and show authoritative decision/application status. On conflict, refresh and show the recorded decision; expiry/cancellation disables controls. If submission times out, refetch the same request before retrying. A recorded decision pending delivery differs from a completed action. Unknown external outcomes require reconciliation, not blind Retry. Rejection/expiry shows the stop reason and backend-permitted next action.

Keep pipeline pause, agent approval wait, terminal failure and cancellation distinct. Parent executions keep existing phases; child agent state uses the new enum. Render controls from allowed actions. Poll through the existing API layer initially, cancel requests/timers on route changes, show last successful refresh/errors and preserve selection. Add streaming only if polling proves inadequate.

### Monitoring, audit and lineage

Extend Monitoring with agent/model/tool failures, waiting approvals, budget stops and recorded usage. Link each entry to the parent run and selected step. Token and monetary usage have different units/availability; retain estimated-value provenance.

Add run Activity from the proposed audit read projection: agent version changes, tool requests, decisions and outcomes. Reuse export where its policy permits those fields. Basic agent run/approval history must work in the agreed OSS milestone; optional deep Temporal trace remains a separate entitlement decision.

Architecture lineage adds saved agents/versions and declared data inputs/outputs. Model/tool versions remain execution metadata; tool effects are operations with acknowledged/unknown status, not additional guaranteed dataset mutations. Runtime lineage links actual agent/tool calls, artifacts and execution/trace IDs. Do not invent field-level lineage for probabilistic transformations: label declared bindings and observed outputs with provenance. Reuse bounded windows/filters and load step details on selection.

## Proposed backend contracts

All endpoints below are proposals, not existing callable APIs. B1 locks public types, pagination, validation/error shape; B2/B4 finalize policies before UI wiring.

| Resource | Proposed API | Minimum frontend needs |
| --- | --- | --- |
| Definitions | `/api/agents`, `/api/agents/{id}/versions` | Immutable IDs/version, schemas, model profile, limits, tool bindings, memory policy, validation, capabilities and pagination. |
| Model profiles | `GET /api/agent-models` | Configured id/provider/tag/resolvedDigest/status/capabilities/contextLimit/priceKnown, timestamped health and optional task evaluation evidence. Operator configuration is authority; browser never contacts Ollama directly. |
| Tools | `/api/agent-tools` | Versioned schemas/destination metadata, connection references, classification and permission/approval policy. Existing `/api/connectors` continues credential management. |
| Child runs | `/api/executions/{id}/agent-runs`, `/api/agent-runs/{id}` | Parent execution/node, pinned definition/model, phase/stop reason, times, output refs, pending approvals and allowed actions. |
| Steps/usage | `/api/agent-runs/{id}/steps`, `/api/agent-runs/{id}/usage` | Stable IDs/order, pagination, attempts/outcomes, redacted bounded previews, tokens, known/estimated/unavailable money, consumed/reserved amounts and limits. |
| Approvals | `/api/approvals`, `/api/approvals/{id}/decision` | Tenant-filtered requests, state/version, call/argument hash, expiry/actor, allowed decisions. Submit requestId, expectedVersion and approve/reject decision. Separate recorded decision from application status; detail-read shape finalized in B4. |
| Audit/lineage | Extend execution/lineage projections; audit read endpoint finalized in B5 | Correlation IDs, actor/action/outcome, sanitized metadata and typed declared/observed relationships. |

Agent phases: `running | awaiting_approval | completed | failed | cancelled`. Stop reasons include budget_exhausted, max_steps, deadline, approval_rejected, approval_expired and unknown_outcome. Preserve parent paused semantics. Display unknown future states visibly and disable unsafe actions until understood.

Tenant identity comes from the authenticated server session. URL IDs are selectors, not authorization. Add typed methods in apps/web/src/api.ts and shared public types in packages/shared. Reuse the existing query/error components; preserve conflict/permission/expiry distinctions for these operations. No second HTTP or state library is needed.

## Ordered frontend implementation PRs after review

Each PR is separate from its backend counterpart and targets a runnable user increment. Contract fixtures may permit parallel development, but integrated sandbox acceptance must pass before declaring completion. Production activation is outside this planning PR.

| PR | Backend dependency | Deliverable and acceptance |
| --- | --- | --- |
| **F1: Preserve definitions and establish browser checks** | B1 | Preserve supported node/pipeline fields across load/save and AI/Mermaid edits; label changed controls; show changed nodes/configuration before applying a proposal using existing Apply/Undo. Coordinate license copy with GAP-DOC-01 rather than duplicate it. A regression check carries timeout/retry/assets/concurrency and an unchanged pipeline. Wire deterministic browser checks into PR CI with the B1 sandbox. |
| **F2: Agent definitions and permitted selection** | B2 | List/editor, immutable version save, approved profiles, tool references, limits/memory and effective capabilities. Validate required fields/errors; save/reload exact definitions. Members cannot invoke owner-only definition/policy actions by URL or altered requests. |
| **F3: Mixed canvas and model-only inspection** | B3 | Agent node, bounded batch mappings, workflow-engine validation, version pinning and model steps in runs. Execute data → agent → sink in Integration, inspect result/usage and reload without creating a new run. |
| **F4: HTTP/MCP tools and durable approval** | B4 | Registration/selection, queue/detail, exact-call decisions, conflict/expiry/submission states and stop reasons. Complete source → agent → approved tool → sink. Reject, expiry, concurrent decisions, lost responses and cancellation cause no duplicate side effect. First complete user milestone. |
| **F5: Recovery, operations and lineage** | B5 | Accurate reconnect/stale state, unknown-outcome reconciliation, Monitoring usage/wait filters, Activity, lineage and retention states. Restart retains pending approvals; run/step/audit/lineage agree; redaction/expiry persists on reload/export. |
| **F6: LangGraph and OSS example** | B6 | Expose supported adapter capabilities through the same editor/mappings/approvals/inspector. Run the documented OSS example end to end, make unsupported options explicit, and keep the native path working. |

Default order is F1 → F2 → F3 → F4 → F5 → F6. A split must retain its integrated acceptance gate. B5 recovery tests may develop alongside F4, but release completion explicitly requires B5/F5 recovery and operations evidence. Mandatory B3/B4 safety checks cannot be deferred to B5.

## Sandbox, accessibility and acceptance evidence

Use installed Playwright. Keep API-mocked UI checks distinct from real sandbox journeys with API, database and Temporal workers. PR CI must run the latter with disposable fixtures, a deterministic fake model and local HTTP/MCP tools; no paid providers, personal credentials or large Ollama downloads. Reuse Compose smoke/browser patterns but explicitly provide the sandbox base URL rather than accidentally using the deployed test default.

| Scenario | Required evidence |
| --- | --- |
| Existing pipelines | Load/edit/save retains timeout/retry/assets/concurrency and configs; source/transform/sink still executes. |
| Authoring | Immutable version round trip; schema/mapping/limit errors attach to fields; unsupported profiles rejected; AI review/undo preserved. |
| Happy path | Real sandbox source → agent → permitted HTTP and MCP fixture calls → sink with known output and correlated usage/run IDs. Exercise both transports. |
| Durable approval | Reload, two decision-makers, stale version, expiry, rejection, timeout after recorded decision and worker restart preserve one decision/side effect. |
| Limits and failures | Invalid model output, tool failure, step/budget/deadline stops and cancellation show correct state/reason/actions. Unknown outcome never offers unqualified retry. |
| Access/data | Owner/member and pipeline permissions, cross-tenant rejection and session recovery; no secrets/disallowed previews in responses, screenshots, browser storage or exports. |
| Accessibility | Keyboard add/configure/connect/run/review; list/form alternative to drag-only graph operations; associated labels/errors; visible focus; dialog focus containment/return and safe Escape; announced pending/errors; text beyond color; usable 390px and 200% zoom in both themes. |
| Operations | Stable step order/pagination during refresh; inaccessible/redacted/expired previews distinguished; monitor/audit/lineage resolve to the same immutable run/version. |

Live Ollama evaluation is a separate recorded gate for the deployment profile. Use the same task fixtures and output validation as backend evaluation; record tag/digest, hardware, context, corpus, repeat count, failures and latency. Schema-valid does not prove semantic accuracy. A model is not default until builder and agent cases pass separately defined criteria.

Each implementation PR records exact commands, pass/fail counts and relevant screenshots/traces, redacting fixture credentials/payloads. This planning document does not report future acceptance tests as already run.

## Approved direction and implementation defaults

- Extend the existing app/canvas using the approved B1–B6/F1–F6 sequence.
- Use owner management, inherited pipeline author/run permissions and owner/pipeline-admin approval authority.
- Provide native agent execution, approval history and the first example in the intended OSS edition.
- Align bounded batch size, retention and model criteria with the backend LLD. Deployment model promotion requires separate measured planner and agent evidence.
