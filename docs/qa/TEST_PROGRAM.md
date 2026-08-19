# APEX Load — 9-Month Progressive Test & Infrastructure-Readiness Program

_Program scope of record. Live state is in [STATUS.md](STATUS.md); defects in [DEFECTS.md](DEFECTS.md); risks in [RISK_REGISTER.md](RISK_REGISTER.md). Started 2026-08-18. Calendar months are forecasts; only exit gates close milestones._

## Context

APEX Load is already heavily tested at the unit level — 1,293 Go test files / 10,508 `Test*`
functions, 323 dashboard unit test files, and a 15-job CI gate in `.github/workflows/ci.yml`.
**This is not a "write more tests" plan.** The gap is different and specific:

| What's missing | Evidence |
|---|---|
| Coverage is collected and thrown away | `ci.yml` writes `coverage.out`, uploads it as a 14-day artifact. No threshold, no diff-coverage, no Codecov. |
| End-to-end coverage is near-zero | 3 Playwright specs in `dashboard/e2e/` against ~24 routable views and 6 wizard/tab systems. |
| Every performance claim is unproven | All four targets in `docs/PERFORMANCE_BENCHMARK_EVIDENCE.md` are `attainment_status: not_evaluated`, and the schema validator *rejects* a report claiming `met`. |
| HA failover has never been tested for real | `APEXLOAD_RUN_HA_FAILOVER_SOAK` is an in-process two-`api.Server` simulation. `docs/AKS_INTEGRATION_READINESS.md` states plainly: no production controller-failover evidence is committed. |
| No golden-file testing on the conversion core | Zero `.golden` files across `pkg/convert/`'s 191 source files and 31 converters. |
| No deterministic time | No fake clock anywhere. For a product built on schedules, leases, pacing, and percentile windows, this is a systemic flake and correctness risk. |
| No SQLite→Postgres migrator, no restore drill | `docs/SQLITE_TO_POSTGRES_MIGRATION_GUIDE.md` tells you to write the migrator yourself. Backup/restore is runbook prose only. |
| Multi-region is a label | `region` is an operator-supplied string on `APEXLOAD_GENERATORS=dns://...\|region=eastus2`, consumed by soft-preference weighting in `pkg/api/distributed_placement.go`. Zero occurrences of "multi-region" in any doc. |

The intended outcome: work bottom-up from a rock-solid single-node end-user experience to a
**proven** AKS deployment running multi-region distributed load generation, converting
`not_evaluated` design targets into published, reproducible evidence at every step.

### Parameters (confirmed)

- **Executor:** solo, ~8 hrs/week, with agent-assisted authoring. ~31 hrs/month is a planning forecast, never authority to waive an exit gate.
- **Emphasis:** automation-first — durable suites that run in CI forever, manual exploration secondary.
- **Target infra:** vendor-neutral K8s artifacts, proven on **Azure/AKS** (most mature path: full Terraform, `aks`+`aks-ha` overlays, `deploy-aks.yml` with cosign verification, and a written 10-gate cutover list).
- **Multi-region end-state:** one control plane, generator fleets in 2+ regions, region-weighted placement proven correct, per-region attribution verified. Not regional control-plane federation.
- **Remediation:** in scope — testing-found defects, the 11 open whole-app-review items, and the 4 disabled resource-leak linters.

---

## Guiding principles

1. **Progressive gating.** A milestone's exit gate becomes the next milestone's entry precondition. Fast checks remain PR gates; repeated/integration suites run nightly; cloud, scale, performance, and soak work is executed manually and its committed evidence is validated on every PR.
2. **Evidence over assertion.** Every claim lands as a committed artifact under `docs/qa/evidence/`. Follow the existing house style in `docs/PERFORMANCE_BENCHMARK_EVIDENCE.md` — versioned JSON plus deterministic Markdown, never prose alone.
3. **Reuse the repo's own patterns.** This codebase already has excellent conventions to extend rather than replace:
   - Self-enforcing meta-tests — `pkg/convert/enterprisearchive/optin_suite_inventory_test.go` fails if a gated suite isn't documented in `docs/OPTIN_TEST_SUITES.md`.
   - Config-as-data tests — `scripts/*_test.go` and `deployments/k8s/*_test.go` are Go tests asserting on shell scripts, workflow YAML, and manifests.
   - Generated-artifact freshness gates — `go generate ./pkg/api` + `docs/api-endpoint-catalog.json`.
   - Build-tag/env-var opt-in suites with paired `!tag` default files (e.g. `kafka_unavailable_default_test.go`).
