# Distribution and pending-joint evidence bounds — 2026-10-20

This [NA-B slice](../../../ROADMAP_IMPLEMENTATION_SPEC.md#na-b--bounded-joint-evidence-across-execution-entry-points) closes a pending-queue admission hole: a single publication exceeding the entire byte allowance is refused before useful earlier windows are evicted. Its omission increments the existing dropped-publication count used to mark joint archive coverage truncated. The 1,024-publication and 64 MiB limits are unchanged.

Duration-sketch cloning and protobuf distribution copies apply their existing 4,096-bin and 1,024-centroid validity limits before retaining oversized arrays. Oversized evidence becomes unavailable, not a shortened valid distribution or measured zero. Source inputs remain unmodified; valid boundary copies remain independent. The [owning analytics contract](../../../NATIVE_ANALYTICS.md) retains the distinction between bounded copying/retention and the broader category, execution-entry-point and recovery work still pending.

Received joint protobuf publications now pass cardinality, canonical-key, projection, scalar and distribution-shape checks before retention. The 64 MiB joint-field encoded budget includes protobuf tags and length prefixes. Malformed joint evidence remains explicitly unavailable; independent action counters survive. Omitted tuples remain incomplete even when a peer omits that flag. This is a bound on retained joint fields, separate from transport limits and the pending queue.

## Validation and review

- Applied engine/gRPC race checks: 22 top-level tests and 26 subtests passed, including wire-envelope admission, copy limits and compatible fallback distributions.
- Applied joint API checks: 25 top-level tests and 0 subtests passed, including queue bounds, retained earlier windows and visible omission accounting.
- Full Go suite: 168 packages, 17,684 top-level tests and 17,385 subtests passed. All production code came from the installed checkout; the only external overlay supplied the private HAR regression.
- The attached ZIP retained its original hash and matching aggregate import result: HTTP 200, 803 requests, 476 actions and zero DSL issues. No replay or converted-content persistence.
- Generated analytics inventory, documentation links, both Help profiles and evidence checksums passed. No dashboard source or embedded Help content changes in this slice; the committed assets were verified by hash and exercised through rebuilt controllers.
- Exact source/asset/controller hashes, failed original-copy regressions, passing draft checks and the distinct combined RSS/joint draft are retained in [the evidence bundle](SHA256SUMS).

Identical focused regressions against the preceding source reproduce the unbounded distribution copies. Failed baseline evidence is retained separately from passing patched checks. Final applied-source and staged review found no remaining actionable findings after checking exact encoded-envelope accounting, canonical tuple/shape admission, explicit source refusal, copy independence, fallback compatibility and queue-loss accounting.

These checks do not establish reference-host analytics performance or qualify unavailable Linux/Windows runners. No broader NA requirement is promoted to complete.
