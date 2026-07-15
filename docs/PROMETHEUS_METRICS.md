# Prometheus Metrics

APEX Load exposes controller metrics at `GET /metrics`. When `APEXLOAD_METRICS_ADDR` is set, `/metrics` is served only on that dedicated listener. When `APEXLOAD_METRICS_TOKEN` is set, scrapes must include `Authorization: Bearer <token>`.

The controller is scrape-only. Remote write is configured on the external Prometheus server; see `deployments/k8s/monitoring/prometheus-remote-write-example.yaml`.

## Controller

| Metric | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `apexload_up` | gauge | none | Controller scrape health. |
| `apexload_websocket_clients` | gauge | none | Connected WebSocket clients. |
| `apexload_test_count` | gauge | none | Tests tracked in controller memory. |
| `apexload_tests_by_status` | gauge | `status` | Tests by lifecycle status. |
| `apexload_max_concurrent_tests` | gauge | none | Configured concurrent-test limit. |
| `apexload_metrics_interval_seconds` | gauge | none | Configured live metrics publish interval. |
| `apexload_notification_delivery_failures_total` | counter | none | Webhook deliveries that failed after retry. |

## Scaling And Capacity

| Metric | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `apexload_scaling_vusers` | gauge | `state` | Queued and active VUsers. |
| `apexload_scaling_tests` | gauge | `state` | Queued and active tests. |
| `apexload_scaling_protocol_vusers` | gauge | `state`, `protocol` | Queued and active VUsers by protocol. |
| `apexload_capacity_available_generators` | gauge | `scope` | Configured/effective/reserved/shared/live generator capacity. |
| `apexload_generator_region_count` | gauge | `region` | Configured generator inventory entries by region. |
| `apexload_generator_region_available_capacity` | gauge | `region` | Available VUser capacity by region. |
| `apexload_generator_region_active_vusers` | gauge | `region` | Active VUsers by region. |
| `apexload_generator_capacity_state_count` | gauge | `region`, `state` | Generator capacity states by region. |
| `apexload_generator_drain_state_count` | gauge | `region`, `state` | Generator drain states by region. |
| `apexload_generator_compatibility_state_count` | gauge | `region`, `state` | Generator compatibility states by region. |

## Telemetry Pipeline

These series are emitted only for non-terminal tests with live telemetry.

| Metric | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `apexload_telemetry_dropped_results_total` | counter | `test_id` | Collector samples dropped by the engine. |
| `apexload_telemetry_shedded_arrivals_total` | counter | `test_id` | Open-workload arrivals shed by the engine. |
| `apexload_telemetry_dropped_subscriber_batches_total` | counter | `test_id` | Generator metrics batches dropped because stream subscribers were full. |
| `apexload_websocket_dropped_messages_total` | counter | `test_id` | WebSocket client messages dropped by the controller. |
| `apexload_websocket_dropped_events_total` | counter | `test_id` | WebSocket broadcast events dropped by the controller. |
| `apexload_telemetry_sample_age_ms` | gauge | `test_id` | Age of the latest telemetry sample. |
| `apexload_telemetry_generator_processing_lag_ms` | gauge | `test_id` | Generator-side collection-to-publish lag. |
| `apexload_telemetry_transport_lag_ms` | gauge | `test_id` | Generator-to-controller transport lag. |
| `apexload_telemetry_controller_fanout_lag_ms` | gauge | `test_id` | Controller-to-dashboard fan-out lag. |

## Messaging

Messaging metrics are emitted only for non-terminal tests whose dependency telemetry includes JMS, Kafka, or Event Hubs targets.

| Metric | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `apexload_messaging_consumer_lag_ms` | gauge | `test_id`, `protocol`, `target` | Average consumer lag. |
| `apexload_messaging_consumer_offset_lag` | gauge | `test_id`, `protocol`, `target` | Average offset or sequence backlog. |
| `apexload_messaging_e2e_latency_ms` | gauge | `test_id`, `protocol`, `target` | Average producer-to-observed-consume latency. |
| `apexload_messaging_partition_skew_pct` | gauge | `test_id`, `protocol`, `target` | Produce skew across partitions. |
| `apexload_messaging_throttled_total` | gauge | `test_id`, `protocol`, `target` | Throttle, server-busy, and quota responses. |
| `apexload_messaging_dead_lettered_total` | gauge | `test_id`, `protocol`, `target` | Dead-lettered message count. |
| `apexload_messaging_messages_sent_per_sec` | gauge | `test_id`, `protocol`, `target` | Average send rate. |
| `apexload_messaging_messages_received_per_sec` | gauge | `test_id`, `protocol`, `target` | Average receive rate. |

## Troubleshooting

- If `apexload_up` is absent, verify the ServiceMonitor points at the `metrics` service port and that NetworkPolicy allows Prometheus to reach it.
- If telemetry lag or drops alert, compare `apexload_telemetry_*` series with dashboard telemetry-health banners and controller logs.
- `APEXLOAD_PPROF_ADDR` can expose unauthenticated Go pprof for CPU/memory investigation when enabled by the operator. Production startup rejects non-loopback pprof binds; use an explicit loopback address plus port forwarding.
- `APEXLOAD_PROMETHEUS_URL` configures the controller's Prometheus query client for dashboard graph overlays; it is separate from the `/metrics` scrape endpoint.
