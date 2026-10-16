# Java Vuser pooling: evidence tooling and implementation status

Status: Shared JVMs available for ordinary runs behind an operator switch and a per-script setting (2026-10-15); the pilot evidence collector and dedicated execution-pod integration remain; no enabled AKS backend

This document tracks the evidence-first implementation of pooled Java Vuser
hosts. Dedicated execution remains the default runtime path. Explicitly wired
operator profiles can enable the experimental pooled HTTP contract. Nothing in
this change raises default process capacity or qualifies an AKS containment backend. The existing performance-evidence report contract is
unchanged; Java evidence has its own `apexload.java-vuser-evidence/v1` schema.

The offline dedicated integration now includes multi-generator sharding, separate
generator/facade and execution-host budgets, cold preparation of all scheduled
lifetime admissions, and live reservation reporting. Down/up schedules consume
fresh identities; closed sessions are never recreated by scaling or rollback.
Review corrections also cover prepared capacity retention, old-peer rejection,
missing placement metadata, and reporting in the compact Configuration view.

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

## Implemented components

| Component | Current boundary |
| --- | --- |
| `apexbench java fixture` and `target` | Reproducible light HTTP and middleware-heavy fixture source, pinned dependency declarations, validated persistent-lifecycle DSL, and a loopback target. |
| `apexbench java baseline` | Linux collector using the ordinary engine → authenticated sidecar → dedicated persistent harness path. Includes aggregate process PSS, cgroup memory, CPU, memory-limit/OOM events, throughput, transaction p95/p99, errors, and dropped results. Requires an already configured contained runner. |
| `apexbench java pilot` | Explicit operator-approved single-group Linux engine → sidecar → pooled-host path, using the same collector as the dedicated baseline. Fresh execution per repetition, independent owner renewal, bounded host diagnostics, and optional observed soak scheduling. No production qualification claim. Requires a reviewed runner manifest and delegated containment. |
| `apexbench java compare` | Strict, bounded report reader and matched single-workload gate evaluator. Requires five complete repetitions, the 30% memory benefit, ≤5% throughput/latency regressions, correctness, achieved offered load, bounded variation, and an observed matching one-hour soak with initialization/closure evidence. A pair never grants production qualification. |
| Operator AppCDS manifest | Opt-in archive validation and launch arguments on dedicated execution paths. Script archive-path restrictions remain enforced. No archive or memory benefit is supplied automatically. |
| Private slot shim and bridge | Per-loader `lr` context and parameters, event destination, and exit option/status. Shared API is independent of application types. Dedicated direct `lr` behavior remains available. |
| Experimental slot worker | Prepare/commit/invoke barrier, construction and lifecycle on one platform thread, one outstanding invocation, sequence replay rejection, independent interruption, permanent closure. Experimental HTTP and programmatic pilot integration; it is not a containment boundary. |
| Experimental pooled host | Shared dependency loader with private script classes; generation checks; commit barrier; sealing and peer-preserving cooperative closure; failed initialization and unexpected invocation failures request whole-host reaping. Component tests exercise two real Java slots. The experimental IPC entry point is separate from the production runner. |
| Experimental IPC v3 | Authenticated, bounded Go/Java frames and multiplexed transport; readiness/open/commit/invoke/interrupt/close/health; two-second cancellation grace; response socket separated from native stdout; bounded Java-stream host diagnostics. HTTP invocation requires a separate operator opt-in; distributed generator integration remains pending. |
| Run/group host ledger | In-memory prepare/ready/commit reservations bound to one owner epoch and configuration; actual host/slot/memory accounting; permanent retirement, expiry, quarantine, and identity-checked positive termination confirmation. |
| Shared execution-host capacity | Atomic host/Vuser/memory admission across run/group ledgers and the dedicated sidecar path; charges survive draining, owner retirement, and uncertain cleanup. Programmatic wiring and an explicit local pilot manifest; no production pooled endpoint or durable broker authority. |
| Pooled run ownership | Programmatic qualified-run handle scoped to one execution/group and the sidecar's generator epoch; independent renewable lease; takeover/expiry/shutdown cleanup; duplicate-run rejection and retained quarantine. An opt-in HTTP contract wraps this handle; generator RPC integration remains pending. |
| Versioned pooled lifecycle control | Explicitly configured authenticated HTTP claim/renew/reserve/abort/close operations, plus separately enabled cold preparation and commit/slot closure. Run tokens, bounded replay, typed slot context, runner-owned event files, frozen resource metadata, and strict client validation. Separately enabled invocation adds at-most-once sequences and bounded attributed events. No automatic mode selection. |
| Managed pooled client | Explicit, frozen-owner run handle with independent group renewal, bounded cold preparation, a separate commit barrier, and non-reusable slot handles. Enforces sequence/lifecycle rules, terminal uncertainty, and positive cleanup receipts. Used by the explicit programmatic engine pilot; normal engine and generator selection remain dedicated. |
| Pooled protocol adapter | Adapts a prepared slot to the engine's streaming result path, with qualified target binding, bounded parameters, strict event decoding, transaction attribution, and authoritative exit status/control. Tested through the Vuser loop and managed HTTP client; normal engine factories and generator assignment staging do not select it yet. |
| Programmatic engine pilot | `NewJavaPooledPilot` freezes an operator-approved DSL/runtime pair, stages the full peak before starting duration/ramp, commits before lifecycle calls, and maintains independent fenced owner/group leases. Single-use, closed workload, exact init/action/end methods. Used by the explicit `apexbench java pilot` command; no distributed generator activation. |
| Host supervisor and local backend | Join the ledger to IPC v3 and strong process containment; independent lease monitoring, all-host commit barrier, bounded native output, cancellation receipts, and exactly-once cleanup. Internal components only: no production sidecar endpoint, engine lease protocol, or durable AKS broker is enabled. |
| Operator pilot manifest | Bounded, strict, versioned local file binding one reviewed qualification, resolved launch request, approved DSL digest, stage permissions, and a shared host/Vuser/memory budget. Runner loads it before opening its listener; Linux-only and disabled when the environment variable is absent. |
| Pilot qualification registry | Operator-owned evidence records bound to inspected runtime/artifact/resource identities; frozen dedicated/pilot selection, stable fallback reasons, and pre-launch revalidation. Programmatic interface and operator manifest loading; no production qualification, registry configuration endpoint, or automatic approval is supplied. |

