#!/usr/bin/env python3
"""Fail CI on reachable Go vulnerabilities unless a reviewed exemption covers them.

govulncheck v1.1.x has no exclude flag, so CI runs it with -format json and this
gate applies .github/govulncheck-exemptions.txt. Like govulncheck's text mode,
only symbol-level findings (code that calls the vulnerable function) count.

Exemption lines: "<OSV id> <review-by YYYY-MM-DD> <reason>". An exemption past
its review-by date fails the gate again; one that matches nothing is reported
so it can be removed.

Usage: govulncheck-gate.py <govulncheck.json> <exemptions.txt> [--today YYYY-MM-DD]
"""
import datetime
import json
import sys


def load_stream(path):
    """govulncheck -format json writes a stream of concatenated JSON objects."""
    text, decoder, pos, messages = open(path, encoding="utf-8").read(), json.JSONDecoder(), 0, []
    while True:
        while pos < len(text) and text[pos].isspace():
            pos += 1
        if pos >= len(text):
            return messages
        message, pos = decoder.raw_decode(text, pos)
        messages.append(message)


def load_exemptions(path, today):
    exemptions, errors = {}, []
    for number, line in enumerate(open(path, encoding="utf-8"), 1):
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        parts = line.split(None, 2)
        if len(parts) < 3:
            errors.append(f"{path}:{number}: expected '<OSV id> <review-by YYYY-MM-DD> <reason>'")
            continue
        osv, review_by, reason = parts
        try:
            due = datetime.date.fromisoformat(review_by)
        except ValueError:
            errors.append(f"{path}:{number}: invalid review-by date {review_by!r}")
            continue
        if due < today:
            errors.append(f"{path}:{number}: exemption for {osv} expired on {review_by}; re-review or remove it")
            continue
        exemptions[osv] = (review_by, reason)
    return exemptions, errors


def main(argv):
    if len(argv) not in (3, 5) or (len(argv) == 5 and argv[3] != "--today"):
        print(__doc__.strip(), file=sys.stderr)
        return 2
    today = datetime.date.fromisoformat(argv[4]) if len(argv) == 5 else datetime.date.today()
    messages = load_stream(argv[1])
    exemptions, errors = load_exemptions(argv[2], today)

    summaries = {m["osv"]["id"]: m["osv"].get("summary", "") for m in messages if "osv" in m}
    called = {}
    for message in messages:
        finding = message.get("finding")
        trace = (finding or {}).get("trace") or []
        if finding and trace and trace[0].get("function"):
            frame = trace[0]
            called.setdefault(finding["osv"], (frame.get("module", "stdlib"), frame.get("version", ""), finding.get("fixed_version", "")))

    blocking = []
    for osv, (module, version, fixed) in sorted(called.items()):
        line = f"{osv} {module}@{version} (fixed in: {fixed or 'N/A'}) {summaries.get(osv, '')}".rstrip()
        if osv in exemptions:
            review_by, reason = exemptions[osv]
            print(f"EXEMPT   {line}\n         review by {review_by}: {reason}")
        else:
            blocking.append(line)
            print(f"VULNERABLE {line}")
    for osv in sorted(set(exemptions) - set(called)):
        print(f"::warning::govulncheck exemption {osv} matched no reachable finding; remove it")
    for error in errors:
        print(f"::error::{error}")
    if blocking or errors:
        print(f"{len(blocking)} reachable vulnerabilit{'y' if len(blocking) == 1 else 'ies'} without a valid exemption.")
        return 1
    print(f"No unexempted reachable vulnerabilities ({len(called)} reachable, {len(called) - len(blocking)} exempted).")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
