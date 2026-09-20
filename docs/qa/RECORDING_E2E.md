# Recording and replay regression suite

This opt-in suite starts disposable controllers and a real recording agent, drives
recorder-owned Chromium and an HTTP proxy, and checks the captured DSL through the
dashboard. The local HTTP/HTTPS fixture keeps an independent request ledger and
rejects stale or reused CSRF tokens. No external application is required.

## Setup and commands

Use the Node version in `.nvmrc`, Go from `go.mod`, OpenSSL, and Python 3. Install
both browser runtimes; their revisions are pinned independently by this repository:

```sh
cd dashboard
npm ci --ignore-scripts
npx playwright install chromium
cd ..
go run github.com/mxschmitt/playwright-go/cmd/playwright@v0.6100.0 install chromium
```

On Linux install Xvfb and Playwright's system dependencies. The entry point wraps
itself in `xvfb-run -a` when no display is configured. macOS requires a logged-in
desktop session; recording windows will appear. Recording remains headed, while the
dashboard driver is headless. Missing prerequisites and failed CDP attachments fail.

```sh
# Complete suite, both dev-admin and local-auth profiles
scripts/run-recording-e2e.sh

# One profile and one independently runnable scenario
APEXLOAD_E2E_PROFILE=local-auth scripts/run-recording-e2e.sh browser.spec.ts --grep 'agent:'
APEXLOAD_E2E_PROFILE=dev-admin scripts/run-recording-e2e.sh proxy.spec.ts
scripts/run-recording-e2e.sh authoring.spec.ts --grep 'every loop mode'
```

The command builds the embedded dashboard and test-tagged controller/agent before
running. It does not reuse your controller, database, browser profile or proxy
settings. Each test owns its workspace, SQLite database, archive and CA directories,
target servers and ephemeral loopback ports. Tests run serially with zero retries. The entry point accepts spec-file and grep
filters; it rejects options that override retry, discovery or evidence policy.
An empty selection, skipped tests, failures and flaky results fail the command.
The complete run must report **42 passed**: 21 scenarios under each profile.
Ordinary `npm run test:cycle` does not discover these specs. The aggregate opt-in
runner includes this suite with `--all`.

## Evidence and troubleshooting

Evidence is retained under `output/playwright/recording/<timestamp>/`; override with
`APEXLOAD_RECORDING_E2E_OUTPUT=/absolute/path`. Inspect `report.json`, per-case
controller/agent logs, `fixture-events.json`, `fixture-summary.json`, `cleanup.json`, conversion/replay attachments and
Playwright traces. The output path is printed even after a failed run. Disposable
working directories and process groups are cleaned after success, failure or a
signal. No test database or private CA is installed in the workspace.

A failure is actionable: do not rerun until green as a substitute for diagnosis.
Inspect the first failing assertion, its controller log, and the fixture ledger.
For a missing browser, install the Go and Node runtimes separately. For a connection
failure, confirm a desktop/Xvfb display and available loopback ports. A normal
controller build intentionally ignores `APEXLOAD_RECORDING_E2E_CDP_PORT`; always use
the entry point. CDP is enabled only under the `recording_e2e` build tag, on loopback,
with separate ports for the controller and agent. There are no new REST routes.

## CI

`recording-e2e.yml` supports manual dispatch, relevant pull-request/main changes and
merge queues. It follows the existing same-repository PR trust restriction and
GHES unprivileged-runner convention. Hosted Linux installs Xvfb; GHES runners must
provide it and use configured dependency mirrors. Evidence uploads even on failure.

## Manual installation checks

Automated HTTPS tests trust only the disposable proxy client's context. For a
release that changes packaging or workstation setup, additionally check:

- Start the packaged recording agent on each supported operating system; verify
  browser installation, dashboard origin checks, pairing and disconnect behavior.
- Follow the UI's CA download/trust instructions in a disposable OS/browser profile;
  verify the displayed fingerprint, HTTPS capture and trust removal afterward.
- Configure an individual application's HTTP/HTTPS proxy using the displayed
  address, check loopback bypass behavior, then restore that application's settings.
- Close the recorder window and dashboard independently, interrupt the agent, and
  confirm a new session can start without stale processes or sockets.
