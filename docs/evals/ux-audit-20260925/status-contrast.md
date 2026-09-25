## Impact

**P2 — low-vision users cannot reliably read execution status.**

The enabled pipeline row's Never run status uses faint 11px text. Computed CSS and alpha compositing give a contrast ratio of approximately **3.81:1**, below the **4.5:1** requirement for normal text.

## Reproduce / measurement

1. Open Pipelines in dark mode with an unrun pipeline (also visible on the cancelled-status bug).
2. Inspect the Never run span and its ancestor backgrounds.
3. Foreground is `rgba(255,255,255,0.4)`, font 11px/400; the opaque ancestor background is `rgb(13,15,23)` (`#0d0f17`). Intervening backgrounds are transparent and opacity is 1.
4. Composite foreground ≈ `rgb(109.8,111,115.8)`. Using WCAG relative luminance: `(L(fg)+0.05)/(L(bg)+0.05) = 3.81255`.

**Expected:** At least 4.5:1 for this meaningful status text. This is content inside an enabled row, not an inactive control or decoration.

## Suggested direction

Use a readable semantic secondary-text color and check it against actual light/dark surfaces. Do not rely on increasing font weight at this small size as a substitute for contrast.

Reference: [WCAG 1.4.3 Contrast (Minimum)](https://www.w3.org/WAI/WCAG21/Understanding/contrast-minimum.html).

## Acceptance checks

- Verify computed/composited status colors meet 4.5:1 on both themes and row states.
- Preserve text labels alongside status dots; color alone should not convey execution state.
- Check adjacent secondary timestamps/labels for the same token issue; only the measured status is asserted here.

## Environment

**Environment:** Local combined PR #42–49 acceptance checkout, `codex/local-acceptance-20260922`, commit `10a82e337a6c392a32ecc7f73d3b9b13ef9701b6`; built web app + real enterprise API/workers. Reviewed 2026-09-25 in the Chromium-based Codex browser using synthetic test data. This reports the local integration, not a claim about the deployed release.

## Screenshot evidence

![Meaningful status text is visibly faint in dark mode](https://github.com/user-attachments/assets/aac02ba8-5fe9-442b-b85e-de054fc1942d)
