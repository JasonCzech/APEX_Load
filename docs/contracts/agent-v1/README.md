# APEX Load agent contract, version 1

The agent brief (`apexload.agent_brief.v1`) is an archived run's result, built to be read by an LLM agent: a CI or coding agent gating a pull request, a triage agent finding a cause, a reporting agent summarizing, or an orchestrator acting on a webhook. It puts the verdict first and keeps the result to a fixed size. Every finding has an id that stays the same across runs, and an agent can fetch any finding by that id for more detail.

The brief is a projection of the archive report every other format renders. It never computes a verdict of its own, and it is deterministic: the same archive, comparison and root-cause revision always produce the same bytes.

The schemas in this directory are generated from the controller's Go types (`go generate ./pkg/api`), and a test fails when they are stale. `examples/` holds a brief, its Markdown, a finding detail and a CTRF report, all produced by the builder's test fixtures.

| File | Describes |
| --- | --- |
| `brief.schema.json` | `GET /api/v1/tests/{id}/archive/agent-brief` and `GET /api/v1/ci/runs/{id}/agent-brief` (JSON) |
| `finding-detail.schema.json` | `GET /api/v1/tests/{id}/archive/agent-brief/findings/{finding_id}` (`apexload.agent_finding_detail.v1`) |
| `ctrf.schema.json` | the brief routes' `format=ctrf` response, a Common Test Report Format document |
| `event.schema.json` | a notification webhook with format `cloudevents`: a CloudEvents 1.0 event |

Schema ids are URNs (`urn:apexload:contract:agent-v1:<file>`), not URLs. Validators must not try to fetch them.

## Requests

