# APEX Load Competitive Evaluator One-Pager

## Positioning

APEX Load is a self-hosted, Kubernetes-native performance testing platform for HTTP/API-heavy enterprise workloads. It is strongest where teams need open JSON DSL assets, controller APIs, governance, CI gates, migration paths, and auditable archives.

## Supported Today

| Area | Status |
| --- | --- |
| HTTP/API, SOAP, WebSocket, gRPC, Java-over-HTTP | Supported |
| JMeter, LoadRunner Web HTTP/HTML, HAR, OpenAPI, WSDL, DevWeb import-review workflows | Supported for documented scopes |
| CI/CD gating with JSON and JUnit XML | Supported |
| Live metrics, archive reports, comparison, release trends, Prometheus/Dynatrace hooks | Supported for current scope |
| Self-hosted named load zones | Supported as region-derived generator pools |

## Partial / Evidence-Gated

| Area | Current boundary |
| --- | --- |
| k6 compatibility | Constrained HTTP import/export and result ingestion only; APEX does not run k6 JavaScript. |
| Browser testing | Diagnostic browser canaries paired with protocol load; no high-density browser claim without release benchmark artifacts. |
| LoadRunner correlation parity | Rules, replay diagnostics, and guided repair exist; VuGen-class breadth remains deeper. |
| Published scale | Design targets require release evidence before customer-facing claims. |

## Non-Goals Unless Customer-Backed

- Managed SaaS load-zone control plane.
- SAP GUI, Citrix, Oracle Forms, and native desktop UI protocols.
- Full k6 JavaScript runtime compatibility.
- Full TruClient/native desktop automation parity.

## Fast Evaluation Path

1. Seed demos with `./scripts/seed-evaluator-demo.sh`.
2. Validate local authoring with `apexctl script validate`.
3. Inspect `/api/v1/system/parity-status` and `/api/v1/fleet/load-zones`.
4. Run HTTP/API protocol load with a small browser canary group.
5. Review archive report, comparison, and CI/JUnit output.