4. **Honesty discipline.** `docs/EVALUATOR_TRUST_PACK.md` and the `*_INTEGRATION_READINESS.md` docs are candid about limits. Keep that. Never promote a status without the artifact behind it.
5. **Trap to avoid:** `.claude/worktrees/` holds a full duplicate checkout. Any `find`/`wc`/coverage tooling you build must exclude it or every count will be roughly doubled.

---

## Tracking system

All tracking lives in the repo under a new `docs/qa/` directory, plus a regenerated dashboard artifact.

```
docs/qa/
  TEST_PROGRAM.md          # this plan, versioned; the single source of scope
  STATUS.md                # milestone + workstream table, updated weekly
  DASHBOARD.md             # generated repository-owned view; never hand-edited
  HEADLINE_METRICS.md      # generated from tracked files and committed evidence
  TIER0_ASSERTION_MATRIX.md # assertion-level M1 completion inventory
  evidence-contract.json   # required committed artifacts per milestone
  evidence-manifest.schema.json # common evidence metadata/hash contract
  RISK_REGISTER.md         # open risks w/ owner, severity, mitigation, milestone
  DEFECTS.md               # rolling defect log: id, severity, found-by, milestone, state
  FLAKE_LOG.md             # quarantined tests, first-seen, root cause, un-quarantine date
  RELEASE_TEST_CHECKLIST.md # drafted in M0, evolved through M7, executed in M8
  evidence/
    M0/ … M8/              # coverage snapshots, perf JSON, failover logs, drill transcripts
```

**Enforcement.** `docs/qa/status_inventory_test.go` derives milestone IDs from this file and checks
the machine-readable evidence contract has the same ordered inventory. A `DONE` milestone must have
every contract-required file committed in `HEAD`, including a passing `manifest.json` whose tested
commit is an ancestor and whose report hashes match the committed blobs. `SUMMARY.md` is a derived
rendering of that manifest and is freshness-checked rather than self-hashed, avoiding a circular
manifest → summary → manifest digest. `FIXED` defects cite a resolvable ancestor
commit; `DEFERRED` defects carry an owner, review date, target milestone, and rationale. The
repository dashboard is freshness-checked against `STATUS.md` and the Tier-0 assertion matrix.

**Gate tiers.** Every PR runs formatting, unit/integration tests, generated-artifact freshness,
coverage, dashboard checks, and Tier-0. Nightly workflows run repeated Go/Playwright,
PostgreSQL, and longer protocol suites. kind/AKS, performance, disruption, and soak work is manual
evidence; PR CI validates its manifest rather than attempting to rerun it.

**Cadence.**
- *Weekly (~30 min):* update `STATUS.md` — items moved, hours spent, blockers. Append to `FLAKE_LOG.md`.
- *Monthly (~2 hrs):* close the milestone, generate `evidence/M<n>/SUMMARY.md` with
  `scripts/qa-generate-evidence-summary.py`, regenerate the repository dashboard, and re-score
  `RISK_REGISTER.md`.

