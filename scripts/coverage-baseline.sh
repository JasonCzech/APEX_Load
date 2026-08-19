#!/usr/bin/env bash
# Capture per-package Go coverage as a machine-readable baseline for the test
# program's coverage ratchet (docs/qa/TEST_PROGRAM.md, M0).
#
# Two exclusions matter and are easy to get wrong:
#   * .claude/worktrees/ holds a full duplicate checkout, so package selection
#     comes from `go list`, never from a bare `find` over the tree.
#   * node_modules ships vendored third-party Go (flatted/golang), which
#     `go list ./...` happily returns. It is not our code and must not sit in
#     our baseline.
#
# The failure path gets the same care as the success path, because a coverage
# run that fails is the run whose output you most need: the test log is kept
# rather than deleted, and the artifact is still written with
# test_run_status=fail so the failure is recorded instead of swallowed.
#
# Usage:
#   scripts/coverage-baseline.sh [output.json]
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

out="${1:-docs/qa/evidence/M0/coverage-baseline.json}"
outdir="$(dirname "$out")"
mkdir -p "$outdir"

# The raw profile is a multi-megabyte intermediate, not evidence — the JSON and
# Markdown derived from it are. It goes to a temp dir so it never lands in the
# repository. The test log is the opposite: it is exactly the artifact you need
# when a run fails, so it is retained alongside the report.
workdir="$(mktemp -d -t apexload-coverage.XXXXXX)"
trap 'rm -rf "$workdir"' EXIT
profile="$workdir/coverage.profile"
pkglist="$workdir/packages.txt"
testlog="$outdir/coverage-test.log"

go list -f '{{.ImportPath}}{{"\t"}}{{if or (gt (len .TestGoFiles) 0) (gt (len .XTestGoFiles) 0)}}true{{else}}false{{end}}' ./... \
  | grep -v '/node_modules/' > "$pkglist"

packages=()
while IFS=$'\t' read -r pkg _has_tests; do
  # A plain `[[ -n "$pkg" ]] && packages+=(...)` would be wrong here: under
  # `set -e` an AND-list whose left side is false returns non-zero, and as a
  # standalone statement in a loop body that aborts the script. One blank line
  # would silently end the run.
  if [[ -n "$pkg" ]]; then
    packages+=("$pkg")
  fi
done < "$pkglist"

if [[ ${#packages[@]} -eq 0 ]]; then
  echo 'ERROR: no packages to test; "go list ./..." returned nothing usable' >&2
  exit 1
fi

echo "Running coverage over ${#packages[@]} packages (this takes a while)..." >&2

# Pass the package list as a single argv rather than piping it through xargs:
# xargs is free to split an over-long list into several invocations, and each
# one would rewrite -coverprofile, silently discarding the earlier batches.
status="pass"
if ! go test -coverprofile="$profile" -covermode=atomic -count=1 \
       "${packages[@]}" > "$testlog" 2>&1; then
  status="fail"
  echo "WARNING: go test reported failures; baseline records status=fail" >&2
  echo "Full log retained at $testlog" >&2
  grep -E '^(FAIL|--- FAIL|panic:)' "$testlog" | head -20 >&2 || true
fi

if [[ ! -s "$profile" ]]; then
  # A compile error produces no profile at all. Still emit the artifact so the
  # failure lands on the record, instead of the script dying on a missing file
  # and reporting a cover-tool error in place of the real one.
  echo "WARNING: no coverage profile produced; emitting an empty baseline" >&2
  : > "$profile"
fi

python3 "$repo_root/scripts/coverage_baseline_report.py" \
  "$profile" "$out" "$pkglist" "$status"

echo "Wrote $out (test run: $status)" >&2
[[ "$status" == "pass" ]]
