# Capabilities & limitations

Import recognition, authoring, runtime execution and production adapter availability are separate capabilities. A script that imports successfully is not necessarily executable on the selected generator. Check the controller's current support matrices and configured pool before launch.

## Choose an execution path

| Capability | Current boundary | What to check |
| --- | --- | --- |
| HTTP, SOAP, WebSocket, gRPC, database and network | Compiled protocol implementations; individual operations have protocol-specific contracts | DSL action reference, dependencies and target connectivity |
| Browser replay and observer canaries | Requires the browser runtime and permitted execution environment | Browser & mobile support and pool readiness |
| Kafka | Default in-memory mock; real network adapter requires kafka_real build | Messaging support and deployment build |
| Azure Event Hubs | Default in-memory mock; real AMQP adapter requires eventhubs_real build | Producer/observer-probe semantics and deployment build |
| JMS Azure Service Bus | Real adapter requires jms_servicebus_real build | Messaging support and credentials |
| Java VUser | Governed external sidecar runner, disabled unless configured | Java VUser runtime and policy |
| Bruno | Version-pinned Node runner, disabled unless policy is configured | Runner installation, source bundle and egress policy |
| SAP GUI | Authorable/importable; no registered execution runtime in standard binaries | Preserve import evidence and plan an alternative execution path |
| Oracle Forms/NCA | Import evidence requires an approved external bridge; ordinary settings do not supply that bridge | Import disposition and administrator-supported alternatives |
| Assisted authoring | Deterministic local provider; external LLM providers are not wired by default | Review, validation, replay evidence and approval gates |
| OpenTelemetry | Opt-in traces from standalone controller/generators; no native metrics/log exporter | Prometheus for metrics; deployment tracing configuration |
| Shared data pools | Runs use read-only snapshots; durable consume/read-write execution is not implemented | Row status, snapshot selection and rerun behavior |
| Sequential execution plans | Next entry starts after the previous entry reaches a terminal state; halt/skip policy applies | Failure policy, governance and recovery evidence |
| Capacity claims | 10,000 HTTP VUsers per pod is a design target, not a published achieved benchmark | Measure your script, protocol, generator and environment |

## Current references

- [DSL action reference](DSL_ACTION_REFERENCE.md)
- [Messaging support](MESSAGING_SUPPORT.md)
- [Browser & mobile support](BROWSER_AND_MOBILE_SUPPORT.md)
- [Java VUser runtime](JAVA_VUSER_RUNTIME.md)
- [Runtime settings support](RUNTIME_SETTINGS_PARITY.md)
- [OpenTelemetry traces](OPENTELEMETRY.md)
- [API support-matrix operations](#/swagger?search=support-matrix)

The running controller's capabilities, actual build tags, policy and selected execution pool determine availability. Documentation bundled with a different UI build cannot establish that a remote generator is ready.
