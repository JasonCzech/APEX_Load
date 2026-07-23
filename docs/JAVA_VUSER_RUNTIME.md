# Java Vuser Runtime

APEX Load executes reviewed `java_vuser` scripts through an operator-enabled Java runner. The feature is disabled by default. Importing a script, or setting fields under `config.java_vuser`, cannot enable process execution or replace the operator's runner, harness, work directory, shared classpath, or process limit.

## Java selection

`jdk_selection` defaults to `internal`.

- Local internal resolution checks an explicitly configured direct `java`/`java.exe` launcher, `APEXLOAD_JAVA_VUSER_DEFAULT_JDK_HOME`, `JAVA_HOME`, `JRE_HOME`, and then `PATH`, in that order.
- Sidecar internal resolution uses the image's bundled JDK 21 unless the sidecar operator config supplies a default home.
- `specified` derives only `<jdk_home>/bin/java` or `java.exe` on the executor. The home and launcher are canonicalized, the launcher must be a regular executable, and a bounded `-version` probe must report Java 17 or newer. There is no fallback and no controller-side rewriting of platform-specific imported paths.

A specified home is a trusted raw executor path for reviewed Java code; there is no script JDK allowlist. Use a signed custom runner image or a read-only operator mount when additional JDKs are required.

## VM parameters and classpath

`additional_vm_parameters` is an array of exact JVM argv tokens. The dashboard represents it as one token per line. A token containing a space remains one token; APEX Load never invokes a shell and never expands command substitution, variables, or argument files.

Runtime validation limits count and size and rejects launcher takeover, classpath/module overrides, raw Xboot options, Java/native agents, custom system class loaders, terminal/dry-run options, command-triggering error handlers, reserved `-Dapexload.*` properties, template expressions, and properties whose names indicate credentials such as passwords, tokens, or secrets. JVM arguments are visible in operating-system process metadata, so secret references are deliberately left unresolved and rejected instead of being copied into the Java command line. Pass sensitive values through governed LR parameters/action data rather than `additional_vm_parameters`. Diagnostics identify only the token index and category, not its value.

Script classpath entries identify precompiled classes or JARs already present on the selected executor. Script entries precede operator shared entries. APEX Load does not compile imported source and does not upload artifacts as part of this feature. Every assigned executor verifies the referenced paths before assignment commit.

## Xboot and dedicated loading

`use_xbootclasspath` preserves the imported LoadRunner choice but does not pass an Xboot option to modern Java. Execution uses the ordinary classpath and records the stable modern-Xboot fallback warning/evidence.

With `dedicated_class_loader`, the system classpath contains only the harness. The harness creates a child `URLClassLoader` for the effective script-plus-shared classpath, uses it for class loading and the complete requested lifecycle, and sets and restores the thread context class loader (including the startup worker). Local and sidecar execution create one JVM, `Actions` instance, and loader for each VUser session, retain them across `init`, repeated `action` calls, and `end`, and close them during teardown. Java fields, static state inside that isolated loader, and `lr.save_*` parameters therefore survive between lifecycle calls for the same VUser. This is class-loading isolation inside the runner; it is not a security sandbox.

## Backends and preflight

`APEXLOAD_JAVA_VUSER_RUNNER_MODE` selects `local` (the backward-compatible default) or `sidecar`.

- Local mode rejects ambient credentials before any Java probe or fork, then starts the child with a small allowlisted environment. A non-Java legacy wrapper remains compatible only for scripts that use none of the new JVM settings.
- Sidecar mode uses the versioned `apexload-java-vuser-runner.v1` loopback HTTP contract with persistent session operations in addition to health, capability, validation, legacy one-shot execution, and cancellation endpoints. The generator container never forks Java. The sidecar owns one JVM session for each assigned Java VUser, accepts only script runtime settings and action data, owns operator configuration and process capacity, limits request/output sizes, and kills and reaps canceled, timed-out, or unusable JVMs.

Local runs preflight before starting VUsers. Distributed runs preflight during two-phase assignment staging, after secret resolution and before commit. Preflight verifies the backend contract, the `java_vuser_persistent_lifecycle_v1` and `java_vuser_session_frame_token_v1` capabilities, Java version, harness, VM tokens, effective classpath, unique target classes/methods, and available Java VUser capacity. The frame-token contract bootstraps a random session secret before the `Actions` class is loaded and derives each result token from that secret and the request ID; later request frames expose neither the secret nor the expected token. A failure aborts the assignment atomically; warnings use stable value-free codes. The configured process limit (25 by default) is the generator's maximum number of simultaneously persistent Java VUsers. A timed-out, canceled, or crashed session is reaped, marked unusable, and never silently restarted with lost state.

