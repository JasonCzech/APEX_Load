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

Installation, signature, and CLI-startup failures still produce schema-v1 JSON,
JUnit, a job summary, and the stable action outputs. The composite action defers
the gate conclusion until those files are finalized, so it fails once in its
last step instead of losing evidence in an earlier step.

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
