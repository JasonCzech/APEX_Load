var e=`# Distributed rendezvous

Native scripts can synchronize a fixed group of VUsers across load generators.
This is an explicit action scope; existing rendezvous actions with omitted scope
or \`"scope": "local"\` retain their current behavior.

\`\`\`json
{
  "type": "rendezvous",
  "name": "Wait for checkout",
  "params": {
    "scope": "distributed_group",
    "key": "checkout",
    "timeout_ms": 30000,
    "on_timeout": "fail"
  }
}
\`\`\`

The default target is every VUser admitted to this group. Set either \`count\` or
\`percent\` to release after a smaller number of distinct participants arrives.
Percentages round up against the frozen group population. Repeated arrival
messages from the same VUser count once. Each encounter with the action advances
that VUser's round for its static key. Keys must be unique in the script.

The timeout starts at the first arrival at the controller. \`on_timeout: "fail"\`
returns an action error; \`"continue"\` permits the action to continue only after
the controller has recorded a timeout outcome. A transport failure or unavailable
coordinator returns an error, even with \`"continue"\`; it never fabricates a
successful release. Existing script error policy decides how an action error
affects the remaining script.

## Admission and lifecycle

Use distributed rendezvous in the main sequential iteration actions of a fixed,
closed workload. Init, teardown and parallel lanes are rejected. Adaptive goals,
traffic profiles, staged groups, open workloads and JMeter source execution plans
are unsupported. Manual population changes are refused while the group uses a
distributed rendezvous.

Ordinary local and sidecar Java groups can place a rendezvous between their
method actions. Experimental pooled and dedicated broker Java entry points
retain their strict init/action/end shape and reject additional DSL barrier
actions; native Java table callbacks do not change that admission contract.

The controller freezes the actual launch shard membership and global VUser IDs.
Only generators advertising \`distributed_rendezvous_v1\` may receive the work.
Their authenticated rendezvous channels must be ready before execution starts.
The same channel works over direct and reverse generator connections.

Losing a generator never reduces the target. The configured run failure policy
and barrier timeout apply. Participant evacuation/replacement is rejected because
this implementation does not checkpoint the VUser's script position and round.

The controller persists arrivals and terminal outcomes before returning them.
An HA successor restores the frozen membership, rounds, deadlines and outcomes
under a new fenced execution term. Retrying an arrival after a lost reply returns
the saved outcome. A stale controller or superseded generator stream cannot
release the barrier. A persistence error makes that coordinator unavailable;
it does not roll forward from uncertain in-memory state.

## Bounds and diagnostics

- Up to 100,000 participants per group and 32 static keys per script.
- At most 32 active rounds per group and 32 controller group coordinators.
- Timeout from 1 through 120,000 milliseconds.
- Generator arrivals are aggregated in batches of at most 256. There is one
  exchange loop per active local round, rather than one RPC per waiting VUser.
- At most 64 recent terminal rounds per key are retained, with a two MiB snapshot
  limit. Requests for retired outcomes fail explicitly.

Rendezvous has its own bounded channel and state budget; waiting at a barrier
does not consume live-table waiter or receipt slots. Timeout errors name the
action key and arrived/target counts. The persisted state contains membership and
round metadata, without script data values.

For mutable shared script data, see [Live data tables](LIVE_DATA_TABLES_RUNTIME.md)
and [Native script data services](NATIVE_SCRIPT_DATA_SERVICES.md).
`;export{e as default};