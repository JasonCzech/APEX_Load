# Execution shutdown joins — 2026-10-21

A local execution driver can publish terminal status before finishing its archive writes. Previously, non-HA shutdown could return while those writes still used the archive directory. CI exposed this through the CI idempotency fixture: its runs outlived shutdown and a later temporary-directory cleanup failed.

Run drivers now retain physical ownership through backend completion and panic finalizers. Shutdown closes admission, requests stops, preserves the existing HA handoff, then waits for entered drivers and terminal retries before dependent component teardown. An expired shutdown deadline reports an error without erasing an outstanding worker. A driver scheduled after shutdown is refused; an admitted INITIALIZING run is synchronously aborted by the existing stop transition.

The 10,000-request iteration accounting regression keeps its exact request count and now allows two minutes on shared race-detector hosts. Its previous 30-second deadline tested incidental runner throughput rather than iteration completeness.

## Validation and review

Go 1.26.10 full regression suite passed: 35,931 passing tests/subtests, 38 explicit skips and no failures. Applied-source focused race checks passed across three repetitions (36 passing tests/subtests). The original private HAR import stable fields match the previous baseline. Tested source hashes and base commit remained unchanged. The isolated prototype also passed admission, blocked-finalizer, deadline, HA handoff and acknowledgement checks before application.

Review checked admission/counter lock ordering, panic/defer ownership, shutdown deadline behavior, terminal retry registration and continue-policy HA handoff. The retry wait conservatively includes registered workers sleeping for at most the existing one-second retry delay; it does not discard an in-flight operation to shorten shutdown. Raw logs remain local under `/private/tmp/apex-shutdown-join/`.

Final source and test review found no further issues after validation. Documentation and whitespace checks passed before commit.
