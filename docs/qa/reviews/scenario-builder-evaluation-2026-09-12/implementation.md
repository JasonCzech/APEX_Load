# Scenario Builder implementation and follow-up review

This implements the evaluation in [README.md](README.md). Existing changes in the working tree were retained.

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

## Additional findings fixed during implementation review

- The old mobile picker CSS hid columns that now contain Protocol and Actions; removed that obsolete rule.
- CSV derivation could remain applicable after ART, guardrail or parent-scenario changes. Derivations are now tied to the submitted input revision, including asynchronous responses.
- Saving an unrelated scenario could erase a different browser checkpoint. Only the checkpoint resolved by that save is cleared, including protection for a newer checkpoint from another tab.
- First-binding alias normalization could change a case-sensitive script reference. Explicit aliases now keep their case.
- Data import errors now explain that rows should be checked before retrying after an ambiguous network failure.
- Clone-row keyboard activation no longer intercepts keyboard events from nested controls.
- Configure-run remains disabled when required scripts/name or goal sizing are missing; empty target reviews explicitly explain what is missing.
- Read-only users can preview pool rows while CSV import stays permission-gated.

## Product boundaries

Browser checkpoints are local to this browser and project; they are not shared, runnable scenarios. Browser storage clearing removes them. Scenario and execution validation on the controller remains unchanged. Pool row previews are bounded samples, not proof of every row's validity. Binding scope is scenario-wide, matching the existing runtime contract. CSV elapsed values use units (for example `60s`), as described beside the upload.

## Verification

- Type checking, linting and the embedded dashboard build passed.
- Unit suite: 4,191 tests across 388 files passed.
- Targeted real-controller browser tests passed for incomplete draft recovery, exact CSV schedule round-trip, and CSV pool import with multiple editable bindings.
- Full `npm run test:cycle` completed successfully (exit 0): 108 dev-admin browser checks and 25 local-auth browser checks passed. The profiles used disposable controllers and databases on ports 18086 and 18087. See [verification log](verification.log).
- Hands-on browser review used the existing local audit fixture, including clone navigation, blocked-draft save/recovery, compact picker actions, 600-pixel layout, manual goal entry, and full review/validation. No load test was launched from the user's existing project. The synthetic browser checkpoint was cleared afterward.

Screenshots: [clone selector](implemented-clone.png), [manual goal entry](implemented-goal-entry.png), [validated review](implemented-review.png), [target and acceptance detail](implemented-review-details.png).
