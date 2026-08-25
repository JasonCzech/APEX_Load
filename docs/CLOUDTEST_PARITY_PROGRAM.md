# CloudTest Outcome-Parity and Migration Program

Status: Active roadmap; initial vertical slices implemented

## Objective

Reproduce the valuable outcomes of current Akamai CloudTest workflows without
copying CloudTest repository objects or its internal Band, Maestro, alpha-grid,
or TouchTest architecture. A capability is complete only when its runtime or API
behavior, dashboard workflow, archive/report representation, and tests are all
present.

The canonical machine-readable ledger is
[`pkg/api/cloudtest_parity_catalog.json`](../pkg/api/cloudtest_parity_catalog.json).
`GET /api/v1/system/parity-status` publishes the same rows in
`cloudtest_capabilities`. The stable in-product dashboard is **Settings →
Integrations → Dashboards → CloudTest outcome parity**; it reads that endpoint
and does not maintain a second status list.

The repository does not currently have a separately published Artifact/Sites
deployment for this dashboard. Its stable application location and API are the
supported views until an external publishing target and stable URL are
provisioned; the ledger therefore does not count external publication as
complete.

## Evidence policy

Every ledger row has one of four evidence levels:

| Confidence | Meaning |
| --- | --- |
| `current_official` | Current Akamai product or documentation page. |
| `current_api` | Current Akamai API reference. |
| `legacy_official` | Akamai/Soasta reference material whose current wire shape must be fixture-proven. |
| `inferred` | Behavior inferred from evidence and not safe to claim as exact parity. |

