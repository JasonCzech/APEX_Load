# Shared Java JVMs

Status: Operator-enabled runtime contracts and scoped benchmark evidence.


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
