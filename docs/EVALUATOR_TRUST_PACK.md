# APEX Load Evaluator Trust Pack

This pack gives evaluators a short, reproducible path through the current competitive claims without implying unsupported parity.

## One-Command Seed

Run a controller locally, then seed the canonical demo assets:

```bash
APEXLOAD_API=http://localhost:8080 PROJECT_ID=default ./scripts/seed-evaluator-demo.sh
```

The seed script loads:

| Area | Asset |
| --- | --- |
| HTTP/API | `examples/web-api/ecommerce-http.json` |
| Mixed protocol + browser canary | `examples/web-api/browser-canary-plus-protocol-load.json` |
| SOAP | `examples/web-api/soap-account-service.json` |
| gRPC | `examples/web-api/grpc-inventory.json` |
| WebSocket | `examples/web-api/websocket-order-feed.json` |
| JMS | `examples/web-api/jms-order-flow.json` |

## Evidence Rules

- Treat browser VUsers as diagnostic canaries unless a release evidence manifest includes controlled raw benchmark output.
- Treat load zones as self-hosted named generator pools, not managed SaaS infrastructure.
- Treat k6 compatibility as constrained import/export and result ingestion, not JavaScript runtime compatibility.
- Treat SAP GUI, Citrix, Oracle Forms, and native desktop automation as demand-gated non-goals.

## Acceptance Checklist

- `/api/v1/system/parity-status` shows the competitive parity dimensions and evidence levels.
- `/api/v1/fleet/load-zones` returns named self-hosted load zones derived from generator regions.
- `apexctl script validate --script examples/web-api/ecommerce-http.json --format json` returns `valid: true`.
- `POST /api/v1/scripts/import/k6` returns executable HTTP DSL for statically parseable k6 HTTP scripts and review diagnostics for unsupported JavaScript/browser constructs.
