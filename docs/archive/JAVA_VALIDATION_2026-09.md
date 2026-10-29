# Java implementation validation history — September 2026

Status: Historical. Dated local verification and evidence retained from the implementation guide during the 2026-10-19 consolidation. Results apply only to their recorded scope; they do not qualify later sources, production pooling, AKS or untested platforms. Current contracts and open gates are linked from [Java implementation scope](../JAVA_SHARED_AND_PILOT_RUNTIME.md).

## Contents

- [Verification of this foundation](#verification-of-this-foundation)
- [Local Linux evidence collected during implementation](#local-linux-evidence-collected-during-implementation)

## Integration validation summary

Final local validation on 2026-09-28 passed the full Go suite (112 packages,
20,981 tests), repeated targeted race checks across six packages, 4,554 dashboard
unit tests, affected browser workflows in both authentication profiles, Go vet,
dashboard typecheck/lint/build, documentation checks, and Linux/Windows generator
cross-builds. A timing-dependent metrics test now uses the collector's completion
barrier instead of a fixed sleep; its percentile assertions remain unchanged.
These checks do not qualify AKS containment, Windows runtime containment, the
external attestor/fencing deployment, or pooled memory/soak/real-workload benefits.
The unpassed pooling evidence gate still prevents broader pooled integration.

Integration with current `main` also passed the full Go suite (113 packages,
21,939 tests), repeated API/generator RPC race checks, 4,564 dashboard unit tests,
and 14 dev-admin plus six local-auth browser workflows. Existing archive and
recovery contracts remain intact; Java placement uses protobuf field 41 while
archive source identity retains field 40.


## Verification of this foundation

Before the supervisor/local-backend increment, the full `go test -p 2 ./...`
run passed on the macOS development host. Affected package suites, focused race tests (engine observers,
evidence, CDS, sessions, leases, and the host ledger), and documentation checks
also pass. The earlier QA dashboard freshness failure was resolved by a separate
repository change; this work did not rewrite the dashboard.

The supervisor/local-backend increment passes the full `go test -p 1 ./...`
suite, affected Java-stack suites, and repeated supervisor race tests.
Real-JVM component tests verify the cold
prepare/commit barrier, cooperative cancellation with a surviving peer, bounded
native output, and independent lease expiry during an invocation that ignores
interrupts. Those tests use package-private process hooks on macOS and do not
prove descendant containment. Linux and Windows test binaries cross-compile;
they have not been executed on those platforms.

The wider test run exposed a timing race in the CLI cancellation test: a fixed
five-millisecond delay could cancel creation before its response was consumed.
The test now cancels after response consumption, and passes 20 consecutive
race-enabled repetitions and the full CLI race suite. Production CLI behavior
is unchanged. An initially failing sidecar lease test passed five isolated race
repetitions and the complete isolated sidecar race suite.

The qualification increment passes the full `go test -p 2 ./...` suite on the
current checkout, the runner race suite, repeated qualification race tests, and
Linux/Windows cross-compilation. Fixtures verify frozen inputs, exact allocation matching,
artifact drift, bounded scans, special-file rejection, concurrent revalidation,
backend enforcement, and rollback before process creation. They use fixture JDK
trees and do not qualify a real JDK installation. Broader verification also
exposed an API test that assumed both concurrent shakeout-verification attempts
must succeed immediately. It now accepts the documented retryable fence, retries
after both attempts finish, and retains its single-execution and settled-outcome
assertions. Production API behavior is unchanged.
That API regression passes 20 race-enabled repetitions. The current storage
race suite also passes after concurrent archive-storage work was completed;
earlier runs during that work did not provide a clean full-suite result.

The shared-admission increment passes the full `go test -p 2 ./...` suite,
the runner and sidecar race suites, and five race-enabled repetitions of its
focused regressions. Coverage includes
mixed dedicated/pooled reservations, concurrent whole-batch admission, independent
host/Vuser/memory ceilings, draining and quarantine, failed preparation,
cancellation, terminal admission closure, and confirmed versus uncertain cleanup
across owner epochs. Both affected packages cross-compile for Linux and Windows;
these are compilation checks, not runtime containment qualification.

The pooled-ownership increment passes the full `go test -p 2 ./...` suite on the
completed code, the affected runner/sidecar race suites, and ten race-enabled
repetitions of its ownership regressions. Tests cover
takeover during invocation and delayed provisioning, per-group expiry,
pooled-only owner loss, retained quarantine, concurrent duplicate claims,
cancelled close, slot accounting, initialization replay rejection, and
publication/takeover races with shutdown. Linux and Windows test binaries for
both packages compile; no new platform runtime or performance qualification is
claimed by those checks.

The reservation-control increment passes the full `go test -p 2 ./...` suite,
the affected runner/sidecar race suites, and five race-enabled repetitions of its
control-contract regressions. Authenticated
HTTP tests cover owner/run token fencing, one-group admission, exact concurrent
reservation replay, expired/aborted reservation rejection, bounded replay history,
renewal/close independence, positive cleanup acknowledgement, strict request fields,
and malformed response/error handling. Fixtures contain inspection-only JDK/JAR
assets and never start Java; they are not runtime qualification evidence. Both
affected packages cross-compile for Linux and Windows.

The pooled-invocation increment passes the full `go test -p 2 ./...` suite,
the runner/sidecar race suites, and five race-enabled repetitions of focused
invocation and capture regressions. Coverage includes lifecycle sequencing,
replay rejection, busy slots, cancellation and concurrent closure, preserved exit
status, capture replacement, malformed or foreign events, and relative temporary
directories. Real Java tests verify rejection of incompatible event-attribution
bridges before user initialization and exclusion of background-thread events
from slot capture. Review fixes were retested, and the final review found no
further actionable issues in this increment. Sidecar test binaries cross-compile
for Linux and Windows; these checks do not establish runtime containment.

The managed-client increment passes the full `go test -p 2 ./...` suite,
the runner/sidecar race suites, five
race-enabled repetitions of the managed lifecycle tests, and ten repetitions
of concurrent slot closure with a surviving peer. Tests exercise frozen owner
epochs and decisions, cold barriers, independent renewal during blocked user
work, staging expiry, cancellation, lost invocation/commit responses, and
positive versus uncertain cleanup receipts. `go vet ./pkg/javasidecar`,
documentation checks, and Linux/Windows sidecar cross-compilation also pass.
HTTP integration tests use the existing private fake-host seam; they do not
qualify real containment or demonstrate engine integration.

The protocol-adapter increment passes the full `go test -p 2 ./...` suite on
the final source, the engine, Java protocol, sidecar, and runner race suites;
five repeated adapter/Vuser-loop and malformed-response
checks; and separate repeated target-identity and JSON-control-boundary checks.
The Vuser integration test uses the real managed HTTP client and protocol
adapter against a wire-fixture peer. It verifies lifecycle order, parameter
updates, at-most-once calls with retries configured, transaction timings,
nested iteration exits, failed-exit samples, and independent iteration failure
accounting. This is result-path evidence, not a real-JVM containment or memory
benchmark. Static analysis, documentation checks, and Linux/Windows protocol
and sidecar cross-compilation also pass.

The engine-staging increment passes the full `go test -p 2 ./...` suite,
the affected engine/sidecar/protocol race suites, five repeated pilot and owner
regression checks, static analysis, documentation checks, and Linux/Windows
engine and sidecar test-binary compilation. One desktop temporary-directory
cleanup failure in an earlier run did not reproduce in five isolated repetitions;
the final full suite passed. The final review found no further actionable issues
in this increment.

Regression coverage includes cold preparation before
duration/ramp, frozen DSL identity, dedicated-compatible zero-based Vuser IDs,
single-use/concurrent start rejection, staging deadline and stop handling,
independent owner/group renewal during blocked calls, stale owner fencing, lost
commit receipts, and slot/run cleanup uncertainty. Review corrected reuse cleanup,
identity offsets, lease-failure races at shutdown, and teardown errors that the
ordinary engine only logs. The integration tests run the real engine, managed
HTTP client, and protocol adapter against a wire-fixture peer; they do not prove
real-JVM containment or pooled memory savings.

The operator-manifest/evidence-command increment passes the full Go suite,
affected race suites, five repeated focused checks, static analysis, documentation
checks, and Linux/Windows benchmark/runner builds and sidecar test compilation.
The full suite required regenerating the QA Markdown dashboard after adding
runner-command tests changed its package-without-tests count; the final full run
passed. The final review found no further actionable issues in this increment.

It adds cold loader and real HTTP
control tests for approval/asset drift, shared dedicated/pooled capacity, strict
JSON and file bounds, and frozen DSL identity. Evidence tests cover engine
selection without fallback, fresh trial identities, retained owner failures,
and configuration hashing without a fabricated soak or qualification result.
Runner-command tests retain disabled-by-default behavior; Linux and Windows
cross-compilation does not establish runtime containment. No real pooled PSS
measurement has been produced by these tests.

The dedicated harness builds for Java 8 and 17 bytecode; the slot/host artifacts
build for Java 8 bytecode. Both fixture scenarios pass through IPC v2. Real Java
tests verify private targets/statics, shared dependencies, thread affinity,
commit barriers, failed initialization, interruption, sealing, and surviving
peers. An opt-in test generates a secret-free AppCDS archive and verifies the
operator-manifest launch against the real VM:

```bash
APEXLOAD_JAVA_CDS_TEST_JDK_HOME="$JAVA_HOME" \
  go test ./pkg/javarunner -run TestOperatorCDSRealArchive -count=1
```

These Java executions used the installed JDK 26, not qualified JDK 17/21 hosts.
No Linux PSS benchmark, one-hour soak, Windows containment test, or AKS execution
test has been run. A local Docker attempt previously failed because Docker's
storage database was read-only; its existing generator also lacked a writable
delegated cgroup. Those attempts supply no performance or containment evidence.

## Local Linux evidence collected during implementation

Docker Desktop arm64 supplied a disposable private cgroup-v2 namespace, with
4 CPU / 6 GiB assigned to the benchmark, sidecar and JVM subtree. The loopback
target stayed outside that subtree. A three-user light smoke and a 25-user light
diagnostic completed without harness errors. A subsequent five-repeat 25-user
light baseline completed, but throughput ranged from 45.77 to 49.98 transactions
per second and p99 from 3 to 412 ms. This exceeds the proposed noise limit.

The middleware-heavy default baseline completed one repetition and stopped on
an incomplete second repetition with runtime-lease failures and missing final
memory evidence. Concurrent workstation activity was not isolated sufficiently
to attribute the cause. These observations are rejected/inconclusive evidence,
not a successful dedicated profile or a pooling capacity claim. No matched
pooled PSS comparison or observed one-hour soak has passed the 30% gate.

The optional representative-fixture test now also runs concurrent pooled slots
with the actual HttpClient/Jackson/Guava dependencies in the shared loader. Both
fixtures pass dedicated and pooled construction/init/action/end checks. This is
component correctness evidence only; it does not substitute for the memory gate.
