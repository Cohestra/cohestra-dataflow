# AI model bake-off

> **Latest result (October 2026):** an 11-model CPU bake-off plus a top-5
> base vs few-shot comparison on the v2 lakehouse suite selected `qwen3:8b`
> with few-shot planner examples (14/17). See
> [evals/cpu-bakeoff-20261016](evals/cpu-bakeoff-20261016/README.md). The
> August notes below are kept for history.

This runbook selects an Ollama model for the DataFlow pipeline planner. Run the
same versioned corpus, prompt, schema, API build, context window, and generation
settings for every candidate. `AI_EVAL_MODEL` only labels the report; it does
**not** change the model used by the API.

## Decision for this host

Run `qwen3:8b` natively first and use `llama3.1:8b` or the already-present
`qwen2.5vl:7b` as the local baseline. Treat `qwen3:14b` as an optional,
single-request native experiment after checking memory pressure. Do not run the
20B or 35B candidates, or a full-stack containerized Ollama bake-off, on this
host.

Observed on 2026-08-05:

| Resource | Observation | Consequence |
|---|---:|---|
| Host memory | 18 GB RAM | 8B models are the safe local tier. |
| Free disk | about 70 GiB | Enough for the 8B runs, but not enough headroom to retain every candidate plus temporary pull data safely. |
| Docker memory | 8,217,305,088 bytes (7.653 GiB) | The Compose `ollama` limit is 10 GB, but the Docker VM cannot supply it. The application stack and an 8B Ollama container would contend for the same 7.653 GiB. |
| Native Ollama models | `qwen2.5vl:7b`, `llama3.1:8b`, and `qwen3:8b` installed | The baseline and local challenger are ready to compare. |

The disk and Docker observations were checked with `df -h /` and
`docker info --format '{{json .MemTotal}}'`. Native `ollama list` was blocked
from this task's sandbox, so the model inventory and active download are the
root orchestration observations.

## Candidate matrix

Published artifact sizes are approximate and do not include KV cache, runtime,
or application memory.

| Candidate | Published artifact | Role | This host | Recommended execution |
|---|---:|---|---|---|
| `qwen2.5vl:7b` | Capture from local `/api/tags` | Observed smoke baseline | Safe natively | Run once; do not promote without beating the text-only baselines. |
| `llama3.1:8b` | 4.9 GB | Compose-configured historical baseline | Safe natively if installed | Useful comparison, not worth blocking the first run. |
| `qwen3:8b` | 5.2 GB | Primary local challenger | Installed; native smoke passed | Run the API smoke, then the full corpus twice. |
| `qwen3:14b` | 9.3 GB | Higher-quality local candidate | Conditional natively; unsafe in Docker | Run only with other heavy apps stopped, a small fixed context, concurrency 1, and no sustained swap. |
| `gpt-oss:20b` | 14 GB | Flagship candidate | Not safe for a representative run | Use at least 24 GB accelerator/unified memory; 32 GB is preferred when the API and context share the host. |
| `qwen3.6:35b` | 24 GB | High-end ceiling | Does not fit | Use a separate 48 GB-class host; 32 GB may fit only with constrained context and little operational headroom. |

