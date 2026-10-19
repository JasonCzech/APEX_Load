# Experimental dedicated Java broker

This guide owns broker configuration, API usage and deployment. The [dedicated execution contracts](JAVA_DEDICATED_EXECUTION_CONTRACTS.md) describe authority, supervision and preparation invariants; [implementation scope and release gates](JAVA_VUSER_POOLING_IMPLEMENTATION.md) distinguish local validation from production qualification.

Status: runnable internal broker, loopback facade and experimental single-group
engine integration with local component tests; no AKS qualification or distributed
assignment integration. Dedicated local execution remains the
default. This command does not enable an AKS backend in the controller.

Build with `make java-host-broker`. The container recipe is
`runners/java_vuser/Dockerfile.broker`. Mount configuration and credentials
read-only, and keep the SQLite authority on broker-owned persistent storage
outside execution pods. Use one active broker per authority. A restart starts a
new epoch and retires old hosts; it never adopts or restarts Java state.

For a separate bootstrap job, `--initialize-store-only` creates the authority
once and exits without loading transport credentials, opening a listener or
contacting Kubernetes. It cannot be combined with `--initialize-store` and also
refuses an existing authority. The normal broker can then start without either
initialization flag.

For a genuinely new, empty authority, start once with:

```bash
bin/apexload-java-host-broker --config /operator/broker.json --initialize-store
```

Subsequent starts **omit** `--initialize-store`. The default refuses a missing
state file. The initialization flag refuses an existing file. Do not replace a
lost authority to bypass quarantine. Starting a new epoch fences older brokers,
including when subsequent startup validation fails.

## Operator configuration

The strict JSON configuration has contract `apexload.java-broker/v1`. File paths
must be absolute. Certificates, keys and tokens are read from files, not embedded
in JSON or forwarded from a facade. Unknown fields, oversized files and invalid
profiles fail closed. This example is a template: replace the digest placeholders
and provision actual independently qualified identities before use.

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

The profile timeouts must match the immutable workload image's supervisor
profile. Authenticated readiness now advertises these values; drift is rejected
before commit. The command requires the host lease to be at most half the owner
lease, leaving renewal margin. Transport validity must cover preparation, the
admitted execution and drain allowance, is bounded to 24 hours, and cannot outlive
the signing CA. Reservations require an explicit `run_duration_ms`; the broker
and facade enforce a fixed post-commit deadline. Renewal cannot extend it.
There is no credential rotation or transparent session replacement.

Only a fresh ECDSA server-only host key, its public certificate chain and public
broker-client trust bundle are placed in each immutable host Secret. The issuer
key, broker client key, Kubernetes token and facade credentials remain outside
execution pods. Unique host/epoch-derived Secret names cannot be overwritten or
adopted after conflicts. Cleanup uses exact Secret UID preconditions after durable
host reap; a round-robin sweep catches late Secret creates. Secret deletion never
proves JVM termination or releases a host reservation.

## Internal API

`POST /v1/java-broker` requires TLS 1.3 and a verified client certificate with
exactly one approved `/java-facade/<owner_id>` URI. The authenticated principal
selects the owner and allowed profiles. Requests are bounded to 512 KiB; a facade
cannot provide images, Kubernetes credentials, VM arguments or termination proof.

Requests contain `contract`, a unique `request_id`, and `operation`. Responses
echo the contract/request and the current `broker_epoch`. The operations are:

| Operation | Input and behavior |
| --- | --- |
| `capabilities` | Authenticated principal; returns only its allowed profiles, dedicated mode, execution-pod containment, host/Vuser/memory ceilings and bounded execution lifetimes. |
| `claim` | One process-lifetime `instance_id`; returns a new `owner_epoch`, secret `owner_token` and lease interval. Repeating a live claim does not renew it. Expired instances cannot reclaim. A different instance waits for release or expiry. |
| `renew` | Owner epoch/token and strictly increasing `renew_sequence`; renews only the owner, independently of invocations. Replayed renewals cannot extend authority. |
| `renew_host` | Owner authority, wave/host ID and an independent per-host `renew_sequence`; renews that reservation and supervisor only. Owner or peer heartbeats cannot preserve abandoned Vusers. |
| `reserve` | Owner authority, execution/group IDs, approved profile ID, expected `configuration_sha256`, complete `policy_sha256`, explicit `run_duration_ms` and canonical typed session contexts. Profile drift is rejected before provisioning. Atomically reserves the wave, returns host/session identities and starts preparation. It runs no user initialization. |
| `ready` | Owner authority and `wave_id`; reads preparation state and exact host/session inventory. |
| `commit` | Owner authority and ready wave ID; crosses the authenticated host and durable commit barriers. |
| `invoke` | Owner authority, wave/host ID, strictly increasing per-host `sequence`, init/action/end and bounded string parameters. Busy calls are not queued; invocations are never retried. |
| `close_host` | Owner authority and committed wave/host ID; closes one Vuser independently of invocations and peers. It retains the reservation until positive termination evidence. |
| `abort` | Owner authority and wave ID; retires the exact wave. |
| `release` | Owner authority; cancels preparation/invocations and starts cleanup. The returned `retiring` state is not a termination receipt. |
| `cleanup` | Authenticated facade plus an owner epoch; reads durable cleanup even after that lease ended or the broker restarted. Only `reaped` confirms all matching reservations are reaped and that owner cannot admit more work. Unknown epochs are not proof. |

