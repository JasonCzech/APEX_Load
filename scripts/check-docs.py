#!/usr/bin/env python3
"""Validate APEX Load Markdown documentation links, anchors, inventories, and API route references."""

from __future__ import annotations

import json
import re
import subprocess
import sys
import urllib.parse
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
# Retired source snapshots preserve former contracts and are not current documentation.
NON_CURRENT_PREFIXES: tuple[Path, ...] = (Path("_retired"),)
ARCHIVE_PREFIX = Path("docs/archive")
INDEX_PATH = Path("docs/INDEX.md")
ARCH_DIAGRAM_PATH = Path("docs/architecture.html")
LINK_RE = re.compile(r"(?<!!)\[[^\]]+\]\(([^)]+)\)")
IMAGE_RE = re.compile(r"!\[[^\]]*\]\(([^)]+)\)")
FENCE_RE = re.compile(r"^\s*(`{3,}|~{3,})")
HEADING_RE = re.compile(r"^\s{0,3}#{1,6}\s+(.+?)\s*#*\s*$")
EXTERNAL_LINK_PREFIXES = ("http://", "https://", "mailto:", "app://")
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


def git_files(pattern: str | None = None, root: Path = ROOT) -> list[Path]:
    cmd = ["git", "ls-files", "--cached", "--others", "--exclude-standard"]
    if pattern:
        cmd.append(pattern)
    out = subprocess.check_output(cmd, cwd=root, text=True)
    return [Path(line) for line in out.splitlines() if line.strip()]


def active_markdown_files(root: Path = ROOT) -> list[Path]:
    return [
        p
        for p in git_files("*.md", root=root)
        if (root / p).is_file()
        and not any(p.is_relative_to(prefix) for prefix in NON_CURRENT_PREFIXES)
    ]


def route_checked_markdown_files(files: list[Path], root: Path = ROOT) -> list[Path]:
    checked: list[Path] = []
    for rel in files:
        if rel.is_relative_to(ARCHIVE_PREFIX):
            continue
        text = (root / rel).read_text(errors="ignore")
        if "Status: Roadmap" in text or "> Status: Roadmap" in text:
            continue
        if any(part in rel.name for part in ("BACKLOG", "FOLLOW_UP", "PLAN", "ROADMAP")):
            continue
        checked.append(rel)
    return checked


def strip_fenced_code(text: str) -> str:
    """Return text with fenced code blocks blanked out while preserving line numbers."""
    out: list[str] = []
    fence: str | None = None
    for line in text.splitlines():
        m = FENCE_RE.match(line)
        if m:
            marker = m.group(1)
            if fence is None:
                fence = marker[0] * len(marker)
                out.append("")
                continue
            if marker[0] == fence[0] and len(marker) >= len(fence):
                fence = None
                out.append("")
                continue
        out.append("" if fence is not None else line)
    return "\n".join(out)


def clean_link_raw(raw: str) -> str:
    target = raw.strip()
    if target.startswith("<") and target.endswith(">"):
        target = target[1:-1].strip()
    elif " " in target and ('"' in target or "'" in target):
        # Strip optional Markdown link title: (path/to/doc.md "Title")
        target = target.split(None, 1)[0]
    return target


def strip_link_target(raw: str) -> str:
    target = clean_link_raw(raw)
    target = target.split("#", 1)[0].split("?", 1)[0].strip()
    return urllib.parse.unquote(target)


def extract_link_fragment(raw: str) -> str:
    target = clean_link_raw(raw)
    if "#" not in target:
        return ""
    frag = target.split("#", 1)[1].split("?", 1)[0].strip()
    return urllib.parse.unquote(frag)


def plain_heading_text(raw_heading: str) -> str:
    text = raw_heading.strip()
    text = re.sub(r"!\[([^\]]*)\]\([^)]*\)", r"\1", text)
    text = re.sub(r"\[([^\]]+)\]\([^)]*\)", r"\1", text)
    text = re.sub(r"`([^`]*)`", r"\1", text)
    text = re.sub(r"<[^>]+>", "", text)
    text = re.sub(r"(\*\*|__)(.*?)\1", r"\2", text)
    text = re.sub(r"(?<!\w)(\*|_)(?!\s)(.+?)(?<!\s)\1(?!\w)", r"\2", text)
    return text.strip()


