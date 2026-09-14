#!/usr/bin/env python3
"""Run synthetic backend tests against owned, disposable native services."""

import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import signal
import socket
import subprocess
import sys
import tarfile
import tempfile
import time

REPO = Path(__file__).resolve().parents[1]
CLI_VERSION = "1.8.3"
ARCHIVES = {
    ("Darwin", "arm64"): ("darwin_arm64", "77c5bef1753ddfcdcaced2a2d44207aeced1c776e7bcbf94520c7911bd0c4080"),
    ("Linux", "x86_64"): ("linux_amd64", "6f0afac1e9ddea71f480c43a49f5db5167a244c21db923707f069a79bcabdfea"),
}


def timestamp():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def stop(process):
    if process.poll() is not None:
        return
    try:
        os.killpg(process.pid, signal.SIGTERM)
        process.wait(timeout=10)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        process.wait(timeout=5)
    except ProcessLookupError:
        process.wait(timeout=5)


def test_results(path):
    """A package pass is insufficient: at least one leaf test must really pass."""
    tests = {}
    for line in path.read_text().splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        name = event.get("Test", "")
        if name.startswith("TestTemporalSandbox"):
            tests.setdefault(name, "unfinished")
            if event.get("Action") in ("pass", "fail", "skip"):
                tests[name] = event["Action"]
    leaves = {name: state for name, state in tests.items()
              if not any(other.startswith(name + "/") for other in tests)}
    valid = bool(leaves) and all(state == "pass" for state in tests.values())
    return {"tests": tests, "leafTests": leaves, "valid": valid}


def self_check():
    cases = [
        ("no tests", [{"Action": "pass"}], False),
        ("all skipped", [{"Test": "TestTemporalSandboxA", "Action": "skip"}], False),
        ("skipped child", [{"Test": "TestTemporalSandboxA/child", "Action": "skip"}, {"Test": "TestTemporalSandboxA", "Action": "pass"}], False),
        ("mixed skip", [{"Test": "TestTemporalSandboxA", "Action": "pass"}, {"Test": "TestTemporalSandboxB", "Action": "skip"}], False),
        ("passed", [{"Test": "TestTemporalSandboxA/child", "Action": "pass"}, {"Test": "TestTemporalSandboxA", "Action": "pass"}], True),
        ("failed", [{"Test": "TestTemporalSandboxA", "Action": "fail"}], False),
        ("unfinished", [{"Test": "TestTemporalSandboxA", "Action": "run"}], False),
    ]
    with tempfile.TemporaryDirectory(prefix="cohestra-parser-") as directory:
        path = Path(directory) / "events.jsonl"
        for name, events, expected in cases:
            path.write_text("".join(json.dumps(event) + "\n" for event in events))
            if test_results(path)["valid"] != expected:
                raise RuntimeError(f"Test event parser regression: {name}")
    print(f"{len(cases)} test event parser checks passed")
    return 0


class Sandbox:
    def __init__(self, artifacts):
        self.artifacts = artifacts
        self.processes = []
        self.deadline = time.monotonic() + 18 * 60
        self.summary = {"startedAt": timestamp(), "status": "running", "commands": [], "editions": [], "versions": {}}
        # CLI profiles and preexisting database connection settings must not
        # redirect the owned runtime or inject personal credentials into tests.
        self.env = {key: value for key, value in os.environ.items()
                    if not key.startswith(("TEMPORAL_", "PG"))}

    def launch(self, argv, log, cwd=REPO, env=None):
        log = self.artifacts / log
        log.parent.mkdir(parents=True, exist_ok=True)
        handle = log.open("w")
        record = {"argv": [str(arg) for arg in argv], "cwd": str(cwd), "log": str(log.relative_to(self.artifacts)), "startedAt": timestamp()}
        self.summary["commands"].append(record)
        try:
            process = subprocess.Popen(record["argv"], cwd=cwd, env=env or self.env,
                                       stdout=handle, stderr=subprocess.STDOUT, start_new_session=True)
        except BaseException:
            handle.close()
            raise
        self.processes.append((process, handle, record))
        return process, record, log

    def run(self, argv, log, timeout=60, check=True, cwd=REPO, env=None):
        timeout = min(timeout, self.deadline - time.monotonic())
        if timeout <= 0:
            raise RuntimeError("Sandbox exceeded its 18-minute runtime budget")
        process, record, path = self.launch(argv, log, cwd, env)
        try:
            process.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            record["timedOut"] = True
            stop(process)
        finally:
            record["exitCode"] = process.poll()
            record["finishedAt"] = timestamp()
        if check and (record.get("timedOut") or process.returncode != 0):
            raise RuntimeError(f"Command failed; see {path}")
        return process.returncode, path

    def ready(self, process, argv, label):
        deadline = time.monotonic() + 60
        attempt = 0
        while time.monotonic() < deadline:
            if process.poll() is not None:
                raise RuntimeError(f"{label} exited during startup; inspect its log")
            attempt += 1
            code, _ = self.run(argv, f"readiness/{label}-{attempt}.log", timeout=5, check=False)
            if code == 0:
                return
            time.sleep(0.3)
        raise RuntimeError(f"{label} did not become ready within 60 seconds")

    def cleanup(self):
        errors = []
        for process, handle, record in reversed(self.processes):
            try:
                stop(process)
            except Exception as error:
                errors.append(str(error))
            record["exitCode"] = process.poll()
            record.setdefault("finishedAt", timestamp())
            handle.close()
        return errors


