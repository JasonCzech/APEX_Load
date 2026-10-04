# AppDynamics setup and operation

APEX Load connects to classic AppDynamics SaaS and on-premises Controllers and reads monitoring data during an opted-in test. A licensed Events Service is optional. AppDynamics Cloud/Cisco Cloud Observability is a separate product and is outside this integration's contract.

## Configure and verify a project connection

1. Create the required credentials in the project's secret provider. Prefer a dedicated read-only AppDynamics OAuth client. Basic authentication is available for compatible Controllers; use a dedicated least-privilege user. Cisco Identity Provider users cannot use Controller Basic authentication. Analytics uses a separate Events Service API key and global account name.
2. Open **Settings → Integrations → AppDynamics test capture**. A disabled draft can be saved with a name before connectivity is available.
3. Supply an HTTPS Controller origin, context path (normally `/controller`), and account name. Select OAuth or Basic authentication and enter the logical project-secret references. Do not paste credential values into these fields. Saved values and reference names are not returned to the browser. Leave replacement fields blank to keep existing credentials.
4. Select the default collection location. **Direct from controller** requires outbound access from the APEX Load controller. **Assigned collector** requires an approved registered collector with the AppDynamics pack, connectivity to the AppDynamics Controller, the job signing trust configuration, and its enrolled X25519 public key. The generators do not contact AppDynamics.
5. For on-premises private endpoints, explicitly approve the required private CIDRs and supply a trusted CA PEM when necessary. DNS and dial policy still rejects unapproved, loopback, link-local, metadata, and multicast destinations. TLS verification remains enabled. Redirects cannot move credentials to another origin.
6. If Analytics is needed, supply the HTTPS Events Service origin, global account name, and a separate API-key reference. The Controller and Events Service have separate request budgets.
7. Save the connection disabled, then run **Test saved connection**. After verification succeeds for the saved revision, edit to enable it. Verification is an asynchronous bounded operation. The controller derives the credential-sealing key from the enrolled collector; operators do not supply a key for saved checks. The UI shows the operation and capability results. A configuration change invalidates verification for the current revision.
8. Use **Discover entities** to browse applications, tiers, nodes, business transactions, or metric paths. Select an application ID for child discovery. Discovery returns bounded results and makes no requests to the application under test.

The upstream account must be able to read the selected applications and capabilities. Missing licenses, insufficient permissions, disabled APIs, upstream errors, and no matching data must be treated separately. An unavailable optional capability does not imply that APM metrics are unavailable.

## Create a reusable source and bind it to a run

In **Monitoring → Telemetry → Run monitor sources**, select the `appdynamics` pack and a saved project connection. The connection supplies its verified direct or collector collection location. A collector connection requires an approved collector and its enrolled public key. Select at least one metric, diagnostic capability, or Analytics definition.

Supply positive numeric application IDs, optional tier/node/business-transaction/backend/machine scopes, and exact metric paths from discovery. Each metric has a stable friendly name, application ID, unit, and requested source statistic. The API supports entity identities as well as scoped paths; a wildcard that returns several nodes produces several distinct series, not a merged number. A source may select up to 20 metrics, 200 returned series, and 5 bounded Analytics queries. The default metric polling interval is 60 seconds.

Choose events, health-rule violations, and/or snapshot summaries. Snapshots are bounded diagnostic summaries; raw call graphs, SQL text, request bodies, and unbounded stack payloads are not a full evidence export. Optional Analytics requires the Events Service credentials and read-only `SELECT` queries scoped to a selected application ID. Choose transaction, log, or metric result kind and project only necessary fields; `SELECT *` is rejected. Numeric values require the alias `value`, such as `SELECT avg(responseTime) AS value FROM transactions`. Set the unit and source statistic explicitly. Timestamped numeric results require `bucket_seconds`; weighted average evaluation requires valid `count` and `sum` columns. Collection enforces bounded responses, rows, query duration, and rate budgets.

