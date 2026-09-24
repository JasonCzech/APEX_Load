# Scenario Builder evaluation (2026-09-12)

Status: Complete. Findings implemented with a follow-up review (see Implementation).

Merged from the original per-topic files of this review. Each section below was one file; the screenshots and evidence next to this README are unchanged.

## Scenario Builder evaluation — September 12, 2026

The Scenario Builder has substantial working capability, but it is not yet a consistently dependable authoring experience. The highest-value improvements are reliable draft preservation, a direct cloning flow, and a readiness model that agrees across scripts, validation, saving, and run setup. Improving these would matter more than adding more workload shapes.

This is an evaluation, not an implementation change. Findings come from fresh browser interaction, inspected screenshots, a read of the current source, and a persisted-scenario API check. The frontend was the working-tree development server on port 4000, connected to the existing local controller on port 8080. The controller identified itself as `v0.1.0-dev.20260912+6d53cf5`. The working tree contained substantial pre-existing changes; none were modified. Current source observations are supporting evidence, not proof that the existing controller binary contains every current source change.

**Workflow coverage**

1. **Start and model selection — usable, with inconsistent routing.** All five entry choices were inspected. Fixed-user guidance is useful. Cloning routes through an unnecessary empty Scripts step; Goal starts at Run Settings before script composition.
2. **Scripts and groups — functional, with readability and readiness problems.** Searched a 41-script library, selected a blocked import, inspected its review link and initialization/role controls, and later selected a synthetic executable script. Selection returns focus to its originating button. Green step completion does not mean the selected script is executable.
3. **Shape and source — capable, with onboarding friction.** Applied Closed VUser, inspected advanced shapes, switched sources, uploaded and analyzed a synthetic CSV, inspected production-source availability, and successfully loaded an existing scenario as a copy. Source switching correctly removes the previous preview and prevents saving until a replacement is applied.
4. **Run settings — strong core, crowded presentation.** Configured 17 VUsers, one-minute ramp, and two-minute sustain. Inspected runtime inheritance and generator placement. Reopening preserved the configured values. A 600×900 responsive check showed stacked navigation and controls, but the preview requires substantial scrolling.
5. **Data — basic attachment, incomplete authoring support.** Inspected the no-pool path, selection strategies, immutable run-snapshot explanation, and inline creation form. This workspace had no pools. Creation asks for one JSON object and sends users to Settings for additional rows.
6. **Review and validation — useful checks, inconsistent status.** Reproduced blocked-script validation and save failures. A clean fixture returned Ready for capacity, placement, demand, runtime, and data. Saving that same scenario cleared the successful validation status.
7. **Save, reopen, and configure run — succeeds for executable scenarios, with misleading timing language.** Saved `LOC-SCN-0038`, confirmed stored values through the API, reopened it, and followed Save and configure run. New Test retained the name, ID, 17 VUsers, and three-minute duration, but called all three minutes steady.
8. **Recovery and reuse — partial.** Keep editing preserved an unsaved draft after attempted navigation. Cloning copied the fixture and timeline after manually jumping to the source step. Durable recovery of incomplete builder drafts is intentionally absent in the current source.

**Prioritized findings**

