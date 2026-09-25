# GPT-OSS few-shot bake-off results

Run date: 2026-08-12
Held-out suite: `tests/ai-evals/cases/few-shot-test-v1.json` (14 cases)
Hardware: NVIDIA L4 24 GB
Inference: Ollama, temperature 0, seed 42, 4096-token context, thinking enabled

| Arm | Passed | Pass rate | Activity | Grounding | Preservation | Mean latency | P95 latency | Mean repairs |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Base GPT-OSS 20B | 4/14 | 28.57% | 0% | 100% | 100% | 23.87 s | 52.23 s | 0.25 |
| Base + category-matched few-shot | 7/14 | **50.00%** | **50%** | 96% | **50%** | **15.77 s** | **35.19 s** | 0.25 |
| QLoRA-trained adapter | 4/14 | 28.57% | 20% | 100% | 0% | 31.50 s | 63.75 s | 0.38 |

All arms scored 100% for schema validity, structural validity, and clarification accuracy.

## Decision

Use base GPT-OSS 20B with category-matched few-shot prompting for the next DataFlow trial. It improved the held-out pass rate by 21.43 percentage points over base, while reducing mean latency by 34% and P95 latency by 33%.

Do not promote this smoke-trained QLoRA adapter. Six training examples over six epochs did not beat the base model and regressed preservation and latency. The useful next training experiment would require a larger, category-balanced corpus with more refinement/preservation examples and an unchanged held-out test set.

## Artifacts

- `base.json`: base GPT-OSS report
- `few-shot.json`: runtime few-shot report
- `trained.json`: QLoRA-trained report
- Final adapter archive in S3: `s3://dataflow-integration-qa-726929246977/model-checkpoints/gpt-oss-20b/few-shot-smoke-v1/2026-08-12/`
- Final adapter SHA-256: `5b950cec51fc17bd1a266bd96c4271f910cf7f42dbc0f38220654fd779e2ff8d`
