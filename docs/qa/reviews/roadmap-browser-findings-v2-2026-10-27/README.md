# Evaluated browser baseline findings and RCA completion hook (BI-A3)

This slice upgrades browser intelligence findings from `browser_findings.v1` to `browser_findings.v2` (`apexload.browser_findings.v2`, `RuleVersion: 2`), replaces the unconditional `not_evaluated` placeholder on application-outcome findings with evaluated baseline statuses and immutable provenance links, and re-triggers browser findings evaluation when root-cause analysis completes in [BI-A](../../../ROADMAP_IMPLEMENTATION_SPEC.md#bi-a--qualified-baseline-selection-and-findings).

## Contract and invariants

1. **Evaluated baseline status and multi-load-level precedence (`pkg/api/browser_findings.go`, `pkg/rca/browser_baseline.go`, `pkg/api/rca_browser_documents.go`):**
   - `evaluateBrowserFindings` emits `RuleSet: "browser_findings.v2"` and `SchemaVersion: "apexload.browser_findings.v2"` (`RuleVersion: 2`) while preserving `browser_score.v1` (`Score.Version: "browser_score.v1"`).
   - Collection-health findings (`collection.absent`, `collection.incomplete`) and suppressed inapplicable cohort findings (`!finding.Applicable` when `mode != "none"`) report `Baseline.Status = "not_applicable"`.
   - Application-outcome findings (`threshold`) evaluate `BrowserFindingBaseline` against the run's completed RCA assessment when `payload.Inputs.Archive` matches `rcaEvidenceDigest(manifest)` and `rcaBrowserBaselineMatchesPolicy` confirms `PolicySHA256 == ref.SHA256`:
     - `regressed`: matched `rca.BrowserBaselineAnomaly` with `Direction != rca.DirectionDecrease` (including sub-threshold regressions where `severity == "good"`, emitted as `State: "active"`, `Severity: "needs_improvement"` while keeping absolute threshold scoring in `Score` distinct).
     - `within_baseline`: matched `rca.BrowserBaselineWithinRange` or `DirectionDecrease`.
     - `insufficient_evidence`: unscored baseline result or limitation with an insufficient-evidence reason (`insufficient_baseline`, `zero_baseline_variance`, `no_baseline_cohort`, `no_same_load_window`, `baseline_evidence_unavailable`, `run_evidence_incomplete`, `run_evidence_unavailable`, `levels_unavailable`, `no_effect_floor`, `outside_steady_load`, `results_truncated`, `metric_not_observed`, `no_qualified_final_observations`).
     - `unqualified`: unqualified baseline run (`Baseline.Qualification == BaselineUnqualified`), `current_unqualified` selection status, or qualification failure (`cohort_changed`, `browser_clock_unavailable`, `browser_source_coverage_unavailable`, `cadence_mismatch`, `source_script_changed`, `baseline_documents_incomplete`).
     - `not_selected`: `mode == "none"` (`policy_off`), `no_compatible_candidate`, or `no_baseline_run` (never reported as `within_baseline`).
     - `not_evaluated`: before RCA completes (`rca_not_completed`) or when the completed RCA's archive or policy digest does not match (`rca_archive_mismatch`, `rca_policy_mismatch`).
   - Across multiple load levels for a `(metric, cohort)` tuple, `evaluateFindingBaseline` selects the worst status deterministically (`regressed > unqualified > insufficient_evidence > within_baseline > not_selected > not_evaluated`), breaking ties by largest `|Z|`, lexicographic `Reason`, earliest `LevelStart`, and `Result.ID`.

2. **Immutable provenance links (`pkg/api/browser_findings.go`, `dashboard/src/components/runReport/BrowserFindingsPanel.tsx`, `cmd/apexctl/analysis_browser_findings.go`):**
   - Each application-outcome finding and `BrowserIntelligenceResult` carries `PolicyPins` (`browser_policy` and `journey_policy` revision/SHA-256 pins) and `BrowserFindingBaseline` provenance (`AnalysisID`, `SelectionReceiptSHA256`, `SelectionStatus`, `BaselineRunID`, `BaselineArchiveSHA256`, bounded `RejectedCandidates` excluding `self_baseline`).
   - `computeBrowserIntelligenceFingerprint` binds `rca_analysis_id`, `rca_revision`, `baseline_run_id`, `baseline_archive_sha256`, `selection_receipt_sha256`, `baseline_mismatch_reason`, and `policy_pins`.

3. **RCA completion re-trigger and monotonic job ordering (`pkg/api/rca_analyses.go`, `pkg/api/browser_findings_delivery.go`, `pkg/storage/feature_evaluation_jobs.go`):**
   - `runRCAAnalysis` invokes `s.autoQueueBrowserIntelligence(analysis.TestID)` after `CompleteRCAAnalysisFenced` commits a completed RCA assessment.
   - `autoQueueBrowserIntelligence` cancels superseded `Queued` system jobs for the same run before queuing the post-RCA evaluation job, and `runBrowserIntelligence` accepts `inputs.preRCAFingerprint` if a pre-RCA job was already `Running` when RCA completed.
   - `CreateEvaluationJob` enforces strictly monotonic `created_at` (`now = latestCreated.Time.Add(time.Microsecond)` when `!now.After(latestCreated.Time)`) under the project lock so back-to-back pre-RCA and post-RCA jobs sort newest-first in `ListEvaluationJobsForSubject` and suppress duplicate webhook transitions in `notifyBrowserFindingTransitions`.

## Validation and mutation testing

- `cd dashboard && npm run typecheck && npx vitest run src/components/runReport/BrowserFindingsPanel.test.tsx src/labelCasing.test.ts && npm run build`: passed.
- `go test -race -count=2 -shuffle=on ./pkg/rca/... ./cmd/apexctl/... ./pkg/storage -run "TestEvaluationJobs|TestFeatureEvaluation"`: passed.
- `go test -race -count=2 -shuffle=on ./pkg/api -run "TestBrowserFindings|TestBrowserScore|TestBrowserCollection|TestBrowserIntelligence|TestRunBrowserFindings|TestRCACompletion|TestDetectBrowserBaseline|TestRCABrowser|TestSwaggerFreshness|TestOpenAPITSGeneratedFreshness"`: passed.
- Mutation verification via `go test -overlay`:
  - Mutant 1 (`case "regressed": return 0` -> `case "regressed": return 4` in `findingBaselineStatusRank`, `pkg/api/browser_findings.go`): killed by `TestBrowserFindingsV2EvaluatedBaselineStatusesAndPrecedence`.
  - Mutant 2 (`return payload.Assessment.BrowserBaseline.PolicySHA256 != "" && payload.Assessment.BrowserBaseline.PolicySHA256 == ref.SHA256` -> `return true` in `rcaBrowserBaselineMatchesPolicy`, `pkg/api/browser_findings.go`): killed by `TestRCACompletionReTriggersBrowserFindingsWithoutDuplicateWebhooks`.

## Review and remaining

- Reviewed by code-safety and test/QA subagents to zero findings.
- Remaining in Track `BI-A`: the 95 s two-run seeded-regression production exit (`BI-A4`).
