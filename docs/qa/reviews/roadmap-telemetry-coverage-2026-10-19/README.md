# Roadmap implementation: replay-safe telemetry coverage

Date: 2026-10-19. Scope: first bounded implementation slice of [TEL-A](../../../ROADMAP_IMPLEMENTATION_SPEC.md#tel-a--durable-windows-and-measured-end-to-end-age).

## Contract and scope

Coverage algorithm `telemetry_coverage.v3` admits only new, ordered windows into receipt-time coverage. Duplicate and late envelopes still contribute publication and loss evidence. Repeated collection identities cannot move an old observation onto a later receipt; the first valid terminal envelope retires the source without repeating its window. Older generations and older or unorderable epochs cannot retire the active source. A valid new epoch resets collection identity without bridging the restart gap. Source-epoch retention and interval retention remain bounded; omitted evidence makes coverage a lower bound.

Legacy inputs without collection identities retain receipt-window compatibility. Missing or incomparable identity after an identified collection supplies no proof of new data. Saved v2 records are unchanged; existing compatibility logic keeps different coverage algorithms separate. No RPC, REST or storage schema changes, policy-default changes, or history rewriting are required.

This is still receipt-time coverage. Durable source windows, recovery across controllers, qualified capture-to-render correlation, policy segmentation, reference-workload qualification, and the other revised-spec tracks remain open. Messaging and CloudTest outcome parity remain outside this spec.

## Validation

Retained logs normalize trailing whitespace only.

Base: `4f75f1ac4e5ed198995258658c3b476597a34828` (current `origin/main`, fetched before builds and the full API suite). Local macOS arm64, Go 1.26.9, Node 22.23.3. Go used a session-specific cache, `GOMAXPROCS=3` and `-p 1` to bound compilation resources.

- [Pre-fix regressions](evidence/regressions-before.log) reproduce duplicate/late collection inflation, stale retirement, unbounded coverage admission and algorithm compatibility before the implementation.
- [Targeted telemetry tests](evidence/telemetry-after.log): `go test -p 1 -timeout=20m ./pkg/api -run '^TestTelemetry' -count=1` passed before the retention review fix. The full suite and final race check cover that fix too.
- [Full Go suite](evidence/full-go.log): `go test -p 1 -parallel 4 -timeout=20m ./...` passed: 168 packages with tests and 10 without tests. This is the standard suite; environment-gated release qualification remains separate.
- [Help tests](evidence/help-tests.log): `npm test -- src/helpContent.test.ts src/contextualHelp.test.ts` passed all 34 tests, including catalog loading and help-link resolution.
- [Documentation check](evidence/docs.log): `python3 scripts/check-docs.py` passed for 250 active Markdown files. The help renderer's fragment/section ownership scan also found no errors.
- [Acceptance inventory](evidence/inventory.log): `APEXLOAD_UPDATE_TELEMETRY_ACCEPTANCE=1 go test -p 1 ./docs/qa -run TestTelemetryAcceptanceInventory` passed. The source inventory and exact race selector include all nine new regression tests.
- [Final dashboard build](evidence/dashboard-build-final.log): `npm run build` passed both TypeScript checks and bundling after the guide update; Vite reported the existing large-chunk advisory. Asset generation finished before the final Go race check.

[Final race matrix](evidence/race-matrix.log): the exact telemetry fault selector from `scripts/run-optin-suites.sh`, run with `go test -race -count=1 -p 1 -parallel 4 -timeout=30m -v`, passed for `pkg/api`, `pkg/distributed/generatorrpc` and `pkg/analysis`, including all nine new regressions. No races were reported. The log retains the complete command.

## Review

The first review found that the bounded epoch ledger also needs to propagate truncation into live and archived coverage. Fixed by taking its truncation flag in the same locked snapshot and preserving the existing lower-bound acceptance semantics. Regression coverage checks that missing retained evidence cannot become an exact below-floor failure. Added compatibility and jitter-gap tests for missing, incomparable and replayed collection identities.

The follow-up review checked bounded allocation, atomic snapshots, legacy identity compatibility, source ordering and retirement, replay-induced gaps, algorithm compatibility, evidence preservation, and the test inventory. No further production-code findings remained. The test helper was aligned with the lower-bound snapshot behavior; final race verification uses that helper and the rebuilt assets.

Final review: no findings remained after the retention fix and follow-up checks. No unrelated working-tree changes or generated dashboard bundles belong to this commit.

## Cleanup

Cleared the session-specific Go build/test cache (about 6.3 GiB, 8 KiB of metadata remaining) and the checkout's disposable Vite caches after all validation. Docker's unused build-cache prune completed with 0 B reclaimed. Dependencies and deliverable build output were preserved; the final dashboard build is retained outside the commit. No agent-opened browser sessions remained.
