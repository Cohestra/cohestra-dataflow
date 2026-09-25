# Integration review and evidence

This PR combines the tested frontend/backend/CI implementation from PRs #42–49,
local acceptance fixes and runners, architecture plans from the planning wave,
and their dated evidence. It is a review integration; earlier PRs remain open.
No merge, deployment, agent-runtime enablement or model promotion is implied.

## Start here

- [Local setup and repeatable commands](../LOCAL_ACCEPTANCE.md)
- [September 25 local results and known failures](../LOCAL_ACCEPTANCE_RESULTS_20260925.md)
- [UX audit and filed GitHub issues #51–58](ux-audit-20260925/README.md)
- [Architecture and delivery plans](../plans/ai-agents/README.md)
- [Implementation status](../plans/ai-agents/IMPLEMENTATION_STATUS.md)

The main local execution, RLS, Temporal, Iceberg, metadata, monitoring and control
checks passed. Two real qwen3:8b refinement cases still fail after repair. The
responsive runner has three passing desktop/mobile/keyboard checks with mocked
responses. Mocked passes are not evidence of model accuracy. LangGraph and the
agent runtime remain deferred.

## What is committed

Only curated summaries and the small historical model-evaluation records cited by
[MODEL_EVALUATION.md](../plans/ai-agents/MODEL_EVALUATION.md) are kept here
(`gcp-main-f4a4d824`, `gptoss-few-shot-bakeoff`, `local-main-857f36f-20260909`).
Raw run output (sandbox histories, acceptance logs, screenshots, CI snapshots) is
not committed: the `Temporal sandbox`, `Canvas regression` and controls workflows
upload equivalent artifacts on each run, and UX screenshots live in the linked
issues. Interpret every result against its recorded source/model/date.

Original-checkout AI-training/QLoRA experiments and unrelated website changes are
not part of this tested integration.
