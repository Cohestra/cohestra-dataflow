# Changelog

All notable changes to DataFlow are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Security
- Monitoring: the overview (`GET /api/executions/monitoring/overview`) showed every member every pipeline in the workspace, including definitions, failure errors and data-quality samples of pipelines they had no access to. It now covers only pipelines the caller can view, like the run list.
- Analytics: ClickHouse now enforces tenant isolation itself. The API queries as a read-only `dataflow_analytics` user (`db/clickhouse/users-analytics.xml`) whose row filters return only the tenant named in each request; a query that lost its `tenant_id` clause used to read every workspace's records. That user can only `SELECT` the two analytics tables (no `url()`/`file()` table functions, no system tables, no writes). Compose and Helm enable it by default; other installs opt in with `CLICKHOUSE_ANALYTICS_USER`/`CLICKHOUSE_ANALYTICS_PASSWORD`, and the ClickHouse container needs `CLICKHOUSE_ANALYTICS_PASSWORD` whenever the file is mounted.
- Rate limits: when Redis is unreachable each API replica now enforces the limits itself; they used to switch off, allowing unlimited login attempts. The Helm web Service uses `externalTrafficPolicy: Local`, because with `Cluster` the node address made nginx trust a client-supplied `X-Forwarded-For`, so clients could choose the address limits are keyed on. `TRUSTED_PROXY_CIDRS` (Helm `api.trustedProxyCIDRs`) narrows which peers may set `X-Real-IP`.
- Auth: refresh-token rotation is atomic. Two requests redeeming the same token could both get new sessions, and two tabs refreshing at once were treated as token theft and signed the user out everywhere.
- Analytics: ClickHouse string literals now escape backslashes; a crafted dataset name could previously drop the tenant filter and read other workspaces' records.
- Webhook triggers, the Razorpay webhook and OAuth sign-up with an invitation resolve their tenant through `SECURITY DEFINER` functions (`db/028_public_entry_resolvers.sql`); under row-level security they previously found nothing (webhooks errored, payments were never credited, invited OAuth users got a new workspace).
- Saving a new version of an existing pipeline needs editor access to it (sharing carries over to the new version); run details, retry, status and trace follow the pipeline's roles, and run/log lists only include pipelines the member can view.
- Webhook triggers verify HMAC-SHA256 over `"<X-Signature-Timestamp>.<raw body>"` with a 5-minute window and reject repeated deliveries; signing secrets are write-only in API responses, must be 16+ characters (no `change-me`), and a path active in another workspace cannot be activated. DataFlow's webhook sink signs the same way.
- Password sign-ups must confirm their email (24-hour link; logged when SMTP is not configured) unless they come from an invitation or are the first account; Google/OIDC logins are never linked to an unverified account and need an explicit `email_verified` claim. Login timing no longer reveals which emails exist.
- In production (`NODE_ENV=production`) the API refuses to start with the default or a short `JWT_ACCESS_SECRET`, or when its database role bypasses row-level security; `bootstrap.sh` no longer installs without production mode on a public IP when secrets are missing (`ALLOW_INSECURE_PUBLIC_INSTALL=true` overrides).
- Service-account API tokens act as the user who created them, so their writes and audit rows no longer fail on `users(id)` foreign keys; audit write failures are logged. Run errors shown to users are redacted (DSN credentials, key=value secrets, bearer tokens).
- Compose Grafana gives anonymous users Viewer instead of Admin and listens on localhost only.
- Review follow-ups: webhook replays are rejected even with a changed `Idempotency-Key`, and a retry of an accepted delivery returns its run; pipeline lists and runtime lineage follow pipeline roles; promoted prod versions keep their author and sharing and check webhook paths; a Google/OIDC login with a verified email claims an unconfirmed password account of the same address, and a new password sign-up replaces one whose link expired; run errors in Temporal history are redacted.