| Priority | Finding and evidence | Recommended change | Acceptance condition |
|---|---|---|---|
| P1 | **Save draft rejects a script that the builder permits users to compose.** Select `ActivateSubscriber_v302`, apply Closed VUser, go to Review, and choose Save draft. The UI says a reusable draft can be saved, but the server returns `bruno_missing_variables, import_review_required ... blocks launch`. The draft remains unsaved. Screens 03, 08, 09. | Introduce a draft persistence contract that accepts incomplete or blocked execution configuration while preserving all execution gates. Until available, make the save limitation explicit before users invest in setup. | A blocked script can be retained in a clearly marked, non-runnable scenario draft; launching remains blocked. |
| P1 | **Cloning starts at an empty, gated Scripts step.** Choosing Model from Existing Scenario shows Group 1 with no script and disables Next. Clicking Shape / Source directly reveals the source selector; Model then fills scripts and settings correctly. Screens 20–21. | Route this start choice directly to source selection, then ask users to review the imported composition. | A first-time user can clone without adding a throwaway script or discovering step-navigation shortcuts. |
| P1 | **Readiness is inconsistent across stages and save.** A blocked script receives a green Scripts valid check; defaults can receive checks without review. A clean Ready result returns to Not validated after Save draft even when values are unchanged. Screens 03, 08, 15. The explanatory sentence helps, but places the burden on the user. | Separate Configured, Needs attention, Not checked, and Ready. Preserve validation against a stable draft/revision fingerprint; explain when a dependency change invalidates it. | Blocking script issues appear on the Scripts step. An unchanged save does not silently erase a successful validation result. |
| P2 | **No durable recovery for incomplete work.** Save is disabled before a source is applied, and current builder code explicitly keeps progress only in React state. Navigation protection works, but is not recovery after session loss. Screens 01, 10, 13; source: `scenarioBuilderDraft.ts`. | Make a deliberate product decision to support resumable authoring drafts, with workspace identity, timestamp, discard, and sensitive-field handling. | An incomplete composition can be explicitly saved and resumed after reopening, without relaxing execution validation. |
| P2 | **Script selection and composition waste space while truncating useful content.** The initial picker’s Actions column is partly outside the visible table. A single group occupies a narrow column with large unused space; warnings wrap into a tall block and long names truncate. Clone selection has a similar wide-table problem. Screens 02–03, 20–21. | Give single/few groups wider cards or rows. Keep selection actions visible. Prioritize script name, protocol, readiness, and last validation in the picker; move secondary metadata into details. Add readiness/protocol filters and multi-select as follow-on capabilities. | Full group names and primary actions remain accessible at common desktop widths; one group does not inherit a four-column minimum-width problem. |
| P2 | **Data setup stops before users can verify their parameters.** The builder exposes one pool, alias, and strategy. It does not expose a row preview, required-column mapping, or per-group bindings here. Inline creation requires a JSON object and supports only the first row. Screens 07, 19. | Add a row/column preview, required-field checks against selected scripts, examples of the resulting parameter reference, and a grid or CSV import. Add multiple/per-group pool bindings when supported by the product contract. | A user can supply and inspect realistic data, understand how scripts consume it, and see uniqueness/exhaustion issues before leaving the builder. |
| P2 | **CSV format guidance arrives after failure.** A small `time,users` CSV with elapsed values 0, 60, 120 passes column inspection but analysis rejects `60`; only the error explains H:MM, H:MM:SS, or Go-duration syntax. There is no sample download in the inspected entry state. Screens 10–11. | Show accepted time formats and units before upload, offer a template generated from current group names, auto-suggest mappings, and put parsing errors beside the time mapping with a row preview. | The supported equivalent `0s, 60s, 120s` is discoverable before a failed submission; numeric elapsed seconds can be selected explicitly if implemented. |
| P2 | **Review does not summarize the complete experiment.** Its summary emphasizes shape, groups, duration, mode, pool name, and shakeout. Effective runtime overrides, group placement, target endpoints, assertions, and acceptance criteria are not consolidated there. New Test reveals no assertions and no response-time SLA only after the handoff. Screens 06, 08, 15–16; current DOM inspected. | Add a concise final experiment summary with expandable runtime/placement details and links to the exact setting. Reuse the existing New Test readiness content earlier. | Users can review targets, acceptance, data, runtime changes, and placement from one final checkpoint. |
| P2 | **Run handoff mislabels ramp time as steady time.** The saved definition and reopened builder show one-minute ramp plus two-minute sustain. New Test labels the three-minute duration `3m steady`. Screen 17 and the stored API result. | Derive phase labels from the same timeline used by the builder; distinguish load-window duration from sustained time. | Builder, run setup, and execution summaries agree on 1m ramp / 2m sustain / 3m total. This observation does not establish an engine execution defect. |
| P2 | **Capacity signals need clearer provenance at handoff.** Builder validation reports one generator available. New Test simultaneously reports zero live inventory hosts and one generator available in its estimate. Screen 17. This may be valid local execution behavior, but is unexplained. | Distinguish local execution capacity, connected generator inventory, and calculated estimates, with source and freshness labels. | Users understand why estimated capacity and live-host counts differ. |
| P3 | **Goal defaults and terminology add avoidable work.** Goal setup defaults to completed-baseline import even with zero completed runs; manual ART is available. The start description implies a baseline is required. Shape actions visually say Use Manual Shape while their accessible names say Use Closed VUser, etc. Screens 04, 14. | Default to manual response-time entry when no baseline exists, explain the baseline alternative, and align visible and accessible action labels. | Empty-baseline users have an immediately usable path; voice-control and screen-reader names match visible controls. |
| P3 | **Dense secondary forms lack the polish of the main worksheet.** CSV mapping and inline data creation use compact native controls; data labels run together visually. Muted helper text, small action labels, and stacked tab groups deserve accessibility review. Screens 06, 11, 18–19. | Apply the established field layout, spacing, typography, and error treatment to these secondary forms. | Labels and instructions remain legible at 200% zoom, field grouping is clear, and keyboard focus is never obscured by sticky chrome. |

