# Roadmap Non-Messaging Completion & End-to-End Integration Gate (`INT-1`)

**Date**: 2026-10-28  
**Branch**: `feat/roadmap-remaining`  
**Scope**: Non-Messaging Roadmap Completion (`REC`, `AUTH`, `PROTO`, `NA`, `TEL`, `BE`/`BI`, and `INT-1` End-to-End Integration Gate)

---

## 1. Completed Non-Messaging Roadmap Tracks

### Track `REC` — Recovery, Evacuation & Fault Qualification (`REC-A1`, `REC-A2`, `REC-B1`, `REC-C1`)
- **`REC-A1`**: Generator evacuation restart topologies, capacity transfer, and stop/natural-completion boundaries (`pkg/api/run_evacuation_restart_test.go`, `pkg/api/run_evacuation_terminal_cause_test.go`).
- **`REC-A2` & `REC-B1`**: Asymmetric network-partition harnesses, successive-leader quarantine convergence, and operator intervention surfaces (`pkg/api/same_leader_evacuation_intervention_test.go`, `pkg/api/run_evacuation_exit_cases_test.go`, `pkg/api/run_evacuation_postgres_acceptance_test.go`, `dashboard/src/components/TestExecution.tsx`).
- **`REC-C1`**: 3-repetition integrated local fault qualification harness across all 7 failover/evacuation scenarios with independent authenticated traffic accounting (`cmd/apexfault-target/`, `pkg/api/rec_c_local_fault_qualification_test.go`, `docs/qa/reviews/roadmap-rec-c-local-faults-2026-10-28/README.md`).

### Track `AUTH` — Authoring Receipts, Validation Context & Import Parity (`AUTH-A1`, `AUTH-B1`, `AUTH-C1`)
- **`AUTH-A1`**: Contract v2 authoring receipts across all crash/fault boundaries and live/offline `RotateAuthoringReceiptCredentialKey` re-wrapping (`pkg/api/authoring_idempotency.go`, `pkg/api/authoring_receipt_outcome_test.go`).
- **`AUTH-B1`**: Scenario-level `ScenarioValidationContext` composite digest over constituent scripts, policies, approvals, and secret references (`pkg/api/handlers_scenarios.go`, `pkg/api/validation_context_test.go`).
- **`AUTH-C1`**: Real-HTTP session-backed import save persisted-copy parity, unsaved sibling draft retention, `If-Match` conflict recovery, and adapter retirement ratchet (`pkg/api/authoring_fidelity_lifecycle_test.go`, `pkg/api/authoring_adapter_retirement_test.go`, `dashboard/e2e/workflow-authoring-intent.spec.ts`).

### Track `PROTO` — Non-Messaging Protocol Diagnostics & Ground-Truth Qualification (`PROTO-A1`, `PROTO-A2`, `PROTO-B1`)
- **`PROTO-A1`**: Java HTTP wire-vs-representation diagnostics (`serialized_representation_vs_http_body`) and contained JVM object-footprint helper (`pkg/protocol/java_http_diagnostics.go`, `pkg/javarunner/footprint_helper.go`).
- **`PROTO-A2`**: Non-Messaging protocol diagnostics propagation across native analytics, baseline comparison, and archive reports (`pkg/analysis/java_http_catalog.go`, `pkg/api/baseline_java_http.go`, `pkg/api/report_java_http_diagnostics.go`).
- **`PROTO-B1`**: Controlled-endpoint telemetry qualification matrix across gRPC, WebSocket, and Java HTTP (`pkg/protocol/grpc/roadmap_groundtruth_test.go`, `pkg/protocol/websocket/roadmap_pressure_test.go`, `pkg/protocol/java_http/representation_diagnostics_test.go`, `pkg/api/baseline_protocol_diagnostics_test.go`).

### Track `NA` — Native Analytics Provenance, Writer Ownership & Pinned Panel Inputs (`NA-A1`, `NA-B1`, `NA-C1`, `NA-C2`, `NA-C3`, `NA-D1`)
- **`NA-A1`**: Multi-chunk duplicate content provenance detection across arbitrary chunk boundaries (`pkg/api/analysis_upload_pipeline.go`, `pkg/api/analysis_import_duplicates_test.go`).
- **`NA-B1`**: Durable `JointStreams` sequence ledger across `telemetryCheckpoint` failover (`pkg/api/telemetry_checkpoint.go`, `pkg/api/telemetry_sources.go`, `pkg/api/telemetry_checkpoint_test.go`).
- **`NA-C1`**: Admitted protocol byte query catalog across `http` (`http_body.v1`), `grpc` (`grpc_uncompressed_payload.v1`), and `websocket` (`ws_application_payload.v1`) (`pkg/analysis/byte_catalog.go`, `pkg/api/native_byte_series.go`, `pkg/api/native_byte_series_test.go`).
- **`NA-C2`**: Frozen `PanelInputs` v2 (`QueryVersion: 5`) and `RunArchiveReport.SavedAnalysisPanels` readiness/staleness tracking (`pkg/storage/browser_panel_inputs.go`, `pkg/api/browser_panel_inputs.go`, `pkg/api/imported_dashboard_report.go`, `pkg/api/browser_panel_inputs_http_test.go`).
- **`NA-C3` & `NA-D1`**: Mint-time `_analysis/source.v1` volume identity binding, child worker staging reclamation, `POST /api/v1/analysis/imports/{id}/attest-writer-stopped` admin attestation, and `darwin/arm64` native reference fault trials (`pkg/storage/analysis_writer_ownership.go`, `pkg/api/analysis_writer_ownership.go`, `pkg/api/analysis_writer_ownership_test.go`, `docs/qa/reviews/roadmap-na-d1-local-macos-2026-10-28/README.md`).