### Fixed
- AI builder no longer returns "ready" for graphs the canvas cannot save (dead ends, orphans, under-connected fork/merge): it repairs, splices an unambiguously dangling step (noted as an assumption), or returns a warned draft.
- AI grounding accepts comma-list fields sent as JSON arrays and the `old:new` rename shorthand; the planner prompt lists each select field's allowed values.
- Plain `docker compose up` no longer silently switches the AI builder to `qwen2.5vl:7b` (the tracked override is now an opt-in example).
- Restore granted editor access under tenant row-level security; use canonical ingestion modes in AI schemas.
- Count source pages once and show consistent run controls, completion times and monitoring totals.
- Keep Save reachable on narrow screens while the AI panel is open.
- AI pipeline proposals now show a keyboard-accessible before/after change review from the same merged definition used by Apply, with sensitive configuration values hidden.
- Preserve pipeline concurrency, node policies and asset bindings across canvas load/save, AI Apply/Undo and Mermaid structural edits; block AI proposals based on an older draft.
- CI secret scanning exempts one exact historical documentation false positive; the external-service smoke workflow is manual-only.
- Upgrade Go to 1.26.8 and the vendored SSH dependency to x/crypto 0.56.0 to clear reachable vulnerability findings.
- Upgrade Go to 1.26.10 for GO-2026-6809 (crypto/x509 distrusts compromised intermediate certificates) and klauspost/compress to 1.18.7 for CVE-2026-63209.
- Upgrade Go to 1.26.9 and golang.org/x/net to 0.60.0 (with x/crypto 0.57.0, x/sys, x/text, x/term, x/sync) for the October 2026 net/http, crypto/tls, html/template, mime/multipart and x/net/http2 advisories.
- Migrate the Snowflake driver to `github.com/snowflakedb/gosnowflake/v2` v2.3.0 (log secret masking, slog instead of logrus) and drop the GO-2026-6665 govulncheck exemption. AWS SDK modules move up with it.

- Self-hosted installs are no longer limited to 5 runs a month: the execution quota applies only with `BILLING_ENABLED=true` (Helm `billingEnabled`); usage is still counted.
- Resumable sources that reach the 50-page per-run cap now finish the run, commit their cursor and resume next run (node meta `truncated`); before, large tables failed on every run.
- Incremental Postgres/MySQL paging no longer drops rows that share the cursor value at a page boundary (optional `keyColumn` for keyset paging); Mongo pages on cursor plus `_id`; Snowflake without `cursorColumn` fails clearly instead of re-reading the same page.
- Postgres, MySQL and Snowflake sinks write each attempt in one transaction, so a retried step no longer duplicates rows; the webhook sink sends a stable `Idempotency-Key`, Kafka records carry an idempotency header, and Spark retries attach to the job they already submitted.
- Bad records, bad configuration and unknown steps fail once (`SchemaValidationError`, `InvalidConfigError`, `UnknownActivityError`) instead of being retried 5 times; non-object records no longer panic transform steps, and expressions compile once per step.
- Cross-run dedupe keys travel by payload reference, not through Temporal history (which overflowed near 30k rows); over-size payload reads name `MAX_PAYLOAD_BYTES`.
- nginx `proxy_read_timeout` is 540s, above the 480s AI model timeout.
- Snowflake incremental paging matches `cursorColumn` against upper-case result columns, and a cursor column missing from the result fails clearly; truncated run errors stay valid UTF-8.

