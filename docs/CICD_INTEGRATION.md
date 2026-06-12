# CI/CD Integration

APEX Load is designed to run as an automated performance gate in delivery
pipelines. `apexctl` exits non-zero on SLA breach and emits machine-readable
output (JSON + JUnit XML), so any CI system can fail a build on a regression and
surface the result in its native test UI.

## Building blocks

### Exit codes

| Command | Exit 0 | Exit 1 | Exit 2 |
| --- | --- | --- | --- |
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
  Ant/Surefire dialect consumed by GitHub Actions, Azure DevOps
  `PublishTestResults`, and the Jenkins `junit` step.

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

### Pattern B — start-then-wait (controller-managed)

For larger, distributed runs, start the test against a controller and gate on
the outcome with `wait`. Useful when the run should execute on the generator
fleet rather than the CI agent.

```
apexctl run  --api https://apexload.internal --script order-flow.json --format json   # starts + waits
# or, to wait on an already-created test from another step:
apexctl wait --api https://apexload.internal --test "$TEST_ID" --junit results.xml
```

`apexctl status` is the cheap point-in-time check if you only need a pass/fail
snapshot without blocking.

## Provided templates

| System | File | Notes |
| --- | --- | --- |
| GitHub Actions | [`ci/github/action.yml`](../ci/github/action.yml) | Reusable composite action; gates + uploads the report. |
| GitHub Actions | [`ci/github/perf-gate-workflow.yml`](../ci/github/perf-gate-workflow.yml) | Example PR workflow that publishes results to the Checks tab. |
| Azure DevOps | [`ci/azure-devops/apexload-perf-gate.yml`](../ci/azure-devops/apexload-perf-gate.yml) | Step template; gates + `PublishTestResults`. |
| Azure DevOps | [`../azure-pipelines-task.yml`](../azure-pipelines-task.yml) | Legacy variant that exports the JSON result as a pipeline variable. |
| Jenkins | [`ci/jenkins/Jenkinsfile.example`](../ci/jenkins/Jenkinsfile.example) | Declarative pipeline; gates + `junit` publishing. |

## Deployment pipelines

These pipelines build and deploy the controller itself (not `apexctl` perf gates).

| System | File | Target | Notes |
| --- | --- | --- | --- |
| GitHub Actions | [`deployments/ci/github-actions-aks.yml`](../deployments/ci/github-actions-aks.yml) | AKS | OIDC auth; `az acr build` + `kubectl apply -k`. Copy to `.github/workflows/` to activate. |
| Azure Pipelines | [`deployments/ci/azure-pipelines-aks.yml`](../deployments/ci/azure-pipelines-aks.yml) | AKS | Mirrors the GitHub Actions AKS pipeline. |
| GitHub Actions | [`deployments/ci/github-actions-appservice.yml`](../deployments/ci/github-actions-appservice.yml) | Azure App Service | Builds dashboard → linux/amd64 controller binary → runs launcher tests → zip-deploys the Node.js launcher package. Needs repo vars `APPSERVICE_NAME` + `APPSERVICE_RESOURCE_GROUP`. |
| Azure Pipelines | [`deployments/ci/azure-pipelines-appservice.yml`](../deployments/ci/azure-pipelines-appservice.yml) | Azure App Service | Mirrors the GitHub Actions App Service pipeline. |
| GitHub Actions | [`deployments/ci/github-actions-onprem-registry.yml`](../deployments/ci/github-actions-onprem-registry.yml) | On-prem registry | SBOM, scan, and sign flow for air-gapped registries. |

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
| `APEXLOAD_GENERATORS` | Static comma-separated `ip:9090` list of internal-LB generator endpoints |
| `APEXLOAD_GRPC_AUTH_TOKEN` | Key Vault reference (same value as the AKS secret) |
| `SCM_DO_BUILD_DURING_DEPLOYMENT` | `false` (prebuilt zip; Oryx must not re-run npm/go) |

WebSockets must be enabled on the App Service plan (dashboard uses `WS /api/v1/ws` and per-test streams). Always On must be enabled so the 30-second schedule poller keeps running. Pin to one instance unless the Redis HA path (`APEXLOAD_REDIS_URL`) is wired up.

All templates default to building `apexctl` from source (`go run ./cmd/apexctl`).
For consumers outside this repo, install a released `apexctl` binary and point
the template at it (the GitHub action accepts `apexctl-bin`).

## Publishing the JUnit report

Always publish the report on `always()` / `post { always }` so an SLA breach is
visible in the test UI rather than buried in the build log:

- **GitHub Actions:** `dorny/test-reporter` (reporter `java-junit`) or upload as
  an artifact.
- **Azure DevOps:** `PublishTestResults@2` with `testResultsFormat: JUnit`.
- **Jenkins:** the `junit` post step.
