# Roadmap Review — Full Real Browser Intelligence Chain in Both Auth Profiles (`BI-C5`)

**Date:** 2026-10-27
**Scope:** End-to-end Playwright proof of the real managed Browser Intelligence chain (`BI-1` through `BI-4`) across both `dev-admin` and `local-auth` profiles, browser and structural journey policy editors, two-run baseline comparison to saved `version: 2` (`query_version: 5`) panel, policy tombstone refusal (`422`), report exports (`HTML`, `CSV`, `PDF`, `DOCX`, and `browser-interactions?format=csv`), and `Viewer` RBAC / cross-project denial.

## Summary of Changes

1. **Structural Journey Policy Editor E2E (`BI-B3` / `BI-C5`):**
   - Added `journey policies pick structural step paths, save revisions, refuse stale edits and show past revisions` in [workflow-browser-intelligence.spec.ts](../../../../dashboard/e2e/workflow-browser-intelligence.spec.ts):
     - Seeds a browser script with duplicate action names (`Submit` at `actions[0]` and `actions[2]`) so structural `step_path` disambiguation is required.
     - Opens `#/settings?setting=analysis-policies`, switches to `Journey policies`, and picks `${scriptId}\nactions[0]\naction` and `${scriptId}\nactions[2]\naction` directly through `Pick journey 1 start step from script` and `Pick journey 1 success step from script`.
     - Saves revision 1 (`start: { action: "Submit", step_path: "actions[0]" }` and `success: [{ action: "Submit", step_path: "actions[2]" }]`), verifies optimistic concurrency conflict (`409`) against an out-of-band revision 2 update, reloads current revision, saves revision 3, inspects past revision 1 read-only, and deletes the policy.

2. **Full Real Managed Chain E2E (`BI-C1` / `BI-A3` / `BI-A4` / `BE-B6` / `BI-C3` / `BI-C4` / `BI-C5`):**
   - Added `real managed Chromium runs drive live provisional intelligence, finalized findings, HAR drill-down, comparison panels, exports and viewer access` in [workflow-browser-intelligence.spec.ts](../../../../dashboard/e2e/workflow-browser-intelligence.spec.ts) with zero `page.route` transport stubs:
     - Enables Root-Cause v2 on the project (`PUT /api/v1/projects/{id}/analysis-settings`), creates a default `browser_policy` (`baseline.mode: "latest_compatible"`, aggressive `lcp`/`fcp` thresholds) and a default `journey_policy` (`step_path: "actions[0]"` → `"actions[2]"`).
     - Launches Run 1 (baseline Chromium run) and pins it for baseline comparison (`POST /api/v1/tests/{id}/comparison-baseline/pin`).
     - Launches Run 2 (35 s candidate Chromium run with `har_capture: "always"`, `capture_interactions: true`, `capture_document_metrics: true`, `client_metrics: true`), verifies provisional live API responses (`provisional: true`, `baseline.status: "not_evaluated_live"`, `journeys[0].match_mode: "step_path"`) and Command Center UI (`Live browser intelligence (provisional — non-verdict)`), and waits for the 30 s cadence to advance sample counts before Run 2 completes.
     - Queues RCA (`POST /api/v1/tests/{id}/archive/root-cause/analyses`) and waits for finalized `browser_findings.v2` (`provisional` falsy, 64-hex `selection_receipt_sha256`, `analysis_id`, and `policy_pins`).
     - Inspects `#/report/{id}?section=browser`, verifies `Browser document comparison`, `Browser findings`, `Browser baseline` (`browser_selection.v1` + receipt digest), `Interaction patterns`, and `Journey risk` (`not.toHaveAttribute('data-provisional', 'true')`), drills down from the active finding's worst document to `Retained archive resource inspector`, verifies token redaction (`?token=[REDACTED]`), views the HAR entry, and downloads the redacted native `.har` and `.zip`.
     - Opens `#/report/{id}?section=comparison`, compares against Run 1, clicks **Save to panel**, and verifies `query_version: 5` (`browser_inputs.version: 2`) with `policy_pins` and single-run `selection_receipt_sha256`.
     - Verifies `HTML`, `CSV`, `PDF`, `DOCX`, and `browser-interactions?format=csv` exports carry `browser_findings.v2` provenance, `selection_receipt_sha256`, policy IDs, `interaction_map`, and `journey_risk`.
     - In `local-auth`, binds `local:user` as `viewer` on the domain, creates a second workspace for cross-project denial (`403` on policies, evaluations, and run creation), verifies `Viewer` can inspect redacted report/resources and download report exports (`200`), verifies raw native HAR and `?include=har` return `403`, verifies `Viewer` write attempts (`POST /api/v1/analysis/policies`, `DELETE /api/v1/analysis/policies/{id}`, `POST /api/v1/tests/{id}/archive/root-cause/analyses`, `POST /api/v1/monitoring/dashboard-definitions`) return `403`, and verifies `#/settings?setting=analysis-policies` blocks non-administrators.
     - Deletes the pinned `browser_policy` and verifies `POST /api/v1/tests/{id}/archive/native-series` returns `422` (`pinned analysis policy has been deleted`) and `#/report/{id}?section=performance` displays `Archive panel unavailable`.

3. **Acceptance Inventory & Suite Catalog:**
   - Updated [browser_intelligence_acceptance_test.go](../../browser_intelligence_acceptance_test.go), [BROWSER_INTELLIGENCE_ACCEPTANCE.md](../../BROWSER_INTELLIGENCE_ACCEPTANCE.md), [E2E_SUITES.md](../../E2E_SUITES.md), and [ROADMAP_IMPLEMENTATION_SPEC.md](../../../ROADMAP_IMPLEMENTATION_SPEC.md) while keeping the existing transport-fixture test explicitly labelled for its transport-fixture scope and verifying [DASHBOARD.md](../../DASHBOARD.md) via `python3 scripts/qa-generate-dashboard.py --check`.

## Verification Evidence

- **Playwright `dev-admin` profile:**
  - `APEXLOAD_E2E_PORT=18181 APEXLOAD_E2E_AUTH_PORT=18182 bash scripts/run-e2e.sh workflow-browser-intelligence.spec.ts` → `4 passed (52.4s)` (controller log `dashboard/test-results/controller-logs/dev-admin/run-TvDSLM/selected.log`).
- **Playwright `local-auth` profile:**
  - `APEXLOAD_E2E_PROFILE=local-auth APEXLOAD_E2E_PORT=18181 APEXLOAD_E2E_AUTH_PORT=18182 bash scripts/run-e2e.sh workflow-browser-intelligence.spec.ts` → `4 passed (1.6m)` (controller log `dashboard/test-results/controller-logs/local-auth/run-tUaQyU/selected.log`).
- **Acceptance & Docs Gates:**
  - `go test -v ./docs/qa -run '^(TestBrowserAcceptanceInventory|TestBrowserIntelligenceAcceptanceInventory)$'` → PASS.
  - Mutation check (`go test -overlay`) on `browser_intelligence_acceptance_test.go` mutating the full-chain Playwright test title → FAIL as expected (`Playwright test "mutated non-existent e2e test title" is not in workflow-browser-intelligence.spec.ts`).
  - `python3 scripts/qa-generate-dashboard.py --check` and `python3 scripts/check-docs.py` → PASS.
