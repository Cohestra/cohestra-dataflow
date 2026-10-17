# CPU model bake-off — October 2026

Which Ollama model should drive the AI pipeline builder, and does few-shot
"training" help? Run on a 4 vCPU / 15 GiB / **CPU-only** host with Ollama
0.40.5, temperature 0, seed 42, thinking off, one request at a time. Every
request went through the **real** `/api/ai/generate` and `/api/ai/refine`
handlers (Go test overlay, [`tooling/eval_harness_test.go`](tooling/eval_harness_test.go))
with the 12 synthetic connector fixtures seeded, and was scored by the repo's
own `tests/ai-evals/run.py`.

**Decision:** `qwen3:8b` with few-shot planner examples (`AI_FEW_SHOT=true`,
`OLLAMA_NUM_CTX=8192`) is now the compose, Helm and API default. It passed
**14/17** lakehouse cases (10/17 without examples). The previous defaults were
among the weakest models tested: `llama3.1:8b` 2/17 and `llama3.2:3b` 4/12.

## Models

Weights came from Docker Hub's `ai/` mirror of the Ollama library (Q4 GGUF;
digests in each `provenance.json`) because ollama.com was not reachable from
the host. The newest Qwen releases (`qwen3.6`, `qwen3.8`) start at 27B
(~17-18 GB) and need ~24 GiB plus a GPU, so `qwen3.5:9b` is the newest Qwen
that fits; `gpt-oss:20b` was excluded for the same reason.

## Phase 1 — base ranking (12 passable v1 cases, previous prompt)

The 12 cases are the v1 corpus minus the G11 conflicts that no model can pass
([`tooling/subset12.json`](tooling/subset12.json)). Both adversarial cases are
caught by the API's deterministic guard, so 2/12 is the floor.

| # | Model | Passed | Sub-score | Schema | Struct | Activity | Grounding | Preserve | Clarify | Errors (timeouts) | Mean / P95 latency |
|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 | `qwen3:8b` | 6/12 | 88% | 100% | 67% | 91% | 83% | — | 100% | 2 (0) | 173s / 303s |
| 2 | `qwen3:4b-instruct-2507` | 5/12 | 92% | 100% | 100% | 100% | 87% | — | 75% | 3 (0) | 90s / 210s |
| 3 | `qwen3.5:4b` | 5/12 | 89% | 100% | 100% | 45% | 100% | — | 100% | 4 (0) | 124s / 184s |
| 4 | `llama3.1:8b` | 5/12 | 86% | 100% | 100% | 28% | 100% | — | 100% | 0 (0) | 123s / 217s |
| 5 | `gemma3:4b` | 4/12 | 87% | 100% | 100% | 36% | 100% | — | 100% | 4 (0) | 53s / 95s |
| 6 | `qwen3.5:9b` | 4/12 | 87% | 100% | 75% | 73% | 85% | — | 100% | 3 (0) | 145s / 256s |
| 7 | `llama3.2:3b` | 4/12 | 72% | 100% | — | 15% | — | — | 100% | 0 (0) | 80s / 131s |
| 8 | `nemotron-3-nano:4b` | 3/12 | 80% | 100% | — | 40% | — | — | 100% | 7 (2) | 134s / 240s |
| 9 | `ministral3:3b` | 3/12 | 72% | 100% | — | 15% | — | — | 100% | 1 (1) | 91s / 240s |
| 10 | `granite4.2:8b` | 3/12 | 71% | 100% | — | 13% | — | — | 100% | 6 (1) | 195s / 334s |
| 11 | `granite4.2:3b` | 2/12 | 100% | 100% | — | — | — | — | 100% | 10 (0) | 83s / 236s |

## Phase 2 — top 5, base vs few-shot ([`v2-datalake`](../../../tests/ai-evals/cases/v2-datalake.json), 17 cases)

Medallion/lakehouse generation (CDC → contract → Iceberg, silver → gold,
joins, S3 flatten, three-zone fork, Kafka parse/dedupe), Spark SQL and cron
engines, multi-turn chat (answering a follow-up, a revert request), G11-safe
refinements, and validation (unknown connection, cycle). Every case passes
with golden outputs through the same handlers ([`reports/golden-v2.json`](reports/golden-v2.json)),
so every miss is the model's. Both arms use the improved prompt (allowed
select values listed, commit `22ff515`) and `num_ctx` 8192 on the enterprise
build.

### Base

| # | Model | Passed | Sub-score | Schema | Struct | Activity | Grounding | Preserve | Clarify | Errors (timeouts) | Mean / P95 latency |
|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 | `qwen3:8b` | 10/17 | 95% | 100% | 80% | 95% | 94% | 100% | 100% | 4 (1) | 159s / 386s |
| 2 | `qwen3:4b-instruct-2507` | 10/17 | 94% | 100% | 75% | 96% | 93% | 100% | 100% | 2 (0) | 137s / 312s |
| 3 | `qwen3.5:4b` | 6/17 | 92% | 100% | 100% | 53% | 100% | 100% | 100% | 8 (1) | 187s / 364s |
| 4 | `gemma3:4b` | 4/17 | 91% | 100% | 100% | 45% | 100% | 100% | 100% | 10 (1) | 111s / 272s |
| 5 | `llama3.1:8b` | 2/17 | 56% | 100% | — | 22% | — | 0% | 100% | 7 (2) | 223s / 392s |

### Few-shot (`AI_FEW_SHOT=true`)