## Shared JVMs (runner-qualified)

The operator policy replaces the hand-signed pilot manifest for ordinary runs.
Set `APEXLOAD_JAVA_VUSER_SHARED_JVM=true` on the Linux runner sidecar together
with the existing runner enablement and authentication. The runner then builds
one admission authority that every shared and dedicated JVM charges:

| Setting | Default | Meaning |
| --- | --- | --- |
| `APEXLOAD_JAVA_VUSER_SHARED_JVM_VUSERS_PER_JVM` | 250 | Lifetime slots per shared JVM (2–1,024). A script may ask for fewer. |
| `APEXLOAD_JAVA_VUSER_SHARED_JVM_HEAP_MB` | 1,024 | `-Xmx` of each shared JVM. |
| `APEXLOAD_JAVA_VUSER_JVM_MEMORY_BUDGET_MB` | process limit × one JVM reservation, capped at the container memory limit less 256 MiB | Runner-wide JVM memory budget. An explicit value is used as given. |
| `APEXLOAD_JAVA_VUSER_DEDICATED_JVM_MEMORY_MB` | 256 | Budget charge of one dedicated JVM. |
| `APEXLOAD_JAVA_VUSER_SLOT_API_JAR`, `..._SLOT_SHIM_JAR` | beside the harness jar | Artifacts from `make java-vuser-slot-runtime`; the runner image ships both. |

`APEXLOAD_JAVA_VUSER_MAX_PROCESSES` still caps JVMs per runner, now counting
shared and dedicated JVMs alike. A runner refuses to start when the policy is
invalid (including a container too small for one shared JVM), when it is not
Linux, or when the pilot manifest is also set.

Shared JVMs need the same delegated cgroup v2 containment as persistent Java
lifecycles. The stock compose service and Helm sidecar run without it, so they
do not advertise shared JVMs even with the policy on, and groups keep one JVM
per VUser. On a governed runner, set the policy through the environment above
or the chart's `javaVuser.runner.sharedJVM` values. Pooled hosts run the
sidecar's own credential check, which accepts the runner's loopback token and
Kubernetes service links; the host JVM's environment is allowlisted.

A shared run starts with the `claim_shared` control operation. It carries the
same runtime settings a dedicated session sends (JDK selection, exact VM
parameters, classpath and uploaded classpath JARs), the target class and the
script digest. The runner resolves them with the dedicated-mode validation and
then qualifies the workload itself:

