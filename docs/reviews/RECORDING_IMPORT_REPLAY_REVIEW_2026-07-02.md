# Recording, Import, and Replay Review - 2026-07-02

## Scope

This review covers the APEX Load authoring path from browser or HTTP recording
through import, parameterization, replay, and dashboard review:

- HTTP MITM proxy recorder in `pkg/recorder`
- Browser recorder lifecycle and bridge APIs in `pkg/api`
- Browser recorder runtime in `pkg/recorder/browser`
- HAR, Chrome Recorder, and Playwright import in `pkg/convert`
- Correlation, parameterization, and repair in `pkg/convert/correlate`
- HTTP and browser replay engines in `pkg/protocol`
- Selector self-heal in `pkg/selector`
- Redaction in `pkg/scrub` and runtime evidence redaction in `pkg/engine`
- Recording Studio and import review dashboard surfaces under `dashboard/src`

The attached remediation program reports 272 findings and 108 feature gaps
after multi-area review and adversarial verification. The referenced raw corpus
file, `scratchpad/raw2.json`, was not present in this workspace when this report
was created, so this document records the verified phased program and the
implementation status from the available attachment.

## Assessment

The subsystem has a sound architecture: nonce-gated browser controls,
production fail-closed posture for headed browser recording, ranked selectors,
self-heal paths, HAR parse diagnostics, and real import/replay foundations.
The dominant risk is not hard failure; it is plausible but wrong output:
scrubbed values becoming correlation candidates, replay extractors that cannot
round-trip, recorder event streams that lose common user flows, and exported
artifacts that expose secrets unless the caller opts into scrubbing.

## P0 Remediation Program

### Security

- Bind the MITM proxy to loopback by default.
- Refuse proxy startup in production unless explicitly enabled.
- Require proxy authentication for wide binds unless unauthenticated mode is
  explicitly accepted.
- Reject loopback, link-local, and private targets by default, including
  DNS-rebinding-safe dialing.
- Make HAR export scrubbed by default and label every export with sensitivity.
- Gate raw HAR export behind editor, project-admin, or system-admin access when
  auth is active, and audit raw export attempts.
- Extend scrub coverage for single-entry HAR roots, HAR sensitivity markers,
  WebSocket payloads, structured bodies, high-entropy tokens, and runtime
  evidence text.
- Ensure baseline and source-review artifacts labeled scrubbed are actually
  scrubbed before persistence.

### Silently Wrong Scripts

- Run HAR correlation and diff on raw values, then scrub only persisted review
  artifacts.
- Exclude `[REDACTED]` from correlation candidate selection.
- Make malformed HAR entries skip with indexed warnings instead of failing the
  whole import.
- Keep HAR accounting honest for filtered, skipped, and converted entries.
- Strip replay-hostile headers, HTTP/2 pseudo-headers, and recorded cookies at
  import so runtime state owns cookies.
- Make static-asset filtering method-aware.
- Validate extractor source/type combinations during DSL validation.
- Preserve JSON numbers during extraction with `json.Decoder.UseNumber()`.
- Honor `max_redirects <= 0` as "return the redirect response".
- Decode zstd responses and avoid decoding empty encoded bodies.
- Preserve Salesforce URL-encoded correlation conversion when a generic HAR
  import has already templated the draft.
- Apply HTTP `clear_cookies_per_iteration` at iteration boundaries.
- Capture pending text-entry fills before Enter/Tab navigation or blur without
  duplicating the subsequent change event.
- Suppress browser-synthesized zero-detail clicks after Enter keyboard
  activation so replay does not double-submit.
- Keep newly retyped fills from merging into excluded or deleted recorder steps.
- Record proxied WebSocket frames at proxy-read time so fast echo responses do
  not reorder sends and receives in generated replay scripts.
- Preserve browser recording URL fragments, section pre-step transaction
  wrappers, step-range boundary transaction markers, split-transaction starting
  pages, popup switch-backs, and stable WebSocket payload match hints.

## P1 Remediation Program

