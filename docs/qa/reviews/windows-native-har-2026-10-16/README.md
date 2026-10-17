# Native HAR capture on Windows

Status: Wave 1 (platform gate, PR #228) is on main. Waves 2–4 (spike and
`pkg/internal/winfs`, Windows ownership, portable tests, CI job, guide) are PR
#229. **Qualified on Windows:** the full HAR step passed three runs in a row on
the final code (see [Repeat runs](#repeat-runs)).

The project has no Windows machine. Every Windows statement below comes from
the Windows leg of `.github/workflows/desktop.yml` on GitHub-hosted runners.
Each one cites the run that showed it.

## Wave 1: platform gate (D7)

Evidence: desktop run 38947072872, job 117128780404 (commit fcb12aeea,
`windows-2025`). In its "Native HAR platform gate" step, with
`APEXLOAD_BROWSER_TESTS=require` and real Chromium:

- `TestNativeHARSupportedMatchesPlatform` passed: `NativeHARSupported()` was
  false on Windows.
- `TestUnsupportedPlatformBrowserVUserRunsWithoutHAR` passed. A HAR-enabled
  browser VUser ran its navigation and reported
  `browser_har_unavailable_har_platform_unsupported`, with nothing in its HAR
  directory. The generator logged `reason=har_platform_unsupported
  platform=windows` once.
- `TestUnsupportedPlatformHARNamespaceCreatesNothing` and
  `TestUnsupportedPlatformHARCaptureRunsWithoutRecordingAndSaysSoOnce` passed.
- `TestBrowserReplayRecordHARRefusedWithoutNativeHAR` passed on both routes:
  a replay asked for `record_har: true` got 422 and started nothing.
- `TestNativeHARRecordsItsEvidenceGeneration` skipped, as intended.

The same job then failed in its "Native analytics contracts and controller
build" step. Main fails that step too (8ae712536, job 117119599571), so the
failure is pre-existing and is being fixed in PR #230.

## Wave 2, WP1: Phase 0 spike

Evidence: desktop run 38950445622, job 117140185924 (commit 161f8bd5c). Runner
image `windows-2025-vs2026` 20261002.596, Windows Server 2025, Go 1.26.9. The
temporary test `pkg/protocol/browser/har_windows_spike_test.go` printed one
`SPIKE` line per observation. It and its workflow step were removed once these
findings were recorded.

### Environment

- The token is elevated. The runner account is the built-in Administrator
  (RID 500), which Windows renders as `LA` in SDDL, and its token default owner
  is that same account. Code must compare descriptors as Windows renders them,
  not as strings built from SIDs.
- `%TEMP%` (`C:\Users\…\AppData\Local\Temp`) is on a local fixed NTFS
  volume. It is owned by Administrators, with inherited full access for SYSTEM,
  Administrators and the user. A directory made there with `os.Mkdir` is not
  private. `RUNNER_TEMP` (`D:\a\_temp`) also grants Users read.
- Microsoft Defender's service runs, but real-time, on-access and behavior
  protection are **off** on hosted runners. Antivirus contention cannot be
  measured there (see Q7).

### Host and process identity (D6)

- `HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid` is present.
- `BootId` under `…\Session Manager\Memory Management\PrefetchParameters` is a
  REG_DWORD (14 on that boot).
- `ContainerType` is absent, so the runner is not a container.
- The physical DNS host name is present.
- A live process (this one) gives `WaitForSingleObject(h, 0)` = WAIT_TIMEOUT
  (258) and a creation time from `GetProcessTimes`.
- An exited child whose handle is still held gives WAIT_OBJECT_0.
- Once every handle to the exited child is closed, `OpenProcess` fails with
  ERROR_INVALID_PARAMETER (87). An unused PID gives the same error.
- PID 4 (System) opens with `PROCESS_QUERY_LIMITED_INFORMATION`.

D6 holds as specified.

### Q1: file identity

`os.Lstat` and `os.Stat` load the file ID lazily, at `os.SameFile`, from
whatever the path names then:

- A `FileInfo` taken before the file was renamed away and replaced compared
  equal to the replacement.
- It did not compare equal to the moved original.

`os.Root.Lstat` and `os.Root.Stat` load it at the call: the same scenario gave
the moved original. `os.Root.Stat(".")` matches the opened directory.

`FILE_ID_INFO` gives a 128-bit ID whose high 64 bits are zero on NTFS. Its low
64 bits equal the 64-bit file index from `GetFileInformationByHandle`. Sys() of
a root `FileInfo` is `*syscall.Win32FileAttributeData`, which carries no ID, so
identity must be read from a handle (D3).

### Q2: `os.Root.Link`

`os.Root.Link` works on Windows. It is create-only: linking over an existing
name fails with ERROR_ALREADY_EXISTS (183). `os.Link` works too. No separate
create-only publish primitive is needed.

### Q3: sharing

- Handles from `os.Root.Open` and `os.Root.OpenFile` share delete. A file held
  through them can be renamed and removed.
- `os.Open` handles do **not** share delete. Rename and remove of a file held
  through one fail with ERROR_SHARING_VIOLATION (32). HAR code must never
  hold namespace objects through `os.Open`.
- A directory held by `OpenRoot`, or by `OpenRoot` plus `Open(".")`, can be
  renamed and removed.
- A directory with an open handle to a **child** cannot be renamed: ERROR_ACCESS_DENIED (5).
- A handle-relative `FILE_RENAME_INFORMATION_EX` without replace works, and
  fails with STATUS_OBJECT_NAME_COLLISION onto an existing name.
- POSIX delete through a handle removes the name at once while a share-all
  reader stays open, and the name can be reused immediately.
- `os.Remove` of a file another share-all handle holds also removed the name at
  once on this build. Delete-pending names can still come from other programs'
  legacy deletes.

### Q4: directory durability

`File.Sync` on a directory handle (`os.Root.Open(".")` or `os.Open`) fails with
ERROR_ACCESS_DENIED. `FlushFileBuffers` on a handle to the same directory
succeeds when it is opened for `GENERIC_WRITE` or for
`FILE_ADD_FILE|FILE_ADD_SUBDIRECTORY`. It fails for `FILE_WRITE_ATTRIBUTES`
or `GENERIC_READ`.

The durability boundary for a directory's entries is therefore
`FlushFileBuffers` on a write-access reopen of the directory
(`winfs.SyncDir`).

### D2 and Q5: private creation and the driver's flush

`NtCreateFile` relative to the parent's handle, with `FILE_CREATE` and the
descriptor `D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;<user>)`, creates a protected
directory owned by the user. Creating it again fails with
STATUS_OBJECT_NAME_COLLISION.

Inheritance:

- A generation directory below it inherits `(A;OICIID;FA;;;SY)(A;OICIID;FA;;;user)`.
- `working.har` inherits `(A;ID;FA;;;SY)(A;ID;FA;;;user)`.

A real Chromium context recording to the pre-created `working.har` flushed
2,081 bytes into the same file ID, with one link and the inherited private
DACL. The generation held only `working.har`.

### Q6: zero-length lock

`LockFileEx` (exclusive, 1 byte at offset 0) on a zero-length file:

- It conflicts with a second handle in the same process: ERROR_LOCK_VIOLATION (33).
- It conflicts with another process.
- It is released by unlock and by closing the holding handle.
- The file stays zero length.

### Q7: transient errors

2,000 cycles of create, write, rename and remove of a generation-shaped
directory gave no errors. Real-time protection was off (see Environment), so
this measures the filesystem, not antivirus. The bounded retry on sharing,
lock and delete-pending errors stays.

### Adjustments to the design

- **D4:** confirmed and required. A generation directory cannot be renamed
  while any handle to a child is open, so the owner lease and the generation
  root are closed before the rename.
  - A scanner briefly holding a child would fail the rename with
    ERROR_ACCESS_DENIED, which Windows also returns for real ACL refusals. So
    the generation's tombstone rename retries ERROR_ACCESS_DENIED within the
    same `runtimeHARCloseWait` bound as sharing errors, and then fails closed.
  - Directory removal also retries ERROR_DIR_NOT_EMPTY, which a delete-pending
    child causes.
- **D5:**
  - No create-only link primitive: `os.Root.Link` already is one.
  - `SyncDir` is `FlushFileBuffers` on a write-access reopen.
  - `RemoveAt` and `RemoveIfSame` count a name that is already delete-pending
    as removed.
- **D3:** confirmed. `os.Lstat` cannot be used for the parent before/after
  checks, and root `FileInfo`s carry no ID. `winfs.Lstat` stats through a
  zero-access handle, and record identity is read from a handle to the name.

## Wave 2, WP2: `pkg/internal/winfs`

The first Windows run of the winfs unit tests (same job) failed three tests.
All three were test errors:

- The private-DACL test compared SDDL text against a string built from the SID,
  which Windows prints as `LA`.
- The rename and remove tests held their reader through `os.Open`, which does
  not share delete (Q3).

The tests now compare the descriptors as Windows renders them and hold readers
as scans do (share all). `pkg/importartifact`, switched to the shared volume
policy and write-through move, passed in that job.

## Wave 3: Windows ownership

Native HAR ownership on Windows, as the
[guide](../../../guides/BROWSER_HAR_WORKING_OWNERSHIP.md#windows) describes it:

- **D1–D4** (464c57f00):
  - handle-based privacy checks (owner, DACL, no reparse point, one link);
  - private creation through `NtCreateFile` with the descriptor;
  - identities read through handles, with the 128-bit file ID and owner SID in
    the ledger;
  - `:` refused in name components;
  - one retirement order on every platform, with the Windows retry bounds.
- **D6** (322e6270b): `MachineGuid` with the physical DNS host name, `BootId`,
  process creation times; crash reclamation disabled inside containers.
- **Replay store** (80c35d54e): the managed replay HAR root is created private;
  a root that is not private is not adopted, and the store uses a fresh private
  directory instead.
- **Platform flip** (872476f60): `NativeHARSupported()` is true on Windows.

## Wave 4: Windows tests and CI

The desktop job's "Native HAR ownership (Windows)" step (60ffc6c5c) installs
Chromium and WebKit and runs `pkg/internal/winfs`, the browser package's `HAR`
tests and `pkg/api`'s `HAR|BrowserReplay` tests with
`APEXLOAD_BROWSER_TESTS=require`. The runs, in order:

1. **Run 38966884185, job 117195439328** (8c7fc92c0). The browser suite: 98
   passed, 7 failed, and every failure was in a test (fixed in ad2108e01):
   - Ledger fixtures renamed generations with their files still open, which
     Windows refuses (D4).
   - A fixture wrote its artifact straight into the HAR directory, so it
     inherited `%TEMP%`'s ACL.
   - An SDDL comparison used a string built from the SID, not the descriptor
     Windows renders.
   - A namespace-replacement test assumed the rename succeeds. Windows refuses
     it while the lock is held.
   - A read-only directory test was defeated by the runner token's enabled
     backup privileges (below).
   - A waterfall test expected WebKit request timing that Windows does not
     expose.
2. **Run 38970933046, job 117208848160** (a7d9f2922). The browser HAR suite
   passed. Three `pkg/api` archive tests failed with
   `sync <dir>: Access is denied.`: two archive directory-sync helpers did not
   tolerate Windows' refusal of `File.Sync` on a directory. All six `pkg/api`
   directory syncs now flush through `winfs.SyncDir` (760731aef).
3. **CodeQL** reported a new path-injection alert. It was the same `os.Lstat`
   of `browser_har_dir` that `main` reports as alerts #309, #310 and #316,
   moved into a helper. The calls are back where `main` has them, and Windows
   pins identity separately (cc23bf40c).
4. **Run 38980828584, job 117241050682** (cc23bf40c; runner image
   `windows-2025-vs2026` 20261007.264.1). **The whole step passed**:
   `winfs`, the browser package (27.7 s) and `pkg/api` (22.1 s), 283 HAR and
   replay tests. Three subtests skipped:
   - WebKit's waterfall comparison, by design (below).
   - Two subtests of `TestHARArtifactOwnershipRetiresEntireRecordedCompletedBatch`,
     which built Unix identities. That test now builds platform identities
     (b10a9e6a4).

The job then failed in its later native analytics step, which `main` fails
too; that is PR #230's subject.

### Done criteria in run 38980828584

| Criterion | Tests | Result |
| --- | --- | --- |
| A HAR-enabled browser VUser publishes a scrubbed HAR into the run archive | `TestHARCaptureAlwaysPersistsScrubbedArtifact` (real Chromium), `TestLocalRunArchiveRetainsBrowserHARs`, `TestDistributedArchiveTransfersHARsWithReceipts` | passed |
| A browser replay with `record_har` serves its native HAR from a private store | `TestBrowserReplayActualNativeHAR` (real Chromium), `TestReplayHARRootIsPrivateOnWindows` | passed |
| A killed generator's generation is reclaimed after restart | `TestHAROwnershipReclaimsKilledGenerations` (write, scrub, publish), `TestHAROwnershipReclaimsKilledNativeRecording` (Chromium and WebKit drivers killed with `TerminateProcess`) | passed |
| `.apexload-har-work`'s DACL allows only the user and SYSTEM | `TestWindowsHARWorkingDirectoryDACLIsPrivate` | passed |
| Unsafe ACLs, UNC paths, delete-pending names and identity across rename | `TestWindowsHARRefusesUnsafeWorkingDirectoryACLs` (inherited Users, Everyone, NULL DACL, foreign owner), `TestWindowsHARRefusesUNCDirectory`, `TestWindowsHARRemoveCountsDeletePendingAsRemoved`, `TestHARPersistentIdentityAtIsStableAcrossRename` | passed |
| 200-cycle create, publish and retire stress | `TestWindowsHARCreatePublishRetireStress` (12.5 s) | passed |
| The full HAR ownership suite passes on Windows, Linux and macOS | Windows: this step. Linux: CI's race lane (the browser package under `-race`) and the three `pkg/api` shards. macOS: local runs of the browser package's HAR tests and `pkg/api`'s HAR, archive and replay tests | Windows passed except the two since-ported subtests, which pass in repeat run 1. macOS passed. Linux passed on b10a9e6a4 (CI run 38986149051): race job 117260064411 ran 19,755 tests with no failures, including the browser package (2m 4s; its only skips are two env-gated benchmarks), and all three `pkg/api` shards passed |

### What the runs showed about Windows

- **Backup privileges.** The runner token has `SeBackupPrivilege` and
  `SeRestorePrivilege` enabled. `os.Root` opens ask for backup intent, so this
  process's own opens get past a DACL. The read-only directory test disables
  both privileges for its duration, and the run logged that both had been
  enabled.
- **Rename of a locked directory.** Windows refuses to rename
  `.apexload-har-work` while a handle to its `namespace.lock` is open
  (`Access is denied.`). So the namespace-replacement attack the test simulates
  cannot happen there, and the test now checks the refusal.
- **WebKit timing.** Playwright's Windows WebKit exposes no request start, so
  its request-wait phase reports `not_exposed`. Its native HAR still records
  the server's timing: wait 301.6 ms and receive 202.6 ms against the
  fixture's 300 ms and 200 ms.
- **Token owner.** The runner's elevated token makes the user the owner of
  what it creates (`TestWindowsHARAcceptsElevatedTokenOwner`).

### Not shown on Windows

- Behavior under on-access antivirus scanning: hosted runners run Defender
  with real-time protection off.
- Windows containers: the runner is not one. `TestWindowsHARHostIdentity`
  shows only that the container check reads it as a non-container; the
  disabled reclamation inside a container has not run.
- ReFS volumes and SMB shares: only the UNC refusal runs against a real
  path. `TestValidateVolume` checks the drive-type and filesystem policy with
  synthetic values.

### Repeat runs

The Windows HAR step must pass three runs in a row on the final code.
Each run is recorded here as it completes. Runs 1 and 2 passed on the HAR
code as of b10a9e6a4. `main` then changed one HAR test,
`TestHAROwnershipCrossProcessCleaner` (#234: it now publishes its ready
marker by rename and allows a slow child 8 s). It also added the Windows
browser memory sampler to the browser package (#233). So three runs on the
merged code (01a0b60ed and later) were needed; they are below.

| # | Run, job | Commit | HAR step |
| --- | --- | --- | --- |
| 1 | 38986149129, 117257968307 | b10a9e6a4 | passed: `winfs`, the browser package (40.1 s), `pkg/api` (39.0 s); 345 tests; the completed-batch subtests ran and passed; one skip, WebKit's waterfall comparison |
| 2 | 38994746174, 117284707561 | 8e6dbdea1 (the record and a main merge; no Go changes) | passed: `winfs`, the browser package (39.7 s), `pkg/api` (35.3 s); 345 tests; one skip, WebKit's waterfall comparison |

On the merged code (01a0b60ed and later):

| # | Run, job | Commit | HAR step |
| --- | --- | --- | --- |
| 1 | 39001525209, 117305285602 | e9d6b3ddc (#235's merge; no Go changes since 01a0b60ed) | passed: `winfs`, the browser package (45.7 s), `pkg/api` (43.9 s), and the memory step's browser tests; 349 tests; one skip, WebKit's waterfall comparison |
| 2 | 39005524599, 117317092061 | c6397573b (the record and #236's merge; no Go changes) | passed: `winfs`, the browser package (35.2 s), `pkg/api` (37.9 s), and the memory step's browser tests; 349 tests; one skip, WebKit's waterfall comparison |
| 3 | 39005524599 attempt 2, 117330597493 | c6397573b (a re-run of run 2's job) | passed: `winfs`, the browser package (46.5 s), `pkg/api` (39.3 s), and the memory step's browser tests; 349 tests; one skip, WebKit's waterfall comparison |

Later `main` merges bumped Go modules the HAR code does not use (`x/mod`, the
AWS SDK, `modernc.org/cc`; #237) and dashboard packages. `x/sys` and the
pinned Playwright driver are unchanged, so they leave the code under test as
qualified.

Each of these jobs then failed in its later native analytics step
(`TestAnalysisCleanupRetriesMetadataReadFailure` and similar), which `main`
fails too and PR #230 fixes. That PR's Windows job has since passed in full.

