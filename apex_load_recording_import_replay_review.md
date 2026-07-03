# APEX Load Recording/Import/Replay Review Memory

Canonical report: `docs/reviews/RECORDING_IMPORT_REPLAY_REVIEW_2026-07-02.md`

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
- P1/P2/P3 items remain as follow-on workstreams unless separately implemented.
- The referenced `scratchpad/raw2.json` corpus was not present in this
  workspace when the report was created.