- Wire browser device, orientation, scale factor, and network emulation through
  replay runtime configuration.
- Reset or watermark browser WebSocket observations per action or iteration.
- Health-check pooled browsers before reuse.
- Measure browser action duration independently from slow-motion and evidence
  capture overhead.
- Honor browser wait, dialog, frame, touch, and window-switching semantics.
- Surface runtime HAR capture artifacts in run results.
- Complete HTTP replay fidelity work for proxy/no-proxy, OAuth clients, SSE
  truncation, shaping, and traceparent sampling controls.
- Cache or disable selector self-heal during load runs and emit explicit
  self-heal metrics.

## P2 Remediation Program

- Add browser recording session list, delete, retention, and janitor behavior.
- Preserve late artifacts on force-stop and avoid orphaned Chromium processes.
- Support post-stop step curation.
- Replace heavy polling in Recording Studio with incremental or WebSocket
  updates.
- Surface replay preflight results, upload progress, converter knobs, and
  replay-repair failures in the import review UI.
- Add Playwright/Chromium installation probes and safer platform detection for
  record-agent downloads.
- Persist recording baselines at every conversion seam so correlation diagnosis
  is reachable.

## P3 Backlog

- Broaden recorder capture for hover, contenteditable, right-click, drag,
  scroll, ARIA keyboard interactions, and multi-select.
- Add replay actions for navigation history, desktop scroll, network response
  waits, downloads, locale/timezone/geolocation, browser HTTP credentials,
  client certificates, and proxy controls.
- Add generic value-shape correlation, custom rules, deeper inline-JS/meta/data
  extraction, occurrence controls, and apply-then-verify repair loops.
- Expand import support for non-JavaScript Playwright codegen, HAR page
  grouping, host filters, per-entry disposition manifests, and external-format
  export.
- Expand HTTP parity for NTLM/Digest/Kerberos, cache emulation, richer JSONPath,
  OAuth refresh, multipart re-encoding, charset conversion, and HTTP/3.

## Implementation Status In This Pass

- Proxy recorder hardening landed with startup validation, loopback default,
  auth token support, and SSRF-resistant dialing.
- HAR import now correlates raw values before scrubbed persistence, skips bad
  entries with indexed diagnostics, strips cookies and replay-hostile headers,
  and reports skipped entries.
- Correlation suggestions now avoid Set-Cookie attributes, emit replayable
  Location header regex extractors, target XML attributes correctly, and reject
  redacted sentinel values.
- HTTP replay now handles JSON number preservation, redirect-response returns
  when redirects are disabled, zstd decoding, empty encoded bodies, and the
  `json_path` alias.
- DSL validation now rejects unsupported extractor source/type combinations.
- HAR export is scrubbed by default for proxy and browser recorder exports, with
  explicit raw export audit and role gating.
- HTTP `clear_cookies_per_iteration` is now enforced between iterations.
- Recorder conversion now applies only high-confidence correlations
  automatically, keeps lower-confidence candidates for review, de-duplicates
  generated correlation names, preserves cross-origin absolute URLs and query
  strings, and avoids silently dropping HTTP actions from mixed HTTP/WebSocket
  captures.
- Recorder input parameterization now seeds password-like fields with
  placeholders and metadata flags instead of captured secret values.
- Correlation ruleset application now handles recorder baselines whose
  observation IDs are zero-based script action indexes.
- Scrub-on-write now covers WebSocket HAR payloads, XML/SOAP fields, multipart
  secret parts, JWT/high-entropy bare tokens, and runtime error, console, DOM,
  and page evidence before metrics/sinks/archive storage.
- Browser recorder keyboard capture now records text-entry values before
  Enter/Tab transitions, suppresses duplicate blur fills and synthetic
  Enter-click steps, preserves retyped fills after undo/exclude curation, and
  serializes in-page event delivery so pre-key fills cannot arrive after their
  Enter/Tab `press_key` steps.
- Proxy WebSocket frame capture now preserves client-send before server-echo
  ordering under fast bidirectional traffic.