def heading_anchors_for_text(text: str) -> set[str]:
    """Compute valid GitHub and Help-renderer heading anchor slugs for Markdown text."""
    stripped = strip_fenced_code(text)
    anchors: set[str] = set()
    # Explicit HTML anchor targets (<a id="..."> or <h2 id="...">)
    for html_id in re.findall(r'\bid=["\']([^"\']+)["\']', stripped):
        anchors.add(html_id)

    gh_counts: dict[str, int] = {}
    help_counts: dict[str, int] = {}
    for line in stripped.splitlines():
        m = HEADING_RE.match(line)
        if not m:
            continue
        plain = plain_heading_text(m.group(1)).lower()
        if not plain:
            continue

        # 1. GitHub Flavored Markdown slug (keeps letters, digits, underscores, spaces->hyphens)
        gh_base = re.sub(r"[^\w -]", "", plain, flags=re.UNICODE).replace(" ", "-")
        if gh_base:
            n = gh_counts.get(gh_base, 0)
            gh_counts[gh_base] = n + 1
            anchors.add(f"{gh_base}-{n}" if n else gh_base)

        # 2. Dashboard Help renderer slug (collapses punctuation runs such as `/` to `-`)
        help_base = re.sub(r"[^a-z0-9]+", "-", plain).strip("-")
        if help_base:
            hn = help_counts.get(help_base, 0)
            help_counts[help_base] = hn + 1
            anchors.add(f"{help_base}-{hn}" if hn else help_base)
    return anchors


def check_local_links(files: list[Path], root: Path = ROOT) -> list[str]:
    problems: list[str] = []
    anchor_cache: dict[Path, set[str]] = {}

    def anchors_for(abs_path: Path) -> set[str]:
        resolved = abs_path.resolve()
        if resolved not in anchor_cache:
            anchor_cache[resolved] = heading_anchors_for_text(
                resolved.read_text(errors="ignore")
            )
        return anchor_cache[resolved]

    for rel in files:
        raw_text = (root / rel).read_text(errors="ignore")
        text = strip_fenced_code(raw_text)
        check_anchors = not rel.is_relative_to(ARCHIVE_PREFIX)
        for match in list(LINK_RE.finditer(text)) + list(IMAGE_RE.finditer(text)):
            raw = match.group(1).strip()
            if raw.startswith(EXTERNAL_LINK_PREFIXES) or raw.startswith("#/"):
                continue
            if raw.startswith("#"):
                if check_anchors:
                    frag = extract_link_fragment(raw)
                    if frag and frag not in anchors_for(root / rel):
                        problems.append(f"{rel}: missing heading anchor -> {raw}")
                continue
            target = strip_link_target(raw)
            if not target:
                continue
            candidate = (root / rel).parent / target
            if not candidate.exists():
                problems.append(f"{rel}: missing local link -> {raw}")
                continue
            frag = extract_link_fragment(raw)
            if (
                check_anchors
                and frag
                and candidate.is_file()
                and candidate.suffix.lower() == ".md"
            ):
                try:
                    cand_rel = candidate.resolve().relative_to(root.resolve())
                except ValueError:
                    cand_rel = None
                if cand_rel is not None and not cand_rel.is_relative_to(ARCHIVE_PREFIX):
                    if frag not in anchors_for(candidate):
                        problems.append(
                            f"{rel}: missing heading anchor #{frag} in {cand_rel} -> {raw}"
                        )
    return problems


def load_catalog(root: Path = ROOT) -> set[tuple[str, str]]:
    data = json.loads((root / "docs/api-endpoint-catalog.json").read_text())
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


def check_api_routes(files: list[Path], root: Path = ROOT) -> list[str]:
    catalog = load_catalog(root=root)
    problems: list[str] = []
    path_re = re.compile(r"`?(https?://[^/`\s|)]+)?(?P<path>/api/v1/[^`\s|),]+)")
    direct_re = re.compile(r"\b(GET|POST|PUT|PATCH|DELETE|WS)\s+(https?://[^/`\s|)]+)?(?P<path>/api/v1/[^`\s|),]+)")
    for rel in files:
        text = (root / rel).read_text(errors="ignore")
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


def check_stale_phrases(files: list[Path], root: Path = ROOT) -> list[str]:
    problems: list[str] = []
    for rel in files:
        if rel.is_relative_to(ARCHIVE_PREFIX):
            continue
        text = (root / rel).read_text(errors="ignore")
        for phrase in STALE_PHRASES:
            if phrase.lower() in text.lower():
                problems.append(f"{rel}: stale phrase remains: {phrase}")
    return problems


def index_linked_paths(root: Path = ROOT) -> set[Path]:
    """Repository-relative paths that docs/INDEX.md links to from a table row."""
    index_dir = (root / INDEX_PATH).parent
    resolved_root = root.resolve()
    linked: set[Path] = set()
    for line in (root / INDEX_PATH).read_text(errors="ignore").splitlines():
        if not line.lstrip().startswith("|"):
            continue
        for match in LINK_RE.finditer(line):
            raw = match.group(1).strip()
            if raw.startswith(EXTERNAL_LINK_PREFIXES) or raw.startswith("#"):
                continue
            target = strip_link_target(raw)
            if not target:
                continue
            try:
                linked.add((index_dir / target).resolve().relative_to(resolved_root))
            except ValueError:
                continue
    return linked


def requires_index_entry(rel: Path) -> bool:
    if rel == INDEX_PATH:
        return False
    if rel.parent == Path("docs"):
        return True
    if rel.parent == Path("docs/guides"):
        return True
    if rel.is_relative_to(Path("deployments")) and rel.name == "README.md":
        return True
    return False


