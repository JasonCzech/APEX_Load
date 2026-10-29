# Java Shared JVMs, Pooled Runtime Contracts, and Pilot Operations

Status: Engineering reference. Dedicated one-JVM-per-VUser execution remains the default `java_vuser` runtime path. Runner-qualified shared JVMs (`config.java_vuser.jvm_sharing: shared` and LoadRunner thread mode `jvm_sharing: threads`) are available for ordinary engine and distributed generator runs on delegated-cgroup-v2 Linux runners when explicitly enabled by the operator. Profile-pinned pooled pilot runs (`APEXLOAD_JAVA_VUSER_PILOT_MANIFEST`), `apexbench java` evidence gates, and operator AppCDS archives (`APEXLOAD_JAVA_VUSER_CDS_MANIFEST`) remain explicit opt-in workflows. Dedicated Kubernetes execution-pod brokerage is documented separately in [JAVA_DEDICATED_BROKER_EXPERIMENTAL.md](JAVA_DEDICATED_BROKER_EXPERIMENTAL.md); dated September verification notes are archived in [archive/JAVA_VALIDATION_2026-09.md](archive/JAVA_VALIDATION_2026-09.md).

## Contents

- [Shared JVMs (runner-qualified)](#shared-jvms-runner-qualified)
- [LoadRunner thread mode](#loadrunner-thread-mode)
- [Shared JVMs across generators](#shared-jvms-across-generators)
- [Shared JVM benchmark (2026-10-15)](#shared-jvm-benchmark-2026-10-15)
- [Pooled runtime contracts](#pooled-runtime-contracts)
  - [Experimental IPC v3 transport](#experimental-ipc-v3-transport)
  - [Supervisor and local containment adapter](#supervisor-and-local-containment-adapter)
  - [Qualification and runtime hashing rules](#qualification-and-runtime-hashing-rules)
  - [Shared runner admission](#shared-runner-admission)
  - [Pooled run ownership](#pooled-run-ownership)
  - [Versioned pooled lifecycle control](#versioned-pooled-lifecycle-control)
  - [Invocation and event capture](#invocation-and-event-capture)
  - [Managed run and slot client](#managed-run-and-slot-client)
  - [Pooled protocol and Vuser results](#pooled-protocol-and-vuser-results)
  - [Programmatic engine staging pilot](#programmatic-engine-staging-pilot)
- [Pilot operations and AppCDS](#pilot-operations-and-appcds)
  - [Building and checking fixtures](#building-and-checking-fixtures)
  - [Collecting a dedicated baseline](#collecting-a-dedicated-baseline)
  - [Explicit local pooled pilot](#explicit-local-pooled-pilot)
  - [Gate evaluation](#gate-evaluation)
  - [Operator AppCDS archives](#operator-appcds-archives)

---

## Shared JVMs (runner-qualified)

The operator shared-JVM policy enables runner-qualified JVM sharing for ordinary engine and distributed runs without a hand-authored pilot manifest. Set `APEXLOAD_JAVA_VUSER_SHARED_JVM=true` on a Linux `apexload-java-vuser-runner` sidecar together with existing runner enablement and authentication. The runner builds a single admission authority (`javarunner.DedicatedHostAdmission`) charged by both shared and dedicated JVMs:

| Setting | Default | Meaning |
| --- | --- | --- |
| `APEXLOAD_JAVA_VUSER_SHARED_JVM_VUSERS_PER_JVM` | `250` | Lifetime slots per shared JVM (`2`–`1,024`). A script may request fewer via `config.java_vuser.vusers_per_jvm`. |
| `APEXLOAD_JAVA_VUSER_SHARED_JVM_HEAP_MB` | `1024` | `-Xmx` of each shared JVM (MiB). |
| `APEXLOAD_JAVA_VUSER_JVM_MEMORY_BUDGET_MB` | `max_processes × dedicated_jvm_memory_mb`, capped at container cgroup limit minus `256 MiB` | Runner-wide JVM memory budget. An explicit positive value is used as given. |
| `APEXLOAD_JAVA_VUSER_DEDICATED_JVM_MEMORY_MB` | `256` | Budget charge of one dedicated JVM (MiB). |
| `APEXLOAD_JAVA_VUSER_SLOT_API_JAR`, `APEXLOAD_JAVA_VUSER_SLOT_SHIM_JAR` | Beside `APEXLOAD_JAVA_VUSER_HARNESS_JAR` | Slot runtime artifacts built by `make java-vuser-slot-runtime` (`apexload-slot-api.jar` and `apexload-slot-shim.jar`); included in `runners/java_vuser/Dockerfile`. |

`APEXLOAD_JAVA_VUSER_MAX_PROCESSES` (default `25`) caps total JVMs per runner across shared and dedicated hosts alike. Startup fails closed when the policy is invalid (including a container too small for one shared JVM), when the OS is not Linux, or when `APEXLOAD_JAVA_VUSER_PILOT_MANIFEST` is also set.

Shared JVMs require delegated Linux cgroup v2 process containment (`delegated_linux`), matching persistent dedicated Java lifecycles. Stock Docker Compose services and unprivileged Helm sidecars without delegated cgroup v2 do not advertise shared JVMs even when the policy flag is set, so groups fall back to one dedicated JVM per VUser. On a governed Linux runner with delegated cgroup v2, configure the policy through the environment variables above or Helm's `javaVuser.runner.sharedJVM` values. Pooled hosts run the sidecar's credential guard, which permits the runner's loopback token and Kubernetes service-link variables while allowlisting the host JVM environment.

Scripts opt in by setting `config.java_vuser.jvm_sharing` to `"shared"` (or `"threads"` for LoadRunner thread mode) and optionally lowering `config.java_vuser.vusers_per_jvm`. Only closed workloads with sequential run logic, string parameters, non-`at_once` initialization, at least one staged VUser, and one exact `init`/`action`/`end` lifecycle on a single target class are eligible; scripts outside that shape fall back to dedicated JVMs with a stable reason (`shared_jvm_open_workload_unsupported`, `shared_jvm_run_logic_unsupported`, `shared_jvm_parameters_unsupported`, `shared_jvm_init_at_once_unsupported`, `shared_jvm_no_staged_vusers`, or `shared_jvm_lifecycle_unsupported`).

Eligible runs open with the `claim_shared` operation on `POST /v1/pooled-control`. It carries the dedicated-mode runtime settings (JDK selection, VM parameters, classpath, and uploaded classpath JARs), target class, script digest, optional `vusers_per_jvm`, and `loadrunner_threads` flag. The runner resolves runtime settings using dedicated-mode validation and qualifies the workload:

- **Runtime**: HotSpot or OpenJ9 17 or 21 in standard `shared` mode.
- **Class loader split**: Every script classpath entry must be a JAR file. Under the default `shared-auto-target-private` profile, the JAR containing the target class is private to each VUser slot so the `Actions` class and its static fields remain isolated per VUser, while remaining script and uploaded JARs load once in the shared parent loader. When the script requests dedicated per-VUser class loading (`shared-auto-dedicated-loader`), every script JAR loads privately per VUser.
- **Static native-code scan**: Application JARs must pass a complete bounded static scan with zero native indicators: `ACC_NATIVE` methods, `System.load`/`loadLibrary` or `Runtime.load`/`loadLibrary` calls, JNA/JNR/`java.lang.foreign` references, or packaged native libraries (`.so`, `.dll`, `.dylib`, `.jnilib`). Methods merely named `load` (such as `Properties.load`, `KeyStore.load`, or `ServiceLoader.load`) are not flagged. Static scanning cannot prove the absence of reflective/dynamic native loading or unattributed background threads; enabling `APEXLOAD_JAVA_VUSER_SHARED_JVM` represents operator acceptance of that boundary.
- **Canonicalized JDK tree hashing**: The resolved JDK installation is hashed file-by-file under the symlink containment rules in [Qualification and runtime hashing rules](#qualification-and-runtime-hashing-rules).
- **VM heap arguments**: Script-level heap flags (`-Xmx`, `-Xms`, `-XX:MaxRAM*`, and equivalents) written for one-JVM-per-VUser sizing are stripped and surfaced as warning `shared_jvm_script_heap_ignored`; the operator's `APEXLOAD_JAVA_VUSER_SHARED_JVM_HEAP_MB` applies. Other validated VM arguments pass through unchanged.

Refusals return a closed error vocabulary (`shared_jvm_disabled`, `shared_jvm_runtime_unsupported`, `shared_jvm_classpath_unsupported`, `shared_jvm_native_code_detected`, `shared_jvm_inspection_incomplete`, `shared_jvm_inputs_unsupported`, `shared_jvm_capacity_unavailable`, `shared_jvm_loadrunner_threads_disabled`, `shared_jvm_vm_parameter_invalid`, `shared_jvm_runtime_rejected`, `shared_jvm_qualification_failed`, or an allowed dedicated-mode runtime code) and leave the group eligible for dedicated fallback. Each JVM launch re-verifies the frozen qualification digest. Healthy shared runners advertise `java_vuser_shared_jvm_v1` and a `shared_jvm` capacity envelope (`vusers_per_jvm`, `jvm_memory_bytes`, `max_jvms`, `max_vusers`, `max_memory_bytes`, `available_vusers`) on `GET /v1/capabilities`.

## LoadRunner thread mode

Classic LoadRunner executes Java Vuser groups as threads inside a single JVM sharing one class loader (`UseThreads=1`, `enable_CL_per_vuser=0`). Legacy enterprise workloads built for that model—such as WebLogic 9.2 clients or vendor SDKs bundling Java 8 bytecode, native libraries, large client archives (>16,384 ZIP entries), and manifest `Class-Path` headers—do not satisfy standard `jvm_sharing: shared` rules. LoadRunner thread mode (`config.java_vuser.jvm_sharing: "threads"`) supports those workloads on the same pooled host infrastructure:

- **Operator gate**: Set `APEXLOAD_JAVA_VUSER_THREAD_MODE=true` alongside `APEXLOAD_JAVA_VUSER_SHARED_JVM=true` (`javaVuser.runner.sharedJVM.threadMode` in Helm). Healthy runners advertise `java_vuser_loadrunner_threads_v1`. Claims against a runner without thread mode return `shared_jvm_loadrunner_threads_disabled` and fall back to dedicated JVMs. Overriding a script into thread mode at scenario level requires re-approving the script.
- **Loader partitioning (`shared-auto-lr-threads`)**: Only the JAR containing the target class and any JAR whose `.class` constant pools reference `lrapi/` load per VUser (so each VUser slot still owns an isolated `lrapi.lr` instance), unless dedicated per-VUser class loading is explicitly configured on the script. All other JARs are treated as shared library JARs loaded once per JVM.
- **Per-VUser vs. shared library JAR rules**: Per-VUser JARs retain full standard checks (16,384-entry ZIP bound, no `META-INF/INDEX.LIST`, no manifest `Class-Path`, and complete native scan with zero indicators). Shared library JARs skip the native-code scan, allow up to 65,535 ZIP entries (`threadLibraryJarLimits`), still reject `META-INF/INDEX.LIST`, and accept relative/schemeless manifest `Class-Path` entries when none of the referenced paths exist on disk relative to the JAR (`classPathReferenceAbsent`), ensuring only the explicitly declared and hashed JARs load.
- **Java 8+ support**: Any HotSpot or OpenJ9 runtime resolved by the runner qualifies, including Java 8 when `APEXLOAD_JAVA_VUSER_ALLOW_LEGACY_JAVA` is enabled. When a Java 8 runtime reports `java.home` as `<jdk>/jre`, runtime hashing automatically includes the `<jdk>/bin/java` launcher and `<jdk>/release` file one level up (`qualificationRuntimeRoot`).
- **Exception containment**: In `loadrunner_threads` mode, an uncaught script exception fails only that VUser's call (returning code `-1` with a single-line exception class/message summary capped at 2,048 characters) instead of reaping the shared JVM. A failed `init` terminates that VUser slot without invoking `end`; an `action` exception fails the current iteration while keeping the slot usable.

Measured on Docker Desktop (Apple silicon, Temurin 8u504, 4 CPUs, 512 MiB heap per shared JVM) with a synthetic script loading 400 classes across seven WebLogic 9.2 client JARs and idling 500 ms per iteration:

| VUsers | Mode | JVMs | JVM memory (PSS) | Errors |
| --- | --- | --- | --- | --- |
| 10 | Dedicated (one JVM per VUser) | 10 | 1,260 MB | 0 |
| 10 | LoadRunner threads (`threads`) | 1 | 297 MB | 0 |
| 100 | LoadRunner threads (`threads`) | 1 | 486 MB | 0 |

## Shared JVMs across generators

Normal engine factories (`engine.New`) and distributed gRPC generators stage and execute shared-JVM groups automatically when both the script (`jvm_sharing: shared` or `threads`) and the generator's runner sidecar (`java_vuser_shared_jvm_v1`) opt in:

- **Heartbeat capacity reporting**: Every generator heartbeat reports `shared_jvm` capacity (`vusers_per_jvm`, `max_vusers`, `available_vusers`) alongside `dedicated_capacity`. Available shared VUsers are computed as `min(runner_live_available, runner_max_vusers - generator_admitted_shared_vusers)`, preventing stale heartbeat snapshots from over-admitting. Because dedicated and shared JVMs charge the same runner process limit and memory budget, generators advertise dedicated Java capacity as `min(dedicated_capacity, max_processes)`.
- **Controller placement and cold staging**: For eligible shared groups, the controller places VUsers against the `shared_jvm` slot envelope (falling back to dedicated capacity on generators without `shared_jvm`). During `AssignWork` staging, the generator reserves, prepares, and readies all required shared JVMs within the 5-minute preparation budget (`engine.JavaSharedPrepareTimeout = 5 * time.Minute`) while keeping sibling staged groups alive, so `CommitWork` never blocks on cold JVM startup. Rollback or staging expiry closes staged JVMs cleanly.
- **Chunking above 4,096 VUsers**: One `/v1/pooled-control` run holds at most 4,096 VUsers (and 1,024 JVMs). When a single generator hosts more than 4,096 VUsers in one group, the engine splits the group into multiple coordinated pooled runs of whole JVMs that are claimed, prepared, committed, and closed together.
- **Fixed staged ceiling and dedicated fallback**: Shared groups cannot scale dynamically beyond their staged VUser count (`java_shared_jvm_additional_slots_required`). If `claim_shared` refuses qualification during staging, the group falls back to dedicated JVMs on that generator provided dedicated capacity suffices (otherwise staging fails closed with a capacity error). With 250 VUsers/JVM and 25 JVMs/runner, a single generator can host up to 6,250 shared Java VUsers within its memory budget.

## Shared JVM benchmark (2026-10-15)

`scripts/java-shared-jvm-bench/run.sh` drives the middleware-heavy fixture (`HeavyActions`: Apache HttpClient, Jackson, and Guava performing a GET and POST per transaction) through `pkg/engine` against a Linux runner container (Docker Desktop on Apple silicon, Temurin 21, 6 CPUs, 6.5 GB container limit, up to 1,024 VUsers and 320 MB heap per shared JVM, target service co-located in the container):

| 25 VUsers, 60 s, 10 s warm-up | Dedicated (1 JVM / VUser) | Shared JVMs |
| --- | --- | --- |
| Paced (target 1,000 tx/s): achieved | 786–927 tx/s | 980–985 tx/s |
| Paced: p50 / p90 / p99 | 5.0–5.7 / 9.0–12.7 / 12.8–51 ms | 2.0 / 6.2 / 9.4 ms |
| Paced: CPU | 1.9–3.0 cores | 1.0 core |
| JVM memory (PSS) | 3.8–4.8 GB (25 JVMs) | 0.30–0.35 GB (1 JVM) |
| All VUsers initialized | 4–13 s | 0.7–1.0 s |
| Closed loop: throughput | 0.9k–4.6k tx/s | 3.8k–8.9k tx/s |

At higher concurrency on a single runner, 5,000 VUsers paced at 1 iteration per 5 s with a 60 s ramp executed inside 5 shared JVMs: 421,758 transactions, 0 errors, steady-state p50 / p95 / p99 of 2.0 / 6.2 / 14 ms, 3.1 GB JVM PSS (4.2 GB container total), and 2.7 CPU cores. The standalone bench binary also accepts `-script <file>` (bare or `{"script": ...}` envelope) with optional `-classpath`, `-vm-args`, `-pace-ms`, `-sharing`, and `-vusers-per-jvm` overrides.

---

## Pooled runtime contracts

Both runner-qualified shared JVMs (`claim_shared`) and profile-pinned pilot runs (`claim`) execute on the same internal Go/Java pooled runtime stack (`pkg/javarunner`, `pkg/javasidecar`, `pkg/protocol/java_vuser`, and `runners/java_vuser/apexload/`).

### Experimental IPC v3 transport

`make java-vuser-slot-runtime` compiles `apexload.host.PooledMain` into `apexload-slot-api.jar` alongside `apexload-slot-shim.jar`.

- **Bootstrap secret and duplex loopback socket**: The supervisor delivers a fresh 32-byte secret over a bootstrap `stdin` pipe and closes the pipe before user code can run (never passing secrets via CLI arguments, environment variables, or script context). Java-visible `System.in` and file descriptor `0` observe immediate EOF. The JVM connects to the supervisor's IPv4 loopback listener (`TCP_NODELAY`) and authenticates with a 32-byte HMAC-SHA256 over `apexload.java_vuser.ipc.v3.host_auth:<generation>`. Raw `stdout` and `stderr` stay separate from the control socket and drain into bounded host diagnostic buffers (32 KiB Java stream buffer plus 256 KiB native stdout/stderr buffers per stream with dropped-byte counters), preventing native or `System.out` writes from corrupting binary frames.
- **Frame layout and bounds**: Each frame consists of a big-endian `uint32` length (max `1 MiB`), payload bytes, and a 32-byte HMAC-SHA256 covering a direction byte (`0` = request, `1` = response) plus payload. Payload fields are length-prefixed strict UTF-8 strings (`apexload.java_vuser.ipc.v3` contract, generation, slot ID, decimal request ID, operation), a strictly increasing positive signed 64-bit host sequence number matching the request ID, and up to 1,024 key/value fields (keys/identities ≤ 128 bytes, values ≤ 64 KiB).
- **Scaled non-blocking queues**: Transport queues scale with the host's slot ceiling (Go: `max(64, 2×slots+64)` queued frames and `max(512, 4×slots+64)` outstanding requests; Java: `max(512, 4×slots+64)` queued replies) so a 1,024-slot burst cannot overflow transport queues. Outstanding Go requests retain correlation identities rather than parameter payloads. Queue overflow, malformed frames, or control uncertainty retire the host and surface `pooled_host_reaped` to peers. Ordinary slot cancellation allows a 2-second cooperative interrupt grace before requesting containment reaping.

### Supervisor and local containment adapter

`javarunner.HostSupervisor` pairs one execution/group ledger with `LocalHostBackend` and delegated Linux cgroup v2 process-tree containment:

- **Provisioning and commit barrier**: Provisioning intents are recorded in the ledger before process creation, and exact returned containment identities are verified before readiness, before termination, and after termination. Preparation runs under the ledger deadline with an independent 25 ms expiry monitor. Commit first opens every prepared JVM's invocation gate while ledger invocation admission stays closed, committing atomically only after every host acknowledges. No user class constructor, static initializer, or lifecycle method runs during `open` or `commit`.
- **Containment-verified charge release**: Closing the first slot seals the host against future admissions and releases only that slot's VUser occupancy while preserving surviving slots. Draining and failed hosts retain their process and full memory reservation charges until the containment backend positively confirms termination of both the root JVM and all descendants; JVM exit alone never releases capacity, and uncertain cleanup transitions the host to permanent quarantine.

### Qualification and runtime hashing rules

`javarunner.InspectPooledQualification` computes a deterministic SHA-256 over `apexload.java-vuser-pooled-qualification/v1`, hashing the launch configuration, resource components, runtime files, `HarnessJar`, `ShimJar`, source archive, private `ScriptClasspath`, and shared `SharedClasspath`:

- **Canonical containment paths**: Top-level `WorkDir`, `JDK.Home`, and `JDK.JavaBin` paths must be clean, canonical, symlink-free absolute UTF-8 paths (`filepath.EvalSymlinks(path) == path`), and both `JavaBin` and `<runtime-root>/release` must be regular files. For Java 8 installations where `JDK.Home` is `<jdk>/jre`, `<runtime-root>` resolves to `<jdk>` so `<jdk>/bin/java` and `<jdk>/release` are hashed into the runtime identity.
- **Permitted JDK symlinks**: Walking `JDK.Home` bounds entries to 32,768 files and 4 GiB total (`2 GiB` per file). Symlinks inside `JDK.Home` are admitted when both their immediate link hop and their fully resolved target remain inside `JDK.Home` at a regular file, a directory, or `JDK.Home` itself (covering standard JDK module legal notices linked to `java.base`, Alpine `jre -> .` links, and JDK man-page locale links); the link target is recorded in the identity while the in-home target file is hashed at its own path. Exactly one symlink may resolve outside `JDK.Home`: `lib/security/cacerts`, whose resolved system CA bundle content is hashed directly into the runtime identity. Symlinks that bounce outside `JDK.Home` and back, broken links, special files/FIFOs, or any other external symlinks are rejected.
- **JAR archive and manifest `Class-Path` bounds**: Every classpath JAR is validated with `zipbounds` before opening. Standard pooled JARs (`validateQualificationJar`, used for all JARs in `jvm_sharing: shared` and pilot mode, and for per-VUser JARs in `jvm_sharing: threads`) cap archives at 2 GiB and 16,384 entries, require at most one `META-INF/MANIFEST.MF` (≤ 64 KiB), and reject `META-INF/INDEX.LIST` as well as any `Class-Path` manifest attribute in any section. In LoadRunner thread mode (`jvm_sharing: threads`), shared library JARs (`validateThreadLibraryJar`) raise the entry ceiling to 65,535 entries, still reject `META-INF/INDEX.LIST`, and admit relative/schemeless manifest `Class-Path` entries provided none of the referenced paths exist on disk relative to the JAR, guaranteeing that `URLClassLoader` cannot silently load unhashed sibling archives.

### Shared runner admission

`HostCapacity` and `DedicatedHostAdmission` enforce a single runner-wide host, VUser, and memory budget across concurrent pooled runs and dedicated sessions. Whole reservation batches are admitted atomically. Uncertain dedicated or pooled cleanup permanently closes shared admission, wakes blocked process/probe waiters, and retains quarantined charges so an owner-epoch change cannot erase leaked process memory. The dedicated health-probe lane remains isolated so full session pools do not block readiness probes.

### Pooled run ownership

Each pooled run binds to the generator's claimed owner epoch and maintains an independent 5-second renewable group lease (`Server.OpenPooledRun`). Every control operation verifies owner fencing; expired or closed runs cannot be renewed, and each `(execution_id, group_id)` pair can be opened at most once per owner epoch (with up to 1,024 tombstones retained to prevent replay). Owner takeover or owner lease expiry fences active pooled runs and initiates containment cleanup within the runner's cleanup deadline (2 seconds by default).

### Versioned pooled lifecycle control

`POST /v1/pooled-control` requires runner authentication plus contract `apexload-java-vuser-pooled-control.v1`:

- **Limits and descriptors**: A runner admits up to 64 concurrent runs per owner, each capped at 4,096 VUsers and 1,024 JVMs, and caps open per-slot event-capture file descriptors at `(RLIMIT_NOFILE / 2) - 1024` (failing early with `pooled_capacity_unavailable` before creating files or JVMs).
- **Operations and stage capabilities**: Profile-based pilot manifests gate `claim`, `renew`, `reserve`, `abort`, and `close` (`java_vuser_pooled_reservations_v1`), plus opt-in `prepare` (`AllowPreparation` → `java_vuser_pooled_preparation_v1`), `commit`/`close_slot` (`AllowCommit` → `java_vuser_pooled_commit_v1`), `invoke` (`AllowInvocation` → `java_vuser_pooled_invocation_v1`), and `diagnostics` (`java_vuser_pooled_diagnostics_v1`). Runner-qualified `claim_shared` (`APEXLOAD_JAVA_VUSER_SHARED_JVM=true`) enables the full lifecycle for qualified workloads under `java_vuser_shared_jvm_v1`.
- **Idempotency and replay cache**: Control mutations cache up to 64 request IDs per run (plus one permanent `close_slot` record per prepared slot) without resetting original absolute preparation deadlines; `renew`, `abort`, and `close` remain non-blocking even when the cache is full. Ambiguous transport outcomes (`PooledControlOutcomeAmbiguous`) are never automatically retried or re-claimed under a fresh request ID.

### Invocation and event capture

- **At-most-once slot sequencing**: Each committed slot requires sequence `1` for `init`, followed by strictly increasing sequences for `action` calls and one `end`, using reserved request IDs `invoke-<slot_id>-<sequence>`. Concurrent calls on the same slot return `pooled_slot_busy`.
- **Pinned descriptor ingestion**: During `prepare`, the runner creates a private `0600` event file per slot inside an absolute runner-owned temp directory (never accepting caller-supplied paths) and retains the open file descriptor. Before each `invoke`, the runner verifies `os.SameFile` identity and truncates the descriptor; after completion, it re-verifies identity and reads at most `256 KiB + 1` detection byte of UTF-8 JSON lines matching the exact IPC request ID and monotonically increasing event sequences. Capture files are deleted only after positive host reap or no-start ledger confirmation.
- **Thread-attributed `lr` shim (`slot_thread_v1`)**: The private slot shim attributes `lr` transactions and messages only when called on the active slot lifecycle thread during an invocation. Child-thread, shared-executor, or late events are routed to bounded host diagnostics with an empty request ID and mark a sticky attribution violation that fails the active or next invocation and triggers host reaping (except for user exceptions handled under `loadrunner_threads`).

### Managed run and slot client

`Client.OpenPooledClientRun` freezes the claimed owner epoch and negotiated control decision, checks decision identity on every response including cleanup, and runs an independent group-lease renewal loop alongside `PooledClientRun.RenewOwner` (`POST /v1/owner/renew`). `Prepare` stages the full peak once and `Commit` opens the invocation barrier before VUser loops start.

### Pooled protocol and Vuser results

`java_vuser.NewPooledSlotProtocol` adapts a committed managed slot to the engine's streaming `protocol.Protocol` contract for exact `init`, `action`, and `end` methods with merged string `parameters` and `lr_parameters` (`lr_parameters` overriding `parameters`, matching dedicated `Harness.java`). Transaction and parent IDs are namespaced by slot and IPC request ID, malformed event timings fail closed, and iteration/VUser exit semantics (`lr.EXIT_*`) derive strictly from the authenticated invocation frame via an in-process lifecycle-control receipt rather than untrusted event text.

### Programmatic engine staging pilot

While ordinary shared runs flow through `engine.New` and distributed generators via `claim_shared`, `engine.NewJavaPooledPilot` provides a separate single-use, single-group engine constructor for `apexbench java pilot` against a profile-pinned `APEXLOAD_JAVA_VUSER_PILOT_MANIFEST`. It requires a pre-claimed sidecar client, frozen `PooledControlDecision`, and exact `engine.JavaPooledScriptSHA256`, with no dedicated fallback.

---

## Pilot operations and AppCDS

The `apexbench java` workflow measures matched dedicated baselines against profile-pinned pooled pilot runs on delegated-cgroup-v2 Linux hosts and manages opt-in operator AppCDS archives. Local benchmark runs do not qualify AKS pod containment.

### Building and checking fixtures

Requirements: Go toolchain, JDK supporting `javac --release 8` and `--release 17`, and Maven.

```bash
bash scripts/build-java-vuser-evidence.sh
go build -o bin/apexbench ./cmd/apexbench
make java-vuser-slot-runtime java-vuser-runner-legacy

APEXLOAD_JAVA_EVIDENCE_FIXTURE_DIR="$PWD/bin/java_vuser/evidence" \
  go test ./runners/java_vuser -run TestJavaEvidenceFixturesThrough -count=1
```

Start the loopback fixture target **outside the accounting cgroup**, then generate the `light` and `middleware-heavy` persistent-lifecycle DSL scripts with ordered classpath entries:

```bash
bin/apexbench java target -addr 127.0.0.1:18099

fixture_args=(-classpath "$PWD/bin/java_vuser/evidence/fixtures.jar")
for dependency in "$PWD"/bin/java_vuser/evidence/lib/*.jar; do
  fixture_args+=(-classpath "$dependency")
done
bin/apexbench java fixture -workload light "${fixture_args[@]}" -output /tmp/java-light.json
bin/apexbench java fixture -workload middleware-heavy "${fixture_args[@]}" -output /tmp/java-heavy.json
```

Both fixtures emit one `order-flow` transaction per iteration with 500 ms fixed-interval pacing (`HeavyActions` exercises Apache HttpClient, Jackson, and Guava). To inspect static native-code indicators in candidate JARs or source archives offline without loading classes, run:

```bash
bin/apexbench java inspect \
  --artifact /approved/application.jar \
  --artifact /approved/source.zip \
  --output native-inspection.json
```

`apexbench java inspect` enforces a 64 MiB scan budget (including nested archives up to depth 3, 16,384 entries, and 4,096 findings) and emits `incomplete` rather than clean status when any limit is exceeded.

### Collecting a dedicated baseline

Run `apexbench`, `apexload-java-vuser-runner`, and all Java descendants inside one **exclusive delegated Linux cgroup v2 subtree** (keeping the HTTP target and unrelated processes outside it):

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

The collector samples every 250 ms, deduplicating PIDs across the cgroup subtree and summing process PSS (avoiding shared-page RSS double counting) while tracking cgroup peak memory and OOM/memory-limit counters separately. Warm-up begins only after every VUser finishes `init`. Throughput counts business transactions completed inside the measurement window, whereas latency (`latency_samples`) follows transactions started in the window through graceful drain so slow tails are never clipped. Raw `memory_bytes` figures represent **total resident PSS** across the accounting subtree.

### Explicit local pooled pilot

For controlled baseline-vs-pilot evidence gates, start a dedicated Linux runner with `APEXLOAD_JAVA_VUSER_PILOT_MANIFEST` (mutually exclusive with `APEXLOAD_JAVA_VUSER_SHARED_JVM`). The manifest file (max `1 MiB`, strict JSON, frozen at startup) uses schema `apexload.java-vuser-pilot-runner/v1`:

| Field | Required operator input |
| --- | --- |
| `schema` | `"apexload.java-vuser-pilot-runner/v1"` |
| `qualification` | `javarunner.PooledQualificationProfile`: `Schema`, `ID`, `ConfigurationSHA256` (from `javarunner.InspectPooledQualification`), and `Evidence` (four operator attestation digests, `ApprovedBy`, and explicit no-application-native-code assessment). |
| `request` | `javarunner.PooledQualificationRequest`: `Launch` (`HarnessJar: apexload-slot-api.jar`, `ShimJar: apexload-slot-shim.jar`, canonical JDK 17/21 record, ordered VM arguments, private/shared classpaths), `SourceArchive`, `SharingProfile`, and `Resources`. |
| `script_sha256` | `engine.JavaPooledScriptSHA256` of the approved DSL script. |
| `budget` | `MaxHosts` (≤ `APEXLOAD_JAVA_VUSER_MAX_PROCESSES`), `MaxVUsers`, and `MaxMemoryBytes`. |
| `dedicated_memory_bytes` | Positive per-dedicated-JVM reservation charged against the shared budget. |
| `allow_preparation`, `allow_commit`, `allow_invocation` | Explicit stage booleans (`allow_invocation` requires `allow_commit` and `allow_preparation`). |

Launch the isolated runner and run the pilot collector (adding `--soak 1h`, up to `2h`, on both baseline and pilot runs when collecting full release-gate soak evidence with staged quarter/half ramp cohorts, three-quarter odd-VUser permanent closure, and JFR `jdk.GCPhasePause` host diagnostics):

```bash
make java-vuser-runner java-vuser-slot-runtime
APEXLOAD_JAVA_VUSER_PILOT_MANIFEST=/operator/java-pilot.json \
  bin/apexload-java-vuser-runner

bin/apexbench java pilot \
  -pilot-manifest /operator/java-pilot.json -preparation-timeout 2m \
  -script /operator/java-light.json -cgroup "$JAVA_EVIDENCE_CGROUP" \
  -runner-pid "$JAVA_RUNNER_PID" -workload light -profile pooled-pilot \
  -runtime-sha256 "$JAVA_RUNTIME_MANIFEST_SHA256" \
  -environment linux-image-and-hardware-id -resources matched-resource-profile-id \
  -target fixture-target-build-id -vusers 25 -repetitions 5 \
  -warmup 40s -duration 60s --soak 1h -output /tmp/java-light-pooled.json
```

### Gate evaluation

```bash
bin/apexbench java compare \
  -baseline /tmp/java-light-best-dedicated.json \
  -candidate /tmp/java-light-pooled.json \
  -output /tmp/java-light-comparison.json
```

`apexbench java compare` writes the comparison report and exits nonzero on failure or inconclusive variation. Passing requires matched workload/runtime/environment/resource/target/load identities, 5 complete repetitions with `(max - min) / median <= 5%` across memory, throughput, p95, and p99, at least a 30% steady-state PSS reduction against the best tuned dedicated profile, ≤ 5% throughput/latency regression, zero errors or dropped samples, and matching observed 1-hour continuous soak schedules.

### Operator AppCDS archives

Dedicated HotSpot 17+ sessions can attach a read-only, hash-verified AppCDS archive by setting `APEXLOAD_JAVA_VUSER_CDS_MANIFEST` in operator runner configuration (`apexload.java-vuser-cds/v1`):

```json
{
  "schema": "apexload.java-vuser-cds/v1",
  "java_version": "exact resolved JDK version",
  "java_sha256": "64 lowercase hex characters",
  "release_sha256": "64 lowercase hex characters",
  "harness_sha256": "64 lowercase hex characters",
  "classpath_sha256": ["one SHA-256 per exact ordered user classpath JAR"],
  "archive_path": "/operator/readonly/workload.jsa",
  "archive_sha256": "64 lowercase hex characters"
}
```

Generate the `.jsa` archive offline without production secrets using the exact JDK, harness, and ordered JAR classpath (directory classpaths are rejected). Mount the manifest, JDK, harness, JARs, and `.jsa` archive on immutable read-only mounts and strip write permission bits from `archive_path`. The validator verifies regular-file types before opening (preventing FIFO hangs), checks the resolved HotSpot 17+ version and all SHA-256 digests, rejects script-supplied `-Xshare:*` or `SharedArchiveFile` overrides, and launches with `-Xshare:on`.
