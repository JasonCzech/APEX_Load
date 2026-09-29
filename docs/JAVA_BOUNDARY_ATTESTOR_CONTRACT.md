# External Java boundary attestor contract

Status: implemented broker client and component tests; no deployed external
attestor or AKS runtime has been qualified. This is an internal service contract,
not a controller API or an execution-pod endpoint. The selected architecture uses
an external runtime attestor with infrastructure fencing.

## Authority and transport

The broker calls `POST /v1/java-boundary/attest` on an operator-configured HTTPS
origin. TLS 1.3, hostname verification, a pinned attestor SPIFFE URI, and a distinct
broker client certificate are mandatory. The attestor URI path is
`/java-boundary-attestor/<identity>`. Execution-host identities cannot act as the
attestor or the broker. The external service must verify the exact authorized
broker URI in addition to certificate trust. No Kubernetes token, cloud
credential, generator credential or user parameter is sent by this client.

Known-container attestation **reads evidence; it does not authorize or initiate
machine fencing**. Infrastructure fencing is a separately governed operation
of the external service. A missing pod or a failed lookup must never
automatically shut down a shared node. The separate no-start operation below
establishes an exact admission fence. The service must retain authoritative
boundary-to-node/machine provenance outside disposable pods, independently of
Kubernetes object lifetime.

Known-container requests and responses use contract
`apexload.java-boundary-attestor/v1`. Unstarted creation intents use the
separate `apexload.java-no-start-attestor/v1` contract below.
`qualification_sha256` binds the operator-reviewed runtime/fencing implementation
and deployment boundary. Successfully establishing TLS is not qualification.
The client sends one request, follows no redirects, uses no environment proxy,
bounds the response to 64 KiB, and enforces an operator timeout up to 30 seconds.
A new unpredictable `request_id` is generated for every lookup.

## Request

```json
{
  "contract": "apexload.java-boundary-attestor/v1",
  "request_id": "<unique challenge>",
  "qualification_sha256": "<64 lowercase hex characters>",
  "boundary": {
    "namespace": "java-execution",
    "name": "java-<host id>",
    "uid": "<exact Kubernetes pod UID>",
    "container_id": "containerd://<exact container ID>",
    "host_id": "<host id>",
    "broker_epoch": "<epoch that created this host>"
  }
}
```

Every boundary field and identity must be echoed exactly. The epoch identifies
the original host; a new broker may clean up a previous epoch but never adopt its
Java state. A pod name, current node name, application-reported process ID or a
replacement container cannot substitute for these identities.

## Response and proof

HTTP 200 returns the request fields plus `result`. `result: "unproven"` contains
no proof. It leaves the host and memory reservation charged. Transport errors,
unknown fields/results, malformed or oversized responses, non-200 statuses, stale
challenges and identity mismatches also leave cleanup unproven.

`result: "terminated"` requires a `proof` object:

```json
{
  "kind": "infrastructure_fence",
  "scope": "pod_container_descendants",
  "evidence_sha256": "<hash of retained external evidence>",
  "observed_at": "<RFC3339 timestamp of latest positive verification>",
  "expires_at": "<RFC3339 timestamp>",
  "node_uid": "<immutable Kubernetes node UID>",
  "machine_id": "<provider resource locator>",
  "machine_uid": "<immutable backing machine identity>",
  "fence_id": "<retained infrastructure-fence receipt identity>"
}
```

The alternative kind `runtime_boundary` must prove the entire exact container
boundary empty, including detached descendants, on the qualified runtime. Its
proof omits node/machine/fence fields. A stopped root JVM, deletion acknowledgment,
Pod NotFound, Node NotReady, lease expiry or an accepted stop request is not proof.

An infrastructure proof must establish that the **original immutable machine**
cannot continue or resume the boundary. A reusable VM/VMSS resource locator alone
is insufficient; `machine_uid` is separate for that reason. The attestor must
validate its durable mapping from the requested pod/container to these immutable
identities and retain the actual fencing receipt. The broker does not derive
this mapping from a missing or potentially replaced Kubernetes object.

