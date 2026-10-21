# Retained replay read contract — 2026-10-21

This BE-B slice separates bounded status-report verification from complete HAR/report download verification. It prepares restart recovery without enabling controller publication or changing HTTP routes.

## Contracts

Status reads verify the published, joined receipt's source identity, report length and SHA-256 within the 16 MiB report bound. They do not read or claim current availability of the native HAR. Downloads continue to verify both complete objects before returning a reader. Missing native bytes remain unavailable.

Recovery accepts only one supported terminal session envelope with exact session/project/report identities and a nonzero completion timestamp. Unknown schema fields, trailing JSON and unscoped identities fail closed. Generic JSON numbers retain their precision. Detached reconstruction preserves the verified historical evidence without applying current field-truncation rules or importing local paths, leases or execution callbacks. It does not fabricate a managed test or register a running replay.

A durable-native snapshot cannot fall through the existing local-only synthetic HAR path. Ordinary local-only bridges retain their disclosed synthetic behavior. Controller recovered authorization, lifecycle cleanup and HTTP wiring remain separate work.

## Review and fixes

Review found that publication accepted a zero completion timestamp although recovery correctly refused it. Publication now rejects that value before receipt admission. Review also identified that using the ordinary snapshot clone would reapply current evidence truncation to a historical report; recovery instead clones the portable JSON while preserving numeric precision. Regression tests cover both findings, native fallback refusal, source/length/hash/state/cancellation failures, historical evidence preservation, invalid identities and unsupported envelopes.

## Validation

Targeted checks pass with 66 test/package events. Three race-detector repetitions of the read, backend, publisher and replay HAR checks pass with 389 events. The existing opt-in `TestBrowserReplayActualNativeHAR` test is skipped in each repetition; no real-browser qualification is claimed by these checks. Final review found no remaining issues, including low severity, and the reviewed source hashes remained unchanged through validation.

The preceding fixed-source full Go gate at `7cc82b4f1` passed with 36,177 test/package events and 56 skips. It includes the private supplied HAR ZIP import check: its compatibility summary is unchanged apart from randomized script identity. No private target requests were replayed. That gate covers the prior committed stack; these subsequent read-contract changes are covered by the targeted/race checks above, with the combined final-source gate still required before merge.

Documentation checks pass for 323 active Markdown files, whitespace checks pass, and all 311 embedded dashboard asset hashes are unchanged. Raw local evidence under `/private/tmp/apex-browser-durable-backend/` includes `read-targeted.jsonl`, `read-reviewed-race.jsonl`, `read-reviewed-source-hashes.json`, `read-docs.log`, `serial-full-go.jsonl` and `serial-private-har-summary.json`. Filesystem and file-backed object fixtures do not qualify cloud backends, native platform crash recovery or shared multi-host storage.
