# Native HAR working-file ownership

This guide describes the narrow native HAR working-file safety implementation.
It does not mark the complete browser evidence program or native archive
transfer/export complete. Qualification for this candidate is recorded in
[the scoped QA bundle](../qa/reviews/feature-completion-native-har-ownership-2026-10-05/README.md).

The existing browser HAR capture policies remain opt-in: off, always, on_error,
on_sla_breach, and manual. The browser_har_dir setting selects an existing
artifact directory; a symlink at that directory is rejected. The current
runtime-secret suppression, embedded-body policy and individual HAR size limit
remain in effect.

## Working and completed files

Before admitting a native context, capture setup creates a fresh unpredictable
generation below .apexload-har-work/. The private staging directories use 0700;
raw working.har, manifests, control files and scrubbed output use 0600. The
raw file is durably reserved before the pinned local SDK truncates/writes it,
so default SDK file creation permissions do not become the capture policy.

The permanent namespace lock and each generation's existing owner lock are
held by exact file descriptors. Cleanup validates their filesystem identities;
it does not replace an existing lock file to bypass another process. A
process-local active-path map is diagnostic and has no deletion authority.

Only a fresh owned SDK entry that joins its complete physical close body
produces a durable writer-joined receipt. The pinned local SDK subset adds
`CloseHAROwned`; ordinary SDK `Close` may return nil for an already closing
or closed context and is never accepted as proof. Prior ordinary/server entry
or unsupported owned-close capability remains an explicit failure, preserving
its lease and charge. Repeat owned callers join the retained same result. An unfinished repeat on
the synchronous SDK dispatcher returns an explicit error so it cannot block
the first owner’s native acknowledgment; it does not alter that owner’s result. See
[the pinned SDK patch and license](../../third_party/playwright-go/APEX_PATCHES.md). Initialization errors, oversized capture
termination and ordinary teardown share that close-once owner. Document
finalizer cancellation uses the same owner. Native workers, the finalizer,
CDP detachment and the HAR flush must join before retirement is accepted.
A failed, panicking or non-completing callback is not success.

HAR-owned teardown now uses one overall two-second budget, shortened by the
supplied context deadline. Its document-finalization and closing phases share
that deadline. Incomplete ownership is an explicit error. It retains the owner,
lease and resource charge while physical work remains unfinished; a timed-out
request does not stop a shared driver or authorize deletion. A later actual
join can finish the owned work. Filesystem/finalization errors remain visible
and may leave a charged owner requiring reconciliation or process shutdown.
A new context cannot reuse an unretired capture owner.

After successful join, scrubbed output is written and synced in private staging,
then linked to a fresh flat apexload_browser_<generation>_scrubbed.har name
without replacing an existing artifact. The final name never exposes an
in-progress copy. Flat completed files remain discoverable by controller HAR
retention. Publication and durability failures return errors.

## Interrupted retirement

A private, versioned retirements.json ledger records a completed artifact
or proven writer-joined generation before rename or unlink. Its identity
contains the original device, inode and owner UID. The shared namespace
descriptor lease serializes updates. Ledger replacement is checked; updates
are synced before destructive steps, and records are cleared only after exact
payload disappearance is checked and synced.

One bounded cleanup plan revalidates the exact ledger inode and record set
before every candidate, including joined generations not yet in the ledger.
It advances its own authorization only after a verified durable removal and
descriptor close. External replacement requires a fresh plan. A completed
artifact interrupted before its recorded rename is listed once by its exact
basename and file identity, even when the normal flat scan also finds it.

Raw and completed retirement records are separate. Raw payloads use a
generation-bound .retired-<generation> directory. Completed payloads use a
recorded .har-retired-<generation> tombstone. Recovery can resume after any
raw control-file unlink, an empty terminal directory, payload removal, or a
completed rename. Existing owner leases are still acquired when present.
A missing lease alone is never proof; it is usable only with a previously
durable identity-bound joined retirement record under the namespace lease.

Malformed, replaced, full or unsupported ledger state fails admission or
maintenance explicitly and preserves the existing state. Changed payloads,
unexpected same-name replacements, unknown tombstones and unproven legacy
*.har.tmp files are preserved. Age, PID state and an unlocked lease do not
establish that another process's native writer has died. This is qualified
terminal-retirement recovery, not general crash cleanup.

## Bounds and compatibility

| Control | Candidate limit |
| --- | --- |
| Pending capture owners in one process | 64 |
| Namespace owners in one process | 128 |
| Existing private generation entries at admission | Fewer than 128 |
| Working admission bytes | 2 GiB at the admission check |
| Retirement ledger | 128 records; 64 KiB |
| Ownership manifest | 4 KiB |
| Ledger auxiliary entries | 128, each at most 64 KiB |
| Private staging, working-plan and runtime-retention scans | 1,024 entries |
| Completed retention | 128 artifacts; 1 GiB |
| Completed-retention error details | 16 plus a count of further failures |

The 2 GiB check measures existing staging bytes at admission. It does not
reserve future native writes, enforce an aggregate active-writer quota, or
bound total process RSS. The existing per-HAR limiter cannot observe all
native in-memory buffering. Those resource guarantees remain separate work.

This implementation uses Linux/macOS owner identity checks and Go's secure
os.Root operations. Other platforms reject native HAR ownership until their
private ACL, lock and durable publication behavior is qualified. The coordinated
local runtime acceptance host is macOS; results are recorded separately in
the QA bundle. Linux/Windows runtime acceptance must not be inferred from it. The filesystems and peer processes must honor the
existing descriptor-lock contract. The implementation neither coordinates
remote object-store writers nor transfers raw files into managed archives.

Legacy transaction/resource formats and HAR import behavior are unchanged.
The deterministic frame-finalizer correction also prevents one stale native
frame's evaluation error from censoring valid live sibling receipts.
