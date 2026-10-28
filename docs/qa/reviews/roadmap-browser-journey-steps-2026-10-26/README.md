# BI-B3 — Structural Intra-Iteration Journey Attempts (`StepPath` Binding)

## Scope

This slice implements structural intra-iteration journey boundaries (`BI-B3`) across the VUser engine, archive pipeline, analysis policy API, journey accounting evaluator, and dashboard policy/report views:

- **Engine & Protocol Attribution (`pkg/protocol`, `pkg/engine`)**:
  - Exported `ValidBrowserAuthoredStepPath` and `ValidBrowserAuthoredDigest` in `pkg/protocol/browser_authored.go` and added `AuthoredStep *BrowserAuthoredStep` onto `protocol.Result` (`pkg/protocol/protocol.go`).
  - Wired `currentAuthoredAction` / `currentBrowserAuthoredStep()` onto `VUser` (`pkg/engine/vuser.go`, `pkg/engine/vuser_run.go`, `pkg/engine/control_parallel.go`) so leaf action results and automatic per-action transactions record their exact structural `StepPath`, while synthetic whole-iteration transactions remain free of leaf `AuthoredStep`.
  - Extended `sourceTransactionRuntimeState` (`pkg/engine/vuser_source_actions.go`) to capture the opening `lr_start_transaction` / `lr_start_sub_transaction` structural `AuthoredStep` and propagate it when `lr_end_transaction` / `lr_end_sub_transaction` or `lr_set_transaction` records the business transaction.
  - Added `ScriptJourneySteps(script *dsl.Script)` (`pkg/engine/browser_authored.go`) to enumerate structural iteration step boundaries (`step_path`, `kind`, `name`, `action_type`) and the script's `StructureSHA256`.
- **Archive Transport & Sanitization (`pkg/distributed/generatorrpc`, `pkg/api`)**:
  - Added `AuthoredStep *protocol.BrowserAuthoredStep` to `archiveTransactionEvent` (`pkg/distributed/generatorrpc/archive.go`) and `TransactionEvent` (`pkg/api/archive.go`, `pkg/api/archive_writer.go`), validated fail-closed in `sanitizeArchivedTransaction` (`pkg/api/archive_transaction_sanitize.go`).
- **Journey Boundary Policy, Step Catalog & Accounting (`pkg/api`)**:
  - Extended `JourneyBoundary` with optional `StepPath` and `StructureSHA256` (`pkg/api/journey_accounting.go`), validated via `ValidBrowserAuthoredStepPath` (`actions[...]`) and `ValidBrowserAuthoredDigest` (refusing `StructureSHA256` when `StepPath` is omitted).
  - Added `GET /api/v1/analysis/journey-steps` (`handleListAnalysisJourneySteps` in `pkg/api/analysis_policies.go`, registered in `pkg/api/route_registry_analysis.go`) returning per-script structural step catalogs for the project.
  - Updated `JourneyBoundary.matches` and `JourneyAccounting.MatchMode` (`"step_path"` vs `"name"`): when `StepPath` is configured and `event.AuthoredStep` is present, matching requires `event.AuthoredStep.StepPath == b.StepPath` (and `SourceStructureSHA256 == b.StructureSHA256` when pinned), disambiguating duplicate display names and surviving non-structural script edits; legacy archives without `AuthoredStep` fall back to `Name` only when `StructureSHA256` is not pinned and disclose `match_mode: "name"`.
  - Updated `closeIteration` (`pkg/api/journey_accounting.go`) with `beginOrdinal` / `seq` ordering so zero-duration (`begin == end`) loop passes complete each attempt before the next pass opens while zero-duration transaction wrappers open before and close after their enclosed steps.
- **Dashboard Editor & Run Report (`dashboard/src`)**:
  - Extended `JourneyDraft`, `JourneyPolicyFields` (`AnalysisPoliciesPanel.tsx`), and `analysisPoliciesApi.listJourneySteps` (`analysisPoliciesApi.ts`) with a structural script step picker and `step_path` inputs while preserving any existing `structure_sha256` on the policy revision until the step path is edited.
  - Updated `journeyRiskModel.ts` and `JourneyRiskPanel.tsx` to format structural `step_path` boundaries and display `Step path` vs `Name fallback` match modes.

## Verification Evidence

- **Go Unit & Race Tests**:
  - `go test -race -count=2 -shuffle=on ./pkg/protocol -run 'TestBrowserAuthored'`
  - `go test -race -count=2 -shuffle=on ./pkg/engine -run 'TestBrowserAuthored|TestBrowserOriginal'`
  - `go test -race -count=2 -shuffle=on ./pkg/distributed/generatorrpc -run 'TestArchive'`
  - `go test -race -count=2 -shuffle=on ./pkg/api -run 'TestListAnalysisJourneyStepsAndBoundaryValidation|TestJourneyAccountingStepPath|TestJourneyAccountingZeroDuration'`
- **Mutation Testing (`go test -overlay`)**:
  - Mutant 1 (`pkg/api/journey_accounting.go` `if event.AuthoredStep.StepPath != b.StepPath` -> `if false`): killed by `TestJourneyAccountingStepPathSeparatesDuplicateNamesAndSurvivesNonStructuralEdits` and `TestJourneyAccountingZeroDurationLoopTieBreaking`.
  - Mutant 2 (`pkg/api/journey_accounting.go` `if left.seq != right.seq` -> `if false`): killed by `TestJourneyAccountingZeroDurationLoopTieBreaking`.
- **Dashboard Checks**:
  - `cd dashboard && npm run typecheck && npm run lint && npm run lint:a11y`
  - `cd dashboard && npx vitest run src/components/analysisPolicies/policyEditorModel.test.ts src/components/analysisPolicies/AnalysisPoliciesPanel.test.tsx src/components/runReport/JourneyRiskPanel.test.tsx src/labelCasing.test.ts` (4 files, 24 tests passed)
  - `cd dashboard && npm run build && npm run test:bundle-budget && npm run bundle:check`

## Remaining Open Items

- Outcome-deadline composition (`BI-B1` / `BI-B2`) and interaction map cohort extensions (`BI-B4`) are tracked in their dedicated slices.