**Headline metrics** (the dashboard's top row — every one starts at zero or unknown):

| Metric | Start | Target |
|---|---|---|
| Perf targets `not_evaluated` → measured | 0 / 4 | 4 / 4 |
| AKS cutover gates passed (`AKS_INTEGRATION_READINESS.md` §Production Migration Gates) | 0 / 10 | 10 / 10 |
| Tier-0 journey E2E specs green in CI | 0 | ~20 |
| Go coverage, enforced floor | none | ratchet |
| Open whole-app-review defects | 11 | 0 or explicitly deferred with rationale |
| Resource-leak linters enabled | 0 / 4 | 1 / 4 — revised, see M0 note |
| Longest clean soak | none | 72 h on AKS |

---

## Milestones

Each is one month, ~31 hrs. Weekly slices are ~8 hrs.

### M0 — Instrument the program (Month 1, ~31 hrs)

Make quality measurable before testing anything, and clear the known debt that would otherwise
pollute every later signal.

| Week | Work | Hrs |
|---|---|---|
| 1 | Stand up `docs/qa/` + `status_inventory_test.go`. Baseline per-package coverage (exclude `.claude/worktrees/`). Publish the first dashboard artifact. | 8 |
| 2 | Coverage gate: add a `coverage` job to `ci.yml` enforcing a **ratchet** (floor = current, never decreases) plus a diff-coverage floor on changed files. Record baselines in `evidence/M0/coverage-baseline.json`. | 8 |
| 3 | **Deterministic time harness.** Extend the repository's injected `now func() time.Time` convention from epoch fencing through scheduler and execution leadership; do not introduce a competing clock abstraction. Run repeated Go and Playwright flake hunts nightly and seed `FLAKE_LOG.md`. | 8 |
| 4 | Enable `bodyclose`, `sqlclosecheck`, `rowserrcheck`, `nilerr` in `.golangci.yml` and fix the ~40 violations its header already enumerates. Triage the 11 open items in `docs/reviews/WHOLE_APP_REVIEW_2026-08-02.md` into `DEFECTS.md`. | 7 |

**Key files:** `.github/workflows/ci.yml`, `.golangci.yml`, `docs/qa/*`, `pkg/api/scheduler.go`,
`pkg/api/execution_leadership.go`, `pkg/distributed/generatorrpc/execution_epoch.go`.

**Exit gate (revised 2026-08-18):** coverage ratchet live and failing on regression · flake baseline
recorded · `DEFECTS.md` populated with all 11 open review items · leak-linter backlog triaged with
true leaks fixed.

> The original gate read "4 leak linters green". That target was based on the `.golangci.yml`
> header's count of ~40 violations, which was itself truncated by golangci-lint's defaults; the real
> figure is 66 (QA-012). Triage found **exactly one true defect** among them (QA-013, fixed) — every
> other production report is a false positive or a panic-path-only residual, and the residual is now
> closed by adding `defer rows.Close()` at all 36 sites. Turning the remaining reports green would
> mean editing correct code to satisfy a heuristic, including removing a genuine `Close`-error check
> in 36 places. The gate now asks for the backlog to be *triaged and its real defects fixed*, which
> is what the original gate was trying to buy. See
> [evidence/M0/leak-linter-triage.md](evidence/M0/leak-linter-triage.md).

> Note on review finding **#11** (`pkg/engine/redaction.go` — the runtime secret matcher redacts
> arbitrarily short values like `en`/`true`, destroying response bodies and correlation evidence).
> Two committed tests pin the current behavior, so this needs a **product decision**, not a patch.
> Decide it in M0 and record the decision in `DEFECTS.md`; do not let it drift to M8.

---

### M1 — Tier-0 end-user journey and API contract (Month 2, ~31 hrs)

Define and lock the golden path a real user walks. This is the "rock solid basics" foundation
everything later assumes.

**The Tier-0 journey** (one continuous E2E arc): sign in → create Domain/Application/Project/Release
→ import a script → open it in the Script Editor → build a scenario in the 6-step wizard
(`TestHub.tsx`) → configure and launch a run (`NewTestPage.tsx`) → watch live metrics in Command
Center → pause/resume/scale VUsers → stop → open the Run Report (all three lenses) → download the
archive.

| Week | Work | Hrs |
|---|---|---|
| 1 | Maintain `TIER0_ASSERTION_MATRIX.md`, extend the reusable journey fixtures, and run S2–S12 under the `dev-admin` profile. Run S1 separately under `local-auth` and perform a real seeded administrator login; dev bypass does not count as sign-in. | 8 |
| 2–3 | Close every `PARTIAL`/`MISSING` assertion in the matrix and add one full-arc smoke. Adopt Testing Library, `user-event`, and `jest-dom` for new accessible component tests without mechanically rewriting the existing suite. | 16 |
| 4 | **API contract matrix.** Generate cases from `docs/api-endpoint-catalog.json` rather than a hard-coded route count. Assert public/authenticated posture plus allowed and denied roles for every protected route. | 7 |

**Key files:** `dashboard/e2e/`, `dashboard/e2e/support/liveRun.ts`, `dashboard/src/test/vitestSetup.ts`,
`pkg/api/route_catalog.go`, `docs/api-endpoint-catalog.json`.

**Exit gate:** every Tier-0 matrix assertion and the full arc run green on every PR · every
catalogued route has generated auth/access assertions · accessible queries and user interaction are
available for new component tests.