The same-pod sidecar is credential-minimized, not a hostile-code sandbox. Its deployment receives no generator gRPC credentials, service-account token, workload identity, or sensitive generator mounts, but it still shares the pod network and egress. Mutually untrusted scripts require a separate authenticated worker pool outside this contract.

## Persistent process containment

Combined Java imports use separate `init`, `action`, and `end` calls against one
long-lived JVM. That persistent lifecycle is advertised only when the runner can
atomically contain the JVM and every native descendant before user code starts:

- Windows uses a kill-on-close Job Object and supports the persistent lifecycle.
- Linux requires a writable, delegated cgroup v2 subtree, `cgroup.kill`, and an
  allowed `clone3` cgroup-fd launch. A typical host-native systemd service must
  be deliberately configured with cgroup delegation (for example,
  `Delegate=yes`) and run in the delegated subtree. Availability is proven by a
  real start-and-kill probe; the runner fails closed if delegation, the kernel
  primitive, permissions, or the syscall policy is missing.
- Darwin and the other supported Unix build targets have no equivalent
  ownership primitive in this implementation. Polling process ancestry is used
  only for backward-compatible one-shot cleanup and is never advertised as
  persistent containment.

The stock non-privileged Helm, Kustomize, and Compose sidecars do **not** receive
a writable cgroup v2 delegation. They therefore advertise legacy
`full_lifecycle` execution only; a combined phase-lifecycle script is rejected
during sidecar capability preflight with `persistent_lifecycle_unavailable`
before a JVM session starts. A local Linux runner without the required
delegation fails its corresponding strong-containment gate with
`process_containment_unavailable`. The release container smoke test verifies
that honest capability boundary. Do not work around it with a privileged
container or a writable mount of the host cgroup hierarchy. Run persistent Java
VUsers on Windows or on a separately governed Linux runner with a narrowly
delegated subtree. Kubernetes installations need an operator-provided
containment integration; this repository intentionally does not ship a
privileged cgroup-mount manifest.

## Operator configuration

The relevant environment variables are:

- `APEXLOAD_JAVA_VUSER_RUNNER_ENABLED`
- `APEXLOAD_JAVA_VUSER_RUNNER_MODE`
- `APEXLOAD_JAVA_VUSER_RUNNER_URL`
- `APEXLOAD_JAVA_VUSER_RUNNER_AUTH_TOKEN`
- `APEXLOAD_JAVA_VUSER_RUNNER_COMMAND` (local/direct Java or legacy-wrapper compatibility)
- `APEXLOAD_JAVA_VUSER_DEFAULT_JDK_HOME`
- `APEXLOAD_JAVA_VUSER_HARNESS_JAR`
- `APEXLOAD_JAVA_VUSER_CLASSPATH`
- `APEXLOAD_JAVA_VUSER_WORK_DIR`
- `APEXLOAD_JAVA_VUSER_MAX_PROCESSES`

Helm, Kustomize, and Compose integrations are opt-in. The standard Java runner image contains JDK 21 and a Java 17-targeted harness. Operators can mount a read-only artifact volume or publish a signed custom runner image with approved precompiled classes/JARs.
The packaged sidecar sets `APEXLOAD_JAVA_VUSER_WORK_DIR=/tmp`; its root filesystem is read-only and `/tmp` is the bounded writable volume used by child JVMs.
Helm and Kustomize derive a per-pod sidecar authentication token from the pod UID and inject the same value into the generator and runner containers. Compose has no safe generated default: operators must provide one explicit token, which Compose passes unchanged to both services.

For the local Compose stack, service selection and Java-execution enablement
remain two explicit controls. Start both together with:

```bash
APEXLOAD_JAVA_VUSER_RUNNER_ENABLED=true \
APEXLOAD_JAVA_VUSER_RUNNER_AUTH_TOKEN='<operator-generated-random-token>' \
docker compose --profile java-vuser up
```

The profile adds the runner in the generator's network namespace so its HTTP
endpoint remains loopback-only. Omitting the environment opt-in keeps the
generator-side Java runtime disabled even if the sidecar service is started.
Omitting the authentication token keeps sidecar readiness false and protected
runner requests unavailable. This stock Compose profile is legacy
`full_lifecycle` only: it does not delegate a writable cgroup v2 subtree, so a
combined persistent Java import fails capability preflight with
`persistent_lifecycle_unavailable`.