**What should be retained**

The fixed-user worksheet communicates peak, ramp, sustain, and optional ramp-down clearly. Its live graph and phase table agree with the persisted scenario. Runtime defaults distinguish effective values from their source and offer group-specific overrides. Script blockers are disclosed with review actions. Source replacement invalidates stale previews. Validation returns actionable dimensions rather than only a pass/fail badge. Save keeps the builder open and confirms the saved reference, while Save and configure run passes the correct asset to New Test. Existing-scenario copying preserves the original and appends Copy to the new draft name.

**Suggested delivery order**

First address the three P1 findings together: define what a draft is, route cloning correctly, and make readiness truthful. Next improve the authoring loop: wider composition, sample CSVs, usable data input, and a complete review summary. Finally unify phase/capacity terminology across builder and run setup, and polish the less frequently visited forms.

Useful regression coverage would include blocked-script draft persistence without execution permission, cloning from a blank builder, validation retention after an unchanged save, incomplete-draft recovery, a complete CSV map/apply/save round trip, data-column mismatch and unique-row exhaustion, and builder-to-run phase-summary agreement. Keep the existing full workflow test cycle for any implementation changes.

**Accessibility observations and limits**

The inspected DOM provides labeled fields, step buttons, tab roles, named dialogs, status text, and alert semantics. Script selection visibly restores focus and Keep editing retains the draft. These are meaningful strengths. Visible/accessible action-name mismatch, small controls, crowded labels, long tables, and sticky regions are risks to verify with keyboard-only use, zoom, and a screen reader. Contrast ratios were not measured and no WCAG conformance claim is made. The 600×900 check covers reflow, not a full mobile or assistive-technology audit.

No load test was launched, scheduled, or held. Production Prometheus/OpenSearch sources were unavailable in this controller, and there were no completed baselines or data pools in the selected workspace. Production replay, completed-baseline import, populated-data execution, every advanced shape, every protocol, RBAC profiles, backend failure recovery, and distributed runtime behavior were not exhaustively exercised. The CSV branch was tested through parsing failure; successful application is an outstanding verification case. The successful end-to-end authoring path used Closed VUser and no data pool.

The audit created one clearly named local script (`31c09ae2-c565-4610-80ad-38b55133680f`, `UX_Audit_Scenario_Fixture_20260912`) and one saved local scenario (`8d7cd06f-4756-4d5f-a804-608c69166b79`, `LOC-SCN-0038`, `UX Audit — Saved Scenario 2026-09-12`) for reproducibility. The script targets the local controller; it was not executed. The blocked scenario save failed and the copied scenario was not saved. No existing scenario/script was edited.

**Implementation entry points**

- `dashboard/src/components/TestHub.tsx:3084`: persistence validation and save paths; `:3182` save/refresh; `:3013` validation invalidation depends on the scripts array; `:3362` readiness and step completion; `:4880` data workflow; `:4947` review; `:5196` final actions.
- `dashboard/src/components/scenarioBuilderDraft.ts:4`: intentional non-persistence policy for builder progress.
- `dashboard/src/components/TestHub.css:1649`: fixed four-column group layout.
- `pkg/api/handlers_scenarios.go:19` and `pkg/api/scenario.go:242`: scenario persistence entry points.
- `dashboard/src/components/newTestRunModel.ts:763` and `NewTestPage.tsx:3443`: load-window value named/labeled steady.
- `dashboard/e2e/tier0-scenario-builder.spec.ts`: existing six-step, source-switching, failure-recovery, and responsive regression coverage. Reviewed, not rerun for this report-only change.

Screenshots and step notes are presented in `walkthrough.html`; all images were captured during this evaluation and inspected from their saved files. A mismatched intermediate capture was replaced before inclusion. Screenshots show actual scroll positions, including table overflow and sticky UI; they are not reconstructed mockups.

