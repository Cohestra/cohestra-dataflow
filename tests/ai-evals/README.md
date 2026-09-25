# AI pipeline evaluations

This is the versioned M0 baseline suite for `POST /api/ai/generate` and
`POST /api/ai/refine`. It uses only the Python standard library and keeps all
fixtures synthetic.

## One-command offline check

```bash
python3 tests/ai-evals/run.py --self-test --strict
```

This validates the corpus and exercises every scorer without credentials,
Ollama, a database, or a running API. The CI tests job runs this same check on
pull requests and pushes to main. It checks evaluator behavior, not model accuracy.

## Run against a deployment

```bash
AI_EVAL_BASE_URL=http://localhost:4000 \
AI_EVAL_TOKEN='<bearer token if required>' \
AI_EVAL_MODEL=llama3.1:8b \
AI_EVAL_PROMPT_VERSION=m0-baseline \
python3 tests/ai-evals/run.py --output /tmp/dataflow-ai-eval.json
```

The token is read from the environment and is never written to the report.
`AI_EVAL_TOKEN` may be omitted for an unauthenticated local API. The default
request timeout is 300 seconds; override it with `AI_EVAL_TIMEOUT_SECONDS`.
Use `--token-file /path/to/auth.json` when the token should not appear in the
process command line; the file may contain either the token or an
`accessToken` JSON field.
Use `--limit N` for a smoke run and `--strict` in CI to fail when any case
misses an expectation. Without `--strict`, model misses are recorded but do
not fail the command; corpus, transport, and JSON errors are still visible in
the report.

Before a live bake-off, seed the evaluation tenant with every connector in
the suite's versioned `fixtures.connectors` manifest. The provider and display
name must match exactly so the planner can resolve each case's
`connectorFixtures` references to tenant-owned `connectionId` values. The
manifest intentionally contains no credentials; supply those through the
deployment's normal connector setup and never add them to this repository.

## Metrics

Each case records:

- response-schema validity;
- DAG structure and case-specific branch/merge constraints;
- required and forbidden activity types;
- connector resource/config grounding;
- rejection of activity types and config keys outside the suite's versioned catalog;
- unchanged-node and edge preservation during refinement;
- clarification or rejection behavior;
- observed request latency;
- repair count when the API exposes `repairCount`, `repairs`,
  `metrics.repairCount`, or `meta.repairCount`.

The JSON summary reports rates over applicable cases only. Results also carry
suite, prompt, schema, and model versions so separate runs remain comparable.

## Add cases

Append objects to [`cases/v1.json`](cases/v1.json); the runner discovers them
without code changes. Each case needs a unique `id`, a `category`, one of the
two supported endpoints, a request with `prompt`, and an `expect` object.
Existing cases demonstrate all supported expectations: `status`,
`activities`, `configs`, `nodes`, `trigger`, `execution`, `graph`, and `preserve`.
Update the file's versioned `catalog` when a supported activity or config key
is intentionally added; unlisted model output is scored as hallucinated.
Create `v2.json` rather than changing established expectations incompatibly.

## Opt-in required paths

New case versions can declare `expect.graph.requiredPaths` to require specific
source-to-transform-to-sink relationships. Each path is an ordered array of
node selectors; consecutive selectors must match nodes joined by a **direct,
directed edge**. Include intermediate steps explicitly. Every declared path
must exist as one continuous chain.

```json
{
  "graph": {
    "requiredPaths": [
      [
        {"activityType": "http.fetch"},
        {"activityType": "transform.filter"},
        {"activityType": "sink.s3", "config": {"bucket": "eval-bucket"}}
      ]
    ]
  }
}
```

Selectors require `id`, `activityType`, or both, with an optional `config`
subset. All supplied fields must match the same node. Activity types must
exist in the suite catalog. An explicitly supplied `requiredPaths` must be a
non-empty array of paths with at least two selectors each; malformed contracts
fail corpus loading before any API call.

Prefer `activityType` (plus a `config` subset when it disambiguates) for
generation cases: node IDs there are chosen by the model and are not stable.
Use `id` selectors for refinement cases, where the request pins existing IDs.

Each path is checked independently. Additional independent branches are valid;
declare their paths separately only when the case requires those relationships.
Missing required edges fail structural validity and the overall case, even when
all requested activity types are present. Cases without this field keep their
existing scoring behavior, including acceptance of disconnected DAGs.

A failed path is reported on the case result as
`requiredPathFailure: {"path": <index>, "hop": <index>, "selector": {...}}`.
Hop 0 means no node matches the first selector; hop N means no direct edge
reaches a node matching selector N from the nodes matched at hop N-1.

The offline self-test covers connected and disconnected chains, duplicate
activity types across separate chains, selector mismatches, independent branches,
and invalid contracts. The v1 corpus and historical reports remain unchanged.
A corrected v2 corpus, fixture binding, and positive golden-output preflight
remain separate work; this contract alone does not establish a model promotion gate.
At least six v1 cases cannot pass the current API and scorer at all (G11); see
[the corpus-conflict analysis](../../docs/plans/ai-agents/PRODUCT_GAPS.md#evaluation-and-ci-findings-during-this-review)
before reading v1 scores as model accuracy.
