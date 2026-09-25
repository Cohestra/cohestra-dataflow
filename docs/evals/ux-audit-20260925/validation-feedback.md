## Impact

**P2 — users cannot understand or recover from an invalid draft.**

The canvas keeps an old Loaded/Saved message after edits. This masks current dirty/validation feedback; narrower layouts hide the feedback entirely.

## Reproduce

1. Open a valid saved source → filter → sink pipeline.
2. Open the only sink and click Delete node; do not save this test change.
3. Inspect the action bar at 1280px and 800px viewport widths.

**Actual:** Save, Activate and Run become disabled. At 1280px, the status still says **Loaded v1**, with no missing-sink explanation. At 800px there is no visible explanation. Renaming a loaded pipeline also leaves the old Loaded message while Run disables.

**Expected:** Show the current unsaved/invalid state and an actionable reason, such as “Add at least one sink before saving.” A prior success message must not conceal a current error. Keep validation available at every supported width.

## Code evidence / suggested direction

`apps/web/src/pages/canvas/PipelineActionBar.tsx:24,33` prioritizes persistent `msg` over dirty-state and validation messages. Line 32 hides the status below `xl`. Editing/deleting nodes does not clear the old message.

Do not solve this by enabling execution of invalid drafts. Give current errors/dirty state priority and expose a persistent recovery cue near the blocked actions.

## Acceptance checks

- Remove the only sink or create another invalid graph: the current reason is visible at desktop and narrow breakpoints.
- Correct the graph: the reason clears and action availability updates.
- Edit after Save: the status indicates unsaved changes rather than continuing to claim Saved.

## Environment

**Environment:** Local combined PR #42–49 acceptance checkout, `codex/local-acceptance-20260922`, commit `10a82e337a6c392a32ecc7f73d3b9b13ef9701b6`; built web app + real enterprise API/workers. Reviewed 2026-09-25 in the Chromium-based Codex browser using synthetic test data. This reports the local integration, not a claim about the deployed release.

## Screenshot evidence

![No sink remains, all actions disabled, but status still reads Loaded v1](https://github.com/user-attachments/assets/82cc77d9-215d-40b2-a1d3-adc30b5bd762)
