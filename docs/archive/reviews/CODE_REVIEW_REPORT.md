# APEX Load — Full Codebase Review

> Status: Historical review snapshot. This file is preserved for audit context
> and should not be treated as current product documentation.

_Generated 2026-06-15. Multi-agent review (22 subsystem reviewers + per-finding adversarial verifiers, 49 agents) plus objective static analysis._

## Methodology

- **Static baselines:** `go build ./...` ✅, `go vet ./...` ✅, dashboard `tsc --noEmit` ✅, `govulncheck ./...` ✅ (0 reachable vulns), `gofmt` — 1 file unformatted (`pkg/protocol/http/oauth.go`).

- **Multi-agent pass:** the codebase was split into 22 subsystems (engine, API auth/orchestration/federation/handlers, controller+gRPC, all protocols, DSL, storage, recorder, the 21 importers, 4 dashboard slices, deployments/IaC, MCP+desktop). Each was independently audited for correctness, concurrency, security, resource, and error-handling defects. **Every** medium-or-higher finding was then handed to an independent adversarial verifier that re-read the actual code to confirm reachability and severity. All 27 reported findings below survived verification (0 false positives in the verified set); 43 lower-severity items were collected unverified.

## Summary

**Verified findings:** 1 critical · 8 high · 14 medium · 4 low.  **Plus** 43 additional low/nit items.

