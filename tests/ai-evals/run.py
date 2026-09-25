#!/usr/bin/env python3
"""Stdlib-only evaluator for the DataFlow AI pipeline builder."""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.request
from copy import deepcopy
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parent
DEFAULT_CASES = ROOT / "cases" / "v1.json"
KNOWN_NODE_TYPES = {"source", "transform", "fork", "merge", "sink"}


class APICallError(RuntimeError):
    def __init__(self, message: str, latency_ms: float):
        super().__init__(message)
        self.latency_ms = latency_ms


def subset(expected: Any, actual: Any) -> bool:
    if isinstance(expected, dict):
        return isinstance(actual, dict) and all(
            key in actual and subset(value, actual[key]) for key, value in expected.items()
        )
    if isinstance(expected, list):
        return isinstance(actual, list) and len(expected) == len(actual) and all(
            subset(left, right) for left, right in zip(expected, actual)
        )
    return expected == actual


def response_status(response: dict[str, Any]) -> str:
    status = response.get("status")
    if isinstance(status, str):
        return status
    return "ready" if isinstance(response.get("definition"), dict) else "unknown"


def activity_node_types(catalog: dict[str, Any]) -> dict[str, str]:
    return {
        activity_type: node_type
        for node_type, activity_types in catalog.get("nodeTypes", {}).items()
        for activity_type in activity_types
    }


def schema_valid(response: dict[str, Any], catalog: dict[str, Any]) -> bool:
    status = response_status(response)
    if status not in {"ready", "needs_input", "rejected"}:
        return False
    if status == "needs_input":
        questions = response.get("questions")
        return isinstance(questions, list) and bool(questions) and all(
            isinstance(question, str) and question.strip() for question in questions
        )
    if status == "rejected":
        warnings = response.get("warnings")
        return isinstance(warnings, list) and bool(warnings) and all(
            isinstance(warning, str) and warning.strip() for warning in warnings
        )
    definition = response.get("definition")
    if not isinstance(definition, dict):
        return False
    trigger, nodes, edges = (
        definition.get("trigger"),
        definition.get("nodes"),
        definition.get("edges"),
    )
    if not isinstance(trigger, dict) or not isinstance(trigger.get("type"), str):
        return False
    if not isinstance(nodes, list) or not nodes or not isinstance(edges, list):
        return False
    allowed_types = activity_node_types(catalog)
    for node in nodes:
        if not isinstance(node, dict):
            return False
        if not all(isinstance(node.get(key), str) and node[key] for key in ("id", "type", "activityType")):
            return False
        if node["type"] not in KNOWN_NODE_TYPES or allowed_types.get(node["activityType"]) != node["type"]:
            return False
        if not isinstance(node.get("config"), dict):
            return False
    return all(
        isinstance(edge, dict)
        and isinstance(edge.get("source"), str)
        and isinstance(edge.get("target"), str)
        for edge in edges
    )