---

### M2 — Import & authoring correctness (Month 3, ~31 hrs)

The largest functional surface: 119 Script Authoring routes, 31 converters in `pkg/convert/`, 16
user-visible import types in `ScriptCreateOptions.tsx`. Zero golden files today.

| Week | Work | Hrs |
|---|---|---|
| 1 | Build a golden-file harness for `pkg/convert` (`-update` flag, deterministic serialization). Seed goldens for the existing 35 `testdata/` fixtures. | 8 |
| 2 | Wire the LoadRunner corpus into CI: `scripts/fetch-lr-scripts-corpus.sh` + `TestPinnedLRScriptsCorpusSmoke` (auto-runs when ≥20 script dirs are present). Cache the corpus so CI doesn't refetch. Extend goldens to Bruno, Postman, OpenAPI, JMeter, HAR, WSDL. | 8 |
| 3 | E2E: import → review (`ScriptImportReview`, `ParameterizationReview`, `SecretBindingReview`, `SourceMappingReview`) → edit in the workbench → save → replay in the debugger. Cover the 3-stage `POST`→`validate`→`commit` flow and `imports/rollback`. | 8 |
| 4 | Lift the genuinely thinnest packages — see the corrected target list below. | 7 |

#### M2 week 4 target list, corrected against measured data

The original list here (`scriptdisclosure`, `telemetry`, `keywrap`, `importartifact`, `javasidecar`,
`selector`) was derived from **test-file-to-source-file ratios**, a proxy that turned out to point at
substantially the wrong packages. `evidence/M0/coverage-baseline.md` now gives real statement
coverage, and only `secretstore` survives from the original six.

Ranked by **absolute uncovered statements**, which is what actually costs risk:

| Package | Coverage | Uncovered | Why it matters |
|---|---:|---:|---|
| `pkg/storage` | 68.6% | **5,561** | Largest gap in the repo by a wide margin, and it is the package M4's Postgres dialect work depends on. |
| `cmd/apexctl` | 65.0% | 1,219 | The CLI is a supported entry point and a CI gate surface. |
| `api/proto` | 0% | 1,652 | Generated protobuf. Almost certainly should be *excluded* from the ratchet rather than tested — decide explicitly. |
| `pkg/protocol/network` | 46.8% | 427 | DNS/TCP/UDP/FTP/SMTP/LDAP; executes real protocol work. |
| `pkg/secretstore` | 63.4% | 299 | Security-sensitive. The one package the original list got right. |
| `pkg/kubeops` | 68.6% | 237 | Cluster auth and dialing; M6 depends on it. |
| `pkg/compute/azure` | 67.3% | 214 | Capacity provisioning on the target cloud. |
| `pkg/protocol/grpc` | 63.8% | 203 | Controller↔generator transport shares this surface. |
| `cmd/generator` | 26.7% | 192 | **A production binary at 27%.** |
| `cmd/controller` | 44.4% | 170 | **The main binary at 44%.** |

`pkg/convert/diag` (73 statements, 0%) and the six untested `cmd/` mains stay in scope but are small.

Two decisions this forces, both in M0 week 2 when the ratchet is built:
1. **Does `api/proto` count?** It is 1,652 generated statements at 0%. Including it drags the
   headline down permanently and rewards nothing; excluding it is defensible but must be explicit
   and recorded, not silent.
2. **Do `cmd/` mains count?** Six are at 0% and two more are the least-covered real code in the
   repository. A ratchet that excludes `cmd/` would never notice the controller binary regressing.

**Key files:** `pkg/convert/*/testdata/`, `pkg/convert/enterprisearchive/`, `scripts/fetch-lr-scripts-corpus.sh`,
`dashboard/src/components/ScriptImportReview.tsx`, `pkg/keywrap/`, `pkg/secretstore/`.

**Exit gate:** every registered converter has a deterministic valid golden and a meaningful
invalid/diagnostic case, with inventory parity enforced · a converter change produces a reviewable
diff instead of a silent behavior shift · the cached LR corpus smoke is repeatable. Coverage risk is
governed by the ratchet, not a test-file-to-source-file ratio.

---

### M3 — Run lifecycle & data integrity, single node (Month 4, ~31 hrs)

