## Impact

**P2 — users cannot identify the current pipeline or distinguish old versions.**

Saving creates another version, and the overview presents every version as a separate row with the same name and no version indicator. This makes routine saves look like duplicate pipelines and invites editing/running an older version.

## Reproduce

1. Create and save a named pipeline.
2. Edit and save the same logical pipeline several times.
3. Return to Pipelines and search for its name.

**Actual:** Multiple indistinguishable rows appear. In the local fixture, `Local review c0948aa2` has four rows corresponding to versions 1–4 of the same pipeline key. The list omits versions; the drawer reveals them. Read-only data inspection confirmed these are versions, not independent fixtures with coincident names.

**Expected:** Default to one current entry per logical pipeline/environment, with clearly accessible version history. If a view intentionally shows all versions, label each version and its status explicitly.

## Code evidence / suggested direction

- `apps/workflow-go/internal/api/routes_pipelines.go:85–91` inserts MAX(version)+1 as a new row.
- The list query at lines 235–239 returns all version rows.
- `apps/web/src/pages/PipelinesPage.tsx:455–469` renders them by row ID with name/trigger/stage, without version.

Preserve historical versions and execution-to-version identity; address default listing/grouping rather than deleting history.

## Acceptance checks

- Three saves produce one clearly identified current pipeline entry in the normal overview.
- Version history exposes all three versions and supports deliberate version selection.
- Pagination/search do not inflate counts with hidden historical versions, and last-run information has clear version semantics.

## Environment

**Environment:** Local combined PR #42–49 acceptance checkout, `codex/local-acceptance-20260922`, commit `10a82e337a6c392a32ecc7f73d3b9b13ef9701b6`; built web app + real enterprise API/workers. Reviewed 2026-09-25 in the Chromium-based Codex browser using synthetic test data. This reports the local integration, not a claim about the deployed release.

## Screenshot evidence

![Four same-named rows after repeated saves](https://github.com/user-attachments/assets/c70098cb-d4a8-45c6-a648-c2b86d59a3a5)
