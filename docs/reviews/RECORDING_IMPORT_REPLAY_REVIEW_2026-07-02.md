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

## Verification Requirements

- Run focused Go tests for every touched package.
- Run `go test ./...` before release qualification.
- For dashboard changes, run TypeScript checks, unit tests, and a browser
  verification flow that records, marks a secret, verifies, saves, and reloads.
- For end-to-end trust, import a HAR with a bearer token, session cookie, and
  reused numeric ID; confirm the token is not correlated as `[REDACTED]`, the ID
  is correlated with a round-tripping extractor, and persisted artifacts are
  scrubbed.
