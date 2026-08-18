# Defect Log

Rolling log for the test program. Two sources feed it:

1. **Inherited** — the 11 defects left open by
   [`docs/reviews/WHOLE_APP_REVIEW_2026-08-02.md`](../reviews/WHOLE_APP_REVIEW_2026-08-02.md).
   Each is *partly* fixed; the review records exactly which half remains.
2. **Found** — anything this program's suites catch, logged as `QA-nnn`.

States: `OPEN` · `IN_PROGRESS` · `BLOCKED` · `FIXED` · `DEFERRED` (needs a written rationale)

`status_inventory_test.go` fails if an entry is marked `FIXED` without a commit reference.

## Inherited — the 11 defects the review left open

**Re-validated 2026-08-18: 10 of the 11 are already fixed. One remains.**

The `WHOLE_APP_REVIEW_2026-08-02.md` snapshot is now more than two weeks stale, and substantial work
landed after it — including `6938e544e` on 2026-08-10. Every entry below was checked against current
code rather than taken from the review. Finding #154 (`pool_id` stripping), which the review did not
even list as open, was likewise found already fixed.

**The lesson for the remaining program:** do not schedule work against a review entry without first
checking whether the defect still reproduces. Three of the twelve items examined so far were already
closed, and acting on any of them would have been wasted effort against code that had moved on.

| ID | Sev | File | Residual | Milestone | State | Commit |
|---|---|---|---|---|---|---|
| DEF-2 | High | `pkg/api/distributed_resilience.go` | **Re-verified fixed 2026-08-18.** `stopEvacuationParticipants` now takes an `unreachable` target set (`distributed_resilience.go:1316`); a stop failure against a known-unreachable generator is classified as tolerated rather than blocking, so `finalizeFailClosedEvacuation` runs and the run reaches a terminal status. The forever-retry the review described cannot occur for a dead source. | M5 | FIXED | verified@1dc937e70 |
| DEF-9 | High | `pkg/convert/jmeter/converter.go` | **Re-verified fixed 2026-08-18.** `jmeterElementEnabled` (`converter.go:1488`) reads the JMX `enabled` attribute and is applied at **16 call sites across 6 files**, including an ancestor-aware check in `converter_diagnostics.go:27`. Disabled elements are no longer imported as live. | M2 | FIXED | verified@1dc937e70 |
| DEF-11 | High | `pkg/engine/redaction.go` | Decision taken 2026-08-18: a short, common cookie value is **not** redacted. Already the implemented behaviour — the root cause was removed in `6938e544e` (2026-08-10), eight days *after* the review that described it as unfixed. No further code change. | M0 | FIXED | 6938e544e |
| DEF-21 | High | `dashboard/src/components/NewTestPage.tsx` | **Re-verified fixed 2026-08-18.** The group dialog now renders an inherit option — `<option value="">{groupErrorHandlingInherit.label}</option>` at `NewTestPage.tsx:3658/3666`, labelled 'Script default' or 'Scenario default'. `buildGroupErrorHandlingSectionForRun` owns the tri-state rule and declines to build a section rather than writing an explicit `false`. | M3 | FIXED | verified@1dc937e70 |
| DEF-26 | High | `pkg/api/persistence.go` | **Re-verified STILL OPEN 2026-08-18, severity reduced.** `compactTerminalManagedTestLocked` now trims a terminal run to one history point and releases its cardinality-unbounded breakdowns, so per-run cost is far smaller. But the defect as written stands: boot still calls `ListTests("", nil)` (`persistence.go:487`) against a query with no `LIMIT` (`sqlite_tests.go:925`), and the only `delete(tr.tests, …)` calls are user-delete and already-gone-from-DB paths — there is no count or age eviction. Resident memory still grows with the install's lifetime run count rather than with concurrent load. | M3 | OPEN | — |
| DEF-35 | Med | `pkg/api/testexecution.go` | **Re-verified fixed 2026-08-18.** `persistManagedTestVUserScale` (`persistence.go:1849`) writes only the durable state a scale changes, and the ramp path calls it (`testexecution.go:3911, 3942`) instead of the full-row upsert. The in-code comment states the reason directly: a gradual ramp would otherwise re-encode every compiled script under `tr.mu` once per single-VUser step. | M3 | FIXED | verified@1dc937e70 |
| DEF-69 | Med | `pkg/convert/bruno/inspect.go` | **Re-verified fixed 2026-08-18.** Both named cost terms are gone. `brunoRequestContentCache` (`inspect.go:1994`) memoizes the structured parse with a bounded entry count, and `classifyFileRole` (`bundle.go:346`) no longer calls `yaml.Unmarshal` at all — it is now pure path/prefix matching, so its ~15 call sites cost nothing. | M2 | FIXED | verified@1dc937e70 |
| DEF-156 | Med | `dashboard/src/types/scripts.ts` | **Re-verified fixed 2026-08-18.** Every field the review named is now declared in `dashboard/src/types/scripts.ts`: `locale`, `timezone`, `har_capture`, `geolocation`, `client_certificates`, plus `eventhubs`, `max_in_flight` and `runtime_policy`. | M2 | FIXED | verified@1dc937e70 |
| DEF-164 | Med | `docs/ENTERPRISE_AUTOSCALING_SIGNALS.md` | **Re-verified fixed 2026-08-18.** `ENTERPRISE_AUTOSCALING_SIGNALS.md:116` now publishes `1-5 (2 recommended)` and `WEB_API_EXECUTION_SUPPORT_MATRIX.md:151` matches. The advisory no longer recommends a density its own release gate blocks. | M5 | FIXED | verified@1dc937e70 |
| DEF-297 | Low | `docs/COMPETITIVE_COMPARISON.md` | **Re-verified fixed 2026-08-18.** `COMPETITIVE_COMPARISON.md` now routes each row family to the endpoint that actually publishes it: bundle entitlements and the Database/Network/Oracle/Templates/SAP rows to `/api/v1/protocol-bundles/support-matrix`, and the protocol-level Web/API rows to `/api/v1/scripts/web-api/support-matrix` and `/api/v1/protocols/native-support-matrix`. Both endpoints confirmed present in the generated catalog. The provenance claim is now true as written. | M8 | FIXED | verified@1dc937e70 |
| DEF-317 | Low | `dashboard/src/components/ScriptImportReview.tsx` | **Re-verified fixed 2026-08-18.** `previewJSON` is wrapped in `useMemo` **and** gated on `showPreview` (`ScriptImportReview.tsx:681-682`), so it neither runs on every render nor runs at all while the preview tab is closed. | M2 | FIXED | verified@1dc937e70 |

