# Local acceptance — 2026-09-22

See [the test plan and results](TEST_PLAN.md). This evidence combines offline,
real database/Temporal, real application API and real browser checks. It does
not establish production readiness or complete manual UI coverage.

The local Qwen3:8b generation request returned successfully but its proposal
failed semantic review; see ai-smoke.json. No model was promoted.

Credentials, worker keys and session traces are intentionally omitted.
The complete runtime sandbox histories remain in the ignored checkout at
`.artifacts/local-acceptance/temporal-verified/`.
