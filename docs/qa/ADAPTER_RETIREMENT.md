# Adapter retirement inventory (AUTH-3)

Status: Wave 5 (A8), 2026-10-09. This is the AUTH-3 seam inventory of the [feature completion specification](../FEATURE_COMPLETION_SPEC.md#auth-3-adapter-retirement-inventory). For each seam it records the decision, the canonical owner that now resolves its values, the callers that were moved, and the tests that prove parity.

Retired seams are kept retired by `dashboard/src/adapterRetirement.test.ts`. That ratchet fails when application code names a retired adapter again, when the scenario form declares any of the 34 version 2 mirror fields as a member again (some, such as the error flags, are legitimate names elsewhere), when a division by a ramp duration (a variable, a member or a parsed field named for the ramp) appears outside the projections that own one, when a caller of a retired copy stops importing its owner, or when `ScriptEditor` keeps its own baseline, initial-save flag, navigation guard, dirty comparison or save rule again. Tests are not scanned, so they can still name an old value to prove it is refused or translated.

## Retired seams

### `aliasImportState` / route selection

- **Decision:** retired. Importers are opened by a launcher descriptor (launcher and optional mode), and the Scripts sub-view that renders one is derived from it (`dashboard/src/scriptImportLaunchers.ts`).
- **Callers:**
  - **Route mode:** the Scripts route accepted `list`, `create`, `edit` and `import` (later also the builder steps `record` and `draft`), so no caller passed an alias.
  - **`setSubview`:** no caller passed an alias either.
  - **Dead branches removed:** ten alias sub-views (`har-import`, `openapi-import` and so on) had renderer branches in `ScriptsAuthoringSurface` and title entries in `appTopbar` that were unreachable; both were removed.
- **Route boundary:**
  - An import route names its importer: `#/scripts?mode=import&launcher=<id>[&importMode=<mode>]`.
  - The page writes the launcher into the address, so a reload or a shared link reopens the same importer. Before, it reopened the LoadRunner archive importer.
  - The library and the Script builder's steps are routes of one mounted page: `#/scripts?mode=create[&workflow=existing|start-new|source][&category=templates|editors|blocks]`, `mode=record[&method=browser|proxy]` and `mode=draft` (an unsaved draft; a reload or Forward to one opens the builder). A deeper step is a history entry, closing a step goes back to the entry it came from, and a tab replaces the current entry (`workflow-script-builder-routes.spec.ts`).
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
- **Fields removed:** 34 mirror fields, which the save used to compose back into the canonical values:
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
  - **Parity corpus:** `scenarioAssemblyParity.test.ts` projects a corpus of stored scenarios (`dashboard/src/test/scenarioAssemblyParityFixtures.ts`) into the save request, capacity estimate, composition timeline and load preview. Its golden file was first recorded from the retired reconstruction. Retirement changed two things in the save and estimate, both corrections:
    - a think-time section saved without `ignore` is no longer given `ignore: false`;
    - an unchanged save no longer re-normalizes a stored section.

    The load preview was then re-recorded with the shared run projection, which draws group start offsets and takes the next stage's level at a stage boundary (see the next two sections). Nothing else changed.
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

### Script editor session state (absorbed from A5)

- **Decision:** retired. `useScriptAuthoringSession` owns the script editor's session state. Before, `ScriptEditor` kept it beside the session, one calculation each.
- **What moved:**
  - **The paired baseline:** loading a stored script's authoring baseline, blocking a draft that changed while it loaded, adopting a reviewed baseline, and blocking for reconciliation after a conflicting save.
  - **The dirty calculation and the initial-save flag:** one calculation, used by the guard, the Save button and the status line.
  - **Guarded navigation:** Close and the return target's button both leave through the editor's unsaved-changes guard.
- **The save rule:** the Save button follows `scriptSaveAvailability` in `scriptAuthoringSession.ts`, beside the session. The save itself still refuses a stored script without a ready baseline.
- **Callers:**
  - `ScriptEditor` reads all of this from the session.
  - `ScriptsAuthoringSurface` passes the return target to the editor instead of drawing its own return button.
- **Behavior changes when going back to a converted archive review:**
  - Leaving an edited converted draft asks first. Before, the page's button discarded the edits without a prompt.
  - The review stays open behind the editor. Its own unsaved-work guard is kept, so going back never prompts for it.
  - Saving a converted draft goes back to the review, which records it as saved and moves on to the next draft. Before, the saved script opened and the review closed, dropping its other unsaved drafts without a prompt.
  - A validation review becomes a conversion review once a draft is saved, as with the review's own saves, so its own save cannot save that draft again. Its remaining drafts are converted again first; the options cannot be changed until that is done.
  - The editor cannot be opened from the review while a validate, convert or save batch runs, while a Java dependency uploads, or while an earlier save of the archive is unconfirmed. Its buttons stay, disabled, and say why.
  - A request to set up the script's secrets is kept. The secrets dialog opens in the script library, as for any new script. When several saved scripts asked for it, their dialogs open one after another.
  - If the draft was edited while the save was in flight, the saved script opens instead. The review still records the save, but it closes, and its other unsaved drafts are dropped without a prompt.
- **Leave prompts:** Save changes goes ahead without saving when the work was saved, or its editor closed, while the prompt was open. This holds for every guarded editor.
  - Only the script editor also waits for a save that is still running, instead of refusing it. That includes queueing a Java Vuser draft for approval.
  - If edits were made after that save started, the prompt stays open and says they are not saved yet. They stay in the editor.
- **A second create:** after a create, Save is off until the page opens the saved script, so a quick second save cannot make a second script. Save changes in a leave prompt, or Save & configure secrets, is refused with the reason.
  - Edits made while the save ran open with the saved script, as a draft to review.
  - Edits made after the save finished, before the script opens, are not kept.
- **Proof:**
  - `useScriptAuthoringSession.ownership.test.tsx` covers the baseline load, the block, adoption, reconciliation, the dirty calculation, guarded leaving with the source's guards kept, a session without a baseline, and the save rule.
  - `EnterpriseArchiveImport.test.tsx` checks:
    - that the review records a draft the editor saved, once, moves on to the next draft, and does not save it again with the rest;
    - that a validated archive with another draft is converted again, and that a later conversion of it after a project change is not shown as that;
    - that while it is converted again, another archive's editor and its "Allow … And Refresh Review" buttons wait, and say why, and that those buttons ask to finish an open settings edit first;
    - that the editor buttons are disabled, with the reason, while a validate batch or a save runs, and while an unconfirmed save of the archive waits for Convert again.
  - `NavigationGuardProvider.test.tsx` checks that a prompt goes ahead without saving once the work was saved or closed, and still saves when the guard reads dirty. It also checks that an unsaved-work guard reads its live state and can be marked clean.
  - `ScriptEditor.test.tsx` covers:
    - the guarded return with the review's guard open beside it, including a save on the way back;
    - Save changes on the way back while a save or a Java Vuser queue is still running, which shares it, and keeps the prompt open when edits were made after it started;
    - a second create refused while the saved script is still opening;
    - the guarded close with a save.
  - `ScriptsPage.test.tsx` checks:
    - the import return, and that the return target names the review's guard;
    - that a converted draft saved from the editor goes back to the review, which records it. That holds for Save, for a save on the way back and for a save that also asks to set up secrets, whose dialog then opens in the library. An embedding page still hears of the save;
    - that the secrets dialogs of two drafts saved that way open one after another;
    - that edits made while saving open the saved script instead, and the review still records the save;
    - for a draft opened without a review to record it, that a script saved on the way back opens even when its save returned no authoring projection, with the editor and the review left in place until it does;
    - for the same kind of draft, that Back, clicked after a plain Save while the saved script is still loading, returns to the review, and the late load does not reopen it.
  - The ratchet fails if `ScriptEditor` keeps its own baseline, initial-save flag, guard, dirty comparison or save rule again.
- **Kept with the page:**
  - `useScriptsAuthoringState` holds the routing state: the open sub-view and the return target it reopens.
  - An archive import's pending sibling drafts live in the importer (`EnterpriseArchiveImport`), which stays mounted behind the editor.
  - The session decides only whether the editor may leave.
- **Completed lifecycle edges:**
  - The [scenario prerequisite handoff slice](reviews/roadmap-scenario-prerequisite-2026-10-19/README.md) retains embedded authoring after an archive-source return, a later-edit save or a secret-setup request. The saved script is assigned to the selected group; explicit return still guards remaining drafts. The source review also stays mounted when a saved editor retains later changes.
  - The [archive approval handoff slice](reviews/roadmap-archive-approval-handoff-2026-10-19/README.md) closes the legacy/plain direct-editor Java queue edge: an accepted receipt marks the lane queued, carries its session identity, and returns an unchanged editor to review. Later edits stay in the editor. Project, folder, options and review-generation fences reject stale callbacks. The review retains source sessions used by pending sibling lanes. Protected imports keep save-before-edit; the separate scenario prerequisite slice above handles its embedding, and the [AUTH-2 property matrix](LIFECYCLE_PARITY_MATRIX.md#auth-2-properties) has zero open cells.

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
- **The Command Center live load model** (`commandCenterLoadModel.ts`): draws a running group's expected ramp beside its live counts. It is outside the authoring seams and keeps its own simple ramp.

## Old addresses

| Address | Destination | Proof |
| --- | --- | --- |
| `#/scripts?mode=import` (before routes named a launcher) | The LoadRunner script archive importer, as before | `appRouting.test.ts`, `workflow-import-source.spec.ts` |
| `#/scripts?mode=import&launcher=<unknown>` | The create options, which list every importer | `appRouting.test.ts`, `workflow-import-source.spec.ts` |
| `#/scripts?mode=list` / `mode=create` / `mode=edit&scriptId=…` | Unchanged | `tier0-routes.spec.ts`, `appRouting.test.ts` |
| The address written while the recorder, the create options or an unsaved draft is open | The create options (`mode=create`), never the archive importer | `appRouting.test.ts` |
| `#/recorder-panel` without a session, `#/repository-guide`, old archive-tab and terminal-run links | Unchanged translations (Script library, the API workflows article, the report section, the Results workspace) | `appRouting.test.ts` |
