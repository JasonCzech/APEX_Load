# APEX Load Desktop Workspace

Wails v2 native desktop shell (`package main`) embedding an in-process loopback controller (`pkg/api`) and the compiled React dashboard (`pkg/api/static`) for macOS, Windows, and Linux.

## Architecture & Security Model

- **Embedded loopback controller** (`controller_lifecycle.go`, `app.go`): Starts an isolated local controller bound to `127.0.0.1` on an ephemeral port with local SQLite metadata, local archive storage, and OS-keyring/private-ACL secret protection (`appdatapath_*.go`, `privatefiles*.go`).
- **Per-launch capability guard** (`securityheaders.go`, `apidownload.go`): Every `/api/v1/*` and WebSocket request to the loopback controller must carry the per-launch `X-APEXLoad-Desktop-Token` header (or `desktop_token` WebSocket query token) so other local processes cannot call the embedded API.
- **Native OS integration**:
  - Single-instance process lock and URL/file handoff (`processlock*.go`, `instance.go`): forwards `.har` file opens and `apexload://` deep links to the running instance.
  - Descriptor-verified file & directory bridge (`files*.go`, `dragdrop.go`, `dropdirectory*.go`): binds OS open/save dialogs and drag-and-drop paths to canonical file identities (up to 64 MiB over the bridge; larger archives stream via multipart upload) to prevent symlink-retarget races.
  - Unsaved-work close guard (`closeguard.go`), native application menus & recent files (`menu.go`, `recents.go`), active-run sleep prevention (`keepawake*.go`), dock badges & notifications (`dockbadge*.go`, `notifications.go`), and window state persistence (`windowstate.go`).
- **Remote Workspaces**: Connects to hosted Kubernetes controllers without navigating the webview away from its local origin; the embedded controller syncs library assets and proxies remote run creation and lifecycle controls. See [../docs/ACCESS_SURFACES.md](../docs/ACCESS_SURFACES.md).

## Building & Testing

Build the embedded dashboard assets (`cd ../dashboard && npm run build`) before building or packaging the desktop binary. From the repository root:

```bash
# Verify Wails CLI, platform SDKs, and embedded assets
make desktop-preflight

# Build the local development desktop app (outputs under desktop/build/bin/)
make desktop

# Build desktop + Recording Agent client artifacts together
make clients

# Run Go unit tests for the desktop package
go test ./desktop/...
```

## Release Packaging & Signing

Local `make desktop` builds are unsigned developer artifacts. Production distribution uses `make desktop-release` ([../scripts/package-desktop.sh](../scripts/package-desktop.sh)), which enforces macOS Developer ID signing + notarization, Windows Authenticode signing, and signed `SHA256SUMS.txt` manifests in `.github/workflows/release.yml`. See [Desktop Distribution Trust](../docs/ACCESS_SURFACES.md#desktop-distribution-trust) for the complete signing environment variable reference.
