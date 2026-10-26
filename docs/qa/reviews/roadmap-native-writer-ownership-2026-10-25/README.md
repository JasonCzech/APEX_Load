# Native physical-writer ownership across restart and HA — 2026-10-25

This [NA-C slice](../../../ROADMAP_IMPLEMENTATION_SPEC.md#na-c--complete-workflows-and-durable-lifecycle) implements the first part of "Finish durable physical-writer ownership across controller restart and remote backends." A controller now records which process writes each import's artifacts. When that process dies, a controller on the same host and in the same PID namespace proves the exit and resumes the import's lifecycle. HA takeover is covered only for a replica that shares that PID namespace; a replica on another host never judges.

**Deployments without restart recovery.** Recovery needs a successor in the dead controller's PID namespace: a process restarted in place under systemd, launchd, a service manager or the desktop app. The Helm chart (no `shareProcessNamespace`) and docker compose restart the controller in a new container, which is a new PID namespace, so those deployments get no restart recovery from this slice: their interrupted work stays owned and their datasets deletion-pending, reported as `unjudgeable_writers`.

Every case it cannot prove keeps its ownership, and its dataset stays explicitly deletion-pending. The operator view is in the [native analytics guide](../../../NATIVE_ANALYTICS.md#import-and-compare-parquet).

## Starting point

**Writer generations and their fences.** Two kinds of physical writer generation existed, each with exact, generation-unique keys:
- **Normalization attempt.** Minted by `ClaimAnalysisImportAttempt` into `analysis_attempt_journal`. Its identity is the attempt ID and import revision, and its planned keys are `normalized-<attempt>-part|reject-….parquet`.
- **Upload chunk writer.** Minted by `BeginAnalysisUploadWriter` into `analysis_upload_writers`. Its keys are `chunk-<file>-<offset>-<sha>-<writer>` and `upload-<writer>.tmp`.

Three fences protected them:
- **Publication fence.** `PublishAnalysisImportAttempt` publishes only under the project lock. It requires the matching owner, revision, input digest, a live SQL lease, the absolute expiry and `WritersQuiesced`. An ambiguous commit is decided by the journal's publication digest.
- **Cleanup queue.** `analysis_cleanup_jobs` deletes a generation's keys only after that generation's own physical proof. It runs on the analytical backend pinned by `analysis_storage_identity_v1`.
- **Dataset namespace removal.** This waits for every generation of the dataset.

**Proof was process-local.** The only physical proof came from the writer's own process:
- `ConfirmAnalysisAttemptWritersQuiesced`, after the pipeline's exact joins;
- `CompleteAnalysisUploadWriter`, after the writer's `Close` or `Sync` returned nil.

Their retry records (`nativePendingWriterPipelines`, `nativePendingPublicationPipelines`, `analysisUploadOwners`) and the heavy-work gate (`nativePipelineGate`, one pipeline per process) lived in memory. The code says so: "they are not proof of cross-process or cross-host writer termination."

**What a controller crash, restart or HA takeover left behind, by boundary:**

| Boundary | State left behind | Before this slice |
| --- | --- | --- |
| Upload, before completion | A `pending` writer row, never physically closed | Its offset was blocked forever (`ErrAnalysisUploadInFlight`). The dataset could never be deleted. |
| Upload, after completion | An accepted writer and its cursor, in one transaction | Nothing pending. |
| Normalization, before writer quiescence | Import `normalizing` with a lease of at most 1 minute. The attempt was not quiesced. | After the lease expired, a commit could start a new generation with new keys. The old attempt's cleanup and the dataset's deletion stayed blocked forever, and its private staging under `_analysis-cache` leaked. |
| After quiescence, before publication | The attempt quiesced, but the import still `normalizing` | The import stayed `normalizing` until its lease expired. |
| Artifact commit and publication | One SQL transaction, with the journal as receipt | The journal decided it; nothing was lost. |
| Cleanup | A durable, idempotent job | It ran again. Its keys are generation-unique, so a late delete can only hit its own generation. |
| Cold HA replica | Same as a restart | The replica could not tell a dead controller from a live, partitioned one, so everything stayed retained. A lease expiry allowed a retry, never cleanup, as the spec requires. |

**What the backends guarantee about a write the controller can no longer observe:**
- **Local filesystem** (`os.Root`: write a temp file, `Sync`, `Rename`, sync the directory). Every write is a synchronous system call of the writing process, so once that process has exited nothing more can be written.
- **S3, Azure Blob and GCS** (gocloud `blob`). An object becomes visible only when its writer commits. Incomplete multipart uploads, uncommitted blocks and unfinalized resumable sessions never appear as objects. A commit request that the process had fully sent before dying can still be applied afterwards, and no provider bounds that delay. Process exit therefore proves that no new request will be issued, but not that no write can still land.
- **A `Close` error** is retained as unjoined in the process, which is correct.

## Contract

**Writer incarnation.** Claim and Begin record the incarnation of their writer in the same transaction that mints the generation, before any artifact I/O.
- The incarnation is a `procidentity.Identity` of the controller process: machine, boot, PID namespace, PID and kernel start time.
- It is stored as JSON fields: `incarnation` and `termination` on the attempt journal, `Incarnation` and `Termination` on the upload writer. There is no SQL schema change.
- The API binds the identity through `storage.WithAnalysisWriterIncarnation`, in `handleAnalysisCommit` and `runAnalysisUploadChunk`. A context without one mints a generation that no exit proof can retire.
- `pkg/internal/procidentity` is the reviewed HAR crash-reclamation proof, moved out of `pkg/protocol/browser`, which now delegates to it.

**Exit proof** (`procidentity.Gone`). A recorded process is gone only when:
- the same identified machine has booted again; or
- within the same boot, on the same machine identity and in the same PID namespace, no process has that PID with that start time.

A process on another machine, boot or namespace is never judged; the machine identity is compared in the same-boot case too, so a boot value two hosts share (the Windows boot counter, VMs cloned from a memory snapshot) cannot let one host's process table answer for another's. Platform rules:
- **Linux.** A missing `/proc` entry counts as gone only when `kill(pid, 0)` returns `ESRCH`, because `hidepid` and `ProtectProc` hide live processes. A `/proc` that does not describe this process's own PID namespace makes the identity unavailable. The time namespace, when the kernel has one, is part of the namespace, because start times are counted in it. Without a machine-id the random per-boot `boot_id` alone scopes the lookup.
- **Windows.** Without a `MachineGuid` and host name the identity is unavailable, because the boot sequence number is a small counter other hosts share.
- **macOS.** The per-boot session UUID scopes the lookup; with no machine identity an earlier boot proves nothing.
- A process restored by checkpoint/restore (CRIU) is unsupported.

These rules also apply to HAR crash reclamation, which uses the same package.

**Exit scope.** A controller judges with its own identity and the pinned backend. It never judges its own process. It grants no scope while the current backend differs from `analysis_storage_identity_v1`, and with no identity of its own. A filesystem backend counts as local only when `localfs.IsLocal` places its artifact directory, `<archive root>/_analysis` (the directory every write, read and cleanup opens with `os.OpenRoot`), on an allowlisted local filesystem. The directory is opened and judged through that handle (`fstatfs` on Linux and macOS; on Windows `GetFinalPathNameByHandle`, so a directory link or junction to a share is judged by its target, and `GetVolumeInformationByHandle` for the filesystem name). The allowlist is:
- **Linux:** ext2/3/4, XFS, Btrfs, F2FS, bcachefs, ZFS, tmpfs, ramfs or overlayfs. The 32-bit `f_type` is read unsigned.
- **macOS:** APFS or HFS with `MNT_LOCAL`.
- **Windows:** a fixed drive formatted NTFS or ReFS, not reached through UNC, by the same policy as `winfs.ValidateVolume(driveType, fs, "NTFS", "REFS")`. A WinFsp mount (rclone, sshfs-win) reports a fixed drive with its own filesystem name and is refused.

NFS, SMB/CIFS, EFS, Azure Files, Ceph, GlusterFS, 9p, virtiofs, every FUSE filesystem and every unlisted local type can complete a write after its process is reaped, or are not qualified, so a filesystem backend there is judged like an object store. Only the `_analysis` directory itself is judged; a filesystem mounted below it is not seen, and the operator guide forbids one. A probe that fails grants no scope for that sweep: it is retried, never recorded as remote.

**The judge never creates the artifact directory.** Writers create `_analysis` on their first artifact write (`analysisObjectWrite`, mode 0700), as before this slice. The exit scope (the sweep and an upload retry's exit proof) and DELETE never create it; DELETE's status judges through `analysisWriterHere` (`procidentity.Judgeable`), not the exit scope. A missing `_analysis` fails the locality probe, so that pass grants no scope and judges nothing, and a later pass retries; an upload retry that meets it logs a rate-limited warning naming the directory and the workaround. Cleanup keeps its existing rule that a missing `_analysis` holds nothing to delete, and never creates it either. Two consequences are listed in Remaining: a fresh backend's first writer that died before any write created `_analysis` stays owned until a later write or the operator creates it, and the existing behaviour on an unmounted share is unchanged. Round 4 let the sweep create the directory and round 5 replaced that with a marker-bound root; both were dropped (Review rounds 5 and 6).

**Effects of a proven exit:**

| Generation | Filesystem backend on a local filesystem | S3, Azure Blob, GCS, or a network/FUSE filesystem |
| --- | --- | --- |
| Attempt still owning a `normalizing`, unpublished import | `WritersQuiesced` and a termination record. In the same transaction the import becomes `failed` ("the controller running it exited; commit the import again to retry"), its revision advances, its owner is cleared and the attempt's cleanup is queued. | The same failure and termination with `remote_commit_unproven`. `WritersQuiesced` stays false. |
| Attempt its own process had already quiesced, still owning a `normalizing`, unpublished import | A termination record, and the same failure | A termination record without `remote_commit_unproven` (its writes were already proven stopped), and the same failure |
| Attempt otherwise unquiesced (failed, replaced, cancelled or deleted) | Becomes quiesced, so its exact cleanup plan runs | Records `remote_commit_unproven`; its cleanup stays blocked |
| Unclosed upload writer | Becomes `abandoned` and physically closed, with a termination record. Its exact temporary and object keys are queued for cleanup. | Becomes `abandoned` with a termination record and `remote_commit_unproven`, but not closed. Begin may reuse its offset with a new writer and new keys; its cleanup stays blocked. |
| Published generation, closed upload writer, or quiesced attempt that owns no normalizing import | Unchanged | Unchanged |

**Where proofs run:**
- **Sweep.** Every replica sweeps a bounded inventory at startup and about once a minute (`reconcileAnalysisWriterIncarnationsOnce`): at most 400 candidates per pass. Permanently blocked (remote-unproven, unjudgeable) and not-yet-due cleanup jobs stay in the inventory, so a backlog is worked through at about 400 outstanding generations a minute per replica. The inventory pages, by key, through the owners of normalizing imports, then through attempts and upload writers held by the cleanup queue.
- **Retried upload.** When an upload retry finds its offset blocked, the controller resolves that exact offset and retries Begin once.
- **Locking.** An unlocked snapshot first settles every generation this host cannot judge, and every settled generation that no longer owns its import. Only a possible change takes the project lock, where everything is decided again.

**Deletion-pending answer.** DELETE of a dataset that is still owned answers 503 with this body:
- `code: analysis_dataset_deletion_pending`;
- `pending` counts:
  - `unproven_writers`: recorded where this host can judge it (`procidentity.Judgeable`: the same machine, boot and PID namespace, or an earlier boot of the same identified machine), so it may settle: it closes, or this host proves its exit at a later sweep. Under `hidepid`, a PID reused by another user's process stays undecidable, so such a writer can stay here;
  - `unjudgeable_writers`: recorded where this host cannot judge it: another machine, or another boot or PID namespace of this one. It resolves only if it is still running and closes, or a controller that can judge it proves its exit. A restarted container never can; a replaced pod can only if its host name and machine ID match its predecessor's, which the machine identity requirement forbids for distinct hosts;
  - `unrecorded_writers`: journalled before incarnations, or rewritten by an older controller;
  - `remote_commits_unproven`;
  - `legacy_writers`: a legacy upload or owner without a journal, or an unplanned output;
- `retryable`: some writer may still resolve without an intervention (unproven, unjudgeable or unrecorded);
- `Retry-After: 60` only while this host itself may settle an unproven writer.

Earlier, a 503 here came in the generic redacted envelope. The import stays `cancelled` or `deleting`. A repeated DELETE of an already revoked import only re-reads this status and retries the physical deletion: it no longer saves the import again, which re-queued every cleanup job 20 minutes later each time.

**Rolling upgrade.**
- **Reading.** New controllers read rows without the new fields; those generations are unrecorded and stay retained.
- **Begin at an older controller.** Older controllers ignore the fields and keep blocking Begin on any unclosed writer, which fails closed.
- **Older rewrites.** An older controller that rewrites a row drops the fields:
  - claiming again, failing, cancelling or completing drops the incarnation, so that generation becomes unrecorded;
  - a remote termination it drops is reported as unrecorded.

  Each of these fails safe.

## Invariants

1. No generation is quiesced or closed, and no import is failed, without a durable termination record proving that exactly its recorded process exited, judged by a different process that can judge it (`procidentity.Judgeable`: the same machine, boot and PID namespace, or the same identified machine after a reboot). The locked transaction decides again before any write.
2. A replica never judges its own process, an unrecorded generation, a legacy sentinel, or a process on another machine, boot or PID namespace. A cold replica on another host therefore keeps everything owned.
3. A remote exit retires the generation from the workflow, but its cleanup and its dataset's deletion stay blocked (`remote_commit_unproven`).
4. The proof that fails an owned import is written in the same transaction that advances its revision and clears its owner. A late `Publish`, `Fail` or quiescence seal from the exited generation cannot adopt the import.
5. A retired upload writer is `abandoned`: a late `Complete` cannot accept it or advance the cursor, which references only the replacement's key.
6. A cleanup plan holds only its generation's own keys, plus obsolete keys of generations already proven quiesced. A retry never plans its own live keys. Plans run only on the pinned backend.
7. A dataset namespace is removed only when every generation is quiesced or closed. Legacy, unrecorded and remote-unproven writers keep the dataset explicitly deletion-pending.
8. A process-local close callback, a cancellation and a lease expiry are never proof. A lease expiry still allows only a new generation.
9. Only a filesystem backend on an allowlisted local filesystem treats an exit as proof that no later write can land; every other backend keeps `remote_commit_unproven`.
10. A filesystem exit is judged only against an existing `_analysis`. The exit scope (sweep, upload retry), DELETE and cleanup never create it; only an artifact write does.

## Validation

**Storage** (`exerciseAnalysisWriterOwnership`, run on SQLite with two stores and on PostgreSQL 16 with two independently opened stores, `TestPostgresAnalysisWriterOwnershipRestartAndReplica`). Controller A mints generations and exits; B, a restart or a replica, judges A. The suite injects that exit:
- **Upload, before completion.** For the filesystem and for remote:
  - a live, self or other-host writer keeps its offset;
  - a stale cursor revision is refused;
  - no cleanup job exists while the writer is live, and one is queued for exactly that writer when its exit is proven;
  - after A's proven exit the writer is retired, and a replacement writer gets new keys;
  - a late `Complete(accepted)` cannot advance the cursor;
  - the cursor references only the replacement's key;
  - the cleanup plan holds exactly the exited writer's temporary and object keys;
  - without a late Complete, remote keeps cleanup blocked and the cancelled dataset deletion-pending.
- **Upload, after completion.** Not judged again; the cursor is unchanged.
- **Unrecorded (legacy) upload writer.** Never retired; it keeps blocking.
- **Normalization, before quiescence.** For the filesystem and for remote:
  - no cleanup job exists while the attempt is live, and the attempt's job is queued with the failure;
  - the import fails with the reason, its revision advanced and its owner cleared;
  - a repeated resolution changes nothing;
  - a late `Publish` and a late `Fail` are refused;
  - the cleanup plan holds exactly the attempt's own keys (filesystem) or is blocked (remote);
  - the retry is a new generation whose keys are disjoint, whose cleanup never touches its own keys, and which its predecessor's exit cannot judge.
- **After quiescence, before publication.** The import fails, the termination is recorded without `remote_commit_unproven` even under a remote scope, and a late publication is refused.
- **After publication**, including an ambiguous commit reply. Not judged again; the journal receipt decides, and the ready revision is unchanged.
- **Lease expired, live re-claim, then exit proven** (the common restart path), for the filesystem and for remote: A's lease expires, live C claims again, and A's exit is then proven. A is proven (and, on the filesystem, quiesced) while C's owner and revision are unchanged.
- **Cancellation while in flight.** Before proof the dataset is not ready. After proof, the filesystem becomes ready and remote does not, and a late publication is refused.
- **Live, self and unrecorded attempts** stay owned, and a scope without a judge is refused.
- **Deletion-pending reasons**, each with its exact count, judged from host A:
  - running, unrecorded, remote and pre-journal legacy writers;
  - running, unrecorded and remote upload writers;
  - a writer recorded on another host's boot (unjudgeable);
  - a published output whose journal is gone (unplanned, so legacy).
- **Pending deletion classification**: `Resolvable` and `ResolvesHere` for each reason alone and in combination.
- **Candidate inventory** pages through both phases, finds owners, cleanup-held attempts and upload writers, and refuses malformed cursors and page sizes.
- **`TestAnalysisUploadWriterTerminationValidation`** covers the row validation and the incarnation bounds.

**API** (default build, `pkg/api`). Each test that proves a fake exit installs B with no exits before minting A's generations, and proves A's exit only just before its explicit sweep. The server's own background sweep, which `NewServer` starts, therefore cannot act first. The upload-retry and real-process tests need no such ordering: until they install B, the background sweep judges with the real process identity, which cannot decide their recorded identities. B's filesystem probe answers "local" without looking (`assumeLocalFilesystem`), and the real-process test installs the real identity and exit judge with that same fixed probe, so only the locality tests (`NetworkFilesystemIsNotLocal`, `ArtifactDirectoryIsNeverCreatedByTheJudge`) depend on the probe's answer.
- **`TestAnalysisWriterOwnershipRestartRetiresExitedGenerationsOnFilesystem`.** A died mid-normalization, mid-deletion and mid-upload; C is still alive.
  - Before the sweep, DELETE is explicitly pending and no output is deleted.
  - A repeated DELETE leaves every cleanup job's schedule unchanged.
  - After the sweep, the interrupted import is `failed` with its reason over GET.
  - The exited attempt's cleanup removes only its own output.
  - Both deletions return 204 and remove the exited objects.
  - C's import and output are untouched, and C's DELETE after the sweep is pending (`unproven_writers: 1`).
- **`TestAnalysisWriterOwnershipRemoteBackendsRetainUnprovenCommits`**, for each of `s3`, `azure_blob` and `gcs`:
  - B's filesystem probe calls every root local, so only the backend kind keeps the scope non-local;
  - the interrupted import becomes `failed` for a retry (the default build cannot commit again; re-commit is covered on the filesystem by the storage retry test);
  - the attempt and dataset cleanup jobs run against a memblob bucket, kept open across cleanup owners, that holds objects which "landed late" at the exited generations' keys;
  - nothing is deleted;
  - DELETE stays pending with `remote_commits_unproven: 1`, `retryable: false` and no `Retry-After`.
- **`TestAnalysisWriterOwnershipNetworkFilesystemIsNotLocal`**:
  - a filesystem backend whose probe reports a network mount retires an exited attempt like an object store: the import fails, DELETE stays pending with `remote_commits_unproven: 1`, and the output is kept;
  - a probe that fails grants no scope at all: the sweep leaves the exited attempt `normalizing` and records no verdict, so a transient error cannot make a local filesystem permanently "remote";
  - the real probe on the test's temporary directory is local;
  - an unknown identity grants no scope on a valid backend.
- **`TestAnalysisWriterOwnershipRealProcessExitAndColdReplica`** uses the real judge:
  - a generation recorded for a real helper process, which lives until its stdin closes, is untouched while that process runs, and is retired once it is killed and reaped;
  - the dataset then deletes and its output is removed;
  - a generation recorded with this node's machine and boot but another PID namespace (a restarted container, another pod on the node) stays `normalizing`, and DELETE answers `unjudgeable_writers: 1` with no `Retry-After`.
- **`TestAnalysisWriterOwnershipUploadRetryResumesAfterExit`**:
  - an unproven writer keeps its offset;
  - a proven one lets the retry resume, through the real HTTP PUT in the native profile and through the same helper and Begin path in the default build;
  - only the replacement's key enters the cursor, the replacement writer records B as its incarnation (in both builds, so the native run checks the retried Begin's own binding), and the exited writer's temporary object is cleaned.
- **`TestAnalysisWriterOwnershipWarnsWhenItCannotJudge`**: a controller without a process identity lists the candidates and logs, rate-limited, that it cannot judge them.
- **`TestAnalysisWriterOwnershipArtifactDirectoryIsNeverCreatedByTheJudge`** (replaces round 5's `ArtifactRootIsProvable`), on a fresh filesystem backend with no `_analysis`:
  - A begins an offset-0 upload writer and dies before writing; the exit scope fails with `fs.ErrNotExist`, and the upload retry's exit proof resolves nothing and logs the missing-directory warning;
  - B's Begin at that offset gets `ErrAnalysisUploadInFlight`, and DELETE answers 503 `analysis_dataset_deletion_pending` with `unproven_writers: 1`, `retryable: true` and `Retry-After` (`requireDeletionPending`);
  - with DELETE's cleanup queued the sweep has a candidate: it warns that it cannot judge, the writer stays unproven and a second DELETE answers the same;
  - none of these created `_analysis`;
  - another dataset's write creates it (mode 0700, not checked on Windows); the next sweep closes A's writer, DELETE returns 204, and every probe received `<root>/_analysis`.
  - The server's background sweep may run the probe too, so the probe's record is locked; the test renames nothing.
- **`TestAnalysisWriterOwnershipCleanupNeverCreatesTheArtifactDirectory`**: on a fresh filesystem backend, a DELETE that returns 204 and the queued dataset cleanup job both treat the missing `_analysis` as nothing to delete, and neither creates it.
- **`TestAnalysisWriterOwnershipDefaultRuntimeIsTheRealProcess`**: the production runtime of a server without an override. It is known on macOS, Linux and Windows; its identity and host are this process's; its judge calls this process and a live child alive and the reaped child exited; and its probe answers a missing path with `fs.ErrNotExist`, not "local".
- **`TestAnalysisWriterOwnershipSweepWrapsOnce`**: a pass that begins mid-inventory, after another pass moved the shared cursor, wraps once and still reaches the normalizing owners.
- **`TestAnalysisWriterOwnershipRequiresThePinnedBackend`**: a substituted backend grants no scope. An unknown identity, even with a valid identity value and a judge that proves every exit, binds nothing and judges nothing.
- **Background-sweep timing** (`evidence/tick-delay.log`):
  - an overlay delaying the loop's first tick passes every ownership test at each of 0, 1, 2, 3, 4, 5, 7, 10, 15, 25 and 50 ms. Each delay is one test-binary run with `-test.count=5`, so the evidence shows one `PASS` line per delay. In round 3 this rerun first failed at 1 ms; that found the non-wrapping sweep, now fixed and covered by `SweepWrapsOnce`;
  - an overlay running the writer sweep on every tick, every millisecond, passes all eleven ownership tests five times each, including round 7's `CleanupNeverCreatesTheArtifactDirectory`, round 6's `ArtifactDirectoryIsNeverCreatedByTheJudge` and round 5's `DefaultRuntimeIsTheRealProcess` (per-test counts in the evidence; the twelfth entry is the helper process's skip). Regenerated on the round-7 code.

**Native** (`analytics_duckdb`, 512 MiB / 128 MiB). `TestAnalysisWriterOwnershipNativePipelineRecordsItsIncarnation` runs the real HTTP upload, mapping, commit and DuckDB normalization:
- both the upload writer and the attempt record this process's identity;
- a sweep and an all-exited judgement do not touch the published dataset;
- DELETE returns 204.

**procidentity and localfs** (`evidence/procidentity-linux.log`):
- **`TestGoneDecidesOnlyProvableExits`**, a table that includes:
  - a reused PID, another namespace and a missing lookup;
  - an unknown machine's earlier boot;
  - the same boot on another or an unidentified machine;
  - macOS's machine-less same boot;
  - `Judgeable`, which mirrors `Gone`'s scoping: the same machine, boot and namespace, or an earlier boot of the same identified machine; not another PID namespace, another machine or an unknown machine's earlier boot.
- **`TestExitedJudgesLiveAndReapedProcesses`**, run on macOS and on Linux (a static test binary in the local Alpine image, as root and as `nobody`). It, and the API real-process and native-pipeline tests, fail rather than skip when no identity is available on macOS, Linux or Windows.
- **`TestDarwinHostIdentity`**: macOS `readHost` succeeds with namespace `darwin` and no machine identity. Its boot equals `kern.bootsessionuuid` and differs from `kern.uuid` (the kernel build UUID every Mac on one build shares), and launchd's start time is read.
- **Linux unit tests with an injected kernel** (they run in the ordinary Linux CI suite):
  - `TestProcessStartNeedsESRCHForAMissingEntry` (`EPERM`, nil and `ESRCH`);
  - `TestReadHostRefusesAForeignProcAndScopesTimeNamespaces`, including an unreadable time namespace, which is an error rather than "none";
  - `TestReadHostPairsMachineIDWithHostName`, with an injected host name: `machine-1@host-a`; no host name gives no machine identity; the `/var/lib/dbus/machine-id` fallback; `/etc/machine-id` taking precedence; an empty `boot_id` is an error;
  - malformed `/proc/<pid>/stat` lines (too few fields, no command bracket, a non-numeric start time) are errors.
- **`TestProcessStartRefusesHiddenProcesses`**, run on Linux with `/proc` remounted `hidepid=2` (a privileged local container, test as `nobody`).
- **localfs:**
  - `TestIsLocalOnTheTemporaryDirectory`, on macOS and Linux.
  - **Shared TMPDIR.** Only the tests about locality probe the real TMPDIR, and they fail, not skip, when it is on a shared or user-space filesystem (a rootless FUSE overlay, a network home, a self-hosted runner such as `apexload-ci-unprivileged` with such a TMPDIR): `TestIsLocalOnTheTemporaryDirectory` and the API tests `NetworkFilesystemIsNotLocal` (its "temporary directory is local" assertion) and `ArtifactDirectoryIsNeverCreatedByTheJudge`. Every other ownership test uses a fixed "local" probe. `evidence/shared-tmpdir-linux.log` shows exactly these three failing, and everything else passing, with TMPDIR on virtiofs in the Linux container, and all passing on a local `/tmp`. The runner provisioning list in `docs/GITHUB_ACTIONS_RELEASE_AND_DEPLOYMENT.md` now requires a local TMPDIR and the job's own `/proc`.
  - `TestWindowsVolumeLocalIsFixedNTFSOrReFS`, portable: a fixed NTFS or ReFS drive only; a WinFsp mount, exFAT, FAT32, and removable, network or RAM drives are not local.
  - The Linux magic table also feeds 32-bit `f_type` values to `magicOf`, and the package tests ran as a 32-bit ARM (`GOARCH=arm`) binary in the container.
  - `TestIsLocalRefusesASharedFilesystem`, on a Docker Desktop bind mount (virtiofs) and on a symbolic link to it from a local directory, in the Linux container. It skips unless `LOCALFS_EXPECT_SHARED` is set.
  - Allowlist tables that run in ordinary CI: `TestLocalMagicTypesAreAnAllowlist` on Linux (NFS, SMB, SMB2, CIFS, FUSE, 9p, Ceph, virtiofs and unknown types are not local), `TestDarwinLocalNeedsALocalAllowlistedMount` on macOS (NFS, SMB, AFP, WebDAV, FUSE types and unflagged mounts are not local), and the portable `TestIsUNCRecognizesNetworkShares`.
- **CI lanes.**
  - The Windows HAR step of `.github/workflows/desktop.yml` runs `pkg/internal/procidentity` (including `TestWindowsHostIdentity` and `TestWindowsProcessStart`) and `pkg/internal/localfs`.
  - The desktop native-analytics step runs both packages on every desktop leg: Linux amd64, Linux arm64, macOS and Windows.
  - Both lines are pinned by `scripts/client_package_workflow_test.go`; removing either is caught. For the native step, the check also requires the desktop job's `runs-on: ${{ matrix.os }}`, exactly the four matrix legs from `include` alone, no `if:` or `continue-on-error` on the job or step, one such step, a script that starts with `set -euo pipefail`, and before the identity command only comments and plain `go test` lines (no shell operator, redirection, substitution, double quote or backslash outside single quotes, and no unterminated quote). Its self-test inserts each bypass and requires the refusal to name it.
  - The same native step runs the `TestAnalysisWriterOwnership*` API tests on Windows, Linux and ARM64 too, where the runner can compile `pkg/api`. None of the Windows runs has been executed yet: locally the Windows code only compiles.
- The browser HAR identity and reclamation tests pass.

**Mutations** (`go test -overlay`, `evidence/mutations.txt`). After round 7 there are 117 recorded mutants: 59 from round 1, 14 from round 2, 11 from round 3, 8 from round 4, 8 from round 5, 8 from round 6 and 9 from round 7. Round 5's ten marker mutants (X1 to X10) were removed with the marker in round 6. Four are obsolete, because the code they mutated no longer exists: F2 (survived), V1 and V2 (round 4's directory creation in the scope, removed in round 5), and K2 (round 5's command check, replaced in round 6 and re-targeted as Y7). Of the 113 applicable mutants, 110 are caught and three survivors are equivalent. R1 and T9, first caught by the removed `ProbesTheArtifactDirectory`, were re-run on the round-7 code and are caught by `ArtifactDirectoryIsNeverCreatedByTheJudge` (and T9 also by `NetworkFilesystemIsNotLocal`); Y8 was re-targeted at the round-7 operator set and is still caught.

| Guard mutated | Caught |
| --- | --- |
| Judge self, judge without exit proof, remote treated as quiesced (attempt) or closed (writer) | Yes |
| Exited owner keeps its import, failure without a revision advance, owner and revision both ignored in the owned check (G12) | Yes |
| Quiesced owner failed without a termination record, or recorded as remote-unproven (M3) | Yes |
| Cleanup not queued for an exited attempt or upload writer (M5) | Yes |
| Exited writer still blocks its offset, filesystem proof without closure, self-proof accepted, unbounded identity | Yes |
| Claim or Begin records no incarnation (storage); commit or upload binds none (native API) | Yes |
| Inventory never reaches the cleanup queue, snapshot never escalates, settled owner never escalates | Yes |
| Every pending reason miscounted (remote attempt and upload writer, unrecorded, unjudgeable, unplanned output), readiness ignoring unrecorded or unjudgeable writers, `Resolvable` dropping unrecorded or adding legacy | Yes |
| Remote backend probed as a filesystem, probe error ignored, every filesystem local, backend pin not required, unknown identity granting a scope or binding | Yes |
| Sweep skipping upload writers, generic 503, offset never resolved, retry after exit removed, `retryable` always, `Retry-After` always, every writer judged as this host's, repeated DELETE re-queuing cleanup | Yes |
| `procidentity`: earlier boot of an unknown machine, another PID namespace, reused PID, same boot on another machine (H1); P7 (recorded in round 1 against `SameHost`, since replaced) is the same-boot machine check that `Judgeable` now holds | Yes |
| Linux-only, in the container: `ESRCH` rule (P4), time namespace (P6), foreign `/proc` (P8), filesystem allowlist (F1L) | Yes |
| S8: an abandoned writer that is not closed no longer blocks Begin | Equivalent. Such a writer arises only when the import is cancelled or deleted, its project is deleted, or a normalizing import is claimed again (each through `analysisUploadQueueImportTx`, or with the import already past `uploading`). Begin is then refused by the import's state, or by the missing project, before the blocking check. |
| G1, G2: the owned check ignores the owner, or the revision | Equivalent alone: each of the two guards alone keeps a replaced owner from failing its successor's import. Removing both (G12) is caught by the lease-expiry test. |
| F2: `IsLocal` skips its existence check | Obsolete (not counted) after round 2: `IsLocal` now opens the directory on every platform, so a missing path fails the open itself. The Windows behaviour is unverified until the Windows lane runs `TestIsLocalOnTheTemporaryDirectory`. |
| Round 2: probing the archive root instead of `_analysis` (R1); a silent no-identity sweep (R2); `Judgeable` ignoring the PID namespace, at the unit, storage and API levels (R3, R3s, R3a; R3s re-detects the unit mutant, because the storage test's host judge calls `procidentity.Judgeable`); `Judgeable` refusing an earlier boot (R4); a boot-only API judge (R5); the retried Begin binding no incarnation (R6, native); the darwin rule ignoring `MNT_LOCAL` (R8); plain and extended UNC paths not recognized (R9, R9b) | Yes |
| Round 3: darwin `readHost` always failing, at the unit and API levels (T6, T6a); darwin recording a machine identity (T7); the Windows volume policy ignoring the filesystem name (T8); a probe error recorded as a remote verdict (T9); a pass that does not wrap (W1) | Yes |
| Round 3, Linux-only in the container: the `@hostname` suffix dropped (T1), the empty-host-name guard dropped (T2), the dbus machine-id fallback dropped (T3), the empty-`boot_id` guard dropped (T4), `f_type` sign-extended on 32-bit (T5) | Yes |
| Round 4 (harness `evidence/mutate-r4.py`): a missing `_analysis` not created before probing, or created with a mode other than the writer's 0700 (V1, V2; renamed from U1 and U2, which round 1 already used); the darwin boot read from `kern.uuid` (V3); a Windows identity accepted without `MachineGuid` or without a host name (V4, V5); the workflow check ignoring a step's `if:` or `continue-on-error` (V6, V7); `magicOf` without `~uint32`, caught by the linux/s390x cross-compile (V8). V1 and V2 tested round 4's directory creation in the scope, which round 5 removed; they are obsolete and not counted, and Y1 checks the inverse on the round-6 code. | Yes (V1, V2 obsolete) |
| Round 5 (harness `evidence/mutate-r5.py`): the default runtime's probe calling everything local (D1), its judge calling everything exited (D2), or having no identity (D3); `composeWindowsHost` accepting a value read with an error (C1, C2); the workflow check skipping the matrix legs (K1), accepting a weakened command (K2, on the round-5 check) or skipping the job keys (K3). D1 to D3, K1 and K3 were re-run on the round-6 code and are still caught; K2 is obsolete and not counted. | Yes (K2 obsolete) |
| Round 6 (harness `evidence/mutate-r6.py`): the judge creating a missing `_analysis` (Y1); a failed probe granting a remote scope (Y2); the workflow check accepting a duplicated step, `found == 0` for `found != 1` (Y3, the review's K6), a matrix `exclude:` (Y4), any line before the command (Y5), a script without strict mode (Y6) or a commented-out command (Y7, K2 on the round-6 check), and treating shell operators as a plain `go test` (Y8) | Yes |
| Round 7 (harness `evidence/mutate-r7.py`): the sweep creating `_analysis` once it has candidates (Q1, the review's CS-L1 mutant); cleanup's root open creating `_analysis` first (Q2, the review's Z5); no missing-directory warning (Q3); the workflow check accepting a backslash continuation (Q4), ignoring `runs-on` (Q5), accepting an unterminated quote (Q6), accepting the command before strict mode (Q7), or accepting `;` (Q8) or `&` (Q9) in a `go test` line | Yes |
| Round 2, Linux-only in the container: NFS added to the allowlist (R7); an unreadable time namespace ignored (R10); the stat field-count bound lowered (R11, caught by an index panic in the test) | Yes |

**Backends.**
- **Local filesystem:** exercised for real, as are the local-filesystem probe and a virtiofs mount.
- **S3, Azure Blob and GCS:** exercised through the repository's existing fakes: memblob behind `analysisCleanupPhysicalOps`, with their configuration validated.
- No real S3, Azurite or GCS emulator was run (see Remaining).

**Gates (after round 7).** CI runs the full suites on push, so round 7 did not repeat them all locally. Regenerated on the round-7 code: `gates-static.log`, `storage-sqlite.log`, `storage-postgres-full.log`, `race.log`, `api-native-analysis.log` and `tick-delay.log`, plus a final build, gofmt, the `TestAnalysisWriterOwnership*` tests and `scripts`. Still from round 6: the `pkg/api` shards, `browser.log`, `procidentity-linux.log` and `shared-tmpdir-linux.log` (round 7 changed neither `procidentity` nor `localfs`); `mutations-linux.txt` is from round 4.
- **Static** (`evidence/gates-static.log`):
  - `gofmt`, `go build ./...` and builds of `pkg/...` and `cmd/...` for linux, windows and darwin are clean;
  - `pkg/internal/localfs` and `pkg/internal/procidentity` also build for linux 386, arm, amd64, arm64, s390x, ppc64le and riscv64 and for freebsd. On 32-bit Linux the existing `pkg/internal/diskspace` and `pkg/protocol/jms` do not build; they are unchanged from the base;
  - `golangci-lint run` reports 0 issues on `pkg/api`, `pkg/storage`, `pkg/internal/...`, `pkg/protocol/browser` and `scripts`, and again for linux, windows and darwin on `pkg/api`, `procidentity` and `localfs`. With the native tag it reports one `rowserrcheck` in `pkg/api/analysis_acceptance_populations_native_test.go`, a file unchanged from the base, and none in changed files;
  - `go generate ./...` shows no drift;
  - `scripts/check-docs.py` and `scripts/ci/validate-github-actions.py` pass.
- **Storage:**
  - full `pkg/storage`, all of `pkg/internal/...` and `scripts` on SQLite passed (`evidence/storage-sqlite.log`);
  - full `pkg/storage` on PostgreSQL 16 with `-tags postgres_integration` passed, running 109 PostgreSQL tests with none skipped (282 s, `evidence/storage-postgres-full.log`).
- **`pkg/api`:**
  - full untagged `pkg/api` on the round-6 code, run in nine test-name shards plus its fuzz seed and example (`evidence/api-shard-*.log`), passed except the six `TestReplayArtifact*` tests in shard R, which already fail on macOS;
  - the native analytics selection with `-tags analytics_duckdb` and 512 MiB / 128 MiB passed (153 s, `evidence/api-native-analysis.log`).
- **`-race -count=2 -shuffle=on`** passed with no race reports (`evidence/race.log`) on:
  - the new storage tests, `procidentity` and `localfs`, and the `scripts` workflow-step check;
  - the PostgreSQL ownership suite;
  - the new and adjacent API upload, deletion and cleanup tests (`TestAnalysisWriterOwnership`, `TestAnalysisDurableUpload`, `TestAnalysisDatasetDeletion`, `TestAnalysisUploadCursor`, `TestAnalysisCleanup`), in the default and the native build.
- **Other:** full `pkg/internal/...` and `scripts` passed with storage, and full `pkg/protocol/browser` passed on the round-6 code (`evidence/browser.log`). The Linux container runs of `procidentity` (as root, as `nobody` and under `hidepid=2`), of `localfs` (on virtiofs) and of both packages as 32-bit ARM binaries are in `evidence/procidentity-linux.log`. The shared-TMPDIR proof on the round-6 code is in `evidence/shared-tmpdir-linux.log`: the Linux `pkg/api` writer-ownership tests and `localfs`, run with TMPDIR on a writable virtiofs bind mount and then on the container's local `/tmp`.
- **Concurrent writer during round 4.** For part of round 4 an earlier instance of this work was still running gates in the same worktree. Its logs and mutation harness were removed or replaced; rounds 5 and 6 regenerated every log with a single writer.
- The embedded dashboard bundle, which carries the Help copies of the edited guides, was not rebuilt.

## Review

### Review round 1

| Finding | Handling |
| --- | --- |
| **H1** (High). A Windows replica could judge a live writer on another Windows host as exited: the boot counter is shared, and `Gone` compared the machine only for an earlier boot. | `Gone` now requires the same machine identity before a same-boot PID lookup too, and `SameHost` adds the same rule. Windows refuses an identity without `MachineGuid` and a host name. New table cases cover the same boot on another or an unidentified machine (mutants P5 and P7 are caught). The Windows boot value is unchanged: changing its format would make every identity recorded by an earlier binary read as an earlier boot of the same machine, which judges it exited. The machine match closes the shared-counter case and the same-boot clone case. |
| **H2** (High). The API tests raced the server's own background sweep. | Each test now installs B with no exits before minting generations, and proves A's exit only just before its explicit sweep; the pinned-backend test changes the backend before proving. Verified with a first-tick delay overlay at 0 to 50 ms and an every-millisecond sweep overlay (`evidence/tick-delay.log`). |
| **M1.** A network or FUSE "filesystem" backend was treated as local. | New `pkg/internal/localfs` allowlist probe, injected through the runtime. A probe that fails or reports a non-local root yields `remote_commit_unproven`. Tested with an injected probe, the real probe and a real virtiofs mount (mutants A1, A1b, A1c, F1L). |
| **M2.** The moved Windows tests no longer ran in CI. | The Windows step of `desktop.yml` now runs `pkg/internal/procidentity` and `pkg/internal/localfs`; the workflow validator and the `scripts` tests pass. |
| **M3.** The record claimed a proof for every failed import, but an already-quiesced owner was failed without one. | Failing an owned import now always writes a termination record, without `remote_commit_unproven` when the attempt was already quiesced. The effects table has a row for it. Fixing the mutant also unmasked the attempt outcome's `RemoteCommitUnproven` (S21, S22). |
| **M4.** The most common restart path was untested. | New lease-expiry test: A's lease expires, C claims again, then A's exit is proven. A is proven while C's owner and revision are unchanged. G12 is caught; G1 and G2 alone are equivalent (see Mutations). |
| **M5.** Cleanup queuing was not asserted. | The exited attempt's and the exited upload writer's jobs are checked in the real queue table, absent before and present after (S25, S26). The API test compares `ListAnalysisCleanupJobs` across a repeated DELETE. |
| **M6.** Pending reasons and the retryable rule were untested. | New cases: a remote upload writer, an unplanned output, a writer on another host, and `Resolvable`/`ResolvesHere` for each reason (U1, D1, D2, D3). |
| **M7.** The Linux `ESRCH` rule and `/proc/self` check had no CI coverage. | The stat reader, link reader and kill probe are injectable. Unit tests in the ordinary Linux suite cover a hidden entry (`EPERM`), `ESRCH`, a visible entry and a foreign `/proc` (P4, P8). |
| **M8.** `retryable: true` with `Retry-After` could be permanent. | New `unjudgeable_writers` reason for writers recorded on another host, boot or PID namespace. `retryable` now means "may still resolve", and `Retry-After` is set only while this host will settle an `unproven_writers` writer. A repeated DELETE of a cancelled or deleting import no longer saves it again or re-queues cleanup. The guide, spec and record state that HA takeover is covered only for a replica in the same PID namespace (A8, A9, A10). |
| **L1.** Linux time namespaces shift start times. | The time namespace joins `Namespace` when present (P6). CRIU-restored controllers are documented as unsupported. |
| **L2.** The sweep was silent when it could not act. | Inventory failures, resolution errors and scope refusals (missing identity, unpinned backend) log a Warn, at most every 10 minutes per server. |
| **L3.** The A6 "equivalent" survivor was killable. | The pinned-backend and network-filesystem tests use a runtime with a valid identity, `known: false` and an always-true judge; A6 and A6b are caught. |
| **L4.** Record and test corrections. | S8's reason now lists every path that abandons an unclosed writer. The mutation count matches its evidence, and the Linux container runs and P4 have logs. "The import is retried" is reworded. C's pending DELETE is described as after the sweep. The remote test's memblob is wrapped with a no-op `Close`. The guide restores the native worker's restart wording. Worker-failure injection and failure during cleanup are listed under Remaining. |

### Review round 2

| Finding | Handling |
| --- | --- |
| **M-1.** The locality probe checked the archive root, while artifacts are written one level down in `_analysis`, through links and mount points. | The scope probes `<root>/_analysis` through the new `analysisArtifactDirectory` helper. That helper now also builds the path for every analysis write, read, size read and cleanup, so the probe cannot drift from them. `IsLocal` opens the directory and judges the handle: `fstatfs` on Linux and macOS, `GetFinalPathNameByHandle` on Windows, so a link or junction to a share is judged by its target. A missing directory is not local. Tests: the API probe-path test, and a symbolic link into virtiofs in the Linux container (R1). Recording locality at Claim/Begin is listed under Remaining. |
| **M-2.** The rate-limited warning never fired for a controller with no identity. | The sweep no longer returns early without an identity: it lists candidates and, when there are any, logs that it cannot judge them. The cursor keeps the last warning for the new test (R2). |
| **M-3.** Nothing tested that `SameHost` compares the PID namespace. | Replaced by `Judgeable` (see L-1), with a table case for another PID namespace. New tests: a storage deletion-pending case with host A's machine and boot in another namespace (`UnjudgeableWriters: 1`), and the API cold-replica identity, which is now this node's machine and boot in another namespace (R3, R3s, R3a). |
| **L-1.** `ResolvesHere` and `unjudgeable_writers` disagreed with `Gone` about an earlier boot. | New `procidentity.Judgeable`, which mirrors `Gone`'s scoping (an earlier boot of the same identified machine is judgeable), used by the API's here-function (R4, R5). |
| **L-2.** Docs. | The guide states the distinct machine-id/host-name requirement and that Helm and compose deployments get no restart recovery. Its misattributed colon and its cross-reference are fixed, and `unrecorded_writers` there and in the storage comment now include a controller with no identity. The HAR guide's Windows section requires `MachineGuid` and a host name, and its proof rule states the same-boot machine match and the Linux time namespace. The native-upload record says "same host and PID namespace". The intro of this record states the Helm and compose scope. |
| **L-3.** HAR records from the earlier binary on Linux cannot be reclaimed until reboot. | Documented in the HAR guide as a fail-safe limitation: those generations stay until the machine reboots or they are removed by hand. A pid-only identity is not accepted: the judge cannot know which time namespace the recording process ran in, so start times could be compared across an offset. |
| **L-4.** The retried upload's incarnation binding was unasserted. | Both branches now read the replacement writer's journal and require `Incarnation == B`; the native run checks the retried Begin itself (R6). |
| **L-5.** The local-filesystem allowlist had no negative test in CI. | Table tests for the Linux magic map and the darwin flag/type rule, and a portable UNC classifier test (R7, R8, R9, R9b). |
| **L-6.** The new Windows workflow line was not pinned. | `scripts/client_package_workflow_test.go` pins both the Windows HAR line and the new native-step line, which runs the two packages on every desktop leg, macOS included. Removing either line was checked to fail the test. |
| **L-7.** Record accuracy. | The API ordering note, the probe-failure claim, the tick-delay description, F2 (now obsolete, Windows unverified), the CI lanes that run the ownership tests and have never run on Windows, and the shared-TMPDIR failure mode are all corrected above. |
| **L-8.** Two Linux guards were untested. | Injected-kernel tests for an unreadable time namespace and for malformed stat lines (R10, R11). |

### Review round 3

| Finding | Handling |
| --- | --- |
| **CS-M1.** On Windows a user-space filesystem (WinFsp, as rclone mounts by default) passed as local. | After the final path, the filesystem name is read from the same handle (`GetVolumeInformationByHandle`). The new portable `windowsVolumeLocal` admits only a fixed NTFS or ReFS drive, the policy of `winfs.ValidateVolume(…, "NTFS", "REFS")`. Table test `TestWindowsVolumeLocalIsFixedNTFSOrReFS` (T8). |
| **TD-M1.** The Linux machine-id and host-name pairing had no test. | `os.Hostname` is injectable. `TestReadHostPairsMachineIDWithHostName` asserts `machine-1@host-a`, no machine identity without a host name, the dbus fallback and its precedence, and that an empty `boot_id` is an error (T1 to T4, in the container). |
| **TD-M2.** The macOS identity path was tested only through tests that skip. | New `TestDarwinHostIdentity` (namespace `darwin`, a non-empty boot of at least 32 characters, no machine identity; round 4 pins the boot to `kern.bootsessionuuid`). `TestExitedJudgesLiveAndReapedProcesses`, the API real-process test and the native-pipeline test fail rather than skip without an identity on macOS, Linux and Windows (T6, T6a, T7). |
| **CS-L1.** A failed probe became a permanent remote verdict. | A probe error (including a missing `_analysis` before its first write) now grants no scope for that sweep, and records nothing. Only a definite not-local answer records `remote_commit_unproven` (T9). |
| **CS-L2.** A mount below `_analysis` is not seen. | Narrowed instead: the package comment, the record and the guide judge only the `_analysis` directory itself, and the guide forbids mounting another filesystem below it. |
| **CS-L3.** `unproven_writers` with `Retry-After` can promise a judgement that never comes. | Documented: `unproven_writers` and `Retry-After` now say the writer "may settle". Under `hidepid`, a dead writer's PID reused by another user's process stays undecidable. Recording the uid is listed under Remaining. |
| **CS-L4, TD-L1.** Docs. | The guide has a machine identity requirement for every host sharing the SQL store, naming the baked machine-id with a fixed host name case (compose `hostname:`, pod `spec.hostname`, StatefulSets), memory-snapshot clones and macOS clones. It copies the local-filesystem allowlist and the remote treatment of everything else (vfat, exFAT, ntfs3, MS-DOS, removable or RAM drives, a missing directory, a failed probe). Every place that said only "the same machine, boot and PID namespace" can judge now includes an earlier boot of the same identified machine: record, guide, spec and the storage comment. The spec says `<root>/_analysis`. The replaced-pod condition was stated, but inverted in the guide; round 4 corrects it (M1). |
| **CS-L5.** The sweep is not "within about a minute" at scale. | Reworded: about a minute per 400 outstanding generations per replica, with permanently blocked and not-yet-due jobs staying in the inventory. The tick-delay rerun separately found that a pass did not wrap, so a pass could end without reaching the normalizing owners. It now wraps once (W1, `SweepWrapsOnce`). |
| **TD-L2.** Mutation table labels. | P7 is relabelled as `Judgeable`'s same-boot machine check, and R3s is noted as re-detecting the unit mutant. |
| **TD-L3.** The every-millisecond evidence predated two tests. | Regenerated with per-test counts for the nine ownership tests (the tenth entry is the helper process's skip). The delay series was widened to 0–50 ms at `-test.count=5`. |
| **TD-L4.** The native step header was not pinned. | `scripts/client_package_workflow_test.go` pins the whole matrix step, from `- name: Native analytics contracts and controller build` with `shell: bash` and `env:` and no `if:`, through the identity and filesystem tests. Inserting an `if:` was checked to fail the test. |
| **TD-L5.** The shared-TMPDIR note was incomplete. | Every test that relies on the real probe is now listed, with the runner cases. |
| **TD-L6.** 32-bit Linux misread filesystem types. | `magicOf` reads `f_type` as unsigned 32-bit. A table case feeds the 32-bit values, and the package ran as a 32-bit ARM binary in the container (T5). |

### Review round 4

Round 4 found two Medium findings and eight Low ones; both reviewers' remaining findings were Low.

| Finding | Handling |
| --- | --- |
| **M1.** The guide inverted the replaced-pod condition ("judged only when its host name or machine ID differs"). | The guide now says that a replacement pod on another node can judge its predecessor only when its host name and machine ID both match (a baked machine-id with a fixed host name), which the machine identity requirement forbids for distinct hosts, and that a replacement on the same node never can, because it runs in a new PID namespace. The round 3 CS-L4 entry records the inversion and its correction. |
| **M2.** Five API tests depended on TMPDIR being a local filesystem, and only this record said so. | `actAsWriterB` uses a fixed "local" probe (`assumeLocalFilesystem`), and the real-process test installs the real identity and exit judge with that same probe. The real probe stays only in `TestIsLocalOnTheTemporaryDirectory` and the API locality tests `NetworkFilesystemIsNotLocal` and `ProbesTheArtifactDirectory`. The runner provisioning list in `docs/GITHUB_ACTIONS_RELEASE_AND_DEPLOYMENT.md` now requires a TMPDIR on a local allow-listed filesystem and a `/proc` of the job's own PID namespace, with `/proc/self/ns/time` readable. On the final code, with TMPDIR on virtiofs in the Linux container, exactly those three tests fail and every other one passes; on a local `/tmp` all pass (`evidence/shared-tmpdir-linux.log`). `ProbesTheArtifactDirectory` was renamed `ArtifactRootIsProvable` in round 5 and replaced by `ArtifactDirectoryIsNeverCreatedByTheJudge` in round 6. |
| **L1.** A fresh filesystem backend could stay stuck forever when the first writer died before its `MkdirAll`. | A probe that finds the artifact directory missing creates it as the writer would (mode 0700) and probes again. `ProbesTheArtifactDirectory` now begins with A's pending writer and no `_analysis`: the retried offset resolves, the directory exists with mode 0700, the writer is closed and the upload resumes (V1, V2). Superseded in round 5 (M-A): the scope no longer creates a directory. Round 6 dropped round 5's marker too; the stuck first writer is a documented gap with an operator workaround (Remaining). |
| **L2.** The build broke on linux/s390x. | `magicOf` accepts `~uint32` as well; the two packages cross-compile for s390x, ppc64le and riscv64 too (V8). |
| **L3.** `SweepWrapsOnce` was in the local-TMPDIR list. | Removed: it passes on virtiofs. The list now names only the three locality tests. |
| **L4.** `TestDarwinHostIdentity` did not pin the boot source. | The test requires the boot to equal `kern.bootsessionuuid` and to differ from `kern.uuid`; the record no longer calls the length check "a boot session UUID" (V3). |
| **L5.** The workflow pin did not stop a trailing `if:` or `continue-on-error:`. | `TestDesktopNativeAnalyticsStepRunsUnconditionallyOnEveryLeg` parses the workflow YAML and refuses either key on the desktop job or the native step. `TestDesktopNativeAnalyticsStepCheckCatchesConditions` inserts each key after the step's name and after its last `run` line, and requires all four insertions to be caught (V6, V7). |
| **L6.** The Windows `MachineGuid` and host-name refusal had no negative test. | `readHost` on Windows now calls a portable `composeWindowsHost`, whose table test refuses a missing or empty `MachineGuid`, a missing or empty host name, and an empty boot (V4, V5). |
| **L7.** A storage contract comment omitted the same-machine condition. | The comment now reads: the same identified machine booted again, or the same boot, machine identity and PID namespace. |
| **L8.** TD-L3 claimed "all ten ownership tests". | Corrected to the nine ownership tests; the tenth entry is the helper process's skip. |

### Review round 5

Round 5 found three Medium findings and five Low ones.

| Finding | Handling |
| --- | --- |
| **M-A.** Creating a missing `_analysis` turned an unmounted share into a "local" verdict, and cleanup reported a missing root as nothing to delete. | Replaced round 4's directory creation in the scope with a provable root: a create-only marker whose random ID is bound in SQL (compare-and-set via insert-if-absent, no schema change). Writers bind it at import creation, before `Claim` and before `Begin`. The judge only verifies; cleanup jobs and DELETE require it and answer 503 `analysis_artifact_root_unproven` otherwise. Legacy roots are adopted by a writer or cleanup, never by the judge, and an empty unbound directory only by a writer. Binding at mint time makes the fresh-backend stuck writer impossible, so the sweep never creates a root; this deviates deliberately from the suggestion that it may, for the legacy-unmounted reason in the contract. Tests `ArtifactRootIsProvable`, `LegacyArtifactRootNeedsAdoption` and, in the native build, `ImportCreationBindsTheArtifactRoot`; two existing cleanup tests now bind the root in their fixtures, as import creation does. The storage suite's `artifact root binding is compare-and-set` races two stores binding different IDs (both see one winner, later binds never replace it) on SQLite and PostgreSQL. The Remaining overclaim about remounts is corrected (X1 to X10). **Superseded in round 6:** the user dropped the marker; the scope still never creates a directory. |
| **M-B.** The mode check failed on Windows. | The directory check runs everywhere; the 0700 mode only where `runtime.GOOS != "windows"`. |
| **M-C.** The default production runtime had no test. | `DefaultRuntimeIsTheRealProcess` checks the runtime a server without an override uses: its identity and host, the real judge against this process and a live then reaped child, and the probe on a missing path (D1 to D3). |
| **L-1.** The workflow check had gaps. | It now also refuses a missing or changed matrix leg (`strategy.matrix.include[].os`) and requires the exact command line. The self-test inserts `if:` and `continue-on-error` on the job, after the step's name and after its last key before the next step. It also drops a leg, comments the command out and appends `\|\| true`. Every refusal must name what it caught (K1 to K3). |
| **L-2.** `composeWindowsHost` error-only paths. | Cases for a `MachineGuid` and a host name read together with an error (C1, C2). |
| **L-3.** `tick-delay.log` predated round 4. | Regenerated on the round-5 code. |
| **L-4.** Duplicate mutant ID `U1`. | Round 4's set is renamed V1 to V8 here, in `evidence/mutations.txt` and in `evidence/mutate-r4.py`. |
| **L-5.** Provisioning doc and record wording. | The runner requirements name `RealProcessExitAndColdReplica` and `DefaultRuntimeIsTheRealProcess` as needing the job's own process identity, list `/proc/sys/kernel/random/boot_id`, and say only the locality tests depend on the probe's answer; the record says the same. |

### Review round 6

Round 6 found one High and three Mediums, all from round 5's marker, and five Lows.

| Finding | Handling |
| --- | --- |
| **High.** A filesystem without hard links (exFAT, FAT, gcsfuse, s3fs, blobfuse2, rclone, Azure Files over SMB) refused every import: the create-only marker needs `link`. | **User decision: drop the marker.** `analysis_artifact_root.go`, the storage methods `AnalysisArtifactRoot` and `BindAnalysisArtifactRoot`, the `analysis_artifact_root_v1` setting, the root checks at import creation, commit/`Claim`, upload `Begin`, cleanup and DELETE, and the `analysis_artifact_root_unproven` 503 are removed, with their tests (`ArtifactRootIsProvable`, `LegacyArtifactRootNeedsAdoption`, `ImportCreationBindsTheArtifactRoot`, the storage compare-and-set subtest, the cleanup fixtures' bindings) and mutants (X1 to X10). The regenerated store overlay has no root methods. The rule is the one from before round 4's L1, without the sweep's directory creation (Contract, invariant 10). |
| **Medium.** Legacy roots were adopted through monitor writes. | Gone with the marker. |
| **Medium.** A wrong binding (an unmounted share at upgrade, a moved volume) had no recovery path. | Gone with the marker; the underlying unmounted-share gap is documented as pre-existing, with the operator rule (Remaining). |
| **Medium.** Several marker guards were untested. | Gone with the marker. The remaining rule is tested by `ArtifactDirectoryIsNeverCreatedByTheJudge` (Y1, Y2). |
| **Low.** A stale test name in `analysis_writer_ownership_test.go`'s probe comment. | Now names `ArtifactDirectoryIsNeverCreatedByTheJudge`. |
| **Low.** The workflow check's step count could be weakened to `found == 0` (K6). | The self-test duplicates the step and requires "2 native analytics steps" (Y3). |
| **Low.** An `exit 0` or `set +e` before the command line was not caught. | The script must start with `set -euo pipefail`, and before the identity command it may hold only comments and plain `go test` lines (no shell operator, substitution or `exit` outside single quotes). Self-test cases for `exit 0`, `set +e` and dropped strict mode (Y5, Y6, Y8). |
| **Low.** A matrix `exclude:` was not caught. | The matrix may hold only `include`; a self-test case adds an `exclude` (Y4). |
| **Low.** A possible Windows flake: a test renamed `_analysis` while the server's background loops ran. | No remaining test renames `_analysis`. The new test only creates it through a write, and locks its probe record against the background sweep. |

### Review round 7

Round 7 found no High or Medium findings, only Lows. All were fixed, and under the user's rule (a round with only Lows is fixed without another review) the review loop ends here.

| Finding | Handling |
| --- | --- |
| **CS-L1.** The sweep was never run while it had a candidate and `_analysis` was missing, so a sweep that created the directory survived. | After DELETE queues the stuck dataset's cleanup, `ArtifactDirectoryIsNeverCreatedByTheJudge` runs the sweep and requires its "cannot be judged" warning, the writer still unproven (a second DELETE answers the same) and `_analysis` still absent (Q1). |
| **CS-L2.** The stuck-first-upload docs named the wrong answer and overstated the gap. | `docs/NATIVE_ANALYTICS.md` and Remaining now name the 409 "upload cursor was not committed; reload durable cursor", say that while the import is `uploading` only a retry of that offset judges the writer and the sweep reaches it only after DELETE queues its cleanup, and that any artifact write, including a monitor artifact (`writeMonitorArtifact`), creates `_analysis`. |
| **CS-L3.** No operator signal. | `resolveExitedAnalysisUploadOffset` logs a warning, rate-limited on its own every 10 minutes, naming the directory and the workaround when the scope fails with `fs.ErrNotExist` (Q3). DELETE's answer is documented; the optional `Retry-After` change was not made. |
| **TD-L1.** Workflow check bypasses. | `plainGoTest` refuses a backslash outside single quotes, so `-args \` cannot join the identity command onto a previous line, and the desktop job's `runs-on` must be `${{ matrix.os }}`. Self-test cases for both (Q4, Q5). |
| **TD-L2.** Untested workflow guards. | Self-test cases for `go test ./x; set +e`, `go test ./x && set +e`, an unterminated quote and the identity line before strict mode (Q6 to Q9). The `exit` substring check is dropped: with operators banned, an `exit` cannot follow a `go test` on its line. |
| **TD-L3.** Cleanup never creating `_analysis` was untested. | `CleanupNeverCreatesTheArtifactDirectory`: a DELETE that returns 204 and the dataset cleanup job on a backend with no `_analysis` leave it absent (Q2, the review's Z5). |
| **TD-L4.** The mutation count overclaimed. | V1, V2 and K2 are marked obsolete with F2; R1 and T9 were re-run and cite their current catching tests. The review's suggested totals (105 applicable, 102 caught) counted F2 as applicable; with all four obsolete mutants excluded the counts are 104 applicable and 101 caught before this round, 113 and 110 after it. |
| **TD-L5.** Wording. | Contract and invariant 10 say the exit scope (sweep, upload retry), DELETE and cleanup never create `_analysis`, with DELETE's status going through `analysisWriterHere`; the guide says the writer is judged at the client's next retry or, after DELETE, at the next sweep (about a minute); DELETE's answer (`unproven_writers: 1`, `retryable: true`, `Retry-After: 60`) is documented and asserted with `requireDeletionPending`. |

## Remaining

- **Remote backends need a commit fence or an intervention.** A dataset with `remote_commits_unproven` (including a filesystem backend on a network or FUSE mount) stays deletion-pending. The next step is one of:
  - a provider-side fence, such as create-only writes (S3 `If-None-Match`, GCS `ifGenerationMatch=0`, Azure `If-None-Match: *`) plus retained tombstones at an exited generation's keys;
  - an explicit administrator attestation for analysis datasets, reusing the evacuation intervention pattern (REC-B).
- **No analysis intervention path exists.** Legacy writers (pre-journal uploads, unknown owners, unplanned outputs) remain deletion-pending as before, now with a reason in the DELETE answer.
- **Hosts that cannot judge.** These keep their work owned and report `unjudgeable_writers`:
  - an HA replica on another host;
  - a replaced Kubernetes pod and a restarted container, which gets a new PID namespace (this includes docker compose restarts);
  - macOS after a reboot, which has no machine identity.

  The next step is a kernel-released file lock per incarnation on a verified local filesystem, which survives PID namespaces on one kernel. A lease, a heartbeat or a NotReady status remain non-proof.
- **Heavy-work reservations and staging.** After a restart, the native worker relies on kernel containment: Linux `Pdeathsig`, and the Windows job object's kill-on-close. Darwin has none, which is the existing "Darwin hard-tree authority" item. The exited attempt's private staging directory under `_analysis-cache` is still never removed. The next step is to record the worker's incarnation through a pipeline start hook, then reclaim staging once both the controller's and the worker's exits are proven.
- **Shared original copies.** `original-<file>.parquet` is written by every attempt of an import. A late remote copy from a lost attempt can therefore overwrite the retry's copy, with identical bytes. The next step is per-attempt originals in the journal's planned keys.
- **PID reuse under hidepid.** An identity does not record the uid, so a dead writer's PID reused by another user's process under `hidepid` stays undecidable: `kill` answers `EPERM`. Recording the uid would let a different uid prove the PID taken.
- **The stuck first upload on a fresh filesystem backend.** Where nothing has been written yet, an upload writer that died after `Begin` and before its first write stays owned, because no judge creates `_analysis`. The client's retry of that offset answers 409 "upload cursor was not committed; reload durable cursor" and logs a rate-limited warning naming the directory and the workaround. While the import is `uploading` only a retry of that offset judges the writer; the sweep reaches it only once DELETE has queued the dataset's cleanup. DELETE answers 503 `analysis_dataset_deletion_pending` with `unproven_writers: 1`, `retryable: true` and `Retry-After: 60`. Any later artifact write creates `_analysis` (an upload, a normalization output or a monitor artifact via `writeMonitorArtifact`, so the gap closes at the first write of any kind); the writer is then judged at the client's next retry of that offset or, after DELETE, at the next sweep (about a minute). Operator workaround: create `<archive root>/_analysis` on the mounted volume, mode 0700, owned by the controller's user (`docs/NATIVE_ANALYTICS.md` says so).
- **Unmounted archive shares (pre-existing, not closed here).** A path does not say which volume it names: with an NFS, SMB or EFS share unmounted, writers create `_analysis` on the local mount point and write there, and cleanup treats a missing `_analysis` as nothing to delete. Operators must start the controller only after the archive volume is mounted (`docs/NATIVE_ANALYTICS.md`).
- **Locality at mint time.** Locality is judged when the exit is proven, against whatever filesystem `_analysis` is on then; a generation does not record the volume or the locality of the directory it wrote. A directory moved from a network filesystem onto a local one between the write and the proof is judged local. Binding the archive volume's identity (and its locality) at `Claim`/`Begin` would close this and the two items above. Round 5's alternative, a hard-link marker whose ID was bound in SQL, was rejected in round 6: it refused every import on filesystems without hard links (exFAT, FAT, gcsfuse, s3fs, blobfuse2, rclone, Azure Files over SMB) and needed a rebind and recovery path of its own.
- **Earlier-binary HAR identities on Linux.** They hold the PID namespace without the time namespace, so on a kernel with time namespaces they are reclaimed only after a reboot or by hand.
- **Live upload writers.** A pending writer of an upload that is still `uploading` is resolved only when its offset is retried, or after cancellation.
- **Memory-snapshot clones without a machine-id.** On Linux without `/etc/machine-id`, two VMs cloned from one memory snapshot share `boot_id`, the PID namespace and an empty machine identity. Hosts sharing analytical storage must have distinct machine-id and host-name pairs, as the HAR guide already requires.
- **Real emulators.** None was run. SeaweedFS, the repository's S3 fixture image, needs path-style addressing for a loopback endpoint, which the archive bucket URL does not set.
- **NA-C exit items not covered:**
  - native worker failure injection, with the controller alive;
  - failure during cleanup (a crash between an object delete and the job's completion);
  - deployed multi-host failure injection;
  - the dashboard items: revision conflicts, input deletion while a view is open, and both auth profiles. This slice changes no dashboard code; the dashboard shows the 503's `error` text.