def temporal_binary(sandbox, runtime, provided):
    if provided:
        binary = Path(provided).expanduser().resolve(strict=True)
        sandbox.summary["temporalBinarySource"] = "explicit local binary; exact version checked"
    else:
        target = ARCHIVES.get((platform.system(), platform.machine()))
        if target is None:
            raise RuntimeError("Supported downloads are macOS arm64 and Linux amd64; use an explicit Temporal CLI 1.8.3 binary on another POSIX host")
        suffix, expected = target
        url = f"https://github.com/temporalio/cli/releases/download/v{CLI_VERSION}/temporal_cli_{CLI_VERSION}_{suffix}.tar.gz"
        archive = runtime / "temporal.tar.gz"
        sandbox.run(["curl", "--fail", "--location", "--silent", "--show-error", "--max-time", "180", "--output", archive, url], "download.log", timeout=190)
        digest = hashlib.sha256()
        with archive.open("rb") as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                digest.update(chunk)
        if digest.hexdigest() != expected:
            raise RuntimeError("Temporal CLI archive checksum mismatch; no files extracted")
        binary = runtime / "temporal"
        with tarfile.open(archive, "r:gz") as bundle:
            members = [member for member in bundle.getmembers() if member.name == "temporal"]
            if len(members) != 1 or not members[0].isfile():
                raise RuntimeError("Archive must contain exactly one regular temporal executable")
            with bundle.extractfile(members[0]) as source, binary.open("xb") as destination:
                shutil.copyfileobj(source, destination)
        binary.chmod(0o700)
        sandbox.summary["temporalBinarySource"] = {"url": url, "sha256": expected}
    _, output = sandbox.run([binary, "--disable-config-env", "--disable-config-file", "--version"], "temporal-version.log")
    version = output.read_text().strip()
    if not re.search(r"\bversion\s+1\.8\.3\b", version):
        raise RuntimeError(f"Expected Temporal CLI {CLI_VERSION}; see {output}")
    sandbox.summary["versions"]["temporal"] = version
    return binary


def free_ports(count):
    reservations = []
    try:
        for _ in range(count):
            sock = socket.socket()
            sock.bind(("127.0.0.1", 0))
            reservations.append(sock)
        return [sock.getsockname()[1] for sock in reservations]
    finally:
        for sock in reservations:
            sock.close()


