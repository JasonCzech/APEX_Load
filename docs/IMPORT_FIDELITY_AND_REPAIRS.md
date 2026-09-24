# Import Fidelity and Repairs (JMeter and LoadRunner)

Status: Current. Behavior reference for imported JMeter plans and LoadRunner archives: fidelity semantics, source-plan execution, and protected guided repair.

Source note: consolidated 2026-09-24 from the former `JMETER_IMPORT_FIDELITY.md`, `JMETER_SOURCE_PLAN_REPAIRS.md` and `LOADRUNNER_IMPORT_REPAIR.md`. Messaging plugin imports are covered separately in [JMETER_MESSAGING_FIDELITY.md](JMETER_MESSAGING_FIDELITY.md).

Contents: [JMeter import fidelity](#jmeter-import-fidelity) · [JMeter source-plan execution and batch repairs](#jmeter-source-plan-execution-and-reviewed-batch-repairs) · [LoadRunner import fidelity and guided repair](#loadrunner-import-fidelity-and-guided-repair)

---

## JMeter import fidelity

Reimport existing plans to receive these changes. Saved imports are not rewritten, and importing never modifies the JMX source.

The importer retains the first effective endpoint independently of header, timer, assertion, and variable scopes. Requests to other hosts remain absolute. An explicit base-URL override retains precedence.

Finite, unscheduled Thread Groups produce `termination_mode: "iterations"`, no duration, and positive `config.run_logic.iterations`. The supplied split-pay fixture preserves 50 users, a one-second ramp, and 200 iterations per user. Normal completion means 10,000 sampler executions. Each user retires after its own count. Duration mode remains the default for API requests that omit termination mode and requires a positive duration.

Iteration mode uses fixed closed-loop populations. Scaling, duration adjustment, traffic profiles, timeslots, and recurring schedules are unavailable. Pause/resume and cancellation remain supported. Generator admission requires `iteration_termination_v1`; loss of a generator produces incomplete work rather than replaying its work on another generator. Duration-based JMeter schedulers retain their authored duration and finite loop limits; unresolved and incompatible completion semantics stay blocked.

In timestamp review, choose either UTC normalization (one timestamp with milliseconds) or the source's IANA timezone. The source option preserves wall-clock fields and explicitly acknowledges a literal `Z` suffix. Supported Java formats become formatted, per-occurrence parameters. Dynamic formats, unsupported patterns, and unsupported `__timeShift` calls require repair. Choices are recorded in import provenance and applied by server-side regeneration.

Variable review lists declared names and unresolved references. Enter the declared name to bind, or an explicit `{{secret.NAME}}` reference. The before/after preview shows the proposed rewrite; inspecting again creates a newly validated, attested draft. Asterisk masks and disclosure placeholders are unavailable credential material. Correcting credentials does not clear timestamp or unsupported-processor gates.

JSON extractors preserve missing versus explicitly empty defaults. `on_miss: "default"` with `default_value` replaces a previous extraction on a miss. The default does not suppress malformed expressions or resource-limit failures. This extractor mode is supported by HTTP-family protocols.

### Native CSV results

A complete match of the supported Groovy trace writer becomes `config.results.csv_exports`. The selected variable must be extracted before the writer, without a later enabled overwrite; other processor orderings retain a review gate. The declaration identifies a source action, a filename, and ordered columns selecting extraction values or `latency_ms`. Arbitrary Groovy execution and source-machine filesystem writes are unsupported.

`traceId-results.csv` is a per-run archive download with `traceId,latencyMs` columns. Rows come from finalized action samples, including failures, and exclude synthetic transaction records. Empty trace values use the writer's fallback. Latency is integer milliseconds to the first response byte; connection failures without a response byte report zero.

Exports use complete local archives or deterministically merged generator archives, not sampled live metrics. Archive authorization, retention, redaction, and integrity controls apply. Missing shards, retention truncation, scan limits, count mismatches, and write failures produce an incomplete export instead of a downloadable partial file. Export definitions are frozen in run provenance. Finalized samples bind to export IDs before action-name redaction, so redaction cannot change which samples belong to a CSV.

Sampler totals and source-behavior classifications are separate. Disabled elements are ignored, informational listeners do not block replay, and enabled unsupported processors retain durable gates after unrelated repairs. Source links identify the contributing assertions, extractors, timers, and postprocessors.

The sanitized original and repaired regression sources are under `pkg/convert/jmeter/testdata`. The repaired fixture uses fake credentials and still requires an explicit timestamp policy; execution tests use loopback services.

### Regression coverage

| Original finding | Regression evidence |
| --- | --- |
| Scoped endpoint loss and explicit override | `pkg/convert/jmeter/fidelity_test.go`: split-pay, nested scopes, multiple hosts, and override cases |
| Invalid timestamps and ambiguous literal Z | Converter format/repair tests and `pkg/engine/parameters_test.go`: frozen midnight and timezone cases |
| Lost 200-iteration limit | `pkg/engine/iteration_termination_test.go` and `pkg/distributed/generatorrpc/iteration_termination_test.go`: exactly 10,000 loopback samples, pause/resume, cancellation, and group completion |
| Omitted Groovy CSV writer | Converter full-pattern rejection tests; `pkg/api/jmeter_fidelity_execution_test.go`: finalized samples, failures, deterministic shard merging, escaping, redaction, and incomplete exports |
| Missing extractor default and stale trace ID | `pkg/protocol/http/extractor_default_test.go`: present/missing/present, empty defaults, invalid responses, bounds, redirects, and connection failure |
| Contradictory accounting and inaccurate source links | Converter source mappings, disabled elements, and durable reason-code regressions; import contract registry tests |
| Unresolved source variable names and masked credentials | Converter binding tests and `dashboard/e2e/workflow-jmeter-fidelity.spec.ts`: explicit review regeneration and retained blockers |
| Persistence and rolling upgrades | SQLite/PostgreSQL termination migrations, scenario admission/persistence tests, and legacy generator capability rejection |

The browser workflow runs in both dev-admin and local-auth profiles. It imports the original fixture, regenerates explicit repairs, saves a small repaired iteration scenario, executes six loopback samples, and downloads the seven-line CSV through the results UI. The source endpoint is never used by these tests.

For messaging plugin imports, see [Kafka / Event Hubs draft fidelity and remaining conformance work](JMETER_MESSAGING_FIDELITY.md).

### Review evidence

New JMeter imports and validation responses include a `review_summary` with distinct source-behavior counts: `blocking_behaviors`, nonblocking `review_behaviors`, `unsupported_behaviors`, `warnings`, and `informational`. Unsupported behaviors are a subset of the affected behaviors, not another count to add to the review total. Sampler/action totals and legacy classification statistics remain separate.

The `diagnostics` array contains typed severity, stable reason codes, source references, and actionable details; an import without findings returns an empty array. Review sections and compatibility evidence use the same normalized findings. `unsupported_elements` and compatibility `unsupported_fragments` contain only enabled source behaviors lacking a faithful implementation. Translated CSV exports, reporting listeners, ordinary warnings, and repairable timezone or credential configuration problems do not belong in these lists.

The dashboard counts blocking behaviors and explicit review decisions separately from warning and informational findings. Marking an item reviewed or editing scenario settings does not remove a server-authored gate. Reinspect with explicit repair choices to regenerate the evidence. Existing saved imports are unchanged; reimport them to obtain the normalized evidence. No storage migration is required.

---

## JMeter source-plan execution and reviewed batch repairs

Messaging imports retain three native drafts and their concurrent scenario. A
converted or saved draft is not necessarily runnable. Strict messaging replay
remains gated by `jmeter_messaging_v1`, which generators do not advertise until
all protocol and live-conformance requirements pass.

### One generator, one source-engine session

The controller sends an immutable version-1 source-plan manifest with every
assignment. It contains the project, source plan, execution generation, sorted
group membership, source-group identities, script hashes, fixed populations,
and the `source_plan_execution_v1` capability requirement. Commit binds to the
manifest digest. The controller rejects generators missing this capability.

The generator reserves the complete population while staging. It constructs
one `MultiGroupEngine` for all groups at commit; missing or inconsistent groups
cannot start. Failed staging/commit tears down the staged plan. Identical retries
are idempotent, and committed membership cannot grow. Per-group sinks, redaction,
secret resolvers, VUser coordinates and archive attribution remain separate.
Counters, random generators, timer gates and Kafka producer sessions share the
engine lifetime. Producers close after the complete engine drains.

All groups use the commit epoch and retain their authored ramps and schedules.
Pause freezes the shared clock without catch-up; interrupted comparisons remain
outside uninterrupted JMeter parity. Scaling and duration changes are rejected.
Generator loss fails the plan: there is no migration, state reset or message replay.
This grouping capability alone is not a strict-parity certification.

### Protected repair workflow

Batch repairs require the existing operator-managed protected import store.
Imports without that store retain the legacy workflow; configuring protected
storage and reimporting creates a repairable batch. Older previews are not
reconstructed from client-submitted drafts.

The encrypted parent retains original source documents and their digest, exact
scripts, scenario, source identities and reviewed operation history. Parent and
child candidates share an expiry. Public previews, diffs and receipts contain
credential-safe projections. Original JMX bytes never change.

The existing repair preview/apply endpoints accept the batch variant:

- `batch_id`, `revision`, `project_id`, and 1–32 typed `operations`.
- `preview_digest` on apply, matching the reviewed operation set and revision.
- Source-group and source-node targets for group/sampler-specific operations.

Supported operations confirm UTC/no property overrides, normalize supported
legacy OAuth option names, bind Kafka and separate Event Hubs secret references,
replace a selected timestamp with one UTC evaluation, add an Event Hubs
application property (including an explicit UUID choice), and parameterize a
selected JSON payload field. Message indices preserve sampled batch boundaries.
Credential binding takes secret names, never secret values. Normalizing OAuth
option names does not provision secrets; bind the standard client ID and
client-secret references separately. Conflicting or
unsupported JAAS options remain blocked; uploaded Java is never executed.

Preview publishes no new revision. Apply prepares replacement candidates and
publishes one create-only receipt under the same cross-process lock that fences
claim epochs. A stale lease cannot publish. Identical apply retries retrieve the
same revision even if the response or old-claim cleanup was lost. Unpublished
replacement candidates expire through the protected-store janitor.

Save seals the revision with a durable save-intent receipt before entering the
existing transactional scripts-plus-scenario persistence path. Repair and save
cannot both win. After a failed save, retry the identical save request; that
revision cannot be repaired or saved with changed content. Existing asset-save
receipts recover completed transactions after a lost response. Mixed revisions,
missing members and stripped child batch identities are rejected.

Every repair rechecks script validation, variable dependencies, secret references
and scenario consistency. Acknowledging a warning cannot remove an execution
blocker. Missing credentials, Kafka batch-allocation differences, remaining
producer failure-boundary differences and unverified live conformance continue
to prevent strict replay. No repair changes the workload to exactly 600,000
messages, enables the disabled CSV, or invents downstream assertions.

### Verification boundary

Automated checks cover complete grouped execution, manifest validation, admission
and cleanup, group iteration attribution, protected revision fencing, retry
receipts, payload edits and browser preview/apply/save. Existing virtual-clock
and reference-vector tests cover schedules and parameter semantics. Build-tag
messaging tests exercise the real adapter code without claiming live service
conformance. Authorized Event Hubs and Kafka/OAuth evidence is still required
before strict compatibility can be advertised.

---

## LoadRunner import fidelity and guided repair

Re-import affected archives to obtain corrected semantics. Existing saved scripts are never rewritten.

The HTTP importer preserves ordered ITEMDATA fields and treats EXTRARES as its terminator. Explicit resources live on the originating request in `params.resources`; the bounded resource fetcher executes them for POST and JSON responses as well as HTML pages. Resource work contributes to the parent request's timing. The shared cookie jar, cache and download filters remain in effect.

`source_verification` registers, pauses or resumes a check at its original position. State belongs to the VUser, and a registration in actions does not affect earlier init requests. Its ID, case flag, failure condition and search scope are preserved. Existing explicitly authored script-wide checks continue to apply globally. Unsupported verification forms remain execution-gated.

The active user-agent selection respects `UseCustomAgent`. Navigator language metadata does not supply an HTTP Accept-Language header. Conflicting active declarations still require review. Transaction-order declarations accept `__*delimiter*__` without splitting punctuation inside transaction names.

### Unique allocation

`parameters[].auto_allocate` records LR automatic allocation. Allocation compiles before execution using the planned VUser population and usable rows after StartRow. Each VUser receives a contiguous block identified by a dense ordinal independently of distributed runtime IDs.

- A duration run divides usable rows by planned VUsers using integer division; remainder rows are unused.
- An iteration run uses the effective iteration requirement per VUser.
- Once reserves one value per VUser. An explicit block size is authoritative.
- Exhaustion stays within the VUser's block. ContinueWithLast cannot lend another VUser's final row to an unallocated VUser.
- Insufficient allocation capacity fails before execution. Automatic each-occurrence allocation requires an explicit block size.
- Blocks remain frozen across planned ramps and shards. Groups executing the same script reserve disjoint ordinals from their combined planned population. Conflicting parameter contracts or iteration requirements for that shared script are rejected. Population changes and generator-loss resharding are rejected when they would lose allocation state. Open workloads, stage-driven rescaling, parallel each-occurrence cursors, and shared at-once init are not accepted for these allocations. Fixed-population ramp-up/ramp-down uses the reserved identities.

Generators advertise `lr_fidelity_v1`; assignments carry population and ordinal offset, and their retry fingerprint includes both. Older generators cannot be selected silently for the new semantics.

Allocation rules follow [OpenText's allocation examples](https://admhelp.microfocus.com/vugen/en/26.1/help/WebHelp/Content/VuGen/UI/116150_ui_parameter_simulation_db.htm). Verification scope follows [OpenText's global verification reference](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/web/lrFr_web_global_verification.htm).

### Protected repair

The existing archive review screen offers source-linked repair and allocation previews. Repair requires encrypted candidate storage. The preview/apply endpoints are `/api/v1/scripts/import/repairs/preview` and `/api/v1/scripts/import/repairs/apply`; see the generated API contract for typed fields.

Requests identify the project, protected candidate, opaque revision, finding and action path. Operations restore a body, replace an encrypted proxy-password secret binding, bind an existing producer, add a parameter, add an extractor on a preceding response, select supported TLS settings, or preview allocation. Preview returns a redacted before/after draft and remaining requirements. Apply rotates the candidate and preserves its original expiry. Encrypted receipts make retries of the same operation stable and fence the replaced revision. Project/actor authorization and existing protected save paths remain authoritative.

Restored request bodies remain encrypted in candidate storage and are fully redacted in repair previews. There is no native LR ciphertext decoder. Ordinary LR masking and stronger encryption have distinct diagnostics. An encrypted proxy password requires a replacement secret, and that secret must be configured before replay. SSL_VERSION=2&3 stays blocked until an operator chooses supported TLS behavior; the choice is recorded as an intentional migration, not exact parity.

Dependency analysis examines executable request fields and nested requests. Definite missing producers block execution; conditional or later producers require review. Commented source is not reactivated, and TSTAMP receives no invented meaning. Every repair refreshes the shared findings and fidelity summary. Acknowledgement cannot clear an execution blocker.

The private acceptance archive is not committed. Regression tests use synthetic values and local HTTP servers. Live Salesforce replay and comparison with an LR baseline remain separate validation requiring usable credentials and target access.

### Local acceptance evidence

The supplied private archive was re-imported locally on 2026-09-19. No target traffic was sent.

| Check | Result |
| --- | --- |
| Primary HTTP requests | 492 retained |
| Transaction boundaries | 25 starts and 25 ends |
| Subtransaction boundaries | 445 starts and 445 ends |
| Correlation extractors | 58 retained |
| Largest request body | 113,839 bytes retained |
| Explicit resource parent requests | 4 |
| False ITEMDATA/EXTRARES blockers | 0 |

Three bodies still need restoration. The encrypted proxy credential, legacy SSL selection, 12 missing producer names (including TSTAMP), and one conditional/later producer remain requirements. The parameter-conversion producer's lowercase alias is recognized. Commented correlations remain disabled. These results establish import structure and local replay semantics, not Salesforce/LR baseline parity.

### Post-implementation review (LoadRunner)

The follow-up review corrected five issues and added regression tests:

- Dependency deduplication now distinguishes `var` and `param`, so a conditional variable producer cannot hide a missing parameter blocker with the same name.
- Producer-binding repairs accept whitespace in template expressions and preserve the `var`/`param` namespace. Source evidence remains unchanged.
- Unknown global-verification failure modes remain execution blockers instead of silently using `Fail=Found`.
- Explicit resources take precedence over automatically discovered duplicates, preserving the authored referer and reserving their bounded fetch slots.
- Positional-verification template values participate in dependency analysis; missing inputs block execution.

Validation summary: the full Go suite, the unit/lint/build cycle and all seven LoadRunner browser workflows (dev-admin and local-auth, including guided repair preview/apply) passed at implementation time. The broader browser profiles were not fully green then; the failures were existing workspace-selector assertions unrelated to this work, and later reviews record a passing `npm run test:cycle`. The focused regression tests and the private-archive structural probe passed after the review fixes.

