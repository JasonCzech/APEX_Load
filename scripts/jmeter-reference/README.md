# JMeter messaging reference baseline

This is a **reference installation**, not a claim about the customer's original installation. `startup-evidence.json` records a successful local-only startup, fixture load, and millisecond-format check. No Kafka or Event Hubs messages were sent. Live conformance remains unverified.

`baseline.json` records the source revisions, toolchain/source archive hashes, fixture hashes, and dependency coordinates embedded in PNOP's shaded artifacts. `startup-evidence.json` records every runtime classpath JAR hash. `dependency-tree.txt` records Maven's resolved runtime dependency graph. Shaded SDK dependencies must be considered alongside the Maven graph; the reduced PNOP POM alone is incomplete.

## Assemble

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

## Conformance still required

Instrument producer creation/sharing/send/acknowledgement/flush/close and sampler timing. Compare counter/random sequences, timer scope and completion, failures, admission and delivery timeouts, OAuth refresh and TLS policy against controlled services. Authorized live Event Hubs evidence and interrupted-run behavior are still required. Startup and mock tests cannot substitute for this evidence.

## Deterministic parameter evidence

`ReferenceParameters.java` uses the pinned JDK 17 and JMeter 5.4.1 classes to generate `pkg/engine/testdata/jmeter_parameter_vectors.json`. It covers 600 Java random values (including rejection sampling bounds and signed seed extremes), 20 counter iterations and 20 RandomVariableConfig iterations. `parameter-evidence.json` records the source and output digests. Engine tests compare both shared and per-user modes against these vectors; concurrent tests verify exact wrap counts and session isolation. This is component evidence, not full sampler lifecycle or live parity.
