# Native HAR working-file ownership

This guide describes the native HAR working-file safety implementation.
It does not mark the complete browser evidence program or native archive
transfer/export complete.

The existing browser HAR capture policies remain opt-in: off, always, on_error,
on_sla_breach, and manual. The browser_har_dir setting selects an existing
artifact directory; a symlink at that directory is rejected. The current
runtime-secret suppression, embedded-body policy and individual HAR size limit
remain in effect.

## Working and completed files

Before admitting a native context, capture setup creates a fresh unpredictable
generation below .apexload-har-work/. The private staging directories use 0700;
raw working.har, manifests, control files and scrubbed output use 0600. On
Windows the staging directory instead gets a protected DACL granting only the
user and SYSTEM, which everything below it inherits (see [Windows](#windows)).
The raw file is durably reserved before the pinned local SDK truncates/writes it,
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
the first owner’s native acknowledgment; it does not alter that owner’s result (see `third_party/playwright-go/APEX_PATCHES.md`). Initialization errors, oversized capture
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

The two-second caller wait is separate from the shared **30-second capture
finalization deadline**. The first teardown document join, runtime join or
finish starts that deadline once; retries and later phases cannot renew it. A
mid-session resource-limit close does not start it, so a VUser that keeps
running after an overflow still has the full budget at teardown. Close,
flush, scrubbing and durable publication must qualify within it. Expiration
returns `browser HAR finalization budget exhausted`, emits the
`har_finalization_budget_exhausted` reason and increments the generator-wide
`har_finalization_exhausted` evidence counter once per capture, including when
a native writer remains blocked. It does not forcibly terminate a callback or
turn a timeout into writer-death proof. An in-progress read, scrub or native
close keeps its owner until it actually returns.

Expired capture output is not published. A deadline crossed after the final
link is created removes only that owner's exact link; a foreign replacement
is preserved and reported as unproven. Once every writer and worker has joined,
raw retirement can still proceed under its separately bounded namespace wait,
even when capture publication was refused. An artifact already qualified and
published within the deadline stays valid during later retirement or retries.
These finalization guards have local deterministic qualification, and native Linux and
Windows crash qualification are verified in CI.

### Outstanding request limit

An active native HAR capture allows **1,024 outstanding issued requests**. The
synchronous native request callback counts issued work, including browser-queued
requests, independently of the bounded diagnostics queue. Completion releases
its count once. More than 1,024 outstanding requests signals the existing native
close owner, records `har_in_flight` as a resource failure and marks unfinished
document observations censored. Issued counts and incomplete evidence remain
visible while closure joins the writer. This is overflow-triggered context
closure; it does not schedule target requests or guarantee that no additional
native callback arrives before closure completes.

Completed sequential requests do not consume the concurrent budget. The
512-entry request-identity inventory and 100,000-entry capture limit remain
separate bounds; a page with more than 512 outstanding requests loses
per-request identities, and its evidence is marked incomplete, before the
outstanding-request limit closes the context. Sessions without active HAR capture retain their existing
resource limits. No new request routing is installed and HTTP cache behavior
is unchanged. Cancellation racing overflow uses the same exact close owner;
a caller timeout retains the working file, lease and charge until physical join.
Local Chromium and WebKit flood, cancellation and import-compatibility
tests verify these bounds.

After successful join, scrubbed output is written and synced in private staging,
then linked to a fresh flat apexload_browser_<generation>_scrubbed.har name
without replacing an existing artifact. The final name never exposes an
in-progress copy. Flat completed files remain discoverable by controller HAR
retention. Publication and durability failures return errors.

## Interrupted retirement

A private, versioned retirements.json ledger records a completed artifact
or proven writer-joined generation before rename or unlink. Its identity
contains the original device, inode and owner UID (on Windows the volume
serial number, the 128-bit file ID and the owner SID). The shared namespace
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
there. Windows identities are described under [Windows](#windows). A writer
record is completed before the path is handed out: one that cannot be written
leaves no writer entered (and is removed), and one a crash left half-written
counts as no writer.

Cleanup reclaims such a generation through the same retirement ledger only
when its owner lease is free and every recorded process provably no longer
exists: the same identified machine has booted again since, or, within this
boot, on the same machine identity and in the same PID namespace, no live
process has that PID with that start time. A boot value two hosts share (the
Windows boot counter, VMs cloned from a memory snapshot) therefore never lets
one host's process table answer for another's. On Linux the PID namespace
includes the time namespace when the kernel has one, because process start
times are counted in it, and a well-formed `/proc/<pid>/stat` record in zombie
`Z` or dead `X`/`x` state has already exited the kernel and closed its
descriptors, so an orphaned Playwright driver reparented to a PID-1 generator
container without `--init` counts as exited once its stdin-EOF flush
finishes. Without
`writer-entered.json` no native writer ever had the working path, so the
generator's exit is enough. A live process, a reused PID with a different
start time, a lookup error, an identity recorded on another machine, an
unidentified machine's earlier boot or another PID namespace (another
container sharing the volume) never proves an exit, and neither does a
writer that entered with no recorded identity; those generations keep the
writer-joined rule. On Linux a missing `/proc/<pid>` entry proves an exit only
when `kill(pid, 0)` also reports no such process, because `hidepid` or
`ProtectProc` hides other users' live processes; a `/proc` that does not
describe the process's own PID namespace leaves every identity undecidable.
Identities recorded by an earlier binary hold the PID namespace alone; on a
kernel with time namespaces they no longer match within the same boot, so
their generations stay until the machine reboots or they are removed by hand,
never reclaimed early.
Pruning re-checks the identities while it holds the lease. The same identity
and exit proof (`pkg/internal/procidentity`) also retires native analytics
writers whose controller exited.

Every new browser context sweeps its HAR directory, and a generator sweeps
the default directory (the system temporary directory) once at start. Within
the same PID namespace, `procidentity.Gone` is always mandatory so a live
orphaned Playwright writer is never reclaimed early. On Unix, when the HAR
working directory lives on a verified local filesystem (`pkg/internal/localfs`)
and the Playwright Node driver inherits the open `owner.lock` file description
across `exec` (`setHARLeaseInheritable`), `manifest.json` and
`writer-entered.json` are written as version 3: the kernel holds the exclusive
`flock` lease until both the Go generator and the inherited Node writer tree
have closed all descriptors and exited, so a replacement container in a new PID
namespace on the same kernel boot and machine identity reclaims predecessor
version-3 generations once that lease is free. Version 1/2 manifests,
non-local mounts (NFS, SMB, EFS, FUSE), foreign machine/boot identities, and
Windows containers (`LockFileEx` is process-scoped) remain preserved under the
fail-safe rule until a later boot of the same identified machine or manual
removal when no generator process runs.

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

Capture losses that occur before an artifact exists also mark the run archive
incomplete. Local and distributed archive sinks retain one omission per capture
owner: `native_capture_failed` for joined finalization/publication failures,
`native_finalization_budget` for deadline exhaustion, or
`native_owner_unretired` when ownership has not retired. Unretired ownership
can mean unfinished writers or a cleanup/identity-fence failure after join;
the count does not claim which physical step is still pending. A full published
artifact queue reports `native_publication_limit` and keeps the owned file
under its existing retention policy. An invalid or panicking status provider
reports `native_status_unavailable`, without exposing its error details.
Per-source counts saturate at 131,072 and set `native_omission_count_limit` to
one when saturated. These counts survive generator archive transfer and appear
in the existing HAR listing. Taking the counts does not release a writer,
change cleanup authority, or turn optional evidence loss into a failed run.
Capture policies that intentionally discard a HAR do not report capture loss.
Replay durability and its fallback disclosure remain separate below.

At finalization the controller fetches each declared HAR from the collection
source cache when `optional_receipt.json` retained it within the collection
cache's byte and disk-space bounds, falling back to the live generator when
reachable; a HAR from a lost generator that was not cached in `_collection` is
counted as `source_unavailable`. Optional HARs and raw interactions are written
into `_collection` as a best-effort pass after required source files succeed,
recording optional digests in `optional_receipt.json` so the SQL
`ReceiptSHA256` anchor for `receipt.json` never diverges across HA pods with
different free disk space. The fetch is
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
connectivity modes. Generators on platforms where native HAR capture is
qualified (`browser.NativeHARSupported()`) advertise `browser_native_har_v1`;
unsupported platforms omit it. When a browser script requests HAR capture
(`always`, `on_error`, `on_sla_breach`, or `manual`, after resolving
`har_capture_policy` over `har_capture`), the controller requires
`browser_native_har_v1` at placement, pre-assignment, `AssignWork`, and shard
evacuation, refusing older or unsupported-platform peers before any session
preflight or target HTTP request is sent. Scripts with HAR capture `off` do not
require `browser_native_har_v1` and continue to run across mixed peers. A
controller older than native HAR transfer still neither requires
`browser_native_har_v1` nor fetches or acknowledges a newer generator's HARs,
which then remain only in that generator's own archive until its retention; run
controllers of at least this release when HARs matter.

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
- A browser replay started through the browser replay API
  (`POST /api/v1/scripts/browser-replay/sessions` or
  `/api/v1/scripts/{id}/browser-replay/sessions`) records a native HAR when
  asked with `record_har: true`; recording sets both capture aliases to
  `always`, overriding a script's own capture policy. It is opt-in because a
  recording runs under the same limits as a load run's: past 100 MiB,
  100,000 entries, or 1,024 outstanding issued requests, or when the memory
  watchdog picks it, the browser context is closed and the replay fails, and an
  owner that cannot be created fails the replay's start. Where native HAR
  capture is not supported (see the platform paragraph below), `record_har: true`
  is refused with 422 and the replay does not start. Without it a replay records only when the script's own
  capture policy does (and serves that HAR as its native one); otherwise it
  serves the synthetic HAR built from its network entries. The Script
  Editor's debugger runs replays as script debug sessions, which retire any
  native HAR recorded by the script's own capture policy when the session
  ends and instead export a synthetic HAR 1.2 of retained session traffic via
  `GET /api/v1/script-debug/sessions/{session_id}/export` with `?format=har`
  governed by `runtime_profile.har_options`. Classic replays (`POST /api/v1/scripts/replay` and
  `/api/v1/scripts/{id}/replay`) offer none either and retire such a HAR
  too. When the replay's browser closes, the
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
  while it runs, and 409 with `Retry-After` while a stopped replay is still
  closing its browser). `GET /api/v1/browser-replay/sessions/{id}/evidence` is a
  streamed ZIP of the session (`report.json`), the HAR (`replay.har`) and
  what the HAR is (`har.json`, written last: fidelity, capture mode,
  completeness, entries, SHA-256 and bytes of the HAR as streamed). Both
  need editor access to the replay's project; at most four replay HAR
  downloads run at once (503 with `Retry-After` beyond that).

  Controllers backed by SQLite or PostgreSQL also publish a joined native HAR
  and its terminal report through the configured archive backend. Publication
  reserves an immutable generation before writing and keeps downloads pending
  until it resolves. Published status and downloads can recover after a restart
  with the same database and artifact source; another replica needs that same
  source, not merely an identical filesystem path on an independent disk.
  This does not qualify multi-host shared-storage behavior. Local-only bridges
  and stores without the receipt capability retain their process-local lifetime.

  `X-APEXLoad-Replay-Durability` distinguishes `local_only`, `pending`, `durable`
  and `unavailable` independently of replay success. A published native receipt
  never falls back to synthetic evidence if its bytes become unavailable:
  downloads return 503. Status polling verifies only the bounded terminal report
  and does not claim that the HAR is currently readable. Downloads verify both
  complete objects and recheck current project/receipt authority before serving.
  Current project authorization still applies after restart; viewers may inspect
  status, while native HAR and evidence ZIP downloads require editor access.

  Native retention is at most two hours, bounded by the terminal lease. Receipt
  admission includes staging and cleanup-pending generations: 250 receipts and
  8 GiB globally, 100 receipts and 2 GiB per project. Each controller admits two
  publications and four retained reads at once. Cache eviction does not free a
  durable replay ID. Cleanup releases its reservation only after deleting the
  exact joined generation; expired or ambiguous writers are never reclaimed
  merely because their process disappeared. Failed publication can leave a
  local copy available for its existing lifetime, without claiming durability.

The dashboard lists a run's HARs above the retained resource inspector, with
a download per HAR and an archive download with its native HARs; each
retained resource row offers the HAR holding it (the native HAR whose
evidence generation matches the row's source generation, else the
synthetic one). The recorder's replay verification, and the replay
validation of a recorder-export import, offer a finished replay's HAR and
evidence ZIP (not for a replay run through a paired recording agent, which
the controller does not hold); the enterprise archive (LoadRunner and
TruClient) import's replay validation does not offer them yet.
From the command line (a download of a replay that was just stopped waits
while the replay closes, honouring `Retry-After`, for at most ten requests):

```bash
apexctl analysis browser-har list --id RUN_ID
apexctl analysis browser-har download --id RUN_ID --artifact browser_har_synthetic.har --out run.har
apexctl analysis browser-har download --replay SESSION_ID --evidence --out replay-evidence.zip
```

Downloads go to a new file (an existing one is never overwritten, and a
failed download, including one cut mid-body, leaves none) or to standard
output with `--out -`. A download has ten minutes, independent of the CLI's
ordinary request timeout.

## Windows

Native HAR ownership on Windows uses the same files, ledger and order as on
Linux and macOS, built on Windows' own privacy, identity and namespace
operations (`pkg/internal/winfs`).

**Volume.** The HAR directory must be on a local fixed NTFS volume. UNC paths
(`\\server\share`, `\\?\UNC\`), device paths, network and removable drives,
and ReFS or FAT volumes are refused before anything is created or read
there.

**Private staging.** `.apexload-har-work` is created relative to the HAR
directory's handle with the security descriptor
`D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;<user SID>)` applied by the create itself, so
it never exists with an inherited ACL. Generations, working files, control
files and scrubbed output inherit that DACL. Every staging object is checked
through the handle that opened it:

- Its owner is the process token's user or the token's default owner.
- Its DACL holds only allow entries for that user and SYSTEM. A missing or
  NULL DACL, a deny entry, or an entry for any other SID is refused.
- It is not a reparse point, so symbolic links and junctions are refused.
- A file has exactly one link.

A name component containing `:` (an alternate data stream), `/` or `\` is
refused. The HAR directory itself keeps its own ACL. A published HAR is a hard
link of a private staging file, so it keeps the private DACL.

**Elevation.** An elevated administrator's token can name the Administrators
group as its default owner, so the staging it creates is owned by that group.
A non-elevated generator running as the same user does not accept that owner
and refuses the staging. Do not alternate elevated and non-elevated
generators on one HAR directory. On GitHub-hosted runners the token is
elevated and its default owner is the user, so the case does not arise there.

**Identity.** A retirement record binds the volume serial number (`device`),
the 128-bit file ID from `FILE_ID_INFO` (`inode` holds its low 64 bits and
`file_id_high` the high 64) and the owner SID (`owner`), with `uid` 0. The
identity is read through a handle to the name. On Windows `os.Lstat` reads
the file ID lazily, when it is compared, from whatever the path names by
then, so the before-and-after checks on the HAR directory stat through
handles instead.

**Retirement order.** On every platform, retirement closes the owner lease and
the generation's root before renaming the generation to its tombstone, then
reopens the tombstone to unlink its files. Windows requires this order,
because it refuses to rename a directory while any handle to a child is open.

- Retirement renames and deletes go through handles with POSIX semantics. A
  rename never replaces an existing name. A deleted name disappears at once,
  even while a share-all reader such as an indexing or antivirus scan still
  holds the file.
- A name another program left delete-pending counts as removed.
- Renames and removals that hit a sharing, lock or delete-pending conflict are
  retried every 25 ms for up to 2 seconds. Two more errors are retried the
  same way: `ERROR_ACCESS_DENIED` on the tombstone rename (a scan holding one
  of the generation's files causes it) and `ERROR_DIR_NOT_EMPTY` on directory
  removal (a delete-pending child causes it).
- Past that bound the step fails and the generation is preserved. Its ledger
  record lets a later sweep resume it.

Directory entries are made durable with `FlushFileBuffers` on a write-access
reopen of the directory.

**Crash reclamation.** A Windows process identity is made of:

- the machine: `MachineGuid` (under `HKLM\SOFTWARE\Microsoft\Cryptography`)
  paired with the physical DNS host name;
- the boot: the kernel's `BootId`, which increments at every boot;
- the namespace `windows`, the PID and the process creation time.

A process is proven gone when `OpenProcess` finds no such process, or the
process has exited. An exited process whose handle someone still holds still
answers `OpenProcess`, and counts as exited. Machines cloned from one image
can share a `MachineGuid`, so, as on Linux, hosts sharing a HAR directory
must differ in host name. Because `BootId` is a small counter other machines
share, the identity requires both `MachineGuid` and the host name: without
either no identity is recorded and crash reclamation is disabled, and even
within one boot value a recorded process is judged only on its own machine.

Inside a Windows container (`ContainerType` set under
`HKLM\SYSTEM\CurrentControlSet\Control`) the process IDs and the boot belong to
the container. No identity is recorded there and crash reclamation is
disabled: a killed generator's generations are preserved, like a writer with
no recorded identity, until they are removed by hand.

**Antivirus.** The retry bound covers brief scans. GitHub-hosted runners have
Microsoft Defender real-time protection off, so behavior under on-access
scanning has not been measured. A scanner that holds a staging file for more
than 2 seconds fails retirement and leaves the generation for a later sweep.
If that recurs, exclude the HAR directory from on-access scanning.

**Replay store.** The managed browser-replay HAR root in `%TEMP%` is created
the same way, with the private DACL applied by the create, and is proven
private before use. A root with any other ACL is not adopted, for example one
an older build made with the temporary directory's inherited ACL. The store
then uses a fresh private directory of its own, without the idle sweep.

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

This implementation uses Go's secure os.Root operations with each platform's
owner identity checks: Linux/macOS owner UIDs and modes, and on Windows
security descriptors and handle file IDs.
Other platforms reject native HAR ownership until their private ACL, lock and
durable publication behavior is qualified. `browser.NativeHARSupported()`
gives each platform's answer. Where it is false, a HAR-recording browser
session runs without a native HAR and reports
`browser_har_unavailable_har_platform_unsupported` once per context, as an
exhausted HAR budget does; nothing creates, cleans or reads a HAR directory's
staging (published-artifact reads fail with "native HAR capture is not
supported"); and a browser replay asked for `record_har: true` is refused
with 422 before it starts. The coordinated local runtime acceptance host is
macOS, where the ownership, crash-reclamation and real-driver tests run. CI runs the same suites,
real-driver crash test included, on Linux (with Chromium and
WebKit) and on Windows. The
filesystems and peer processes must honor the existing descriptor-lock
contract. Only scrubbed, completed HARs leave a generator; raw working files
never do.

Crash reclamation changed the on-disk generation format:
`writer-entered.json` and version 2/3 manifests with `processes` (version 3
denotes an inherited local-filesystem Unix lease covering the writer tree). A
build from before it rejects a HAR directory holding such a generation
("unowned file") and cannot clean it, so do not share a HAR directory between
older and newer builds, and after a rollback remove leftover generations while
no generator runs.

Legacy transaction/resource formats and HAR import behavior are unchanged.
The deterministic frame-finalizer correction also prevents one stale native
frame's evaluation error from censoring valid live sibling receipts.
