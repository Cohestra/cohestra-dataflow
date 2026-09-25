# Durable execution controls

`POST /api/executions/{id}/pause`, `/resume`, and `/cancel` acknowledge a
committed control request with `ok: true`, `controlState`, and
`controlRevision`. Acknowledgement does not establish terminal completion or
remote rollback. Workspace owners, pipeline creators, and pipeline editors or
admins can control a nonterminal execution. A visible viewer receives 403;
inaccessible or cross-tenant IDs receive 404. Terminal executions return 409.
After cancellation is requested, pause/resume return 409. Repeating the current
intent returns the existing revision without duplicating its audit entry. The
first request at revision zero is always delivered, including resume, because a
legacy workflow may be paused only in Temporal while its database row is active.
Rollback retains its existing signal behavior with the same authorization and
nonterminal checks; it is not a durable pause/resume/cancel state.

This tightens the previous behavior, where any caller who could resolve the
execution could send any of these signals. Viewers now receive 403 and callers
without pipeline access 404.

The API updates execution intent and its audit entry in one tenant transaction.
The activity worker delivers existing signal names using its current namespace
client. Signals carry revision hints; new workflow histories use PostgreSQL
intent as authority, including when an old or duplicate signal arrives.
`control_delivered_revision` only means Temporal accepted that revision's wake-up.
It does not mean the workflow applied it. A concurrent newer request stays
pending. Delivery scans at most 20 due rows, uses a two-second deadline per RPC
and a 45-second batch deadline, and holds no SQL transaction over an RPC.
Failures wait 30 seconds before another attempt; newer intent resets that delay.
If Temporal reports the workflow as not found (terminated, reset or past
retention outside the app), delivery stops: the revision is marked delivered and
a warning is logged. The execution phase is left unchanged for reconciliation.
Delivery selects rows whose `environment` equals the worker's
`TEMPORAL_NAMESPACE`; a worker in any other namespace (for example `default`)
logs a warning at startup and delivers nothing.

The new `durable-execution-controls-v1` history branch reads control before
business work. This handles the existing API start order, where Temporal can
start before the execution row is inserted: reads retry at most ten times within
30 seconds. If metadata stays missing, no business activity is admitted.
Source pages and subsequent nodes wait while paused. A listener consumes controls
while activities are active and polls every 30 seconds if a wake-up is lost.
Control-read failures before business work and at each activity admission stop
new work. A failed background poll (for example during a short database outage)
keeps the last known control state and retries at the next poll instead of
failing the run; histories started before `control-watch-nonfatal-refresh-v1`
keep the earlier fail-on-read behavior. Cancel cancels the shared activity context
and prevents downstream nodes. Source/dispatch activities heartbeat once per
second, with the worker heartbeat throttle capped at one second, so running
connector code that observes its context can receive cancellation promptly.

Before each source fetch and transform/sink handler attempt, the activity reads
the tenant execution row under a short shared lock. That transaction's completion
is the admission point. A pause/cancel committed before it blocks the external
call; already-admitted work can still finish. No database lock spans connector
network I/O. A paused automatic retry returns a structured admission-blocked
result; the workflow waits for resume and preserves the remaining business
attempt budget. Waiting for pause does not exhaust that budget. Existing connector
retries still have their existing effect semantics; this slice adds no effect
ledger, automatic compensation, or exactly-once delivery guarantee.

Merge/edge evaluation and final cursor/deduplication commits are gated by the
workflow, not by the activity-side external-call admission check. They may finish
after their workflow gate if a control request wins immediately afterward. They
do not authorize a subsequent sink. Successful output from a cancelled parent
is not passed downstream. Extended pauses add polling history; a new general
run-duration/history compaction policy is outside this change.

Finalization uses a disconnected context bounded by a 30-second schedule-to-close
timeout. Terminal database phases are monotonic; a committed cancel wins over
completion. Backfill and alert updates use the actual terminal phase and explicit
tenant predicates. If finalization cannot reach the worker/database within its
bound, the workflow returns an error and the read projection may remain stale;
terminal reconciliation remains a later operations milestone.

Apply `db/027_execution_controls.sql` before deploying the new API/activity worker;
register the new activities before enabling the new workflow worker. Compose
initialization mounts apply migrations only to new volumes; apply 027 explicitly
to an existing database before rolling out those processes. The migration
preserves existing `phase=paused` rows. Its three new columns have constant
defaults (metadata-only on PostgreSQL 11+), but `CREATE INDEX executions_pending_control`
is not `CONCURRENTLY` and blocks writes to `executions` while it builds; on a
large table, create that index `CONCURRENTLY` first during a quiet period, then
apply the migration. Previously running histories keep their
legacy signal branch and its responsiveness, as do the separate enterprise engine
workflows. Do not remove the new activity registrations while new-branch histories
remain active. Existing direct signals are accepted as wake-ups by the new branch;
operators must use the API to change durable intent.

The database tests require an explicitly disposable, fully migrated PostgreSQL
instance in `CONTROL_TEST_DATABASE_URL`. They create unique tenant fixtures,
exercise API RLS through `dataflow_app`, and clean up their own records. Run:

```sh
go test -race -p 2 ./internal/api ./internal/workflows -run '^TestControl' -count=1
go test -race -tags ee -p 2 ./internal/api ./internal/workflows -run '^TestControl' -count=1
```

Tests cover intent/audit atomicity, role and tenant boundaries, outage recovery,
namespace isolation, revision delivery races, effect admission, terminal
monotonicity, page pause, stale wake-ups, active cancellation, delayed metadata,
retry accounting through pause, bounded cleanup, non-fatal watcher read
failures, and stopping delivery to a missing workflow. The existing history replay
and node-policy tests retain their explicit legacy-control branch coverage.
