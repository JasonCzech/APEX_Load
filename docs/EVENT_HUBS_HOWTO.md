# Event Hubs load, observer probes and processors

APEX's native `eventhubs` protocol produces events and provides a read-only consumer observer. Opt-in processor mode balances partition ownership and persists fenced Blob checkpoints. Live Azure acceptance remains pending. The default binary is mock-only; build approved generator images with `-tags eventhubs_real` for the Azure SDK transport. Kafka endpoints use the separate `kafka_real` build and `config.kafka.profile: eventhubs`.

Configure `config.eventhubs` with the namespace, event hub, explicit `mock: false`, authentication type and an isolated observer consumer group. Use project secrets for connection strings. For workload identity, configure the existing Azure identity facilities and approve the exact namespace through `APEXLOAD_AZURE_MESSAGING_ALLOWED_NAMESPACES`; unapproved token destinations fail closed. Give the identity only the send/receive access needed by the approved workload.

Generator deployment defaults are optional:

```yaml
env:
  - name: APEXLOAD_EVENTHUBS_AUTH
    value: aad
  - name: APEXLOAD_EVENTHUBS_PROBE_CONSUMER_GROUP
    value: apex-observer
  - name: APEXLOAD_AZURE_MESSAGING_ALLOWED_NAMESPACES
    value: example.servicebus.windows.net
```

Explicit script `auth_type` and `consumer_group` values take precedence. Defaults do not enable a real adapter, create a consumer group, provision a namespace, or relax outbound policy. Use an existing, dedicated consumer group so load-test probes do not compete with production consumers. Keep namespace/identity configuration consistent on every assigned generator.

Consumer age and offset lag carry availability separately from value. A received event with observed zero backlog is valid evidence; a missing timestamp or unavailable backlog probe is not zero. Exact named-transaction terminal rules now accept `max_consumer_lag_ms` and `max_consumer_offset_lag`, through the existing API and CLI `--transaction-rule` contract. Every matched business transaction must carry the relevant observed measurement; mixed producer/consumer transactions and old archives may be `not_evaluated`.

```text
--transaction-rule 'Consume|max_consumer_offset_lag|lte|100|fail'
```

Use an automatically measured consume action or a business transaction that actually retains its telemetry. These rules evaluate closed archives and flow through existing JSON/JUnit/report output; they do not add live automatic stop conditions. Provider-wide acknowledgement coverage and live Azure acceptance remain separate work. Mock tests are not evidence of real namespace readiness.

## Durable processor consumers

Set `config.eventhubs.consumer_mode: "processor"`, `auth_type: "aad"`, `mock: false`, an explicit namespace/event hub/consumer group, and `checkpoint_container_url: "https://account.blob.core.windows.net/checkpoints"`. Do not configure a fixed partition. Use a dedicated, existing consumer group and Blob container. Configure `APEXLOAD_EVENTHUBS_CHECKPOINT_CONTAINERS` on every assigned generator with a comma-separated list of exact approved HTTPS container URLs, alongside namespace identity approval. Query strings, embedded credentials and cross-container destinations fail closed. Grant the workload identity scoped Blob data access to that container and receive access to the approved Event Hub. Storage approval grants no Azure role automatically.

Generators advertise `eventhubs_processor_checkpoints_v1` only in the real build; older/default peers cannot execute processor scripts. Initialization stages configuration without constructing Azure credentials, opening clients or reading checkpoints. The first consume after Start creates the SDK processor with prefetch disabled. Preparation remains traffic-free.

Ownership and checkpoint share one conditional Blob metadata record per namespace/hub/group/partition under `apexload-processor-v1/`. ETag checks plus ownership incarnation tokens prevent stale consumers, including an A→B→A ownership transition, from advancing a checkpoint. Restarted processors load durable checkpoints before consuming. Extraction and assertions must succeed before checkpointing. A rejected action or failed checkpoint retains the event for retry; three consecutive checkpoint failures terminate that processor visibly. Delivery is at least once: interrupted actions and process crashes can repeat an event. Successful action acknowledgement confirms durable checkpoint completion, not downstream exactly-once effects.

The runtime admits at most four processors and 32 active partition workers per process, with 1–32 partitions per Event Hub. Each worker holds one event; retained body/properties are bounded to 1 MiB, scalar property values to 64 KiB, and Blob responses to 2 MiB. Limit failures are explicit. Teardown cancels workers and closes clients with bounded cleanup. Processor enqueue age is measured when available; processor offset backlog is unavailable rather than fabricated as zero. The default observer probe behavior remains available.

Local tests exercise ownership takeover, ABA fencing, restart checkpoint recovery, rejected/failed delivery, cancellation, storage failure, destination limits and the actual Blob SDK's conditional HTTP writes. An approved disposable Azure namespace/container is still required for live rebalance and crash/restart acceptance before production readiness is advertised.

## Fixed-partition exclusive consumption

For an explicitly owned partition, set `consumer_mode: "exclusive"`, `mock: false`, a dedicated `consumer_group`, `event_hub`, `partition_id`, and `owner_level` in `config.eventhubs`. The epoch must be an exact integer from 0 through 9007199254740991; zero is explicit ownership, while an omitted value is never treated as zero. Existing AAD/SAS identity and destination policies still apply. This requires the real build and the `eventhubs_exclusive_consumer_v1` generator capability. Consumer actions cannot change the configured hub or group. `checkpoint_container_url` is not accepted in this mode; use `processor` for balanced ownership and durable restart positions.

A higher epoch can disconnect lower-priority and non-exclusive consumers of that partition/group, as described in [Azure's ownership semantics](https://learn.microsoft.com/en-us/azure/event-hubs/event-hubs-features). Choose the group/partition intentionally and use one consumer per assigned partition. A reported SDK ownership-loss error fences future receives in that client; APEX does not silently reconnect with the old epoch. Receiver creation remains lazy until execution, with prefetch disabled. Local validation, SDK-option and ownership-loss tests do not replace live Azure takeover acceptance.