The managed Go `BrokerClient` pins both TLS principals, freezes one owner epoch,
validates exact host/session inventory and profile identity, and never retries
initialization or adopts state after restart. Its owner and per-host renewal lanes
must be driven by real upstream authority. During preparation only the admitted
execution can maintain the reserved peak; after activation each Vuser must retain
its independent runtime lease. A healthy generator heartbeat is insufficient.

The internal invocation payload uses authenticated supervisor host-call
identities. `DedicatedBrokerSession` validates and translates these into the
dedicated response/event contract with fresh correlation IDs and namespaced
transactions. It preserves stdout/stderr and excludes unattributable late events.
`DedicatedBrokerProtocol` connects the translated capture to existing engine
result helpers. Neither adapter renews its own authority or replays init.
The loopback facade, explicit distributed assignment and controller reporting
are described below. Backend selection remains explicit; no default mode changes
or general pooled integration are enabled by this command.

Independent reconciliation requires the configured
[external attestor](JAVA_BOUNDARY_ATTESTOR_CONTRACT.md). API errors, absent pod
objects, deletion acknowledgments and node loss alone retain charged
quarantine. Even after positive runtime proof, a DELETE acknowledgment does not
release capacity until a fresh API read confirms that the pod object is gone.
Known-container lookups never initiate infrastructure fencing. A missing
container identity requires a separately qualified admission fence and complete
never-started proof plus authoritative pod-object removal for the exact durable
creation intent; until then its reservation stays charged. No cluster or
attestor is contacted merely to qualify itself during startup; successful
startup is not evidence that containment is safe.

## Deployment scaffold

`deployments/k8s/java-host-broker` renders a standalone Kustomize scaffold with
separate Restricted broker/execution namespaces and service accounts, a narrowly
scoped execution-namespace pod/Secret Role, explicit broker token projection,
default-deny policies, a broker Service/Deployment and independent execution-pod
quotas. It is not included in existing AKS/local overlays or KEDA generator
replicas. The Deployment has zero replicas and a placeholder image digest.

```bash
kubectl kustomize deployments/k8s/java-host-broker > /tmp/java-broker.yaml
```

The operator overlay must supply the built image digest, the named transport and
configuration Secrets, and an existing `java-broker-state` PVC with reliable
SQLite file locking/fsync. Match Secret filenames to the configuration paths
above. Initialize a new authority with the separate
`bootstrap-job.optional.yaml`, then retain it through broker restarts. The job
mounts only configuration and state; it receives no projected token or signing
key and does not start a listener. It is deliberately absent from the scaffold's
resource list. Existing or lost authority requires recovery, not bootstrap.

Before setting replicas to one, add reviewed API-server/attestor egress rules,
workload target/proxy egress, placement and the correct facade namespace. The
scaffold opens only DNS and the explicit control paths; it does not invent the
cluster's API, attestor or target addresses. Keep metadata/node services outside
execution egress, and verify CNI enforcement plus actual DNS/SNAT behavior on the
target cluster. Keep quotas consistent with the approved profiles and durable
broker budget. Host assets remain baked into immutable workload images.

The authority namespace/PVC and unresolved execution records must survive until
cleanup is positively proved; do not use blanket namespace deletion as cleanup.
Only the broker has the execution-namespace Role. There are no node permissions,
pods/exec permissions, cluster roles or tokens mounted into execution pods.
Target-runtime containment and fault qualification remain required before release.


## Loopback facade and single-group engine integration

Build `make java-host-facade` or use `runners/java_vuser/Dockerfile.facade`.
The facade runs beside the generator, with separate read-only credentials:

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

