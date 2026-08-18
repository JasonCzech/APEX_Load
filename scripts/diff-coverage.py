#!/usr/bin/env python3
"""Enforce statement coverage on changed, testable Go lines."""

from __future__ import annotations

import argparse
from collections import defaultdict
from pathlib import Path
import re
import subprocess
import sys


EXCLUDED_PREFIXES = ("api/proto/",)
HUNK = re.compile(r"^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@")
BLOCK = re.compile(r"^(.+):(\d+)\.\d+,(\d+)\.\d+ (\d+) (\d+)$")


def changed_lines(diff: str) -> dict[str, set[int]]:
    result: dict[str, set[int]] = defaultdict(set)
    current = ""
    for line in diff.splitlines():
        if line.startswith("+++ b/"):
            current = line[6:]
            continue
        match = HUNK.match(line)
        if not current or not match:
            continue
        start = int(match.group(1))
        count = int(match.group(2) or "1")
        result[current].update(range(start, start + count))
    return result


def repository_path(profile_path: str, module: str) -> str:
    prefix = module.rstrip("/") + "/"
    return profile_path[len(prefix):] if profile_path.startswith(prefix) else profile_path


def score(profile: str, changed: dict[str, set[int]], module: str) -> tuple[int, int]:
    total = covered = 0
    for line in profile.splitlines():
        match = BLOCK.match(line)
        if not match:
            continue
        path = repository_path(match.group(1), module)
        if path.endswith("_test.go") or path.endswith(".pb.go") or path.startswith(EXCLUDED_PREFIXES):
            continue
        start, end, statements, count = map(int, match.groups()[1:])
        if not any(number in changed.get(path, set()) for number in range(start, end + 1)):
            continue
        total += statements
        if count > 0:
            covered += statements
    return covered, total


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("profile", type=Path)
    parser.add_argument("--base", default="HEAD^")
    parser.add_argument("--diff-file", type=Path)
    parser.add_argument("--module", default="github.com/jasonczech/apexload")
    parser.add_argument("--floor", type=float, default=80.0)
    args = parser.parse_args()

    try:
        profile = args.profile.read_text()
    except OSError as exc:
        print(f"error: read coverage profile: {exc}", file=sys.stderr)
        return 2
    if args.diff_file:
        diff = args.diff_file.read_text()
    else:
        base = args.base
        if not base or set(base) == {"0"}:
            base = "HEAD^"
        command = ["git", "diff", "--unified=0", f"{base}...HEAD", "--", "*.go"]
        completed = subprocess.run(command, text=True, capture_output=True)
        if completed.returncode:
            print(f"error: {' '.join(command)}: {completed.stderr.strip()}", file=sys.stderr)
            return 2
        diff = completed.stdout

    covered, total = score(profile, changed_lines(diff), args.module)
    if total == 0:
        print("diff coverage: no changed executable Go statements")
        return 0
    percent = covered * 100.0 / total
    print(f"diff coverage: {covered}/{total} statements = {percent:.2f}% (floor {args.floor:.2f}%)")
    if percent + 1e-9 < args.floor:
        print("DIFF COVERAGE FAILED: add tests for changed Go statements", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
