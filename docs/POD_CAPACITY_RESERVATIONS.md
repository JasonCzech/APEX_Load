# Peak-Based Pod Capacity Reservations

Peak reservations are logical admission budgets inside a generator process.
They do not create separate containers, CPU quotas, memory heaps, or garbage
collectors for individual tests. Live CPU and memory utilization remain separate
from reserved capacity.

## Enablement

1. Upgrade generators first, then controllers. Generators advertise
   `pod_capacity_v1`; old generators cannot accept partitioned launches.
2. Drain existing unreserved tests. A generator with legacy assignments is
   reported as unreconciled until those assignments finish.
3. Configure network capacity, maximum VUsers, and headroom in Capacity Planning.
   CPU and memory use the lower of the configured budget and each pod's reported
   cgroup limits. Missing resource limits
   make partitioned capacity unavailable. Network capacity remains a configured
   estimate, not a measured link guarantee.
4. Enable **Enforce peak pod reservations** in the generator capacity settings
   (`capacity_planning.pod_reservations_enabled`). It defaults to false for
   rolling-upgrade compatibility. Deploy the updated KEDA manifests as well.

Enforcement cannot be disabled while the ledger holds reservations. Drain tests
and wait for confirmed release before disabling it.

The default sharing mode is `shared`. Set test-level
`assignment.sharing_mode` to `dedicated` to claim the entire schedulable budget
of each selected pod. Dedicated execution isolates tests at pod level, not from
other pods on the same node. Existing domain, pool, entitlement, and project
restrictions still apply.

## Accounting

The scheduler checks CPU, memory, network, total VUsers, and per-protocol VUsers
independently. It applies the configured headroom once, then reserves each
assigned group's estimated peak. Group peaks are conservatively summed even
when their scheduled peaks occur at different times. Model estimates are not
published benchmark guarantees.

Adaptive tests require an explicit maximum VUser count. Because adaptive group
weights can change, v1 reserves that maximum for each load-bearing group and
sums those group peaks; observer groups retain their configured peaks. This can
be deliberately more conservative than a fixed weighted split of the maximum.

With 20% headroom, the schedulable budget is 80% of raw capacity. A test using
40% of raw capacity in its limiting dimension therefore shows **50% reserved /
50% remaining**, with **20% headroom excluded**. A different workload must fit
every remaining dimension; the headline percentage alone cannot establish fit.

Reservations survive ramp changes, pause, and teardown. Runtime increases
acquire a revisioned capacity delta before changing VUsers. Model coefficients
stay frozen for the run. Failed changes roll back the capacity delta only after
the generator confirms the earlier envelope is safe; uncertain responses retain
capacity until reconciliation.

The launch preview reports each proposed pod's before/after budget. It does not
reserve capacity. Launch revalidates and atomically records the full placement;
capacity contention queues the test. Compatible pods are packed before idle
pods are used, subject to existing placement preferences.

## Recovery And Operations

The durable `pod_capacity_reservations` ledger records pending, committed, and
releasing claims. Storage transactions use the existing execution-owner and
capacity-settings fences. The generator independently enforces the envelope and
group peaks under its admission lock. No test IDs from this ledger are exposed
in the aggregate capacity display.

Each launch attempt also has a unique reservation identity. Delayed cleanup from
an earlier queued attempt cannot release a later attempt's capacity.

Release requires confirmed execution drainage or an incarnation change for a
terminal/releasing claim. A predicted end time never releases capacity. A stale
or unreconciled snapshot blocks new partitioned admission. The inventory refresh
worker retries release after completion and controller recovery.

Evacuation reserves replacement peaks before staging replacement work, retaining
the source claim until its execution is fenced. A group whose future peak cannot
be mapped to a replacement is refused rather than losing its capacity guarantee.

The KEDA `apexload_pod_reserved_replica_floor` metric protects reserved StatefulSet
ordinals from scale-down; active VUser scaling demand also includes held peaks.
`apexload_pod_capacity_demand` reports reserved and queued resource vectors for
capacity monitoring (CPU cores, memory MiB, network Mbps, and VUser counts).
Non-ordinal targets use the configured fleet size as a conservative floor.
Managed pool scale-down waits for retained reservations. External/manual pod
deletion is outside this logical admission mechanism.

## Validation

Focused tests cover capacity arithmetic, half-capacity concurrency, duplicate
claims, dedicated exclusion, active-release rejection, immutable model versions,
and transaction rollback. Run the repeatable concurrent SOAP probe with:

```sh
APEXLOAD_POD_CAPACITY_SOAK=1 go test ./pkg/distributed/generatorrpc -run '^TestPodCapacityConcurrentSOAP$' -count=1 -v
```

This probe runs two SOAP tests with eight active VUsers each, 2 KiB responses,
and independently held peaks for 30 seconds. Its heap samples describe the local
test process; they do not validate a 10,000-VUser production density or prove the
absence of a long-duration memory leak.
