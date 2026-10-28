# NA-C3 Physical Writer Ownership Completion & NA-D1 Local macOS (`darwin/arm64`) Native Suite — 2026-10-28

## Overview

This review record documents the implementation and local `darwin/arm64` native verification of:

1. **NA-C3: Physical Writer Ownership Completion, Remote Store Emulators & Intervention**
   - **Mint-time volume identity and locality binding (`_analysis/source.v1`)**: Materializes `_analysis/source.v1` at import creation (`POST /api/v1/analysis/imports`), upload writer `Begin`, and normalization attempt `Claim`, binding the archive volume UUID in `app_settings` (`analysis_volume_identity_v1`) via compare-and-set (`EnsureAnalysisVolumeIdentity`) and recording `AnalysisWriterVolume{Identity, LocalFilesystem}` on each minted `AnalysisAttempt` and `AnalysisUploadWriter`.
   - **Judge and cleanup non-creation preserved**: `verifyAnalysisVolumeFile` (used by `ensureAnalysisBackendFor` and `analysisWriterExitScope`) never creates `_analysis` or `source.v1`. Before any volume is bound in `app_settings`, a missing `_analysis` behaves identically to prior behavior; once bound, a missing or mismatched `_analysis/source.v1` (such as an unmounted NFS/SMB share or replaced mountpoint) fails closed with `fs.ErrNotExist` or a volume mismatch error.
   - **Per-attempt original copies (`original-<attemptID>-<fileID>.parquet`)**: Normalization attempts plan and write attempt-scoped original keys (`AnalysisAttempt.Originals` and `AnalysisImport.Originals`), eliminating cross-attempt overwrites from late remote copies after a lost writer and including prior attempts' original keys in `ObsoleteObjects` and `AnalysisAttemptCleanupPlan`.
   - **Worker staging reclamation**: Normalization attempts bind their dataset and attempt identity to private staging directories under `_analysis-cache/job-<datasetID>-<attemptID>-*`, atomically writing `.staging-owner.json` (controller incarnation) before worker spawn and `.worker-owner.json` (controller + worker child incarnation) across worker process start. `ReclaimExitedStagingDirectories` reclaims orphaned staging directories during the writer ownership sweep only after `procidentity` proves both the controller and worker incarnations have exited.
   - **Administrator writer-stopped intervention (`POST /api/v1/analysis/imports/{id}/attest-writer-stopped`)**: Allows project/system administrators to attest that all remote (`remote_commits_unproven`), unjudgeable (`unjudgeable_writers`), unrecorded (`unrecorded_writers`), or legacy (`legacy_writers`) writers of a `cancelled`, `failed`, or `deleting` dataset have stopped, recording `AnalysisWriterProofAdminAttested` and an `analysis.import.attest-writer-stopped` audit event while refusing self-attestation of a writer still active on the attesting controller (`409 Conflict`).

2. **NA-D1: Local macOS (`darwin/arm64`) Native Suite & Deterministic Reference Fault Trials**
   - **Deterministic fault trials (`TestNativeAnalyticsReferenceFaultTrials`)**: Exercises explicit fault injection during reference normalization and query execution in `pkg/api/analysis_reference_benchmark_native_test.go`, covering worker crash mid-normalization with staging reclamation and retry publication, cancelled dataset with remote unproven writer resolved via administrator attestation, and query cancellation under bounded DuckDB memory limits (`512 MiB` / `128 MiB`).
   - **Local `darwin/arm64` native DuckDB verification**: Executes the full `pkg/analysis/duckdb`, `pkg/storage` (SQLite + PostgreSQL 16), `pkg/internal/procidentity`, `pkg/internal/localfs`, and `pkg/api` (`-tags analytics_duckdb`) suites natively on `darwin/arm64`.
   - **CGO-free binary verification**: Confirms `./cmd/apexctl` and `./cmd/generator` remain free of `pkg/analysis/duckdb` and `github.com/marcboeker/go-duckdb`.

## Key Files Modified

