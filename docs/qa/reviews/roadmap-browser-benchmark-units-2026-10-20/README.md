# Browser benchmark memory units — 2026-10-20

This [BE-C slice](../../../ROADMAP_IMPLEMENTATION_SPEC.md#be-c--publish-measured-browser-limits) makes the existing Chromium target of less than 250 MB explicit as 250,000,000 bytes. It does not relax it to 250 MiB or raise the supported density. Five-repetition and reference-host requirements remain unchanged.

New `apexload.browser_benchmark.v2` artifacts retain raw byte values, nullable missing measurements, the observed process population, attempted snapshot count and partial/unavailable sample counts. Host capacity has byte and explicitly named MiB fields. Historical v1 artifacts remain unchanged; their `peak_rss_mb` and `memory_mb` values were binary MiB. The Go RSS benchmark similarly reports bytes and MiB and refuses unavailable snapshots instead of reporting zero.

The observed process population includes shared driver/browser costs. A sampled maximum is not a continuously measured peak, complete upper bound or independent per-VUser attribution. Concurrent sessions, process enumeration and partial reads need independent accounting before performance qualification. The [owning guide](../../../PERFORMANCE_BENCHMARKS.md#memory-units-and-measured-population) defines these limits.

## Validation and review

- Applied API and generated-contract checks: 7 top-level tests and 0 subtests passed.
- Applied native harness/units: 7 top-level tests and 15 subtests passed; one local benchmark iteration per declared Chromium profile verifies execution, not reference-host performance.
- Applied units race suite: 4 top-level tests and 15 subtests passed.
- Full Go regression: 168 packages, 17,677 top-level tests and 17,368 subtests passed, including the actual private ZIP import through the test-only overlay.
- The private ZIP retained its original hash and matching aggregate import result: HTTP 200, 803 requests, 476 actions and zero DSL issues. No traffic replay or converted-content persistence.
- API/TypeScript generation, inventory, dashboard typecheck/lint/Help units/build, both Help profiles, documentation checks and evidence hashes passed. Exact source/asset/controller cohorts and the distinct combined RSS/joint draft are retained in [the evidence bundle](SHA256SUMS).

Review corrected the API and owner guide's stale CDP-attribution label to the actual shared Playwright driver OS sampler and added an explicit denominator for partial/unavailable RSS sample counts. Final applied-source and staged review: no remaining actionable findings after the recorded unit, sample-denominator and process-population corrections.

One local iteration per Chromium profile verifies that the harness runs with the new units; it is not a five-run reference-host acceptance result. Linux/Windows/native reference and density qualifications remain pending, and no acceptance record is promoted.
