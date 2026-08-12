# Generator run supervision

Distributed runs are fail-closed. Every current controller assignment carries a
random run token and every current generator requires a renewable lease for that
exact test, execution generation, and token. If renewals stop, the generator
fences the run locally; it does not wait for a controller command that may never
arrive.

The shipped contract is:

- lease TTL: `60s`
- controller renewal interval: `10s`
- graceful abandonment drain: `5s`
- hard cancellation grace: `1s`, followed by process self-termination when work
  still cannot be stopped

The controller accepts TTLs from 30 to 60 seconds and requires the generator's
granted TTL to cover at least three configured renewal intervals. The generator
accepts abandonment drains from 0 to 10 seconds. Kubernetes, Helm, and Compose
ship the 60/10/5 profile above. There is no legacy opt-out: a generator that does
not advertise `run_supervision_v1` is excluded from new distributed runs.

`assignment.tolerate_generator_loss` is a separate evacuation feature. It is
rejected unless the controller uses HA execution leadership and the leadership
lease is backed by the same durable test store. A single controller cannot
prove that replacement shards and a lost source belong to one execution epoch.

## Configuration

| Process | Setting | Shipped value |
| --- | --- | --- |
| Controller | `APEXLOAD_GENERATOR_RUN_SUPERVISION_TTL` | `60s` |
| Controller | `APEXLOAD_GENERATOR_RUN_SUPERVISION_RENEW_INTERVAL` | `10s` |
| Generator | `APEXLOAD_GENERATOR_RUN_SUPERVISION_TTL` | `60s` |
| Generator | `APEXLOAD_GENERATOR_ABANDON_DRAIN` | `5s` |

Keep the controller TTL equal to the generator TTL. A generator may grant less
than requested, but the controller rejects that claim when it would leave fewer
than three renewal opportunities. Generator fencing state and terminal evidence
must use the retained per-ordinal state volume; Compose uses `generator-data`
and the production StatefulSets use `generator-state` PVCs.

Before acknowledging the first supervision claim in an execution generation,
the generator durably arms a restart fence in that execution-epoch state. A
clean exact terminal transition disarms it only after every supervised
assignment and retained drain in the generation has quiesced. Semantic lease
expiry keeps it armed even when cooperative cancellation succeeds. After a
restart, an armed positive generation cannot be revalidated by its old owner; a
higher HA generation is required. Generation zero has no owner epoch, so an
armed generation-zero fence rejects new staging and commits until a positive
generation is durably acquired. Already-active unrelated generation-zero runs
in the original process may continue renewing while they quiesce normally.

Helm includes the supervision values in the controller pod-template checksum,
so changing either controller value rolls the Deployment. The static Kustomize
overlays reference an ordinary ConfigMap; after a config-only apply, restart and
wait for the controller Deployment before admitting tests:

```bash
kubectl -n <namespace> rollout restart deployment/apexload-controller
kubectl -n <namespace> rollout status deployment/apexload-controller
```

Changing the generator TTL or drain edits its StatefulSet pod template and
therefore rolls the generator fleet. Apply the no-active-tests upgrade gate
below whenever either side of this fail-closed contract changes.

## Upgrade and rollback gate

This contract is intentionally incompatible with tokenless controller or
generator releases. Use a maintenance window for the first upgrade:

1. Close run admission and let every active distributed test reach a terminal
   state. Do not rely on a rolling mixed fleet for active traffic.
2. Upgrade the complete generator fleet first while the old controllers remain
   admission-closed. Wait for every desired generator pod to become ready, then
   inspect system inventory and confirm every schedulable generator advertises
   `run_supervision_v1`.
3. Upgrade every controller only after the generator verification succeeds.
   Keep admission closed until the controller rollout is complete and healthy.
4. Execute the connectivity-loss drill below, then reopen run admission.

Use the same no-active-tests gate when rolling back to a release that does not
implement the contract, in reverse order: roll back every controller first,
then roll back the complete generator fleet, verify both rollouts, and only then
reopen admission. Rolling back only one side leaves the fleet deliberately
unavailable for new runs.

## Probes and restart behavior

Kubernetes readiness and liveness probes use the generator's dedicated gRPC
health listener. They are useful for removing a draining process and restarting
a failed process, but they do not prove controller connectivity and are not the
runaway-load safety mechanism. Adding a controller-connectivity healthcheck
would be misleading and could restart healthy generators during a transient
control-plane interruption before their per-run lease decision is due.

Compose relies on `restart: unless-stopped` after the generator exits. It does
not add a second connectivity healthcheck. The renewable run lease remains the
authoritative signal in both environments.

## Acceptance drills

Run these against a non-production target before reopening admission:

1. Start a load test and confirm each assigned generator advertises
   `run_supervision_v1`.
2. Drop only controller-to-generator control traffic while leaving the target
   system reachable. Do not delete the generator pod.
3. Confirm the generator logs `generator_run_supervision_expired` after the TTL,
   stops producing target traffic, and completes the five-second graceful drain.
   If a VUser refuses to stop, confirm `generator_run_abandon_escalated` and a
   non-zero process exit. With the shipped profile, target traffic must cease no
   later than roughly 73 seconds after the last successful renewal, including
   the external runner's owner-lease and cleanup-exit backstop.
4. Restore connectivity and confirm the replacement pod is ready. The old run
   token must not revive the fenced incarnation.
5. Repeat with one generator isolated in a multi-generator run. With loss
   tolerance disabled, the controller fails closed. With HA and explicit loss
   tolerance enabled, replacement work must not start until the source's
   process-fence deadline has passed. That deadline uses the maximum accepted
   10-second drain plus the longer post-drain containment path (currently the
   external runner's five-second owner lease and two-second cleanup exit), plus
   a scheduling margin.
6. In HA, force leadership loss and confirm the old leader cancels and joins its
   renewal keeper and sends token-fenced stop requests before closing clients.
7. After a hard escalation, restart both the generator and controller. Query
   `GET /api/v1/system/inventory` and confirm that generator's
   `safety_exits` still contains the exact test and execution generation with
   `lifecycle_state: FAILED` and `reason: controller_supervision_lost`. Repeat
   the incident with another run and confirm both entries survive both restarts.
   The generator retains a bounded journal of the latest 64 token-free incidents;
   `last_safety_exit` is only the rolling-upgrade projection of its newest entry.
   This heartbeat evidence is authoritative when an interrupted generator archive
   could not reach its normal finalization path.

Alert on either safety counter:

```promql
increase(apexload_generator_run_supervision_expirations_total[5m]) > 0
increase(apexload_generator_run_supervision_escalations_total[5m]) > 0
```

The same counters are available on each connected generator entry in the system
inventory response. An expiration is an operational incident even when the
automatic fence succeeds; an escalation means the process safety exit was
needed.
