# Script Authoring Consolidation Roadmap

## Objective
Consolidate Stormrunner's script-building system around shared contracts and lifecycle behavior without collapsing distinct user entry points into one monolithic UI.

The goal is to keep the current acquisition paths that matter:

- Direct editing
- Import
- Recording
- Replay/debugging

While consolidating the parts that are currently drifting:

- Validation
- Convert/import response shapes
- Review and save workflow
- Stale duplicate files and migration artifacts

Test and scenario assembly should be improved in parallel, but treated as a separate implementation slice from script authoring.

## Principles

### 1. Consolidate Contracts, Not Inputs
Multiple ways to create scripts are valid. The target is shared orchestration and behavior after acquisition, not one single input screen.

### 2. Validation Must Have One Owner
The same script should produce the same validation result whether it is parsed from disk, saved through the API, replayed, or imported from another format.

### 3. Importers Should Differ Only At Acquisition Time
LoadRunner, JMeter, sample import, and recorder conversion should keep their source-specific input logic, but converge on one review, edit, and save contract.

### 4. Script Authoring And Test Assembly Are Adjacent, Not The Same
Script creation defines executable behavior. Test and scenario assembly define how those scripts are run. They can share components, but they should remain separate concerns.

## Current Problem Areas

### Validation Drift
Validation logic is currently split between parser-level and API-level code paths, which creates redundant logic and the risk of inconsistent results.

### Import Contract Fragmentation
Different script-building sources return different response envelopes and use different preview and save semantics.

### Frontend Workflow Duplication
The script hub currently owns separate orchestration paths for multiple builders instead of delegating into a shared review and save model.

### Duplicate And Stale Files
The dashboard contains duplicate page files and the repository still includes patch and temp artifacts that appear to reflect prior ad hoc refactors.

## Implementation Phases

### Phase 0: Lock Boundaries
Document the supported surfaces and classify them into three lanes:

- Script authoring
- Script replay/debugging
- Test and scenario assembly

This phase should also confirm that the live app shell imports only the current dashboard pages and that duplicate page copies are cleanup candidates rather than active variants.

#### Exit Criteria
- The live route owners are confirmed.
- Duplicate page files are classified as active or dead.
- Browser replay is explicitly classified as either a specialized debug surface or a future participant in the shared authoring lifecycle.

### Phase 1: Unify Validation
Move shared validation out of API handlers and parser-local functions into one DSL-owned validator API.

All of the following paths should use the same validation rules:

- Parser
- Create script
- Update script
- Validate script
- Replay draft script
- Replay stored script
- Browser replay
- Import save paths

JMS-specific validation should be exposed through the same validation surface so protocol-specific rules remain consistent with general validation.

#### Exit Criteria
- One validation API exists under the DSL package.
- Parser and handlers call the same validator.
- Browser and JMS special cases are covered.
- Validation tests prove parity across all entry points.

### Phase 2: Standardize Import Contracts
Define a shared backend import result envelope for all script-building sources.

The shared outer contract should support:

- One or more scripts
- Warnings
- Stats
- Optional correlations
- Optional assumptions
- Optional unsupported elements
- Optional scenario draft

Each source can still include source-specific details, but the frontend should no longer need custom normalization for unrelated payload shapes.

This phase should also normalize lifecycle semantics so all import and conversion flows behave like:

1. Acquire input
2. Validate or convert
3. Review the result
4. Optionally edit
5. Save

#### Exit Criteria
- LoadRunner, JMeter, sample import, and recorder conversion emit the same envelope shape.
- Preview and save semantics are consistent across builders.
- Temporary compatibility adapters are minimized and explicitly tracked.

### Phase 3: Consolidate Frontend Orchestration
Turn the script hub into a shared authoring shell instead of a bespoke controller for every builder.

The specialized components should keep source-specific acquisition logic, but downstream review and save behavior should converge on shared UI patterns:

- Review summary
- Warning display
- Stats display
- Correlation display where applicable
- Edit handoff
- Save behavior

