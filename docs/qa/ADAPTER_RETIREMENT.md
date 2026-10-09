# Adapter retirement inventory (AUTH-3)

Status: Wave 5 (A8), 2026-10-09. This is the AUTH-3 seam inventory of the [feature completion specification](../FEATURE_COMPLETION_SPEC.md#auth-3-adapter-retirement-inventory). For each seam it records the decision, the canonical owner that now resolves its values, the callers that were moved, and the tests that prove parity.

Retired seams are kept retired by `dashboard/src/adapterRetirement.test.ts`. That ratchet fails when application code names a retired adapter or one of its mirror fields again. Tests are not scanned, so they can still name an old value to prove it is refused or translated.

## Retired seams

### `aliasImportState` / route selection

- **Decision:** retired. Importers are opened by a launcher descriptor (launcher and optional mode), and the Scripts sub-view that renders one is derived from it (`dashboard/src/scriptImportLaunchers.ts`).
- **Callers:**
  - **Route mode:** the Scripts route only ever accepted `list`, `create`, `edit` and `import`, so no caller passed an alias.
  - **`setSubview`:** no caller passed an alias either.
  - **Dead branches removed:** ten alias sub-views (`har-import`, `openapi-import` and so on) had renderer branches in `ScriptsAuthoringSurface` and title entries in `appTopbar` that were unreachable; both were removed.
- **Route boundary:**
  - An import route names its importer: `#/scripts?mode=import&launcher=<id>[&importMode=<mode>]`.
  - The page writes the launcher into the address, so a reload or a shared link reopens the same importer. Before, it reopened the LoadRunner archive importer.
- **Old addresses:** see below.
- **Proof:**
  - `appRouting.test.ts` checks the old address, round trips and an unknown launcher.
  - `useScriptsAuthoringState.test.tsx` checks a descriptor for every surface family, and that a return target reopens its launcher.
  - `appTopbar.test.ts` checks the titles.
  - `workflow-import-source.spec.ts` ("import addresses name their importer…") runs in a real browser in both profiles.

### `CompatibleDebugStartRequest`

- **Decision:** retired. `ScriptDebugStartRequest` is now the generated transport body of `handleStartScriptDebugScript`, with only `script` typed by the DSL's `ScriptDefinition`.
- **Why `script` is retyped:** the generated contract carries a script's open maps (assertions, conditions, control steps) as open objects.
- **What the adapter allowed:** it let any domain field override its transport type.
- **Callers:**
  - `useScriptApi.startScriptDebugSession` and `startDraftScriptDebugSession`.
  - Every component that builds a start request type-checks against the generated body.
- **Proof:** `openapiContract.test.ts` checks three things:
  - the request's keys equal the generated keys;
  - the draft route takes the same keys;
  - every field except `script` has the transport's own type.

### TestHub flat `ScenarioFormState` mirrors and `scenarioFormModel` runtime reconstruction

- **Decision:** retired. The scenario form keeps only canonical values: `runtime_settings` (the stored override), `goal_config` and `assignment`. Every editor writes those directly.
- **Fields removed:** 27 mirror fields, which the save used to compose back into the canonical values:
  - runtime pacing, think time, iterations, logging and auto-transaction fields;
  - the `continue_on_error` and `snapshot_on_error` flags;
  - the four browser fields;
  - the five goal bounds;
  - `assignment_mode` and `assignment_targets`.
- **Writers moved:**
  - wizard seeds (`seededScenarioRuntime`);
  - load-model adaptation (`withAdaptedGoalBounds`);
  - traffic-profile application;
  - goal-driven updates;
  - the root placement editor;
  - the error-handling checkboxes (`withScenarioErrorHandlingFlag`).
- **Readers:** `runtimeSettingsFromForm`, `goalConfigFromForm` and `assignmentFromForm` now read the canonical values.
- **Conflicting legacy aliases:** where a stored override has both a legacy alias and its canonical section, the alias is dropped (top-level `iterations` and `auto_transactions` under `run_logic`, `log_level` under `log`, the legacy `browser` block under `browser_config`).
- **Proof:**
  - **Parity corpus:** `scenarioAssemblyParity.test.ts` projects a corpus of stored scenarios (`dashboard/src/test/scenarioAssemblyParityFixtures.ts`) into the save request, capacity estimate, composition timeline and load preview. Its golden file was first recorded from the retired reconstruction. Retirement changed exactly two things, both corrections:
    - a think-time section saved without `ignore` is no longer given `ignore: false`;
    - an unchanged save no longer re-normalizes a stored section.
  - **Unchanged saves:** every corpus runtime is saved exactly as stored, apart from the conflicting-alias fixture, which drops its aliases.
  - **Editor tests:** `TestHub.test.tsx` round-trips the canonical sections and checks hidden fields through the editor.

### `newTestRunModel` effective/override composition

- **Decision:** the run-only projection stays (run default overrides, group run overrides, clear tombstones and the run payload). Only proven duplicated workload calculations were removed.
- **Duplicates removed:**
  - **Stage peak:** three copies (`scenarioAssembly`, `scenarioWizardUtils`, `newTestRunModel`).
  - **Stage level at a time:** two copies that disagreed:
    - the run preview kept a stage at its exact end, while the controller has already applied the next one;
    - the wizard chart started every schedule from zero, while the controller ramps from the group's current level.
  - Both now follow `dashboard/src/components/workloadStages.ts`, which mirrors `runStageSchedule` in `pkg/api/testexecution.go`.
- **Kept per caller:**
  - **Missing target:** the controller reads it as zero, while the wizard keeps the level for TPS-only stages.
  - **Empty or unparseable duration:** the controller refuses one, while the wizard draws an unfinished draft stage as a minute.
- **Proof:** `workloadStages.test.ts` covers boundaries, starting levels and caller options, and shows the editors' model chart and the run preview draw the same staged curve.

### `testExecutionScenarioModel` preview calculation

- **Decision:** retired. The execution page's preview of a queued run is the shared run projection (`buildRunLoadProfilePreview`), the same one the launch page and the editors' load preview use.
- **Group start offsets:** that projection now honors them. A group activates at its offset, its ramp and stages count from activation, and it holds until the shared deadline, as the engine runs it. Before, no load preview drew offsets.
- **`CreateTestModal`:** its chart had its own copy of the simple ramp curve; it now draws `scenarioLoadPreview`.
- **Proof:** `testExecutionScenarioModel.test.ts` shows a queued run is drawn exactly as the editors draw its scenario, for every corpus scenario (start offsets, stages, baselines and goal TPS), and checks a delayed group directly.

## Retained seams

These are purposeful and documented here rather than removed.

- **Versioned browser-checkpoint readers** (`scenarioAuthoringDraft.ts`, `scenarioAuthoringDraftV2.ts`):
  - Version 1 checkpoints used a six-step builder.
  - Version 2 forms carried the flat mirrors; `scenarioAuthoringDraftV2.ts` composes them into the canonical values once, as the version 2 save did. It is the only code allowed to name them.
  - New checkpoints are version 3.
- **Versioned import metadata readers:** supported user-data read migrations, not response adapters.
- **Domain service adapters that preserve authorization or compare-and-swap:** purposeful boundaries.
- **`NewTestPage` group edit draft** (`GroupEditDraft`): a legitimate run-only editing projection. Its flat browser fields edit a run override, not a stored scenario.
- **The wizard's model chart** (`buildScenarioModelPreview`): series metadata, TPS stages and draft placeholders for the scenario editor. It shares stage semantics (`workloadStages`) but draws neither baselines nor start offsets; the composition timeline beside it draws offsets.

## Old addresses

| Address | Destination | Proof |
| --- | --- | --- |
| `#/scripts?mode=import` (before routes named a launcher) | The LoadRunner script archive importer, as before | `appRouting.test.ts`, `workflow-import-source.spec.ts` |
| `#/scripts?mode=import&launcher=<unknown>` | The create options, which list every importer | `appRouting.test.ts`, `workflow-import-source.spec.ts` |
| `#/scripts?mode=list` / `mode=create` / `mode=edit&scriptId=…` | Unchanged | `tier0-routes.spec.ts`, `appRouting.test.ts` |
| `#/recorder-panel` without a session, `#/repository-guide`, old archive-tab and terminal-run links | Unchanged translations (Script library, the API workflows article, the report section, the Results workspace) | `appRouting.test.ts` |
