# Reviewing AI pipeline proposals

After Generate or Refine, expand the items under **Review changes** before choosing Apply. The review lists added/removed nodes, activity changes, edited configuration fields, policies and asset bindings, connections/conditions, and execution settings. Each field shows its change action and before/after representation. Unlisted settings stay unchanged.

The review and Apply use the same merged definition, captured against the draft sent to the planner. Settings omitted by a refinement stay on matching node IDs/activity types. An activity change removes incompatible old settings and the review lists those removals. Editing the draft makes the proposal stale and disables Apply until the draft matches again or a new proposal is generated.

Known short identifiers and numbers can be shown. Credentials, connection references, headers, request bodies, URLs, SQL, conditions and arbitrary text are hidden; structured values show only counts unless individual safe binding fields can be described. A changed hidden value is still listed as Changed even when both representations say Hidden. The preview is not a credential-inspection tool. Proposals and their draft snapshots remain in component memory and are cleared on Apply/Discard/navigation, without browser storage.

The disclosure summaries support keyboard Enter/Space. Apply and Discard return focus to the request input; Undo restores the complete previous draft and also returns focus there. Viewing or expanding the review does not save or execute a pipeline.

## Verification boundary

`npm -w @dataflow/web test` includes the proposal merge/summary regression. `npm -w @dataflow/web run test:e2e -- tests/pipeline-metadata.spec.ts` exercises the actual canvas using synthetic intercepted API responses: review expansion and redaction, stale blocking, Apply/save, Undo/save, Discard/save and Mermaid metadata preservation. It starts a disposable localhost Vite server and uses Chrome; it does not invoke Ollama, Temporal or external connectors.

This slice does not complete F1's real backend sandbox/data-output comparison, full mobile/zoom/theme accessibility coverage, catalog/agent UI, or saved-version readback recovery. Those remain separate integration and follow-up gates in the approved frontend plan.