Sources: [Llama 3.1 tags](https://ollama.com/library/llama3.1/tags),
[Qwen3 tags](https://ollama.com/library/qwen3/tags),
[gpt-oss](https://ollama.com/library/gpt-oss), and
[Qwen3.6 tags](https://ollama.com/library/qwen3.6/tags). Ollama states that
`gpt-oss:20b` can run in 16 GB memory; that is a fit claim, not enough headroom
for this 18 GB development host to run the model and DataFlow reliably.

## Fixed test conditions

1. Run from the repository root.
2. Pin the API commit, `tests/ai-evals/cases/v1.json`, prompt version, schema
   version, temperature, seed, context window, and thinking mode for the whole
   comparison.
3. Configure the API's real `OLLAMA_MODEL` before each run. Then verify the
   model returned by Ollama; never rely on the `AI_EVAL_MODEL` report label.
4. Use concurrency 1. Record one cold smoke run, then two warm full-suite runs.
5. Keep the first result even when it fails. Do not tune a prompt between
   candidates.

The current v1 corpus contains 31 cases. It is enough for a bake-off checkpoint,
not the final 100-case promotion suite. A winner is provisional until the
planned 100 cases pass the same gates.

## Commands

First validate the corpus and scorers without a running API:

```bash
python3 tests/ai-evals/run.py --self-test --strict
```

For a Compose API using native Ollama, configure the actual candidate. The API
and the evaluator both default to port 4000 in this repository.

```bash
OLLAMA_URL=http://host.docker.internal:11434 \
OLLAMA_MODEL=qwen3:8b \
OLLAMA_THINK=false \
docker compose up -d --no-deps api
```

If native Ollama is not reachable from the container, run the API natively or
adjust the already-approved local Ollama binding. Do not fall back to the
containerized Ollama service on the 7.653 GiB Docker VM for a full-stack run.

Run a five-case transport and schema smoke test:

If authentication is enabled, export `AI_EVAL_TOKEN` in the shell first. Leave
it unset for an unauthenticated local API.

```bash
AI_EVAL_BASE_URL=http://localhost:4000 \
AI_EVAL_MODEL=qwen3:8b \
AI_EVAL_PROMPT_VERSION=m3-grounded-v1 \
AI_EVAL_SCHEMA_VERSION=pipeline-definition-v1 \
python3 tests/ai-evals/run.py \
  --limit 5 \
  --timeout 300 \
  --output /tmp/dataflow-ai-qwen3-8b-smoke.json
```

Then run the complete corpus. Increase the timeout for CPU-only candidates,
but keep it identical for their repeated runs.

```bash
AI_EVAL_BASE_URL=http://localhost:4000 \
AI_EVAL_MODEL=qwen3:8b \
AI_EVAL_PROMPT_VERSION=m3-grounded-v1 \
AI_EVAL_SCHEMA_VERSION=pipeline-definition-v1 \
AI_EVAL_TIMEOUT_SECONDS=300 \
python3 tests/ai-evals/run.py \
  --output /tmp/dataflow-ai-qwen3-8b-run-1.json
```

Repeat as `run-2` without changing configuration. Do not use `--strict` for
model selection: in this harness it means every case must pass, while the
promotion gates below are rate-based. Keep `--strict` for scorer self-tests or
an eventual all-cases CI gate.

## Promotion gates

| Measure | Gate | Current harness evidence |
|---|---:|---|
| Schema-valid responses | >= 99% | `summary.schemaValidRate` |
| Structurally valid DAGs | >= 95% | `summary.structuralValidRate` |
| Activity and connector direction accuracy | >= 95% | `summary.activityAccuracyRate` |
| Resource/config grounding | >= 90% | `summary.groundingAccuracyRate` |
| Unrelated refinement preservation | >= 90% | `summary.preservationAccuracyRate` |
| Correct clarification/rejection | >= 95% | `summary.clarificationAccuracyRate` |
| Repairs | No case above 1 | Inspect each `results[].repairCount`; the summary only reports the mean. |
| Warm interactive latency | p95 < 30 seconds | `summary.latencyMsP95`; this is a production-hardware gate, not a reason to reject an otherwise useful CPU-only local characterization. |
| Invented credentials, secrets, or connector IDs | 0 | Manual review of adversarial results until the corpus adds an automated detector. |
| Stability | No OOM, process exit, or sustained swap | Host and Ollama resource capture below. |

Promote only a candidate that beats the 8B baseline materially on grounding,
preservation, or clarification without failing any hard gate. A larger model
that only improves prose is not a pipeline-planner improvement.

## Identity, usage, and resource capture

Before each run, save model identity alongside the evaluation JSON. `/api/tags`
provides the immutable digest and size; `/api/show` provides parameter size,
quantization, capabilities, parameters, and model metadata.

```bash
curl -sS http://127.0.0.1:11434/api/tags \
  > /tmp/ollama-tags-qwen3-8b.json

curl -sS http://127.0.0.1:11434/api/show \
  -H 'Content-Type: application/json' \
  -d '{"model":"qwen3:8b","verbose":false}' \
  > /tmp/ollama-show-qwen3-8b.json
```

During a run, capture `/api/ps`; it reports the loaded model digest, resident
size, VRAM size, and active context length.

```bash
curl -sS http://127.0.0.1:11434/api/ps \
  > /tmp/ollama-ps-qwen3-8b.json
```

Ollama's non-streaming `/api/chat` response includes `model`, `total_duration`,
`load_duration`, `prompt_eval_count`, `prompt_eval_duration`, `eval_count`, and
`eval_duration`; durations are nanoseconds. Output tokens per second is
`eval_count / (eval_duration / 1e9)`. See the official
[usage fields](https://docs.ollama.com/api/usage),
[chat response](https://docs.ollama.com/api/chat),
[model list](https://docs.ollama.com/api/tags),
[model details](https://docs.ollama.com/api-reference/show-model-details), and
[running-model response](https://docs.ollama.com/api/ps).

The current evaluator records end-to-end HTTP latency and recognizes repair
count, but the DataFlow API must propagate or log the Ollama usage fields for
per-case token and model timings. Until it does, preserve the API logs and mark
those result columns unavailable rather than estimating them.

## Results

Fill one row from each full-suite report and its Ollama sidecars.

| Candidate + digest | Host/runtime | Suite | Schema | Structure | Activities | Grounding | Preserve | Clarify | Mean / p95 | Repairs max | Peak resident | Decision |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|
| `qwen2.5vl:7b` / TBD | 18 GB / native | v1, 31 | TBD | TBD | TBD | TBD | TBD | TBD | TBD | TBD | TBD | Baseline only |
| `llama3.1:8b` / `46e0c10c039e` | 18 GB / native, 5.3 GB resident | v1 smoke, 3 | 100% | N/A | 30% | N/A | N/A | N/A | 60.5s / 84.5s | 1 | 5.3 GB | Baseline only; no acceptable DAG |
| `qwen3:8b` thinking off / `500a1f067a9f` | 18 GB / native, 5.6 GB resident | v1 smoke, 3 | 100% | 100% | 100% | 91.67% | N/A | N/A | 50.0s / 67.4s | 1 | 5.6 GB | Provisional local winner; not production-promoted |
| `qwen3:14b` / TBD | 18 GB / native, conditional | v1, 31 | TBD | TBD | TBD | TBD | TBD | TBD | TBD | TBD | TBD | Conditional |
| `gpt-oss:20b` / TBD | External >= 24 GB | v1, 31 | TBD | TBD | TBD | TBD | TBD | TBD | TBD | TBD | TBD | Flagship candidate |
| `qwen3.6:35b` / TBD | External 48 GB-class | v1, 31 | TBD | TBD | TBD | TBD | TBD | TBD | TBD | TBD | TBD | Ceiling |

Final selection requires the expanded 100-case suite, the same warm latency
protocol on intended production hardware, and a recorded model digest.

### Executed checkpoint — 2026-08-07 IST

Both installed 8B models ran through the authenticated DataFlow API with the
same first three v1 generation cases, seeded `tenant-connectors-v1` fixtures,
prompt/schema versions, temperature 0, seed 42, `num_ctx=4096`, concurrency 1,
`OLLAMA_THINK=false`, and a 300-second evaluator timeout. Both controlled runs
used API binary SHA-256
`837abcc302b48153426d3f535146e43c1c8646abaf899338d157205cb0ec2ad4`,
built from Git HEAD `b09ce901b9cbf6eb03bfee1cf75a5c8be732bc5c` plus the documented working diff.

- `qwen3:8b`, thinking disabled: two of three cases passed every expectation;
  all responses were ready, schema-valid DAGs with correct activities.
  Grounding was 91.67%, and latency was 50.0 seconds mean / 67.4 seconds p95.
  One case used the single repair attempt.
- `llama3.1:8b`: all three responses were schema-valid, but none
  produced an acceptable ready DAG; all three used the repair path. Its
  controlled latency was 60.5 seconds mean / 84.5 seconds p95.

Decision: **promote neither model to production yet**. `qwen3:8b` with thinking
disabled is the provisional local winner and `llama3.1:8b` remains the
historical baseline. Repeat the full 31-case suite, then the planned 100-case
promotion suite, on intended production hardware before changing the default
model. Ollama documents the per-request thinking switch in its
[thinking capability guide](https://docs.ollama.com/capabilities/thinking).
