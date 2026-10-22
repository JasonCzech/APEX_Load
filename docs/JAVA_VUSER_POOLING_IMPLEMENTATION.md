# Java Vuser pooling: evidence tooling and implementation status

Status: Shared JVMs available for ordinary runs behind an operator switch and a per-script setting (2026-10-15); pilot evidence tooling and experimental dedicated execution-pod integration are implemented, with delegated-Linux pooled evidence and live dedicated AKS qualification still required; no automatically enabled AKS backend

This document tracks the evidence-first implementation of pooled Java Vuser
hosts. Dedicated execution remains the default runtime path. The [shared-JVM operator policy](JAVA_SHARED_JVMS.md) enables ordinary shared runs; explicitly wired pilot profiles remain a separate experimental evidence path. Nothing in
this change raises default process capacity or qualifies an AKS containment backend. The existing performance-evidence report contract is
unchanged; Java evidence has its own `apexload.java-vuser-evidence/v1` schema.

The offline dedicated integration now includes multi-generator sharding, separate
generator/facade and execution-host budgets, cold preparation of all scheduled
lifetime admissions, and live reservation reporting. Down/up schedules consume
fresh identities; closed sessions are never recreated by scaling or rollback.
Review corrections also cover prepared capacity retention, old-peer rejection,
missing placement metadata, and reporting in the compact Configuration view.

## Reference ownership

| Reference | Owns |
| --- | --- |
| [Shared JVMs](JAVA_SHARED_JVMS.md) | Operator policy, thread mode, distributed admission and measured benchmark evidence |
| [Pilot operations](JAVA_PILOT_OPERATIONS.md) | Fixtures, dedicated baselines, pooled pilot evidence, comparison gates and AppCDS |
| [Pooled runtime contracts](JAVA_POOLED_RUNTIME_CONTRACTS.md) | IPC, containment, admission, ownership, lifecycle and review-derived constraints |
| [Dedicated execution contracts](JAVA_DEDICATED_EXECUTION_CONTRACTS.md) | Durable authority, execution-pod supervision and preparation invariants |
| [Broker service](JAVA_BROKER_SERVICE.md) | Operator configuration, API, deployment, facade and distributed placement |
| [Boundary attestor](JAVA_BOUNDARY_ATTESTOR_CONTRACT.md) | External runtime termination and infrastructure-fencing proof |
| [Validation history](archive/JAVA_VALIDATION_2026-09.md) | Dated tests, failures, local evidence and qualification limits |

The pilot-stage gates below describe the manually qualified evidence path. Ordinary runner-qualified shared JVMs, including distributed placement, follow the [current shared-JVM contract](JAVA_SHARED_JVMS.md); its scoped measurements do not qualify an AKS backend.

## Implemented components