The starting evidence set is the current [CloudTest product
page](https://www.akamai.com/products/cloudtest), [composition
model](https://techdocs.akamai.com/cloudtest/docs/test-composition), [grid
guidance](https://techdocs.akamai.com/cloudtest/docs/cloudtest-grid), [Preview
Mode](https://techdocs.akamai.com/cloudtest/docs/navigate-the-user-interface),
[analytics](https://techdocs.akamai.com/cloudtest/docs/analytics), [HTTP/2 target
documentation](https://techdocs.akamai.com/cloudtest/docs/edit-target-information),
[SCommand reference](https://techdocs.akamai.com/cloudtest/docs/scommand-reference-1),
and [reporting documentation](https://techdocs.akamai.com/cloudtest/docs/report-generation-analysis).

## Corrected baseline

| Capability | Verified APEX position | Program decision |
| --- | --- | --- |
| HTTP/2 | Supported by APEX and CloudTest. | Parity; no competitive gap claim. |
| Tentative live ramp preview | Existing runtime-adjustment preview is supported. | Reuse it on the composition timeline. |
| Transaction drill-down | Existing archive filters and redacted request/response inspector are partial foundations. | Extend the same archive and inspector; no `/archive/messages`. |
| Page resources | Existing HTTP resource loader and network entries are partial foundations. | Add page/hierarchy attribution; no duplicate page transport. |
| Result evidence | Failure evidence and `snapshot_on_error` are separate concerns. | Add an optional bounded message-detail policy without changing legacy behavior. |
| Streaming response pacing | Already implemented for HTTP responses. | Build media playback and metrics on it. |
| Shared dashboards | Project-scoped Grafana imports exist; native workspaces are private local drafts. | Preserve both and add a versioned native definition. |
| Reporting | HTML, PDF, DOCX, CSV, JSON, JSONL, and Parquet exist; report charts export CSV/SVG. | Extend the shared chart export wrapper to remaining quantitative dashboard charts; do not list PDF or DOCX as missing. |
| Scale | Density numbers are design targets without published evidence. | Never present them as achieved. |
| Native mobile | TouchTest remains in current terminology. | APEX native-mobile automation is a product-fit non-goal, not a competitor-discontinuation claim. |

## Capability ledger views

The checked-in JSON ledger is the only editable capability list. Its stable IDs
are grouped below so reviewers can navigate the program without duplicating
statuses:

- Authoring and load model: `timed_composition`, `reusable_clips`,
  `sequenced_parallel_containers`, `page_resource_model`, `repeat_models`,
  `conditional_switch`, and `visual_timeline`.
- Capacity: `elastic_grid`, `cloud_breadth`, and `prepare_mode`.
- Analytics: `regional_analytics`, `message_drilldown`, `waterfall`,
  `hierarchy_phase_filters`, `network_profile_dimension`, `error_analysis`, and
  `comparison_baselines`.
- Dashboards: `composable_analytics`, `shared_dashboards`, and
  `property_analytics`.
- Monitoring: `infrastructure_monitors`, `external_data_sources`, and
  `monitor_correlation`.
- Execution and protocols: `live_ramp_preview`, `database_load`, `websocket`,
  `jms`, `streaming_media`, and `extension_model`.
- Automation/reporting: `cli_automation` and `word_widget_export`.
- Migration: `cloudtest_import`.
- Explicit decisions: `native_mobile` and `rum_pairing`.

The API test suite validates row count, stable-ID uniqueness, evidence/status
vocabularies, required acceptance tests, and important outcome decisions. The
competitive comparison documents the API-level CloudTest dimensions and is
also checked against the endpoint names.

## Phase 0 — evidence and migration fixtures

Acquire three sanitized, dependency-complete XML or ZIP exports: a basic HTTP
Clip, a multi-track Composition, and a complex repository export. Record the
CloudTest build and export options beside each immutable fixture. All jsDoc and
CloudLink-only mappings remain provisional until a fixture proves the wire
shape.

Exit gate: every claim has a URL and confidence; each proposed mapping is
fixture-proven or provisional; no source claims CloudTest lacks HTTP/2 or says
TouchTest is discontinued.

Current implementation: the evidence-linked ledger, comparison view, program
document, generated API contract, and in-product filtered dashboard are live.
The three sanitized CloudTest exports and their build/export provenance have
not been supplied, so fixture-dependent mappings and external Artifact
publication remain open gates.

## Phase 1 — live regional analytics and safe detail

Add bounded `by_region` aggregation to engine snapshots, distributed protobuf
summaries, WebSocket messages, the dashboard, and terminal archive
reconciliation. Regions are controller-approved load-zone labels, not
unbounded script values, and the geographic view consumes the same aggregation.

Add optional `config.results.message_detail` modes `off`, `errors`, `sampled`,
and `adaptive`, with deterministic sampling and item, item-byte, and run-byte
caps. The controller partitions the run budget and every component enforces its
share. Failures outrank tail/outlier samples, which outrank representative
successes. Protocols retain successful bodies only when the execution hint asks
for them. Existing snapshot bounds and unconditional secret/sensitive-name
redaction still apply.

Extend the transaction archive with region, network profile, source
transaction, hierarchy, phase, and detail-presence filters. Stable event IDs
support bounded authorized live-detail fetches; WebSocket notifications contain
metadata only. The existing inspector provides bucket-to-transaction-to-network
waterfall navigation. Timestamped point/range annotations and named load-stage
phase ranges are distinct from report notes.

Exit gate: a two-region distributed run reconciles live and archive totals;
capture caps hold under load; redaction proves no secret leakage; a browser test
walks bucket → transaction → waterfall.

Current implementation: bounded `by_region` snapshots flow through local and
distributed execution, protobuf, live summaries, dashboard status panels, and
archive reconciliation. The optional message-detail contract admits all four
modes, including project-gated HTTP `sampled` and `adaptive`. Adaptive capture
uses bounded priority classes for failures, protocol/latency outliers, and
representative successes; assignment-time item and total-byte budgets are
partitioned across generators and enforced again at archive admission. Archived
transactions receive stable event/trace IDs and the existing inspector exposes
region/network/hierarchy/phase filters plus DNS/connect/TLS/TTFB waterfall
timings. Success notifications are metadata-only and an authorized bounded live
detail endpoint returns redacted retained evidence. Formal run annotations and
the complete browser bucket-to-waterfall exit test remain in progress.

## Phase 2 — composition authoring and shared analytics

Add active-time `start_offset` to scenario groups and carry it through storage,
revisions, scheduling, capacity validation, immutable snapshots, provenance,
archives, and reports. Paused time does not consume an offset and a group does
not activate while paused. Peak capacity is calculated from overlapping group
windows. The timeline and form/JSON editors round-trip the same scenario model;
scripts referenced by groups already satisfy reusable Clip outcomes.

Add CEL-backed ordered first-match `control_switch`. Add feature-gated
`control_parallel` only with named branches, bounded concurrency, isolated input
snapshots/protocol instances, deterministic `var.parallel.<branch>` extraction
merge, and explicit per-protocol safety. Nested parallel, rendezvous, loop
control, database transactions, persistent WebSocket, browser, and Java Vuser
actions remain rejected until advertised safe. Add bounded hierarchy/page/
promoted-dimension attribution to ordinary actions and reuse the HTTP document
resource loader for Page semantics.

Persist a project-scoped, versioned `DashboardDefinition` with optimistic
revision checks and a code-owned bounded analytics-query catalog. Private
localStorage workspaces remain drafts; Grafana imports retain their current
contract. Property analytics require operator promotion, sensitive-name and
cardinality review, a value cap, and generator-side aggregation into `other`.

Exit gate: offset groups execute at active-time 0/60/120 through pause/resume;
switch and permitted parallel replay match load execution; a second project
user can read a shared dashboard while private drafts remain private.

Current implementation: `start_offset` is persisted and propagated through
scenario payloads, generator assignments, execution gating, provenance, and the
shared form/timeline editor. Ordered CEL `control_switch` is cataloged,
validated, authorable, traversed by dashboard/backend safety tooling, replayed,
redacted, and executed with generated schema coverage. Count, foreach, while,
and scenario iteration models are implemented; fixture-proven parallel-repeat
mapping remains partial. Launch admission now evaluates overlap-aware group
windows rather than summing delayed tracks. Project-gated `control_parallel`
now provides named branches, bounded concurrency, isolated variable/protocol
instances, deterministic extraction namespaces, container metrics, replay, and
runtime safety admission. Typed hierarchy/page attribution reaches execution
and archives. Native project dashboards now provide bounded query definitions,
optimistic revisions, private-draft save/import, and viewer-visible shared
reopening; richer native panel authoring remains in progress.

## Phase 3 — first-class CloudTest import

Register CloudTest in the existing import registry. Accept bounded XML or
dependency-closure ZIP uploads through the standard inspection, encryption,
attestation, parameterization, validate, commit, and batch-commit paths. Import
one script per Clip and one scenario draft per Composition, plus target,
environment, data-pool, monitor, dashboard, and review artifacts.

Every recognized source element receives `executable_exact`,
`executable_bounded`, `blocked_draft`, or `rejected`. Unknown elements retain
path/type evidence. Do not add runtime `call_script`: scenario groups already
reuse scripts. If fixtures prove irreducible nested launch semantics, materialize
an immutable controller-resolved `script_ref` with cycle/depth checks.

Exit gate: golden fixtures have zero unclassified known elements and one
imported composition batch-commits and executes successfully.

Current implementation: `POST /api/v1/scripts/import/cloudtest` and
`GET /api/v1/scripts/import/cloudtest/support-matrix` are registered and the
dashboard exposes an **Akamai CloudTest Export** import flow. The bounded
XML/ZIP inspector classifies every element, emits one review-only script per
Clip plus Composition/data/environment/monitor artifacts, and saves all Clip
drafts through the generic atomic batch endpoint. The original upload is held
in an encrypted short-lived inspection session and becomes a durable encrypted
source revision only after commit. All mappings deliberately remain
`blocked_draft` with `fixture_proven: false`; acquiring the three sanitized
dependency-complete exports is still the gate for exact conversion and the
execution exit test.

## Phase 4 — run-bound monitoring and elastic capacity

Persist collector samples as run-bound resources, then add project monitor
sources and scenario/run bindings. Extend the existing collector with signed,
revisioned, assigned jobs; public-key-sealed expiring secrets; destination/CIDR
and command-pack allowlists; identity pins; and hard time/response bounds. The
controller never opens arbitrary connections into customer infrastructure.

Ship Prometheus/OTel, SSH/Linux, JMX, JDBC, CloudWatch, and Azure Monitor first.
SNMP, WMI/PerfMon, New Relic, AppDynamics, and GCP require customer-backed
evidence. CA Introscope and unrestricted commands are non-goals.

Add `existing|ephemeral` capacity and `off|preferred|required` spot policy on
the durable provision → ready → execute → drain → deprovision state machine.
`prepare` materializes dependencies and capacity without SUT traffic and returns
an expiring revision. Spot uses a configurable percentage ceiling plus
on-demand floor; no CloudTest-style alpha generator is required. Never fail
over silently across regions or clouds.

Exit gate: prepare emits no target traffic; loss and cleanup are visible and
reconciled; capacity survives until archive terminality.

## Phase 5 — media, automation, and reporting

Add a bounded HTTP `media_stream` action for HLS or DASH, live/VOD refresh,
adaptive/fixed rendition, alternate CDN targets, and player/network policy
presets. CMAF is a segment/container format, not a separate manifest protocol.
Report manifest/startup/segment latency, bitrate switches, playback ratio,
estimated buffer, rebuffer risk/duration, errors, CDN, and region. HDS remains
review-only/demand-gated.

Exact named-transaction SLA rules are now evaluated against the complete closed
transaction archive, with bounded repeatable CLI flags/rule files, structured
SLA API/archive/report evidence, stable JSON/JUnit, warning/fail severity, and
fail exit codes. `apexctl library export|import` now wraps the governed library
federation bundle APIs with bounded input and conflict-aware exit behavior.
Monitor CLI commands remain after run-bound monitor bindings ship.
Add DOCX through the common Go report model and per-chart CSV plus PNG/SVG
exports with definition-derived filenames and accessibility descriptions.

Current implementation: Executive, Engineer, and Audit profiles generate DOCX
directly as OOXML from the shared report section registry, including saved
analyst annotations and audit integrity evidence. The report chart set exposes
definition-derived CSV and accessible SVG downloads. Applying the same bounded
export wrapper to every remaining live/operational quantitative chart is still
in progress.

Exit gate: local bounded media fixtures, named-transaction CI gates, DOCX
round-trip inspection, and chart export browser tests pass.

## Cross-phase compatibility and rollout

New fields are optional. Existing offsets default to zero; existing local
dashboard drafts and Grafana imports remain usable; absent capture policy keeps
legacy evidence behavior. Archive and protobuf schemas retain old readers.
Admission fails closed if a generator lacks required capabilities.

Each phase requires Go unit/distributed tests, SQLite and PostgreSQL migrations,
API authorization/bounds/pagination checks, dashboard tests and browser
verification, generated DSL/API freshness, `go test ./...`, and
`python3 scripts/check-docs.py`. Risky features begin disabled, then progress
internal/local → single-project preview → enterprise opt-in → default-on only
after compatibility, security, and browser acceptance gates pass.
