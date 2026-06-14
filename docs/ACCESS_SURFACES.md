# APEX Load Access Surfaces

APEX Load is reachable through three supported surfaces. They share the same API and asset model, but they are intentionally labeled so users can tell whether they are working against a hosted controller, a local desktop workspace, or a headless CLI run.

## 1. Hosted Browser

The Kubernetes controller serves the React dashboard from the same origin as the REST API. This is the canonical zero-install experience: users open the controller URL in a browser and can author, schedule, run, monitor, and review tests without installing desktop software.

Hosted deployments opt into a wide HTTP bind explicitly through `APEXLOAD_ADDR` in the container and Kubernetes manifests. Browser traffic remains same-origin, so the dashboard does not need client-side controller code or cross-origin API switching.

## 2. Desktop Local Workspace

The Wails desktop app is optional. It runs an embedded local controller on loopback, uses local SQLite/keyring-backed storage, and supports local authoring and local execution.

Remote K8s connectivity is handled through Remote Workspaces. The desktop SPA stays same-origin against its embedded controller; it does not switch the whole application origin to the hosted controller. The local controller uses configured Remote Workspace credentials to push and pull scripts, scenarios, results, and dependencies, and to proxy remote run creation and controls.

Remote run v1 is intentionally poll-based:

- `POST /api/v1/remotes/{id}/runs` pushes required local assets, translates local IDs to remote IDs, and creates the remote test.
- `GET /api/v1/remotes/{id}/runs/{remote_test_id}` polls the remote test status.
- `POST /api/v1/remotes/{id}/runs/{remote_test_id}/start|stop|abort|pause|resume` forwards lifecycle controls.

Run links are persisted under `remote.run_links.v1`, separate from library sync links (`library.sync_links.v1`). Full realtime viewing should use the hosted run page link; a server-side WebSocket relay is deferred.

### Desktop Distribution Trust

The local development Wails build is not a production installer. macOS desktop
packages must be signed with a Developer ID Application certificate and
notarized before distribution to end users, otherwise Gatekeeper may block the
downloaded `.app` with an Apple verification warning. Build release packages
with:

```bash
APEXLOAD_MACOS_CODESIGN_IDENTITY='Developer ID Application: Example Corp (TEAMID)' \
APEXLOAD_MACOS_NOTARIZE=1 \
APEXLOAD_MACOS_NOTARY_PROFILE=apexload-notary \
make desktop-release
```

Windows desktop distribution should likewise use an Authenticode-signed
installer or executable from CI. Raw local debug archives are acceptable for
developer machines only, not for broad user distribution.

## 3. CLI

`apexctl` remains the CI/headless surface. It can run scripts locally or target a controller API, and this access-surface work does not change its contract.

## Binding Defaults

Standalone local/dev controllers bind to `127.0.0.1:8080` when `APEXLOAD_ADDR` is unset. Production environments (`APEXLOAD_ENV=production` or `prod`) default to `:8080`. Any explicit `APEXLOAD_ADDR` is honored exactly.

Container and server deployment contexts should set `APEXLOAD_ADDR` explicitly. The checked-in Docker image, compose file, and Kubernetes manifests do this so Services and port mappings remain reachable.

The deployment identity banner reads `/api/v1/system/version` and shows the deployment kind, bind scope, and an explicit warning only when the server is wide-bound while auth bypass is active.

## Desktop Promotion Governance

`SystemSettings.client_app.availability` controls whether the hosted browser UI promotes the optional desktop client. It is a system-wide admin setting with values:

```json
{
  "availability": "enabled"
}
```

This is promotion governance, not API authorization. Disabling it hides hosted UI promotion, docs links, and future download CTAs. It does not block already-installed desktop clients, CLI users, or any other authorized caller from using REST APIs. API access remains governed by auth, RBAC, service-account scopes, and project permissions.

## Auth Direction

Remote desktop auth uses existing Remote Workspace bearer/API-key credential storage. Desktop OIDC SSO is deferred; the expected future shape is system-browser PKCE with token storage in the OS keyring, reusing the controller's existing OIDC configuration where possible.
