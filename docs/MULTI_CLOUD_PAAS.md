# Multi-cloud PaaS compute

APEX Load supports registered, provider-managed generator capacity alongside
the existing Kubernetes and static-generator paths:

- Azure Container Apps for warm capacity and Container Apps Jobs for burst.
- AWS ECS Fargate services for warm capacity and `RunTask` for burst.
- Azure Container Apps or ECS Fargate as a single-replica controller host.

Terraform bootstraps networks, identities, databases, caches, registries, and
runtime templates. APEX Load imports the emitted registration manifest and
manages only capacity attached to those templates. A reverse-connect manifest
contains an enrollment token and must be handled as a secret.

## Model

`CloudConnection` stores provider scope and authentication. `ExecutionTarget`
stores placement, connectivity, capacity ownership, archive mode, runtime
profiles, and provider resource references. Existing cluster connections are
transactionally projected into Kubernetes execution targets; legacy cluster
foreign keys remain populated during the compatibility period.

Provider placement is immutable while referenced. Changing an execution
target's runtime resource, connection, connectivity, ownership, archive mode,
or runtime profile requires a new target and an explicit pool/policy repoint.
Likewise, a referenced cloud connection may rotate credentials but may not
change subscription, account, partition, region, or resource-group scope.
Pools must release all active and durable capacity before their target can be
changed.

A domain deployment policy has a default execution target and an allowlist.
Selecting another target is explicit. APEX Load never silently fails a run over
to a different cloud.

PaaS mutations are disabled unless `APEXLOAD_PAAS_COMPUTE_ENABLED=true`.
Registration, validation, and capability discovery remain available while the
feature is disabled.

## Capacity lifecycle

Cloud capacity is asynchronous. A provisioning request records desired,
observed, ready, pending, draining, and failed counts while a provider backend
reconciles the resource. Provider acceptance is not readiness: a generator is
ready only after its provider instance is healthy and either its reverse
session is authenticated or its direct gRPC heartbeat succeeds.

Operator-created provisioning requests reconcile durable warm capacity only.
Burst tasks and Job executions are launched only for a specific run, so every
ephemeral instance has an archive-safe teardown owner. Provider mutations are
serialized per pool to prevent a cancellation from rolling back a newer scale
operation.

A PaaS pool must be scaled to zero and have a zero warm floor before it can be
deleted. This keeps the pool identity available for maintenance reconciliation
and prevents an ECS service desired count or Container App minimum replica count
from becoming an unowned billable resource.

Every burst launch gets a fresh generator ID, launch nonce, and provider
idempotency token. The intent is persisted before the provider call. Provider
resources are tagged with controller, pool, operation, test, generator, and
launch identities so leadership recovery can discover and reap orphans.

Warm and burst capacity have independent owners. Set an owner to `external`
when KEDA, Container Apps scaling rules, or ECS Application Auto Scaling owns
it; APEX Load then refuses a conflicting mutation.

The current reverse enrollment protocol scopes sessions to an execution target,
not a pool. A PaaS execution target therefore binds to exactly one fleet pool;
the API rejects an ambiguous second binding. Kubernetes and static targets keep
their existing multi-pool behavior.

Warm-capacity scale-down transiently cordons every connected generator in the
pool, waits for runs that had already started to reach a durable terminal
archive, and only then lowers provider capacity. Operator-owned cordons are
preserved. This pool-wide drain is intentional because ECS and Container Apps
may choose which service replica to remove.

## Reverse connectivity

Container Apps and Jobs use an outbound generator-to-controller WebSocket on
the controller HTTPS port. The WebSocket is exposed as the existing gRPC byte
stream, so `GeneratorControl` is not reimplemented. The gateway rejects browser
origins, requires a target-scoped enrollment token, and additionally requires
the persisted launch ID and nonce for burst workers. Production generators
refuse plaintext or tokenless reverse connections.

Every reconnect also carries a fresh timestamped session nonce; stale or
replayed enrollment requests are rejected. The reference PaaS stacks explicitly
trust the managed ingress HTTPS scheme header because TLS terminates before the
controller container. Direct and self-managed deployments do not enable that
trust switch and retain the normal trusted-proxy gate.

The outer WSS session is the authenticated encrypted transport for reverse-only
PaaS generators, so the reference stacks mark that channel explicitly rather
than requiring a second TLS handshake inside the WebSocket. That assertion is
rejected for direct or dual-mode generator processes; those paths continue to
require the existing gRPC TLS/mTLS configuration.

Enrollment tokens are returned only during registration or rotation. Only a
SHA-256 verifier is persisted. Provider-bound HTTP handshake identity remains
a future hardening step; provider managed identity/task role is currently
attested independently by the execution-target probe.

Rotation returns the replacement token once and retains the previous verifier
for a 24-hour rolling overlap. Update the Container Apps secret or AWS Secrets
Manager value and roll standing replicas during that window; new burst
instances consume the current provider secret. The controller deliberately
cannot inject the plaintext token through a backend call because it persists
only the verifier.

The Terraform stacks generate a strong enrollment token when one is not
supplied and place it in the generator secret plus a sensitive one-time
registration manifest. This avoids a create-target/apply chicken-and-egg loop;
the manifest contains no cloud credential, but it must still be handled as a
secret because it enrolls generators. AWS additionally supports `auto`,
`direct`, or `reverse` generator connectivity explicitly.

## Data-loss and cost fences

Engine drain and archive drain are separate. Ephemeral compute is terminated
only after the controller-pulled archive is durably terminal. Provider backends
also reject deprovisioning when that terminal marker is absent. This prevents a
successful engine drain from deleting the only copy of generator artifacts.

Maximum instance lifetime is a separate cost fence. On expiry, the reconciler
may terminate an instance whose archive never became durable and records the
archive state as expired. This is intentionally distinguishable from normal
archive-safe cleanup.

Connection tests inspect provider-observed task/app definitions for every
runtime-profile sidecar and separately attest the Azure principal or ECS task
role. Bruno and Java Vuser protocols must be represented by loopback sidecars
in the registered template.

Provisioning happens before run supervision begins, so a 30–120 second cloud
cold start does not consume the supervision deadman TTL.

## PaaS controller requirements

A PaaS-hosted controller requires PostgreSQL, Redis/Valkey, and object-backed
archive/import-asset storage. SQLite and local import assets are refused.
Unsupported process-role splits and controller HA fail closed. The first PaaS
release therefore runs one `all` controller replica; multi-replica API plus a
lease-elected gateway/orchestrator remains future work.

Azure and AWS Terraform can disable AKS/EKS independently and emit an
execution-target registration manifest. The reference PaaS controller
definitions opt into the compute feature and configure durable database,
cache, and object storage.

## Production evidence

The provider contracts, signing, endpoint allowlists, migration behavior,
reverse transport, archive fence, orphan reconciliation, dashboard, OpenAPI,
and Terraform syntax are test-covered offline. Production readiness still
requires opt-in live-cloud evidence for identity permissions, private network
reachability, quota behavior, cold starts, scale, archive durability, restart
recovery, and orphan cleanup. Design targets are not claims of achieved scale.
