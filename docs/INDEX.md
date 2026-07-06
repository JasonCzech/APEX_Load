# Documentation Index

Status: Current

This is the canonical map for APEX Load documentation. Generated references are
the source of truth for API and DSL contracts; roadmap and historical files are
kept for planning or audit context only.

## Current Guides

| Document | Status | Purpose |
| --- | --- | --- |
| [README.md](../README.md) | Current | Project overview, quick start, and primary links |
| [ARCHITECTURE.md](../ARCHITECTURE.md) | Current | System architecture and runtime model |
| [DEVELOPMENT.md](../DEVELOPMENT.md) | Current | Developer setup, package guide, testing policy |
| [QUICK_REFERENCE.md](../QUICK_REFERENCE.md) | Current | Commands, DSL examples, common fixes |
| [TROUBLESHOOTING.md](../TROUBLESHOOTING.md) | Current | Operational troubleshooting |
| [API_USER_GUIDE.md](API_USER_GUIDE.md) | Current | API workflows for operators and automation |
| [SCRIPT_LIFECYCLE.md](SCRIPT_LIFECYCLE.md) | Current | Script source-to-run lifecycle |
| [BROWSER_AND_MOBILE_SUPPORT.md](BROWSER_AND_MOBILE_SUPPORT.md) | Current | Browser canaries, mobile emulation, client metrics, benchmark evidence |
| [MESSAGING_SUPPORT.md](MESSAGING_SUPPORT.md) | Current | JMS, Kafka, AsyncAPI, and Event Hubs support summary |
| [AUTH_RBAC_OPERATIONS_RUNBOOK.md](AUTH_RBAC_OPERATIONS_RUNBOOK.md) | Current | Auth/RBAC operations |
| [ENTERPRISE_DATA_PLATFORM_OPERATIONS_RUNBOOK.md](ENTERPRISE_DATA_PLATFORM_OPERATIONS_RUNBOOK.md) | Current | Enterprise storage/cache/archive operations |
| [PROMETHEUS_METRICS.md](PROMETHEUS_METRICS.md) | Current | Prometheus scrape metrics |
| [DYNATRACE_WEBHOOKS.md](DYNATRACE_WEBHOOKS.md) | Current | Dynatrace event webhook setup |
| [recording-agent.md](recording-agent.md) | Current | Local browser Recording Agent |
| [deployments/README.md](../deployments/README.md) | Current | Deployment chooser |

## Generated References

| Document | Status | Source |
| --- | --- | --- |
| [swagger.yaml](swagger.yaml) | Generated | `go generate ./pkg/api` |
| [api-endpoint-catalog.json](api-endpoint-catalog.json) | Generated | `go generate ./pkg/api` |
| [dsl.schema.json](dsl.schema.json) | Generated | `go generate ./pkg/dsl` |
| [DSL_ACTION_REFERENCE.md](DSL_ACTION_REFERENCE.md) | Generated | `go generate ./pkg/dsl` |
| [API_AND_DSL_REFERENCE.md](../API_AND_DSL_REFERENCE.md) | Current | Compatibility landing page for generated references |

## Roadmap And Planning

These files contain tactical plans, future work, or parity analysis. They are
not the source of truth for current API or DSL contracts.

| Document | Status |
| --- | --- |
| [BROWSER_CLIENT_METRICS_PLAN.md](BROWSER_CLIENT_METRICS_PLAN.md) | Roadmap/detail |
| [MOBILE_BROWSER_EMULATION_PLAN.md](MOBILE_BROWSER_EMULATION_PLAN.md) | Roadmap/detail |
| [MOBILE_UX_PLATFORM_STRATEGY.md](MOBILE_UX_PLATFORM_STRATEGY.md) | Roadmap/detail |
| [BROWSER_REPLAY_HAR_CAPTURE_PLAN.md](BROWSER_REPLAY_HAR_CAPTURE_PLAN.md) | Roadmap/detail |
| [KAFKA_MESSAGING_AND_ASYNCAPI_PLAN.md](KAFKA_MESSAGING_AND_ASYNCAPI_PLAN.md) | Roadmap/detail |
| [MESSAGING_COVERAGE_AND_PLAN.md](MESSAGING_COVERAGE_AND_PLAN.md) | Roadmap/detail |
| [PROTOCOL_EXTENSION_PLAN.md](PROTOCOL_EXTENSION_PLAN.md) | Roadmap/detail |
| [PROTOCOL_TELEMETRY_PARITY_PLAN.md](PROTOCOL_TELEMETRY_PARITY_PLAN.md) | Roadmap/detail |
| [AUTH_RBAC_BACKLOG.md](AUTH_RBAC_BACKLOG.md) | Roadmap/detail |
| [AUTH_RBAC_FOLLOW_UP_TICKETS.md](AUTH_RBAC_FOLLOW_UP_TICKETS.md) | Roadmap/detail |
| [ENTERPRISE_DATA_PLATFORM_UPGRADE_PLAN.md](ENTERPRISE_DATA_PLATFORM_UPGRADE_PLAN.md) | Roadmap/detail |
| [SCRIPT_AUTHORING_CONSOLIDATION_ROADMAP.md](SCRIPT_AUTHORING_CONSOLIDATION_ROADMAP.md) | Roadmap/detail |

## Historical

Historical reviews are archived under [archive/reviews](archive/reviews/README.md).
They are preserved for audit context and should not be treated as current
product documentation.
