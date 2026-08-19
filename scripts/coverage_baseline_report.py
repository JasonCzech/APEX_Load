#!/usr/bin/env python3
"""Turn a Go coverage profile into a per-package statement-coverage baseline.

Emits the versioned-JSON-plus-deterministic-Markdown shape the repository
already uses for performance evidence (docs/PERFORMANCE_BENCHMARK_EVIDENCE.md),
so the coverage ratchet has a stable artifact to compare against.

Usage: coverage_baseline_report.py PROFILE OUT PKGLIST STATUS

The profile is parsed directly rather than going through `go tool cover -func`.
That tool reports coverage per *function*, which is a materially different
number from the statement coverage in its own `total:` line: a package where
every function is entered once but almost no branch is exercised scores near
100% by function and far lower by statement. A ratchet fed the function figure
would barely move while real coverage eroded, so this report deals in
statements only, at both package and repository level.

PKGLIST is a tab-separated `go list` inventory containing import path and an
explicit true/false derived from TestGoFiles/XTestGoFiles. Profile presence is
not a valid proxy: `go test -coverprofile` emits zero-count blocks even for a
package with no tests. Untested packages retain any known statement totals but
are emitted with has_tests=false so changed-line enforcement cannot mistake
instrumentation for a test suite.
"""
import collections
import datetime
import json
import pathlib
import subprocess
import sys

SCHEMA_VERSION = 3
RATCHET_EXCLUDED_PACKAGES = {"api/proto"}


def module_path() -> str:
    try:
        return subprocess.run(["go", "list", "-m"], capture_output=True,
                              text=True, check=True).stdout.strip()
    except (subprocess.CalledProcessError, FileNotFoundError, OSError):
        return ""


def parse_profile(path: pathlib.Path):
    """Return {package: (statements_total, statements_covered)}.

    Profile lines look like:
        import/path/file.go:12.34,15.2 3 1
    i.e. <location> <numStatements> <executionCount>. Identical blocks can
    appear more than once when profiles are merged, so blocks are aggregated by
    location and a block counts as covered if any occurrence executed.
    """
    blocks: dict[str, tuple[str, int, int]] = {}
    if not path.exists():
        return {}
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("mode:"):
            continue
        parts = line.rsplit(" ", 2)
        if len(parts) != 3:
            continue
        location, num_stmt, count = parts
        try:
            num_stmt_i, count_i = int(num_stmt), int(count)
        except ValueError:
            continue
        file_path = location.split(":", 1)[0]
        # Filter here as well as at the package-list stage. The profile is not
        # always produced by this repository's own script: CI runs `go test
        # ./...`, which happily instruments the vendored third-party Go under
        # node_modules. Without this, feeding that profile in would silently
        # reintroduce the leak the package-list filter exists to prevent.
        if "/node_modules/" in file_path:
            continue
        pkg = str(pathlib.PurePosixPath(file_path).parent)
        prev = blocks.get(location)
        if prev is None:
            blocks[location] = (pkg, num_stmt_i, count_i)
        else:
            blocks[location] = (prev[0], prev[1], prev[2] + count_i)

    totals: dict[str, list[int]] = collections.defaultdict(lambda: [0, 0])
    for pkg, num_stmt, count in blocks.values():
        totals[pkg][0] += num_stmt
        if count > 0:
            totals[pkg][1] += num_stmt
    return {pkg: (t[0], t[1]) for pkg, t in totals.items()}


def ratchet_totals(per_pkg: dict[str, tuple[int, int]], relative) -> tuple[int, int]:
    included = (
        values for pkg, values in per_pkg.items()
        if relative(pkg) not in RATCHET_EXCLUDED_PACKAGES
    )
    totals = list(included)
    return sum(value[0] for value in totals), sum(value[1] for value in totals)


def package_inventory(path: pathlib.Path | None) -> dict[str, bool]:
    inventory: dict[str, bool] = {}
    if path is None or not path.exists():
        return inventory
    for line in path.read_text().splitlines():
        fields = line.split("\t")
        package = fields[0].strip()
        if not package:
            continue
        if len(fields) != 2 or fields[1] not in {"true", "false"}:
            raise ValueError(f"invalid package inventory row: {line!r}")
        inventory[package] = fields[1] == "true"
    return inventory


