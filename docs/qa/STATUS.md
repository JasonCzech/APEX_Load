# Test Program — Status

Updated weekly. Scope is defined by [TEST_PROGRAM.md](TEST_PROGRAM.md); this file records only
*state*. Enforced by `status_inventory_test.go` — a milestone cannot be marked `DONE` without a
populated `evidence/M<n>/` directory.

**Program start:** 2026-08-18 · **Target completion:** 2027-05 · **Budget:** ~8 hrs/week

**Dashboard:** [DASHBOARD.md](DASHBOARD.md) is generated from this repository state and is the only
state-bearing dashboard. External published views may mirror it, but must not carry unique status.

## Milestones

| ID | Milestone | State | Hrs spent | Evidence |
|---|---|---|---|---|
| M0 | Instrument the program | IN_PROGRESS | not recorded | `evidence/M0/` |
| M1 | Tier-0 end-user journey | IN_PROGRESS | 10 | `evidence/M1/` |
| M2 | Import & authoring correctness | NOT_STARTED | 0 | `evidence/M2/` |
| M3 | Run lifecycle & data integrity | NOT_STARTED | 0 | `evidence/M3/` |
| M4 | Persistence hardening & migration | NOT_STARTED | 0 | `evidence/M4/` |
| M5 | Distributed execution at scale | NOT_STARTED | 0 | `evidence/M5/` |
| M6 | AKS deployment & HA failover | NOT_STARTED | 0 | `evidence/M6/` |
| M7 | Multi-region & scale evidence | NOT_STARTED | 0 | `evidence/M7/` |
| M8 | Production readiness & release gate | NOT_STARTED | 0 | `evidence/M8/` |

Valid states: `NOT_STARTED` · `IN_PROGRESS` · `BLOCKED` · `DONE`

## Recovery gate

The QA implementation baseline through `b172f1bbd` was verified from an isolated detached worktree,
not from the concurrently edited application workspace. `go test ./...`, the configured
golangci-lint suite, dashboard type-check/lint/accessibility/unit/build, the dev-admin browser suite,
the local-auth browser suite, workflow validation, tracker validation, and the QA Python mutation
tests all passed. The live workspace still contains separately owned runtime-adjustment changes, so
it is intentionally not described as clean.

The subsequent review corrections through `ec843df76` were re-verified from isolated detached
worktrees with 22 QA Python tests, `go test ./docs/qa ./scripts`, workflow validation, generated
dashboard freshness, and diff checks. The second review found and fixed the false assumption that
coverage-profile presence implies a package has tests; no unresolved finding remains in the
QA-owned diff.

M1 is underway. `scripts/run-e2e.sh` reproduces CI's browser-smoke recipe locally against a fresh
controller and throwaway database. Completion is tracked by
[TIER0_ASSERTION_MATRIX.md](TIER0_ASSERTION_MATRIX.md), not spec count: S1, S2, and S7 are green;
S8–S12 are partial; S3–S6 and the full arc are missing. S1 uses a separate local-auth controller
profile and drives both rejected and successful seeded administrator login.

The generated M1 API contract matrix now inventories the endpoint catalog without embedding a
route total in program prose. It proves public/authenticated registration parity, anonymous 401s
through the generic auth middleware, and allowed/denied RBAC role decisions where structured access
metadata exists. Legacy recorder/replay middleware now consumes the same viewer/editor access
profiles used by generated documentation, eliminating role-list drift while retaining each
server-instance session-to-project lookup. Metrics, recording-bridge, and collector-token routes
have explicit handler-level anonymous/authenticated cases. The route matrix is fully `GREEN`; M1
remains open on the Tier-0 browser journey and full arc.

## Headline metrics

Every number is generated from tracked files or committed evidence in
[HEADLINE_METRICS.md](HEADLINE_METRICS.md). `STATUS.md` no longer carries a hand-maintained copy.

### Reference counts (baseline, 2026-08-18)

Derived by `scripts/qa-baseline-counts.sh` — quote that script, never a hand-run glob. These
numbers first disagreed across three files because they were gathered ad hoc; the dashboard figure
was undercounted by 16 because a `**` glob skips files sitting directly under `dashboard/src/`.

| | Count |
|---|---:|
| Go test files | 1293 |
| Go source files (non-test) | 1060 |
| `func Test*` | 10508 |
| Dashboard unit test files | 323 |
| Playwright specs | 3 |
| `.golden` files | 0 |
| `testdata/` files | 34 |

## M0 — current week

| Week | Work | State |
|---|---|---|
| 1 | Stand up `docs/qa/` + `status_inventory_test.go`; baseline coverage; first dashboard artifact | DONE — 77.0% statement coverage measured, gate proven against four kinds of drift |
| 2 | Coverage ratchet + diff-coverage gate in `ci.yml` | IN_PROGRESS — ratchet built and tested; wired into CI in reporting-only mode pending a CI-measured baseline |
| 3 | Deterministic time seams; nightly Go + Playwright flake hunt | DONE — epoch fencing, scheduler, and execution leadership use injected clocks; nightly Go and zero-retry Playwright repetition are configured |
| 4 | Enable viable leak linters; triage review defects; clear existing quality failures | DONE — `rowserrcheck` enabled, false positives documented, and the configured linter suite reports zero issues |

