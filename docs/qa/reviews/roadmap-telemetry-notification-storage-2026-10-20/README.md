# Telemetry notification outbox storage — 2026-10-20

Storage groundwork for [TEL-B](../../../ROADMAP_IMPLEMENTATION_SPEC.md#tel-b--policy-evolution-and-complete-consumers). Runtime delivery and atomic archive-publication integration remain subsequent work; the existing notification path is unchanged by this slice.

Schema release 68 appends a new fingerprint without rewriting prior releases. Older binaries refuse to open this newer schema ledger; retain the database and use a compatible binary rather than deleting its migration history. The legacy claim marker also protects an already-running older controller during a mixed rollout.

The additive SQLite/PostgreSQL tables retain a versioned immutable announcement and independent delivery state for each destination. Identity binds project, run ID/incarnation, policy ID/revision, assessment digest and verdict. Enqueue locks and checks the terminal run in the insertion transaction. Repeated identical input replays; changed payload or destinations conflict. An empty destination set retains the original no-recipient decision, so later settings cannot redirect that outcome.

The complete serialized envelope is bounded to 64 KiB, including metadata and JSON escaping; each run admits at most 32 events and each event at most 64 destinations, matching the existing notification settings limit. Only opaque destination fingerprints are accepted. The eventual caller must supply a scrubbed projection and resolve fingerprints against current authorized configuration; this storage API does not sanitize arbitrary notification content itself.

Delivery uses fresh owner tokens, exact observed attempt numbers, bounded leases, eight attempts and capped exponential backoff. Expired delivery leases allow retry; they never prove that a receiver did not commit a request. Delivered, revoked and exhausted states leave the due scan. Old owners cannot complete replacement leases. Unrecognized future envelope versions remain intact and are not dispatched.

Legacy timestamp claims retain their unknown delivery outcome and suppress blind replay. New events atomically install a distinct legacy marker, which prevents older claim-based controllers from duplicating the same outcome while allowing new policy/assessment revisions in capable controllers. Changed run incarnations revoke active old deliveries without deleting their evidence. Test/project deletion cascades through events and deliveries; recreation cannot consume an old claim.

## Validation and review

- Applied-source full Go suite: 168 packages and 17,646 top-level tests passed, with 17,309 passing subtests. The API package passed 6,412 top-level tests in 877.438 seconds. Environment-specific skips remain recorded in the raw log.
- Applied SQLite/PostgreSQL outbox, upgrade and schema checks: 14 top-level tests and 46 subtests passed. The reviewed outbox race suite passed all seven default tests and 19 subtests.
- Generated test-store adapter freshness, generated telemetry acceptance inventory, documentation links/invariants and whitespace checks passed. This storage-only slice changes no embedded Help source or UI behavior; the preceding recovery slice's full dashboard/Help cycle remains the applicable unchanged cohort.
- The earlier full-suite failure (missing schema release and foreign-key bookkeeping) and separate API timeout are retained as superseded evidence, alongside the passing replacement run. Neither is relabeled as a pass.

[Evidence checksums](SHA256SUMS) cover the raw compressed logs, source hashes, commands and review history. The source hashes were checked against the exact installed files after validation.

The review fixed unsupported-version admission, obsolete rows starving the bounded queue, UTF-8 identity ambiguity, nil/empty recipient replay, a destination limit below the existing settings contract, and serialized-envelope expansion beyond the byte cap. Final review status: clean after the schema/foreign-key corrections and the final source, tests, generated adapter and evidence review.

The stored envelope and leases are internal groundwork. The next runtime slice must enqueue atomically with the exact archive publication, preserve a durable failure decision on capacity/projection failure, reuse the bounded dispatcher, resolve only unchanged authorized destinations, handle shutdown and partial delivery, and scrub all outbound projections. No external receiver exactly-once guarantee or deployed multi-host qualification is claimed. No user capture or external notification destination was used by these local storage tests.
