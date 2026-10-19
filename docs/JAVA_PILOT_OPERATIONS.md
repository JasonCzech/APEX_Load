# Experimental Java pilot operations

Status: Experimental. Dedicated execution remains the default; pooled execution requires explicit operator approval. Fixture construction, baseline/pilot collection, gate evaluation and AppCDS operation are collected here. [Implementation scope and release gates](JAVA_VUSER_POOLING_IMPLEMENTATION.md) still apply. These commands do not qualify AKS containment or production pooled execution.

## Contents

- [Building and checking fixtures](#building-and-checking-fixtures)
- [Collecting a dedicated baseline](#collecting-a-dedicated-baseline)
- [Explicit local pooled pilot](#explicit-local-pooled-pilot)
- [Gate evaluation](#gate-evaluation)
- [Operator AppCDS archives](#operator-appcds-archives)

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
