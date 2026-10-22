# Telemetry acceptance policy selection — 2026-10-22

This [TEL-B slice](../../../ROADMAP_IMPLEMENTATION_SPEC.md#tel-b--policy-evolution-and-complete-consumers) adds explicit non-default policy selection. TEL-3 asks that the CLI support policy selection; this slice delivers it for `apexctl ci run` and the API. Plain `apexctl run` remains open.

Before this slice, every run was judged by its project's default telemetry acceptance policy, pinned at Start. A pipeline could not ask for a different gate for one run without changing the project's default for every run.

## Contract

### Choosing

- `telemetry_acceptance_policy_id` is accepted on:
  - `POST /api/v1/tests` (`CreateTestRequest`), trimmed before the keyed-create digest;
  - `POST /api/v1/ci/runs` (`ciRunCreateRequest`);
  - `POST /api/v1/remotes/{id}/runs`, which forwards it to the remote controller.
- `apexctl ci run --telemetry-policy <id>` sends it, in controller mode only, and so does the composite action input `telemetry-policy`. It is omitted when unset, and it is part of `apexctl`'s derived idempotency key when set.
- `project_default` names the project's default explicitly. It matters for a revised copy, where it drops the source run's choice. Elsewhere it is the same as naming none: CI and `apexctl` treat it as unset. A keyed retry of `POST /api/v1/tests` must still repeat its spelling, because on a revised copy the two spellings differ.

### At creation

`validateRunTelemetryAcceptancePolicy` requires one of the run's own project's live, valid acceptance policies, at most 128 bytes. Lookups never leave the run's project, so another project's policy is refused exactly like a missing one. Failures are answered as:

| Choice | Answer |
| --- | --- |
| A policy the project lacks | `400` |
| An invalid policy (`errRunPolicyConfiguration`) | `409` |
| A failed read (`errRunPolicyStoreUnavailable`) | `503` |

The choice is persisted with the run on the tests table (`telemetry_acceptance_policy_id`, schema v74, SQLite and PostgreSQL). An older binary refuses the v74 database.

A revised copy of a queued run keeps its source's choice unless it names another, or `project_default`. An inherited choice that is no longer usable is refused with a message that names the source run's choice and the way out.

### CI

- **Permission.** A new CI run may choose only where the project's GitHub Actions policy sets `allow_telemetry_policy_selection`. That is an administrator's decision, like `validity_gate`, and it is off by default. Otherwise the choice is refused (`403`), so a pipeline cannot exchange a `fail_ci` default for a weaker policy. `project_default` needs no permission.
- **Early check.** A new run's choice is checked before any GitHub check or run is written.
- **Idempotency.** The choice is part of the run's idempotency metadata, so a retry under the same key that names another policy conflicts. An exact retry of an accepted run returns that run even after its choice was disallowed or deleted, as an accepted run keeps its validity gate.
- **Inconclusive mapping.** `ciTelemetryInconclusiveRequirement` judges by the chosen policy when there is one.
- **Echo.** The CI responses echo the choice (`telemetry_acceptance_policy_id`). An older controller ignores the field and would judge the run by the default. So when the create response lacks the echo, `apexctl` stops the accepted run and exits with a configuration error. It also refuses a final response that lacks it.
- **Audit.** The `ci.run.create` audit event records the choice.

### Remote runs

A remote controller that does not echo the forwarded choice would judge the run by its project's default. Its run is deleted before it starts, and the create is refused (`409`).

### At Start

`resolveRunPolicyPins(projectID, selected)` pins the chosen policy's current revision with source `run_selection` (`runPolicySourceSelected`), and freezes its definition beside the pin in place of the default. The other policy kinds keep their defaults.

A chosen policy that is gone or invalid by Start refuses Start (`errRunPolicyConfiguration`), as an invalid default does, rather than falling back to the default. A direct Start leaves the run queued.

### Shown

- The verdict's policy pin carries `source: run_selection` in the CI response, the run manifest and the report JSON.
- The GitHub check says "(chosen by the run)".
- The `apexctl` job summary says "policy chosen by the run".
- The HTML/PDF reports and the dashboard show the policy's name and revision only.

## Validation

- **`TestRunChosenTelemetryAcceptancePolicyIsPinnedAtStart`**:
  - another project's policy refused like a missing one;
  - an over-long ID;
  - a trimmed choice persisted, and kept by a runner reloaded from the store;
  - a revised copy keeping it, `project_default` dropping it, and another choice replacing it;
  - Start pinning the chosen policy (`run_selection`, its `fail_ci` definition) and not the default.
- **`TestRunChosenTelemetryAcceptancePolicyGoneRefusesStart`**: a policy deleted after creation refuses Start and leaves the run queued, and a revised copy inheriting it is refused with the source run's choice (`doomed-gate`) named.
- **`TestCreateTestRouteChosenTelemetryAcceptancePolicy`**: `POST /api/v1/tests`, with a keyed retry spelling the choice with spaces replaying the run, a keyed retry naming another policy conflicting, and an invalid policy answered `409`.
- **`TestChosenTelemetryAcceptancePolicyStoreOutageIs503`**: a policy store outage answered `503` on `POST /api/v1/tests` and `POST /api/v1/ci/runs`.
- **`TestCITelemetryInconclusiveRequirementFollowsTheChosenPolicy`**: the mapping requirement under each combination of default and choice, an unknown choice, and an explicit mapping.
- **`TestCIRunCarriesTheChosenTelemetryAcceptancePolicy`**, on `POST /api/v1/ci/runs`:
  - refusal while selection is not allowed, and `project_default` accepted there with nothing echoed;
  - the choice reaching the run and echoed;
  - a retry naming another policy conflicting;
  - an unknown choice refused (`400`) with and without a mapping;
  - an exact retry returning the run after selection was turned off and the policy deleted.
- **`TestCIGitHubCheckNamesAChosenPolicy`** and **`TestTelemetryVerdictModeLabelNamesAChosenPolicy`**: the GitHub check and the `apexctl` summary label a chosen policy, and only a chosen one.
- **`TestRemoteRunNeedsTheChosenPolicyConfirmed`**: a remote run whose controller does not echo the choice is deleted and refused (`409`); one that does is created.
- **`TestParseCIArgsTelemetryPolicy`**, **`TestDerivedCIIdempotencyKeySeparatesGateConfiguration`** and **`TestControllerCIRunNeedsTheChosenPolicyConfirmed`**:
  - the `apexctl` flag, with `project_default` as no choice;
  - the derived key, pinned for an unchosen request to the value before this slice, and changed by a choice;
  - an unconfirmed create stopped without polling and reported as a configuration error.
- **`TestSchemaReleaseFingerprintIsCurrent`**: records schema v74. The PostgreSQL storage integration suite passes on v74: `go test -tags postgres_integration ./pkg/storage` with `APEXLOAD_TEST_POSTGRES_URL` set.

## Review

Round 1 used two reviewers, one on the backend and one on contracts:
- A CI caller could exchange a `fail_ci` default for an advisory policy. CI selection is now an administrator's opt-in.
- An older controller silently dropped the choice. The choice is now echoed and checked.
- Errors:
  - a revised copy could not drop an inherited choice;
  - a store failure looked like a refusal;
  - the CI choice was checked after GitHub check work.
- Wording and tests:
  - the derived key ignored the choice, and the keyed digest was taken before trimming;
  - the wording was stale and the docs overclaimed;
  - there were test gaps.

Round 2, the same reviewers:
- **Retries.** An exact CI retry was refused once the choice was disallowed or deleted. The check now applies only to a new run.
- **`project_default` on CI** needed permission and ended in an unconfirmed-choice error. It is now no choice.
- **Late detection.** `apexctl` noticed a missing echo only after the whole load. It now stops the run at create.
- **Remote runs** dropped the choice silently on an older remote. Such a run is now deleted and refused.
- **Smaller items:**
  - the audit event lacked the choice;
  - the dashboard's GitHub Actions policy type lacked the flag, so its round-trip test missed it;
  - the README claimed an untested capacity-queue outcome;
  - evidence and assertion gaps.
- **Not changed.** A keyed `POST /api/v1/tests` retry must repeat the spelling of `project_default`, because a revised copy gives it a different meaning from an empty choice.

## Remaining

- **Dashboard.** There is no dashboard control for choosing a policy for a run, and no revise-modal notice of an inherited choice.
- **Entry points.** Plain `apexctl run`, scheduled runs and execution-plan runs use the project's default.
- **Telemetry budget policy.** A run cannot choose a telemetry budget policy (`telemetry_policy`).
- **Library bundles.** A run record carrying a choice changes the library result's content hash, so an older importer refuses it, as with any new run-record field.
- **Rest of TEL-B.** In-run policy segments and the rest of the TEL-B consumer matrix.
