# PR #267 whole-branch review — 2026-10-23

A cross-commit review of everything [PR #267](https://github.com/JasonCzech/APEX_Load/pull/267) merged into `main` (merge commit `33125479c`): the telemetry acceptance and budget policy choices for runs and scenarios (TEL-B), REC-A evacuation rollback, the HTTP body byte query profile (NA-C), the protocol matrix check (PROTO-B), two capacity-hint fixes and the lint fixes inherited from `main`. Each slice had already been reviewed on its own to zero findings; this pass looked for defects that only show when the pieces combine.

## Round 1

Three reviewers read a snapshot of the merge commit.

- **Telemetry choices, backend and CLI.** No high or medium findings: CI idempotency metadata, the GitHub Check and job summary, the Start admission write, restart and HA reload, inheritance, remote runs, the schema releases (v80–v83), library portability and the CLI all carry both choices consistently. Two low findings, both routes where a policy failure collapsed to `400`:
  1. **CI create after the pre-check.** `handleCreateCIRun` judges both choices before materializing the run, and `CreateTest` judges them again. A policy read failing in between, or a budget policy revised in between to another sustained window, fell through to `400`, which `apexctl ci` treats as a non-retryable configuration error. The create error now goes through `runPolicyErrorStatus` first (`503`, `409`).
  2. **Launch preflight.** `POST /api/v1/tests/preflight` judges both choices (it is not a capacity estimate), but answered every failure with `400`. It now answers `503` for a failed policy read and `409` for an unusable policy, as `POST /api/v1/tests` does, and the API documentation lists both.
- **Recovery and the lint commit.** No findings. One lint fix (`replay_artifact_volume.go`) changes behaviour, for the better: a failed marker re-read after `Link` used to return an empty volume identity as success. The capacity-hint commits change allocation only. REC-A does nothing with its flag off, converges across repeated restarts and never adopts staged work. The CI failure of `TestDistributedEvacuateGeneratorProvisionsPreviouslyUnassignedSurvivor` is test timing, not a race: the failing case does nothing REC-A-specific, and its 400 ms backend timeout covers AssignWork, CommitWork and a 200 ms stream setup window, which a loaded runner can exceed. The scenario's backend timeout is now 2 s (`evacuationProvisionBackendTimeout`), and its barrier wait follows it; the 200 ms setup window assertion is unchanged.
- **Analytics, QA, dashboard and docs.** No findings: NA-C's authorization, limits and population, PROTO-B failing on bad input, the acceptance and budget selects together, the v1 and v2 receipt views and the docs all agree.

## Validation

- **`TestCIRunCreateKeepsPolicyStatusesAfterThePreCheck`**: an outage injected after the pre-check (through `beforeCIRunMaterialize`) is `503`, and a budget policy revised to another window in between is `409`.
- **`TestLaunchPreflightAnswersTelemetryPolicyFailuresLikeCreate`**: `400` for a missing budget policy, `409` for one with another window and for an invalid acceptance policy, `503` for either choice during an outage, and `200` without choices.
- A negative control (the handlers as merged) fails both tests.
- The evacuation scenario passes 5 of 5 alone and 3 of 3 under `-race` with every variant.

## Round 2

A fresh reviewer verified the fixes: no findings. The two sentinels come only from policy reads, policy definitions and run policy selections, which `POST /api/v1/tests` already answers with `503` and `409`, so the CI create and preflight now answer exactly as it does. One consequence follows from that: a malformed browser or journey policy selection on preflight is now `409`, as on create, instead of `400`. The generated API documentation is fresh, and the evacuation scenario's 200 ms setup window assertion and barrier wait still hold at the 2 s timeout.