Verification must be within the previous 30 seconds (up to two seconds of
positive clock skew is allowed). Expiry must be in the future and at most 30
seconds after verification. A longstanding fence may be reverified and freshly
attested; its original operation timestamp must not be presented as a new fence.
The scope always covers the pod/container and all descendants. The client rejects
root-process-only evidence and cannot reuse an earlier response for a new lookup.

## Integration and qualification

`ExternalAttestor` implements the broker's `TerminationAttestor` interface. Pass
it to `NewKubernetesBackend`; reconciliation consumes it independently of pod
existence, user invocations and generator leases. A positive result releases only
the exact durably retained UID/container reservation after a UID-preconditioned
deletion request and an authoritative Kubernetes API read observes the pod name
absent. DELETE 200/202/404 alone does not release the host or pod-capacity charge.

Tests exercise real mutual TLS, both proof kinds, malformed/future/stale/replayed
proofs, host-role rejection, response limits, exact identity checks, and cleanup
of a missing pod with retained capacity until infrastructure proof arrives.
Production enablement additionally requires external-service qualification on
the selected AKS runtime: detached descendants, node loss/partitions, delayed
operations, immutable machine mapping, infrastructure fencing and broker restart.
No test substitute or Kubernetes-only fallback is enabled by this client.

## Creation intents with no known container

An ordinary rejected create, ambiguous create response, or pending pod may have
no container ID. None of those states proves that user code never ran. The broker
retains the reservation and asks `POST /v1/java-boundary/attest-never-started`
for a separate proof. The request binds the exact durably stored creation intent:

```json
{
  "contract": "apexload.java-no-start-attestor/v1",
  "request_id": "<fresh challenge>",
  "qualification_sha256": "<64 lowercase hex characters>",
  "intent": {
    "namespace": "java-execution",
    "name": "java-<host id>",
    "uid": "<exact pod UID, or empty if no UID was returned>",
    "host_id": "<host id>",
    "broker_epoch": "<original broker epoch>",
    "pod_policy_sha256": "<hash of the exact attempted pod request>"
  }
}
```

The attestor must return `result: "never_started"` with a fresh proof of kind
`creation_fence`, scope `pod_never_started_object_removed`, a retained evidence
digest and a `creation_fence_id`. The earlier `pod_never_started` scope is
insufficient because an unstarted pending pod can still consume pod quota.
That fence must durably prevent this request from being
admitted or started **after** the proof, including an in-flight or delayed create.
The attestor must independently examine a complete admission and runtime history
for this namespace, pod name, host/epoch annotation, policy digest and, when
known, pod UID. It must prove no container, replacement container, or descendant
ever started. If any relevant event is missing, ambiguous or could still start,
it must return `unproven` without a proof. A Kubernetes 403/404, deletion
acknowledgment, pending phase, empty `containerStatuses`, or a node status alone
is insufficient. The external attestor must arrange the admission fence with
the cluster; the broker's lookup does not initiate infrastructure fencing.

After establishing the admission fence, the attestor must also prove that no
pod object for this creation intent remains and cannot reappear. If it finds an
object, including when the broker never received its UID, it must discover and
retain that exact UID, delete using a UID precondition, and wait for an
authoritative no-object observation before issuing positive proof. A deletion
acknowledgment or a stale cache read does not qualify. The retained evidence
digest must cover any discovered UID, the UID-preconditioned deletion receipt
when the attestor deleted an object, the complete object history, and the
post-fence absence observation. If the attestor cannot obtain this evidence,
it must return `unproven` and the broker retains the host, memory and
pod-capacity charge.

For a known UID, the broker first requests deletion with that UID precondition,
then verifies the external proof of removal. For an unknown UID it cannot issue
an exact deletion and relies on the attestor's discovery, exact cleanup,
admission fence and complete history. The response must echo every request
field and challenge; the same
mTLS, freshness, bounds and replay checks apply. Only then may the broker mark
the exact durable reservation reaped. No-start qualification requires testing
rejected and ambiguous creates, delayed creation, pod disappearance, broker
restart, and pending pod cancellation on the selected runtime. Until the
external service supports this contract, these reservations remain charged.
