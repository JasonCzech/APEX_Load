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
*.har.tmp files are preserved. Age, PID state and an unlocked lease alone do
not establish that another process's native writer has died; crash
reclamation below needs exact recorded process identities.

## Crash reclamation

A generation whose generator was killed never writes its writer-joined
receipt. Its sealed manifest (version 2) records the exact identity of the
generator holding the lease, and `writer-entered.json`, written and synced
before the native HAR writer (the Playwright driver) is given the working
path, records that writer. An identity is the machine, the kernel boot, the
PID namespace, the PID and the process start time: on Linux the boot ID,
`/proc/self/ns/pid`, `/etc/machine-id` paired with the host name (a
machine-id alone is shared by container images and cloned VMs) and
`/proc/<pid>/stat`. Hosts that share one HAR directory must differ in that
pair: a VM cloned from a template must have its `/etc/machine-id` regenerated
or a host name of its own, and a host that still reports `localhost` must not
share the directory, or one host's reboot could be taken as proof that the
other's live writer exited. On macOS an identity is the boot session UUID and
the kernel process table, with no machine identity (macOS offers none without IOKit, and
`kern.uuid` names the kernel build), so an earlier boot never proves an exit
there. A writer record is completed before the path is handed out: one that
cannot be written leaves no writer entered (and is removed), and one a crash
left half-written counts as no writer.

Cleanup reclaims such a generation through the same retirement ledger only
when its owner lease is free and every recorded process provably no longer
exists: the same identified machine has booted again since, or, within this
boot and PID namespace, no process has that PID with that start time. Without
`writer-entered.json` no native writer ever had the working path, so the
generator's exit is enough. A live process, a reused PID with a different
start time, a lookup error, an identity recorded on another machine, an
unidentified machine's earlier boot or another PID namespace (another
container sharing the volume) never proves an exit, and neither does a
writer that entered with no recorded identity; those generations keep the
writer-joined rule. Pruning re-checks the identities while it holds the lease.

Every new browser context sweeps its HAR directory, and a generator sweeps
the default directory (the system temporary directory) once at start. A
generator restarted inside the same PID namespace reclaims its killed
generations. A replacement container has a new PID namespace, so it
preserves them; they stay until a later boot of the same identified machine
or manual removal. The shipped images run the generator as PID 1 of its
container, so every restart is a new PID namespace: there leftovers persist
in the HAR directory (an `emptyDir` in Kubernetes survives container
restarts) until the pod is replaced, and once they reach the admission bounds
below every HAR-enabled browser VUser's initialization fails. Remove them
when no generator process runs.

## Run archive transfer and receipts

A completed, scrubbed HAR moves into the run archive when its session
closes: the session hands it to the archive sink, which copies it under the
HAR namespace lease. A distributed generator copies it into its own run
archive as `browser_har_<generation>.har`, lists it in the archive manifest
(`browser_har_artifacts`: kind `har_native`, fidelity `native`, source
generation, sequence, SHA-256 and bytes) and retires the published file from
the HAR directory only when that archive is finalized; an archive that fails
and is discarded leaves its HARs in the HAR directory under its retention. A
controller running the test itself copies it straight into the run archive
and retires the published file once that archive is finalized; a copy that
fails part-way is removed from the archive. A HAR that cannot be
retained stays in its HAR directory under its retention, is counted in
`browser_har_omitted` by reason (`artifact_limit`, `artifact_too_large`,
`run_byte_limit`, `archive_quota`, `transfer_failed`, `invalid_artifact`,
`duplicate`, `source_unavailable`, `receipt_refused`, and `safe_export` in a
safe archive export, which leaves HARs out) and marks the archive
`browser_har_incomplete`. HAR is optional evidence and never fails a run. A
run keeps at most 128 HARs and 1 GiB of them, each at most 100 MiB. Optional
evidence (HARs and interaction records together) is admitted only within one
allowance fixed at its first admission: a quarter of the run archive byte
limit or of the free space above the archive volume's floor, whichever is
smaller, so required evidence always keeps the rest. This holds on a
generator and on a controller running the test itself: when space is tight,
a HAR beyond the allowance is `archive_quota`.