- The JDK must be HotSpot or OpenJ9 17 or 21.
- Every script classpath entry must be a jar. The jar that holds the target
  class is private to each VUser, so the Actions class and its statics stay per
  VUser. Other script jars and uploaded JARs load once in the shared loader. With
  the script's dedicated class loader setting, every script jar is private.
- The static native scan of the application jars must be complete and find no
  indicators: native methods, `load` or `loadLibrary` calls on `System` or
  `Runtime` (any `loadLibrary` name counts), JNA, JNR or `java.lang.foreign`
  references, or packaged native libraries. Methods that are merely named
  `load` (`Properties.load`, `KeyStore.load`, `ServiceLoader.load`) do not
  count. The scan does not prove the absence of dynamic native loading or
  background work; enabling the policy is the operator's acceptance of that.
- The JDK home is hashed file by file. A symlink inside it counts when its
  target and its resolution both lie inside the home, at a regular file, a
  directory or the home itself: the per-module legal notices JDK builds link to
  `java.base`, the `jre -> .` link of Alpine's packages, the man-page locales of
  full JDKs. Its target joins the identity. The one link allowed out of the
  home is the trust store `lib/security/cacerts`, which distribution packages
  such as Alpine's link to the system CA bundle; the bundle's content joins the
  identity. Any other link is refused.
- Script heap sizing (`-Xmx`, `-Xms`, `-XX:MaxRAM*` and similar) is dropped and
  reported as `shared_jvm_script_heap_ignored`; the operator heap applies. Other
  VM parameters pass through unchanged.

A refusal uses a stable code (`shared_jvm_runtime_unsupported`,
`shared_jvm_classpath_unsupported`, `shared_jvm_native_code_detected`,
`shared_jvm_inspection_incomplete`, `shared_jvm_inputs_unsupported`, or a
dedicated-mode runtime code) and leaves the workload eligible for dedicated
JVMs. Each launch re-verifies the frozen qualification, as with the pilot.
Healthy runners advertise `java_vuser_shared_jvm_v1` and a `shared_jvm`
capacity envelope (slots per JVM, JVM reservation, maximum and available
VUsers) on `/v1/capabilities`.

## LoadRunner thread mode

LoadRunner runs a Java Vuser script's VUsers as threads of one JVM with one
class loader (`UseThreads=1`, `enable_CL_per_vuser=0`). Scripts written for
that model, such as clients of full WebLogic 9.2 and vendor libraries with
native code, fail every shared-JVM check above: they need Java 8, their
libraries call native code, and the WebLogic client jar has 34,254 entries and a
manifest `Class-Path`. Thread mode (`jvm_sharing: threads`) keeps LoadRunner's
model on the same host:

- `APEXLOAD_JAVA_VUSER_THREAD_MODE=true` admits it on a runner with shared JVMs
  (`javaVuser.runner.sharedJVM.threadMode` in Helm). The runner advertises
  `java_vuser_loadrunner_threads_v1`; a threads claim to a runner without it is
  refused with `shared_jvm_loadrunner_threads_disabled` and the group runs
  dedicated. A scenario override into thread mode needs the script approved
  again.
- Per VUser: the jar holding the target class and every jar whose class files
  name `lrapi/`, since each slot has its own `lr`. With the script's dedicated
  class loader setting, every jar. The rest are libraries loaded once.
- Per-VUser jars keep every check (bounds, no manifest `Class-Path`, a complete
  native scan with no indicators). Library jars are not scanned for native code,
  may hold up to 65,535 entries, and may carry a manifest `Class-Path` as long as
  nothing it names exists, so the hashed jars are still all that loads.
- Any HotSpot or OpenJ9 runtime the runner resolved qualifies, Java 8 included
  under `APEXLOAD_JAVA_VUSER_ALLOW_LEGACY_JAVA`. A Java 8 home reported as the
  JDK's `jre` directory is hashed with the launcher and release file above it,
  and in-home directory links (JDK man-page locales) join the identity.
- The sharing profile is `shared-auto-lr-threads`, and the launch spec's
  thread-mode flag is part of the configuration digest (omitted when unset, so
  other modes keep their digests). The host receives `loadrunner_threads` as its
  last argument.
