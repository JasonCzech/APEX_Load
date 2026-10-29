# APEX Load GitHub Action

Use the versioned action from a downstream repository:

```yaml
permissions:
  contents: read
  id-token: write

steps:
  - uses: actions/checkout@<reviewed-commit-sha>
  - id: performance
    uses: JasonCzech/Project_Stormrunner/.github/actions/apexload-run@v1
    with:
      controller-url: https://apexload.example.com
      project-id: project-a
      script-path: performance/order-flow.json
      p95-latency-ms: "800"
      max-error-pct: "2"
```

Controller mode is the default. It exchanges the GitHub Actions OIDC identity
for a project-scoped, short-lived APEX token, starts an idempotent run, waits for
the controller verdict, and emits JSON/JUnit evidence. Use `auth-mode: api-key`
and expose a scoped `APEXLOAD_API_KEY` only when the GitHub server cannot provide
Actions OIDC. The controller independently verifies the referenced workflow run
for this fallback. If project policy requires a protected-environment or runner
environment claim, use OIDC; GitHub's workflow-run API cannot prove those
job-scoped claims.

Local mode is an explicit smoke-test fallback:

```yaml
- uses: JasonCzech/Project_Stormrunner/.github/actions/apexload-run@v1
  with:
    execution-mode: local
    script-path: performance/smoke.json
    publish-check: "false"
    require-sla: "false"
```

The action does not upload artifacts itself because GitHub.com and GHES support
different artifact-action generations. Upload the `json-path` and `junit-path`
outputs in the caller using the action version supported by that server.
Because the default evidence directory is `.apexload`, set the uploader's
`include-hidden-files: true` input for those explicit output paths; do not enable
hidden files for broad or untrusted directories.
When the action downloads `apexctl`, its pinned `actions/setup-node` step
provides Node 22 for strict signed-manifest validation; mirror that action and
toolcache on air-gapped GHES runners.

