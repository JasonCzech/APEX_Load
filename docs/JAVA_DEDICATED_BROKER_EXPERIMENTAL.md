# Experimental Dedicated Java Broker, Execution Pods, and Boundary Attestation

> **Experimental — Disabled by Default**
> `apexload-java-host-broker`, `apexload-java-host-facade`, `apexload-java-host-supervisor`, and external boundary attestation are **experimental** components for one-JVM-per-pod Kubernetes execution. None of these services is enabled by default in the controller, generator sidecar, Helm chart, or standard Kustomize overlays, and the external boundary-attestor service itself is **unshipped** (only the broker's mTLS client contract is implemented).
>
> Standard `java_vuser` runs use the local sidecar runner (`apexload-java-vuser-runner`), which supports both dedicated one-JVM-per-VUser sessions and operator-enabled shared JVMs (`APEXLOAD_JAVA_VUSER_SHARED_JVM=true`); see [JAVA_SHARED_AND_PILOT_RUNTIME.md](JAVA_SHARED_AND_PILOT_RUNTIME.md).

## Contents

- [Broker service and state initialization](#broker-service-and-state-initialization)
- [Operator configuration](#operator-configuration)
- [Internal API](#internal-api)
- [Deployment scaffold](#deployment-scaffold)
- [Loopback facade and single-group engine integration](#loopback-facade-and-single-group-engine-integration)
- [Explicit distributed dedicated placement](#explicit-distributed-dedicated-placement)
- [Durable broker authority and execution-pod supervision](#durable-broker-authority-and-execution-pod-supervision)
  - [Durable dedicated broker state](#durable-dedicated-broker-state)
  - [Dedicated execution-pod supervisor and broker control adapter](#dedicated-execution-pod-supervisor-and-broker-control-adapter)
  - [Dedicated preparation waves](#dedicated-preparation-waves)
- [External boundary attestor contract](#external-boundary-attestor-contract)
  - [Authority and transport](#authority-and-transport)
  - [Known-container termination request and proof](#known-container-termination-request-and-proof)
  - [Creation intents with no known container](#creation-intents-with-no-known-container)
  - [Integration and qualification requirements](#integration-and-qualification-requirements)

---

## Broker service and state initialization

Build the experimental broker binary with `make java-host-broker` (container recipe: `runners/java_vuser/Dockerfile.broker`). Mount configuration and credentials read-only, keep the SQLite authority file on broker-owned persistent storage outside execution pods, and run at most one active broker per authority database. Every broker restart advances to a new `broker_epoch`, fences older connections, and retires/quarantines prior hosts without adopting or restarting Java state.

For a standalone Kubernetes bootstrap job, `--initialize-store-only` initializes the SQLite authority once and exits without loading transport credentials, opening a listener, or contacting Kubernetes (mutually exclusive with `--initialize-store`, and fails if the database already exists). For a manual first start on an empty volume:

```bash
bin/apexload-java-host-broker --config /operator/broker.json --initialize-store
```

Subsequent starts **must omit** `--initialize-store`: normal startup fails closed if the state file is missing, and initialization fails if the file already exists. Never replace a lost authority database to bypass quarantined pod reservations.

## Operator configuration

The broker configuration uses strict JSON contract `apexload.java-broker/v1`. All file paths must be absolute; certificates, private keys, and tokens are read from bounded files rather than embedded in JSON or forwarded from a facade. Unknown fields, oversized files, or mismatched timeouts fail closed:

```json
{
  "contract": "apexload.java-broker/v1",
  "listen": ":9444",
  "state_path": "/state/java-broker.db",
  "execution_namespace": "apexload-java-execution",
  "max_hosts": 25,
  "max_vusers": 25,
  "max_memory_bytes": 8589934592,
  "owner_lease_ms": 10000,
  "kubernetes_endpoint": "https://kubernetes.default.svc",
  "kubernetes_ca": "/kubernetes/ca.crt",
  "kubernetes_token_file": "/kubernetes/token",
  "server_tls": {
    "certificate": "/server/tls.crt",
    "key": "/server/tls.key",
    "ca": "/server/facade-client-ca.crt"
  },
  "control_tls": {
    "certificate": "/control/tls.crt",
    "key": "/control/tls.key",
    "ca": "/control/host-server-ca.crt"
  },
  "control_client_uri": "spiffe://apexload/java-broker",
  "host_issuer": {
    "certificate": "/issuer/ca.crt",
    "key": "/issuer/ca.key",
    "ca": "/issuer/broker-client-ca.crt"
  },
  "host_trust_domain": "apexload",
  "host_dns_domain": "java-hosts.internal",
  "attestor_endpoint": "https://attestor.operator.example",
  "attestor_uri": "spiffe://apexload/java-boundary-attestor/qualified",
  "attestor_ca": "/attestor/ca.crt",
  "attestor_qualification_sha256": "<qualified implementation digest>",
  "principals": [{
    "uri": "spiffe://apexload/java-facade/generator-1",
    "owner_id": "generator-1",
    "profiles": ["approved-workload"]
  }],
  "profiles": {
    "approved-workload": {
      "configuration_sha256": "<exact /assets/java-host.json digest>",
      "image": "registry.example/approved-java@sha256:<immutable image digest>",
      "service_account": "java-execution",
      "memory_bytes": 268435456,
      "cpu_milli": 1000,
      "scratch_bytes": 67108864,
      "node_selector": {},
      "host_lease_ms": 5000,
      "preparation_ms": 120000,
      "invocation_ms": 30000,
      "transport_lifetime_ms": 7200000
    }
  }
}
```

Key configuration invariants:

- **Lease and lifetime bounds**: Profile timeouts (`host_lease_ms`, `preparation_ms`, `invocation_ms`) must match the immutable workload image's `/assets/java-host.json` supervisor profile (verified over mTLS during readiness before commit). `host_lease_ms` must be at most half of `owner_lease_ms`. `transport_lifetime_ms` is capped at 24 hours, cannot outlive the signing CA, and must cover preparation plus the requested `run_duration_ms` and drain margin. Reservations enforce a fixed post-commit wall-clock deadline with no credential rotation or transparent session replacement.
- **Scoped host Secrets**: For each execution pod, `HostCredentialIssuer` generates a fresh ECDSA server-only private key, certificate chain, and public broker-client trust bundle stored in a unique immutable Kubernetes `Secret`. The issuer signing key, broker client key, Kubernetes bearer token, and facade credentials never enter execution pods. Secrets are deleted with exact `uid` preconditions only after durable host reap; Secret deletion never proves JVM termination.

## Internal API

`POST /v1/java-broker` requires TLS 1.3 and a verified client certificate carrying a single approved `spiffe://<trust-domain>/java-facade/<owner_id>` URI SAN. Requests are capped at 512 KiB and carry `contract`, unique `request_id`, and `operation`; responses echo those fields alongside `broker_epoch`:

| Operation | Input and behavior |
| --- | --- |
| `capabilities` | Returns only the caller principal's allowed profiles, dedicated mode, execution-pod containment, host/VUser/memory ceilings, and bounded execution lifetimes. |
| `claim` | Claims one process-lifetime `instance_id`, returning a fresh `owner_epoch`, secret `owner_token`, and lease interval. Expired instances cannot reclaim; another instance waits for explicit release or lease expiry. |
| `renew` | Takes `owner_epoch`/`owner_token` and strictly increasing `renew_sequence`; renews generator owner authority only. |
| `renew_host` | Takes owner authority, `wave_id`/`host_id`, and strictly increasing per-host `renew_sequence`; renews only that host reservation and supervisor so abandoned VUsers expire independently. |
| `reserve` | Takes owner authority, execution/group IDs, approved profile ID, expected `configuration_sha256`, complete `policy_sha256`, explicit `run_duration_ms`, and canonical session contexts. Atomically reserves the wave and begins cold pod preparation without running user initialization. |
| `ready` | Polls preparation state and exact host/session inventory for `wave_id`. |
| `commit` | Crosses the authenticated host readiness barrier and atomic durable commit barrier for a ready `wave_id`. |
| `invoke` | Executes `init`, `action`, or `end` on a committed `wave_id`/`host_id` with strictly increasing per-host `sequence` and bounded string parameters. Busy calls fail immediately and invocations are never retried. |
| `close_host` | Closes a single committed VUser host independently of peers while retaining host/memory capacity charges until external termination proof arrives. |
| `abort` | Retires an exact `wave_id`. |
| `release` | Cancels preparation/invocations for the owner and initiates cleanup (`retiring` is not a termination receipt). |
| `cleanup` | Queries durable cleanup state for an `owner_epoch` even after lease expiry or broker restart; only `reaped` confirms every reservation of that epoch has been positively attested and removed. |

`BrokerClient` pins TLS URI identities, freezes one owner epoch, and separates owner renewal from per-VUser renewal. `DedicatedBrokerSession` and `DedicatedBrokerProtocol` translate authenticated supervisor frames into the engine's dedicated transaction/output stream with fresh correlation IDs and namespaced transaction IDs while excluding late unattributable events.

## Deployment scaffold

`deployments/k8s/java-host-broker` provides an inactive standalone Kustomize scaffold (`replicas: 0`, placeholder image digest, excluded from standard local/onprem/AKS/EKS overlays and KEDA scaling):

```bash
kubectl kustomize deployments/k8s/java-host-broker > /tmp/java-broker.yaml
```

The scaffold defines separate Pod Security Admission `restricted` namespaces for the broker and execution pods, dedicated service accounts (no service-account tokens or service links mounted in execution pods), a namespace-scoped Role granting the broker Pod/Secret access only inside the execution namespace (no `pods/exec`, node, or cluster-wide permissions), default-deny NetworkPolicies, ResourceQuotas, and an optional `bootstrap-job.optional.yaml` for `--initialize-store-only`. Before scaling to 1 replica in an isolated test cluster, operators must supply pinned image digests, TLS/config Secrets, a `java-broker-state` PVC with POSIX/SQLite fsync semantics, and explicit NetworkPolicy egress rules for the Kubernetes API server, external boundary attestor, and target system under test.

## Loopback facade and single-group engine integration

`cmd/apexload-java-host-facade` (`make java-host-facade`, `runners/java_vuser/Dockerfile.facade`) runs alongside a generator pod so the generator process never holds mTLS broker credentials:

```json
{
  "contract": "apexload.java-facade/v1",
  "listen": "127.0.0.1:8092",
  "broker_endpoint": "https://java-host-broker.example:9444",
  "broker_uri": "spiffe://apexload/java-broker",
  "facade_uri": "spiffe://apexload/java-facade/generator-1",
  "certificate": "/facade/tls.crt",
  "key": "/facade/tls.key",
  "ca": "/facade/broker-ca.crt",
  "token_file": "/facade/loopback-token",
  "invocation_ms": 30000
}
```

`listen` must be a numeric loopback IP and port; requests from non-loopback peers or carrying browser `Origin` headers are rejected, and callers authenticate with a bearer token (≥ 32 characters) on `POST /v1/java-facade`.

The facade separates three lease tiers:

1. **Owner heartbeats** maintain generator-epoch authority.
2. **Run heartbeats** maintain pending hosts during cold preparation and ramp-up.
3. **Session activation** transfers an individual host to an independent VUser lease before `init`, after which run heartbeats can no longer keep that VUser alive.

`engine.NewJavaBrokerExecution` provides an experimental single-group programmatic entry point for closed workloads with sequential run logic and one explicit `init`/`action`/`end` lifecycle on an operator-approved DSL SHA-256 (`engine.JavaPooledScriptSHA256`), profile, and target class.

## Explicit distributed dedicated placement

Generators opt into distributed broker placement via `APEXLOAD_JAVA_HOST_CONFIG` (`apexload.java-placement/v1`):

```json
{
  "contract": "apexload.java-placement/v1",
  "endpoint": "http://127.0.0.1:8092",
  "token_file": "/java-host/loopback-token",
  "approvals": [{
    "script_sha256": "<canonical engine.JavaPooledScriptSHA256>",
    "target_class": "Actions",
    "facade_resources": {
      "cpu_millicores": 100,
      "memory_bytes": 8388608,
      "network_kbps": 100
    },
    "profile": {
      "id": "approved",
      "configuration_sha256": "<supervisor configuration SHA256>",
      "policy_sha256": "<complete operator profile SHA256>",
      "memory_bytes": 536870912,
      "host_lease_ms": 5000,
      "preparation_ms": 120000,
      "invocation_ms": 30000,
      "max_run_ms": 3300000
    }
  }]
}
```

Configured generators advertise `java_host_broker_v1` (and `java_host_prepared_population_v1` for staged schedules) plus `java_host` capability records. Scenarios select a broker profile per group under `groups[].assignment.java_host`:

```json
{"mode": "auto", "java_host": {"profile_id": "approved", "preparation_ms": 120000, "run_lifetime_ms": 3600000}}
```

Distributed placement rules:

- **Frozen negotiation and dual budgets**: The controller refreshes capability advertisements (requiring a matching advertisement within the preceding 30 seconds), freezes script/configuration/policy digests, and shards fixed or staged populations across eligible generators within the scenario's fleet pool. When `facade_resources` is configured, the controller charges per-VUser local generator/facade overhead under `java_host_facade` separately from remote broker pod/memory quotas.
- **Cold lifetime preparation for staged schedules**: For staged schedules (`java_host_prepared_population_v1`), the controller reserves and prepares the initial population plus every positive scheduled step before `CommitWork` (down/up cycles consume fresh session identities rather than reusing closed sessions). Scale-down permanently retires closed sessions; unreserved scale-up, duration extensions, adaptive goals, `at_once` init, source-plan assembly, and mid-run controller adoption are rejected.

---

## Durable broker authority and execution-pod supervision

### Durable dedicated broker state

`pkg/javarunner/broker` persists reservation state in SQLite with full synchronous transactions (hard lifetime ceiling: 10,000 retained records; released tombstones remain on disk to prevent session replay across epochs and restarts).

- **Write-ahead provisioning**: Every pod creation intent—including the SHA-256 of the exact Kubernetes Pod request—is durably committed (`BeginPrepare`) before calling the Kubernetes API.
- **Bounded reconciliation**: The reconciler processes cleanup with bounded concurrency, deletes pods only with recorded `uid` preconditions, verifies authoritative Pod object absence via a subsequent Kubernetes `GET`, and requires positive proof from the external boundary attestor before releasing host, memory, and pod-quota charges. Kubernetes `DELETE` 200/202/404 responses, missing Pod objects, `Node NotReady` conditions, or JVM exit codes never release capacity on their own.

### Dedicated execution-pod supervisor and broker control adapter

`cmd/apexload-java-host-supervisor` (`make java-host-supervisor`, `runners/java_vuser/Dockerfile.supervisor`) runs as Linux PID 1 inside each single-container, digest-pinned execution pod (`restartPolicy: Never`, non-root UID, read-only root filesystem, all capabilities dropped, `allowPrivilegeEscalation: false`, `RuntimeDefault` seccomp, disabled dumpability, and bounded scratch mount):

- **Immutable asset verification**: Startup reads `/assets/java-host.json` (max 512 KiB), verifies its SHA-256 against `--configuration-sha256`, resolves symlinks, opens the Java 17+ launcher, `Harness.java` JAR, and ordered user classpath JARs with non-blocking descriptors, verifies regular-file SHA-256 digests, and checks that their backing filesystem mounts are read-only (preventing writable scratch mounts from supplying executable bytes).
- **Cold IPC v2 readiness and mTLS control**: Preparation launches `Harness.java` with a random readiness nonce; the harness captures its control stream, exposes empty `System.in` to user code, and signals `APEXLOAD_JAVA_HOST_READY_V1` before loading or initializing the target class. The supervisor serves TLS 1.3 with mutual authentication on port `9443`, requiring the exact broker SPIFFE URI SAN on client certificates while presenting its host-specific `/java-host/<host-id>` URI SAN. Request payloads are capped at 512 KiB (dedicated IPC payloads at 5 MiB; outer base64 responses at 7 MiB). The supervisor reaps adopted child processes without stealing the root JVM wait and exits immediately on lease/preparation expiry, transport uncertainty, or termination signals.

Repeatable local Docker containment verification lives in `TestDedicatedPodDockerContainment` (`runners/java_vuser/pod_containment_test.go`), gated by `APEXLOAD_JAVA_POD_DOCKER_IMAGE` pointing to a pre-built local Alpine JDK 21 image (`scripts/run-optin-suites.sh`).

### Dedicated preparation waves

`ReserveDedicatedWave` and `DedicatedWave` coordinate all-or-nothing wave admission across up to 8 concurrent `KubernetesHostPreparer` workers. After `Provision` creates a Pod, `ObservePrepared` re-verifies the admitted Pod specification against an allowlist of standard Kubernetes defaults (rejecting mutated images, commands, security contexts, volumes, labels/annotations, resources, or controller references), binds the exact runtime container ID, and completes a verified mTLS handshake before issuing stateful control calls. Per-VUser context (session ID, VUser/group/scenario identity, and think-time limits) travels in a canonical host-context envelope included in the Pod policy digest.

---

## External boundary attestor contract

> **Status**: Only the broker's mTLS client (`pkg/javarunner/broker.ExternalAttestor`) and unit/component tests are implemented in this repository. No external boundary-attestor daemon or AKS infrastructure-fencing service is shipped. Until an operator deploys and qualifies an external attestor matching this contract, terminated or ambiguous broker pod reservations remain durably quarantined.

### Authority and transport

The broker calls an operator-configured HTTPS origin using TLS 1.3, DNS/IP hostname verification, a pinned attestor SPIFFE URI (`/java-boundary-attestor/<identity>`), and the broker's distinct client certificate (`control_client_uri`). Execution-host certificates are rejected for both attestor and broker roles. No Kubernetes bearer tokens, cloud credentials, generator tokens, or script parameters are transmitted.

- **Read-only attestation**: Known-container lookups read termination evidence; they **never** authorize or trigger node/VM fencing. Infrastructure fencing must be governed independently by the external service so a missing Pod or transient lookup error cannot shut down a shared node.
- **Transport bounds and timeout**: Each lookup generates a fresh unpredictable `request_id`, follows no HTTP redirects, ignores ambient proxy variables, and caps response bodies at 64 KiB. While the Go library struct `broker.AttestorConfig` accepts programmatic `Timeout` values up to 30 seconds (`(0, 30s]`), `cmd/apexload-java-host-broker` enforces a hardcoded **`3 * time.Second`** per-request timeout (`cmd/apexload-java-host-broker/main.go`).

### Known-container termination request and proof

For reservations with a recorded `container_id`, the broker posts to `POST /v1/java-boundary/attest` with contract `apexload.java-boundary-attestor/v1`:

```json
{
  "contract": "apexload.java-boundary-attestor/v1",
  "request_id": "<unique challenge>",
  "qualification_sha256": "<64 lowercase hex characters>",
  "boundary": {
    "namespace": "apexload-java-execution",
    "name": "java-<host id>",
    "uid": "<exact Kubernetes pod UID>",
    "container_id": "containerd://<exact container ID>",
    "host_id": "<host id>",
    "broker_epoch": "<epoch that created this host>"
  }
}
```

HTTP 200 must echo every request field and return `"result": "unproven"` (retaining the reservation charge) or `"result": "terminated"` with a fresh `proof`:

```json
{
  "kind": "infrastructure_fence",
  "scope": "pod_container_descendants",
  "evidence_sha256": "<hash of retained external evidence>",
  "observed_at": "<RFC3339 timestamp within preceding 30s (+2s skew allowance)>",
  "expires_at": "<RFC3339 future timestamp <= observed_at + 30s>",
  "node_uid": "<immutable Kubernetes node UID>",
  "machine_id": "<provider resource locator>",
  "machine_uid": "<immutable backing machine identity>",
  "fence_id": "<retained infrastructure-fence receipt identity>"
}
```

Two proof kinds are accepted, both requiring `scope: "pod_container_descendants"`:

1. `runtime_boundary`: Proves the exact container cgroup/PID boundary (including all detached descendants) is empty on the qualified container runtime; omits `node_uid`, `machine_id`, `machine_uid`, and `fence_id`.
2. `infrastructure_fence`: Proves via durable boundary-to-machine provenance that the original immutable backing machine (`node_uid` + `machine_uid`, not merely a reusable VM/VMSS resource name) has been fenced and cannot continue or resume execution.

### Creation intents with no known container

When a Pod create is rejected, times out ambiguously, or stays `Pending` without a bound `container_id`, the broker posts to `POST /v1/java-boundary/attest-never-started` using contract `apexload.java-no-start-attestor/v1`:

```json
{
  "contract": "apexload.java-no-start-attestor/v1",
  "request_id": "<fresh challenge>",
  "qualification_sha256": "<64 lowercase hex characters>",
  "intent": {
    "namespace": "apexload-java-execution",
    "name": "java-<host id>",
    "uid": "<exact pod UID, or empty if no UID was returned>",
    "host_id": "<host id>",
    "broker_epoch": "<original broker epoch>",
    "pod_policy_sha256": "<hash of the exact attempted pod request>"
  }
}
```

To release capacity, the external service must return `"result": "never_started"` with `proof.kind: "creation_fence"`, `proof.scope: "pod_never_started_object_removed"`, `evidence_sha256`, `creation_fence_id`, and fresh `observed_at`/`expires_at` timestamps. That proof attests both an admission fence preventing any delayed/in-flight creation for the intent from starting afterward, complete runtime history confirming no container ever started, and UID-preconditioned Pod object removal verified by an authoritative API read.

### Integration and qualification requirements

Before enabling `apexload-java-host-broker` in any cluster environment, operators must deploy and qualify an external boundary attestor and verify detached-descendant termination, node loss/network partitions, delayed admission fencing, immutable machine identity mapping, CNI egress isolation, and broker restart recovery on the target Kubernetes runtime.
