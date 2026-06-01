# Runtime Settings Parity

This is the W3 evidence record for runtime settings parity across APEX Load's current Web/API, browser, and migration workflows.

## API Contract

| Endpoint | Purpose |
| --- | --- |
| `GET /api/v1/runtime-settings/support-matrix` | Unified runtime setting matrix across HTTP, browser, SOAP, Java-over-HTTP, WebSocket, gRPC, JMS, DevWeb, HAR, JMeter, and LoadRunner Web HTTP/HTML. |
| `GET /api/v1/scripts/{id}/runtime-settings/report?format=json\|csv` | Script-level runtime setting parity export for migration review and support handoff. |
| `GET /api/v1/scenarios/{id}/runtime-settings/report?format=json\|csv` | Scenario-level runtime setting parity export after scenario/runtime overrides are applied. |

`POST /api/v1/scenarios/validate-load-model`, `POST /api/v1/tests/estimate`, `POST /api/v1/scenarios`, and `POST /api/v1/tests` use the same runtime review contract. Compatibility-only settings are surfaced as review warnings; settings that would silently mislead execution, such as browser `download_resources=recorded` without recorded-resource metadata, block save/run.

## Status Vocabulary

| Status | Meaning |
| --- | --- |
| `executed` | APEX Load executes this setting in the named runtime path. |
| `partial` | APEX Load executes a defined subset and preserves or warns on the rest. |
| `compatibility_only` | APEX Load preserves the setting for migration/reporting, but does not execute its semantics. |
| `non_applicable` | The setting has no equivalent meaning for the protocol/runtime scope. |
| `blocked` | The setting prevents save/run until repaired or changed. |
| `planned` | The setting is future work and must not be claimed as executable. |

## Runtime Presets

The support endpoint publishes six reusable profiles:

| Preset | Purpose |
| --- | --- |
| `ci_smoke` | Fast deterministic validation for pull requests and import sanity checks. |
| `release_gate` | Balanced release qualification with snapshots and per-iteration grouping. |
| `migration_replay` | High-evidence replay for LoadRunner, JMeter, HAR, and DevWeb migration review. |
| `browser_canary` | Low-density browser UX sentinel paired with protocol load. |
| `debug_trace` | Verbose troubleshooting for protocol onboarding and failed migrations. |
| `soak_stress` | Longer-running resource trend profile with conservative pacing and standard logging. |

## Streaming And Media Decision

Streaming/media runtime settings are `executed` for HTTP scripts. APEX Load maps imported playback mode, buffer, bitrate, and bandwidth-estimation settings into media-like response pacing so fixed bitrate playback, startup buffering, and adaptive bandwidth estimates affect action duration and transfer telemetry.

## Evidence

- Unified support matrix and presets: `pkg/api/runtime_settings_support.go`
- Runtime review, scenario/run gating, and JSON/CSV exports: `pkg/api/runtime_settings_review.go`
- Scenario validation coverage: `pkg/api/runtime_settings_support_test.go::TestScenarioRuntimeSettingsValidationSurfacesCompatibilityAndBlockers`
- Export coverage: `pkg/api/runtime_settings_support_test.go::TestRuntimeSettingsReportEndpointsExportScriptAndScenarioEvidence`
- Browser recorded-resource guard: `pkg/protocol/browser/browser_test.go::TestBrowserProtocolDownloadResourcesRecordedRequiresMetadata`
- Protocol snapshot/error evidence:
  - SOAP faults: `pkg/protocol/soap/soap_test.go`
  - gRPC status/snapshots: `pkg/protocol/grpc/grpc_test.go`
  - WebSocket receive/close/error frames: `pkg/protocol/websocket/websocket_test.go`
  - JMS broker/dead-letter diagnostics: `pkg/protocol/jms/jms_test.go`
  - Java-over-HTTP decode failures: `pkg/protocol/java_http/java_http_test.go`
