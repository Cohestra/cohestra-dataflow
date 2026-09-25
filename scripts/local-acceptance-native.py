#!/usr/bin/env python3
"""Foreground local application supervisor; isolated Compose owns infrastructure."""
import fcntl
import importlib.util
import json
import os
from pathlib import Path
import shlex
import signal
import subprocess
import sys
import time

SCRIPT = Path(__file__).resolve()
ROOT = SCRIPT.parents[1]
ARTIFACTS = ROOT / ".artifacts/local-acceptance/native"
PIDFILE = ARTIFACTS / "supervisor.pid"
BASE_ENV = {key: value for key, value in os.environ.items() if key in ("PATH", "HOME", "TMPDIR", "USER")}


def identity(pid):
    result = {}
    for key, column in (("command", "command="), ("started", "lstart=")):
        process = subprocess.run(["ps", "-p", str(pid), "-o", column], capture_output=True, text=True, env=BASE_ENV)
        if process.returncode:
            return None
        result[key] = process.stdout.strip()
    return result


def owned(record, current):
    return bool(current and record["identity"] == current and str(SCRIPT) in shlex.split(current["command"]))


def stop():
    try:
        record = json.loads(PIDFILE.read_text())
    except FileNotFoundError:
        print("Local supervisor is already stopped.")
        return 0
    current = identity(record["pid"])
    if current is None:
        print("Local supervisor is already stopped.")
        return 0
    if not owned(record, current):
        raise RuntimeError("Supervisor identity does not match; refusing to signal PID")
    try:
        os.kill(record["pid"], signal.SIGTERM)
    except ProcessLookupError:
        pass
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        if not owned(record, identity(record["pid"])):
            print("Local supervisor stopped after cleaning up its children.")
            return 0
        time.sleep(0.2)
    raise RuntimeError("Supervisor did not finish cleanup within 30 seconds; infrastructure must remain running")


