# Dynatrace Webhooks

APEX Load sends lifecycle and telemetry-health notifications to Dynatrace through the Dynatrace Events API v2. This is an event webhook integration, not an OTLP exporter.

## Setup

1. Create a Dynatrace API token with event-ingest permissions.
2. In APEX Load settings, add a notification webhook with format `dynatrace`.
3. Use the Dynatrace Events API endpoint, for example `https://<tenant>/api/v2/events/ingest`.
4. Paste the API token once. The settings API stores it server-side and returns only `has_api_token: true`; the raw token is write-only.
5. Optionally set `entity_selector` and `timeout_minutes`.

## Delivery Behavior

- APEX Load sends `Authorization: Api-Token <token>`.
- Retryable delivery failures (`408`, `429`, `5xx`, or transport errors) are retried with bounded exponential backoff and `Retry-After` support.
- Failed deliveries after retry increment `apexload_notification_delivery_failures_total`.
- Dynatrace payloads include an `apexload.idempotency_key` property so downstream workflows can dedupe repeated attempts.

## Events

Dynatrace webhooks can subscribe to the same event names as Slack and Teams, including `test_started`, `test_complete`, `test_failed`, `telemetry_degraded`, `telemetry_breached`, `telemetry_recovered`, and fleet alert events.
