#!/usr/bin/env python3
"""Sample controller health through existing loopback pod port-forwards; never mutate a cluster."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import json
import math
import os
from pathlib import Path
import signal
import subprocess
import sys
import time


def validate(ports, seconds):
    if (not isinstance(ports, list) or not 1 <= len(ports) <= 8
            or any(type(port) is not int or not 1 <= port <= 65535 for port in ports)
            or len(set(ports)) != len(ports)):
        raise ValueError("require one to eight distinct loopback ports")
    if (type(seconds) not in (int, float) or not math.isfinite(seconds)
            or not 10 <= seconds <= 1800):
        raise ValueError("duration must be between 10 and 1800 seconds")


def probe(port):
    started = time.monotonic()
    try:
        # curl's total deadline covers the response body as well as headers.
        # Never follow redirects or inherit a proxy for these explicit loopbacks.
        result = subprocess.run([
            "curl", "--disable", "--silent", "--noproxy", "*", "--max-time", "2",
            "--output", os.devnull, "--write-out", "%{http_code}",
            "http://127.0.0.1:" + str(port) + "/api/v1/system/health"],
            capture_output=True, timeout=2.5, check=False)
        ok = result.returncode == 0 and result.stdout == b"200"
    except (OSError, subprocess.TimeoutExpired):
        ok = False
    return {"ok": ok, "seconds": time.monotonic() - started}


def probe_batch(ports):
    with ThreadPoolExecutor(max_workers=len(ports)) as pool:
        return list(pool.map(probe, ports))


def measure(ports, seconds, *, read=probe_batch, clock=time.monotonic, sleep=time.sleep):
    validate(ports, seconds)
    samples = []
    reason = None
    started = clock()
    try:
        while True:
            elapsed = clock() - started
            results = read(ports)
            if (len(results) != len(ports) or any(
                    not isinstance(row, dict) or type(row.get("ok")) is not bool
                    or type(row.get("seconds")) not in (int, float)
                    or not math.isfinite(row["seconds"]) or row["seconds"] < 0
                    for row in results)):
                raise ValueError("invalid probe result")
            samples.append({"elapsed_seconds": elapsed, "probes": [
                {"ok": row["ok"], "seconds": row["seconds"]} for row in results]})
            if clock() - started >= seconds + 5:
                reason = "window_deadline_exceeded"
                break
            if elapsed >= seconds:
                break
            # Do not issue bursts of catch-up probes after a slow response.
            sleep(max(0, min(started + seconds, started + elapsed + 1) - clock()))
    except KeyboardInterrupt:
        reason = "interrupted"
    except Exception:
        reason = "probe_failed"
    summaries = []
    for index, port in enumerate(ports):
        rows = [sample["probes"][index] for sample in samples]
        latencies = sorted(row["seconds"] for row in rows)
        p99 = latencies[math.ceil(.99 * len(latencies)) - 1] if latencies else None
        summaries.append({"port": port, "samples": len(rows),
                          "failures": sum(not row["ok"] for row in rows),
                          "p99_ms": p99 * 1000 if p99 is not None else None})
    coverage = bool(samples) and samples[0]["elapsed_seconds"] <= 1 and samples[-1]["elapsed_seconds"] >= seconds
    gaps = [b["elapsed_seconds"] - a["elapsed_seconds"] for a, b in zip(samples, samples[1:])]
    coverage = coverage and bool(gaps) and all(0 < gap <= 3 for gap in gaps)
    passed = reason is None and coverage and all(
        row["samples"] > 0 and row["failures"] == 0 and row["p99_ms"] < 500 for row in summaries)
    return {"passed": passed, "reason": reason, "requested_seconds": seconds,
            "elapsed_seconds": clock() - started, "coverage_passed": coverage,
            "scope": "HTTP liveness sampling only; pod identity, restarts and dependency outage require separate evidence",
            "controllers": summaries}, samples


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--ports", type=int, nargs="+", required=True)
    parser.add_argument("--seconds", type=float, default=600)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    try:
        validate(args.ports, args.seconds)
        output = Path(args.output)
        if not output.is_absolute():
            raise ValueError("output must be a new absolute directory")
        output.mkdir(mode=0o700, exist_ok=False)
        def interrupted(_signum, _frame):
            raise KeyboardInterrupt
        signal.signal(signal.SIGTERM, interrupted)
        report, samples = measure(args.ports, args.seconds)
        # The verdict is published last, after the supporting series is saved.
        for name, data in (("samples.json", samples), ("report.json", report)):
            fd = os.open(output / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, "w") as stream:
                json.dump(data, stream, indent=2)
                stream.write("\n")
        print("Liveness sampling passed" if report["passed"] else "Liveness sampling failed; inspect private evidence")
        return 0 if report["passed"] else 1
    except (ValueError, OSError):
        print("Cannot prepare or save private liveness evidence", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
