#!/usr/bin/env python3
"""Compare a fresh coverage report against the committed baseline and fail on regression.

The rule is a ratchet: coverage may rise and may hold, but it may not fall.
Overall and per-package figures are both checked, because an overall number can
stay flat while one package quietly collapses and another grows.

Usage:
  coverage-ratchet.py CURRENT.json [--baseline docs/qa/evidence/M0/coverage-baseline.json]
                                   [--tolerance 0.5] [--overall-tolerance 0.1]
                                   [--update]

  --update rewrites the baseline from CURRENT instead of comparing. Use it when
  a drop is deliberate and justified; the diff makes the decision reviewable.

Exit codes: 0 pass, 1 regression, 2 usage/data error.

Two policy decisions are encoded here and are meant to be argued with rather
than inherited silently — see EXCLUDED_PACKAGES.
"""
import argparse
import json
import pathlib
import sys

# Generated code. `api/proto` is 1,652 protobuf statements at 0%; testing it
# would exercise protoc's output rather than this repository's code, and
# including it permanently depresses the headline while rewarding nothing.
# Excluding it is a defensible choice but NOT a silent one: it is listed here so
# the decision is visible in review, and removing a line re-enables the package.
#
# Note what is deliberately absent: cmd/. Six cmd/ mains sit at 0% and
# cmd/generator (26.7%) and cmd/controller (44.4%) are among the least-covered
# real code in the repository. A ratchet blind to cmd/ would never notice the
# controller binary regressing, so they stay in scope. The ratchet only forbids
# decreases, so including a low package costs nothing today and protects it.
EXCLUDED_PACKAGES = {
    "api/proto",
}


def load(path: pathlib.Path) -> dict:
    try:
        return json.loads(path.read_text())
    except FileNotFoundError:
        sys.exit(f"error: {path} not found")
    except json.JSONDecodeError as exc:
        sys.exit(f"error: {path} is not valid JSON: {exc}")


def coverage_map(report: dict) -> dict[str, float]:
    """package -> statement coverage, skipping excluded and untested packages."""
    out = {}
    for pkg in report.get("packages", []):
        name = pkg.get("package")
        pct = pkg.get("statement_coverage_pct")
        if name in EXCLUDED_PACKAGES or pct is None or not pkg.get("has_tests"):
            continue
        out[name] = float(pct)
    return out


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("current", type=pathlib.Path)
    parser.add_argument("--baseline", type=pathlib.Path,
                        default=pathlib.Path("docs/qa/evidence/M0/coverage-baseline.json"))
    parser.add_argument("--tolerance", type=float, default=0.5,
                        help="per-package percentage points a package may drop (default 0.5)")
    parser.add_argument("--overall-tolerance", type=float, default=0.1,
                        help="percentage points the overall figure may drop (default 0.1)")
    parser.add_argument("--update", action="store_true",
                        help="rewrite the baseline from CURRENT instead of comparing")
    args = parser.parse_args()

    current = load(args.current)

    # Neither comparison nor re-baselining may bless a partial/unknown run.
    # In particular, --update is the final step that turns a report into the
    # source of truth, so accepting a failed report there would permanently
    # disable the ratchet on the next invocation.
    status = current.get("test_run_status")
    if status != "pass":
        print("error: the current run's tests did not pass; only a report with "
              "test_run_status=pass may be compared or committed as a baseline",
              file=sys.stderr)
        return 2

    if args.update:
        companion = args.current.with_suffix(".md")
        try:
            companion_text = companion.read_text()
        except OSError as exc:
            print(f"error: cannot update baseline without the generated Markdown companion "
                  f"{companion}: {exc}", file=sys.stderr)
            return 2
        args.baseline.parent.mkdir(parents=True, exist_ok=True)
        args.baseline.write_text(json.dumps(current, indent=2) + "\n")
        args.baseline.with_suffix(".md").write_text(companion_text)
        print(f"baseline JSON and Markdown updated from {args.current} and {companion}")
        return 0

    baseline = load(args.baseline)

    if baseline.get("test_run_status") != "pass":
        print("error: the committed coverage baseline was not produced by a passing test run; "
              "replace it with a green CI artifact before enabling the ratchet",
              file=sys.stderr)
        return 2

    # A failed test run produces a partial profile: packages that crashed report
    # less coverage than they truly have. Comparing against that would raise a
    # wall of false regressions and teach everyone to ignore this gate.
    base_pkgs, cur_pkgs = coverage_map(baseline), coverage_map(current)

    regressions, new_pkgs, dropped = [], [], []
    for name, base_pct in sorted(base_pkgs.items()):
        if name not in cur_pkgs:
            dropped.append(name)
            continue
        delta = cur_pkgs[name] - base_pct
        if delta < -args.tolerance:
            regressions.append((name, base_pct, cur_pkgs[name], delta))
    for name in sorted(cur_pkgs):
        if name not in base_pkgs:
            new_pkgs.append(name)

    base_overall = baseline.get("overall_statement_coverage_pct")
    cur_overall = current.get("overall_statement_coverage_pct")
    overall_regressed = (
        base_overall is not None and cur_overall is not None
        and (cur_overall - base_overall) < -args.overall_tolerance
    )

    if cur_overall is not None and base_overall is not None:
        print(f"overall: {base_overall}% -> {cur_overall}% "
              f"({cur_overall - base_overall:+.2f} pp)")
    if new_pkgs:
        print(f"new packages (not yet in the baseline): {', '.join(new_pkgs)}")

    if not regressions and not overall_regressed and not dropped:
        print(f"coverage ratchet OK — {len(cur_pkgs)} packages compared, no regression")
        return 0

    # Flush the summary above before switching streams, or CI logs interleave it
    # after the failure block and read as though the run recovered.
    sys.stdout.flush()
    print("\nCOVERAGE RATCHET FAILED", file=sys.stderr)
    if overall_regressed:
        print(f"  overall fell {base_overall}% -> {cur_overall}% "
              f"(tolerance {args.overall_tolerance} pp)", file=sys.stderr)
    for name, was, now, delta in sorted(regressions, key=lambda r: r[3]):
        print(f"  {name}: {was}% -> {now}% ({delta:+.2f} pp)", file=sys.stderr)
    if dropped:
        # A package in the baseline that produces no coverage now has either lost
        # every test or been deleted. Treating that as informational would leave
        # the most direct way to lower coverage — removing the tests — as the one
        # route straight through the gate.
        print(f"  these packages were covered in the baseline but produced no coverage in this run,",
              file=sys.stderr)
        print(f"  which means their tests were removed or the package was deleted:", file=sys.stderr)
        for name in dropped:
            print(f"    {name} (baseline {base_pkgs[name]}%)", file=sys.stderr)
    print("\nAdd tests, or if the drop is deliberate re-baseline with:\n"
          f"  python3 scripts/coverage-ratchet.py {args.current} --update\n"
          "and explain why in the commit message.", file=sys.stderr)
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
