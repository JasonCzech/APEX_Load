# APEX Load Enterprise Monitoring

These manifests are production-oriented examples for Phase 3 of the enterprise data platform upgrade.

APEX Load keeps exposing `/metrics` from the controller. Prometheus scrapes that endpoint for live operational metrics, then remote-writes to the enterprise retention backend:

- Grafana Mimir
- Thanos Receive
- A managed Prometheus-compatible remote-write endpoint

Use these examples as overlays, not as local-kind defaults. `APEXLOAD_PROMETHEUS_REMOTE_WRITE_URL` is consumed by the external Prometheus configuration in this directory; the APEX Load controller remains scrape-only and does not remote-write samples itself.

## Files

- `servicemonitor.yaml` - Prometheus Operator scrape definition for the controller.
- `prometheus-remote-write-example.yaml` - example Prometheus config with remote-write destinations.
- `prometheusrule.yaml` - starter alert rules for scrape absence, telemetry staleness, telemetry drops, and WebSocket fan-out drops.

## Required Labels

The controller metrics endpoint should preserve bounded labels suitable for retention:

- `environment`
- `domain`
- `project`
- `release`
- `scenario`
- `run`
- `protocol`
- `generator_region`
- generator identity is intentionally kept out of controller `/metrics`; use Kubernetes pod labels, cAdvisor, and kube-state-metrics for per-generator host telemetry.

High-cardinality raw transaction details stay in APEX Load archive bundles instead of Prometheus.
