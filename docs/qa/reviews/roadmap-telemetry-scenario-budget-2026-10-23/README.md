# Scenario telemetry budget choice — 2026-10-23

This [TEL-B slice](../../../ROADMAP_IMPLEMENTATION_SPEC.md#tel-b--policy-evolution-and-complete-consumers) closes what the [run budget choice slice](../roadmap-telemetry-budget-choice-2026-10-23/README.md) left open: a scenario-level budget choice, the dashboard's budget selects and `apexctl run`. It mirrors the [scenario acceptance policy slice](../roadmap-telemetry-scenario-policy-2026-10-22/README.md), whose rules it follows unless this record says otherwise.

Before this slice, a run could choose its telemetry budget policy only on `POST /api/v1/tests`, `POST /api/v1/ci/runs` and remote runs. A schedule or an execution-plan entry was always classified by the system threshold settings, and the dashboard's revise modal could not drop an inherited budget choice.

## Contract

### Scenario choice

- `telemetry_budget_policy_id` is a scenario field on `CreateScenarioRequest` (`POST /api/v1/scenarios`, `PUT /api/v1/scenarios/{id}`), `StoredScenario` and the authoring projection (`ScenarioAuthoringDocument`).
- It is stored on the scenarios table (`telemetry_budget_policy_id`, schema v83 `scenario-telemetry-budget-policy`, SQLite and PostgreSQL), NULL when the scenario's runs use the system threshold settings.
- The column is authored content: it is in `scenarioAuthoredColumns` (the revision triggers, which are replaced at open when their SQL changed, so upgraded databases get it) and in `scenarioRuntimeCommitFingerprint`. A change to it alone mints a new content revision.
- Empty and `project_default` both store no choice. The project's default budget policy never sets a scenario's runs' budgets, as before.

### At save

`storeScenarioLockedWithSources` calls `validateRunTelemetryBudgetPolicy` outside the runner lock under the same condition as the acceptance choice: when the save changes the budget choice, or keeps it while the scenario's project (derived from its scripts) differs from the one the runner has. The two choices are judged independently, so keeping one never re-judges the other.

| Choice | Answer |
| --- | --- |
| A policy the project lacks, another project's, or another kind's (an acceptance policy's ID) | `400` |
| An over-long ID | `400` |
| An invalid policy, or one whose `sustained_window_seconds` is not live classification's 15 s (`errRunPolicyConfiguration`) | `409` |
| A failed read (`errRunPolicyStoreUnavailable`) | `503` |

`runPolicyErrorStatus` already maps both sentinels for create and update, so no handler changed.

### Inheritance

`createTestRequestFromScenarioRevision` copies the scenario's budget choice into a request that names none (`telemetryBudgetPolicyFromScenario`), independently of the acceptance choice: a request naming one choice still inherits the other. Every entry point that runs a scenario goes through it: a direct run with `scenario_id`, a schedule occurrence and an execution-plan entry.

- A request naming another budget policy, or `project_default`, keeps its own choice. A revised copy still inherits from its source run.
- **A choice that is no longer usable** (deleted, or revised to another sustained window) refuses the run with a message naming the scenario's choice and the way out. A schedule occurrence refused this way is recorded as `failed`.
- **Policy-read outages.** A choice inherited from the scenario survives a failed read at creation, as the acceptance choice does; Start pins it or refuses. A run naming its own budget policy still gets `503`.
- **Estimates and capacity previews** skip the check through the existing `skipTelemetryPolicyCheck`, which already covered the budget choice.
- **At Start** the inherited choice is pinned (`run_selection`) and its `apexload.telemetry_policy.v2` receipt sets the run's budgets, exactly as for a run-level choice.

### Not portable

- `normalizePortableScenarioRequestDefaults` clears the budget choice, so an export omits it and the library content hash ignores it.
- `importLibraryScenario` replaces any choice a bundle names: a new import uses the system threshold settings, and an import that updates a linked scenario keeps that scenario's own choice.
- `createScenarioRequestFromStored` (the rollback journal) keeps it.
- **Remote runs.** A remote run of a scenario with a budget choice must name a remote budget policy or `project_default`; otherwise it is refused (`409`, `errRemoteRunScenarioPolicy`, whose message now covers both choices).

### Dashboard

- `TelemetryPolicySelect` takes `kind="budget"` and lists `analysisPoliciesApi.list(projectId, 'telemetry_policy')`. Its empty option is "System threshold settings"; a project's default budget policy is never named as a default. A policy whose definition sets another `sustained_window_seconds` is shown as unusable and disabled unless it is already the choice. A choice the list no longer offers, a failed load and no project behave as for the acceptance select.
- **Scenario builder** (Advanced settings): a budget select beside the acceptance one, carried by the form state (`telemetry_budget_policy_id`) and the draft (`ScenarioDraft.telemetryBudgetPolicyId`) into the save and run payloads, never into the estimate request. A checkpoint whose budget choice is not a string is not restored. At save both choices are checked against fresh reads under the same save lock, abort and 10-second timeout; a block opens the panel and reloads only the select that blocked. A choice the scenario already stores is not judged.
- **Revise modal:** "Keep the source run's choice (name)" omits the field, "System threshold settings" sends `project_default`, and another policy sends its ID, with the same fresh-read check, busy state, stale-snapshot guard and unmount behaviour as the acceptance select.

### `apexctl run`

`apexctl run --api URL --telemetry-budget-policy ID` sends `telemetry_budget_policy_id` and requires the create response to echo it, as `--telemetry-policy` does:
- an unconfirmed choice (no echo, another policy, or only an acceptance echo) deletes the queued run before Start and exits `2`; a delete that cannot be confirmed names the run and exits `3`;
- a controller refusal of the budget choice (`400`/`409` in the controller's wording: missing, over-long, invalid or another sustained window) exits `2`; other refusals keep exit `3`;
- `project_default` is the same as omitting the flag, and the flag needs `--api`.

## Validation

- **`TestScenarioTelemetryBudgetPolicyIsAuthoredContent`** (storage; also the `telemetry_budget_policy` case of `TestPostgresScenarioContentRevisionIntegration`): stored beside the acceptance choice and read by `GetScenario`, `ListScenarios` and the authoring snapshot; a change to it alone, including a direct column write, mints a new revision; clearing it keeps the acceptance choice; `SaveScenario` stores it.
- **`TestSchemaReleaseFingerprintIsCurrent`** records schema v83.
- **`TestScenarioTelemetryBudgetPolicyCarriesThroughEveryEntryPoint`**: `400` for missing, foreign and acceptance-kind IDs (missing and foreign with the same message) and an over-long ID; `409` for another sustained window; `project_default` stores none and spaces are trimmed; the choice in the response, the store, the authoring projection and a reloaded runner; inherited by a direct run, a plan entry and a schedule occurrence, while a run naming another policy or `project_default` keeps its own, and the acceptance choice is inherited regardless.
- **`TestScenarioTelemetryBudgetPolicyGoneRefusesInheritingRuns`**: a deleted choice refuses the inheriting run (`400`) and one revised to another window refuses it (`409`), each naming the scenario's choice; `project_default` is accepted; an unrelated edit keeps the deleted choice; a missing policy is refused at save and clearing is accepted.
- **`TestScenarioTelemetryBudgetPolicySaveStatuses`**: `409` on update; `503` on create and update during an outage; an edit and a restore keeping the choice succeed during it.
- **`TestScenarioTelemetryBudgetPolicyKeptChoiceIsJudgedAfterAProjectChange`**.
- **`TestScenarioTelemetryBudgetPolicyScheduleOutcomes`**: during an outage a governed schedule occurrence, a plan entry and a direct run are created with the choice (the occurrence's Start fails as any run's would) and a run naming its own policy gets `503`; once deleted, the occurrence is `failed`, naming the choice.
- **`TestScenarioTelemetryBudgetPolicyCreatedDuringOutageIsPinnedAtStart`**: the version 2 receipt names the scenario's policy and its limits, and the budget pin is `run_selection`.
- **`TestTelemetryBudgetPolicyIsNotJudgedByCapacityEstimates`** and **`TestScenarioTelemetryBudgetPolicyIsNotPortable`**.
- **`TestRemoteScenarioRunNeedsAnExplicitTelemetryBudgetPolicy`**: `409` through the route unless a remote policy or `project_default` is named.
- **`run_telemetry_budget_policy_test.go`** (`cmd/apexctl`): both choices sent and confirmed; an unconfirmed budget choice (no echo, another policy, an acceptance echo only) deletes the run and exits `2`; another policy is named; an undeleted run exits `3`; budget refusals in the controller's four wordings exit `2`, an acceptance refusal with only a budget choice and an unrelated refusal exit `3`; no choice sends none; flag parsing.
- **Dashboard:** `TelemetryPolicySelect.test.tsx` (budget options and wording, the unusable window, the issue helpers), `TestHub.test.tsx` (save, a policy revised since the select loaded to another window blocked by the fresh read and the budget select reloaded, estimates omitting the field), `CreateTestModal.test.tsx` (keep omits the field, `project_default` and another policy are sent; a policy usable when the select loaded but revised to another window is refused by the create's fresh read, and a kept choice the project no longer has and an unusable one are refused), `scenarioAssembly.test.ts` and `scenarioAuthoringDraft.test.ts`.
- A negative control (removing the inheritance line) failed four of the API tests.

## Review

Round 1 used two reviewers, one on the backend and CLI and one on the dashboard and docs.

- **Backend and CLI:** no findings. The reviewer checked every scenario path that handles the acceptance choice for a budget counterpart (storage on both dialects, triggers on upgraded databases, projections, fingerprint, library, remote, inheritance, outage handling) and apexctl's refusal check against the controller's wording.
- **Dashboard and docs:** two low test gaps, no code defects.
  - The revise test's name claimed "another policy" without sending one. It now chooses another policy and checks its ID is sent.
  - The revise refusal test's mock answered every read alike, so it did not prove the create judged a fresh read; and the builder test's name claimed a deleted policy it did not cover. The revise test now offers a policy that is usable when the select loads and revised to another window by the time of the create, and the builder test's name says what it covers.
