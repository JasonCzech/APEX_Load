# ACME IAO runtime-settings import evaluation

Evaluated `ACME_IAO_Azure_Akamai_2025.zip` with the local converter. The archive
was inspected as source data; its target services were not contacted. Tests use
synthetic actions and a sanitized copy of its runtime settings, not its data or
credentials.

## Findings

The import covers substantially more settings than the review panel suggested.
Most `Source value unavailable` messages were caused by the panel requiring a
one-to-one destination-field match even when the converter reported a whole
configuration object or several destination fields. Expanding those objects
also manufactured rows for unrelated distribution fields. These were display
and provenance bugs, not redaction of numeric runtime settings.

Random percentages of recorded think time were a real missing capability. The
importer explicitly fell back to recorded timing and raised a partial-coverage
finding for `Options=RANDOM`. The existing `uniform.min_ms/max_ms` fields could
not substitute for percentages: each source delay needs its own scaled range.
The source semantics are documented in [OpenText's runtime-settings reference](https://admhelp.microfocus.com/vugen/en/latest/help/WebHelp/Content/VuGen/166950_t_how_to_confugre_rts_manually.htm).

This particular archive sets `Options=NOTHINK`. Its saved random bounds of 50
and 150, factor of 1, and disabled one-second cap are inactive. Moreover, its
source contains **no `lr_think_time` calls**. Enabling percentage mode alone
would therefore add no recorded waits. Retaining these settings as source
information is appropriate; claiming they are active replay mappings is not.

## Actual mappings for this archive

| Source | Effective DSL / behavior | Assessment |
| --- | --- | --- |
| `ThinkTime.Options=NOTHINK` | `think_time.type=recorded`, `ignore=true` | Correctly ignores recorded waits. |
| `ThinkTimeRandomLow=50`, `ThinkTimeRandomHigh=150` | Inactive source settings | Previously misleadingly classified as mapped; now marked not applicable under NOTHINK. |
| `Factor=1`, `Limit=1`, `LimitFlag=0` | Default multiplier 1, no active cap | Factor and saved cap are inactive; numeric values are retained in source evidence. |
| `.usp RunLogicPaceType=Const`, `RunLogicPaceConstTime=5` | `pacing.type=fixed_interval`, `interval_ms=5000` | The selected run-logic profile supersedes cfg `IterationASAP`. Other saved pacing ranges are not the active schedule. |
| `.usp RunLogicNumOfIterations=1` | `run_logic.iterations=1` | Mapped; scenario scheduling can supersede script iteration limits. |
| `AutomaticTransactions=0` | No automatic transaction wrapping | Explicit source transaction markers still execute. |
| `ConnectTimeout=500`, `ReceiveTimeout=500` | Connect/request timeout 500000 ms | Mapped. Source RECEIVE/STEP timeout calls also specify 500 seconds. |
| `HTTPVer=1.1`, `EnableHTTP2=1` | Automatic H2-preferred negotiation with HTTP/1.1 fallback | Empty/omitted DSL version here is intentional. |
| `KeepAlive=1`, `MaxRedirectionDepth=10`, `MaxConnections=0` | Keep-alive enabled; default redirect limit; default connection policy | Defaults can be omitted from serialized DSL without being dropped. |
| `KeepAliveTimeout=6000` | Explicit repair decision for `http.idle_timeout_ms` | Importer supports positive whole seconds up to 3600. This source value exceeds the current limit; it is preserved without applying or clamping it. |
| Enabled custom proxy, matching HTTP/HTTPS host and port | `http.proxy_mode=custom`, proxy URL | Mapped; host details intentionally omitted from this report. |
| `UseCustomAgent=1`, `CustomUserAgent=...` | `browser_emulation.mode=custom`, custom User-Agent | Selected custom identity wins over dormant recorded browser identity. |
| `BrowserAcceptEncoding=gzip, deflate, br` | `http.accept_encoding` | Mapped. |
| Browser cache, HTML cache, resource-download and prefetch toggles | `browser_emulation.cache.*` | Mapped, including explicit false values. |
| `LogOptions=LogExtended`, parameter/data flags enabled | Extended logging with `parameter_substitution`, `data_returned` | Mapped; error-only is disabled by `AutoLog=0`. |
| `General.ContinueOnError=0` plus `Action.c:13 lr_continue_on_error(1)` | `error_handling.continue_on_error=true` | Source-code control supersedes cfg; not a missing cfg mapping. |
| `Action.c:17 web_set_sockets_option("SSL_VERSION", "TLS1.2")` | Exact TLS 1.2 minimum/maximum | Source-code mapping; legacy cfg SSL selector is retained separately. |
| Both modem-speed selectors disabled | `network_simulation.mode=max` | Correct; saved speed numbers are inactive. |
| Empty include/exclude filter lists | No active download filter | Correct. |
| `PageDownloadTimeout=500` | `http.page_timeout_ms=500000` | Whole request plus resource waves share one deadline. |
| `DeleteCacheUnreferencedIterations=1` | `http.cache_max_unused_iterations=1` | Entries age by unreferenced main iterations, for each VUser. |
| `ParseHtmlContentType=TEXT` | `http.html_parse_mode=text` | HTML discovery accepts text MIME types except `text/xml`. |
| `IPVersionPolicy=2` | Explicit repair decision for `http.ip_version` | Native auto/IPv4/IPv6 selection exists; the numeric source encoding is unverified and is not guessed. |
| `.usp` block scheduling and lifecycle order | Verified native sequential lifecycle | Exact flat source tree and compiled action inventory match init/action/teardown. Custom trees still require review. |

## Changes made

- Added DSL mode `random_percentage` with `min_percent` and `max_percent`.
  A fresh percentage is sampled for each recorded delay. For example, 50–150%
  of a four-second source wait produces a delay between two and six seconds.
- Load execution and diagnostic replay use the same resolution logic. Ignore,
  multiplier, cap, cancellation, and transaction think-time accounting continue
  to apply. Percentages are bounded to finite `0 <= min <= max <= 10000`;
  0–0 is an intentional zero delay. Invalid imported bounds require review.
- C imports preserve leading and consecutive source waits separately in every
  initial mode, so later runtime edits cannot combine percentage samples or
  per-call caps.
- Added percentage controls to import review, script runtime settings, and
  scenario/group runtime settings. Changing modes initializes percentage bounds
  and clears obsolete millisecond-distribution values. Settings apply atomically
  and retain original source evidence.
- Fixed grouped/multiple-destination evidence matching, missing native logging
  aliases, absent-source rows, and unrelated mode controls in the RTS panel.
  Protected credential placeholders are identified explicitly and remain managed
  through Credentials; ordinary runtime values are shown from preserved source.
- Classified inactive think-time factor, percentage bounds, and cap values
  separately from executed mappings.
- Added `KeepAliveTimeout` import to `http.idle_timeout_ms`, with seconds-to-ms
  conversion, source evidence, an editable import-review field, and explicit
  handling for disabled connection reuse, malformed values, conflicts, and
  out-of-range values. The existing one-hour resource bound is retained.
- Authentication child options are now inactive when integrated authentication
  is explicitly disabled. `SupportCharset=None` is an inactive conversion
  setting. Prefetch callback timing is inactive when prefetch simulation is off.
  Missing, invalid, or conflicting parent selectors retain review findings.
- Streaming and synchronization defaults are inactive when inspected C sources
  contain no corresponding operation. The check covers native HTTP and SOAP,
  scans helpers/headers, and retains warnings for parse failures, external
  runners, mixed-language source, ambiguous macros, or indirect function
  references (including callbacks and function pointers). It proves absence in the
  inspected source, not arbitrary control-flow reachability.
- Cfg pacing and iteration values superseded by an applied USP name that
  selected profile in their explanation. Inactive USP pacing bounds are also
  separated from the selected schedule, while ambiguous selectors require review.
- Kept reference-table headers in normal flow inside the preserved/inactive
  disclosures so they cannot cover the row's Details control while scrolling.

## Remaining coverage limits

The selected cfg and usp contain **206 keys**. After the compatibility implementation,
per-key classification is **56 mapped/consumed, 9 compatibility-only, and 141
not applicable** (previous pass: 49, 16, and 141). These are accounting counts,
**not a replay-fidelity score**. In particular, `KeepAliveTimeout` and
`IPVersionPolicy` are consumed into dedicated **partial** repair entries; their
source values were not applied.

The nine remaining compatibility-only cfg keys are `IgnoreContentCachingTypes`,
`KeepNonTextMimeType`, `MaxErrorMatchesAsERRORS`, `MaxSelfMetaRefreshCount`,
`NetBufSize`, `ResetContext`, `SSLVersionKey`, `WebSocketCallBackTimerIntervalMS`,
and `ZlibHeadersInCompressedRequestBody`. Native explicit MIME exclusion lists
now exist, but the meanings of the two source cache/MIME switches are not
sufficiently verified to translate them automatically. The remaining transport,
callback, compression, and meta-refresh behaviors require separate implementation.

The `KeepAliveTimeout` mapping is now implemented through the existing
`http.idle_timeout_ms`, backed by the HTTP transport.
[OpenText documents the source timeout
in seconds](https://admhelp.microfocus.com/lre/en/26.1/online_help/Content/PC/ui_runtime_settings_ip_preferences.htm),
and this archive's 6000 seconds exceeds APEX's current 3600000-ms (one-hour)
configured-timeout bound. The implementation retains that limit and makes the
mismatch editable and explicit. A different supported timeout is an intentional
behavior change; accepting the exact source value would still require a
separate resource-limit change. The inaccurate note claiming idle timeout is
not configurable has been removed.

The three previously compatibility-only USP keys are now mapped for this
archive: its complete flat sequential profile matches the imported native lifecycle.
The proof rejects extra/nested children, repeated init/end blocks, conflicts,
missing files, mismatched action declarations, and active custom error handlers.
The engine continues to execute its native lifecycle; this does not claim generic
LoadRunner block-tree scheduling.

The reported authentication, charset, unused streaming, and stale cfg pacing
classification gaps are fixed. General control-flow reachability, runtime-loaded
code, and complete source-code override accounting remain beyond this pass.

Percentage execution applies to native DSL think-time waits. External runner
implementations of their own waits (such as an external Java Vuser process)
are not extended by this change. Existing dynamic/unresolvable C think-time
expressions continue to require review.

## Verification

Regression coverage includes the archive's sanitized runtime-setting shape,
valid/invalid percentages, zero and fractional bounds, independent source waits,
load and replay resolution/cancellation, DSL validation, atomic settings updates,
source-value provenance, mode editing, and browser import/apply/save flows.

Initial-pass validation (before the follow-up changes above):

| Check | Result |
| --- | --- |
| `go test ./...` | Passed. |
| Dashboard lint, TypeScript checks, production build | Passed. |
| Dashboard unit suite | 4413 tests passed across 416 files. |
| Focused LoadRunner browser suite, dev-admin | 10 passed, 1 optional private-fixture test skipped. |
| Focused LoadRunner browser suite, local-auth | 10 passed, 1 optional private-fixture test skipped. |
| Full browser cycle, dev-admin | 118 passed, 4 skipped, 9 failed. |
| Full browser cycle, local-auth | 46 passed, 1 skipped, 1 failed. |
| `git diff --check` | Passed. |

The required `npm --prefix dashboard run test:cycle` completed with exit 1.
All LoadRunner tests also passed within that full cycle. The dev-admin failures
were in command-center details, initial auth display, live metrics, two run
lifecycle flows, workspace hierarchy, authoring navigation, Source Mapping
column widths, and guided setup. The local-auth failure was the broad
sign-in/import/launch workflow, at the workspace setup assertion expecting a
`Current project` button, before import. These failures are outside the tested
runtime-settings flow; this evaluation does not establish that they pass on the
baseline. The Source Mapping width test passed in the local-auth profile.

The additional documentation checker reported 35 link/index issues in other
documents, including existing audit-report path/line links and missing top-level
index entries. No error referenced this report. Those broader failures remain
unresolved by this runtime-settings change.

### Follow-up verification

Added regression coverage for timeout conversion, boundaries, invalid values,
disabled and conflicting parent selectors, conservative operation-absence
checks, cfg/USP precedence, dormant pacing bounds, source-unit display, and
browser edit/validate/save behavior with preserved source evidence.

The actual private archive was reconverted locally to verify the updated counts
and explanations. Its target endpoints were not contacted.

| Check | Follow-up result |
| --- | --- |
| Converter package, including new regression cases | Passed. |
| Dashboard lint, TypeScript checks, production build | Passed. |
| Dashboard unit suite | 4428 tests passed across 416 files. |
| New browser timeout/context workflow | Passed in dev-admin and local-auth; screenshots inspected. |
| Full browser cycle, dev-admin | 119 passed, 4 skipped, 9 failed; all 11 LoadRunner cases passed. |
| Full browser cycle, local-auth | 47 passed, 1 skipped, 1 failed; all 11 LoadRunner cases passed. |
| Final `go test ./...` recheck | Passed across all packages. |
| `git diff --check` | Passed. |

The required full cycle completed with exit 1. Dev-admin repeated the same nine
failures listed in the initial pass above; local-auth repeated the workspace
setup assertion for the missing `Current project` button, before import. All
22 LoadRunner cases passed across the two profiles, with two optional
private-fixture cases skipped. A Go run encountered an intermittent desktop file-cleanup failure in
`TestSecureWriteGrantedFileFromEnforcesLimitBeforePublish`; that test then passed
three isolated runs and the complete desktop package passed. All other packages,
including the API package, passed that run. The complete Go recheck then passed
across all packages without code changes for that intermittent failure.
Browser runs use separate ports and result directories to avoid an independent
test run already using the default ports.


## Code-review corrections

The review found two additional edge cases, both corrected:

- Recorded/default/ignored/multiplied C imports now retain leading and
  consecutive literal waits independently. Changing to percentage mode or adding
  a cap after import therefore preserves per-call timing. Regression cases cover
  all initial modes; the engine checks that separate 2s and 3s waits at 100–100%
  with a 3s cap still total 5s.
- Streaming/synchronization absence checks now retain compatibility warnings for
  indirect function references, including pointer assignments and callbacks.
  Identifier scanning excludes literals and comments and preserves the existing
  treatment of SDK declarations. Tests cover both source families and the ZIP
  disposition output.

Validation after these corrections:

| Check | Result |
| --- | --- |
| Final `go test ./...` | Passed across all packages. |
| Dashboard typecheck, lint, unit suite, build | Passed; 4428 unit tests across 416 files. |
| Full browser cycle, dev-admin | 120 passed, 4 skipped, 8 previously observed failures outside the import flow. |
| Full browser cycle, local-auth | 47 passed, 1 skipped, 1 previously observed workspace-setup failure before import. |
| LoadRunner browser cases within the full cycle | All 22 passed; 2 optional private-fixture cases skipped. |
| Private archive reconversion | Unchanged: 49 mapped, 16 compatibility-only, 141 non-applicable. |
| `git diff --check` | Passed. |

The full dashboard cycle remains exit 1 because of the existing browser failures.
The Source Mapping width check passed in both profiles on this run. The first
Go run reproduced the intermittent desktop file-save test failure; three isolated
reruns and the final complete Go suite passed without changing desktop code.
Previously saved imports that already combined waits need re-importing to recover
the original call boundaries.

### Native compatibility implementation

- Added whole-page deadlines, outgoing TCP IP-family selection, cache aging by
  main iteration, HTML-discovery MIME modes, and explicit cache/discovery MIME
  exclusion lists. These execute in HTTP, SOAP, and Java-over-HTTP transports.
- Added script and group editor controls, import-review controls and source
  aliases, shared validation, schema generation, and atomic edit/save coverage.
- Imported page deadlines are positive whole seconds up to one hour; native
  zero disables the extra deadline. The per-request timeout remains independent.
- Cache aging zero disables aging; positive values are bounded to one million.
  Entry counts, byte budgets, cancellation, and VUser cache isolation still apply.
- IP family selects the client connection, including the connection to a proxy;
  it cannot force a proxy's separate origin connection to use the same family.
- MIME exclusions accept at most 64 exact types without parameters or wildcards.
  Discovery policies affect parsed HTML resource discovery; explicit recorded
  resource URLs remain eligible. Cache/discovery feature switches still apply.
- Sequential compatibility is certified only against the selected profile and
  resolved C lifecycle declarations. External runners and custom trees remain
  review-only.

Source semantics for page timeouts, cache iteration aging, and HTML parsing were
checked against the [OpenText runtime preferences reference](https://admhelp.microfocus.com/lre/en/26.1-26.3/online_help/Content/PC/ui_runtime_settings_ip_preferences.htm).
The archive's numeric IP enum and two MIME/cache switches remain explicitly
unverified; the native fields can be configured during review.

Review fixes in this pass include evicting obsolete entries when a 200 or 304
response changes to an excluded MIME type, retaining teardown on replay iteration
setup failure, keeping deadline failures consistent with protocol evidence,
normalizing validated cache counts consistently, and refusing lifecycle proofs
for leaf-level scheduling overrides or unknown group controls. The public support
matrix also scopes the new policies to native HTTP, SOAP, and Java-over-HTTP.
Regression tests cover those paths.


Compatibility-pass verification:

| Check | Result |
| --- | --- |
| Actual archive reconversion, without contacting source targets | 56 mapped/consumed, 9 compatibility-only, 141 inactive; IP and idle-timeout decisions retained. |
| `go test ./...`, including converter, protocol, engine lifecycle, DSL, and settings API regressions | Passed on final code. |
| Focused HTTP race checks | Passed. |
| Dashboard TypeScript, lint, production build | Passed. |
| Dashboard unit suite | 4429 tests passed across 417 files. |
| Full browser cycle, dev-admin | 120 passed, 4 skipped, 8 failed; all 11 LoadRunner cases passed. |
| Full browser cycle, local-auth | 47 passed, 1 skipped, 1 failed; all 11 LoadRunner cases passed. |
| `git diff --check` | Passed. |

The dev-admin failures are the same eight previously observed in command-center
navigation, initial auth display, live metrics, two run-lifecycle flows, workspace
hierarchy, authoring scope navigation, and guided setup. They are outside the
runtime-settings import flow; no baseline comparison is claimed. The expanded
browser test verifies all six new HTTP controls through atomic apply and saved
DSL, while keeping original source values visible. Screenshots were inspected in both auth profiles.

The required dashboard `test:cycle` exited 1 because of those nine broader
workflow failures. The local-auth failure again occurs at the missing `Current
project` workspace button before the import step. All 22 LoadRunner browser
checks passed across the two profiles. The final Go suite and focused HTTP race
checks passed; no unresolved findings from this compatibility code review remain.