### Week 2 — ratchet built, not yet enforcing

`scripts/coverage-ratchet.py` compares a fresh report against the committed baseline and fails on
regression, per package and overall. Verified against four cases: identity compare, a simulated
8.6 pp drop in `pkg/storage`, a 0.3 pp drop inside tolerance, and a failed-run guard that refuses to
judge coverage when tests did not pass.

It is wired into the existing `go-tests` job in `ci.yml` as a step rather than a new job, which
avoids touching the `required` meta-gate that enumerates every lane.

**It runs with `continue-on-error: true` and is therefore not yet a gate.** The committed baseline
was measured locally without `-race` and without `-shuffle`; CI runs with both. Enforcing across
that difference would compare two measurements that were never the same, and the first PR would fail
for reasons nobody could act on. The sequence to make it real is written into the workflow step:
land it, download one green CI run's adjacent `coverage-current.json` and
`coverage-current.md`, update both baseline companions via `--update`, then delete
`continue-on-error`.

**Two policy decisions are now encoded in `EXCLUDED_PACKAGES` and should be argued with, not
inherited.** `api/proto` (1,652 generated protobuf statements at 0%) is excluded, because testing it
would exercise protoc rather than this repository. `cmd/` is deliberately *not* excluded, because
`cmd/generator` sits at 26.7% and `cmd/controller` at 44.4% — a ratchet blind to `cmd/` would never
notice the controller binary regressing.

### Week 4 rescoped — the leak linters are a triage project, not a cleanup

The plan budgeted 7 hours to "enable 4 leak linters and fix the ~40 violations the header
enumerates." Both halves of that were wrong.

**The count was wrong.** Measured properly (`--max-same-issues=0 --max-issues-per-linter=0`) the
three leak linters report **66** violations, not 8: bodyclose 23, sqlclosecheck 41, rowserrcheck 2.
The header's figures came from a run under golangci-lint's default truncation. Logged as QA-012.

**And many are not defects.** `sqlclosecheck` reports "Close should use defer" against a deliberate
house idiom — close on every path *and* check the `Close` error, which a bare `defer` discards.
`bodyclose` flags a constructed cache-entry response in `pkg/protocol/http/http.go` and a
raw-connection read in `pkg/recorder/websocket.go` whose connection is explicitly closed.

Of the leak-focused candidates, **`rowserrcheck` is enabled**. The two broad heuristic linters remain
disabled because their inventories are dominated by documented false positives. The genuine findings
the exercise surfaced were retained:

- **QA-013**, a real silent-truncation bug in `ListAuthoringSessions` — fixed.
- Five test-file hygiene fixes (unclosed response bodies, unchecked `rows.Err()`).
- One defensive `defer rows.Close()` in `sqlite_auth.go` alongside the existing checked close.

The pre-existing failures in the already-enabled lint set were also corrected. The configured
golangci-lint suite, including `rowserrcheck`, now reports zero issues.

**Triage done** — see [evidence/M0/leak-linter-triage.md](evidence/M0/leak-linter-triage.md).
**Exactly one of the 66 was a true defect** (QA-013, fixed). All 36 production `sqlclosecheck` sites
close on every path and additionally check the `Close` error, which a bare `defer` would discard.
All 8 production `bodyclose` sites are false positives — three are `gorilla/websocket` dials whose
library documents that the response need not be closed, one already has `defer resp.Body.Close()`,
one flags an in-memory cache reconstruction, and three read from raw connections that are explicitly
closed.

**The panic-path residual is now closed at 33 of 36 sites.** `defer rows.Close()` was added
alongside the existing checked `Close`. The other **3 sites sit inside `for` loops and must not have
one** — `defer` is function-scoped, so it would accumulate a pending `Close` per iteration and hold
every row set open until return, which in `sqlite_tests.go`'s batch loop scales with input size and
is worse than the original code. A self-review caught this after the first pass had introduced it. This was done for the panic path, not the linter: a recovered panic between `Query` and
`Close` pinned that connection for the life of the process, and over a 72-hour M8 soak repeated
occurrences would exhaust the pool. The explicit checked `Close` is retained everywhere, so no error
check was traded away — `sql.Rows.Close` is documented idempotent, so both run safely.

Production `sqlclosecheck` is now **3** (was 41) — the three loop-scoped sites, deliberately left. 5 more remain in test files.

`bodyclose` and `sqlclosecheck` remain disabled deliberately: their residuals are documented false
positives or loop-scoped cases where a function-scoped defer would be worse. `rowserrcheck` is now
enabled after fixing its two remaining test-query paths. The five pre-existing govet/nolintlint/
unconvert failures in the already-enabled suite were also corrected; the configured linter suite
reports zero issues.

## Blockers

- The coverage ratchet remains reporting-only because the committed baseline records
  `test_run_status: fail`. Replace it with `coverage-current.json` from a green CI run, then remove
  `continue-on-error`; a local approximation does not satisfy this gate.
- ~~**DEF-11** needs a product decision~~ — **resolved 2026-08-18.** A short, common cookie value is
  not redacted. This is already the implemented behaviour: the root cause was removed in `6938e544e`,
  which landed eight days after the review that recorded it as unfixed. No code change was needed and
  none was made. See [DEFECTS.md](DEFECTS.md).
