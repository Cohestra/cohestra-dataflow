# Sandbox evidence — 2026-09-14

Current PR48 head: `21b137aa3db30ec84eb0a555ddb8b2a275d040ed`.

- `linux-verified/`: successful final Linux / PostgreSQL16.15 CI run34817419742; six leaf scenarios pass in each edition.
- `recovery-verified/`: successful final local macOS / PostgreSQL17.10 run, including default pinned binary download.
- `linux-initial-failure/`: initial restart-query timeout, history evidence and diagnosis; fixed by bounded read polling through the old worker-task lease expiry.
- `failure-cleanup-check/`: intentionally failing test subprocesses verify nonzero result, retained artifacts and service/runtime cleanup.
- Root-level runtime/test logs: earlier successful local run before the polling correction. Its source revision is recorded separately in `summary.json`.
- `github-checks.json`: final PR check snapshot. All14 ordinary checks passed; image signing was skipped and the existing repository security job still failed.

Test boundaries: injected synthetic tenant authentication and source/sink handlers; real API routing, RLS, encrypted payload storage, Temporal workers/history and coded transform. Worker restart is graceful within the same test process with sticky cache disabled. No production model/tool execution or external provider access.