The default canonical edit surface should be the main script editor unless a source-specific editor is demonstrably necessary.

#### Exit Criteria
- The script hub delegates into a shared authoring workflow.
- Importers no longer own unrelated review and save behavior.
- Users can complete the same lifecycle from every authoring path.

### Phase 4: Consolidate Scenario Assembly Separately
Extract reusable scenario assembly state and sections from the existing test creation flow.

This work should stay separate from script authoring and focus on improving reuse across:

- New test creation
- Test execution scenario editing

The current goal is to make scenario editing a thin wrapper over a stable shared assembly surface.

#### Exit Criteria
- Scenario assembly state is reusable.
- Shared sections are isolated and stable.
- Test creation and scenario editing use the same underlying assembly model.

### Phase 5: Cleanup And Remove Adapters
After parity checks, remove stale dashboard duplicates and confirmed obsolete patch artifacts.

Then remove temporary normalization and migration code introduced during earlier phases.

#### Exit Criteria
- No references remain to duplicate dashboard page files.
- Patch and temp artifacts are either removed or documented as intentionally retained.
- Temporary adapters introduced during migration are deleted.

## Recommended Execution Order
The recommended order is:

1. Validation first
2. Import contract second
3. Frontend orchestration third
4. Cleanup last

That order minimizes churn and avoids rebuilding the UI around unstable backend behavior.

## Implementation Status

### Completed
- Phase 1 validation unification is in place through the shared DSL validator.
- Phase 2 backend import envelopes are standardized across LoadRunner, JMeter, sample import, and recorder conversion.
- The shared frontend import model and review/save contracts are in place for the active import and recorder paths.
- Shared script, scenario, and dashboard API hook typing has been tightened across the current live authoring surfaces.
- The temporary generic frontend import-result normalizer has been removed; the active LoadRunner, sample, and recorder paths now consume their typed backend envelopes directly, while JMeter keeps only its source-specific default filling.
- Phase 5 cleanup has started in the repo: duplicate dashboard page copies and obsolete patch/temp artifacts were removed after confirming that the live app shell imports only the current page surfaces.

