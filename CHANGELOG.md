# Changelog

All notable changes to DataFlow are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- Honor per-node workflow activity timeouts and retry attempts, including source pages; reject invalid settings before execution admission and omit unsupported manifest-only sinks from registration/catalog.

### Added
- AI pipeline builder — natural-language-to-Mermaid via local Ollama or cloud
- React Flow canvas with live Mermaid sync
- Go backend: one module builds separate API, Temporal workflow-worker, and activity-worker binaries
- Pipeline lifecycle management (draft → integration → production) with stage gates
- Durable execution via Temporal: retries, pause/resume/cancel, crash-safe backfills
- Pluggable connector system with manifest-driven HTTP connectors
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
