var e=`# Prometheus target-system monitoring

APEX Load collects target-system metrics from an approved Prometheus HTTP API while a managed test runs. Project connections and \`prometheus-query\` sources use the shared governed monitoring, evidence, reconciliation and acceptance pipeline ([governed provider registry](OBSERVABILITY_INTEGRATIONS.md#governed-provider-registry)). Prometheus must already scrape the system under test; APEX does not install exporters, alter scrape configuration, or write to the TSDB.

This workflow is separate from **APEX platform graphs**, the controller \`/metrics\` export, and the existing collector \`prometheus\` text-scrape pack. Existing sources and imported Grafana dashboards continue to work. They do not automatically become new query sources or acceptance criteria.

### Package boundaries and ownership

\`pkg/prometheus\` owns read-only Prometheus \`/api/v1\` and Azure Managed Prometheus query transport, dial-time DNS/TLS/CIDR enforcement, PromQL parsing, typed whole-token \`{{param.*}}\` and \`{{window.duration}}\` validation, independent capability negotiation, bounded discovery, freshness-companion checks and normalization over \`pkg/analysis\`. It never imports \`pkg/api\`, \`pkg/storage\` or \`pkg/engine\`. \`pkg/analysis\` owns typed evaluation-grid and exact-window semantics, coverage and terminal criteria. \`pkg/api\` and \`pkg/storage\` own project and system-administrator authorization, secret resolution, revisioned connections and operations, immutable run snapshots, execution-owner fencing, content-addressed receipts and canonical heads. \`pkg/collectoragent\` executes the same \`pkg/prometheus\` normalization under local \`--adapter-targets\` origin/identity approval, X25519-sealed credential grants and durable spool replay.

## Set up a connection

1. A project administrator opens **Settings → Prometheus test monitoring** (\`#/settings?setting=prometheus-capture\`). **Monitoring → Telemetry** also provides a connection summary and manage link.
2. Save a disabled connection with a name. Incomplete drafts are permitted. Complete its endpoint, backend profile, authentication, transport policy and execution location before testing it.
3. Use an origin such as \`https://metrics.example.com\`. A reverse proxy prefix belongs in the separate context-path field, for example \`/prometheus\`. Embedded credentials, query strings, fragments, redirects and insecure TLS bypasses are rejected.
4. Select direct controller collection or an enrolled collector. A private endpoint must be verified from the selected execution location. The collector must approve that exact origin locally, advertise \`prometheus-query.v1\`, and retain its enrolled encryption key.
5. Enter logical references to credentials already held in the project's secret provider. Never paste credential values into connection JSON. The connection response shows \`has_*\` flags, omits reference names and private trust details, and leaves credential inputs blank when reopened.
6. **Test saved connection**, then browse metrics, labels, label values, series, target summaries or metadata with bounded discovery. Administrators can browse bounded metric/label catalogs without a selector; use filters to narrow them. Series discovery requires a metric selector, and scoped target discovery uses explicit safe label matchers. Metadata filters select the metric name; metadata is not specific to a series' label values. Discovery retains bounded safe identity fields and metric type/unit metadata. Capability permissions and result truncation appear independently. Preview typed queries against a recent window before enabling the verified revision.
7. Enable the connection after the saved revision passes validation. Transport, authentication or execution-location edits invalidate verification. Revision conflicts return \`409\`; reload before editing again.

Supported authentication is none, bearer token or Basic, with optional mTLS client credentials on HTTPS. Custom CA bundles are bounded and hostname verification remains enabled. Blank credential references preserve saved references only when endpoint, context path, authentication identity and mTLS identity remain compatible. Replacing a Basic username requires its corresponding password; replacing a client certificate requires the matching key.

System administrators approve exact private origins and CIDRs. Direct and collector transports validate DNS at dial time and connect to the validated address. Metadata, loopback, unspecified and link-local destinations are denied. HTTP is an explicit private-network exception with approved CIDRs; bearer/Basic/client credentials are never sent over HTTP. No-auth HTTPS can still be used with network policy.

### Azure Monitor managed Prometheus

Select backend profile \`azure_managed\` and authentication \`azure\`. Use the workspace's HTTPS query origin ending in \`.prometheus.monitor.azure.com\`, with no proxy context path. Choose an explicit workload-identity, managed-identity or service-principal mode. Workload identity requires saved tenant and client IDs matching its federated identity. A service principal also requires a project-secret reference for its client secret. With **Direct from controller**, workload and managed identity authenticate as the controller's own Azure principal, so only a system administrator may create such a connection or change its endpoint, identity mode, client ID, tenant ID or execution location; project administrators may still rename, verify, enable and disable it. Collector execution uses the collector's locally approved identity instead. APEX does not choose an implicit developer login or a default credential chain. Assign **Monitoring Data Reader** on the Azure Monitor workspace to the identity at the saved execution location; controller and collector identities may differ. APEX requests tokens for \`https://prometheus.monitor.azure.com/.default\`. See Microsoft's [query API authentication instructions](https://learn.microsoft.com/en-us/azure/azure-monitor/metrics/prometheus-api-promql).

For Private Link, keep the normal workspace query hostname in the saved endpoint and in the collector's local approval. Configure the selected executor's DNS to resolve it to the private endpoint and approve the exact origin and private CIDRs through APEX trust policy. TLS continues to verify that normal hostname. Use a collector with network access to the private endpoint when the controller cannot reach it. Permit the selected Azure identity's token acquisition path separately. Microsoft's [workspace private endpoint guide](https://learn.microsoft.com/en-us/azure/azure-monitor/metrics/azure-monitor-workspace-private-endpoint) describes the DNS configuration.

Azure's query endpoint does not imply support for upstream Prometheus administrative, discovery or target APIs. Read the connection's independently reported capabilities and use explicit query definitions when a discovery operation is unsupported. See Microsoft's [Prometheus query API guide](https://learn.microsoft.com/en-us/azure/azure-monitor/metrics/prometheus-api-promql).

### Private collector setup

Register a collector in the project, enroll its X25519 public key, and persist the corresponding protected private-key file and spool directory. Save the controller's public signing key and a local adapter-target JSON file. For an Azure workload identity, the local profile must match the saved connection's endpoint, backend, authentication mode, tenant and client IDs; provision the federated token file and \`AZURE_FEDERATED_TOKEN_FILE\` at the collector execution location. The selected identity needs the workspace role described above.

For example, \`adapter-targets.json\` approves one destination and its private address range. Replace the hostname, CIDRs and identity IDs with the operator-approved values. This file contains no credential values:

\`\`\`json
[
  {
    "pack": "prometheus-query",
    "destination": "https://workspace-abcd.eastus.prometheus.monitor.azure.com",
    "prometheus": {
      "endpoint": "https://workspace-abcd.eastus.prometheus.monitor.azure.com",
      "backend_profile": "azure_managed",
      "auth_type": "azure",
      "azure_credential_mode": "workload_identity",
      "azure_tenant_id": "TENANT-ID",
      "client_id": "CLIENT-ID",
      "approved_cidrs": ["10.40.1.0/24"]
    }
  }
]
\`\`\`

Launch the collector with its registration token supplied through \`APEXLOAD_COLLECTOR_TOKEN\` and signing key through \`APEXLOAD_MONITOR_PUBLIC_KEY\`:

\`\`\`bash
apexload-collector-agent \\
  --controller https://apex.example.com \\
  --collector-id COLLECTOR-ID \\
  --packs prometheus-query \\
  --adapter-targets /etc/apex/adapter-targets.json \\
  --credential-key-file /etc/apex/collector-x25519.key \\
  --spool-directory /var/lib/apex/collector-state
\`\`\`

For an ordinary private Prometheus endpoint, use backend \`prometheus\`, its approved authentication mode and matching local transport profile instead. Custom CA material belongs in the local \`prometheus.ca_pem\` configuration. Bearer, Basic, service-principal and mTLS credentials arrive only in sealed assigned grants. Select this collector on the saved connection and test it there before enabling the source. \`--prometheus-url\` configures the older text-scrape pack; query collection uses \`--adapter-targets\`.

## Define a reusable source

In **Monitoring → Telemetry → Run monitor sources**, select \`prometheus-query\`, choose an enabled verified project connection, and define up to 20 queries. The connection fixes the execution location; a source cannot override it with a different collector or endpoint.

Each query has a stable ID and metric name, PromQL expression, declared unit and meaning, retained label allowlist, and minimum expected series count. Choose labels that identify the application, service, job, namespace or node. Sensitive label names, unrestricted retained label sets and unexpected unapproved output labels are rejected; adjust the PromQL projection or approved labels rather than dropping identity silently. Query scope is an administrator responsibility: a project connection grants read access, while the PromQL matchers determine which target data is selected.

**Discover target query presets** checks a bounded administrator metric catalog through the saved connection. CPU, memory, process/request rates, Go/JVM GC and Kubernetes presets appear only when their required metric names are discovered. Kubernetes presets cover pod CPU, working memory, restarts, OOM kills (\`container_oom_events_total\` from the kubelet's cAdvisor), available Deployment replicas, and HorizontalPodAutoscaler desired and current replicas (kube-state-metrics); set the target job to the scrape job of the exporter that serves the metric, such as \`kube-state-metrics\` or \`kubelet\`. kube-state-metrics labels each series with the object's \`namespace\`; when Prometheus scrapes it without \`honor_labels\`, that label becomes \`exported_namespace\` and \`namespace\` names the exporter's own namespace, so edit the preset's matcher before enabling it. The dashboard presets are checked against the controller's query validation through \`pkg/prometheus/testdata/dashboard-presets.json\`; regenerate it with \`UPDATE_PROMETHEUS_PRESET_FIXTURE=1\` when presets change. Catalog presence does not prove those metrics exist for your selected application. Enter the target job and, for Kubernetes, namespace before adding a preset. Templates include explicit scope, declared units, approved output labels and a conservative freshness companion. Review instrumentation meaning and duplicate scrape sources, then preview the scoped query before enabling collection. Truncated discovery leaves undiscovered presets unavailable.

Queries can include up to 16 saved typed bindings through **Typed query bindings**. A \`label_value\` replaces an entire label-value token, for example \`job={{param.target}}\`; APEX quotes and escapes its value. A \`duration\` replaces an entire duration token such as \`[{{param.lookback}}]\`. A \`timestamp\` parses a UTC value such as \`2026-10-03T12:00:00Z\` into a numeric comparison value, for example \`timestamp(up{job={{param.target}}}) > {{param.since}}\`, or a fixed continuous-query anchor such as \`up @ {{param.at}}\`. Request-relative \`@ start()/end()\` anchors are unsupported, and exact terminal windows reject fixed anchors too. Do not put quotes around binding tokens or use bindings for metric names, operators or arbitrary expression fragments. Bindings are static administrator values, bounded and included in the frozen query definition hash; credential values do not belong here.

For example:

\`\`\`json
{
  "id": "request_rate",
  "name": "request_rate",
  "expression": "sum by(job) (rate(http_requests_total{job=\\"checkout\\"}[2m]))",
  "unit": "requests/s",
  "semantics": "rate",
  "allowed_labels": ["job"],
  "min_series": 1
}
\`\`\`

Polling frequency, evaluation step, scrape interval and settling allowance are different quantities. Defaults are a 30-second poll, 15-second evaluation grid and 120-second settling allowance. PromQL range evaluation can repeat a recent scrape value across evaluation times. A successful range query therefore does not establish fresh target samples. An optional companion expression such as \`timestamp(up{job="checkout"})\`, with matching series labels and temporal scope, establishes source freshness; its anchors and offsets must match the main expression. The default allowed sample age is 90 seconds. With no valid companion evidence, freshness remains unknown.

Optional target-health and alert-state capture requires an explicit evidence selector, such as \`{job="checkout",namespace="production"}\`. The selector restricts diagnostic summaries to the monitored scope. API health, query coverage, target health and freshness remain separate. Alert records describe observed pending/firing states; they are not a complete Alertmanager incident history. First/last seen fields record actual sightings. \`no_longer_observed\` means the record was absent from a later successful scoped snapshot; it does not establish an incident's historical resolution time. Raw scrape URLs, unrestricted annotation content and sensitive target labels are excluded.

### Existing Grafana dashboards

On an imported dashboard, select **Create Prometheus monitor source**. Choose an approved project connection and **Resolve dashboard query draft**. The server resolves committed variable bindings, project identifiers and time macros using the existing safe resolver. It records the dashboard source hash, connection revision, original datasource identities, resolved variables, fixed macro lookback and skipped-target warnings. Unsupported, unresolved, unsafe and run-specific \`test_id\` expressions require explicit replacement with reusable target queries. A source is bounded to 20 queries.

Review each unit, semantic declaration, retained label and datasource mapping. All draft expressions will query the newly selected project connection. Dashboard credentials and color thresholds are never copied. Explicit review creates a disabled source; enable and attach it separately. Time macros resolve to a fixed lookback, not a future test duration.

## Attach sources and prepare a test

Editors select approved sources in Run setup or reusable scenario bindings. Omitted per-test \`monitor_bindings\` inherits the scenario's bindings; an empty array disables monitoring for that test; a nonempty array replaces the scenario selection. Mark a source required only when its readiness and retained evidence should gate acceptance.

Preparation verifies connection revision, credentials, capability permissions, budgets, storage readiness and collector enrollment without sending SUT traffic. A preparation freezes its source and connection revisions, resolved bindings and acceptance rules. Changes invalidate unconsumed preparations and affect future tests. Runtime outages update monitoring health while load execution, pause/resume and stop controls continue to work.

Selected-source readiness has a bounded 60-second inspection budget. It probes the approved main, freshness and terminal expressions plus selected target/alert capabilities; unrelated catalog and administrative probes do not consume this check. A selection whose request count cannot fit the configured shared quota and deadline is rejected before network calls. Reduce the number of queries or optional probes when readiness reports a budget failure; the live collector's separate 30-second poll and continuation budgets still apply.

Metrics are correlated by selected labels and the run's UTC time window. They include all activity in that scope, including other production traffic. APEX does not add a run label to Prometheus metrics or infer causation from correlation. Choose queries and labels that suit the intended comparison.

## Live evidence, reconciliation and archives

The run's monitoring section offers source and series selection, units, retained entity labels, source freshness, upstream warnings, evaluation coverage and an overlay against VUsers, TPS, errors or response time. Missing observations remain gaps; explicit finite zero remains zero. Cached values are not repeatedly saved as new observations. Native histogram result documents are omitted with coverage information; select a numeric PromQL aggregate when needed.

When load ends, its terminal status and exact end time are recorded immediately. Monitoring enters **Collecting delayed Prometheus data** and reconciles the original load window, for at most five minutes. Its deadline survives restarts and controller ownership changes. Collection can finish after settled stable successful passes, or become incomplete when its deadline expires. This is an APEX completion criterion, not a guarantee that the upstream backend will never deliver later data.

Archive finalization waits for the durable monitoring dependency. Canonical observations and bounded diagnostics are exported once; late revisions replace a canonical head while immutable receipts remain available for integrity verification. A published archive remains immutable. Archived timelines read these frozen files rather than querying today's Prometheus server. Analysis and comparisons use the same canonical provider observations and retain query-definition identity, units, labels and evaluation step.

Analysis can resample settled evaluations using their grid mean, minimum or maximum at a display interval no finer than the saved evaluation step. Missing grid cells remain gaps. Derived points are labelled \`analysis_bucket\` and retain query provenance separately from the display interval; they cannot enter ingestion or acceptance. Exact-window aggregates remain single results and cannot be spread across a timeline. APEX does not reconstruct rates, totals or percentiles from evaluation levels. Same-series overlays require compatible query definitions, backend profile, statistic, labels and original evaluation step.

The run report's **Baseline Comparison → Resources** view reads frozen canonical Prometheus observations alongside existing resource evidence. It compares the arithmetic mean, minimum and maximum of settled, valid expression evaluations within matched stable load windows. Missing, stale and provisional grid cells reduce coverage; zero remains a measured value. The mean of rolling quantile estimates is labelled explicitly and does not represent a whole-run request percentile. Exact-window summaries and raw counter levels are excluded from this timeline comparison. Compatible comparisons require the same query ID and definition hash, backend profile, statistic, full canonical labels and entity identity, unit and original evaluation step. Source names can change when the full identity remains compatible. Query provenance and coverage accompany shared reports and exports. Prometheus series cannot be combined or remapped through the legacy resource mapping editor; save the intended aggregation in approved PromQL before capture.

## Terminal performance criteria

Use **Monitoring performance criteria** in Run setup. A criterion selects an exact source/query/series, unit, window offsets, comparator, threshold, minimum coverage and \`fail\` or \`warning\` severity. The default minimum is 95% and at least one eligible observation.

\`series_points\` evaluates settled compatible expression results on the frozen grid. Average, minimum and maximum describe those evaluations. They are not request-weighted latency statistics. Raw counter levels remain overlays; a sum over rolling rates or cumulative counters is not a run total. Quantile rules describe the minimum, maximum or mean of grid quantile estimates, and never a whole-run request percentile.

For a run-window aggregate, supply an administrator-approved terminal expression such as \`sum(increase(http_requests_total{job="checkout"}[{{window.duration}}]))\`, declare its compatible unit/meaning, and choose \`exact_window\`. Every vector dependency must use exactly one full-window range; fixed ranges, nested ranges, fixed timestamps and offsets are rejected because they extend or shift the frozen interval. A single full-window subquery over an instant selector is supported. Only the supported duration token is substituted; APEX evaluates the expression at the specified window end and records that exact window. Prometheus range-function extrapolation and lookback semantics still apply. APEX does not reconstruct whole-run percentiles by combining per-step quantiles.

Enable **Require known target sample freshness** when unknown or aged source samples must make a criterion unevaluable. Conclusive failures fail acceptance. Missing evidence for a fail-severity rule, or incomplete required monitoring, produces an **Inconclusive** report. Optional capture problems without fail criteria are warnings. Criteria are evaluated after collection finishes and never participate in load control or early stopping.

CI remains pending during reconciliation (\`monitoring_pending\`). Missing required evidence closes the check as \`monitoring_incomplete\` with an evidence explanation; it cannot produce a passing check. The performance report retains its Inconclusive verdict.

## CLI, MCP and API

Controller-managed runs use the normal test and preparation APIs with \`monitor_bindings\` and \`goal.monitor_rules\`. Standalone engine-only \`apexctl run\` does not launch target-query capture.

\`\`\`bash
apexctl prometheus create --project PROJECT --file disabled-connection.json
apexctl prometheus test --project PROJECT --id CONNECTION --wait
apexctl prometheus discover --project PROJECT --id CONNECTION --kind metrics --selector '{job="checkout"}' --wait
apexctl prometheus preview --project PROJECT --id CONNECTION --file query-preview.json --wait
apexctl prometheus adopt-dashboard --project PROJECT --id CONNECTION --dashboard DASHBOARD
\`\`\`

Connection update JSON includes its current \`revision\`. Delete requires \`--revision\`. Test/discovery/preview return an operation ID; use \`--wait\` or the \`operation\` command. These operations always use the saved location. Preview windows are bounded to ten minutes. The adoption command returns a draft and creates no source.

MCP tools are \`prometheus_connection_list/create/get/update/delete/test\`, \`prometheus_discover\`, \`prometheus_preview\`, \`prometheus_operation_get\`, and \`prometheus_dashboard_adopt\`. Existing generic source, binding, monitor-series, monitor-evidence and archive tools provide the rest of the workflow. \`prometheus_metrics\` remains the APEX platform export surface.

The generated [OpenAPI specification](swagger.yaml), [endpoint catalog](api-endpoint-catalog.json), and the archived implementation specification (\`docs/archive/plans/PROMETHEUS_MONITORING_SPEC_2026-10.md\`) are authoritative references.

## Limits, troubleshooting and qualification

Defaults and upper bounds protect the controller, collector and upstream backend: 20 queries/source, 200 expanded series/source, 1,000 series/run, 32 sources/run, 60 requests/minute per endpoint/authentication identity, two concurrent upstream requests, a 10-second request deadline, a 30-second poll budget and a 4 MiB decompressed response. Existing archive budgets also apply. Batch bounds remain 1 MiB, 512 measurements and 100 diagnostic records. Coverage indicates truncation, omissions and budget exhaustion.

Narrow labels and expressions when cardinality, memory or query budgets are exceeded. Authentication/permission errors require configuration changes. Throttling and transient transport failures retry within the shared budget; an expensive expression rejected for data-point/sample limits requires smaller query windows or a narrower expression. API success with no data is distinct from fresh zero-valued results. Check target health and freshness companions when dashboards look flat or delayed.

Direct private access requires approved trust policy and durable shared artifacts for HA. Collector mode additionally requires persisted signing configuration shared across controller replicas, enrolled encryption keys and local destination approval. Direct-only capture does not need an unused collector signing key. Credentials resolve in memory or are sealed into short-lived assigned collector grants.

The SDK query/API contract has real disposable-server evidence for Prometheus **2.55.1** and **3.13.0** on Linux/arm64, alongside fixture tests. Production private-network deployments and Azure workspaces remain qualification prerequisites. The exact image digests, observed capabilities and explicit limits are recorded in \`pkg/prometheus/testdata/compatibility-matrix.json\` and \`docs/archive/plans/PROMETHEUS_MONITORING_SPEC_2026-10.md\`.
`;export{e as default};