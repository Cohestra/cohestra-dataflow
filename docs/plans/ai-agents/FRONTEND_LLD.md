# Frontend low-level design: agents and mixed pipelines

Status: implementation design, 2026-09-14, following maintainer review of the agent planning PRs (#38–#40). The [frontend architecture](FRONTEND_ARCHITECTURE.md) and [backend architecture](BACKEND_ARCHITECTURE.md) are the parent direction, and the [backend LLD](BACKEND_LLD.md) is authoritative for wire contracts. This document specifies the next implementation work, not an already shipped capability or approval to deploy. Application source remains the inspected main baseline `857f36f51d9d58c05b32a4d2941448b1eeebbcbd`.

Read with the [system HLD](ARCHITECTURE_HLD.md), [backend LLD](BACKEND_LLD.md), [product work packages](PRODUCT_GAPS.md) and [sandbox gates](SANDBOX_AND_CI.md). All new routes, types and component names below are **proposed**. Existing paths are identified explicitly. Backend LLD is the authority for wire names and server-side invariants; shared fixture changes must update both consumers in the same contract increment.

## 1. Responsibilities and minimum component map

Keep React, React Router, React Flow, native controls, existing Tailwind styles, api.ts and useApiQuery. No second canvas, global query cache, custom form framework or generalized agent SDK is required. New files below have one concrete screen or reusable UI responsibility; do not create empty scaffolding ahead of its F milestone.

| Owner / proposed change | Existing reuse and implementation responsibility | Milestone |
| --- | --- | --- |
| Existing `apps/web/src/App.tsx` and `apps/web/src/components/AppShell.tsx` | Lazy routes for Agents and Approvals inside current protected/feature/catalog providers; current navigation, route fallback and error boundary. Create remains owner-only; server authorizes every call. | F2/F4 |
| New `apps/web/src/pages/AgentsPage.tsx` | List, search, pagination, create entry, loading/empty/error states. Read-only users can select permitted immutable versions without owner actions. | F2 |
| New `apps/web/src/pages/AgentEditorPage.tsx` | Local unsaved definition, version history/selection, form validation and immutable publication. One editor handles new and existing agents. | F2 |
| Existing `apps/web/src/pages/ConnectorsPage.tsx`; new `apps/web/src/components/agents/AgentToolsPanel.tsx` | Add Tools tab using existing connection list/create/test. Panel edits only tool definitions/policies and saved connection references; secrets remain in credential creation. | F2 metadata/F4 execution-capable tools |
| Existing `apps/web/src/pages/canvas/NodePalette.tsx`, `apps/web/src/components/canvas/FlowNode.tsx`, `apps/web/src/pages/canvas/InspectorPanel.tsx`; new `apps/web/src/components/agents/AgentNodeSettings.tsx` | Agent category/icon and exact-version selection/input binding in the existing inspector. Do not turn all catalog fields into a new schema-rendering system. | F3 |
| Existing `apps/web/src/pages/PipelineCanvasPage.tsx`, `apps/web/src/utils/pipelineConvert.ts`, `apps/web/src/utils/validatePipeline.ts` | Own canonical editable definition, graph projection, preservation, node validation, unsaved state and save/run/lifecycle. Existing AI proposal review receives changed-field summary. | F1/F3 |
| Existing `apps/web/src/pages/RunDetailPage.tsx`, `apps/web/src/pages/runGraph.ts`; new `apps/web/src/components/agents/AgentRunInspector.tsx` | Select agent from graph or node list; show child state, steps, usage and authorized previews. Parent page owns parent cancel/retry actions. | F3/F5 |
| Existing `apps/web/src/components/canvas/ExecutionMonitor.tsx` and `apps/web/src/pages/canvas/OutputDrawer.tsx` | Show child approval summary and links into the canonical run inspector. Fix visible polling/action errors in this touched flow. Do not duplicate step fetching in every canvas node. | F3/F4 |
| New `apps/web/src/pages/ApprovalsPage.tsx`, `apps/web/src/pages/ApprovalDetailPage.tsx` | Queue/history and durable request view/decision interaction. Share the same request details when opened from a run. | F4 |
| Existing `apps/web/src/pages/MonitoringPage.tsx`, `apps/web/src/pages/LineagePage.tsx`, `apps/web/src/pages/RuntimeLineage.tsx`, `apps/web/src/pages/ArchitectureLineage.tsx` | Add agent operational summaries, run Activity and declared/observed lineage projections. Tool/model versions are metadata; tool calls do not imply successful dataset mutation. | F5 |
| Existing `apps/web/src/api.ts`, `apps/web/src/hooks/useApiQuery.ts`, `apps/web/src/components/ApiError.tsx`; new `packages/shared/src/agents.ts` exported from shared entry point | Typed new resource requests, structured errors, abort forwarding and shared public contracts. Keep existing API behavior compatible. | F1–F5 as needed |

The existing query hook is per-component request state, **not a shared cache**. It retains previous data while loading, clears data on errors and soft-cancels stale results; most current API methods do not forward its AbortSignal. New reads must forward the signal. Existing error parsing stringifies HTTP/body information; agent mutations need explicit status/code/details without showing raw server bodies to users.

## 2. Routes, navigation and deep links

| Proposed route | State carried in URL | Behavior |
| --- | --- | --- |
| `/agents` | `search`, opaque `cursor` | Permitted agent summaries; Create for owner. Empty means no records only after a successful load. |
| `/agents/new` | None | Empty local draft initialized from configured model/default limits; no creation until Save. |
| `/agents/:id` | Integer `version` | Exact immutable version when given; otherwise load current published version once. Editing creates a local draft; background metadata refresh cannot replace it. |
| `/connectors?tab=tools` | `tool`, integer `version` if selected | Existing Connections page with Tools section; normal connector routes remain compatible. |
| `/?pipeline=:rowId` | Existing pipeline selector | Existing builder. Use in pipeline inserts a node with exact version into the current editable graph through explicit navigation state; opening a different pipeline requires saving/discarding dirty edits. |
| `/runs/:id` | `node`, `agentRun`, `step`, `tab` | Canonical parent execution. Backend verifies that selected child/step belongs to this parent; URL selectors never grant access. Tab defaults to run summary. |
| `/approvals` | `status=pending|decided`, opaque `cursor` | Server-filtered queue/history; no cross-tenant client filtering. |
| `/approvals/:id` | None | Direct-linkable authoritative request; after decision it remains readable within approved read policy. An inaccessible ID renders the same not-found state as a nonexistent one (the API returns 404 for both), so IDs cannot be probed. |

Add Agents/Approvals to AppShell and reachable canvas navigation. Keep Build with AI as pipeline drafting, not an agent chat route. Do not expose prompts, input rows, approval arguments, credential names containing secrets or model responses in URLs. Invalid route selectors show a safe invalid/not-found state; never silently switch to another version or tenant.

## 3. Shared wire contract and examples

New collection reads use `{items, nextCursor}`; a missing cursor is represented as null. Cursor is opaque; use URLSearchParams and encode resource identifiers. Page size defaults to 50 and caps at 100. Do not use the existing listAllPipelines loop for unlimited agent/step history. Dates are UTC ISO strings; display local time with the full timestamp available. Nullable usage/digest values mean unknown, not zero or an empty successful result.

Core TypeScript projections, with complete definitions finalized in shared fixtures:

```ts
type Page<T> = { items: T[]; nextCursor: string | null };
type AgentPhase = 'running' | 'awaiting_approval' | 'completed' | 'failed' | 'cancelled';
type ApprovalStatus = 'pending' | 'approved' | 'rejected' | 'expired' | 'cancelled';
type ApiFailure = {
  error: string;
  code: string;
  details?: { fieldErrors?: Array<{ path: string; message: string }>; currentVersion?: number; currentStatus?: string };
};
type AgentNodeConfig = {
  agentId: string; // canonical lowercase UUID (36 chars); server rejects other forms
  agentVersion: number; // positive safe integer, exact immutable version
  inputBinding: { mode: 'batch'; fields: string[]; maxRecords: number }; // fields: unique literal top-level keys; maxRecords: 1..100
};
type AgentRunSummary = {
  id: string; executionId: string; nodeId: string;
  agentId: string; agentVersion: number;
  phase: AgentPhase; stopReason: string | null; revision: number;
  controlState: 'active' | 'paused' | 'cancel_requested';
  startedAt: string; completedAt: string | null;
  model: { profileId: string; tag: string; resolvedDigest: string | null };
  pendingApprovalIds: string[]; outputAvailable: boolean; capabilities: string[];
};
```

Unknown additive object fields may be ignored by rendering. Unknown phase/status strings must render as an unsupported state with actions disabled; a TypeScript cast is not wire validation. Treat bodies as untrusted data and render as text, never executable HTML or browser tool requests.

| New API method in api.ts | Proposed endpoint | Required response/use |
| --- | --- | --- |
| listAgents / getAgent | GET `/api/agents`, GET `/api/agents/{id}` | Paged summaries/current metadata; detail capabilities control edit/use actions. |
| listAgentVersions / getAgentVersion | GET `/api/agents/{id}/versions`, GET `/api/agents/{id}/versions/{version}` | Version summaries and one exact immutable definition; selected schema/limits must come from that version. |
| createAgent / publishAgentVersion | POST `/api/agents`, POST `/api/agents/{id}/versions` | One immutable version; dedupe requestId, publication expectedVersion equals current published head. |
| listAgentModels | GET `/api/agent-models` | Operator-approved profiles, observed health, capabilities/context ceiling/price availability; no browser-to-Ollama connection. |
| listAgentTools / getAgentToolVersion / publishAgentToolVersion | GET `/api/agent-tools`, GET `/api/agent-tools/{id}/versions/{version}`, POST `/api/agent-tools` or `/api/agent-tools/{id}/versions` | Permitted versioned tool metadata, schema, effect/policy and connection references. HTTP/MCP registration does not execute the tool. |
| listExecutionAgentRuns / getAgentRun | GET `/api/executions/{id}/agent-runs`, GET `/api/agent-runs/{id}` | Child summaries and current revision/state. |
| listAgentSteps / getAgentUsage | GET `/api/agent-runs/{id}/steps`, GET `/api/agent-runs/{id}/usage` | Ordered paged summaries, attempts, aggregate usage with completeness/reservations. |
| getAgentStepPreview / getAgentOutput | GET `/api/agent-runs/{id}/steps/{stepId}?include=preview`, GET `/api/agent-runs/{id}?include=output` | Explicit read_preview authorization; bounded JSON bodies with contentStatus, never raw storage references. |
| setAgentDisabled / setAgentToolDisabled | PATCH `/api/agents/{id}`, PATCH `/api/agent-tools/{id}` | Owner mutation `{requestId, expectedRevision, disabled}`; resource revision is separate from immutable definition version. |
| listApprovals / getApproval / decideApproval | GET `/api/approvals`, GET `/api/approvals/{id}`, POST `/api/approvals/{id}/decision` | Full authorized request snapshot; decision returns that snapshot and separates recording from application. |
| cancel parent | Existing POST `/api/executions/{id}/cancel` | Accepted request then poll authoritative parent/child state; no optimistic terminal result. |

Collection Create can use the existing owner role for presentation, but the API remains authoritative. Resource actions use server capabilities. Unknown capability names do not grant access. Agent capabilities are use_agent, with owner-only create_version/read_instructions/disable; tool owners receive create_version/disable. A validated available model may expose use_model. Run capabilities include read_steps/read_usage/read_preview and inherited cancel; approval capabilities include approve/reject only when currently permitted. Unknown strings are ignored. The UI calls parent cancellation for a child's cancel action; parent rerun is unavailable after successful/ambiguous mutations. A disabled flag uses expectedRevision, never agentVersion, and must not mutate historical definitions.

### Agent definition and node

The new-definition/version request carries requestId and a definition; version publication additionally requires expectedVersion, while initial creation omits it. The definition contains name, instructions, modelProfileId, inputSchema, outputSchema, pinned tools, limits and memoryPolicy `{scope: 'run', retentionDays: 7}` as the initial operator-bounded policy. Instructions are an authorized authoring body stored through the backend's encrypted-reference boundary; list responses exclude instruction bodies. A user permitted to use an agent is not automatically permitted to retrieve its full authoring body. read_preview is independent of read_instructions: model-step prompt previews must omit/redact agent/system instruction segments unless the caller also has instruction-disclosure permission. The API applies this before returning preview/export/history data; hiding instruction text only in the browser is insufficient. A permitted preview may therefore contain explicit restricted segments while showing allowed user inputs and observable outputs.

The following **synthetic node** demonstrates mapping; it is not a shipped catalog entry:

```json
{
  "id": "classify_tickets",
  "type": "agent",
  "activityType": "agent.run",
  "label": "Classify tickets",
  "config": {
    "agentId": "11111111-1111-4111-8111-111111111111",
    "agentVersion": 3,
    "inputBinding": {"mode": "batch", "fields": ["ticket_id", "subject"], "maxRecords": 100}
  },
  "timeoutSec": 60,
  "retry": {"maximumAttempts": 1},
  "inputAssets": [],
  "outputAssets": []
}
```

fields is a nonempty unique array of literal top-level source keys: no dotted paths, expressions or implicit renaming in v1. Each selected field must exist in every projected row; missing/oversized input fails visibly. The resulting bounded array must satisfy the pinned inputSchema. Multiple upstream references use the existing merge node first. Excess rows do not silently sample/truncate. Output is a schema-valid record array, or passes through an existing transform before the sink.

Suggested configured initial limits are maxSteps 8, maxToolCalls 4, maxInputTokens 16384, maxOutputTokens 4096, maxOutputTokensPerCall 512, deadlineSeconds 900 and approvalTtlSeconds 600, with maxCostMicros/currency null when unpriced. These are conservative design defaults bounded by operator caps, not measured model-promotion thresholds. Display server-returned values instead of copying constants throughout components. timeoutSec describes an individual activity timeout; the agent deadline includes the entire loop and approval wait. Tool retry policy can tighten node settings.

### Approval request and decision

Example GET response; UUIDs, hashes and arguments are synthetic fixture data:

```json
{
  "id": "22222222-2222-4222-8222-222222222222",
  "runId": "33333333-3333-4333-8333-333333333333",
  "executionId": "44444444-4444-4444-8444-444444444444",
  "nodeId": "classify_tickets",
  "version": 1,
  "status": "pending",
  "tool": {"id": "55555555-5555-4555-8555-555555555555", "version": 2, "name": "Set ticket category", "effect": "write", "destination": "https://support.example.test/tickets"},
  "argumentHash": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "argumentPreview": {"ticket_id": "demo-42", "category": "billing"},
  "expiresAt": "2026-09-14T12:10:00Z",
  "decision": null,
  "deliveryStatus": "not_required",
  "applicationStatus": "not_applied",
  "capabilities": ["approve", "reject"]
}
```

```json
{
  "requestId": "66666666-6666-4666-8666-666666666666",
  "expectedVersion": 1,
  "decision": "approve",
  "reason": "Reviewed the ticket category change."
}
```

POST returns HTTP 200 with the updated approval snapshot. status approved plus deliveryStatus pending and applicationStatus not_applied means **Approval recorded; waiting for execution**. It does not mean the external action succeeded. The returned decision is `{requestId, value, actorId, decidedAt, reason}`; actorId and decidedAt are server-owned, and value records the submitted decision. Do not send tenant, actor, argument changes or workflow IDs in this request.

Example recoverable conflict:

```json
{
  "error": "This approval has already been decided.",
  "code": "ERR_APPROVAL_CONFLICT",
  "details": {"currentVersion": 2, "currentStatus": "approved"}
}
```

Backend LLD defines the final code constants. Match on those codes/status, never on translated message substrings. Reuse the same requestId and exact body for transport retries. A changed decision or reason requires a new deliberate submission after refreshing the authoritative request.

## 4. Editor state, publication and validation

AgentEditorPage owns `{loadedVersion, publishedHead, draft, dirty, submitState, fieldErrors}`. Add exact-version disable/enable policy actions only to permitted owner detail, using the independent resource revision; disabling an agent blocks new runs without altering a saved version; current tool/model policy revocation blocks the next affected call in active runs. Hydrate once when the agent/version route key changes. Model/connection health refresh only updates availability, never draft instructions/schema/tools. Owned editor navigation to another resource while dirty offers Save/Discard/Stay; full browser unload gets the native unload warning. App.tsx currently uses BrowserRouter: do not assume a data-router-only blocker hook works here. F2 must verify browser Back/Forward as well as in-app links; if full route blocking needs React Router's data-router boundary, migrate the existing route tree once without changing URLs/providers rather than adding ad hoc popstate cancellation. The navigation test is required before claiming draft protection across all navigation. Draft contents are held in memory and never placed in localStorage, sessionStorage, telemetry or URLs. Explain unsaved loss instead of promising persistence.

Form sections, with native inputs/textarea/select/fieldset:

| Section | Client checks and server authority |
| --- | --- |
| Name/instructions | Required nonblank values; show the relevant server size limit/error. No executable interpolation. Instructions are not copied into model health/usage requests. |
| Model | Required configured profile; show tag/digest/health age and evaluated capabilities. Refresh stale status before publication/run; server revalidates. Unavailable model is not silently replaced. |
| Input/output schema | Editable JSON textarea with labeled examples; JSON.parse catches syntax. Backend performs full supported-schema validation; do not build an incomplete JSON Schema validator in browser. Render schema field errors from API. |
| Tools | Select permitted exact tool/version, show effect/destination/approval policy and capability status. No arbitrary URLs, secret text, duplicate tool version bindings or model-granted permissions. |
| Limits | Positive finite safe integers for step/token/call/time limits; per-call output cannot exceed total output; approval TTL cannot exceed overall configured deadline. Monetary input converts fixed decimal currency units to integer micros with exact decimal parsing; reject excess precision, negative values and unsafe integer overflow. Empty money stays null, not zero. API checks operator caps/price availability. |
| Memory | Run-only scope in v1 with explicit retention information. Disable cross-run controls until the scoped backend contract exists; do not auto-enable storage because a text field is present. |
| Node binding | Positive maxRecords no greater than 100 or a tighter server bound, required unique literal fields, exactly one incoming data reference (merge first), workflow engine and exact agentVersion. Backend checks actual data/schema at execution. |

Local errors associate with inputs using aria-describedby and aria-invalid. On submit failure focus the first invalid field and announce the summary. Use server fieldErrors paths to locate the matching agent field/node; unknown paths remain in the summary instead of being dropped. Client success is not proof of valid permissions or model capability.

Create/publication starts only on Save. The 201 response contains `{id, version, revision}`, not the full normalized definition; then GET that exact version to hydrate the saved state. If that read fails, show “Version saved; details unavailable” and offer a read retry without publishing again. Generate one browser-native UUID requestId before the request and retain it in submit state. Versions use expectedVersion from the current published head, even when editing an older version. On 409 preserve draft and display the newer head plus changed fields; do not automatically rebase/publish. On lost response, retry the exact deduplicated request, not a new publication ID. Navigation after a reload refetches versions; it does not resubmit a remembered mutation. An expired session requires sign-in/review again without storing sensitive draft contents for automatic replay.

## 5. Lossless canvas projection and review rules

F1 fixes the existing converter once before F3 adds agents. The in-memory graph is a view of a complete editable pipeline definition, not its only storage format.

1. Keep a canonical base definition in PipelineCanvasPage and the original server-approved PipelineNode in each graph node's data.definition. Use native structuredClone on JSON-compatible definition snapshots at hydration/Undo boundaries so nested config arrays cannot share mutable draft state. Existing convenience fields may remain while components migrate. Inspector edits overlay the corresponding definition fields. This avoids unrelated field loss without a repository-wide form rewrite.
2. Export begins with the base definition, applies explicit edited name/trigger/execution/metadata/SLO/notification/concurrency fields, and exports each preserved node overlaid with edited id/type/activityType/label/config/ingestion/merge settings. Preserve timeoutSec, retry, inputAssets and outputAssets as first-class node fields. Agent version/inputBinding live in config and survive unchanged unless the user edits them.
3. Never serialize React Flow layout/internal IDs, live status/recordCount, selection, errors, capabilities, expanded panels or approval decisions as pipeline config. API-derived tenant/creator/version publication remains authoritative; equality checks exclude legitimate server-assigned identity/version changes. Server validates known public schema; preserving loaded config is not permission to accept arbitrary model fields.
4. Mermaid changes graph structure only. Matching stable node IDs retain **all** supported non-graph configuration/policy/asset fields. New agent nodes without a selected version stay invalid. A changed activityType invalidates incompatible configuration rather than silently carrying secrets or an old agent binding into another connector. Renamed/deleted IDs require explicit review of removed bindings.
5. AI proposals stay detached until Apply. Compare added/removed/changed nodes/edges and changed config/policy fields against the exact draft used to request the proposal. If the user edits the canvas while generation is running, mark the result based on an older draft and require regeneration/review; do not overwrite the intervening edit automatically. Apply restores a complete previous-definition snapshot to support Undo, including metadata and policies, not just nodes/edges.
6. Use one semantic save fingerprint that excludes live/layout state, includes every supported persisted field and normalizes object key ordering. After save returns its persisted identity/version, read that exact saved pipeline before updating the normalized base/fingerprint. If readback fails, retain the acknowledged saved identity and offer Refresh; do not call Save again to resolve a read failure. Loaded unsupported contract versions are read-only until support exists; do not silently drop fields to make saving succeed.

Add agent to both shared NodeType/NodeKind and the catalog/Go mirror in B1 contracts. Extend validatePipeline for agent connectivity/missing configuration and invalid edge endpoints/duplicate node IDs in the changed flow; keep server DAG validation authoritative. Node labels show Agent, version and textual runtime status. Keyboard users select nodes from a list and connect via source/target selects, exercising the same add/connect handlers as pointer input.

## 6. Query ownership, refresh and mutation rules

Use useApiQuery for new key-based resource reads. For mutable live detail, extend the existing useEffect polling pattern inside AgentRunInspector/ApprovalDetailPage: one sequential setTimeout loop, one in-flight request, AbortController cleanup and a current-route guard. Do not use overlapping async setInterval calls. Active run/step/approval detail polls every 2 seconds; while the snapshot is unchanged, back off by 1.5× per poll up to 10 seconds, and reset to 2 seconds on any change or user action. A queue can refresh every 15 seconds while visible. Pause background polling when the document is hidden, refresh on return, and stop terminal run polling. Pending approval delivery may continue until applied even after the decision itself is final. Stop on authoritative run cancellation/revocation/terminal failure even when a previously approved decision will never be applied; show that reason rather than polling indefinitely.

| Owner / query key | Refresh or invalidation |
| --- | --- |
| AgentsPage: tenant + search + cursor | Reset cursor when search changes; refresh current page after returning from successful publication. No hidden all-page prefetch. |
| AgentEditorPage: tenant + agent ID + selected version | Immutable detail loads once; explicit version switch replaces draft only after dirty-state decision. Separately refresh head/availability. |
| AgentNodeSettings: tenant + agent ID + exact version | Cache only mounted response state; do not substitute latest when exact version is unavailable. Refresh permitted versions after returning from creation. |
| AgentRunInspector: tenant + execution + child run | Refresh summary/current step tail/usage together, ignore lower revisions; keep selected historical step stable. Fetch older pages only on Load more. |
| ApprovalDetailPage: tenant + approval ID | GET before enabling decision, refresh after submission/conflict/visibility change, poll delivery until applied. Clear on permission loss. |
| ApprovalsPage: tenant + status + cursor | Reset cursor on filter change; refresh when returning from detail so a decided row leaves pending. |
| Monitoring/lineage | Existing bounded windows plus explicit Refresh. Decision completion refreshes an open related projection when navigating back; no application-wide event bus. |

For live states retain the last authorized successful snapshot during a transient network failure and label it Stale with last successful timestamp. Disable decisions/actions until a current authoritative read succeeds. A 403/404 after revocation clears sensitive data immediately; it is not a stale-success case. Existing useApiQuery clears on error, so this last-good behavior belongs in the live inspector/approval owner (or a small compatible hook enhancement), not a claim about current hook semantics.

List pagination is cursor-based, ordered by backend contract; append pages using stable IDs and reject repeated cursors. Active step summaries update in place by step ID/revision; older immutable entries do not jump around or lose selection. All navigation/unmount paths abort/ignore stale responses. Logout/workspace change unmounts and clears all agent/approval/draft data; do not render the previous tenant's snapshot while the next loads.

## 7. Approval interaction and cancellation state machine

Local state is separate from server status: loading → ready → submitting → recorded → applied, with stale/conflict/error/unknown-submission branches. No generic “optimistic approve” updates.

| Trigger | Required client behavior |
| --- | --- |
| Authorized pending GET | Show effect/destination/exact version/sanitized arguments/expiry and Approve/Reject from capabilities. Indicate redacted fields; if preview is insufficient, show backend's unavailable reason rather than imply complete visibility. |
| User decides | Freeze immutable request body and UUID; disable both controls; keep detail visible with Submitting decision. Approval is always an explicit action, not a consequence of opening the page. |
| HTTP 200, not applied | Render returned actor/decision and Approval recorded; waiting for execution. Continue read polling; never send generic Resume. |
| Network/5xx after submission | Mark outcome unknown, GET the request. If it records this requestId show it; if still pending retry only the original body/id upon deliberate retry. If another decision won, show conflict. Never generate a fresh ID automatically. |
| HTTP 409 | GET current request, preserve displayed prior intent, show decision/expiry/cancellation and remove ineligible actions. Do not silently retry against the new version. |
| UI countdown reaches zero | Disable decisions pending server refresh; browser time is only presentation. Database decision/expiry order is authoritative. A decision committed before expiry remains valid when delivery is delayed. |
| Permission/session loss | Clear protected previews; show sign-in or access-denied message. Stop automatic mutation retry. Backend enforces human owner/pipeline-admin authority regardless of UI. |
| Worker applies decision | Show application acknowledgement; actual tool-step outcome is separate and may be running, succeeded, failed or unknown. |
| Parent pauses while approval pending | Show paused controlState separately from awaiting_approval phase. A permitted human can record a decision, but dispatch waits for parent Resume. Resume never creates approval. |
| Cancel parent while approval pending | Submit parent cancellation once; show Cancellation requested and poll. Server cancels pending approval and stops new dispatch. Do not mark external actions rolled back. |

Use a page for approval detail, not a disappearing confirmation toast. Put the tool action details before the decision controls, keep the submit status announced with aria-live=polite, and never move focus away while the user reads. Approve and Reject are never autofocused or the default action; a decision needs an explicit second confirmation step that repeats the tool, destination, argument preview and hash. After an action makes buttons unavailable, focus the durable result heading. A confirmation dialog, if used for parent cancellation, traps/restores focus and describes already dispatched actions accurately.

## 8. Run inspection, errors and OSS essentials

AgentRunInspector renders Summary, Steps, Usage and Activity sections within the existing run page. Step summaries are `{id, sequence, kind, status, attempt, startedAt, completedAt, usage, previewAvailable, contentStatus}`. The usage response is `{limits, consumed, reserved, usageComplete, cost: {consumedMicros, reservedMicros, currency, known}}`; unknown money remains null and incomplete usage retains visible reservations. Authorized step detail adds `preview: {input?, output?, truncated}` and `contentStatus: 'available' | 'expired' | 'restricted'`; JSON previews are bounded to 16 KiB. An authorized run-output read uses the same explicit availability distinction. Never fetch object-store keys or expose raw DataRef bodies to the browser. Step rows show kind, ordered sequence, observable action/result summary, attempt, duration and status text. Selecting a row requests only authorized bounded detail; raw private reasoning is excluded. Response content is plain text/escaped JSON, previews carry truncation/redaction/expiry indicators, and unavailable bodies do not appear as an empty successful output.

Parent execution phase remains running while a child awaits approval. Show both states. controlState active/paused/cancel_requested is orthogonal to phase. Existing parent signal responses retain ok:true with additive controlState/controlRevision; cancellation acceptance is not terminal cancellation. Completion requires the latest parent/child snapshots; show unknown_outcome when the remote result cannot be established. Retry run starts a new execution in the existing API, so successful/ambiguous mutations must remove that shortcut until a reviewed rerun flow obtains new approvals. A Refresh button never repeats a tool call. No Rollback action is offered for agents without a separately implemented compensation contract.

| Condition / proposed code family | User-facing outcome and available action |
| --- | --- |
| invalid_definition / invalid_argument | Highlight the field/node, preserve draft, offer editing. |
| model_unavailable | “This model is unavailable. Refresh its status or choose another approved profile.” No automatic model replacement. |
| forbidden_tool / permission denied | “You no longer have access to this action.” Clear disallowed details and show permitted navigation. |
| version_conflict / approval_expired | Show current version/decision/expiry from GET; require review rather than replay. |
| budget_exhausted / max_steps / deadline | Explain the specific limit and recorded/unknown usage; a larger budget is a new deliberate run, not an automatic continuation. |
| unknown_outcome | “The tool may have completed. Review its recorded request before starting another run.” Link to authorized evidence; no blind Retry. |
| transient read/network failure | Stale snapshot/last update and Refresh; do not display success or empty queue. |
| expired/redacted preview | Explicit “Content expired” / “Content hidden by policy”; keep allowed metadata without fetching raw storage keys. |
| unsupported state / malformed response | Safe load error and Refresh, actions disabled; retain correlation ID if supplied. |

Support human-readable error codes/messages without dumping raw HTTP response bodies, secret URLs or stack traces. Existing ApiError styling can remain while its caller supplies a safe message and appropriate action; generic Retry is only wired to a read or a specified idempotent mutation.

Published final pipeline artifacts keep the existing pipeline retention policy; seven-day run-memory expiry is not a promise to delete the sink's published output. Show these lifetimes distinctly and never erase the pipeline artifact from UI solely because a step preview expired.

OSS essentials are versioned agent authoring/use, permitted HTTP/MCP tools, approvals, bounded execution, run steps/usage, cancellation state, basic Activity and lineage evidence for the native example. Reuse FeatureContext for actual shipped capability availability; do not gate essential evidence behind enterprise deepObservability. Distinguish absent capability, unavailable model and permission denial. LangGraph integration and UI support are [deferred options](README.md#deferred-options), pending a concrete reuse case. No adapter screen, implementation milestone or delivery date is committed. Cross-run memory remains outside core scope; core memory is limited to the run-scoped policy above.

## 9. Implementation and verification gates

F1 is GAP-FE-01; B1 includes GAP-BE-01/02. GAP-CI-01 provides the integration foundation. GAP-DOC-01 owns licensing copy separately. These are aligned work packages, not duplicate implementation stacks.

| PR / dependency | Exact frontend deliverable | Smallest meaningful checks and sandbox gate |
| --- | --- | --- |
| F1 / B1 contract + GAP-CI-01 | Lossless converter, canonical snapshots/fingerprint, AI change review, relevant catalog labels and typed error/abort support needed by new flow. | Extend existing pipelineConvert.test.ts with full policies/assets/concurrency and AI/Mermaid same-ID preservation; one stale-proposal/Undo browser journey. Data-only pipeline output unchanged (S01/S03/S06). |
| F2 / B2 | Agents list/editor/version publication, configured profiles, tool metadata/credential references, limits/run-memory form and revision-based owner disable controls. | Exact-version and publication-idempotency API fixtures, invalid field/409/403 handling, owner/member author/use journey; no raw secrets in captured bodies or storage (S14/S18). |
| F3 / B3 | Agent catalog/config/input mapping, workflow compatibility, parent/child run summary/steps/usage, accessible node selection. | Synthetic model-only source → agent → sink; reload preserves same run/version; invalid/oversized input/output, max steps/budget/deadline and cancellation show exact state (S07/S10/S13/S15, tools disabled). |
| F4 / B4 | HTTP/MCP Tools UI, queue/detail, exact-call decisions, delivery/application states, conflict/expiry/lost-response handling. | Real local HTTP and MCP fixtures; no side effect before approval; two actors/repeated submission/reload/expiry/rejection/restart and unknown remote outcome; no blind rerun (S08/S09/S11/S12/S14/S18). |
| F5 / B5 | Stale/reconnect handling, retention views, Monitoring/Activity/lineage links, revocation propagation/feedback for B2 policy controls. | Kill worker during call/approval; cancellation across boundaries; permission loss clears previews; all projections agree; documented native OSS example end to end; keyboard/390px/200%-zoom/light/dark journey (S11–S16/S18). |

Core implementation is B1–B5/F1–F5, with acceptance scenarios S01–S16 and S18. S17 is reserved for the deferred LangGraph option and is not a core release requirement. S07's read-tool portion is completed with F4/B4; F3 proves only the model-only portion. B4/F4 is the first complete functional path; release completion requires B5/F5 recovery/operations evidence. Mandatory B3/B4 safety checks stay in those increments. Known baseline CI findings remain tracked release gates, not permission to skip security checks.

Use existing assert tests and installed Playwright; add cases only where the behavior lands. PR commands include `npm -w @dataflow/shared test`, `npm -w @dataflow/web test`, `npm -w @dataflow/web run typecheck`, `npm run build`, and the relevant existing Playwright command/config. Synthetic browser fixtures verify control behavior; the real disposable API/Postgres/Temporal sandbox verifies integration. Explicitly provide sandbox base URL and test tenants; never use deployed-test defaults or personal credentials. No live model downloads/paid keys on ordinary PR CI.

Shared JSON fixtures cover node/version/run/step/approval success, malformed/unknown status, field errors, stale version, recorded-not-applied, expired/redacted detail and usage with missing cost. Backend emits and frontend consumes the same examples. Assertions verify persisted state/outcomes, not incidental CSS or implementation structure. Browser evidence includes keyboard labels/focus, visible status beyond color, nonoverlapping mobile controls and dirty-draft preservation. Test logs/traces/screenshots use synthetic content. Include a caller with use_agent/read_preview but without read_instructions: model-step preview, history and export responses must omit instruction segments at the server boundary, while permitted data/output remains readable.

This LLD is documentation only. Validation of this document checks referenced existing paths, link targets, JSON examples and contract consistency; it does not report unimplemented application scenarios as passed.