| # | Sev | Category | Area | Finding | Location |
|---|-----|----------|------|---------|----------|
| 1 | **critical** | concurrency | API test orchestration | Distributed goal-oriented test panics (nil deref) on first scale decision — test.goalControl never initialized in distributed path | `pkg/api/testexecution.go:843` |
| 2 | **high** | concurrency | API misc handlers | Data race: handleHealthCheck reads runner.tests map without holding the lock | `pkg/api/handlers.go:746` |
| 3 | **high** | concurrency | API test orchestration | Unsynchronized read/write of test.goalControl, test.Groups, test.GoalConfig/GoalTargets in the distributed metrics path races with locked writers and with sibling consumeMetrics goroutines | `pkg/api/distributed_execution_backend.go:917-928` |
| 4 | **high** | concurrency | Controller / distributed gRPC | Heartbeat reads s.assignments without holding s.mu (data race / possible fatal crash) | `pkg/distributed/generatorrpc/service.go:477,483` |
| 5 | **high** | concurrency | Controller / distributed gRPC | Concurrent Heartbeat streams race on the shared ResourceCollector state | `pkg/distributed/generatorrpc/service.go:477,502-507` |
| 6 | **high** | concurrency | Deployments / IaC / CI | On-prem controller overlay runs 2 replicas, but the boot-time test reconciler is not leader/lease-guarded → duplicate test launches | `deployments/k8s/onprem/controller-deployment.yaml:9` |
| 7 | **high** | resource-leak | Importers: enterprise/trueclient/devweb/jmeter | DevWeb ZIP import has no decompression-bomb or unbounded-memory guard (zip bomb DoS) | `pkg/convert/devweb/converter.go:137-159, 2003-2010` |
| 8 | **high** | resource-leak | Importers: openapi/asyncapi/postman/salesforce/etc | Unbounded recursion on self-referential XSD types crashes the controller (WSDL import) | `pkg/convert/wsdl/converter.go:679-722, 642-645, 710-714` |
| 9 | **high** | correctness | Other protocols | JMS buildMessage writes to a nil map → panics and crashes the generator | `pkg/protocol/jms/jms.go:691-708` |
| 10 | **medium** | security | API auth / security | Auth diagnostics endpoint is unauthenticated in OIDC/dev modes (admin-gated only for trusted-headers) | `pkg/api/handlers_auth.go:383-397` |
| 11 | **medium** | security | API federation / outbound / downloads | Federation/remote-run SSRF guard is bypassable when an outbound HTTP(S) proxy env var is set | `pkg/api/outbound_guard.go:65-101` |
| 12 | **medium** | error-handling | Binaries (cmd/*) | apexctl run --api polls forever on non-terminal / error responses (no status-code check, no deadline) | `cmd/apexctl/api_client.go:164-216` |
| 13 | **medium** | security | Controller / distributed gRPC | GetArchiveArtifact joins untrusted request.Name without path-traversal confinement | `pkg/distributed/generatorrpc/service.go:452-457` |
| 14 | **medium** | correctness | DSL parser / validate / templating | Nested JMS protocol actions inside control_if/control_loop spuriously require a target, ignoring config.jms.destination fallback | `pkg/dsl/validate.go:270-294, 720-727` |
| 15 | **medium** | correctness | Dashboard: settings / access / import | CSV export does not escape embedded double-quotes (malformed CSV + formula injection) in SourceMappingReview | `dashboard/src/components/SourceMappingReview.tsx:249-273` |
| 16 | **medium** | correctness | Dashboard: test flows & charts | useExecutionMetricsStream bleeds previous run's live metrics/history into the next run on testId change | `dashboard/src/components/useExecutionMetricsStream.ts:53-93` |
| 17 | **medium** | error-handling | Engine / VUser hot path | NewTokenBucket panics the process for very large pacing.tps (NewTicker(0)) | `pkg/engine/pacing.go:163-215` |
| 18 | **medium** | resource-leak | HTTP protocol (hot path) | Non-SSE response body is buffered and decompressed with no size limit (OOM / decompression-bomb on a misbehaving SUT) | `pkg/protocol/http/http.go:253, 432-446` |
| 19 | **medium** | correctness | Importers: har/k6/sample/paramize/correlate | JSON-numeric data-param replacement corrupts the field name when the value's digits appear in the key | `pkg/convert/paramize/dataparams.go:76-81` |
| 20 | **medium** | error-handling | Importers: openapi/asyncapi/postman/salesforce/etc | nil-pointer dereference on malformed imported target URLs in Salesforce analyzer | `pkg/convert/salesforce/analyzer.go:436-438, 662-667` |
| 21 | **medium** | concurrency | Other protocols | Data race on Browser.pwCDP between Playwright OnPage dispatch goroutine and the action goroutine | `pkg/protocol/browser/lifecycle.go:177-192` |
| 22 | **medium** | concurrency | Recorder / recording bridge | Data race on Session.Status and Session.StoppedAt: lifecycle methods write under r.mu, record/snapshot paths read under s.mu | `pkg/recorder/recorder.go:217-218, 239, 257, 326-327, 620, 743` |
| 23 | **medium** | security | scrub / selector / refkey / config / kubeops | JSON-body scrub pattern omits authorization/auth/session/csrf field names that the query/header sets redact, leaking secrets into HAR artifacts | `pkg/scrub/scrub.go:60` |
| 24 | **low** | correctness | Dashboard: ScriptEditor / RecordingStudio | CSV export breaks on (and is injectable via) imported source fields with embedded quotes | `dashboard/src/components/SourceMappingReview.tsx:249-273` |
| 25 | **low** | correctness | Importers: openapi/asyncapi/postman/salesforce/etc | OpenAPI converter emits non-existent fn.now() so date/date-time fields ship literal template text in request bodies | `pkg/convert/openapi/converter.go:799-801` |
| 26 | **low** | resource-leak | Recorder / recording bridge | Hijacked CONNECT/MITM tunnels have no read/idle deadline — goroutine + fd leak on idle or slow-loris clients | `pkg/recorder/tls.go:350-361` |
| 27 | **low** | security | Storage (SQLite/Postgres/archives) | Declared ON DELETE CASCADE on user_role_bindings is never enforced (FKs off on SQLite, stripped on Postgres) — DeleteUser orphans RBAC bindings | `pkg/storage/sqlite_schema.go:457-467, 335-349; pkg/storage/sqlite_auth.go:347-350` |

## Verified findings — detail

### 1. [CRITICAL] Distributed goal-oriented test panics (nil deref) on first scale decision — test.goalControl never initialized in distributed path
- **Area:** API test orchestration — **Category:** concurrency — **Confidence:** high
- **Location:** `pkg/api/testexecution.go:843`
- **Problem:** For distributed runs, startDistributedTestRun (distributed_execution_backend.go:620-668) initializes every remote* field but NEVER sets test.goalControl, leaving it nil. Only the LOCAL path (startManagedTestRun, testexecution.go:880) does `test.goalControl = &goalControllerState{}`. When a distributed test is goal_oriented (ScenarioMode=="goal_oriented" && GoalConfig!=nil), consumeMetrics -> applyRemoteMetricsBatch reaches `evaluateGoalController(live.Timestamp, currentTotalVUsers, live, test.GoalConfig, test.goalControl)` (distributed_execution_backend.go:923) with a nil state. evaluateGoalController tolerates a nil state (it is full of `if state != nil` guards) and, because shouldEvaluateScale(now, nil)==true (goal_controller.go:421-422), returns ShouldScale=true on the very first batch whenever live.TPS is below target (e.g. TPS=0 at ramp start). applyGoalControllerUpdate then executes the scale branch and unconditionally dereferences the nil pointer at `test.goalControl.lastScaleAt = &now` (testexecution.go:843), panicking. The panic is raised inside the per-target consumeMetrics goroutine (distributed_execution_backend.go:666) with no recover() anywhere in the call chain, so it crashes the entire controller process.
- **Fix:** Initialize test.goalControl in startDistributedTestRun (distributed_execution_backend.go) under tr.mu alongside the other run-state fields, mirroring startManagedTestRun (testexecution.go:880): add `test.goalControl = &goalControllerState{}` inside the `tr.mu.Lock()` block (around lines 652-658). As defense-in-depth, also guard applyGoalControllerUpdate (testexecution.go) so the scale branch can never nil-deref: before line 841, under the lock, do `if test.goalControl == nil { test.goalControl = &goalControllerState{} }`. Add a regression test exercising the distributed goal path: a goal_oriented test with non-nil GoalConfig whose first applyRemoteMetricsBatch (TPS below target) must not panic and must record lastScaleAt — the existing applyGoalControllerUpdate test pre-seeds goalControl, so a new test that does NOT pre-seed it is required to lock this in.

### 2. [HIGH] Data race: handleHealthCheck reads runner.tests map without holding the lock
- **Area:** API misc handlers — **Category:** concurrency — **Confidence:** high
- **Location:** `pkg/api/handlers.go:746`
- **Problem:** handleHealthCheck (GET /api/v1/system/health) reads len(s.runner.tests) directly, bypassing the RWMutex (tr.mu) that guards the tests map. Every other access to tr.tests in the package is performed under tr.mu (RLock for reads, Lock for writes); writers such as DeleteTest (tests.go:421), StoreScript-path scheduler (scheduler.go:462 `tr.tests[test.ID]=test`), and persistence loading (persistence.go:166) all mutate the map while holding tr.mu.Lock(). A concurrent test create/delete writing the map while this handler reads len() is a concurrent map read+write, which is undefined behavior under the Go memory model and can panic with 'concurrent map read and map write', crashing the controller. /system/health is a low-privilege endpoint frequently polled by load balancers and liveness probes, so the read side is easy to drive concurrently with test lifecycle churn.
- **Fix:** Read the map under the existing lock instead of touching it bare. Simplest: replace `"test_count": len(s.runner.tests),` at handlers.go:746 with `"test_count": len(s.runner.ListTests()),` (ListTests at tests.go:274 takes tr.mu). To avoid the extra slice allocation and the refresh side-effects ListTests performs, prefer adding a dedicated locked accessor on TestRunner, e.g. `func (tr *TestRunner) TestCount() int { tr.mu.RLock(); defer tr.mu.RUnlock(); return len(tr.tests) }`, and call `s.runner.TestCount()` from handleHealthCheck. Add a unit/race test (go test -race) that polls handleHealthCheck while concurrently creating/deleting tests to lock in the fix.

### 3. [HIGH] Unsynchronized read/write of test.goalControl, test.Groups, test.GoalConfig/GoalTargets in the distributed metrics path races with locked writers and with sibling consumeMetrics goroutines
- **Area:** API test orchestration — **Category:** concurrency — **Confidence:** high
- **Location:** `pkg/api/distributed_execution_backend.go:917-928`
- **Problem:** applyRemoteMetricsBatch releases tr.mu at line 915, then (917-928) calls aggregateDistributedMetricsBatches, buildLiveMetricsFromDistributedBatch(test, ...), applyRuntimeGoalTargetsToLiveMetrics(test, live), liveGroupVUsers(test, live), and evaluateGoalController(..., test.goalControl) — all WITHOUT holding tr.mu. These read test.Groups (buildLiveMetricsFromDistributedBatch lines 2211/2353-2355/2362; liveGroupVUsers goal_controller.go:339), test.GoalTargets/test.GoalConfig/test.ScenarioMode (applyRuntimeGoalTargetsToLiveMetrics goal_controller.go:353-357), and read/MUTATE test.goalControl.breachStartedAt (evaluateGoalController goal_controller.go:165-172). Concurrent writers hold the lock: UpdateGoalTargetTPS writes test.GoalTargets, test.GoalConfig and `test.goalControl.lastScaleAt = nil` under tr.mu (testexecution.go:343-346); applyGoalControllerUpdate writes test.Groups[i].VUsers and test.goalControl fields under tr.mu (testexecution.go:834,843-845); updateTestGroupVUsers writes test.Groups[i].VUsers under tr.mu. Moreover, one consumeMetrics goroutine is spawned per generator target (line 666), so with >1 generator multiple goroutines run applyRemoteMetricsBatch concurrently, each reading test.Groups and reading/writing test.goalControl unlocked at the same time. This is a data race on shared mutable ManagedTest state (the local broadcastMetrics path at metrics.go:911 has the same unlocked test.goalControl access).
- **Fix:** In applyRemoteMetricsBatch (and the identical broadcastMetrics/processMetricsSnapshot sites), snapshot the immutable inputs under tr.mu before unlocking, and serialize all goalControl reads/mutations under tr.mu. Concretely: while still holding tr.mu (before line 915), capture a copy of test.Groups, the GoalConfig pointer, ScenarioMode, and a clone of GoalTargets; pass those locals into buildLiveMetricsFromDistributedBatch/applyRuntimeGoalTargetsToLiveMetrics/liveGroupVUsers instead of dereferencing test under no lock. For the goalControl state machine, move the evaluateGoalController call so its breachStartedAt/lastScaleAt read+mutate runs under tr.mu (the same lock UpdateGoalTargetTPS and applyGoalControllerUpdate already use), or give goalControllerState its own mutex and guard breachStartedAt/lastScaleAt/lastScale* with it. This also closes the write/write race between sibling per-generator consumeMetrics goroutines on breachStartedAt. Add a -race test that runs a goal_oriented distributed run with >1 generator target while concurrently calling UpdateGoalTargetTPS and GetTest to assert the detector stays clean.

### 4. [HIGH] Heartbeat reads s.assignments without holding s.mu (data race / possible fatal crash)
- **Area:** Controller / distributed gRPC — **Category:** concurrency — **Confidence:** high
- **Location:** `pkg/distributed/generatorrpc/service.go:477,483`
- **Problem:** The Heartbeat gRPC handler calls s.activeVUsersLocked() (line 477) and s.availableCapacityLocked() (line 483, which itself calls activeVUsersLocked) WITHOUT holding s.mu. Both helpers iterate the s.assignments map (lines 665-671). Every other caller of these *Locked helpers (AssignWork, AdjustVUsers, StopTest, etc.) holds s.mu.Lock() while concurrently writing s.assignments. Heartbeat streams run continuously and concurrently with these mutators, so this is an unsynchronized concurrent map read against locked map writes. In Go this is a hard data race and can also produce a runtime 'fatal error: concurrent map iteration and map write' that crashes the generator process. I reproduced the race with the -race detector: a writer doing AssignWork + delete under the lock, racing a reader calling activeVUsersLocked()/availableCapacityLocked() exactly as Heartbeat does, fires 'WARNING: DATA RACE ... AssignWork() service.go:233 ... activeVUsersLocked() service.go:667'. The existing round-trip test misses it only because it performs the heartbeat after assignment is quiescent.
- **Fix:** Acquire the read lock around both bare reads in Heartbeat so the map iteration is synchronized with the write-locked mutators. Replace lines 477 and 483 with a single locked snapshot computed once, e.g.:

  s.mu.RLock()
  active := s.activeVUsersLocked()
  avail := s.availableCapacityLocked()
  s.mu.RUnlock()
  stats := generatorStatsFromResources(s.collectResources(), active)
  // ... then use `avail` for AvailableCapacity below

Compute collectResources() OUTSIDE the lock (it touches s.resCollector, not s.assignments) to avoid holding the RLock across resource collection. Alternatively add public wrappers activeVUsers()/availableCapacity() that take s.mu.RLock() internally (mirroring activeTestIDs at lines 654-663) and call those from Heartbeat. Add a -race regression test that spins AssignWork+StopTest in a loop on one goroutine while another loops calling the heartbeat read path concurrently (the existing round-trip test misses it because it heartbeats only after assignment is quiescent).

### 5. [HIGH] Concurrent Heartbeat streams race on the shared ResourceCollector state
- **Area:** Controller / distributed gRPC — **Category:** concurrency — **Confidence:** high
- **Location:** `pkg/distributed/generatorrpc/service.go:477,502-507`
- **Problem:** Heartbeat calls s.collectResources() -> s.resCollector.Collect(). ResourceCollector.Collect() (pkg/engine/sys_metrics.go) mutates internal delta-tracking state (lastCPU, lastRuntime, lastTime, lastNet, lastDisk, lastCgroup, initialized) on every call with NO mutex. A single Service shares one s.resCollector across all Heartbeat streams. The controller opens heartbeat streams per placement probe (probeGeneratorLoad in pkg/api/distributed_execution_backend.go), and the controller runs 2 replicas, so two controllers probing the same generator at once produce two concurrent server-side Heartbeat handlers calling Collect() on the same collector simultaneously -> data race on the collector's last* fields (and garbage CPU/GC deltas). I reproduced it with -race: four goroutines calling svc.collectResources() concurrently fire 'WARNING: DATA RACE ... ResourceCollector.Collect() sys_metrics.go:64 ... collectResources() service.go:506'.
- **Fix:** Serialize resource collection in the generator RPC service. Minimal fix: add a dedicated mutex to Service (e.g. resCollectorMu sync.Mutex) and wrap the call in collectResources(): lock around s.resCollector.Collect() so concurrent Heartbeat streams cannot enter Collect() simultaneously. Alternatively (broader, protects the engine path too), give ResourceCollector its own sync.Mutex and Lock/Unlock at the top of Collect() in pkg/engine/sys_metrics.go. While fixing, also close the adjacent race: take s.mu.RLock() around the s.activeVUsersLocked() and s.availableCapacityLocked() reads in Heartbeat (service.go:477,483) since those helpers assume the lock is held but the handler does not hold it — currently a concurrent read of s.assignments against AssignWork/StopTest writers that can panic. Add a -race test that fires several goroutines calling svc.Heartbeat (or collectResources) concurrently while AssignWork/StopTest mutate assignments, asserting no race/panic.

### 6. [HIGH] On-prem controller overlay runs 2 replicas, but the boot-time test reconciler is not leader/lease-guarded → duplicate test launches
- **Area:** Deployments / IaC / CI — **Category:** concurrency — **Confidence:** high
- **Location:** `deployments/k8s/onprem/controller-deployment.yaml:9`
- **Problem:** The production on-prem overlay sets the controller Deployment to replicas: 2 against a shared PostgreSQL store (configmap.yaml: APEXLOAD_STORE: postgres). The AKS overlay (deployments/k8s/aks/controller-deployment.yaml:12) and the Helm chart (values.yaml:51) both deliberately pin replicas: 1 with a comment that doing otherwise is unsafe until the "scheduler/reconciler singleton loops are PostgreSQL-lease guarded." Only the cron scheduler is lease-guarded: scenarioScheduler.RunDueSchedules() calls acquireScenarioSchedulerLease (pkg/api/scheduler.go:80-85). The other singleton loop — ReconcilePendingTests() → promotePendingTests() (pkg/api/testexecution.go:207-212, 938+) — runs unconditionally in NewTestRunner at every process boot (pkg/api/runner.go:801) with NO lease/leader guard; tr.mu only serializes within one process. With 2 replicas each loading the same QUEUED tests from PostgreSQL at startup, both controllers independently promote them to INITIALIZING and launch execution. Because every rolling update / pod restart boots a fresh replica that reconciles while the other is still running, this double-starts queued and scheduled load tests against the system under test (doubling VUsers) and corrupts run accounting. This is reachable in normal operation (any deploy or restart), not just a rare race.
- **Fix:** Two acceptable fixes (prefer both):

1) Immediate, low-risk: change deployments/k8s/onprem/controller-deployment.yaml:9 from `replicas: 2` to `replicas: 1`, matching the AKS overlay (deployments/k8s/aks/controller-deployment.yaml:12) and the Helm default (deployments/helm/apexload/values.yaml:51). This restores the project's own stated invariant ("Keep one active controller until scheduler/reconciler singleton loops are PostgreSQL-lease guarded").

2) Proper HA fix (required before any overlay sets replicas>1): gate the boot-time reconciler the same way the scenario scheduler is gated. Wrap the promotion in promotePendingTests()/ReconcilePendingTests() (pkg/api/testexecution.go:207-212, 938-988) behind a distinct durable lease, e.g. tr.scenarioStore.TryAcquireSchedulerLease("pending-reconciler", ownerID, now, ttl) (same primitive used at pkg/api/scheduler.go:180-185), so only the lease-holding replica promotes PENDING tests. Additionally, harden the store write so promotion is a compare-and-swap: make SaveTest (or a new conditional update) only transition PENDING->INITIALIZING when the current row status is still PENDING (add a `WHERE status='PENDING'` guarded UPDATE in pkg/storage/sqlite_tests.go and the pgx equivalent), and skip the in-memory go runTest() if the conditional update affected 0 rows. That makes double-start impossible even under a brief two-leader window during rolling updates. Add a multi-runner test (mirroring pkg/api/runner_test.go:4536 TestReconcilePendingTestsPromotesQueuedWork) asserting that two TestRunners sharing one store promote a PENDING test exactly once.

### 7. [HIGH] DevWeb ZIP import has no decompression-bomb or unbounded-memory guard (zip bomb DoS)
- **Area:** Importers: enterprise/trueclient/devweb/jmeter — **Category:** resource-leak — **Confidence:** high
- **Location:** `pkg/convert/devweb/converter.go:137-159, 2003-2010`
- **Problem:** devweb.ConvertZip iterates every entry in an attacker-supplied ZIP and calls readZipFile, which does an unbounded io.ReadAll(reader) on each entry's decompressed stream, storing every file fully in a files map[string]string. There is no per-entry cap, no cumulative-uncompressed cap, and no entry-count cap. The compressed upload is only bounded by webAPIImportMaxBodyBytes = 20 MiB (and the enterprise path below allows 100 MiB). A ~20 MiB DEFLATE 'zip bomb' expands ~1000x to tens of GiB held in memory at once, OOM-killing the controller (DoS). This is reachable two ways: (1) directly via POST /api/v1/scripts/import (DevWeb, .zip) through handleImportDevWeb -> ConvertZip; (2) via the enterprise endpoint: enterprisearchive.ConvertZip detects a DevWeb main.js and calls convertDevWebZipViaEnterpriseArchiveImport(zipData, ...) passing the ORIGINAL 100 MiB zipData, which devweb re-extracts with no caps — bypassing the enterprise importer's own caps. The sibling enterprisearchive.ConvertZip (pkg/convert/enterprisearchive/zip.go:44-56) demonstrates the intended guard (maxEnterpriseArchiveEntryBytes 64 MiB, maxEnterpriseArchiveTotalBytes 512 MiB via io.LimitReader); DevWeb is missing it entirely.
- **Fix:** Add a decompression-bomb guard to pkg/convert/devweb/ConvertZip mirroring the sibling enterprisearchive/RAML guards. Introduce package consts (e.g. maxDevWebEntryBytes = 64<<20, maxDevWebTotalBytes = 256<<20, maxDevWebEntries = 4000). In the loop at converter.go:143-152: enforce an entry-count cap; read each entry via io.ReadAll(io.LimitReader(rc, remaining+1)) against a shared `remaining` total-uncompressed budget (or a per-entry cap + accumulating total), and return an error when any entry or the cumulative total exceeds the cap. Update readZipFile to accept/return within a cap (or inline the LimitReader read in ConvertZip and drop the unbounded readZipFile usage there).

For the enterprise->devweb bypass at enterprisearchive/zip.go:92, do NOT pass the raw zipData. The enterprise ConvertZip already extracted and capped the entries into `files` (lines 36-68); change convertDevWebZipViaEnterpriseArchiveImport (and a corresponding devweb entrypoint) to operate on that already-capped `files` map rather than re-extracting zipData — e.g. add devweb.ConvertFromFiles(files, opts) that runs selectDevWebMainSource + convertSourceWithContext on the pre-extracted, already-bounded map. This both removes the duplicate extraction and closes the 100 MiB budget bypass.

Add table-driven tests in pkg/convert/devweb covering: a single oversized entry, a many-small-entries total-budget overflow, an excessive entry count, and a normal small archive (happy path), asserting the error for bomb cases and success for the benign case. Add an api-level test that the enterprise path with a DevWeb main.js bomb is rejected.

### 8. [HIGH] Unbounded recursion on self-referential XSD types crashes the controller (WSDL import)
- **Area:** Importers: openapi/asyncapi/postman/salesforce/etc — **Category:** resource-leak — **Confidence:** high
- **Location:** `pkg/convert/wsdl/converter.go:679-722, 642-645, 710-714`
- **Problem:** renderXSDFields -> renderXSDFieldSet -> renderXSDFields recurse through XSD type references with no cycle-detection set and no depth limit (the `depth` parameter is used only for indentation, never bounded). A complexType that references itself — e.g. `<complexType name="Node"><sequence><element name="child" type="tns:Node"/></sequence></complexType>`, an extremely common shape (tree/org-chart/linked-list payloads) — makes xsdFieldsForType return the same type forever, producing infinite recursion. This is reachable from the unauthenticated-in-dev / operator REST import path handleImportWSDL -> wsdl.Convert (pkg/api/handlers_web_api_imports.go:306) with a few-hundred-byte WSDL. A Go stack overflow is a fatal runtime throw that recover() cannot catch and that net/http's per-request recovery does NOT intercept, so the entire controller process (serving all tenants) crashes. I confirmed statically: xsdFieldsForType("Node") at line 600 always re-finds the same complexType and returns ok=true, and renderXSDFields line 710-712 recurses on it unconditionally. No existing test exercises a recursive type.
- **Fix:** Thread a path-scoped visited-set of resolved type/element names through soapBodySkeleton/soapHeaderSkeleton -> renderXSDFieldSet -> renderXSDFields (and the xsdFieldsForType/xsdFieldsForElement resolution sites), mirroring the OpenAPI resolver's `seen map[string]bool` (pkg/convert/openapi/converter.go:509-525).

Concretely: change renderXSDFieldSet/renderXSDFields to accept `seen map[string]bool` (a per-recursion-path set, copied or add-then-delete on the way back up so siblings that legitimately reuse a type still render once). At the three recursion sites in renderXSDFields (the inline-complexType branch L690-700, the element-ref branch L702-708, and the named-type branch L710-714), before recursing, resolve the target name (localName(field.Type) for the named-type case; the ref/element name for the others) and check `seen`. If the name is already on the current path, emit a placeholder element (e.g. `<tns:name>{{data.wsdl.<name>}}</tns:name>`) plus a diag.Warning ("WSDL XSD type", location, "recursive/self-referential type <name> detected; emitted a placeholder to break the cycle — fill in nested levels manually before replay") and `continue` instead of recursing. Otherwise add the name to `seen`, recurse, then remove it on return.

Additionally add a hard depth cap (e.g. depth >= 50) as defense-in-depth at the top of renderXSDFields that returns "" + a diag.Warning, in case of pathological mutually-recursive chains the name-set misses.

Add a table-driven test in pkg/convert/wsdl/converter_test.go feeding a self-referential complexType (the Node example) and a mutually-recursive A->B->A pair, asserting Convert returns without panic/stack-overflow, the script is produced, and the cycle diagnostic is present.

### 9. [HIGH] JMS buildMessage writes to a nil map → panics and crashes the generator
- **Area:** Other protocols — **Category:** correctness — **Confidence:** high
- **Location:** `pkg/protocol/jms/jms.go:691-708`
- **Problem:** buildMessage builds the message properties via `properties := cloneStringMap(j.cfg.Properties)`. cloneStringMap returns nil when the input is empty (jms.go:987-989). It then iterates `action.Metadata` and writes `properties[k] = s` for every string-valued entry. When the script does not configure `jms_properties` (so j.cfg.Properties is empty → properties is nil) but the action carries any string metadata, this is an assignment to a nil map, which panics. This is highly reachable: protocol.Action.Metadata is populated directly from the DSL action metadata (pkg/engine/vuser.go:1966) and the engine also auto-propagates string source metadata such as `source_transaction`/`loadrunner_transaction` into action metadata (pkg/engine/vuser.go:1788-1793), which is attached by the browser recorder and the LoadRunner importer. The panic is not recovered anywhere: there is no recover() in pkg/engine, pkg/distributed, or pkg/controller execution paths, so a panic in Proto.Execute tears down the entire generator process / VUser goroutine. The sibling kafka.buildRecord guards exactly this case (kafka.go:285-287: `if headers == nil && len(action.Metadata) > 0 { headers = make(...) }`); JMS is missing the guard. Reproduced the nil-map write panic standalone.
- **Fix:** In buildMessage (pkg/protocol/jms/jms.go), guard the nil map before the metadata loop, mirroring kafka.buildRecord:

  properties := cloneStringMap(j.cfg.Properties)
  if properties == nil && len(action.Metadata) > 0 {
      properties = make(map[string]string)
  }
  for k, v := range action.Metadata {
      if s, ok := v.(string); ok {
          properties[k] = s
      }
  }

Add a JMS test that calls Execute on a jms_publish action with non-empty string Metadata and no jms_properties config, asserting no panic and that the property is carried through.

### 10. [MEDIUM] Auth diagnostics endpoint is unauthenticated in OIDC/dev modes (admin-gated only for trusted-headers)
- **Area:** API auth / security — **Category:** security — **Confidence:** high
- **Location:** `pkg/api/handlers_auth.go:383-397`
- **Problem:** handleGetAuthDiagnostics is registered as an intentionally-public route (route_coverage_test.go intentionallyPublicAPIRoutes includes "GET /api/v1/auth/diagnostics"), and it only enforces a system-admin check when settings.Mode == authModeTrustedHeaders. In OIDC mode (the production-typical hardened mode) and in dev mode, the `if settings.Mode == authModeTrustedHeaders` block is skipped, so ANY unauthenticated caller receives the full buildAuthDiagnosticsResponse. For OIDC that response discloses internal configuration: issuer URL, redirect URL, configured scopes, env-var NAMES for the client/session secrets, and the provider label. It also calls buildOIDCDiscoveryCheck, which makes an outbound HTTP discovery request to the configured issuer (newOIDCProvider) — so an anonymous caller can both read auth config and trigger a controller-originated outbound probe. The existence of a deliberate admin gate for trusted-headers mode (and the matching test TestTrustedHeaderAuthRequiresSystemAdminForAuthDiagnostics) indicates the OIDC/dev paths were an oversight, not an intentional public surface. Values exposed are non-secret, which keeps this below high severity, but it is an unauthenticated information-disclosure inconsistent with the rest of the RBAC posture.
- **Fix:** Apply the admin gate in all non-bypass modes, not just trusted-headers. In handleGetAuthDiagnostics replace the `if settings.Mode == authModeTrustedHeaders` branch with `if !isAuthBypassMode(settings.Mode) { resolution, err := resolveRequestAuth(r, settings, s.store); if err != nil { writeError(w, http.StatusUnauthorized, err.Error()); return }; if resolution.User == nil || !resolution.User.hasGlobalRole(RoleSystemAdmin) { writeError(w, http.StatusForbidden, \"forbidden\"); return } }`. Note resolveRequestAuth resolves trusted-header identities; for OIDC the session-cookie path (resolveOIDCSession) should be checked instead/also so a legitimately-signed-in admin still passes — mirror however handleGetAuthMe/effective-access resolve OIDC identity. Add an OIDC-mode test asserting anonymous=401, non-admin session=403, admin session=200, paralleling TestTrustedHeaderAuthRequiresSystemAdminForAuthDiagnostics, and update TestHandleGetAuthDiagnosticsReturnsHealthyOIDCReadiness to authenticate via a real admin OIDC session rather than inert trusted-header values. Optionally move the route off intentionallyPublicAPIRoutes and wrap it in auth()/systemAdminAccess() so the public-surface test enforces the gate.

### 11. [MEDIUM] Federation/remote-run SSRF guard is bypassable when an outbound HTTP(S) proxy env var is set
- **Area:** API federation / outbound / downloads — **Category:** security — **Confidence:** medium
- **Location:** `pkg/api/outbound_guard.go:65-101`
- **Problem:** newGuardedHTTPClient is the SSRF-safe transport for ALL control-plane federation and remote-run calls (library probe/catalog/pull/push/diff, remote run create/start/status). Its protection relies on the custom DialContext resolving the target host and rejecting private/loopback/link-local/metadata IPs. However the same transport sets `Proxy: http.ProxyFromEnvironment`. When HTTP_PROXY/HTTPS_PROXY is present in the controller environment, the Go transport dials the PROXY address (which is what DialContext + isBlockedOutboundIP inspect) and then issues a CONNECT/absolute-URI request for the real target through the proxy. The proxy — not the guarded dialer — performs the final connection to the actual destination host, so the resolved-IP SSRF re-check never sees the target's IP. A registered remote base_url (validated only as a literal IP at create time, DNS names pass) pointing at an internal host or 169.254.169.254 would then be reachable via the proxy. Notably the host-allowlisted GitHub client (newHostAllowlistedHTTPClientWithTLS in security.go) deliberately does NOT set a Proxy, so this inconsistency is specific to the federation client. Blast radius is limited because it requires a proxy env var to be configured on the controller, but on-prem deployments commonly set egress proxies.
- **Fix:** Make the guarded transport's protection robust to proxies. Preferred, lowest-risk: drop `Proxy: http.ProxyFromEnvironment` from newGuardedHTTPClient so all federation/remote-run dials go directly through the IP-checking DialContext (matches the GitHub allowlisted client and the webhook/external-metric clients, which set no Proxy). If an egress proxy genuinely must be supported for these calls, then the dial-time IP check is insufficient and must be supplemented by a request-time TARGET-host check that runs regardless of proxy: in the federation/remote-run request builders (library_federation.go ~1266/1313/1361 and the remote-run create/start path), call validateOutboundURL/validateOutboundRemoteHost on the fully-resolved target URL immediately before .Do(req), and additionally resolve the target DNS name and run isBlockedOutboundIP on every returned address there (accepting the small DNS-rebind window, or pinning the resolved IP). Also document that when a proxy is configured the proxy itself is a trusted boundary. Add a test that sets HTTP_PROXY/HTTPS_PROXY to a stub and asserts a base_url whose DNS name resolves to a loopback/link-local/metadata address is rejected, mirroring outbound_guard_test.go's direct-dial cases.

### 12. [MEDIUM] apexctl run --api polls forever on non-terminal / error responses (no status-code check, no deadline)
- **Area:** Binaries (cmd/*) — **Category:** error-handling — **Confidence:** high
- **Location:** `cmd/apexctl/api_client.go:164-216`
- **Problem:** pollTest() drives the `apexctl run --api ...` path. Inside its unbounded `for {}` loop it issues GET /api/v1/tests/{id}, then immediately does `json.NewDecoder(resp.Body).Decode(&test)` WITHOUT checking resp.StatusCode and WITHOUT checking the decode error. If the controller responds with anything that is not a terminal ManagedTest body — a 404 (test deleted/garbage-collected mid-run), a 401/403 (auth posture flips), or a 5xx with a JSON error envelope — `test.Status` stays the empty string, never matches "COMPLETE"/"FAILED"/"ABORTED", and the loop sleeps 2s and retries indefinitely. There is no timeout/deadline here (unlike WaitForTest, which both validates the status code via GetTestStatus at lines 225-230 AND enforces a deadline). Net effect: a CLI invocation used as a CI gate hangs forever, consuming a pipeline runner, with no diagnostic output beyond repeated `... Status: ` lines.
- **Fix:** In pollTest, after the Get and before decoding, validate the response status code, and surface decode errors. Concretely: treat 404 as a hard error (`return nil, fmt.Errorf("test %s not found", testID)`), treat other non-2xx as an error (or bounded transient retry), and capture the json decode error instead of discarding it. Additionally add a bounded deadline mirroring WaitForTest so the loop cannot spin forever. The cleanest fix is to delete pollTest's bespoke loop and have RunPreparedTest delegate to the already-correct WaitForTest (which both checks status via GetTestStatus and honors a deadline), threading a timeout through RunPreparedTest. If RunPreparedTest must stay separate, at minimum reuse GetTestStatus (which already handles 404/non-200) inside the poll loop and add a deadline parameter, plumbing a new `--timeout` flag into parseRunArgs for the `run` command.

### 13. [MEDIUM] GetArchiveArtifact joins untrusted request.Name without path-traversal confinement
- **Area:** Controller / distributed gRPC — **Category:** security — **Confidence:** medium
- **Location:** `pkg/distributed/generatorrpc/service.go:452-457`
- **Problem:** The generator-side GetArchiveArtifact handler builds the file path as filepath.Join(record.ArchivePath, request.GetName()) with no validation of request.GetName(). filepath.Join cleans but does not confine, so a name like '../../../etc/passwd' (or an absolute path / Windows volume) resolves outside record.ArchivePath, and the handler returns os.ReadFile(fullPath) content back over gRPC — an arbitrary-file-read primitive on the generator pod. The only barrier is that the caller is the authenticated controller (mTLS+token) and the normal flow passes manifest-derived names, so this is a hardening gap rather than a directly exploitable hole. Notably the controller side already has the exact confinement primitive — normalizeArchiveArtifactName/archiveArtifactPath in pkg/api/archive_manifest.go reject '..', absolute, backslash, NUL, and Windows-volume names — but the generator gRPC handler does not apply any equivalent check, so it trusts a field that the rest of the codebase treats as untrusted.
- **Fix:** In GetArchiveArtifact (pkg/distributed/generatorrpc/service.go), confine request.GetName() to record.ArchivePath before reading. Add a small local equivalent of pkg/api/archive_manifest.go's checks (the generatorrpc package can't import the api package, so duplicate the logic or move it to a shared helper, e.g. pkg/storage or pkg/refkey): reject names containing "..", a leading "/" (path.IsAbs/filepath.IsAbs), backslashes, NUL bytes, or a Windows volume prefix; then compute fullPath = filepath.Join(record.ArchivePath, filepath.FromSlash(cleaned)) and verify filepath.Rel(filepath.Clean(record.ArchivePath), fullPath) does not equal ".." or start with ".."+os.PathSeparator. On any violation return status.Error(codes.InvalidArgument, ...). Keep the special-case where name == "manifest.json" maps to record.ManifestPath. Add a table-driven unit test alongside service_test.go covering "../../etc/passwd", "/etc/passwd", a backslash/NUL/Windows-volume name, and a valid nested name, asserting InvalidArgument for the escapes and success for the legitimate name.

### 14. [MEDIUM] Nested JMS protocol actions inside control_if/control_loop spuriously require a target, ignoring config.jms.destination fallback
- **Area:** DSL parser / validate / templating — **Category:** correctness — **Confidence:** high
- **Location:** `pkg/dsl/validate.go:270-294, 720-727`
- **Problem:** Top-level JMS actions are validated by validateJMSScriptIssues, which lets jms_publish/jms_consume/jms_request_reply omit `target` when config.jms.destination is set (line 725). But when such an action is nested inside a control_loop/control_if body, validation reaches it via a different path: validateJMSScriptIssues -> validateEngineActionIssues(protocol="jms-solace") -> validateControlFlowIssues -> validateActionForProtocolAtPath(protocol="jms-solace", nested action). The protocol switch in validateActionForProtocolAtPath (lines 270-285) has no case for jms (or kafka), so the nested jms_publish falls through to the generic tail at line 292 (`if action.Target == "" { ... missing target }`) which has no knowledge of the config.jms.destination fallback. A valid script that uses a control block over JMS actions with a config-level destination is therefore rejected with a false `"missing target"` error. Confirmed empirically: a jms-solace script with config.jms.destination="orders.in" and a control_loop wrapping a jms_publish (no target) yields `actions[0].control.steps[0] "pub": missing target`.
- **Fix:** In validateActionForProtocolAtPath (pkg/dsl/validate.go), handle the jms family before the generic target check. Since destination fallback rules depend on script-level config (config.jms.destination) which is not available in this function's signature, the cleanest fix is to route JMS-family nested actions through the same destination-aware logic used at top level. Option A: thread the *Script (or its ScriptConfig) into validateControlFlowIssues/validateActionForProtocolAtPath so nested jms_publish/jms_consume/jms_request_reply can apply the `action.Target == "" && jms.Destination == ""` rule (mirroring line 725) instead of the unconditional `missing target`. Option B (minimal): in validateActionForProtocolAtPath, add a guard so the generic `target required` rule is NOT applied to jms_* (and ideally kafka_*) action types — i.e. `if ProtocolFamily(protocol) == \"jms\" { return issues }` before line 292 — accepting that the per-action destination-vs-config check then only runs at the top level (slightly under-validates nested actions but never produces a false positive). Add a table-driven test in pkg/dsl/validate_test.go covering a jms-solace script with config.jms.destination set and a control_loop/control_if wrapping a target-less jms_publish, asserting no `missing target` issue; mirror for the negative case (no config destination, no target → still flagged). Separately consider fixing the Kafka validator to recurse into control bodies so control_loop/control_if are not rejected as unsupported Kafka action types.

### 15. [MEDIUM] CSV export does not escape embedded double-quotes (malformed CSV + formula injection) in SourceMappingReview
- **Area:** Dashboard: settings / access / import — **Category:** correctness — **Confidence:** high
- **Location:** `dashboard/src/components/SourceMappingReview.tsx:249-273`
- **Problem:** exportCsv() builds the CSV by wrapping most fields in double-quotes but only the `notes` field escapes interior quotes (`.replace(/"/g, '""')`). The `source_file`, `function`, `target_path`/`target_name`, `status`, and `support_mode` cells are emitted as `"${value}"` with no quote-doubling. Any of these values that contains a `"` (these strings originate from imported third-party source archives — LR/JMeter scripts, function names, file paths — so they are attacker/foreign-data influenced) breaks the CSV row structure, corrupting the export and shifting all subsequent columns. Separately, because no cell is prefixed-guarded, a value beginning with `=`, `+`, `-`, or `@` becomes a live formula when the exported file is opened in Excel/Sheets (CSV formula injection). The mapping data comes straight from the converted import (e.g. a function name or notes field), so a crafted source archive can plant a formula in the downloadable review CSV.
- **Fix:** In dashboard/src/components/SourceMappingReview.tsx, route every cell of every CSV row through one helper that both doubles interior quotes and neutralizes leading formula characters, e.g.:

const csvCell = (v: unknown) => {
  let s = String(v ?? '')
  if (/^[=+\-@\t\r]/.test(s)) s = `'${s}`
  return `"${s.replace(/"/g, '""')}"`
}

Then build the row as:
[
  csvCell(m.source_file || ''),
  csvCell(m.start_line ?? ''),
  csvCell(m.function || ''),
  csvCell(m.target_path || m.target_name || ''),
  csvCell(m.status || ''),
  isApproved ? 'Yes' : 'No',
  csvCell(m.support_mode || ''),
  csvCell(m.notes || '')
].join(',')

(The 'Approved' Yes/No column is code-controlled so it may stay unquoted, though wrapping it is harmless.) Add a unit test feeding a mapping whose function/source_file contains a `"` and one starting with `=` to assert the exported row is well-formed and formula-prefixed, consistent with the existing escaping convention in scriptEditorUtils.ts:506 and testExecutionChartModel.ts:1115.

### 16. [MEDIUM] useExecutionMetricsStream bleeds previous run's live metrics/history into the next run on testId change
- **Area:** Dashboard: test flows & charts — **Category:** correctness — **Confidence:** high
- **Location:** `dashboard/src/components/useExecutionMetricsStream.ts:53-93`
- **Problem:** The hook holds localMetricsHistory and latestMetrics in state that is only appended/replaced by incoming WebSocket messages. There is no effect that resets this state when testId changes. useWebSocket reconnects to the new test's stream, but the accumulated history and last-metrics snapshot from the previous test persist until the first message of the new test arrives (~1-2s, or never if the new run is COMPLETED/QUEUED so streaming is disabled by shouldStreamExecutionMetrics). The parent renders both consumers without a remount key: App.tsx renders <TestExecution testId={view.testId}> (line 714) and <CommandCenterPrototype testId={view.testId}> (line 681) with NO key={view.testId}, so navigating run A -> run B reuses the same instance and hook state. In TestExecution.tsx the stale latestMetrics is merged into statusMetrics (419-428) and passed to charts as latestLatency/latestMetrics (671-672), so run B momentarily shows run A's latency/metrics and a blended chart series. Sibling hook useExecutionArchiveResourceHistory correctly resets on testID change, confirming this hook is the outlier; its test only uses one fixed testId so the switch case is uncovered.
- **Fix:** Reset the accumulated stream state when testId changes, inside the hook so both callers (TestExecution and CommandCenterPrototype) are covered:

In dashboard/src/components/useExecutionMetricsStream.ts, add after the two useState declarations (line 55):

  useEffect(() => {
    setLocalMetricsHistory([])
    setLatestMetrics(undefined)
  }, [testId])

and add useEffect to the existing `import { useCallback, useState } from 'react'`.

This clears run A's history and last-metrics snapshot the moment testId flips, so run B starts from an empty stream regardless of whether B is streaming (RUNNING) or static (QUEUED/COMPLETE). Add a regression test that re-renders the harness with a second testId and asserts count resets to 0 and latestMetrics clears (the existing useExecutionMetricsStream.test.tsx only exercises a single fixed testId). A parent-level key={view.testId} on the two App.tsx render sites would also work but is broader and would discard unrelated local UI state (zoom, expanded chart, controls), so the hook-level reset is the safer localized fix.

### 17. [MEDIUM] NewTokenBucket panics the process for very large pacing.tps (NewTicker(0))
- **Area:** Engine / VUser hot path — **Category:** error-handling — **Confidence:** high
- **Location:** `pkg/engine/pacing.go:163-215`
- **Problem:** NewTokenBucket computes interval := time.Duration(float64(time.Second) / tps). For tps > 1e9 this truncates to 0. fill() then calls time.NewTicker(tb.interval) with a 0 interval, and time.NewTicker panics on a non-positive duration ("non-positive interval for NewTicker"). fill() runs in its own goroutine with no recover(), so the panic terminates the entire generator/controller process. The constructor only guards tps<=0/NaN/Inf, not the interval==0 case. This path is reachable from an uploaded script: Engine.Run -> newIterationRateLimiter (engine.go:298-303) -> NewTokenBucket(script.Config.Pacing.TPS) whenever Config.Pacing.Type=="tps". I confirmed pkg/dsl/validate.go places NO upper bound on Pacing.TPS, so an operator-authored (or malicious) script with e.g. {"pacing":{"type":"tps","tps":2000000000}} crashes the worker. By contrast runArrivalScheduler in open_workload.go:41-44 already guards interval<=0 by clamping to 1ns; NewTokenBucket does not.
- **Fix:** In NewTokenBucket (pkg/engine/pacing.go:167-171), clamp the interval after computing it, mirroring runArrivalScheduler (open_workload.go:42-44):

	interval := time.Duration(float64(time.Second) / tps)
	if interval <= 0 {
		interval = time.Nanosecond
	}
	tb := &TokenBucket{
		tokens:   make(chan struct{}, capacity),
		interval: interval,
		done:     make(chan struct{}),
	}

Additionally bound the token-channel capacity to avoid a pathological allocation for very large tps, e.g. capacity := int(math.Min(tps*2, 1e6)) + 1. Add a table-driven test in pkg/engine/pacing_test.go covering tps values above 1e9 (e.g. 2e9, 1e10) asserting NewTokenBucket does not panic and fill() runs, plus a defensive recover() is optional but the clamp is the real fix. Optionally also enforce a sane upper bound on Config.Pacing.TPS in pkg/dsl/validate.go for clearer operator feedback, but the engine-side clamp is the necessary guard since the engine is invoked directly by apexctl and the distributed worker as well as the controller.

### 18. [MEDIUM] Non-SSE response body is buffered and decompressed with no size limit (OOM / decompression-bomb on a misbehaving SUT)
- **Area:** HTTP protocol (hot path) — **Category:** resource-leak — **Confidence:** high
- **Location:** `pkg/protocol/http/http.go:253, 432-446`
- **Problem:** For the regular (non-SSE) path, the full response body is read with an unbounded io.Copy(buf, resp.Body) and then content-decoded via decodeGzipBody/decodeDeflateBody/decodeBrotliBody, each of which calls io.ReadAll on a decompression reader with no cap. There is no max-response-body knob anywhere in the pipeline (grep confirms only the API server's request bodyLimitMiddleware and the SSE path's defaultSSEMaxBodyBytes=8MiB cap exist). A single large response — or a small gzip/brotli payload that inflates to gigabytes — is fully materialized into the per-VUser pooled buffer AND copied into result.ResponseBody (a string). With the documented 10,000 VUsers/pod target, even a few-hundred-MB inflated response per VUser can OOM the generator pod. The SSE code path's own comment (sse.go:21-25) acknowledges exactly this risk ('Without a bound a long-lived ... stream grows the in-memory buffer without limit ... a VUser looping over such a read exhausts memory') and adds a default cap; the non-SSE body and the decompressors have no equivalent bound, an inconsistency. Even under the trusted-SUT model, a buggy backend returning an oversized or pathologically-compressible response degrades into a pod-wide availability failure rather than a single failed sample.
- **Fix:** Introduce a configurable response-body cap (e.g. APEXLOAD-level config / per-action param, default ~32-64 MiB, with opt-out <=0 for legitimate streaming/download tests) mirroring sse.go's defaultSSEMaxBodyBytes. On the non-SSE path (http.go:253), wrap the source as `io.Copy(buf, io.LimitReader(resp.Body, cap+1))` and, when n > cap, record/flag truncation on the Result and stop (so a single oversized response becomes a bounded failed sample rather than an OOM). In decodeHTTPResponseContent, bound each decompressor: replace the bare `io.ReadAll(reader)` in decodeGzipBody/decodeDeflateBody/decodeBrotliBody with `io.ReadAll(io.LimitReader(reader, cap+1))` and treat exceeding cap as a decode/limit error, so a compression bomb is bounded by the decoded-size cap instead of available RAM. Add table-driven tests covering: oversized raw body truncation, gzip/deflate/brotli inflate-past-cap, and the opt-out (cap<=0) preserving current unbounded behavior — matching the existing sse_test.go pattern.

### 19. [MEDIUM] JSON-numeric data-param replacement corrupts the field name when the value's digits appear in the key
- **Area:** Importers: har/k6/sample/paramize/correlate — **Category:** correctness — **Confidence:** high
- **Location:** `pkg/convert/paramize/dataparams.go:76-81`
- **Problem:** In collectBodyParams, the JSON-number branch builds the replacement via strings.Replace(match[0], value, template, 1), where match[0] is the whole '"key": number' segment and value is just the numeric literal. Because the replace targets the first occurrence of the bare number anywhere in the segment, when the value's digit string is a substring of the field name it replaces a character inside the key instead of the value. The resulting Location.ReplaceText is then applied verbatim by ApplySuggestions/applyReplacements (apply.go:97-103, strings.ReplaceAll over SearchText=match[0]), producing a corrupted, invalid-JSON body with a {{var.X}} template embedded in the key. Reachable end-to-end: keys like order1_id, user2_id, line1_id all pass LooksParameterName (heuristics.go:30-38) and contain a digit matching small numeric values. Reproduced: body {"order1_id": 1} yields ReplaceText "order{{var.order1_id}}_id": 1, applied body {"order{{var.order1_id}}_id": 1}. The string-value branch (dataparams.go:66-70) avoids this by quoting the value; only the number branch is affected. Existing tests use non-overlapping values (e.g. account_id=99887), so the bug is untested.
- **Fix:** In collectBodyParams's JSON-number branch (dataparams.go:76-81), stop using substring search for the replacement. The numeric value is always the final token of match[0], so splice at its known offset instead, e.g.:

  collector.add(key, value, "JSON body field", section, index, action,
      "body", match[0], func(template string) string {
          cut := strings.LastIndex(match[0], value)
          return match[0][:cut] + template + match[0][cut+len(value):]
      })

(strings.LastIndex is safe here because the value is the trailing token; for full robustness capture the value's start offset via jsonNumberPattern.FindAllStringSubmatchIndex and splice at match indices [4]:[5].) Add a table-driven test with overlapping keys: {"order1_id": 1} must yield body {"order1_id": {{var.order1_id}}}, plus line2_id=2 and user3_id=3 cases, to lock in the fix and cover the currently-untested overlap path.

### 20. [MEDIUM] nil-pointer dereference on malformed imported target URLs in Salesforce analyzer
- **Area:** Importers: openapi/asyncapi/postman/salesforce/etc — **Category:** error-handling — **Confidence:** high
- **Location:** `pkg/convert/salesforce/analyzer.go:436-438, 662-667`
- **Problem:** classifySalesforceAction does `parsed, _ := url.Parse(rawURL)` then immediately calls parsed.Hostname()/parsed.EscapedPath(); endpointObservations does the same with parsed.Host/parsed.EscapedPath(). url.Parse returns a NIL *url.URL on error (verified: inputs containing a space, a DEL/control byte, or invalid percent-encoding like "%zz" all yield nil,err). rawURL/ref.URL derive from action.Target of an imported script (enterprise-script-archive / HAR import), which is untrusted and may contain such characters. salesforce.Analyze runs unconditionally and reaches these via flattenRequestActions, so a single malformed target panics. The sibling helpers hostOf (line 1188) and equivalentURL (410-412) correctly check the error, showing the guarded pattern was intended but missed here. net/http's per-request recover keeps the process alive, but the import request panics and returns 500 — a request-level DoS on attacker/operator-controlled import content.
- **Fix:** Guard the discarded parse error/nil result in both functions, mirroring the existing hostOf/equivalentURL pattern. In classifySalesforceAction: `parsed, err := url.Parse(rawURL); if err != nil || parsed == nil { return "generic" }` before reading host/path. In endpointObservations: inside the loop, `parsed, err := url.Parse(ref.URL); if err != nil || parsed == nil { continue }` (skip the ref) before building key/Host/Path. Add a table-driven test in analyzer_test.go that runs Analyze over actions whose Target contains a control byte (\x7f), invalid percent-encoding (%zz), and an embedded colon, asserting no panic and sane classification.

### 21. [MEDIUM] Data race on Browser.pwCDP between Playwright OnPage dispatch goroutine and the action goroutine
- **Area:** Other protocols — **Category:** concurrency — **Confidence:** medium
- **Location:** `pkg/protocol/browser/lifecycle.go:177-192`
- **Problem:** installCDPSession writes `b.pwCDP = cdp` (lifecycle.go:188) WITHOUT holding b.mu. It is invoked from registerPage (lifecycle.go:132-134) which runs on Playwright's event-dispatch goroutine via the context OnPage handler (lifecycle.go:59-61) whenever a popup/new page activates during an action. Concurrently, the action goroutine reads b.pwCDP unlocked in captureRuntimeMetrics (runtime_metrics.go:116-123) at the end of every action when client metrics are enabled, and Teardown→close() reads/writes b.pwCDP unlocked (lifecycle.go:4-7). The code already acknowledges this cross-goroutine hazard for b.pwPage (the currentPage()/registerPage comment) and guards that field with b.mu, but b.pwCDP is left unsynchronized. This is a genuine -race-detectable data race; it triggers when a new page opens (e.g. a target=_blank click) while metrics capture or teardown runs.
- **Fix:** Guard all accesses to b.pwCDP with b.mu, mirroring the existing b.pwPage protection (note: the code is in pkg/protocol/browser/runtime_context.go, not lifecycle.go as the finding states).

1. In installCDPSession (runtime_context.go:188), assign under the lock: `b.mu.Lock(); b.pwCDP = cdp; b.mu.Unlock()`. (The cdp.Send Emulation call at 190 can stay outside the lock using the local cdp var.) Be careful that registerPage already released b.mu at line 131 before calling installCDPSession, so re-acquiring here is fine and avoids the nested-lock issue.

2. In captureRuntimeMetrics (runtime_metrics.go:116), snapshot under lock before use: `b.mu.Lock(); cdp := b.pwCDP; b.mu.Unlock(); if cdp != nil { ... use cdp ... }`.

3. In close() (lifecycle.go:4-7), move the pwCDP read/write under the same b.mu critical section already used for pwPage: `b.mu.Lock(); cdp := b.pwCDP; b.pwCDP = nil; page := b.pwPage; b.pwPage = nil; b.mu.Unlock(); if cdp != nil { _ = cdp.Detach() }; if page != nil { _ = page.Close() }`.

Add a -race test that drives an action while concurrently invoking registerPage (simulating an OnPage popup) and asserting no race, alongside captureRuntimeMetrics/close.

### 22. [MEDIUM] Data race on Session.Status and Session.StoppedAt: lifecycle methods write under r.mu, record/snapshot paths read under s.mu
- **Area:** Recorder / recording bridge — **Category:** concurrency — **Confidence:** high
- **Location:** `pkg/recorder/recorder.go:217-218, 239, 257, 326-327, 620, 743`
- **Problem:** Session.Status (and StoppedAt) is mutated by StopSession/PauseSession/ResumeSession while holding the Recorder-level lock r.mu (StopSession lines 205-218 sets s.Status="stopped" and s.StoppedAt under r.mu; PauseSession line 239 and ResumeSession line 257 likewise write s.Status under r.mu). The same fields are read on the recording hot path and the snapshot accessor while holding the Session-level lock s.mu: recordRequestWithOptions checks `if s.Status != "recording"` at line 620, appendWebSocketMessage at line 743, and snapshot() copies s.Status/s.StoppedAt at lines 326-327 — all under s.mu, never r.mu. Because r.mu and s.mu are different mutexes that do not exclude each other, these accesses race. The window is real and reachable: handleProxy (line 427-429) reads r.active under r.mu, releases r.mu, then runs recordRequestStarted/streamAndRecordResponse on the live *Session; concurrently an operator hitting POST /stop (handlers_recorder.go) calls StopSession. The dashboard polling GET /sessions/{id} -> GetSession -> snapshot() reading Status/StoppedAt races a concurrent stop the same way. The recent 'Fix data races in engine, recorder proxy' commit (1ce4b55d) addressed the s.Requests race by adding snapshot(), but left Status/StoppedAt guarded by the wrong lock.
- **Fix:** Guard Session.Status and Session.StoppedAt with s.mu everywhere (make s.mu their sole owning lock). In StopSession/PauseSession/ResumeSession, keep r.mu only for the r.sessions map lookup and r.active reassignment, but take s.mu around the status precondition check AND the write, e.g.:

  s, ok := r.sessions[id]   // under r.mu
  ...
  s.mu.Lock()
  if s.Status != "recording" && s.Status != "paused" { s.mu.Unlock(); return nil, err }
  now := time.Now(); s.Status = "stopped"; s.StoppedAt = &now
  s.mu.Unlock()
  if r.active != nil && r.active.ID == id { r.active = nil }  // still under r.mu

The precondition checks at 212/236/254 must move under s.mu too, so the check+set is atomic w.r.t. concurrent readers. Lock ordering r.mu -> s.mu is safe: the readers (recordRequestWithOptions, appendWebSocketMessage, snapshot) only ever hold s.mu and never acquire r.mu while holding it, so no reverse ordering / deadlock is introduced. Also note s.notify(...) in PauseSession/ResumeSession passes the live *s while holding r.mu — emitting the event after releasing both locks (or snapshotting first) avoids notifying with the lock held, though that is a secondary concern. Add a pkg/recorder concurrency test that starts a session, fires concurrent recordRequest-style calls and StopSession/GetSession, and runs under go test -race to lock in the fix.

### 23. [MEDIUM] JSON-body scrub pattern omits authorization/auth/session/csrf field names that the query/header sets redact, leaking secrets into HAR artifacts
- **Area:** scrub / selector / refkey / config / kubeops — **Category:** security — **Confidence:** high
- **Location:** `pkg/scrub/scrub.go:60`
- **Problem:** sensitiveJSONPattern only redacts JSON string fields whose name is exactly one of password|passwd|secret|token|access_token|refresh_token|id_token|api_key|apikey|session_id|csrf_token|client_secret. It is missing several names that the query-param/header sets DO treat as sensitive: notably `authorization`, `auth`, `session` (vs only `session_id`), `sessionid`, `csrf` (vs only `csrf_token`), and `xsrf`. scrub.HAR is the sole redaction pass for HAR artifacts written to disk and exported (pkg/protocol/browser/har_capture.go:110, pkg/api/handlers_browser_recorder.go:685, pkg/convert/har/converter.go:68), and request/response JSON bodies are scrubbed only via cp.value -> sensitiveJSONPattern. A response or request body such as {"authorization":"Bearer eyJ..."} or {"session":"<sid>"} therefore passes through unredacted and is persisted in the *_scrubbed.har artifact, even though the very same names in a query string or HTTP header are redacted. There is no separate Bearer-token regex to catch it, and the `token` alternative does not match because the JSON field name is anchored by quotes (must equal the whole name). The asymmetry between sensitiveParamNames (lines 269-275: includes session/sessionid/auth/csrf/xsrf) and the JSON pattern (line 60) is the concrete evidence this is an oversight rather than intentional.
- **Fix:** Align sensitiveJSONPattern's alternation with the param/header sensitive sets by adding authorization|auth|session|sessionid|csrf|xsrf (keeping all existing entries):

var sensitiveJSONPattern = regexp.MustCompile(`(?i)("(?:authorization|password|passwd|secret|token|access_token|refresh_token|id_token|api_key|apikey|auth|session|session_id|sessionid|csrf|csrf_token|xsrf|client_secret)"\s*:\s*")([^"]*)(")`)

Better, derive the JSON field list, sensitiveParamNames, and sensitiveParamPattern alternation from one shared []string constant so they cannot drift apart again (build the regexes from regexp.QuoteMeta-joined names). Add a table-driven test asserting JSON bodies like {"authorization":"Bearer ..."}, {"session":"<sid>"}, {"csrf":"..."}, {"xsrf":"..."}, {"auth":"..."}, {"sessionid":"..."} are redacted by HAR() in both request postData.text and response content.text. Optionally add a value-level Bearer-token regex (e.g. (?i)bearer\s+[A-Za-z0-9._~+/-]+=*) for defense in depth against tokens embedded in non-keyed body text.

### 24. [LOW] CSV export breaks on (and is injectable via) imported source fields with embedded quotes
- **Area:** Dashboard: ScriptEditor / RecordingStudio — **Category:** correctness — **Confidence:** high
- **Location:** `dashboard/src/components/SourceMappingReview.tsx:249-273`
- **Problem:** exportCsv() wraps every field in double quotes but only escapes embedded double-quotes for the `notes` field (line 261 uses `.replace(/"/g, '""')`). The other quoted fields — source_file (254), function (256), target_path/target_name (257), status (258), support_mode (260) — do NOT escape embedded `"`. These values originate from the imported LoadRunner/JMeter source (SourceReviewMapping is produced by the importer from operator-uploaded scripts), so they are untrusted/operator-influenceable. A single `"` in e.g. a function name or file path closes the CSV quoting early, corrupting that row and shifting all subsequent columns — a data-integrity bug in the exported review artifact. Secondarily, because no leading `=`/`+`/`-`/`@` neutralization is applied, a crafted field is also a classic CSV/Excel formula-injection vector when the export is opened in a spreadsheet.
- **Fix:** Add one CSV-escape helper and apply it to every string field in exportCsv() (SourceMappingReview.tsx), matching the existing pattern in testExecutionChartModel.ts:1115 / scriptEditorUtils.ts:506. Example:

const csvField = (v: unknown) => {
  let s = String(v ?? '')
  // Neutralize spreadsheet formula injection on values that begin with a formula trigger.
  if (/^[=+\-@\t\r]/.test(s)) s = "'" + s
  return '"' + s.replace(/"/g, '""') + '"'
}

Then build each row as:
return [
  csvField(m.source_file),
  m.start_line || '',
  csvField(m.function),
  csvField(m.target_path || m.target_name),
  csvField(m.status),
  isApproved ? 'Yes' : 'No',
  csvField(m.support_mode),
  csvField(m.notes),
].join(',')

Numeric start_line / the Yes/No literals don't need quoting. Add a unit test feeding a mapping whose function/source_file contains an embedded `"` and asserting the exported row parses back to the same column count.

### 25. [LOW] OpenAPI converter emits non-existent fn.now() so date/date-time fields ship literal template text in request bodies
- **Area:** Importers: openapi/asyncapi/postman/salesforce/etc — **Category:** correctness — **Confidence:** high
- **Location:** `pkg/convert/openapi/converter.go:799-801`
- **Problem:** For string schema fields with format `date-time`/`date`, sampleForSchema returns the templates {{fn.now()}} and {{fn.now("2006-01-02")}}. The engine has no `fn.now` builtin: resolveBuiltinFunction (pkg/engine/parameters.go:546-595) only recognizes fn.uuid, fn.timestamp.unix/unix_ms/iso8601/rfc3339, fn.random(...), fn.random_string(...). For an unrecognized fn.* token, resolveTemplateValue returns ("", false) and interpolateWithSecrets (pkg/engine/vuser.go:2015-2018) leaves the token verbatim. So every imported request body built from a date/date-time field is sent to the server containing the literal string `{{fn.now()}}`, corrupting the payload at runtime. There is no DSL validation that rejects unknown fn.* templates, so this passes import validation silently. The WSDL converter explicitly documented this exact pitfall and used fn.timestamp.iso8601 instead (pkg/convert/wsdl/converter.go:731-733), confirming intent.
- **Fix:** In pkg/convert/openapi/converter.go (sampleForSchema, the string-format switch ~lines 798-801), replace the non-existent builtins with engine-supported templates, mirroring the WSDL converter:
- case "date-time": return "{{fn.timestamp.iso8601}}", nil  (resolves to RFC3339 via resolveBuiltinFunction)
- case "date": return "2025-01-01", nil  (static placeholder; the engine has no date-only formatter, and fn.timestamp.iso8601 would emit a full timestamp). Alternatively wire it to a data-source column like the scalar-property path does.
Add an OpenAPI converter test covering a request body whose root schema is a bare date-time string and an array-of-date-strings body, asserting the emitted Body does NOT contain "{{fn.now()}}" (matching the existing WSDL guard at wsdl/converter_test.go:333-334). Optionally add DSL-level validation rejecting unknown fn.*/fake.* tokens so future misses fail at import rather than silently at runtime.

### 26. [LOW] Hijacked CONNECT/MITM tunnels have no read/idle deadline — goroutine + fd leak on idle or slow-loris clients
- **Area:** Recorder / recording bridge — **Category:** resource-leak — **Confidence:** medium
- **Location:** `pkg/recorder/tls.go:350-361`
- **Problem:** After the proxy hijacks a CONNECT connection and completes the TLS handshake, handleConnectMITM enters an unbounded `for { http.ReadRequest(reader) ... }` loop over the intercepted TLS connection with no read or idle deadline set on tlsConn. The http.Server's ReadTimeout/WriteTimeout (recorder.go lines 397-398) do not apply to a hijacked connection — it is fully detached. A client that establishes a tunnel and then stays idle (or trickles bytes) holds the goroutine and file descriptor open indefinitely. The same applies to forwardPlainConnect's loop. peekConnectTunnelByte sets a 10s deadline only for the first byte (lines 365-366) and clears it immediately, so it does not bound the subsequent request loop. There is no IdleTimeout on the http.Server either. Many such tunnels exhaust goroutines/fds.
- **Fix:** In handleConnectMITM's request loop (tls.go ~351), set an idle deadline before each ReadRequest and clear/extend it after: at the top of each iteration call `tlsConn.SetReadDeadline(time.Now().Add(idleTimeout))` (e.g. 60-120s), and after forwardHTTPS returns clear it with `tlsConn.SetReadDeadline(time.Time{})` (or extend per active request). Treat an os.ErrDeadlineExceeded / net.Error Timeout() read as end-of-tunnel and return to close the connection. Apply the identical change to forwardPlainConnect's loop on clientReader (tls.go:374-397). Additionally, set IdleTimeout (and optionally ReadHeaderTimeout) on the proxy http.Server in StartProxy (recorder.go:393-399) for defense-in-depth, though note IdleTimeout does not cover already-hijacked connections, so the per-loop deadline is the load-bearing fix. Add a unit test that opens a CONNECT tunnel, goes idle, and asserts the handler goroutine returns within the idle window.

### 27. [LOW] Declared ON DELETE CASCADE on user_role_bindings is never enforced (FKs off on SQLite, stripped on Postgres) — DeleteUser orphans RBAC bindings
- **Area:** Storage (SQLite/Postgres/archives) — **Category:** security — **Confidence:** high
- **Location:** `pkg/storage/sqlite_schema.go:457-467, 335-349; pkg/storage/sqlite_auth.go:347-350`
- **Problem:** The user_role_bindings table is declared with `FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE`, and DeleteUser relies entirely on that cascade — it runs only `DELETE FROM users WHERE id = ?` with no manual cleanup of bindings. However the cascade is non-functional on BOTH backends: (1) On SQLite (the default backend) foreign-key enforcement is OFF unless `PRAGMA foreign_keys=ON` is set, and grep confirms that pragma is never issued (sql.Open("sqlite", path) with a bare path). (2) On Postgres, stripPostgresForeignKeyReferences() removes every line containing `FOREIGN KEY` from CREATE/ALTER statements, so the constraint (and its ON DELETE CASCADE) is dropped at schema-creation time. Net effect: deleting a user leaves its role bindings — which grant RBAC permissions — orphaned in user_role_bindings. If a user ID is ever reused, the new principal silently inherits the stale grants. This is currently latent because store.DeleteUser has no production caller (only the per-binding DELETE handler is wired), but the integrity assumption is broken and any future wiring of DeleteUser inherits the bug.
- **Fix:** Make DeleteUser self-sufficient rather than relying on a non-functional FK cascade. In pkg/storage/sqlite_auth.go, wrap both deletes in a single transaction:

  func (s *SQLiteStore) DeleteUser(id string) error {
      tx, err := s.db.Begin()
      if err != nil { return err }
      defer tx.Rollback()
      if _, err := tx.Exec(`DELETE FROM user_role_bindings WHERE user_id = ?`, id); err != nil { return err }
      if _, err := tx.Exec(`DELETE FROM users WHERE id = ?`, id); err != nil { return err }
      return tx.Commit()
  }

(Use s.db.Exec via the existing rewrite layer so the `?` placeholders are translated for Postgres.) This is the reliable fix because the Postgres FK-stripping rewriter means you cannot depend on the DB-level cascade on either backend. Optionally also add a Begin/commit-safe explicit cleanup before wiring any future DELETE-user route. Enabling `PRAGMA foreign_keys=ON` on SQLite connections is a defensible hardening step but is insufficient alone (Postgres still has the constraint stripped), so keep the explicit delete regardless. Add a test that creates a user with bindings, calls DeleteUser, and asserts ListUserRoleBindings returns empty on both the SQLite and (rewritten) Postgres paths.

## Additional low / nit items

- **[low]** subtleQueryValue does not perform a constant-time comparison despite its name — `pkg/api/auth_oidc.go:638-640`
- **[low]** originAllowed same-origin fallback ignores scheme (host-only match) — `pkg/api/security.go:64-68`
- **[low]** Imported library result archive integrity check is skipped when bundle metadata omits the hash/size — `pkg/api/library_federation.go:1126-1131`
- **[low]** UpdateSystemSettings skips normalization for GitHub, capacity reservations, schedule defaults, and notifications — `pkg/api/settings.go:1268-1299`
- **[low]** StopTest reads test.multiEngine/test.cancel without holding tr.mu, racing the distributed start that assigns them — `pkg/api/testexecution.go:247-262`
- **[low]** GetTest shallow-copy shares mutable LiveMetrics/GoalConfig/Assignment/Archive pointers with the live test — `pkg/api/tests.go:32-37`
- **[low]** Controller graceful shutdown uses an unbounded context; SIGTERM can hang the process indefinitely — `cmd/controller/main.go:115-118`
- **[low]** API client silently ignores JSON decode errors on createTest/pollTest, masking server failures — `cmd/apexctl/api_client.go:146-148`
- **[low]** AssignWork does not reject negative VuserCount — `pkg/distributed/generatorrpc/service.go:175-201`
- **[low]** Standalone pkg/controller shares mutable *TestRun pointers across the RWMutex boundary — `pkg/controller/controller.go:84-92`
- **[low]** Condition expression normalizer mangles 'var' used as an intermediate map key (e.g. data.var.x -> data.vars.x) — `pkg/dsl/condition_expr.go:81-101`
- **[low]** Browser replay bundle export has no error handling around base64 screenshot decode — `dashboard/src/components/ScriptEditor.tsx:4607-4751`
- **[low]** Recording-agent command interpolates --addr unquoted while --allow-origin is shell-quoted — `dashboard/src/components/RecordingStudio.tsx:196-208`
- **[low]** Credential redaction is not applied to the user-visible error summary/rawMessage, only to evidence lines — `dashboard/src/utils/errorUtils.ts:191-202, 418-425`
- **[low]** Bridge requests leak dev-preview headers (active project id + dev-view mode) to a user-supplied recording-agent origin — `dashboard/src/hooks/api/browserRecorderBridge.ts:99-131`
- **[low]** WebSocket reconnect churn: bridge object identity changes on every storage event — `dashboard/src/hooks/useWebSocket.ts:36-47, 84`
- **[low]** Settings RBAC gating fails open when the effective-access API call fails — `dashboard/src/components/SettingsPage.tsx:331-350, 548`
- **[low]** Invalid backend timestamps collapse all such points into one and produce NaN relative-time — `dashboard/src/components/testExecutionMetricsModel.ts:34-37, 296-306, 331-342`
- **[low]** HA controller example pins image by :latest with no digest — `deployments/k8s/ha/controller-ha-deployment.example.yaml:41`
- **[low]** App Service launcher writes private gRPC TLS key to a predictable shared-tmp path — `deployments/azure/appservice/launcher.js:95-97`
- **[low]** MultiGroupEngine.wg is assigned outside the lock; ScaleVUsers can read a nil/torn pointer and panic — `pkg/engine/group.go:131-148`
- **[low]** MultiGroupEngine.Stop reads mge.cancel without holding mge.mu — `pkg/engine/group.go:652-656`
- **[low]** recordResult publishes the *protocol.Result pointer to the metrics aggregator before scrubbing/mutating it — `pkg/engine/vuser.go:567-582`
- **[low]** random_number parameter generation panics if min/max validation is bypassed — `pkg/engine/parameters.go:201-202`
- **[low]** OAuth2 token response body decoded with no size limit and a no-timeout default client — `pkg/protocol/http/oauth.go:178-194`
- **[low]** oauthTokenCache is a process-global sync.Map that is never evicted — `pkg/protocol/http/oauth.go:61, 88`
- **[low]** JMeter/DevWeb/TruClient build an unbounded in-memory parse tree from untrusted XML/JS with no node/recursion bound — `pkg/convert/jmeter/converter.go:1235-1291`
- **[low]** Legacy enterprise-archive line scanners silently truncate on lines longer than 64 KiB (bufio.Scanner default) — `pkg/convert/enterprisearchive/parser.go:155-214, 801-827, 845-917`
- **[low]** Non-deterministic auth selection when one OpenAPI security requirement lists multiple schemes — `pkg/convert/openapi/converter.go:1146-1147`
- **[low]** RAML !include depth cap counts structural YAML nesting, silently dropping deep includes — `pkg/convert/raml/converter.go:246-289`
- **[low]** Boolean values inside list/tuple query params serialize as Python-cased 'True'/'False' instead of 'true'/'false' — `mcp/apexload_mcp/client.py:66-73`
- **[low]** Browser session accumulates console/network/resource-validator entries unbounded for the VUser lifetime — `pkg/protocol/browser/runtime_context.go:137-161`
- **[low]** Network tcp/udp read allocates an unbounded script-controlled buffer (read_bytes) — `pkg/protocol/network/network.go:301-313`
- **[low]** decodeBody drops Content-Encoding header after decoding only the outermost layer of a multi-encoding body — `pkg/recorder/recorder.go:810-854`
- **[low]** GetScript/ListScripts scan nullable columns (name, protocol, version, generated_by, generated_from) into non-pointer strings without COALESCE — `pkg/storage/sqlite_scripts.go:54-61, 83-116`
- **[low]** diagnoseFailed may re-suggest the exact selector that just failed when locator forms are unnormalized — `pkg/selector/repair.go:147-155`
- **[nit]** Staged-package checksum silently dropped for filenames containing whitespace (loses integrity header) — `pkg/api/handlers_client_downloads.go:199-209`
- **[nit]** truncateMessage can split a multi-byte UTF-8 rune, producing invalid UTF-8 in JUnit XML — `cmd/apexctl/junit.go:218-225`
- **[nit]** Bearer-token comparison leaks token length via early-out — `pkg/distributed/generatorrpc/transport.go:257-262`
- **[nit]** HTTP bearer token rendered in a cleartext input while other credentials are masked — `dashboard/src/components/ScriptEditor.tsx:8566-8568`
- **[nit]** chartCardRefs retains detached/stale DOM nodes for charts removed from the display set — `dashboard/src/components/TestExecutionCharts.tsx:1982-1984, 480, 488`
- **[nit]** max_redirects struct comment contradicts actual behavior for value 0 — `pkg/protocol/http/http.go:44`
- **[nit]** ListUsers does not escape LIKE wildcards in user-supplied query — `pkg/storage/sqlite_auth.go:306-310`
---

## Remediation applied (2026-06-15)

All 27 verified findings were fixed, each with a regression test; `go build ./...`,
`go vet ./...`, `gofmt`, dashboard `tsc`, the full `go test ./...` suite, and the
dashboard vitest suite are green. Per-package `-race` runs are clean for every
concurrency fix (engine, api, generatorrpc, browser, recorder).

| # | Finding | Resolution |
|---|---------|-----------|
| 1 | Distributed goal-test nil-deref (CRITICAL) | `goalControl` initialized in `startDistributedTestRun` + defensive guard in `applyGoalControllerUpdate`. Test reproduces the crash. |
| 2 | health-check map race | `TestRunner.TestCount()` locked accessor; handler uses it. `-race` test. |
| 3 | distributed metrics-path race | `applyRemoteMetricsBatch` + the two local broadcast paths now read test state/`goalControl` under `tr.mu` (released before the gRPC scale call). |
| 4,5 | gRPC Heartbeat races | RLock around `assignments` reads; dedicated mutex serializes `resCollector.Collect`. `-race` test. |
| 6 | 2-replica controller double-start | on-prem overlay set to `replicas: 1` (matches AKS/Helm). **Follow-up:** PostgreSQL-lease guard for the pending-test reconciler before any `replicas>1`. |
| 7 | DevWeb zip-bomb | per-entry / total / entry-count caps via `io.LimitReader` (also closes the enterprise→devweb bypass). |
| 8 | WSDL infinite recursion | depth cap in `renderXSDFields`. Recursive-type test. |
| 9 | JMS nil-map panic | allocate map when metadata present (mirrors kafka). |
| 10 | unauth auth-diagnostics (OIDC) | admin gate widened to all non-bypass modes; only caller is the post-login admin page. New gate tests. |
| 11 | SSRF guard proxy bypass | dropped `Proxy: ProxyFromEnvironment` from the guarded client. **Behavior change:** control-plane egress can't route via `HTTP_PROXY`. |
| 12 | apexctl polls forever | routed through `GetTestStatus` (404 fast-fail, bounded transient retries). |
| 13 | gRPC archive path traversal | name confinement helper. Table test. |
| 14 | DSL nested-JMS false positive | JMS/Kafka families skip the generic target rule nested in control bodies. |
| 15,24 | CSV injection (SourceMappingReview) | `csvCell` quote-doubling + formula-prefix on every cell. |
| 16 | dashboard metrics bleed | `useExecutionMetricsStream` resets state on `testId` change. |
| 17 | TokenBucket `NewTicker(0)` | interval clamp + capacity bound. |
| 18 | unbounded HTTP body | configurable `max_body_bytes` cap (default 64 MiB) on raw read + decompressors; decode-error path now fails the sample. |
| 19 | paramize key corruption | numeric value spliced at its offset, not substring-replaced. |
| 20 | Salesforce nil-deref | guard discarded `url.Parse` errors. |
| 21 | browser `pwCDP` race | all accesses under `b.mu`. |
| 22 | recorder status race | `s.mu` is the owning lock for `Status`/`StoppedAt`. `-race` test. |
| 23 | scrub JSON body fields | **narrowed** to add only `authorization`. `session`/`sessionid`/`csrf`/`xsrf` are correlation targets — redacting them breaks replay (caught by `TestConvertHAR`); the JSON-pattern narrowness is intentional. |
| 25 | OpenAPI `fn.now()` | replaced with `fn.timestamp.iso8601` / static date. |
| 26 | MITM tunnel fd leak | idle read deadline per request loop. |
| 27 | DeleteUser orphans bindings | explicit transactional binding cleanup. Test. |

**Also fixed (selected low/nit):** `MultiGroupEngine` `wg`/`cancel` locking, `gofmt` on `oauth.go`, `AssignWork` rejects negative VUser count, errorUtils redacts the user-visible summary/rawMessage (not just evidence).

**Deferred (flagged, not auto-applied):** the full PostgreSQL-lease HA guard for the reconciler (#6, larger change); the remaining low/nit backlog in the report above (image digest pinning, `GetTest` shallow-copy sharing, settings-RBAC fail-open, etc.) — recommend a follow-up pass.