- `pkg/storage/analysis_backend.go` — `GetAnalysisVolumeIdentity`, `EnsureAnalysisVolumeIdentity`, and `RebindAnalysisVolumeIdentity`.
- `pkg/storage/analysis_imports.go` — `Originals []string` and `WriterAttestation *AnalysisWriterAttestation` on `AnalysisImport`.
- `pkg/storage/analysis_attempts.go` — `Originals []string` and `Volume *AnalysisWriterVolume` on `AnalysisAttempt`, per-attempt original key planning in `ClaimAnalysisImportAttempt`, validation in `ValidateAnalysisPublication`, publication in `PublishAnalysisImportAttempt`, and cleanup planning in `AnalysisAttemptCleanupPlan`.
- `pkg/storage/analysis_upload_writers.go` — `Volume *AnalysisWriterVolume` on `AnalysisUploadWriter`.
- `pkg/storage/analysis_cleanup.go` — `Originals` tracking and `WriterAttestation` integration in `queueAnalysisImportAttemptsCleanupTx` and `AnalysisDatasetCleanupStatus`.
- `pkg/storage/analysis_writer_ownership.go` — `AnalysisWriterProofAdminAttested`, `ErrAnalysisWriterActive`, `AnalysisWriterAttestation`, `AnalysisWriterVolume`, `WithAnalysisWriterVolume`, `AnalysisWriterExitScope.VolumeIdentity`, `localFor`, and `AttestAnalysisImportWritersStopped`.
- `pkg/storage/analysis_writer_ownership_test.go` — SQLite and PostgreSQL tests for volume identity compare-and-set, mint-time locality binding, per-attempt originals isolation, and administrator writer attestation.
- `pkg/analysis/duckdb/worker_staging_reclaim.go` — `WithNativeStagingScope`, `BindStagingAttemptScope`, `.staging-owner.json` / `.worker-owner.json` persistence, and `ReclaimExitedStagingDirectories`.
- `pkg/analysis/duckdb/worker_pipeline.go`, `pkg/analysis/duckdb/worker_attempt.go`, `pkg/analysis/duckdb/worker_parent.go`, `pkg/analysis/duckdb/worker_resources.go` — Staging owner recording, worker child incarnation recording, and metadata file exclusion from scratch byte quotas.
- `pkg/api/analysis_backend.go` — `readAnalysisVolumeSourceFile`, `verifyAnalysisVolumeFile`, and `ensureAnalysisVolumeFile`.
- `pkg/api/analysis_writer_ownership.go` — Mint-time volume binding in `bindAnalysisWriterIncarnation`, volume verification in `analysisWriterExitScope`, and staging reclamation in `reconcileAnalysisWriterIncarnationsOnce`.
- `pkg/api/analysis_attempt_pipeline.go` — Per-attempt original key writes in `normalizeAnalysisImportOwned`.
- `pkg/api/handlers_analysis_imports.go` & `pkg/api/route_registry_analysis.go` — `handleAnalysisImportAttestWriterStopped` (`POST /api/v1/analysis/imports/{id}/attest-writer-stopped`), mint-time volume initialization on import creation, and staging scope binding on preview/commit.
- `pkg/api/analysis_writer_ownership_test.go` & `pkg/api/analysis_writer_ownership_native_test.go` — API and native DuckDB tests for mint-time volume identity, unmounted share refusal, worker staging reclamation, per-attempt originals, and administrator attestation across `s3`, `azure_blob`, and `gcs`.
- `pkg/api/analysis_reference_acceptance_test.go` & `pkg/api/analysis_reference_benchmark_native_test.go` — `analyticsReferenceFaultTrial` contract and `TestNativeAnalyticsReferenceFaultTrials`.

## Local `darwin/arm64` Verification Summary

| Suite / Check | Command | Result |
| --- | --- | --- |
| Process identity & filesystem locality (`darwin/arm64`) | `go test -count=1 ./pkg/internal/procidentity ./pkg/internal/localfs` | PASS (`0.085s` / `0.083s`) |
| Storage analysis suite (SQLite) | `go test -count=1 ./pkg/storage -run 'TestAnalysis.*'` | PASS (`0.794s` on writer ownership, `10.955s` full analysis storage) |
| Storage analysis suite (PostgreSQL 16) | `APEXLOAD_TEST_POSTGRES_URL="postgres://postgres:apexload@127.0.0.1:55432/apexload?sslmode=disable" go test -tags postgres_integration -count=1 ./pkg/storage -run 'TestPostgresAnalysis.*'` | PASS (`2.353s` on writer ownership, `19.935s` full Postgres analysis storage) |
| DuckDB engine unit & containment suite (CGO-free) | `go test -count=1 ./pkg/analysis/duckdb` | PASS (`52.472s`) |
| DuckDB engine native suite (`-tags analytics_duckdb`, `darwin/arm64`) | `CGO_ENABLED=1 go test -tags analytics_duckdb -count=1 ./pkg/analysis/duckdb` | PASS (`156.085s`) |
| API native analytics, writer ownership, acceptance populations & fault trials (`-tags analytics_duckdb`, `512 MiB` / `128 MiB`) | `CGO_ENABLED=1 APEXLOAD_ANALYSIS_DUCKDB_MEMORY_LIMIT_MIB=512 APEXLOAD_ANALYSIS_WORKER_QUERY_MEMORY_LIMIT_MIB=128 go test -tags analytics_duckdb -count=1 -v ./pkg/api -run 'TestNative.*\|TestAnalysisWriterOwnership.*\|TestAnalysisAcceptance.*'` | PASS (`88.382s`) |
| CGO-free binary dependency check (`apexctl`, `generator`) | `go list -deps ./cmd/apexctl ./cmd/generator \| grep -E 'duckdb'` | PASS (0 matches; no DuckDB dependency) |
