# Messaging Support

Status: Current

APEX Load supports messaging workloads through compiled protocol packages and
build-tag-gated real adapters. The default build keeps broker execution safe for
local validation by using in-memory mocks or fail-closed unavailable adapters.

## Current Support Summary

| Platform | Import | Runtime | Notes |
| --- | --- | --- | --- |
| JMS family | Partial | Mock/default; Solace and Azure Service Bus real adapters are build-tag gated | IBM MQ remains mock/stub-only. |
| Apache Kafka | Partial | Mock/default; real franz-go adapter behind `-tags kafka_real` | Includes produce/consume/request-reply and Event Hubs Kafka-endpoint profile guidance. |
| Azure Event Hubs | Partial | Mock/default; native AMQP adapter behind `-tags eventhubs_real` | Native protocol is producer-first with observer consume probes. |
| AsyncAPI import | Partial | Generates Kafka/Event Hubs or review drafts depending on bindings and adapter availability | Unsupported bindings remain review-only rather than pretending to execute. |

Real broker adapters are intentionally opt-in so standard binaries remain
deterministic and fail closed when live broker dependencies are unavailable.

## Build Tags

| Tag | Enables |
| --- | --- |
| `kafka_real` | Real Kafka / Event Hubs Kafka-endpoint transport |
| `jms_solace_real` | Real Solace JMS-over-AMQP transport |
| `jms_servicebus_real` | Real Azure Service Bus adapter |
| `eventhubs_real` | Native Azure Event Hubs AMQP adapter |

## Results And Telemetry

Messaging results report protocol-neutral throughput, message latency, consumer
lag where supported, payload sizing, and reliability counters when adapters
populate them. Deeper broker-native parity remains uneven by provider, especially
for offset/queue-depth semantics, DLQ/retry details, transactions, and
provider-specific management-plane probes.

## Current Guidance

- Use mocks for local authoring and CI smoke validation.
- Use tagged controller/generator builds for live broker execution.
- Do not claim IBM MQ live execution until a real adapter is added.
- Keep unsupported AsyncAPI/JMS/MQ/MQTT bindings as review-only drafts with
  explicit notes.

## Detailed Planning References

The following files are retained for deeper implementation history and parity
work:

- [MESSAGING_COVERAGE_AND_PLAN.md](MESSAGING_COVERAGE_AND_PLAN.md)
- [KAFKA_MESSAGING_AND_ASYNCAPI_PLAN.md](KAFKA_MESSAGING_AND_ASYNCAPI_PLAN.md)
- [PROTOCOL_EXTENSION_PLAN.md](PROTOCOL_EXTENSION_PLAN.md)
- [WEB_API_EXECUTION_SUPPORT_MATRIX.md](WEB_API_EXECUTION_SUPPORT_MATRIX.md)