- A script exception fails only its VUser's call: the host answers with return
  code -1 and the exception's class and message (one line, at most 2,048
  characters) instead of reaping the JVM. A failed `init` ends that VUser
  without its `end` call; an `action` exception fails the iteration and the slot
  stays usable.

Measured on Docker Desktop (Apple silicon, Temurin 8u504, 4 CPUs, 512 MB heap
per shared JVM) with a synthetic script that loads 400 classes from each of a
WebLogic 9.2 client classpath's seven jars and then idles 500 ms per iteration:

| VUsers | Mode | JVMs | JVM memory (PSS) | Errors |
| --- | --- | --- | --- | --- |
| 10 | One JVM per VUser | 10 | 1,260 MB | 0 |
| 10 | LoadRunner threads | 1 | 297 MB | 0 |
| 100 | LoadRunner threads | 1 | 486 MB | 0 |

The imported scripts themselves qualified on the same runner and ran their real
`init` in one JVM: offline, the first VUser's vendor login failed with its own
error and the others reported the shared class's failed initialization, as
LoadRunner reports it, without ending each other's VUsers.

## Shared JVMs across generators

A generator whose runner hosts shared JVMs reports a `shared_jvm` envelope on
the `java_vuser` runtime capacity in every heartbeat: VUsers per JVM, the
runner's maximum, and the VUsers still available. The available figure is the
lower of the runner's live figure (which already reflects dedicated JVMs and
quarantined hosts) and the runner maximum minus every shared VUser this
generator has admitted, so a heartbeat-cached runner figure cannot over-admit.

For a group whose script asks for shared JVMs and has a supported shape, the
controller places VUsers by that envelope (one slot per VUser) instead of by
dedicated JVM slots, and generators without an envelope fall back to their
dedicated capacity. The generator admits such a group against the same
envelope, records it as shared so it never charges dedicated capacity, and
stages its JVMs during assignment staging, inside the staging deadline, so
commit never waits on cold JVM starts. Rollback and expiry close the staged
JVMs like pod-based Java groups. A shared group cannot grow past its staged
VUsers (`java_shared_jvm_additional_slots_required`). One runner run holds at
most 4,096 VUsers, so the engine splits a larger group on one generator into
runs of whole JVMs that are claimed, prepared, committed and closed together;
a failure of any run after the first claim fails the group. While one group
prepares, the generator keeps its other staged groups from expiring, and the
controller waits the whole preparation budget (5 minutes) for the assignment.

Dedicated JVMs share the runner's process slots and memory budget with shared
JVMs, so the runner also reports `dedicated_capacity`: the dedicated JVMs it
could hold beside its current shared JVMs. Generators admit and advertise
dedicated Java work against the lower of that and the process limit.

If the runner refuses the claim at staging, the group runs dedicated and must
fit dedicated capacity; otherwise staging fails with the dedicated capacity
error. With 250 VUsers per JVM and the default 25 JVMs per runner, one
generator can host 6,250 shared Java VUsers, subject to its memory budget, so
10,000 VUsers need two generators instead of 400.

## Shared JVM benchmark (2026-10-15)

`scripts/java-shared-jvm-bench/run.sh` runs the middleware-heavy evidence
fixture (`HeavyActions`: Apache HttpClient, Jackson and Guava, a GET and a POST
per transaction) through the real engine against a Linux runner in Docker
(Docker Desktop on Apple silicon, Temurin 21, 6 CPUs and 6.5 GB for the runner
container, 1,024 VUsers and a 320 MB heap per shared JVM). The order service
runs in the same container, so CPU figures include it.

| 25 VUsers, 60 s, 10 s warm-up | One JVM per VUser | Shared JVMs |
| --- | --- | --- |
| Paced (target 1,000 tx/s): achieved | 786–927 tx/s | 980–985 tx/s |
| Paced: p50 / p90 / p99 | 5.0–5.7 / 9.0–12.7 / 12.8–51 ms | 2.0 / 6.2 / 9.4 ms |
| Paced: CPU | 1.9–3.0 cores | 1.0 core |
| JVM memory (PSS) | 3.8–4.8 GB (25 JVMs) | 0.30–0.35 GB (1 JVM) |
| All VUsers initialized | 4–13 s | 0.7–1.0 s |
| Closed loop: throughput | 0.9k–4.6k tx/s | 3.8k–8.9k tx/s |