At finalization the controller fetches each declared HAR from the live
generator, never from the collection cache, so a source served only from
the cache (its generator gone) has its HARs `source_unavailable`. The fetch is
charged to the run's transfer reservation, whose writer refuses to go below
the controller's free-space floor, with its own allowance (a refused HAR is
`transfer_failed`); HAR fetches share that floor with the other sources'
concurrent fetches. It checks size and SHA-256 and moves the verified file
into the run archive. Across filesystems (the shipped compose and
Kubernetes layouts keep transfers on another volume) it is copied and
checked again within one optional-evidence allowance for the whole merge
(shared with the interactions sidecar): a quarter of the merge byte budget
and of the archive volume's free space above its floor, both as they stood at
the first optional copy, so the required outputs written after it keep the
rest; a refused copy is `archive_quota`. The
archived HAR is listed in the manifest with its source identity, and the
controller records an accepted receipt in `run_evidence_receipts` (schema release 51):
run, execution generation, source identity and generation, sequence, kind,
SHA-256, bytes and state. All of this happens before the manifest is
written, so nothing is in flight when it is. Each staging attempt first
forgets an earlier attempt's accepted receipts, so a HAR that attempt held
but this one could not fetch is never published or acknowledged. Receipts are idempotent by source artifact: refetching
the same HAR after a controller restart keeps one receipt (a partial fetch
left by a crash is never receipted; it stays in the transfer directory until
cleaned up, as [archive retention](../ARCHIVE_SOURCE_RETENTION.md) describes), and different
content for the same artifact is refused. A HAR received after the run's
archive was published, after the run was deleted, or under an older
execution generation than one already recorded is tombstoned and its copy
removed; a collection retry, which runs without the execution fence
(generation 0), inherits the newest recorded generation instead. The run's
receipts are published in the same transaction that publishes its archive,
then each generator is acknowledged in the background (`AcknowledgeEvidence`,
by name and SHA-256, at most 128 HARs plus the interactions sidecar), through
a retained client or a fresh dial, since the run's clients are closed by
then; only then does it delete its copy. A changed or unknown artifact is
retained, a repeated acknowledgement is harmless, and a failed
acknowledgement leaves the copy to the generator's archive retention, as
does a tombstoned receipt, which is never acknowledged.
Deleting a run archive (including retention) or its project tombstones its
receipts. Receipt rows are kept, at most one per HAR (128 per run) and one per
source's interactions sidecar: they outlive the run so that a deleted
archive's tombstones keep refusing late uploads (a deleted run refuses them
anyway, and still records a tombstone for each). Receipt admission is a
read-then-write; under the durable execution fence it first locks the
execution lease row (on PostgreSQL), and otherwise takes no row lock. Two
stagers of one run are excluded by the execution lease (one
controller holds a run), and receipts are fenced by execution generation: an
older generation's staging attempt (a deposed controller still merging)
neither forgets nor changes a newer generation's claims, and a controller
that has lost the lease records no further receipts: under the durable
execution fence each accept checks, in its own transaction, that the
controller still holds the execution lease term it staged under. The
generator is acknowledged with an attempt per commit of the run; one that
fails (no reachable generator, an error, a retained copy) is retried by a
later commit only after 15 minutes, and a generator that no longer has the
run's archive counts as acknowledged.

