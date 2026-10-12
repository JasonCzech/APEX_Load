# APEX Load live data tables revised implementation plan

Status: Design reviewed October 12, 2026; implemented as explicit opt-in live tables. This document preserves the design rationale. The [runtime guide](LIVE_DATA_TABLES_RUNTIME.md) records the delivered limits, configuration, and validation; the [VTS compatibility matrix](LIVE_DATA_TABLES_VTS_COMPATIBILITY.md) records intentionally blocked imports and semantic differences.

This plan revises [Live Data Tables (VTS parity) — Implementation Spec](https://claude.ai/code/artifact/3b04ebf6-e0cc-4c91-88b0-dce845c45ad6). Keep its central architecture: one authoritative table service on the execution leader, a shared engine interface, and a dedicated operation stream over the existing generator transport. Run-scoped sharing is a useful first release; existing read-only data snapshots should remain the default.

The design needs stronger contracts before implementation. Its current promises about exactly-once consumption, checkpoint recovery, memory capacity, and write-back exceed what the specified mechanisms guarantee. The revisions below make those guarantees testable and protect the controller from becoming an unbounded part of the load workload.

Repository observations refer to local commit `cd1d4a100`; the original proposal cites `6659b608b`. All performance numbers below are proposed targets or calculations, not measured capabilities.

## Changes to the original proposal and rationale

| Priority | Change | Reason |
| --- | --- | --- |
| Before implementation | Define exactly-once effects within a bounded operation-retry contract; describe leases as permitting redelivery. | A deduplicated table mutation cannot make an external HTTP side effect exactly once. |
| Before implementation | Preserve retry receipts across checkpointing, with durable rejection of retired identities. | The proposal combines a 10-minute receipt cache, a 120-second checkpoint receipt window, and recovery from the journal tail. These do not describe one consistent guarantee. |
| Before implementation | Publish only committed state in durable mode. | Waiting to acknowledge a write is insufficient if a read, lease grant, or waiter can observe it before commit. |
| Before implementation | Make table recovery part of the full adoption budget. | Current Phase A has a 15-second claim budget and the epoch keeper waits 20 seconds; the generator's 45-second grace is not the only deadline. |
| Before implementation | Separate native work queues from VTS column semantics. | Independently shifted columns have positional rows; leases need stable work-item identity. Combining both without rules can change which values belong together. |
| Before implementation | Correct VTS clear semantics and preserve absent versus empty fields. | Clearing a field sets an empty value; it does not shift subsequent fields. Clearing a row also preserves absent cells. |
| Before implementation | Add consistent seeding and an idempotent finalization workflow. | Seeding, run commit, archive publication, and pool replacement can each fail independently. |
| Before implementation | Specify an atomic write-back API and preserve excluded pool rows. | The existing replacement API expects a content fingerprint, not the decimal `rows_revision`; replacing an active-only seed can erase inactive source rows. |
| Before implementation | Bound bytes, result retention, CPU work, and controller-wide demand. | Field counts and per-run operation limits alone do not constrain total memory or protect other runs. |
| Before general availability | Add explicit disclosure rules, recovery-aware UI, and browser tests. | Runtime-produced values can be sensitive, reconnects miss updates, and current binding controls use permissions that are insufficient for pool write-back. |
| Before claiming VTS parity | Gate each imported function on semantic and control-flow evidence. | Recognizing a function name is weaker than preserving its return codes, connection state, parameter outputs, and behavior. |

OpenText documents the relevant distinctions directly: [clear message](https://admhelp.microfocus.com/vugen/en/26.1-26.3/help/function_reference/Content/FuncRef/VTS/lrFr_lrvtc_clear_message.htm) empties a cell, while [clear row](https://admhelp.microfocus.com/vugen/en/26.1-26.3/help/function_reference/Content/FuncRef/VTS/lrFr_lrvtc_clear_row.htm) leaves cells without values absent. The original proposal's claim that clearing a middle row shifts a column must be removed.

## Product scope and consistency contract

Introduce `access_mode: live` explicitly. Continue rejecting newly authored `read_write` and `consume`; preserve their existing legacy normalization to `read_only`. Do not change static snapshot allocation, sharding, or rerun behavior.

One run-scoped table has a server-generated identity shared by every admitted group and generator that binds it. An alias resolves to that identity. Two aliases do not implicitly share state because they reference the same seed pool, and two runs never share a live instance. If multiple aliases may reference one instance, make that an explicit binding relationship.

Define real-time visibility as follows: once an operation returns successfully, a subsequently admitted read of that table observes its effect or a later committed effect. Existing VUser variables remain snapshots of prior reads; they are not pushed or rewritten when another VUser changes the table. Scripts must issue another read to refresh them. Dashboard updates are bounded, eventually refreshed views and do not provide the runtime consistency guarantee.

Guarantee atomic operations and linearizability within one table while its owner is valid. Cross-table transactions, synchronized updates to every VUser's variables, offline writes, project-wide sharing, and exactly-once effects against the system under test remain outside v1.

Keep project-scoped tables as a separate later milestone. They need independent lifecycle, durability, retention, operator editing, and concurrent-run access rules; treating the existing pool editor as another uncoordinated writer would break the table contract.

## Table semantics and action contract

Use one manager and transport with two explicit semantic profiles:

| Profile | Identity and operations | Lease support |
| --- | --- | --- |
| Native work queue | Stable record IDs, fixed declared columns, row push/read/conditional update/pop, and explicit consumption outcomes. | Lease, renew, acknowledge, and release operate on a stable record and acquisition token. |
| VTS columns | Named ordered columns, 1-based positions, field presence, column pop/rotate, row and range operations, conditional replacement, increment, and column creation. | Disabled in the initial profile. Add only after defining how row identity survives independent column mutations. |

This is the largest intentional departure from the original plan. It gives producer/consumer workloads a reliable record model without silently changing imported VTS behavior. Implement a shared operation envelope and separate semantic models rather than forcing both into one representation.

Use a single DSL action type, `live_table`, with a typed operation discriminator. The original alternates between `table_*` action types and one `live_table` action; choose one contract throughout the catalog, importer, UI, and documentation.

Specify required and prohibited fields per operation. Include alias, columns, values, positions, expected value or record version, result prefix, deadline, and operation-specific empty/condition policies. Native results use the existing `var` namespace; the VTS adapter explicitly writes the parameters expected by imported scripts. Clear stale output variables on empty, skipped, or missing results so a previous iteration's data cannot leak into the next one.

Distinguish absent, empty string, and nonempty string in memory, results, checkpoints, and canonical exports. Use stable field IDs or logical offsets for indexes so head removal does not require renumbering every indexed position. Specify integer width, overflow, nonnumeric input, duplicate column names, row bounds, maximum returned bytes, and behavior after schema changes.

Iteration-start strategies are `none`, fleet-wide `sequential`, `pop`, and native `lease`. Define how sequential cursors advance after deletion and wrap; cursor movement is a mutation requiring deduplication and durable recovery. Empty producer-first tables are valid with declared schema. Support bounded `wait`; distinguish empty, canceled, table closed, unavailable, deadline expired, and exhausted-by-policy outcomes.

Waiters must never block the actor from processing producers, acknowledgments, releases, or cancellation. Serve eligible conflicting waiters fairly; a waiter for column A must not block one that can read column B. Register duplicate retries as the same pending operation. Remove canceled waiters within a bounded time and define the race between fulfillment and cancellation through the operation receipt.

## Operation identity and retry safety

Separate stable operation identity from current ownership credentials. Identify an operation using table/run incarnation, generator process or client-session incarnation, group or assignment identity, VUser identity, and an atomic sequence number. Include a canonical request digest. Controller adoption preserves the operation identity while rotating authorization credentials; generator restart or a genuinely new assignment receives a new client incarnation.

The server must return the original outcome for a duplicate accepted mutation with the same digest. Reusing an ID with a different request is an error. Cover pop, lease, renew, acknowledgment, release, increment, cursor movement, and conditional operations including definitive no-change results. An earlier failed compare-and-set must not become a successful new mutation when retried under the same ID.

Pure reads may be retried as new observations, with that behavior documented. Any operation that selects work, advances state, or promises replay of its original result needs a retained receipt. A transport frame acknowledgment is not an operation commit acknowledgment and is not a client acknowledgment that the result was received.

Define a minimum supported retry window in the negotiated contract. Reserve receipt capacity before accepting work. Maintain exact results for that window, or until a safe acknowledged-result retirement protocol permits compaction; retain sequence retirement markers so an old request cannot become new work. When space is exhausted, reject new admission instead of evicting protected outcomes. The existing authoring receipt policy already refuses expired retries instead of executing them anew.

The original 1,000,000-operation cap lasts only **50 seconds at 20,000 operations/s** for a busy table. It cannot simultaneously promise ten minutes. Results must be bounded by bytes as well as count; one million large pop responses can exceed the table's own memory many times over.

Atomic sequence allocation does not guarantee arrival order. Negotiate a bounded in-flight sequence window and maintain a contiguous retirement watermark with bounded gaps. Completing sequence 2 cannot retire an unseen sequence 1. Persist the required gaps and retirement state through checkpoints. Closed client incarnations reject further operations; cap active incarnations and retirement metadata so repeated generator replacement cannot grow this state indefinitely.

A client timeout after dispatch means the outcome may be unknown. Resolve or retry the same operation identity; do not mint a new ID and consume another row. Only a server-confirmed rejection before acceptance proves that no effect occurred. Lease expiry, explicit cancellation, and run cancellation must have defined outcomes even if the reply is lost.

## Durability and leader recovery

Keep memory mode for runs that cannot be adopted. A controller loss in that mode fails the run and can lose table contents. Reports must say unavailable when no final snapshot survived; a lifecycle metadata row is not evidence of a recoverable final table.

Require durable mode for admitted HA continuation. The service computes a bounded batch against private state, writes state transitions and exact outcomes in one fenced database transaction, then exposes the committed state. Reads, waiter fulfillment, cursor results, lease grants, and events cannot observe speculative changes. On an uncertain database commit, reconcile the batch identity before proceeding. Persist nondeterministic decisions such as generated tokens, chosen records, queue order changes, and lease deadlines so replay is deterministic.

Fence each durable change with the execution lease's owner token, monotonic generation, and expiry checked using database time in the same transaction. Apply this to journal writes, ownership claims, checkpoint publication, compaction, lease transitions, and finalization. A separate table generation is additional protection. In-memory reads and replies also require a valid serving term; rejecting stale SQL writes alone does not stop stale reads.

A complete checkpoint includes schema and profile version, committed sequence, field presence, queue order, cursors, leases and deadlines, retained outcomes and request digests, retirement markers, and bounded evidence counters. Publish a manifest identifying every chunk, size, hash, and exact sequence. Only advance the recovery pointer after all chunks are verified. Compact only covered committed journal records whose retry information remains recoverable. Restore from the checkpoint **and** journal tail, not the tail alone.

Bound compressed and decompressed bytes, batch sizes, snapshot work, and journal retention. Checkpointing must not copy the whole table inside the operation critical section without a measured pause bound. Retain the last valid recovery point until its replacement is complete.

Integrate recovery with the existing adoption state machine. Current budgets are a 15-second Phase A claim pass, a 20-second epoch-keeper wait, and a default 45-second generator adoption grace. Measure the complete workload across all active tables and runs, including ownership acquisition, loading, replay, generator claims, token rotation, stream readiness, and supervision renewal. A ten-second benchmark for one table does not establish successful fleet recovery.

For the first HA implementation, admit only a bounded aggregate recovery workload that fits these budgets with margin. If it cannot fit, revise and test the coordinated adoption lifecycle before enabling larger workloads; do not simply extend one timeout. Preserve immediate stop/abort and generator supervision during recovery. Table actions wait behind a recovery barrier. Missing or corrupt durable state fails the affected run; never reseed it and continue as if nothing happened.

## Leases and external work

A lease ensures one current table-issued claim; it cannot ensure that only one worker is still executing an external action. A paused or partitioned worker can continue after its lease expires. A late acknowledgment must fail without touching a later lease acquisition, but that cannot undo an already submitted order or payment.

Add renewal, maximum lease lifetime, owner validation, and acquisition tokens. Use an authoritative deadline policy with database time for durable transitions and bounded clock assumptions. Lease time continues during controller gaps unless an explicit, durable suspension protocol says otherwise. Recover expiry decisions before new claims, and never resurrect an expired acquisition.

For automatic iteration leases, define success independently of continue-on-error: an iteration that encountered a failed required action does not acknowledge merely because it reached its end. Acknowledge only after all parallel branches finish. Renewal may run while the VUser waits or is softly paused, but stops on cancellation, fencing, or the maximum lifetime. Explicit action leases require explicit acknowledgment and have documented cleanup on iteration exit and VUser termination.

Add a bounded delivery-attempt count and a quarantine outcome for repeatedly failing work. Otherwise returning poison records to the head can starve every other record. Applications requiring one external effect should use a stable business idempotency key supported by the target or reconciliation logic in the script.

Report `pop` as destructive consumption. After generator loss, the controller may know what was removed but not whether the target processed it. Label this processing outcome unknown; do not claim an exact list of business operations lost without completion evidence.

## Seeding and finalization

Seed once during start preparation, immediately before the coordinated start barrier. Read the pool's active rows, field schema, row revision, and relevant metadata from one consistent snapshot. Record seed digest, source identity, revision, and time. For durable runs, commit initialized table state and its continuity identity before any generator receives `CommitWork`. Retrying initialization must return that same table rather than duplicate the seed.

The fresh-run default is the current pool at start, as proposed. A repeat or revision should default to its retained original seed, with an explicit choice to use current pool data. Each gets a new table, cursor state, and lease population. If the original seed is no longer retained, report that and require explicit reseeding rather than silently changing the workload.

Define end modes as `discard` by default, `save_as_new`, or `write_back`. Finalize only after the run stops issuing new operations, outstanding effects are resolved within a bound, and lease disposition is settled. On failed or aborted runs, default to retaining authorized evidence without applying write-back; writing partial results requires a separately selected policy.

Freeze an immutable final snapshot and create an idempotent finalization job. Identify the output pool or replacement intent durably so a retry cannot create a second pool. Keep recovery material until required final snapshot publication and finalization outcomes are committed. Cleanup follows retention policy and confirmed durable publication. Run execution success and data-finalization success are separate report fields.

Add a storage primitive that atomically checks pool identity, project, archival state, row generation, and relevant metadata/schema version before applying write-back. Reuse the existing parent-lock ordering and full execution-term fencing. Do not pass `DataPoolRowsPageState`'s decimal revision to `BulkReplaceDataPoolRowsIfRevision`, whose expected token is a content fingerprint.

The default write-back policy must preserve source rows excluded from seeding. Whole-pool replacement is permitted only when the pool is explicitly eligible for that behavior and no inactive or otherwise excluded rows would be silently erased. Prefer an active-subset replacement or identity-aware native queue merge with defined new/deleted row semantics. VTS positional output needs an explicit conversion contract.

On revision conflict, keep a recoverable final snapshot and report the conflict. `save_as_new` can be an explicitly configured fallback, subject to current create permission, quotas, and archival policy; it is not an unconditional side effect. Deleted or archived pools, permission revocation, quota exhaustion, and failed audit writes produce inspectable finalization outcomes. Check permissions at configuration, start, and finalization. Two runs seeded from one revision must not both replace that revision successfully.

Preserve empty versus absent values in pool JSON when representable. CSV is a human export, not the canonical recovery or parity format; use a versioned structured artifact for lossless state. Formula-safe CSV transformations must not alter internal values or write-back data.

## Transport and controller resource protection

The controller-opened bidirectional generator RPC is a good fit for both direct and reverse connections. Keep a dedicated stream and engine-facing interface; do not use WebSocket or Redis telemetry fanout to perform authoritative mutations. Attach and validate the channel before initial commit and before any new assignment begins live operations.

Bind the channel to authenticated generator identity, admitted assignment, run incarnation, current generation, supervision token, and allowed aliases/operations. Validate ownership of lease tokens separately. A generator prefix in an untrusted string is not authorization. Generate opaque aliases for imported endpoints; do not embed hostnames or credentials in artifact paths, logs, or unrestricted labels.

Use independent reader/writer loops, a bounded number of outstanding frames, operation correlation, and jittered reconnect. Waiting for one batch's complete response must not prevent later producer or renewal requests from being transmitted. Deduplicate pending operations and define partial-batch handling. Prioritize stop/abort, heartbeats, supervision, acknowledgment, and renewal so table saturation cannot expire healthy runs. Test shared-connection congestion, including reverse tunnels.

Negotiate limits for operations and encoded bytes in both directions. The original maxima allow a **64 MiB row** (`4,096 × 16 KiB`) while capping frames at **1 MiB**. For v1, reject any atomic operation whose bounded request or result cannot fit before mutating state. Read-only ranges may paginate against a stable revision; do not split one destructive operation into independently applied fragments. A future chunked-result protocol needs its own retained-result and reconnect rules.

Budget allocated slots as well as nonempty cells. Cleared cells, sparse columns, indexes, leases, waiters, request queues, retry receipts, journal buffers, checkpoint copies, encoding overhead, and export snapshots all consume resources. Enforce per-table, per-run, per-project, and controller-wide byte budgets with reserved space for execution control and metrics. Admission and growth must account for existing reservations across concurrent runs.

Bound CPU work as well as operation count. Full-column scans, lazy index construction, wide row operations, and checkpoints can block the actor even at low operations/s. Establish maximum scanned cells/bytes and snapshot duration; use prebuilt indexes or reject costly operations when their atomic cost exceeds the supported bound. Benchmark worst-case distributions and mixed operations.

Keep 512 MiB and 20,000 operations/s as experimental configuration candidates, not capacity claims. Two million 16 KiB values alone occupy about **30.5 GiB**. At 32-byte values plus the proposal's 48-byte estimate, the field allocation is 160 MB before auxiliary state. Three such tables are not a safe admission guarantee.

Also correct journal sizing: flushing at five milliseconds **or** 512 operations does not cap a busy table at 200 inserts/s. For example, 200,000 operations/s can trigger about 391 batches/s by the operation threshold, before byte limits. Size storage by bytes/s, write-ahead-log volume, commit latency, and shared lease-row lock contention as well as insert count.

## Permissions and data handling

Use the repository's explicit `view_data_pools` and `manage_data_pools` permissions, project membership, test access, and domain feature controls. Scenario or script editing alone does not grant permission to replace a pool. Distinguish creation of a new pool from mutation of an existing one. Revocation during a run must have a defined effect on inspection and finalization.

Replace the blanket statement that values are not secrets. The existing policy for literal credentials inside imported scripts does not classify every runtime response or `{{secret.NAME}}` value as safe. Platform-managed secrets remain protected regardless of `redact_run_evidence`; imported literal credentials retain the existing configurable evidence policy.

Apply the same disclosure rules to peek, exports, debugger receipts, errors, reports, and archive artifacts. Default telemetry carries counts, bounded status codes, and revisions, with no row values or lease capabilities. Reuse no-store headers, sensitivity headers, required export auditing, and CSV formula protection. Resolve a table-level evidence policy across producing groups so a reader with weaker logging settings cannot expose another group's protected output.

Internal recovery state must preserve executable values without treating redacted evidence as a recovery snapshot. Specify encryption, key availability after adoption, access, and deletion for journal/checkpoint data. The initial release should reject insertion of known platform-managed secret material until a compatible protected recovery and publication path is implemented; do not serialize it into ordinary base64-gzip text and call that encryption. This does not promise automatic identification of all sensitive application data.

Archive row contents only under an explicit retention/disclosure policy; counts and outcomes are sufficient by default. A data-pool write-back must not become a route for publishing secret material to pool viewers.

## Dashboard and run evidence

Keep the proposed binding editor, live-table panel, debugger sandbox, and report section. Show semantic profile, seed policy, durability, scope, lease/redelivery behavior, end mode, and capacity limits. Use sentence-case labels and actual data-pool permissions. Live configuration must update scenario revision tracking and generated API types.

Validate explicit live-table actions as well as `{{data.alias.column}}` templates across init, actions, teardown, and nested control flow. Detect alias collisions with script data sources. Producer-first tables should be valid; static analysis may warn about possible starvation but must not claim it can prove all dynamic production or consumption behavior.

Publish bounded stats with table revision, sample time, and serving/recovery state. Reuse existing scoped WebSockets for notification and fetch current state on reconnect or detected gaps. Show stale, reconnecting, recovering, empty, and closed states distinctly. Do not overwrite dirty authoring forms in response to telemetry.

Peek must declare whether it is a current changing view or a revision-bound snapshot. Exports must represent one coherent revision and must not hold the actor while streaming to a slow browser. If an HA follower cannot serve authoritative rows, use an authenticated leader-routing strategy or return a clear retryable state; a load balancer must not produce unexplained intermittent failures.

Replay, debugger, shakeout, and unmanaged `apexctl` execution use isolated in-process instances with explicit seeds. They never borrow a production run's table or write back implicitly. If a mode is unsupported, fail before execution rather than silently using a static snapshot.

Record table-operation latency separately from target protocol and transaction metrics. A user-defined transaction spanning a table wait still measures its real elapsed time; report the wait as a component. Preserve failover gaps, throttling, queue starvation, unknown pop outcomes, and lease redelivery in run validity/RCA evidence. Controller interference with unrelated runs must be measured rather than assumed absent.

## VTS compatibility and delivery phases

Maintain a function-by-function matrix against the [official VTS function reference](https://admhelp.microfocus.com/vugen/en/26.1-26.3/help/function_reference/Content/FuncRef/VTS/etc/lrFr_lrvtc_Alphabetical.htm). Each entry records language, parameters, return codes, parameter outputs, side effects, verified edge cases, and import status. The reference lists 28 single-connection functions, but that count is not evidence of full compatibility.

Test ragged columns, absent/empty cells, range behavior, clear/pop/rotate interactions, numeric conversion, and the three [send-row modes](https://admhelp.microfocus.com/vugen/en/26.1-26.3/help/function_reference/Content/FuncRef/VTS/lrFr_lrvtc_send_row1.htm). Documented behavior can be implemented immediately; ambiguous behavior needs captured results from the target VTS version before receiving a verified-parity label.

Connection and index calls may be adapted only when their observable behavior is preserved. Repeated connects, dynamic endpoints, multiple handles, disconnect state, and checked return codes cannot be dropped as arbitrary no-ops. Unsupported control flow or unresolved bindings remain explicit import blockers/review items. Do not contact imported endpoints merely because they appear in a script.

| Phase | Deliverable | Exit gate |
| --- | --- | --- |
| 0 | Semantic profiles, operation/receipt state machines, timing budgets, resource prototype, VTS matrix. | Resolve guarantee contradictions and demonstrate bounded cost before building the full UI. |
| 1 | Native local queue, explicit live bindings, isolated replay, cancellation, leases, and required unit/property tests. | Local producer/consumer run reconciles; read-only behavior and dirty drafts remain intact. |
| 2 | Direct/reverse distributed transport, admission/capability checks, scoped metrics and usable dashboard. | Concurrent groups on multiple generators, reconnect, overload, partial startup, and generator-loss tests pass. |
| 3 | Durable journal/checkpoint/receipt recovery, full-term fencing, HA adoption integration. | PostgreSQL crash, partition, compaction, split-brain, and aggregate recovery-budget gates pass. Continuation remains disabled until then. |
| 4 | Idempotent finalization, protected artifacts, write-back, proven VTS mappings, report renderers, documentation. | Authorization/conflict/finalization faults and claimed parity cases pass; measured capacity and browser gates pass. |
| Later | Project-scoped tables, governed operator mutations, Java/JavaScript adapters, distributed rendezvous. | Separate contracts and acceptance evidence for each capability. |

Implement the local in-memory service behind a capability flag so early phases can be exercised without advertising HA or VTS completeness. Design durable receipts and finalization identities in Phase 0 even where their implementation lands later. Run small performance probes in every phase; a late benchmark cannot cheaply repair the wrong data structure or recovery design.

Allocate migration numbers and protobuf field numbers against the branch at implementation time. At this review's commit, schema release 56 and WorkAssignment field 47 are current, so the proposed next numbers are plausible, not reserved. Add `pkg/livetable` to AGENTS.md's package inventory when created. Generate DSL, OpenAPI, and TypeScript contracts through repository tooling, and add the plan to the documentation index.

## Acceptance tests and release evidence

The release gate needs more than a successful happy-path producer/consumer run:

| Area | Required evidence |
| --- | --- |
| Table semantics | Table-driven edge cases and concurrent histories checked against a pure model for each semantic profile. |
| Retry safety | Lost reply after commit, repeated conditional failure, duplicate pending wait, changed payload under same ID, sequence 2 completing before sequence 1 arrives, bounded generator-incarnation churn, and retries after both checkpoint compaction and receipt retirement. |
| Committed visibility | Crash after speculative apply but before commit; no successful read, waiter, event, or lease observes state that disappears. |
| Ownership | Old leader resumes before and after takeover; expired term, stale read, stale checkpoint, stale cleanup, and stale finalization all fail closed. |
| Leases | Expire/reacquire/late acknowledgment, renewal loss, long pause, cancellation, parallel branches, poison work, and target success followed by worker crash before acknowledgment. |
| Startup and recovery | Seed read racing with pool edits, crash during initialization, partial CommitWork, direct/reverse reconnect, multiple failed leaders, corrupt checkpoint, and total restoration beyond the allowed budget. |
| Finalization | Crash before/after pool replacement and artifact publication, repeated save-as-new retry, concurrent runs, archived/deleted pool, inactive rows, permission revocation, and quota/audit failure. |
| Resource isolation | Max-width results, sparse allocations, large receipts, slow exports, costly scans, many runs/tables, and saturation while stop/abort and supervision remain responsive. |
| Disclosure | Managed-secret insertion policy, cross-project access, forged lease ownership, raw export auditing, formula-safe CSV, archive retention, and no values/tokens in generic telemetry. |
| Browser behavior | Both impacted auth profiles: editing and permissions, empty table/wait, reconnect and stale state, recovery, revision-bound peek/export, conflicts, dirty forms, and debugger isolation. |

For a controlled queue scenario with unique record IDs and no arbitrary updates, maintain the conservation equation:

`seeded + successfully pushed = available + currently leased + acknowledged + destructively popped + quarantined + explicitly discarded`

Each record belongs to exactly one category at an observed committed revision. Redelivery changes attempts, not the record population. Once finalization releases outstanding leases, the currently leased category is zero. Separately count external processing attempts and target-side idempotency results; table reconciliation alone cannot prove exactly-once business processing.

Keep proposed latency and throughput targets from the original as benchmark hypotheses. Publish hardware, topology, payload distribution, profile, durability, active table count, operation mix, receipt retention, GC/memory, database cost, and impact on target traffic. Benchmark recovery under the largest admitted aggregate workload, not just the largest field count.

Follow the repository's testing policy: affected Go packages during development; the full `go test -timeout=20m ./...` suite for this cross-package feature before merge; dashboard typecheck, lint, relevant unit tests and affected Playwright workflows; `npm run test:cycle` for the broad/shared UI change. Regenerate contracts and run documentation checks. Clean build caches only once all dependent tests, agents, and follow-up fixes have finished.

## Repository evidence

| Existing behavior | Source |
| --- | --- |
| Explicit read-only bindings and legacy normalization | [scenario.go](../pkg/api/scenario.go), `normalizeScenarioDataPoolBindings`, around line 644. |
| Immutable revision seeds and active-row CSV construction | [testcreation.go](../pkg/api/testcreation.go), around lines 1387 and 3111. |
| DataProvider is created per group and shares its counters with that group's VUsers | [group.go](../pkg/engine/group.go), around line 406; [data.go](../pkg/engine/data.go). This narrows the original's blanket fleet-wide exactly-once description of existing static pop. |
| Adoption budgets and sequencing | [run_adoption.go](../pkg/api/run_adoption.go), constants around line 40; [distributed_execution_backend.go](../pkg/api/distributed_execution_backend.go), start sequence around line 1604. |
| Full execution-term fence using database time | [sqlite_tests.go](../pkg/storage/sqlite_tests.go), `schedulerLeaseTermHeldInTransaction`, around line 1483. |
| Distinct paging generation and bulk-replacement fingerprint | [sqlite_datapools.go](../pkg/storage/sqlite_datapools.go), around lines 579 and 640. |
| Refusal of expired idempotency keys | [authoring_mutation_receipts.go](../pkg/storage/authoring_mutation_receipts.go), retention contract around line 55. |
| Existing bidirectional RPC and assignment capabilities | [generator.proto](../api/proto/generator.proto), StreamMetrics and WorkAssignment. |
| Current binding editor and scope of permission input | [ScenarioDataBindings.tsx](../dashboard/src/components/ScenarioDataBindings.tsx); [TestHub.tsx](../dashboard/src/components/TestHub.tsx), around line 4572. |
| Export sensitivity and required audit | [handlers_data_pools.go](../pkg/api/handlers_data_pools.go), `handleExportDataPoolRows`, around line 1515. |
| Managed secrets and imported literal credential policy | [Application user guide](APPLICATION_USER_GUIDE.md#secrets); [script types](../dashboard/src/types/scripts.ts), LogConfig. |