Bind sources to a scenario for repeated and scheduled runs. During Run Setup, use scenario bindings, turn monitoring off for that run, or select a replacement set. Omitted `monitor_bindings` inherits the scenario; an explicit empty array disables monitors; a nonempty array replaces the inherited set. A source marked required blocks Start when its collection path is not ready. Connection and source revisions are frozen into the preparation/run: later edits and deletion cannot redirect an active run.

Preparation resolves connectivity and credentials without SUT traffic. Collection continues during a soft pause, aligned to elapsed wall time. Monitoring errors during load do not stop the load engine.

## Interpret live and archived evidence

The run's monitoring view shows source state, coverage diagnostics, source watermark, collection success time, typed metric series, and bounded diagnostic records. Metrics retain the source timestamp, bucket, resolution, statistic, entity dimensions, and unit. Select one metric/entity series before plotting; unit or entity mismatches must not be averaged together. Zero is a measured value. Missing and stale observations remain gaps.

AppDynamics data can arrive after load ends. The default settling delay is 120 seconds, with a bounded 300-second final drain and overlapping queries to reconcile late buckets. The UI distinguishes **load finished; collecting delayed data** from **collection finished** and **collection incomplete**. A snapshot or event within the run's time/entity scope is contextual evidence; it does not establish that this test caused it. Stronger run correlation requires the explicit opt-in header and matching AppDynamics data-collector configuration on approved SUT origins.

Optional performance criteria are terminal guardrails. Select a source, metric, exact dimensions, unit, comparison statistic, threshold, severity, minimum coverage (95% default), and an optional elapsed window. Only settled, valid, compatible buckets count. Source averages require their observation weights; sums require interval-count/sum metrics. Whole-run p95/p99 cannot be reconstructed by averaging bucket percentiles. Missing evidence produces `not_evaluated`; missing required monitoring produces an inconclusive result and the CI reason `monitoring_incomplete`.

Archived points and diagnostics are content-addressed, redacted, bounded, and listed in the archive manifest. Coverage, truncation, and terminal criterion outcomes remain available after a connection is removed. Missing artifacts or integrity failures must be shown as unavailable evidence; no records does not mean no errors. Compatible series can be overlaid or compared in Analysis and baseline workflows using their units, dimensions, and source aggregation metadata.

## Deployment and shared artifacts

AppDynamics capture requires the project run-monitoring feature gate. A required source fails preparation when monitoring is disabled. Readiness checks account for configured queries across sources using the same Controller and the bounded reconciliation workload; reduce selections if the shared account budget cannot cover them.

When execution HA is enabled, every controller replica must access the same monitoring artifacts. Supported object storage backends satisfy this requirement. For the filesystem backend, mount the same durable shared filesystem at the monitoring artifact path on every replica and set `APEXLOAD_MONITOR_SHARED_FILESYSTEM=true` only after the system administrator verifies that the mount and path are shared. The variable is an administrator attestation; it does not create or validate a shared mount. HA preparation rejects enabled AppDynamics sources on an unattested filesystem backend so a failover cannot silently lose retained evidence.

## Troubleshooting

- **Draft or unverified:** complete the selected authentication fields, save, and test the current revision. Enabling a connection does not by itself verify access.
- **Collector operation remains pending:** confirm the collector is healthy, assigned to this project, allowed to run the AppDynamics pack, and configured with the signing public key and enrolled credential-sealing key.
- **Transport failure:** check DNS, approved private CIDRs, HTTPS origin/context path, CA trust, and egress from the selected execution location. Do not disable TLS validation as a workaround.
- **Metrics unavailable:** check application visibility, exact metric path/ID, Controller API variant, and returned source bucket resolution. Short tests may have no settled full bucket.
- **Analytics unavailable:** verify the separate global account name, API key, Events Service endpoint, API permissions, and license. APM polling does not depend on Analytics.
- **Partial or truncated capture:** narrow the application/entity scope or wildcard paths, reduce concurrent sources/queries, and inspect upstream throttling. Collection is bounded so monitoring cannot monopolize test execution resources.
- **Criterion not evaluated:** check dimensions, units, source statistic, settled-bucket coverage, and the selected elapsed window. Do not turn missing samples into zeros to obtain a verdict.