Shared JVMs had the higher closed-loop throughput in every same-container pair.
Closed-loop runs saturate the CPUs, and the shared Docker VM made them vary
widely between runs; their p99 was mixed (13–47 ms shared, 32–89 ms
dedicated). Shared paced figures repeated closely; ranges cover every run.

5,000 VUsers on one runner, paced at one iteration per VUser every 5 s with a
60 s ramp, ran in 5 shared JVMs: 421,758 transactions, no errors, steady-state
p50 / p95 / p99 2.0 / 6.2 / 14 ms, 3.1 GB of JVM memory (4.2 GB for the whole
container) and 2.7 cores. One JVM per VUser at this fixture's 150–190 MB each
would need several hundred gigabytes.

The bench binary's `-script <file>` runs an APEX java_vuser DSL script, such
as an imported one (bare or wrapped in `{"script": …}`), instead of the
fixture. `-classpath`, `-vm-args` and `-pace-ms` then replace the script's own
values when given, `-sharing` and `-vusers-per-jvm` apply as usual, and
`-target` is refused. `run.sh` always runs the fixture, so for a script start
the runner container the same way and call `/bench/bench -script …` with
`docker exec`. The summary's `failed_transactions` counts business
transactions that ended failed, warm-up included.

The benchmark found and fixed: a native-code scan that flagged any method
named `load`; JDK legal-notice symlinks failing input qualification; pooled
hosts running the generator's credential guard, which refuses the runner's own
token; groups over 4,096 VUsers on one generator; and a 40 ms tail from Nagle's
algorithm on the host control socket.

## Building and checking fixtures

Requirements: Go toolchain, JDK supporting `javac --release 8` and `--release 17`,
and Maven. The first build resolves the pinned Maven dependency graph; subsequent
builds can use its local cache. The versions reproduce the prototype dependency
family and are not production dependency recommendations.

```bash
bash scripts/build-java-vuser-evidence.sh
go build -o bin/apexbench ./cmd/apexbench
make java-vuser-slot-runtime java-vuser-runner-legacy

APEXLOAD_JAVA_EVIDENCE_FIXTURE_DIR="$PWD/bin/java_vuser/evidence" \
  go test ./runners/java_vuser -run TestJavaEvidenceFixturesThrough -count=1
```

The optional fixture tests exercise the real IPC v2 harness and concurrent pooled
slot workers against a local target, with the actual middleware dependencies in
the shared loader. They do not run the engine/sidecar containment path, measure
PSS, qualify pooled containment, or validate a JDK 8 runtime. The slot sources and legacy harness compile
to Java 8 bytecode; actual JDK 8/17/21 runtime qualification remains separate.

Start the target in a separate terminal **outside the accounting cgroup**:

```bash
bin/apexbench java target -addr 127.0.0.1:18099
```

Generate each fixture using exact classpath entries, preserving their order:

```bash
fixture_args=(-classpath "$PWD/bin/java_vuser/evidence/fixtures.jar")
for dependency in "$PWD"/bin/java_vuser/evidence/lib/*.jar; do
  fixture_args+=(-classpath "$dependency")
done
bin/apexbench java fixture -workload light "${fixture_args[@]}" -output /tmp/java-light.json
bin/apexbench java fixture -workload middleware-heavy "${fixture_args[@]}" -output /tmp/java-heavy.json
```

The fixture target accepts only loopback bindings, and generated fixtures use a
loopback HTTP origin. To measure on another backend, run its equivalent target in
the appropriate network namespace and independently qualify that deployment.
Both fixtures emit one `order-flow` transaction per iteration, with fixed-interval
500 ms pacing. The heavy fixture uses HttpClient, Jackson, and Guava, checks the
response, computes a digest, posts the payload, and checks the acknowledgment.

## Collecting a dedicated baseline

Use delegated cgroup v2 on Linux with the already-supported strong containment
configuration. Place the benchmark process, sidecar, and all Java descendants in
one **exclusive accounting subtree**, including all runner overhead. The target
service must be outside it. The operator must attest this boundary: observing
the benchmark and runner PIDs does not prove that every related process is in
the subtree. Do not use the root/system cgroup or a subtree shared by unrelated
runs. Keep background processes outside the subtree.

The benchmark inherits the existing operator runner configuration; it does not
enable Java, grant permissions, install containment, or bypass policy. Supply a
runner PID visible in the benchmark's PID namespace. For example, from inside
the preconfigured accounting subtree:

