## Impact

**P1 — silent loss of user work.**

Changing a pipeline and using ordinary in-app navigation discards the draft without warning or recovery.

## Reproduce

1. Open a saved pipeline from Pipelines → Edit.
2. Change its name (observed: `Browser cancel acb254` → `UX unsaved navigation check`) without saving.
3. Click **All pipelines** in the canvas rail.
4. Open the same pipeline and click Edit again.

**Actual:** Navigation proceeds immediately; no confirmation is shown. Reopening restores the old name and loses the edit. The browser reported no JavaScript dialog. The test did not overwrite the saved pipeline.

**Expected:** Offer Save / Discard / Stay when leaving a dirty draft, or restore an explicitly identified recoverable draft. Do not imply that an unsaved change is persisted.

## Code evidence / suggested direction

- `apps/web/src/pages/canvas/NodePalette.tsx:87` navigates directly to `/pipelines`.
- `apps/web/src/pages/PipelineCanvasPage.tsx:294` detects dirty state, but it only gates activation/running; draft values live in component state.
- No navigation/unload guard was found in web source.

Reuse the existing dirty-state calculation. Cover in-app navigation, browser back, refresh and tab close appropriately.

## Acceptance checks

- Rename or change a node field, then leave: the user can stay with the draft intact, save successfully, or explicitly discard.
- Clean drafts navigate without a prompt.
- A failed save preserves the draft and prevents unintended navigation.

## Environment

**Environment:** Local combined PR #42–49 acceptance checkout, `codex/local-acceptance-20260922`, commit `10a82e337a6c392a32ecc7f73d3b9b13ef9701b6`; built web app + real enterprise API/workers. Reviewed 2026-09-25 in the Chromium-based Codex browser using synthetic test data. This reports the local integration, not a claim about the deployed release.

## Screenshot evidence

![Unsaved replacement name before leaving](https://github.com/user-attachments/assets/0c51874d-cb05-489d-9626-a264c6658553)

![Reopened canvas has the old name](https://github.com/user-attachments/assets/19f78dce-affe-4bb6-af37-ca79ce164b87)