### In Progress
- Phase 3 frontend orchestration is partially consolidated through shared error handling, typed recorder/import flows, and a single ScriptsPage authoring-state model for the active transitions, but the script hub still owns the remaining high-level route selection.
- The recorder path now uses the same preview-first review/edit/save lifecycle as the other active non-JMeter builders, with recorder output able to hand off into the canonical script editor before save.
- The active non-JMeter builders now share a common review/save panel structure for title, warnings, edit handoff, save action, and script preview while keeping source-specific stats and metadata sections local.
- The scripts library authoring-state hook now has focused regression coverage for create-mode normalization, draft entry, edit loading, and failed edit fetch behavior, so the remaining route-level consolidation work is bounded by explicit state-transition tests instead of page-only behavior.
- The live script library now uses typed sort helpers for both reusable scripts and linked scenario runs instead of local `any` sort casting.
- Phase 4 scenario assembly cleanup has started from the reusable test-creation surface. `CreateTestModal` now uses typed payload, chart, and callback contracts instead of local `any` seams, and the scenario draft/group mapping shared with `TestExecutionScenarioEditor` now lives in one helper instead of being rebuilt in each caller.
- `CreateTestModal` now uses one shared `ScenarioDraftState` reducer for core fields, goal config, runtime overrides, and group mutations, so scenario assembly no longer splits form ownership across three local reducers.
- The execution surface now shares typed history, archive summary, live-control, and chart contracts instead of relying on local `any` casts in the main `TestExecution` path.
- Focused dashboard regression tests now cover the two browser-found scenario-surface defects: duplicate associated-release keys in `NewTestPage` and the queued preview summary/edit interaction in `TestExecutionScenarioPreview`.
- Scenario payload normalization is now shared between `CreateTestModal` and `TestHub`, so group shaping, goal-config/runtime normalization, and single-group flattening are owned in one helper instead of being rebuilt in each save path.
- The queued-run execution editor now makes its behavior explicit: revising a queued run opens a revised-copy flow in `TestExecutionScenarioEditor`/`CreateTestModal` instead of implying an in-place scenario edit that the current test API does not support.
- The queued execution preview model now lives in a shared helper instead of being derived inline inside `TestExecution`, so the queued load-shape timeline and script-row summaries are covered as execution-side data contracts rather than page-local calculations.
- The queued execution header actions now own their revise-modal bridge in a dedicated component instead of keeping queued-run modal state inside `TestExecution`, which narrows the page to execution data and control orchestration rather than queued-only UI state.
- The live execution controls now consume a shared preview model helper instead of deriving projected VUser and duration state inline inside `TestExecution`, so both queued and running execution previews are converging on testable execution-side contracts.
- The execution metrics history path now uses shared helper functions for backend-history mapping, websocket/backend reconciliation, paused-chart freeze behavior, zoom filtering, and summary rollups instead of keeping those calculations inline in `TestExecution`.
- The execution chart zoom state now uses a shared reducer model for time-range changes and drag-to-zoom ref-area behavior instead of keeping those state transitions inline inside `TestExecution`.
- The queued execution preview now loads script details through a dedicated preview-model hook instead of keeping that fetch lifecycle and row assembly inline in `TestExecution`.
- The archive report, drill-down fetches, pagination, and archive download actions now flow through a dedicated execution-archive hook instead of keeping that state and fetch lifecycle inline in `TestExecution`.
- The execution test detail page now loads and refreshes test data through a dedicated polling hook instead of keeping the visibility-aware fetch loop inline in `TestExecution`.
- The live execution controls now load their form state, preview inputs, and scale/duration action bridge through a dedicated live-controls hook instead of keeping those mutations inline in `TestExecution`.
- The execution detail page now consumes a dedicated metrics-stream hook for websocket message mapping and rolling local history instead of keeping that append logic inline in `TestExecution`.
- The remaining execution action bridge now flows through a dedicated hook for queued-start advisories, optimistic pause/resume updates, and the shared control-feedback channel instead of keeping those mutations inline in `TestExecution`.
- The remaining execution page UI toggles now flow through one small view-state hook for expanded charts and queued script panels instead of keeping the last standalone UI state fields inline in `TestExecution`.
- The queued execution scenario hook now owns both the queued run-shape preview summary and the script-detail row assembly instead of splitting that same scenario-preview contract between `TestExecution` and `useExecutionScenarioPreviewModel`.
- The execution detail page now consumes a dedicated status/result model helper for goal-controller copy, transaction/group collections, shakeout state, and multi-group flags instead of deriving those execution view contracts inline in `TestExecution`.
- The execution detail page now routes chart zoom state, backend/local history reconciliation, filtered timeline slices, and summary rollups through a dedicated chart-model hook instead of keeping that reducer-and-memo pipeline inline in `TestExecution`.
- The execution detail page now renders its multi-group summary chips and shakeout-failure banner through a dedicated scenario-overview component instead of keeping those last scenario-specific UI blocks inline in `TestExecution`.
- The execution detail page and header now share one small phase model for running, paused, live-control, and archive-visibility rules instead of duplicating those status gates between `TestExecution` and `TestExecutionHeader`.
- The execution action bridge now owns stop and abort transitions alongside start, pause, and resume, including the shared optimistic status path for `STOPPING` and `ABORTED`, instead of leaving those last execution actions inline in `TestExecution`.
- The execution metrics stream now enables the per-test websocket only for active execution phases instead of attempting a live stream for completed or aborted runs, which removes terminal-run stream churn from the execution detail page.
- The LoadRunner import flow now exposes `preserve_transactions` end-to-end and supports a validate-only review path, so it follows the same preview-first lifecycle shape as the newer JMeter importer instead of forcing conversion as the only review entry point.
- The LoadRunner import response now promotes unsupported functions and project/runtime review data into standardized `unsupported_elements` and `summary` fields, and the review UI renders those sections directly instead of relying on preserved metadata JSON for first-pass migration cleanup.
- The JMeter import review now uses the shared import-review shell and edit handoff instead of keeping a bespoke review header/save shell that diverged from the LoadRunner, sample, and recorder flows.
- The scripts library now routes create/import/edit authoring transitions through a dedicated authoring-state hook instead of keeping those route-level draft/edit/subview transitions inline in `ScriptsPage`.
- The scripts library create-options surface now renders through its own authoring-shell component instead of keeping the import/editor launcher lanes inline in `ScriptsPage`.
- The scripts library now renders editor/import/recorder/sample authoring subviews through a dedicated authoring-surface component instead of keeping that switch and wrapper markup inline in `ScriptsPage`.
- The scripts library now drives table sorting, linked-run expansion, cached run loading, and inner-run sorting through a dedicated library-model hook instead of keeping that state and fetch logic inline in `ScriptsPage`.
- The scripts library table and linked-run drill-down markup now render through a dedicated table component instead of keeping the entire sortable library table inline in `ScriptsPage`.
- The scripts library now sources protocol-specific starter drafts from a dedicated factory module instead of keeping HTTP, browser, and JMS default draft definitions inline in `ScriptsPage`.
- The scripts library now renders list, create-options, and active authoring subviews through a dedicated `ScriptsAuthoringShell`, leaving `ScriptsPage` focused on library fetch, refresh, delete, and table callbacks.
- Editor replay and LoadRunner browser-import replay validation now build replay requests through one shared helper, so draft, saved, and imported browser paths agree on project binding, group names, variables, browser defaults, viewport fallback, and screenshot-capture behavior.
- Draft script validation now has a first-class API endpoint and the script editor uses one validation action for both unsaved drafts and stored scripts, keeping browser compatibility warnings and validation issues aligned before save.
- The script editor now returns the API's stored-script payload through the shared save callback, giving the authoring shell a concrete persisted-script handoff for both create and update saves.
- Saving a draft script now advances the shared authoring state into persisted edit mode instead of dropping back to the library immediately, so create/edit/save flows stay in one continuous editor lifecycle while the script library refreshes in parallel.
- LoadRunner, JMeter, sample-import, Java-over-HTTP sample-import, and recorder save actions now use that same persisted-script handoff, so saved import review flows reopen in the canonical editor instead of ending at the library list.
- Multi-script import saves now preserve that lifecycle context too: the shared shell reopens the first persisted script in edit mode and surfaces a follow-on notice when the import also saved additional scripts and/or a scenario draft.
- Import save handoffs now also backfill the reviewed draft payload when the save API returns a thin stored-script response, so persisted reopen stays hydrated for import-review flows even without an embedded script body from the backend.
- Import and recorder subviews now share one create-options frame in `ScriptsAuthoringSurface`, which removes the remaining per-surface back-navigation drift and starts trimming duplicated wrapper orchestration as the contracts stabilize.
- The remaining JMeter response adapter has been removed from the dashboard. `JMeterImport` now trusts the backend's shared import envelope directly, so A1.5 cleanup has started by deleting one of the last temporary normalization seams rather than keeping it alive out of habit.

