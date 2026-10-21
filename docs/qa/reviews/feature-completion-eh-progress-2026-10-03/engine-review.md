# Engine lifecycle progress review and verification

This slice publishes bounded local Event Hubs processor lifecycle evidence separately from action results. It does not add transaction or action observations. The engine injects the actual group identity, clears untrusted origins, preserves per-row source capture timestamps, and replaces cumulative evidence by source identity and revision. Active reporters remain registered through pending cleanup; only completed protocol teardown plus a `released` processor snapshot retires a reference. Final collector sampling records explicit incomplete coverage for unresolved cleanup and releases its remaining provider references.

Independent review and the final self-review corrected a phase-eligibility issue: at-once main VUsers register only when their main phase consumes; shared init and teardown protocols use their own phase. This prevents false reporter overflow in large producer main phases with a single lifecycle consumer. Independent review also corrected a final-sampling registration race: the collector now closes registration before capturing its final inventory. A deterministic blocked-capture regression verifies that late registrations cannot disappear from an accepted inventory. A final malformed-adapter review also contained panicking metadata and typed-nil adapters before registration can escape established lifecycle panic guards; bounded malformed coverage remains visible without retaining an unsafe provider. Source-owned history eviction and provisional generation replacement preserve cumulative loss coverage across replays. Shared merger priority was separately corrected so current ownership remains visible before bounded historical rows.

The meaningful regressions cover no-action idle windows, real engine pauses and scale-to-zero, final publication before a first tick, partial/malformed/panicking sources, teardown drain/release/quarantine, revision and capture replay ordering, deep-clone isolation, bounded provider references and retained bytes, producer-only and at-once phase exclusion, expected-source failure, and direct/closed/open/at-once execution. Zero-VUser at-once uses the existing explicit `AllowZeroVUsers` contract.

## Gates

All commands used the dedicated cache `/private/tmp/apexload-feature-completion-go-cache`, `GOMAXPROCS=2`, `GOMEMLIMIT=4GiB`, and `-p 1`.

- Focused `TestMessagingProgress*`: passed, 3.786 seconds, exit 0; log (`engine-progress-default.txt`).
- Targeted progress/resource/idle/final-publication race tests: passed, 6.811 seconds, exit 0; log (`engine-progress-race.txt`).
- Complete `pkg/engine` suite: passed, 36.785 seconds, exit 0; log (`engine-full.txt`).
- Final engine whitespace check: passed.
- Independent provider-agent engine review: clean after final registration, source identity, eviction, missing-source, group, phase eligibility, and malformed-adapter fixes.

This is controlled local lifecycle and propagation evidence. It is not live Azure qualification. The existing soft pause still gates between actions; cancelling and acknowledging background receive/tail quiescence belongs to the following strict-pause slice. No claim of full Event Hubs feature completion is made here.
