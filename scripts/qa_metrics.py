#!/usr/bin/env python3
"""Render QA headline metrics from tracked files and committed evidence."""

from __future__ import annotations

import json
from pathlib import Path
import re
import subprocess


def tracked_count(root: Path, pattern: str) -> int:
    output = subprocess.run(
        ["git", "-C", str(root), "ls-files", "-z", pattern], check=True, text=True, capture_output=True
    ).stdout
    return sum(
        1 for name in output.split("\0")
        if name and Path(name).parts[0] != "_retired" and (root / name).is_file()
    )


def packages_without_tests(root: Path) -> int:
    output = subprocess.run(
        [
            "go", "list", "-f",
            "{{.ImportPath}}{{\"\\t\"}}{{if or (gt (len .TestGoFiles) 0) (gt (len .XTestGoFiles) 0)}}true{{else}}false{{end}}",
            "./...",
        ],
        cwd=root,
        check=True,
        text=True,
        capture_output=True,
    ).stdout
    return sum(
        1 for line in output.splitlines()
        if "/node_modules/" not in line and "/artifacts/" not in line and line.endswith("\tfalse")
    )


def tier0_states(matrix: str) -> dict[str, int]:
    result = {"GREEN": 0, "PARTIAL": 0, "MISSING": 0}
    for state in result:
        result[state] = len(re.findall(rf"^\| S\d+ \|.*\| {state} \|", matrix, re.MULTILINE))
    return result


def open_inherited_defects(defects: str) -> int:
    section = defects.split("## Found by this program", 1)[0]
    return len(re.findall(r"^\| DEF-\d+ \|.*\| (?:OPEN|IN_PROGRESS|BLOCKED) \|", section, re.MULTILINE))


def render_table(root: Path) -> list[str]:
    qa = root / "docs" / "qa"
    baseline = json.loads((qa / "evidence" / "M0" / "coverage-baseline.json").read_text())
    matrix_states = tier0_states((qa / "E2E_SUITES.md").read_text())
    ci = (root / ".github" / "workflows" / "ci.yml").read_text()
    coverage_step = ci.split("- name: Coverage ratchet", 1)[1].split("- name:", 1)[0]
    coverage_enforced = "continue-on-error: true" not in coverage_step
    enabled_linters = set(re.findall(r"^    - ([a-z0-9]+)", (root / ".golangci.yml").read_text(), re.MULTILINE))
    leak_enabled = len(enabled_linters & {"bodyclose", "sqlclosecheck", "rowserrcheck", "nilerr"})
    performance = (root / "docs" / "PERFORMANCE_BENCHMARKS.md").read_text()
    measured_perf = 4 - len(re.findall(r"\| `not_evaluated` \|", performance))
    return [
        "| Metric | Baseline | Current | Target |",
        "|---|---:|---:|---:|",
        f"| Performance targets measured | 0 | {measured_perf} | 4 |",
        "| AKS cutover gates passed | 0 | 0 | 10 |",
        f"| Tier-0 assertions GREEN | 0 | {matrix_states['GREEN']} | 12 |",
        f"| Tier-0 assertions PARTIAL/MISSING | 12 | {matrix_states['PARTIAL'] + matrix_states['MISSING']} | 0 |",
        f"| Playwright spec files | 3 | {tracked_count(root, 'dashboard/e2e/*.spec.ts')} | assertion-driven |",
        f"| Go statement coverage | {baseline.get('overall_statement_coverage_pct', 'unknown')}% | {baseline.get('overall_statement_coverage_pct', 'unknown')}% | ratchet |",
        f"| Go coverage floor enforced | 0 | {1 if coverage_enforced else 0} | 1 |",
        f"| Packages with no test file | {baseline.get('packages_without_tests', 'unknown')} | {packages_without_tests(root)} | 3 |",
        f"| Open inherited review defects | 11 | {open_inherited_defects((qa / 'DEFECTS.md').read_text())} | 0 |",
        f"| Resource-leak linters enabled | 0 | {leak_enabled} | 1 |",
        f"| Converter golden files | 0 | {tracked_count(root, '*.golden')} | all registered converters |",
        "| Longest clean soak | none | none | 72 h on AKS |",
    ]