The `validity` output carries the run's validity verdict. For projects that
present root cause, the `root-cause` output carries its root cause in one line;
set `wait-analysis` (for example `5m`) to wait for the automatic analysis after
the run ends (see [Root-cause summary](../../../docs/CICD_INTEGRATION.md#root-cause-summary)).

Installation, signature, and CLI-startup failures still produce schema-v1 JSON,
JUnit, a job summary, and the stable action outputs. The composite action defers
the gate conclusion until those files are finalized, so it fails once in its
last step instead of losing evidence in an earlier step.

## Inputs

All inputs are defined in `action.yml`. In `auth-mode: api-key`, also provide the scoped service-account key via the job/step environment variable `APEXLOAD_API_KEY` (never as an action input).

| Input | Default | Description |
| --- | --- | --- |
| `execution-mode` | `controller` | `controller` (default distributed/managed gate) or `local` (explicit smoke-test fallback). |
| `controller-url` | `""` | HTTPS controller base URL; required in `controller` mode. |
| `project-id` | `""` | Controller project ID; required in `controller` mode. |
| `script-path` | `""` | Path to a JSON DSL script in the workspace. Specify exactly one of `script-path` or `script-id`. |
| `script-id` | `""` | Existing controller script ID. Specify exactly one of `script-path` or `script-id`. |
| `release-id` | `""` | Optional release correlation ID. |
| `test-name` | `""` | Optional display name for the test run. |
| `auth-mode` | `oidc` | `oidc` (default short-lived GitHub OIDC exchange) or `api-key` (reads `APEXLOAD_API_KEY` from the environment). |
| `vusers` | `"10"` | Number of VUsers. |
| `duration` | `1m` | Test target duration. |
| `ramp-up` | `0s` | Ramp-up duration. |
| `base-url` | `""` | Policy-controlled base URL override. |
| `variables-file` | `""` | Optional path to a JSON object containing non-secret string variable overrides. |
| `p95-latency-ms` | `""` | Maximum P95 latency threshold in milliseconds. |
| `max-error-pct` | `""` | Maximum error percentage threshold. |
| `max-avg-server-processing-ms` | `""` | Maximum average server processing time threshold in milliseconds. |
| `max-avg-transfer-ms` | `""` | Maximum average transfer time threshold in milliseconds. |
| `max-error-count` | `""` | Maximum total error count threshold. |
| `require-sla` | `"true"` | Fail the gate when no SLA thresholds are evaluated. |
| `require-results` | `"true"` | Fail the gate when the test produces zero transactions. |
| `timeout` | `30m` | Maximum wall-clock time for the CI run. |
| `wait-analysis` | `""` | Optional post-run wait (up to `10m`, e.g. `5m`) for automatic root-cause analysis so `root-cause` and the job summary include the completed verdict. |
| `abort-on-cancel` | `"true"` | Request controller abort if the timeout elapses or the workflow is canceled. |
| `publish-check` | `""` | Publish a controller-owned GitHub Check (`true` by default in `controller` mode, `false` in `local` mode). |
| `evidence-directory` | `.apexload` | Workspace or runner-temp directory for JSON and JUnit evidence (symlink traversal is rejected). |
| `idempotency-key` | `""` | Optional unique gate key; set when a single workflow run executes multiple gates for the same script. |
| `telemetry-policy` | `""` | Project telemetry acceptance policy ID overriding the project default (requires `allow_telemetry_policy_selection`). |
| `telemetry-budget-policy` | `""` | Project telemetry budget policy ID overriding system live telemetry thresholds (requires `allow_telemetry_policy_selection`). |
| `telemetry-inconclusive` | `""` | Maps an `inconclusive` telemetry acceptance verdict to `pass` or `fail`; required (no default) when the effective acceptance policy mode is `fail_ci`. |
| `apexctl-version` | `""` | Exact released `apexctl` version; empty uses the version pinned by this action revision. |
| `apexctl-path` | `""` | Explicit pre-provisioned trusted `apexctl` binary path; skips release download and `actions/setup-node`. |
| `release-base-url` | `""` | Optional release mirror root ending before `/VERSION` (defaults to this repository's Releases URL). |
| `release-public-key` | `""` | Path to PEM public key verifying `SHA256SUMS.sig` when `release-signing-key.pem` is not bundled with the action. |

## Outputs

| Output | Description |
| --- | --- |
| `outcome` | Stable machine-readable gate outcome (`passed`, `sla_breached`, `run_invalid`, `telemetry_failed`, `telemetry_inconclusive`, `execution_error`, etc.). |
| `test-id` | Controller test ID (empty when local or rejected preflight). |
| `status` | Terminal controller or local execution status (`COMPLETE`, `FAILED`, `ABORTED`, etc.). |
| `sla-evaluated` | `"true"` or `"false"` indicating whether SLA thresholds were evaluated. |
| `sla-breached` | `"true"` or `"false"` indicating whether any SLA threshold was breached. |
| `transactions` | Total recorded transactions. |
| `errors` | Total recorded errors. |
| `tps` | Final transactions per second. |
| `duration-ms` | Run duration in milliseconds. |
| `report-url` | Human-readable APEX Load report URL. |
| `telemetry-verdict` | Telemetry acceptance verdict (`pass`, `fail`, or `inconclusive`; empty when no acceptance policy applied). |
| `validity` | Run validity verdict (`valid`, `suspect`, `invalid`, or `unknown`; empty when unreported). |
| `root-cause` | Single-line root-cause summary (leading hypothesis, `"no cause supported"`, `"insufficient evidence"`, or analysis state; empty when disabled). |
| `check-url` | Controller-owned GitHub Check URL. |
| `json-path` | Absolute path to the schema-v1 JSON evidence file. |
| `junit-path` | Absolute path to the JUnit XML evidence file. |
| `apexctl-version` | Installed or verified `apexctl` version string. |

## Binary trust

By default, the action downloads the exact `apexctl` release pinned in
`apexctl-version`. It verifies:

1. the detached OpenSSL SHA-256 signature on `SHA256SUMS`;
2. the signed checksum for `release-manifest.json`; and
3. the signed checksum for the selected OS/architecture binary; and
4. schema version, release tag/version, unique platform record, asset name,
   checksum, and nonzero inventory size inside the signed manifest.

The matching PEM public key must be bundled as `release-signing-key.pem` or
passed as `release-public-key`. Missing signature trust fails closed. Internal
GHES and air-gapped installations can set `release-base-url` to a trusted mirror.
`apexctl-path` skips download only for an explicitly provisioned trusted binary.
The repository intentionally does not contain a placeholder signing identity:
release owners must provision the real public verification key before publishing
the moving `v1` action tag.

## Pull requests

Never run a load script from an untrusted fork. Gate the job on the pull request
head repository identity and never combine `pull_request_target` with checkout
or execution of contributor-controlled files. The controller independently
checks base and head repository IDs, workflow/ref/environment, execution caps,
protocols, and target allowlists before accepting the run.
