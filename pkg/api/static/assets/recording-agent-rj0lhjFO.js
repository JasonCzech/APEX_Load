var e=`# APEX Load Recording Agent

The APEX Load Recording Agent is the production local bridge for browser
recording when the controller runs remotely or cannot launch a headed browser.
It is a foreground, loopback-only process that the dashboard pairs with using a
short-lived token.

## User Flow

1. Install the trusted \`apexload-record-agent\` package from the dashboard's
   **Download the APEX Load Recording Agent** link.
2. Open Recording Studio in the dashboard.
3. Copy the generated terminal command and paste it into Terminal (PowerShell
   on Windows) in the folder you extracted the agent into. The studio writes
   \`./apexload-record-agent\`, or \`.\\apexload-record-agent.exe\` for a Windows
   package, because that folder is usually not on \`PATH\`.
4. When the agent asks, paste the pairing token shown under the command.
5. Click **Connect** in the dashboard. It checks that the agent answers, then
   pairs with it.

The command reads the token from standard input (\`--token-stdin\`), so it never
appears in the command line or the process arguments, and the agent does not
print it back. The studio keeps the token for the browser tab, so an agent
started with it still pairs after the studio reloads; **Regenerate token**
replaces it, after which the agent must be started again. If Connect reports
that the agent was started with a different pairing token, stop it with Ctrl-C
and run the command again. If the studio says the agent no longer accepts the
connection (it was restarted, or the connection expired), click **Connect**
to pair again.

Once paired, the studio compares the agent's build with the controller's and
says so when they differ; download the agent again if recording, conversion or
saving misbehaves.

The agent's terminal shows its startup details, each pairing and failing
requests; successful requests are not logged there.

Exclude hosts (and the HTTP proxy's include and exclude hosts) take host
names: each entry covers that host and its subdomains, \`*.example.com\` works
too, and an entry with a port matches that port only. Parts of a name no
longer match, so \`t.co\` does not catch \`microsoft.com\`. When the recording
stops, its warnings say how many HAR entries each entry left out and which
entries matched nothing. The recording panel groups console and network
issues by host and error, the recorded site's own first, and folds requests
cancelled as pages moved on into one line.

Verify replays a recording headless by default. Some sites refuse a headless
browser they let a person's browser through; with the agent connected,
**Verify in a visible browser on your computer** replays a browser output in
a visible window instead, and a refused headless replay points to it.

Credential bindings and parameterization chosen while reviewing a recording
are applied by the agent, which converted the recording; saving then hands the
reviewed scripts to the controller.

Example:

\`\`\`bash
./apexload-record-agent --addr 127.0.0.1:27777 --token-stdin --allow-origin 'https://apexload.example.com'
\`\`\`

## Privileges

Running the agent does not normally require admin privileges. It binds to
\`127.0.0.1\` on a high port and launches a browser in the user session.

Locked-down workstations may still require IT approval for:

- running the signed/notarized agent binary
- launching browser automation
- downloading or executing the Playwright Chromium payload
- browser private-network access from the dashboard origin to loopback

## macOS Gatekeeper

The local development package is a raw command-line binary archive. If it was
not built with a Developer ID Application certificate and notarized by Apple,
macOS may block it with:

\`\`\`text
Apple could not verify "apexload-record-agent" is free of malware that may harm your Mac or compromise your privacy.
\`\`\`

For local development only, verify the artifact first, then clear the quarantine
attribute on the extracted binary:

\`\`\`bash
curl -fsS 'http://127.0.0.1:8080/api/v1/downloads/recording-agent/latest/checksum?platform=darwin-arm64'
shasum -a 256 apexload-recording-agent-*-darwin-arm64.tar.gz
tar -xzf apexload-recording-agent-*-darwin-arm64.tar.gz
xattr -dr com.apple.quarantine apexload-recording-agent-*-darwin-arm64/apexload-record-agent
\`\`\`

Production macOS distribution should not rely on that workaround. Build with a
Developer ID identity and notarization credentials so Gatekeeper can verify the
downloaded agent.

## Browser Dependency Preflight

Release or desktop engineering can pre-provision the browser dependency with:

\`\`\`bash
apexload-record-agent install-browsers
\`\`\`

This downloads the Playwright Chromium payload into the user/runtime cache used
by the agent. For stricter environments, package that cache or use an approved
enterprise Chrome/Edge integration when that mode is available.

## Building Release Packages

Build the local platform binary:

\`\`\`bash
make recording-agent
\`\`\`

Build cross-platform release archives:

\`\`\`bash
make package-recording-agent
\`\`\`

The package script writes archives and \`SHA256SUMS.txt\` to:

\`\`\`text
dist/recording-agent/
\`\`\`

Release packaging should sign/notarize the resulting binaries or wrap them in
the enterprise installer format used by your fleet management system.

For macOS release builds, configure Developer ID signing and notarization:

\`\`\`bash
xcrun notarytool store-credentials apexload-notary \\
  --apple-id "$APPLE_ID" \\
  --team-id "$APPLE_TEAM_ID"

APEXLOAD_MACOS_CODESIGN_IDENTITY='Developer ID Application: Example Corp (TEAMID)' \\
APEXLOAD_MACOS_NOTARIZE=1 \\
APEXLOAD_MACOS_NOTARY_PROFILE=apexload-notary \\
make package-recording-agent
\`\`\`

Enter the app-specific password at the interactive prompt so it is not exposed
through the process list.

The package script signs Darwin binaries with hardened runtime before archiving
and submits a temporary ZIP to \`notarytool\` when notarization is enabled.

For Windows release builds, configure Authenticode signing. On Windows CI with
the Windows SDK installed, import the PFX through PowerShell's \`SecureString\`
API, then select the certificate-store thumbprint with \`signtool\`:

\`\`\`bash
APEXLOAD_WINDOWS_CODESIGN=1 \\
APEXLOAD_WINDOWS_CODESIGN_TOOL=signtool \\
APEXLOAD_WINDOWS_CODESIGN_SHA1="$IMPORTED_CERTIFICATE_THUMBPRINT" \\
make package-recording-agent
\`\`\`

For cross-signing from macOS or Linux CI, install \`osslsigncode\` and use
protected certificate and unencrypted private-key files:

\`\`\`bash
APEXLOAD_WINDOWS_CODESIGN=1 \\
APEXLOAD_WINDOWS_CODESIGN_TOOL=osslsigncode \\
APEXLOAD_WINDOWS_CODESIGN_CERT_PEM="$WINDOWS_CERTIFICATE_PEM" \\
APEXLOAD_WINDOWS_CODESIGN_KEY_PATH="$WINDOWS_PRIVATE_KEY_PEM" \\
make package-recording-agent
\`\`\`

The package scripts reject signing-password arguments because they are visible
to other processes on the runner. Recording Agent notarization similarly
requires a preconfigured \`APEXLOAD_MACOS_NOTARY_PROFILE\`.

Unsigned Windows development packages can run, but SmartScreen or enterprise
application-control policies may warn or block them. Broad user distribution
should use a signed, timestamped executable or an enterprise-managed installer.

For Linux and archive-level integrity, publish the generated \`SHA256SUMS.txt\`.
Release CI can also create a detached ASCII-armored GPG signature:

\`\`\`bash
APEXLOAD_CHECKSUM_SIGN=1 \\
APEXLOAD_CHECKSUM_SIGN_KEY="$GPG_SIGNING_KEY" \\
make package-recording-agent
\`\`\`

That writes \`SHA256SUMS.txt.asc\` next to the archives so downstream users can
verify both the checksum manifest and the downloaded package.

Release CI can fail closed instead of emitting warning-only unsigned packages:

\`\`\`bash
APEXLOAD_REQUIRE_TRUSTED_PACKAGES=1 \\
APEXLOAD_MACOS_NOTARIZE=1 \\
make package-recording-agent
\`\`\`

With that switch enabled, Darwin packages require Developer ID signing and
notarization, Windows packages require Authenticode signing, and the checksum
manifest requires a detached GPG signature.

## Dashboard Download Link

Set this at dashboard build time to show a direct download link in Recording
Studio:

\`\`\`bash
VITE_APEXLOAD_RECORDING_AGENT_DOWNLOAD_URL=https://downloads.example.com/apexload-recording-agent
\`\`\`

If unset, the dashboard link points to the same-origin download path:

\`\`\`text
/api/v1/downloads/recording-agent/latest
\`\`\`

Production deployments should either serve the signed installer at that path or
set \`VITE_APEXLOAD_RECORDING_AGENT_DOWNLOAD_URL\` to the release portal URL.

The controller serves this endpoint from \`dist/recording-agent\` by default.
Set \`APEXLOAD_RECORDING_AGENT_DIST_DIR\` when release packages are staged
somewhere else.

## React and Salesforce Aura replay

Browser capture runs the application's JavaScript in Chromium. Use browser
scripts to validate frontend journeys; use reviewed HTTP drafts for bulk backend
load. HTTP replay does not measure React rendering or reproduce Aura's client-side
batching decisions. Size browser concurrency with a separate capacity measurement.

The recorder preserves settled intermediate text values and navigation keys
(including arrows and modified keys such as Shift+Tab). Ordinary fills use
Playwright's value-setting behavior. For widgets that depend on per-character
keyboard events, edit the fill action to use:

\`\`\`json
{
  "name": "Search",
  "type": "browser_fill",
  "selector": "[data-testid=search]",
  "value": "Alice",
  "params": { "input_mode": "type", "typing_delay_ms": 50 }
}
\`\`\`

\`type\` clears the input before typing. It does not reproduce the original cursor
position, IME composition, or keystroke timings. The delay accepts 0–1000 ms and
is bounded by the action timeout.

After observed asynchronous activity, generated scripts can wait for the next
recorded element in the same frame and section. They do not infer global
network-idle waits, which can stall on polling applications. This visibility
hint does not prove hydration or business success. Use the recorder's assertion
picker on the loaded suggestion, saved record, or other transaction result.
Text assertions now wait for the element to be visible with the expected text before
checking the assertion. The equivalent editable DSL is:

\`\`\`json
{
  "name": "Wait for save confirmation",
  "type": "browser_wait",
  "wait_for": { "type": "element_text", "value": "#result", "timeout_ms": 10000 },
  "params": { "text": "Saved Alice" }
}
\`\`\`

Use an application readiness element before interacting with controls that are
visible before hydration. Closed shadow roots still require an accessible public
interaction surface; ordinary locators cannot address their internals.

### Authenticated sessions

Prefer recording login into \`init\`, with project secrets for credentials and
separate test identities for concurrent users. Alternatively, store a Playwright
cookies/localStorage JSON snapshot in a project secret, then explicitly reference
it in the script:

\`\`\`json
{
  "config": {
    "browser": { "storage_state": "{{secret.BROWSER_SESSION}}" }
  }
}
\`\`\`

The reference is resolved by the execution host and seeds each new browser
context. Inline JSON and server file paths are not accepted in this DSL field.
Snapshots are limited to 1 MiB, 1,024 cookies, 128 origins, and 256 localStorage
entries per origin. Runtime target-host policy also applies to the snapshot's
cookie domains and origins. Snapshot credential values are registered for runtime
redaction.

Use a snapshot from **before** the transaction. Recording Studio does not silently
copy its end-of-recording session into a saved script. The downloadable export is
scrubbed; do not use its redacted values as authentication credentials. This
feature accepts cookies and localStorage, not sessionStorage or IndexedDB; an
unsupported field is rejected rather than silently ignored. A snapshot can expire,
and all VUsers using the same snapshot share its server-side identity. Use login
flows or separate secret-backed groups when distinct identities are required.

### Salesforce HTTP drafts

Recording Studio's optional HTTP output uses the same Salesforce native builder
as the HAR importer. It includes eligible dynamic correlations and Aura error
assertions, plus the review findings. Review unresolved correlations and
parameterize record IDs before increasing load. The generated response checks
are a starting point; add assertions for the intended business outcome.

### Qualification checks

The Go recorder regression suite includes a real React fixture with controlled
inputs, autocomplete navigation, continuous polling, asynchronous save results,
and generated Aura-style IDs. It records the flow and replays it twice in fresh
contexts. Install dashboard dependencies to provide its React runtime. CI runs
this fixture with the browser runtime required.

Qualify the actual React application and Salesforce sandbox separately:

1. Record login, search/select, save, and a business-result assertion.
2. Replay from fresh sessions repeatedly with distinct test users and record IDs.
3. Repeat with delayed responses, changed generated IDs, and continuing polling.
4. Confirm each backend result, not just successful clicks or HTTP status 200.
5. Compare browser-generated requests with the HTTP draft before a staged load ramp.

The local React fixture and synthetic Salesforce tests do not establish live
Salesforce org compatibility or LoadRunner baseline parity.
`;export{e as default};