| Component | Current boundary |
| --- | --- |
| `apexbench java fixture` and `target` | Reproducible light HTTP and middleware-heavy fixture source, pinned dependency declarations, validated persistent-lifecycle DSL, and a loopback target. |
| `apexbench java baseline` | Linux collector using the ordinary engine → authenticated sidecar → dedicated persistent harness path. Includes aggregate process PSS, cgroup memory, CPU, memory-limit/OOM events, throughput, transaction p95/p99, errors, and dropped results. Requires an already configured contained runner. |
| `apexbench java pilot` | Explicit operator-approved single-group Linux engine → sidecar → pooled-host path, using the same collector as the dedicated baseline. Fresh execution per repetition, independent owner renewal, bounded host diagnostics, and optional observed soak scheduling. No production qualification claim. Requires a reviewed runner manifest and delegated containment. |
| `apexbench java compare` | Strict, bounded report reader and matched single-workload gate evaluator. Requires five complete repetitions, the 30% memory benefit, ≤5% throughput/latency regressions, correctness, achieved offered load, bounded variation, and an observed matching one-hour soak with initialization/closure evidence. A pair never grants production qualification. |
| Operator AppCDS manifest | Opt-in archive validation and launch arguments on dedicated execution paths. Script archive-path restrictions remain enforced. No archive or memory benefit is supplied automatically. |
| Private slot shim and bridge | Per-loader `lr` context and parameters, event destination, and exit option/status. Shared API is independent of application types. Dedicated direct `lr` behavior remains available. |
| Experimental slot worker | Prepare/commit/invoke barrier, construction and lifecycle on one platform thread, one outstanding invocation, sequence replay rejection, independent interruption, permanent closure. Experimental HTTP and programmatic pilot integration; it is not a containment boundary. |
| Experimental pooled host | Shared dependency loader with private script classes; generation checks; commit barrier; sealing and peer-preserving cooperative closure; failed initialization and unexpected invocation failures request whole-host reaping. Component tests exercise two real Java slots. The experimental IPC entry point is separate from the production runner. |
| Experimental IPC v3 | Authenticated, bounded Go/Java frames and multiplexed transport; readiness/open/commit/invoke/interrupt/close/health; two-second cancellation grace; response socket separated from native stdout; bounded Java-stream host diagnostics. HTTP invocation requires operator opt-in; ordinary distributed shared execution follows the shared-JVM contract. |
| Run/group host ledger | In-memory prepare/ready/commit reservations bound to one owner epoch and configuration; actual host/slot/memory accounting; permanent retirement, expiry, quarantine, and identity-checked positive termination confirmation. |
| Shared execution-host capacity | Atomic host/Vuser/memory admission across run/group ledgers and the dedicated sidecar path; charges survive draining, owner retirement, and uncertain cleanup. Programmatic wiring and an explicit local pilot manifest; no production pooled endpoint or durable broker authority. |
| Pooled run ownership | Programmatic qualified-run handle scoped to one execution/group and the sidecar's generator epoch; independent renewable lease; takeover/expiry/shutdown cleanup; duplicate-run rejection and retained quarantine. An opt-in HTTP contract wraps this handle; generator RPC integration remains pending. |
| Versioned pooled lifecycle control | Explicitly configured authenticated HTTP claim/renew/reserve/abort/close operations, plus separately enabled cold preparation and commit/slot closure. Run tokens, bounded replay, typed slot context, runner-owned event files, frozen resource metadata, and strict client validation. Separately enabled invocation adds at-most-once sequences and bounded attributed events. No automatic mode selection. |
| Managed pooled client | Explicit, frozen-owner run handle with independent group renewal, bounded cold preparation, a separate commit barrier, and non-reusable slot handles. Enforces sequence/lifecycle rules, terminal uncertainty, and positive cleanup receipts. Used by the explicit programmatic engine pilot; normal engine and generator selection remain dedicated. |
| Pooled protocol adapter | Adapts a prepared slot to the engine's streaming result path, with qualified target binding, bounded parameters, strict event decoding, transaction attribution, and authoritative exit status/control. Tested through the Vuser loop and managed HTTP client; normal engine factories and generator assignment staging do not select it yet. |
| Programmatic engine pilot | `NewJavaPooledPilot` freezes an operator-approved DSL/runtime pair, stages the full peak before starting duration/ramp, commits before lifecycle calls, and maintains independent fenced owner/group leases. Single-use, closed workload, exact init/action/end methods. Used by the explicit `apexbench java pilot` command; no distributed generator activation. |
| Host supervisor and local backend | Join the ledger to IPC v3 and strong process containment; independent lease monitoring, all-host commit barrier, bounded native output, cancellation receipts, and exactly-once cleanup. Internal components only: no production sidecar endpoint, engine lease protocol, or durable AKS broker is enabled. |
| Operator pilot manifest | Bounded, strict, versioned local file binding one reviewed qualification, resolved launch request, approved DSL digest, stage permissions, and a shared host/Vuser/memory budget. Runner loads it before opening its listener; Linux-only and disabled when the environment variable is absent. |
| Pilot qualification registry | Operator-owned evidence records bound to inspected runtime/artifact/resource identities; frozen dedicated/pilot selection, stable fallback reasons, and pre-launch revalidation. Programmatic interface and operator manifest loading; no production qualification, registry configuration endpoint, or automatic approval is supplied. |


## Remaining implementation and release gates

1. **Limited pooled runner:** run the implemented manifest/evidence collector on a
   qualified delegated-Linux deployment; obtain five matched comparisons and an
   actual one-hour soak with ramp changes and permanent closure for both fixtures.
   Automated scheduling, host diagnostics, host GC pause capture, and representative
   loader fixtures are implemented. General distributed integration remains gated
   by measured synthetic success, and arbitrary dependency compatibility still
   requires representative execution and operator approval.
   The slot worker does not prove arbitrary background work stopped and must not
   be treated as a reusable session or process containment mechanism.
2. **Independent AKS dedicated track:** the experimental dedicated backend,
   authenticated broker and loopback facade, PID 1 supervisor, durable reservation
   and quarantine, prepare/ready/commit staging and deployment scaffold are
   implemented; see [the broker service guide](JAVA_BROKER_SERVICE.md) and
   [dedicated execution contracts](JAVA_DEDICATED_EXECUTION_CONTRACTS.md).
   Release still requires a qualified independent runtime attestor and infrastructure
   fencing, plus live network, descendant-process and node-failure evidence.
   Local component tests do not establish AKS containment or performance; no backend
   is automatically enabled.
3. **After the synthetic gate:** normal-engine mode selection, distributed pooled placement and reservations,
   Windows execution/containment validation, qualified AKS pooling, preflight and
   reporting UI/API changes, real-workload qualification, and production release.

The default process limit remains 25. The evidence pilot requires an explicit operator manifest. Ordinary shared runs use the separate [runner-qualified operator policy](JAVA_SHARED_JVMS.md), with per-script selection and the documented distributed admission limits. Neither path enables pooling by default.
Failure of its gate must not cancel the independent AKS dedicated track.


## Section navigation

Previous section bookmarks remain below. Repository links point directly to the focused references.

<a id="building-and-checking-fixtures"></a>