```bash
bin/apexbench java baseline \
  -script /tmp/java-light.json \
  -cgroup /sys/fs/cgroup/apex-java-evidence \
  -runner-pid "$JAVA_RUNNER_PID" \
  -workload light -profile dedicated-default \
  -runtime-sha256 "$JAVA_RUNTIME_MANIFEST_SHA256" \
  -environment linux-image-and-hardware-id \
  -resources dedicated-resource-profile-id \
  -target fixture-target-build-id \
  -vusers 25 -repetitions 5 -warmup 40s -duration 60s \
  -output /tmp/java-light-dedicated.json
```

`runtime-sha256` is the hash of an operator-maintained manifest identifying the
exact JDK, harness build, and immutable dependency contents for the comparison.
The comparison manifest should identify both harness variants when a pooled
runner exists, so matched reports use the same comparison identity. Labels and
manifests must not contain credentials. `configuration_sha256` hashes
the exact authored DSL for dedicated runs and the DSL approval plus frozen runner
decision for pooled runs. The separately recorded profile and runtime manifest
must identify operator tuning and archive configuration as well.

The collector samples every 250 ms. It deduplicates PIDs across the cgroup tree
and sums each process's PSS, avoiding RSS double counting of shared pages. Missing
PSS/cgroup data, disappearing processes, lost metrics, early completion, or an
inconsistent active-Vuser gauge mark the run incomplete. Memory-limit, OOM, and
OOM-kill counters are compared with pre-run values; their sum is a failure
indicator, not a count of distinct OOM incidents. Startup peak includes startup
and warm-up and is a sampled peak, not a kernel-recorded maximum. Cgroup peak is
reported separately from PSS. The benchmark's own collection overhead is inside
the accounting scope and must remain identical in matched comparisons.

Warm-up begins only after every Vuser has completed initialization, not when its
goroutine becomes active. Throughput counts business-transaction completions in
the measurement window. Latency follows transactions started in that window
through graceful drain, including slow completions after the window ends;
`latency_samples` records that separate population. This avoids discarding the
slow tail at the end of a run. Warm-up/teardown failures still invalidate results.

Raw `memory_bytes` values are **total resident PSS**, not per-Vuser or incremental
memory. The gate uses the median of steady-state samples within each repetition,
then the median across repetitions. Divide total memory by the admitted Vusers
only when reporting a per-Vuser average. An incremental slope requires separate
matched runs at different Vuser counts; it is not that average. Do not infer a
250-user total from a 25-user average.

## Explicit local pooled pilot

The local runner accepts `APEXLOAD_JAVA_VUSER_PILOT_MANIFEST` only on Linux, with
runner enablement and authentication already configured. Without that variable,
its existing dedicated behavior and 25-process default are unchanged. An invalid
manifest stops startup before opening the listener; it does not fall back to a
different execution mode. The benchmark and runner must read the same reviewed
file and immutable assets in the same filesystem/PID namespace.

The manifest schema is `apexload.java-vuser-pilot-runner/v1`. Its outer fields are:

| Field | Required operator input |
| --- | --- |
| `schema` | The exact schema above. |
| `qualification` | A `javarunner.PooledQualificationProfile`: `Schema`, `ID`, `ConfigurationSHA256`, and `Evidence`, including all four evidence hashes, `ApprovedBy`, and the native assessment. |
| `request` | The corresponding `javarunner.PooledQualificationRequest`: `Launch`, `SourceArchive`, `SharingProfile`, and `Resources`. Use the previously resolved JDK record, exact absolute artifact paths, exact ordered JVM arguments, private/shared classpaths, and explicit total-memory components. |
| `script_sha256` | `engine.JavaPooledScriptSHA256` of the parsed, approved DSL, reviewed together with that runtime profile. |
| `budget` | `MaxHosts`, `MaxVUsers`, `MaxMemoryBytes`. The host ceiling cannot exceed the existing operator process limit. |
| `dedicated_memory_bytes` | Positive per-dedicated-JVM reservation charged against the same budget; cannot exceed total memory. |
| `allow_preparation`, `allow_commit`, `allow_invocation` | Explicit stage permissions. Invocation requires commit and preparation; omitted flags are false. |

