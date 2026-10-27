# QA Review Record — Replacement-Container Native HAR Reclamation (`BE-B2`)

**Date:** 2026-10-26  
**Scope:** `BE-B2` — Version-3 native HAR working-generation manifests with inherited Unix `flock` writer-tree leases and replacement-container cross-PID-namespace reclamation on verified local filesystems.  
**Primary Packages:** `pkg/protocol/browser`, `docs/guides/BROWSER_HAR_WORKING_OWNERSHIP.md`

---

## 1. Summary of Changes

1. **Inherited Unix Lease Across Playwright Driver `exec` (`pkg/protocol/browser/har_working_security_unix.go`, `pkg/protocol/browser/har_working_lease.go`, `pkg/protocol/browser/pool.go`):**
   - Added `setHARLeaseInheritable` (`f.SyscallConn()` + `SYS_FCNTL` `F_SETFD`) on Unix (`darwin` and `linux`) so `owner.lock` can be temporarily marked non-`FD_CLOEXEC` while spawning the Playwright Node driver (`startPlaywrightWithInheritedHARLeases`) and immediately restored to `FD_CLOEXEC` in the Go parent.
   - When an idle Playwright driver from an earlier closed session exists without having inherited a new pending local-filesystem HAR owner's lease (`pendingHAROwnerNeedsDriverInheritance` and `browserEvidenceBudget.liveCount() <= 1`), `getBrowserInstance` recycles the idle pool so the fresh driver inherits the active `owner.lock` open file description.
   - Bumped `harOwnershipManifestVersion` to `3` (preserving the 6-entry generation directory whitelist `harWorkingGenerationEntries = 6`). `manifest.json` (`sealed`) and `writer-entered.json` (`writer_entered`) record `Version: 3` only on Unix when `harWorkingDirectoryIsLocal` (`localfs.IsLocal`) is `true` and (for `writer-entered.json`) the recorded writer PID inherited the `owner.lock` open file description. Otherwise `Version: 2` (or `Version: 1`) is written.

2. **Cross-PID-Namespace Replacement-Container Reclamation (`pkg/protocol/browser/har_process_identity.go`, `pkg/protocol/browser/har_working_lease.go`):**
   - Added `harProcessGoneWithLease` and `harProcessesGoneWithLease`:
     - Within the **same** PID namespace (`id.Namespace == host.namespace`), `procidentity.Gone` remains strictly mandatory even for version-3 manifests.
     - Across **different** PID namespaces (`id.Namespace != host.namespace`) on the **same kernel boot** (`id.Boot == host.boot`) and **same machine identity** (`id.Machine == host.machine`), reclamation is permitted only when `leaseCoversWriterTree` is `true` (Unix, `localfs.IsLocal == true`, `manifest.json` is version 3, any present `writer-entered.json` is version 3, and the caller holds or verified free the exclusive `flock` lease on `owner.lock`).
     - Version 1/2 manifests, non-local filesystems (NFS, SMB, FUSE), foreign machine/boot identities, and Windows containers remain preserved under the fail-safe rule.

---

## 2. Verification & Two-Container Linux Qualification

- **Host & Subprocess Qualification (`darwin/arm64` & `linux/arm64`):**
  - `APEXLOAD_BROWSER_TESTS=require go test -race -v ./pkg/protocol/browser -run "^(TestHAROwnershipReplacementContainerReclaimsOnlyAfterInheritedWriterExits|TestHAROwnershipReclaimsKilledNativeRecording|TestHAROwnershipPreservesForeignAndUnprovenGenerations)$" -count=1` -> **PASS** (`4.16s`).
  - Verified in both `TestHAROwnershipReclaimsKilledNativeRecording` (Chromium and WebKit) and `TestHAROwnershipReplacementContainerReclaimsOnlyAfterInheritedWriterExits` that:
    1. While the killed generator's orphaned writer process is still alive, a replacement container in a new PID namespace plans `0` generations because the kernel holds `flock(LOCK_EX)` on the inherited `owner.lock` open file description.
    2. Within the same PID namespace, `procidentity.Gone` remains mandatory and refuses live writers even when `leaseCoversWriterTree == true`.
    3. On a non-local filesystem (`harWorkingDirectoryIsLocal == false`) or foreign machine/boot, cross-namespace reclamation is refused even after the writer exits.
    4. Once the writer exits on a verified local filesystem, the replacement container in the new PID namespace plans and prunes the predecessor container's version-3 generation.
- **Two-Container Linux PID-Namespace Qualification on Shared `ext4` Volume (`apexload-generator:local`):**
  - Container 1 (`hostname=pod-gen-0`, `namespace="pid:[4026534490] time:[4026534751]"`, `boot="6adde715-2336-4efc-a2a8-71e6baeab40b"`) created generation `ae59fb60d13ec97207a6063a6ef7307c` with `version: 3` `manifest.json` (`pid: 1`) and `writer-entered.json` (`pid: 18`) on `ext4` volume `apexload-har-reclaim-ext4`, and was killed via `docker kill -s KILL`.
  - Container 2 (`hostname=pod-gen-0`, `namespace="pid:[4026534757] time:[4026534887]"`, `boot="6adde715-2336-4efc-a2a8-71e6baeab40b"`) ran `ReclaimRuntimeHARWorking` against the shared `ext4` volume and reclaimed the predecessor container's generation (`reclaimed=1 err=<nil>`).

---

## 3. Mutation Verification (`go test -overlay`)

- `mut1_disable_v3_cross_ns` (`pkg/protocol/browser/har_process_identity.go`: forced `!leaseCoversWriterTree` to return `false`) -> **Caught** by `TestHAROwnershipReplacementContainerReclaimsOnlyAfterInheritedWriterExits` (`replacement container planned [], want 1 abandoned candidate`).
- `mut2_no_inherited_lease` (`pkg/protocol/browser/har_working_lease.go`: disabled `setLeaseInheritable(true)` in `startProcessWithInheritedLease`) -> **Caught** by `TestHAROwnershipReplacementContainerReclaimsOnlyAfterInheritedWriterExits` (replacement container prematurely planned the generation while the orphaned writer was still alive).

---

## 4. Code Review Resolution

- **Round 1 Findings Fixed:**
  1. `newHARCaptureOwner` (`pkg/protocol/browser/har_working_lease.go`) now assigns `o.lease` and `o.release` under `o.mu.Lock()`, sets `o.localFS` under `o.mu.Lock()` only after manifest setup completes, and guards `setLeaseInheritable` and `writerInheritedLease` with `o.mu.Lock()` (including requiring `!o.writerEntered` when enabling inheritance).
  2. `browserExecutableAvailable` (`pkg/protocol/browser/pool.go`) now spawns Playwright through `startPlaywrightWithInheritedHARLeases()` rather than `playwright.Run()`.

