#!/usr/bin/env python3
"""Run the source-backed import matrix; skipped tests never count as proof."""
import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[1]
MANIFEST = ROOT / "docs/qa/import-fixture-matrix.json"


def summarize(output, returncode, output_format):
    counts = {"passed": 0, "failed": 0, "skipped": 0}
    if output_format == "go-json":
        for line in output.splitlines():
            try:
                event = json.loads(line)
            except (ValueError, TypeError):
                continue
            if not isinstance(event, dict) or not event.get("Test"):
                continue
            action = event.get("Action")
            # Count top-level tests. A failing/skipped child is independently
            # material even when its parent reports pass.
            if action == "pass" and "/" not in event["Test"]:
                counts["passed"] += 1
            elif action == "fail":
                counts["failed"] += 1
            elif action == "skip":
                counts["skipped"] += 1
    else:
        for field, label in [("passed", "pass"), ("failed", "fail"), ("skipped", "skipped")]:
            matches = re.findall(rf"^# {label}\s+(\d+)\s*$", output, re.MULTILINE)
            counts[field] = sum(map(int, matches))
    if returncode != 0 or counts["failed"]:
        status = "failed"
    elif counts["passed"] == 0:
        status = "blocked" if counts["skipped"] else "failed"
    elif counts["skipped"]:
        status = "partial"
    else:
        status = "passed"
    return {"status": status, "tests": counts}


def preflight(row, environment, include_live):
    if row.get("manual_gap"):
        return row["manual_gap"]
    if row.get("live") and not include_live:
        return "Live provider execution was not selected (--include-live)."
    missing = [key for key in row.get("requires_env", []) if not environment.get(key)]
    if missing:
        return "Required configuration is missing: " + ", ".join(missing)
    return None


def run_command(command, cwd, timeout):
    # Go and Node can create child processes. Kill the entire owned group on
    # timeout so a failed evidence case cannot leave a runner behind on POSIX.
    with subprocess.Popen(command, cwd=cwd, text=True, stdout=subprocess.PIPE,
                          stderr=subprocess.STDOUT, start_new_session=os.name == "posix") as process:
        try:
            output, _ = process.communicate(timeout=timeout)
        except subprocess.TimeoutExpired as error:
            if os.name == "posix":
                os.killpg(process.pid, signal.SIGKILL)
            else:
                process.kill()
            output, _ = process.communicate()
            error.stdout = output
            raise
        return subprocess.CompletedProcess(command, process.returncode, output)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True, help="New evidence directory (must not exist)")
    parser.add_argument("--include-live", action="store_true", help="Run configured live profiles against dedicated test resources")
    parser.add_argument("--only", action="append", default=[], help="Run a matrix case ID; may be repeated")
    args = parser.parse_args(argv)
    manifest_bytes = MANIFEST.read_bytes()
    rows = json.loads(manifest_bytes)["cases"]
    unknown = set(args.only) - {row["id"] for row in rows}
    if unknown:
        parser.error("Unknown case IDs: " + ", ".join(sorted(unknown)))
    if args.only:
        rows = [row for row in rows if row["id"] in args.only]
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    revision = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    dirty = bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT))
    report = {"schema_version": 1, "started_at": dt.datetime.now(dt.timezone.utc).isoformat(),
              "revision": revision, "working_tree_dirty": dirty,
              "manifest_sha256": hashlib.sha256(manifest_bytes).hexdigest(), "cases": []}
    for row in rows:
        result = {"id": row["id"], "scope": row["scope"], "source_kind": row["source_kind"],
                  "source_sha256": {source: hashlib.sha256((ROOT / source).read_bytes()).hexdigest()
                                    for source in row["sources"]}}
        reason = preflight(row, os.environ, args.include_live)
        if reason:
            result.update(status="blocked", reason=reason)
        else:
            command = row["command"]
            started = time.monotonic()
            log = output / (row["id"] + ".log")
            try:
                completed = run_command(command, ROOT / row.get("cwd", "."), row.get("timeout_seconds", 300))
                text = completed.stdout
                result.update(summarize(text, completed.returncode, row.get("format", "go-json")))
                result["exit_code"] = completed.returncode
            except subprocess.TimeoutExpired as error:
                text = error.stdout or ""
                if isinstance(text, bytes):
                    text = text.decode("utf-8", errors="replace")
                result.update(status="failed", reason="Command exceeded its bounded execution time.")
            except OSError as error:
                text = str(error)
                result.update(status="blocked", reason="Required command could not be started.")
            log.write_text(text)
            result.update(command=command, elapsed_seconds=round(time.monotonic()-started, 3), log=log.name,
                          log_sha256=hashlib.sha256(log.read_bytes()).hexdigest())
        report["cases"].append(result)
        (output / "results.json").write_text(json.dumps(report, indent=2) + "\n")
        print(f'{row["id"]}: {result["status"]}', flush=True)
    report["completed_at"] = dt.datetime.now(dt.timezone.utc).isoformat()
    report["summary"] = {status: sum(row["status"] == status for row in report["cases"])
                         for status in ["passed", "failed", "partial", "blocked"]}
    (output / "results.json").write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report["summary"]))
    return 1 if report["summary"]["failed"] else 0


if __name__ == "__main__":
    sys.exit(main())
