# QA Review — Replay HAR Crash, Shared-Filesystem, and Cloud-Emulator Qualification (`BE-B5` Local)

- **Date:** 2026-10-27
- **Package / Slice:** `BE-B5` local (`pkg/api`)
- **Status:** Qualified (local crash, two-controller shared filesystem, and containerized S3/Azure Blob/GCS object-store emulators; live production cloud credentials remain external)

## Scope

1. **Symlink-Resolved Local Replay Archive Root (`pkg/api/replay_artifact_backend.go`):**
   - `openReplayArtifactBackend` now resolves `backend.LocalRoot` via `resolveArchiveDirectoryRoot(backend.LocalRoot, false)` (`filepath.EvalSymlinks` after `filepath.Abs`) before calling `openArchiveDirectoryNoSymlink(backend.LocalRoot)`.
   - Fixes macOS `t.TempDir()` (`/var/folders/...` -> `/private/var/folders/...`) `ELOOP` failures across all 29 existing `TestReplayArtifact*` tests while keeping `O_NOFOLLOW` protection on the resolved directory root and all descendant components.

2. **Abandoned Native HAR Working-Directory Reclamation on Replay Cleanup (`pkg/api/replay_artifact_cleanup.go`):**
   - `(*Server).startReplayArtifactCleanup` now invokes `browser.ReclaimRuntimeHARWorking(ctx, strings.TrimSpace(os.Getenv("APEXLOAD_BROWSER_HAR_DIR")), 128)` alongside `s.replayArtifacts.Sweep(ctx)` on startup and each periodic sweep tick.
   - Ensures local `.apexload-har-work/<generation>/working.har` working directories left behind by a `SIGKILL`ed controller/replay process are reclaimed once both the owner process and writer tree have exited.

3. **Custom Endpoint & Emulator Support for S3, Azure Blob, and GCS Replay Backends (`pkg/api/archive_backend.go`):**
   - **S3 (`s3blob`):** Appends `s3ForcePathStyle=true` when `b.Endpoint` is configured, `disable_https=true` for `http://` endpoints, and defaults `region=us-east-1` when `b.Region` is empty on custom endpoints.
   - **Azure Blob (`azureblob`):** Opens buckets via a per-call `azureblob.URLOpener` (rather than `blob.DefaultURLMux()`'s process-global `lazyOpener` `sync.Once`) and formats `domain`, `protocol`, `localemu=true`, and `storage_account` query parameters when `b.Endpoint` is set.
   - **GCS (`gcsblob`):** Opens custom-endpoint / `STORAGE_EMULATOR_HOST` buckets via `gcsblob.OpenBucket` with `option.WithoutAuthentication()`, `option.WithEndpoint(...)`, `option.WithHTTPClient(...)`, and `cloudstorage.WithJSONReads()` (avoiding `gcsblob.lazyCredsOpener`'s process-global `sync.Once` and XML path-encoding incompatibilities in `fake-gcs-server`).

4. **Qualification Tests (`pkg/api/replay_artifact_qualification_test.go`):**
   - `TestReplayArtifactSIGKILLMidReplayAndRestartReclamation`: Spawns an isolated child controller helper process (`runReplaySIGKILLChild`) across three stages (`pre-stage`, `unjoined-stage`, `post-publish`), sends `SIGKILL` directly to the child PID, starts a replacement controller against the shared SQLite store and archive/HAR directories, and verifies:
     - `pre-stage` returns HTTP `404 Not Found` (before any artifact receipt is reserved) while its retained session lease blocks session-ID reuse (`TryAcquireReplaySessionLease`) and reclaims the dead child's `.apexload-har-work/<gen>/working.har` directory.
     - `unjoined-stage` returns HTTP `503 Service Unavailable` with no synthetic HAR fallback, preserves the unjoined receipt reservation against session-ID reuse across `Sweep`, and reclaims the dead child's `.apexload-har-work/<gen>/working.har` directory.
     - `post-publish` serves HTTP `200 OK` with `X-APEXLoad-Replay-Durability: durable`, `X-APEXLoad-HAR-Fidelity: native`, and byte-identical native HAR bytes, and reclaims the dead child's `.apexload-har-work` directory.
   - `TestReplayArtifactTwoControllersSharedFilesystemHandoff`: Verifies two concurrent `Server` instances sharing `PostgreSQL`/`SQLite` + a shared POSIX directory with `source.v1` volume identity immediately serve status and native HAR on Controller 2 without restart, while a third controller pointed at a foreign filesystem root fails closed with `503` and does not corrupt or delete the shared artifact during `Sweep`.
   - `TestReplayArtifactCloudEmulatorsCrashAndRestart`: Qualifies `IfNotExist` duplicate-generation rejection, Controller 1 publication -> shutdown/crash -> Controller 2 recovery with a fresh `APEXLOAD_ARCHIVE_STAGING_DIR`, unjoined `503` fail-closed retention, and project-delete `Sweep` cleanup across `s3-minio`, `azure-blob-azurite`, and `gcs-fake-gcs-server` (both deterministic in-process protocol servers and real Docker containers `bitnamilegacy/minio:2025.7.23-debian-12-r5`, `mcr.microsoft.com/azure-storage/azurite:latest`, and `fsouza/fake-gcs-server:latest`).

## Verification Evidence

- Existing replay artifact suite on macOS:
  - `go test -count=1 ./pkg/api ./pkg/storage ./pkg/recordbridge -run 'TestReplayArtifact'` -> 29/29 PASS.
- Qualification suite with `-race`:
  - `go test -race -count=1 ./pkg/api -run 'TestReplayArtifact(SIGKILLMidReplayAndRestartReclamation|TwoControllersSharedFilesystemHandoff|CloudEmulatorsCrashAndRestart)'` -> PASS (`21.756s`).
- Real Docker container qualification (`bitnamilegacy/minio:2025.7.23-debian-12-r5`, `mcr.microsoft.com/azure-storage/azurite:latest`, `fsouza/fake-gcs-server:latest`):
  - `APEXLOAD_TEST_MINIO_ENDPOINT=... APEXLOAD_TEST_AZURITE_ENDPOINT=... APEXLOAD_TEST_FAKE_GCS_ENDPOINT=... go test -v -count=1 ./pkg/api -run '^TestReplayArtifactCloudEmulatorsCrashAndRestart$'` -> PASS (`s3-minio` `0.25s`, `azure-blob-azurite` `0.24s`, `gcs-fake-gcs-server` `0.16s`).
- Mutation verification (`go test -overlay`):
  - Reverting `resolveArchiveDirectoryRoot(backend.LocalRoot, false)` to `filepath.Abs(backend.LocalRoot)` in `pkg/api/replay_artifact_backend.go` fails `TestReplayArtifactTwoControllersSharedFilesystemHandoff` (`symlink alias sourceSHA256 ... != canonical sourceSHA256 ...`).
  - Removing `browser.ReclaimRuntimeHARWorking` from `(*Server).startReplayArtifactCleanup` in `pkg/api/replay_artifact_cleanup.go` fails `TestReplayArtifactSIGKILLMidReplayAndRestartReclamation/pre-stage` (`working HAR file ... was not reclaimed after child SIGKILL + replacement server startup`).