def run_sandbox(sandbox, runtime, args):
    if os.geteuid() == 0:
        raise RuntimeError("Run as an ordinary user: initdb refuses root")
    _, source = sandbox.run(["git", "rev-parse", "HEAD"], "source-revision.log")
    sandbox.summary["sourceRevision"] = source.read_text().strip()
    _, changes = sandbox.run(["git", "status", "--porcelain", "--untracked-files=no"], "source-status.log")
    sandbox.summary["trackedChanges"] = bool(changes.read_text().strip())
    temporal = temporal_binary(sandbox, runtime, args.temporal_bin)
    pg_bin = os.environ.get("PG_BIN")
    if not pg_bin:
        _, output = sandbox.run(["pg_config", "--bindir"], "pg-bindir.log")
        pg_bin = output.read_text().strip()
    pg = Path(pg_bin).expanduser().resolve()
    for name in ("initdb", "postgres", "pg_isready", "createdb", "psql"):
        if not (pg / name).is_file():
            raise RuntimeError(f"Missing PostgreSQL executable: {pg / name}")
    for name, argv in (("postgres", [pg / "postgres", "--version"]), ("go", ["go", "version"])):
        _, output = sandbox.run(argv, f"{name}-version.log")
        sandbox.summary["versions"][name] = output.read_text().strip()
    pg_port, temporal_port = free_ports(2)
    sandbox.summary["ports"] = {"postgres": pg_port, "temporal": temporal_port}
    data, sockets = runtime / "pgdata", runtime / "sockets"
    sockets.mkdir(mode=0o700)
    sandbox.run([pg / "initdb", "-D", data, "--username=dataflow", "--auth-local=trust", "--auth-host=trust", "--no-locale", "--encoding=UTF8"], "initdb.log")
    (data / "pg_hba.conf").write_text("local all all trust\nhost all all 127.0.0.1/32 trust\n")
    postgres, _, _ = sandbox.launch([pg / "postgres", "-D", data, "-h", "127.0.0.1", "-p", str(pg_port), "-k", sockets, "-c", "shared_buffers=32MB", "-c", "max_connections=30"], "postgres.log")
    connection = ["-h", "127.0.0.1", "-p", str(pg_port), "-U", "dataflow"]
    sandbox.ready(postgres, [pg / "pg_isready", *connection, "-d", "postgres"], "postgres")
    sandbox.run([pg / "createdb", *connection, "temporal_sandbox"], "createdb.log")
    migrations = sorted(path for path in (REPO / "db").iterdir() if re.fullmatch(r"[0-9]{3}_.+\.sql", path.name))
    if not migrations:
        raise RuntimeError("No numeric PostgreSQL migrations found")
    sandbox.summary["migrations"] = [path.name for path in migrations]
    for migration in migrations:
        sandbox.run([pg / "psql", *connection, "-d", "temporal_sandbox", "-X", "-v", "ON_ERROR_STOP=1", "-f", migration], f"migrations/{migration.stem}.log")
    cli = [temporal, "--disable-config-env", "--disable-config-file"]
    address = f"127.0.0.1:{temporal_port}"
    server, _, _ = sandbox.launch([*cli, "server", "start-dev", "--ip", "127.0.0.1", "--port", str(temporal_port), "--namespace", "test", "--headless", "--db-filename", runtime / "temporal.sqlite", "--log-level", "warn"], "temporal.log")
    sandbox.ready(server, [*cli, "operator", "cluster", "health", "--address", address, "--command-timeout", "3s"], "temporal")
    sandbox.ready(server, [*cli, "operator", "namespace", "describe", "--namespace", "test", "--address", address, "--command-timeout", "3s"], "namespace")
    for edition in ("community", "ee"):
        print(f"Running {edition} Temporal sandbox tests…", flush=True)
        edition_dir = sandbox.artifacts / edition
        edition_dir.mkdir()
        env = {**sandbox.env,
               "CONTROL_TEST_DATABASE_URL": f"postgresql://dataflow@127.0.0.1:{pg_port}/temporal_sandbox?sslmode=disable",
               "SANDBOX_TEMPORAL_ADDRESS": address, "SANDBOX_TEMPORAL_NAMESPACE": "test",
               "SANDBOX_ARTIFACT_DIR": str(edition_dir)}
        command = ["go", "test", "-race", "-p", "2", "./internal/api", "-run", "^TestTemporalSandbox", "-count=1", "-timeout=6m", "-json"]
        if edition == "ee":
            command += ["-tags=ee"]
        code, output = sandbox.run(command, f"{edition}/go-test.jsonl", timeout=420, check=False, cwd=REPO / "apps/workflow-go", env=env)
        result = {"edition": edition, "exitCode": code, **test_results(output)}
        sandbox.summary["editions"].append(result)
    if any(item["exitCode"] != 0 or not item["valid"] for item in sandbox.summary["editions"]):
        raise RuntimeError("One or more editions failed, ran no tests, or skipped tests; inspect go-test.jsonl")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--self-test", action="store_true", help="Check Go event parsing without starting services")
    parser.add_argument("--artifacts", type=Path, help="New or empty artifact directory inside this repository or a system temporary directory")
    parser.add_argument("--temporal-bin", help="Explicit existing Temporal CLI binary; must report version 1.8.3")
    args = parser.parse_args()
    if args.self_test:
        return self_check()
    if args.artifacts is None:
        parser.error("--artifacts is required unless --self-test is used")
    artifacts = args.artifacts.expanduser().resolve()
    roots = (REPO, Path(tempfile.gettempdir()).resolve(), Path("/tmp").resolve())
    if not any(artifacts.is_relative_to(root) and artifacts != root for root in roots):
        parser.error("--artifacts must be below the repository or a system temporary directory")
    if artifacts.exists() and (not artifacts.is_dir() or any(artifacts.iterdir())):
        parser.error("--artifacts must be new or empty; existing evidence is never overwritten")
    artifacts.mkdir(parents=True, exist_ok=True)
    sandbox = Sandbox(artifacts)
    runtime = tempfile.TemporaryDirectory(prefix="cohestra-ts-", dir="/tmp")
    sandbox.summary["runtimeDirectory"] = runtime.name
    previous = {}
    def interrupted(signum, _frame):
        raise KeyboardInterrupt(f"Received signal {signum}")
    for signum in (signal.SIGTERM, signal.SIGINT):
        previous[signum] = signal.signal(signum, interrupted)
    code = 1
    try:
        run_sandbox(sandbox, Path(runtime.name), args)
        sandbox.summary["status"] = "passed"
        code = 0
    except (Exception, KeyboardInterrupt) as error:
        sandbox.summary.update(status="failed", error=str(error))
        print(f"Sandbox failed: {error}", file=sys.stderr)
    finally:
        for signum in previous:
            signal.signal(signum, signal.SIG_IGN)
        cleanup_errors = sandbox.cleanup()
        if not cleanup_errors:
            try:
                runtime.cleanup()
            except Exception as error:
                cleanup_errors.append(str(error))
        sandbox.summary["cleanupErrors"] = cleanup_errors
        if cleanup_errors:
            sandbox.summary["status"] = "failed"
            code = 1
        sandbox.summary["finishedAt"] = timestamp()
        (artifacts / "summary.json").write_text(json.dumps(sandbox.summary, indent=2) + "\n")
        for signum, handler in previous.items():
            signal.signal(signum, handler)
    print(f"Evidence: {artifacts / 'summary.json'}", flush=True)
    return code


if __name__ == "__main__":
    sys.exit(main())
