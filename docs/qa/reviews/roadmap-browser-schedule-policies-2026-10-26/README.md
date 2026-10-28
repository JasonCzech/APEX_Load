# Roadmap Review — Recurring Schedule Browser & Journey Policy Selection (`BI-C2`, 2026-10-26)

## Scope

Work package `BI-C2` (`BI-4`, `docs/ROADMAP_IMPLEMENTATION_SPEC.md` §BI-C):

1. **Durable Schedule Persistence (`pkg/storage`, Schema v85):**
   - Added `AnalysisPolicySelections any` (`json:"analysis_policy_selections,omitempty"`) to `storage.ScenarioSchedule` and `analysis_policy_selections_json TEXT` to `scenario_schedules` DDL and `schemaColumnMigrations` (schema v85 `scenario-schedule-analysis-policy-selections`).
   - Updated SQLite/PostgreSQL schedule create, CAS update, approval-atomic save, and scan paths in `pkg/storage/sqlite_schedules.go` and regenerated `docs/DATA_MODEL.md`.
2. **Schedule Validation, Materialization & Tombstone Refusal (`pkg/api`):**
   - Added `AnalysisPolicySelections` to `CreateScenarioScheduleRequest` and validated selected browser/journey policy revisions in `PreviewScenarioScheduleRuns`, `CreateScenarioSchedule`, and `UpdateScenarioSchedule` via `validateScenarioSchedulePolicySelections`.
   - Preserved existing `AnalysisPolicySelections` when omitted on partial `PUT /api/v1/scenarios/{id}/schedules/{schedule_id}` updates and cleared back to project defaults (`nil`) when explicitly passed `{}`.
   - Propagated `AnalysisPolicySelections` into `runScenarioScheduleLocked` so materialized scheduled runs pin the selected historical revision (`source: "run_selection"`) even after newer policy revisions are published, and fail materialization with `occurrence.Status = "failed"` and `occurrence.ReasonCode = "analysis_policy_unavailable"` when a selected policy is deleted before materialization.
   - Mapped configuration errors (`errAnalysisPolicySelectionUnavailable`) to `409 Conflict`, caller-supplied digest/malformed errors (`errInvalidRunAnalysisPolicySelection`) to `400 Bad Request`, and store read outages (`errRunPolicyStoreUnavailable`) to `503 Service Unavailable` across schedule preview, create, and update handlers.
3. **Dashboard Schedule Authoring & Catalog (`dashboard/`):**
   - Added `analysis_policy_selections` to `ScenarioSchedule`, `ScenarioScheduleRequest`, and `ScheduleDraft` (including `newTestDraft.ts` storage validation).
   - Mounted `<RunPolicySelector>` in the `NewTestPage.tsx` schedule form, wired `analysis_policy_selections` into debounced schedule preview, create, and update requests, and rendered pinned revision chips (`browser policy rN`, `journey policy rN`) in the saved schedules table and `NewTestScheduleCatalog.tsx`.

## Validation & Mutation Verification

- **Storage Unit & Migration Tests:**
  - `go test -race -count=1 ./pkg/storage -run "TestAnalysisPolicySelection|TestRunAnalysisPolicySelections|TestSQLiteStorePersistsScenarioSchedules|TestSchema"`
- **API Unit & Integration Tests:**
  - `go test -race -count=1 ./pkg/api -run "TestRunAnalysisPolicySelection|TestScenarioScheduleAnalysisPolicySelection"`
- **Dashboard Unit, Typecheck, Lint & Playwright E2E:**
  - `cd dashboard && npm run typecheck && npm run lint`
  - `cd dashboard && npx vitest run src/types/openapiContract.test.ts src/components/newTestDraft.test.ts src/components/NewTestScheduleCatalog.test.tsx src/components/NewTestPage.test.tsx src/labelCasing.test.ts`
  - `cd dashboard && npx playwright test e2e/workflow-run-policy-selection.spec.ts` (both `dev-admin` and `local-auth` profiles)
- **Mutation Checks (`go test -overlay`):**
  - Mutant 1 (`pkg/api/scheduler.go`): Drop `AnalysisPolicySelections: decodeStoredRunAnalysisPolicySelections(schedule.AnalysisPolicySelections)` in `runScenarioScheduleLocked` → caught by `TestScenarioScheduleAnalysisPolicySelectionValidationMaterializationAndTombstone` (materialized run falls back to project default rev 2 instead of pinned rev 1).
  - Mutant 2 (`pkg/api/scheduler.go`): Omit `occurrence.ReasonCode = "analysis_policy_unavailable"` on `errAnalysisPolicySelectionUnavailable` in `runScenarioScheduleLocked` → caught by `TestScenarioScheduleAnalysisPolicySelectionValidationMaterializationAndTombstone`.

## Remaining Dependencies

- CLI/MCP parity for `--browser-policy` and `--journey-policy` on `apexctl schedule`, `apexctl ci run`, and `apexctl run --api` is tracked in `BI-C4`.
