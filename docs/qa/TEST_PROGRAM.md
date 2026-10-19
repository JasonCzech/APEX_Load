# APEX Load — 9-Month Progressive Test & Infrastructure-Readiness Program

_Program scope of record. Live state is in [STATUS.md](STATUS.md); defects in [DEFECTS.md](DEFECTS.md); risks in the [live risk register](#live-risk-register). Started 2026-08-18. Calendar months are forecasts; only exit gates close milestones._

## Context

APEX Load is already heavily tested at the unit level; the current inventory is generated in the
[QA dashboard](DASHBOARD.md), rather than copied into this scope document.
**This is not a "write more tests" plan.** The gap is different and specific:

| What's missing | Evidence |
|---|---|
| Coverage is collected and thrown away | `ci.yml` writes `coverage.out`, uploads it as a 14-day artifact. No threshold, no diff-coverage, no Codecov. |
| End-to-end coverage is incomplete | the [Tier-0 assertion matrix](E2E_SUITES.md#tier-0-assertion-matrix) records required behavior as `GREEN`, `PARTIAL`, or `MISSING`; spec-file count is not completion evidence. |
| Every performance claim is unproven | All four targets in `docs/PERFORMANCE_BENCHMARKS.md` are `attainment_status: not_evaluated`, and the schema validator *rejects* a report claiming `met`. |
| HA failover has never been tested for real | `APEXLOAD_RUN_HA_FAILOVER_SOAK` is an in-process two-`api.Server` simulation. `deployments/k8s/aks/README.md` (Integration readiness) states plainly: no production controller-failover evidence is committed. |
| Converter output lacks an enforced golden inventory | M2 must derive registered converters and require valid and diagnostic cases for each; source-file and converter counts are generated, not embedded here. |
| Deterministic-time coverage is incomplete | The existing injected `now` convention must cover scheduler, execution leadership, and epoch fencing without introducing a competing clock abstraction. |
| No SQLite→Postgres migrator, no restore drill | `docs/ENTERPRISE_PLATFORM_RUNBOOK.md` tells you to write the migrator yourself. Backup/restore is runbook prose only. |
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
2. **Evidence over assertion.** Every claim lands as a committed artifact under `docs/qa/evidence/`. Follow the existing house style in `docs/PERFORMANCE_BENCHMARKS.md` — versioned JSON plus deterministic Markdown, never prose alone.
3. **Reuse the repo's own patterns.** This codebase already has excellent conventions to extend rather than replace:
   - Self-enforcing meta-tests — `pkg/convert/enterprisearchive/optin_suite_inventory_test.go` fails if a gated suite isn't documented in `docs/qa/TEST_PROGRAM.md`.
   - Config-as-data tests — `scripts/*_test.go` and `deployments/k8s/*_test.go` are Go tests asserting on shell scripts, workflow YAML, and manifests.
   - Generated-artifact freshness gates — `go generate ./pkg/api` + `docs/api-endpoint-catalog.json`.
   - Build-tag/env-var opt-in suites with paired `!tag` default files (e.g. `kafka_unavailable_default_test.go`).
4. **Honesty discipline.** `docs/COMPETITIVE_COMPARISON.md#evaluator-trust-pack` and the AKS/EKS README "Integration readiness" sections are candid about limits. Keep that. Never promote a status without the artifact behind it.
5. **Trap to avoid:** `.claude/worktrees/` holds a full duplicate checkout. Any `find`/`wc`/coverage tooling you build must exclude it or every count will be roughly doubled.

---

## Tracking system

All tracking lives in the repo under a new `docs/qa/` directory, plus a regenerated dashboard artifact.

```
docs/qa/
  TEST_PROGRAM.md          # this plan, versioned; the single source of scope
  STATUS.md                # milestone + workstream table, updated weekly
  DASHBOARD.md             # generated status, coverage, and metric summaries; never hand-edited
  E2E_SUITES.md            # Tier-0 journey + assertion matrix, workflow and recording suites
  evidence-contract.json   # required committed artifacts per milestone
  evidence-manifest.schema.json # common evidence metadata/hash contract
  DEFECTS.md               # rolling defect log: id, severity, found-by, milestone, state
  FLAKE_LOG.md             # quarantined tests, first-seen, root cause, un-quarantine date
  (open risks and the release checklist live in this file: see "Live risk register" and "Release test checklist")
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
  the live risk register (this file).

**Headline metrics** (the dashboard's top row — every one starts at zero or unknown):

| Metric | Start | Target |
|---|---|---|
| Perf targets `not_evaluated` → measured | 0 / 4 | 4 / 4 |
| AKS cutover gates passed (`deployments/k8s/aks/README.md` (Integration readiness) §Production Migration Gates) | 0 / 10 | 10 / 10 |
| Tier-0 journey assertions green in CI | generated assertion inventory | every required assertion plus the full arc |
| Go coverage, enforced floor | none | ratchet |
| Open whole-app-review defects | 11 | 0 or explicitly deferred with rationale |
| Resource-leak linters enabled | 0 / 4 | 1 / 4 — revised, see M0 note |
| Longest clean soak | none | 72 h on AKS |

---

## Reassessment — 2026-08-18

The plan below was written assuming ~8 hrs/week for nine months, with one milestone per month worked
strictly in sequence. **One day in, that model is obsolete.** M0–M4 are all open simultaneously, 32
commits have landed, and the measured state is:

| | Baseline | Now |
|---|---:|---:|
| Tier-0 assertions GREEN | 0 / 12 | **12 / 12** |
| Playwright specs | 3 | **14** |
| Converter golden files | 0 | **30** (all 15 registered surfaces) |
| Open inherited review defects | 11 | **1** (DEF-26) |
| Go statement coverage | 77.01% | 77.01% |
| **Coverage floor enforced** | 0 | **0** |
| **Packages with no test file** | 6 | **8** |
| **Performance targets measured** | 0 / 4 | **0 / 4** |
| AKS cutover gates passed | 0 / 10 | **0 / 10** |

### What this changes

**The bottleneck has moved, and the plan should follow it.** Authoring effort is no longer the
scarce resource. What remains scarce is *wall-clock time and infrastructure access*: a 72-hour soak
takes 72 hours no matter how fast anything is written, an AKS gate needs a real cluster and real
spend, and multi-region needs two regions. Those cannot be compressed by working faster, and they
are now the critical path.

So the month-per-milestone framing is retired in favour of two tracks:

**Track A — software correctness (M0–M4).** Parallelisable, no external dependencies, roughly 60%
done. Continue opportunistically; do not serialise it.

**Track B — infrastructure evidence (M5–M8).** Serialised by wall-clock and infrastructure, entirely
untouched, and it is the half of the original brief that remains completely unevidenced. **Its
long-lead items should start now, not after Track A closes** — cloud account and quota, a realistic
target application to generate load against, and booked soak windows. Waiting for Track A to finish
before requesting cloud access would idle the critical path behind work that no longer needs the
time.

### Three things the numbers say that the prose would not

1. **The coverage ratchet still runs `continue-on-error: true`.** It has never been a gate. Every
   suite built since exists without the floor that was supposed to protect it, and closing this is
   cheap — one CI-measured baseline and a deleted line.

2. **Packages with no test file went 6 → 8.** Coverage regressed while the program was actively
   adding tests. This is exactly what an enforcing ratchet exists to catch, and it is the concrete
   argument for item 1 rather than a hypothetical one.

3. **Performance targets remain 0 of 4.** This was named in the Context section as the single
   largest gap — every published target still reads `not_evaluated`. Nothing in the work so far has
   touched it, because it lives in Track B.

### Revised immediate sequence

1. **Enforce the coverage ratchet.** Take one green CI run's `coverage-current.json`, re-baseline,
   delete `continue-on-error`. Cheapest high-value item outstanding.
2. **Fix the 6 → 8 untested-package regression**, or record each as a deliberate exclusion.
3. **Close M1** — the per-step assertions are green and the U01 full-arc smoke exists (`workflow-local-auth.spec.ts`); confirm it is a required per-PR gate.
4. **Start Track B's long-lead items in parallel**, beginning with the local scale ladder on kind
   (M5), which needs no cloud spend and is the prerequisite for every later infrastructure gate.

The milestone definitions below remain accurate as *scope*. Only their sequencing and their
month-sized effort estimates are superseded by this section.

---

## Milestones

The month labels and hour slices below are planning forecasts. A milestone stays open until its exit
gate passes, regardless of elapsed calendar time.

### M0 — Instrument the program (Month 1, ~31 hrs)

Make quality measurable before testing anything, and clear the known debt that would otherwise
pollute every later signal.

| Week | Work | Hrs |
|---|---|---|
| 1 | Stand up `docs/qa/` + `status_inventory_test.go`. Baseline per-package coverage (exclude `.claude/worktrees/`). Publish the first dashboard artifact. | 8 |
| 2 | Coverage gate: add a `coverage` job to `ci.yml` enforcing a **ratchet** (floor = current, never decreases) plus a diff-coverage floor on changed files. Record baselines in `evidence/M0/coverage-baseline.json`. | 8 |
| 3 | **Deterministic time harness.** Extend the repository's injected `now func() time.Time` convention from epoch fencing through scheduler and execution leadership; do not introduce a competing clock abstraction. Run repeated Go and Playwright flake hunts nightly and seed `FLAKE_LOG.md`. | 8 |
| 4 | Enable `bodyclose`, `sqlclosecheck`, `rowserrcheck`, `nilerr` in `.golangci.yml` and fix the ~40 violations its header already enumerates. Triage the 11 open items of the 2026-08-02 whole-application review into `DEFECTS.md` (done; see its "Inherited" section). | 7 |

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
| 1 | Maintain the Tier-0 assertion matrix in `E2E_SUITES.md`, extend the reusable journey fixtures, and run S2–S12 under the `dev-admin` profile. Run S1 separately under `local-auth` and perform a real seeded administrator login; dev bypass does not count as sign-in. | 8 |
| 2–3 | Close every `PARTIAL`/`MISSING` assertion in the matrix and add one full-arc smoke. Adopt Testing Library, `user-event`, and `jest-dom` for new accessible component tests without mechanically rewriting the existing suite. | 16 |
| 4 | **API contract matrix.** Generate cases from `docs/api-endpoint-catalog.json` rather than a hard-coded route count. Assert public/authenticated posture plus allowed and denied roles for every protected route. | 7 |

**Key files:** `dashboard/e2e/`, `dashboard/e2e/support/liveRun.ts`, `dashboard/src/test/vitestSetup.ts`,
`pkg/api/route_catalog.go`, `docs/api-endpoint-catalog.json`.

**Exit gate:** every Tier-0 matrix assertion and the full arc run green on every PR · every
catalogued route has generated auth/access assertions · accessible queries and user interaction are
available for new component tests.

---

### M2 — Import & authoring correctness (Month 3, ~31 hrs)

This is a large functional surface. Its route, converter, import-type, fixture, and golden breadth
must come from generated inventories; this plan deliberately carries no fixed totals.

| Week | Work | Hrs |
|---|---|---|
| 1 | Build one deterministic golden harness for `pkg/convert` with explicit `-update`, stable ordering, and normalized generated identifiers/timestamps. Enforce registered-converter parity with at least one valid golden and one meaningful invalid/diagnostic case per converter. | 8 |
| 2 | Cache and run the pinned LoadRunner corpus without network access in normal PR execution. Extend reviewable deterministic output across every registered converter. | 8 |
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
| 2 | **Build the SQLite→Postgres migrator** (`cmd/apexload-migrate` or `scripts/`; *to be created, neither exists yet*). Supply the DSN by environment variable, default to dry-run, require `--apply` for mutation, and support resumable checkpoints. Derive an ordered entity plan and emit count reconciliation plus final verification automatically. | 8 |
| 3 | **Schema version ledger.** `pkg/storage/sqlite_schema.go` applies ~100+ `ALTER TABLE ADD COLUMN` on *every* startup, idempotent only by string-matching `"duplicate column name"`. Since this plan was written a `schema_migrations` version ledger has landed (`pkg/storage/schema_migrations.go`); ordering, `--dry-run` and the release-tag upgrade/rollback tests are still open. Add `--dry-run`, keeping the additive/idempotent policy. Test upgrade *and* rollback across the last 3 release tags. | 8 |
| 4 | Scripted backup/restore drill: managed-PG backup → restore into an isolated DB → validate counts → point a controller at it. Also archive-store (Blob) recovery. Commit as `scripts/restore-drill.sh` + evidence (*to be created; the script does not exist yet*). | 7 |

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
| 4 | Convert the first two perf targets: **harness overhead < 50 µs** and **ramp rate 500 VUsers/s/pod**, via `make performance-evidence`. Publish real numbers into `docs/PERFORMANCE_BENCHMARKS.md`. | 7 |

**Exit gate:** 10-generator distributed run reproducible from a script · fanout ceiling documented ·
2 of 4 perf targets measured. Also record where `maxDistributedTargetsPerRun = 256`
(`pkg/api/distributed_execution_backend.go:24`) and controller-pull archive flow start to bite.

---

### M6 — AKS deployment & HA failover evidence (Month 7, ~31 hrs)

| Week | Work | Hrs |
|---|---|---|
| 1 | Terraform `deployments/azure/terraform/` → AKS + Postgres Flexible Server + Azure Cache for Redis + Blob + Key Vault + workload identity. Deploy via Helm (`deployments/helm/apexload`) and the `aks-ha` overlay. Script the whole thing; it will be re-run many times. | 8 |
| 2 | Walk **all 10 AKS cutover gates** verbatim from `deployments/k8s/aks/README.md` (Integration readiness) §Production Migration Gates. Record pass/fail per gate in `evidence/M6/aks-gates.md`. Gate 6 (script → distributed run → stream → archive → report) reuses the M1 Tier-0 journey. | 8 |
| 3 | **Real HA failover drills** — the thing the simulation cannot do. With `APEXLOAD_CONTROLLER_HA=true`, shared Postgres/Redis: pod delete mid-run, node drain, network partition of the lease holder, Postgres failover. Assert the five properties gate 10 demands: one execution start, no stale-owner mutation, strictly newer lease generation, one terminal test outcome, one terminal archive. Verify the generation-fenced active EndpointSlice follows. | 8 |
| 4 | KEDA scale-out/scale-in mid-run. Resolve the doc conflict: `ENTERPRISE_PLATFORM_RUNBOOK.md:79` says "at least two controller replicas"; `ENTERPRISE_PLATFORM_RUNBOOK.md` and `deployments/k8s/aks/README.md` (Integration readiness) say "keep one controller by default." Pick one and correct the other. | 7 |

**Exit gate:** 10/10 AKS gates pass · real failover evidence committed — `deployments/k8s/aks/README.md` (Integration readiness)
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
| 1 | Security pass: execute the generated route matrix under local, trusted-header, OIDC, and service-account authentication, including production fail-closed posture (`APEXLOAD_ENV=production` refusing `dev`/`local`, trusted-proxy gate, bootstrap admin); SSRF guard (`pkg/netpolicy/`); secret handling and redaction; gRPC mTLS hard-fail; supply-chain verification. | 8 |
| 2 | Chaos & dependency loss on AKS: kill Postgres, kill Redis, revoke Blob access, induce clock skew (a documented fatality in `deployments/k8s/eks/README.md` (Integration readiness)), rolling upgrade in place with a run active. | 8 |
| 3 | **72-hour endurance soak on AKS**, multi-region, Postgres, HA enabled. Full observability capture. This is where the resource leaks the M0 linters didn't catch will surface. | 8 |
| 4 | Finalize and execute the release checklist drafted in M0. Update `docs/GITHUB_ACTIONS_RELEASE_AND_DEPLOYMENT.md` (Release evidence manifest) to link every claim to its M0–M8 artifact. Regenerate the repository dashboard. | 7 |

**Exit gate:** 72 h soak clean · release checklist executed · every performance and readiness claim in
the docs traceable to a committed artifact.

---

## Risk register (initial)

Scoping-time summary. The scored, owned register is [below](#live-risk-register).

| Risk | Impact | Mitigation |
|---|---|---|
| Perf targets prove unreachable when measured | Docs and positioning need revision | M5/M7 publish real numbers regardless. `PERFORMANCE_BENCHMARKS.md` is already built to record honest results — the schema rejects unearned `met`. |
| Postgres dialect drift is worse than expected | M4 overruns | Timebox week 1 to *cataloguing*; if the list is large, fix by blast radius and carry the tail into M5. |
| Azure spend during M6–M8 | Budget | Scripted teardown after every drill; use spot node pools for generators; keep the ladder on kind through M5. |
| Solo bus factor on cloud drills | Program stalls | Every drill is a committed script, never a manual sequence. If it isn't scripted, it isn't done. |
| E2E flake erodes trust in the gate | Suites get ignored | Strict `FLAKE_LOG.md` policy: quarantine within 24 h with a root-cause entry and a dated un-quarantine commitment. The M0 fake clock removes the largest structural cause. |
| Azure subscription, quota, DNS/TLS, or budget prerequisites are unavailable | M6 cannot produce honest evidence | Validate and commit the prerequisite report before provisioning; mark M6 `BLOCKED` rather than substituting simulation. |

## Live risk register

Re-scored at each milestone close. Severity = Impact × Likelihood, both High/Med/Low.

| ID | Risk | Sev | Milestone | Owner | Mitigation | State |
|---|---|---|---|---|---|---|
| R-1 | Performance targets prove unreachable once actually measured. All four are `not_evaluated` today and the docs already say "design target, not yet benchmarked." | High | M5, M7 | Performance/QA | Publish real numbers regardless of outcome. `PERFORMANCE_BENCHMARKS.md` is built for this — its schema validator rejects an unearned `met`. If a target is missed, revise the target or the docs, not the evidence. | OPEN |
| R-2 | Postgres dialect drift is worse than expected. `PostgresStore` is `struct { *SQLiteStore }` with a flag; one SQL codebase runtime-translated by `rewriteSQL()`, and most of `pkg/storage`'s 192 files are SQLite-shaped. | High | M4 | Storage | Timebox M4 week 1 to *cataloguing* divergences, not fixing them. If the list is large, fix by blast radius and carry the tail into M5. | OPEN |
| R-3 | Azure spend during M6–M8 cloud drills. | Med | M6–M8 | Cloud operator | Scripted teardown after every drill. Spot node pools for generators. Keep the scale ladder on kind through M5. Set a budget alert before the first `terraform apply`. | OPEN |
| R-4 | Solo bus factor — cloud drills done by hand can't be repeated or reviewed. | Med | M6–M8 | Program owner | Hard rule: every drill is a committed script. If it isn't scripted, it isn't done and it doesn't count as evidence. | OPEN |
| R-5 | E2E flake erodes trust in the gate; suites get ignored or disabled. | High | M1 onward | QA | Log the first unexplained failure; after the second, fix or quarantine within 24 h with an owner and dated commitment. | OPEN |
| R-6 | DEF-11 (over-eager redaction) stays undecided and every run archive collected in M3–M8 is full of over-redacted evidence. | High | M0 | Engine | **Closed 2026-08-18.** Decision taken (short common cookie values are not redacted) and found already implemented in `6938e544e`. Archives from M3 onward are not at risk from this. | CLOSED |
| R-7 | The API surface grows during the program, outpacing the contract matrix. | Med | M1 onward | API | Generate the matrix from `docs/api-endpoint-catalog.json`, which is already regenerated and freshness-enforced. New route ⇒ new row automatically. | OPEN |
| R-8 | Schema migrations are ~100+ unordered `ALTER TABLE ADD COLUMN` applied on every startup with no version table, so an upgrade/rollback test has nothing to assert against. | Med | M4 | Storage | M4 week 3 adds the version ledger first, then the upgrade/rollback matrix becomes expressible. | OPEN |
| R-9 | Counting/coverage tooling silently double-counts because `.claude/worktrees/` holds a full duplicate checkout. | Low | M0 | QA | All tooling reads `git ls-files`, never bare `find`. Recorded in TEST_PROGRAM.md principles. | MITIGATED |
| R-10 | Azure subscription access, quota, budget alert, or DNS/TLS prerequisites are unavailable when M6 starts. | High | M6 | Cloud operator | Commit `azure-prerequisites.json` before provisioning. If any prerequisite fails, mark M6 `BLOCKED`; do not substitute simulation for AKS evidence. | OPEN |

## Release test checklist

_Draft created in M0. It remains a living checklist until M8 records an executed result._

- [ ] Repository quality and generated-artifact gates pass on the release commit.
- [ ] Coverage ratchet and changed-line coverage pass.
- [ ] Tier-0 assertion matrix and full-arc journey are green.
- [ ] Converter golden inventory and protected import journey are green.
- [ ] Run-control, archive-integrity, PostgreSQL migration, and restore evidence pass.
- [ ] kind scale/failure matrix and all four performance measurements are committed.
- [ ] AKS prerequisites, 10/10 readiness gates, and HA disruption evidence pass.
- [ ] Multi-region placement and attribution reconcile.
- [ ] Four-mode authentication matrix and production fail-closed checks pass.
- [ ] 72-hour soak meets the committed thresholds.
- [ ] Every open high-severity risk has an owner and no expired deferral.
- [ ] Release evidence manifest links every published claim to a committed artifact.

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
- **M1:** every assertion in the Tier-0 assertion matrix in `E2E_SUITES.md` is green against a freshly built controller; the generated route contract matrix passes.
- **M2:** `go test ./pkg/convert/...` green with goldens; a deliberate converter tweak produces a golden diff.
- **M3:** `evidence/M3/soak-24h/` shows flat goroutine and heap curves; archive verify exits 0.
- **M4:** full suite green under `APEXLOAD_STORE=postgres`; migrator count parity once the migrator exists; restore drill executed with evidence (target: a `scripts/restore-drill.sh` exiting 0, once created).
- **M5:** 10-generator kind run reproducible from one script; perf JSON shows 2 targets measured.
- **M6:** `evidence/M6/aks-gates.md` shows 10/10; failover transcripts show a single execution owner and monotonic lease generations.
- **M7:** a 2-region run whose per-region archive numbers reconcile with per-generator data; perf JSON shows 4/4 measured.
- **M8:** 72 h AKS soak artifacts; the release test checklist (this file) executed with results recorded.

Progress is visible continuously in `docs/qa/STATUS.md` (enforced by `status_inventory_test.go`) and
in the dashboard Artifact regenerated at each milestone close.

## Opt-in Test Suites

`go test ./...` (and `make test`) runs the default suite. A number of suites are
**excluded from that default** — either by a Go build tag or by an environment
variable — and must be invoked deliberately. Selected suites also have dedicated
automation: the deployment-artifact CI job provisions PostgreSQL 16 for the
scheduler-migration and concurrent-startup tests, and the protected protocol-integration workflow runs
the live broker/cloud suites weekly or by trusted repository dispatch.

This page is the inventory of every such gate, what it covers, and how to run it.
It is machine-enforced: `TestOptInSuiteInventoryIsDocumented`
(`pkg/convert/enterprisearchive/optin_suite_inventory_test.go`) scans every
`*_test.go` in the repository for build-tag and environment-variable gates and
fails if one is missing from the tables below. Adding a new opt-in gate means
adding a row here.

What the scanner can see, precisely: `//go:build` constraints, and the name of an
environment variable inside a `Skip`/`Skipf`/`Skipln` call on any testing receiver
(`t`, `b`, …) — written literally in the message, or passed as a format argument
via a package-level string constant, across as many lines as the call spans. What
it cannot see is a skip whose message was assembled somewhere else and handed in as
a variable (`t.Skip(message)`); `APEXLOAD_BROWSER_TESTS` is that shape, so its row
below is maintained by hand. Keep the gate's name inside the `Skip` call and the
guard stays honest.

Everything can be run at once with:

```bash
./scripts/run-optin-suites.sh          # everything that needs no external infrastructure
./scripts/run-optin-suites.sh --all    # also suites needing live infrastructure (needs the env vars below)
./scripts/run-optin-suites.sh --list   # show the inventory and what would run
```

### Why these are opt-in

Four different reasons, and they are worth keeping straight:

1. **Needs a large external corpus** — the LR_Scripts smoke test needs a public
   LoadRunner script corpus that is not vendored into this repository.
2. **Needs real infrastructure** — the `*_integration` suites talk to a live
   PostgreSQL database, Kafka, Solace or Azure cloud endpoint.
3. **Needs a real client library compiled in** — the `*_real` build tags swap the
   in-memory mock adapters for the vendored network clients. These compile and run
   locally; they do not need a broker.
4. **Needs a deliberately tagged fault build** — `apexload_fault_probe` makes a
   disposable controller pause after a durable evacuation guard so a deployed
   interruption can be injected. The hook is absent from release builds.

Only category 3 is a candidate for the default `go test ./...` CI step.
Categories 1 and 2 need provisioning, which is why they are gated. CI
separately provisions PostgreSQL 16 for scheduler migration and concurrent-startup tests. The
protected protocol-integration workflow runs the Kafka, Solace, Service Bus, and
Event Hubs suites against configured infrastructure; other infrastructure- or
corpus-backed suites remain manual. Category 4 is intentionally excluded from
ordinary builds even though its unit tests run locally.

### Environment-variable gates

| Environment variable | Suite | Package | How to run |
|---|---|---|---|
| `APEXLOAD_LR_SCRIPTS_CORPUS` | `TestPinnedLRScriptsCorpusSmoke` — runs the LoadRunner / DevWeb / JMeter importers over the pinned public LR_Scripts corpus | `pkg/convert/enterprisearchive` | **Auto-runs when the corpus is present.** `./scripts/fetch-lr-scripts-corpus.sh` then `go test ./pkg/convert/enterprisearchive`. Set the variable only to point at a checkout somewhere else — a path that does not resolve now **fails** rather than skipping. Auto-discovery admits a root only when its `Scripts` directory holds at least **20** script directories (the same bar the suite asserts), so a half-finished fetch, or an unrelated directory left at the `/tmp` default, skips with instructions instead of conscripting the package into failing. An explicitly named checkout is held to the weaker "holds any `.usr` / `.jmx` / `main.js`" test, so an operator who pointed at an incomplete corpus gets the suite's own loud assertion rather than a silent skip. |
| `APEXLOAD_RAML_FIDELITY_ARCHIVES` | Private MuleSoft RAML archive fidelity checks: operation coverage, explicit request examples, library traits/types, credential headers, query fields, and review gates | `pkg/convert/raml`, `pkg/api` | Point to a local directory containing `customer-crm-billing-eapi-1.0.32-raml.zip`, `cc-mule-orders-api-1.0.0-raml.zip`, and `customer-profile-crm-eapi-1.0.75-raml.zip`; run `APEXLOAD_RAML_FIDELITY_ARCHIVES=/path/to/archives go test ./pkg/convert/raml ./pkg/api -run 'TestCustomerRAML' -count=1`. The customer archives are not vendored. Synthetic fixtures run in the default suite. All checks are offline; no requests are sent to the customer APIs. |
| `LR_SCRIPTS_DEST` | Relocates the corpus for both `./scripts/fetch-lr-scripts-corpus.sh` and the auto-discovery in `TestPinnedLRScriptsCorpusSmoke` (probed first when set) | `pkg/convert/enterprisearchive` | `LR_SCRIPTS_DEST=$HOME/corpora/lr ./scripts/fetch-lr-scripts-corpus.sh`, then run the package with the same variable exported. Not a switch — it only moves the location. |
| `APEXLOAD_BROWSER_TESTS` | Decides how the Playwright browser suites treat a launch failure: `require` = always a FAILURE, `skip` = always a skip, `auto` (default) = skip only when the probe cannot find the engine's executable | `pkg/protocol/browser`, `pkg/api` | Runs by default; set `APEXLOAD_BROWSER_TESTS=require go test ./pkg/protocol/browser` anywhere the runtime is installed on purpose so a broken runtime cannot hide as a skip. Any other value is a hard failure. **`auto` is not a complete guarantee:** the probe itself boots the Playwright driver and stats the driver-resolved executable path, so it also reports "absent" — and therefore skips — when the driver fails to run or when the installed browser build drifts from the pinned driver version. CI installs chromium and webkit (`.github/workflows/ci.yml`) and pins playwright-go in `go.mod`, so an installed-but-broken runtime already fails there; `require` is the belt-and-braces against driver/browser version drift. The separate archive acquisition qualification runs only with explicit `require`: `APEXLOAD_BROWSER_TESTS=require TZ=America/New_York go test ./pkg/api -run '^TestBrowserResourceActualChromiumAcquisitionArchiveAndPagination$' -count=1`. Its default/`auto`/`skip` paths are explicit non-qualification skips; `require` must launch actual Chromium and reconcile more than 50 acquired observations through the actual archive writer, JSONL and physical Parquet reader without further target traffic. This correctness fixture does not establish complete capture coverage or performance. The native replay HAR qualification is gated the same way: `APEXLOAD_BROWSER_TESTS=require go test ./pkg/api -run '^TestBrowserReplayActualNativeHAR$' -count=1` replays a real Chromium page with `record_har`, downloads the native HAR, checks that a replay without it serves the synthetic HAR, and that the original is retired and the copy pruned. `./scripts/run-optin-suites.sh --all` runs both qualifications. The BI-4 golden is gated the same way: `APEXLOAD_BROWSER_TESTS=require go test ./pkg/api -run '^TestBrowserIntelligenceGoldenAcceptance$' -count=1` runs a controlled page in Chromium and WebKit through the archive writer and the findings, interaction and journey readers; `./scripts/run-optin-suites.sh --all` runs it as the "browser intelligence acceptance" suite, writing its results and F4 acceptance requests, see [BROWSER_INTELLIGENCE_ACCEPTANCE.md](BROWSER_INTELLIGENCE_ACCEPTANCE.md). |
| `APEXLOAD_TELEMETRY_REFERENCE` | The TEL-4 telemetry reference workload (`TestTelemetryReferenceWorkload`): two in-process generators at 2,000 accepted observations a second for 60 s, a headless-Chromium foreground dashboard reporting render evidence, and concurrent archive analyses; stage P50/P95/P99, coverage, loss against the target's served count, CPU and RSS | `pkg/api` | `APEXLOAD_TELEMETRY_REFERENCE=1 go test ./pkg/api -run '^TestTelemetryReferenceWorkload$' -v` (`APEXLOAD_TELEMETRY_REFERENCE_SECONDS`, `_RATE`, `_OUT`); `./scripts/run-optin-suites.sh --all` runs it as the "telemetry reference workload" suite, and the TEL-4 fault matrix runs as the "telemetry fault matrix" suite (its Go tests under `-race`), see [TELEMETRY_ACCEPTANCE.md](TELEMETRY_ACCEPTANCE.md) |
| `APEXLOAD_BROWSER_BENCHMARKS` | Browser launch / action-latency / Chromium RSS benchmarks (`BenchmarkBrowser*`), and the BE-4 browser acceptance benchmark (`TestBrowserBenchmark`: Chromium and WebKit desktop and mobile profiles, cold launch, session start, CPU/RSS, capture overhead, throttled separation, cleanup and leftover processes) — they start real browser processes | `pkg/protocol/browser` | `APEXLOAD_BROWSER_BENCHMARKS=1 go test ./pkg/protocol/browser -run '^$' -bench 'BenchmarkBrowser' -benchmem -count 5`; see `docs/PERFORMANCE_BENCHMARKS.md`. The acceptance benchmark: `APEXLOAD_BROWSER_BENCHMARKS=1 APEXLOAD_BROWSER_BENCHMARK_OUT=<dir> APEXLOAD_COMMIT_SHA=<commit> go test ./pkg/protocol/browser -run '^TestBrowserBenchmark$' -v`, which writes the results and F4 acceptance requests; `./scripts/run-optin-suites.sh --all` runs it in the "browser acceptance" suite, see [BROWSER_ACCEPTANCE.md](BROWSER_ACCEPTANCE.md) |
| `APEXLOAD_H3_SPIKE` | HTTP/3 socket-ownership measurement (`TestSpikeSocketOwnership`): client goroutines, heap and file descriptors per VUser for a socket per VUser versus one shared socket, with the fixture server re-executed in a child process (`APEXLOAD_H3_SPIKE_SERVE=1`, set by the test itself). A measurement, not a regression gate; results are recorded in decision D3 of [HTTP3_IMPLEMENTATION.md](../HTTP3_IMPLEMENTATION.md) | `pkg/internal/h3fixture` | `APEXLOAD_H3_SPIKE=1 go test ./pkg/internal/h3fixture -run '^TestSpikeSocketOwnership$' -v -count=1` |
| `APEXLOAD_MP_BASE_URL` (with `APEXLOAD_MP_CONTROLLER_URLS`, `APEXLOAD_MP_MUTATE_GLOBAL_SETTINGS` and the other settings in [the suite README](../../scripts/multiproject/README.md)) | `TestMultiProjectConcurrency`: runs tests from three projects at once against one deployment through the REST API only. Covers simultaneous launch, cross-tenant denial, project-quota queueing and promotion, global fair share, a last-slot start race, per-run request counts against an independent counting target, replica agreement, and a 400 ms admission sampler | `scripts/multiproject` | `python3 scripts/multiproject/stack.py run` builds the images and runs it on a disposable production-posture Docker stack (two HA controllers, three mTLS generators, PostgreSQL, Redis, S3). `stack.py test --external <config.json>` runs it against an existing deployment such as AKS. `./scripts/run-optin-suites.sh --all` runs the Docker form. |
| `APEXLOAD_RUN_HA_FAILOVER_SOAK` | HA execution-leader failover soak | `pkg/api` | `APEXLOAD_RUN_HA_FAILOVER_SOAK=1 go test ./pkg/api -run HAFailoverSoak -count=1` |
| `APEXLOAD_RUN_PERFORMANCE_SMOKE` | Controlled performance probes that produce the perf-evidence artifacts | `pkg/perfevidence` | `make performance-smoke`, or `APEXLOAD_RUN_PERFORMANCE_SMOKE=1 go test ./pkg/perfevidence -count=1` |
| `APEXLOAD_RCA_E2E` | Root-cause v2 end to end with nothing faked: real local runs against an in-process HTTP target (a latency step, a fast baseline run and a staged run) through finalize and its sidecars, the analysis worker, the report's rca section, the bundle, the narration check and the exemplars. Long-running (about eight minutes), not infrastructure-bound | `pkg/api` | `APEXLOAD_RCA_E2E=1 go test ./pkg/api -run TestRCAEndToEnd -count=1 -timeout 30m` |
| `APEXLOAD_ELASTIC_INTEGRATION` | `TestVendorQualification` in [vendor_integration_test.go](../../pkg/elastic/vendor_integration_test.go): verified TLS, scoped reader permissions, discovery, filtered aliases, DSL metrics, PIT pagination beyond 10,000 documents, scrubbing, and Elasticsearch synchronous/asynchronous ES\|QL | `pkg/elastic` | Provision a disposable Elasticsearch or OpenSearch fixture, then set `APEXLOAD_ELASTIC_INTEGRATION_URL`, `APEXLOAD_ELASTIC_INTEGRATION_PRODUCT`, `APEXLOAD_ELASTIC_INTEGRATION_VERSION`, `APEXLOAD_ELASTIC_INTEGRATION_USERNAME`, `APEXLOAD_ELASTIC_INTEGRATION_PASSWORD`, and `APEXLOAD_ELASTIC_INTEGRATION_CA`; optionally set `APEXLOAD_ELASTIC_INTEGRATION_SERVER_NAME` for TLS verification. Run `APEXLOAD_ELASTIC_INTEGRATION=1 go test ./pkg/elastic -run '^TestVendorQualification$' -count=1 -v`. The fixture setup credentials create and delete test indices and a scoped reader; never point this suite at production. See [recorded exact-version qualifications](../ELASTIC_QUALIFICATION.md) for tested images, commands, results, and deployment limits. |
| `APEXLOAD_TEST_ARCHIVE_S3` | PostgreSQL/S3 archive identity and checksum rejection through fresh HA HTTP readers; uploads into a unique test prefix and cleans it afterward | `pkg/api` | Set to `1` with `APEXLOAD_TEST_POSTGRES_URL`, standard AWS test credentials, and `APEXLOAD_ARCHIVE_BUCKET`, `APEXLOAD_ARCHIVE_REGION`, `APEXLOAD_ARCHIVE_ENDPOINT` pointing at a disposable S3-compatible bucket; run `go test -tags postgres_integration ./pkg/api -run 'TestPostgres(S3)?ArchiveIdentityAfterReload' -count=1` |
| `APEXLOAD_JAVA_CDS_TEST_JDK_HOME` | Generate a secret-free AppCDS archive and validate the operator manifest against a real HotSpot 17+ VM; does not establish memory savings or containment | `pkg/javarunner` | Point to the test JDK home; run `go test ./pkg/javarunner -run TestOperatorCDSRealArchive -count=1` |
| `APEXLOAD_JAVA_EVIDENCE_FIXTURE_DIR` | Explicit Java fixture IPC v2 and shared-loader pooled lifecycle checks; do not establish containment or memory performance | `runners/java_vuser` | Point to prebuilt `fixtures.jar` and `lib/*.jar` outputs; requires `java` and `javac`; run `go test ./runners/java_vuser -run TestJavaEvidenceFixturesThrough -count=1` |
| `APEXLOAD_JAVA_POD_DOCKER_IMAGE` | Restricted local Linux PID 1 containment probe: cold preparation, ignored interrupts, detached descendants and supervisor failure; not AKS qualification | `runners/java_vuser` | Use an already-built local Alpine JDK 21 image with `java` at `/usr/lib/jvm/java-21-openjdk/bin/java`; requires Docker, Go, `javac` and `jar`; no pull/install occurs; run `go test ./runners/java_vuser -run TestDedicatedPodDockerContainment -count=1` |
| `APEXLOAD_TEST_POSTGRES_URL` | Real PostgreSQL scheduler migration and concurrent initialization: isolated schemas verify `INTEGER` to `BIGINT` preservation, eight simultaneous starts/reopens, retained settings, one-connection pools, lock cancellation/release and failure after loss of the locked session; the CI and opt-in selection also covers the feature-completion storage tests and the scenario authoring API across replicas | `pkg/storage`, `pkg/api` | Against a dedicated test database whose user may create/drop schemas: `APEXLOAD_TEST_POSTGRES_URL='postgres://...' go test -tags postgres_integration ./pkg/storage -run '^TestPostgres(SchedulerLeaseGenerationMigration\|ConcurrentInitialization\|InitializationLockLifecycle)$' -count=1` |
| `KAFKA_BROKERS`, `KAFKA_TOPIC` | Live Kafka produce/consume integration and imported AsyncAPI payload fidelity | `pkg/protocol/kafka`, `pkg/convert/asyncapi` | `KAFKA_BROKERS=localhost:9092 go test -tags "kafka_real kafka_integration" ./pkg/protocol/kafka ./pkg/convert/asyncapi -count=1` (`KAFKA_TOPIC` optional for protocol smoke; AsyncAPI fixture creates/deletes its own topic) |
| `APEXLOAD_KAFKA_SEEK_FIXTURE` | Genuine owned-partition seek: earliest/latest/exact, unpolled/unowned refusal, broker revocation during lookup and approved-group commit fence | `pkg/protocol/kafka` | The bounded `scripts/test-kafka-owned-seek.sh --report <unused-path>` creates a protected fixture settings file and injects its path; requires Docker, Python 3, OpenSSL and Go. Direct `go test -tags 'kafka_real kafka_seek_integration' ./pkg/protocol/kafka -run '^TestKafkaOwnedSeekActualBroker$' -count=1` **fails** when the required fixture is absent. [Qualified profile and retained attempts](../archive/qa-reviews/MESSAGING_2026-10.md#feature-completion-kafka-seek-2026-10-05). |
| `APEXLOAD_KAFKA_TRANSACTION_FIXTURE` | Genuine read-committed latest/LSO, fresh-source consume-transform-commit/abort and stale producer fencing | `pkg/protocol/kafka` | The separate bounded `scripts/test-kafka-transactions.sh --report <unused-path>` creates and injects its protected settings; same local tools, separate broker ports and exact transaction ACLs. Direct `go test -tags 'kafka_real kafka_transaction_integration' ./pkg/protocol/kafka -run '^TestKafkaTransactionsActualBroker$' -count=1` **fails** without the fixture. [Qualified fresh-source profile and explicit open gates](../archive/qa-reviews/MESSAGING_2026-10.md#feature-completion-kafka-transactions-2026-10-05). |
| `SOLACE_AMQP_URL`, `SOLACE_QUEUE` | Live Solace AMQP integration and imported topology payload fidelity | `pkg/protocol/jms`, `pkg/convert/brokerconfig` | `SOLACE_AMQP_URL=amqp://... SOLACE_QUEUE=q go test -tags 'jms_solace_real jms_solace_integration' ./pkg/protocol/jms -count=1` |
| `SERVICEBUS_CONNECTION_STRING` *or* `SERVICEBUS_NAMESPACE`; plus `SERVICEBUS_TOPIC`, `SERVICEBUS_SUBSCRIPTION`, `SERVICEBUS_REPLY_QUEUE` for the topic/reply cases | Live Azure Service Bus integration | `pkg/protocol/jms` | `SERVICEBUS_CONNECTION_STRING=... go test -tags 'jms_servicebus_real jms_servicebus_integration' ./pkg/protocol/jms -count=1` |
| `EVENTHUBS_CONNECTION_STRING` (plus `EVENTHUBS_EVENT_HUB`, optional `EVENTHUBS_PARTITION_ID`) | Live Azure Event Hubs round trip — Event Hubs has no GA local emulator | `pkg/protocol/eventhubs` | `EVENTHUBS_CONNECTION_STRING=... EVENTHUBS_EVENT_HUB=hub go test -tags eventhubs_real ./pkg/protocol/eventhubs -count=1` |

### Build-tag gates

| Build tag | What it swaps in | Package(s) | How to run |
|---|---|---|---|
| `recording_e2e` | Enables a loopback-only CDP attachment hook in test-built controller/agent binaries; normal builds ignore the hook | `pkg/recorder/browser` and dedicated dashboard E2E | `scripts/run-recording-e2e.sh`; see [Recording E2E](E2E_SUITES.md#3-recording-and-replay-suite). The separate Playwright configuration excludes these scenarios from the normal test cycle. |
| `apexload_fault_probe` | Enables a bounded post-evacuation-guard pause only in a deliberately tagged controller build; standard and enterprise builds compile a no-op. `APEXLOAD_FAULT_HOLD_AFTER_EVACUATION_GUARD` accepts a duration up to two minutes for a disposable cluster trial. | `pkg/api` | `go test -tags apexload_fault_probe -run '^TestFaultProbe' ./pkg/api -count=1`; deploy the tagged binary only to a disposable fault cluster. |
| `kafka_real` | franz-go network client instead of the in-memory mock broker | `pkg/protocol/kafka`, `pkg/dsl` | `go test -tags kafka_real ./pkg/protocol/kafka ./pkg/dsl -count=1` |
| `kafka_integration` | Live-broker Kafka tests (also needs `KAFKA_BROKERS`) | `pkg/protocol/kafka` | see the env table above |
| `kafka_seek_integration` | Separate genuine owned-seek acceptance with `kafka_real`; its four required cases cannot skip or qualify from offline helpers | `pkg/protocol/kafka` | `scripts/test-kafka-owned-seek.sh --report <unused-path>` or `scripts/run-optin-suites.sh --all`; pinned Apache Kafka 3.9.2 single-broker TLS/SASL PLAIN, read-uncommitted scope only. |
| `kafka_transaction_integration` | Separate genuine LSO/transaction/fencing acceptance with `kafka_real`; all three cases and independent offset/visibility oracles are mandatory | `pkg/protocol/kafka` | `scripts/test-kafka-transactions.sh --report <unused-path>` or `scripts/run-optin-suites.sh --all`; fresh-source single-broker TLS/SASL PLAIN only. Restart/continuation, multi-broker and hosted/provider breadth remain open. |
| `jms_solace_real` | Real AMQP adapter instead of the mock | `pkg/protocol/jms` | `make test-jms-solace-real` |
| `jms_solace_integration` | Live Solace tests (with `jms_solace_real`, also needs `SOLACE_AMQP_URL`) | `pkg/protocol/jms` | see the env table above |
| `jms_servicebus_real` | Real Azure Service Bus adapter instead of the mock | `pkg/protocol/jms`, `pkg/dsl` | `go test -tags jms_servicebus_real ./pkg/protocol/jms ./pkg/dsl -count=1` |
| `jms_servicebus_integration` | Live Service Bus tests (with `jms_servicebus_real`) | `pkg/protocol/jms` | `go test -tags 'jms_servicebus_real jms_servicebus_integration' ./pkg/protocol/jms -count=1` |
| `eventhubs_real` | Real azeventhubs/AMQP adapter instead of the mock | `pkg/protocol/eventhubs`, `pkg/dsl` | `go test -tags eventhubs_real ./pkg/protocol/eventhubs ./pkg/dsl -count=1` |
| `analytics_duckdb` | Native embedded DuckDB import, disclosure-safe preview, normalization, bounded queries and cancellation; requires CGO and a supported native platform | `pkg/analysis/duckdb`, `pkg/api` | `go test -tags analytics_duckdb ./pkg/analysis/... ./pkg/api -run 'Test(Analysis\|Native\|Parquet\|Container\|Lagged\|Bucket)'`; desktop native CI covers supported release platforms. The NA-6 reference workload (`APEXLOAD_ANALYTICS_BENCHMARK=1 go test -tags analytics_duckdb ./pkg/api -run '^TestNativeAnalyticsReferenceBenchmark$' -v`, a 1 GiB partitioned import with timed queries) runs as the "native analytics reference workload" suite under `--all`, writing its results and an F4 acceptance request; see [ANALYTICS_ACCEPTANCE.md](ANALYTICS_ACCEPTANCE.md). The 10 GB acceptance benchmark remains separately unverified. |
| `postgres_integration` | Real PostgreSQL catalog/DDL migrations and archive precision/reload tests (also needs `APEXLOAD_TEST_POSTGRES_URL`) | `pkg/storage`, `pkg/api` | see the env table above |
| `multiproject_integration` | Compiles the black-box multi-project concurrency suite; it still skips until `APEXLOAD_MP_BASE_URL` names a deployment | `scripts/multiproject` | see the env table above |

Note the paired `!tag` files (`kafka_unavailable_default_test.go`,
`servicebus_unavailable_test.go`, `eventhubs_unavailable_default_test.go`,
`validate_*_default_test.go`). Those run in the DEFAULT build and pin the
mock/unavailable behaviour, so the default suite is not blind to these protocols —
it just never exercises the real client.

The two bounded Kafka wrappers provision their own broker when explicitly selected
by `scripts/run-optin-suites.sh --all`; they do not require `KAFKA_BROKERS`. The
runner allocates a fresh private receipt directory and prints its path. Missing
tools, missing required fixture settings, failed cases and absent/skipped case
markers fail qualification. A `SKIP` in the default runner or a `LIST` entry
means the suite was not run and establishes no provider acceptance. The wrapper
control regressions are offline evidence for registration and failure propagation;
they do not replace the genuine broker receipts linked above.

### Not opt-in

Platform constraints (`//go:build windows`, `darwin`, `linux`, the BSD list) and
`//go:build race` / `!race` are not opt-in gates: they select per-platform or
per-`-race` implementations and one branch always runs. The inventory guard ignores
them.

### Adding a new opt-in suite

1. Add the row to the matching table above.
2. Add it to `scripts/run-optin-suites.sh` so it is actually runnable in one command.
3. Make the skip message name the gate and the command that satisfies it — a bare
   `t.Skip("skipping")` is how a suite becomes invisible.

#### VUser footprint evidence

`APEXLOAD_VUSER_FOOTPRINT=1 go test ./pkg/perfevidence -run TestVUserFootprint` runs the opt-in local memory probe. `APEXLOAD_VUSER_FOOTPRINT_PG_DSN` supplies the optional PostgreSQL DSN for that suite; use a disposable local database.
