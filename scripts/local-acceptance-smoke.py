#!/usr/bin/env python3
"""Real, local Compose acceptance. Requires the acceptance stack already running."""
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid


ROOT = Path(__file__).resolve().parents[1]
ARTIFACTS = ROOT / ".artifacts/local-acceptance"
BASE = "http://127.0.0.1:14000"
PROJECT = os.environ.get("COHESTRA_ACCEPTANCE_PROJECT", "cohestra-acceptance-20260922")
SENSITIVE = []
RESULT = {"status": "running", "api": BASE, "project": PROJECT,
          "credentialsFile": str(ARTIFACTS / "credentials.json"),
          "checks": [], "evidence": {}}


def save():
    text = json.dumps(RESULT, indent=2, default=str)
    for value in SENSITIVE:
        text = text.replace(value, "[redacted]")
    (ARTIFACTS / "smoke.json").write_text(text + "\n")


def check(name, passed, detail=None):
    entry = {"name": name, "passed": bool(passed)}
    if detail is not None:
        entry["detail"] = detail
    RESULT["checks"].append(entry)
    save()
    print(("PASS " if passed else "FAIL ") + name, flush=True)


def request(opener, method, path, body=None, token=None):
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(BASE + path, method=method, headers=headers,
                                 data=None if body is None else json.dumps(body).encode())
    try:
        response = opener.open(req, timeout=10)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        raw = response.read()
        try:
            data = json.loads(raw) if raw else None
        except ValueError:
            data = {"error": raw.decode(errors="replace")[:2000]}
        return response.code, data


def require(opener, method, path, body=None, token=None):
    status, data = request(opener, method, path, body, token)
    if not 200 <= status < 300:
        raise RuntimeError(f"{method} {path}: HTTP {status}: {json.dumps(data)}")
    return data


def authenticate(filename, suffix):
    path = ARTIFACTS / filename
    if path.exists():
        os.chmod(path, 0o600)
        account = json.loads(path.read_text())
    else:
        account = {"email": f"acceptance-{suffix}-{uuid.uuid4().hex[:10]}@local.test",
                   "password": secrets.token_urlsafe(24)}
        with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "w") as file:
            json.dump(account, file)
            file.write("\n")
    SENSITIVE.append(account["password"])
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    status, _ = request(opener, "POST", "/api/auth/register",
                        dict(account, tenantName="Local acceptance " + suffix))
    if status == 409:
        require(opener, "POST", "/api/auth/login", account)
    elif status != 201:
        raise RuntimeError(f"Local account registration returned HTTP {status}")
    auth = require(opener, "POST", "/api/auth/refresh")
    SENSITIVE.append(auth["accessToken"])
    return opener, auth["accessToken"]


