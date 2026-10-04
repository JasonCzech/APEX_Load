# AppDynamics integration specification

## Objective and boundaries

Provide native project-governed connectivity, discovery, ingestion, evidence retention, analysis, and terminal performance evaluation for classic AppDynamics Controller SaaS and on-premises deployments. The same collection semantics apply to direct controller polling and assigned collectors. APM is the core capability; licensed Analytics is an independent optional capability.

The release is complete when an administrator can configure and verify a connection, an operator can attach a reusable monitor source to an ad hoc or scheduled test, both execution locations retain compatible typed data, and live/archive views disclose coverage and verdict limits accurately. Raw transaction tracing export, unrestricted diagnostic payload export, AppDynamics configuration writes, agent installation, and AppDynamics Cloud are outside this contract.

## Architecture and interfaces

`pkg/appdynamics` owns the bounded read-only vendor client, configuration/selection validation, discovery, authentication, policy checks, source parsing, bucket/statistic metadata, and capability results. It shares typed metric points with `pkg/analysis`. Storage owns project connections, revisioned monitor sources and bindings, immutable private connection snapshots, durable jobs/operations, receipts, drainage state, and evidence. API orchestration owns secret resolution, administrative operations, preparation/readiness, controller collection, collector grants, archive integration, and terminal evaluation. The dashboard exposes these workflows. CLI and MCP provide matching contracts.

All connections, sources, bindings, operations, series, diagnostics, and comparison inputs are project-isolated. Create/update/delete and secret use are governed by existing project/admin/test permissions, audited, and revision-fenced. Administrative discovery/test operations are asynchronous, expiring, bounded, and produce no target traffic.

### Connection and source model

| Contract | Required behavior |
| --- | --- |
| Project connection | Name, enablement, collection default (`direct` or `collector`), optional assigned collector, nested transport `config`, configuration revision and verified revision, safe capability/verification metadata, write-only project-secret references. |
| Transport config | Controller HTTPS origin/context path/account/auth mode; optional Events Service origin/global account; explicit private CIDR approvals/CA trust; bounded time, bytes, requests, points, and series. |
| Authentication | Controller OAuth client or compatible Basic user. Analytics uses a separate API key. Resolved credentials never persist in public records, provenance, diagnostics, or collector grants. |
| Monitor source | Existing revisioned `MonitorSource` with `pack: appdynamics`, `connection_id`, `executor_mode: direct|collector`, collector identity/key when selected, `interval_seconds`, and nested `appdynamics` selection. |
| Selection | Project/application IDs; metric name/path/application/unit/statistic/entity identity; optional tier/node/business-transaction scopes; events/violations/snapshots; bounded Analytics queries with explicit application ID, result kind, unit, source statistic, and optional bucket duration; settling delay; explicit run-header opt-in and approved origins. |
| Bindings | Existing scenario bindings plus create-test `monitor_bindings`: omitted inherits, `[]` disables, nonempty replaces; each binding contains `source_id` and `required`. |
| Private snapshot | Frozen connection revision/config and logical secret references held privately in prepared/run jobs. Public source/job projections remove references and credential-sealing public keys. |

Connection APIs use project-scoped CRUD, saved connection test, entity/metric discovery, and operation polling. The generated OpenAPI specification defines exact routes and wire fields; documentation does not maintain a duplicate endpoint catalog. Connection requests support optional credential-reference replacements: omitted or blank inputs preserve references only while the corresponding endpoint/account/authentication identity is unchanged. Identity changes clear retained references unless replacements are supplied; replacing a client ID or username also requires its matching secret or password. Responses expose only presence booleans.

### Ingestion contracts

Metric points preserve source UTC time, value, unit, metric identity, source/entity dimensions, quality, original bucket boundaries/resolution, selected statistic, weighting/count/sum where provided, retrieval time, and settled/aggregation eligibility. A collection timestamp must not replace a metric's source timestamp. Missing data is not zero. Dimension-distinct series remain separate. Unsupported or nonnumeric responses generate bounded coverage reasons rather than silently inventing values.

Events, violations, snapshots, and Analytics diagnostics have stable IDs where available, source time, capability/type, entity/application identity, severity/status, bounded redacted summary, and correlation strength. Collector receipts and archived diagnostic artifacts preserve ownership/integrity and support deterministic deduplication. Events-only evidence does not satisfy numeric metric coverage.

Both execution locations use identical provider parsing and budgets. Direct jobs use controller ownership fences. Collector jobs are signed, assigned, expiring, sealed to the approved collector key, destination-restricted, and acknowledged durably. Retries and spool replay are idempotent. Tokens are scoped by remote identity; credential-bearing requests cannot redirect to an unapproved origin. On-premises reachability is explicit, and TLS validation remains enabled.

