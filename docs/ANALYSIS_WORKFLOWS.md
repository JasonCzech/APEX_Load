# Run monitoring, preparation, and Parquet analysis

These workflows are explicitly enabled per project. Existing runs, clients, and
archives remain readable without them. The generated [API specification](swagger.yaml)
is the authoritative route and request contract.

## Choose a workflow

| Reference | Owns |
| --- | --- |
| [Monitoring and preparation](MONITORING_AND_PREPARATION.md) | Collector trust, run bindings, traffic-free preparation, capacity and messaging sources |
| [Native analytics](NATIVE_ANALYTICS.md) | Parquet import, measurement identities, queries, panels and resource limits |
| [Root-cause analysis](ROOT_CAUSE_ANALYSIS.md) | Validity, budgets, onset, anomalies, graphs, hypotheses and service contracts |
| [Baseline comparison](BASELINE_COMPARISON.md) | Saved run comparisons and workload qualification |

## Enable the workflows

Set comma-separated project IDs on every controller replica:

```text
APEXLOAD_RUN_MONITOR_PROJECTS=project-id
APEXLOAD_PREPARATION_PROJECTS=project-id
APEXLOAD_ANALYSIS_PROJECTS=project-id
```

Native Parquet operations additionally require `CGO_ENABLED=1` and the
`analytics_duckdb` build tag (`make controller-analytics`). The pinned driver is
`github.com/duckdb/duckdb-go/v2`; published controller images use
`Dockerfile.controller`, and desktop packaging includes the tag. The ordinary
Go build and `Dockerfile` preserve the CGO-free profile, whose capabilities
response explicitly reports that native Parquet is unavailable. Generator and
collector builds do not import DuckDB. Native build/test lanes cover Linux
amd64/arm64, macOS arm64 and Windows amd64; a CI lane is not evidence that its
current run has passed.

DuckDB databases are process-local, disposable caches. SQLite/PostgreSQL owns
metadata, revisions and publication state. The configured archive backend owns
original and normalized artifacts. HA replicas never share a writable DuckDB
database. Queries use approved operations with extension installation/loading
disabled and file access limited to their protected staging directory.


## Section navigation

Previous section bookmarks remain below. Repository links point directly to the focused references.

<a id="bind-monitoring-to-a-run"></a>

- [Bind monitoring to a run](MONITORING_AND_PREPARATION.md#bind-monitoring-to-a-run)

<a id="prepare-before-start"></a>

- [Prepare before Start](MONITORING_AND_PREPARATION.md#prepare-before-start)

<a id="import-and-compare-parquet"></a>

- [Import and compare Parquet](NATIVE_ANALYTICS.md#import-and-compare-parquet)

<a id="analysis-policies-and-run-pins"></a>

- [Analysis policies and run pins](NATIVE_ANALYTICS.md#analysis-policies-and-run-pins)

<a id="resource-and-acceptance-limits"></a>

- [Resource and acceptance limits](NATIVE_ANALYTICS.md#resource-and-acceptance-limits)

<a id="collector-adapter-configuration"></a>

- [Collector adapter configuration](MONITORING_AND_PREPARATION.md#collector-adapter-configuration)

<a id="qualified-diagnostics-and-dependency-evidence"></a>

- [Qualified diagnostics and dependency evidence](ROOT_CAUSE_ANALYSIS.md#qualified-diagnostics-and-dependency-evidence)

<a id="run-validity"></a>

- [Run validity](ROOT_CAUSE_ANALYSIS.md#run-validity)

<a id="root-cause-analysis-budgets"></a>

- [Root-cause analysis budgets](ROOT_CAUSE_ANALYSIS.md#root-cause-analysis-budgets)

<a id="root-cause-onset-detection"></a>

- [Root-cause onset detection](ROOT_CAUSE_ANALYSIS.md#root-cause-onset-detection)

<a id="browser-baseline-anomalies"></a>

- [Browser baseline anomalies](ROOT_CAUSE_ANALYSIS.md#browser-baseline-anomalies)

<a id="root-cause-graph"></a>

- [Root-cause graph](ROOT_CAUSE_ANALYSIS.md#root-cause-graph)

<a id="root-cause-hypotheses-and-capacity"></a>

- [Root-cause hypotheses and capacity](ROOT_CAUSE_ANALYSIS.md#root-cause-hypotheses-and-capacity)

<a id="root-cause-evaluation"></a>

- [Root-cause evaluation](ROOT_CAUSE_ANALYSIS.md#root-cause-evaluation)

<a id="root-cause-analysis-service"></a>

- [Root-cause analysis service](ROOT_CAUSE_ANALYSIS.md#root-cause-analysis-service)

<a id="business-keys"></a>

- [Business keys](NATIVE_ANALYTICS.md#business-keys)

<a id="preparation-capacity-policies"></a>

- [Preparation capacity policies](MONITORING_AND_PREPARATION.md#preparation-capacity-policies)

<a id="approved-cloud-metric-collectors"></a>

- [Approved cloud metric collectors](MONITORING_AND_PREPARATION.md#approved-cloud-metric-collectors)

<a id="response-properties-and-native-panels"></a>

- [Response properties and native panels](NATIVE_ANALYTICS.md#response-properties-and-native-panels)

<a id="saved-panel-revisions-and-pinned-inputs"></a>

- [Saved panel revisions and pinned inputs](NATIVE_ANALYTICS.md#saved-panel-revisions-and-pinned-inputs)

<a id="filtered-archive-panels-and-comparisons"></a>

- [Filtered archive panels and comparisons](NATIVE_ANALYTICS.md#filtered-archive-panels-and-comparisons)

<a id="live-and-grouped-evidence-panels"></a>

- [Live and grouped evidence panels](NATIVE_ANALYTICS.md#live-and-grouped-evidence-panels)

<a id="merged-live-publication-buckets"></a>

- [Merged live publication buckets](NATIVE_ANALYTICS.md#merged-live-publication-buckets)

<a id="dimensions-timeline-annotations-and-transaction-investigation"></a>

- [Dimensions, timeline annotations and transaction investigation](NATIVE_ANALYTICS.md#dimensions-timeline-annotations-and-transaction-investigation)

<a id="export-displayed-charts"></a>

- [Export displayed charts](NATIVE_ANALYTICS.md#export-displayed-charts)

<a id="retained-observation-rates"></a>

- [Retained observation rates](NATIVE_ANALYTICS.md#retained-observation-rates)

<a id="shared-metric-and-operation-catalog"></a>

- [Shared metric and operation catalog](NATIVE_ANALYTICS.md#shared-metric-and-operation-catalog)

<a id="executable-derivations-in-queries"></a>

- [Executable derivations in queries](NATIVE_ANALYTICS.md#executable-derivations-in-queries)

<a id="event-hubs-processor-progress-analysis"></a>

- [Event Hubs processor progress analysis](MONITORING_AND_PREPARATION.md#event-hubs-processor-progress-analysis)

<a id="exact-messaging-management-evidence"></a>

- [Exact messaging management evidence](MONITORING_AND_PREPARATION.md#exact-messaging-management-evidence)

<a id="kafka-management-source-configuration"></a>

- [Kafka management source configuration](MONITORING_AND_PREPARATION.md#kafka-management-source-configuration)

<a id="service-bus-management-source-configuration"></a>

- [Service Bus management source configuration](MONITORING_AND_PREPARATION.md#service-bus-management-source-configuration)

<a id="solace-management-source-configuration"></a>

- [Solace management source configuration](MONITORING_AND_PREPARATION.md#solace-management-source-configuration)
