# JMeter source-plan execution and reviewed batch repairs

Messaging imports retain three native drafts and their concurrent scenario. A
converted or saved draft is not necessarily runnable. Strict messaging replay
remains gated by `jmeter_messaging_v1`, which generators do not advertise until
all protocol and live-conformance requirements pass.

## One generator, one source-engine session

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

## Protected repair workflow

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

## Verification boundary

Automated checks cover complete grouped execution, manifest validation, admission
and cleanup, group iteration attribution, protected revision fencing, retry
receipts, payload edits and browser preview/apply/save. Existing virtual-clock
and reference-vector tests cover schedules and parameter semantics. Build-tag
messaging tests exercise the real adapter code without claiming live service
conformance. Authorized Event Hubs and Kafka/OAuth evidence is still required
before strict compatibility can be advertised.