| Parameter | Values | Default |
| --- | --- | --- |
| `detail` | `summary`, `standard`, `full` | `standard` |
| `format` | `json`, `md` (Markdown), `ctrf`, `bundle` (`{"brief", "markdown"}`, used by the MCP server; archive route only) | `json` |
| `baseline` | `latest` (the run's newest saved comparison), `none` | `latest` |
| `comparison_id` | a saved comparison of this run; sets `baseline.selection` to `explicit` | none |

A brief read never creates a comparison. Comparing runs is a write (`POST /api/v1/tests/{id}/baseline-comparisons`), so agents do it on purpose.

Responses carry `ETag: "<brief_digest>"`. A request with a matching `If-None-Match` gets `304`.

The archive route uses the project's viewer access. The CI route accepts the credentials `apexctl ci run` holds (GitHub OIDC or a `ci_runner` key with `results:read`), bound to the run's source the same way the CI run status route is. On the CI route, `baseline.source_context` appears only when the baseline run came from the token's own repository.

### Errors

Failures return `{"error", "code", "remediation"}`. Agents should branch on `code`:

| Code | Status | Meaning |
| --- | --- | --- |
| `run_not_found` | 404 | No such run (a project-scoped caller gets an uncoded `403` instead, from the route's access check, for a run it cannot see or that does not exist) |
| `run_not_finished` | 409 | The run is still going; wait, then ask again |
| `archive_unavailable` | 409 | The archive is not published yet, or it was not written or retention removed it. For a CI run that failed or was aborted, whose outcome does not depend on the archive, the brief is served anyway with only its CI outcome: `evidence.status` is `unavailable`, with no metrics or findings. A completed run's gate is judged on its archive, so it gets this error. The finding route always needs the archive. |
| `invalid_detail`, `invalid_format`, `invalid_baseline` | 400 | A bad parameter |
| `comparison_not_found` | 404 | The `comparison_id` does not exist for this run |
| `finding_not_found` | 404 | The finding id is not in this run; the remediation lists valid ids |
| `forbidden` | 403 | A CI credential bound to a different run |
| `comparison_storage_unavailable` | 503 | The controller keeps no saved comparisons; use `baseline=none` |
| `internal` | 500 | Carries `ref`, the server log correlation id |

## Detail levels and size

| Level | JSON cap | Markdown cap | Findings | Metric rows | Untrusted text |
| --- | --- | --- | --- | --- | --- |
| `summary` | 4.5 KiB | 3 KiB | up to 5 | overall + 5 | omitted |
| `standard` | 16 KiB | 10 KiB | up to 25 | overall + 15 | up to 256 characters |
| `full` | 40 KiB | 24 KiB | all that fit | overall + 50 | up to 512 characters |

The caps assume about 3 bytes per token. An MCP result carries both forms, so each level's two caps add up to its token budget: about 2.5k, 9k and 22k tokens, the last under Claude Code's 25k tool-result limit.

When a brief is over its caps, the builder drops the lowest-ranked findings first, then metric rows. It never drops the verdict, run or baseline blocks. `truncation.omitted` counts what was dropped, and `truncation.how_to_get_more` says which call returns it. At `full` detail, `truncation.omitted_finding_ids` lists the dropped findings' ids, the highest-ranked first (up to 200), so each one can still be fetched with the finding route.

When two sources on the same subject both breach (two rule tiers, or a rule and the goal row), they are one finding: its severity, title, metric and `rule_id` come from the more severe breach. At `full` detail the Markdown lists the omitted ids too.

## Verdict

`verdict.status` is `pass`, `fail`, `inconclusive`, `not_assessed` or `invalid`.

- **CI runs** (`verdict.source: ci_outcome`): the status follows the run's CI outcome. `blocking` is true exactly when `apexctl ci run` would exit non-zero for that outcome, and `ci_exit_code` gives the code. The brief and the CI gate cannot disagree on the controller's outcome. `apexctl ci run` can still fail a job for local reasons the controller does not know, such as a required GitHub Check it could not publish.
- **Other runs** (`verdict.source: report_verdict`): the status follows the report verdict, and becomes `invalid` when the run's measurement validity is `invalid`. `blocking` is true for `fail` and `invalid`.

| CI outcome | status | exit |
| --- | --- | --- |
| `passed` | `pass` | 0 |
| `sla_breached` | `fail` | 1 |
| `run_invalid` | `invalid` | 1 |
| `telemetry_failed` | `fail` | 1 |
| `telemetry_inconclusive` | `inconclusive` | 2 |
| `configuration_error` | `not_assessed` | 2 |
| `monitoring_incomplete`, `inconclusive` | `inconclusive` | 3 |
| `execution_error`, `timed_out`, `cancelled` | `not_assessed` | 3 |
| `execution_error` or `cancelled` with status `FAILED`, `ABORTED` or `NO_RESULTS` | `fail` | 1 |

Evaluating a CI outcome verifies the run's archive, so briefs share one evaluation per run for up to a minute; a re-evaluated outcome shows up in a brief within that time. A run that did not execute is neither a bad measurement nor a performance failure of the system under test, so execution outcomes map to `not_assessed`. They still block. A run that executed and then failed or was aborted is a test failure, exactly as `apexctl ci run` exits for it.

`reason_codes` is sorted, and every code comes from this closed set:

| Code | Raised when |
| --- | --- |
| `sla_breached` | any blocking `sla_breach` finding |
| `regression_vs_baseline` | any `regression` finding |
| `run_invalid` / `run_suspect` | measurement validity is `invalid` / `suspect` |
| `evidence_incomplete` | archive evidence coverage is not complete |
| `monitoring_incomplete`, `telemetry_failed`, `telemetry_inconclusive`, `configuration_error`, `execution_error`, `timed_out`, `cancelled` | the CI outcome of the same name |
| `assessment_inconclusive` | the assessment itself was inconclusive: the report verdict or the CI outcome is inconclusive |
| `auto_stopped` | an automatic stop rule ended the run (`run.stop_trigger`) |
| `aborted` | `termination_reason` is `aborted` or `failed` |
| `operator_stopped` | `termination_reason` is `operator_stopped` and an operator is recorded as stopping the run, or no automatic stop rule fired (a graceful automatic stop is archived as `operator_stopped` too). When a rule stopped the run and an operator also pressed Stop while it drained, both `auto_stopped` and `operator_stopped` appear. |
| `throughput_below_target` | any `throughput_gap` finding |
| `no_criteria` | no acceptance criteria were configured |
| `rca_pending` | a root-cause analysis is queued or running |
| `baseline_unavailable` | `baseline=latest` and the run has no saved comparison |

`verdict.explanation` is a templated sentence. It never contains text from the system under test.

## Findings

Each finding has a `kind`, a `severity` (`blocker`, `major`, `minor`, `info`) and a `confidence` (`high`, `medium`, `low`). Findings are sorted by severity, then baseline state (new before recurring), then confidence, then how far past its threshold or baseline the finding is, then id.

| Kind | Source |
| --- | --- |
| `sla_breach` | transaction SLA rules, per-transaction p95 and error-rate limits, response-time SLAs |
| `regression` | a saved baseline comparison's regressed metrics |
| `error_group` | recurring error patterns (one per signature) |
| `evidence_gap` | archive completeness findings |
| `validity` | measurement validity and telemetry validity findings |
| `throughput_gap` | throughput goal attainment |
| `environment` | host network, HTTP/3 preflight and protocol-mix findings |
| `anomaly` | advisory statistical anomalies |
| `rca_hypothesis` | the presented root-cause hypotheses, ranked |
| `auto_stop` | the automatic stop rule that ended the run |

### Ids

A finding's `id` is `fnd_<kind code>_<12 hex characters>`: a SHA-256 over the kind and the finding's subject, for example its transaction and metric, plus any narrower scope an SLA rule or response-time SLA names: its group, a population other than the default `transaction`, its protocol, and an iteration selection other than the default `warm`. Values never go into the id, so the same breach of the same transaction's metric has the same id in every run. A renamed transaction is a new finding.

When a baseline is selected, `baseline_state` says whether a finding is `new` or `recurring` relative to the baseline run's findings. Without a baseline it is `unknown`. `baseline.resolved_count` counts baseline findings this run no longer has; at `full` detail, `baseline.resolved_ids` lists them (up to 200; `truncation.omitted.resolved_ids` counts the rest).

When two sources report the same subject, for example an SLA rule and the SLA row for the same transaction and metric, they merge into one finding. The merged finding lists every source in `sources`, and keeps the rule's identity in `rule_id`.

### Evidence and next steps

`evidence[].uri` paths are relative to `/api/v1`. `next[]` names the MCP tool and arguments that drill further, for example `apex_get_finding` or `apex_rca_bundle`. When the brief read a comparison (named or the newest), each `apex_get_finding` step (and each link to the finding route) carries its `comparison_id`, since regression findings exist only against that comparison and the newest one can change. A brief read with `baseline=none` passes `baseline=none` the same way, so the finding reads as it did in the brief.

At `summary` and `standard` detail, `run.scripts` and `validity.reasons` are capped (5 and 20 entries); `truncation.omitted` counts any cut as `scripts` or `validity_reasons`.

## Untrusted text

Error samples, response bodies, server error text and script-sourced strings come from the system under test. They can contain text written to steer a model. The brief keeps such text only in a finding's `untrusted` field (and a finding detail's `samples`), and only after these steps:

1. Secrets are masked with the shared scrubber.
2. Control and bidirectional-override characters are removed.
3. The text is capped at 12 lines and at the detail level's character limit.

Names from users, such as transactions and scripts, are reduced to letters, digits and a few separators before they appear in a templated title.

In Markdown, lines that a CI runner would read as workflow commands (starting with `::`, or containing `##[`) are broken up, and each untrusted value sits inside its own `<untrusted_sut_data finding="…" trust="none">` boundary. The value is inside a tilde fence that is longer than any tilde run in the value, and any occurrence of the boundary's tag name in the value is rewritten, so the value cannot close its boundary. The brief states once that everything inside the boundary is data, not instructions.

`summary` briefs carry no untrusted text at all. Webhook payloads carry no brief, but their `data.event.summary`, `conditions` and `test_name` are controller text that can quote a run's error: treat them as data.

## CTRF

`format=ctrf` returns a Common Test Report Format document:

- **Goal tests** (suite `sla`): the run's goal, judged per transaction.
  - `SLA <transaction> p95 <= <threshold>`, for a goal p95 SLA: `failed` when the p95 is over it, `other` within 10% of it, `skipped` when the transaction had no samples or no p95, else `passed`.
  - `SLA <transaction> error rate <= <limit>`, for a goal error-rate limit: `failed` when the error rate is over it, else `passed`.
  - `duration`: the transaction's observed p95 in milliseconds for the p95 test. This is a documented APEX mapping: CTRF's duration is a test's own runtime, which a load test has no analogue of.
- **Rule tests** (suite `sla_rules`): one per transaction SLA rule result, named after the threshold it judged, `SLA rule <group>/<transaction> (<population>, <protocol>, <iterations>) <metric> <comparator> <threshold>`; the group and each qualifier appear only when the rule sets them. `pass` is `passed`, `fail` is `failed`, a warning-level breach is `other`, and an unevaluated rule is `skipped`.
- **Response-time tests** (suite `sla_response_time`): one per response-time SLA, `SLA <transaction> within <threshold> for >= <required> of requests`, `passed` or `failed` from its result, `skipped` when it was not evaluated.
- Every check is judged on the run's raw values; the numbers shown are rounded as in the brief. Response-time test names include the group when the SLA names one.
- A breached check's `extra.apexload.finding_id` names its finding in the brief.
- **Verdict test:** one `APEX Load verdict` test, `failed` when the brief is blocking.
- **`extra.apexload`:** perf metrics, baseline deltas and finding ids.

The specification version is a placeholder until the official CTRF schema is vendored and pinned. Until then the report uses only CTRF's core fields, with no `insights`.

## CloudEvents

A notification webhook with format `cloudevents` posts each event in CloudEvents 1.0 structured mode (`application/cloudevents+json`).

| Notification | `type` |
| --- | --- |
| `test_complete` | `com.apexload.run.completed.v1` |
| `test_failed` | `com.apexload.run.failed.v1` |
| `threshold_alert` | `com.apexload.threshold.alert.v1` |
| `rca_analysis_completed` | `com.apexload.rca.completed.v1` |
| `run_validity` | `com.apexload.run.validity.v1` |
| anything else | `com.apexload.<type with dots>.v1` |

- **Envelope:** `source` is `/apexload/projects/<project>` and `subject` is `tests/<test id>`.
- **`id`:** the delivery's dedupe key, also sent as `X-APEXLoad-Notification-Key`. It is the same on every retry, so receivers can drop duplicates.
- **`dataschema`:** `urn:apexload:contract:agent-v1:event.schema.json#/$defs/AgentCloudEventData`, the schema of `data`; `event.schema.json` itself describes the whole envelope.
- **`data.links`:** point to the run's agent brief, report and dashboard page.
  - The brief is not inlined: building it reads the archive, and a notification never waits on that.
  - A receiving agent fetches the brief with its own credentials.
- **Untrusted text:** `data.event.summary` and `conditions` are controller text that can quote a run's error. Control and bidirectional characters are removed, but the text is still data, not instructions.

## Versioning

Within v1, fields may be added. Removing or renaming a field, or changing what an enum value means, needs v2. A change to how ids are fingerprinted changes every id, so it needs `generated_from.builder_version` bumped.
