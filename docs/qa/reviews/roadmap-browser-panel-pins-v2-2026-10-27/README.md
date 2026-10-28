# QA Review — Saved Panel Policy and Baseline Selection Pins v2 (`BI-C3`) (2026-10-27)

## Scope & Parent Requirement

- **Roadmap Track:** `BI-C` (`BI-4`: Complete policy, panel and evidence workflows)
- **Package:** `BI-C3` (`query_version: 5`, `storage.BrowserPanelInputs` `version: 2`, `BaselineComparisonWorkspace` "Save to panel", and `apexctl analysis replay-panel`)
- **Acceptance Authority:** [ROADMAP_IMPLEMENTATION_SPEC.md](../../../ROADMAP_IMPLEMENTATION_SPEC.md), [BROWSER_INTELLIGENCE_WORKFLOWS.md](../../../guides/BROWSER_INTELLIGENCE_WORKFLOWS.md)

## Contract & Invariants

1. **Versioned Saved Browser Panel Inputs (`version: 2`, `query_version: 5`):**
   - [`BrowserPanelInputs`](../../../../pkg/storage/browser_panel_inputs.go) (`version: 2`, bound to `query_version: 5`) extends `version: 1` (`query_version: 4`) with `policy_pins` (`[]BrowserPanelPolicyPin`: `kind` in `{"browser_policy", "journey_policy"}`, `id`, `revision >= 1`, 64-hex `sha256` in strictly ascending `(kind, id)` order) and `selection_receipt_sha256` (optional 64-hex SHA-256 digest of the RCA `browser_selection` receipt), plus per-run `policy_pins` on `BrowserPanelRunInput`.
   - `version: 1` (`query_version: 4`) queries and saved panels remain valid and forbid `version: 2` policy/selection fields so existing saved panels open unchanged.
2. **Server-Side Policy Tombstone & Selection Receipt Verification (`POST /api/v1/tests/{id}/archive/native-series` and `validateSavedBrowserPanelInputs`):**
   - [`verifyAndPopulateBrowserPanelV2`](../../../../pkg/api/browser_panel_inputs.go) verifies every `browser_policy` and `journey_policy` pin against both the pinned revision (`GetAnalysisDefinition(projectID, id, revision)`) for SHA-256 digest integrity and the head revision (`GetAnalysisDefinition(projectID, id, 0)`) to detect policy tombstones (`Deleted == true`). Advancing a policy to a newer non-deleted revision keeps historical pinned revisions valid; deleting a policy refuses `query_version: 5` panel reads (`422`) and saves (`400`).
   - When a completed RCA analysis matches the current archive digest and pinned browser policy (`rcaBrowserBaselineMatchesPolicy`) and is compatible with `comparison_run_id`, `verifyAndPopulateBrowserPanelV2` populates `selection_receipt_sha256` from `payload.Inputs.BrowserSelection`, and [`bindBrowserPanelInputs`](../../../../pkg/api/browser_panel_inputs.go) refuses any mismatch against saved `BrowserInputs`.
3. **Comparison → Saved Panel → Report → CLI Replay:**
   - [`BaselineComparisonWorkspace.tsx`](../../../../dashboard/src/components/baselineComparison/BaselineComparisonWorkspace.tsx) adds **Save to panel**, freezing a `query_version: 5` separate-tuple browser document comparison query via [`freezeBrowserPanelDefinition`](../../../../dashboard/src/components/browserPanelInputs.ts) and persisting the project dashboard definition.
   - [`BrowserTupleArchivePanel.tsx`](../../../../dashboard/src/components/BrowserTupleArchivePanel.tsx) renders and exports (`selectionReceiptPin`, `policyPins`) the `version: 2` selection receipt and policy pins in CSV and the Run Report panel inspector.
   - [`replaySavedPanel`](../../../../cmd/apexctl/analysis_workflows.go) implements `apexctl analysis replay-panel --id <panel_id> [--revision <n>]`, fetching the saved dashboard definition (verifying the requested revision when `--revision` is passed), re-executing each panel query against `POST /api/v1/tests/{id}/archive/native-series`, and verifying that the server's echoed `browser_inputs` match the saved pins.

## Validation

- **Storage & API Unit/HTTP Race Suites:**
  - `go test -race -count=2 -shuffle=on ./pkg/storage -run 'TestBrowserPanel|TestValidateNativeArchiveQuery|TestDashboardDefinition'`
  - `go test -race -count=2 -shuffle=on ./pkg/api -run 'TestBrowserPanel|TestSwagger|TestOpenAPI'`
  - `go test -race -count=2 -shuffle=on ./cmd/apexctl -run 'TestAnalysisPanel|TestAnalysisReplayPanel'`
