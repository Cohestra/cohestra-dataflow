#!/usr/bin/env python3
"""Integrated controls/admission checks against the owned local acceptance runtime."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import uuid

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / ".artifacts/local-acceptance/full-controls"
spec = importlib.util.spec_from_file_location("smoke", ROOT / "scripts/local-acceptance-smoke.py")
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)
COMPOSE = ["rtk", "proxy", "docker", "compose", "-p", "cohestra-acceptance-20260922", "-f", "docker-compose.yml", "-f", "docker-compose.acceptance.yml"]
RESULT = {"status": "running", "realAPI": True, "realTemporal": True, "realPostgres": True, "checks": [], "runs": []}


def save():
    text = json.dumps(RESULT, indent=2)
    for value in smoke.SENSITIVE:
        text = text.replace(value, "[redacted]")
    (OUT / "controls.json").write_text(text + "\n")
    if RESULT["status"] != "running" and RESULT["runs"]:
        (OUT / f"controls-{RESULT['runs'][0]['executionId']}.json").write_text(text + "\n")


def check(name, passed, evidence=None):
    RESULT["checks"].append({"name": name, "passed": bool(passed), "evidence": evidence})
    save()
    print(("PASS " if passed else "FAIL ") + name, flush=True)


def sql(statement):
    run = subprocess.run([*COMPOSE, "exec", "-T", "postgres", "psql", "-X", "-qAt", "-v", "ON_ERROR_STOP=1", "-U", "dataflow", "-d", "dataflow"],
                         cwd=ROOT, input=statement, text=True, capture_output=True, timeout=20)
    if run.returncode:
        raise RuntimeError("Fixture SQL failed: " + run.stderr[:2000])
    return run.stdout.strip()


def literal(value):
    return "'" + str(value).replace("'", "''") + "'"


def api(actor, method, path, body=None):
    return smoke.require(actor[0], method, path, body, actor[1])


def expected(actor, method, path, status, body=None):
    actual, data = smoke.request(actor[0], method, path, body, actor[1])
    check(f"{method} {path} returns {status}", actual == status, {"status": actual, "body": data})
    return data


def wait(label, get, predicate, timeout=120):
    # Shared developer-host load can delay real Temporal tasks; assertions stay exact.
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        value = get()
        if predicate(value):
            return value
        time.sleep(0.2)
    raise TimeoutError(label + ": " + json.dumps(value))


def progress(execution, pipeline):
    return json.loads(sql(f"""SELECT json_build_object(
      'phase',e.phase,'state',e.control_state,'revision',e.control_revision,
      'sourceRecords',coalesce((SELECT record_count FROM node_runs WHERE execution_id=e.id AND node_id='src'),0),
      'sinkRuns',(SELECT count(*) FROM node_runs WHERE execution_id=e.id AND node_id='snk'),
      'cursors',(SELECT count(*) FROM connector_state WHERE tenant_id=e.tenant_id AND connection_id={literal(pipeline['pipelineKey'] + ':src')}),
      'pauseAudits',(SELECT count(*) FROM audit_log WHERE resource=e.id AND action='execution.pause'))
      FROM executions e WHERE e.id={literal(execution)};"""))


def fixture(owner, connector, label):
    collection = "controls_" + uuid.uuid4().hex[:12]
    definition = {"id": str(uuid.uuid4()), "name": label, "trigger": {"type": "manual"}, "nodes": [
        {"id": "src", "type": "source", "activityType": "postgres.fetch", "label": "Slow local pages",
         "config": {"connectionId": connector, "table": "local_acceptance.control_slow", "cursorColumn": "id", "columns": "id,name,active", "syncMode": "cursor", "cursorType": "number"},
         "ingestion": {"mode": "incremental", "pageSize": 3}},
        {"id": "fil", "type": "transform", "activityType": "transform.filter", "label": "Active rows", "config": {"predicate": "r.active === true"}},
        {"id": "snk", "type": "sink", "activityType": "sink.records", "label": "Control output", "config": {"collection": collection}}],
        "edges": [{"id": "a", "source": "src", "target": "fil"}, {"id": "b", "source": "fil", "target": "snk"}]}
    for node in definition["nodes"]:
        node.update(timeoutSec=30, retry={"maximumAttempts": 1})
    pipeline = api(owner, "POST", "/api/pipelines", definition)
    pipeline.update(definition=definition, collection=collection)
    return pipeline


def main():
    os.umask(0o077)
    OUT.mkdir(parents=True, exist_ok=True)
    owner = smoke.authenticate("credentials.json", "primary")
    me = api(owner, "GET", "/api/auth/me")["user"]
    running = []
    try:
        if not me["email"].endswith("@local.test"):
            raise RuntimeError("Quota fixture must belong to a generated local.test account")
        quota = sql(f"UPDATE billing_plans SET extra_quota=100 WHERE tenant_id={literal(me['tenant_id'])} RETURNING extra_quota;")
        check("isolated synthetic tenant granted finite 100-run fixture quota", quota == "100", {"tenantId": me["tenant_id"], "extraQuota": 100, "fixtureOnly": True})
        actors = {}
        for role in ("viewer", "editor", "cross"):
            actor = smoke.authenticate(f"full-controls/{role}.json", role)
            user = api(actor, "GET", "/api/auth/me")["user"]
            if role != "cross":
                sql(f"UPDATE users SET tenant_id={literal(me['tenant_id'])},role='member' WHERE id={literal(user['id'])};")
                auth = smoke.require(actor[0], "POST", "/api/auth/refresh")
                smoke.SENSITIVE.append(auth["accessToken"])
                actor = (actor[0], auth["accessToken"])
            actors[role] = (actor, user["id"])
        sql("""CREATE SCHEMA IF NOT EXISTS local_acceptance;
