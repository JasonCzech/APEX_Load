# Native HAR working-file ownership: read-only design review

Date: 2026-10-05. Source checkout: `/Users/jasonczech/.codex/worktrees/feature-completion-resume/Project_Stormrunner`.

This review proposes the next narrow native HAR safety slice. It implements nothing and does not qualify native HAR export, attachment to run archives, end-to-end replay, request-admission memory limits, or full BE-3 delivery. The completed sample compatibility slice changes only synthetic regression tests and its QA evidence. The private original HAR was not opened or replayed for this design review.

## Findings in the current native implementation

1. **Live writers in another process can be deleted.** `pkg/protocol/browser/har_capture.go:34-87` uses a process-local `sync.Map` as deletion authority. `applyHARCaptureOptions` calls the flat-directory purge before every new capture. A controller, desktop process, or second generator sharing the directory sees none of another process's registrations. `pkg/api/har_artifact_retention.go:100-120,168-208` similarly treats every matching unregistered raw file as immediately eligible, including fresh files when retention is disabled. The controller's inode/size/time checks and tombstone rename guard replacement of a planned file; they do not prove that its writer stopped.

2. **Some teardown paths lack writer-join proof.** `runtime_context.go:90-93` discards the working file before calling `browserContext.Close` after an emulation-script failure. Other initialization failures close first but ignore the close result. `lifecycle.go:27-29` ignores the close error and continues to finalize. `closeOversizedHARContext` removes the context from `b.pwContext` before a close whose result is ignored, so normal close cannot subsequently join that exact context through that field. On Unix, unlinking an open file does not stop its writer; a later flush that reopens the path can create a new raw file after cleanup.

3. **A completed filename is visible before completion.** `har_capture.go:327-333` writes scrubbed output directly to the retained final filename. Concurrent completed-artifact retention may observe or unlink a partial file. The runtime retention mutex in `har_retention.go:27,80-81` serializes only one process. Its check-then-path-remove at `har_retention.go:128-139` also does not share the controller's held-root/tombstone discipline.

4. **Directory ownership is weaker than the existing archive patterns.** The native purge does an unbounded `os.ReadDir`, and capture reads, writes and removals reopen absolute paths. Existing API and generator archive helpers hold `os.Root` descriptors and verify directory/file identity before and after open. Those patterns are reusable; their package-private functions cannot be called from browser without an import cycle.

These are existing implementation findings, not regressions introduced by the synthetic import tests.

## Proposed narrow seam

Add `pkg/protocol/browser/har_working_lease.go` and `har_working_fs.go` beside the existing capture code. API already imports browser, so a small exported cleanup entry point there can serve both browser startup maintenance and controller maintenance without introducing a top-level package or importing API into browser. Keep manifest parsing, filesystem operations, lease identity and candidate validation in one implementation. The process-local map may remain diagnostic compatibility information, but must no longer authorize raw deletion.

Keep `browser_har_dir` / `HARArtifactDir` as the published artifact root. Beneath it use a reserved private directory such as `.apexload-har-work/`, then a cryptographically random, never-reused capture-generation directory. Each generation contains only a fixed allowlist: `owner.lock`, a bounded versioned ownership manifest, `working.har`, and a private scrubbed publication file. Directories are private, files are 0600, and the root is rejected if its existing ownership/type/permissions are inconsistent with the supported platform's contract. Do not repair or adopt an unknown directory by chmod, following a symlink, creating a missing lock, or interpreting its age as ownership proof.

The raw basename and private child directory intentionally do not match the legacy flat `apexload_browser_*.har.tmp` pattern. An older controller therefore cannot mistake new raw staging for a legacy candidate. Completed output remains a direct child named `apexload_browser_<random-generation>_scrubbed.har`, preserving both existing retention scanners and scrubbed sensitivity classification. Raw staging is never a download/attachment candidate.

### Lease acquisition and initialization

Open and verify each namespace component through held `os.Root` descriptors using the before/open/after `os.SameFile` pattern in `pkg/api/archive_backend.go:997-1066` and `pkg/distributed/generatorrpc/archive_fs.go:163-199`. Open an existing regular lock through that root, verify the opened descriptor and directory entry refer to the same inode, and use `filelock.LockContextOwned`. Retain the exact release callback and file descriptor even if lock acquisition returns both a callback and an error; invoke release once and close the descriptor under the actual owner.

A short shared namespace lock protects generation-directory creation, lease acquisition, and manifest sealing. Cleaners use that same namespace lock when inspecting a generation for reclamation. They skip incomplete, unsealed or unknown-version records. This removes the creation race in which a new directory becomes visible before its lock is held. The lock file must not be unlinked or replaced while the namespace is in service: different lock inodes would create independent holders. Use the same identity validation for this parent lock, and refuse unverifiable replacement.

The per-generation lease is held before handing `RecordHarPath` to Playwright, and through context close, monitor/callback joins, bounded read, scrub, publication, raw removal and directory retirement. Record an opaque process incarnation and capture-generation token; PID, mtime, a heartbeat or TTL are informational, not evidence that writers are dead. No source URL, header, body, secret or credential belongs in the manifest.

