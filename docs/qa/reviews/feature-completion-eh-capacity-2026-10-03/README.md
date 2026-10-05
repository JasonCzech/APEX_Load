# Event Hubs processor capacity review — 2026-10-03

Scope: completion specification EH-1: process-wide typed processor/worker demand, target-free staging, immutable peak claims, local/distributed placement, preparation readiness, execution adoption, cleanup and visible requested/admitted evidence. This does not close EH-2 lag/recovery or EH-3 provider qualification.

The default real-adapter pool retains the established four processors, 32 workers and 32 partitions per processor. Lower-only startup limits and immutable per-processor ceilings are explicit. Producer-only and observer work have no processor demand. Scheduled/adaptive peaks and open-workload effective maximum concurrency determine demand independently of optional pod reservations. Phase-only consumers are accounted without averaging or inventing TPS-to-processor equivalence.

Independent review and regression fixes cover:

- Single-use adoption, duplicate-owner cleanup safety and atomic pending-only release; zero resize ends the parent instead of accumulating dormant registry entries.
- Actual partition inventory checked after execution commit; incompatible or tampered claims fail before traffic. Prepared zero-peak consumers cannot grow outside their frozen admission.
- Capacity-only and engine-backed staged commit/readiness checks, expired/fenced stage cleanup and same-process HA transfer retaining the exact reservation.
- SDK queued partition drain after cancellation, worker/transport close lifetime accounting and explicit retained draining capacity when cleanup is unproven.
- Preservation of immediate ordinary staged-engine shutdown while Java host cleanup keeps its existing asynchronous drain barrier.
- Local allocation distinguishes current VUsers from the frozen resource peak. Resource-bearing generator-loss evacuation remains unavailable until qualified ownership/checkpoint recovery exists.
- Dashboard handwritten/generated DSL parity and missing-versus-confirmed reservation display.
- Resource-free capacity weighting for HTTP, observer and producer groups. The first repository gate exposed a 90:10 placement regression; the corrected resource-fit upper bound retains the expected 8:2 allocation.
- Startup and teardown deadline coverage across lifecycle-gate wait, inventory requests and late consumer ownership transfer; cancelled startup cannot launch a processor.
- Asynchronous snapshot tests wait for completed publication with a bounded deadline instead of assuming a fixed sleep covers resource sampling. The ten raw samples are buffered before aggregation so their expected single window is independent of test-goroutine scheduling; idle negative and data assertions remain.

Final verification passed. Independent repeat reviews found no remaining issues in this slice.

Confirmed checks: full provider default/tagged tests; provider resource race tests (1.495s/1.505s); full tagged generator tests (40.248s); targeted generator resource/graceful-cleanup race tests (6.206s); documentation checks for 100 active files; dashboard typecheck, lint, 4,756 unit tests, build and full browser cycle (190 dev-admin passes/4 configured skips, 110 local-auth passes/1 configured skip).

The initial repository run exposed the fixed resource-free placement regression, reached the API package's cumulative 20-minute deadline during Swagger generation, and failed two asynchronous engine snapshot tests using fixed sleeps. The latter tests now use bounded publication waits and passed in the full engine rerun (35.512s). The second repository run uses a 40-minute package deadline and 4 GiB compile/runtime memory headroom. Its API package passed (563.661s), including the placement and generated-contract checks; its generator package passed (44.138s). It exited nonzero on a Java broker readiness regression: the readiness handler could expose internal `reserved` before asynchronous preparation began, which the client correctly rejected as an unsupported public state. The deterministic service/client regression failed before the fix and passed afterward. Ready now projects internal `reserved` to the established public `preparing` state without advancing preparation or weakening owner, expiry, inventory, commit or invocation fences. Thirty repetitions of the regression/original failure, both full affected package suites and focused race checks passed. Independent repeat review is clean. The third full repository run exited zero on the final source; unchanged package results were reused by Go's test cache, and broker/sidecar executed in 3.248s/27.496s. The first targeted API race run failed the existing HTTP supervision integration fixture with a two-second RPC deadline and finalizer timeout; the current-source serialized rerun passed (20.828s) with unchanged production and fixture deadlines and no race warnings. Its original timeout cause remains unproven. Reviewed snapshot fixtures passed default (0.604s) and race (1.765s). A redundant focused default build was interrupted while compiling and is superseded by the final full repository gate; it is not recorded as passing.

Out-of-slice evidence findings discovered while preparing EH-2: database interval-history reload omits messaging JSON, and distributed action messaging projection follows an early return for absent business aggregates. These remain explicit next-slice fixes; EH-1 closes processor admission and lifecycle semantics only. Local/tagged tests do not certify Azure rebalance, identity, service takeover or target-wide resource limits.

Remaining acceptance: approved Event Hubs emulator subset and live Azure processor/epoch/checkpoint matrix; full tail/checkpoint progress diagnostics; measured capacity increases. IBM MQ remains low priority. The operator-waived 10 GB analytics benchmark was not run.

Commands and evidence:

- Final repository: `GOCACHE=/private/tmp/apexload-feature-completion-go-cache GOMAXPROCS=2 GOMEMLIMIT=4GiB go test -p 1 -timeout 40m ./...` — exit 0; [log](go-tests.log).
- Provider: default and full `eventhubs_real` suites plus focused resource race; [default](provider-default.log), [tagged](provider-real.log), [race](provider-race.log).
- Generator: full tagged suite and focused resource/graceful-cleanup race; [race transcript](generator-race-transcript.log).
- API and snapshot gates: [review and logs](engine-snapshot-review.md), including the earlier API timeout and unchanged-deadline successful serial rerun.
- Broker: [deterministic failure](broker-ready-before-fix.log), [fixed case](broker-ready-after-fix.log), [30 repeats](broker-ready-repeat.log), [full affected suites](broker-default.log), [race](broker-race.log).
- Dashboard: Node 22.22; `npm run test:cycle` — exit 0, 4,756 unit tests, 190 dev-admin passes/4 configured skips and 110 local-auth passes/1 configured skip. The new preparation case also passed both targeted auth profiles, and the narrow-layout screenshot was inspected. Skips are not provider acceptance.
- Documentation and generated DSL/API/TypeScript freshness passed; `git diff --check` passed.

Dashboard full-cycle transcript SHA-256: `309117ac5572381d06574e3a71377b8ec5d4d3ddb08d94dd0dae219ca3688664`. Its temporary transcript is `/private/tmp/apexload-eh-capacity-dashboard-cycle.log`; counts and commands above are preserved here.
