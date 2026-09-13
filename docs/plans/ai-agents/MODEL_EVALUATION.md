# Ollama model assessment and evaluation record

Status: investigation; no production model promotion or default change.
Research date: 2026-09-09 UTC; Notion and primary candidate registry rechecked 2026-09-14.
Application baseline: `857f36f51d9d58c05b32a4d2941448b1eeebbcbd`.

## Latest recorded Dataflow accuracy

The latest relevant Notion page found was **DataFlow AI Pipeline Builder —
M0–M4 & Ollama Bake-off**, last edited 2026-08-12. Its retained full-suite
measurements are dated August 7, against main `f4a4d824...` on an NVIDIA L4.
A separate search for few-shot results returned that same page, not a newer
completed experiment. This is the latest evidence located, not proof that no
unconnected or differently named report exists.

| Historical model | Passed, two 31-case runs | Warm p95 | Interpretation |
| --- | --- | --- | --- |
| gpt-oss:20b, thinking on | 10 / 10 | 47.41s | Safety-first reference; only 1/8 generation cases, 16 HTTP failures after repair. |
| qwen3:8b, thinking off | 6 / 6 | 23.90s | Faster generation baseline; poor overall task coverage. |
| llama3.1:8b, thinking off | 8 / 9 | 19.04s | Mostly clarification/rejection; no passing generation cases. |

All three failed every refinement and engine/trigger case. None met promotion
gates. Conditional 100% structure/grounding on scoreable responses must not be
presented as 100% end-to-end accuracy. Raw historical reports referenced by the
runbook live under `docs/evals/gcp-main-f4a4d824/` in the prior experiment checkout;
they are not assumed present on this clean main branch.

The current branch and original experiment checkout differ. New measurements
must compare candidates on the same current build; historical cloud latency
is not directly comparable to local laptop latency. Few-shot prompting and
fine-tuning are separate experiments and are not silently enabled here.

## Current official candidates

Registry information is a reason to test, not proof of Dataflow accuracy.
Artifact sizes exclude runtime, KV cache, the application and operating system.

| Model | Published artifact | Fit and next action |
| --- | --- | --- |
| granite4.2:8b | 5.3 GB | Newly listed text model with tools/structured JSON support; primary new local challenger at roughly the existing 8B footprint. |
| qwen3.5:9b | 6.6 GB | Practical second local challenger; compare with qwen3 using the same context and thinking controls. |
| gemma4:12b | 7.6 GB | Conditional local candidate; validate runtime/template support and memory pressure before a sustained run. |
| qwen3.6:27b / 35b | 18 / 23 GB | Exceeds practical headroom on this 18 GiB host. Separate larger-memory machine required for representative testing. |
| qwen3:8b | Installed, digest prefix 500a1f067a9f | Current local comparison baseline, not a promoted production model. |

As of this check the official Qwen3.6 library lists 27B/35B, not a 9B tag;
choose verified registry tags rather than a guessed newer version. Gemma E2B/E4B
names describe effective parameters; their default artifacts are 7.2/9.6 GB,
so they are not automatically smaller downloads than the 12B quantized tag.
Do not extrapolate the largest family model's published benchmark to a small variant.

