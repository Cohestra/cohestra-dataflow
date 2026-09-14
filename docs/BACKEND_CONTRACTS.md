# Backend Compatibility Contracts

This inventory is the frozen frontend/POC contract for the Go cutover. Unless a
route explicitly returns a redirect, successful responses are JSON with
`Content-Type: application/json; charset=utf-8`. Errors are
`{"error":"message"}`. JSON request bodies are limited to 5 MiB. Authenticated
Errors use `{"error":"message","code":"ERR_*","details":{...}}`; `code` and
`details` are omitted when not applicable, and unexpected server details are
never returned. Routes accept `Authorization: Bearer <JWT-or-API-token>`; browser refresh auth
uses the existing HttpOnly refresh cookie. CORS permits the configured
`APP_URL`, credentials, `Authorization`, and `Content-Type`. OAuth callbacks
validate signed state and redirect to the existing frontend connector routes.
The Razorpay webhook consumes the unmodified request bytes and validates
`X-Razorpay-Signature` before decoding JSON.

Public/operational routes:

- `GET /health`, `GET /metrics`
- `POST /api/auth/register`, `POST /api/auth/login`, `POST /api/auth/refresh`, `POST /api/auth/logout`
- `GET /api/auth/google`, `GET /api/auth/google/callback`, `GET /api/auth/oidc`, `GET /api/auth/oidc/callback`, `GET /api/auth/accept-invite`
- `POST /api/billing/webhook`, `POST /api/hooks/{path}`, `POST /api/openlineage`
- `GET /api/analytics/shared/{token}`

Authenticated control-plane routes:

- `GET /api/auth/me`
- `GET /api/edition`, `PUT /api/edition/features/{feature}`, `GET /api/edition/audit-export`
- `GET /api/alerts`, `POST /api/alerts/{id}/acknowledge`, `POST /api/alerts/{id}/resolve`, `POST /api/alerts/{id}/retry-notification`
- `GET|POST /api/team/invitations`, `DELETE /api/team/invitations/{email}`, `GET /api/team/members`, `GET|POST /api/team/tokens`, `DELETE /api/team/tokens/{id}`
- `GET|POST /api/pipelines`, `GET /api/pipelines/{rowId}`, `POST /api/pipelines/{rowId}/activate`, `POST /api/pipelines/{rowId}/run`, `POST /api/pipelines/{rowId}/promote`, `POST /api/pipelines/{rowId}/stage`
- `POST /api/pipelines/{rowId}/backfills/plan`, `GET|POST /api/pipelines/{rowId}/backfills`, `DELETE /api/pipelines/{rowId}/backfills/{jobId}`, `POST /api/pipelines/{rowId}/backfills/{jobId}/retry`
- `GET|POST /api/pipelines/{rowId}/access`, `DELETE /api/pipelines/{rowId}/access/{userId}`
- `POST /api/pipelines/lineage/openlineage`, `POST|DELETE /api/pipelines/lineage/openlineage-key`, `GET /api/pipelines/lineage/changes`, `GET /api/pipelines/lineage/workspace`
- `GET /api/executions`, `GET /api/executions/logs`, `GET /api/executions/monitoring/overview`, `GET /api/executions/{id}`, `GET /api/executions/{id}/status`, `GET /api/executions/{id}/trace`, `POST /api/executions/{id}/retry`, `POST /api/executions/{id}/{pause|resume|cancel}`
- `GET /api/connectors/catalog`, `GET|POST /api/connectors`, `DELETE /api/connectors/{connectionId}`, `POST /api/connectors/{connectionId}/refresh`, `POST /api/connectors/{connectionId}/test`, `GET|PUT|DELETE /api/connectors/{connectionId}/cdc`
- `GET /api/connectors/google/auth`, `GET /api/connectors/google/callback`, `GET /api/connectors/google/spreadsheets`, `GET /api/connectors/google/spreadsheets/{id}/sheets`, `GET /api/connectors/google/spreadsheets/{id}/sheets/{name}/preview`, `GET /api/connectors/google/drive/folders`, `GET /api/connectors/google/drive/files/{id}/preview`
- `GET /api/connectors/microsoft/auth`, `GET /api/connectors/microsoft/callback`, `GET /api/connectors/microsoft/drives`, `GET /api/connectors/microsoft/drives/{driveId}/items`, `GET /api/connectors/microsoft/workbooks/{itemId}/sheets`, `GET /api/connectors/microsoft/workbooks/{itemId}/sheets/{name}/preview`
- `POST /api/connectors/zendesk/auth`, `GET /api/connectors/zendesk/callback`, `GET /api/connectors/zendesk/resources`
- `GET /api/analytics/datasets`, `GET /api/analytics/datasets/{name}/schema`, `GET /api/analytics/datasets/{name}/rows`, `POST /api/analytics/query`, `GET|POST /api/analytics/dashboards`, `GET|PUT|DELETE /api/analytics/dashboards/{id}`, `POST /api/analytics/dashboards/{id}/share`, `GET /api/analytics/dashboards/{id}/shares`, `DELETE /api/analytics/dashboards/{id}/shares/{hash}`
- `POST /api/ai/generate`, `POST /api/ai/refine`
- `GET /api/billing/usage`, `POST /api/billing/orders`, `GET /api/billing/history`