- Browser recorder conversion now preserves SPA fragments, assigns pre-step
  markers to the first step's section, keeps transaction wrappers around
  trimmed step ranges, starts split transaction scripts from the page active at
  transaction start, strips non-JSON WebSocket transport prefixes from match
  hints, and records switch-back steps when returning from a popup to an
  earlier window.
- Browser replay now evicts disconnected pooled browser instances, honors
  `timeout_ms` on selector/target-only `browser_wait`, clears stale WebSocket
  observations on navigation/close, advances WebSocket wait/assert watermarks
  after matched frames, and bounds one-shot dialog policies with `wait_ms` so
  missing dialogs fail clearly instead of arming an unrelated future dialog.
- HTTP replay P1 fidelity now honors custom `no_proxy`/bypass rules, routes
  OAuth token source actions through the configured HTTP client/proxy/TLS stack,
  accepts string `expires_in` token responses, fails prematurely truncated SSE
  streams instead of treating partial events as success, allows traceparent
  auto-generation to be disabled, amortizes simulated network latency once per
  socket direction while preserving per-byte bandwidth shaping, and lets
  explicit status assertions bless recorded 4xx/5xx responses.
- Selector self-heal now preserves role/text candidates in fallback metadata
  instead of dropping them when a lower-ranked CSS/XPath selector becomes the
  primary, memoizes healed selectors per action identity during a run, exposes
  `browser_self_heal`/`self_heal` modes, limits healing to selector-timeout or
  not-found failures, counts only real retry attempts after successful probes,
  emits `selector_self_heal_metrics` evidence, and applies the same healing path
  to wait, tap, long-press, swipe, drag, upload, and keyed element actions.
- Browser replay runtime configuration now applies device scale factor,
  orientation-aware viewport/screen sizing, device-profile network presets, and
  Chromium/CDP network emulation for latency and bandwidth while retaining route
  latency fallback for non-CDP engines. Browser action duration now excludes
  slow-motion delay and evidence capture, and browser process table scans are
  skipped unless client metrics are enabled.
- Browser replay correctness now keeps popup pages registered without
  auto-activating them, supports numeric `browser_switch_window` targets,
  follows nested frame selector paths, uses Playwright's native tap for
  `browser_tap`, and records manual HAR sessions only when an action explicitly
  requests capture via HAR params.
- Proxy recorder fidelity now timestamps HAR/request records at request start,
  computes think time from the previous response end and places replay waits on
  the previous action, reports raw forwarded response byte sizes while keeping
  decoded captured bodies for correlation, forwards full non-streaming bodies
  while retaining only the configured capture prefix, preserves multipart
  request uploads as full replayable base64 bodies within the governed capture
  cap, strips WebSocket permessage-deflate negotiation, caps peer-declared
  WebSocket frame allocation, applies WebSocket tunnel idle deadlines, and
  closes active WebSocket tunnels when their recording session stops.
- Browser recording lifecycle recovery now exposes a stopped/live session list,
  deletes terminal sessions with conflict protection for live recordings,
  provides a retention prune primitive for old stopped artifacts, publishes the
  new lifecycle routes through generated API docs, tears down the recorder
  backend on forced stops, recovers late graceful-stop HAR/storage artifacts
  after a force-stop snapshot, supports post-stop step curation for
  exclude/mark/delete-last/promote-selector operations against retained
  artifacts, and treats browser step-mark PATCH bodies as partial updates so
  omitted mark flags are not clobbered.
- Browser recorder availability responses now include stable machine-readable
  unavailability codes, including `browsers_not_installed` for Playwright/
  Chromium launch failures, and Recording Studio now keys the install
  affordance on that code instead of only parsing reason text.
- Browser replay progress review now treats stream inactivity as a waiting
  state instead of false completion, ignores duplicate terminal events, accepts
  reports nested under the session payload, and synthesizes a minimal report
  when terminal status arrives after observable replay events.
