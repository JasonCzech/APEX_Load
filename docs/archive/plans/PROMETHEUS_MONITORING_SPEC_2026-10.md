# Prometheus target-system monitoring — specification and implementation plan

Status: Historical (completed specification archived 2026-10-29)

## Purpose and baseline evaluation

Complete the connection, discovery, collection, visualization, archive, comparison and acceptance workflow for Prometheus monitoring of the system under test. Reuse APEX's durable provider pipeline established for AppDynamics. Load-generator observability, controller `/metrics`, platform graphs and deployment monitoring are separate concerns.

The preexisting implementation had three useful but incomplete workflows:

| Existing workflow | Preserved behavior | Gap addressed by this implementation |
| --- | --- | --- |
| Deployment Prometheus/Grafana query client | Platform graphs and imported panels | Global transport settings and panel snapshots do not provide a project connection with immutable per-test query selection |
| Collector `prometheus` pack | Bounded text-exposition scrape and durable generic run-monitor batches | Unlabeled scrape points do not provide PromQL, retained labels, query provenance or source freshness |
| Imported Grafana dashboards | Safe variable resolution, panel rendering and existing threshold behavior | Explicit adoption is needed to create governed reusable target-query sources without migrating credentials or thresholds |

No automatic migration changes existing sources, dashboards, thresholds or generator configuration. Provider packs have distinct identities: `appdynamics`, legacy `prometheus`, and target-query `prometheus-query`.

## Agreed scope

| Area | Contract |
| --- | --- |
| Primary API | Read-only Prometheus `/api/v1` query API, direct HTTPS or assigned collector |
| Managed profile | Azure Monitor managed Prometheus with explicit Azure identity and capability negotiation |
| Core data | Numeric PromQL range results, query metadata, retained label identities, optional scoped target health and alert states |
| Histograms | Numeric PromQL aggregates supported; raw native histogram payloads omitted with explicit coverage |
| Correlation | Exact run UTC window and administrator-selected target labels; all activity in that scope |
| Setup | Disabled drafts, independent capability checks, bounded discovery and query preview at the saved location |
| Test surfaces | Ad-hoc, scenario, preparation, schedule, distributed, desktop, CI and controller-managed CLI |
| Durability | Accepted content-addressed batches, idempotent receipts, cursor commits, canonical late revisions and immutable archives |
| Finalization | Collecting → reconciling → stopped or incomplete, fixed maximum five-minute drain |
| Acceptance | Explicit terminal monitoring rules, sufficient coverage and compatible semantics |
| Qualification | Fixtures plus real Prometheus 2.55.1/3.13.0 SDK contract checks; cloud identity and production private deployment qualification remain pending |

Exporter/agent installation, Prometheus configuration changes, remote write, unrestricted metadata/annotations, Alertmanager incident-history ingestion, trace causation inference and standalone engine-only capture are excluded. Prometheus-compatible products such as Thanos, Mimir and VictoriaMetrics need their own tested matrix; API resemblance alone is not a support claim. AWS SigV4 and arbitrary tenant-header authentication are not implicit fallbacks.

## Functional contracts

### Connections and permissions

A project connection stores name, description, enabled state, revision, backend profile, origin/context path, authentication, direct/collector location, trust policy, secret references and independent capability health. Incomplete disabled drafts require only a name; supplied transport fields must still be valid. Enabling requires complete credentials/configuration and successful verification of that saved revision. Updates and deletion require current revisions.

Project administrators manage connections, secret references, sources and query definitions. Editors choose approved sources, bindings and acceptance rules. Viewers receive safe summaries and run evidence. System administrators approve direct private origins/CIDRs, private HTTP exceptions and direct-mode Azure workload/managed identities, which authenticate as the controller's own principal; any change to such a connection's endpoint, identity mode, client/tenant ID or execution location needs that approval again. Collector tokens access only assigned signed jobs and submission routes.

Authentication is none, Basic, bearer, or Azure, with optional HTTPS mTLS. Azure identity mode is explicit workload identity, managed identity or service principal; no implicit developer/default identity is used. Workload identity requires saved tenant/client IDs; service-principal identity also requires a project-secret client-secret reference. Public projections omit secret names, values, private CIDRs and CA material. Reopening an editor leaves secret inputs blank. Blank references preserve an existing reference only for an unchanged authentication identity. Endpoint/context-path/auth/mTLS changes do not carry old secrets to a new identity.