### Remaining High-Value Work
- Finish the scenario assembly path by tightening `TestExecution` adjacencies and shared execution/result models now that `CreateTestModal` is aligned on one shared draft state owner.
- Continue shared authoring lifecycle work by tightening any remaining reopen-after-save inconsistencies beyond the current multi-script import and scenario-draft follow-on coverage.
- Run the end-to-end parity pass, then remove any temporary normalization or migration adapters that remain after validation.

### Current Cleanup Note
- The duplicate page copies and obsolete patch/temp artifacts have been removed.
- Remaining cleanup work is now focused on parity verification, route-level orchestration cleanup, and adapter removal rather than dead-file deletion.

## File-Level Targets

### Live Dashboard Entry Points
- [dashboard/src/App.tsx](dashboard/src/App.tsx)
- [dashboard/src/components/ScriptsPage.tsx](dashboard/src/components/ScriptsPage.tsx)
- [dashboard/src/components/ScriptEditor.tsx](dashboard/src/components/ScriptEditor.tsx)
- [dashboard/src/components/JMeterImport.tsx](dashboard/src/components/JMeterImport.tsx)
- [dashboard/src/components/LoadRunnerImport.tsx](dashboard/src/components/LoadRunnerImport.tsx)
- [dashboard/src/components/SampleScriptImport.tsx](dashboard/src/components/SampleScriptImport.tsx)
- [dashboard/src/components/RecordingStudio.tsx](dashboard/src/components/RecordingStudio.tsx)
- [dashboard/src/components/BrowserReplayDebugger.tsx](dashboard/src/components/BrowserReplayDebugger.tsx)