CREATE TABLE IF NOT EXISTS local_acceptance.control_records(id integer PRIMARY KEY,name text NOT NULL,active boolean NOT NULL);
TRUNCATE local_acceptance.control_records;
INSERT INTO local_acceptance.control_records SELECT i,'Record '||i,i%2=1 FROM generate_series(1,12)i;
CREATE OR REPLACE VIEW local_acceptance.control_slow AS
SELECT r.id,r.name,r.active FROM local_acceptance.control_records r
CROSS JOIN LATERAL (SELECT pg_sleep(0.2 + r.id*0)) delay;
""")
        connector = api(owner, "POST", "/api/connectors", {"provider": "postgres", "name": "Control fixture " + uuid.uuid4().hex[:10],
          "config": {"host": "127.0.0.1", "port": 15433, "database": "dataflow", "user": "dataflow", "sslMode": "disable"}, "secret": {"password": "dataflow"}})["id"]
        first = fixture(owner, connector, "Controls pause resume " + uuid.uuid4().hex[:6])
        for role in ("viewer", "editor"):
            api(owner, "POST", f"/api/pipelines/{first['rowId']}/access", {"userId": actors[role][1], "role": role})
        execution = api(owner, "POST", f"/api/pipelines/{first['rowId']}/run")["executionId"]
        running.append(execution)
        RESULT["runs"].append({"case": "pause-resume", "executionId": execution, "pipeline": first})
        wait("first real source page", lambda: progress(execution, first), lambda p: p["sourceRecords"] >= 3)
        path = f"/api/executions/{execution}"
        paused = expected(owner, "POST", path + "/pause", 200)
        repeat = expected(owner, "POST", path + "/pause", 200)
        check("duplicate owner pause preserves durable revision", paused["controlRevision"] == repeat["controlRevision"])
        for action in ("pause", "resume", "cancel"):
            expected(actors["viewer"][0], "POST", path + "/" + action, 403)
            expected(actors["cross"][0], "POST", path + "/" + action, 404)
        wait("real workflow observes pause", lambda: api(owner, "GET", path + "/status"), lambda p: p.get("phase") == "paused")
        time.sleep(3)  # At most one already-admitted 4-row page may drain.
        before = progress(execution, first)
        time.sleep(2)
        after = progress(execution, first)
        check("pause holds after admitted page drains; no sink or cursor commit", before == after and 3 <= after["sourceRecords"] < 12 and after["sinkRuns"] == 0 and after["cursors"] == 0 and after["pauseAudits"] == 1, after)
        expected(actors["editor"][0], "POST", path + "/pause", 200)
        expected(actors["editor"][0], "POST", path + "/resume", 200)
        final = wait("resumed workflow completes", lambda: progress(execution, first), lambda p: p["phase"] in ("completed", "failed", "cancelled"))
        check("resume completes all four real pages and commits cursor", final["phase"] == "completed" and final["sourceRecords"] == 12 and final["cursors"] == 1, final)
        dataset = api(owner, "GET", f"/api/analytics/datasets/{first['collection']}/rows")
        check("resumed output matches six odd IDs", dataset["total"] == 6 and sorted(row["id"] for row in dataset["rows"]) == [1,3,5,7,9,11], dataset)
        for action in ("pause", "resume", "cancel"):
            expected(owner, "POST", path + "/" + action, 409)
        second = fixture(owner, connector, "Controls cancel " + uuid.uuid4().hex[:6])
        api(owner, "POST", f"/api/pipelines/{second['rowId']}/access", {"userId": actors["editor"][1], "role": "editor"})
        execution = api(owner, "POST", f"/api/pipelines/{second['rowId']}/run")["executionId"]
        running.append(execution)
        RESULT["runs"].append({"case": "cancel", "executionId": execution, "pipeline": second})
        wait("cancel fixture started paging", lambda: progress(execution, second), lambda p: p["sourceRecords"] >= 3)
        expected(actors["editor"][0], "POST", f"/api/executions/{execution}/cancel", 200)
        final = wait("cancel reaches durable terminal", lambda: progress(execution, second), lambda p: p["phase"] in ("completed", "failed", "cancelled"))
        check("cancel prevents sink and cursor commit", final["phase"] == "cancelled" and final["sinkRuns"] == 0 and final["cursors"] == 0, final)
        dataset = api(owner, "GET", f"/api/analytics/datasets/{second['collection']}/rows")
        check("cancelled output is empty", dataset["total"] == 0 and dataset["rows"] == [])
        expected(owner, "POST", f"/api/executions/{execution}/resume", 409)
        reserved = copy.deepcopy(first["definition"])
        reserved.update(id=str(uuid.uuid4()), tenantId=me["tenant_id"], name="Reserved agent admission fixture")
        reserved["nodes"] = reserved["nodes"][:1] + [{"id": "agent", "type": "agent", "activityType": "agent.run", "config": {
            "agentId": str(uuid.uuid4()), "agentVersion": 1, "inputBinding": {"mode": "batch", "fields": ["id"], "maxRecords": 12}}}]
        reserved["edges"] = [{"id": "agent-edge", "source": "src", "target": "agent"}]
        denial = expected(owner, "POST", "/api/pipelines", 400, reserved)
        check("valid reserved agent save denied for implementation gate", "agent execution is not implemented" in json.dumps(denial))
        legacy = str(uuid.uuid4())
        sql(f"INSERT INTO pipelines(id,pipeline_key,tenant_id,name,definition,created_by) VALUES({literal(legacy)},{literal(reserved['id'])},{literal(me['tenant_id'])},'Reserved legacy fixture',{literal(json.dumps(reserved))}::jsonb,{literal(me['id'])});")
        for action in ("run", "activate"):
            denial = expected(owner, "POST", f"/api/pipelines/{legacy}/{action}", 400)
            check("stored reserved agent " + action + " denied by admission", "agent execution is not implemented" in json.dumps(denial))
        reserved["trigger"] = {"type": "cron", "schedule": "0 0 1 1 *"}
        expected(owner, "POST", "/api/pipelines", 400, reserved)
        sql(f"UPDATE pipelines SET definition={literal(json.dumps(reserved))}::jsonb WHERE id={literal(legacy)};")
        expected(owner, "POST", f"/api/pipelines/{legacy}/activate", 400)
        check("reserved cron activation leaves draft and no executions", sql(f"SELECT status||':'||(SELECT count(*) FROM executions WHERE pipeline_id=p.id) FROM pipelines p WHERE id={literal(legacy)};") == "draft:0")
        schedule = subprocess.run([*COMPOSE, "exec", "-T", "temporal", "temporal", "schedule", "describe", "--address", "127.0.0.1:7233", "--namespace", "test", "--schedule-id", f"sched-{reserved['id']}-test"], cwd=ROOT, capture_output=True, text=True, timeout=15)
        check("reserved cron creates no Temporal schedule", schedule.returncode != 0 and "not found" in (schedule.stderr + schedule.stdout).lower(), {"exitCode": schedule.returncode, "output": (schedule.stderr + schedule.stdout)[-1000:]})
        RESULT["uiPipeline"] = fixture(owner, connector, "Browser controls " + uuid.uuid4().hex[:6])
        RESULT["uiCancelPipeline"] = fixture(owner, connector, "Browser cancel " + uuid.uuid4().hex[:6])
        RESULT["uiEditorUserId"] = actors["editor"][1]
        for key in ("uiPipeline", "uiCancelPipeline"):
            api(owner, "POST", f"/api/pipelines/{RESULT[key]['rowId']}/access", {"userId": actors["editor"][1], "role": "editor"})
    except Exception as error:
        RESULT["error"] = str(error)
        RESULT["checks"].append({"name": "all integrated cases reached", "passed": False, "evidence": str(error)})
        print("FAIL integrated controls; see sanitized controls.json", flush=True)
    finally:
        for execution in running:
            smoke.request(owner[0], "POST", f"/api/executions/{execution}/cancel", token=owner[1])
        RESULT["status"] = "passed" if all(check["passed"] for check in RESULT["checks"]) else "failed"
        save()
    return 0 if RESULT["status"] == "passed" else 1


if __name__ == "__main__":
    sys.exit(main())