def structural_valid(response: dict[str, Any], expected: dict[str, Any], diagnostics: dict[str, Any] | None = None) -> bool | None:
    definition = response.get("definition")
    if not isinstance(definition, dict):
        return None
    nodes, edges = definition.get("nodes"), definition.get("edges")
    if not isinstance(nodes, list) or not isinstance(edges, list):
        return False
    ids = [node.get("id") for node in nodes if isinstance(node, dict)]
    if not ids or len(ids) != len(nodes) or len(ids) != len(set(ids)):
        return False
    known = set(ids)
    incoming = {node_id: 0 for node_id in ids}
    outgoing: dict[str, list[str]] = {node_id: [] for node_id in ids}
    for edge in edges:
        if not isinstance(edge, dict):
            return False
        source, target = edge.get("source"), edge.get("target")
        if source not in known or target not in known or source == target:
            return False
        outgoing[source].append(target)
        incoming[target] += 1
    max_in_degree = max(incoming.values(), default=0)
    ready = [node_id for node_id, count in incoming.items() if count == 0]
    visited = 0
    while ready:
        node_id = ready.pop()
        visited += 1
        for target in outgoing[node_id]:
            incoming[target] -= 1
            if incoming[target] == 0:
                ready.append(target)
    if visited != len(ids):
        return False
    graph = expected.get("graph")
    if not isinstance(graph, dict):
        return True
    if len(nodes) < graph.get("minNodes", 0):
        return False
    if max((len(targets) for targets in outgoing.values()), default=0) < graph.get("minOutDegree", 0):
        return False
    if max_in_degree < graph.get("minInDegree", 0):
        return False
    nodes_by_id = {node["id"]: node for node in nodes}
    for path_index, path in enumerate(graph.get("requiredPaths", [])):
        candidates = {node["id"] for node in nodes if subset(path[0], node)}
        failed_hop = None if candidates else 0
        for hop, selector in enumerate(path[1:], start=1):
            if failed_hop is not None:
                break
            candidates = {
                target for source in candidates for target in outgoing[source]
                if subset(selector, nodes_by_id[target])
            }
            if not candidates:
                failed_hop = hop
        if failed_hop is not None:
            if diagnostics is not None:
                # hop 0: no node matches the first selector; hop N: no direct edge
                # reaches a node matching selector N from the previous matches.
                diagnostics["requiredPathFailure"] = {"path": path_index, "hop": failed_hop, "selector": path[failed_hop]}
            return False
    conditional = sum(bool(edge.get("condition")) for edge in edges if isinstance(edge, dict))
    return conditional >= graph.get("minConditionalEdges", 0)


def activities_score(response: dict[str, Any], expected: dict[str, Any], catalog: dict[str, Any]) -> float | None:
    activity_expectation = expected.get("activities")
    if not isinstance(activity_expectation, dict):
        return None
    definition = response.get("definition", {})
    actual = [node.get("activityType") for node in definition.get("nodes", []) if isinstance(node, dict)]
    required = activity_expectation.get("required", [])
    forbidden = activity_expectation.get("forbidden", [])
    checks = [activity in actual for activity in required] + [activity not in actual for activity in forbidden]
    expected_types = activity_node_types(catalog)
    for node in definition.get("nodes", []):
        activity_type = node.get("activityType", "")
        wanted = expected_types.get(activity_type)
        if wanted:
            checks.append(node.get("type") == wanted)
    return sum(checks) / len(checks) if checks else None


def grounding_score(response: dict[str, Any], expected: dict[str, Any], catalog: dict[str, Any]) -> float | None:
    config_expectations = expected.get("configs")
    definition = response.get("definition")
    if not isinstance(definition, dict):
        return None
    nodes = definition.get("nodes", [])
    checks = []
    common_keys = set(catalog.get("commonConfigKeys", []))
    config_keys = catalog.get("configKeys", {})
    for node in nodes:
        allowed = common_keys | set(config_keys.get(node.get("activityType"), []))
        checks.append(node.get("activityType") in activity_node_types(catalog) and set(node.get("config", {})) <= allowed)
    for config_expectation in config_expectations if isinstance(config_expectations, list) else []:
        activity_type = config_expectation.get("activityType")
        wanted = config_expectation.get("contains", {})
        checks.append(any(
            node.get("activityType") == activity_type and subset(wanted, node.get("config", {}))
            for node in nodes if isinstance(node, dict)
        ))
    for node_expectation in expected.get("nodes", []):
        activity_type = node_expectation.get("activityType")
        wanted = node_expectation.get("contains", {})
        checks.append(any(
            node.get("activityType") == activity_type and subset(wanted, node)
            for node in nodes if isinstance(node, dict)
        ))
    trigger = expected.get("trigger")
    if isinstance(trigger, dict):
        checks.append(subset(trigger, response.get("definition", {}).get("trigger", {})))
    execution = expected.get("execution")
    if isinstance(execution, dict):
        checks.append(subset(execution, response.get("definition", {}).get("execution", {})))
    return sum(checks) / len(checks) if checks else None


