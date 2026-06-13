# APEX Load Recording Agent

The APEX Load Recording Agent is the production local bridge for browser
recording when the controller runs remotely or cannot launch a headed browser.
It is a foreground, loopback-only process that the dashboard pairs with using a
short-lived token.

## User Flow

1. Install the signed `apexload-record-agent` package from the dashboard's
   **Download the APEX Load Recording Agent** link.
2. Open Recording Studio in the dashboard.
3. Copy the generated terminal command.
4. Paste the whole command into Terminal or PowerShell.
5. Click **Verify Agent** to confirm the local bridge is reachable.
6. Click **Pair Local Bridge** in the dashboard.

The token is already included in the generated command as `--token`. Users do
not paste it separately.

Example:

```bash
apexload-record-agent --addr 127.0.0.1:27777 --token <dashboard-token> --allow-origin 'https://apexload.example.com'
```

## Privileges

Running the agent does not normally require admin privileges. It binds to
`127.0.0.1` on a high port and launches a browser in the user session.

Locked-down workstations may still require IT approval for:

- running the signed agent binary
- launching browser automation
- downloading or executing the Playwright Chromium payload
- browser private-network access from the dashboard origin to loopback

## Browser Dependency Preflight

Release or desktop engineering can pre-provision the browser dependency with:

```bash
apexload-record-agent install-browsers
```

This downloads the Playwright Chromium payload into the user/runtime cache used
by the agent. For stricter environments, package that cache or use an approved
enterprise Chrome/Edge integration when that mode is available.

## Building Release Packages

Build the local platform binary:

```bash
make recording-agent
```

Build cross-platform release archives:

```bash
make package-recording-agent
```

The package script writes archives and `SHA256SUMS.txt` to:

```text
dist/recording-agent/
```

Release packaging should sign/notarize the resulting binaries or wrap them in
the enterprise installer format used by your fleet management system.

## Dashboard Download Link

Set this at dashboard build time to show a direct download link in Recording
Studio:

```bash
VITE_APEXLOAD_RECORDING_AGENT_DOWNLOAD_URL=https://downloads.example.com/apexload-recording-agent
```

If unset, the dashboard link points to the same-origin download path:

```text
/api/v1/downloads/recording-agent/latest
```

Production deployments should either serve the signed installer at that path or
set `VITE_APEXLOAD_RECORDING_AGENT_DOWNLOAD_URL` to the release portal URL.

The controller serves this endpoint from `dist/recording-agent` by default.
Set `APEXLOAD_RECORDING_AGENT_DIST_DIR` when release packages are staged
somewhere else.
