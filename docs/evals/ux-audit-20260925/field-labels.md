## Impact

**P2 — configuration fields are ambiguous to assistive technology.**

Visible field titles are not associated with their input/select controls. The node Label field is labelled correctly, but most catalog and connection controls are not.

## Reproduce

1. Open a PostgreSQL source node's settings.
2. Inspect Database connection, Source table, Sync mode, Columns, Incremental cursor column, Cursor type, Data layer and Ingestion mode.
3. Inspect their accessible names or DOM label associations.

**Actual:** These controls have no associated HTML label, `aria-label`, or `aria-labelledby`; several have only an example placeholder or selected value. The connection and enum selectors have no placeholder either. A selected value such as `number` does not communicate that this is the cursor type.

**Expected:** Each control has a stable accessible name matching the visible field title; helper text is associated as a description. Required/error states should be exposed when applicable.

## Code evidence / suggested direction

`apps/web/src/components/canvas/ConfigPanel.tsx:73–93` renders catalog titles as sibling spans; line 36 renders the connection selector without a label. Fix the shared renderer with native labels/stable IDs so all connector fields benefit. Preserve the correctly labelled node Label input.

Relevant criterion: [WCAG 1.3.1 Info and Relationships](https://www.w3.org/WAI/WCAG21/Understanding/info-and-relationships.html). This finding is from DOM/AX inspection, not a claimed VoiceOver/NVDA speech test.

## Acceptance checks

- Each input, select, checkbox and textarea in representative source/transform/sink inspectors has the expected accessible name.
- Clicking a visible label focuses/toggles its control where appropriate.
- Helper text remains distinguishable from the field name.

## Environment

**Environment:** Local combined PR #42–49 acceptance checkout, `codex/local-acceptance-20260922`, commit `10a82e337a6c392a32ecc7f73d3b9b13ef9701b6`; built web app + real enterprise API/workers. Reviewed 2026-09-25 in the Chromium-based Codex browser using synthetic test data. This reports the local integration, not a claim about the deployed release.

## Screenshot evidence

![PostgreSQL inspector with visually named but unassociated controls](https://github.com/user-attachments/assets/22cd3e62-be4a-46a5-b985-f4eda4d61705)