Sources: [Granite registry](https://ollama.com/library/granite4.2),
[IBM model card](https://huggingface.co/ibm-granite/granite-4.2-8b),
[Qwen3.5 registry](https://ollama.com/library/qwen3.5),
[Gemma4 registry](https://ollama.com/library/gemma4),
[Qwen3.6 registry](https://ollama.com/library/qwen3.6).

## Local protocol

Host inventory: 18 GiB unified/system memory, approximately 64 GiB available disk,
Ollama 0.30.10. Existing developer services remain running; memory pressure and
concurrent workloads therefore limit production latency conclusions. Native
Ollama, one request/model at a time; no new cloud instances or paid inference.

Use unchanged main generate/refine handlers through a temporary loopback test
harness, with disposable fixture-only PostgreSQL and the coded catalog. This
avoids the tracked automatic Compose model override and unrelated background
services. The unchanged Python evaluator supplies/scorers the 31 v1 requests.
The harness injects a synthetic authenticated tenant: it does not test login,
RLS policy installation, full API startup, real connector execution, Temporal
workers, agent tool calling or approvals. Those remain separate sandbox gates.

Record source/binary/corpus/prompt/schema hashes, model digest, Ollama version,
actual serving model, context4096, seed42, temperature0, thinking mode, repair
limit, request timeout and serial execution. Capture original failed responses,
not only final scores. A runtime compatibility failure is not an accuracy score.

The first comparison is a measured checkpoint. Model promotion requires two
full runs per candidate, the expanded promotion suite, intended-hardware latency,
and separate agent tool-use tests. Model switch and planner-contract changes
must be reviewed separately so attribution remains clear.

## Promotion gates

Schema >=99%, structure >=95%, activities/direction >=95%, grounding >=90%,
refinement preservation >=90%, correct clarification/rejection >=95%, at most
one repair per case, no invented secrets/connector IDs, no OOM or sustained swap.
Warm p95 <30 seconds is evaluated on intended deployment hardware. Always report
all-case pass count, HTTP failures and category coverage beside applicable rates.

## Executed local baseline

Qwen3:8b completed all 31 cases: **9 passed (29.03%)**, 12 HTTP422 failures,
11 ready responses, 4 needs-input and 4 rejected. There were 50 actual model
calls, all verified as qwen3:8b with the pinned settings. Three adversarial
passes were deterministic API guards without inference: this measures the
model-plus-handler system, not standalone model intelligence.

| Category | Qwen3 baseline |
| --- | --- |
| Generation | 2/8 |
| Refinement | 0/5 |
| Ambiguity | 2/4 |
| Branching | 0/3 |
| Engines/triggers | 0/6 |
| Adversarial | 5/5 |

API mean latency was 34.70s, p95 62.52s. A mid-run snapshot showed 5.6GB model
residency on GPU/context4096 and host swap usage 17567.81MiB. No pre-run swap
measurement exists; this cannot attribute swap to the model or establish a
controlled production latency/stability result. Schema-valid=100% in the report
excludes HTTP failures; do not present it as overall request reliability.

[Scored results](evals/qwen3-8b-baseline-report.json),
[diagnostics](evals/qwen3-8b-baseline-diagnostics.json), and
[provenance](evals/qwen3-8b-baseline-provenance.json).

## Benchmark defects confirmed before testing the challenger

An offline proof exercised the unchanged evaluator's request preparation and
strict preservation scorer, then checked one actual captured refinement prompt:

- Five refinement cases preserve six connector configs without mandatory
  connectionId. The outgoing request is unmodified. Adding the required IDs
  lowers preservation to 0.6667–0.9; per-case passing requires exactly 1.0.
- gen-postgres-snowflake requires config.mode=upsert, which the current coded
  grounding contract forbids.

At least six distinct cases are impossible under current API/scorer contracts;
the v1 theoretical maximum is at most 25/31. All six failed in this baseline.
The original 31-case report remains intact for comparison. Correct this in a
versioned corpus (G11) with golden positive outputs and consistent symbolic
fixture bindings, not by silently discarding failures or lowering expectations.
Historical August runs used a different commit, so this proof alone does not
establish the exact cause of every historical failure.

[Corpus-conflict evidence](evals/corpus-conflicts.json). The runnable offline
proof and raw synthetic API/model traces are retained with the local experiment.

Separately, main's prompt lists config names and required markers but omits
allowed types/enum values; config JSON schema is generic. Retained responses
show rejected format/layer/syncMode/pageSize guesses. Explicit prompt/schema
constraints are a concrete planner improvement to evaluate independently of weights.

## CI evidence

[PR38 tests](https://github.com/Cohestra/cohestra-dataflow/actions/runs/34389928849/job/102595433239)
passed community/enterprise race tests and vet; frontend and both-edition builds
also passed. Overall CI is not green:

- [History scan](https://github.com/Cohestra/cohestra-dataflow/actions/runs/34389928849/job/102595432908)
  reported two detections, also reported on exact baseline main's
  [security job](https://github.com/Cohestra/cohestra-dataflow/actions/runs/34274080469/job/102222660483).
  Logs do not disclose affected values or establish whether credentials are live.
- [Workflow image scan](https://github.com/Cohestra/cohestra-dataflow/actions/runs/34389928849/job/102595433788)
  reports four unique advisories: Thrift CVE-2026-43871, x/crypto CVE-2026-56854,
  x/net CVE-2026-46600, gRPC CVE-2026-84445 (three high, one critical).
  Dependencies are unchanged. Baseline already flagged the first three; an
  additional report on unchanged gRPC is consistent with scanner database
  freshness, not a newly introduced dependency. Exploitability is not established.

G10 tracks separate triage/remediation and rerunning skipped downstream security
checks; a documentation-only diff is not grounds to waive those failures.

### September14 HLD/LLD checkpoint

Builds and tests passed on the published HLD/LLD commits. The refreshed history
scan still failed (one detection in PR38's inspected job); the inspected backend
image scan reported the same four advisories, now all classified high. PR38's
image job passed while architecture-stack image jobs failed. No source dependency
change or security remediation was made, and differing job outcomes do not
establish that the historical findings are fixed. The [dated CI checkpoint](evals/ci-checkpoint-20260914.json)
records exact heads and job links; earlier counts above describe September9.

## Completed Granite comparison and decision

Granite4.2 8B completed the fresh 31-case run on September14 IST with **9/31
passes (29.03%)**, matching Qwen3:8b's raw count. This is a single current-stack
checkpoint, not a promotion-quality ranking: the corpus has contradictory cases
and an additional connectivity blind spot described below.

| Category / diagnostic | Qwen3:8b | Granite4.2:8b |
| --- | --- | --- |
| All cases | 9/31 | 9/31 |
| Generation | 2/8 | 0/8 |
| Refinement | 0/5 | 0/5 |
| Ambiguity | 2/4 | 4/4 |
| Branching | 0/3 | 0/3 |
| Engines/triggers | 0/6 | 0/6 |
| Adversarial | 5/5 | 5/5 |
| HTTP422 failures | 12 | 9 |
| Ready / needs-input / rejected responses | 11 / 4 / 4 | 2 / 17 / 3 |
| Actual model calls | 50 | 49 |
| Generated tokens | 17,165 | 23,137 |
| API mean / p95, diagnostic only | 34.70s / 62.52s | 44.56s / 85.72s |

Granite exchanged two generation passes for two clarification passes: seven
cases passed both models, twenty failed both, and each model alone passed two.
All Granite passes were ambiguity/adversarial cases; three were deterministic
API guards without inference. It did not improve executable pipeline generation
in this run. **Do not switch the default model or promote either candidate.**

Source, handler binary, corpus, first request apart from model, actual serving
model/digest, sampling/thinking settings and context4096 were verified. Granite
registry digest is f586c02fdecdf151b656207c339aa003997345774a41768bac1fd6d2fb85913b;
its official full weight SHA-256 is
16a9369d0805f80b7377d25d87f937a90c05dc04ad79173a52001e42c9aab311. All 49 model calls
returned HTTP200; nine final API failures came from planner validation, with zero
fixture-infrastructure failures in the completed retry. All model stop reasons
were stop, with no explicit context/truncation error; server truncation logs were
not captured. Token counts alone do not establish truncation.

The failed native model download was recovered over IPv4, verified, then
registered through Ollama normally. The first evaluation attempt lost Docker
after one inference: 27 later model-dependent cases failed DB connections and only
three deterministic guards passed. Its raw 3/31 is preserved as an invalid
infrastructure-interrupted attempt, never included as Granite accuracy.

The completed retry used a temporary native PostgreSQL 17.10 cluster on
loopback 15432 (12 synthetic fixtures, 32 MiB shared buffers, 10 connections). Qwen used
a Docker PostgreSQL 16 fixture. Application inputs stayed fixed, but database
deployment, run date and host workloads differ. Native-run swap rose from
10,983.62 to 15,096.06 MiB; the loaded model occupied 5.88 GB on GPU. These observations
do not attribute swap to Granite or establish controlled latency/stability. The
harness bypasses auth and workflow execution, and this suite does not test agent
tool calls or execute the generated pipelines.

A separate offline proof retained the first actual response, whose missing edges
serialized as null and failed the scorer. Changing only that copied field to an
empty array made the same disconnected source/filter/sink graph pass. The actual
report is unchanged. Corrected v2 cases need explicit expected path assertions
alongside consistent fixture bindings and golden positive answers; fixing null
serialization alone would hide the semantic failure. Preserve legitimate
independent branches rather than banning every disconnected graph globally.

[Granite report](evals/granite42-8b-report.json),
[diagnostics](evals/granite42-8b-diagnostics.json),
[paired cases](evals/granite42-8b-comparison.json),
[provenance](evals/granite42-8b-provenance.json),
[native DB provenance](evals/granite42-native-db-provenance.json),
[runtime model metadata](evals/granite42-runtime-metadata.json),
[invalid-attempt diagnostics](evals/granite42-invalid-attempt-diagnostics.json),
and [connectivity scorer proof](evals/empty-edge-scorer-proof.json).
The post-run model-details endpoint reports tools/thinking/completion while the
tag-list endpoint reports completion only for the same digest. These are metadata
observations, not evidence that agent tool calling passed a test.
Raw synthetic traces and runnable comparison/proof scripts remain in the durable
local experiment folder.

Next model work should correct and preflight v2, evaluate explicit planner
config/path constraints with fixed weights, then compare candidates with repeated
runs on intended hardware. Qwen3.5:9b remains an untested secondary candidate;
latest availability alone is not evidence it is better for Dataflow. Native agent
tool-use acceptance remains separate from planner accuracy. LangGraph is not
needed to fix either the corpus or the native runtime design.