Make preparation errors observable before `NewContext`: either change the context-options helper to return an error and update its callers, or split a fallible `prepareHARCapture` from pure option assembly. An explicitly requested capture cannot silently proceed without its ownership boundary. Retain the current runtime-secret gate before staging allocation.

### Exact-generation teardown and late writers

Store a single immutable capture owner on `Browser` rather than treating its mutable path field as authority. The owner holds the generation token, opened roots, lease descriptor, exact Playwright context, monitor join, close result and publication status. Monitor callbacks capture that owner; compare owner identity as well as evidence generation before marking, closing, removing or publishing. Never resolve an old cleanup through the browser's newer path or context fields.

One closer owns `context.Close` and all callers join it. A cancelled waiter, timeout, close error, panic or missing current context is not join proof. An oversized-context close retains its exact owner so normal teardown joins the already-entered close. Every initialization-error branch closes and joins before discarding. On a failed or unjoined close, leave the lease and bounded registry charge with that owner; report capture unavailable/incomplete, reject further admission when retained ownership reaches its cap, and allow a late successful join to retire only that owner. Do not open unlimited replacement generations to hide failed teardown.

The existing document-evidence generation is initialized after `NewContext` in `installDocumentEvidence`. Create a separate capture token before `NewContext`; link it to the evidence generation once installed rather than requiring the later token to fence early failures.

### Safe maintenance and crash cleanup

Use bounded batched enumeration, a bounded ownership-manifest read, an overall maintenance deadline and a maximum generation count per pass. Lock contention/deadline is a skip or explicit incomplete maintenance result, never a dead-owner classification. `LockContextOwned` already supports a bounded wait; a new single-attempt existing-descriptor API is optional only if measured contention requires it, and would need the same release-ownership tests.

A maintenance candidate must pass all of these conditions immediately before deletion:

- Recognized sealed manifest and exact expected generation/namespace identity.
- Existing regular lease descriptor opened through the held directory; current entry and descriptor identity still match.
- Exclusive lease acquired and held through the entire retirement operation.
- Affirmative writer-quiescence proof for that generation.
- Allowed regular children only; symlink, hardlink, mount or identity ambiguity fails closed.

Under the shared namespace fence, rename the exact generation to a unique private retirement name, verify directory identity, and remove only allowlisted children through the held roots. Keep lease authority through unlink and exact descriptor closure. Failed remove/unlock/close retains an unproven cleanup result and charge; it is not a successful prune. Do not fall back to an unrestricted `RemoveAll` or reconstruct an absolute path after verification. A retry rejoins the same physical owner.

**Important crash limit:** an unlocked Go-owner lease does not by itself prove that Playwright's native writers stopped. `pool.go` shares a driver and browser across many contexts. The pinned Go client exposes `Pid()` for the driver, and its transport joins the driver only on `Stop`; these are not per-context ownership, containment, or child-death receipts. Killing that shared driver would disrupt unrelated contexts. Therefore the narrow slice can safely reap a generation with a durable `writer_joined` receipt written only after an actual successful context/worker join, or a generation with separately verified owned-runtime quiescence. It must leave crash remnants with no such receipt private and unavailable for automatic deletion. Admission accounts for their disk occupancy and rejects capture when quota is exhausted rather than deleting uncertain writers.

Automatic cleanup of arbitrary crashed/unjoined native captures remains a separate owned-runtime prerequisite: give the writer runtime or supervisor ownership of the lease throughout the complete writer subtree, use platform containment with exact process incarnation/exit proof, and validate kill/flush/crash behavior. Passing an inheritable lock to just one arbitrary child, checking a PID is absent, or assuming stdin EOF implies all writers immediately exited does not satisfy this proof. Conservative retained crash remnants are a compatibility/safety limitation to document explicitly.

### Durable, collision-safe scrubbed publication

After writer join, read the raw file through the held generation root with before/open/after identity checks and the existing 100 MiB bound. Scrub using both existing generic and runtime-secret redactors; failures publish nothing. Write scrubbed bytes to an exclusive private file, verify full write, file sync and close success, then make the closed file discoverable atomically in the published parent namespace.

The local Go 1.26.5 `os.Root` includes `Link`. On a filesystem qualified for it, linking the private completed file to a fresh final name provides no-replace publication; collision returns an error rather than replacing another artifact. Validate both source and published inode, synchronize the parent, remove the private link and synchronize staging. Both names must be beneath the same held published root. Do not introduce a copy-to-final fallback that reveals partial output. If a platform/filesystem cannot support the required no-replace durable publication, explicitly report capture unavailable until an equivalent implementation is qualified.

A shared completed-namespace lock serializes publication and both runtime/controller pruning. Preserve 128-artifact/1 GiB runtime safety limits and configured completed-HAR age retention. Do not claim the local artifact path is durably attached to a run or safe for a future reader after retention: artifact transfer/reader reservations and archive manifest references remain separate work.

## Compatibility and acceptance boundaries

