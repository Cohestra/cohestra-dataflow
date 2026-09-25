# Product UI/UX review — September 25, 2026

Completed after the local development acceptance pass. Eight actionable bugs were filed in Cohestra/cohestra-dataflow with reproduction steps, expected/actual behavior, severity, code evidence, acceptance checks and hosted screenshots. Every issue is open, labelled `bug`, and its published body was read back and verified. Existing open/closed issues were checked before filing; the repository had none at the start.

## Findings

| Priority | GitHub issue | Finding |
| --- | --- | --- |
| P1 | [51](https://github.com/Cohestra/cohestra-dataflow/issues/51) | Leaving the canvas silently discards unsaved pipeline edits |
| P1 | [52](https://github.com/Cohestra/cohestra-dataflow/issues/52) | Enter and Space cannot open focused node settings |
| P2 | [53](https://github.com/Cohestra/cohestra-dataflow/issues/53) | Node configuration controls lack programmatic labels |
| P2 | [54](https://github.com/Cohestra/cohestra-dataflow/issues/54) | Stale Loaded/Saved status hides dirty state and why Save is disabled |
| P2 | [55](https://github.com/Cohestra/cohestra-dataflow/issues/55) | Cancelled pipelines are labelled Never run despite a recent execution |
| P2 | [56](https://github.com/Cohestra/cohestra-dataflow/issues/56) | Each saved version appears as an indistinguishable pipeline row |
| P2 | [57](https://github.com/Cohestra/cohestra-dataflow/issues/57) | Dark-mode pipeline status text falls below AA contrast |
| P2 | [58](https://github.com/Cohestra/cohestra-dataflow/issues/58) | Local refinement fails after repair and exposes raw 422 without useful recovery |

## Product assessment

The list and node-based builder communicate the basic task clearly, and primary actions are visually prominent. The most serious weaknesses are preserving user work and making editing operable from the keyboard. The overview also loses trust by calling cancelled runs “Never run” and presenting historical versions as duplicate pipelines.

The UI already has useful foundations: descriptive navigation controls, explicit disabled terminal execution controls, a responsive list layout at 430px, and a connector form that closes with Escape and restores focus to its Configure button. The previous mobile Save fix remains distinct from this review's open bugs.

## Accessibility evidence

- Focused node buttons do not open inspectors with Enter/Space; pointer activation does. This is mapped to [WCAG 2.1.1 Keyboard](https://www.w3.org/WAI/WCAG21/Understanding/keyboard.html).
- Node catalog controls lack programmatic label associations, mapped to [WCAG 1.3.1 Info and Relationships](https://www.w3.org/WAI/WCAG21/Understanding/info-and-relationships.html). DOM and browser accessibility trees were inspected; actual VoiceOver/NVDA speech was not tested.
- Dark-mode status text is 11px/400, rgba(255,255,255,0.4) over rgb(13,15,23). Alpha-composited foreground is approximately rgb(109.8,111,115.8), giving 3.81255:1. Normal meaningful text requires 4.5:1 under [WCAG 1.4.3](https://www.w3.org/WAI/WCAG21/Understanding/contrast-minimum.html). This measurement is for the status, not every text token.

## Scope and provenance

Live review covered pipeline discovery/details, canvas editing and navigation, keyboard node activation, node configuration labels, validation/dirty feedback, mobile navigation, and connector setup presentation. Layouts were inspected at the normal 800px browser width, 1280×900 and 430×932. The previous real-model browser failure supplies issue #58's AI evidence; it was not re-generated in this UX pass. Earlier run/monitoring and development checks remain in the [local acceptance report](../local-acceptance-20260925/LOCAL_ACCEPTANCE_RESULTS_20260925.md).

Source: `.local-acceptance/checkout`, `codex/local-acceptance-20260922`, `10a82e337a6c392a32ecc7f73d3b9b13ef9701b6`. This is a focused local audit, not production verification or full WCAG certification. No paid-service actions or external connector authorization were exercised. No product code or saved pipeline/credential was changed in this pass. Unsaved synthetic changes were discarded by the navigation path being tested; the saved fixture remained intact. Temporary viewport overrides were reset.

Two agents independently located the source mechanisms for the reproduced UX findings; root performed browser reproduction and issue publication. Paused-list status is explicitly identified as source-supported rather than a newly exercised live case. Inspector focus-return concerns were not filed as a separate confirmed bug. Full screen-reader, 200% zoom, cross-browser and exhaustive billing/team/lifecycle coverage remain outside this focused pass.

## Evidence

Screenshots, reproduction steps and acceptance checks are in the linked GitHub issues (#51–58); they are not duplicated in the repository. Private account files, trace archives, tokens and credential values were not uploaded. Connector credentials were not saved or changed.

The stale Loaded v1 status after removing the only sink is shown in [issue 54](https://github.com/Cohestra/cohestra-dataflow/issues/54).
