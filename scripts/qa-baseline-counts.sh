#!/usr/bin/env bash
# Emit the reference counts quoted by the test program's tracker
# (docs/qa/STATUS.md, docs/qa/TEST_PROGRAM.md).
#
# This exists because those counts were first gathered ad hoc and promptly
# disagreed with each other: three files stated three different values for the
# number of dashboard test files, and the lowest was wrong because a `**` glob
# silently skipped everything sitting directly under dashboard/src/. Deriving
# them from one command is the only way they stay consistent.
#
# Everything counts git-tracked files only. A bare `find` would also sweep the
# full duplicate checkout under .claude/worktrees/ and roughly double the totals.
#
# Usage:
#   scripts/qa-baseline-counts.sh          # human-readable table
#   scripts/qa-baseline-counts.sh --json   # machine-readable
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

go_test_files=$(git ls-files '*_test.go' | wc -l | tr -d ' ')
go_src_files=$(git ls-files '*.go' | grep -vc '_test\.go$' || true)
go_test_funcs=$(git grep -h '^func Test' -- '*_test.go' | wc -l | tr -d ' ')
# Note the pathspec: 'dashboard/src/' matches the whole subtree, including files
# at its root. A '**/*.test.ts' glob does not, and undercounts by 16.
dashboard_tests=$(git ls-files 'dashboard/src/' | grep -Ec '\.test\.tsx?$' || true)
playwright_specs=$(git ls-files 'dashboard/e2e/*.spec.ts' | wc -l | tr -d ' ')
golden_files=$(git ls-files '*.golden' | wc -l | tr -d ' ')
testdata_files=$(git ls-files '*/testdata/*' | wc -l | tr -d ' ')
# Counted from the workflow itself rather than quoted from a doc. This number
# was stated as 14 for a while because a hand-built list omitted a job.
ci_jobs=$(python3 -c "import yaml;print(len(yaml.safe_load(open('.github/workflows/ci.yml'))['jobs']))" 2>/dev/null || echo "unknown")

if [[ "${1:-}" == "--json" ]]; then
  cat <<JSON
{
  "generated_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "source": "git ls-files (tracked files only; excludes .claude/worktrees/)",
  "go_test_files": $go_test_files,
  "go_source_files": $go_src_files,
  "go_test_functions": $go_test_funcs,
  "dashboard_test_files": $dashboard_tests,
  "playwright_specs": $playwright_specs,
  "golden_files": $golden_files,
  "testdata_files": $testdata_files,
  "ci_jobs": $ci_jobs
}
JSON
else
  printf '%-28s %s\n' \
    "Go test files"          "$go_test_files" \
    "Go source files"        "$go_src_files" \
    "Go test functions"      "$go_test_funcs" \
    "Dashboard test files"   "$dashboard_tests" \
    "Playwright specs"       "$playwright_specs" \
    "Golden files"           "$golden_files" \
    "testdata/ fixtures"     "$testdata_files" \
    "CI jobs in ci.yml"      "$ci_jobs"
fi
