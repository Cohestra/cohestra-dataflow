## Impact

**P2 — the tested local AI refinement journeys cannot reach proposal review.**

Two real Ollama refinement requests fail with invalid pipeline output after the bounded repair. The UI shows raw HTTP/JSON error text; the screenshot also retains “Refining pipeline...” while the error is already visible.

## Reproduce

1. Run the local acceptance stack with Ollama 0.30.10 and `qwen3:8b`.
2. Open a valid saved HTTP source → filter → managed-records sink pipeline.
3. Ask Build with AI to change only the sink collection, preserving the rest of the definition; click Refine.
4. A separate tested request changing only the source URL fails similarly.

**Observed:** Both requests returned HTTP 422: `could not refine pipeline: model response invalid after one repair: pipeline name is required`. No proposal reaches Apply/Discard review. The screenshot shows raw JSON in a small error strip and stale in-progress status. This is an observed two-case failure, not a universal failure-rate estimate.

**Expected:** For supported local-model requests, return a valid reviewable proposal preserving unrelated metadata. When provider output is invalid, stop loading state, keep the existing pipeline/draft unchanged, and give a clear retry/manual-edit path without exposing raw transport formatting.

## Evidence / boundaries

- `tests/local-acceptance/review.spec.ts` retains both real-provider failing assertions.
- `.artifacts/local-acceptance/full-review/continuation-results.json` and `continuation-case-outcomes.json` record the failures on September 25.
- The application's ingestion schema mismatch was already corrected before these requests. These failures remain; mocked UI passes do not establish model accuracy.
- Relevant areas: `internal/api/routes_ai.go` and the canvas AI request/error state.

## Acceptance checks

- Rerun both real-provider cases with explicit model/version/config provenance; retain semantic scope/preservation assertions.
- On failure, loading clears, existing metadata remains untouched, the request remains recoverable, and the user sees actionable copy.
- Keep schema/security validation and bounded repair. Do not bypass validation, promote a model from mocked tests, or fabricate a valid proposal.

## Environment

**Environment:** Local combined PR #42–49 acceptance checkout, `codex/local-acceptance-20260922`, commit `10a82e337a6c392a32ecc7f73d3b9b13ef9701b6`; built web app + real enterprise API/workers. Reviewed 2026-09-25 in the Chromium-based Codex browser using synthetic test data. This reports the local integration, not a claim about the deployed release.

## Screenshot evidence

![Real provider error visible while the canvas still says Refining pipeline](https://github.com/user-attachments/assets/bdd50c24-0b1a-4e23-800d-9af642d9440d)