## Scenario Builder implementation and follow-up review

This implements the evaluation in [evaluation](#scenario-builder-evaluation--september-12-2026). Existing changes in the working tree were retained.

| Evaluation area | Implemented behavior |
| --- | --- |
| Blocked and incomplete drafts | Explicit, project-scoped browser checkpoints preserve unfinished configuration without calling the runnable-scenario API. A timestamped recovery card offers Resume and Clear. Completed configurations retain the project scenario save workflow. |
| Recovery and isolation | Reload does not automatically apply a draft. Resume restores the step, source, groups and settings. A changed or unavailable original resumes as a new scenario to avoid overwriting it. Inline environment values and credential-bearing runtime configuration are refused with an explanation rather than silently removed. Storage errors remain visible. |
| Clone entry | Model from Existing Scenario opens the source selector directly. The picker keeps the model action visible and moves source/release/history metadata into details. |
| Readiness | Known authoring blockers give Scripts an attention indicator. Validation is invalidated by semantic input/script changes, and survives equivalent save/refresh responses. |
| Script composition | Responsive cards, a compact picker, readiness/protocol filters, bulk addition, and usage/history details. Actions remain available at narrow widths. |
| Data pools | Every binding is editable/removable. Alias validation, script data references, bounded row previews, empty-pool guidance, and accurate selection-strategy explanations. Empty pool creation followed by CSV preview/import supports bulk setup within the draft. |
| CSV schedule | Format guidance and a downloadable sample using current group names, suggested time/target columns and group mappings, uploaded-row previews, consistent form styling, and guards against applying stale derivations. |
| Review | Safe target origins, script assertion inventory, response-time SLAs, runtime summaries, placement, stage durations, all data bindings, and direct editing actions. Unknown script details are explicitly unverified. |
| Run handoff | The total workload interval is labelled “load window (includes ramp-up)”. Capacity estimates identify their source and update time separately from live host inventory. |
| Goal entry | Manual average response time is the default; completed baselines are optional. Workload-shape button text agrees with its accessible name. |

### Additional findings fixed during implementation review

- The old mobile picker CSS hid columns that now contain Protocol and Actions; removed that obsolete rule.
- CSV derivation could remain applicable after ART, guardrail or parent-scenario changes. Derivations are now tied to the submitted input revision, including asynchronous responses.
- Saving an unrelated scenario could erase a different browser checkpoint. Only the checkpoint resolved by that save is cleared, including protection for a newer checkpoint from another tab.
- First-binding alias normalization could change a case-sensitive script reference. Explicit aliases now keep their case.
- Data import errors now explain that rows should be checked before retrying after an ambiguous network failure.
- Clone-row keyboard activation no longer intercepts keyboard events from nested controls.
- Configure-run remains disabled when required scripts/name or goal sizing are missing; empty target reviews explicitly explain what is missing.
- Read-only users can preview pool rows while CSV import stays permission-gated.

### Product boundaries

Browser checkpoints are local to this browser and project; they are not shared, runnable scenarios. Browser storage clearing removes them. Scenario and execution validation on the controller remains unchanged. Pool row previews are bounded samples, not proof of every row's validity. Binding scope is scenario-wide, matching the existing runtime contract. CSV elapsed values use units (for example `60s`), as described beside the upload.

### Verification

- Type checking, linting and the embedded dashboard build passed.
- Unit suite: 4,191 tests across 388 files passed.
- Targeted real-controller browser tests passed for incomplete draft recovery, exact CSV schedule round-trip, and CSV pool import with multiple editable bindings.
- Full `npm run test:cycle` completed successfully (exit 0): 108 dev-admin browser checks and 25 local-auth browser checks passed. The profiles used disposable controllers and databases on ports 18086 and 18087. See [verification log](verification.log).
- Hands-on browser review used the existing local audit fixture, including clone navigation, blocked-draft save/recovery, compact picker actions, 600-pixel layout, manual goal entry, and full review/validation. No load test was launched from the user's existing project. The synthetic browser checkpoint was cleared afterward.

Screenshots: [clone selector](implemented-clone.png), [manual goal entry](implemented-goal-entry.png), [validated review](implemented-review.png), [target and acceptance detail](implemented-review-details.png).
