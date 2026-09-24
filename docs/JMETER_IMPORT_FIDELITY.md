# JMeter import fidelity

Reimport existing plans to receive these changes. Saved imports are not rewritten, and importing never modifies the JMX source.

The importer retains the first effective endpoint independently of header, timer, assertion, and variable scopes. Requests to other hosts remain absolute. An explicit base-URL override retains precedence.

Finite, unscheduled Thread Groups produce `termination_mode: "iterations"`, no duration, and positive `config.run_logic.iterations`. The supplied split-pay fixture preserves 50 users, a one-second ramp, and 200 iterations per user. Normal completion means 10,000 sampler executions. Each user retires after its own count. Duration mode remains the default for API requests that omit termination mode and requires a positive duration.

Iteration mode uses fixed closed-loop populations. Scaling, duration adjustment, traffic profiles, timeslots, and recurring schedules are unavailable. Pause/resume and cancellation remain supported. Generator admission requires `iteration_termination_v1`; loss of a generator produces incomplete work rather than replaying its work on another generator. Duration-based JMeter schedulers retain their authored duration and finite loop limits; unresolved and incompatible completion semantics stay blocked.

In timestamp review, choose either UTC normalization (one timestamp with milliseconds) or the source's IANA timezone. The source option preserves wall-clock fields and explicitly acknowledges a literal `Z` suffix. Supported Java formats become formatted, per-occurrence parameters. Dynamic formats, unsupported patterns, and unsupported `__timeShift` calls require repair. Choices are recorded in import provenance and applied by server-side regeneration.

Variable review lists declared names and unresolved references. Enter the declared name to bind, or an explicit `{{secret.NAME}}` reference. The before/after preview shows the proposed rewrite; inspecting again creates a newly validated, attested draft. Asterisk masks and disclosure placeholders are unavailable credential material. Correcting credentials does not clear timestamp or unsupported-processor gates.

JSON extractors preserve missing versus explicitly empty defaults. `on_miss: "default"` with `default_value` replaces a previous extraction on a miss. The default does not suppress malformed expressions or resource-limit failures. This extractor mode is supported by HTTP-family protocols.

## Native CSV results

A complete match of the supported Groovy trace writer becomes `config.results.csv_exports`. The selected variable must be extracted before the writer, without a later enabled overwrite; other processor orderings retain a review gate. The declaration identifies a source action, a filename, and ordered columns selecting extraction values or `latency_ms`. Arbitrary Groovy execution and source-machine filesystem writes are unsupported.

`traceId-results.csv` is a per-run archive download with `traceId,latencyMs` columns. Rows come from finalized action samples, including failures, and exclude synthetic transaction records. Empty trace values use the writer's fallback. Latency is integer milliseconds to the first response byte; connection failures without a response byte report zero.

Exports use complete local archives or deterministically merged generator archives, not sampled live metrics. Archive authorization, retention, redaction, and integrity controls apply. Missing shards, retention truncation, scan limits, count mismatches, and write failures produce an incomplete export instead of a downloadable partial file. Export definitions are frozen in run provenance. Finalized samples bind to export IDs before action-name redaction, so redaction cannot change which samples belong to a CSV.

Sampler totals and source-behavior classifications are separate. Disabled elements are ignored, informational listeners do not block replay, and enabled unsupported processors retain durable gates after unrelated repairs. Source links identify the contributing assertions, extractors, timers, and postprocessors.

The sanitized original and repaired regression sources are under `pkg/convert/jmeter/testdata`. The repaired fixture uses fake credentials and still requires an explicit timestamp policy; execution tests use loopback services.

## Regression coverage

| Original finding | Regression evidence |
| --- | --- |
| Scoped endpoint loss and explicit override | `pkg/convert/jmeter/fidelity_test.go`: split-pay, nested scopes, multiple hosts, and override cases |
| Invalid timestamps and ambiguous literal Z | Converter format/repair tests and `pkg/engine/parameters_test.go`: frozen midnight and timezone cases |
| Lost 200-iteration limit | `pkg/engine/iteration_termination_test.go` and `pkg/distributed/generatorrpc/iteration_termination_test.go`: exactly 10,000 loopback samples, pause/resume, cancellation, and group completion |
| Omitted Groovy CSV writer | Converter full-pattern rejection tests; `pkg/api/jmeter_fidelity_execution_test.go`: finalized samples, failures, deterministic shard merging, escaping, redaction, and incomplete exports |
| Missing extractor default and stale trace ID | `pkg/protocol/http/extractor_default_test.go`: present/missing/present, empty defaults, invalid responses, bounds, redirects, and connection failure |
| Contradictory accounting and inaccurate source links | Converter source mappings, disabled elements, and durable reason-code regressions; import contract registry tests |
| Unresolved source variable names and masked credentials | Converter binding tests and `dashboard/e2e/workflow-jmeter-fidelity.spec.ts`: explicit review regeneration and retained blockers |
| Persistence and rolling upgrades | SQLite/PostgreSQL termination migrations, scenario admission/persistence tests, and legacy generator capability rejection |

The browser workflow runs in both dev-admin and local-auth profiles. It imports the original fixture, regenerates explicit repairs, saves a small repaired iteration scenario, executes six loopback samples, and downloads the seven-line CSV through the results UI. The source endpoint is never used by these tests.

For messaging plugin imports, see [Kafka / Event Hubs draft fidelity and remaining conformance work](JMETER_MESSAGING_FIDELITY.md).