The archive manifest's artifact inventory lists each HAR (format `har`,
record type `browser_har_native`) with its SHA-256, so object-store
publication and materialization carry it, and the inventory refuses a HAR
whose bytes differ from its manifest entry. The default safe archive
export omits HARs (`safe_export`); an editor's `?include=har` export keeps
them (see [Downloads and exports](#downloads-and-exports)). The transfer is
pulled by the controller, as the run's other archive artifacts are, rather
than pushed by the generator: generators host the gRPC server in both
connectivity modes. The transfer advertises no capability of its own, so
mixed versions degrade silently: a generator older than this declares no
HARs and its run archive does not record that HARs were captured, and a
controller older than this never fetches or acknowledges a newer generator's
HARs, which then remain only in that generator's own archive until its
retention. Run controllers and generators of the same release when HARs
matter.

## Synthetic HAR

A browser context recorded without native capture, or by a generator older
than native HAR transfer, still leaves retained network entries in the run
archive's transactions. A synthetic HAR 1.2 (`harsynth`, algorithm
`apexload.synthetic_har` version 1) is derived from them on request and never
stored, so archives written before it existed have one too, and the same
archive always yields the same bytes. Each native HAR names the browser
evidence generation of the context it recorded (`evidence_generation` in the
manifest's HAR entries, from the context's resource evidence); entries of
those contexts are left out of the synthetic HAR (counted as `native_har`),
so a context's requests appear in one HAR or the other. Entries whose
generation is unknown (older peers, malformed evidence, or a row whose
evidence the engine dropped at its evidence budget, which may belong to a
covered context) are kept. A native
HAR that does not name its context (one written before evidence generations
were recorded, or with a malformed one) makes the overlap unknown: the
synthetic HAR's `_apexload` block then reports
`native_har_coverage_unknown` (how many such HARs) and lists it among the
completeness reasons, since those contexts' requests may be in both.

Entries first pass the same sanitizer as the safe transaction download, then
the native HAR scrubber, so a synthetic HAR never shows more than that
download does. The writer invents nothing: an optional timing phase the
evidence lacks (`blocked`, `dns`, `connect`, `ssl`) is HAR's -1, while the
required `send`, `wait` and `receive`, which HAR 1.2 requires to be
non-negative, are 0 when unmeasured and named in the entry's
`_apexload.unavailable_fields` (`send` always, since the evidence measures
request start to response start as one phase, written as `wait`);
`startedDateTime` is the measured request start, or the transaction's
record time labelled `started_source: record_time`; `time` is the sum of the
measured phases, or the entry's duration labelled `entry_duration` and
written as the whole `wait` (`wait_is_entry_duration`) so that `time` stays
the sum of the timings, or 0 labelled `unavailable`; header and body sizes
the evidence lacks are -1, and the decoded `content.size`, never measured,
is 0 and listed as unavailable; HTTP versions, status text and cookies are
empty; request and response bodies are never written (body
policy `omitted`). A Parquet-only archive's projection keeps no headers, so
its synthetic HAR carries none and says so. The log's `_apexload` block
records fidelity `synthetic`, capture mode (`retained_network_entries`, or
`replay_network_entries` for a replay), completeness (`partial`, since
retained entries are a bounded sample of what the browser issued, or
`truncated` when the writer's own 100,000-entry or 100 MiB limit left entries
out), entry count, omitted entries by reason, omitted fields, algorithm and
schema version and body policy; each entry's `_apexload` names its action,
page, request, resource type, failure kind and where its start and time came
from.

## Downloads and exports

Only archived HARs, and HARs derived from archived evidence, are served: a
HAR directory's working, published or retired files are never reachable,
and names are matched against the archive's manifest, never used as paths.
Because native HARs embed scrubbed response content, anyone who can view a
run lists its HARs, and downloading one needs editor (or project
administrator) access to the project.

- `GET /api/v1/tests/{id}/archive/browser/har` lists the run's native HARs
  (name, fidelity `native`, source identity and generation,
  `evidence_generation`, SHA-256 and bytes) and its synthetic HAR (fidelity
  `synthetic`, derived on download; unavailable without a transaction
  source), whether native HARs were omitted and why, the role a download
  needs and whether the caller has it.
- `GET /api/v1/tests/{id}/archive/browser/har/{artifact}` downloads one. A
  native HAR is checked against its manifest SHA-256 and size first (a
  mismatch is 409); `browser_har_synthetic.har` is derived from the archive
  then (`X-APEXLoad-HAR-Completeness`). `X-APEXLoad-HAR-Fidelity` names the
  fidelity.
- `GET /api/v1/tests/{id}/archive?include=har` is the safe archive export
  with the native HARs kept in the archive inventory and manifest; viewers
  are refused (403). The default export, and library bundles, still omit
  them.
- A browser replay records a native HAR by default (`record_har: false`
  turns it off; recording sets both capture aliases to `always`, overriding
  a script's own capture policy). When the replay's browser closes, the
  controller copies the published HAR into its own directory (under the
  system temporary directory's `apexload-replay-har`, used only when it is a
  real 0700 directory the controller's user owns; a running controller
  keeps its own directory fresh and others idle for a day are removed;
  otherwise a private temporary directory that is not swept after a crash)
  and retires the original, so the HAR directory keeps nothing for replays. A replay that
  fails before producing a report retires its recording too. The copy
  belongs to that session record: it is kept only while the record holds
  its lease and integrity fence, a later replay reusing the session ID never
  sees it, and it is removed when the record is pruned, evicted or retired.
  `GET /api/v1/browser-replay/sessions/{id}/har` streams it, or a synthetic
  HAR from the replay's network entries when there is none
  (`capture_mode: replay_network_entries`), once the replay has ended (409
  while it runs). `GET /api/v1/browser-replay/sessions/{id}/evidence` is a
  streamed ZIP of the session (`report.json`), the HAR (`replay.har`) and
  what the HAR is (`har.json`, written last: fidelity, capture mode,
  completeness, entries, SHA-256 and bytes of the HAR as streamed). Both
  need editor access to the replay's project; at most four replay HAR
  downloads run at once (503 with `Retry-After` beyond that). Replay HARs
  live with the controller process that ran the replay: they are not
  durable across a restart and not reachable from another replica.

The dashboard lists a run's HARs above the retained resource inspector, with
a download per HAR and an archive download with its native HARs; each
retained resource row offers the HAR holding it (the native HAR whose
evidence generation matches the row's source generation, else the
synthetic one). The browser replay cockpit offers the replay's HAR and
evidence ZIP once a replay ends. From the command line:

```bash
apexctl analysis browser-har list --id RUN_ID
apexctl analysis browser-har download --id RUN_ID --artifact browser_har_synthetic.har --out run.har
apexctl analysis browser-har download --replay SESSION_ID --evidence --out replay-evidence.zip
```

Downloads go to a new file (an existing one is never overwritten, and a
failed download, including one cut mid-body, leaves none) or to standard
output with `--out -`. A download has ten minutes, independent of the CLI's
ordinary request timeout.

## Bounds and compatibility

| Control | Candidate limit |
| --- | --- |
| Pending capture owners in one process | 64 |
| Namespace owners in one process | 128 |
| Existing private generation entries at admission | Fewer than 128 |
| Working admission bytes | 2 GiB at the admission check |
| Retirement ledger | 128 records; 64 KiB |
| Ownership manifest, writer-entered record | 4 KiB each |
| Files in one generation | 6 |
| Ledger auxiliary entries | 128, each at most 64 KiB |
| Private staging, working-plan and runtime-retention scans | 1,024 entries |
| Completed retention | 128 artifacts; 1 GiB |
| Completed-retention error details | 16 plus a count of further failures |

The 2 GiB check measures existing staging bytes at admission. The
aggregate active-writer reservation and the memory bound are the
generator-wide evidence budget (Feature completion Wave 1, D3); the per-HAR
limiter still cannot observe all native in-memory buffering.

This implementation uses Linux/macOS owner identity checks and Go's secure
os.Root operations. Other platforms reject native HAR ownership until their
private ACL, lock and durable publication behavior is qualified; Windows stays
rejected. The coordinated local runtime acceptance host is macOS, where the
ownership, crash-reclamation and real-driver tests run; results are recorded
separately in the QA bundle. The same ownership and crash-reclamation tests
also pass as a static Linux binary in a container without browsers, so the
real-driver crash test has not run on Linux. Windows acceptance must not be
inferred from either. The filesystems and peer processes must honor the
existing descriptor-lock contract. Only scrubbed, completed HARs leave a
generator; raw working files never do.

Crash reclamation changed the on-disk generation format one way:
`writer-entered.json` and version 2 manifests with `processes`. A build from
before it rejects a HAR directory holding such a generation ("unowned file")
and cannot clean it, so do not share a HAR directory between older and newer
builds, and after a rollback remove leftover generations while no generator
runs.

Legacy transaction/resource formats and HAR import behavior are unchanged.
The deterministic frame-finalizer correction also prevents one stale native
frame's evaluation error from censoring valid live sibling receipts.
