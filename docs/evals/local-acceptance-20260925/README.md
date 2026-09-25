# Local acceptance — September 25, 2026

[Results and remaining failures](LOCAL_ACCEPTANCE_RESULTS_20260925.md) · [Repeatable test plan](LOCAL_ACCEPTANCE.md) · [Source and evidence manifest](manifest.json)

Source: `10a82e337a6c392a32ecc7f73d3b9b13ef9701b6` on `codex/local-acceptance-20260922` in `.local-acceptance/checkout`. The source checkout is clean; no remote push, merge or deployment occurred.

Core local execution and UI tests passed after fixes. Two real Ollama refinement cases remain failed (HTTP 422 after repair). Mocked UI checks are identified separately. The initial loaded controls failure and the secret-scanner false positive remain in the evidence. The history scan covered the pre-commit 177-commit history; the final working-files scan covered all 21 changed files.

Private credentials and browser trace archives are excluded. Raw local artifacts remain in the isolated checkout.
