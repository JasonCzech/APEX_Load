# LoadRunner Runtime-Settings Import Evaluation — 2026-07-30

Evaluation archive: `ACME_RCI_Dummy.zip` (VuGen 24.3, Web – HTTP/HTML, one `Action`,
one `ACME_RCI` transaction, SOAP-over-HTTP `web_custom_request`). Runtime settings:
`default.cfg` (9 sections, ~137 keys) and `default.usp` (run logic tree + pacing),
selected via the `.usr` `[CfgFiles]`/`[RunLogicFiles]` profile pointers.

The question evaluated: **are all Runtime Settings in the source script accounted
for, translated correctly, and displayed/editable in the Script Editor Runtime
Settings tab and the Scenario runtime-settings override surfaces?**

## Verdict before this change

The destination model and UI were largely ready — `dsl.ScriptConfig` and the Script
Editor already carried speed simulation, download filters, streaming, data
formatting, block scheduling, and action sequence — but the importer only
recognized APEX-invented section/key names for several RTS pages, so on a real
VuGen archive those pages imported as *zero settings, silently*:

| Gap (before) | Effect on this archive |
| --- | --- |
| `[ModemSpeed]` unread | Speed Simulation page dropped (this cfg: "Use maximum bandwidth") |
| `[FILTERS]` unread | Download Filters page dropped (this cfg: no filtering configured) |
| `[Streaming]` real keys unmatched | `StreamTimeout/RetryTime/SeekMethod/StreamLog/SaveStreamSnapshot` invisible |
| `[Log] AutoLog` unread | "Send messages only when an error occurs" dropped (here `0` = off, benign) |
| `.usp RunLogicRunMode` unread | `Sequential` block scheduling dropped |
| `.usp [Profile Actions]`/`RunLogicActionOrder` unread | `vuser_init,Action,vuser_end` sequence dropped |
| `[Java] UseExternalJVM/ExternalJREPath` unread | Read as absent instead of "internal JDK" |
| `*_extra_arg_N` spill rows (non-JavaVM) ignored | `BrowserAcceptLanguage_extra_arg_2=en-us` lost behind `BrowserAcceptLanguage=None` |
| `.usr [Transactions]/[TransactionsOrder]` unread | No conservation check for declared transactions |
| Silent-drop default | Only an 18-entry hand allowlist warned; every other unmapped key vanished into preserved metadata with **no parity entry**, and because the readiness score averaged only matched entries, dropping a section *raised* the reported score |

## What was changed

### Importer (`pkg/convert/enterprisearchive`)

- **Real key mappings** — `[ModemSpeed]` → `network_simulation` (bps → Kbps;
  both-toggles-off → `max`), `[FILTERS]` → `download_filters`
  (`allow_list`/`block_list` from whichever list is populated; both populated →
  review), `[Log] AutoLog` → `log.on_error_only`, `.usp RunLogicRunMode` →
  `block_scheduling` migration evidence, `.usp [Profile Actions]` +
  `RunLogicActionOrder` → `action_sequence` migration evidence (both
  `compatibility_only` because the engine does not consume those fields;
  standard lifecycle vocabulary only and custom orders → review), bare
  `[Java]` `UseExternalJVM`/`ExternalJREPath` → Java VM JDK selection (java_vuser
  scripts), `.usr [Transactions]/[TransactionsOrder]` parsed and conservation-checked
  against started transactions.
- **Spill rows** — `*_extra_arg_N` rows next to Accept-Language/Accept-Encoding
  are detected; divergent values raise a `request_headers` review finding
  (`runtime_config.go: noteRuntimeHeaderSpillRows`).
- **Error-handler run logic** — `[RunLogicErrorHandlerRoot]` is accounted for: a
  real handler action → `compatibility_only`; the stock scaffold without a
  matching action file (this archive) → `non_applicable` with evidence.
- **No-silent-drop completeness pass** (`runtime_disposition.go`) — replaces the
  hand allowlist. Every key of the selected cfg/usp classifies as `mapped`
  (parity matcher or consumed-key), `compatibility_only` (active, no execution
  path), or `non_applicable` (VuGen authoring/recorder metadata, run-logic tree
  bookkeeping, inactive defaults). Recorded per-key at
  `metadata.import.preserved.runtime_setting_dispositions`; rolled up into
  `runtime_unmapped` (compatibility_only) + `runtime_non_applicable` parity
  entries so the migration-readiness score now counts them.
