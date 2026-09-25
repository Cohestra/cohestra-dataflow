## Impact

**P2 — pipeline overview communicates an incorrect execution history.**

A cancelled run is displayed as **Never run** in the pipeline list, alongside its real last-run timestamp. The details drawer simultaneously reports one recent run.

## Reproduce

1. Run a saved pipeline and cancel it through the execution controls.
2. Return to Pipelines.
3. Inspect the row and open its details.

**Actual:** The cancelled fixture `Browser cancel acb254` shows Never run and a recent timestamp. Its drawer reports 1 recent run. The confirmed execution phase is `cancelled` (execution `exec-ecb81787-9046-405f-8f90-50f23e144d8c`).

**Expected:** Show Cancelled consistently in list/details/history. Reserve Never run for genuinely absent execution history.

## Code evidence / suggested direction

- `apps/web/src/pages/PipelinesPage.tsx:110–114`: RunLabel only handles completed, failed and running; all other values fall through to Never run. RunDot has the same limited mapping at lines 103–107.
- Row rendering at lines 467/469 shows label and timestamp independently.
- `apps/workflow-go/internal/api/routes_pipelines.go:235` correctly returns the cancelled phase; the execution is not missing.

Also cover effective paused state when fixing the status contract: the list currently projects stored phase without control_state. That paused discrepancy is source-supported, not newly exercised in this UX pass.

## Acceptance checks

- Cancelled, paused, running, completed and failed executions have explicit consistent labels.
- Never run appears only when no run exists.
- Timestamp, status and details agree after refresh.

## Environment

**Environment:** Local combined PR #42–49 acceptance checkout, `codex/local-acceptance-20260922`, commit `10a82e337a6c392a32ecc7f73d3b9b13ef9701b6`; built web app + real enterprise API/workers. Reviewed 2026-09-25 in the Chromium-based Codex browser using synthetic test data. This reports the local integration, not a claim about the deployed release.

## Screenshot evidence

![Pipeline row says Never run alongside its recent run time](https://github.com/user-attachments/assets/2bf4b32f-dbc8-4c79-8cb3-90b938041b69)

![Same pipeline details report one recent run](https://github.com/user-attachments/assets/e4b41599-886a-43ef-b62f-e7f918cdfe98)