- Verify native desktop packaging, browser focus/window placement and OS file dialogs.

OS trust installation, external applications, distributed load generation and
performance benchmarking are intentionally outside this automated suite.

## Coverage matrix

The exact scenario inventory is `scripts/recording-scenarios.json`. A full run
requires every listed scenario once under **each** authentication profile; renamed,
missing, duplicate, skipped and retried scenarios fail validation. File/`--grep`
selections run a subset with the same zero-skip and zero-retry requirement.

| Scenario (file) | Controls/features | Observable acceptance evidence |
| --- | --- | --- |
| `controller: capture checkout, verify, save and replay` (`browser`) | Recording Studio launch; session identity/project; live panel; dashboard assertion picker; HAR; verification; verified save; reopening; browser debugger; one VUser | Real input/click/assert steps, fresh CSRF/order IDs, successful assertion and replay reports, four distinct journeys, terminal COMPLETE, exactly one main checkout and teardown in execution |
| `agent: capture checkout, verify, save and replay` (`browser`) | Real agent startup, Verify Agent, Pair Local Bridge; recording/control/convert bridge; save into controller library and execute | Real pairing response and CDP attachment; same fresh-token checkout, verification, debugger and one-VUser assertions as controller launch |
| `HTTP proxy captures HTTP and HTTPS, edits requests, correlates and replays` (`proxy`) | Separate proxied Chromium; HTTPS CONNECT; redirects, cookies, query/form/JSON bodies; include/exclude hosts and static filtering; pause/resume/stop; naming, request details, transaction, note, exclusion, think time; HAR; conversion; save/reopen/delete | Requests reach independent HTTP/HTTPS fixture; paused/excluded/static requests absent; form body preserved; diagnostic excluded from generated HTTP; automatic CSRF extractor present; lower-confidence order ID requires review and is bound through the editor; fresh cookies/tokens/IDs accepted on replay and one-VUser execution; deleted session disappears |
| `dashboard and closed-shadow toolbar control the same live session` (`controls`) | Companion panel and separate local-auth sign-in; focus; pause/resume, navigation, inclusion and data/secret marking on both control surfaces; section selection; both transaction/note controls; suggestions; toolbar undo and dashboard delete-last; assertion picker; collapse/expand/placement; reload; thumbnails; HAR | Authoritative state and exact live step counts; no paused/control input captured; init/main/teardown retained; markers retained; thumbnails loaded through UI; conversion verified |
| `capture modern web surfaces and preserve conversion warnings` (`controls`) | Text/password, keyboard, select, checkbox/radio, click/double-click, hover, drag/drop, upload, dialog, iframe, open shadow root, popup/window switching, SPA, scrolling, WebSocket payloads | Captured kinds/values/selectors/frame context; masked password and scrubbed socket secret; upload filename and password environment binding preserved; explicit preparation warnings retained with converted DSL |
| `HAR host filtering preserves browser actions and disabled WebSocket payloads stay absent` (`options`) | Browser HAR host exclusion and WebSocket payload switch | Excluded host request reaches fixture but is absent from exported HAR; browser action still replays; socket observed without payload retention |
| `transaction splitting accounts for every output` (`options`) | Transaction splitting, per-output verification, verified batch save, workspace placement and reopening | Exactly two outputs; both verified, saved and debugged; exact six-event capture/verification/replay ledger |
| `recording verification promotes a working fallback selector and re-verifies the changed conversion` (`options`) | Selector self-heal and promotion through recording verification UI | Fixture changes the control ID; fallback succeeds, Accept changes the authoritative candidate, new verification and saved-script replay pass |
| `parameterization review applies a recorded input and saves it into the selected folder` (`options`) | Review selection/application; folder creation/selection; verified save | Applied badge/selection survives update; generated parameter persists; saved project/folder IDs match; capture value received on verification and reopened replay |
| `conversion preview is ephemeral and changing options invalidates verification before unverified save` (`conversion`) | Preview without persistence; conversion settings; verification invalidation; unverified save | Script list remains empty after preview; option change clears verification and disables old output verification; unverified saved draft reopens and debugs successfully |
| `browser: recorded actions exercise branches and every loop mode` and `http: recorded actions exercise branches and every loop mode` (`authoring`) | Source-editor authoring around captured actions: if/else, switch/default, counted/while/do-while/foreach/nested loops, break/continue, safety limit; HTTP parallel branches | Both initial browser/HAR outputs verified and saved; exact ordered 26-event sequential ledger; HTTP additionally has two order-independent branch outcomes; nested iteration evidence and no leaked loop/row variables |
| `recorded browser inputs use list parameters, CSV rows and session overrides` (`authoring`) | Literal/list values, CSV-backed input, runtime overrides, invalid override JSON, missing and configured secret bindings | Fixture receives values different from capture; invalid JSON marked invalid; missing secret disables replay; UI-configured secret reaches target but is absent from replay report |
| `recorded HTTP correlation fails with the wrong extraction and succeeds after repair` (`authoring`) | Manual JSON extraction/substitution; deliberate broken correlation; diagnosis and source-editor repair | Wrong token rejected with 409; Problems identifies failure; repaired extraction gets a new token and succeeds |
| `recorded browser flow consumes a fresh browser extraction result` (`authoring`) | Browser evaluation/extraction workflow | Runtime `result` is present in replay extractions and supplies the fixture's subsequent input |
| `debugger steps a recorded HTTP script, diagnoses failure and repairs it` (`authoring`) | Run, start paused, pause during an in-flight request, step, continue, stop/restart, permanent breakpoint, run-to, break on failure, runtime profile save/reload, watches, timeline selection, HTTP evidence, history comparison | Paused action names and exact run-to ledger; deliberate assertion failure then successful repair; captured response body; watched extraction; historical outcome comparison; second breakpoint honored; in-flight pause gates the next action, stop prevents it, and a clean restart completes |
| `saved browser and HTTP recordings execute one VUser through New Test` (`authoring`) | Both conversion outputs, New Test workload and iteration override, normal engine execution | One VUser, one iteration, COMPLETE status; exact fixture-side main and final teardown events; execution manifest retained |
| `reject duplicate capture, survive dashboard reload and recover after window closure` (`recovery`) | Invalid URL; duplicate active session; dashboard reload; window-close recovery; minimal toolbar stop and HAR off | Invalid field, 409 duplicate, adopted live session, preserved steps/`browser_closed`, successful verification and subsequent session without HAR |
| `mobile recording reuses exported session state with HAR disabled` (`recovery`) | Mobile preset, minimal toolbar, HAR off, storage-state export/reuse | Touch-enabled runtime; no HAR; exported state verifies; next browser actually restores a localStorage choice from capture |
| `force-finish preserves captured steps and releases a stuck recording for the next session` (`recovery`) | Deterministic test-tag stop gate, Force Finish | Stopped status with steps retained, successful verification, subsequent capture/stop succeeds |
| `unavailable and disconnected agent can be verified and paired again` (`browser`) | Unreachable verification, killed agent, failed launch, Disconnect, replacement agent | Visible verification/launch failures; new real agent pairs, captures and verifies successfully |

Every `debug()` journey exports its session and checks the report outcome. All
multi-output conversions explicitly check output count and verify each output.
The modern-surfaces output deliberately remains a preparation case: password
values are never captured, and local upload names cannot become executable paths
without an operator-provided fixture asset. Its test requires those exact warnings
and bindings; it does not count generation as a successful replay. Its recorded
iframe/shadow/window context is retained for inspection in the attachment.

Browser parallel control blocks are not an executable engine feature: the engine
rejects browser actions in parallel branches. Parallel assertions therefore use
HTTP branches. The proxy UI always excludes static assets; it offers no static
inclusion toggle. The companion control panel opens automatically on browser
launch; its traffic must remain outside the application HAR (also covered by
`TestControlPanelTrafficIsExcludedFromHAR`). The available browser extraction
workflow is an authored `browser_evaluate_js` action in the source editor.

Package-level regression tests additionally cover the normal/test build boundary,
last-click flush, one-shot assertion picking, scoped import preservation, loop and
parallel variable readiness, sparse/out-of-order debugger events, bridge CORS
credentials, authoring state during pairing, and applied parameterization state.
Harness tests exercise real child-process cleanup/socket release, startup failure,
chunked log redaction, report inventory enforcement and trace/report sanitization.
