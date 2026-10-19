# Experimental Java dedicated execution contracts

Status: Experimental. Durable broker authority, diagnostics, execution-pod supervision, preparation and distributed integration contracts. Use [the broker service guide](JAVA_BROKER_SERVICE.md) for operator configuration and API usage, and [the boundary-attestor contract](JAVA_BOUNDARY_ATTESTOR_CONTRACT.md) for external cleanup proof. [Release gates](JAVA_VUSER_POOLING_IMPLEMENTATION.md#remaining-implementation-and-release-gates) remain separate from local validation.

## Contents

- [Durable dedicated broker state and explicit pilot diagnostics](#durable-dedicated-broker-state-and-explicit-pilot-diagnostics)
- [Host GC diagnostics, observed soaks, and Kubernetes transport](#host-gc-diagnostics-observed-soaks-and-kubernetes-transport)
- [Dedicated execution-pod supervisor and broker control adapter](#dedicated-execution-pod-supervisor-and-broker-control-adapter)
- [Dedicated preparation waves](#dedicated-preparation-waves)
- [Runnable experimental dedicated broker](#runnable-experimental-dedicated-broker)

## Durable dedicated broker state and explicit pilot diagnostics

`pkg/javarunner/broker` now supplies a separate durable reservation authority for
one dedicated JVM per execution pod. It uses SQLite transactions with full
synchronous writes on broker-owned persistent storage, outside execution pods.
Opening a missing recovery database fails; initializing a new authority is a
separate explicit operation. Namespace and budget are immutable store identities.
This is a single-volume foundation, not an HA deployment or enabled AKS backend.

Each provisioning intent is committed before pod creation. Broker epoch rotation
fences old connections and quarantines every potentially started old host;
never-started reservations alone can release immediately. Late creation receipts
can add exact pod/container identities but cannot reactivate Java state. New
admission for an affected owner remains blocked until its unresolved reservations
are cleared. Vuser occupancy and host/memory charges release separately.
Preparation deadlines survive owner renewal; expired reservations cannot revive.
The commit barrier includes every pending host of the same immutable group.

The reconciler uses bounded concurrency so one stalled node cannot serialize all
cleanup. It discovers exact host/epoch provenance and deletes only with the
recorded pod UID. The Kubernetes backend confirms pod-object absence after the
UID-preconditioned delete before releasing quota. A trusted runtime attestor
must separately establish whole container termination for that exact UID and
container ID. Deletion acceptance,
missing pod objects, unreachable nodes, and JVM exit alone never release capacity.
Persisted boundary identities can still be checked by that independent attestor
when the pod object is missing; absence alone is never sufficient. Pending or
unknown creation outcomes require a separate externally attested creation fence
plus complete never-started runtime history and authoritative pod-object removal
before release; otherwise they remain
durably quarantined. The first
failure is retained. A qualified runtime attestor, infrastructure fencing, PID 1 supervisor,
TLS facade/broker service, deployment
manifests, and assignment staging integration are still required. The store has
a hard 10,000-record lifetime bound and retains released records; operator-safe
compaction remains a future operation rather than silent evidence deletion.

Pilot runners advertise `java_vuser_pooled_diagnostics_v1` when preparation is
enabled. The explicit `diagnostics` operation and managed-run `Diagnostics`
method return one authenticated host snapshot, with separately bounded native
stdout/stderr (256 KiB each) and Java diagnostics (32 KiB). Arbitrary bytes are
base64 encoded; dropped counts and truncation remain visible. These are untrusted
host output, never Vuser events or transaction results. A snapshot does not run
user code, extend a lease, or release capacity. Caller cancellation cannot cancel
an in-progress health exchange; the exchange has a separate two-second deadline
and remains subject to supervisor retirement. Only one diagnostic exchange per
local host can run at once. Genuine control-transport uncertainty still reaps the
host. Buffers are released after confirmed host cleanup, so final diagnostic
raw-output archiving remains an explicit future operation. Automatic value-free
evidence collection is described below.

Regression coverage includes concurrent database connections, abrupt broker
process exit, failed durable writes, late/pending pod receipts, exact-identity
mismatches, missing pod records, expiry, all-host commit, epoch fencing, separate
occupancy accounting, diagnostic wire bounds/authentication, caller cancellation,
and Java diagnostic collection through a real JVM component test. Component
backends do not establish AKS runtime containment or production qualification.

The local Docker daemon responds, but a JDK 17 container creation attempt failed
because its stored overlay snapshot was missing. Only the Docker Desktop
Kubernetes context is configured. No delegated-Linux memory comparison, one-hour
soak, Windows runtime test, AKS containment trial, or real-workload qualification
is claimed by these changes.


Validation for this increment: `go test -json -p 1 ./...` completed successfully
with 109 tested packages and no failures; targeted race tests, five repeated
broker race runs, `go vet`, Linux/Windows cross-builds, documentation checks, and
`git diff --check` passed. The first full-suite attempt encountered host disk
exhaustion. After removing this task's disposable build outputs, the next run
had one browser replay lease-loss timeout outside these changes; that test passed
five isolated repetitions and the final full-suite rerun passed. No test deadline
was relaxed and no unrelated browser code was changed.

## Host GC diagnostics, observed soaks, and Kubernetes transport

`apexbench java baseline` and `pilot` accept `--soak 1h` (up to `2h`), after
exactly five repeated samples and with at least three Vusers. This runs a fresh
execution through the same engine/runner path. The benchmark reserves the entire
population before execution, starts staged cohorts at the beginning and at
quarter/half duration, and permanently closes odd-numbered Vusers at three-quarter
duration while survivors continue. No slot is reused. Observed initialization
and closure times are retained; a two-second scheduling/cleanup tolerance is an
explicit evidence rule. Invalid/missed ramps, duplicate lifecycle observations,
cleanup failures, missing metrics, memory-limit events, and insufficient continuous
active duration prevent a pass. Protocol teardown failures now have a counter in
`engine.RunResult`; existing normal-run teardown behavior is unchanged.

Add `--soak 1h` to both matched [baseline and pilot commands](JAVA_PILOT_OPERATIONS.md#collecting-a-dedicated-baseline). The collector
writes `soak` details separately from steady samples, preserving the distinction
between total PSS, startup peaks, cgroup memory, and throughput/latency comparison.
The comparison gate requires matching observed soak schedules and durations;
legacy `soak_passed`/`soak_seconds` assertions without observations cannot pass.
Accelerated HTTP and pooled-wire fixtures test scheduling locally, but do not
stand in for the real one-hour Linux run.

The pooled evidence command negotiates diagnostics and samples each host once
per polling pass, independently of slot invocation queues. Final snapshots precede
slot cleanup. Reports retain host/slot identities, output sizes/drop counts, and
GC pause observations, never raw output that could contain workload secrets.
The collector bounds outstanding health requests and retains only the latest
128 pause events per host, plus cumulative observed and missing counts. Sequence
resets are diagnostic failures, never hidden restarts. Missing reads, output
truncation, unavailable GC capture, missing sequences, and incomplete host/slot
coverage make pooled evidence incomplete.

On supported runtimes, one JFR stream per host captures the built-in
`jdk.GCPhasePause` event by its platform event identity. Reflection preserves Java
8 source compatibility; initial runtime qualification remains JDK 17/21. The
collector does not force GC or alter transaction durations. Tests induce real
pauses through bounded allocations, and separately exercise overflow and ring
retention. A private operator-owned JFR repository is removed only after proven
containment cleanup; uncertain termination retains it. JFR chunk retention is
configured to 8 MiB/30 seconds, not claimed as a hard filesystem quota. Its memory
and scratch overhead belongs in runtime profiles and matched measurements.
JFR delivery is asynchronous: `unflushed_tail` explicitly discloses that final
pending events may be unavailable when the host is reaped. Slot mapping is
cooperative correlation, not thread ownership proof.

Real-loader fixtures exercise shared class identity, private statics/shims,
resource precedence/enumeration, service discovery via the context loader,
sealed packages, and shared dependencies that incorrectly resolve slot-private
classes. These fixtures do not approve an arbitrary dependency graph.

The dedicated broker now includes a concrete HTTPS Kubernetes transport with
origin-bound credentials, no redirects or ambient proxy, bounded responses, and
namespace-scoped create/get/delete operations. Pod creation follows durable
`BeginPrepare`, happens once, and persists exact receipts. An uncertain create
quarantines its reservation. Delete uses the exact pod UID; neither API success
nor NotFound is termination evidence. A separate qualified attestor remains
mandatory and is intentionally not replaced with pod status.

The operator-only pod builder emits a digest-pinned, single-container restricted
pod with `restartPolicy: Never`, private process namespace, non-root execution,
RuntimeDefault seccomp, dropped capabilities, read-only root, disabled service
account tokens/service links, equal CPU/memory resource requests/limits, bounded
scratch and a read-only host transport secret. Approved assets and a compatible
supervisor must be supplied by the immutable image. The PID 1 supervisor, asset/readiness verification, authenticated host
control transport and durable commit adapter are now implemented below. This
builder is a component of the [experimental broker integration](JAVA_BROKER_SERVICE.md), whose facade, orchestration, inactive deployment scaffold and distributed assignment contracts are documented separately. Independent fencing/termination attestation and live infrastructure qualification remain required. No AKS backend is enabled by these utilities.

No Linux PSS improvement, hour-long soak, Windows containment, AKS termination,
or production workload qualification has been measured on this macOS host.
The synthetic gate still blocks normal/distributed pooled execution and its UI.

`apexbench java inspect --artifact /approved/application.jar --artifact
/approved/source.zip --output native-inspection.json` produces reproducible,
SHA-256-bound static evidence without executing classes. It scans Java source
indicators, class method native flags and constant-pool references, packaged
native binaries, and bounded nested JAR/ZIP entries. The scan budget is 64 MiB
including nested expansion, 16,384 entries, depth three and 4,096 findings.
ZIP central directories are checked before materializing entry objects. Corrupt,
unsupported, cancelled or over-limit inputs yield `incomplete`, not a clean result.
`no_static_indicators` is deliberately different from the operator qualification
assessment; reflection, obfuscation, runtime downloads, alternate source encodings
and unattributable background work still require representative runs and review.
Only explicit application inputs are scanned, avoiding classification of the
JDK's implementation libraries as application JNI use. Findings contain artifact
hashes, entry names and categories; source text and constant values are omitted.


Validation for this increment: the stable full Go rerun passed all 109 tested
packages. Targeted race checks passed for the Java runner/broker, sidecar, engine,
evidence collector, archive preflight, and CLI; the diagnostic-close ordering
fixture also passed 20 race-enabled repetitions. Java 8 slot and dedicated
harness builds, the Java 17 dedicated build, real allocation-driven JFR capture,
loader fixtures, Linux/Windows broker cross-builds, vet, documentation checks,
and diff whitespace checks passed. Cross-builds are not runtime qualification.
The initial full run overlapped newly added scanner files and failed two package
builds from an inconsistent source snapshot; the stable rerun passed. No runtime
test deadline was relaxed. No dashboard or generated public API contract changed.

## Dedicated execution-pod supervisor and broker control adapter

`cmd/apexload-java-host-supervisor` is an experimental executable for the
independent dedicated-AKS track. `make java-host-supervisor` builds it;
`runners/java_vuser/Dockerfile.supervisor` defines a base image to extend with
approved workload assets. It does not enable a generator backend or deploy a
broker. Production construction requires Linux PID 1, a non-root UID, no retained
capabilities, no-new-privileges and a read-only root filesystem. The supervisor
clears dumpability, reaps adopted children without stealing the JVM root's wait,
and exits on an independent lease/preparation deadline, runtime failure or signal.
Root/JVM exit and pod deletion are never returned as whole-container proof.

The default image profile is `/assets/java-host.json`. It is bounded to 512 KiB,
strictly decoded, and its exact SHA-256 must match `--configuration-sha256` from
the reservation. The profile contains the versioned host contract, exact broker
SPIFFE URI, lease/preparation/invocation limits in milliseconds, and an
operator-owned `DedicatedPodProfile`. That profile binds the selected JDK
launcher, harness and ordered JAR classpath to SHA-256 values and records exact
VM arguments and dedicated execution context. Assets must reside in the immutable
image outside the scratch directory. This initial adapter requires Java 17+ and
private script class loading; it does not broaden or replace ordinary dedicated
execution's supported settings, Java 8 path or operator AppCDS interface.

Preparation starts the normal dedicated IPC v2 harness with an opt-in random
readiness nonce. The harness saves its control input, exposes empty `System.in`
to user code, and acknowledges readiness before loading or initializing the target
class. The first authenticated init command is possible only after host commit.
The adapter reuses existing bounded capture, event parsing, HMAC response checks
and persistent-session semantics. Ordinary lifecycle failures remain responses
from the same live Actions instance; uncertain transport retires the host with
no replay. No public constructor exposes an uncontained process-launch override.

The supervisor listens on TLS port 9443. `/transport/tls.crt`, `tls.key` and
`ca.crt` supply only host-scoped transport material. TLS 1.3 requires a verified
client certificate, and the handler additionally requires the exact broker URI
SAN. Another host certificate from the same CA cannot renew the lease. The broker
client checks the exact `/java-host/<host-id>` server URI, host/epoch/configuration
and request identity on every response; redirects and ambiguous invocations are
not retried. Host-local monotonic invocation IDs prevent reuse of dedicated IPC
response authentication tokens. Control and invocation have independent lanes.
Request bodies are bounded to 512 KiB; IPC payloads retain the dedicated 5 MiB
bound and the outer base64 response has a 7 MiB bound.

`DedicatedGroup` connects exact bound pod receipts to authenticated harness
readiness and the durable store. Remote commits run no user code; the invocation
gate opens only after every host acknowledgement and the atomic durable commit.
Failed barriers atomically retire their attached hosts and omitted pending
reservations while preserving earlier committed ramp waves. Reservations with
an ambiguous create, failed close or missing pod remain charged for reconciliation.
Even a dedicated `end` acknowledgement retains occupancy until whole-pod cleanup
is proved, since ordinary application background work is not qualified cleanup.
Upstream owner authority must drive renewals; this adapter never renews itself
indefinitely or adopts a host after a broker epoch change.

Local coverage includes real mutual TLS, wrong host/client identities, malformed
and replayed responses, blocked runtime calls, preparation and lease expiry,
pre-cancelled requests, copied/bounded output, busy invocation rejection, partial
commit rollback, omitted reservation cleanup and broker epoch fencing. Real-JVM
tests verify the preparation barrier, empty user stdin, parameters, persistent
state and same-thread lifecycle behavior. These component tests do not qualify
AKS PID namespace termination, admission-controller changes, unreachable nodes,
network/SNAT behavior or the container runtime's termination attestor.


The broker now persists the SHA-256 of its exact pod request before creation.
`Provision` checks the admitted response and `ObservePrepared` rechecks policy
when binding the actual container. Expected Kubernetes defaults and equivalent
CPU/storage quantity spellings are allowed; changed images, commands, privileges,
credentials, volumes, labels/annotations, placement, resources or controller
ownership are rejected. Unknown changes remain unqualified. Rejected creation
still records the exact returned UID for cleanup. A missing container identity
or changed annotation cannot erase a known UID's deletion fence, and cancellation
after observation cannot discard a received container identity. None of these
checks treat Kubernetes status or deletion acknowledgment as termination proof.
The defaulting allowlist is based on [upstream Kubernetes defaults](https://github.com/kubernetes/kubernetes/blob/v1.34.3/pkg/apis/core/v1/defaults.go);
actual AKS admission/CNI behavior still requires validation on the target cluster.

Local Docker Desktop arm64 evidence now covers the full Java harness suite on
OpenJDK 17 and 21, including IPC, loader/bridge and GC capture fixtures. The
restricted execution-pod probe uses Alpine OpenJDK 21.0.12, non-root PID 1,
read-only root, dropped capabilities, no-new-privileges, 1 CPU, 256 MiB and bounded
scratch. It verifies no target traffic before commit/init, writable temporary
files, a blocked invocation, detached descendant traffic stopping on lease expiry,
and the same stop after supervisor SIGKILL. Both cases passed repeated runs.
This is local runtime evidence, not AKS qualification or the pooled memory gate.

The repeatable test is `TestDedicatedPodDockerContainment`, enabled by
`APEXLOAD_JAVA_POD_DOCKER_IMAGE` pointing to an already-built local Alpine JDK 21
image. It freezes the local image ID, builds the current supervisor/harness and
fixture, generates test-only certificates, and cleans up its own containers. It
never pulls images or installs dependencies. The normal test suite skips it; the
QA inventory and `scripts/run-optin-suites.sh` include its explicit requirements.
The initial prototype was replaced by this checked-in test. Review corrected the
fixture's TCP-proxy readiness race by waiting for verified TLS before its first
control request. Java `/tmp` and `/scratch` share the pod's bounded scratch volume;
the standalone Docker fixture uses two separately bounded temporary mounts.

The supervisor/control increment passed a full Go run (110 tested packages),
targeted race runs, vet, Java 8/17 builds and Linux/Windows cross-builds. A later
full run exposed the missing opt-in-suite documentation row; that inventory entry
and the runner command were added and the inventory checks pass. Admission-policy
changes passed another full Go run (110 tested packages), repeated broker race
tests, vet, documentation checks and Linux/Windows cross-builds.

## Dedicated preparation waves

`ReserveDedicatedWave` atomically reserves a complete requested peak or increase.
Durable session identities are scoped to execution/group across generator owners,
owner epochs, configuration changes and broker restarts. Closed identities remain
consumed; there is no transparent session replacement. Capacity rejection leaves
no partial wave. The existing bounded store inventory includes these tombstones;
exhaustion is explicit rather than silently discarding replay protection.

`DedicatedWave` connects these reservations to the Kubernetes and supervisor
barriers. It provisions with at most eight workers, uses the fixed reservation
preparation deadline, and admits invocation only after every authenticated host
and the durable group commit succeed. `KubernetesHostPreparer` creates once,
reobserves the approved pod policy and exact container identity, and waits for a
verified TLS handshake before its first stateful control request. Preparation
never runs user code. Operator callbacks supply host-scoped credentials/routing;
this internal component is not a public authenticated broker service.

Upstream owner renewal remains explicit and independent of blocked preparation
or Java execution. It includes reserved hosts not yet attached to their TLS
clients. Host closure preserves memory/host charges until positive cleanup proof,
while surviving Vusers continue receiving valid owner renewals. A failed wave
aborts exactly its original reservation inventory: a delayed callback cannot
capture a later admitted increase. Commit control calls use bounded parallelism.

Review corrected renewal of closed peers, abort scope across later waves, and
session replay across changes of generator owner. Tests exercise concurrent
admission, all-or-nothing capacity, preparation expiry under renewal, cancellation
with an ambiguous created pod, no initialization before commit, real mTLS
handshakes, and the Kubernetes-create → exact-container → harness → commit path.
The broker API and host credential provisioning described below now connect
these primitives. The generator-facing facade, assignment staging integration
and deployment wiring remain separate implementation work.

Per-Vuser context now travels in a bounded canonical host-context envelope,
separate from the immutable image profile. The pod policy digest includes the
session identity, Vuser/group/scenario identity and think-time settings. New
session reservations require this context. Conflicting baked-in values are
rejected, and real Java tests verify context is already visible in construction.
Asset validation also resolves symlinks before rejecting scratch-backed code.

The public PID 1 runtime additionally opens each code asset and verifies that its
actual filesystem mount is read-only. A writable alternate mount cannot supply
approved Java/harness/classpath bytes merely by avoiding `/scratch` in its path.
Nonblocking opens reject swapped non-regular files without hanging validation.

The selected cleanup architecture uses an external runtime attestor with
infrastructure fencing. The implemented mTLS client and its
[versioned contract](JAVA_BOUNDARY_ATTESTOR_CONTRACT.md) require fresh,
challenge-bound exact-container/descendant proof. Infrastructure evidence must
identify both the original node UID and immutable machine UID, separately from
a reusable provider resource locator. The lookup does not authorize fencing.
An external service still needs deployment and runtime qualification. Tests
exercise actual mTLS, stale/replayed/forged proofs, host-role rejection and
missing-pod reconciliation that retains charges until positive fencing proof.

Evidence samples now retain bounded `incomplete_reasons` codes. A report cannot
hide a failure reason by setting `complete` back to true. This adds diagnostic
information without relaxing the gate or exposing raw process/user errors.

## Runnable experimental dedicated broker

`cmd/apexload-java-host-broker` now connects the persistent epoch authority,
profile-authorized facade API, restricted pod preparation, independent owner
leases and external-attestor reconciliation. Its
[operator/API contract](JAVA_BROKER_SERVICE.md) documents explicit state creation,
credential provisioning and the remaining integration boundary. The existing
controller and local sidecar do not select this backend automatically.

Each authenticated facade is bound to one generator owner and approved immutable
profiles. Reservation prepares a complete wave asynchronously; commit is separate
from user initialization. Expired owner instances cannot reclaim, renewal and
invocation sequences reject replay, and cleanup polling remains possible after
lease loss. A `retiring` response is not a positive termination receipt. Broker
restart retains quarantine and session tombstones while fencing the old epoch.

Host credential provisioning issues fresh server-only ECDSA certificates and
unique immutable Secrets. Neither the issuer private key nor broker/facade/
Kubernetes credentials enter execution pods. The managed preparer binds the
approved profile and session context, resolves the exact pod/container IP, and
waits for pinned mutual TLS. Readiness also verifies actual supervisor lease and
timeout policy, so configuration drift cannot cross the commit barrier. The
broker command requires renewal margin between host and owner lease intervals.

The managed broker client now checks the expected configuration digest at
reservation, before creating any host. Owner renewal does not renew hosts:
each dedicated Vuser has a separate sequenced renewal lane, and abandoned
Vusers expire while healthy peers continue. Explicit per-host closure interrupts
blocked work without aborting peers; its memory and host charges remain until
external termination evidence arrives. Response identities include the exact
host-local invocation, preventing cross-call event substitution.

`DedicatedBrokerSession` supplies the typed response boundary for the local
facade. It translates only authenticated response/event identities, merges
bounded fallback capture, namespaces transaction IDs and preserves user output,
timing, lifecycle failure diagnostics and exit events. Late events are excluded.
There is no initialization replay or implicit lease renewal. The explicit
`DedicatedBrokerProtocol` adapter feeds these results into the existing engine
transaction/reporting helpers without selecting the backend automatically.

Local tests cover the complete managed-client → mTLS broker → mTLS supervisor
path with a simulated JVM and durable store, including cold readiness, commit,
per-host renewal, end/closure, blocked invocation cancellation, retained cleanup
charges, profile drift and invalid response attribution. These are component
integration tests, not AKS containment qualification.

The inactive Kustomize scaffold provides separate restricted namespaces,
namespace-scoped broker RBAC, explicit transport/attestor mounts, deny-by-default
network policies, quotas and optional state bootstrap. It renders with zero
broker replicas and is not included by existing overlays. Deployment remains an
operator action after target-cluster configuration and qualification.

The runnable loopback facade now supplies separate owner, run and Vuser lease
operations, strict local authentication, capabilities, full policy-digest binding
and fixed execution-lifetime admission. An experimental single-group engine
entry point freezes the approved DSL/profile, stages the peak before the ramp
clock, and maintains independent Vuser leases through lifecycle completion.
Review tests cover late activation after abort, preparation/run expiry, changed
profiles, replay, blocked invocations and end/renewal races. See
[JAVA_BROKER_SERVICE.md](JAVA_BROKER_SERVICE.md) for configuration and boundaries.

Explicit dedicated distributed assignment staging is now implemented through
versioned placement selection, operator-approved source/profile bindings and a
generator-owned shared facade lease. Preparation remains cold until CommitWork;
rollback, stage expiry and stop abort reserved hosts. Placement acknowledgements
carry frozen identities, and old peers cannot silently satisfy a selected backend.
The multi-group engine excludes remote hosts from local JVM accounting. Scale-down
is supported; increases without additional ready reservations and fixed-lifetime
extensions are rejected. A real transport regression verifies cancellation of one
dedicated group does not invalidate its peer.

Controller placement and dashboard profile selection/reporting are now wired for
explicit dedicated groups, including bounded staged schedules. The controller freezes the approved
script/configuration/policy identities, refreshes negotiation before reservation,
uses the preparation deadline, and retains the reservation in execution manifests.
Selecting a Java profile for a new group preserves the scenario's placement and
required labels through explicit group inheritance. A group can still choose
automatic or pinned hosts, but it cannot escape a scenario fleet pool. Java
placement fails closed when dynamic pool membership is unresolved or an
inherited weighted placement cannot be honored by the host allocator.
Scale-down uncertainty stops the run without recreating closed sessions. The API
supports bounded sharding across generators with separate frozen reservations.
Partial scale-down failure stops the run; closed sessions are never recreated by
compensation. Adaptive goals, increases beyond unused prepared admissions, and
recovery/adoption remain unsupported.
Optional operator-owned facade resource budgets now support generator pod-ledger
admission separately from broker execution-pod reservations. The resource identity
is frozen through negotiation and assignment; missing budgets fail closed when
the generator ledger is enabled.

Staged schedules now negotiate prepared-population support and reserve all fresh
lifetime admissions before commit. Down/up cycles consume new identities; unused
sessions stay cold and capacity remains charged through generator cleanup and
broker quarantine. Both placement and generator validation enforce the prepared
count. Fixed wall-clock lifetime and mode/profile bindings remain unchanged.
Configuration is documented in JAVA_BROKER_SERVICE.md. These additions do not
establish target-cluster containment or production qualification.
General pooled integration remains blocked by the unpassed evidence gate;
dedicated AKS qualification is independent of that gate.
