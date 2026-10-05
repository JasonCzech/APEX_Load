# Shared analytical contract review — 2026-10-03

Scope: the metric/query catalog foundation for R1/NA-1, scalar/DuckDB derivation identity, existing native storage validation, typed analysis capabilities and generated API contracts. This does not close the full native analytics program.

Independent review found incompatible derived statistics could share identity and native dimension/filter options exceeded executable combinations. Fixes retain bounded full transformation lineage and expose mode/population-specific query profiles tested against the actual validators. Repeat review found summary/histogram operations advertised without executors; those entries were removed until implemented. Required public list properties now serialize arrays, including empty filter lists. A final consistency regression rejects unsupported operations for empty bucket inputs. Adjacent review also found canonical/wire unit ambiguity and epoch-less raw rates could acquire qualified lineage. Fixes preserve legacy wire units with explicit catalog mapping, retain reset-only raw rate display and exclude unknown epochs from the new qualified comparisons/statistics. Final independent repeat review reported no additional actionable findings after these fixes.

Validation:

- Full shared analysis and storage suites passed; the storage profile matrix exercises each metric/aggregation, every filter subset and allowed property selection.
- Shared analysis race suite passed after the final consistency fix.
- Native DuckDB series/aggregation/counter/summary regressions passed, including retained statistic lineage and independent output copies.
- Focused API capabilities/native/messaging regressions passed. Generated OpenAPI/TypeScript contracts were refreshed.
- Pinned Node 22.22.0 dashboard typecheck, lint, relevant native panel unit tests (19) and production build passed.
- The real dashboard native panel save/reopen workflow and controller catalog assertion passed under dev-admin and persisted local-auth profiles, using disposable controllers/stores.
- Final frozen `go test -p 1 -timeout 20m ./...` passed with a dedicated temporary `GOCACHE`, `GOMAXPROCS=2` and `GOMEMLIMIT=2GiB` ([complete log](go-tests.txt)). Documentation checks passed for 99 active Markdown files. The API suite took 1009.088 seconds in that cold-cache run; it completed without failures.

Gate history: the first mutable run encountered stale QA inventory and concurrent source changes; inventory was regenerated and the frozen pre-decoder run passed. A later final run passed API tests but failed remaining builds because files disappeared from the shared Go cache. The successful dedicated-cache repeat supersedes that environmental failure.

Limits: the public catalog is independent of capture/project capability. Provider qualification, typed summary/histogram ingestion, broader measured joints, native worker isolation and release/scale evidence remain separate open gates. The waived 10 GB import benchmark was not run.
