var e=`# APEX Load Access Surfaces

APEX Load is reachable through three supported surfaces. They share the same API and asset model, but they are intentionally labeled so users can tell whether they are working against a hosted controller, a local desktop workspace, or a headless CLI run.

## 1. Hosted Browser

The Kubernetes controller serves the React dashboard from the same origin as the REST API. This is the canonical zero-install experience: users open the controller URL in a browser and can author, schedule, run, monitor, and review tests without installing desktop software.

Hosted deployments opt into a wide HTTP bind explicitly through \`APEXLOAD_ADDR\` in the container and Kubernetes manifests. Browser traffic remains same-origin, so the dashboard does not need client-side controller code or cross-origin API switching.

## 2. Desktop Local Workspace

The Wails desktop app is optional. It runs an embedded local controller on loopback, uses local SQLite/keyring-backed storage, and supports local authoring and local execution. Closing the window, quitting or reloading (View › Reload) while a page holds unsaved work (an editor change, an import review) asks first, with the same prompt as leaving that page. File › Open Script… opens script and import files from disk, as opening them from Finder or Explorer does.

Remote K8s connectivity is handled through Remote Workspaces. The desktop SPA stays same-origin against its embedded controller; it does not switch the whole application origin to the hosted controller. The local controller uses configured Remote Workspace credentials to push and pull scripts, scenarios, results, and dependencies, and to proxy remote run creation and controls.

Remote run v1 is intentionally poll-based:

- \`POST /api/v1/remotes/{id}/runs\` pushes required local assets, translates local IDs to remote IDs, and creates the remote test.
- \`GET /api/v1/remotes/{id}/runs/{remote_test_id}\` polls the remote test status.
- \`POST /api/v1/remotes/{id}/runs/{remote_test_id}/start|stop|abort|pause|resume\` forwards lifecycle controls.

Run links are persisted under \`remote.run_links.v1\`, separate from library sync links (\`library.sync_links.v1\`). Full realtime viewing should use the hosted run page link; a server-side WebSocket relay is deferred.

### Desktop Distribution Trust

The local development Wails build is not a production installer. macOS desktop
packages must be signed with a Developer ID Application certificate and
notarized before distribution to end users, otherwise Gatekeeper may block the
downloaded \`.app\` with an Apple verification warning. Build release packages
with:

\`\`\`bash
APEXLOAD_MACOS_CODESIGN_IDENTITY='Developer ID Application: Example Corp (TEAMID)' \\
APEXLOAD_MACOS_NOTARIZE=1 \\
APEXLOAD_MACOS_NOTARY_PROFILE=apexload-notary \\
make desktop-release
\`\`\`

Windows desktop distribution should likewise use an Authenticode-signed
installer or executable from CI. The desktop release script can sign Windows
executables with either \`signtool\` on Windows CI or \`osslsigncode\` on
macOS/Linux CI. Import a PFX through PowerShell's \`SecureString\` API first, then
select the certificate-store thumbprint; the package script never accepts a PFX
password on its command line:

\`\`\`bash
APEXLOAD_WINDOWS_CODESIGN=1 \\
APEXLOAD_WINDOWS_CODESIGN_TOOL=signtool \\
APEXLOAD_WINDOWS_CODESIGN_SHA1="$IMPORTED_CERTIFICATE_THUMBPRINT" \\
make desktop-release WAILS_PLATFORM=windows/amd64
\`\`\`

Cross-platform \`osslsigncode\` use requires protected certificate and
unencrypted private-key files; password arguments are rejected because they are
visible in the process list.

\`WAILS_PLATFORM\` is passed to both \`wails build -platform\` and the desktop
package script so the archive name matches the built app. When packaging an
already-built desktop artifact directly, set
\`APEXLOAD_DESKTOP_PACKAGE_PLATFORM=windows/amd64\` or the matching target.

Release CI can also sign the generated \`SHA256SUMS.txt\` manifest:

\`\`\`bash
APEXLOAD_CHECKSUM_SIGN=1 \\
APEXLOAD_CHECKSUM_SIGN_KEY="$GPG_SIGNING_KEY" \\
make desktop-release
\`\`\`

Raw local debug archives are acceptable for developer machines only, not for
broad user distribution.

The GitHub Actions desktop workflow (\`.github/workflows/desktop.yml\`) is an
unsigned validation lane for pull requests, \`main\`, and manual diagnostics. It
builds and smoke-tests every native package without receiving release signing
credentials. The protected release workflow (\`.github/workflows/release.yml\`)
is the only publication lane: it fails closed unless macOS signing and
notarization, Windows Authenticode signing (including the installed
uninstaller), and signed checksums all succeed.

Configure these repository secrets/variables to produce trusted downloads:

| Name | Purpose |
| --- | --- |
| \`APEXLOAD_MACOS_CERTIFICATE_P12_BASE64\` | Base64-encoded Developer ID Application \`.p12\` certificate for macOS signing. |
| \`APEXLOAD_MACOS_CERTIFICATE_PASSWORD\` | Password for the \`.p12\` certificate. |
| \`APEXLOAD_MACOS_CODESIGN_IDENTITY\` | Developer ID identity name passed to \`codesign\`. |
| \`APEXLOAD_MACOS_NOTARIZE\` | Repository variable; set to \`1\` to require notarization. |
| \`APEXLOAD_MACOS_NOTARY_PROFILE\` | Optional local-development notarytool keychain profile name; protected CI uses the file-backed API key below. |
| \`APEXLOAD_MACOS_NOTARY_KEY_P8_BASE64\` | Protected release secret containing a base64-encoded App Store Connect API private key. |
| \`APEXLOAD_MACOS_NOTARY_KEY_PATH\`, \`APEXLOAD_MACOS_NOTARY_KEY_ID\`, \`APEXLOAD_MACOS_NOTARY_ISSUER_ID\` | File-backed App Store Connect API-key authentication when not using a stored profile. The release workflow materializes the private key with mode \`0600\`; it never passes the private key or an Apple password in process arguments. |
| \`APEXLOAD_WINDOWS_CODESIGN_PFX_BASE64\` | Base64-encoded Authenticode \`.pfx\`/\`.p12\` certificate. |
| \`APEXLOAD_WINDOWS_CODESIGN_PASSWORD\` | Password for the Windows signing certificate. |
| \`APEXLOAD_RELEASE_SIGNER_PATH\` or \`APEXLOAD_RELEASE_SIGNING_KEY_PEM\` | Protected KMS/HSM signer executable (preferred) or PEM fallback used by the release workflow to sign \`SHA256SUMS\`. |
| \`APEXLOAD_RELEASE_SIGNING_PUBLIC_KEY_PEM\` | Public verification key bundled with published release assets. |

## 3. CLI

\`apexctl\` remains the CI/headless surface. It can run scripts locally or target a controller API, and this access-surface work does not change its contract.

## Binding Defaults

Standalone local/dev controllers bind to \`127.0.0.1:8080\` when \`APEXLOAD_ADDR\` is unset. Production environments (\`APEXLOAD_ENV=production\` or \`prod\`) default to \`:8080\`. Any explicit \`APEXLOAD_ADDR\` is honored exactly.

Container and server deployment contexts should set \`APEXLOAD_ADDR\` explicitly. The checked-in Docker image, compose file, and Kubernetes manifests do this so Services and port mappings remain reachable.

The deployment identity banner reads \`/api/v1/system/version\` and shows the deployment kind, bind scope, and an explicit warning only when the server is wide-bound while auth bypass is active.

## Desktop Promotion Governance

\`SystemSettings.client_app.availability\` controls whether the hosted browser UI promotes the optional desktop client. It is a system-wide admin setting with values:

\`\`\`json
{
  "availability": "enabled"
}
\`\`\`

This is promotion governance, not API authorization. Disabling it hides hosted UI promotion, docs links, and future download CTAs. It does not block already-installed desktop clients, CLI users, or any other authorized caller from using REST APIs. API access remains governed by auth, RBAC, service-account scopes, and project permissions.

## Auth Direction

Remote desktop auth uses existing Remote Workspace bearer/API-key credential storage. Desktop OIDC SSO is deferred; the expected future shape is system-browser PKCE with token storage in the OS keyring, reusing the controller's existing OIDC configuration where possible.
`;export{e as default};