| # | Model | Passed | Sub-score | Schema | Struct | Activity | Grounding | Preserve | Clarify | Errors (timeouts) | Mean / P95 latency |
|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 | `qwen3:8b` | **14/17** | 98% | 100% | 92% | 100% | 98% | 100% | 100% | 1 (0) | 151s / 328s |
| 2 | `qwen3:4b-instruct-2507` | 11/17 | 98% | 100% | 92% | 99% | 96% | 100% | 100% | 2 (0) | 88s / 196s |
| 3 | `qwen3.5:4b` | 9/17 | 94% | 100% | 75% | 88% | 100% | 100% | 100% | 5 (2) | 123s / 317s |
| 4 | `gemma3:4b` | 7/17 | 91% | 100% | 62% | 91% | 92% | 100% | 100% | 1 (0) | 60s / 224s |
| 5 | `llama3.1:8b` | 3/17 | 68% | 92% | 67% | 27% | 96% | 30% | 100% | 5 (2) | 171s / 307s |

### By category (base → few-shot)

| Model | Lake generation (6) | Engines (2) | Chat (3) | Refinement (4) | Validation (2) |
|---|---|---|---|---|---|
| `qwen3:8b` | 1 → **4** | 1 → 1 | 2 → **3** | 4 → 4 | 2 → 2 |
| `qwen3:4b-instruct-2507` | 2 → 4 | 0 → 0 | 2 → 3 | 4 → 3 | 2 → 1 |
| `qwen3.5:4b` | 0 → 1 | 0 → 0 | 1 → 3 | 4 → 3 | 1 → 2 |
| `gemma3:4b` | 0 → 1 | 0 → 0 | 2 → 2 | 0 → 3 | 2 → 1 |
| `llama3.1:8b` | 0 → 0 | 0 → 0 | 1 → 1 | 0 → 1 | 1 → 1 |

## Findings

- **Few-shot beats fine-tuning here.** Real QLoRA training needs a GPU, and the
  August GPU attempt with 6 examples did not beat its base model. Two
  category-matched worked examples ([`ai_fewshot.go`](../../../apps/workflow-go/internal/api/ai_fewshot.go))
  lifted every top-5 model by 1 to 4 cases, mostly on multi-node lake
  generation and chat, and cut `qwen3:8b` errors from 4 to 1.
- **Enum guessing was the main failure mode.** The prompt named config keys
  but not their allowed values, so models guessed `keep`, `writeMode`,
  `syncMode` or `layer` and burned their single repair. Fixed in `22ff515`.
- **Array vs comma-list (fixed after the runs, `b75aa24`):** every model sent
  `transform.parse` `fields` as a JSON array, which grounding rejected even
  though the runtime accepts lists. Grounding now joins string arrays for
  comma-list fields and the prompt states the format. Re-run with `qwen3:8b` +
  few-shot, `lake-kafka-parse-dedupe-iceberg` now returns a valid ready graph
  with `fields: "amount,currency"` and no repair; it still misses one
  grounding check (`dedupe.scope: pipeline` for "across the whole pipeline").
- **Dead-end branches (fixed after the runs, `b16d9ce`, `6308912`):** the live
  browser run showed `qwen3:8b` attaching a requested contract as a dead-end
  branch, which the API returned as ready although the canvas cannot save it.
  Planner proposals now go through the canvas's shape rules and use the
  repair round; if the model repeats the mistake, the proposal is returned
  with a "Review the graph before saving" warning instead of an error.
- **Insert-before-sink refines (fixed, `f024f7c`, `96ff16c`, `c3074ce`):** a
  3-node insertion few-shot example, an explicit edge-replacement rule, the
  `old:new` rename shorthand, an unambiguous dangling-step splice (recorded as
  an assumption) and `OLLAMA_TIMEOUT_SECONDS` (480 in compose/Helm). Real-model
  check, contract and rename inserted before an Iceberg sink: 4/4 correct
  graphs with no repair round on both picker models (two correct from the
  model, two spliced), against 0/4 before. The live browser run with
  `qwen3:8b` then produced a saveable refine on the first call.
- **Spark SQL:** the community build rejects `execution.engine=spark-sql`
  ("requires the enterprise build"), and the ee build accepts only
  `s3.fetch`/`iceberg.fetch` → `sink.iceberg`/`sink.clickhouse`, so v1's
  `engine-spark-sql` and `engine-flink-sql` cases cannot pass a community API.
- **Latency on CPU:** `qwen3:8b` averages ~2.5 min per request (P95 ~5.5 min
  including one repair), and the API's per-call client timeout is 4 min, which
  caused the occasional timeout. Production should run Ollama on a GPU, or use
  `qwen3:4b-instruct-2507` (11/17, mean 88 s) where latency matters more.

## Reproduce

Tooling is in [`tooling/`](tooling/): `fetch.sh` (Docker Hub GGUF; waits out
the anonymous pull rate limit), `bakeoff.sh` / `one.sh` (phase 1),
`p2one.sh` (one phase-2 arm), `run_model2.py` (harness + scorer), `rank.py`
(tables), and `make_v2.py` / `golden_v2.py` / `golden_check.sh` (v2 corpus and
golden preflight). Set `BAKE` to a work directory holding `scripts/`, `runs/`
and `models/`, and build the harness with
`go test -mod=vendor [-tags ee] -overlay overlay.json -c ./internal/api/`,
where `overlay.json` maps `internal/api/eval_harness_test.go` to the tooling
copy. The harness needs a Postgres on `127.0.0.1:15432` (user `eval`).
