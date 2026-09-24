# CI/CD Integration

APEX Load is designed to run as an automated performance gate in delivery
pipelines. `apexctl` exits non-zero on SLA breach and emits machine-readable
output (JSON + JUnit XML), so any CI system can fail a build on a regression and
surface the result in its native test UI.

## Building blocks

### Exit codes

| Command | Exit 0 | Exit 1 | Exit 2+ |
| --- | --- | --- | --- |
| `apexctl ci run` | passed | SLA breach | configuration, execution, timeout, or cancellation (see JSON `outcome`) |
| `apexctl run` | test ran, no SLA breach | execution error **or** SLA breach | — |
| `apexctl status` | not breached, not failed | SLA breached, or test FAILED/ABORTED | bad args / fetch error |
| `apexctl wait` | terminal COMPLETE, not breached | breach, FAILED/ABORTED, or timeout | bad args |

A pipeline that simply runs `apexctl run ... ` and lets a non-zero exit fail the
step already has a working performance gate.

### Machine-readable output

- `--format json` prints a result object (`status`, `sla_evaluated`,
  `sla_breached`, `sla_details`, `transactions`, `errors`, `tps`, `duration_ms`).
- `--junit <path>` writes a JUnit XML report. The report contains an
  `execution` testcase (errors on a transport/runtime failure) and an `sla`
  testcase (fails on breach, with each breached threshold in the failure body;
  skipped when no thresholds are configured). This is the de-facto
  Ant/Surefire dialect consumed by GitHub Actions test reporters and artifact
  processors.

### SLA thresholds

Provide thresholds on the CLI (override) or in the script's `goal_config`:

```
apexctl run --script order-flow.json \
  --p95-latency-ms 800 \
  --max-error-pct 2.0 \
  --max-error-count 50 \
  --junit apexload-results.xml
```

CLI flags take precedence over script-declared goals (see
`cmd/apexctl/sla.go:mergeGoalConfigs`).

## Patterns

### Pattern A — run-and-gate (self-contained)

One command runs the test and gates the build. Best when the load is small
enough to run on the CI agent, or when targeting an ephemeral PR environment via
`--base-url`.

```
apexctl run --script order-flow.json --p95-latency-ms 800 --junit results.xml
```

### Pattern B — controller-native CI gate (recommended)

For larger, distributed runs, use the bounded CI API through `apexctl ci run`.
This path binds the run to a project and GitHub identity, snapshots an inline
script without adding library clutter, refreshes short-lived OIDC credentials,
publishes the controller-owned Check, and aborts on cancellation.

```
apexctl ci run \
  --controller-url https://apexload.internal \
  --project-id project-a \
  --script-path order-flow.json \
  --p95-latency-ms 800 \
  --json .apexload/result.json \
  --junit .apexload/result.xml
```

OIDC is the default. The workflow grants `id-token: write`; a scoped
`APEXLOAD_API_KEY` is the fallback for GHES installations without Actions OIDC.
The fallback still verifies the exact workflow run through the controller's
GitHub App before accepting a load. Because that API cannot prove job-scoped
environment or runner claims, a project policy that requires either claim must
use OIDC rather than the API-key fallback.

Production and HA controllers must expose one stable, deployment-specific OIDC
audience. Set `APEXLOAD_PUBLIC_URL` to the canonical external HTTPS controller
URL (the controller derives an opaque audience from it), or set the explicit
`APEXLOAD_CI_OIDC_AUDIENCE` override and configure the same value in each
project Actions policy. Discovery and token exchange return `503` rather than
falling back to a generic or per-pod audience when neither value is available.
GitHub.com and GHES issuer/server policy URLs must use HTTPS; plaintext HTTP is
accepted only for explicit non-production development.

## Supported integration

| System | File | Notes |
| --- | --- | --- |
| GitHub Actions | [`.github/actions/apexload-run/action.yml`](../.github/actions/apexload-run/action.yml) | Canonical versioned composite action; emits JSON/JUnit paths for a caller-selected artifact action. |