Vendor references: [OAuth API clients](https://help.splunk.com/en/appdynamics-saas/extend-splunk-appdynamics/26.4.0/extend-splunk-appdynamics/splunk-appdynamics-apis/api-clients), [least-privilege API client](https://help.splunk.com/en/appdynamics-saas/mcp-server-integration/26.7.0/configure-a-least-privileged-api-client), [metric and snapshot APIs](https://help.splunk.com/en/appdynamics-saas/extend-splunk-appdynamics/26.4.0/extend-splunk-appdynamics/splunk-appdynamics-apis/metric-and-snapshot-api), and [Analytics Events API](https://help.splunk.com/en/appdynamics-saas/extend-splunk-appdynamics/26.4.0/extend-splunk-appdynamics/splunk-appdynamics-apis/analytics-events-api). Documentation version links describe vendor contracts, not a claim that those Controller releases passed APEX Load customer fixtures.

See the generated [OpenAPI specification](swagger.yaml) and [endpoint catalog](api-endpoint-catalog.json) for the complete current API surface. [Implementation specification](APPDYNAMICS_SPEC.md) records defaults, data contracts, and release acceptance criteria.

Current validation is fixture-backed. Real SaaS, private on-premises, and licensed Analytics qualification is deferred; no exact Controller version is currently qualified. The [compatibility and qualification matrix](APPDYNAMICS_SPEC.md#compatibility-and-qualification-status) lists fixture evidence and the remaining real-environment checks.

Metric presets are populated from saved-connection discovery, so unavailable paths are never invented. Choose response time, calls, errors, slow/stalled calls, CPU, memory, heap, or garbage collection, then browse the exposed hierarchy and review each metric’s units and source statistic. Discovery includes backends and bounded search results with truncation notices.

For Analytics run correlation, configure an AppDynamics HTTP data collector for `X-Apex-Test-Run` and name its projected ADQL field in the definition’s optional **run ID field**. APEX safely encodes the runtime equality filter. Empty run ID fields retain entity/time correlation. AppDynamics request GUIDs remain separate from W3C trace IDs.

Live metric overlays can select response time, TPS, VUsers, or errors. Missing native buckets remain gaps. Archive metadata includes frozen capture coverage, bounded diagnostics, approved Controller links, and an optional canonical metric timeline preview (8 MiB / 10,000 points); download the complete JSONL artifact for larger analyses.

Each Analytics definition also requires an explicit application filter: provide the verified Controller application name for the standard transaction schema, or an administrator-approved application ID field for custom schemas. APEX inserts the appropriate runtime filter and rejects unscoped queries.

Run correlation destinations may use explicitly approved HTTP or HTTPS SUT origins. This allowance carries only the opaque run marker; Controller and Analytics connections continue to require HTTPS with hostname verification. Redirects outside the approved SUT origins receive no run header.

Unbucketed Analytics aggregates summarize their entire query window and display as a labeled window summary. The default terminal full-run criterion requires a completed aggregate for that exact load window. Partial evaluation windows require explicit timestamped Analytics buckets; overlapping live query aggregates are not combined into a time series or a whole-run statistic.

Multi-day runs use the same bounded reconciliation workflow. Request deadlines, response sizes, data-point limits, and upstream query limits still apply; oversized queries are split where supported, and exhausted budgets produce explicit partial coverage.

For collector backlogs, the limit of 32 sources applies per run. A collector can serve concurrent runs using bounded grant pages and retains pending batches while it completes the assignment scan. Its local spool is bounded to 256 pending job identities and 33 MiB of decompressed data; expired assignments are removed without replacing accepted observations.

Archive retention also respects the existing shared adjunct evidence quota (64 MiB by default). When the monitoring export exhausts that budget, the performance archive is preserved with `archive_evidence_budget` coverage and explicit truncation. Rules needing the omitted evidence become not evaluated; required evidence remains inconclusive and cannot produce a passing CI check.