- [Building and checking fixtures](JAVA_PILOT_OPERATIONS.md#building-and-checking-fixtures)

<a id="collecting-a-dedicated-baseline"></a>

- [Collecting a dedicated baseline](JAVA_PILOT_OPERATIONS.md#collecting-a-dedicated-baseline)

<a id="explicit-local-pooled-pilot"></a>

- [Explicit local pooled pilot](JAVA_PILOT_OPERATIONS.md#explicit-local-pooled-pilot)

<a id="gate-evaluation"></a>

- [Gate evaluation](JAVA_PILOT_OPERATIONS.md#gate-evaluation)

<a id="operator-appcds-archives"></a>

- [Operator AppCDS archives](JAVA_PILOT_OPERATIONS.md#operator-appcds-archives)

<a id="experimental-ipc-v3-transport"></a>

- [Experimental IPC v3 transport](JAVA_POOLED_RUNTIME_CONTRACTS.md#experimental-ipc-v3-transport)

<a id="supervisor-and-local-containment-adapter"></a>

- [Supervisor and local containment adapter](JAVA_POOLED_RUNTIME_CONTRACTS.md#supervisor-and-local-containment-adapter)

<a id="shared-runner-admission"></a>

- [Shared runner admission](JAVA_POOLED_RUNTIME_CONTRACTS.md#shared-runner-admission)

<a id="pooled-run-ownership"></a>

- [Pooled run ownership](JAVA_POOLED_RUNTIME_CONTRACTS.md#pooled-run-ownership)

<a id="versioned-pooled-lifecycle-control"></a>

- [Versioned pooled lifecycle control](JAVA_POOLED_RUNTIME_CONTRACTS.md#versioned-pooled-lifecycle-control)

<a id="invocation-and-event-capture"></a>

- [Invocation and event capture](JAVA_POOLED_RUNTIME_CONTRACTS.md#invocation-and-event-capture)

<a id="managed-run-and-slot-client"></a>

- [Managed run and slot client](JAVA_POOLED_RUNTIME_CONTRACTS.md#managed-run-and-slot-client)

<a id="pooled-protocol-and-vuser-results"></a>

- [Pooled protocol and Vuser results](JAVA_POOLED_RUNTIME_CONTRACTS.md#pooled-protocol-and-vuser-results)

<a id="programmatic-engine-staging-pilot"></a>

- [Programmatic engine staging pilot](JAVA_POOLED_RUNTIME_CONTRACTS.md#programmatic-engine-staging-pilot)

<a id="review-fixes-and-next-phase-boundaries"></a>

- [Review fixes and next-phase boundaries](JAVA_POOLED_RUNTIME_CONTRACTS.md#review-fixes-and-next-phase-boundaries)

<a id="verification-of-this-foundation"></a>

- [Verification of this foundation](archive/JAVA_VALIDATION_2026-09.md#verification-of-this-foundation)

<a id="durable-dedicated-broker-state-and-explicit-pilot-diagnostics"></a>

- [Durable dedicated broker state and explicit pilot diagnostics](JAVA_DEDICATED_EXECUTION_CONTRACTS.md#durable-dedicated-broker-state-and-explicit-pilot-diagnostics)

<a id="host-gc-diagnostics-observed-soaks-and-kubernetes-transport"></a>

- [Host GC diagnostics, observed soaks, and Kubernetes transport](JAVA_DEDICATED_EXECUTION_CONTRACTS.md#host-gc-diagnostics-observed-soaks-and-kubernetes-transport)

<a id="dedicated-execution-pod-supervisor-and-broker-control-adapter"></a>

- [Dedicated execution-pod supervisor and broker control adapter](JAVA_DEDICATED_EXECUTION_CONTRACTS.md#dedicated-execution-pod-supervisor-and-broker-control-adapter)

<a id="dedicated-preparation-waves"></a>

- [Dedicated preparation waves](JAVA_DEDICATED_EXECUTION_CONTRACTS.md#dedicated-preparation-waves)

<a id="runnable-experimental-dedicated-broker"></a>

- [Runnable experimental dedicated broker](JAVA_DEDICATED_EXECUTION_CONTRACTS.md#runnable-experimental-dedicated-broker)

<a id="local-linux-evidence-collected-during-implementation"></a>

- [Local Linux evidence collected during implementation](archive/JAVA_VALIDATION_2026-09.md#local-linux-evidence-collected-during-implementation)

<a id="shared-jvms-runner-qualified"></a>

- [Shared JVMs (runner-qualified)](JAVA_SHARED_JVMS.md#shared-jvms-runner-qualified)

<a id="loadrunner-thread-mode"></a>

- [LoadRunner thread mode](JAVA_SHARED_JVMS.md#loadrunner-thread-mode)

<a id="shared-jvms-across-generators"></a>

- [Shared JVMs across generators](JAVA_SHARED_JVMS.md#shared-jvms-across-generators)

<a id="shared-jvm-benchmark-2026-10-15"></a>

- [Shared JVM benchmark (2026-10-15)](JAVA_SHARED_JVMS.md#shared-jvm-benchmark-2026-10-15)
