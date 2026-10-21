# Filesystem replay source identity — 2026-10-21

Review before activating controller replay recovery found that a filesystem path alone could not distinguish independent disks mounted at the same path on different hosts. A cleanup worker could otherwise mistake absence on the wrong disk for successful deletion of the original artifacts. This slice fixes that ownership boundary before runtime activation.

## Contracts

The reserved replay namespace retains a random, immutable `source.v1` marker. Initialization syncs a private temporary file, publishes it with an exclusive hard link and syncs the containing directories. Concurrent openers converge on the same complete identity; corrupt or linked markers fail closed. The filesystem source digest now binds the configured path and persisted namespace identity. Object-backend source digests are unchanged.

Each filesystem adapter holds its opened root directory for its lifetime. Stage, verified reads and exact cleanup use that handle, so replacing the configured mount/path cannot redirect an admitted operation to another disk. Operations also recheck the retained marker, and successful staging rechecks it before providing publication proof. File creation/deletion and their directory entries are synced, including cleanup retries that find an already-absent generation.

A fresh replacement volume cannot read or acknowledge cleanup of the original receipt. Historical path-only receipts are preserved but refused: their digest cannot prove the volume. No migration silently adopts their files or releases their reservation. This implementation was still passive when the boundary was corrected.

The marker belongs to the archive namespace and must be preserved with an intentional restoration of that namespace. Copying it asserts the same source authority; it is not proof that independently cloned disks provide shared storage. The filesystem must support exclusive hard links and directory synchronization; unsupported operations fail closed. Actual multi-host filesystem, native Windows/Linux and cloud-backend qualification remain separate gates.

## Review

The initial fix added persistent source identity and held-root operations. Follow-up review added rechecks for a marker changed after opening and directory synchronization on cleanup retries. Regression tests cover reopening, concurrent initialization, same-path replacement, preserved foreign generations, legacy receipt refusal and corrupt/linked/changed markers. A final review of source ownership, publication ordering, read verification, cleanup and compatibility found no remaining findings, including low severity.

Controller recovered authorization/download/cleanup wiring remains open.

## Validation

Initial targeted checks pass with 58 test/subtest events. After the final changed-marker fence was added, three race-detector repetitions of all replay volume, backend and publisher checks pass with 177 events, no skips and no failures. The final source hashes remained unchanged through that run.

Documentation checks pass for 322 active Markdown files; whitespace checks pass. Local evidence under `/private/tmp/apex-browser-durable-backend/` includes `volume-targeted.jsonl`, `volume-reviewed-race.jsonl`, `volume-reviewed-source-hashes.json` and `volume-docs.log`. Tests ran on the local macOS filesystem and the existing file-backed object fixture; they do not certify remote shared filesystems or native Windows/Linux behavior. The broader serial final-source Go gate remains required before merge.