Temporal activity names are exactly `prepareScheduledExecution`,
`fetchSourcePage`, `commitSourceCursors`, `commitDedupeKeys`, `dispatchNode`,
`mergeRefs`, `evalEdgeCondition`, and `markExecution`. Workflow name remains
`DynamicDAGWorkflow`; workflow queues remain `dynamic-dag-{test,prod}` and
activity queues remain `dynamic-activities-{test,prod}`. Signals remain
`pause`, `resume`, and `cancel`; query name remains `status`.

Canonical JSON examples, including `PipelineDefinition`, activity inputs,
`DataRef`, `NodeResult`, connector manifest, and AES-GCM bytes, live in
`tests/contracts/backend-wire.json`.

## Node activity policies

New workflow executions honor node `timeoutSec` as each activity attempt's
start-to-close timeout, including every source page and source-page merge.
`retry.maximumAttempts: 1` disables automatic retries. Omitted/null optional
values, including an empty `retry` object, retain the existing ten-minute
timeout and five-attempt policy. Execution bookkeeping retains these defaults;
a node override does not change parallel siblings. The heartbeat timeout is
lowered when necessary to fit the node timeout.

Explicit values must be positive integers: `timeoutSec` cannot exceed
9,223,372,036 seconds (Go duration range), and `maximumAttempts` cannot exceed
2,147,483,647 (Temporal's signed 32-bit field). Zero does not request unlimited
retries. Save, execution admission and schedule creation reject invalid values.
Explicit node policies are supported only by the `workflow` engine; other
engines reject them instead of silently ignoring them.

The `node-activity-policy-v1` Temporal version marker preserves the old global
options for existing histories, including previously ignored node settings.
Rollout must retain this compatibility branch while old histories remain
replayable. Pause/cancel behavior is unchanged by this policy increment.


## Reserved agent node contract (B1)

`tests/contracts/agent-pipeline.json` is the shared TypeScript/Go wire fixture.
Agent nodes pin a UUID `agentId` and positive safe-integer `agentVersion`, use
`type: agent` / `activityType: agent.run`, and accept exactly one predecessor.
`inputBinding` requires `mode: batch`, unique nonempty literal top-level `fields`
(no dotted paths, bracket paths or templates), and integer `maxRecords` from 1 to 100.
Only the workflow engine supports this contract. Unknown configuration fields,
including credentials, instructions and model tags, are rejected.

B1 reserves this shape only: saves, runs, activation and promotion reject agent
pipelines until the B3 child workflow is implemented. There is no enable flag or
catalog entry. `ValidateAgentNodes` checks the reusable shape;
`ValidateAgentAdmission` enforces the temporary execution boundary. Future runtime
preparation must validate immutable version ownership, input/output schemas,
every projected field, and byte/record limits before any model or tool call.
Data-only pipeline admission remains unchanged.
