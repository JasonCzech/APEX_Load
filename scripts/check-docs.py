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
# Historical folders exempt from link/route checks. Currently none: docs/archive and docs/qa/reviews
# are validated like active docs (add a prefix here if archived text needs to stay untouched).
NON_CURRENT_PREFIXES: tuple[Path, ...] = ()
INDEX_PATH = Path("docs/INDEX.md")
LINK_RE = re.compile(r"(?<!!)\[[^\]]+\]\(([^)]+)\)")
EXTERNAL_LINK_PREFIXES = ("#", "http://", "https://", "mailto:", "app://")
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
    link_re = LINK_RE
    image_re = re.compile(r"!\[[^\]]*\]\(([^)]+)\)")
    for rel in files:
        text = (ROOT / rel).read_text(errors="ignore")
        for match in list(link_re.finditer(text)) + list(image_re.finditer(text)):
            raw = match.group(1).strip()
            if raw.startswith(EXTERNAL_LINK_PREFIXES):
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


def index_linked_paths() -> set[Path]:
    """Repository-relative paths that docs/INDEX.md links to from a table row.

    docs/INDEX.md states that every Markdown file directly under docs/ appears in
    one of its tables, because the table is what carries the Status/Purpose
    classification. Only links on table rows (lines starting with `|`) count, so a
    document dropped from every table cannot stay "listed" via a passing mention in
    prose.
    """
    index_dir = (ROOT / INDEX_PATH).parent
    linked: set[Path] = set()
    for line in (ROOT / INDEX_PATH).read_text(errors="ignore").splitlines():
        if not line.lstrip().startswith("|"):
            continue
        for match in LINK_RE.finditer(line):
            raw = match.group(1).strip()
            if raw.startswith(EXTERNAL_LINK_PREFIXES):
                continue
            target = strip_link_target(raw)
            if not target:
                continue
            try:
                linked.add((index_dir / target).resolve().relative_to(ROOT))
            except ValueError:
                continue
    return linked


def check_index_membership(files: list[Path]) -> list[str]:
    """docs/INDEX.md calls itself the canonical map; hold it to that claim.

    Every current Markdown file directly under docs/ (other than the index itself)
    must appear in one of the index tables — current guide, specialized reference,
    generated artifact, or roadmap/planning material — rather than being silently
    absent or mentioned only in prose.
    """
    if not (ROOT / INDEX_PATH).exists():
        return [f"{INDEX_PATH}: canonical documentation index is missing"]
    linked = index_linked_paths()
    problems: list[str] = []
    for rel in sorted(files):
        if rel == INDEX_PATH or rel.parent != INDEX_PATH.parent:
            continue
        if rel not in linked:
            problems.append(
                f"{rel}: not listed in a {INDEX_PATH} table, which claims to be the canonical documentation map"
            )
    return problems


def agents_structure_block(text: str) -> str:
    """The fenced Project Structure tree in AGENTS.md, or '' if absent."""
    match = re.search(r"## Project Structure\s*\n```(.*?)```", text, re.DOTALL)
    return match.group(1) if match else ""


def agents_pkg_section(block: str) -> list[str]:
    """The lines of the indented `pkg/` subtree in the Project Structure fence.

    The fence also lists cmd/, api/, dashboard/, desktop/, mcp/ and deployments/,
    whose entries collide by name with real packages (`cmd/controller/` vs
    `pkg/controller/`, the top-level `api/` block vs `pkg/api/`). Searching the
    whole fence therefore lets an unrelated line permanently satisfy the check for
    those packages, so scope the search to the pkg/ section itself.
    """
    lines = block.splitlines()
    start = None
    for index, line in enumerate(lines):
        if line.strip() == "pkg/" and not line.startswith((" ", "\t")):
            start = index + 1
            break
    if start is None:
        return []
    end = len(lines)
    for index in range(start, len(lines)):
        line = lines[index]
        if line.strip() and not line.startswith((" ", "\t")):
            end = index
            break
    return lines[start:end]


def agents_pkg_entries(section: list[str]) -> set[str]:
    """Directory names that the pkg/ section gives their own entry line.

    Only the first token of a line counts, so `pkg/api/` needs its own `api/`
    entry: a nested `api/static/` line documents a subpackage, not the package.
    """
    entries: set[str] = set()
    for line in section:
        token = line.strip().split()[0] if line.strip() else ""
        if token.endswith("/") and token.count("/") == 1:
            entries.add(token[:-1])
    return entries


def check_package_inventory() -> list[str]:
    """AGENTS.md is the canonical agent/platform context; its package tree must be complete.

    Half the pkg/ tree used to be missing from it, including the Java execution
    stack and the secret/disclosure packages, so an agent orienting from AGENTS.md
    could not see that those subsystems exist.
    """
    agents_rel = Path("AGENTS.md")
    if not (ROOT / agents_rel).exists():
        return [f"{agents_rel}: canonical agent context is missing"]
    block = agents_structure_block((ROOT / agents_rel).read_text(errors="ignore"))
    if not block:
        return [f"{agents_rel}: Project Structure code block not found"]
    entries = agents_pkg_entries(agents_pkg_section(block))
    if not entries:
        return [f"{agents_rel}: pkg/ section not found in the Project Structure tree"]
    problems: list[str] = []
    for pkg_dir in sorted(p for p in (ROOT / "pkg").iterdir() if p.is_dir()):
        if pkg_dir.name not in entries:
            problems.append(
                f"{agents_rel}: pkg/{pkg_dir.name}/ is missing from the Project Structure tree"
            )
    return problems


def main() -> int:
    files = active_markdown_files()
    problems = []
    problems.extend(check_local_links(files))
    problems.extend(check_api_routes(route_checked_markdown_files(files)))
    problems.extend(check_stale_phrases(files))
    problems.extend(check_index_membership(files))
    problems.extend(check_package_inventory())
    if problems:
        for problem in problems:
            print(problem)
        return 1
    print(f"Documentation checks passed for {len(files)} active Markdown files.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