### Scenario Assembly Surfaces
- [dashboard/src/components/CreateTestModal.tsx](dashboard/src/components/CreateTestModal.tsx)
- [dashboard/src/components/TestExecutionScenarioEditor.tsx](dashboard/src/components/TestExecutionScenarioEditor.tsx)
- [dashboard/src/components/CreateTestGroupsEditor.tsx](dashboard/src/components/CreateTestGroupsEditor.tsx)
- [dashboard/src/components/CreateTestGoalSettings.tsx](dashboard/src/components/CreateTestGoalSettings.tsx)
- [dashboard/src/components/CreateTestRuntimeOverrides.tsx](dashboard/src/components/CreateTestRuntimeOverrides.tsx)

### Backend Validation And Contracts
- [pkg/dsl/parser.go](pkg/dsl/parser.go)
- [pkg/dsl/types.go](pkg/dsl/types.go)
- [pkg/api/handlers.go](pkg/api/handlers.go)
- [pkg/api/handlers_scripts.go](pkg/api/handlers_scripts.go)
- [pkg/api/handlers_recorder.go](pkg/api/handlers_recorder.go)
- [pkg/api/handlers_replay.go](pkg/api/handlers_replay.go)
- [pkg/api/handlers_browser_replay.go](pkg/api/handlers_browser_replay.go)
- [pkg/convert/loadrunner/converter.go](pkg/convert/loadrunner/converter.go)
- [pkg/convert/jmeter/converter.go](pkg/convert/jmeter/converter.go)
- [pkg/convert/sample/converter.go](pkg/convert/sample/converter.go)
- [pkg/recorder/converter.go](pkg/recorder/converter.go)

### Cleanup Status
- Route-owner verification confirmed that the live shell imports only the current dashboard pages.
- Duplicate dashboard page copies and obsolete patch/temp artifacts were removed from the repository.
- The remaining cleanup target is temporary adapter code that may still exist behind the now-stable shared contracts.

## Verification Checklist

1. Completed: confirm that only live dashboard pages are referenced before deleting duplicate page files.
2. In progress: build a validation matrix and verify that parser, create, update, draft validation, stored validation, replay, browser replay, and import save all return the same validation outcome for the same scripts.
3. Snapshot current import payloads before standardization, then verify that every builder emits the shared envelope afterward.
4. Verify that each authoring path supports the same lifecycle after consolidation: acquire input, review warnings and stats, edit if needed, save, and reopen the saved script.
5. Verify that scenario editing still works both from new test creation and execution-time editing after scenario assembly extraction.
6. Run final parity checks before cleanup so no route, create-script option, import flow, or saved workflow still references deleted files or temporary adapters.

## Decisions

- Keep multiple entry points into script creation.
- Make the main script editor the canonical edit destination by default.
- Treat the richer JMeter result model as a likely baseline for the shared import envelope.
- Keep script authoring and test/scenario assembly in separate implementation tracks.
- Do not force browser replay into the shared model unless its lifecycle aligns cleanly.

## Risks

- Adapter sprawl during migration if temporary normalization code is allowed to linger.
- Frontend churn if the UI is refactored before the backend validation and import contracts stabilize.
- Cleanup regressions if temporary adapters are deleted before route and parity verification is complete.

## Best First Slice
If the team wants one early win before full consolidation, the best first slice is validator unification. It reduces defects immediately and clarifies the contract that every later phase depends on.