- **Host fidelity fix** — download-filter hosts no longer pass through enum
  normalization (which corrupted hyphenated hostnames).

### Dashboard

- **Imported Source Runtime Settings** — new read-only Script Editor section
  rendering every preserved cfg/usp file/section/key with its source value and
  disposition badge (`Mapped → <field>` / Compatibility-only / Not applicable),
  including non-selected profiles marked "preserved only". Runtime Parity sidebar
  links to it.
- **Vocabulary fixes in the scenario override editor** (`WebHttpTab`) — it
  previously emitted values the validator rejects (`allow`/`block` instead of
  `allow_list`/`block_list`; network-simulation `none` instead of `max`;
  streaming `none`; content-check scopes `body/headers/url` instead of
  `any_response/main_document/resource`; free-text presets). All selects now
  emit exactly the `pkg/dsl/validate.go` enums, locked by test.
- **Override reach** — the full per-group runtime editor (Summary/Think
  Time/Pacing/Run Logic/Error Handling/HTTP Transport/Browser Emulation/
  Web-HTTP/Java Env) is now also mounted for **scenario-root defaults** (TestHub
  Run Settings) and the **Create Test "Runtime Settings Overrides"** panel — one
  shared component, three surfaces. `java_vuser` joined the server-side
  apply-to-groups field contract (`runtimeOverrideFieldKeys` +
  `projectRuntimeFields`), which previously dropped it silently.
- **Script Editor field gaps closed** — TLS client cert/key/CA file paths and the
  Playwright browser User-Agent are now editable in the Script Editor (previously
  group-editor-only); `download_resources`/`capture_screenshots` in the group
  editor became enum selects; `web_runtime` sections now show for every
  HTTP-transport protocol (this archive classifies as SOAP-over-HTTP and
  previously lost the whole Web HTTP/HTML Compatibility section).

## Per-key evaluation of this archive (after the change)

Every one of the ~137 cfg keys plus the `.usp` tree now carries an explicit
disposition (`TestGoldenVuGen24RealRuntimeSettings` enforces zero unclassified
keys against the sanitized twin fixture `testdata/golden_web_http_html_vugen24`).

**Executed settings** — highlights with this archive's values:

| Source | Imported as |
| --- | --- |
| `.usp RunLogicPaceType=Const`, `RunLogicPaceConstTime=5` | `pacing: fixed_interval, 5000 ms` (supersedes cfg `IterationASAP`) |
| `.usp RunLogicNumOfIterations=1` | `run_logic.iterations: 1` |
| `[General] AutomaticTransactions=1` | `run_logic.auto_transactions: per_action` |
| `[General] ContinueOnError=1` + `[WEB] SnapshotOnErrorActive=1` | `error_handling.continue_on_error/snapshot_on_error: true` |
| `[ThinkTime] Options=RECORDED` | `think_time: recorded ×1` |
| `[WEB] ConnectTimeout/ReceiveTimeout=500` | `http.connect/request_timeout_ms: 500000` |
| `[WEB] HTTPVer=1.1 + EnableHTTP2=1` | auto-negotiated H2-preferred with 1.1 fallback (LR-equivalent) |
| `[WEB] BrowserAcceptEncoding` | `http.accept_encoding: gzip, deflate, br` |
| `web_set_sockets_option SSL_VERSION TLS1.3` | `http.tls 1.3–1.3` exact |
| `[WEB]` browser selection + cache toggles | `browser_emulation: specific IE 10 Windows` + all five cache simulations on |
| `[ModemSpeed]` both toggles off | `web_runtime.network_simulation.mode: max` |
| `[FILTERS]` empty lists | no download filter (exclude-nothing, LR default) |
| `[Log] LogOptions=LogExtended + MsgClassParameters=1` | `log: extended + parameter_substitution` (`AutoLog=0` → error-only off) |

**Partial (2)** — correctly flagged for review, values preserved:

- `proxy` — `ProxyUseProxy=1` with `cso proxy host:8888`, but `ProxyNoLocal=1`
  has no exact `no_proxy` equivalent; the importer deliberately fails the proxy
  closed with a review note rather than silently rerouting local traffic.
- `request_headers` — the `UseCustomAgent=1` custom UA conflicts with the
  differing `BrowserUserAgent` line, and `BrowserAcceptLanguage_extra_arg_2=en-us`
  diverges from the selected `None`; both surfaced as review evidence.

