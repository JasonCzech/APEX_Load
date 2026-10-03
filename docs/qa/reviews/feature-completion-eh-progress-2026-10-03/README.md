# Event Hubs processor progress review — 2026-10-03

Scope: the progress evidence portion of EH-2. Standalone bounded generation snapshots preserve ownership, exact delivered/checkpoint/tail sequences, opaque provider offsets, checkpoint populations/distributions/outcomes and source-monotonic freshness. They survive zero-action live windows, trusted group/peer projection, JSONL/Parquet, SQLite/PostgreSQL reload, reports and bounded native API/CLI queries. Tail management traffic requires explicit opt-in and negotiated capabilities.

Strict processor pause acknowledgement, recovery qualification and real Azure/emulator acceptance remain separate open slices. This record does not close EH-2, R2 or full feature completion. The operator-waived 10 GB import benchmark was not run.

## Review and fixes

Independent reviews and meaningful regressions corrected:

- Durable history omitted messaging JSON, and distributed live projection discarded messaging when business transaction aggregates were absent. Neither repair manufactures transactions or rates.
- Group performance selection cleared standalone progress for idle groups; trusted group projection now survives without completed transactions.
- A fresh archive review found admitted legacy processors that never emitted progress did not invalidate otherwise qualified peer backlog. Archive expectations now use controller final/private initial admission claims; remote legacy provenance cannot invent expectations. Mixed/all-legacy gaps explicitly remain unavailable, and released sources stop being expected.
- Unrelated HTTP intervals falsely implied missing processor evidence. Known nonprocessor sources are skipped; actual admitted missing/legacy processor peers remain explicit. Unknown uncollected archive sources conservatively mark coverage incomplete.
- Shared tail-query evidence could hide another client's permission failure. Only poll admission is shared; each identity queries and publishes its own frozen generations. Poll cancellation joins before consumer cleanup and suppresses late publication.
- A fresh provider review found acquired-but-unverified generations could still be evicted by terminal history, and an ownership loss followed by a fenced checkpoint could double-count loss. Provider retention now prioritizes both current ownership states and loss transitions occur once per acquired generation, while each fenced attempt remains counted.
- Historical rows could displace current ownership at row/byte limits. Current owned and unverified rows now precede history, with replay-stable lower-bound omission counts. New verified generations retire prior acquired-but-unverified owners.
- Numeric case aliases could overwrite validated populations with null. Canonical required numeric keys are enforced before decoding.
- Final engine sampling could miss concurrently accepted registrations. Registration closes before final inventory; unresolved cleanup retains incomplete evidence. Phase-only consumers no longer register unrelated producer-phase clients.
- Protocol reinitialization could race the pure metric snapshot reader. Atomic tracker publication removes reads of mutable runtime configuration and retains prior evidence during successful replacement.
- HA identity refresh could return stale local evidence, or a new project's evidence after RBAC resolved an older project. Getter identity validation and live/immutable archive-manifest project binding prohibit both cases before evidence publication.
- Dashboard/report parity now preserves exact provider offsets, nullable qualified backlog, checkpoint outcomes and duration populations. Measured zero remains distinct from zero observed samples; reports use actual mergeable histograms for approximate P95.

Per-row source capture timestamps remain authoritative provenance. Qualified backlog is as of that capture, with unqualified transit time and cross-host alignment. Missing, stale, empty, unconfirmed, fenced or incomplete evidence never becomes measured zero. These findings are advisory, not new stopping policies.

## Verification

Final gates passed on the reviewed source. The first full Go run exposed stale generated API contract and authorization snapshots for the new route; both fixtures were corrected and the full suite was repeated. Console logs retain outcomes with trailing whitespace removed. Verified gates:

- [Full Go suite](go-all.txt), including the API suite (433.764 seconds); [real-adapter generator suite](generator-real.txt) (43.655 seconds); [targeted API race](api-race.txt) (6.576 seconds); [documentation](documentation.txt) and staged diff checks.
- [Fresh independent repeat reviews](repeat-review.txt) found no additional actionable issues after the final fixes.
- [Provider/shared review](provider-review.txt), [default](provider-progress-default.txt), [real-adapter build](provider-progress-real.txt), [race](provider-progress-race.txt).
- [Engine review and tests](engine-review.md).
- [API targeted](api-default.txt), [API generation](api-generation.txt), [dashboard typecheck/lint/unit/build](dashboard-quality.txt) (4,760 tests), and affected Playwright workflows in [dev-admin](browser-dev-admin.txt) (four passed) and [local-auth](browser-local-auth.txt) (four passed). [Captured browser evidence](eventhubs-progress.png) was visually inspected.
- [Wire/storage normal](wire-storage-default.txt), [race](wire-storage-race.txt), [disposable PostgreSQL 16 migration/round-trip](postgres-history.txt). The private fixture and credential file were removed.

Local/tagged tests do not certify Azure identity, rebalance, crash recovery, epoch takeover or production service behavior. Full program work continues after this commit.
