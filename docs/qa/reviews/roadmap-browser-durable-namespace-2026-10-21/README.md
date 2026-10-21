# Durable replay namespace in non-HA controllers — 2026-10-21

Storage-backed controllers now acquire replay session leases whenever their store supports the durable coordination contract, even when HA execution leadership is disabled. Classic replay, browser replay and script debugging share this admission path. This prevents a restarted single controller from reusing a session ID still protected by an artifact receipt or cleanup tombstone.

A failed durable acquisition does not fall back to local ownership. Existing local-only adapters without that coordination capability keep their local behavior; they do not gain durable artifact publication. HA still requires the capability. Terminal retention, renewal, owner-fenced release and bounded operation deadlines reuse the existing lease lifecycle.

Cancellation after acquisition releases both the durable lease and the pending local entry. Non-HA cancellation reports its cancellation error rather than incorrectly reporting leadership loss. A lost renewal fences the active browser replay. An unjoined artifact writer remains reserved after process lease pruning; cleanup releases the namespace only after exact-generation physical removal is confirmed by the adapter.

## Validation and review

Go 1.26.10 full suite passed: 35,938 tests/subtests, 38 explicit skips and no failures. Targeted race validation passed across three repetitions (63 assertions, no skips). The original private HAR import stable fields match the previous baseline. Tested source, base commit and embedded dashboard assets were verified unchanged. Documentation checks passed for 316 active Markdown files. Tests cover cross-controller ownership and stale release, retained IDs, unjoined and published receipt protection after process pruning, cleanup tombstones, failed database admission, cancellation after acquisition and renewal-loss termination. Existing HA and stopped-worker lifecycle checks are included in targeted repeated race validation. Raw logs remain local under `/private/tmp/apex-browser-durable-replay/`.

Review fixed the non-HA cancellation error classification and added a regression for both local and durable release. A second review of the final ownership transitions and tests found no further issues.

## Remaining integration

This slice protects the durable namespace; it does not publish HAR/report bytes to an archive backend or enable recovered downloads. Backend publication, physical-writer proof, bounded cleanup and authenticated recovery reads remain open. Local macOS tests do not qualify multi-host HA or external storage failure lanes.
