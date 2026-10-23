# CI/CD Integration

APEX Load is designed to run as an automated performance gate in delivery
pipelines. `apexctl` exits non-zero on SLA breach and emits machine-readable
output (JSON + JUnit XML), so any CI system can fail a build on a regression and
surface the result in its native test UI.

## Building blocks

### Exit codes

| Command | Exit 0 | Exit 1 | Exit 2+ |
| --- | --- | --- | --- |
| `apexctl ci run` | passed | SLA breach, a run the project's validity gate fails (`run_invalid`), or a `fail_ci` telemetry acceptance failure (`telemetry_failed`) | configuration (including an unmapped inconclusive telemetry verdict, `telemetry_inconclusive`), execution, timeout, or cancellation (see JSON `outcome`) |
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

## Feature acceptance evidence

Support claims rest on append-only acceptance records: which commit and binary ran which suite for a requirement and feature, optionally per profile, provider and platform, with SHA-256 hashes of the sanitized evidence. `apexctl qa record-acceptance` hashes the binary and every `--artifact` locally and posts only the hashes; a `passed` record needs at least one artifact. Recording needs a system-administrator credential (a `ci_runner` key receives 403); reading records and status needs only a global viewer. Records are ordered by a database-assigned sequence, never by a controller clock. Only the `/api/v1/system/acceptance-records` and `/api/v1/system/acceptance-status` routes and `apexctl qa acceptance` read these records today; the capability ledger and the dashboard do not consult them yet.

```bash
apexctl qa record-acceptance --api "$APEX_URL" --requirement BE-4 --feature browser-har --platform linux-amd64 --commit "$GITHUB_SHA" --binary ./apexload --suite browser-acceptance --result passed --artifact evidence/browser-matrix.json
```

A subject is supported when its latest record is `passed`. A newer `failed`, `pending` or `revoked` record withdraws it, and one recorded without a profile, provider or platform withdraws every narrower subject of that requirement and feature whose pass is older. A later broad pass does not restore a withdrawn subject: only a new pass for that subject re-qualifies it. After a broad revoke, record a new pass for each narrower subject, then a broad pass so the broad subject itself is supported again, or gate on each subject with `--platform`. `apexctl qa acceptance` reads the status; with `--commit` or `--binary` a subject is supported only by a pass recorded for exactly that build, and `--require-supported` exits 1 unless at least one subject matches and every match is supported, so it can gate a release:

```bash
apexctl qa acceptance --api "$APEX_URL" --requirement BE-4 --feature browser-har --commit "$GITHUB_SHA" --require-supported
```

Each command refuses flags that do not apply to it. Deleting evidence files does not withdraw a claim; record a `revoked` result instead. The status endpoint returns at most 5,000 subjects and says when it truncated.

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

## GitHub Enterprise Integration

APEX Load treats GitHub Enterprise Server (GHES) as a first-class source-control
and CI surface for JSON DSL scripts. The preferred automation path is a
GitHub App installation token, per-user OAuth remains available for human browse
and push actions, and a PAT can be configured only as a headless fallback.

### Authentication Model

- **GitHub App automation:** configure `app_id`, optional app slug, and a private
  key env var or file. Project policies bind to an installation id. The
  controller mints cached one-hour installation tokens and scopes them to the
  target repository when the API supports it.
- **Per-user OAuth:** users connect from Settings for interactive repo browsing,
  single-file import, and human-attributed pushes. Stored access and refresh
  tokens are encrypted with `APEXLOAD_GITHUB_TOKEN_KEY`; expiring OAuth tokens
  are refreshed and rotated when possible.
- **PAT fallback:** configure only an env var name. The token value is read from
  the controller environment and is never persisted. Use this for headless labs
  where a GitHub App cannot be installed yet.

### Global Settings

Dashboard -> Settings -> Integrations -> GitHub Script Sync:

| Field | Example | Notes |
| --- | --- | --- |
| Enable GitHub integration | on | Master switch. |
| OAuth Client ID | `Iv1...` | Required only for per-user OAuth. |
| Client Secret Env Var | `APEXLOAD_GITHUB_CLIENT_SECRET` | Env var name only. |
| GitHub App ID | `123456` | Required for App automation and Checks. |
| App Private Key Env/File | `APEXLOAD_GITHUB_APP_PRIVATE_KEY` | Env value may contain escaped `\n`. |
| Webhook Secret Env Var | `APEXLOAD_GITHUB_WEBHOOK_SECRET` | Used for `X-Hub-Signature-256` validation. |
| PAT Fallback Env Var | `APEXLOAD_GITHUB_PAT` | Optional fallback only. |
| Automation Mode | `app_oauth_pat` | Resolution order: App -> OAuth -> PAT. |
| Publish GitHub Checks | on | Requires GitHub App permissions. |
| API/Web Base URL | `https://ghe/api/v3`, `https://ghe` | github.com defaults also work. |
| CA / TLS | `/etc/apexload/ghe-ca.pem` | Internal PKI support; skip verify is lab-only. |

Secrets are not stored in settings; only env var names or file references are.
The configured GHES host is added to the outbound allow-list without disabling
the broader SSRF guard.

### Project Policy

Settings -> Projects & Compliance -> GitHub Project Policy defines the server
authority for each project:

- Allowed repositories and branches, including glob patterns such as
  `perf/*` or `release/*`.
- Path template and default sync folder.
- PR-required vs direct-commit policy.
- Import, push, bulk sync, webhook auto-sync, and Checks enablement.
- GitHub App installation id for project automation.

All GitHub import, push, bulk sync, webhook, and check-run routes enforce this
policy even if the underlying GitHub credential can access more repositories.

### Script Sync

- Single-script push stores both `remote_blob_sha` and `remote_commit_sha`.
  `last_commit_sha` remains for compatibility and represents the remote blob
  SHA used for Contents API optimistic concurrency.
- Single-script import returns source metadata through the standard import-review
  flow.
- Bulk import scans a configured repo path for `.json` DSL files, imports valid
  scripts, records job progress, and links imported scripts to their GitHub file.
- Bulk push writes selected or project scripts through the same policy checks.

Dashboard surfaces include installation health, project policy editing, row-level
drift badges, a bulk import action, per-script push/import modals, webhook logs,
and check-run status links.

### Webhooks

Configure the GHES webhook target:

`POST https://<apexload-host>/api/v1/integrations/github/webhook`

The controller validates `X-Hub-Signature-256`, deduplicates
`X-GitHub-Delivery`, records each delivery, and processes push events to update
linked script commit state. Auto-sync can be enabled by project policy; webhook
processing is designed to create reviewable state rather than silently overwrite
local scripts.

### CI Checks

GitHub Checks are controller-authoritative: a Check is created when the bounded
CI run is accepted and completed from controller state even if the Actions job
disconnects. The caller grants read access and OIDC only; the controller's
GitHub App owns `Checks: write`.

```yaml
permissions:
  contents: read
  id-token: write

jobs:
  performance:
    # Never execute contributor-controlled load scripts from a fork.
    if: github.event_name != 'pull_request' || github.event.pull_request.head.repo.id == github.event.repository.id
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@<reviewed-commit-sha>
      - id: apexload
        uses: JasonCzech/Project_Stormrunner/.github/actions/apexload-run@v1
        with:
          controller-url: https://apexload.example.com
          project-id: project-a
          script-path: apexload/scripts/order-flow.json
          p95-latency-ms: "800"
          max-error-pct: "2"
      - name: Retain evidence on GitHub.com
        if: always()
        uses: actions/upload-artifact@<reviewed-v4-commit-sha>
        with:
          name: apexload-evidence
          path: |
            ${{ steps.apexload.outputs.json-path }}
            ${{ steps.apexload.outputs.junit-path }}
          include-hidden-files: true
```

The action downloads a released `apexctl`; the consumer repository does not
need the APEX source tree or a Go toolchain. An operator must commit the release
public key with the action revision or supply `release-public-key`. Missing
signature trust fails closed. GHES callers mirror the action dependencies and
release assets, select the artifact action supported by their server release,
and use a self-hosted runner when the controller is private.

