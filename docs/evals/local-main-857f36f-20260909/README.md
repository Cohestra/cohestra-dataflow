# Isolated Cohestra model evaluation, 2026-09-09 UTC

Source is `/private/tmp/cohestra-plan-20260909` at main commit
`857f36f51d9d58c05b32a4d2941448b1eeebbcbd`.

The harness adds one Go test through an overlay, leaving tracked application
source unchanged. It serves the original `registerAI` handlers, including their
prompt, schema, normalization, validation and one-repair logic, on loopback port
14000. It seeds all 12 synthetic connector records from the original 31-case
suite in a separate PostgreSQL container. No user data or existing services are
accessed. Only the coded catalog is loaded; no custom connector manifests.

This is a model/handler comparison, not a deployed authentication, connector
execution, worker, tenant-isolation, or end-to-end test. Tenant identity is
injected by the harness. Fixtures use no real credentials. The independent
container is `cohestra-eval-db-20260909`, publishing loopback port 15432, capped
at 256MiB and one CPU. Its data is disposable.

## Build

From `apps/workflow-go` in the isolated clone:

```sh
rtk proxy env GOCACHE=/private/tmp/cohestra-eval-20260909/go-cache go test -mod=readonly -overlay=/private/tmp/cohestra-eval-20260909/overlay.json -c -o /private/tmp/cohestra-eval-20260909/ai-harness.test ./internal/api
```

The normal Go dependency cache may need permission for missing downloads.
The initial default-cache build failed with cache permissions and missing-package
messages. Those messages do not establish that the vendor tree is incomplete;
this harness was successfully built in readonly module mode with a writable cache.

## Evaluate (one model at a time)

```sh
rtk proxy python3 /private/tmp/cohestra-eval-20260909/run_model.py qwen3:8b --label qwen3-8b-baseline
rtk proxy python3 /private/tmp/cohestra-eval-20260909/run_model.py granite4.2:8b --label granite42-8b-candidate
```

The model must already exist in native Ollama at loopback port 11434. The runner
does not pull models or change defaults. Each label creates a new directory and
refuses to overwrite earlier evidence. Use `--limit N` only for a labeled smoke
run. The full comparison requires all 31 cases for both models. All calls are
serial with temperature 0, seed 42, context 4096, and thinking false. These are
the original handler settings. HTTP client permits four minutes per model call
and up to one repair; the evaluator waits up to 500 seconds per case.

The runner invokes the original evaluation module and scorers unchanged, with a
thin call wrapper to record API responses and progress. It starts and stops the
dedicated harness. Report files include:

- `provenance.json`: source commit/hashes, configured model, Ollama version/tags.
- `ollama.jsonl`: actual model requests and responses, including returned model
  identity, timing and token counts. These contain only synthetic evaluation
  prompts and fixture names.
- `responses.jsonl`: per-case API output and latency, excluding HTTP failures
  which are recorded in the report.
- `report.json`: unchanged repository evaluator output.
- `server.log`: startup, configured model and fixture count.

Compare actual Ollama request/response model identity and source hashes before
interpreting accuracy. `AI_EVAL_MODEL` alone is a label and cannot prove identity.
Compose is not used, so its tracked model override cannot silently alter the run.

## Offline scorer check

```sh
rtk proxy python3 /private/tmp/cohestra-plan-20260909/tests/ai-evals/run.py --self-test --strict --output /private/tmp/cohestra-eval-20260909/scorer-self-test.json
```

Passed before the live comparison. This checks corpus validation and scorer
behavior only; it is not evidence of model accuracy.

## Cleanup

After both evaluations finish, stop/remove only the disposable container:

```sh
rtk proxy docker rm -fv cohestra-eval-db-20260909
```

Keep the reports and recipe until their durable copies are attached to planning
work. The temporary build cache/binary can then be removed if desired.

## September14 continuation and completion

Working source was recovered at `/private/tmp/cohestra-plan-20260914`; main is
still 857f36f. The unchanged retained binary reads its corpus at the old clone
path, which was restored from identical tracked v1 bytes. Source/binary hashes
were checked against the Qwen baseline.

Granite4.2:8b was installed after an IPv4 download verified the full official
weight SHA-256. `granite42-8b-candidate` is the first attempt: Docker disappeared
after one model call and 27 later requests lost their fixture DB. It is invalid
for accuracy; preserve its report. `granite42-8b-native-db-retry` is the completed
31-case run: 9 passes, zero infrastructure failures, 49 model calls. Qwen also 9/31,
but generation was 2/8 for Qwen and 0/8 for Granite. No model was promoted.

Recovery used PostgreSQL 17.10 Homebrew runtime, with post-install default
cluster/login service skipped. A separate temporary cluster used loopback 15432,
scram auth with synthetic credentials, 32 MiB shared buffers and 10 connections.
Its database was `cohestra_eval`, role `eval`, and fixtures came from the existing
harness. The native cluster has now been stopped; runtime remains installed for
reproducibility. Docker was not restarted. Its old stopped fixture container
cannot be removed while the Docker daemon is unavailable.

Raw reports were not edited. `compare_model_runs.py` checks identities, source
and corpus controls. `prove_empty_edge_acceptance.py` demonstrates the separate
connectivity scoring defect using an in-memory copied response. It does not
change the captured response or raw report. The metadata-only show endpoint and
tag endpoint disagree on capabilities; neither is an agent tool-use benchmark.

Different database deployment/date and heavy host swap mean latency is diagnostic.
Versioned corpus fixes and repeated intended-hardware evaluation remain required
before promotion. Durable copies live in this workspace at
`docs/evals/local-main-857f36f-20260909/`; curated JSON is also attached to PR38.