def normalized_edges(definition: dict[str, Any]) -> set[tuple[Any, Any, Any]]:
    return {
        (edge.get("source"), edge.get("target"), edge.get("condition"))
        for edge in definition.get("edges", []) if isinstance(edge, dict)
    }


def preservation_score(case: dict[str, Any], response: dict[str, Any]) -> float | None:
    preserve = case.get("expect", {}).get("preserve")
    if not isinstance(preserve, dict):
        return None
    before = case.get("request", {}).get("definition", {})
    after = response.get("definition", {})
    before_nodes = {node.get("id"): node for node in before.get("nodes", []) if isinstance(node, dict)}
    after_nodes = {node.get("id"): node for node in after.get("nodes", []) if isinstance(node, dict)}
    checks = []
    for item in preserve.get("nodes", []):
        node_id = item.get("id")
        fields = item.get("fields", ["id", "activityType", "config"])
        checks.extend(
            node_id in before_nodes and node_id in after_nodes
            and before_nodes[node_id].get(field) == after_nodes[node_id].get(field)
            for field in fields
        )
    if preserve.get("edges"):
        checks.append(normalized_edges(before) == normalized_edges(after))
    return sum(checks) / len(checks) if checks else None


def clarification_score(response: dict[str, Any], expected: dict[str, Any]) -> float | None:
    wanted = expected.get("status")
    if wanted not in {"needs_input", "rejected"}:
        return None
    status_ok = response_status(response) == wanted
    if wanted == "needs_input":
        questions = response.get("questions")
        return float(status_ok and isinstance(questions, list) and bool(questions))
    return float(status_ok)


def nested_number(value: dict[str, Any], paths: list[tuple[str, ...]]) -> float | None:
    for path in paths:
        current: Any = value
        for key in path:
            if not isinstance(current, dict) or key not in current:
                break
            current = current[key]
        else:
            if isinstance(current, (int, float)) and not isinstance(current, bool):
                return float(current)
    return None


def score_case(case: dict[str, Any], response: dict[str, Any], latency_ms: float, catalog: dict[str, Any]) -> dict[str, Any]:
    expected = case.get("expect", {})
    diagnostics: dict[str, Any] = {}
    result = {
        "id": case["id"],
        "category": case["category"],
        "responseStatus": response_status(response),
        "schemaValid": schema_valid(response, catalog),
        "structuralValid": structural_valid(response, expected, diagnostics),
        "activityAccuracy": activities_score(response, expected, catalog),
        "groundingAccuracy": grounding_score(response, expected, catalog),
        "preservationAccuracy": preservation_score(case, response),
        "clarificationAccuracy": clarification_score(response, expected),
        "latencyMs": round(latency_ms, 2),
        "repairCount": nested_number(response, [
            ("repairCount",), ("repairs",), ("metrics", "repairCount"), ("meta", "repairCount")
        ]),
    }
    scored = [
        value for key, value in result.items()
        if key.endswith("Valid") or key.endswith("Accuracy")
        if isinstance(value, (bool, int, float))
    ]
    result["passed"] = bool(scored) and all(float(value) == 1.0 for value in scored)
    result.update(diagnostics)
    return result


