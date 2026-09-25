# Baseline comparison in Run Results

Open **Baseline Comparison** (or **Compare**) on an archived run. Choose up to five independently pinned runs; pins in the same release remain independent. The picker defaults to the same scenario, with an explicit option for other scenarios in the application. Failed and aborted references remain visibly qualified.

Apply the selection to create an immutable assessment. The persistent context shows its load range, matching tolerance and measurement-window settings. Changing controls creates a draft; existing findings and exports continue to describe the saved assessment until you apply again. Permanent links and JSON, CSV, HTML, PDF and Word exports carry the comparison ID. Scenario editors can save shared policies with revision conflict checking.

## Reading evidence

Start with findings, then search the transaction matrix or switch to Resources and Setup differences. Open a row to inspect values, matched windows, coverage, evaluation rules and archived evidence. The load chart shows each selected plateau; the separate elapsed-time chart retains each run's own clock. Resource changes are contemporaneous evidence, not transaction-level attribution.

Transactions match group, protocol and name within one population. Named transactions, requests/actions and legacy populations remain separate. Copied groups need unambiguous identities or explicit one-to-one mappings. Percentiles are calculated from successful events using a mergeable distribution, never by averaging reported percentiles.

Stable observed plateaus exclude recorded pauses, idle/baseline phases and the first 30 seconds; at least 60 seconds must remain. Matching prefers exact loads, otherwise the closest within the configured tolerance (5% by default), with increasing and decreasing stages separate. Group load aligns transactions; total load aligns resources unless explicitly mapped to a group. Workload mix and setup differences qualify the assessment.

Default changes: latency 10% **and** 10 ms; throughput 10%; errors 1 percentage point; resources 20% in either direction. Mean/P95 require 100 successful samples, P99 1,000; resources require 10 samples and 80% coverage. Zero references have absolute differences with percentage unavailable. Lower resource usage is not automatically an improvement. Rules are advisory and do not alter recorded SLA or acceptance outcomes.

## Monitoring and historical limitations

Only archived resources and frozen dashboard captures are read. Historical comparisons never query current monitoring. Known source identity, units, metric kind and labels must match, or an operator must explicitly confirm a mapping. Sum/mean/max aggregation uses aligned contributing instances; incomplete intervals reduce coverage. Counters are converted to rates before instance aggregation and reset intervals are excluded. Instance counts are displayed.

New captures include versioned fingerprints of disclosure-safe configuration and optional metric definitions. Existing IDs do not establish content equality. Older archives, absent units, unknown metric semantics, missing windows and insufficient samples remain explicit evidence gaps. Archive and comparison budgets are bounded; exceeded limits fail explicitly rather than silently replacing or dropping selected runs. Large comparisons may need fewer selected runs or narrower measurement windows.

See the generated [OpenAPI contract](swagger.yaml) and [endpoint catalog](api-endpoint-catalog.json) for candidate, snapshot and policy interfaces. Saved assessments are scoped to the current project and application and recheck all referenced runs on access/export.
