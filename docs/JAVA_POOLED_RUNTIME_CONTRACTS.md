# Experimental Java pooled runtime contracts

Status: Experimental. IPC, containment, admission, ownership, lifecycle and review-derived constraints for the explicit local pooled pilot. [Pilot operations](JAVA_PILOT_OPERATIONS.md) owns the operator procedure; [implementation scope and release gates](JAVA_VUSER_POOLING_IMPLEMENTATION.md) owns the remaining qualification boundaries. Ordinary distributed shared execution uses the separately enabled [shared-JVM policy](JAVA_SHARED_JVMS.md); the pilot contract does not enable it by itself.

## Contents

- [Experimental IPC v3 transport](#experimental-ipc-v3-transport)
- [Supervisor and local containment adapter](#supervisor-and-local-containment-adapter)
- [Shared runner admission](#shared-runner-admission)
- [Pooled run ownership](#pooled-run-ownership)
- [Versioned pooled lifecycle control](#versioned-pooled-lifecycle-control)
- [Review fixes and next-phase boundaries](#review-fixes-and-next-phase-boundaries)

## Experimental IPC v3 transport

`make java-vuser-slot-runtime` includes `apexload.host.PooledMain` in the separate
slot API artifact. This is an internal transport component. Runner admission comes from the explicitly enabled [shared-JVM policy](JAVA_SHARED_JVMS.md) or the evidence-pilot manifest; the raw transport is not an admission path.

The owner delivers a fresh 32-byte secret through a bootstrap stdin pipe, then
closes that pipe before user code runs. It must never put the secret in arguments,
environment, or script context. The JVM proves possession with a 32-byte
HMAC-SHA256 of `apexload.java_vuser.ipc.v3.host_auth:` plus the generation before
the owner accepts its IPv4 loopback socket. Unauthenticated preconnections are
rejected; the listener remains open for the JVM. Requests and replies then use
that duplex socket. The owner must bound socket acceptance/startup, independently
enforce the run lease, and drain raw stdout/stderr into bounded host diagnostics.
Java-visible stdin is empty, and direct reads of file descriptor 0 see EOF.
Java stdout/stderr are captured into a shared 32 KiB diagnostic buffer with a
dropped-byte count; they are not attributed to a slot. Direct file descriptor or
native writes stay on the supervisor's diagnostic streams and cannot corrupt
control framing. This protocol does not isolate hostile code within a JVM.

The binary envelope is a big-endian unsigned 32-bit length (at most 1 MiB), then
payload and a 32-byte HMAC-SHA256. Its MAC covers a direction byte (0=request,
1=response) followed by the full payload. Payload fields, in order, are:

- Contract `apexload.java_vuser.ipc.v3`, generation, slot, request, and operation,
  each encoded as a 32-bit UTF-8 byte length followed by strict UTF-8 bytes.
- Positive 64-bit sequence, bounded to signed Java `long`; request must be its
  decimal representation. Sequences strictly increase across the entire host.
- A 32-bit field count, followed by length-prefixed key/value strings. Duplicate
  keys, malformed UTF-8, trailing bytes, and oversized frames are rejected.

Identity strings and field keys are bounded to 128 bytes; values to 64 KiB;
field count to 1,024 (including the invocation's method field). Open binds slot
context without loading user classes. Commit opens the invocation barrier but
does not run constructors or initialization. Responses echo every identity and
operation, carry stable status/reap fields, and preserve both exit option and
exit status. The client rejects unknown, replayed, or mismatched responses.

Both transport writers have bounded queues and never block a slot callback or
control reader behind a slow write. The queues scale with the host's slot count
(Go: `2×slots+64` queued frames and `4×slots+64` outstanding requests, never below
the original 64/512; Java: `4×slots+64` queued replies), so a whole host finishing
an invocation at once queues instead of overflowing. With the original fixed
64/512 bounds, a 1,024-slot init burst reaped the host. The Go client retains identities rather than
parameter bodies for outstanding requests. Queue overflow, malformed transport,
and uncertain control completion retire the host; peers receive
`pooled_host_reaped`. Ordinary invocation cancellation requests permanent slot
closure and allows two seconds for qualified cooperative cleanup before notifying
the owner to reap containment. Neither this callback nor JVM exit is positive
proof that descendants stopped; the existing reservation remains charged until
backend evidence confirms that separately.

The real-JVM tests exercise forged/replayed frames, generation rejection, private
slot state, commit barriers, controlled exits, bounded diagnostics, raw stdout/
stderr writes, peer progress during a blocked invocation, cooperative closure,
ignored interrupts, and preservation of the triggering failure. Go tests also
cover blocked writers, queue exhaustion, response schema validation, and the
ledger's matching 1,024-slot host admission ceiling. A real-JVM burst test opens,
initializes, runs and closes all 1,024 slots of one host concurrently.

## Supervisor and local containment adapter

`HostSupervisor` owns backend handles for one execution/group ledger. It records
provisioning before external creation, binds the exact returned containment
identity before readiness, and retains uncertain or delayed creations for cleanup.
The identity is checked again before execution/termination and after termination;
a replacement identity cannot release the original reservation.
Preparation uses the ledger's deadline. A separate 25 ms monitor requests cleanup
on expiry without waiting for Java invocation queues; run expiry also cancels
pending provisioning. Closing the supervisor permanently retires the ledger, so
neither direct reservations nor lease renewal can revive the owner.

Commit first opens each prepared JVM's gate while ledger admission still rejects
all invocations. Only after every host acknowledges does the ledger commit the
reservation atomically. Concurrent preparation/commit attempts cannot bypass this
barrier. Any failure aborts the reservation; user initialization is never replayed.
A cooperative cancellation carries an explicit closure receipt from IPC v3 to
release only the slot's occupancy. Draining hosts keep their process and memory
charges until the containment backend confirms root and descendant termination.
Unknown cleanup becomes quarantine, including failed atomic starts, timeouts, or
missing creation outcomes. Normal drain is not reported as a transport failure.
Completed runtime handles and diagnostic buffers are released after confirmed
cleanup; bounded ledger history retains identity and accounting evidence.

`LocalHostBackend` uses the existing strong process-tree primitives. Production
launch fails closed on unsupported containment or ambient platform credentials.
The backend constructs a fixed `PooledMain` entry point, uses the existing VM
argument validator and minimized environment, and keeps scripts/dependencies off
the system classpath. It requires a resolved JDK 17/21, regular-file artifacts,
an explicit unambiguous heap ceiling below the host's total memory reservation,
and the same configuration identity as the allocation. It does not override
conflicting arguments. Raw stdout and stderr are drained independently into
256 KiB buffers per stream with saturating dropped-byte counts. JVM exit alone
never releases a reservation, and a failed strong start remains quarantined even
if a later kill appears to succeed.

The operator-owned `PooledLaunchProvider` must supply and revalidate qualified,
immutable artifact/runtime/resource identities. `PooledQualificationRegistry`
now supplies that provider through a frozen `PooledSelection`. Inspection hashes
the full JDK tree (including modules, configuration, and native implementation
libraries), harness, private shim, source archive, and ordered private/shared
classpath files. Exact VM arguments, target, work-directory path, sharing policy,
delegated-Linux backend, cleanup policy, and separate heap/native/stack/metaspace/
code-cache/capture budgets are part of the configuration identity. File scans
reject symlinks and special files, bound individual and aggregate bytes, and
honor cancellation between reads. Executable jars must pass bounded ZIP checks;
manifest `Class-Path` attributes and JAR indexes are rejected because Java can
use them to find dependencies outside the declared, fingerprinted classpath.
This does not prove that application code cannot load more code dynamically.
JDK and artifact inputs must be canonical,
operator-controlled immutable assets; hashing is not protection against a writer
changing files between verification and launch. The work-directory binding is
its path, not a snapshot of arbitrary files accessed by application code.

Inspection does not approve a workload. The trusted registry requires an operator
approval identity and evidence digests for artifact inspection, representative
execution, loader checks, and cleanup checks. Native assessment must explicitly
report no application native code observed; absent or positive findings cannot
qualify. JDK implementation libraries are fingerprinted without being classified
as application JNI. Evidence digests are operator attestations, not automatically
verified test results or a claim that scanning detects all native/background work.
The registry accepts at most 1,024 immutable profiles and grants only pilot
eligibility. It has no script/API mutation path and no production capacity claim.

One slot selects dedicated execution without inspecting pooled inputs. Missing,
incompatible, or unmatched profiles return dedicated mode with stable reasons.
Successful selection owns copies of its inputs; each launch revalidates those
inputs and checks the reservation's configuration, slot ceiling, and exact total
memory charge. Drift after selection fails preparation instead of changing mode.
Resource profiles, shared runner quotas, event ingestion, and pilot lease
negotiation are connected programmatically. Operator registry loading and
runner-command configuration are available through the explicit local pilot
[manifest in the pilot operations guide](JAVA_PILOT_OPERATIONS.md#explicit-local-pooled-pilot); distributed assignments remain dedicated. The local backend enforces the selected containment identity
before process start, in addition to its existing strong-containment capability
check; a delegated-Linux profile cannot execute through Windows Job Objects.
The in-memory ledger cannot serve as durable AKS quarantine storage.

Real-JVM component tests now drive supervisor → local backend → IPC v3 → two
slots. They verify commit gating, peer-preserving cancellation, retained draining
capacity, bounded raw output, and lease expiry while Java ignores interrupts.
Package-private test hooks use ordinary child processes for fixtures with no
descendants on the macOS development host. Those tests do not qualify Darwin,
JDK 26, delegated Linux, Windows Job Objects, or AKS containment. Production
containment and runtime checks remain enabled by default.

## Shared runner admission

`HostCapacity` is an in-memory execution-host budget shared by every participating
run/group and dedicated JVM. `NewHostLedgerWithCapacity` enforces both per-run
limits and that shared budget. Whole reservation batches are admitted atomically;
failure leaves the ledger's profile, identities, and usage unchanged. A draining
host continues to consume a host and its entire memory reservation even after a
qualified slot closure releases Vuser occupancy. Prepared or quarantined hosts
retain their charges until positive termination or definitive no-start evidence.
An owner epoch change cannot reset those charges.

`ServerOptions.HostAdmission` optionally connects dedicated execution to this
same authority with an explicit operator memory reservation. The sidecar retains
its existing local process limit and cancellation behavior. Uncertain dedicated
cleanup closes shared admission permanently, wakes blocked process/probe callers,
and retains the affected charge. Closing admission is not termination evidence.
The existing bounded probe lane stays separate so a full session pool does not
prevent readiness checks; operators must budget probe and runner overhead in
addition to execution-host memory. Reservations do not impose JVM or OS memory
limits or rewrite script arguments; the dedicated profile must cover every
workload admitted through that sidecar.

The default sidecar has no shared-admission option configured and keeps its
dedicated-only behavior. The pilot path has no environment switch that bypasses its manifest. The separate [shared-JVM operator policy](JAVA_SHARED_JVMS.md) configures ordinary shared runs.
Pilot integration must construct one shared authority, retain it
across owner replacement, and use it for all participants. Standalone ledgers
remain useful for component tests but do not enforce cross-group capacity.
Neither an in-memory authority nor a replacement empty authority is sufficient
for AKS restart recovery; durable quarantine remains required.

## Pooled run ownership

`Server.OpenPooledRun` accepts a frozen pilot qualification selection and binds
the resulting local supervisor to the currently claimed generator epoch. It uses
the same capacity authority as the sidecar's dedicated admission path. The opaque
in-process handle exposes reserve, prepare, commit, invoke, slot closure, abort,
renew, and close operations. The operator must explicitly supply shared admission
and enable Java execution. This programmatic interface is separate from the
opt-in pooled lifecycle and invocation contract below.

Each run has its own five-second renewable lease, independent of owner renewal.
Every operation checks owner fencing; expired or closed runs cannot be renewed.
Preparation remains cold, and only a committed reservation permits lifecycle
invocation. Owner takeover fences pooled runs and starts their cleanup before
waiting for in-flight operations. Owner expiry recognizes pooled-only activity
and starts containment shutdown. Run expiry closes its own group; a successful
cleanup does not terminate unrelated groups. Shutdown and run publication share
an admission lock, and takeover rechecks shutdown before acknowledging readiness.

Cleanup continues when a caller disconnects. Its runner-owned deadline is the
existing runtime-lease cleanup deadline (two seconds by default); uncertain
cleanup closes shared admission and retains the supervisor and reservations.
Late provisioning remains owned and must be positively terminated before a
replacement owner becomes ready. Closing or aborting a reservation is not proof
of termination.

An execution/group pair can be opened only once per epoch, including after
successful close. Up to 1,024 run records are retained; successful old-epoch
records are discarded only after fencing, while unresolved cleanup remains
tracked. The managed client and explicit engine pilot now supply renewal,
slot execution, and terminal-error handling; distributed placement remains
separate work.

## Versioned pooled lifecycle control

An operator can build immutable `PooledControlProfiles` from already-qualified
selections and explicit per-run budgets, then provide it together with
`HostAdmission` to `NewServer`. The local runner can also load that configuration
from `APEXLOAD_JAVA_VUSER_PILOT_MANIFEST`. Configured healthy runners
advertise `java_vuser_pooled_reservations_v1`, which does **not** advertise pooled
Vuser execution. Existing dedicated contracts and clients remain unchanged.

The authenticated `POST /v1/pooled-control` endpoint requires both the existing
runner contract and `apexload-java-vuser-pooled-control.v1`, a request identity,
and the claimed owner identity/epoch. Claim selects an operator profile and an
execution/group; later operations require the returned opaque run ID and token.
Profiles bind immutable runtime/resource identities before reservation. Effective
host/Vuser/memory ceilings cannot exceed the operator profile or shared budget,
and the host ceiling also respects the sidecar process limit. Remote callers
cannot supply artifact paths, JVM arguments, memory budgets, or arbitrary context
variables.

A runner accepts up to 64 concurrent runs per owner, one per execution/group,
with at most 4,096 Vusers and 1,024 JVMs per run; the shared host admission
budget caps their combined JVMs, Vusers and memory. Each prepared slot holds one
event-capture descriptor, so the runner also caps open capture files at half of
`RLIMIT_NOFILE` minus 1,024 reserved descriptors; a preparation beyond that fails
with `pooled_capacity_unavailable` before any file or JVM is created. Claim, renew, reserve, abort, and close are
available by default. A profile must separately
set `AllowPreparation` to enable `prepare`. Healthy runners with such a profile
advertise `java_vuser_pooled_preparation_v1`; the selected decision also reports
`preparation_enabled`. This operation provisions qualified local hosts and opens
cold slots; it cannot commit execution or invoke user code. A further
`AllowCommit` opt-in (requiring `AllowPreparation`) enables `commit` and
`close_slot`, advertises `java_vuser_pooled_commit_v1`, and freezes
`commit_enabled` in the run decision. None of these controls invokes user code.
Normal owner and run-lease expiry still apply in every phase.

Reserve, admitted preparation, commit, and abort stop adding request records
once the replay cache reaches 64 entries. Exact retries use the original
operation; changing a request under the same ID fails. Reserve replay checks that its hosts are still reserved. Responses
retain the original absolute preparation deadline, not a new window. A new attempt
after a cached reservation failure needs a new request ID. Renewal and closure
bypass this cache and its limit. Abort remains available when the cache is full
without adding a record; its ledger operation is idempotent. Closed-run reply
caches are discarded. Permanent slot closure can add one record per prepared
slot even above that limit; retries and foreign identities cannot grow this
extension. This keeps closure available after admission bookkeeping fills.
Aborting a reservation does not assert positive termination. Close reports
`reaped` only after the run supervisor confirms cleanup.

The client checks response contracts and request/run identities, resource bounds,
unique host/slot identities, operation-specific commit/closure receipts, and
positive run-close evidence. Unknown peer error text
is replaced with a bounded generic error. `PooledControlOutcomeAmbiguous` keeps
transport failures, malformed responses, and unproven cleanup distinguishable
from authoritative rejections. It never retries or falls back automatically.
An ambiguous claim must be abandoned and allowed to expire; issuing a new claim
must not be used to replay the same execution/group. A lost reservation response
can be retried with exactly the same request ID and input while its lease lives.

Preparation accepts the reservation's complete slot list with positive 32-bit
Vuser identities, an ignore-think-time flag, and a nonnegative millisecond limit.
Slot and Vuser identities must be unique; Vuser identities cannot be readmitted
in the same run. The runner binds scenario/group context to the claimed
execution/group IDs. It creates a private temporary directory and a distinct
0600 event file for each slot; paths never appear in control responses. The
runner retains the original descriptor for bounded event ingestion and never
opens a script-supplied capture path. Temporary paths are made absolute before
Java receives them, even if the operator supplies a relative `TMPDIR`. Existing shim file-size bounds still apply.

The first accepted preparation request owns the operation; disconnecting it
during startup cancels preparation. Concurrent exact retries only wait, with independently
cancellable waits. They do not provision again. An admitted preparation failure
cannot be retried under a new request ID or rebound to new context. Successful
replays recheck that every host remains ready. Both the startup timeout and the
response retain the reservation's original absolute deadline. Provisioning never
holds the control-cache lock, so abort and renewal remain available during
startup. No constructor, static initializer, or lifecycle method runs in this
phase; the existing Java worker preserves the commit/invoke barrier.

Event-directory reclamation waits until preparation has returned and all hosts
in that reservation have positive reaped/no-start ledger evidence. It then
attempts filesystem removal; removal failure does not imply failed containment.
Uncertain termination retains both capacity and capture files. Runner-owned
read descriptors close after active capture operations finish, including during
quarantine; closing those descriptors does not assert host termination. This local
in-memory ownership is not durable AKS quarantine. Component tests substitute
containment to exercise HTTP retries, blocked readiness, deadlines, disconnects,
foreign slots, context validation, and capture retention. They are not evidence
of a qualified delegated-Linux deployment or a pooling performance benefit.

Commit accepts only a successfully prepared reservation. One frozen operation
owns each reservation's commit: exact concurrent retries wait for its result;
a different request ID cannot commit it again. Commit uses the original
preparation deadline, including time spent waiting for host acknowledgments.
The supervisor keeps ledger invocation admission closed until every host has
acknowledged commit. Commit itself performs no construction, initialization, or
lifecycle calls. Failed or disconnected commit attempts abort the reservation,
including cancellation before the supervisor call. A successful replay checks
that every host is still committed; aborted, retired, and draining allocations
cannot be reported as freshly committed.

`close_slot` binds the run, reservation, host, and unique slot identity. It only
operates after successful commit. One frozen operation owns each slot closure;
exact retries reuse its result even after positive host retirement. Its
`slot_closed` receipt asserts qualified cooperative closure, never host reaping
or memory release. Closing the first slot seals its host, releases only that
slot's Vuser occupancy, and preserves surviving slots. Closing the last slot
requests host cleanup; the host and memory charge release only after positive
termination evidence. Uncertain closure fails the host and reaps peers while
preserving the triggering error; changing a request ID cannot repeat it.

Commit and slot-close I/O never hold the replay-cache lock. Renewal, abort, and
independent slot closures remain available during blocked controls. The first
request owns cancellation; cancelling a retry only cancels that retry's wait.
The client rejects inconsistent receipts and treats `pooled_host_reaped` as an
ambiguous failure, never as permission to release capacity or restart a Vuser.
The runner command enables these controls only through the explicit operator
pilot manifest. Distributed generator assignments do not enable them.

### Invocation and event capture

An operator profile must additionally set `AllowInvocation`, which requires
`AllowCommit` and `AllowPreparation`. Healthy configured servers advertise
`java_vuser_pooled_invocation_v1`; claim freezes `invocation_enabled`. The
existing authenticated control endpoint accepts `invoke` only for a committed,
prepared slot bound to its run/reservation/host. This remains a bounded local
pilot interface; no production mode selection or generator wiring is supplied.

Each slot accepts sequence 1 for `init`, then monotonically increasing sequences
for `action` and one `end`. Request IDs must be
`invoke-<slot_id>-<sequence>`; that prefix is reserved against other control
operations. Sequence consumption precedes capture reset and IPC submission.
Invocations are never replayed or response-cached, and failed initialization
cannot be restarted. After an ambiguous result, close/abort the session; never
invent another sequence to repeat the same work. Parameters have count, key,
value, UTF-8, and aggregate byte limits. Each request also carries a bounded
execution timeout independent of preparation's deadline.

One invocation owns a slot capture at a time. Concurrent submission receives
`pooled_slot_busy` without touching occupancy, files, or the active operation.
Renewal and permanent slot closure remain independent. Cancellation uses the
existing two-second interrupt grace and qualified closure receipt; unproven
termination reaps the host. Successful invocation returns the original return
code, exit option/status where present, duration, and the IPC event-request ID.
A host failure never triggers a replacement or state replay.

Before submission the runner checks that the capture path still identifies its
original regular-file descriptor and truncates that descriptor. After receipt,
it checks identity again and reads at most 256 KiB plus one detection byte.
Only complete UTF-8 JSON lines with the expected event contract, matching IPC
request ID, nonempty event type, and increasing positive event sequence are
returned. Truncation drops a partial final record and is reported explicitly;
shim truncation or fallback to host diagnostics also marks the result incomplete.
Malformed or mismatched events and replaced files fail the host. Cleanup waits
for active capture reads before closing descriptors and removing positively
reaped files. A replacement path can never make the runner truncate or read an
unrelated file.

The pooled private shim attributes events only while the slot lifecycle thread
is executing its invocation. Child-thread, shared-executor, and late events go
to bounded host diagnostics with an empty request ID. Such an attribution
violation is sticky: the current invocation's end or the next invocation's
begin fails, and the host follows the existing failure/reap path. This detects
observed misuse; it does not prove absence of arbitrary background work. Direct
stdout/stderr remain host diagnostics and are never merged into a slot's events.
Readiness must advertise `slot_thread_v1`; the slot worker also checks that
the private bridge and `lr` shim implement that attribution contract before
user initialization. Older hosts fail readiness and older/mismatched shims fail
before constructors or lifecycle code. Rebuild and requalify the changed host
and shim artifacts; an earlier qualification hash is not an upgrade.
Dedicated IPC v2 event behavior is unchanged. The pooled adapter now supplies
transaction ingestion to the Vuser loop. The explicit programmatic engine factory
also supplies staging and lifecycle execution. Normal mode selection and
host-diagnostic reporting remain required for broader pooling qualification.

### Managed run and slot client

`Client.OpenPooledClientRun` accepts an operator-owned frozen decision and an
execution/group identity after the generator has claimed its owner epoch. It
requires explicit invocation negotiation and checks the entire decision on
every response, including cleanup. The client snapshots the claimed owner;
changing the original client's owner cannot redirect an existing pooled run.
A failed or ambiguous claim is never replayed. Claim duration is bounded by the
lease interval independently of the run's lifetime context.

The managed handle renews its group lease independently of preparation,
commit, and invocation. The generator must maintain its existing owner
heartbeat separately. Group renewal cannot reacquire an owner or keep an expired
owner alive. Lifetime cancellation, renewal failure, or decision drift fences
new work immediately and starts a bounded close with the original epoch.
`Err` retains the triggering failure; `Close` separately reports cleanup
failure. A cancelled close wait may be repeated, and neither a timeout nor a
missing positive receipt is reported as successful cleanup.

`Prepare` reserves the full admitted peak once, assigns runner-created slot IDs
to validated Vuser contexts, and waits for cold readiness under the original
staging deadline. `Commit` opens the invocation barrier without initializing
Vusers. A failed or ambiguous preparation/commit permanently retires the run;
it cannot allocate replacement hosts or replay initialization. Dynamic expansion
is not provided by this bounded pilot client.

Slot handles enforce init/action/end sequencing and consume invocation sequence
numbers before submission. Any remote invocation failure permanently fences that
handle. Local argument rejection does not consume a sequence. Concurrent
invocation is rejected immediately, and slot closure does not wait on its
invocation mutex. An exact closure retry may recover a lost receipt without
reusing the slot. Closing one slot does not assert host or memory release.
The returned invocation retains its bounded events, return code, and exit
option/status for the protocol adapter.

### Pooled protocol and Vuser results

`NewPooledSlotProtocol` accepts a concrete managed slot and an action timeout.
It is not registered as a script-selectable protocol and does not claim, prepare,
commit, or renew a run. Its caller must first match the immutable qualification
to the script and own the managed run. The qualified target class is now carried
in the frozen control decision; clients reject missing/invalid target identities
and decision drift. Each action must target that exact class. Existing pilot
client/runner components must be upgraded together for the required target field.

The adapter supports exact `init`, `action`, and `end` methods with bounded string
parameter maps. It merges `parameters` followed by `lr_parameters`, preserving
the dedicated harness's override order. Other lifecycle spellings and complex
parameter values remain outside this pilot adapter and must use dedicated
selection before reservation. `Init` and health checks are cold; the authored
Java init/end actions own user lifecycle execution. Protocol teardown closes the
slot independently of blocked invocation and never invokes an implicit user end.

Every captured event is decoded into the typed timing schema. Malformed timings
fail the adapter instead of disappearing silently. Transaction and parent IDs
are prefixed with slot and IPC request identities, preventing collisions across
hosts or invocations. Child samples use the existing transaction parser and
engine metrics/redaction path, retaining measured durations. Host generation and
slot identity accompany both aggregate and child results; shared host output is
not treated as slot output. Explicit bounded host-diagnostic reads and automatic value-free evidence collection are implemented; raw-output archiving remains separate.

Exit behavior comes from the authenticated invocation frame, not event text.
Failed exit status produces one synthetic failed exit sample even when capture
is truncated. A private in-process lifecycle-control receipt preserves status
independently of action/iteration continuation. The Vuser loop honors iteration
exit through nested action lists, continues subsequent iterations, and runs normal
teardown. Action continuation preserves failed iteration accounting without
silently converting it into a whole-Vuser stop. This receipt is excluded from
JSON contracts. Dedicated IPC v2 behavior and automatic mode selection are unchanged.

### Programmatic engine staging pilot

`engine.NewJavaPooledPilot` is a separate trusted factory. The ordinary `New`
factory, script settings, ordinary CLI runs, and generator assignments still
select dedicated execution. The separate `apexbench java pilot` command opts in.
The pilot requires an already claimed sidecar client, an explicitly
negotiated immutable pooled decision, and an operator-approved DSL SHA-256 paired
with that runtime identity. `JavaPooledScriptSHA256` computes identity only; it
does not qualify workloads. The constructor freezes the DSL and accepts only a
closed workload with one exact `init`, `action`, and `end` action, the qualified
target class, bounded string parameters, and bounded timeouts. It has no fallback
or dynamic expansion. Each engine instance can run once, including after failure.

Preparation claims one execution/group, reserves and readies its complete peak,
and commits before the engine starts its duration timer or ramp. Preparation has
its own operator deadline. The engine binds one prepared protocol adapter to each
Vuser, preserving direct-engine zero-based IDs. The trusted run options supply
Java's group and scenario identities. Static parameter validation happens before
admission; resolved parameters are revalidated by the adapter before invocation.
The existing shared runner ledger remains authoritative for host/memory capacity;
the dedicated per-Vuser capacity reservation is not applied to this pilot.

Authenticated `POST /v1/owner/renew` extends only the current, unexpired owner
epoch. It cannot claim or replace an owner. `PooledClientRun.RenewOwner` uses the
run's frozen epoch, and the pilot renews it independently of blocked preparation
or invocation. Group renewal remains independent too. Either lease failure cancels
the pilot and requests containment cleanup; neither permits automatic takeover or
replay. Staging failure, cancellation, lost commit receipts, and cleanup uncertainty
remain failures. Final cleanup also checks the retained group failure so a lease
lost at the last Vuser's exit cannot be hidden by heartbeat shutdown. Slot-close
errors are also retained, even when subsequent whole-run cleanup is confirmed;
the ordinary engine's teardown logging cannot turn an uncertain slot close into
a successful pilot result.

## Review fixes and next-phase boundaries

Repeated review added regression coverage and corrected these issues:

- Protocol integration binds target identity to the frozen qualification,
  namespaces transaction IDs by slot and request, rejects malformed typed
  event timings, and derives exit behavior/status from the invocation frame.
  Iteration continuation is independent of failure accounting and unwinds
  nested action lists without replaying initialization. Empty slot handles
  are rejected before dereferencing their run binding.

- Managed clients freeze the owner epoch, preserve the original staging
  deadline through commit, and reject decision drift even on cleanup receipts.
  Run renewal and close remain independent of blocked work. Parameter bounds
  are checked before copying, and the initial claim has its own timeout.

- Pooled background/late events cannot inherit the active request ID; observed
  attribution violations remain terminal rather than resetting next iteration.
  Capture reads pin the created file and reject path replacement.
- New invocation/capture failure reasons are accepted by the host ledger's
  stable-code allowlist, so failures actually request immediate cleanup while
  retaining their triggering reason. Cooperative concurrent closure preserves
  peer slots. Failed initialization cannot restart, and invocation identities
  cannot collide with control-request identities.

- Commit cannot extend the original preparation deadline or be repeated under
  a new request identity. Cancellation before supervisor dispatch now aborts the
  reservation or fails the closing host, rather than leaving a live allocation
  behind a permanently failed control record. Slot-close records remain bounded
  while allowing every admitted slot to close after the normal replay limit.

- Cold preparation freezes typed slot context, never accepts remote event paths,
  and reclaims capture files only after positive host cleanup. Concurrent retries
  cannot reprovision, and abort cannot be blocked by startup or a full replay cache.

- Reservation replay retains the original preparation deadline and rejects
  expired or aborted allocations. It cannot replenish the preparation window or
  charge a second allocation. The bounded replay cache cannot block renewal or
  closure when full.

- Shutdown could otherwise miss a run published after its cleanup snapshot.
  Pooled publication now shares shutdown's admission lock. Owner takeover also
  checks shutdown again after cleanup before publishing a ready generation.
- Owner-expiry detection now includes pooled-only activity. Completed pooled
  records are removed when an idle epoch is relinquished, while same-epoch
  tombstones continue to reject initialization replay.

- Shared admission closure originally woke only callers waiting on the shared
  budget. Callers waiting on local process/probe semaphores now receive the same
  terminal fence, and readiness checks recognize closure by another participant.

- Warm-up could start while Java initialization was still running. An opt-in
  engine initialization observer now gates it on every Vuser completing init.
- Latency collection excluded requests that completed after the measurement
  deadline. The collector now retains those timings through graceful drain and
  reports throughput and latency population counts separately.
- A target class missing from the private classpath could resolve from the
  shared loader. The worker rejects that before static initialization.
- A queued cancellation could complete a future while holding the control lock.
  Completion callbacks now run outside that lock.
- AppCDS validation could block opening a FIFO before checking its type. It
  checks the file type before opening, with a Unix FIFO regression test.
- Engine-configuration errors could leave a sample marked complete despite
  missing measurements. Error samples are now explicitly incomplete.
- The Go ledger accepted profiles above the Java host admission ceiling. Both
  now enforce the same maximum of lifetime slot admissions per host (raised from
  256 to 1,024 on 2026-10-15).
- A binary response stream on stdout could be corrupted by raw/native output.
  IPC v3 replies now use a separate authenticated loopback socket.
- Pending transport requests retained parameter bodies after serialization.
  They now retain only correlation identities; oversized maps are rejected before
  copying, and response diagnostics are strictly bounded and decoded.
- Interrupting a peer could race ahead of the original failing invocation's
  response. The host records the triggering request before interrupting peers;
  only that terminal response may retire the transport. A concurrent blocked-peer
  regression test verifies the original error and peer host-reaped classification.
- Supervisor review fixed delayed-creation cancellation on lease expiry, runtime
  buffer retention after successful cleanup, late preparation admission, and
  normal drain being misclassified as transport loss. Cancelled slots now carry
  an explicit closure receipt rather than incurring a second interrupt grace.
- Changed backend identities, blocked cleanup, and containment uncertainty during
  qualification retain quarantine. Partial native-output drains are marked
  truncated, and later peer calls preserve the `pooled_host_reaped` terminal code.
- Launch construction is fixed to `PooledMain`; callers cannot provide a custom
  command that initializes user code during preparation. Runtime, classpath,
  heap, VM-policy, environment, and configuration checks run before process start.
- Qualification review replaced ambiguous native-code defaults with an explicit
  assessment, bound and enforced the containment backend, and required regular launcher/release
  files. Trailing whitespace cannot make the launcher select a different artifact
  than the one hashed, and malformed UTF-8 cannot collapse distinct inputs into
  the same JSON-based identity. Manifest/index references cannot add undeclared
  jars to the loader. Tests cover ordered inputs, runtime/artifact drift, frozen ownership,
  cancellation, scan limits, concurrent revalidation, and pre-start rollback.
- The full-suite opt-in inventory guard found the AppCDS test gate undocumented.
  It is now in the QA inventory and both Java opt-in suites are discoverable
  through `scripts/run-optin-suites.sh`.

The next-phase ledger never treats a deleted/missing pod or a requested kill as
positive termination evidence. A provisioning operation must be recorded before
its first external side effect; if that operation completes after preparation
expiry, its identity is attached to the quarantined reservation for cleanup and
cannot pass the ready/commit barrier. Closed slots release their Vuser charge;
draining and quarantined hosts retain process and memory charges until reaped.
The ledger also bounds retained lifetime records and admissions, failing capacity
instead of recycling identities. The Java host seals on its first slot closure,
does not replace active state, and rejects later admission.

These are internal components. The supervisor/local backend now drives expiry
checks, independently enforces leases, executes cleanup intents, and submits
termination evidence from the containment backend. Production runner and generator
integration is still required; the explicit engine pilot is experimental. The in-memory ledger must not
be used as the durable quarantine store for AKS. Neither a Java worker's stopped
future nor a closed classloader proves arbitrary background work has stopped.

The next-phase review also verifies failed initialization requests host reaping,
expected cooperative interruption leaves a peer usable, host failures preserve
the first stable failure reason, unrelated hosts remain usable, and rejected
reservations cannot mutate accounting. Configuration profiles cannot change
within one ledger. This ledger covers a single execution/group; fleet-wide
admission must additionally enforce shared generator/broker limits.