- **Dashboard Unit & Build Verification:**
  - `cd dashboard && npm run typecheck && npx vitest run src/components/browserPanelInputs.test.ts src/components/ArchivedNativePanel.inputs.test.tsx src/components/NativePanelAuthoring.inputs.test.tsx src/components/baselineComparison/BaselineComparisonWorkspace.test.tsx src/labelCasing.test.ts && npm run build`
- **Mutation Testing (`go test -overlay`):**
  - `m1_skip_tombstone_check`: Bypassed the head-revision tombstone check (`latestDef.Deleted`) in `verifyAndPopulateBrowserPanelV2` (`pkg/api/browser_panel_inputs.go`) — killed by `TestBrowserPanelInputPinsHTTPV2PolicyTombstoneAndSelectionReceipt`.
  - `m2_skip_selection_receipt_bind`: Omitted `expected.SelectionReceiptSHA256 != pins.SelectionReceiptSHA256` in `bindBrowserPanelInputs` (`pkg/api/browser_panel_inputs.go`) — killed by `TestBrowserPanelInputPinsV2PolicyAndSelectionPins` and `TestBrowserPanelInputPinsHTTPV2PolicyTombstoneAndSelectionReceipt`.

## Review

- **Round 1 (`BE-B3 Code Safety Reviewer` & `BE-B3 Test and QA Reviewer`):**
  - `BE-B3 Test and QA Reviewer`: 0 issues found.
  - `BE-B3 Code Safety Reviewer`: Found 4 issues (1 Medium, 3 Low) — all fixed:
    1. **[Medium]** `verifyAndPopulateBrowserPanelV2` performed redundant DB lookups on untrusted `q.BrowserInputs` policy pins and could bind a `selection_receipt_sha256` from a refused/mismatched RCA baseline selection when `q.ComparisonRunID != ""`. Fixed by verifying only manifest-derived `response.Current.policyPins` and `response.Baseline.policyPins` in `verifyAndPopulateBrowserPanelV2` (leaving `bindBrowserPanelInputs` to enforce exact equality with `q.BrowserInputs`) and requiring `validateRCABrowserSelection(payload) == nil` and baseline test ID compatibility before binding `selection_receipt_sha256`.
    2. **[Low]** Frontend `verifyBrowserPanelInputs` did not enforce trimmed `pin.id`, strictly ascending `kind`, or `policy_pins == current.policy_pins` parity with `pkg/storage`. Fixed in `dashboard/src/components/browserPanelInputs.ts` and updated v2 test fixtures.
    3. **[Low]** `BaselineComparisonWorkspace.tsx` `saveToPanel` could generate invalid `end_seconds <= start_seconds` or `> 1000` buckets with custom windows and did not prefer the `browser_documents` row matching `baselineRunId`. Fixed by clamping window bounds, scaling `bucket_seconds`, and preferring the `baselineRunId` document row.
    4. **[Low]** `apexctl analysis replay-panel` silently ignored `--file` and `--project`. Fixed in `cmd/apexctl/analysis_workflows.go` to reject `--file` and verify `def.ProjectID` when `--project` is supplied.
- **Round 2 (`BE-B3 Code Safety Reviewer` & `BE-B3 Test and QA Reviewer`):**
  - `BE-B3 Test and QA Reviewer`: 0 issues found.
  - `BE-B3 Code Safety Reviewer`: Found 1 Low regression (`verifyBrowserPanelInputs` dropped `tuple_ids` validation when rewritten). Fixed by restoring `validTupleList` in `dashboard/src/components/browserPanelInputs.ts` and adding an out-of-order `tuple_ids` assertion in `dashboard/src/components/browserPanelInputs.test.ts`.
- **Round 3 (`BE-B3 Code Safety Reviewer` & `BE-B3 Test and QA Reviewer`):** 0 issues found (0 High, 0 Medium, 0 Low).

## Remaining

- Report/RCA selection provenance when `browser_baseline` is `nil` and `apexctl schedule` / `ci run` / `run --api` policy flags (`BI-C4`).
- Provisional 30 s live findings, interactions, and journeys (`BI-C1`).
- Unstubbed full-chain Playwright E2E across both auth profiles (`BI-C5`).