def call_api(base_url: str, token: str | None, case: dict[str, Any], timeout: float) -> tuple[dict[str, Any], float]:
    url = base_url.rstrip("/") + case["endpoint"]
    body = json.dumps(case["request"]).encode()
    headers = {"Content-Type": "application/json", "Accept": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    request = urllib.request.Request(url, data=body, headers=headers, method="POST")
    started = time.perf_counter()
    try:
        with urllib.request.urlopen(request, timeout=timeout) as opened:
            response = json.load(opened)
    except urllib.error.HTTPError as error:
        detail = error.read(1000).decode("utf-8", errors="replace").strip()
        suffix = f": {detail}" if detail else ""
        latency_ms = (time.perf_counter() - started) * 1000
        raise APICallError(f"HTTP {error.code} from {case['endpoint']}{suffix}", latency_ms) from error
    except urllib.error.URLError as error:
        latency_ms = (time.perf_counter() - started) * 1000
        raise APICallError(f"cannot reach {base_url}: {error.reason}", latency_ms) from error
    except json.JSONDecodeError as error:
        latency_ms = (time.perf_counter() - started) * 1000
        raise APICallError("API returned invalid JSON", latency_ms) from error
    latency_ms = (time.perf_counter() - started) * 1000
    if not isinstance(response, dict):
        raise APICallError("API response must be a JSON object", latency_ms)
    return response, latency_ms


def summarize(results: list[dict[str, Any]]) -> dict[str, Any]:
    fields = [
        "schemaValid", "structuralValid", "activityAccuracy", "groundingAccuracy",
        "preservationAccuracy", "clarificationAccuracy",
    ]
    passed = sum(bool(item.get("passed")) for item in results)
    summary: dict[str, Any] = {"cases": len(results), "passed": passed, "passRate": round(passed / len(results), 4) if results else None}
    for field in fields:
        values = [float(item[field]) for item in results if isinstance(item.get(field), (bool, int, float))]
        summary[field + "Rate"] = round(sum(values) / len(values), 4) if values else None
    latencies = sorted(float(item["latencyMs"]) for item in results if "latencyMs" in item)
    summary["latencyMsMean"] = round(sum(latencies) / len(latencies), 2) if latencies else None
    summary["latencyMsP95"] = round(latencies[(95 * len(latencies) + 99) // 100 - 1], 2) if latencies else None
    repairs = [item["repairCount"] for item in results if item.get("repairCount") is not None]
    summary["repairCountMean"] = round(sum(repairs) / len(repairs), 2) if repairs else None
    return summary


def validate_required_paths(paths: Any, activities: dict[str, str]) -> None:
    if not isinstance(paths, list) or not paths:
        raise ValueError("graph.requiredPaths must be a non-empty array")
    for path in paths:
        if not isinstance(path, list) or len(path) < 2:
            raise ValueError("each required path must contain at least two node selectors")
        for selector in path:
            if (
                not isinstance(selector, dict)
                or set(selector) - {"id", "activityType", "config"}
                or not {"id", "activityType"}.intersection(selector)
            ):
                raise ValueError("path selectors require id or activityType, with optional config")
            for key in ("id", "activityType"):
                if key in selector and (not isinstance(selector[key], str) or not selector[key].strip()):
                    raise ValueError(f"path selector {key} must be a non-empty string")
            if "activityType" in selector and selector["activityType"] not in activities:
                raise ValueError("path selector activityType must exist in the catalog")
            if "config" in selector and not isinstance(selector["config"], dict):
                raise ValueError("path selector config must be an object")


def load_suite(path: Path) -> dict[str, Any]:
    with path.open(encoding="utf-8") as source:
        suite = json.load(source)
    if not isinstance(suite, dict) or not isinstance(suite.get("cases"), list):
        raise ValueError("case file must contain a cases array")
    catalog = suite.get("catalog")
    if not isinstance(catalog, dict) or not isinstance(catalog.get("version"), str):
        raise ValueError("case file must contain a versioned catalog")
    node_types, config_keys = catalog.get("nodeTypes"), catalog.get("configKeys")
    if not isinstance(node_types, dict) or not isinstance(config_keys, dict):
        raise ValueError("catalog must contain nodeTypes and configKeys objects")
    activities = activity_node_types(catalog)
    if not activities or set(node_types) - KNOWN_NODE_TYPES or set(config_keys) - set(activities):
        raise ValueError("catalog contains invalid node or activity types")
    fixtures = suite.get("fixtures")
    if not isinstance(fixtures, dict) or not isinstance(fixtures.get("version"), str):
        raise ValueError("case file must contain versioned connector fixtures")
    connectors = fixtures.get("connectors")
    if not isinstance(connectors, list) or not connectors:
        raise ValueError("fixtures.connectors must be a non-empty array")
    fixture_names = []
    for connector in connectors:
        if not isinstance(connector, dict) or not all(
            isinstance(connector.get(key), str) and connector[key].strip()
            for key in ("provider", "kind", "displayName")
        ):
            raise ValueError("connector fixtures require provider, kind, and displayName")
        fixture_names.append(connector["displayName"])
    if len(fixture_names) != len(set(fixture_names)):
        raise ValueError("connector fixture display names must be unique")
    ids = [case.get("id") for case in suite["cases"]]
    if any(not value for value in ids) or len(ids) != len(set(ids)):
        raise ValueError("case ids must be present and unique")
    for case in suite["cases"]:
        if case.get("endpoint") not in {"/api/ai/generate", "/api/ai/refine"}:
            raise ValueError(f"{case['id']}: unsupported endpoint")
        if not isinstance(case.get("request", {}).get("prompt"), str):
            raise ValueError(f"{case['id']}: request.prompt is required")
        unknown_fixtures = set(case.get("connectorFixtures", [])) - set(fixture_names)
        if unknown_fixtures:
            raise ValueError(f"{case['id']}: unknown connector fixtures {sorted(unknown_fixtures)}")
        graph = case.get("expect", {}).get("graph")
        if isinstance(graph, dict) and "requiredPaths" in graph:
            try:
                validate_required_paths(graph["requiredPaths"], activities)
            except ValueError as error:
                raise ValueError(f"{case['id']}: {error}") from error
    return suite


def self_test(suite: dict[str, Any]) -> list[dict[str, Any]]:
    from io import BytesIO
    from unittest.mock import patch

    if len(suite["cases"]) < 25:
        raise AssertionError("v1 corpus must contain at least 25 cases")
    base_definition = {
        "trigger": {"type": "manual"},
        "nodes": [
            {"id": "source", "type": "source", "activityType": "http.fetch", "config": {"url": "https://example.test/orders"}},
            {"id": "sink", "type": "sink", "activityType": "sink.s3", "config": {"bucket": "eval-bucket", "key": "orders.json"}},
        ],
        "edges": [{"source": "source", "target": "sink"}],
    }
    cases = [
        {
            "id": "self-ready", "category": "self-test", "request": {},
            "expect": {"activities": {"required": ["http.fetch", "sink.s3"]}, "configs": [{"activityType": "sink.s3", "contains": {"bucket": "eval-bucket"}}]},
        },
        {
            "id": "self-preserve", "category": "self-test", "request": {"definition": base_definition},
            "expect": {"preserve": {"nodes": [{"id": "source"}], "edges": True}},
        },
        {
            "id": "self-clarify", "category": "self-test", "request": {},
            "expect": {"status": "needs_input"},
        },
    ]
    responses = [
        {"status": "ready", "definition": base_definition, "metrics": {"repairCount": 1}},
        {"definition": base_definition},
        {"status": "needs_input", "questions": ["Which destination should receive the data?"]},
    ]
    catalog = suite["catalog"]
    results = [score_case(case, response, 1.0, catalog) for case, response in zip(cases, responses)]
    if not all(result["passed"] for result in results):
        raise AssertionError(f"scoring self-test failed: {results}")
    hallucinated = score_case(
        {"id": "self-hallucination", "category": "self-test", "request": {}, "expect": {}},
        {"definition": {
            "trigger": {"type": "manual"},
            "nodes": [
                {"id": "source", "type": "source", "activityType": "http.fetch", "config": {"url": "https://example.test/orders", "teleportMode": True}},
                {"id": "sink", "type": "sink", "activityType": "sink.teleport", "config": {}},
            ],
            "edges": [{"source": "source", "target": "sink"}],
        }},
        1.0,
        catalog,
    )
    if hallucinated["schemaValid"] or hallucinated["groundingAccuracy"] != 0 or hallucinated["passed"]:
        raise AssertionError(f"hallucination self-test was accepted: {hallucinated}")
    path_case = deepcopy(cases[0])
    path_case["expect"]["graph"] = {"requiredPaths": [[
        {"activityType": "http.fetch"},
        {"activityType": "transform.filter"},
        {"activityType": "sink.s3", "config": {"bucket": "eval-bucket"}},
    ]]}
    connected = deepcopy(base_definition)
    connected["nodes"].insert(1, {
        "id": "filter", "type": "transform", "activityType": "transform.filter", "config": {},
    })
    connected["edges"] = [
        {"source": "source", "target": "filter"}, {"source": "filter", "target": "sink"},
    ]
    disconnected = {**connected, "edges": []}
    split = deepcopy(connected)
    split["nodes"].append({**connected["nodes"][1], "id": "other-filter"})
    split["edges"][1]["source"] = "other-filter"
    independent = deepcopy(connected)
    independent["nodes"].extend([
        {**base_definition["nodes"][0], "id": "other-source"},
        {**base_definition["nodes"][1], "id": "other-sink"},
    ])
    independent["edges"].append({"source": "other-source", "target": "other-sink"})
    branches_case = deepcopy(path_case)
    branches_case["expect"]["graph"]["requiredPaths"].append([
        {"id": "other-source"}, {"id": "other-sink"},
    ])
    wrong_config = deepcopy(path_case)
    wrong_config["expect"]["graph"]["requiredPaths"][0][-1]["config"]["bucket"] = "another-bucket"
    wrong_identity = deepcopy(path_case)
    wrong_identity["expect"]["graph"]["requiredPaths"][0][0]["id"] = "sink"
    for label, case, definition, passed in [
        ("connected chain", path_case, connected, True),
        ("disconnected chain", path_case, disconnected, False),
        ("legacy disconnected graph", cases[0], disconnected, True),
        ("split chain with matching activities", path_case, split, False),
        ("direct hops required", path_case, base_definition, False),
        ("undeclared independent branch", path_case, independent, True),
        ("declared independent branches", branches_case, independent, True),
        ("missing declared branch", branches_case, connected, False),
        ("selector config mismatch", wrong_config, connected, False),
        ("selector fields match the same node", wrong_identity, connected, False),
    ]:
        paths = case["expect"].get("graph", {}).get("requiredPaths")
        if paths is not None:
            validate_required_paths(paths, activity_node_types(catalog))
        result = score_case(case, {"definition": definition}, 1.0, catalog)
        if result["passed"] != passed or result["structuralValid"] != passed:
            raise AssertionError(f"{label} self-test failed: {result}")
    split_result = score_case(path_case, {"definition": split}, 1.0, catalog)
    if split_result.get("requiredPathFailure") != {"path": 0, "hop": 2, "selector": path_case["expect"]["graph"]["requiredPaths"][0][2]}:
        raise AssertionError(f"required-path diagnostic names the wrong hop: {split_result}")
    identity_result = score_case(wrong_identity, {"definition": connected}, 1.0, catalog)
    if identity_result.get("requiredPathFailure", {}).get("hop") != 0:
        raise AssertionError(f"required-path diagnostic misses a first-selector failure: {identity_result}")
    if "requiredPathFailure" in score_case(path_case, {"definition": connected}, 1.0, catalog):
        raise AssertionError("passing required paths must not report a failure")
    for paths in [None, [], {}, [[]], [[{"id": "source"}]], [["source", "sink"]],
                  [[{}, {"id": "sink"}]], [[{"id": ""}, {"id": "sink"}]],
                  [[{"activityType": "sink.teleport"}, {"id": "sink"}]],
                  [[{"id": "source", "config": []}, {"id": "sink"}]],
                  [[{"id": "source", "unknown": True}, {"id": "sink"}]]]:
        invalid_suite = deepcopy(suite)
        invalid_suite["cases"][0]["expect"]["graph"] = {"requiredPaths": paths}
        with patch.object(Path, "open", return_value=BytesIO(json.dumps(invalid_suite).encode())):
            try:
                load_suite(DEFAULT_CASES)
            except ValueError:
                pass
            else:
                raise AssertionError(f"invalid required paths were accepted: {paths}")
    with patch("urllib.request.urlopen", return_value=BytesIO(b"not-json")):
        try:
            call_api("http://example.test", None, {"endpoint": "/ai", "request": {}}, 1)
        except APICallError as error:
            if error.latency_ms < 0 or str(error) != "API returned invalid JSON":
                raise AssertionError(f"invalid JSON lost call metadata: {error}") from error
        else:
            raise AssertionError("invalid API JSON was accepted")
    return results


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cases", type=Path, default=DEFAULT_CASES)
    parser.add_argument("--base-url", default=os.getenv("AI_EVAL_BASE_URL", "http://localhost:4000"))
    parser.add_argument("--timeout", type=float, default=float(os.getenv("AI_EVAL_TIMEOUT_SECONDS", "300")))
    parser.add_argument("--output", type=Path)
    parser.add_argument("--token-file", type=Path, help="read a bearer token or auth refresh JSON without exposing it in the command line")
    parser.add_argument("--self-test", action="store_true", help="exercise corpus validation and scoring without network access")
    parser.add_argument("--strict", action="store_true", help="exit non-zero when any evaluated case fails")
    parser.add_argument("--limit", type=int, help="run only the first N cases")
    args = parser.parse_args()

    try:
        token = os.getenv("AI_EVAL_TOKEN")
        if args.token_file:
            raw_token = args.token_file.read_text(encoding="utf-8").strip()
            try:
                decoded_token = json.loads(raw_token)
                token = decoded_token.get("accessToken") if isinstance(decoded_token, dict) else None
            except json.JSONDecodeError:
                token = raw_token
            if not isinstance(token, str) or not token:
                raise ValueError("token file must contain a token or accessToken JSON field")
        suite = load_suite(args.cases)
        if args.self_test:
            results = self_test(suite)
            mode = "self-test"
        else:
            cases = suite["cases"][: args.limit] if args.limit else suite["cases"]
            results = []
            for case in cases:
                try:
                    response, latency_ms = call_api(args.base_url, token, case, args.timeout)
                    results.append(score_case(case, response, latency_ms, suite["catalog"]))
                except APICallError as error:
                    results.append({"id": case["id"], "category": case["category"], "passed": False, "error": str(error), "latencyMs": round(error.latency_ms, 2)})
                except json.JSONDecodeError as error:
                    results.append({"id": case["id"], "category": case["category"], "passed": False, "error": str(error)})
            mode = "live"
    except (OSError, ValueError, AssertionError, json.JSONDecodeError) as error:
        print(f"ai-evals: {error}", file=sys.stderr)
        return 2

    report = {
        "suiteVersion": suite.get("version"),
        "catalogVersion": suite["catalog"]["version"],
        "fixtureVersion": suite["fixtures"]["version"],
        "mode": mode,
        "model": os.getenv("AI_EVAL_MODEL", "unknown"),
        "promptVersion": os.getenv("AI_EVAL_PROMPT_VERSION", "unknown"),
        "schemaVersion": os.getenv("AI_EVAL_SCHEMA_VERSION", "pipeline-definition-v1"),
        "generatedAt": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "summary": summarize(results),
        "byCategory": {
            category: summarize([item for item in results if item["category"] == category])
            for category in sorted({item["category"] for item in results})
        },
        "results": results,
    }
    rendered = json.dumps(report, indent=2, sort_keys=True)
    print(rendered)
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(rendered + "\n", encoding="utf-8")
    return 1 if args.strict and any(not result.get("passed") for result in results) else 0


if __name__ == "__main__":
    raise SystemExit(main())