Nested records retain the Go field names shown in
[pilot configuration types](../pkg/javasidecar/pooled_manifest.go) and
[qualification types](../pkg/javarunner/pooled_qualification.go). Use
`javarunner.InspectPooledQualification` to calculate the runtime/artifact/resource
identity for review. Computing either hash does **not** approve the workload.
The loader consumes previously reviewed evidence; it does not synthesize evidence,
probe application behavior, or approve unknown native/background work.

The file is limited to 1 MiB, regular files, one JSON value, known fields, bounded
nesting, and unique keys including case aliases. Inputs are frozen at load time;
there is no hot reload. Keep the file and assets under operator control on
immutable mounts. Runtime/asset changes fail inspection or subsequent launch
revalidation. The approved DSL hash is included in the runner's frozen decision,
so differing benchmark and runner approvals fail before host reservation.

Budget memory is an admission reservation, not a new per-JVM OS memory limit.
Provision the enclosing cgroup's limits and headroom for the engine, runner,
probes, and JVMs separately, and keep them identical for matched comparisons.
All dedicated and pooled execution uses the shared runner ledger. A manifest
never overrides conflicting script JVM arguments or creates an AppCDS archive.

Build the runner, slot artifacts, and benchmark, then launch the runner in the
exclusive delegated cgroup using the existing authenticated sidecar setup:

```bash
make java-vuser-runner java-vuser-slot-runtime
go build -o bin/apexbench ./cmd/apexbench
APEXLOAD_JAVA_VUSER_PILOT_MANIFEST=/operator/java-pilot.json \
  bin/apexload-java-vuser-runner
```

The variable above supplements existing runner configuration; it does not enable
the runner, set credentials, or provide cgroup delegation. The pooled launch's
`HarnessJar` is `apexload-slot-api.jar` and `ShimJar` is
`apexload-slot-shim.jar`. Keep the ordinary dedicated harness configured for
health probes and baseline execution. Qualification initially requires JDK 17/21.

Run one approved synthetic workload per manifest, keeping the target outside the
accounting cgroup. For example, with the same comparison identities and limits
as the dedicated report:

```bash
bin/apexbench java pilot \
  -pilot-manifest /operator/java-pilot.json -preparation-timeout 2m \
  -script /operator/java-light.json -cgroup "$JAVA_EVIDENCE_CGROUP" \
  -runner-pid "$JAVA_RUNNER_PID" -workload light -profile pooled-pilot \
  -runtime-sha256 "$JAVA_RUNTIME_MANIFEST_SHA256" \
  -environment linux-image-and-hardware-id -resources matched-resource-profile-id \
  -target fixture-target-build-id -vusers 25 -repetitions 5 \
  -warmup 40s -duration 60s -output /tmp/java-light-pooled.json
```

The command explicitly claims the isolated benchmark runner's owner epoch, so
use a runner dedicated to this experiment. Each repetition receives a fresh
execution identity and stages all hosts before duration/ramp. The owner heartbeat
continues between repetitions; it never reclaims after takeover. Group and owner
failures, lost cleanup receipts, and late renewal failures invalidate evidence.
Warm-up begins after all Vusers initialize. Preparation has its own deadline and
is included in sampled startup memory without consuming the measurement window.

The report records pooled mode on the Java evidence schema. Add `--soak 1h` to
collect the observed continuous soak after all five repetitions; without it the
report cannot pass the complete gate. Soak scheduling includes ramp changes,
permanent closure, iterations and initialization/end receipts. Host diagnostics
and supported GC pause events are collected automatically, without retaining raw
host output in the evidence report. Collection alone does not qualify a workload
or establish a memory-saving claim.

## Gate evaluation

```bash
bin/apexbench java compare \
  -baseline /tmp/java-light-best-dedicated.json \
  -candidate /tmp/java-light-pooled.json \
  -output /tmp/java-light-comparison.json
```

The command returns nonzero for failure or inconclusive evidence, after writing
the result. It refuses unmatched workload/runtime/backend/resource/target/load
identities, partial repetitions, and dropped results. Repetition variation is
defined as `(maximum - minimum) / median` for memory, throughput, p95, and p99;
more than 5% makes the comparison inconclusive. These are engineering gate
defaults, not a statistical confidence interval.

Select the best dedicated profile that independently meets the same functional
and performance tests; this command does not select it automatically. Compare
unchanged dedicated execution, workload-specific tuning, and a supported AppCDS
profile. No universal small heap or GC configuration is applied.

