# Roadmap implementation: durable controller cursors

Date: 2026-10-19. Scope: the durable controller cursor and source-window recovery slice of [TEL-A](../../../ROADMAP_IMPLEMENTATION_SPEC.md#tel-a--durable-windows-and-measured-end-to-end-age), following [source collection provenance](../roadmap-source-windows-2026-10-19/README.md).

## Contract and recovery

The controller persists accepted collection high-water marks and their counted run/group effects in one versioned checkpoint, together with the publication/counter ledger, source requirements, receipt coverage and bounded source-window evidence. Storage checks the existing run incarnation, project/reference/creation identity, active lifecycle and execution lease term. Increasing revisions prevent a delayed write from replacing a newer snapshot. Database I/O is outside the runner mutex. Ordinary full-row saves cannot overwrite this controller-owned state.

Adoption reads durable state after securing generators and before resuming streams. It restores cursors and totals together; it does not combine a durable cursor with totals reconstructed from sampled history. A generation change preserves same-process collection deduplication while resetting the receipt-jitter bridge. Missing, incompatible or unreadable state keeps legacy unknown/lower-bound recovery. A failed write leaves the previous snapshot; a lost reply after commit is recovered from the database. Unknown payload versions remain untouched.

Source-clock windows retain collection/publication identity, source epoch, generation, admission-policy hash and available cumulative loss-counter readings. Classification remains unknown. At most 1,024 collections and 4,096 group intervals are retained; evictions are marked. The opaque checkpoint is bounded to 8 MiB; oversized writes are refused atomically. The source-epoch archive sidecar retains these windows with nanosecond precision. Its byte budget trims optional window detail before pre-existing source/counter evidence. Original generator archives and rate denominators are unchanged.

SQLite and PostgreSQL schema release 67 adds two private columns to the existing test row. The primary-key lookup needs no new index; deletion/retention follows the test row. Existing rows start without evidence. A binary whose schema ledger predates release 67 refuses that newer database under the existing downgrade guard; restore a compatible backup for a schema rollback. The application rejects unknown checkpoint payload versions without inventing measured zero or replacing them.

Receipt coverage remains `telemetry_coverage.v3` and adopted coverage remains a lower bound. Persisted source windows do not qualify cross-host clocks, recover observations never committed, prove dashboard delivery or establish an end-to-end SLA. Policy/cadence segmentation, qualified source-clock coverage, capture-to-presentation correlation and reference-workload acceptance remain open. Messaging and CloudTest outcome parity remain outside scope.

## Validation

Base: `8f120a25f` on `origin/main` `4f75f1ac4e5ed198995258658c3b476597a34828`; fetched and integrated before builds and the full API suite. Local macOS arm64, Go 1.26.9 and Node 22.23.3, with a task-specific Go cache. [Commands and environment](evidence/commands.txt) record the validation setup.

- [Focused API checks](evidence/api-targeted-final.log): checkpoint, receipt coverage, source ledger and adoption tests passed after the final fixes. [Focused storage checks](evidence/storage-targeted.log) passed.
- [Full Go suite](evidence/full-go-summary.log): `go test -json -p 1 -parallel 4 -count=1 -timeout=20m ./...` exited 0: 168 packages passed and 10 had no runnable tests. The summary retains all package outcomes and the SHA-256 of the raw JSON events. All seven new API tests and both new SQLite tests passed in this run.
- [Real PostgreSQL checks](evidence/postgres-final.log): upgrade from the previous schema, lease/incarnation fencing, concurrent revision ordering and schema fingerprint checks passed using PostgreSQL 16 in a disposable local container.
- [Acceptance inventory](evidence/inventory-final.log): freshness check passed. The nine new default-build tests are included in the maintained telemetry race matrix; `bash -n scripts/run-optin-suites.sh` passed.
- [Help tests](evidence/help-tests-final.log): 34 content and contextual-help tests passed. The renderer fragment/section-ownership scan completed without errors.
- [Dashboard build](evidence/dashboard-build-final.log): both TypeScript checks and Vite bundling passed. The existing large-chunk advisory remains. Asset generation completed before the full Go suite and race matrix; generated bundles are excluded from the source commit.

The [final telemetry race matrix](evidence/race-matrix-final.log) passed for `pkg/api`, `pkg/distributed/generatorrpc`, `pkg/analysis` and `pkg/storage`, including all nine new default-build tests. No races were reported. The exact maintained selector ran serially with `GOGC=20`, `GOMAXPROCS=2`, `-p 1` and `-parallel 4`. Its third-party `go-m1cpu` compiler warning is retained. [Documentation checks](evidence/docs-final.log) passed for 252 active Markdown files. Retained text logs normalize trailing whitespace only.

Browser verification used the built controller and a disposable local database in both dev-admin and local-auth profiles. [Local-auth search](evidence/help-local-search-click.txt), [deep-link reload](evidence/help-local-reload.txt), the [CI cross-guide link and focus](evidence/help-final-linked-page.txt), and the [final dev-admin reload](evidence/help-final-devadmin-reload.txt) worked. The [760-pixel layout check](evidence/help-final-layout.txt) found no horizontal overflow and confirmed the corrected unknown/recovery wording. The [final screenshot](evidence/help-final.png) was visually inspected. The [browser session was closed](evidence/browser-close.txt).

The existing generation/clock-order assertion exposed an overly strict intermediate guard; [its failed run is retained](evidence/api-generation-review-failure.log), and the assertion was preserved when fixing the implementation. Two full-suite attempts were deliberately stopped for review fixes; their [hashes and interruption reasons](evidence/interrupted-attempts.txt) distinguish them from the successful final run.

## Review

Self-review covered atomicity, lease/generation/incarnation fences, concurrent write ordering, restart and takeover behavior, bounded retention, unknown/legacy data, archive budgets and unchanged measurement semantics.

Findings fixed during implementation and review:

- Preserve collection identity across a generation change without bridging the takeover gap. Persist generation and process-start identity so stale processes are rejected within a term, while a genuinely newer term still outranks another process’s clock. The existing `TestTelemetryCoverageNewEpochRestartsCollectionIdentity` caught an overly strict clock guard; its assertion was preserved.
- Stream installation must not replace a restored accumulator with an earlier copy after a terminal collection has already advanced its cursor. The new regression exercises that interleaving.
- Require version-1 totals and completeness flags explicitly. Missing fields, malformed boundaries, incompatible identities and unknown payload versions cannot become measured zero; read failures and unknown versions prevent replacement of retained evidence.
- Apply the same structural retention limits to checkpoint writers and readers. A ledger beyond those limits leaves its previous recoverable cursor/effects snapshot intact.
- Bound total retained group intervals as well as collection count. Keep optional window details out of live-summary copies, and trim those details before existing source/counter records when the archive sidecar’s byte allowance is tight.
- Correct earlier help wording that still described all durable recovery as open or all pre-adoption observations as lost. Preserve the distinction between recovered provenance and qualified source-clock coverage.

The final review round found no remaining issues after those fixes. It rechecked the tested implementation, test assertions, migration fingerprint, archive limits, adoption interleaving, maintained test selectors, help wording and staged file scope. `gofmt` and `git diff --cached --check` were clean. The staged evidence review also corrected a trailing blank line and explicitly included validation logs excluded by the repository's general log-file ignore rule.

## Cleanup and isolation

[Cleanup evidence](evidence/cleanup.log): task-specific Go/test and compiler caches plus worktree Vite caches were cleared, reclaiming 9.49 GiB. Unused Docker build-cache pruning reclaimed 0 bytes. The disposable PostgreSQL container/volume and owned browser/controller sessions were closed; existing service containers, dependencies and application data were preserved. The tested dashboard bundle is retained outside the commit.

[Isolation check](evidence/scope.txt): the primary checkout’s unrelated launch configuration and dashboard lockfile retain their original hashes. The commit contains only this slice’s implementation, tests, documentation and evidence.