def main() -> int:
    if len(sys.argv) < 3:
        print(__doc__, file=sys.stderr)
        return 2

    profile = pathlib.Path(sys.argv[1])
    out = pathlib.Path(sys.argv[2])
    pkglist = pathlib.Path(sys.argv[3]) if len(sys.argv) > 3 else None
    status = sys.argv[4] if len(sys.argv) > 4 else "unknown"

    per_pkg = parse_profile(profile)
    mod = module_path()

    def relative(pkg: str) -> str:
        return pkg[len(mod) + 1:] if mod and pkg.startswith(mod + "/") else pkg

    try:
        inventory = package_inventory(pkglist)
    except ValueError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2

    packages = []
    all_packages = sorted(set(per_pkg) | set(inventory))
    for pkg in all_packages:
        totals = per_pkg.get(pkg)
        stmts, covered = totals if totals is not None else (None, 0)
        has_tests = inventory.get(pkg, False)
        packages.append({
            "package": relative(pkg),
            "has_tests": has_tests,
            "statements_total": stmts,
            "statements_covered": covered,
            "statement_coverage_pct": round(100.0 * covered / stmts, 2) if stmts else None,
        })

    untested = [relative(pkg) for pkg, has_tests in inventory.items() if not has_tests]

    grand_total, grand_covered = ratchet_totals(per_pkg, relative)
    overall = round(100.0 * grand_covered / grand_total, 2) if grand_total else None

    report = {
        "schema_version": SCHEMA_VERSION,
        "generated_at": datetime.datetime.now(datetime.timezone.utc)
                                .replace(microsecond=0).isoformat(),
        "test_run_status": status,
        "metric": "statement_coverage",
        "ratchet_excluded_packages": sorted(RATCHET_EXCLUDED_PACKAGES),
        "note": ("Baseline for the coverage ratchet. Every percentage here is STATEMENT coverage, "
                 "at both package and repository level; function coverage is deliberately not "
                 "reported, because ratcheting on it would gate on a signal that barely moves. "
                 "Package selection comes from `go list` with node_modules excluded, so neither "
                 "the duplicate checkout under .claude/worktrees/ nor vendored third-party Go is "
                 "counted. Generated api/proto code remains visible per-package but is excluded "
                 "from the ratcheted overall total. Packages with no test file remain in the "
                 "overall statement total and are marked has_tests=false; their statement total "
                 "is null only when the profile contains no executable blocks for that package. "
                 "Test presence comes from go list TestGoFiles/XTestGoFiles rather than profile presence."),
        "overall_statement_coverage_pct": overall,
        "statements_total": grand_total,
        "statements_covered": grand_covered,
        "packages_total": len(packages),
        "packages_without_tests": len(untested),
        "packages": packages,
    }

    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(report, indent=2) + "\n")

    tested = [p for p in packages if p["has_tests"] and p["statement_coverage_pct"] is not None]
    thinnest = sorted(tested, key=lambda p: (p["statement_coverage_pct"], -p["statements_total"]))
    lines = [
        "# Go Coverage Baseline",
        "",
        f"Generated `{report['generated_at']}` · schema v{SCHEMA_VERSION} · test run **{status}**",
        "",
        (f"**Overall statement coverage: {overall}%** "
         f"({grand_covered:,} of {grand_total:,} statements) across {len(packages)} packages, "
         f"{len(untested)} of them with no test file."
         if overall is not None else "**Overall statement coverage: unavailable**"),
        "",
        "Every figure below is statement coverage. Package selection comes from `go list` with",
        "`node_modules` excluded.",
        "",
        "## Packages with no test file",
        "",
    ]
    if untested:
        lines += [f"- `{pkg}`" for pkg in sorted(untested)]
    else:
        lines.append("_None._")
    lines += [
        "",
        "## Thinnest 25 packages by statement coverage",
        "",
        "| Package | Covered | Total | % |",
        "|---|---:|---:|---:|",
    ]
    for p in thinnest[:25]:
        lines.append(f"| `{p['package']}` | {p['statements_covered']} | "
                     f"{p['statements_total']} | {p['statement_coverage_pct']} |")
    lines.append("")
    out.with_suffix(".md").write_text("\n".join(lines))

    print(f"{len(packages)} packages ({len(untested)} untested), "
          f"overall {overall}% statements, run {status}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
