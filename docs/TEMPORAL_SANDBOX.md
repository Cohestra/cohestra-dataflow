# Native Temporal sandbox

Run the backend sandbox against a new local PostgreSQL cluster and a real Temporal development server:

```sh
python3 scripts/temporal-sandbox.py --artifacts /tmp/cohestra-temporal-evidence
```

Use a new or empty artifact directory below the repository or system temporary directory. Evidence is retained there on success or failure; an existing nonempty directory is rejected to avoid overwriting a previous run.

## Prerequisites and isolation

The runner requires Python 3.10+, Go from `apps/workflow-go/go.mod` with race-detector/C compiler support, curl, and native PostgreSQL binaries. Run as an ordinary user. It discovers PostgreSQL through `pg_config --bindir`; set `PG_BIN` explicitly if necessary:

```sh
PG_BIN=/usr/lib/postgresql/16/bin python3 scripts/temporal-sandbox.py --artifacts /tmp/cohestra-temporal-evidence
```

CI installs PostgreSQL 16. Other locally installed versions are recorded in the run summary and must successfully apply the migrations; this is not a promise of a tested compatibility matrix. No global install, Docker daemon, personal database or existing Temporal server is used by the runner.

Each invocation owns a temporary runtime directory, two independently selected free ports, a fresh PostgreSQL cluster/database and a separate Temporal SQLite store. PostgreSQL listens on `127.0.0.1`, with trust authentication limited to loopback and its private Unix socket directory. Temporal runs headless on `127.0.0.1` and creates the `test` namespace within that unique server. All numbered PostgreSQL migrations run with `ON_ERROR_STOP` before tests. Personal PostgreSQL/Temporal connection environment and CLI profiles cannot redirect the runtime; the test environment is constructed from the new endpoints.

The default download is [Temporal CLI 1.8.3](https://github.com/temporalio/cli/releases/tag/v1.8.3), verified against these archive SHA-256 values **before** extracting only the regular `temporal` executable into the runtime directory:

| Archive | SHA-256 |
| --- | --- |
| `temporal_cli_1.8.3_darwin_arm64.tar.gz` | `77c5bef1753ddfcdcaced2a2d44207aeced1c776e7bcbf94520c7911bd0c4080` |
| `temporal_cli_1.8.3_linux_amd64.tar.gz` | `6f0afac1e9ddea71f480c43a49f5db5167a244c21db923707f069a79bcabdfea` |

An explicit existing CLI is supported with `--temporal-bin /absolute/path/to/temporal`; its reported version must be exactly 1.8.3. That path is never modified or deleted. This option checks the version rather than authenticating a user-supplied binary with the archive checksum. Supported download hosts are macOS arm64 and Linux amd64; other POSIX hosts need a compatible explicit binary.

## Test contract and evidence

The runner sets `CONTROL_TEST_DATABASE_URL`, `SANDBOX_TEMPORAL_ADDRESS`, `SANDBOX_TEMPORAL_NAMESPACE=test`, and a separate `SANDBOX_ARTIFACT_DIR` for each edition. It runs these tests serially inside `apps/workflow-go`:

```sh
go test -race -p 2 ./internal/api -run '^TestTemporalSandbox' -count=1 -timeout=6m -json
go test -race -p 2 ./internal/api -run '^TestTemporalSandbox' -count=1 -timeout=6m -json -tags=ee
```

The scenarios use real API handlers, PostgreSQL, Temporal workers and history, with deterministic synthetic connector handlers and explicitly injected synthetic tenant authentication. They cover persisted data output, retries/timeouts, pause and worker restart, cancellation and unsupported-agent denial. They do not verify interactive sign-in, an external provider or Ollama, the full UI stack, production Temporal deployment, or completed agent functionality.

`summary.json` records commands, versions, applied migrations, exit status and test outcomes. Each edition retains its Go JSON event log and the test-generated synthetic history/summary artifacts. A successful Go package exit alone is insufficient: zero matching tests, any skipped tests, failed tests, or unfinished tests make the runner fail. The result parser has seven stdlib self-check cases (no tests, all-skipped, skipped child, mixed skip/pass, pass, failure and unfinished): run `python3 scripts/temporal-sandbox.py --self-test`. CI executes these checks before starting services. No environment dumps or personal credentials belong in these artifacts.

Startup cluster/namespace health checks, command timeouts and cleanup are bounded. Each edition has a six-minute Go test timeout and a seven-minute process limit; the runner enforces an overall 18-minute runtime budget. PostgreSQL uses 32 MiB shared buffers and at most 30 connections. On success, failure, SIGINT or SIGTERM, the runner terminates and waits for its child process groups and removes its temporary runtime while retaining evidence. Cleanup errors fail the run and are recorded in the summary. As with any process, forced OS termination such as SIGKILL cannot execute a cleanup handler.

## CI gate

`.github/workflows/temporal-sandbox.yml` runs both editions on relevant Go/backend, migration, contract-fixture or runner changes. Documentation-only changes retain the same successful job name without starting services. Manual dispatch forces a run; newer runs cancel superseded runs. The job has a 20-minute limit and uploads available synthetic evidence even when verification fails.

This sandbox is one integration gate in the agent plan. Passing it does not complete the separate browser accessibility, live-model accuracy, provider integration, approval/tool mutation or production rollout gates.