| Week | Work | Hrs |
|---|---|---|
| 1 | Exhaustive run-control matrix: `start`/`stop`/`pause`/`resume`/`abort`/`PATCH duration`/`PATCH vusers`/`PATCH goal-targets`, from every status in `pkg/controller/controller.go:42` (`QUEUED`→`ABORTED`). Include the illegal transitions. | 8 |
| 2 | Archive integrity: manifest, SHA-256 verify, retention plan/apply, comparison, orphan recovery, finalization outbox. Report generation across HTML/PDF/CSV/React via the section registry. ~50 `pkg/api/archive_*.go` files back this. | 8 |
| 3 | Data pools sharding (`pkg/engine/data.go` `DataShardConfig`), parameterization runtime, secret resolution and the redaction split (`config.log.redact_run_evidence`). Scheduler + execution-plan approvals/fencing/recovery. | 8 |
| 4 | **24-hour single-node soak** against `scripts/synthetic-mixed-protocol-test/dummy_server.py`, with pprof + `/metrics` sampled throughout. Watch goroutine/heap growth and the acknowledged-unbounded `audit_events` / `usage_accounting_snapshots` tables. | 7 |

**Exit gate:** 24 h soak clean — no goroutine leak, no unbounded heap growth, archive verify passes ·
run-control matrix green · `evidence/M3/soak-24h/` committed.

---

### M4 — Persistence hardening & migration tooling (Month 5, ~31 hrs)

`PostgresStore` is literally `struct { *SQLiteStore }` with a dialect flag — one SQL codebase,
runtime-translated by `rewriteSQL()`. Most of the 192 files in `pkg/storage/` are SQLite-shaped and
only some have a Postgres twin test. **Dialect drift is the single highest-value target here.**

| Week | Work | Hrs |
|---|---|---|
| 1 | Run the **entire** suite under `APEXLOAD_STORE=postgres` against a real PG 16 container (CI already stands one up in the `deployment-artifacts` job). Catalogue and fix every dialect divergence. | 8 |
| 2 | **Build the SQLite→Postgres migrator** (`cmd/apexload-migrate` or `scripts/`). None exists — `docs/SQLITE_TO_POSTGRES_MIGRATION_GUIDE.md` says write it yourself. Follow the guide's 8-tier dependency order; emit the ~20-entity validation counts automatically. | 8 |
| 3 | **Schema version ledger.** `pkg/storage/sqlite_schema.go` applies ~100+ `ALTER TABLE ADD COLUMN` on *every* startup, idempotent only by string-matching `"duplicate column name"`. No version table, no ordering, no dry-run, and startup time grows with the list. Add a version table + `--dry-run`, keeping the additive/idempotent policy. Test upgrade *and* rollback across the last 3 release tags. | 8 |
| 4 | Scripted backup/restore drill: managed-PG backup → restore into an isolated DB → validate counts → point a controller at it. Also archive-store (Blob) recovery. Commit as `scripts/restore-drill.sh` + evidence. | 7 |

**Exit gate:** full suite green on Postgres · migrator validated on a real database with count
parity · restore drill scripted and executed once · schema version introspectable.

---

### M5 — Distributed execution at scale, local → AKS (Month 6, ~31 hrs)

The distributed plane is real, not stubbed — `api/proto/generator.proto`, 77 files / ~30k LOC in
`pkg/distributed/generatorrpc/`, epoch fencing, two-phase `CommitWork(startAt)`. It has just never
been run wide.

| Week | Work | Hrs |
|---|---|---|
| 1 | Scale ladder on kind (`make k8s-kind-apply`): 2 → 5 → 10 generators. Verify two-phase common-start, epoch fencing rejects a stale controller, `dns://` discovery re-resolution (15 s), archive pull-through. | 8 |
| 2 | Failure injection: kill a generator mid-run, partition one, exhaust one's capacity, drain one (`PATCH /api/v1/system/generators/drain`). Assert `pkg/api/distributed_resilience.go` behavior — this file holds open review defect #2. | 8 |
| 3 | **Find the WebSocket fanout ceiling.** `maxHubClients = 1024`, per-client buffer 64, broadcast channel 256, all with drop counters (`pkg/api/websocket.go:97`). The counters exist because nobody knows where it breaks. Measure and document it. | 8 |
| 4 | Convert the first two perf targets: **harness overhead < 50 µs** and **ramp rate 500 VUsers/s/pod**, via `make performance-evidence`. Publish real numbers into `docs/PERFORMANCE_BENCHMARK_EVIDENCE.md`. | 7 |