- Replace the unsafe immediate legacy-raw purge: a flat `.har.tmp` with no ownership manifest is unknown and is skipped by default, even if old or fresh and even when normal retention is disabled. An explicitly quiesced administrative migration can remove legacy files later; this slice must not invent such authorization or infer quiescence from age.
- New raw staging avoids the old flat scanner, but mixed old/new processes do not share the new completed-namespace fence. Require upgrade/quiescence before advertising a globally enforced shared completed budget. Existing already-complete scrubbed files remain discoverable and eligible under the unchanged naming/age policies.
- The native Playwright API accepts an absolute path, not an opened `os.Root` descriptor. Held-root internal I/O does not make that external path traversal secure if operator-owned ancestors are concurrently replaced or a malicious process of the same identity mutates staging. Qualify an operator-controlled canonical root and cooperative lease participants; fail closed on detected replacement. Strong adversarial containment requires the owned-runtime work, not a pre/post `Lstat` claim.
- POSIX mode bits are not Windows ACL enforcement. Windows handle-sharing, hardlink publication, directory sync and lock/rename behavior require native qualification. Network filesystems require explicit advisory-lock/atomic-publication qualification; do not advertise an untested NFS/SMB/cloud-cache ownership contract.
- Current size/entry monitoring checks completed requests and on-disk growth. This design does not solve Playwright's pre-flush embedded-body memory consumption or issued/in-flight request admission.
- Capture policy, DSL fields and final sensitivity naming can remain compatible. Any new public retention status/field requires generated API/TypeScript freshness checks. Do not edit those generated documents by hand.

## Exact implementation and test targets

| File | Proposed work / required oracle |
|---|---|
| `pkg/protocol/browser/har_working_lease.go`, `har_working_fs.go` (new) | Common bounded ownership, secure root opens, sealed manifest, held descriptor leases, exact cleanup and collision-safe publication. |
| `pkg/protocol/browser/har_working_lease_test.go` (new) | Real separate-process live lock survives both cleaners; incomplete creation cannot be reaped; abandoned lock with no writer-join proof remains; joined abandoned generation can retire; generation/lock replacement and malformed/oversized/unknown manifest rejected; cancellation/late acquisition/unlock/close failure retains exact authority. |
| `pkg/protocol/browser/har_capture.go`, `browser.go`, `runtime_context.go`, `lifecycle.go` | Replace mutable-path cleanup with owned generation; propagate fallible capture setup; close/join all error branches; retain oversized close owner; atomic publication. |
| `pkg/protocol/browser/har_capture_integration_test.go` | Replace `TestApplyHARCaptureOptionsPurgesOrphansButPreservesActiveWorkingFiles` with lease/proof-based behavior; existing policy and runtime-secret scrub/gate tests use real owned staging; no raw recreation after joined close. |
| `pkg/protocol/browser/har_limit_test.go`, `runtime_context_test.go` | Late old monitor/close cannot mutate new owner; blocked/error close retains capture lease and does not finalize; successful late join cleans only old generation; setup failure precedes `NewContext`; init failure close precedes remove. Private close/context seams should test meaningful lifecycle behavior, with real browser integration for native flush. |
| `pkg/protocol/browser/har_retention.go`, `har_retention_test.go` | Share held completed-namespace fence and secure deletion; preserve count/byte bounds; subprocess publisher/pruner barrier cannot expose or delete partial final file; collision never overwrites; external/unowned/symlink files remain. |
| `pkg/api/har_artifact_retention.go`, `har_artifact_retention_test.go` | Delegate new raw maintenance to common helper; apply must reacquire/fence and revalidate. Change `TestPlanHARArtifactRetentionImmediatelyPurgesRawWorkingFilesEvenWhenDisabled` and the raw expectation in `TestPlanHARArtifactRetention` to explicit unknown-legacy preservation. Keep replaced-candidate, completed-age, bounded-candidate and symlink tests. Add browser-process writer versus controller-process cleanup test and failed/unproven deletion reporting. |
| `pkg/internal/filelock/context_test.go`, `context_owned_test.go` | Reuse existing actual subprocess exclusion and exact release tests. Extend only if adding a single-attempt helper; test descriptor preservation, acquisition-plus-error authority, repeat release, panic/failure and native platform contention. |
| `pkg/protocol/browser/pool.go` and later owned-runtime tests | Separate follow-on if arbitrary crash cleanup is required: per-owned runtime/subtree lease lifetime and physical join/containment. Do not call shared `Stop` as a per-capture fix. |

Affected Go suites, targeted race checks and actual cross-process tests are required for an implementation. Native browser tests must establish zero raw leftovers after successful native close, controlled failed/blocked closes, and absence of unrelated-context disruption. No such checks were run for this read-only proposal.

## Source qualification

Inventory: `/private/tmp/apexload-native-har-working-ownership-source-20261005.json`, SHA-256 `a480ae92d10c6e31965991ee85efcf95ba5d3238abe097497078793155fea3a2`. It records 30 direct repository inputs and three local pinned dependency/toolchain references, not a full transitive Go inventory. The freeze HEAD was `81bd5eb760007adce060450a1f0cce03d8846732`. Before/after revalidation is recorded separately; this note is intentionally outside the committed HAR compatibility QA bundle.