**Compatibility-only run ordering (2 parity entries)** — the importer preserves
`.usp RunLogicRunMode=Sequential` as
`run_logic.block_scheduling: sequential` and
`Profile Actions name=vuser_init,Action,vuser_end` as
`run_logic.action_sequence: [vuser_init, action, vuser_end]`. The current engine
does not consume either field; it uses the imported action list in its fixed
init/action/teardown lifecycle. An executable migration policy must include
`compatibility_only` in `migration_block_statuses` so random/percentage
scheduling, lifecycle omission, and reordering remain review-gated.

**Compatibility-only (44 active keys, all itemized in `runtime_unmapped`)** —
semantic source controls with no APEX execution path, e.g.
`HttpErrorsAsWarnings`, `FailNonCriticalItem`, `ResourcePageTimeoutIsWarning`,
`EnableChecks`, the NTLM/Kerberos block (`IntegratedAuthentication`,
`UseNativeNTLM`, `DisableNTLM2SS`, `OverrideNTLMCreds`, `HeavyKDCLoad`,
`SPNAddNoneDefPort`, `SPNCNameLookup`), `SupportCharset`, `UTF8InputOutput`,
`Retry401ThinkTime`, `WebSyncRetry*`, `EnableIPCache`, `EnableDynatrace`,
`UseDataFormatExtensions`, PAC-proxy keys, `KeepAliveTimeout`,
`KeepNonTextMimeType`, `MaxErrorMatchesAsERRORS`, `NetBufSize`,
`PageDownloadTimeout`, `ParseHtmlContentType`, `ResetContext`, `SSLVersionKey`,
`ZlibHeadersInCompressedRequestBody`, `[Streaming] StreamTimeout/RetryTime/SeekMethod`,
`[General] automatic_nested_transactions/AutomaticTransactionsPerFunc/FailTransOnErrorMsg`,
the active JavaScript transport block (`EnableJsForTransport`,
`JsForTransportRuntimeSize`, `JsForTransportStackSize`), response-log truncation
controls, and active prefetch/WebSocket callback intervals. These transport
settings affect source replay and therefore require compatibility review;
only disabled controls and child settings whose parent feature is off are
non-applicable.

**Non-applicable (97 rows)** — VuGen authoring/recorder metadata
(`WebRecorderVersion`, `TreeViewRequestBodyLimit`, graph toggles, mdrv log
formatting), run-logic tree bookkeeping (`MercIniTree*`,
init/end group scaffolding, the inert `vuser_errorhandler` scaffold), and
inactive defaults (`DisableAED=0`, `[Java] UseExternalJVM=0`, `FilterType` with
empty lists, unused `ThinkTimeRandomLow/High`, unused `CustomModemSpeed`, and
transport/log-routing controls whose enabling parent or toggle is off).

## Deliberate deviations from the original remediation sketch

- `UseDataFormatExtensions=1` was **not** mapped to
  `data_formatting.auto_detect`: the LR key enables a configured DFE chain (none
  exists in this archive), while APEX auto-detect actively decodes base64/gzip —
  wiring it would invent decode behavior LR never exhibited. Classified
  compatibility-only instead.
- `HTTPVer=1.1 + EnableHTTP2=1` needs no extra warning: that pair is the normal
  VuGen encoding of "HTTP/2 enabled", and APEX's auto-negotiation reproduces it.
- `ProxyUseSame=0` with differing endpoints already fails closed with a review
  note in the existing proxy resolver; no additional note required (endpoints are
  identical in this archive).

## Evidence

- Fixture: `pkg/convert/enterprisearchive/testdata/golden_web_http_html_vugen24`
  (sanitized hosts; the exact VuGen 24.3 section/key/value surface otherwise)
- Golden + completeness gate: `pkg/convert/enterprisearchive/runtime_vugen24_golden_test.go`
- Run-order classification and migration-policy gate: `pkg/convert/enterprisearchive/runtime_execution_fidelity_test.go`
- Classification tables: `pkg/convert/enterprisearchive/runtime_disposition.go`
- Sanitized end-to-end regression (same script family): `pkg/api/acme_enterprise_archive_regression_test.go`
- Editor vocabulary lock: `dashboard/src/components/runtimeTabs/WebHttpTab.test.tsx`
- Imported-source panel: `dashboard/src/components/ScriptRuntimeSettingsView.test.tsx`