def serve():
    os.umask(0o077)
    ARTIFACTS.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(ARTIFACTS, 0o700)
    lock = PIDFILE.open("a+")
    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    lock.seek(0)
    lock.truncate()
    json.dump({"pid": os.getpid(), "identity": identity(os.getpid())}, lock)
    lock.flush()
    sys.dont_write_bytecode = True
    spec = importlib.util.spec_from_file_location("temporal_sandbox", ROOT / "scripts/temporal-sandbox.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    sandbox = module.Sandbox(ARTIFACTS)
    sandbox.env = BASE_ENV.copy()
    sandbox.deadline = time.monotonic() + 90
    sandbox.summary.update(status="starting", supervisorPid=os.getpid(), api="http://127.0.0.1:14000", web="http://localhost:13002")

    def save():
        (ARTIFACTS / "run.json").write_text(json.dumps(sandbox.summary, indent=2) + "\n")

    def interrupted(signum, frame):
        raise KeyboardInterrupt()

    for signum in (signal.SIGINT, signal.SIGTERM):
        signal.signal(signum, interrupted)
    code = 1
    try:
        config = subprocess.run([
            "docker", "compose", "-p", os.environ.get("COHESTRA_ACCEPTANCE_PROJECT", "cohestra-acceptance-20260922"), "-f", "docker-compose.yml",
            "-f", "docker-compose.acceptance.yml", "--profile", "container-app", "config", "--format", "json"
        ], cwd=ROOT, env=BASE_ENV, capture_output=True, text=True, timeout=20)
        if config.returncode:
            raise RuntimeError("Could not read local Compose configuration (output withheld because it may contain secrets)")
        env = {**BASE_ENV, **{key: str(value) for key, value in json.loads(config.stdout)["services"]["api"]["environment"].items() if value is not None}}
        del config
        for key in list(env):
            if key.startswith(("GOOGLE_", "AZURE_", "ZENDESK_", "RAZORPAY_", "OPENLINEAGE_", "PAYLOAD_S3_", "SMTP_")):
                env[key] = ""
        env.update(
            DATABASE_URL="postgres://dataflow:dataflow@127.0.0.1:15433/dataflow?sslmode=disable",
            APP_DATABASE_URL="postgres://dataflow_app:dataflow_app@127.0.0.1:15433/dataflow?sslmode=disable",
            REDIS_URL="redis://127.0.0.1:16379", TEMPORAL_ADDRESS="127.0.0.1:17233",
            CLICKHOUSE_URL="http://127.0.0.1:18123", OLLAMA_URL=os.environ.get("ACCEPTANCE_OLLAMA_URL", "http://127.0.0.1:11434"),
            OLLAMA_MODEL=os.environ.get("ACCEPTANCE_OLLAMA_MODEL", "qwen3:8b"),
            API_PORT="14000", API_HOST="127.0.0.1", APP_URL="http://localhost:13002",
            CONNECTORS_DIR=str(ROOT / "connectors/manifests"), WORKER_PRIVATE_KEY_PATH=str(ROOT / "secrets/worker-keypair.pem"),
            COHESTRA_URL="http://127.0.0.1:1", SMTP_HOST="127.0.0.1", SMTP_PORT="1",
            OTEL_EXPORTER_OTLP_ENDPOINT="http://127.0.0.1:1", KAFKA_CONNECT_URL="http://127.0.0.1:1", KAFKA_BROKERS="127.0.0.1:1")
        binaries = ROOT / ".artifacts/local-acceptance/bin"
        children = []
        for binary, queue in (("activity-worker", "dynamic-activities"), ("worker", "dynamic-dag")):
            for namespace in ("test", "prod"):
                process, _, _ = sandbox.launch([binaries / binary], f"{binary}-{namespace}.log", cwd=ROOT,
                                               env={**env, "TEMPORAL_NAMESPACE": namespace, "TASK_QUEUE": f"{queue}-{namespace}"})
                children.append(process)
        api, _, _ = sandbox.launch([binaries / "api"], "api.log", cwd=ROOT, env=env)
        web, _, _ = sandbox.launch(["node", ROOT / "node_modules/vite/bin/vite.js", "preview", "--config", "vite.acceptance.config.ts"],
                                   "web.log", cwd=ROOT / "apps/web", env=BASE_ENV)
        children.extend((api, web))
        save()
        for process, url, label in ((api, "http://127.0.0.1:14000/health", "api"), (web, "http://localhost:13002", "web")):
            sandbox.ready(process, ["curl", "--fail", "--silent", "--show-error", "--max-time", "2", url], label)
            if any(child.poll() is not None for child in children):
                raise RuntimeError("An owned application process exited during startup; inspect native logs")
        sandbox.summary["status"] = "ready"
        save()
        print("Local acceptance ready: http://localhost:13002 (API http://127.0.0.1:14000)", flush=True)
        while True:
            if any(process.poll() is not None for process in children):
                raise RuntimeError("An owned application process exited; inspect native logs")
            time.sleep(1)
    except KeyboardInterrupt:
        sandbox.summary["status"] = "stopped"
        code = 0
    except Exception as error:
        sandbox.summary.update(status="failed", error=str(error))
        print(f"Local supervisor failed: {error}", file=sys.stderr)
    finally:
        for signum in (signal.SIGINT, signal.SIGTERM):
            signal.signal(signum, signal.SIG_IGN)
        sandbox.summary["cleanupErrors"] = sandbox.cleanup()
        if sandbox.summary["cleanupErrors"]:
            sandbox.summary["status"], code = "failed", 1
        sandbox.summary["finishedAt"] = module.timestamp()
        save()
        PIDFILE.unlink(missing_ok=True)
        lock.close()
    return code


if __name__ == "__main__":
    command = sys.argv[1] if len(sys.argv) == 2 else "serve" if len(sys.argv) == 1 else ""
    if command == "--self-test":
        from unittest.mock import Mock, patch
        valid = {"command": f"python3 {shlex.quote(str(SCRIPT))} serve", "started": "now"}
        assert owned({"identity": valid}, valid)
        assert not owned({"identity": valid}, None)
        assert not owned({"identity": valid}, {**valid, "started": "later"})
        other = {"command": "python3 /tmp/unrelated.py", "started": "now"}
        assert not owned({"identity": other}, other)
        with patch.object(PIDFILE.__class__, "read_text", return_value=json.dumps({"pid": 123, "identity": valid})), \
                patch("os.kill") as kill, patch("time.sleep"), patch("builtins.print"):
            with patch.dict(globals(), identity=Mock(side_effect=[valid, valid, None])):
                assert stop() == 0
                kill.assert_called_once_with(123, signal.SIGTERM)
            kill.reset_mock()
            with patch.dict(globals(), identity=Mock(return_value=None)):
                assert stop() == 0
                kill.assert_not_called()
            with patch.dict(globals(), identity=Mock(return_value=valid)), patch("time.monotonic", side_effect=[0, 31]):
                try:
                    stop()
                    raise AssertionError("cleanup timeout must fail")
                except RuntimeError as error:
                    assert "30 seconds" in str(error)
        print("Supervisor identity, cleanup wait, idempotent stop and timeout checks passed")
    elif command in ("serve", "stop"):
        if command == "serve" and sys.argv[0] != str(SCRIPT):
            os.execv(sys.executable, [sys.executable, str(SCRIPT), "serve"])
        sys.exit(serve() if command == "serve" else stop())
    else:
        sys.exit("Usage: local-acceptance-native.py [serve|stop|--self-test]")