**Exit gate:** 10-generator distributed run reproducible from a script · fanout ceiling documented ·
2 of 4 perf targets measured. Also record where `maxDistributedTargetsPerRun = 256`
(`pkg/api/distributed_execution_backend.go:24`) and controller-pull archive flow start to bite.

---

### M6 — AKS deployment & HA failover evidence (Month 7, ~31 hrs)

| Week | Work | Hrs |
|---|---|---|
| 1 | Terraform `deployments/azure/terraform/` → AKS + Postgres Flexible Server + Azure Cache for Redis + Blob + Key Vault + workload identity. Deploy via Helm (`deployments/helm/apexload`) and the `aks-ha` overlay. Script the whole thing; it will be re-run many times. | 8 |
| 2 | Walk **all 10 AKS cutover gates** verbatim from `docs/AKS_INTEGRATION_READINESS.md` §Production Migration Gates. Record pass/fail per gate in `evidence/M6/aks-gates.md`. Gate 6 (script → distributed run → stream → archive → report) reuses the M1 Tier-0 journey. | 8 |
| 3 | **Real HA failover drills** — the thing the simulation cannot do. With `APEXLOAD_CONTROLLER_HA=true`, shared Postgres/Redis: pod delete mid-run, node drain, network partition of the lease holder, Postgres failover. Assert the five properties gate 10 demands: one execution start, no stale-owner mutation, strictly newer lease generation, one terminal test outcome, one terminal archive. Verify the generation-fenced active EndpointSlice follows. | 8 |
| 4 | KEDA scale-out/scale-in mid-run. Resolve the doc conflict: `ENTERPRISE_DATA_PLATFORM_OPERATIONS_RUNBOOK.md:79` says "at least two controller replicas"; `ENTERPRISE_PLATFORM_ARCHITECTURE_RUNBOOK.md` and `AKS_INTEGRATION_READINESS.md` say "keep one controller by default." Pick one and correct the other. | 7 |

**Exit gate:** 10/10 AKS gates pass · real failover evidence committed — `AKS_INTEGRATION_READINESS.md`
can finally say controller failover has passed an AKS disruption test, because it has.

---

### M7 — Multi-region generation & scale evidence (Month 8, ~31 hrs)

| Week | Work | Hrs |
|---|---|---|
| 1 | Generator fleets in 2 Azure regions, tagged `\|region=`, one control plane. Prove `region_weighted` and `load_zone_weighted` placement (`pkg/api/distributed_placement.go`, `runner_assignment.go`) distribute as configured — and that the documented soft-preference fallback degrades to capacity ordering rather than failing. | 8 |
| 2 | Per-region attribution end to end: `Region` on results (`pkg/protocol/protocol.go:606`) → Parquet dictionary column (`archive_parquet.go:22`) → `archive_analysis.go` → report sections → `GET /api/v1/fleet/load-zones`. Verify a region's numbers are actually that region's. | 8 |
| 3 | **Regression-proof fleet-pool pinning.** Review finding #154 (`pool_id` silently stripped by `scenarioDraftUtils.ts:26` + `scenarioPayloadUtils.ts:215`, permanently destroying a region/residency pool binding on dashboard re-save) is **already fixed** — `pool_id` is now in `types/testConfiguration.ts:26` and carried through both builders. But there is no round-trip test proving it survives API-create → dashboard-load → save → relaunch. Write that test and verify pinning end to end against the 2-region fleet. | 8 |
| 4 | Push the **10,000 VUsers/pod** target on a genuinely constrained 4 vCPU / 8 GB pod against a real HTTP target (not loopback), plus **metrics latency < 2 s** generator→dashboard. Publish. | 7 |

**Exit gate:** multi-region run with verified per-region attribution · fleet-pool pinning proven to survive a full re-save round-trip ·
4 of 4 perf targets measured and published with honest numbers, whatever they turn out to be.

---

### M8 — Production readiness & the release gate (Month 9, ~31 hrs)

