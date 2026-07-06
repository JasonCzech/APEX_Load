# APEX Load Recording/Import/Replay Review Memory

> Status: Historical review memory. This file is preserved for audit context
> and should not be treated as current product documentation.

Canonical report: `docs/archive/reviews/RECORDING_IMPORT_REPLAY_REVIEW_2026-07-02.md`

Use this memory entry when working on the recording, import, replay,
correlation, scrub, recorder bridge, or Recording Studio surfaces. The review
program prioritizes secret-safe artifacts and trustworthy replay drafts: raw
correlation before scrubbed persistence, no redacted sentinel correlations,
scrubbed-by-default exports, replayable extractor suggestions, robust HAR
accounting, and faithful iteration/browser replay behavior.

Current pass status:

- P0 proxy hardening, HAR import robustness, correlation replayability, HTTP
  replay fixes, DSL extractor validation, scrubbed export defaults, and HTTP
  per-iteration cookie clearing have landed with Go tests.
- Continuation fixes also landed for recorder high-confidence-only correlation
  auto-apply, cross-origin query preservation, mixed HTTP/WebSocket HTTP action
  retention, zero-based recorder-baseline ruleset apply, expanded scrub
  coverage for artifacts/runtime evidence, and placeholder seeding for
  password-like recorded input parameters.
- Browser recorder keyboard fidelity fixes landed for Enter/Tab fill capture,
  duplicate blur-fill suppression, synthetic Enter-click suppression, and
  retyping after undo/exclude curation. In-page recorder events are now
  serialized before they cross the Playwright binding so pre-key fills cannot
  arrive after their Enter/Tab `press_key` steps.
- Proxy WebSocket frame capture now records at read time to preserve
  client-send before fast server-echo ordering in generated replay scripts.
- Browser recorder conversion fixes landed for SPA URL fragments, pre-step
  marker sectioning, step-range transaction boundaries, split-transaction
  starting pages, popup switch-backs, and stable WebSocket wait match hints.
- Browser replay P1 fixes landed for disconnected pooled-browser eviction,
  selector/target-only wait timeout handling, WebSocket observation reset and
  consumption watermarks, and bounded one-shot dialog policies.
- HTTP replay P1 fixes landed for custom `no_proxy`/bypass handling, OAuth
  token requests on the configured HTTP client/proxy/TLS stack, string
  `expires_in`, SSE timeout failure semantics, traceparent opt-out, amortized
  network-shaping latency, and explicit status assertions for recorded 4xx/5xx
  responses.
- Selector self-heal P1 fixes landed for role/text fallback preservation,
  per-action healed-selector memoization, configurable self-heal modes,
  selector-timeout/not-found gating, retry/probe metrics evidence, and broader
  selector-action coverage.
- Browser replay runtime P1 fixes landed for device scale/orientation context
  options, device-profile network preset resolution, Chromium/CDP network
  latency/bandwidth emulation, non-CDP route-latency fallback, action-duration
  accounting that excludes slow-mo/evidence capture, and client-metrics-gated
  process scans.
- Browser replay correctness fixes landed for non-hijacking popup registration,
  numeric target window switching, nested frame selector paths, native
  `browser_tap`, and explicit manual HAR capture retention.
- Proxy recorder fidelity fixes landed for request-start timestamps, previous
  response-end think-time calculation, replay wait placement on the previous
  action, raw forwarded response byte-size reporting, bounded response capture
  while forwarding full bodies, replayable full-base64 multipart upload capture
  within the governed cap, success-only generated status assertions, WebSocket
  permessage-deflate stripping, oversized WebSocket frame rejection, WebSocket
  tunnel idle deadlines, and session-stop tunnel teardown.
- Browser recording lifecycle recovery fixes landed for stopped/live session
  listing, terminal-session delete with live-conflict protection, retention
  pruning for old stopped artifacts, generated route documentation,
  force-stop backend teardown, late graceful artifact recovery, partial
  step-mark PATCH semantics, and post-stop exclude/mark/delete-last/promote
  curation against retained artifacts.
- Browser recorder availability now includes machine-readable unavailability
  codes, including `browsers_not_installed`, when Playwright/Chromium startup
  failures should drive the install affordance. Recording Studio now uses that
  code directly while retaining older reason-text fallback behavior.
- P2 dashboard review hardening landed for BrowserReplayProgress inactivity and
  duplicate-terminal handling, parameterization review reset/reapply behavior
  with secret sample masking, import dropzone validation, HAR preflight
  visibility, capped large JSON previews, visible replay-repair errors, and a
  searchable full browser-recorder step history.
- P2 Recording Agent first-run handling landed with shared platform detection,
  explicit platform selection when needed, per-platform checksum display, and
  platformed managed downloads.
- P2 proxy recording conversion fixes landed for stored-wrapper normalization,
  no-double-save handoff, visual-editor gating when nested DSL is absent,
  stopped-session delete confirmation, and scrubbed baseline persistence at the
  `/recorder/{id}/convert` seam.
- 2026-07-03 continuation fixes landed for production-safe controller startup
  when the proxy is disabled, proxy-token response redaction, scheduled browser
  recording retention, managed private HAR working files, long WebSocket tunnel
  idle windows, raw/redacted request-header split for correlation and
  baselines, production-gated browser install, replayable XML attribute XPath,
  encoded-form correlation substitution, Salesforce form-key boundaries,
  synthetic non-touch `browser_tap`, wait-then-assert WebSocket matching,
  self-heal probe budgeting, raw HAR WebSocket-upgrade detection, JMESPath
  `UseNumber`, LR `NewUserPerIteration` to HTTP cookie clearing, anchored
  transaction suggestions, restored live-step render cap, safer download
  platform detection, Postman environment-file validation, and direct zstd
  dependency tracking.
- Remaining P2/P3 items are follow-on workstreams unless separately
  implemented.
- The referenced `scratchpad/raw2.json` corpus was not present in this
  workspace when the report was created.