Run `bin/apexload-java-host-facade --config /operator/facade.json`. The listener
must be a numeric loopback IP and explicit port. The generator receives only the
loopback endpoint and a bearer token of at least 32 characters. The facade owns
its mTLS client credential; it receives no Kubernetes token, host issuer key or
fencing authority. HTTP requests with a non-loopback peer or browser Origin are
rejected. Configuration/token files and request/response bodies are bounded;
errors do not echo credentials or configuration values.

The internal `POST /v1/java-facade` contract supports capabilities, claim,
owner renewal, reserve, ready, commit, run renewal, activation, session renewal,
invoke, session closure, abort, release and cleanup. `BrokerFacadeClient` supplies
typed handles. A process owns one generator epoch, with no transparent takeover.
Every renewal and invocation has an independent monotonic sequence; uncertain
transport cannot trigger replay. Tokens bind runs and sessions to the owner.

Owner heartbeats maintain only generator authority. Run heartbeats renew pending
hosts during preparation and ramp. Activation transfers a host to a separate
Vuser lease; subsequent run heartbeats cannot keep that Vuser alive. Ordinary
closures, abort and release return `retiring`; only authenticated durable cleanup
with positive external evidence can return `reaped`. Delayed activation responses
cannot revive an aborted run. Preparation expiry cannot release initialization.

Capabilities include the exact supervisor configuration digest and a digest of
the complete operator profile (image, resources, placement and transport policy).
Both must match reservation. The advertised maximum run lifetime is transport
validity minus preparation and two host-lease intervals. The requested lifetime
must include ramp, pauses, iterations and teardown. Execution exceeding that
bound fails explicitly; sessions and credentials are never silently replaced.

`engine.NewJavaBrokerExecution` is an experimental, explicitly configured
single-group entry point. Its fresh facade client is exclusive to that engine.
An operator must approve the exact DSL SHA256 together with the selected profile
and target class. It freezes the DSL, negotiates the full profile, prepares the
admitted peak, waits for readiness and commits before starting the ramp clock.
Activation runs no Java initialization. Duration-based admission reserves an
additional preparation-timeout allowance for handoff and an invocation-timeout
allowance for teardown within the fixed run lifetime; longer script action
timeouts are rejected against the frozen profile. Independent Vuser renewal continues
through authored end calls and stops on teardown; pending and active hosts are
aborted when the execution scope ends. Cleanup requests do not prove capacity is
free, and broker reservations remain authoritative. Parent and transaction
evidence carries the reservation ID, profile ID, complete policy digest and
supervisor configuration digest alongside host/session IDs; authority tokens
are never included.

This entry point currently accepts closed workloads with one explicit
init/action/end phase each and sequential run logic. It does not select a backend
from script input or supply distributed placement, dynamic scaling or controller
reporting. Ordinary dedicated execution retains its supported behavior and stays
the default. Local tests cover real loopback HTTP plus mTLS broker/supervisor
contracts with a simulated JVM, and the engine/client/adapter path with a wire
peer. These tests do not establish AKS containment or performance qualification.

## Explicit distributed dedicated placement

`javasidecar.BrokerRuntime` now owns a generator-lifetime facade connection.
Independent prepared groups share that owner and retain separate run and Vuser
leases. Closing a group aborts only its reservation. `engine.GroupConfig.JavaBroker`
binds a frozen script/profile to the multi-group engine. Runtime preflight prepares
hosts; launch commits and transfers their leases before the ramp clock. No constructor
or lifecycle method runs during preparation or activation. A missed coordinated
start fails instead of shifting the ramp. Closing a staged engine or stopping its
assignment requests cleanup without starting it.

The generator accepts an optional `APEXLOAD_JAVA_HOST_CONFIG` absolute file path:

