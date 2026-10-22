# Scenario telemetry acceptance policy — 2026-10-22

This [TEL-B slice](../../../ROADMAP_IMPLEMENTATION_SPEC.md#tel-b--policy-evolution-and-complete-consumers) closes three items that the [policy selection slice](../roadmap-telemetry-policy-selection-2026-10-22/README.md) left open: dashboard selection, plain `apexctl run`, and scheduled and execution-plan runs.

Before this slice, a run could choose its telemetry acceptance policy only on `POST /api/v1/tests`, `POST /api/v1/ci/runs` and remote runs. A schedule or an execution-plan entry had no request of its own to name one, so it was always judged by its project's default. The dashboard and `apexctl run` offered no way to choose.

## Contract

### Scenario choice

- `telemetry_acceptance_policy_id` is a scenario field on:
  - `CreateScenarioRequest` (`POST /api/v1/scenarios`, `PUT /api/v1/scenarios/{id}`);
  - `StoredScenario`;
  - the authoring projection (`ScenarioAuthoringDocument`).
- It is stored on the scenarios table (`telemetry_acceptance_policy_id`, schema v75, SQLite and PostgreSQL), NULL when the scenario uses its project's default.
- The column is authored content. It is in `scenarioAuthoredColumns` (the revision trigger) and in `scenarioRuntimeCommitFingerprint`, so a change to it alone mints a new content revision.

### At save

`storeScenarioLockedWithSources` calls `validateRunTelemetryAcceptancePolicy` with the scenario's project, derived from its scripts, outside the runner lock, when the save changes the choice, or keeps it while the scenario's project (derived from its scripts) differs from the one the runner has for it. A save that keeps the scenario's current choice is not judged again: an unrelated edit, a library import update or a journal rollback never fails on a choice deleted since, or on a policy read. Runs that inherit it check it again. The rules match those at run creation:

| Choice | Answer |
| --- | --- |
| A policy the project lacks, or another project's | `400` |
| An invalid policy (`errRunPolicyConfiguration`) | `409` |
| A failed read (`errRunPolicyStoreUnavailable`) | `503` |

Empty and `project_default` both store no choice. `runPolicyErrorStatus` maps the statuses for create and update. Both routes already document `409` and `503`, so `go generate ./pkg/api` changed only the field.

### Inheritance

`createTestRequestFromScenarioRevision` copies the scenario's choice into a request that names none. Every entry point that runs a scenario goes through it: a direct run with `scenario_id`, a schedule occurrence and an execution-plan entry.

- A request naming another policy, or `project_default`, keeps its own choice.
- A revised copy still inherits from its source run, not from the scenario.
- An inherited choice is then validated like any run's choice. One deleted since the scenario was saved refuses the run with a message naming the scenario's choice and the way out (`telemetryPolicyFromScenario`). It is never silently judged by the default.
- **Schedules.** A schedule occurrence refused this way is recorded as `failed`, including for a governed schedule (any created through the API), whose sweep first builds a capacity preview.
- **Policy-read outages.** When the policy read fails (`errRunPolicyStoreUnavailable`) for a choice inherited from the scenario, the run is still created with the choice. That covers a schedule occurrence, a plan entry, and a direct run naming no policy. The choice was validated when the scenario was saved, and Start pins it or refuses.
  - This slice changes only creation. Start reads the project's policies of every kind for every run (`resolveRunPolicyPins`), so a run started while they cannot be read is refused at Start, with or without a choice, as before this slice. A schedule or plan entry that starts its run at once during the outage records that Start failure.
  - A run naming its own policy is still answered `503` at creation.
- **Remote runs.** A remote run of a scenario with a choice must name the remote policy or `project_default`. Otherwise it is refused (`409`, `errRemoteRunScenarioPolicy`), because the pushed bundle omits the choice. The remote would judge the run by its linked copy's own choice or its project's default, never by the local choice.
- **Estimates and capacity previews.** These builds skip the check (`skipTelemetryPolicyCheck`), so a lost or unreadable choice never fails one or hides a schedule preview's capacity findings:
  - an editor's capacity estimate (`EstimateDraftCapacity`) and load-model check;
  - a schedule's capacity previews: the one shown when a schedule is created or updated, and the conflict preview a governed schedule's sweep builds before each occurrence;
  - a plan entry's secret preflight.

  The dashboard's estimate request omits the field. Launch preflight and every real create still judge the choice.
- **At Start**, the run's choice is pinned exactly as in the earlier slice.
- **Locking.** Inherited choices are validated in `buildManagedTestLocked`, as run-level choices already were. That runs on a build snapshot of the runner, with the live runner lock released. A schedule sweep, plan entry or direct run of a scenario with a choice now reads the policy store there.

### Library bundles

A policy ID names one of the source project's policies, and a bundle does not carry policies. So the choice is not portable:
- `normalizePortableScenarioRequestDefaults` clears it. An export omits it, and the library content hash and the recovery hash ignore it, so an existing bundle's hash is unchanged.
- `importLibraryScenario` replaces any choice a bundle names. A new import uses the destination project's default. An import that updates a linked scenario keeps that scenario's own choice.
- The rollback journal restores a scenario from `createScenarioRequestFromStored`, which keeps the choice.

### Dashboard

- `TelemetryPolicySelect` lists the project's live telemetry acceptance policies with `analysisPoliciesApi.list(projectId, 'telemetry_acceptance_policy')`, which needs viewer access. It names the project's default (`apply_by_default`). A choice the list no longer offers stays visible as "no longer available", so a save never drops it silently. A failed load keeps the current choice and says so.
- **Accessibility.** The select is described by its explanation and, when there is one, by its inline error (`aria-describedby`, `aria-invalid`). The inline error is not announced separately: a blocked save or create announces it in the page error. Without a project nothing is read and no failure is claimed.
- **A stored choice the project lost** is shown as a warning (`settings-inline-warning`, described by the select, not `aria-invalid`) that runs created from the scenario are refused until another policy or the default is chosen. A save that keeps it succeeds, as the controller allows.
- **Scenario builder** (Run settings → Advanced settings).
  - The select edits `ScenarioFormState.telemetry_acceptance_policy_id`, carried by the draft (`ScenarioDraft.telemetryAcceptancePolicyId`) into the save and run payloads.
  - A draft checkpoint whose choice is not a string is not restored.
  - **Save check.** Under the save lock, a choice that differs from the loaded scenario's stored choice is judged against a fresh read of the project's policies (`telemetryPolicySaveIssue`).
    - One the project no longer has blocks the save before the save request, opens the Advanced settings panel and reloads its list, which marks the choice.
    - A choice the scenario already stores is not judged, as the controller does not judge it.
    - A read that fails or takes more than 10 seconds blocks nothing, because the controller decides. Closing the builder or leaving the scenario hub ends the read, and the save it guarded is dropped rather than sent unchecked.
  - Clearing the choice saves none, which returns the scenario to the default.
- **CreateTestModal**, which the dashboard opens only to revise a queued run.
  - The source run's choice is shown as "Keep the source run's choice (name)", marked "no longer available" when the project lacks it. Keeping it omits the field, so the server's inheritance applies.
  - "Project default" sends `project_default`, and another policy sends its ID.
  - **Create check.** A kept or chosen policy the project no longer has blocks the create, judged against a fresh read. The modal clears the previous error and shows it is busy from the start of that check. Its fields stay editable, so keyboard focus is never lost. If the settings change while the policy is checked, the copy is not created from the submitted snapshot: the modal asks for a review and another create. A block reloads the select's list. The modal cannot be closed while it is busy (the read lasts at most 10 seconds). If it unmounts meanwhile, the read ends and the copy is never created.

### `apexctl run`

`apexctl run --api URL --telemetry-policy ID`:
- sends `telemetry_acceptance_policy_id` on `POST /api/v1/tests` (`runcontract.CreateTestRequest`);
- requires the create response to echo it (`runcontract.TestStatus`). Otherwise the queued run is deleted before Start (`deleteQueuedTestContext`), and the command fails with `cliConfigurationError` (exit `2`), with a message for a missing echo and another for a different policy. Any delete answer but success is checked by a read. A `404` can be a run the controller will not delete now, and a `500` can follow a delete that removed the run but not all its files. If the run can still be read, it is still queued: the command names it and exits `3`. A command deadline that passes meanwhile keeps that outcome beside the timeout, so it is never hidden;
- treats a controller refusal of the choice itself (`400` or `409` naming the telemetry acceptance policy: missing, over-long or invalid, matched in the controller's own wording) as a configuration error (exit `2`), as `apexctl ci run` does. Other refusals keep their usual exit `3`;
- needs `--api`. `project_default` is the same as omitting the flag.

## Validation

- **`TestScenarioTelemetryPolicyIsAuthoredContent`** (storage; also the `telemetry_acceptance_policy` case of `TestPostgresScenarioContentRevisionIntegration`):
  - the choice stored and read by `GetScenario`, `ListScenarios` and the authoring snapshot;
  - a change to it alone, including a direct column write, mints a new content revision;
  - clearing it.
- **`TestSchemaReleaseFingerprintIsCurrent`** records schema v75.
- **`TestScenarioTelemetryAcceptancePolicyCarriesThroughEveryEntryPoint`**:
  - `400` for a missing policy and for another project's policy, with the same message;
  - `409` for an invalid policy;
  - `project_default` stores none, and a choice with spaces is trimmed;
  - the choice in the API response, the store, the authoring projection and a runner reloaded from the store;
  - inherited by a direct run, an execution-plan entry and a schedule occurrence, while a run naming another policy or `project_default` keeps its own.
- **`TestScenarioTelemetryAcceptancePolicyGoneRefusesInheritingRuns`**:
  - a deleted choice refuses the inheriting run, naming the scenario's choice;
  - a run naming `project_default` is accepted;
  - an unrelated edit keeps the deleted choice;
  - a save choosing a missing policy is refused, and a save clearing the choice is accepted.
- **`TestScenarioTelemetryAcceptancePolicySaveStatuses`**:
  - `409` for an invalid policy on update;
  - `503` on create and update while the policies cannot be read;
  - an edit keeping the choice, and a restore through `UpdateScenarioIfUnchanged`, succeed during that outage.
- **`TestScenarioTelemetryAcceptancePolicyKeptChoiceIsJudgedAfterAProjectChange`**: a kept choice whose policy was deleted is not judged in the same project, and is refused once the scenario's project differs from the runner's.
- **`TestScenarioTelemetryAcceptancePolicyScheduleOutcomes`**:
  - during a policy-read outage, a schedule occurrence, a plan entry and a direct run are created with the scenario's choice, and the occurrence's immediate Start fails (`start_failed`) as any run's would. A run naming its own policy gets `503`;
  - once the policy is deleted, the occurrence is recorded `failed`, naming the scenario's choice.

  The schedules are created through `CreateScenarioSchedule`, so they are governed and their sweep builds the conflict preview first.
- **`TestScenarioTelemetryAcceptancePolicyCreatedDuringOutageIsPinnedAtStart`**: a run created during an outage, started once policies can be read, pins the scenario's choice (`run_selection`), not the default.
- **`TestTelemetryAcceptancePolicyIsNotJudgedByCapacityEstimates`**:
  - an estimate (direct and through `POST /api/v1/tests/estimate`) naming a missing policy succeeds, and so does one during an outage;
  - a schedule's capacity preview of a scenario whose choice was lost still builds;
  - a launch-preview build still refuses the choice.
- **`TestRemoteScenarioRunNeedsAnExplicitTelemetryPolicy`**: a remote run of a scenario with a choice is refused (`409` through `POST /api/v1/remotes/{id}/runs`) unless it names a remote policy or `project_default`.
- **`TestScenarioTelemetryAcceptancePolicyIsNotPortable`**:
  - an export omits the choice, and the content hash ignores it;
  - a crafted bundle naming a policy imports with the default;
  - a linked import that updates the scenario keeps its choice.
- **`TelemetryPolicySelect.test.tsx`**:
  - the listed options and the default's name;
  - an unavailable choice, with its described inline error and `aria-invalid`;
  - the warning for a stored choice the project lost, styled and described as a warning, not an error;
  - a reload when asked;
  - an unavailable kept source choice;
  - a failed load, and no project at all;
  - the revise options;
  - `telemetryPolicyIssue` and `telemetryPolicySaveIssue`, including a failed read, a read that times out or is ended, and the stored choice not being judged.
- **`TestHub.test.tsx`**:
  - the scenario builder saves a chosen policy;
  - a stored scenario's choice survives a save without changes;
  - an unrelated edit (a rename) keeping a stored choice deleted since is saved with both, and the builder's capacity estimates never send the choice;
  - a save whose check is pending when the hub unmounts is dropped;
  - choosing a policy deleted after the select loaded is blocked by the fresh read at save, from Review, opening the panel whose list marks it. Clearing the choice then saves none;
  - the same block with the panel already open reloads the mounted select's list, which marks the choice.
- **`CreateTestModal.test.tsx`**:
  - a revised copy keeping the source's choice omits the field;
  - choosing the project default sends `project_default`, and choosing another policy sends it;
  - a kept source policy the project no longer has blocks the create until the default is chosen;
  - a policy deleted after the modal loaded is blocked by the fresh read, with the modal busy (its fields still editable) while the read is pending, and the reloaded select marking the choice;
  - settings changed while the check is pending are not created from the stale snapshot, and the next create sends the new choice;
  - a modal unmounted while its check is pending creates nothing.
- **`scenarioAssembly.test.ts`** and **`scenarioAuthoringDraft.test.ts`**: the choice round-trips from a stored scenario into the save and run payloads (not the estimate request) and through a draft checkpoint. Clearing it omits it, and a malformed one is not restored.
- **`run_telemetry_policy_test.go`** (`cmd/apexctl`):
  - the choice sent and confirmed;
  - an unconfirmed choice, either missing or another policy, deletes the queued run without starting it and exits `2`, and another policy is named;
  - an undeleted unconfirmed run exits `3`, naming it, even when the command's deadline passes during the delete. So does a `404` or `500` delete of a run that can still be read, while either answer for a run that can no longer be read counts as deleted;
  - controller refusals of the choice exit `2`: missing, over-long and invalid, in the controller's wording. An unrelated refusal, with or without a choice, stays `3`;
  - no choice sends none;
  - flag parsing.

## Review

Round 1 used two reviewers, one on the backend and CLI and one on the dashboard and docs.

Backend and CLI:
- **Remote scenario runs** dropped the scenario's choice silently. They now need an explicit remote choice.
- **Transient policy reads** could fail a scheduled occurrence for good, and would fail imports and rollbacks. A save that keeps the choice is no longer judged, and the sweep retries.
- **`apexctl` exit codes.** A controller refusal exited `3`, and a failed cleanup delete still exited `2`.
- **Smaller items:** the message for a different echoed policy, the run-only wording at scenario save, and status tests.

Dashboard:
- **Revise modal.** A deleted kept source policy looked valid until the server refused it, and the modal did not block a gone choice.
- **Save check.** The builder's save check could use a list read for another scenario. It now reads fresh under the save lock.
- **Smaller items:** accessibility attributes, the no-project notice, a misplaced comment, and test gaps.

Round 2 used two fresh reviewers with the same split.

Backend and CLI:
- **Outage retries.** A policy-read outage failed plan entries for good. The round-1 schedule retry could launch an occurrence late, past its missed-run policy. An inherited scenario choice now survives the outage instead.
- **`apexctl`:**
  - a `404` delete was taken as proof of deletion;
  - any `400`/`409` exited `2` once the flag was set.
- **Smaller items:** a stored choice was not re-judged after its project changed underneath, and the schedule test did not cover the retry.

Dashboard:
- **Save check.** The builder judged a kept stored choice that the controller would accept.
- **Smaller items:**
  - the save-time read had no timeout;
  - the modal showed no busy state during its check;
  - the select did not reflect a blocked choice;
  - the tests did not prove freshness or that the panel opened.

Round 3 used two fresh reviewers with the same split.

Backend and CLI:
- **Outage at Start.** The docs claimed an outage never fails a schedule or plan entry. Start reads the policy store for every run, so the claim is narrowed to creation, which is all this slice changes, and the test comment is corrected. A test now shows a run created during an outage pinning the choice at Start.
- **`apexctl` classification.** It matched only one wording, so invalid-policy `409`s and over-long IDs exited `3`.
- **Delete check.** A `500` after a delete that removed the run was not checked by a read.
- **Locking.** The locking wording was inexact.

Dashboard:
- **Revise modal:**
  - it did not reload its select after a block;
  - it kept a stale error during its check;
  - its tests did not prove freshness, the busy state or an explicit choice.
- **Inline error.** It was announced twice, and alarmed on a stored choice that a save keeps.
- **Save-time read.** It outlived its screen.
- **Docs.** Two wordings were inexact.

Round 4 used two fresh reviewers with the same split. They found low-severity items only.

Backend and CLI:
- **Untested rules.** No test covered:
  - the kept-choice skip during an outage (the test's scenario stored no choice);
  - the project-change re-check.

  Both tests now fail when their clause is removed.
- **CLI delete message.** It called any failed read "still readable".

Dashboard:
- **Ended save check.** An ended check fell through to an unchecked save, and closing the builder did not end the read.
- **Untested reload.** No test proved the open select reloads after a block. The new test fails without it.

Round 5 used two fresh reviewers with the same split. They found low-severity items only.

Backend and CLI:
- **Hidden outcome.** A command deadline hid an undeleted run, or a refused input, behind the timeout.
- **Untested status.** The remote route's `409` had no test through the route.

Dashboard:
- **Warning shown as an error.** The warning for a lost stored choice was styled and described as an error.
- **Test did not edit.** The "unrelated edit" test made no edit.
- **Untested drop.** No test showed that unmounting drops a pending check's save. The new test fails without the abort clause.

Round 6 used two fresh reviewers with the same split. They found low-severity items only.

Backend:
- **Unasserted claims.** Two claims had no assertion behind them: an outage's Start failure, and a plan entry created during an outage.

Dashboard:
- **Overstated claim.** The modal's "closing ends the read" claim overstated what the modal does. It cannot be closed while busy, so the claim now says an unmount ends the read. A test shows an unmount creates nothing, and it fails without the guard.

Round 7 used two fresh reviewers with the same split.
- **Backend and CLI:** no findings.
- **Dashboard and docs** (medium): the builder's capacity estimate sent the scenario's choice. The controller judged it there, so a lost or unreadable choice failed every estimate while editing. Estimates no longer judge or send the choice, and both the server test and the dashboard test fail without their fix.

Round 8 used two fresh reviewers with the same split.
- **Dashboard and docs:** no findings.
- **Backend** (low): a schedule's capacity preview, and a plan entry's secret preflight, failed on a lost choice and dropped the error. That hid the schedule's real capacity conflicts. These capacity builds now skip the check like estimates. The schedule preview's test fails without the fix. The plan entry's secret preflight skip has no test of its own: without it, a lost choice only delays that entry's refusal from the preflight to its create.

Round 9 used two fresh reviewers with the same split.
- **Dashboard and docs:** no findings.
- **Backend:**
  - **Medium.** A governed schedule's sweep built its conflict preview with the policy check. With a lost choice the sweep failed before any occurrence existed, so it recorded no `failed` occurrence and retried silently. The preview now skips the check. The schedule test now uses governed schedules created through `CreateScenarioSchedule`, and it fails without the fix.
  - **Low.** The round-8 note overstated test coverage of the plan secret preflight. It now says that skip is untested.

Round 10 used two fresh reviewers with the same split.
- **Dashboard and docs:** no findings.
- **Backend (low, wording):** the docs said a remote run naming no policy would be judged by the remote's default. The remote's linked copy can carry its own choice, so the wording now says the remote judges the run by its own copy's choice or its project's default, never the local one.

Round 11 used two fresh reviewers with the same split.
- **Backend and CLI:** no findings. Two wording nits were also aligned: a test comment and the `apexctl` help for an undeleted run.
- **Dashboard** (low): the revise modal stayed editable during its policy check, while the create sends what was submitted. (Round 12 replaced the round-11 fix below.)

Round 12 used two fresh reviewers with the same split.
- **Dashboard** (low): the round-11 fix made the fields read-only with a disabled fieldset, which dropped keyboard focus when a field submitted the form. It is replaced. The fields stay editable, and a form changed during the check is not created, using the fingerprint the modal's dirty tracking already keeps. The test fails without the check.
- **Backend and CLI:** no findings. One optional wording point was taken: the guide now says any delete that cannot be confirmed exits `3`.

Round 13 used two fresh reviewers with the same split. Neither found anything.

Gates on the reviewed code (before the `origin/main` merge):
- the full `pkg/api` suite;
- the PostgreSQL storage suite (`-tags postgres_integration`), including `TestPostgresScenarioContentRevisionIntegration/telemetry_acceptance_policy`;
- the dashboard's full Vitest suite, `typecheck`, `lint` and `contracts:check`;
- `cmd/apexctl`, `pkg/runcontract` and the scenario storage tests.

## Remaining

- **Telemetry budget policy.** A run or scenario cannot choose a telemetry budget policy (`telemetry_policy`).
- **Library bundles** do not carry a scenario's choice. A team that moves scenarios between projects chooses again in the destination.
- **Rest of TEL-B.** In-run policy segments and the rest of the TEL-B consumer matrix.
