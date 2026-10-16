# Native HAR capture on Windows

Status: Wave 1 (platform gate, PR #228) delivered. Wave 2 (Phase 0 spike and
`pkg/internal/winfs`) in this branch. Waves 3 and 4 (Windows ownership, portable
tests, CI job, guide) follow.

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
