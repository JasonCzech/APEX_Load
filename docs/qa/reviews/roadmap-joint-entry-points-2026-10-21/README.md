# Joint evidence through every entry point — 2026-10-21

This [NA-B slice](../../../ROADMAP_IMPLEMENTATION_SPEC.md#na-b--bounded-joint-evidence-across-execution-entry-points) carries the joint evidence v4 projection beyond direct test creation, which until now was the only way to set one. The contract is documented in the [native analytics guide](../../../NATIVE_ANALYTICS.md).

## What changed

- **Scenarios.** A scenario stores a `joint_evidence` projection (schema v73, `scenarios.joint_evidence_json`), returned by the scenario APIs and the authoring projection, carried by clones and library bundles, and part of the scenario's authored content: changing it alone mints a new content revision.
- **Every scenario entry point.** A run created from a scenario captures the scenario's projection unless its request names another. Direct runs, schedule occurrences and execution-plan entries all build their runs this way, so they share it.
- **CI runs.** A CI run has no scenario, so `POST /api/v1/ci/runs` accepts `joint_evidence`. It is validated on the request and recorded with the run's idempotency metadata: a retry under the same key that names another projection is refused (409).
- **Promoted categories only.** A `property:<category>` must be one a run's scripts promote (`results.property_analytics`, after the scenario's and the group's runtime overrides), or the engine's own `browser_iteration` (cold/warm). Scenario saves, runtime-settings changes applied to a scenario's groups and run creates refuse any other, naming it. A revised copy is checked like any run, except that it may keep a projection its source run was admitted with before this rule (one the source's own groups do not promote either), so such a queued run can still be revised. A script edited to drop a promotion leaves the scenario's later runs refused with the category named until the scenario or the script is updated; there is no per-run opt-out of an inherited projection.
- **Dashboard.** The scenario builder's run settings (Advanced settings) gain **Joint evidence**: a population (off, actions or business transactions), up to three dimensions and the property categories the scenario's groups promote, resolved as the controller resolves them (plus `browser_iteration` for browser groups). An empty projection or an unpromoted category blocks the save with a stated reason and opens the panel; a chosen category that lost its promotion is marked. Save and run payloads carry the projection, and checkpoints keep it. The revise-copy modal shows the source run's projection, which the copy inherits, and warns when its group edits stop promoting the kept category.

## Validation

| Gate | Result |
| --- | --- |
| `TestJointEvidenceCarriesThroughEveryScenarioEntryPoint`: one scenario fixture; a direct run, a run naming its own projection, an execution-plan entry and a materialized schedule occurrence, each checked for the run's projection and the projection frozen into its provenance | Passed |
| `TestJointEvidenceCarriesThroughCIRuns`: projection on a CI run, a conflicting retry under the same key, an invalid projection | Passed |
| `TestJointEvidenceAdmitsOnlyPromotedPropertyCategories`: scenario and run refusals, promotion by a runtime override | Passed |
| `TestScenarioJointEvidenceIsAuthoredContent` on SQLite and PostgreSQL (`-tags postgres_integration`) | Passed |
| Dashboard: `jointEvidenceModel.test.ts`, the scenario-assembly round trip and the TestHub save test | Passed |

## Review

Two reviewers (backend and dashboard) read scratch snapshots only:

- Round 4: dashboard confirmed its fix; two nits, fixed: the warning claimed a refusal the controller may not make (an unchanged group's snapshot can still promote the category), and this README did not mention the warning.
- Round 3: backend found none. Dashboard found one low, fixed: the revise modal judged promotions against the library's current scripts, while the controller compiles a copy's unchanged groups from the source run's script snapshots, so its notice could misreport after a library edit. It now warns only when the modal's own group edits stop a promotion.
- Round 2: both confirmed their fixes. Backend found one low, fixed: the revised-copy exemption ignored the copy's own groups, so a copy that dropped a promotion its source had could keep the projection. Dashboard found one low and one nit, fixed: the revise modal gave no warning when its groups stopped promoting the kept category, and `browser_iteration` was labelled as a raw property and could be marked "Not promoted".
- Round 1: backend three medium and one low, dashboard one medium and four lower, all fixed. Backend: the engine's own `browser_iteration` category was refused; copying runtime settings between a scenario's groups could drop a promotion its projection used; the guide's older "Limits" paragraph contradicted the new contract; a revised copy of a run admitted before the rule could no longer be created. Dashboard: the revise modal hid the inherited projection; a blocked save did not open the panel; the checkpoint parser accepted a malformed projection; the fieldset's label hid its legend and the error was announced twice; tests covered only the happy path.

## Remaining

Validating category, cardinality, encoded-byte and distribution budgets at ingestion beyond the existing preflight; persisting expected source identities and publication/window ownership so recovery reconstructs the exact population or exposes lost batches; and the remaining exit fixtures (delayed, missing and duplicate sources, changed generations, overflow, pause boundaries, restart and cold HA adoption, and archive-versus-native agreement on independently counted tuples).
