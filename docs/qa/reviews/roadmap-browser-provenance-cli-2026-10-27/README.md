# QA Review Record — `BI-C4`: Provenance in Reports, RCA Selection UI & CLI (`apexctl schedule`, `ci run`, `run --api`)

**Date:** 2026-10-27  
**Package:** `BI-C4` (`browser_findings.v2` & `browser_selection.v1` Provenance in Reports/RCA UI + CLI Analysis Policy Selection)  
**Status:** Verified (Zero Open Findings)

## Scope

1. **Report Exports (`HTML`, `PDF`, `DOCX`, `CSV`) for `browser_findings.v2` & `journey_risk`:**
   - Updated [report_browser_intelligence.go](../../../../pkg/api/report_browser_intelligence.go) and [report_section_templates.go](../../../../pkg/api/report_section_templates.go) to surface `Policy.SHA256` (`policy_sha256` CSV row + summary annotation), `PolicyPins` notes, per-finding `Baseline` summary and structured CSV rows (`baseline`, `baseline_status`, `baseline_run_id`, `baseline_archive_sha256`, `selection_receipt_sha256`, `analysis_id`, `rejected_candidates`), and `SampleDocumentID` + `DocumentLink` (`#/report/<run>?section=browser&document_id=<doc>`).
   - Fixed [report_docx_html.go](../../../../pkg/api/report_docx_html.go) so SPA hash routes (`#/report/...`) with `&` query parameters do not render as unescaped Word XML `w:anchor` attributes.
2. **RCA `browser_selection.v1` Surface in Reports & Dashboard UI:**
   - Added `BrowserSelection *ReportBrowserSelection` (`json:"browser_selection,omitempty"`) to `ReportRootCause` in [report_root_cause.go](../../../../pkg/api/report_root_cause.go) and rendered it across HTML, DOCX, PDF, and CSV (`browser_selection` rows) in [report_sections.go](../../../../pkg/api/report_sections.go) and [report_section_templates.go](../../../../pkg/api/report_section_templates.go).
   - Updated [rca.ts](../../../../dashboard/src/types/rca.ts), [rcaModel.ts](../../../../dashboard/src/components/rca/rcaModel.ts), [RcaBrowserBaseline.tsx](../../../../dashboard/src/components/rca/RcaBrowserBaseline.tsx), [RootCausePanel.tsx](../../../../dashboard/src/components/rca/RootCausePanel.tsx), [BrowserIntelligenceView.tsx](../../../../dashboard/src/components/runReport/BrowserIntelligenceView.tsx), and [BrowserFindingsPanel.tsx](../../../../dashboard/src/components/runReport/BrowserFindingsPanel.tsx) to surface `policy.sha256`, `baseline.rejected_candidates`, and `browser_selection` (including `inputs.browser_selection` receipt digest and rejected candidate reasons/digests even when `browser_baseline` is absent).
3. **CI Runs & CLI (`apexctl run`, `apexctl ci run`, `apexctl schedule`):**
   - Added `AnalysisPolicySelection` / `AnalysisPolicySelections` to [run.go](../../../../pkg/runcontract/run.go) and `POST /api/v1/ci/runs` in [ci_runs.go](../../../../pkg/api/ci_runs.go) (gated by `policy.AllowTelemetryPolicySelection`, bound into `Idempotency-Key` metadata, and echoed in `ciRunResponse`).
   - Added `--browser-policy` and `--journey-policy` (`<id>@<revision>` or `project_default`) to `apexctl run` ([main.go](../../../../cmd/apexctl/main.go), [api_client.go](../../../../cmd/apexctl/api_client.go)) and `apexctl ci run` ([ci.go](../../../../cmd/apexctl/ci.go), [ci_client.go](../../../../cmd/apexctl/ci_client.go)), failing closed (deleting/aborting the unconfirmed run) if an older controller does not echo the requested selection.
   - Added `apexctl schedule` (`list`, `preview`, `create`, `update`, `delete`, `occurrences`, `resolve`) in [schedule.go](../../../../cmd/apexctl/schedule.go) supporting `--browser-policy` and `--journey-policy` with controller echo confirmation.

## Verification Evidence

- **Unit & Integration Tests:**
  - `go test -v ./pkg/api -run 'TestBrowserIntelligenceReportSectionsCarryV2ProvenanceAndDeepLinks|TestRootCauseSectionRendersBrowserSelectionWithoutBaselineInEveryFormat|TestCIRunCarriesTheChosenAnalysisPolicySelections'`
  - `go test -v ./cmd/apexctl -run 'TestParseAnalysisPolicyFlag|TestParseRunArgsBrowserAndJourneyPoliciesRequireAPI|TestParseCIArgsBrowserAndJourneyPoliciesRequireControllerMode|TestAPIClientCreateTestEchoesAndCleansUpUnconfirmedAnalysisPolicySelections|TestExecuteScheduleLifecycleAndEchoVerification|TestExecuteControllerCIEchoesAndAbortsUnconfirmedAnalysisPolicySelections'`
  - `cd dashboard && npx vitest run src/components/runReport/BrowserFindingsPanel.test.tsx src/components/rca/RootCausePanel.test.tsx`
- **Mutation Verification (`go test -overlay`):**
  - Mutant 1 (`pkg/api/report_docx_html.go` dropping `!strings.HasPrefix(href, "#/")` on `w:anchor`): killed by `TestBrowserIntelligenceReportSectionsCarryV2ProvenanceAndDeepLinks` (`word/document.xml is not valid XML`).
  - Mutant 2 (`cmd/apexctl/schedule.go` bypassing schedule `analysis_policy_selections` echo check): killed by `TestExecuteScheduleLifecycleAndEchoVerification`.
  - Mutant 3 (`pkg/api/ci_runs.go` bypassing `!policy.AllowTelemetryPolicySelection` check): killed by `TestCIRunCarriesTheChosenAnalysisPolicySelections`.