GitHub Actions is the sole supported CI/CD path. Obsolete Jenkins, Azure
Pipelines, and copy-to-activate workflow templates have been removed so the
canonical action and active workflows remain the only executable integration
contract.

## Copy-ready GitHub Actions gates

### GitHub.com pull-request gate with OIDC

This job is intentionally disabled for fork pull requests. Those pull requests
should receive static DSL validation from the authoritative `CI` workflow only.
That workflow always publishes `CI / Required`: GitHub.com uses an ephemeral
hosted runner, while GHES requires the isolated `apexload-ci-unprivileged`
runner label. Full CI remains enabled for same-repository and Dependabot pull
requests, and no fork job may fall back to a trusted runner group.

```yaml
name: APEX Load gate
on:
  pull_request:
  workflow_dispatch:

permissions:
  contents: read
  id-token: write

jobs:
  performance:
    if: github.event_name != 'pull_request' || github.event.pull_request.head.repo.full_name == github.repository
    runs-on: ubuntu-24.04
    timeout-minutes: 40
    steps:
      - uses: actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683 # v4.2.2
        with:
          persist-credentials: false
      - id: apexload
        uses: JasonCzech/Project_Stormrunner/.github/actions/apexload-run@v1
        with:
          controller-url: https://apexload.internal.example
          project-id: checkout-service
          script-path: performance/order-flow.json
          p95-latency-ms: "800"
          max-error-pct: "2"
          timeout: 30m
      - if: always()
        uses: actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2
        with:
          name: apexload-evidence
          path: |
            ${{ steps.apexload.outputs.json-path }}
            ${{ steps.apexload.outputs.junit-path }}
          if-no-files-found: error
          include-hidden-files: true
```

Pin regulated consumers to the immutable semantic action tag or full commit
instead of the moving `v1` tag. A trusted-branch nightly or release gate uses
the same job under `schedule`, `push` to a protected branch, or a protected
environment; increase VUsers/duration only within the controller project policy.

### GHES/private-controller gate with scoped API key

Use this only when the GHES Actions OIDC issuer is not usable. The runner group
must reject public/fork jobs and have network reachability to the private
controller and the approved target allowlist. Mirror the exact action commits
before using this example.

```yaml
name: APEX Load GHES gate
on:
  workflow_dispatch:

permissions:
  contents: read

jobs:
  performance:
    runs-on: [self-hosted, linux, x64, apexload-private]
    timeout-minutes: 40
    steps:
      - uses: actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683 # mirrored v4.2.2
        with:
          persist-credentials: false
      - id: apexload
        uses: JasonCzech/Project_Stormrunner/.github/actions/apexload-run@v1.0.0
        env:
          APEXLOAD_API_KEY: ${{ secrets.APEXLOAD_CI_RUNNER_API_KEY }}
        with:
          auth-mode: api-key
          controller-url: https://apexload.corp.example
          project-id: checkout-service
          script-id: trusted-nightly-script
          apexctl-version: v1.0.0
          release-base-url: https://artifacts.corp.example/apexload/releases/download
          release-public-key: .github/apexload-release-signing-key.pem
      - if: always()
        uses: actions/upload-artifact@c6a3b2bd78b3985e4b2f15397fec357f0fd808de # mirrored v3.2.2-node20 for GHES
        with:
          name: apexload-evidence
          path: |
            ${{ steps.apexload.outputs.json-path }}
            ${{ steps.apexload.outputs.junit-path }}
          if-no-files-found: error
          include-hidden-files: true
```

The canonical action provisions Node 22 for signed-manifest validation. An
air-gapped GHES installation must mirror the pinned `actions/setup-node` action
and Node toolcache, as well as the APEX release assets. The public key file in
the example is reviewed source protected by CODEOWNERS; it is not a secret.