- Parameterization review now resets stale apply state when the reviewed script
  changes, supports re-applying after selection changes, and masks high-risk or
  secret-like samples in the dashboard.
- Import review UI now surfaces HAR replay preflight results, rejects invalid or
  oversized dropped files before upload, caps large JSON previews with a clear
  truncation note, and reports unsupported replay-repair patch types visibly.
- Browser recorder control-panel review now guards overlapping refreshes,
  keeps load errors visible while showing the last known session, exposes
  searchable full step history instead of the first 25 steps only, and reports
  unsupported replay-repair patch types.
- Recording Studio now requires an explicit managed Recording Agent platform
  choice when detection is inconclusive, shares the same platform detector as
  the client downloads menu, requests per-platform SHA-256 metadata, and sends
  platformed download requests for the local bridge package.
- Proxy recording conversion now normalizes already-stored script wrappers from
  `/recorder/{id}/convert`, avoids double-saving those stored scripts, disables
  visual-editor handoff when no nested DSL script is available, confirms before
  deleting stopped proxy sessions, and persists scrubbed recording baselines at
  the conversion seam for later correlation diagnosis.

## Round-2 Continuation Status - 2026-07-03

The follow-up remediation pass closed the highest-risk round-2 regressions and
several small P1/P2 backlog items:

- Controller startup no longer fails when the recording proxy is disabled by
  production policy; the proxy is logged as disabled while the API continues to
  serve.
- Recorder status/start responses no longer disclose the proxy auth token.
- Browser recording retention pruning is now scheduled by the server lifecycle
  and cancelled during shutdown.
- Browser recorder side-car HAR working files now use managed
  `apexload_browser_*.har.tmp` names under the configured HAR artifact
  directory (or `os.TempDir`) with `0600` permissions.
- WebSocket proxy tunnels use a longer browser-appropriate idle window instead
  of the short CONNECT deadline.
- Request capture keeps raw request headers in memory for correlation while
  preserving redacted public/persisted headers; baseline generation can use raw
  request headers before scrub-on-write.
- Browser recorder install is gated in production/default-off posture before
  any background Playwright install starts.
- XML/SOAP attribute correlation now emits node-set XPath expressions that
  replay extraction can evaluate, and encoded correlation forms are substituted
  when generated scripts contain form-escaped values.
- Salesforce form-field boundary extractors now use the actual form key, not
  the correlation rule name.
- `browser_tap` falls back to synthetic pointer/touch/click dispatch when the
  browser context is not touch-enabled.
- `browser_ws_assert` can validate the same frame found by a preceding
  `browser_ws_wait` without consuming the wait watermark itself.
- Selector self-heal probe failures now count against the heal-attempt budget.
- HAR WebSocket upgrade detection now reads raw HAR headers before stripping
  replay-hostile headers.
- JMESPath extraction now preserves large JSON numbers with `UseNumber()`.
- Enterprise Archive `NewUserPerIteration` now maps to HTTP
  `clear_cookies_per_iteration`.
- Browser recording transaction suggestions now anchor accepted transaction
  markers at the suggested step instead of click-time.
- Recording Studio's live browser step feed again caps the unfiltered live
  render to 25 rows while stopped review/search still sees the full history.
- Client download platform detection no longer maps every macOS User-Agent to
  `darwin-arm64`; ambiguous macOS agents require an explicit platform, and
  Linux arm64 is detected.
- The Postman environment dropzone now uses environment-specific validation and
  rejects collection-shaped files in the environment slot.
- `github.com/klauspost/compress` remains a direct module dependency so zstd
  support is explicit.

## Verification Requirements

- Run focused Go tests for every touched package.
- Run `go test ./...` before release qualification.
- For dashboard changes, run TypeScript checks, unit tests, and a browser
  verification flow that records, marks a secret, verifies, saves, and reloads.
- For end-to-end trust, import a HAR with a bearer token, session cookie, and
  reused numeric ID; confirm the token is not correlated as `[REDACTED]`, the ID
  is correlated with a round-tripping extractor, and persisted artifacts are
  scrubbed.