def check_index_membership(files: list[Path], root: Path = ROOT) -> list[str]:
    """Ensure docs/*.md, docs/guides/*.md, and deployments/**/README.md appear in docs/INDEX.md tables."""
    if not (root / INDEX_PATH).exists():
        return [f"{INDEX_PATH}: canonical documentation index is missing"]
    linked = index_linked_paths(root=root)
    problems: list[str] = []
    for rel in sorted(files):
        if not requires_index_entry(rel):
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


def agents_tree_section(block: str, header: str) -> list[str]:
    """The lines of an indented top-level subtree (such as `pkg/` or `cmd/`) in Project Structure."""
    lines = block.splitlines()
    start = None
    for index, line in enumerate(lines):
        if line.strip() == header and not line.startswith((" ", "\t")):
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


def agents_pkg_section(block: str) -> list[str]:
    return agents_tree_section(block, "pkg/")


def agents_section_entries(section: list[str]) -> set[str]:
    """Directory names that a Project Structure subtree gives their own entry line."""
    entries: set[str] = set()
    for line in section:
        token = line.strip().split()[0] if line.strip() else ""
        if token.endswith("/") and token.count("/") == 1:
            entries.add(token[:-1])
    return entries


def agents_pkg_entries(section: list[str]) -> set[str]:
    return agents_section_entries(section)


def check_package_inventory(root: Path = ROOT) -> list[str]:
    """Ensure AGENTS.md Project Structure lists every top-level cmd/* and pkg/* directory."""
    agents_rel = Path("AGENTS.md")
    if not (root / agents_rel).exists():
        return [f"{agents_rel}: canonical agent context is missing"]
    block = agents_structure_block((root / agents_rel).read_text(errors="ignore"))
    if not block:
        return [f"{agents_rel}: Project Structure code block not found"]
    problems: list[str] = []
    for tree_name in ("cmd", "pkg"):
        section = agents_tree_section(block, f"{tree_name}/")
        entries = agents_section_entries(section)
        if not entries:
            problems.append(
                f"{agents_rel}: {tree_name}/ section not found in the Project Structure tree"
            )
            continue
        tree_root = root / tree_name
        if not tree_root.is_dir():
            continue
        for sub_dir in sorted(p for p in tree_root.iterdir() if p.is_dir()):
            if sub_dir.name not in entries:
                problems.append(
                    f"{agents_rel}: {tree_name}/{sub_dir.name}/ is missing from the Project Structure tree"
                )
    return problems


def check_architecture_diagram_refs(root: Path = ROOT) -> list[str]:
    """Ensure docs/*.md and docs/guides/*.md paths cited in docs/architecture.html exist."""
    diagram = root / ARCH_DIAGRAM_PATH
    if not diagram.is_file():
        return []
    text = diagram.read_text(errors="ignore")
    problems: list[str] = []
    for ref in sorted(set(re.findall(r"\bdocs/[A-Za-z0-9_./-]+\.md\b", text))):
        if not (root / ref).is_file():
            problems.append(f"{ARCH_DIAGRAM_PATH}: referenced doc does not exist -> {ref}")
    return problems


def check_prohibited_repo_artifacts(all_files: list[Path]) -> list[str]:
    """Reject raw QA evidence captures, screenshots under docs/qa/, and binary .docx under docs/archive/."""
    problems: list[str] = []
    qa_reviews = Path("docs/qa/reviews")
    qa_root = Path("docs/qa")
    for rel in sorted(all_files):
        if rel.is_relative_to(qa_reviews) and rel.name == "error-context.md":
            problems.append(
                f"{rel}: Playwright error-context.md capture must remain gitignored, not committed"
            )
        elif rel.is_relative_to(qa_reviews) and "evidence" in rel.parts:
            problems.append(
                f"{rel}: raw QA review evidence under docs/qa/reviews/**/evidence/ must not be tracked"
            )
        elif rel.is_relative_to(qa_root) and rel.suffix.lower() == ".png":
            problems.append(
                f"{rel}: PNG screenshots under docs/qa/ must remain untracked/gitignored"
            )
        elif rel.is_relative_to(ARCHIVE_PREFIX) and rel.suffix.lower() == ".docx":
            problems.append(
                f"{rel}: binary .docx artifacts under docs/archive/ are prohibited"
            )
    return problems


def main() -> int:
    files = active_markdown_files()
    all_files = [p for p in git_files() if (ROOT / p).is_file()]
    problems = []
    problems.extend(check_local_links(files))
    problems.extend(check_api_routes(route_checked_markdown_files(files)))
    problems.extend(check_stale_phrases(files))
    problems.extend(check_index_membership(files))
    problems.extend(check_package_inventory())
    problems.extend(check_architecture_diagram_refs())
    problems.extend(check_prohibited_repo_artifacts(all_files))
    if problems:
        for problem in problems:
            print(problem)
        return 1
    print(f"Documentation checks passed for {len(files)} active Markdown files.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