### Explicit runner-local smoke

Local mode is limited to a small deterministic smoke and never substitutes for
the controller-backed distributed gate:

```yaml
- id: smoke
  uses: JasonCzech/Project_Stormrunner/.github/actions/apexload-run@v1
  with:
    execution-mode: local
    script-path: performance/smoke.json
    vusers: "1"
    duration: 15s
    publish-check: "false"
    require-sla: "true"
```

### API-key rotation and air-gapped mirroring

Create one service account per project with only the `ci_runner` project role,
exact scopes `tests:run` and `results:read`, and a future expiry. The one-time
key is stored only in the protected environment secret. Rotate it through
`POST /api/v1/auth/service-accounts/{id}/rotate`, update the environment secret
immediately, run a trusted smoke, and confirm the old key is rejected. See
[AUTH_RBAC_OPERATIONS_RUNBOOK.md](AUTH_RBAC_OPERATIONS_RUNBOOK.md#service-accounts)
for lifecycle and audit checks.

For an air-gapped consumer, mirror the semantic Release without changing any
asset bytes or filenames, verify `SHA256SUMS.sig` at ingestion, mirror the APEX
action and every nested pinned action commit, and configure `release-base-url`
plus the matching public key as shown above. The consumer needs no APEX source
or Go toolchain. See
[GITHUB_ACTIONS_RELEASE_AND_DEPLOYMENT.md](GITHUB_ACTIONS_RELEASE_AND_DEPLOYMENT.md#github-enterprise-server-compatibility)
for runner/tool mirrors and the qualification gate.

## Deployment pipelines

These active workflows deploy released controller artifacts (not arbitrary
consumer checkouts and not rebuilt application artifacts).

| System | File | Target | Notes |
| --- | --- | --- | --- |
| GitHub Actions | [`.github/workflows/release.yml`](../.github/workflows/release.yml) | Release assets and registries | Signed tags; build once; SBOM, scan, sign, draft-then-publish. |
| GitHub Actions | [`.github/workflows/deploy-aks.yml`](../.github/workflows/deploy-aks.yml) | AKS | Signed manifest and image-digest Helm deployment with rollback. |
| GitHub Actions | [`.github/workflows/deploy-appservice.yml`](../.github/workflows/deploy-appservice.yml) | Azure App Service | Verified release zip, staging validation, slot swap, and swap-back. |
| GitHub Actions | [`.github/workflows/publish-onprem.yml`](../.github/workflows/publish-onprem.yml) | On-prem registry | Mirror, scan, sign, then expose immutable release tags. |
| GitHub Actions | [`.github/workflows/infra-azure.yml`](../.github/workflows/infra-azure.yml) | Azure infrastructure | Default-branch `repository_dispatch`; encrypted saved-plan handoff with independent apply approval. |

### App Service deployment notes

The controller binary is pure Go (`CGO_ENABLED=0`), so the CI pipeline cross-compiles a static `linux/amd64` binary and bundles it with a zero-dependency Node.js launcher (`deployments/azure/appservice/launcher.js`). The launcher:

- Maps App Service's `$PORT` → `APEXLOAD_ADDR` so the Go process binds the port directly (no Node reverse proxy).
- Materializes Key Vault PEM references (`APEXLOAD_GRPC_TLS_*_PEM` app settings) into temp files before spawning the binary, since the gRPC transport accepts file paths only.
- Forwards `SIGTERM`/`SIGINT` to the controller for graceful shutdown.

Required app settings beyond the standard controller env vars:

| App setting | Value |
| --- | --- |
| `APEXLOAD_STORE` | `postgres` (SQLite file locking is unsafe on the CIFS-backed `/home` share) |
| `APEXLOAD_DATABASE_URL` | Key Vault reference → Terraform output `apexload_database_url` |
| `APEXLOAD_PUBLIC_URL` | Canonical `https://<app>.azurewebsites.net` or custom-domain URL; required for stable CI OIDC discovery and Check links. Mark it as a deployment-slot setting, with distinct staging and production values matching the protected workflow URLs. |
| `APEXLOAD_GENERATORS` | Static comma-separated `ip:9090` list of internal-LB generator endpoints |
| `APEXLOAD_GRPC_AUTH_TOKEN` | Key Vault reference (same value as the AKS secret) |
| `SCM_DO_BUILD_DURING_DEPLOYMENT` | `false` (prebuilt zip; Oryx must not re-run npm/go) |

WebSockets must be enabled on the App Service plan (dashboard uses `WS /api/v1/ws` and per-test streams). Always On must be enabled so the 30-second schedule poller keeps running. Pin to one instance unless the Redis HA path (`APEXLOAD_REDIS_URL`) is wired up.

The GitHub action never builds from the consumer checkout. It uses an explicitly
trusted `apexctl-path`, or downloads the exact action-pinned release, verifies
the signed checksum manifest, and verifies the binary and release-manifest
checksums. GHES and air-gapped installations can provide `release-base-url` and
`release-public-key` for an internal mirror.

## Publishing the JUnit report

Always publish the report under `if: always()` so an SLA breach is
visible in the test UI rather than buried in the build log:

- **GitHub Actions:** `dorny/test-reporter` (reporter `java-junit`) or upload as
  an artifact.

## Migration history

GitHub Actions is the sole CI/CD system for APEX Load; Jenkins, Azure Pipelines
templates, and dormant copy-to-activate deployment templates were removed when
the active workflows replaced them. The cutover is complete only after observed
parity, not merely after workflow files merge:

1. Establish a clean `main` baseline and run the authoritative `CI` workflow on
   five representative documentation, Go, dashboard, deployment, and protocol
   changes. Resolve flakes before enforcement.
2. Configure the `main` ruleset and merge queue to require only `CI / Required`
   (a stable aggregate that avoids branch-protection drift). Keep the default
   `GITHUB_TOKEN` read-only.
3. Publish a signed `v1.0.0-rc.1`, then invoke the canonical
   `owner/repository/.github/actions/apexload-run@v1.0.0-rc.1` from a separate
   fixture repository with no APEX source and no Go toolchain.
4. Complete GitHub.com and GHES staging release, controller-backed execution,
   deployment, evidence, and rollback drills
   ([GITHUB_ACTIONS_RELEASE_AND_DEPLOYMENT.md](GITHUB_ACTIONS_RELEASE_AND_DEPLOYMENT.md)).
5. Publish the signed stable release, move the protected signed `v1` action tag,
   and activate production environment reviewers and tag restrictions.
6. Confirm no workflow, documentation example, or deployment path invokes
   Jenkins or Azure Pipelines.

Trust rules that survive the cutover: fork pull requests are untrusted and run
only `fork-static` (patch-whitespace and JSON/DSL syntax checks using
runner-provided `git` and `jq`; no checked-out scripts, binaries, tests, or
source), all full CI lanes must be skipped, and `CI / Required` verifies that
exact success/skipped contract. Same-repository pull requests, including
Dependabot, get full CI. GitHub.com uses only the hard-coded ephemeral
`ubuntu-24.04` runner; GHES requires a dedicated ephemeral pool labelled
`apexload-ci-unprivileged` and never falls back to a trusted runner. Trusted
self-hosted runner groups are restricted at the organization/enterprise layer to
selected workflows pinned to protected refs; controller-backed performance gates
need a trusted same-repository ref or a protected manual workflow with
independent approval; `pull_request_target` is prohibited for any workflow that
checks out or executes repository content. Required reviewers, prevent-self-review,
protected-tag deployment restrictions, OIDC federated credentials, GitHub App
permissions, rulesets, and KMS/HSM identities live outside Git and must be
configured and audited by administrators before production activation.
