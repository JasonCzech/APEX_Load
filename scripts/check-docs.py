#!/usr/bin/env python3
"""Validate APEX Load Markdown documentation links and API route references."""

from __future__ import annotations

import json
import re
import subprocess
import sys
import urllib.parse
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
NON_CURRENT_PREFIXES = (Path("docs/archive/reviews"), Path("docs/reviews"))
METHODS = {"GET", "POST", "PUT", "PATCH", "DELETE", "WS"}
STALE_PHRASES = [
    "OpenTelemetry native",
    "OpenTelemetry-native",
    "future browser client metrics",
    "WebSocket Protocol (Not Yet Implemented)",
    "gRPC Protocol (Not Yet Implemented)",
    "Helm was documented but not implemented",
    "complete endpoint",
    "full endpoint catalogue",
    "Complete REST API",
]


def git_files(pattern: str) -> list[Path]:
    out = subprocess.check_output(
        ["git", "ls-files", "--cached", "--others", "--exclude-standard", pattern],
        cwd=ROOT,
        text=True,
    )
    return [Path(line) for line in out.splitlines() if line.strip()]


def active_markdown_files() -> list[Path]:
    return [
        p
        for p in git_files("*.md")
        if not any(p.is_relative_to(prefix) for prefix in NON_CURRENT_PREFIXES)
    ]


def route_checked_markdown_files(files: list[Path]) -> list[Path]:
    checked: list[Path] = []
    for rel in files:
        text = (ROOT / rel).read_text(errors="ignore")
        if "Status: Roadmap" in text or "> Status: Roadmap" in text:
            continue
        if any(part in rel.name for part in ("BACKLOG", "FOLLOW_UP", "PLAN", "ROADMAP")):
            continue
        checked.append(rel)
    return checked


def strip_link_target(raw: str) -> str:
    target = raw.strip()
    if target.startswith("<") and target.endswith(">"):
        target = target[1:-1]
    target = target.split("#", 1)[0].split("?", 1)[0].strip()
    return urllib.parse.unquote(target)


def check_local_links(files: list[Path]) -> list[str]:
    problems: list[str] = []
    link_re = re.compile(r"(?<!!)\[[^\]]+\]\(([^)]+)\)")
    image_re = re.compile(r"!\[[^\]]*\]\(([^)]+)\)")
    for rel in files:
        text = (ROOT / rel).read_text(errors="ignore")
        for match in list(link_re.finditer(text)) + list(image_re.finditer(text)):
            raw = match.group(1).strip()
            if raw.startswith(("#", "http://", "https://", "mailto:", "app://")):
                continue
            target = strip_link_target(raw)
            if not target:
                continue
            candidate = (ROOT / rel).parent / target
            if not candidate.exists():
                problems.append(f"{rel}: missing local link -> {raw}")
    return problems


def load_catalog() -> set[tuple[str, str]]:
    data = json.loads((ROOT / "docs/api-endpoint-catalog.json").read_text())
    return {(route["method"], route["path"]) for route in data["routes"]}


def route_matches(catalog: set[tuple[str, str]], method: str, path: str) -> bool:
    method = "GET" if method == "WS" else method
    path = path.split("?", 1)[0]
    path = re.split(r"[\]\"'<>]", path, maxsplit=1)[0]
    path = path.rstrip(".,;")
    parts = [part for part in path.split("/") if part]
    for route_method, route_path in catalog:
        if route_method != method:
            continue
        route_parts = [part for part in route_path.split("/") if part]
        if len(parts) != len(route_parts):
            continue
        matched = True
        for got, want in zip(parts, route_parts):
            if want.startswith("{") and want.endswith("}"):
                continue
            if got != want:
                matched = False
                break
        if matched:
            return True
    return False


def methods_before_path(line: str, start: int) -> list[str]:
    prefix = line[:start]
    found = [
        m
        for m in METHODS - {"WS"}
        if re.search(rf"(^|[\s|`/]){m}($|[\s|`/])", prefix)
    ]
    return sorted(found)


def check_api_routes(files: list[Path]) -> list[str]:
    catalog = load_catalog()
    problems: list[str] = []
    path_re = re.compile(r"`?(https?://[^/`\s|)]+)?(?P<path>/api/v1/[^`\s|),]+)")
    direct_re = re.compile(r"\b(GET|POST|PUT|PATCH|DELETE|WS)\s+(https?://[^/`\s|)]+)?(?P<path>/api/v1/[^`\s|),]+)")
    for rel in files:
        text = (ROOT / rel).read_text(errors="ignore")
        for lineno, line in enumerate(text.splitlines(), start=1):
            for match in direct_re.finditer(line):
                method = match.group(1)
                path = match.group("path")
                if not route_matches(catalog, method, path):
                    problems.append(f"{rel}:{lineno}: {method} {path} is not in generated endpoint catalog")
            if direct_re.search(line):
                continue
            for match in path_re.finditer(line):
                path = match.group("path")
                methods = methods_before_path(line, match.start())
                for method in methods:
                    if not route_matches(catalog, method, path):
                        problems.append(f"{rel}:{lineno}: {method} {path} is not in generated endpoint catalog")
    return problems


def check_stale_phrases(files: list[Path]) -> list[str]:
    problems: list[str] = []
    for rel in files:
        text = (ROOT / rel).read_text(errors="ignore")
        for phrase in STALE_PHRASES:
            if phrase.lower() in text.lower():
                problems.append(f"{rel}: stale phrase remains: {phrase}")
    return problems


def main() -> int:
    files = active_markdown_files()
    problems = []
    problems.extend(check_local_links(files))
    problems.extend(check_api_routes(route_checked_markdown_files(files)))
    problems.extend(check_stale_phrases(files))
    if problems:
        for problem in problems:
            print(problem)
        return 1
    print(f"Documentation checks passed for {len(files)} active Markdown files.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