| Week | Work | Hrs |
|---|---|---|
| 1 | Security pass: RBAC access-matrix simulation across all 543 routes × the 4 auth modes; production fail-closed posture (`APEXLOAD_ENV=production` refusing `dev`/`local`, trusted-proxy gate, bootstrap admin); SSRF guard (`pkg/netpolicy/`); secret handling and redaction; gRPC mTLS hard-fail; supply-chain verification. | 8 |
| 2 | Chaos & dependency loss on AKS: kill Postgres, kill Redis, revoke Blob access, induce clock skew (a documented fatality in `EKS_INTEGRATION_READINESS.md`), rolling upgrade in place with a run active. | 8 |
| 3 | **72-hour endurance soak on AKS**, multi-region, Postgres, HA enabled. Full observability capture. This is where the resource leaks the M0 linters didn't catch will surface. | 8 |
| 4 | Finalize and execute the release checklist drafted in M0. Update `docs/RELEASE_EVIDENCE_MANIFEST.md` to link every claim to its M0–M8 artifact. Regenerate the repository dashboard. | 7 |

**Exit gate:** 72 h soak clean · release checklist executed · every performance and readiness claim in
the docs traceable to a committed artifact.

---

## Risk register (initial)

| Risk | Impact | Mitigation |
|---|---|---|
| Perf targets prove unreachable when measured | Docs and positioning need revision | M5/M7 publish real numbers regardless. `PERFORMANCE_BENCHMARK_EVIDENCE.md` is already built to record honest results — the schema rejects unearned `met`. |
| Postgres dialect drift is worse than expected | M4 overruns | Timebox week 1 to *cataloguing*; if the list is large, fix by blast radius and carry the tail into M5. |
| Azure spend during M6–M8 | Budget | Scripted teardown after every drill; use spot node pools for generators; keep the ladder on kind through M5. |
| Solo bus factor on cloud drills | Program stalls | Every drill is a committed script, never a manual sequence. If it isn't scripted, it isn't done. |
| E2E flake erodes trust in the gate | Suites get ignored | Strict `FLAKE_LOG.md` policy: quarantine within 24 h with a root-cause entry and a dated un-quarantine commitment. The M0 fake clock removes the largest structural cause. |
| Azure subscription, quota, DNS/TLS, or budget prerequisites are unavailable | M6 cannot produce honest evidence | Validate and commit the prerequisite report before provisioning; mark M6 `BLOCKED` rather than substituting simulation. |

---

## Verification

Each milestone is verified by artifacts, not by assertion.

```bash
go test ./... -race -shuffle=on -count=1
```

```bash
cd dashboard && npm run typecheck && npm run lint && npm run lint:a11y && npm test && npm run e2e
```

```bash
make performance-evidence PERF_JSON=docs/qa/evidence/M7/perf.json PERF_MD=docs/qa/evidence/M7/perf.md
```

```bash
APEXLOAD_RUN_HA_FAILOVER_SOAK=1 APEXLOAD_HA_SOAK_ITERATIONS=25 go test ./pkg/api -run HAFailoverSoak -count=1
```

```bash
make docker-local-up && make k8s-kind-apply && make k8s-smoke-test
```

- **M0:** CI fails deliberate overall/package/missing-package coverage regressions; `rowserrcheck` is enabled and other leak-linter exclusions remain documented.
- **M1:** every assertion in `TIER0_ASSERTION_MATRIX.md` is green against a freshly built controller; the generated route contract matrix passes.
- **M2:** `go test ./pkg/convert/...` green with goldens; a deliberate converter tweak produces a golden diff.
- **M3:** `evidence/M3/soak-24h/` shows flat goroutine and heap curves; archive verify exits 0.
- **M4:** full suite green under `APEXLOAD_STORE=postgres`; migrator count parity; `scripts/restore-drill.sh` exits 0.
- **M5:** 10-generator kind run reproducible from one script; perf JSON shows 2 targets measured.
- **M6:** `evidence/M6/aks-gates.md` shows 10/10; failover transcripts show a single execution owner and monotonic lease generations.
- **M7:** a 2-region run whose per-region archive numbers reconcile with per-generator data; perf JSON shows 4/4 measured.
- **M8:** 72 h AKS soak artifacts; `RELEASE_TEST_CHECKLIST.md` executed with results recorded.

Progress is visible continuously in `docs/qa/STATUS.md` (enforced by `status_inventory_test.go`) and
in the dashboard Artifact regenerated at each milestone close.
