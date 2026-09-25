# Evaluation evidence index

Raw, dated evidence behind [MODEL_EVALUATION.md](../MODEL_EVALUATION.md) and
[PRODUCT_GAPS.md](../PRODUCT_GAPS.md). Files are point-in-time records; do not
edit results after the fact. Each claim below has one authoritative file.

| Claim | Authoritative file | Supporting files |
| --- | --- | --- |
| qwen3:8b baseline score (9/31) and category breakdown | [qwen3-8b-baseline-report.json](qwen3-8b-baseline-report.json) | [diagnostics](qwen3-8b-baseline-diagnostics.json) |
| Exact model, settings, source hashes and host for the baseline | [qwen3-8b-baseline-provenance.json](qwen3-8b-baseline-provenance.json) | — |
| At least six v1 cases cannot pass the current API and scorer (G11) | [corpus-conflicts.json](corpus-conflicts.json) | — |
| A disconnected graph passes once `edges: null` becomes `[]` | [empty-edge-scorer-proof.json](empty-edge-scorer-proof.json) | — |
| granite4.2:8b score and decision | [granite42-8b-report.json](granite42-8b-report.json) | [diagnostics](granite42-8b-diagnostics.json), [invalid attempts](granite42-invalid-attempt-diagnostics.json) |
| Case-by-case Granite vs qwen3 comparison | [granite42-8b-comparison.json](granite42-8b-comparison.json) | — |
| Exact model, settings, source hashes and host for Granite | [granite42-8b-provenance.json](granite42-8b-provenance.json) | [runtime metadata](granite42-runtime-metadata.json), [native DB](granite42-native-db-provenance.json) |
| CI heads and job outcomes on 2026-09-14 | [ci-checkpoint-20260914.json](ci-checkpoint-20260914.json) | — |
