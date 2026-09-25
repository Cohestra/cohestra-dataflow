# Integration review and evidence

This PR combines the tested frontend/backend/CI implementation from PRs #42–49,
local acceptance fixes and runners, architecture plans from the planning wave,
and their dated evidence. It is a review integration; earlier PRs remain open.
No merge, deployment, agent-runtime enablement or model promotion is implied.

## Start here

- [Local setup and repeatable commands](../LOCAL_ACCEPTANCE.md)
- [September 25 local results and known failures](local-acceptance-20260925/LOCAL_ACCEPTANCE_RESULTS_20260925.md)
- [UX audit and filed GitHub issues #51–58](ux-audit-20260925/README.md)
- [Architecture and delivery plans](../plans/ai-agents/README.md)
- [Implementation status](../plans/ai-agents/IMPLEMENTATION_STATUS.md)
- [Fresh publication checks](pr-publication-20260925/checks.json)
- [Publication hashes, transformations and omissions](PUBLICATION_MANIFEST.json)

The main local execution, RLS, Temporal, Iceberg, metadata, monitoring and control
checks passed. Two real qwen3:8b refinement cases still fail after repair. The
responsive runner has three passing desktop/mobile/keyboard checks with mocked
responses. Mocked passes are not evidence of model accuracy. LangGraph and the
agent runtime remain deferred.

## Historical evidence

The remaining dated directories preserve earlier model evaluations, planning,
CI repairs and implementation-wave results. Interpret every result against its
recorded source/model/date. Historical commands and absolute paths in logs are
provenance; use the current local acceptance guide to reproduce the stack.

## Publication handling

Private credentials and runtime directories remain ignored. Raw model transcripts,
large execution histories, runner caches and obsolete temporary test configs were
omitted; their names and hashes are in the publication manifest. Canonical test
sources remain under tests/ and apps/. Dated reports may name omitted raw files;
those files remain in the original local evidence archive and are not advertised
as included here.

A few publication copies omit redundant fixture pipelineKey values, reshape source
hashes into path/sha256 records, or paraphrase non-secret prose that triggered the
secret detector. Test outcomes and source hash values are unchanged. Text-file
trailing whitespace was normalized for review. The original local evidence is
untouched. Nested historical manifests describe original observations; the
publication manifest is authoritative for the bytes in this PR.

Original-checkout AI-training/QLoRA experiments and unrelated website changes are
not part of this tested integration.
