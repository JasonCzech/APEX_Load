# Scripts

Helper scripts and reference harnesses. The two sections below own the
documentation for `jmeter-reference/` and `synthetic-mixed-protocol-test/`;
other scripts in this directory carry usage in their own headers.

| Script / directory | Purpose |
| --- | --- |
| `jmeter-reference/` | Pinned JMeter/Kafka/Event Hubs reference harness ([details](#jmeter-messaging-reference-baseline)) |
| `synthetic-mixed-protocol-test/` | Runnable 45-minute mixed-protocol synthetic test ([details](#synthetic-mixed-protocol-45-minute-test)) |
| `check-docs.py` | Documentation link/structure checks |
| `render-aks-manifests.sh`, `render-eks-manifests.sh` | Render AKS/EKS Kustomize manifests from Terraform outputs (see `deployments/README.md`) |
| `run-optin-suites.sh` | Opt-in test suites (see `docs/qa/TEST_PROGRAM.md`) |
| `multiproject/` | Multi-project concurrency suite: a black-box Go test plus a disposable production-posture Docker stack (2 HA controllers, 3 mTLS generators, PostgreSQL, Redis, S3); also runs against an existing deployment such as AKS (see its README) |
| `ha-failover-harness.sh` | Local-process HA failover harness (2 controllers, 2 generators, throwaway Postgres): leader kill/suspend, double failover, stop, stage shapes, mixed fleet; records load continuity at the target (see `docs/HA_RUN_CONTINUITY_PLAN.md` §9, §12) |
| `coverage-baseline.sh`, `coverage-ratchet.py`, `diff-coverage.py` | Coverage baseline, ratchet and diff-coverage tooling |

## JMeter messaging reference baseline

This is a **reference installation**, not a claim about the customer's original installation. `startup-evidence.json` records a successful local-only startup, fixture load, and millisecond-format check. No Kafka or Event Hubs messages were sent. Live conformance remains unverified.

`baseline.json` records the source revisions, toolchain/source archive hashes, fixture hashes, and dependency coordinates embedded in PNOP's shaded artifacts. `startup-evidence.json` records every runtime classpath JAR hash. `dependency-tree.txt` records Maven's resolved runtime dependency graph. Shaded SDK dependencies must be considered alongside the Maven graph; the reduced PNOP POM alone is incomplete.

### Assemble

Use JDK 17 and Maven 3.9.9 with an isolated Maven repository. The recorded installation uses Temurin 17.0.16+8 for macOS aarch64. The hashes describe the actual assembly; locally compiled JARs can differ on rebuild because their upstream packaging embeds timestamps.

1. Download DI-KafkaMeter source at `8c2dcc9c3c5452ff7cbbcdb33303ca7d74e79b71` from `https://github.com/rollno748/di-kafkameter`. Build/install its POM with `-Djmeter-version=5.4.1 -DskipTests`. The source POM defaults to JMeter 5.5, so the explicit override is required. Kafka remains 3.3.1.
2. Download PNOP source at `7fe06c76bb3b729da994e4a168895ba27a72abbd` from `https://github.com/pnopjp/jmeter-plugins`. Build/install `plugins/protocol/aad/pom.xml`, then `amqp/pom.xml`, then `eventhubs/pom.xml`, with `-DskipTests`. These produce AD 0.3.0, AMQP 0.2.2, and Event Hubs 0.2.8. Event Hubs 0.2.8 was unavailable from Maven Central during assembly; using a different release is not an acceptable fallback.
3. Build this directory's POM with `package dependency:build-classpath -Dmdep.outputFile=/absolute/path/classpath.txt dependency:tree -DoutputFile=/absolute/path/dependency-tree.txt`. Use the same `-Dmaven.repo.local=/absolute/path/isolated-m2` for every Maven command. `-Dreference.build.dir=/absolute/path/build` keeps build output outside the checkout. Throughput Shaping Timer 2.6 resolves from Maven Central; its inspected source revision is recorded in the baseline.
4. Populate a reference JMeter home with `bin/jmeter.properties`, `bin/saveservice.properties`, and `bin/upgrade.properties` from Apache JMeter's `rel/v5.4.1` source tag. Their hashes are recorded in the baseline.
5. Run:

   ```sh
   python3 scripts/jmeter-reference/verify.py \
     --java-home /absolute/path/jdk17 \
     --classpath-file /absolute/path/classpath.txt \
     --jmeter-home /absolute/path/jmeter-home \
     --fixture pkg/convert/jmeter/testdata/messaging_original.jmx \
     --output /absolute/path/startup-evidence.json
   ```

The verifier checks JDK/JMeter/Kafka versions, loads the plugin classes and synthetic fixture, checks single-`S` formatting against Java, then executes one independent Debug Sampler. It never configures or runs the imported messaging plan. UTC and no overrides are reference defaults only; they do not confirm the source environment.

### Conformance still required

Instrument producer creation/sharing/send/acknowledgement/flush/close and sampler timing. Compare counter/random sequences, timer scope and completion, failures, admission and delivery timeouts, OAuth refresh and TLS policy against controlled services. Authorized live Event Hubs evidence and interrupted-run behavior are still required. Startup and mock tests cannot substitute for this evidence.

### Deterministic parameter evidence

`ReferenceParameters.java` uses the pinned JDK 17 and JMeter 5.4.1 classes to generate `pkg/engine/testdata/jmeter_parameter_vectors.json`. It covers 600 Java random values (including rejection sampling bounds and signed seed extremes), 20 counter iterations and 20 RandomVariableConfig iterations. `parameter-evidence.json` records the source and output digests. Engine tests compare both shared and per-user modes against these vectors; concurrent tests verify exact wrap counts and session isolation. This is component evidence, not full sampler lifecycle or live parity.

### Deterministic timer calculations

`ReferenceSchedules.java` calls TST 2.6's real `getRPSForSecond` and, through reflection, its private `getDelay`. `verify-schedules.py` rejects classpaths whose JAR hashes differ from the recorded startup installation. Run it with `--java-home`, `--classpath-file`, `--jmeter-home`, `--vectors pkg/engine/testdata/jmeter_schedule_vectors.json`, and `--evidence scripts/jmeter-reference/schedule-evidence.json`.

The captured 7,804 rate vectors cover both workload curves through second 3,901. The 448 delay vectors cover fractional rates, Java integer saturation, sent counts, waiting-thread counts and within-second offsets. Go tests match these exactly and exercise full-duration virtual clocks and concurrent admission. The final segment includes second 3,900; the timer first requests stopping at second 3,901. The displayed 3,900-second authored duration and integrated nominal volume remain unchanged.

This evidence does **not** verify Java monitor wake ordering, pre-sampler integration, thread-group stop escalation, or cancellation/pause integration. Separate component tests cover the Go wait loop's timer ordering, notifications, contention counts and cancellation cleanup. The Go admission state is now connected to pre-sampler engine paths and the active pause clock, with component tests for group completion and cancellation. Compatibility launch gates remain in force; this does not establish reference lifecycle conformance.


### Deterministic OAuth wire evidence

`ReferenceOAuth.java` invokes the real Kafka 3.3.1 callback's private formatters without sending network requests. `verify-oauth.py` uses the same classpath hash lock and requires JDK 17.0.16+8. Its seven synthetic vectors cover Basic URL-safe Base64, UTF-8, Java's narrow whitespace trimming, omitted scope, and Java form encoding of spaces, `*` and `~`. Go OAuth tests compare the actual HTTP header and request body with these vectors.

Run it with the same harness flags as `verify-schedules.py`, setting `--vectors pkg/protocol/http/testdata/kafka331_oauth_vectors.json --evidence scripts/jmeter-reference/oauth-evidence.json`. This verifies callback formatting only; token refresh against an identity service and broker authentication remain separate conformance work.


### Deterministic DI Kafka record evidence

`ReferenceKafkaKeys.java` invokes the pinned sampler's private record builder with StringSerializer and an explicit partition. No producer is constructed and no broker is contacted. The four vectors demonstrate that empty keys become null while whitespace and nonempty UTF-8 keys survive. The synthetic variable context reproduces this revision's null serializer-variable-name fields for its matching string serializers.

Run `verify-kafka-keys.py` with the same harness flags, using `--vectors pkg/protocol/kafka/testdata/di_kafka_key_vectors.json --evidence scripts/jmeter-reference/kafka-key-evidence.json`. This checks record construction, not producer creation or send/acknowledgement behavior.

### Offline PNOP lifecycle traces

`instrumentation/ReferenceEventHubsLifecycle.java` executes the unchanged pinned PNOP sampler with a Java instrumentation agent. The agent replaces only SDK producer creation, send and close with deterministic hooks, and observes JMeter's real `sampleStart`/`sampleEnd` calls. A JDK 17 network guard rejects connections, listening and acceptance; the SDK producer constructor never runs. Internal ASM and allocation APIs are confined to this separate, version-pinned harness and are not part of the product or the ordinary Maven assembly.

Run `verify-eventhubs-lifecycle.py` with the same harness flags, setting `--vectors scripts/jmeter-reference/eventhubs-lifecycle-vectors.json --evidence scripts/jmeter-reference/eventhubs-lifecycle-evidence.json`. The verifier checks the complete pinned classpath, requires JDK 17.0.16+8 and asserts that all five intended methods were instrumented. Repeated generation is deterministic; an altered classpath is rejected before execution.

The five cases exercise two successful samples with distinct producers, creation failure, send failure, and close failure. Three messages (including an empty message) stay within one sampled send. Success follows `sampleStart → create → send → acknowledgement → sampleEnd → close`. Creation and send failures return an unsuccessful sample without `sampleEnd`. A close exception escapes the sampler, even after a successful send: it does not return a failed `SampleResult`.

This is **plugin control-flow evidence with a substituted transport**, not real SDK lifecycle or service conformance. APEX currently represents close failure as a failed protocol result; the difference remains unresolved for strict replay. Actual SDK creation/cleanup, batch allocation, acknowledgement, network timing and service behavior still require separate evidence.

## Synthetic mixed-protocol 45-minute test

This folder contains a runnable synthetic 45-minute load test for HTTP, browser/HTML, and DevWeb-converted users.

### 1) Start the dummy backend

```bash
python3 scripts/synthetic-mixed-protocol-test/dummy_server.py
```

The dummy server exposes:

- `GET /` and `GET /next` HTML pages for browser navigation
- `GET /api/health`
- `POST /api/orders`
- `GET /api/orders`

### 2) Start the controller (local/dev mode)

```bash
cd /Users/jasonczech/Documents/GitHub/Project_Stormrunner
go run ./cmd/controller
```

### 3) Create and start the mixed-protocol test

```bash
python3 scripts/synthetic-mixed-protocol-test/create_synthetic_test.py
```

The script:

1. creates one HTTP script,
2. creates one browser script,
3. imports and saves one DevWeb script,
4. creates a 45m multi-group test,
5. starts it.

### 4) Verify and control

```bash
TEST_ID=<paste output test id>
curl -s http://127.0.0.1:8080/api/v1/tests/$TEST_ID | jq '{id:.id,status:.status,total_vusers:.total_vusers,duration:.duration}'
curl -s http://127.0.0.1:8080/api/v1/tests/$TEST_ID/metrics
curl -s "http://127.0.0.1:8080/api/v1/tests/$TEST_ID/errors/evidence?signature=500&limit=20"
```

Stop early if needed:

```bash
curl -X POST http://127.0.0.1:8080/api/v1/tests/$TEST_ID/abort
```

### Optional env overrides

- `APEXLOAD_API_URL` (default `http://127.0.0.1:8080`)
- `SYNTHETIC_DUMMY_BASE_URL` (default `http://127.0.0.1:18080`)
- `APEXLOAD_API_KEY` (if you run controller with auth enabled)