Azure setup assigns the executing identity **Monitoring Data Reader** at the workspace and requests `https://prometheus.monitor.azure.com/.default`. Private Link retains the normal workspace query hostname, with executor-local private DNS, exact-origin/CIDR approval and TLS hostname verification. Collector local configuration explicitly approves the same Azure identity and transport profile. See [setup and collector examples](../../PROMETHEUS_MONITORING.md#azure-monitor-managed-prometheus).

Saved origins reject embedded credentials, query strings and fragments. TLS verifies hostnames and bounded custom CAs. Transport rejects redirects, validates DNS at dial time, connects to the validated address and denies metadata/link-local/loopback/unspecified destinations. Collector access also requires local exact-origin approval. Private HTTP is explicit, CIDR-bounded and credential-free. Azure credentials are restricted to the exact workspace query origin.

### Capabilities, discovery and query preview

Verification runs at the saved direct or assigned collector location; a private endpoint is never tested from an unrelated controller. Authentication, expression query, discovery, targets, alerts and optional backend-specific features have independent results: supported, unsupported, permission denied, authentication failure, unreachable and successful no-data.

Selected-source readiness uses a bounded 60-second inspection context and probes only approved main/freshness/terminal expressions and selected target/alert capabilities. Estimate the selected request count against the configured shared quota and deadline before transport; selections that cannot fit fail with an explicit budget error. General connection verification retains separate discovery/capability checks. Readiness bounds are independent of the live 30-second poll continuation budget.

Discovery supports bounded metric names, label names/values, series, metadata and target summaries. Administrators can browse bounded metric/label catalogs without a selector; result limits and truncation notices prevent an exhaustive TSDB inventory. Series discovery requires a metric selector, and supplied filters narrow the requested population. Metadata filters select an explicit metric name; metadata does not describe an individual label-selected series. Scoped target discovery and run diagnostics use explicit safe label matchers. Public discovery retains at most 12 safe identity fields per entity and only type/unit metadata, with truncation when fields are omitted. Query preview uses the typed source contract over a recent window, maximum ten minutes, and creates no SUT traffic or artificial test.

Test/discovery/preview return `202` with an operation ID and have short durable expiry/claim fences. Operation results exclude credentials and retain bounded safe projections. Stale verification results cannot bless an edited revision. Resolved dashboard adoption is a local read-only draft operation returning `200`.

### Query definitions and scope

Each source selects one project connection, an evaluation step, polling interval, settling allowance and up to 20 query definitions. A query contains stable ID/name, expression, declared unit/semantics, allowed retained labels, minimum expected series, optional freshness expression/sample-age bound, optional terminal expression and up to 16 typed saved bindings. A binding has a bounded name/value and `label_value`, `duration` or `timestamp` type. Only whole-token `{{param.name}}` substitution is supported: quoted/escaped label literals, normalized durations bounded to one day, or parsed UTC timestamps. Arbitrary syntax insertion is rejected. Bindings participate in immutable definition identity. Internal continuation/grid fields are not accepted as public source configuration.

Discovery gates CPU, memory, request/process rate, GC and Kubernetes presets on their actual upstream metric dependencies. An operator explicitly selects the application job and Kubernetes namespace, approves units/labels/freshness, and previews the editable result. Presets do not configure exporters or infer instrumentation semantics solely from a metric's name.

Expressions are parsed as PromQL, bounded and read-only. Administrators approve the queried population, including recording rules and aggregates that may not have explicit matchers. Retained labels are allowlisted, deduplicated and bounded. Unexpected unapproved or sensitive output labels reject a result rather than silently dropping identity. Catalog discovery may be bounded and unscoped; scoped target discovery and diagnostic selectors use safe label matchers. A separate explicit evidence selector scopes optional run target/alert records. Raw target URLs, arbitrary annotations and unsafe fields are excluded. Provider series identity remains separate from display labels so label projection cannot silently merge distinct upstream series.

There is no automatic injection of an APEX run label. Administrators select application/job/instance/namespace/environment labels and choose whether the queried population includes concurrent production traffic. This default scope plus UTC time is `entity_time` correlation, not causal proof.

### Existing dashboard adoption

Reuse the established safe Grafana variable resolver, committed variable bindings, reserved project identity and macro behavior. The draft records source hash, selected connection revision, original datasource UIDs, resolved variables, fixed range/step and skipped-target warnings. Reject unresolved or unsafe expressions and reusable-source adoption of historical `test_id` templates. Bound the result to 20 supported queries and report truncation.

An administrator explicitly reviews query unit, semantics, label projection and mapping of every adopted expression to the selected project connection. A disabled source is created only after that review. No credentials, Grafana colors/thresholds, run acceptance rules or hidden bindings migrate. Legacy dashboards retain their current behavior.

### Test binding and preparation

Omitted per-test bindings inherit scenario bindings; an empty array disables monitoring; a nonempty array replaces inherited bindings. Preparation checks monitoring project gate, configuration/secret revisions, capability permissions, collection/storage budgets and collector enrollment without SUT requests. Execution freezes the resolved source/connection selection, query definition identity, run grid and acceptance rules. Edits invalidate unused preparations and apply to future tests.

Both direct and collector workers use the shared provider adapter. Direct workers use the current execution-owner fence; collectors use signed expiring assignments, enrolled keys and sealed runtime credentials bound to project/run/job/origin/grant. No synthetic collector or self-HTTP ingestion is created for direct collection. Independent bounded workers keep upstream queries from blocking load controls or unrelated packs. Endpoint/authentication budgets and executor quotas are shared across concurrent runs.

## Measurement semantics and ingestion

Prometheus range-query timestamps are expression evaluation instants, not guaranteed scrape timestamps. Freeze a run-aligned evaluation grid and preserve the exact step. Preserve explicit finite zero; absent, nonfinite, unsupported histogram or invalid points remain missing/omitted with coverage. Do not persist cached values as new observations. Prometheus lookback/staleness semantics apply; an optional matching freshness companion distinguishes aged target samples from current evaluations. The companion must match temporal anchors/offsets as well as selected label identities. Unknown freshness stays explicit. Azure series identity folds label names/values as required by that backend while a bounded safe `display_labels` map retains original spelling for presentation.

Provider metadata preserves backend profile, query ID and definition hash, projected labels and stable series identity, evaluation step/mode, source freshness and receive time, provisional/settled state and aggregation eligibility. Ordinary range results use `evaluation_point`; terminal numeric aggregates use `query_window` with exact start/end. Generic 90-second receive-age rules do not discard valid historical query evidence merely because its source timestamp is old.

Retain upstream warning/info annotations in bounded safe coverage. Partial responses and limit exhaustion cannot become complete coverage. Empty results are successful queries with no observations, not zero-valued series. Expected evaluation slots and minimum series count provide evidence completeness independently from transport success. Changing cardinality, source freshness and target health are visible separately.

Classic and native histogram expressions may yield numeric PromQL aggregates. Raw native histogram documents are not coerced to numeric points. Rolling histogram quantiles and summary quantiles remain estimates; never derive a whole-run percentile by averaging or merging them. Counter resets are handled by explicit PromQL `rate`/`increase`, not by summing counter levels in APEX. Numeric averaging is an average of eligible uniform-grid expression evaluations, not a weighted per-request statistic.

The common ingestion service validates assignment/project/run incarnation, owner/grant, scope and batch limits; persists content-addressed normalized batches; atomically commits receipts/cursors/canonical observations; acknowledges identical retries and rejects conflicting retries. Canonical keys include job, query definition, upstream series, evaluation timestamp/mode and exact window identity. A late revision replaces only the canonical head; immutable receipts remain available for integrity. Mutable target/alert records have equivalent canonical heads. Advance a query watermark only after all chunks commit.

## Lifecycle, archive and acceptance

Persist load terminal status and exact end time immediately. Continue querying only data overlapping the frozen load window during reconciliation, maximum 300 seconds. Preserve this deadline across restarts, leases and ownership changes. Stable successful settled passes can close collection early; budget exhaustion/outage produces explicit incomplete coverage. Load pause/resume/failure/abort controls remain independent.

Archive finalization has a durable monitoring dependency and waits without holding mutation locks. At closure freeze canonical metrics, diagnostic summaries, query provenance, coverage and acceptance results, then seal integrity. Existing capacity cleanup rules continue through archive terminality. Published archives are immutable; artifact corruption uses the existing archive-integrity failure path. Live series, JSONL exports, optional native analysis, comparisons and acceptance all consume canonical observations. Core ingestion does not require DuckDB.

Analysis resampling supports mean, minimum and maximum of settled evaluation-grid values and preserves absent cells. Its `analysis_bucket` projection declares the display interval and `analysis_statistic`, retaining the original query/hash/backend/statistic/step/label identity. It cannot be submitted as provider evidence or satisfy acceptance rules. Exact-window results cannot be rebucketed. Evaluation levels cannot reconstruct rates, totals or percentiles. Same-series overlays validate the preserved definition and label identity before rendering a compatible comparison.

Native run-to-run Baseline Resources comparisons consume frozen canonical numeric observations. Matched stable load windows show an explicitly labelled arithmetic evaluation-grid mean, minimum and maximum, with missing/stale/provisional cells excluded from the numerator and retained in expected coverage. Zero values remain eligible. Whole-window summaries and raw counter levels are excluded. Delta compatibility requires the saved query ID/hash, backend, statistic, full labels/entity identity, exact unit and original step. Manual legacy resource mappings cannot erase or combine Prometheus query identities. Reports and exports retain provenance and statistic availability; grid means of rolling quantile estimates never claim whole-run request percentiles.

Terminal monitor rules select exact source/query/series dimensions, expected unit, evaluation mode/statistic, window offsets, comparator, threshold, minimum coverage, freshness requirement and fail/warning severity. Default window is the load interval, minimum coverage 95%, and at least one eligible observation. Never aggregate different queries, backends, units, steps or series identities silently.

`series_points` supports average/minimum/maximum over compatible settled grid points. A counter level is overlay evidence. Rules over rolling quantiles describe the minimum, maximum or mean of evaluation-grid estimates, never a whole-run request percentile. Sum is reserved for a declared exact-window total. `exact_window` requires an approved terminal expression and records one numeric aggregate for that window, evaluated at its end; only `{{window.duration}}` is a supported runtime substitution. Each vector dependency must use exactly one full-window range, with no fixed or nested ranges, fixed timestamps or offsets. Direct range functions and one full-window subquery over an instant selector are supported; for example, `sum(increase(http_requests_total{job="checkout"}[{{window.duration}}]))`. The backend's extrapolation and lookback behavior remain part of the query's meaning.

Results are pass, fail, warning or not_evaluated. A conclusive failing criterion fails acceptance. Missing fail-rule evidence or incomplete required monitoring makes the performance report Inconclusive. Optional capture problems without fail criteria appear as warnings. Rules never enter load-control/early-stop logic. CI remains `monitoring_pending` during reconciliation and closes incomplete required evidence as `monitoring_incomplete`, never a pass.

## APEX defaults and bounds

These are APEX limits, not upstream service guarantees. Existing archive quotas also apply, and every bound produces coverage/truncation metadata.

| Setting | Default or upper bound |
| --- | --- |
| Poll / evaluation step | 30 seconds / 15 seconds |
| Settling / overlap / post-load drain | 120 / 300 / maximum 300 seconds |
| HTTP / complete poll deadline | 10 / maximum 30 seconds |
| Decompressed response | 4 MiB |
| Endpoint/authentication request budget | 60/minute, two concurrent requests |
| Query definitions / retained labels | 20/source / 12/query |
| Expanded series | 200/source, 1,000/run |
| Sources | Existing maximum 32/run |
| Point query budget | 10,000 points/poll, split into existing bounded batches |
| Batch | 1 MiB, 512 measurements, 100 diagnostic records |
| Setup preview | Maximum ten-minute window |
| Freshness companion default max age | 90 seconds |

Transport/authentication/permission and query-cost failures are classified separately. Throttling/transient outages retry within shared budgets and honor bounded retry state; excessive sample/data-point or cardinality errors require narrower expressions or smaller windows, not blind retry loops.

## Interfaces and product completion

Project resource: `/api/v1/projects/{id}/monitoring/prometheus-connections`, with revisioned CRUD, saved test/discover/preview, operation status and safe dashboard adoption. Reuse monitor-source, scenario-binding, preparation, test-monitor, paginated series/evidence and archive resources. Source pack `prometheus-query` is additive; collector capability `prometheus-query.v1` gates assignment. Old point-only batches, existing collector packs, AppDynamics/Dynatrace/Elastic behavior and archives remain readable.

Generate OpenAPI, endpoint catalogs and dashboard contracts from route/type definitions. CLI commands and MCP tools expose the same project connection/query/binding/evidence operations. Settings labels distinguish target capture from platform graphs. Live/archive timelines show selectable compatible units/labels, gaps, freshness, source watermark, delay, coverage and bounded evidence. Existing numeric external maps alone do not replace the canonical timeline.

## Implementation sequence and exit criteria

| Milestone | Deliverables and exit criteria |
| --- | --- |
| 1. Contracts/persistence | Revisioned safe connections, additive SQLite/PostgreSQL schema, source/query config, per-test binding precedence, frozen preparation fingerprints, generated APIs |
| 2. Provider adapter | Bounded PromQL parsing/query API, auth/TLS/private policy, independent capability negotiation, discovery/preview, labelled normalization, freshness and histogram warnings |
| 3. Execution/ingestion | Shared direct/collector contract, inspections, sealing, independent workers, quotas, fenced cursors/receipts/canonical heads and restart recovery |
| 4. Reconciliation/archive | Fixed drain deadline, canonical exports/diagnostics, integrity, HA recovery and monitoring archive dependency |
| 5. Acceptance/analysis | Compatible evaluation-grid rules, exact terminal expressions, missing evidence Inconclusive, pending/incomplete CI and canonical comparisons |
| 6. Product/tooling | Connections/source/rule editors, explicit Grafana adoption, live/archive evidence, CLI/MCP parity and operator documentation |
| 7. Release qualification | Full regression gates plus exact backend versions and cloud identity/network evidence published below |

## Verification and release acceptance

Tests cover write-only secrets/project isolation/identity changes; hostname/custom CA/mTLS/private HTTP/dial-time DNS/redirect/metadata denial; Basic/bearer/Azure renewal and explicit identity; finite zero/missing/nonfinite/native histograms; label projection without identity collision; grid/step alignment and freshness companions; partial responses/warnings/cardinality changes; request costs and truncation; exact windows/reset/extrapolation semantics; retries/conflicts/lost acknowledgments/partial chunks; owner/grant fencing, restart and failover; fixed reconciliation deadlines; frozen archives/corruption; binding inheritance; prepare without SUT traffic; passing/failing/warning/unevaluable rules; CI pending/incomplete; old packs/dashboard thresholds/provider regressions.

Run affected Go tests during development, race scheduling/ingestion tests, SQLite and PostgreSQL integrations, full Go suite, MCP suite, generated-contract freshness, documentation checks and full dashboard `npm run test:cycle`. Browser verification covers dev-admin and local-auth connection drafts, credential replacement, roles/project switching, source selection/direct vs collector, query validation, adoption, live delay/coverage/freshness/gaps, archive retrieval and comparisons.

Completion requires direct and private collector runs to collect/archive selected data, outages to leave load controls responsive, one canonical observation per provider identity/evaluation, no excluded secrets/diagnostics in public output, closure within the drain bound, missing required evidence unable to pass CI, and consistent live/archive/analysis/rule data. Fixture gates establish implementation behavior; exact real backend/service qualification is a separate release prerequisite.

## Compatibility and release evidence

| Backend | Current evidence | Real-environment qualification required |
| --- | --- | --- |
| Prometheus HTTP `/api/v1` | Real disposable Linux/arm64 Docker servers **2.55.1** and **3.13.0**, checked 2026-10-04: range queries, terminal query, target/alert summaries and all six discovery operations; fixtures additionally cover warnings, limits and errors | Production proxy context path, workload instrumentation, retained labels, source staleness and target scope |
| Private Prometheus via collector | Signed/sealed/fenced collector and transport fixtures | On-premises origin/CIDR/CA/mTLS approval, persistent keys, restart and HA artifact identity |
| Azure Monitor managed Prometheus | Explicit identity/profile/query-contract fixtures | Workspace query origin, RBAC, workload/managed/service-principal tokens and rotation, capability differences and service quotas |
| Other compatible backends | No compatibility certification | Product/version-specific qualification before publishing support |

The reproducible [compatibility matrix](../../../pkg/prometheus/testdata/compatibility-matrix.json) records exact image digests, date, architecture and observed result counts. Its [harness](../../../pkg/prometheus/testdata/run_compatibility.py) exercises the SDK transport seam against owned disposable real servers. This establishes the listed query/API contracts; production private-network TLS, collector enrollment/HA and Azure identity qualification remain separate release checks. Other server versions and compatible products are not certified by resemblance.

Primary upstream references: [Prometheus HTTP API](https://prometheus.io/docs/prometheus/latest/querying/api/), [query evaluation and staleness](https://prometheus.io/docs/prometheus/latest/querying/basics/), and [Azure query endpoint](https://learn.microsoft.com/en-us/azure/azure-monitor/metrics/prometheus-api-promql). These explain upstream behavior; APEX limits and acceptance semantics above are implementation contracts.