The baseline and pilot collectors both support `--soak 1h`. The evaluator requires
actual observed soak evidence; changing a legacy `soak_passed` flag is insufficient.
The evaluator does not authenticate externally supplied reports. Expansion
requires passing comparisons for **both** synthetic fixtures. Production also
requires at least three real workload evaluations, at least two qualifying, and
publication of dedicated fallbacks and exact workload/runtime/resource bounds.

## Operator AppCDS archives

Set `APEXLOAD_JAVA_VUSER_CDS_MANIFEST` only in operator runner configuration. Its
JSON schema is `apexload.java-vuser-cds/v1`, with these fields:

```json
{
  "schema": "apexload.java-vuser-cds/v1",
  "java_version": "exact resolved JDK version",
  "java_sha256": "64 hexadecimal characters",
  "release_sha256": "64 hexadecimal characters",
  "harness_sha256": "64 hexadecimal characters",
  "classpath_sha256": ["one SHA-256 per exact ordered user classpath file"],
  "archive_path": "/operator/readonly/workload.jsa",
  "archive_sha256": "64 hexadecimal characters"
}
```

Generate the archive offline without production secrets, using the exact JDK,
harness, artifacts, and intended launch configuration. Mount the manifest, JDK,
harness, artifacts, and archive immutably; mount the archive read-only and remove
its file write permission bits. File hashes at validation time do not prevent a
later change by an actor able to modify those mounts. Directory classpaths are
not accepted by this opt-in profile. The manifest checks the Java executable and
JDK release metadata; the operator's immutable runtime image must bind the rest
of the JDK. Rebuild the archive whenever its inputs change.

The implementation accepts identified HotSpot runtimes at major 17 or later,
checks the exact recorded version and hashes, and launches with `-Xshare:on`.
Script-owned `-Xshare:*` conflicts fail closed. Existing script restrictions on
`SharedArchiveFile` and archive-generation options remain in force. Unsupported
runtimes, invalid manifests, writable archives, or changed files fail before
user invocation. JVM archive incompatibility fails at startup. Sharing behavior
still needs measurement; archive acceptance is not evidence of a memory benefit.

## Experimental IPC v3 transport

`make java-vuser-slot-runtime` includes `apexload.host.PooledMain` in the separate
slot API artifact. This is an internal transport component, not a new runner
configuration or production admission path. It is not launched by the sidecar.

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
manifest below; distributed assignments remain dedicated. The local backend enforces the selected containment identity
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
dedicated-only behavior. There is no environment switch enabling pooling.
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

## Remaining implementation and release gates

1. **Limited pooled runner:** run the implemented manifest/evidence collector on a
   qualified delegated-Linux deployment; obtain five matched comparisons and an
   actual one-hour soak with ramp changes and permanent closure for both fixtures.
   Automated scheduling, host diagnostics, host GC pause capture, and representative
   loader fixtures are implemented. General distributed integration remains gated
   by measured synthetic success, and arbitrary dependency compatibility still
   requires representative execution and operator approval.
   The slot worker does not prove arbitrary background work stopped and must not
   be treated as a reusable session or process containment mechanism.
2. **Independent AKS dedicated track:** dedicated backend on the lifecycle/reservation interface;
   authenticated TLS facade and namespace-scoped broker; PID 1 supervisor with
   independent lease enforcement; restricted per-JVM execution pods; exact-UID
   termination evidence and durable quarantine; epoch fencing and reconciliation;
   prepare/ready/commit integration and bounded assignment staging; manifests and
   real network/descendant/node-failure tests. Durable dedicated reservation and
   reconciliation primitives are implemented below; no AKS execution backend is enabled.
3. **After the synthetic gate:** normal-engine mode selection, distributed pooled placement and reservations,
   Windows execution/containment validation, qualified AKS pooling, preflight and
   reporting UI/API changes, real-workload qualification, and production release.

The default process limit remains 25. The runner enables pooled slots only through an explicit operator manifest,
never a default or script setting. Distributed generator pooled execution must
remain unavailable until its end-to-end ownership, protocol, containment, and
evidence paths are implemented and tested.
Failure of its gate must not cancel the independent AKS dedicated track.

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

Add `--soak 1h` to both matched baseline and pilot commands above. The collector
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
builder is not wired into a runnable broker deployment: generator-facing facade,
broker service orchestration, infrastructure
fencing/termination attestation, Helm/Kustomize deployment wiring and assignment
staging remain implementation work. No AKS backend is enabled by these utilities.

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