## Runtime behavior and defaults

| Default/limit | Value |
| --- | --- |
| Metric poll interval | 60 seconds |
| Overlap for late/revised source buckets | 300 seconds |
| Settling delay | 120 seconds |
| Final drain | At most 300 seconds after load ends |
| Sources per run | 32 |
| Metric selections per source | 20 |
| Returned metric series per source | 200 |
| Expanded metric series per run | 1,000 |
| Analytics queries per source | 5 |
| Response body | At most 4 MiB |
| Metric points per provider collection | At most 10,000 |
| Durable batch | At most 1 MiB, 512 measurements, and 100 diagnostic records |
| Diagnostics retained per source/run | 1,000 events, 200 violations, 200 snapshots, 100 logs, 100 transaction samples |
| Request timeout | 10 seconds default, at most 30 seconds |
| Requests per minute | 60 Controller, 30 Analytics maximum; shared account budgets, with conservative 30/15 executor caps |
| Concurrent requests per Controller endpoint/account | 2 |
| Poll work | 30 requests / 30 seconds by default |
| Terminal rule minimum coverage | 95% |

Prepare validates the required collection paths without SUT traffic and freezes the intended monitoring revision. Start consumes the prepared revision or rejects a stale preparation. Required readiness failures block Start; optional failures are visible warnings. A source/configuration edit does not change an active run.

Collection continues through pauses and follows elapsed wall time. Upstream failure, throttling, credential refresh failure, pagination/budget exhaustion, unsupported/unlicensed capabilities, empty data, and partial data have separate coverage reasons. Retries use bounded backoff and shared connection budgets. Source timestamps, settling, and deduplication govern eligibility rather than the number of successful HTTP requests.

Reconciliation can cover multi-day run windows while retaining the same per-poll request, time, response, and point budgets. Actual source buckets retain their bounded resolution; an unbucketed Analytics aggregate records the duration of its exact query window separately. A wider window does not waive upstream limits or guarantee complete coverage.

Load completion transitions jobs to drainage, while source watermarks and capability coverage continue to advance. The deadline produces a fixed terminal evidence set, archive artifacts, and rule evaluation. Required incomplete capture is inconclusive; optional incompleteness is disclosed. Loss of ownership, cancellation, deadlines, and collector outage cannot cause unbounded shutdown or falsely complete evidence.

The run correlation header is opt-in, limited to approved HTTP/SOAP origins, and cannot override a conflicting explicit user header. Redirects must not propagate it to unapproved targets. AppDynamics data collectors must be configured separately to retain/query it. Time/entity correlation is the default and does not establish causation.

### Terminal performance criteria

`goal_config.monitor_rules` contains source ID, metric, exact dimensions, unit, statistic (`avg|min|max|sum`), comparator (`lt|lte|gt|gte|eq|neq`), finite threshold, severity (`fail|warning`), minimum coverage, and optional start/end offsets. A criterion identifies one consistent series. Settled, valid, compatible full source buckets are eligible. Source averages require valid weights, sums require interval-count/sum semantics, and bucket percentiles are never averaged into whole-run percentiles.

Results retain the rule, `pass|fail|warning|not_evaluated`, observed value when evaluable, coverage percentage, eligible bucket count, and reason. Rules run after drainage and never stop load traffic. Missing required monitoring produces the CI failure reason `monitoring_incomplete`; an unevaluable optional rule remains explicitly not evaluated.

## Dashboard, verification, and rollout

Settings owns connection CRUD and saved test/discovery; Monitoring shows safe summaries and reusable source editing. Scenario editing owns reusable bindings; Run Setup supports per-test replacement/off/inheritance and criteria. Drafts preserve selections while project changes clear them. Viewers see safe names, health, evidence, and results, and cannot manage credentials or redirect collection.

Run monitoring displays source/capability health, watermarks, freshness, drainage, typed metric overlays, entity selection, units, gaps, coverage/truncation, and bounded evidence. Archived views show integrity and terminal criteria. Analysis uses the same retained source-series semantics; baseline comparison requires compatible identities, dimensions, units, kinds, resolution, and statistics. Older archives remain readable without AppDynamics fields.

Release tests must cover:

- OAuth refresh/account identity and Basic auth; separate Analytics credentials; TLS/private-CIDR/SSRF/DNS-rebinding/redirect policy; no credential/reference disclosure; caller/project permissions.
- Classic metric API variants, discovery, pagination, scope IDs/wildcards, duplicate/revised buckets, source times/resolution/weights, unknown units/statistics, partial capability results, no data, rate limiting, malformed/oversized bodies, cancellations, and budgets.
- Direct/collector data parity; signed/sealed assigned grants; lease/HA fencing; idempotent receipts/spool recovery; reconnect/outage; overlapping late data; drainage deadline and archive terminality.
- Scenario inheritance, explicit off and replacement bindings; frozen revisions; preparation without target traffic; required/optional readiness; scheduled runs; safe run-header injection and redirect stripping.
- Terminal rule exact-series compatibility, weighted averages, interval sums, gaps, coverage, no settled bucket, warning/failure/not-evaluated outcomes, and CI required-monitoring incompleteness.
- Dashboard draft/secret/revision behavior, project switching, role restrictions, ready-source selection, source dimensions/units, live/archive evidence, missing/integrity-failed artifacts, and old archives.
- CLI/MCP parity, generated contract freshness, both dashboard authentication browser profiles, and real SaaS/on-premises customer fixtures before claiming version compatibility.

Run affected Go/MCP tests, the full Go suite for shared changes, dashboard typecheck/lint/unit tests, and browser workflows under dev-admin and local-auth. Document supported Controller versions only after evidence-backed real integration checks. Keep the feature opt-in; use a nonproduction Controller first, measure request budgets and ingestion lag, then enable project connections with narrowly scoped applications.

### Compatibility and qualification status

Current validation uses local deterministic fixtures. Real SaaS, on-premises, and licensed Analytics qualification is deferred at the user's request. No exact Controller version is currently qualified, and vendor documentation version links do not establish tested compatibility.

| Product / API | Exact qualified version | Current evidence | Remaining release qualification |
| --- | --- | --- | --- |
| Classic SaaS Controller, direct execution | None | OAuth, discovery, current metric API, diagnostics, direct ingestion, and bounded reconciliation fixtures | Customer-backed SaaS run, archive, and failure recovery; record exact Controller version |
| Classic on-premises Controller, assigned collector | None | Basic/OAuth, custom CA and network policy, signed/sealed collector jobs, durable receipts, and evidence fixtures | Private Controller run through an enrolled collector; record exact Controller version and capabilities |
| Legacy Controller metric API | None | Unsupported-only fallback and authentication/permission failure fixtures | A real Controller exposing the legacy API; record exact version and returned resolution |
| Analytics Events Service | None | Separate authentication, scoped read-only ADQL, bounded projections, query-window semantics, and truncation fixtures | Real licensed transaction/log/aggregate queries; record Events Service and Controller versions |

Replace pending qualification entries with exact environment versions, enabled capabilities, test dates, and retained release evidence after those checks succeed. Fixture results establish implementation behavior against the modeled contracts; they do not establish vendor licensing or deployment compatibility.

Analytics series require a numeric column alias `value`; average series are eligible for weighted criteria only with valid `count` and `sum` columns. Timestamped projections must declare `bucket_seconds`. Queries must select explicit safe fields (`COUNT(*)` is permitted; `SELECT *` is rejected). Connection creation begins as a disabled draft; operators verify the saved revision before enabling it. Controller identity changes clear retained credentials unless replacement references are supplied. Public connection responses hide custom CA contents and private network trust.

Execution HA requires shared monitoring artifacts. Object storage backends are accepted. Filesystem installations must use a common durable mount on all replicas and explicitly attest it with `APEXLOAD_MONITOR_SHARED_FILESYSTEM=true`; the flag never provisions or verifies a mount. Preparation rejects selected enabled AppDynamics sources when this condition is unmet. Required sources also fail preparation when the project's run-monitoring gate is off. Account budget admission covers configured queries across all sources and the 300-second reconciliation window.

Unbucketed Analytics values carry `query_window` metadata and describe their full queried interval. Terminal rules require an aggregate matching the exact evaluation window; earlier overlapping live windows are not accumulated. Partial rule windows require timestamped buckets. The dashboard presents the latest query-window summary rather than plotting overlapping query aggregates as native bucket samples.

Collector assignment traverses bounded pages of 32 signed grants across concurrent runs; pending batches remain until an assignment scan is complete. The source limit stays 32 per run. A collector’s local durable spool supports 256 pending job identities and at most 33 MiB of decompressed data. Older collectors retain the existing signed array contract for their supported packs.

The existing archive adjunct evidence budget (64 MiB default) also bounds canonical monitoring exports. Exhaustion records `archive_evidence_budget` and `provider_coverage.archive` as partial/truncated while preserving the performance archive. Affected criteria are not evaluated and missing required evidence produces an incomplete CI outcome. Finalization reserves 1 MiB plus four times the initial manifest JSON size for bounded terminal metadata.