### Changed
- **Upgrade notes:** apply `db/028_public_entry_resolvers.sql`. Webhook senders must add `X-Signature-Timestamp` and sign `"<timestamp>.<body>"` (or set `WEBHOOK_LEGACY_SIGNATURES=true` temporarily); webhook pipelines with a short or placeholder secret must be given a new one before they can be saved or activated. HTTP sources without a saved watermark now read full history on their first run unless `incremental.initialWatermark` is set (it used to start silently at now-1h). Hosted deployments that sell top-ups need `BILLING_ENABLED=true`.
- CI govulncheck now runs through `scripts/govulncheck-gate.py`: reachable vulnerabilities fail unless listed in `.github/govulncheck-exemptions.txt` with a review-by date (expired exemptions fail again).
- The AI builder default model is `qwen3:8b` with few-shot planner examples (`AI_FEW_SHOT`, `OLLAMA_NUM_CTX=8192`), chosen by the October 2026 bake-off (`docs/evals/cpu-bakeoff-20261016`). Helm's Ollama pod limit is now 12 GiB (was 4 GiB) on a 20 GiB volume; the model-call timeout is configurable (`OLLAMA_TIMEOUT_SECONDS`, 480 in compose/Helm).
- Per-node `timeoutSec` and `retry.maximumAttempts` are now validated: `0`, negative and overflowing values are rejected on save, manual/webhook/backfill runs, activation and promotion. Previously `0` was silently ignored, so an existing pipeline saved with `0` must be re-saved without that field. Temporal schedules created before this release embed the old definition; their next run fails with `InvalidNodePolicy` if it carries such a value. Re-activate the pipeline to refresh its schedule.
- Connector manifests with a kind other than `source` are skipped at startup (logged as `skipping unsupported connector manifest`). Saved pipelines that reference such a manifest sink fail at dispatch until a coded handler exists.

### Added
- Password reset: "Forgot password?" on the login page emails a single-use, one-hour link (`POST /api/auth/forgot-password`, `POST /api/auth/reset-password`); resetting signs out every session. Apply `db/029_password_resets.sql`.
- AI builder model picker (`qwen3:8b` best quality, `qwen3:4b-instruct-2507` faster) backed by `GET /api/ai/models` and an operator allow-list (`OLLAMA_MODELS` / `ollamaModels`).
- AI builder "Redo last apply".
- `tests/ai-evals/cases/v2-datalake.json` lakehouse/chat corpus with a golden-output preflight, and mocked plus live browser tests for AI generate/refine/Apply/Undo/Redo.
- Isolated real Temporal/PostgreSQL sandbox with golden data output, retry/timeout, control delivery, cancellation, and workflow-worker restart acceptance in both editions
- Durable execution pause/resume/cancel intent with atomic audit, retryable worker delivery, responsive workflow controls, and tenant-scoped activity admission
- Execution pause/resume/cancel now require owner, pipeline creator, or pipeline editor/admin access (viewers get 403, others 404); see `docs/EXECUTION_CONTROLS.md` for rollout of migration 027
- Reserved agent node wire contract with immutable version binding, bounded batch input validation, and fail-closed admission until the agent runtime is implemented
- AI pipeline builder — natural-language-to-Mermaid via local Ollama or cloud
- React Flow canvas with live Mermaid sync
- Go backend: one module builds separate API, Temporal workflow-worker, and activity-worker binaries
- Pipeline lifecycle management (draft → integration → production) with stage gates
- Durable execution via Temporal: validated per-node activity timeouts/retries (including source pages), pause/resume/cancel, crash-safe backfills
- Pluggable connector system with manifest-driven HTTP sources and coded sinks; unsupported sink manifests are excluded from registration/catalog
- Medallion architecture lineage graph (external → bronze → silver → gold)
- Monitoring dashboard: execution logs, quality checks, pipeline health
- Run history with PipelinesPage-style filter pills and slide-in detail drawer
- `datetime-local` pickers (with seconds) on backfill form and run filters
- Graceful API error display via `ApiError` component across all pages
- AES-256-GCM encrypted payload storage (`DataRef`: inline, PostgreSQL, S3)
- OpenLineage event emission for external lineage consumers
- Temporal schedule triggers (cron + ad-hoc) with a UI management screen
- Data contract publishing and breaking-change gate on production promotion
- ClickHouse hot-tier analytics sidecar
- Multi-tenant PostgreSQL with row-level tenant isolation
- Docker Compose stack: one command brings up the full local environment
- Public project policies for security reporting, governance, conduct, and contributions

[Unreleased]: https://github.com/Cohestra/cohestra-dataflow/commits/main
