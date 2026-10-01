# Event Hubs load and observer probes

APEX's native `eventhubs` protocol produces events and provides a read-only consumer observer. It does not implement a production Processor/checkpoint owner. The default binary is mock-only; build approved generator images with `-tags eventhubs_real` for the Azure SDK transport. Kafka endpoints use the separate `kafka_real` build and `config.kafka.profile: eventhubs`.

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

Use an automatically measured consume action or a business transaction that actually retains its telemetry. These rules evaluate closed archives and flow through existing JSON/JUnit/report output; they do not add live automatic stop conditions. Partition/subscription dashboards, full checkpoint ownership, acknowledgement latency across every provider and live Azure acceptance remain separate work. Mock tests are not evidence of real namespace readiness.
