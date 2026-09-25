## Impact

**P1 — keyboard-only users cannot configure existing graph nodes.**

The graph exposes each node as a focusable button, but activating it from the keyboard does not open the inspector. A pointer click works.

## Reproduce

1. Open a saved pipeline with a source node; close any inspector.
2. Focus the node button (observed: **Slow local pages PostgreSQL**).
3. Press Enter, then test Space with the inspector still closed.
4. Click that same node with the pointer.

**Actual:** Enter/Space leave the inspector closed. Pointer click opens Node settings. Focused node and closed/open inspector were verified using the live accessibility tree.

**Expected:** Keyboard activation opens the same settings as pointer activation, with a usable focus path to edit and back to the node.

## Code evidence / suggested direction

- `PipelineCanvasPage.tsx:494` updates the inspector's separate `selected` state only in `onNodeClick`; visibility depends on it at line 478.
- `pages/canvas/PipelineFlowCanvas.tsx:41` wires the click callback without a keyboard/selection bridge.
- `components/canvas/FlowNode.tsx:38` renders presentation content without a keyboard activation handler.
- Installed React Flow core 11.11.4 gives nodes `role=button`/`tabIndex=0`, but its Enter/Space handler only updates internal graph selection; the application click callback is mouse-only.

Bridge activation to the existing inspector path without breaking graph keyboard movement or multi-selection. Relevant criterion: [WCAG 2.1.1 Keyboard](https://www.w3.org/WAI/WCAG21/Understanding/keyboard.html).

## Acceptance checks

- Tab to source, transform and sink nodes; Enter/Space open the corresponding inspector.
- Keyboard users can edit a field, close the inspector and continue from a meaningful focus target.
- Pointer selection and graph keyboard navigation still work.

## Environment

**Environment:** Local combined PR #42–49 acceptance checkout, `codex/local-acceptance-20260922`, commit `10a82e337a6c392a32ecc7f73d3b9b13ef9701b6`; built web app + real enterprise API/workers. Reviewed 2026-09-25 in the Chromium-based Codex browser using synthetic test data. This reports the local integration, not a claim about the deployed release.

## Screenshot evidence

![Focused node after Enter and Space; inspector remains closed](https://github.com/user-attachments/assets/560b7575-07e3-4e74-84ba-a1eb90ece169)

![Pointer click opens the same node inspector](https://github.com/user-attachments/assets/22cd3e62-be4a-46a5-b985-f4eda4d61705)
