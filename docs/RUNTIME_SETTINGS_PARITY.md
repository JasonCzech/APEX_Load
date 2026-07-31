# Runtime Settings Parity

This is the W3 evidence record for runtime settings parity across APEX Load's current Web/API, browser, and migration workflows.

## API Contract

| Endpoint | Purpose |
| --- | --- |
| `GET /api/v1/runtime-settings/support-matrix` | Unified runtime setting matrix across HTTP, browser, SOAP, Java-over-HTTP, WebSocket, gRPC, JMS, DevWeb, HAR, JMeter, and Enterprise Script Archive Web HTTP/HTML. |
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
| `migration_replay` | High-evidence replay for Enterprise Script Archive, JMeter, HAR, and DevWeb migration review. |
| `browser_canary` | Low-density browser UX sentinel paired with protocol load. |
| `debug_trace` | Verbose troubleshooting for protocol onboarding and failed migrations. |
| `soak_stress` | Longer-running resource trend profile with conservative pacing and standard logging. |

## Streaming And Media Decision

Streaming/media playback settings (`mode`, `buffer_size_seconds`, `fixed_bitrate_kbps`, `bandwidth_estimation`) are `executed` for HTTP scripts. APEX Load maps imported playback mode, buffer, bitrate, and bandwidth-estimation settings into media-like response pacing so fixed bitrate playback, startup buffering, and adaptive bandwidth estimates affect action duration and transfer telemetry. The operational keys real VuGen writes in `[Streaming]` (`StreamTimeout`, `StreamRetryTime`, `StreamSeekMethod`) have no execution path and are classified `compatibility_only`; `StreamLog`/`SaveStreamSnapshot` are VuGen diagnostics and classify `non_applicable`.

## Real VuGen Key Coverage And The No-Silent-Drop Guarantee

The importer reads the section/key vocabulary real VuGen writes, not only APEX-native names:

| VuGen source | Destination |
| --- | --- |
| `[ModemSpeed]` `EnableModemSpeed`/`EnableCustomModemSpeed`/`ModemSpeed`/`CustomModemSpeed` (bits per second) | `config.web_runtime.network_simulation` (`max` when both toggles are off, `custom` + Kbps when one is on) |
| `[FILTERS]` `IncludeFiltersInList`/`ExcludeFiltersInList` | `config.web_runtime.download_filters` (`allow_list`/`block_list`; both lists populated fails to review) |
| `[Log]` `AutoLog` | `config.log.on_error_only` (JIT "send messages only when an error occurs") |
| `.usp` `RunLogicRunMode` | Preserved in `config.run_logic.block_scheduling` as `compatibility_only`; the current engine does not consume the field. |
| `.usp` `[Profile Actions]`/`RunLogicActionOrder` | Preserved in `config.run_logic.action_sequence` as `compatibility_only` (standard lifecycle vocabulary only; custom multi-action orders fail to review); the current engine uses its fixed init/action/teardown lifecycle. |
| `[Java]` `UseExternalJVM`/`ExternalJREPath` | `config.java_vuser.jdk_selection`/`jdk_home` (java_vuser scripts; non-applicable elsewhere) |
| `*_extra_arg_N` spill rows (e.g. `BrowserAcceptLanguage_extra_arg_2`) | The base value stays authoritative; divergent spill rows raise a `request_headers` review finding |

Every key of the **selected** cfg/usp receives an explicit per-key disposition — `mapped`, `compatibility_only`, or `non_applicable` — recorded at `metadata.import.preserved.runtime_setting_dispositions` and rendered read-only in the Script Editor's **Imported Source Runtime Settings** section. Unmapped-but-active settings roll up into a `runtime_unmapped` (`compatibility_only`) parity entry and inert/authoring rows into `runtime_non_applicable`, so unexecuted source settings lower the migration-readiness score instead of silently inflating it. The `.usr` `[TransactionsOrder]`/`[Transactions]` declarations are cross-checked against the transactions the converted source actually starts.

`block_scheduling` and `action_sequence` are migration evidence, not current
execution controls. An executable migration policy should include
`compatibility_only` in `migration_block_statuses`; this keeps imported random,
percentage, omitted-phase, or reordered lifecycle semantics review-gated until
they are replaced with behavior the engine executes.

## Java Vuser Java VM Settings

Imported or existing `java_vuser` scripts expose a protocol-scoped Java VM section. JDK selection/home, exact JVM argument tokens, and script-controlled path references to operator-provisioned precompiled artifacts are `executed`. Dedicated class loading is also `executed`: local and sidecar execution create one child class loader and Actions object per VUser session, retain them across init, action iterations, and end, and close them at teardown. APEX Load does not reproduce every LoadRunner JVM-reuse topology — VUser sessions remain isolated governed processes. `use_xbootclasspath` is `compatibility_only`: on supported Java 17+ runtimes it produces stable fallback evidence and uses the ordinary classpath; APEX Load never emits an `-Xbootclasspath` launcher option.

Execution remains operator-gated. A specified JDK is resolved only on the selected executor, must contain an executable Java 17+ launcher, and never falls back to an internal JDK. Distributed assignments run Java-specific preflight before commit so an unavailable runner, JDK, harness, classpath, or target class fails before VUsers are allocated. See `docs/JAVA_VUSER_RUNTIME.md` for the runtime and deployment contract.

## Evidence

- Real VuGen 24.3 golden fixture (full stock cfg/usp key surface) + completeness gate: `pkg/convert/enterprisearchive/runtime_vugen24_golden_test.go`
- Run-order classification and migration-policy gate: `pkg/convert/enterprisearchive/runtime_execution_fidelity_test.go`
- Per-key classification tables: `pkg/convert/enterprisearchive/runtime_disposition.go`
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
- Java Vuser import and runtime evidence:
  - Canonical Java VM import: `pkg/convert/enterprisearchive/java_runtime_config_test.go`
  - JDK/VM argument/command enforcement: `pkg/javarunner`
  - Assignment-time preflight: `pkg/distributed/generatorrpc/runtime_preflight_test.go`
