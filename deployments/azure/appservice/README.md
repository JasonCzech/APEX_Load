# Azure App Service Controller Launcher

Thin zero-dependency Node.js wrapper (`launcher.js`) for running the prebuilt `linux/amd64` APEX Load controller binary on Azure App Service for Linux. For full Azure infrastructure provisioning and CI release promotion, see [AKS & Azure Terraform](../../k8s/aks/README.md#azure-terraform) and [GitHub Actions release and deployment](../../../docs/GITHUB_ACTIONS_RELEASE_AND_DEPLOYMENT.md).

## Runtime and Binary Contract

- **Node.js runtime**: requires `Node.js >= 20` (`package.json` `"engines": { "node": ">=20" }`), invoked via `npm start` (`node launcher.js`) with zero npm runtime dependencies.
- **Controller binary resolution**: resolves `APEXLOAD_LAUNCHER_BINARY` when set, defaulting to `./bin/controller` (`deployments/azure/appservice/bin/controller`).
- **Linux architecture verification**: before spawning on Linux, `launcher.js` inspects the first 20 bytes of the binary and verifies either a `#!` script header (used by `launcher.test.js`) or a `linux/amd64` ELF executable (`\x7fELF`, `e_machine == 0x003e` `EM_X86_64`), failing immediately with an explicit cross-compile command if a wrong-architecture binary was packaged:

  ```bash
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -o deployments/azure/appservice/bin/controller ./cmd/controller
  ```

## Port Binding, Metrics, and TLS Materialization

- **Single-port App Service binding**: App Service exposes one HTTP port per container via `$PORT` (reflecting `WEBSITES_PORT` when customized). `launcher.js` maps `PORT` to `APEXLOAD_ADDR=:<PORT>` so the Go controller owns the listening socket directly without a Node.js reverse proxy, and deletes `APEXLOAD_METRICS_ADDR` so the controller does not attempt to bind a second metrics port.
- **Inline gRPC TLS PEM materialization**: because App Service delivers Key Vault secrets as environment variables while the controller's gRPC transport reads certificate paths, `launcher.js` writes any non-empty `APEXLOAD_GRPC_TLS_CA_PEM`, `APEXLOAD_GRPC_TLS_CERT_PEM`, and `APEXLOAD_GRPC_TLS_KEY_PEM` values to `0600` temporary files under `os.tmpdir()`, sets `APEXLOAD_GRPC_TLS_CA`, `APEXLOAD_GRPC_TLS_CERT`, and `APEXLOAD_GRPC_TLS_KEY`, and deletes the `*_PEM` variables from the child environment before `spawn`.
- **Signal forwarding and shutdown budget**: `launcher.js` forwards `SIGTERM` and `SIGINT` directly to the controller process (`signal.NotifyContext` graceful HTTP/scheduler drain) and exits with the child's status code (or `128 + signal` on signal termination). Azure App Service for Linux sends `SIGTERM` on stop/slot-swap and escalates to `SIGKILL` after the platform container stop window (~25–30 seconds by default unless `WEBSITES_CONTAINER_STOP_TIME_LIMIT` is raised), so active performance runs are gated before slot swaps by `.github/workflows/deploy-appservice.yml`.

## Persistence, Storage, and Generator Topology

App Service mounts `/home` over CIFS/SMB network storage, where SQLite advisory locking and cross-instance POSIX `flock` archive staging are unsafe. Production App Service deployments must use external Azure data services:

| Setting | Required posture |
| --- | --- |
| `APEXLOAD_STORE` | `postgres` (with `APEXLOAD_DATABASE_URL` supplied via Key Vault reference). Never run SQLite on `/home`. |
| `APEXLOAD_ARCHIVE_BACKEND` | `azure_blob` (`APEXLOAD_ARCHIVE_CONTAINER`, `AZURE_STORAGE_ACCOUNT` with system-assigned managed identity granted `Storage Blob Data Contributor`). Local staging/export scratch uses ephemeral container storage (or `/home/data/apexload` strictly for single-instance non-locked export scratch, never shared HA staging). |
| `APEXLOAD_AZURE_KEY_VAULT_URL` | Key Vault URL with `APEXLOAD_AZURE_KEY_VAULT_AUTH=azure_identity` and `APEXLOAD_AZURE_KEY_VAULT_WRITE_ENABLED=true` (`Key Vault Secrets Officer` on the App Service managed identity). |
| `APEXLOAD_GENERATORS` | Static comma-separated `host:9090` internal-LB or private-VNet load generator targets reachable through App Service VNet integration (plus `APEXLOAD_GRPC_AUTH_TOKEN` and mTLS `APEXLOAD_GRPC_TLS_*_PEM`), or PaaS reverse-connect load generators. |
| `APEXLOAD_PUBLIC_URL` | Canonical external `https://<host>` URL marked as a **deployment-slot setting** (distinct values on staging and production slots) for stable GitHub Actions OIDC audience discovery and Check links. |
| `SCM_DO_BUILD_DURING_DEPLOYMENT` | `false` — deploy the signed release zip as-is without Oryx rebuilds. |
| Platform toggles | Enable **WebSockets** (`WS /api/v1/ws`) and **Always On** (keeps the 30-second scenario scheduler active). Keep instance count at `1` unless `APEXLOAD_CACHE_BACKEND=redis` (`APEXLOAD_REDIS_URL`) and `APEXLOAD_CONTROLLER_HA=true` are configured. |

## Local Verification

```bash
cd deployments/azure/appservice
npm test
npm run lint
```