### Track `TEL` — Observation-to-Render SLA, Coverage v4, Evidence Archive & 10-Consumer Scrubbed Parity (`TEL-A1`, `TEL-A2`, `TEL-B1`, `TEL-B2`)
- **`TEL-A1`**: Calibrated publication-window start bounds, foreground presentation proofs, and durable `RenderPopulation` checkpointing (`pkg/distributed/generatorrpc/collection_window.go`, `pkg/api/telemetry_stages.go`, `pkg/api/telemetry_client_render.go`, `pkg/api/telemetry_checkpoint.go`, `pkg/storage/schema_migrations.go` v86).
- **`TEL-A2` & `TEL-B1`**: Qualified source-window coverage v4, append-only in-run policy segments, and `telemetry_evidence.json` archive sidecar (`pkg/api/telemetry_coverage.go`, `pkg/api/telemetry_policy_segments.go`, `pkg/api/telemetry_evidence.go`, `pkg/api/telemetry_roadmap_test.go`).
- **`TEL-B2`**: Complete 10-consumer SLA projection parity, centralized credential + run-rule sentinel scrubbing, fail-closed archive artifact sanitization, and regression matrix (`pkg/api/telemetry_text_scrub.go`, `pkg/api/archive_export_sanitize.go`, `pkg/api/telemetry_text_scrub_test.go`, `docs/qa/TELEMETRY_ACCEPTANCE.md`).

---

## 2. Wave 4 (`INT-1`) End-to-End Non-Messaging Release Gate

`TestRoadmapIntegratedNonMessagingReleaseScenario` in `pkg/api/roadmap_integrated_non_messaging_test.go` chains all 6 non-Messaging workflow phases in a single deterministic integration gate:

1. **Phase 1 (`Phase1_AuthoringImportValidationAndReceiptRotation`)**:
   - Exercises Contract v2 script & scenario creation with idempotent replay after edits, live `RotateAuthoringReceiptCredentialKey` re-wrapping across `master-v1` -> `master-v2`, `ScenarioValidationContext` composite digest transitions across secret provisioning and rotation, and real-HTTP session-backed import save persisted-copy parity (`runAuthoringFidelitySessionImportAndLifecycleEdges` + `assertAuthoringFidelitySessionImportReopen`).
2. **Phase 2–4 (`Phase2To4_MultiCohortProtocolsTelemetryByteQueriesAndConsumerParity`)**:
   - Populates multi-cohort baseline and candidate runs combining stateless HTTP (`http_body.v1`), browser cohort (`attachD2Documents`), gRPC (`grpc_uncompressed_payload.v1` + streaming overlap), WebSocket (`ws_application_payload.v1` + Ping/Pong RTT), and Java HTTP (`contained_jvm_helper_v1` shallow + reachable-graph footprint).
   - Exercises `TEL-A1` browser calibration and foreground presentation proof registration, 2-segment `TEL-A2`/`TEL-B1` policy receipts and `telemetry_evidence.json` sidecar, admitted `NA-C1` byte queries across `http`, `grpc`, and `websocket`, `NA-C2` frozen `PanelInputs` v2 dashboard persistence and `SavedAnalysisPanels` `"ready"` -> `"stale"` transitions, and `TEL-B2` consumer parity with zero sentinel leakage.
3. **Phase 5 (`Phase5_NativeAnalyticsProvenanceWriterOwnershipAndJointLedger`)**:
   - Exercises `NA-A1` multi-chunk duplicate provenance detection across distinct chunk splits, `NA-C3` remote writer exit + `POST /api/v1/analysis/imports/{id}/attest-writer-stopped` physical cleanup, and `NA-B1` durable `JointStreams` sequence ledger round-trip across `telemetryCheckpoint`.
4. **Phase 6 (`Phase6_StatelessHTTPEvacuationAndFailoverReconciliation`)**:
   - Executes `runRECCActiveControllerAndWorkerLoss` against real `generatorrpc.Service` workers and an authenticated HTTP fault target, verifying capacity transfer, deposed-owner fencing, `DuplicateGenerationOverlap == 0`, and `RetainedEvidenceTransactions == TargetTotalRequests`.
