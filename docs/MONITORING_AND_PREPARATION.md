# Run monitoring and traffic-free preparation

Collector trust, source binding, preparation capacity and messaging management contracts. Start with [project enablement](ANALYSIS_WORKFLOWS.md#enable-the-workflows). Provider-specific setup remains in the [observability guides](OBSERVABILITY_INTEGRATIONS.md).

## Contents

- [Bind monitoring to a run](#bind-monitoring-to-a-run)
- [Prepare before Start](#prepare-before-start)
- [Collector adapter configuration](#collector-adapter-configuration)
- [Preparation capacity policies](#preparation-capacity-policies)
- [Approved cloud metric collectors](#approved-cloud-metric-collectors)
- [Event Hubs processor progress analysis](#event-hubs-processor-progress-analysis)
- [Exact messaging management evidence](#exact-messaging-management-evidence)
- [Kafka management source configuration](#kafka-management-source-configuration)
- [Service Bus management source configuration](#service-bus-management-source-configuration)
- [Solace management source configuration](#solace-management-source-configuration)

## Bind monitoring to a run

Register a collector through the existing project administration workflow.
Configure a persistent, base64-encoded Ed25519 seed in
`APEXLOAD_MONITOR_SIGNING_SEED` on controllers. Pin its public key with the
collector's `-monitor-public-key` flag or `APEXLOAD_MONITOR_PUBLIC_KEY`.

The collector must approve the same pack and exact Prometheus URL locally:

```sh
apexload-collector-agent -collector-id COLLECTOR_ID \
  -packs prometheus -prometheus-url https://metrics.example/metrics \
  -spool-directory /var/lib/apexload/collector
```

Supply the collector token using `APEXLOAD_COLLECTOR_TOKEN`, rather than a
command-line argument. Keep the spool directory persistent and private. It
retains bounded unacknowledged batches and locks out a second process using
the same controller/collector identity. Signed mode sends availability
heartbeats without polling the monitored system until an execution commits.

In **Monitoring → Telemetry → Run monitor sources**, project administrators
create Linux host, Go runtime, or Prometheus sources. In an existing scenario's
data step, editors select sources and save revisioned bindings. Required
sources must have a recent healthy heartbeat and advertise support for the
signed job and approved destination. Optional failures remain visible as
incomplete coverage. Source edits affect subsequent runs.

Each run captures immutable source and binding revisions. Samples have source
timestamps, durable receipt times and an elapsed wall-time clock that includes
pauses. Batches are acknowledged after artifact persistence and metadata
publication; retries deduplicate by job, sequence and evidence digest. A run
archive includes hashed `monitor_evidence.v1.jsonl` evidence and coverage
metadata. The execution page overlays selected monitor measurements with run
latency. Missing/stale evidence is not plotted as zero.

The collector supports fixed SSH/Linux, bounded OTLP/HTTP JSON, and restricted
JMX/JDBC packs in addition to Linux/runtime/Prometheus. CloudWatch and Azure
Monitor are opt-in preview adapters with bounded identity-scoped queries; see
the cloud collector configuration below. Live cloud acceptance remains required; selecting a pack cannot silently return host metrics.

## Prepare before Start

For an enabled project, queued runs expose **Prepare**. Preparation validates
the immutable script/asset revision, reserves or provisions capacity using the
existing target backends, stages assignments and checks collector availability.
It waits before runtime preflight, protocol initialization, reflection, init
actions and monitoring-source polling. Runtimes that cannot honor this boundary
are rejected explicitly (currently Java Vuser, Bruno, and shakeout preparation).

Readiness defaults to 30 minutes from the ready transition; API/CLI callers can choose 5 minutes to 4 hours. Renewal rechecks the execution revision, reservation, ownership and collector readiness. Provisioning itself also has the requested TTL deadline.

Distributed preparation requires both `prepare_v1` and `prepare_readiness_v1` from every selected generator. Its durable allocation records the staged group/peer/VUser reservations before readiness is published. A separate control-plane RPC checks the immutable allocation, unstarted execution groups, execution generation, supervision lease and peak-capacity claim. Periodic checks refresh the existing generator stage deadline without runtime preflight, user code, protocol initialization or target probes. Missing, expired, drained or changed reservations fail readiness; an expired assignment cannot be recreated by a check. Start and renewal repeat the check. Revision or controller-ownership loss invalidates preparation and follows ordinary fenced cleanup. Older peers reject this requested preparation capability while ordinary Start remains compatible.

Local HTTP/TCP counters and real in-process gRPC transport test the preparation boundary, including HTTP init, gRPC reflection, WebSocket, SQL, network, browser navigation and Kafka staging (the native Kafka profile has its own build-tag check). Deterministic Kubernetes/Azure/ECS provider tests cover cancellation and cleanup. These tests do not establish live cloud production acceptance.

**Start prepared run** supplies the preparation ID and expected execution hash.
Start rechecks revision, secrets, ownership, collector availability and the
execution reservation, then atomically consumes readiness. Concurrent consumers
cannot start the same preparation twice. Ordinary Start remains available for
runs without preparations; while a preparation is in progress it answers 409
`preparation_in_progress`. Prepare reserves capacity at once: if quota, a launch
window or a timeslot holds the run back, the preparation fails with 409 and the
run stays queued for ordinary Start or a later Prepare. If peak pod capacity is
unavailable once preparation has begun, the preparation and the run fail rather
than waiting in the queue. Cancellation and expiry interrupt provisioning and
release capacity through the existing execution cleanup path; audit history is
retained. Existing readiness and cancellation remain accessible through the API and CLI after the project preparation flag is disabled. Controller recovery fails closed when a reservation cannot be recovered.

```sh
apexctl prepare create --id RUN_ID --ttl 30m
apexctl prepare show --id RUN_ID
apexctl prepare renew --id RUN_ID --preparation-id PREPARATION_ID --revision REVISION_NUMBER --ttl 30m
apexctl prepare start --id RUN_ID --preparation-id PREPARATION_ID --revision HASH
apexctl prepare cancel --id RUN_ID
```

Zero-traffic acceptance must be verified for each target/runtime combination.
Provider implementations reusing the common gate do not by themselves establish
that every cloud or runtime combination has passed live acceptance.

## Collector adapter configuration

Use signed jobs for remote packs. `--adapter-targets` names a local JSON file of
approved pack/destination pairs. A scenario cannot change this allowlist.
For SSH, include the exact OpenSSH `host_key`; the only command reads loadavg,
meminfo, and uptime from `/proc`. No supplied command is executed.

Provide a protected `--credential-key-file` containing the base64 X25519 private
key. Enroll its public key in the project-admin source configuration using
`collector_public_key`. Set `credential_ref` to a project secret whose JSON value
contains `username` and `password`, or `private_key` for SSH. Credential bundles
are sealed to the collector and bound to project, job, source revision,
destination, and a one-minute signed lease. Readiness advertises the same public
key. Changing credentials never writes plaintext into spool or archive files.

OTLP uses `--otlp-listen 127.0.0.1:4318` and a protected `--otlp-token-file` (32 or
more bytes). Approve `otel-metrics` with an `otlp://stream-name` destination.
Emitters POST uncompressed OTLP JSON to `/v1/metrics` with bearer authentication
and `X-APEX-Monitor-Stream: otlp://stream-name`. Remote access requires a TLS proxy.
The receiver accepts gauges and cumulative monotonic sums, retaining timestamps
and bounded string attributes. Unsupported histogram, summary, delta, or
attribute semantics return explicit partial-success rejection counts. Payloads
are capped at 1 MiB and 512 buffered points per stream; full buffers return 429.
No automatic exporter/probe traffic is introduced by preparation.

Build and configure the [restricted Java helper](../runners/monitor-java/README.md)
for the `jmx` and PostgreSQL `jdbc` packs. Broader database drivers and attribute
packs require explicit implementations and acceptance; they are not advertised.

## Preparation capacity policies

Preparation accepts an immutable `capacity` object. `selection: existing`
reserves available generators without a provider mutation. `selection: ephemeral`
requires an approved asynchronous launch-capable pool; unsupported local/static
or Kubernetes targets fail before preparation starts. Azure Apps/Jobs/Instances
and ECS reuse their durable launch intent and cancellation paths.

`spot` is `off`, `preferred`, or `required`. ECS spot must be approved by a target
runtime profile. `on_demand_floor` and `spot_ceiling_percent` bound the exact
integer allocation. Required spot fails when unsupported or rounded to zero.
Preferred uses on-demand where spot is unsupported; it does not fall back across
clouds or regions, or retry failed provider spot launches as on-demand. Standing
services are not scaled by ephemeral preparation. Once consumed, existing
archive-terminal teardown fences govern cleanup. Standalone provisioning retries
of run-owned requests are refused because they would lose execution ownership.

Equivalent CLI flags are `--capacity`, `--spot`, `--on-demand-floor`, and
`--spot-ceiling` on `apexctl prepare create`.

OTLP receiver buffering is bounded to 512 samples per approved stream and five
minutes. The receiver returns explicit partial rejection for unsupported units,
semantics, timestamps or dimensions, and HTTP 429 when full. Late arrivals within
this retention period remain collectable. Per-run accepted sample identities and
pending batches survive collector restart through the spool. The passive receiver acknowledges HTTP ingestion only after its protected local
journal has been replaced and synced. It refuses ingestion until the collector
holds its spool ownership lock and has recovered that journal. Five-minute
retention still applies; this is not an OTLP archival gateway.

Dependency monitor links now carry the selected UTC interval into the overlay.
A bounded receipt scan reports `scan_limited` rather than claiming absent historical
evidence is zero; mixed valid and invalid observations report incomplete coverage.

Use `apexctl monitor dependencies --id RUN --at 2026-01-01T00:00:00Z` to inspect
the same qualified dependency interval through the CLI (omit `--at` for latest).

## Approved cloud metric collectors

`cloudwatch` and `azure-monitor` are opt-in signed-job packs. Their source destinations are local aliases (`cloudwatch://orders`, `azure-monitor://orders`), with polling intervals of at least 60 seconds. No controller-provided URL, SQL, expression or secret can change the approved query. They use the collector's cloud identity, not sealed password bundles. Configure a persistent spool directory as for other run jobs.

Example entry in the operator-owned adapter-targets file:

```json
{
  "pack": "cloudwatch",
  "destination": "cloudwatch://orders",
  "cloud": {
    "region": "us-east-1",
    "account_id": "123456789012",
    "namespace": "AWS/EC2",
    "metrics": [{"name":"CPUUtilization","unit":"Percent","statistic":"Average","dimensions":{"InstanceId":"i-approved"}}]
  }
}
```

The file is an array of entries. AWS identity is checked against the approved account using STS before CloudWatch use; service endpoints are fixed to commercial AWS regional endpoints. Ambient AWS credentials must belong to the collector's approved role, with permission to read only the necessary metrics.

Azure entries use `pack: azure-monitor`, an `azure-monitor://name` alias, and `cloud` fields `tenant_id`, `client_id`, `identity` (`workload` or `managed`), `resource_id`, `namespace`, and `metrics`. Resource IDs must be full subscription/resource-group/provider paths. The adapter uses the approved user-assigned managed identity or workload identity and the fixed Azure Resource Manager endpoint. The first slice accepts resource totals; dimensional Azure series are rejected. Configure resource-scoped read access externally.

Each target permits eight explicitly named metrics, fixed one-minute periods and a five-minute lookback after execution starts. Supported statistics are Average, Minimum, Maximum and Sum (Azure Total); percentiles and arbitrary expressions are rejected. Values retain summary semantics and statistic/period dimensions. Seconds normalize to milliseconds; supported units also include percent, count, bytes and their per-second rates. Missing points are never filled with zero. Provider-null buckets remain absent and can acquire late numeric observations during the five-minute lookback. Acknowledged identities are retained in the durable spool so overlapping polls do not duplicate observations; accepted historical values are immutable.

Requests use bounded deadlines, at most three attempts, a 1 MiB response limit, and no redirects. Incomplete, paginated, duplicate-timestamp or incompatible-unit results fail visibly. Deterministic adapter tests are implemented; live AWS/Azure acceptance is still required before advertising production readiness. API contracts: [CloudWatch GetMetricData](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_GetMetricData.html), [Azure Monitor Metrics](https://learn.microsoft.com/en-us/rest/api/monitor/metrics/list?view=rest-monitor-2023-10-01).

## Event Hubs processor progress analysis

The live and completed-run diagnostics retain a standalone processor progress snapshot even when an interval has no actions or business transactions. SQLite/PostgreSQL history reload now preserves both messaging action summaries and processor snapshots; old nullable rows remain missing. JSONL/Parquet archives and reports retain the same bounded evidence. The live read can recover the last processor snapshot after unrelated intervals while it remains in bounded resident history; archives retain final evidence.

Use `apexctl analysis progress --id RUN_ID --mode live`, or `--mode archive` for a completed archive. Optional `--group` and `--source-id` select exact retained identities. The equivalent authorized read is `GET /api/v1/tests/{id}/messaging-progress`; see the generated [API contract](swagger.yaml). This operation admits fixed live/archive modes and bounded identity filters, with shared analysis admission and a 30-second deadline. It does not accept SQL or formulas. Snapshots are limited to 64 rows and 128 KiB; overflow marks coverage incomplete, and aggregate omitted-row counts are lower bounds.

Positions and lag retain exact int64 decimal strings. Opaque provider offsets are displayed separately and never subtracted. Delivered-position and durable-checkpoint backlog are different measures; neither is enqueue age. Tail observation is opt-in and adds bounded management traffic only after Start: `config.eventhubs.observe_partition_tail: true`, with `tail_poll_interval_ms` from 10000 to 300000 (zero/omitted uses 10000). See [processor configuration](EVENT_HUBS_HOWTO.md#processor-progress-and-checkpoint-evidence) for identity approval and capability requirements.

The API returns nullable delivered/checkpoint lag. Complete inventory, owned generation and source-monotonic ages for ownership and both cursors must qualify within 30 seconds of each row's capture. A stale/missing/disabled/fenced/unconfirmed cursor, in-flight checkpoint or incomplete scope stays unscored; an observed zero remains distinct. Per-row capture timestamps preserve provenance, while transit time and cross-host clock alignment remain unqualified. Results describe backlog as of source capture and association, not current-live freshness or causation.

Counters and checkpoint timing distributions are cumulative by authenticated source, execution group, session, partition and ownership generation. Latest evidence/capture revisions replace prior rows instead of summing repeated windows. Checkpoint outcome, explicit retry, redelivery and ownership verification evidence remain visible. Current owned/unverified rows take precedence when retained history reaches its bound. Progress queries do not turn these cumulative values into transaction counts or percentile averages, and they do not add CI or live-stop verdicts. Strict processor pause confirms actual receive/tail/checkpoint quiescence; recovery and live Azure qualification remain open; local/tagged checks do not certify provider readiness.

## Exact messaging management evidence

The management foundation is separate from data-plane action diagnostics and from native DuckDB queries. Enable new poll admission explicitly with both `APEXLOAD_RUN_MONITOR_PROJECTS` and `APEXLOAD_MESSAGING_MANAGEMENT_PROJECTS`. The capabilities response distinguishes the v1 evidence contract, project enablement and genuinely registered executable packs. The foundation and provider adapters are distinct releases. The collector registers the genuine `kafka-offsets`, pinned shared-key `servicebus-runtime` and pinned Basic `solace-semp` readers. Provider qualification is recorded separately from reader availability; the Service Bus workflow below describes its finite supported scope.

An administrator-approved source carries a versioned entity inventory, provider identity, principal/policy metadata and an exact `management://alias` destination. The alias selects an operator-owned collector target containing finite approved endpoints; a job cannot replace those endpoints. Kafka scopes state `read_committed` or `read_uncommitted` explicitly. Source revisions are immutable for already-started runs. Collector readiness publishes startup instance and scope/identity hashes; required sources remain unavailable without a matching genuine observer.

Credential names for management are separate from send/receive credentials. SASL/basic usernames and shared-key key names must equal the approved principal. The controller seals a purpose-specific short-lived credential bundle to the enrolled collector key. An opaque keyed credential version pins each source revision; rotation blocks observed publication until an administrator saves a new revision. Secret values and their raw hashes are never returned.

One admitted poll captures a bounded cohort of eligible overlapping runs. The collector performs one source observation and the controller durably writes private run-owned envelopes before atomically publishing their receipts. Exact retries retain source capture times, generation, poll identity and core digest. Controller receive timestamps use UTC microsecond precision consistently in immutable artifacts and database receipts; source timestamps retain their original precision. Even a denied poll with no numeric points and no rows is a durable snapshot with explicit missing inventory; it does not become an observed zero.

Offsets and populations are decimal strings, including observed `"0"`, through API, archive, CLI and dashboard. Kafka committed positions and read boundaries are next offsets. High watermark and last stable positions stay distinct; the physical replica log end is unavailable in the foundation. [Apache Kafka protocol semantics](https://kafka.apache.org/22/design/protocol/) describe these boundaries. Service Bus active counts can include deferred messages, so the foundation never invents a separate deferred count; see [Service Bus message counters](https://learn.microsoft.com/en-us/azure/service-bus-messaging/message-counters). Solace depth refers to the instantaneous queue collection count, as described in [SEMP collection counts](https://docs.solace.com/Admin/SEMP/SEMP-Features.htm).

Use `apexctl monitor management --id RUN_ID --source-id SOURCE_ID --limit 20` to read bounded receipt pages. Add `--from`/`--to` for an inclusive/exclusive capture window and `--cursor` for the returned opaque continuation. An entity filter selects complete snapshots containing that entity; it preserves their original approved inventory coverage. The dashboard's **Messaging management evidence** table displays exact values, unavailable reasons, source capture/receipt clocks and cadence gaps. Reads of accepted evidence remain authorized after feature disable.

Run archives keep the historical point artifact and optionally add `monitor_management.v1.jsonl` with snapshot/row counts, schema version, coverage and integrity metadata. Older archives omit the sidecar and gain no guessed management evidence. Scalar queries ignore typed-only observations rather than rounding large integers.

Poll expiry is not proof that provider calls stopped. A source with unresolved physical ownership remains unavailable to a new collector instance. The **Management collection ownership** controls show its exact owner/generation. Administrators may attest a verified process stop, with a durable audit trail, to release that exact fence. `apexctl monitor collection --project PROJECT_ID --id SOURCE_ID` reads the state; `monitor release-collection` accepts an explicit JSON attestation using `--file`. A stale owner/generation cannot release a newer collector. Collector-confirmed completion remains a distinct workflow.

[Review and verification status](qa/reviews/feature-completion-management-2026-10-03/README.md) records foundation acceptance separately from subsequent real provider qualification.

Terminal management poll journals remain available for seven days for retry and audit. The leader maintenance sweep scans at most 512 terminal journals and locks at most 50 projects per sweep. A durable cursor advances across protected records so repeated maintenance cannot scan the full retained history. Receipt-referenced polls, unresolved physical ownership, and the active configuration fairness cursor remain retained; run deletion makes obsolete journals eligible after the retry window.

A collector supports at most 32 assigned collecting run jobs, separately from its 32-job pending evidence budget. Required bindings reserve assignment slots before optional bindings; excess required bindings block Prepare/Start, while excess optional bindings record `collector_capacity` incomplete coverage. Stopping a run releases its assignments. Existing databases above the assignment limit report an explicit error until runs are stopped or expired; they cannot claim full monitoring coverage.

## Kafka management source configuration

The `kafka-offsets` pack reads only approved topic/partition/group tuples. Select it in **Monitoring sources** when project management collection is enabled and the controller advertises that executable pack. Administrators enter the exact cluster ID, read isolation, finite inventory, collector alias, SASL principal/policy revision, management-secret reference and enrolled collector public key. The saved approval displays its source revision and opaque credential version separately from unsaved edits. Source updates use the expected revision; a conflict retains the draft for review and requires reloading the current approval.

A local collector target pins every permitted TLS broker endpoint to an explicit numeric address at the same port. No hostname resolution or broker-discovered destination expands this map. The cluster ID must equal the scope provider. Supply a protected CA file containing only approved certificates; the collector loads and validates it once during configuration. Changing the file does not alter a running collector's trust. Reconstruct the collector with the revised configuration to approve new trust.

```json
{
  "pack": "kafka-offsets",
  "destination": "management://orders-kafka",
  "management": {
    "endpoints": ["tls://broker.example:9093"],
    "tls_ca_file": "/protected/kafka-ca.pem",
    "kafka": {
      "cluster_id": "APPROVED_CLUSTER_ID",
      "dial_addresses": {"tls://broker.example:9093": "192.0.2.10:9093"}
    }
  }
}
```

This fragment illustrates routing, not a complete target: copy the saved `management_scope` and `management_identity` into the corresponding local `management.scope` and `management.identity`, including the controller-generated credential version. Keep the sealed management credential separate from script send/receive credentials. Supported authentication is SASL PLAIN over verified TLS and SCRAM SHA-256/SHA-512. Anonymous, OAuth and ambient credentials are unavailable for this reader. Readiness validates only local immutable approval and does not contact the broker.

One source poll admits every TCP dial, TLS handshake and fixed Kafka request under the shared limits. The reader makes no background retries, consumption, production, offset commits or group joins. It verifies cluster identity on authenticated connections before reading positions. Observations preserve committed-next, high-watermark-next and last-stable-next separately; physical replica log-end remains unsupported. The capture time is the oldest successful component of a row, including cleanup in its source-age qualification. Partial permission or budget failures retain explicit field availability rather than converting missing positions to zero. The minimum source cadence is ten seconds.

```sh
apexctl monitor create-source --project PROJECT_ID --file source.json
apexctl monitor update-source --project PROJECT_ID --id SOURCE_ID --file revised-source.json
apexctl monitor delete-source --project PROJECT_ID --id SOURCE_ID --revision SOURCE_REVISION
```

An update file contains the currently saved `revision`. Deletion also requires that exact revision. Rotate a secret by saving a new administrator-approved source revision before collecting with the new credential. Existing run bindings retain their immutable older approval and fail visibly if its pinned credential no longer matches.

A timeout or failed socket close cannot release physical ownership. The collector keeps that resource charged until closure is proven; an unresolved close can also hold collector shutdown. Verify the process has actually stopped before using the existing audited stopped-process release controls. A lease expiry alone is insufficient.

Provider qualification is recorded separately from these implementation contracts. Controlled protocol fixtures cover framing, missing data, authentication, budgets and cleanup. Real broker results must identify the exact broker version/image, authentication profile and topology. The pinned Apache Kafka 3.9.2 single-broker fixture verifies all three SASL profiles and the distinction between high-watermark and last-stable next positions during an open transaction and after abort. This does not certify full consume-transform-produce atomicity, broker failover, newer releases, Event Hubs Kafka endpoints or protected hosted clusters.

## Service Bus management source configuration

The `servicebus-runtime` pack reads runtime descriptions for an administrator-approved finite inventory of queues or topic/subscription pairs. Select it in **Monitoring sources** when project management collection is enabled and the executable pack is advertised. Enter the canonical lowercase namespace hostname, public collector alias, exact Manage policy principal and policy revision, management-secret reference and enrolled collector public key. The source never accepts a connection string, raw key, external SAS or send/receive credential. A saved approval shows its revision and opaque credential version separately from the unsaved draft; revision conflicts retain the draft until the current approval is reloaded.

The initial management profile uses a pinned shared-key identity. A Manage claim can authorize provider changes; the collector itself exposes only fixed entity-description GET operations. Entra/ambient identity and live Azure interoperability are separate qualification gates. Data-plane Service Bus execution has its own runtime capabilities and is not enabled by selecting this management source.

A collector target approves one HTTPS namespace, one literal numeric address at port 443, and a protected explicit CA bundle. Construction loads trust once and makes no target requests. No DNS lookup, proxy, redirect, automatic retry, AMQP connection or discovered destination expands the approval. Changing a CA file requires reconstructing the collector with a revised operator-approved configuration.

```json
{
  "pack": "servicebus-runtime",
  "destination": "management://orders-servicebus",
  "management": {
    "endpoints": ["https://example.servicebus.windows.net"],
    "tls_ca_file": "/protected/servicebus-ca.pem",
    "service_bus": {"dial_address": "192.0.2.20:443"}
  }
}
```

This routing fragment is not a complete target. Copy the saved `management_scope` and `management_identity` into local `management.scope` and `management.identity`, including the opaque credential version. The scope provider must equal the approved namespace hostname. The first profile accepts queue/topic hierarchical ASCII paths of at most 256 characters and one-segment subscription names of at most 50; reserved management path segments and case-aliased duplicates are rejected. Authored entity casing remains in evidence.

Collection cadence is 30–3600 seconds, with a 30-second default for local approval. Older stored ten-second definitions remain readable and deletable, but a new revision must meet the current policy. The shared namespace origin gate prevents overlapping sources from bypassing the cadence. Each successful entity consumes separate admitted TCP, TLS and GET operations under the signed authorization deadline, whole-poll deadline, response-byte and shared activity budgets. Approval of 256 entities does not promise that all fit one poll; remaining evidence explicitly reports budget exhaustion.

Observed populations remain exact decimal strings, including zero. Missing or nil fields remain missing. Deferred counts are unsupported because active counts can include deferred messages; this reader does not estimate a separate count. Subscription scheduled/size evidence stays unsupported in this initial profile. Queue total, active, scheduled, dead-letter, transfer, transfer-dead-letter and size observations retain individual availability. A row's capture/age includes its actual read and physical cleanup; partial coverage remains visible in the existing management table, archive sidecar and CLI queries.

A timeout or unexplained socket close retains physical ownership. Verify the actual collector process has stopped before using the audited stopped-process release controls; expiry alone does not prove release. Controlled TLS/HTTP/XML tests qualify local parsing, authentication, budgets and cancellation behavior. They do not certify live Azure counters, service tiers or identity profiles. Provider results must identify the exact approved namespace/profile and independently reconcile populations.

## Solace management source configuration

The `solace-semp` collector pack polls only exact administrator-approved VPN/queue or VPN/topic-endpoint tuples through individual SEMP v2 monitor requests. The dashboard advertises it only with project management collection enabled and an executable collector pack. Enter the canonical lowercase broker hostname, exact Basic principal without a colon, policy revision, separate management-secret reference and enrolled collector public key. Entity case, Unicode and literal punctuation remain part of the identity. Saved approval and its opaque credential version remain separate from the unsaved draft; revision conflicts retain the draft.

The initial decoder profile follows the pinned software-broker 10.26.7.12655 monitor/action schemas. A controlled fixture is not live broker qualification. Collection discovery, pagination, legacy XML and alternate provider versions remain separate acceptance gates. Depth comes only from `collections.msgs.count`, preserving exact decimal strings and observed zero; absent/null counts remain missing. A DMQ relationship is retained only when its exact queue is approved in the same VPN. The legacy empty DMQ field means the relationship is unavailable, and cannot establish that no DMQ exists.

Approve one HTTPS origin, one fixed numeric route with a matching port and a protected explicit CA bundle in the collector. Startup loads trust once without target traffic. The collector requires a protected durable spool directory for session quarantine and explicit permission for the fixed own-session logout action.

```json
{
  "pack": "solace-semp",
  "destination": "management://orders-solace",
  "management": {
    "endpoints": ["https://broker.example.test:943"],
    "tls_ca_file": "/protected/solace-ca.pem",
    "solace": {
      "profile": "semp_v2",
      "dial_address": "192.0.2.30:943",
      "allow_own_session_logout": true
    }
  }
}
```

This routing fragment is not a complete target. Copy the saved `management_scope` and `management_identity` into local `management.scope` and `management.identity`, including the controller-generated credential version. Use the existing source CLI create/update/delete commands above; edits and deletion require the saved revision. The management identity is separate from message send/receive credentials.

Each source has a minimum thirty-second interval; aliases at the same normalized origin share the cooldown and a bounded five-HTTP-operation-per-second rate with burst two. Every TCP, TLS, fixed GET and own-session PUT operation consumes signed activity/deadline authority. There is no DNS, proxy, redirect, retry, discovery or arbitrary action. The response capture time precedes physical GET cleanup, and row age includes later transport and logout work. Remaining inventory is visibly incomplete when poll bounds are exhausted.

Before Basic bytes can escape, the collector durably records a bounded credential-free session intent. It reuses only the exact in-memory owned Session cookie and logs out using `PUT /SEMP/v2/action/about/user/logout` with fixed `{}` inside the original signed authority. A successful provider response, validated action metadata, proven raw socket close and durable intent removal are all required to release the session. Cookies and credentials never enter evidence. Cancellation may finish this bounded cleanup only while the original authority remains valid.

A missing/ambiguous cookie, rejected logout, expired authority, failed persistence or unproven close retains quarantine and prevents further authentication at that origin. Restart reloads unresolved intents even if the collector identity, controller URL or enabled pack changes. Process/socket destruction is separate from provider-session destruction; the existing stopped-process attestation cannot clear this remote-session quarantine. No automatic session reset or public provider-audited recovery workflow is advertised by this first slice. Preserve the protected spool and independently audit the provider session before any later recovery procedure.

Controlled TLS fixtures validate request surfaces, session ownership, exact depth, cancellation, physical cleanup and retained restart quarantine. Live software-broker/provider-version acceptance and audited restarted-source recovery remain open. Selecting this monitoring pack does not enable Solace SMF or JMS load execution.