### DEF-11 — decided, and already implemented

**The decision (2026-08-18):** a short, common cookie value such as `locale=en` or
`cookieconsent=true` must **not** be redacted. Redaction requires a minimum length or a sensitive
name, accepting that a genuinely short secret could survive in evidence.

**That is already the behaviour**, via a stronger fix than the one the question implied. Rather than
adding a length floor to the matcher, commit `6938e544e` removed the root cause: the bounded cookie
jar no longer harvests values from the system under test into the runtime secret matcher at all. A
cookie value issued by the SUT was never platform-managed secret material, so treating it as such
was the actual defect.

**Why the tracker said otherwise.** `WHOLE_APP_REVIEW_2026-08-02.md` recorded DEF-11 as "no code
change at all" and named two tests as pinning the disputed behaviour. The review is dated
**2026-08-02**; the fix landed **2026-08-10**. One of the two named tests
(`TestBoundedCookieJarRuntimeSnapshotProtectsShortApplicationDefinedCookies`) no longer exists —
it was replaced by `TestBoundedCookieJarDoesNotHarvestCookieValuesAsSecrets`, which asserts the
opposite and now also guards structurally that neither the jar nor `protocol.Result` may
reintroduce a cookie-value side channel.

**Verified, not assumed:**

| Check | Result |
|---|---|
| `6938e544e` is an ancestor of HEAD | yes |
| `TestBoundedCookieJarDoesNotHarvestCookieValuesAsSecrets` | passes |
| `TestShortSUTCookieDoesNotScrubMetricDimensionsOrZeroGroupTotals` (asserts DEF-11's literal failure — dimension keys scrubbed, group totals zeroed — no longer occurs) | passes |
| The harvesting side channel exists anywhere in non-test code | no |

**What was deliberately NOT done.** No minimum-length floor was added to the runtime matcher. The
second test the review named, `TestRuntimeSecretRedactionDoesNotAmplifyShortMatches`, still asserts
that a one-byte **platform-managed** secret — one an operator explicitly placed in the project
secret store — is redacted. That is a different case from a SUT-issued cookie and is consistent with
the decision: an operator who registers a value as a secret has asked for it to be redacted.
Weakening that path was not authorised and would trade security for nothing.

Run-evidence redaction is now a setting: `config.log.redact_run_evidence` (`pkg/dsl/types.go:664`).

## Found by this program

| ID | Sev | Found by | File | Summary | Milestone | State | Commit |
|---|---|---|---|---|---|---|---|
| QA-001 | High | M0 baseline sweep | `pkg/api/server.go`, `pkg/api/route_registry_compute_test.go`, `pkg/keywrap/wrapping_aws.go` | Three files on `main` are not gofmt-clean (struct field alignment). CI's `go-quality` job runs `gofmt -l .` with a hard fail, so **the main branch does not currently pass its own format gate**. Pure-formatting fix, zero behavioural risk. Verified pre-existing: all three are unmodified in the working tree. | M0 | OPEN | — |
| QA-002 | High | Self-review | `docs/qa/status_inventory_test.go` | The defect-row parser indexed `cells[len(cells)-2]` with no length guard, so a truncated single-cell row panicked with `index out of range [-1]`, crashing the whole test binary instead of reporting the malformed row. Reproduced, then fixed with a length guard matching the two sibling tests. | M0 | IN_PROGRESS | — |
| QA-003 | High | Self-review | `scripts/coverage_baseline_report.py` | The baseline reported repository-level *statement* coverage alongside per-package *function* coverage. A package can score near 100% by function and far lower by statement, so a ratchet fed the per-package figure would gate on a signal that barely moves. Rewritten to parse the coverage profile directly and report statements only, at both levels. | M0 | IN_PROGRESS | — |
| QA-004 | High | Self-review | `scripts/coverage-baseline.sh` | The script deleted its own `test.log` via a cleanup trap, destroying the diagnostic output for exactly the runs that need it. This actively cost us the identity of the failing test in FLAKE-1. The log is now retained in the evidence directory. | M0 | IN_PROGRESS | — |
| QA-005 | Med | Self-review | `docs/qa/STATUS.md` | Dashboard test-file baseline stated 307; the true count is 323. A `**` pathspec silently skipped the 16 test files directly under `dashboard/src/`. Corrected, and the counts now come from `scripts/qa-baseline-counts.sh` so three documents cannot state three values again. | M0 | IN_PROGRESS | — |
| QA-006 | Med | Self-review | `scripts/coverage-baseline.sh` | When a compile error produced no coverage profile, `set -e` aborted at the report step, so no artifact was written and the operator saw a missing-file error instead of the real compile failure — making the `test_run_status=fail` field unreachable in the very case it exists for. Now emits the artifact regardless. | M0 | IN_PROGRESS | — |
| QA-007 | Low | Self-review | `docs/qa/TEST_PROGRAM.md` | Stated 324 dashboard test files against STATUS.md's 307, with the truth at 323. Corrected to the canonical count. | M0 | IN_PROGRESS | — |
| QA-008 | Low | Self-review | `scripts/coverage-baseline.sh` | The package list was piped through `xargs`, which may split an over-long list into several `go test` invocations that each rewrite `-coverprofile`, silently truncating the baseline. Not triggered at today's 5.4 KB against a 1 MB `ARG_MAX`, but it fails silently. Now passed as a single argv. | M0 | IN_PROGRESS | — |
| QA-009 | Low | M0 wk2 | `docs/qa/TEST_PROGRAM.md` | Stated a "14-job CI gate"; `ci.yml` actually defines 15 jobs — a hand-built list omitted `ghes-fork-block`. Third count in this program quoted from a secondary source rather than measured. Corrected, and the job count is now derived by `scripts/qa-baseline-counts.sh`. | M0 | IN_PROGRESS | — |
| QA-015 | Med | Self-review | `scripts/coverage-ratchet.py` | The ratchet ignored a package that disappeared from the report, so deleting **every** test in a package evaded the gate entirely: dropping `pkg/storage` (17,723 statements, the largest package) still exited 0. Now a baseline package producing no coverage fails the ratchet and names its baseline figure; a deliberate removal is re-baselined with `--update`. Verified against three cases: deletion fails, unchanged passes, real regression still fails. | M0 | IN_PROGRESS | — |
| QA-016 | Low | Self-review | `.github/workflows/flake-hunt.yml`, `pkg/api/generator_gateway_test.go` | Two small ones from the same review. The flake-hunt step recorded an `exit_code` output nothing consumed and carried a redundant `continue-on-error` (the script cannot fail the runner), implying a control flow that did not exist. The gateway test's `if response != nil { defer response.Body.Close() }` guarded the pointer while dereferencing `.Body` — and gorilla/websocket documents that this response need not be closed at all, so the close was removed rather than hardened. | M0 | IN_PROGRESS | — |
| QA-014 | Med | M0 wk4 | `pkg/storage/*.go` (16 files) | Row sets were closed explicitly on every normal path but without a `defer`, so a panic between `Query` and `Close` leaked the row set and pinned its connection. Because the controller recovers panics, that connection was pinned for the life of the process — repeated occurrences would exhaust the pool over a long soak. Added `defer rows.Close()` at 33 of 36 production sites while retaining the explicit checked `Close`, so no error check was traded away. The other 3 sit inside `for` loops, where a function-scoped defer would accumulate one pending `Close` per iteration — worse than the original; a self-review caught and reverted those after the first pass introduced them. | M0 | IN_PROGRESS | — |
| QA-012 | High | M0 wk4 | `.golangci.yml` | **The repository's own linter backlog is understated by roughly 8x.** The header's rejected-linter counts were measured under golangci-lint's defaults (`max-same-issues=3`, `max-issues-per-linter=50`), so every "3" may be far larger and every "50+" is just the cap. Re-measured with both limits disabled: bodyclose **23** (not 3), sqlclosecheck **41** (not 3), rowserrcheck 2 (accurate). Header corrected with the measurement flags so the next person does not repeat it. | M0 | IN_PROGRESS | — |
| QA-013 | High | M0 wk4 | `pkg/storage/sqlite_scripts.go:977` | `ListAuthoringSessions` iterated rows without checking `rows.Err()`. A truncated read — closed connection, context cancellation mid-scan — returned the rows gathered so far as if they were the complete set, so a caller listing authoring sessions could silently see fewer than exist and treat that as authoritative. Fixed. Found by enabling `rowserrcheck`. | M0 | IN_PROGRESS | — |
| QA-011 | High | M0 wk3 | `pkg/distributed/generatorrpc/*`, `pkg/api/distributed_evacuation_postfence_test.go` | FLAKE-1: a 30 ms wall-clock epoch TTL raced the scheduler and failed 2 runs in 5 under load. Fixed at the cause — `WithClock` seam added following the repo's existing `now func() time.Time` idiom, all 7 epoch time reads routed through `Service.nowTime()`, and the flaking test migrated to advance a controlled clock. Verified over 300 repetitions including under 8-way CPU contention. | M0 | IN_PROGRESS | — |
| QA-010 | Med | M0 wk2 | `scripts/coverage_baseline_report.py` | The profile parser filtered `node_modules` only via the package list, not the profile itself. CI runs `go test ./...`, which instruments vendored third-party Go, so feeding CI's profile to the report would have silently reintroduced the very leak the package-list filter exists to prevent — found while wiring the ratchet into CI, before it could bite. | M0 | IN_PROGRESS | — |

## Closed / corrected during triage

| ID | Note |
|---|---|
| #154 | **Already fixed — do not re-open.** `assignment.pool_id` silently stripped by `scenarioDraftUtils.ts` + `scenarioPayloadUtils.ts`, permanently destroying a fleet-pool (region/residency) binding on dashboard re-save. Verified fixed: `pool_id` is present in `dashboard/src/types/testConfiguration.ts:26`, preserved in `scenarioDraftUtils.ts:29`, and carried in `scenarioPayloadUtils.ts:218`. It is **not** in the review's open-11 list. What is still missing is a *round-trip regression test* proving it survives API-create → dashboard-load → save → relaunch — scheduled in M7 week 3. |
