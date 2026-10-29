# Java Vuser runner harness, shims and container images

`runners/java_vuser/` contains the Java runtime harness, LoadRunner `lrapi.lr` compatibility shim, shared-JVM slot runtime packages, qualification fixtures and container build definitions used by the `java_vuser` protocol (`cmd/apexload-java-vuser-runner` and the experimental dedicated host-broker stack).

For runtime architecture, qualification rules and pilot operations, see:
- [Java shared JVMs and pilot runtime](../../docs/JAVA_SHARED_AND_PILOT_RUNTIME.md) — runner-qualified shared JVMs (`claim_shared`), LoadRunner Thread Mode (`jvm_sharing: threads`), IPC v3, `HostSupervisor` containment, AppCDS and local pilot harnesses.
- [Java dedicated broker (experimental)](../../docs/JAVA_DEDICATED_BROKER_EXPERIMENTAL.md) — experimental mTLS pod broker, loopback generator facade, PID 1 execution-pod supervisor and external boundary attestation contract.

## Source layout

### `Harness.java`

`Harness` is the entry point packaged into `java-vuser-runner.jar` (`--main-class Harness`, compiled targeting Java 17):
- **`--health` capability probe:** prints the `APEXLOAD_JAVA_VUSER_V2` capabilities frame (`contract: "apexload.java_vuser.ipc.v2"`, `event_contract: "apexload.java_vuser.lrapi_events.v2"`) advertising `java_vuser_phase_lifecycle_v1`, `java_vuser_persistent_lifecycle_v1`, `java_vuser_session_frame_token_v1`, `java_vuser_validation_only_v1`, `java_vuser_dedicated_classloader_v1` and, when native data classes are present, `native_data_local_v1`.
- **Execution modes:** runs either persistent framed stdin/stdout phase sessions (`--session` for `init`, `action`, `end` and validation-only checks authenticated with per-session HMAC frame tokens) or single-invocation one-shot commands.
- **Diagnostic bounds:** enforces `MAX_DIAGNOSTIC_BYTES` (128 KiB), `MAX_DIAGNOSTIC_FILE_BYTES` (256 KiB), `MAX_ERROR_BYTES` (64 KiB) with `...truncated` markers, and a 2,000 ms exit-halt grace window.

### `lrapi/`

LoadRunner Java Vuser API compatibility layer compiled into both `java-vuser-runner.jar` (dedicated mode) and `apexload-slot-shim.jar` (`--release 8`, shared-JVM slot mode):
- `lrapi/lr.java` — implements the `lrapi.lr` static surface used by imported VuGen Java Vuser scripts (transactions, status constants `PASS`/`FAIL`/`AUTO`, think time, parameter evaluation/saving, message/log emission and rendezvous/pacing hooks) and emits structured `apexload.java_vuser.lrapi_events.v2` events.
- `lrapi/SlotBridge.java` — bridges static `lrapi.lr` calls inside a shared JVM to the active slot thread's isolated `SlotRuntime` context so concurrent VUsers in one JVM never cross-contaminate parameters, transactions or log streams.

### `apexload/`

Internal runtime support packages split across three subpackages:
- `apexload/data/` (`Data.java`, `NativeJson.java`) — dependency-free JSON encoding/decoding and native parameter/table data bindings exposed to Java Vusers.
- `apexload/bridge/` (`NativeBridge.java`, `NativeSession.java`, `NativeWire.java`, `SlotRuntime.java`) — framed native service bridge for local data/livetable calls and the minimal `SlotRuntime` interface shared across classloader boundaries.
- `apexload/host/` (`PooledMain.java`, `PooledHost.java`, `PooledWire.java`, `SlotLoader.java`, `SlotWorker.java`, `GCPauses.java`) — shared-JVM (`apexload.java_vuser.ipc.v3`) worker process entry point, length-prefixed binary/JSON wire framing, per-slot child-first `SlotLoader` classloader isolation (or shared loader in `jvm_sharing: threads` Thread Mode), slot worker lifecycle execution and JMX GC pause delta attribution (`apexload-slot-api.jar`, compiled with `--release 8`).

### `fixtures/` and `evidence/`

- `fixtures/SmokeActions.java` — minimal `Actions` class compiled into `/opt/apexload/java-vuser/smoke/SmokeActions.class` inside the runner image for container smoke and readiness checks.
- `evidence/` (`LightActions.java`, `HeavyActions.java`, `PodContainmentActions.java`, `pom.xml`) — benchmark and containment fixtures exercised by the Go integration tests in `runners/java_vuser/*_test.go` (`lr_shim_test.go`, `pooled_ipc_test.go`, `shared_loader_test.go`, `slot_capacity_test.go`, `slot_runtime_test.go`, `gc_pause_test.go`, `pod_containment_test.go`, `evidence_test.go`).

## Container images (`Dockerfile*`)

| Dockerfile | Binary & JARs | Default port / user | Status & operational role |
| --- | --- | --- | --- |
| `Dockerfile` | `/usr/local/bin/apexload-java-vuser-runner` (`cmd/apexload-java-vuser-runner`), `java-vuser-runner.jar`, `apexload-slot-api.jar`, `apexload-slot-shim.jar`, `smoke/SmokeActions.class`, OpenJDK 21 JDK (`jmods` stripped) | `127.0.0.1:8098` (`USER 65532:65532`) | **Standard governed sidecar runner.** Compiles imported Java Vuser sources (`/v1/compile`), runs `full_lifecycle` executions out of the box, and—when deployed with governed containment and `APEXLOAD_JAVA_VUSER_SHARED_JVM` / cgroup delegation—serves persistent phase lifecycles and runner-qualified shared JVMs. |
| `Dockerfile.broker` | `/apexload-java-host-broker` (`cmd/apexload-java-host-broker`) on `scratch` (no Java runtime or baked keys) | `9444` (`USER 10001:10001`) | **Experimental** (disabled by default). mTLS control-plane broker that provisions dedicated single-run Java execution pods and Secrets, fences lease renewals and requires external boundary attestation before releasing pod ownership. |
| `Dockerfile.facade` | `/apexload-java-host-facade` (`cmd/apexload-java-host-facade`) on `scratch` (no Java runtime or Kubernetes credentials) | `8092` (`USER 10001:10001`) | **Experimental** (disabled by default). Loopback sidecar deployed beside `cmd/generator` to translate local generator host-allocation requests into mTLS broker calls using read-only token/TLS mounts. |
| `Dockerfile.supervisor` | `/apexload-java-host-supervisor` (`cmd/apexload-java-host-supervisor`), `/assets/harness.jar`, OpenJDK 21 headless JRE | `9443` (`USER 10001:10001`) | **Experimental** (disabled by default). Base image for digest-pinned dedicated execution pods where `apexload-java-host-supervisor` runs as PID 1 inside a private Linux PID namespace with read-only root filesystem, dropped capabilities and `/scratch` workspace. |