The controller trust policy binds issuer, audience, repository identity,
workflow ref, ref/environment, project, target hosts, protocols, VUsers, and
duration. Pull-request source repository identity is sent separately from the
base repository and is rejected when it is not trusted. Do not use
`pull_request_target` to check out or execute contributor-controlled content.

Target-bearing `{{secret.*}}` references are resolved by the controller before
a CI run is accepted, and the resolved host must match the project allowlist.
Keep the same project-scoped binding available to the controller and every load
generator; the resolved value is used only for preflight/runtime enforcement
and is not written into the stored CI metadata or returned in API responses.

#### Run validity gate

Every completed CI run reports the run's validity verdict (see
[Run validity](ROOT_CAUSE_ANALYSIS.md#run-validity)) as `validity` in the run
response and in `apexctl ci run`'s JSON result, and as `Run validity` in the
Check and the job summary. It is advisory unless the project opts in with the
Actions policy's `validity_gate`:

| `validity_gate` | Fails the run when validity is |
| --- | --- |
| empty (default) | never; validity is advisory |
| `invalid` | `invalid` |
| `suspect` | `invalid` or `suspect` |

A failed gate makes the outcome `run_invalid` (exit `1`, Check `failure`) even
when the SLA was breached or required monitoring was incomplete, because an
invalid measurement cannot judge the system under test. `validity.enforced` is
then `true` and `error` names the verdict and its rules. An `unknown` verdict,
such as a run with no archived intervals, never fails the gate, and a failed or
cancelled run keeps its own outcome. If the controller cannot build the run's
report to judge it, a gated run that would otherwise pass is an
`execution_error` (exit `3`) rather than a pass.

System administrators set the gate with the project policy update
(`PUT /api/v1/projects/{id}/github/policy`). Changing it is a new policy
revision: each CI run keeps the gate it was accepted under, so an idempotent
retry is unaffected, but as with any Actions policy change, CI runs still
queued for capacity fail at promotion. Use an `apexctl` that knows
`run_invalid` before enabling the gate; an older one rejects the outcome as an
invalid controller response and exits `3`.

#### Root-cause summary

For a project that presents root cause (see
[Root-cause analysis service](ROOT_CAUSE_ANALYSIS.md#root-cause-analysis-service)),
a completed CI run's response and `apexctl ci run`'s JSON result carry
`root_cause`: the `state` of the run's newest analysis (`none`, `queued`,
`running`, `completed`, `failed` or `cancelled`), and once one completed its
`outcome`, `top_hypothesis`, `confidence`, `analysis_id` and a `report_url`
that opens it; `auto_analyze` says the project analyzes every archived run, so
a run without one yet reads `analysis pending`. It never changes the
outcome or the exit code.

The Check and the job summary add a `Root cause` row (the leading hypothesis
with its confidence, `no cause supported`, `insufficient evidence`,
`analysis pending`, `not analyzed`, or the analysis state), and the action's `root-cause` output holds the same line,
beside a `validity` output with the verdict. An automatic analysis is queued
when the run's archive is finalized and usually completes after the Check
has; a completed Check is not edited afterwards, so it shows the state it
saw. `apexctl ci run --wait-analysis 5m` (the action's `wait-analysis` input;
at most `10m`, default no wait) polls after the run ends until the analysis
completes or the wait passes, so the job summary and outputs carry the
verdict; the wait never fails the job.

#### Telemetry acceptance gate

A run that pinned a project `telemetry_acceptance_policy` (see [Telemetry acceptance verdicts](guides/TELEMETRY_POLICY_RECEIPTS.md#telemetry-acceptance-verdicts)) has a `telemetry_verdict` beside the application outcome. It appears in the run response, in `apexctl ci run`'s JSON result and in the `telemetry-verdict` output. The Check lists it in its own section, the job summary shows it in its own rows, and the JUnit report has a separate `telemetry_acceptance` test case. The field holds the verdict (`pass`, `fail` or `inconclusive`), the policy revision, `mode`, every criterion with its evidence, and `enforced`.

Only a `fail_ci` policy can change the outcome, and only for a run that would otherwise pass:

| Verdict under `fail_ci` | Outcome | Exit |
| --- | --- | --- |
| `pass` | `passed` | `0` |
| `fail` | `telemetry_failed` | `1` |
| `inconclusive`, `--telemetry-inconclusive=pass` | `passed` | `0` |
| `inconclusive`, `--telemetry-inconclusive=fail` | `telemetry_failed` | `1` |
| `inconclusive`, no mapping | `telemetry_inconclusive` | `2` |

There is no default mapping. The controller refuses to create a CI run (`400`) without `telemetry_inconclusive` (`apexctl --telemetry-inconclusive`, action input `telemetry-inconclusive`) when the policy that will judge it is `fail_ci`. That is the run's chosen policy (`telemetry_acceptance_policy_id`, `apexctl --telemetry-policy`, action input `telemetry-policy`) when it names one, otherwise the project's default.

A CI run may choose only where the project's GitHub Actions policy sets `allow_telemetry_policy_selection`; otherwise the choice is refused (`403`). Naming `project_default` is the same as naming none. A chosen policy the project lacks is refused (`400`). The choice is part of the request a retry under the same `Idempotency-Key` must repeat, and of `apexctl`'s derived key. An exact retry of an accepted run returns that run even if its choice was since disallowed or deleted. The run's response echoes the choice. When the create response does not, because an older controller would judge the run by the project's default, `apexctl` stops the run and exits with a configuration error. If the policy becomes `fail_ci` between acceptance and Start, an inconclusive verdict is `telemetry_inconclusive`, never a pass.

A CI run can also set its live telemetry budgets from one of the project's telemetry budget policies (`telemetry_budget_policy_id`, `apexctl --telemetry-budget-policy`, action input `telemetry-budget-policy`) instead of the system threshold settings. Because the budgets decide which telemetry time is breached, the choice needs the same `allow_telemetry_policy_selection` permission (otherwise `403`) and follows the same rules: `400` for a policy the project lacks, `409` for an unusable one, part of the retry identity, echoed, and a configuration error from `apexctl` when it is not echoed. The GitHub Check and the `apexctl` job summary say when a run chose its budgets. See [Choosing a budget policy](guides/TELEMETRY_POLICY_RECEIPTS.md#choosing-a-budget-policy).

An advisory verdict never changes the outcome or the exit code. Neither does a verdict on a run that already failed for another reason, or a provisional verdict over live evidence. In JUnit, an advisory verdict that did not pass is a skipped case with its reasons, so a test-report gate does not fail on it. Use an `apexctl` that knows the telemetry outcomes before setting a policy to `fail_ci`; an older one rejects them as an invalid controller response and exits `3`.

Controller-backed CI currently rejects the `browser` protocol. Browser routing
hooks cannot enforce the target-host boundary across every Chromium redirect
and worker transport, so browser load remains disabled until egress is enforced
below the browser process. Use HTTP/protocol scripts for authoritative gates;
runner-local browser execution is suitable only for explicitly trusted smoke
tests on a network-isolated runner.

### CLI And MCP

`apexctl github` provides headless access to controller-backed GHES workflows:

```bash
apexctl github diagnostics --api http://localhost:8080
apexctl github installations --api http://localhost:8080 --refresh
apexctl github policy --api http://localhost:8080 --project project-a
apexctl github sync --api http://localhost:8080 --project project-a --kind import
apexctl github status --api http://localhost:8080 --script script-a
apexctl github check-run --api http://localhost:8080 --test test-a --commit-sha "$GITHUB_SHA"
```

The MCP endpoint catalog exposes the same diagnostics, policy, sync-job,
import, push, status, webhook-delivery, check-link, and check-run publish routes
with the existing confirmation guard for mutating tools.

### Operations

Readiness reports GHES reachability, auth mode configuration, token encryption,
webhook secret presence, check publishing prerequisites, and cached installation
state. Prometheus includes GitHub integration gauges and sync-job counters.
Audit events are recorded for project policy changes, imports, pushes, sync
jobs, webhook processing, and check-run publishing.
