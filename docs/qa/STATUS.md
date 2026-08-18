# Test Program — Status

Updated weekly. Scope is defined by [TEST_PROGRAM.md](TEST_PROGRAM.md); this file records only
*state*. Enforced by `status_inventory_test.go` — a milestone cannot be marked `DONE` without a
populated `evidence/M<n>/` directory.

**Program start:** 2026-08-18 · **Target completion:** 2027-05 · **Budget:** ~8 hrs/week

**Dashboard:** <https://claude.ai/code/artifact/2f07ea80-f800-4599-aff1-5327b3d05663> — the
published view of this file. Keep it current: update it whenever a milestone or headline metric
moves, and republish to **that same URL** rather than creating a second tracker. Publishing without
the URL makes a new artifact, which is how a program ends up with two dashboards disagreeing.

## Milestones

| ID | Milestone | State | Hrs spent | Evidence |
|---|---|---|---|---|
| M0 | Instrument the program | IN_PROGRESS | 0 | `evidence/M0/` |
| M1 | Tier-0 end-user journey | IN_PROGRESS | 6 | `evidence/M1/` |
| M2 | Import & authoring correctness | NOT_STARTED | 0 | `evidence/M2/` |
| M3 | Run lifecycle & data integrity | NOT_STARTED | 0 | `evidence/M3/` |
| M4 | Persistence hardening & migration | NOT_STARTED | 0 | `evidence/M4/` |
| M5 | Distributed execution at scale | NOT_STARTED | 0 | `evidence/M5/` |
| M6 | AKS deployment & HA failover | NOT_STARTED | 0 | `evidence/M6/` |
| M7 | Multi-region & scale evidence | NOT_STARTED | 0 | `evidence/M7/` |
| M8 | Production readiness & release gate | NOT_STARTED | 0 | `evidence/M8/` |

Valid states: `NOT_STARTED` · `IN_PROGRESS` · `BLOCKED` · `DONE`

M1 is underway. `scripts/run-e2e.sh` reproduces CI's browser-smoke recipe locally — build the
controller, run it on a dedicated port with a throwaway database, wait for health, run Playwright,
clean up — so specs are written against a running system rather than an imagined one. The first
three Tier-0 specs are green, and each was **mutation-tested**: the assertion was deliberately
broken to prove it fails, because a browser assertion that cannot fail is worse than no assertion.

Original note follows. M1 remained `NOT_STARTED` while its first deliverable — [TIER0_JOURNEY.md](TIER0_JOURNEY.md), the
twelve-step specification the Playwright suite implements — is written. It was pulled forward
because it is the one M1 artifact that needs no command execution, and week 2 of M0 is blocked on
exactly that. No spec in it is implemented, so the milestone has not started in any sense that
counts.

## Headline metrics

Every number here is measured, never estimated. Baselines captured 2026-08-18 from git-tracked
files only (`.claude/worktrees/` holds a full duplicate checkout and must be excluded).

| Metric | Baseline | Current | Target |
|---|---|---|---|
| Perf targets measured (of 4) | 0 | 0 | 4 |
| AKS cutover gates passed (of 10) | 0 | 0 | 10 |
| Tier-0 E2E specs green in CI | 0 | 4 | ~20 |
| Playwright specs total | 3 | 7 | ~20 |
| Go statement coverage | 77.0% | 77.0% | ratchet (never decreases) |
| Go coverage floor enforced | none | none | ratchet |
| Packages with no test file | 6 | 6 | 3 (the 3 test-only dirs) |
| Open whole-app-review defects | 11 | **1** | 0 or deferred-with-rationale |
| Resource-leak linters enabled (of 4) | 0 | 0 | 1 (`rowserrcheck`; the rest are triaged false positives — see [triage](evidence/M0/leak-linter-triage.md)) |
| Converter golden files | 0 | 0 | all 31 converters |
| Longest clean soak | none | none | 72 h on AKS |

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
| 3 | Deterministic `Clock` harness; nightly flake-hunt job | NOT_STARTED |
| 4 | Enable 4 leak linters + fix violations; triage the 11 open review defects | RESCOPED — see below |

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
land it, take one green CI run's figure as the baseline via `--update`, then delete
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

So the linters are **not enabled**. Enabling a gate that fails 66 times is not a gate. What was kept
is the genuine findings the exercise surfaced:

- **QA-013**, a real silent-truncation bug in `ListAuthoringSessions` — fixed.
- Five test-file hygiene fixes (unclosed response bodies, unchecked `rows.Err()`).
- One defensive `defer rows.Close()` in `sqlite_auth.go` alongside the existing checked close.

**Also found: `main` does not pass its own lint gate** — 8 pre-existing issues (gofmt 3, govet 2,
nolintlint 1, unconvert 2) with linters that are *already enabled*. CI's `go-quality` job runs
`golangci-lint`, so this is broader than QA-001's gofmt-only finding.

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

**Still not enabled as gates**, and deliberately so: `bodyclose` still reports 23, all triaged as
false positives, so enabling it would force edits to correct code. `rowserrcheck` (2, both tests) is
the one that could be enabled almost immediately.

## Blockers

- **QA-001** — `main` is not gofmt-clean (3 files), so CI's `go-quality` job fails its own format
  gate. Pure-formatting fix, zero risk, but it blocks the M0 week-2 CI work. Fix first.
- ~~**DEF-11** needs a product decision~~ — **resolved 2026-08-18.** A short, common cookie value is
  not redacted. This is already the implemented behaviour: the root cause was removed in `6938e544e`,
  which landed eight days after the review that recorded it as unfixed. No code change was needed and
  none was made. See [DEFECTS.md](DEFECTS.md).
