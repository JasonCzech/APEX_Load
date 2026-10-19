# Root-cause analysis contracts and qualification

Run validity, evidence budgets, onset detection, browser anomalies, graph construction, hypotheses and service boundaries. These contracts preserve the distinction between observations, advisory inference and qualified causal evidence. Start with [analysis enablement](ANALYSIS_WORKFLOWS.md#enable-the-workflows); imported data and panels are described in [native analytics](NATIVE_ANALYTICS.md).

## Contents

- [Qualified diagnostics and dependency evidence](#qualified-diagnostics-and-dependency-evidence)
- [Run validity](#run-validity)
- [Root-cause analysis budgets](#root-cause-analysis-budgets)
- [Root-cause onset detection](#root-cause-onset-detection)
- [Browser baseline anomalies](#browser-baseline-anomalies)
- [Root-cause graph](#root-cause-graph)
- [Root-cause hypotheses and capacity](#root-cause-hypotheses-and-capacity)
- [Root-cause evaluation](#root-cause-evaluation)
- [Root-cause analysis service](#root-cause-analysis-service)

## Qualified diagnostics and dependency evidence

Enable `APEXLOAD_DIAGNOSTICS_PROJECTS` with a comma-separated project allowlist.
Live TPS, average response time, and error percentage are compared with 30
consecutive buckets having the same VUser count, load levels, and interval.
Staleness, missing buckets, telemetry drops, and pauses break qualification.
Zero-variance baselines remain unscored. Three-standard-deviation findings and
repeated error patterns are advisory; explicit SLA stop rules are unchanged.
Findings flow through existing incident history/webhook delivery. Reports derive
the same statistical evidence from retained interval summaries.

Set a source's `service` to the exact dependency label displayed by the run.
Dependency drilldown returns one actual interval with monitor coverage and
selectable metric links. It never averages percentile values or treats an
association as proof of cause. Missing and bounded-scan coverage are visible.

## Run validity

Every archived run report carries `validity` (rules `validity.v1`): whether the
load generators and telemetry could carry the load the run reports. It is one
of `valid`, `suspect`, `invalid` or `unknown`, with the rules behind it, the
intervals that were not valid and any limitations.

| Verdict | Rule (first values, recalibrated by the evaluation corpus) |
| --- | --- |
| `suspect` | A generator held CPU at or above 85%, lost 10% or more to steal, or was throttled in 10% or more of its CFS periods, for three consecutive intervals |
| `invalid` | A generator container was OOM-killed, more than 0.1% of an interval's results were dropped, an open model shed arrivals, or test data ran out |
| `unknown` | Generator statistics are missing for more than 10% of judged intervals, a generator's archive was not collected, or no intervals were archived |

- **Dropped results** are counted per protocol result, so their share is
  taken against the interval's archived result rows (or its completed
  transactions, when larger). The counters are cumulative; only a rise above
  their previous high counts, because a distributed run's fleet total dips
  when one generator's batch is late.
- **Missing statistics** count inside the span each generator reported over,
  so a generator added or removed mid-run is not missing outside it. An
  expected generator that never reported is missing throughout.

- **Judged intervals.** The verdict is the worst among intervals that overlap
  a measured steady level (the same plateaus baseline comparison uses, after
  their 30-second settling): steady VUsers for a closed model, a steady target
  arrival rate for an open model, and both for a mixed run. With no steady
  level, every interval is judged and `no_steady_level` is listed. Saturation
  must be sustained within judged intervals, so a ramp does not count.
- **Generators.** CPU is the higher of process CPU against allocated cores and
  container CPU against its limit. Distributed archives name each resource
  sample's generator (its target) when generator archives merge, so the rules
  hold per generator and an expected generator that never reported counts as
  missing. Archives merged before this are judged as one pool by the worst
  sample, with `generator_identity_unrecorded`.
- **Throughput.** An open model that misses its arrival target while its
  generators are healthy is an `open_model_shortfall` anomaly, not invalidity.
  A closed model whose throughput falls by more than 10% while latency rises by
  more than 10% at one steady level is a `closed_model_slowdown` anomaly: a
  system-under-test outcome, and the run stays valid.
- **Limitations** name evidence a rule could not use, such as throttle periods,
  dropped results or shed arrivals that the run did not record. Data-pool
  exhaustion is not recorded yet, so every verdict lists
  `data_exhaustion_unrecorded`.
- **Root-cause summary.** The verdict replaces the peak generator-CPU rule:
  `suspect` is a warning resource finding, `invalid` a critical one, and every
  verdict appears as the `Run validity` signal.
- **CI.** A CI run reports the verdict as `validity`. It stays advisory unless
  the project's Actions policy sets `validity_gate` to `invalid` or `suspect`;
  a run the gate fails is `run_invalid` (see
  [Run validity gate](CICD_INTEGRATION.md#run-validity-gate)).

Archived intervals are about one second long, so three consecutive intervals
is a short window; the evaluation corpus (WP3.9) recalibrates the rules under a
new version.

## Root-cause analysis budgets

The v2 root-cause engine (`pkg/rca`) runs every analysis inside a budget
(`rca.DefaultBudget`):

| Limit | Default |
| --- | --- |
| Series | 2,000 |
| Points per series (`MaxSeriesPoints`) | 10,000 |
| Exemplars | 20,000 |
| Graph nodes / edges | 5,000 / 20,000 |
| Wall time | 120 s |
| Heap growth | 512 MiB |

Exceeding a limit never fails the analysis and never cuts evidence silently:
the engine keeps what the limit allows and adds a `budget_truncated`
limitation naming the limit, for example `series: 2000 of 2500`. Wall time and
heap are checked before each stage; a stage that would start over budget is
skipped and reported the same way, while a stage that finishes over time keeps
its result and is not reported as truncated. A stage is not interrupted part
way: validity is linear (about 0.2 µs per resource sample), so it reaches the
wall limit only on inputs of hundreds of millions of rows. The heap limit
applies to the live heap's growth since the analysis started, measured
process-wide, so two concurrent analyses see each other's growth. Analysis
jobs run in the controller process, at most two at a time (see the
root-cause analysis service below); they do not take the native analytical
pipeline's single slot, whose limits govern DuckDB imports and queries.

An assessment judges acceptance (the run's SLA verdict) and validity, lists
the anomalies onset detection finds, builds the run's graph, ranks the
hypotheses that explain the anomalies and reports capacity (all below). Each
stage starts only within the wall-time and heap budget; a skipped stage is
listed as `stage_skipped`.

Baselines (Apple M4, Go 1.26.5, darwin/arm64, 2026-10-05, load average 4–6).
`BenchmarkRCAAnalyze` measures acceptance and validity, where a row is one
generator resource sample from ten generators at one steady level;
`BenchmarkRCADetect` measures onset detection over six hours of 10-second
buckets at one level, a quarter of the series stepping halfway:

| Benchmark | Time per analysis | Allocated | Allocations |
| --- | --- | --- | --- |
| Analyze, 10,000 rows | 0.9–2.1 ms | 0.67 MB | 73 |
| Analyze, 1,000,000 rows | 95–220 ms | 67 MB | 888 |
| Detect, 48 series × 2,160 buckets | 17 ms | 6 MB | 1,654 |
| Detect, 384 series × 2,160 buckets | 136–151 ms | 48 MB | 13,484 |

The upper analysis times are from a heavily loaded machine (load average
about 15). Allocation does not vary with load.

Rerun with `go test ./pkg/rca -run '^$' -bench BenchmarkRCA -benchtime 5x`.

## Root-cause onset detection

Onset detection (`pkg/rca/detect.go`, parameters `detect.v2` in
`detect_params.go`, recorded in each assessment's `detection` block) finds
where a series changed and whether it recovered. It reads only frozen
evidence.

- **Series.** Per transaction: the interval p95, error rate and throughput
  from `interval_distributions.jsonl`. Archives without that sidecar are
  rebuilt from their rows within bounds (`interval_distributions_reconstructed`).
  A run whose scripts authored no business transactions is judged on its
  actions too (`actions_judged`), beside the automatic per-iteration
  transaction the engine records under each script's name, so a slow request
  is named rather than only a slow iteration; an authored transaction takes
  precedence. A transaction absent from a complete interval completed
  nothing in it, so its throughput there is zero. Provider series come from the
  archived monitor points, one per measurement series identity
  (`analysis.MeasurementSeriesIdentity`, `apexload.series_identity.v1`) on the
  observation mode and grid the provider declares. Entity IDs are not part of
  the identity, except a provider's own series discriminator (Prometheus's
  label-set hash); a series collects them as graph nodes. A point recorded
  without a project belongs to the run's, and a point whose identity fields
  are incomplete keeps the earlier provider/source/metric/query/statistic/
  dimension key with no identity. Interval and snapshot providers are compared
  bucket to bucket. Counters and cumulative providers are excluded
  (`cumulative_series_excluded`), and points from providers that declare no
  observation semantics are skipped (`monitor_semantics_unknown`). Every
  transaction and action series also carries the identity of its MetricPoint
  projection; its readable ID is unchanged. Equal identities name the same
  measurement but never by themselves authorize a baseline comparison, which
  still needs a qualified baseline run.
- **Joint and imported series.** Joint interval evidence (version 3) becomes
  series on its exact `[action, network profile, hierarchy, phase]` tuple
  (`action_dimensions/<tuple>/p95_ms`, `error_rate`, `throughput`), measuring
  the tuple's action; intervals recorded without joint evidence are not zeros,
  and no slice is synthesized from marginals. A ready analysis dataset of the
  run's project whose run association names the run contributes `imported`
  series, one per metric and identity its partition manifest records over the
  run's span, read through one bounded native query each (at most 10,000
  points, the mean per bucket, a counter as its rate; histograms and
  summaries are not read). A dataset sampled more coarsely than the run's
  interval grid is regridded to its own interval, so detection still needs
  ten of those buckets before an onset. Imported series count as external
  evidence for hypotheses, like providers, and name their host as a node.
  Partitions that mix metrics or identities, and histogram or summary
  measures, are not enumerated (`imported_series_unenumerated`); empty
  partitions are skipped; native analysis that is off or unavailable for the
  project is `imported_dataset_unavailable`. Imported reads use at most a
  quarter of the analysis's wall-time budget and 128 native queries, so the
  run's own evidence is always judged (what they leave out is
  `detection_evidence_cut`). A read the single native-analysis slot refuses
  is retried with backoff, and concurrent analyses queue their reads in
  arrival order. A slot still busy at the read budget before any imported
  series was read fails the analysis, which an identical request retries,
  rather than completing it without that evidence; once some were read, what
  the budget leaves is `detection_evidence_cut`, and so is a read still
  running at the budget even when nothing was read (retrying cannot make it
  faster). Other admission failures (a build without native analysis,
  resource limits, an invalid resource configuration) stop the imported
  reads with one `imported_dataset_unavailable` limitation. A dataset is checked again after its reads and dropped
  (`imported_dataset_changed`) if it changed or lost its association
  meanwhile. Joint slices rank last within the series budget: imported and
  document series take the room they hold, and under the anomaly cap their
  anomalies never displace those of a series that can be a symptom or cause.
- **Provider series IDs** pair a run's series with a baseline run's. They
  leave out the scenario the run was launched from (the series' identity keeps
  it), so a copied scenario or an ad-hoc run still pairs. They keep the host
  that collected the series, so a provider source moved between collector and
  direct collection, or to another collector, starts new series that do not
  pair with the baseline's; a provider's own
  series discriminator (Prometheus's label-set hash) is part of both, so
  series that the allowed labels alone would merge stay apart.
- **Browser document series.** Each measurement tuple of the run's final
  FCP, LCP, CLS and INP document measurements (one algorithm, unit,
  lifecycle phase, frame scope, engine, device profile and actual runtime
  cohort, within one group and region) becomes a `browser_document` series
  (`browser_document/<metric>/<tuple digest>/p75`): the p75 of the final
  measurements whose window closed in each bucket of the run's interval
  grid. Each measurement counts once, at its latest retained state;
  provisional and censored measurements never enter, and a bucket without a
  final document is a gap. The tuple is part of the measurement identity, so
  a baseline-run judgment pairs only the same tuple. Layout shift (`score`)
  is judged on a robust scale never below 0.01 (`score_floor`), so changes
  well below it build no onset. Incomplete document evidence is
  `detection_evidence_cut`.
- **Levels.** Only buckets inside a steady load level are judged: the same
  settled spans validity and baseline comparison use, which skip each level's
  first 30 s. Ramps and pauses are never judged; a run with no steady level
  gets `load_levels_unavailable` and no anomalies. Levels whose load is within
  5% are the same level.
- **Two baselines.** A `time` anomaly compares each bucket with earlier buckets
  at the same load in this run. The baseline is the level's first ten buckets,
  growing to 120 while nothing is alarming, so a slow drift is measured against
  where the level began; buckets from an onset on never join it. A `load`
  anomaly compares the level with the baseline run's buckets at the same level,
  when `baseline_test_id` names a run whose workload qualification is not
  `unqualified`. A level with fewer than ten baseline buckets is not judged
  (`insufficient_baseline`).
- **Statistic.** Robust z = (x − median) / max(1.4826 × MAD, 5% of the median,
  unit floor): 5 ms of latency, 0.5 points of error rate, 0.1 requests per
  second of throughput, 0.01 of layout shift score (`score_floor`), and for
  providers the unit's latency or percentage floor. Two-sided CUSUM with k = 0.5 and h = 5. An onset needs three
  consecutive buckets beyond k while the alarm is on, so noise just before a
  change cannot complete it. The onset is the maximum-likelihood change point
  since the CUSUM last left zero. Its uncertainty spans every change point
  within two log-likelihood units, plus one bucket and the series' clock
  uncertainty (WP2.2). Three in-band buckets end an anomaly as `recovered`. A
  single bucket that alone reaches the alarm, with an in-band bucket after it,
  is a spike: listed under `detection.spikes`, never an onset. A gap of more
  than three buckets resets the CUSUM.
- **Anomalies** carry a stable ID, series, onset, end, uncertainty, scope,
  direction, level, effect (median minus baseline median, in the series' unit),
  peak z, baseline and an evidence reference to the series window. At most 500
  anomalies and 200 spikes are kept, the largest first (`anomalies_truncated`,
  `spikes_truncated`).
- **Evidence limits.** A sidecar that cannot be read costs its series, not
  the analysis (`detection_evidence_unreadable`). Transaction series take the
  series budget first, provider series then, imported and browser document
  series and joint action-dimension slices what remains (joint slices last); what the budget or the analysis budget
  leaves out is `detection_evidence_cut`. A baseline run
  none of whose series matches one of the run's is `baseline_series_unmatched`,
  and the limits of the baseline's own evidence are listed in its `baseline`
  coverage.
- **Live findings.** The run's live 3σ findings stay as they are, labelled
  "advisory (live)" in the run summary.

Measured on synthetic 10-second series with noise of one standard deviation,
500 trials each: a step of 3σ is placed exactly in 82% of trials and within
one bucket in 94%; 5σ, 97% and 99%; 10σ, 99.6%. Healthy one-hour series
produce about 0.24 false onsets per series-hour. That is inherent to k = 0.5 and
h = 5, which alarm about once per 465 buckets even with a perfect baseline.
Raising h to 8 would cut false onsets to about 0.03 while missing no more 3σ
steps (about 2% at either setting).
The fault corpus (WP3.9) calibrates these values; until then they are the
plan's starting values.

## Browser baseline anomalies

An assessment of a run with browser document evidence also carries
`browser_baseline` (`pkg/rca/browser_baseline.go`, version
`browser_baseline.v1`). It compares each browser document cohort with the same
cohort of the workload-qualified baseline run, at the same steady load. Only
it compares browser document series with the baseline: the general detector
judges them within the run, so the two never disagree about one cohort.

The run's pinned `browser_policy` applies here: its effect floors raise the
defaults below (never lower them), and its baseline mode decides whether a
comparison is made. `none` turns it off (`policy_baseline_off`), `pinned_run`
compares only when the analysis's baseline is the pinned run
(`policy_pinned_run` when it compares with another run; without a qualified
baseline the section says `no_qualified_baseline`), and `latest_compatible`
uses the analysis's baseline run. The section names the policy (`policy`, id@revision). A pinned
revision that is missing, no longer matches its pin or is invalid leaves the
defaults (`policy_unavailable`, `partial`); a store that cannot read it fails
the analysis, which can be retried.

- **Cohorts.** A cohort is one exact measurement tuple: metric and algorithm
  version, engine, device profile, scenario group and region, frame scope,
  lifecycle phase and the actual runtime cohort. The runtime cohort covers
  browser version, platform, viewport, device emulation, CPU and network
  shaping, and cache and new-user policy. Document, navigation and measurement
  IDs are lineage only, never matching keys. Each document counts once, at its
  latest retained measurement, so a republished observation adds nothing.
  Documents become per-bucket p75 values before any comparison.
- **Scoring.** The run's value at a level is the median of its bucket p75
  values there. It is scored with the three-sigma scorer of
  `pkg/analysis/anomaly.go` (`ThreeSigmaScore`) against the baseline's valid
  buckets at every steady level with the same load. A cohort needs at least
  30 such buckets. An anomaly needs |z| ≥ 3 and a practical effect: at least
  max(50 ms, 10% of the baseline mean) for a timing, 0.02 for layout shift,
  or a catalog floor for any other metric.
- **Results.** Each result is `anomaly` (with its direction), `within_range`
  or `unscored`. An unscored result has a reason:
  - `insufficient_baseline`: fewer than 30 buckets;
  - `zero_variance`;
  - `no_same_load_window`;
  - `no_baseline_cohort`;
  - `cohort_changed`: the baseline measured the same measurement under another
    runtime cohort, and the result's detail names the fields that differ from
    the closest baseline cohort, such as `shaping_applied`. A result is also
    `cohort_changed` when its scenario group ran a different or unrecorded
    script in the baseline (`script_changed`, `script_unrecorded`: only that
    group's cohorts), or when the run's configuration or workload changed or
    was not recorded (`config_changed`, `config_unrecorded`,
    `workload_unrecorded`: every cohort), since the group then no longer
    stands for the same authored pages;
  - `baseline_evidence_unavailable`: the baseline's document evidence was not
    fully read (unreadable, dropped observations or cut by the series
    budget), so its matching cohort may be missing; the section is then
    `partial`.

  When the run's own document evidence was not fully read (dropped
  observations or series the budget left out), the section is `partial` with
  `run_evidence_incomplete`, and a cohort left out has no result (`unavailable`
  when none was read); when it could not be read at all, the section is
  `unavailable` with `run_evidence_unavailable` rather than missing.

  Every result carries current and baseline bucket and document counts, the
  baseline run with its qualification and reasons, its own evidence
  reference, the level's start (two steady levels at one load stay apart),
  and `source_loss` or `baseline_source_loss` when that run's document
  evidence dropped observations. A scored result also carries the expected
  range, the baseline's evidence reference, z and the effect. The expected
  range is the baseline mean ± the larger of 3σ and the effect floor (never
  below zero), so a value inside it is within range and one outside it is an
  anomaly.

  Buckets outside every steady level (ramp, pause, settle) are counted under
  `excluded.outside_steady_load`. Without a qualified or partial baseline run,
  or without steady levels, the section is `unavailable` with that
  limitation. At most 200 results are kept, anomalies first; an assessment
  trimmed to fit its size limit cuts these advisory results before its graph,
  anomalies or hypotheses, and says so (`browser_baseline_truncated`).
- **Advisory.** Browser baseline results never affect hypotheses, CI outcomes
  or stop decisions; application SLAs own those. The RCA panel lists them
  under **Browser baseline**.

## Root-cause graph

Each assessment carries the run's typed graph (`graph`, schema
`apexload.rca_graph.v1`, built by `pkg/rca/graph.go`) from frozen evidence:
the archived rows, the monitor jobs' archived topology, the archived monitor
points and the mapping decisions frozen into the evidence revision.

| Node | From |
| --- | --- |
| `transaction`, `action` | Archived rows; IDs match the detection series (`transaction:<name>`, `action:<name>`) |
| `dependency` | The dependency type and target an action reached, with the target's host |
| `monitor_source` | Each monitor job bound to the run |
| `provider_entity` | Dynatrace entities (classic and Smartscape), AppDynamics tiers, nodes and business transactions |
| `infrastructure` | Hosts, processes, containers, pods, nodes and queues, from provider entities or monitor point dimensions |

| Edge | Meaning | Confidence |
| --- | --- | --- |
| `calls` | An action reached a dependency; a provider entity calls another (Smartscape, classic `calls`) | explicit (rows) or provider |
| `contains` | A transaction encloses an action; a tier its nodes; an entity its instances | explicit or provider |
| `shares_infrastructure` | An entity runs on an infrastructure node; entities on the same node share it | provider |
| `monitors` | A monitor source measured an entity or infrastructure node | provider |
| `maps_to` | A dependency is a provider entity | explicit (approved) or inferred |

- **Approval.** A dependency whose host is a provider entity's name (including
  provider-reported hosts and processes) gets an inferred `maps_to` edge with
  `used: false`: listed, never walked, and counted in
  `graph_unapproved_mappings`. Hosts and pods seen only in point dimensions
  are not candidates, since no decision can name them. Several candidates are
  all kept and marked `ambiguous` (`graph_ambiguous_mappings`). An operator
  records a decision as an `rca_mapping` definition at
  `/api/v1/analysis/rca-mappings?project_id=…` (GET, POST; GET, PUT and
  DELETE by ID; editor to write, viewer to read; available where the project
  computes root-cause analyses) with `{dependency, provider, entity_id,
  entity_name, decision}`, where `dependency` is the dependency label as the
  graph shows it and `decision` is `approved` or `rejected`. Approved
  decisions become explicit edges carrying the definition's ID and
  revision; rejected ones remove the candidate. Decisions are revisioned,
  apply to every run of the project, and have their own allowance of 1,000
  per project apart from saved mappings and comparisons. Dynatrace entities
  collected through Smartscape also answer to their classic IDs, which points
  and classic relationships use.
- **Freezing.** An evidence revision freezes the project's decisions and
  their digest, which is the analysis's `mapping` input. A decision changed
  after the latest revision makes the next POST capture a new one, so the
  same request is a new analysis.
- **Cycles** among used `calls` edges are found (Tarjan) and listed under
  `graph.cycles`; they are allowed.
- **Bounds.** The graph keeps 5,000 nodes and 20,000 edges (the run's own
  nodes first, explicit edges before provider and inferred ones) and at most
  1 MiB; a run contributes at most 20,000 distinct calls and 1,000 observed
  entities and infrastructure nodes per source (`graph_truncated`,
  `graph_evidence_cut`). The bundle drops unused edges first.

## Root-cause hypotheses and capacity

Hypotheses (`pkg/rca/hypotheses.go`, signatures `signatures.v1` in
`pkg/rca/signatures/`, one file each) explain symptoms: rising latency or
errors, or falling throughput, on a transaction or action series.

- **Evidence kinds.** Provider series are classified by keywords in their
  metric names (`signatures.Classify`): CPU, memory, GC, pool, consumer lag,
  rebalances, dead letters, retries, scaling (replicas, restarts, OOM kills,
  cold starts), database wait, and the queue depth, arrival rate and
  residence time Little's law reads. The run's own DNS, connect and TLS time
  per dependency, averaged per interval over the requests that spent any,
  become `dependency/<label>/dns_ms`, `connect_ms` and `tls_ms` series and are
  detected like any other; the baseline run's are built the same way, so
  they are compared at the same level too.
- **Window and path.** For each symptom, the anomalies of the same scope that
  began within five minutes before its onset and before its end are
  candidates. The graph is walked from the symptom's transaction or action
  through used `calls`, `contains`, `maps_to` and `shares_infrastructure`
  edges (eight hops at most); evidence on a reached node, or on one of its
  aliases, is connected. Only series detection judged in the symptom's scope
  count as measured: a series it cut, excluded or could not baseline says
  nothing by staying quiet.
- **Ordering.** A cause precedes a symptom only when the symptom began later
  by more than both onsets' uncertainty; otherwise they are unordered, and a
  cause that began later by more than that contradicts. An onset within its
  uncertainty of a level's settle period is `load_coincident`.
- **Signatures.** `resource_pressure`, `garbage_collection`,
  `pool_exhaustion`, `consumer_lag`, `rebalance`, `dead_letters`, `retries`,
  `cold_start`, `database_wait` and `network_phase`. Each names its required
  evidence kinds, the indicators that support it, its competitors and its
  falsification check. With none of its required kinds measured it lacks
  evidence, and one that cannot explain the symptom's kind is not
  applicable. Each indicator that moved the right way no later than the
  symptom adds weight: more for a strong anomaly, half when unconnected, 0.6
  when unordered. One that followed the symptom subtracts 0.5. When nothing
  supports the signature, each indicator measured on the path that stayed in
  band subtracts 1 (0.25 off the path). A signature is supported with any
  support and a positive score. A cold start scores higher when the symptom
  began at a load step.
- **Hypotheses** carry the symptoms they explain (one cause behind several
  symptoms, or seen both over time and against the baseline, is one
  hypothesis: the same signature on the same evidence series), score, rank,
  chain of onsets, the supporting
  and contradicting evidence, the other supported signatures for the same
  symptoms, the falsification check, limitations naming competitors that
  could not be judged, and the graph nodes the supporting evidence measured.
  Confidence is `high` when connected, preceding and uncontradicted;
  `medium` when connected; `low` otherwise, with the reasons. Validity caps
  it: an invalid run's hypotheses are all `low`, and a suspect or unknown
  run's at most `medium`, because its symptoms may come from the load
  generators (`validity_caps_confidence`). The 100 strongest are kept, best
  first.
- **Outcome.** `supported` with any hypothesis; `insufficient_evidence` when
  detection could not run (no steady level, or no budget left) or no
  signature's evidence was measured (`signatures_unmeasured`);
  `no_supported_hypothesis` otherwise, including a run whose detection found
  no symptom (`no_symptoms`).

Capacity is reported beside the hypotheses:

- **Knee.** For each transaction or action latency series measured at three
  or more steady loads, the median p95 per load is fitted with two lines
  joined at an interior level, choosing the join with the least squared
  error. The join is the knee when the second slope is positive, at least
  three times the first, and raises p95 by more than 5 ms; otherwise the
  result says why there is none.
- **Little's law** applies only at a broker queue (a queue named in the
  series' dimensions, or else the provider entity they name) whose depth, arrival rate
  (per second) and residence time (a time unit) were all measured, in each
  steady level with at least three points of each and a depth trend that
  moves it by no more than 10% of its mean (or one message). It reports the
  time-average depth against arrival rate × residence time and their ratio.
  Any other queue or level is `eligible: false` with the reason.

## Root-cause evaluation

The engine is calibrated and judged on a labelled corpus in
[`docs/qa/rca-eval`](qa/rca-eval/README.md) (WP3.9). Each trial names its run,
fault class, fault window and the signature that should rank first; healthy
controls name none. A trial's seed fixes its split: 30% calibrate, 70% are
held out, decided by the seed's hash before any tuning (`rca.EvalSplit`).
`apexctl rca eval --corpus DIR` scores a split's assessments. It reports
top-1 and top-3 recovery, unsupported high-confidence conclusions, false
attribution on controls, abstention, per-class recall and onset error, each
rate with a Wilson 95% interval. It also checks the release bars (top-1
≥ 80%, top-3 ≥ 90%, no unsupported high-confidence conclusion, false
attribution ≤ 5%) and the corpus minimums (8 fault classes × 20 trials, 100
controls). `--replay` first re-analyzes each trial's frozen archive with the
controller's current engine; `--require-bars`, held-out split only, fails
unless the corpus is ready and every bar is met. The default split is calibration, so held-out
labels are scored only when asked for. No corpus has been collected yet, so
the detection, signature and capacity parameters above remain the plan's
starting values.

## Root-cause analysis service

v2 root-cause analyses run as revisioned jobs over a run's archive. Each
project turns them on in **Settings › Project › Analysis** (`GET`/`PUT
/api/v1/projects/{id}/analysis-settings`; project administrators change it,
viewers read it, each save is a new revision and an audit event
`project.analysis_settings.update`):

| Setting | Effect | Deployment list that also turns it on |
| --- | --- | --- |
| Compute root-cause analyses (`root_cause.enabled`) | Editors can request analyses; saved ones stay readable when it is off | `APEXLOAD_RCA_V2_PROJECTS` |
| Show root cause (`root_cause.present`) | Summary, Results, the dashboard, Command Center, Diagnostics and reports present v2 (each response carries `presented`) | `APEXLOAD_RCA_V2_PRESENT_PROJECTS` |
| Analyze every archived run (`root_cause.auto_analyze`) | Queues an analysis when a run's archive is finalized; needs `enabled` | `APEXLOAD_RCA_AUTO_ANALYZE_PROJECTS` |
| Capture provider evidence at archive time (`root_cause.auto_enrich`) | Freezes an enriched evidence revision at finalize | `APEXLOAD_RCA_AUTO_ENRICH_PROJECTS` |
| Business-key lookups (`business_keys.enabled`) | See [Business keys](NATIVE_ANALYTICS.md#business-keys) | `APEXLOAD_BUSINESS_KEY_PROJECTS` |

A flag is on when the project sets it **or** the deployment's list names the
project; the lists stay deployment-wide overrides, and the settings response
says which flags they force (`forced_by_env`). Removing a project from a list
leaves its own setting in force. The general project update never changes
these settings. v1 routes, explain, AI root-cause and the v1 MCP tools keep
returning v1.

| Route | Access | Returns |
| --- | --- | --- |
| `POST /api/v1/tests/{id}/archive/root-cause/analyses` | Editor | 202 with the queued analysis; body `{enrich, baseline_test_id}`. Identical frozen inputs return the existing analysis with 200 |
| `GET /api/v1/tests/{id}/archive/root-cause/analyses/{analysis_id}` | Viewer | State (`queued`, `running`, `completed`, `failed`, `cancelled`), inputs and, once completed, the `apexload.root_cause.v2` assessment |
| `POST /api/v1/tests/{id}/archive/root-cause/analyses/{analysis_id}/cancel` | Editor | The cancelled analysis; 409 when it already ended |
| `GET /api/v1/tests/{id}/archive/root-cause/v2` | Viewer | The latest completed analysis, or `{"state":"none"}` |
| `GET /api/v1/tests/{id}/archive/root-cause/analyses/{analysis_id}/bundle` | Viewer | `apexload.rca_bundle.v2`: the assessment (with its coverage and limitations), the evidence excerpts its reference IDs name and the evidence revision's inventory, at most 256 KiB (`truncated` when something was cut) |
| `POST /api/v1/tests/{id}/archive/root-cause/analyses/{analysis_id}/narration-check` | Viewer | Whether a narrative `{narrative}` stays within the analysis's bundle (see Agents and critiques) |
| `GET /api/v1/tests/{id}/archive/root-cause/analyses/{analysis_id}/critiques` | Viewer | The analysis's critiques, oldest first |
| `POST /api/v1/tests/{id}/archive/root-cause/analyses/{analysis_id}/critiques` | Editor | 201 with the stored critique; 422 with the check when the narrative fails it |
| `GET /api/v1/tests/{id}/archive/root-cause/links?from=&to=` | Viewer | `{links: [{provider, connection_id, url}]}`: the window (RFC 3339, at most 7 days) opened in each of the run's trace providers that has a window template (see [Deep links](OBSERVABILITY_INTEGRATIONS.md#deep-links)) |
| `GET /api/v1/projects/{id}/analysis-settings` | Viewer | The project's analysis settings, the flags its deployment lists force on, and the gates in effect |
| `PUT /api/v1/projects/{id}/analysis-settings` | Project admin | Body `{expected_revision, root_cause, business_keys}`; 409 when the settings changed since that revision |

- **Stored size.** An assessment larger than the 2 MiB stored limit is cut,
  not failed. The least load-bearing parts go first: the coverage rows'
  source lists, validity windows,
  spikes, capacity results, the graph's unused and weakest edges and uncited
  nodes, anomalies no hypothesis cites, and then the lowest-ranked
  hypotheses. A kept hypothesis's anomalies and nodes stay. The cut is
  listed as `assessment_trimmed`. The 256 KiB bundle is cut the same way
  before its evidence and SLA details.
- **Frozen inputs.** An analysis's `input_digest` hashes the archive's
  manifest digest, the evidence revision, the baseline archive's digest, the
  digest of the mapping decisions frozen into the evidence revision, the
  policy revision (unused: the pinned `browser_policy` the browser baseline
  applies is covered by the archive manifest digest, and its revision is
  immutable), the signature set and the engine version. The
  engine version (`rca.EngineVersion`) carries the rules', parameters' and
  signatures' versions and a fingerprint of every value the stages use: the
  budget, detection parameters, validity rules, hypothesis, graph and
  capacity bounds, signature weights and the classifier, followed by the
  archive adapter's version (how the archive is read into series, levels and
  evidence; `archive.3` keys provider series by measurement identity, gives
  every series an identity, adds joint, imported and browser document series,
  and reads unscored telemetry as unknown rather than healthy). Changing a value makes a new analysis; a change to
  a stage's logic bumps the engine's base version, and a change to how the
  archive is read bumps the adapter's. A POST
  whose digest matches a queued, running or completed analysis returns it;
  a failed or cancelled one can be retried. A run keeps at most 50
  analyses and 50 evidence revisions.
- **Evidence revisions** freeze the archive's evidence sidecars, the run's
  monitor sources with their outcomes, the project's mapping decisions and the
  associated analysis datasets (at most eight, by ID; `imported_datasets_truncated`
  beyond that, or when the project's dataset listing exceeds the 2,000 rows
  scanned; the listing pages by offset, so a dataset can be missed by a
  capture during which another of the project's datasets is deleted, and is
  picked up by the next), each with its revision, mapping SHA-256 and partition-manifest
  digest. An analysis reads a dataset only while all three still match; one
  that changed or lost its association afterwards is
  `imported_dataset_changed` and is not read. A POST captures a revision when
  the run has none, when the archive, the mapping decisions or the associated
  datasets changed since the latest, or when `enrich` is true.
  A project that captures provider evidence at archive time (`auto_enrich`)
  gets an enriched revision at archive finalize. Provider evidence pulls (topology and diagnostics) arrive with the
  per-run graph and signatures (WP3.7–WP3.8); until then enrichment records
  the same inventory.
- **Jobs** run on the execution leader, at most two at a time per controller,
  inside the engine's budgets. A job holds the archive's materialization
  lease while it reads; an analysis whose archive changed after the request
  fails rather than judging different evidence. A claimed job holds a
  five-minute lease, renewed while it runs; if its controller stalls, shuts
  down or loses leadership, another takes it over once the lease expires,
  and the stalled owner's late result is rejected, so each analysis records
  one outcome. A job that panics fails instead of being retried.
  Cancellation stops the running job, on another controller once its next
  renewal finds the analysis cancelled, and discards its result.
- **Automatic analysis.** For projects that analyze every archived run, the
  controller queues an analysis (`origin: automatic`) once the run's archive
  is attached: after the catalog commit, so every sidecar and the final
  manifest are durable. An archive still being refreshed is skipped; the
  refresh attaches it again. A later attach of the same evidence (a note, a
  recovered publication) finds the analysis of identical inputs and queues
  nothing; one that changed the evidence queues a new analysis. Requested
  analyses (`origin: requested`) are claimed before automatic ones, and at
  most one automatic analysis runs at a time, so a request always finds a
  worker slot. An identical request answers with the automatic analysis; one
  still queued becomes a requested analysis, so it is claimed ahead of the
  other automatic ones.
- **Retention.** Analyses and evidence revisions are deleted with their run,
  their project, or the run's archive.
- **Baselines.** `baseline_test_id` names an archived run in the same
  project. Its workload qualification against the run appears as `baseline`
  coverage. Unless it is `unqualified`, onset detection compares each load
  level with the baseline's same level.
- **Investigation.** Where a project presents v2, the run report's
  Diagnostics area leads with a Root cause panel: the analysis (automatic or
  requested, its revision), the run's validity, the series judged, a
  sentence saying how to read the outcome, and the ranked hypotheses with
  their confidence reasons, evidence chain, contradicting evidence,
  competing hypotheses and the check that would disprove each. With no
  hypothesis open, the panel draws the run's largest changes (the anomalies
  furthest from their own spread, whether or not a hypothesis explains them)
  on its timeline with its suspect and invalid spans and load steps, so "no
  supported hypothesis" still shows what moved; each change can be focused.
  Coverage names each monitor source that left a gap, with links to the
  run's archived monitor evidence and, for project administrators, the
  project's monitor sources. While a newer analysis is queued or running the
  panel says so. Editors can re-analyze; a comparison baseline open in the
  report is sent and named. Investigating a hypothesis opens one timeline
  (anomaly windows, onset uncertainty bands, the archived load-phase
  events, suspect and invalid spans, throughput and p95 drawn at interval
  starts) with the symptom's transaction population, exemplars with trace
  links, links that open the window in the run's trace providers, the cause
  evidence and the matching error patterns (each links to its row). A run
  whose archived phase events mark no load level (one that held a single
  level archives only its stop) shows load steps inferred from the
  detection levels, marked as inferred, beside any archived pause, resume or
  stop. When the run's archive changed after the analysis read it, as late
  monitoring evidence does, the panel and the report say so. The report URL
  carries `analysis`, `rev`, `hypothesis` and `window` (and `focus` for a
  part of the panel), so a shared link, which the panel copies, reopens the
  same view.
- **Hand-offs.** An editor can record a hypothesis as a finding of the
  current report audience (generated evidence an analyst adopts; the
  generated section is never edited), add it to the run timeline's
  annotations at its earliest symptom onset, or open a test note drafted
  from it with its link.
- **Where it shows.** Every archive report response, single and batch,
  carries `root_cause_summary`: whether the project presents root cause, the
  state of the run's newest analysis and the verdict of its newest completed
  one. From it the Summary's Run outcome shows a Root cause cell (validity
  beside the leading hypothesis, "no cause supported" or the analysis
  state, kept current while an analysis runs, with an action that opens it),
  the Results list and the dashboard's recent runs show validity and the
  leading hypothesis, the dashboard flags invalid and suspect runs and found
  causes for attention and counts invalid and suspect runs in Release
  quality, and the Command Center's banner for an ended run states the root
  cause. The Performance timeline marks the analysis's anomalies, suspect
  and invalid spans and load steps on every lane, and a mark opens its
  hypothesis. Error patterns name the hypotheses they overlap. Baseline
  Comparison and the compare bar analyze the root cause against the chosen
  baseline. Summary validity reasons also appear among the evidence
  limitations to review before acceptance.
- **Reports.** The report section `rca` renders the generated root cause in
  the dashboard's report preview, HTML, PDF, Word, CSV and the analysis ZIP
  from one model (`root_cause_view` on the report JSON): the latest
  completed v2 assessment where the project presents v2, otherwise the v1
  summary, with a note when v2 is presented but no analysis completed. It
  is separate from the Findings section, which holds analyst-curated or
  derived findings, and never carries run annotations.
- **Notifications.** `rca_analysis_completed` announces a completed analysis
  of a project that presents root cause, once per analysis: its outcome as
  the status, the reports' one-line summary, the leading hypotheses and the
  report route. `run_validity` announces a suspect or invalid run once per
  run and verdict, from the archive-attached hook. Grafana annotations tag
  `rca:<outcome>` and `validity:<verdict>`; see
  [Notification events](OBSERVABILITY_INTEGRATIONS.md#load-phase-events-and-grafana-annotations).
- **CI.** A CI run's response carries `root_cause` (state, outcome, leading
  hypothesis, confidence, report URL) for presenting projects; it never
  changes the outcome. The GitHub Check and the job summary add a Root cause
  row, and the action outputs `validity` and `root-cause`. An automatic
  analysis usually completes after the Check does; `apexctl ci run
  --wait-analysis` waits for it (see
  [Root-cause summary](CICD_INTEGRATION.md#root-cause-summary)).
- **Evidence excerpts.** Each analysis stores, beside its assessment, one
  excerpt per anomaly evidence reference: the judged series' points from
  three intervals before the onset's earliest time to one interval after the
  anomaly's end, at most 24 (spread evenly, marked `sampled`), keyed by the
  reference ID hypotheses cite; analyses made before excerpts existed have
  none until re-analyzed. Excerpts no hypothesis cites are cut first
  when an assessment is trimmed, and an excerpt goes with its anomaly. The
  bundle is cut in this order: an evidence inventory over 16 KiB, uncited
  excerpts, the assessment as stored assessments are cut, the inventory,
  the remaining excerpts, and last the SLA details.
- **Agents and critiques.** An agent narrates an analysis from its bundle.
  The narration check rejects a narrative that cites a reference ID
  (`anomaly-…`, `hypothesis-…`, `evidence-…` or the analysis ID) the bundle
  does not hold, a time or date no bundle time matches at the precision
  written, or a number no bundle number rounds to at the precision written.
  Bundle numbers count in their own unit and as nanoseconds in ms, s or min,
  milliseconds in s, and fractions in percent; integers up to ten count
  things and pass; a number joined to a word (`p95`) names something and is
  not checked. A critique is a narrative that passed the check, stored apart
  from the analysis with its author kind (`agent` or `analyst`), model and
  author, at most 100 per analysis; it never changes the assessment and
  goes with its run. External LLM wiring stays disabled: the explain
  provider remains `deterministic-local`.
- **Clients.** `apexctl rca analyze --id RUN [--enrich] [--baseline-id RUN]`,
  `apexctl rca status --id RUN --analysis-id ID`, `apexctl rca show --id RUN`
  and `apexctl rca bundle --id RUN --analysis-id ID`, and `apexctl rca eval`
  for the evaluation corpus; `--wait DURATION` (at most 10m) on `analyze`,
  `status` and `show` polls until the analysis ends (`show`: until an
  automatic analysis completes), and exits 3 (controller or transport
  failure) when a poll fails or answers with something unreadable, rather
  than printing the last state as if it were final; MCP tools
  `run_rca_analyze`, `run_rca_status`, `run_rca_assessment`,
  `run_rca_bundle`, `run_rca_check_narration`, `run_rca_critiques` and
  `run_rca_critique_add`.

Schema release 40 (`rca-analysis-revisions`) adds `rca_evidence_revisions`
and `rca_analyses`; release 42 (`rca-critiques`) adds `rca_critiques`;
release 55 (`rca-analysis-origin`) adds `rca_analyses.origin`. The project
analysis settings ride the project's settings JSON and need no release.