```json
{
  "contract": "apexload.java-placement/v1",
  "endpoint": "http://127.0.0.1:8092",
  "token_file": "/java-host/loopback-token",
  "approvals": [{
    "script_sha256": "<canonical DSL SHA256>",
    "target_class": "Actions",
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

Replace the example's profile with the exact capability returned by the broker;
these numbers are illustrative, not sizing recommendations. The canonical source
hash is computed by `engine.JavaPooledScriptSHA256` from the parsed DSL. Raw file
hashes are separately used by distributed script delivery and are not interchangeable.
The generator config contains only its loopback token; broker and Kubernetes
credentials remain outside the generator. Owner renewal continues through bounded
shutdown drain, followed by explicit release.

Configured generators advertise `java_host_broker_v1` and separate `java_host`
capabilities. An explicitly selected `WorkAssignment.java_host` binds the contract,
source hash, profile/configuration/policy identities, preparation timeout and fixed
execution lifetime. Omission retains the existing local dedicated path. Unsupported
or changed selections fail before reservation; there is no execution-time fallback.
The preparation timeout cannot exceed the generator's assignment staging window.
The acknowledgement echoes the frozen selection and opaque reservation identity,
plus mode, containment and ceilings. The Go client requires a matching capability advertisement on the same client
connection within the preceding 30 seconds before sending selected work. Retained
clients share that evidence; missing, stale or malformed advertisements fail closed.
It also rejects accepted assignments without matching placement evidence, including
acknowledgements from older peers.
Assignment retries include placement in their fingerprint and do not reserve again.

Optional `approvals[].facade_resources` supplies an operator-owned **per-Vuser**
generator/facade budget, separate from the broker's execution-pod profile:

```json
{"cpu_millicores":100,"memory_bytes":8388608,"network_kbps":100}
```

These values are illustrative, not measured defaults. Include amortized shared
facade overhead and choose them from workload evidence. All three must be positive;
omitting the budget leaves generator pod-ledger admission unavailable for that
profile. Advertisements and assignments bind a canonical resource digest, so an
approval change cannot silently alter an admitted budget. The controller reserves
this budget under `java_host_facade`, independently of remote host/Vuser/JVM-memory
reservations. Both budgets must fit before preparation. Generator reservations
retain the admitted peak until run cleanup; broker host and memory quarantine
continues until positive termination evidence. Execution pods are not KEDA
generator replicas. This configuration supplies capacity limits, not throughput
or production sizing claims.

The current distributed dedicated path accepts the same bounded closed-workload
lifecycle as the single-group entry point. Source-plan assembly and evacuation
with a duration offset are rejected. Scale-down closes admitted Vusers. Staged increases consume unused sessions
prepared before commit; increases beyond that reservation are rejected. Duration
extensions are rejected against the fixed execution lifetime. Pauses consume that
wall-clock lifetime. Local JVM capacity accounting excludes remote hosts; broker
quarantine remains authoritative even after assignment cleanup acknowledges.

Review corrected cancellation scope: a caller-cancelled request with a known
run/host retires that scope without invalidating unrelated dedicated groups.
Lost claim/reservation replies, unknown transport loss, malformed responses and
epoch mismatches still fence the owner. Invocation cancellation is never retried.
A regression exercises this through the real loopback HTTP, mTLS broker and mTLS
supervisor stack with a simulated JVM.

The controller and dashboard can now explicitly select these dedicated profiles
per group. The selection lives in `groups[].assignment.java_host`:

```json
{"mode":"auto","java_host":{"profile_id":"approved","preparation_ms":120000,"run_lifetime_ms":3600000}}
```

The generator must advertise an approval for the exact effective script. The
controller freezes its script/configuration/policy identities before reservation,
refreshes capability negotiation before AssignWork, and rejects changed profiles
without changing mode. Preparation uses its configured deadline rather than an
ordinary action/RPC timeout. Inventory and execution manifests report configured
host/Vuser ceilings and the opaque reservation identity; ceilings are not free
capacity or positive cleanup evidence.

The controller can split a fixed or staged population across eligible generators,
with an independently frozen profile and reservation per shard. Placement caps
each shard by generator, host, Vuser and memory limits. It supports ramp-up and
irreversible scale-down without moving or recreating active Java state. It rejects
unreserved increases, adaptive goals, source-plan execution, at-once initialization,
controller adoption and generator-loss recovery before admission.
Duration adjustments are rejected; pauses consume the fixed wall-clock lifetime.
An uncertain scale-down invokes terminal cleanup rather than attempting to revive
closed sessions. Profile selection is group-scoped and cannot supply endpoints,
credentials, JVM flags or assets. The existing dedicated path remains the default.

Staged populations require the explicit `java_host_prepared_population_v1`
capability. Before commit, the controller reserves the initial population plus
all positive scheduled changes. A down/up cycle needs fresh sessions, so this
lifetime admission count can exceed the peak simultaneous population. Every
prepared host and generator/facade allocation stays charged; closures do not
replenish unused admissions. Preparation and activation of these cold sessions
run no user initialization. The ramp starts user code only when its scheduled
population increases. Zero-user shards stay attached for later stages, with
fixed slot identities, profile and reservation. Missing capability or insufficient
capacity rejects the schedule before traffic. Manual increases can use remaining
prepared admissions; exhaustion rejects the change without provisioning during
the ramp or reviving closed state. Partially applied changes trigger cleanup,
never compensating initialization.

Existing overlays do not deploy or enable this backend. Target AKS containment, external attestor/fencing, egress,
node-loss and production workload qualification remain required before release.
