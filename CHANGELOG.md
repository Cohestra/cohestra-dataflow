# Changelog

All notable changes to DataFlow are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- AI pipeline proposals now show a keyboard-accessible before/after change review from the same merged definition used by Apply, with sensitive configuration values hidden.
- Preserve pipeline concurrency, node policies and asset bindings across canvas load/save, AI Apply/Undo and Mermaid structural edits; block AI proposals based on an older draft.
- CI secret scanning exempts one exact historical documentation false positive; the external-service smoke workflow is manual-only.
- Upgrade Go to 1.26.8 and the vendored SSH dependency to x/crypto 0.56.0 to clear reachable vulnerability findings.

### Changed
- Per-node `timeoutSec` and `retry.maximumAttempts` are now validated: `0`, negative and overflowing values are rejected on save, manual/webhook/backfill runs, activation and promotion. Previously `0` was silently ignored, so an existing pipeline saved with `0` must be re-saved without that field. Temporal schedules created before this release embed the old definition; their next run fails with `InvalidNodePolicy` if it carries such a value. Re-activate the pipeline to refresh its schedule.
- Connector manifests with a kind other than `source` are skipped at startup (logged as `skipping unsupported connector manifest`). Saved pipelines that reference such a manifest sink fail at dispatch until a coded handler exists.

### Added
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
