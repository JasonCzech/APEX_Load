# Stormrunner Enterprise Monitoring

These manifests are production-oriented examples for Phase 3 of the enterprise data platform upgrade.

Stormrunner keeps exposing `/metrics` from the controller. Prometheus scrapes that endpoint for live operational metrics, then remote-writes to the enterprise retention backend:

- Grafana Mimir
- Thanos Receive
- A managed Prometheus-compatible remote-write endpoint

Use these examples as overlays, not as local-kind defaults.

## Files

- `servicemonitor.yaml` - Prometheus Operator scrape definition for the controller.
- `prometheus-remote-write-example.yaml` - example Prometheus config with remote-write destinations.

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
- `generator_id`

High-cardinality raw transaction details stay in Stormrunner archive bundles instead of Prometheus.