def main():
    ARTIFACTS.mkdir(parents=True, exist_ok=True)
    os.chmod(ARTIFACTS, 0o700)
    ignored = subprocess.run(["git", "check-ignore", "-q", ".artifacts/local-acceptance/credentials.json"],
                             cwd=ROOT, capture_output=True)
    if ignored.returncode:
        raise RuntimeError("Ignore /.artifacts/ before generating local credentials")
    save()
    try:
        sql = """BEGIN;
CREATE SCHEMA IF NOT EXISTS local_acceptance;
CREATE TABLE IF NOT EXISTS local_acceptance.source_records
  (id integer PRIMARY KEY, name text NOT NULL, active boolean NOT NULL);
TRUNCATE local_acceptance.source_records;
INSERT INTO local_acceptance.source_records VALUES
  (1, 'Alpha', true), (2, 'Beta', false), (3, 'Gamma', true);
COMMIT;
"""
        seeded = subprocess.run([
            "docker", "compose", "-p", PROJECT,
            "-f", "docker-compose.yml", "-f", "docker-compose.acceptance.yml",
            "exec", "-T", "postgres", "psql", "-v", "ON_ERROR_STOP=1", "-U", "dataflow", "-d", "dataflow"
        ], cwd=ROOT, input=sql, text=True, capture_output=True, timeout=30)
        check("isolated PostgreSQL fixture seeded", seeded.returncode == 0,
              {"schema": "local_acceptance", "rows": 3, "stderr": seeded.stderr[-2000:]})
        if seeded.returncode:
            raise RuntimeError("Fixture seed failed")
        opener, token = authenticate("credentials.json", "primary")
        check("password registration/login and cookie refresh", True)
        connector = require(opener, "POST", "/api/connectors", {
            "provider": "postgres", "name": "Local acceptance " + uuid.uuid4().hex[:10],
            "config": {"host": "127.0.0.1", "port": 15433, "database": "dataflow", "user": "dataflow", "sslMode": "disable"},
            "secret": {"password": "dataflow"}}, token)
        RESULT["connectorId"] = connector["id"]
        collection = "local_acceptance_" + uuid.uuid4().hex[:12]
        definition = {
            "id": str(uuid.uuid4()), "name": "Local acceptance records", "trigger": {"type": "manual"},
            "metadata": {"owner": "Local acceptance", "domain": "Testing", "tags": ["local", "acceptance"]},
            "slo": {"freshnessMinutes": 60, "maxFailureRatePercent": 5, "maxDurationMs": 30000},
            "concurrency": {"maxParallelNodes": 2}, "execution": {"engine": "workflow"},
            "nodes": [
                {"id": "src", "type": "source", "activityType": "postgres.fetch", "label": "Local PostgreSQL",
                 "config": {"connectionId": connector["id"], "table": "local_acceptance.source_records", "syncMode": "cursor",
                            "columns": "id,name,active", "cursorColumn": "id", "cursorType": "number"},
                 "ingestion": {"mode": "incremental", "pageSize": 100}},
                {"id": "fil", "type": "transform", "activityType": "transform.filter", "label": "Active records",
                 "config": {"predicate": "r.active === true"}},
                {"id": "snk", "type": "sink", "activityType": "sink.records", "label": "Managed records",
                 "config": {"collection": collection}}],
            "edges": [{"id": "e1", "source": "src", "target": "fil"}, {"id": "e2", "source": "fil", "target": "snk"}]}
        for node in definition["nodes"]:
            node.update(timeoutSec=30, retry={"maximumAttempts": 1})
        pipeline = require(opener, "POST", "/api/pipelines", definition, token)
        pipeline.update({key: definition[key] for key in ("metadata", "slo", "concurrency")})
        pipeline["nodePolicies"] = {node["id"]: {"timeoutSec": node["timeoutSec"], "retry": node["retry"]} for node in definition["nodes"]}
        pipeline["definition"] = definition
        RESULT.update(pipeline=pipeline, collection=collection)
        save()
        path = "/api/pipelines/" + pipeline["rowId"]
        stored = require(opener, "GET", path, token=token)["definition"]
        RESULT["evidence"]["savedDefinition"] = stored
        check("saved definition metadata, policies and topology roundtrip",
              all(stored.get(key) == value for key, value in definition.items()))
        anonymous = urllib.request.build_opener()
        status, _ = request(anonymous, "GET", path)
        check("unauthenticated pipeline read denied", status == 401, {"httpStatus": status})
        other, other_token = authenticate("secondary-credentials.json", "secondary")
        status, _ = request(other, "GET", path, token=other_token)
        check("cross-tenant pipeline read denied", status in (403, 404), {"httpStatus": status})
        require(opener, "POST", path + "/activate", token=token)
        run = require(opener, "POST", path + "/run", token=token)
        RESULT["run"] = run
        save()
        deadline = time.monotonic() + 180
        state = {}
        while time.monotonic() < deadline:
            state = require(opener, "GET", "/api/executions/" + run["executionId"] + "/status", token=token)
            RESULT["evidence"]["executionStatus"] = state
            save()
            if state.get("phase") in ("completed", "failed", "cancelled"):
                break
            time.sleep(2)
        check("real pipeline completed within 180 seconds", state.get("phase") == "completed", {"phase": state.get("phase")})
        data = require(opener, "GET", "/api/analytics/datasets/" + collection + "/rows", token=token)
        RESULT["evidence"]["datasetRows"] = data
        golden = [{"id": 1, "name": "Alpha", "active": True}, {"id": 3, "name": "Gamma", "active": True}]
        actual = sorted(({key: value for key, value in row.items() if key != "_ingested_at"} for row in data["rows"]), key=lambda row: row.get("id", -1))
        check("golden managed dataset contains only Alpha and Gamma", data.get("total") == 2 and actual == golden)
        other_data = require(other, "GET", "/api/analytics/datasets/" + collection + "/rows", token=other_token)
        check("cross-tenant dataset is empty", other_data.get("total") == 0 and other_data.get("rows") == [])
    except Exception as error:
        RESULT["error"] = str(error)
        check("all mandatory acceptance steps reached", False, {"error": str(error)})
    RESULT["status"] = "passed" if RESULT["checks"] and all(item["passed"] for item in RESULT["checks"]) else "failed"
    save()
    print(f"{RESULT['status'].upper()}: sanitized evidence: {ARTIFACTS / 'smoke.json'}")
    print(f"Manual sign-in credentials (private file): {ARTIFACTS / 'credentials.json'}")
    return 0 if RESULT["status"] == "passed" else 1


if __name__ == "__main__":
    sys.exit(main())
