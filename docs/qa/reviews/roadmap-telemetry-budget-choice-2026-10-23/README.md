# Run telemetry budget choice — 2026-10-23

This [TEL-B slice](../../../ROADMAP_IMPLEMENTATION_SPEC.md#tel-b--policy-evolution-and-complete-consumers) lets a run choose the project telemetry budget policy that sets its live budgets. The [policy selection slice](../roadmap-telemetry-policy-selection-2026-10-22/README.md) did the same for the acceptance policy.

Before this slice, every run was classified by the system threshold settings. A project's budget policies were only pinned, so a team could not run with the budgets it had defined.

Scenario inheritance, the dashboard selects and `apexctl run` follow in a separate slice.

## Contract

- **The choice.** `telemetry_budget_policy_id` on `POST /api/v1/tests`, `POST /api/v1/ci/runs` and remote runs names one of the run's project's telemetry budget policies (`telemetry_policy`). It sets the run's live budgets instead of the system threshold settings.
  - **Without a choice**, a run keeps the version 1 system receipt.
  - **The project's default budget policy** is still pinned only. It does not set a run's budgets, as before this slice.
- **Validation at creation** (`validateRunTelemetryBudgetPolicy`, `validateRunTelemetryPolicyChoice`):
  - The policy must be a live, valid `telemetry_policy` of the run's own project.
  - A policy the run's project lacks, including another project's or another kind's, gets `400` with the same message.
  - An over-long ID gets `400`.
  - An invalid policy gets `409`.
  - A policy whose `sustained_window_seconds` differs from live classification's fixed 15 seconds gets `409` (`telemetryBudgetPolicyDefinition`).
  - A failed policy read gets `503`.
  - Capacity estimates and previews do not judge the choice, as for the acceptance choice.
- **Persistence.** The choice is stored in `tests.telemetry_budget_policy_id` (schema release v82, `run-telemetry-budget-policy`) and reloaded with the run.
- **Revised copies.** A revised copy keeps its source's choice unless it names another, or `project_default`. An inherited choice that is no longer usable refuses the copy and names the source run's choice.
- **At Start** (`persistManagedTestStartOutsideLockLocked`, `resolveRunPolicyPinsForRun`):
  - The chosen revision is pinned with source `run_selection`, in place of the project default's budget pin.
  - `projectTelemetryBudgetPolicy` then replaces the system receipt captured at admission, in the same write-once admission record.
  - A chosen policy that is gone or unusable refuses Start (`errRunPolicyConfiguration`) and leaves the run queued.
- **The version 2 receipt** (`pkg/telemetrypolicy`, `VersionProjectPolicy`):
  - **Identity.** It is `apexload.telemetry_policy.v2` and names the policy ID, its revision and the SHA-256 of its definition (`definition_sha256`).
  - **Kept from the controller.** Cadence, cadence scope, capture time, capture boundary, classification algorithm, the 15-second sustained window and the coverage rule are unchanged.
  - **Stage limits.** Each stage takes the policy's limit. A stage the policy leaves unset uses the built-in default, as `resolveTelemetryReevaluationPolicy` does, and a stage set to `0` is disabled.
  - **Version 1 is unchanged.** Version 1 receipts and their hashes are unchanged: `definition_sha256` is omitted when empty.
  - **Validation.** `Validate` accepts version 1 only with the system ID, revision 1 and no digest. It accepts version 2 only with these:
    - a policy ID other than the system's, at most 128 bytes, valid UTF-8, without surrounding whitespace and without control characters;
    - a revision of at least 1;
    - a lowercase 64-character hexadecimal digest.
- **Live classification** reads the receipt (`runTelemetryPolicy`, `telemetryPolicyThresholds`). The chosen budgets therefore decide degraded and breached time, and with it an acceptance policy's `max_breached_known_time_fraction`.
- **CI.**
  - **Permission.** A CI run may choose a budget policy only where `allow_telemetry_policy_selection` is set, the same permission as the acceptance choice, because looser budgets can loosen a `fail_ci` acceptance policy. Otherwise the request is refused with `403`. `project_default` needs no permission.
  - **Retries.** The choice is part of the request metadata a retry under the same key must repeat. An exact retry of an accepted run returns it even after the choice is disallowed or deleted.
  - **Echo.** The response echoes the choice.
- **apexctl CI.**
  - `apexctl ci run --telemetry-budget-policy` (action input `telemetry-budget-policy`) sends the choice. It is part of the derived idempotency key only when set, and it needs controller mode.
  - A create response that does not echo the choice is a configuration error, and the accepted run is aborted.
  - The result file carries `telemetry_budget_policy_id`, and the job summary has a "Telemetry budgets" row when the run chose them.
- **GitHub Check.** The check summary, from the queued check on, says when a run chose its live budgets, naming the policy, because looser budgets can loosen a `fail_ci` criterion.
- **Remote runs.** A remote controller that does not echo the choice would classify the run by its system settings. Its run is deleted before it starts, and the create is refused (`409`).
- **Readers.**
  - `apexctl analysis telemetry-policy` (`decodeTelemetryPolicyCLI`) accepts `definition_sha256` only alongside version 2.
  - The dashboard (`telemetryPolicyModel.ts`) checks the same identity rules and verifies the version 2 digest against the controller's encoding. `TelemetryPolicy.tsx` labels a chosen project policy and shows its definition digest.
  - An older reader never presents a version 2 receipt as the system settings. An older dashboard shows it as unsupported, and an older `apexctl analysis telemetry-policy` refuses it as an invalid receipt.

## Validation

- **`TestProjectPolicyReceiptIdentity`** (`pkg/telemetrypolicy`):
  - accepts a valid version 2 receipt, and leaves the version 1 hash unchanged;
  - refuses a digest on version 1, version 1 with another ID, the system ID or an empty, padded, control-character or over-long ID on version 2;
  - refuses revision 0, a missing, upper-case or short digest, another sustained window and an unknown version;
  - refuses a changed revision under the old hash.
- **`TestProjectTelemetryBudgetPolicyReceipt`** (`pkg/api`):
  - **Identity.** The receipt names the revision, its digest and the controller's cadence.
  - **Stage limits.** The policy's limits are used, unset stages get defaults, and a stage set to 0 is disabled.
  - **Use.** It sets the live thresholds and gives available evidence.
  - **Untouched input.** The system receipt is not changed.
  - **Refusals.** It refuses another window, an invalid policy, another kind, revision 0, the system ID, an invalid system receipt and a version 2 base. It accepts the live 15-second window.
- **`TestRunChosenTelemetryBudgetPolicySetsLiveBudgets`**:
  - **At creation:** missing and foreign choices get the same message, and an over-long ID, another window and an acceptance policy's ID are refused.
  - **Persistence:** the choice is persisted and reloaded.
  - **Revised copies:** they keep the choice, drop it for `project_default` and take another when named.
  - **At Start:** a version 2 receipt and a `run_selection` budget pin, with the live thresholds taken from it.
  - **Without a choice:** a run keeps the system receipt and pins the project's default budget policy.
- **`TestRunChosenTelemetryBudgetPolicyUnusableRefusesStart`**:
  - a deleted policy refuses Start, and so does one changed to another window; the run stays queued with no receipt;
  - a revised copy inheriting the choice is refused, naming the source run's choice.
- **`TestCreateTestRouteChosenTelemetryBudgetPolicy`**: the route echoes the choice, a keyed retry with spaces replays, a retry naming another policy conflicts, and an unusable policy gets `409` and a missing one `400`.
- **`TestCIRunCarriesTheChosenTelemetryBudgetPolicy`**:
  - without the permission, the choice gets `403`, while `project_default` is allowed;
  - the choice is echoed and reaches its run;
  - a retry naming another policy conflicts, and an unknown policy gets `400`;
  - an exact retry after the choice was disallowed and deleted returns the run.
- **`TestChosenTelemetryBudgetPolicyStoreOutageIs503`**: both routes answer `503` during a policy-read outage.
- **`TestRunPolicyChoiceWithoutPolicyStoreNamesThePolicies`**: without a policy store, both choices are refused with the plural policy name.
- **`TestRemoteRunNeedsTheChosenBudgetPolicyConfirmed`**: an unconfirmed choice deletes the remote run and gets `409`, `project_default` needs no confirmation, and a confirmed choice is created.
- **`TestControllerCIRunNeedsTheChosenBudgetPolicyConfirmed`** (`cmd/apexctl`): the choice is sent and changes the derived key; an unconfirmed choice aborts the run as a configuration error, and a confirmed one passes.
- **`TestCIGitHubCheckNamesChosenTelemetryBudgets`** and **`TestCISummaryShowsChosenTelemetryBudgets`**: the GitHub Check (queued and completed) and the job summary name a chosen budget policy, and say nothing about budgets when the run made no choice.
- **`TestParseCIArgsTelemetryBudgetPolicy`**: flag parsing and `project_default`; local mode is refused, and the field is omitted when unset.
- **`TestTelemetryPolicyCLIProjectPolicyReceipt`**: accepts a version 2 receipt. Refuses a digest on version 1, version 2 without a digest, a null, numeric or changed digest, and the system ID.
- **`telemetryPolicyModel.test.ts`** and **`TelemetryPolicy.test.tsx`** (dashboard):
  - a version 2 receipt verifies against the Go-computed hash `f034a3ac…`, which `TestTelemetryPolicyCLIProjectPolicyReceipt` pins too;
  - the identity refusals mirror the Go ones and happen before any digest is computed;
  - a 128-byte ID, an ID Go's encoder escapes (`<`, `>`, `&`, U+2028, U+2029) and an ID with a leading U+FEFF, which Go's `strings.TrimSpace` keeps, verify against Go-computed hashes;
  - a changed digest is refused;
  - the card labels the system receipt and a chosen project policy, with its digest.
- **Existing suites.** The existing acceptance-choice, CI, telemetry and remote-run tests, plus `cmd/apexctl`, `pkg/storage` and `pkg/runcontract`, pass. `go generate ./pkg/api` and `contracts:check` are current.

## Review

Independent reviewers worked on scratch snapshots without git. Each round reviewed the whole diff.

- **Round 1** (backend and CLI; dashboard and docs):
  - **Backend and CLI.** The `telemetry-budget-policy` action input had split the `telemetry-policy` input's `required` and `default` lines. An unavailable policy store said "policys".
  - **Dashboard.** The identity tests passed even with the identity checks removed, because the digest check refused the mutated receipts anyway; they now assert no digest is computed. The digest input lacked Go's escaping of `<`, `>`, `&`, U+2028 and U+2029, and used JavaScript's `trim()` instead of Go's whitespace set. Added cases: an empty ID, a short digest, a 128-byte ID, an escaped ID and a leading U+FEFF.
  - **Tests and docs.** The apexctl test now pins the receipt hash. The default-pin check now fails when no pin is found. Card text, spec wording and QA notes were corrected.
- **Round 2** (both scopes):
  - **CI visibility.** The GitHub Check and the apexctl job summary did not say that a run chose its budgets.
  - **Dashboard revise.** The revise modal can neither keep nor drop the budget choice explicitly. It is documented under Remaining and follows in the scenario slice.
  - **Whitespace tests.** Only plain space and U+2029 exercised the whitespace set.
  - **Wording.** Older-reader behaviour and two other phrases were reworded.
- **Round 3** (whole diff): the queued Check lacked the budget line, and the whitespace tests sampled the U+2000–U+200A range instead of covering it.
- **Round 4** (whole diff): no findings. Every caller of `ciGitHubCheckSummary` passes the run's own metadata. The dashboard's whitespace set equals Go's `unicode.IsSpace`, checked by enumerating every rune.

## Remaining

- **Scenario level.** A scenario-level budget choice, inherited by its runs, schedules and plan entries. Also the scenario builder's and revise modal's selects, and `apexctl run --api … --telemetry-budget-policy`.
- **Dashboard revise.** Until the revise modal has a budget select, a dashboard revise always keeps the source run's choice. If that policy is gone or unusable, the revise is refused (`409`), and only the API can name another policy or `project_default`.
- **Older readers.** Until they are upgraded, an older dashboard shows a version 2 receipt as unsupported and an older apexctl refuses it as invalid.
- **Credential-like policy IDs.** The public archive projection scrubs credential-like text in a receipt's policy ID, as it does for pins. A project policy ID that looks like a credential would therefore show as an unsupported receipt there.
