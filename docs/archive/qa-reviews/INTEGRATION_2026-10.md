# Completed integration implementation reviews — October 2026

Status: Historical. Consolidated on 2026-10-19. These are completed, scoped implementation reviews, not closure of their parent requirements. Original dates, failed checks, pending acceptance and qualification limits remain below. Evidence files remain at their original paths. Use the [review index](../../qa/reviews/README.md) for outstanding work and other records.

## Records
- [Committed-history publication review, 2026-10-04](#committed-publication-2026-10-04)
- [Composed review and fixes — 2026-10-05](#feature-completion-composed-review-2026-10-05)

<!-- Original record: docs/qa/reviews/committed-publication-2026-10-04/README.md; evidence retained in docs/qa/reviews/committed-publication-2026-10-04/ -->
<a id="committed-publication-2026-10-04"></a>

## Committed-history publication review, 2026-10-04

Status: Publication reviewed; full implementation program remains paused.

This merge publishes the 13 completed feature commits through `d8d6c1ca311d395e677a5ced3d5981c3685dc68a` alongside the 20 newer main commits through `f2ebdfeb2076e76dedba944021e057079f05f0ed`. The isolated checkout excludes the paused, uncommitted identity and disclosure drafts.

<a id="committed-publication-2026-10-04--composition-corrections"></a>

### Composition corrections

- Preserve published protobuf field 43 for run correlation origins; assign runtime resource claims to field 44. Preserve published database releases 27–30 and append feature releases 31–34.
- Preserve provider provenance alongside analytical population/derivation metadata, and refuse provider attribution at generic ingestion and native worker boundaries.
- Keep durable management polling, sealed credentials, asynchronous provider collection, shared collector retry limits and complete paginated job discovery together.
- Limit the 32 management assignment slots to typed management jobs. Preserve 256 concurrent generic jobs, bounded pages, legacy rotation and required management admission.
- Preserve both provider and management archive artifacts, accepted counts, reconciliation and explicit incomplete coverage. Apply the shared evidence budget without failing the performance archive.
- Respect actual caller deadlines during collection-cache cleanup, including when context timer publication lags. Preserve the bounded busy-source retry for live callers.
- Restore binary upload API inference after the owned-reader refactor; regenerate Swagger and TypeScript contracts.
- Compose monitoring controls and provider configuration, chart export selections, sidebar layout, and bounded editor parsing. Fix the wrapped-script range lookup using the completed syntax tree.

Independent source review and re-review found no further actionable publication issues after these corrections.

<a id="committed-publication-2026-10-04--actual-verification"></a>

### Actual verification

| Gate | Observed result |
| --- | --- |
| Full Go package lane | 123 packages passed; API reported four composition findings. Every reported API finding passed its corrective repeats below. The entire package lane was not rerun after corrections. |
| Corrected API checks | Default 37/37 and race 37/37 selected top-level tests passed; no skips or source/embedded-input changes. |
| Paging and management capacity | Default 10/10 and race 10/10 passed, including actual 256-job pagination and mixed-family admission. |
| Final PostgreSQL checks | 18/18 passed, no skips; published ledgers, migrations, foreign keys, management/native/upload ownership and concurrent initialization covered. |
| Core race lane | Analysis, default DuckDB, engine, generator RPC and collector passed. Storage reported the paging regression; corrective race checks passed. |
| Dashboard | Typecheck, lint, all 5,394 unit tests across 501 files and build passed after the parser correction. Typecheck/build repeated after the final generated contract. |
| Browser workflows | 30 dev-admin and 30 local-auth workflows passed against freshly built local controllers. |
| Native DuckDB | Complete `analytics_duckdb` package suite passed on local darwin/arm64 with 512 MiB memory and 128 MiB spill settings. |
| Generated contracts | DSL/API generation passed; final OpenAPI upload generation and freshness checks passed. |

Go lanes use the pinned 1.26.5 toolchain, isolated task cache, bounded parallelism and an API compile profile without DWARF. Native qualification on other platforms and large-import benchmarks remain part of the paused program.

[Raw receipts and logs](../../qa/reviews/committed-publication-2026-10-04/raw/api-final.json) retain both the original broad failures and the successful corrections. [Full Go output](../../qa/reviews/committed-publication-2026-10-04/raw/full-go-before-corrections.log), [core race output](../../qa/reviews/committed-publication-2026-10-04/raw/core-race-before-corrections.log), [corrected API default](../../qa/reviews/committed-publication-2026-10-04/raw/api-final-default.log), [corrected API race](../../qa/reviews/committed-publication-2026-10-04/raw/api-final-race.log), [PostgreSQL](../../qa/reviews/committed-publication-2026-10-04/raw/postgres-final.json), [browser profiles](../../qa/reviews/committed-publication-2026-10-04/raw/browser.json), [dashboard](../../qa/reviews/committed-publication-2026-10-04/raw/client-corrected.json), and [native analytics](../../qa/reviews/committed-publication-2026-10-04/raw/native.json) describe the executed scope and resource profiles.

<!-- Original record: docs/qa/reviews/feature-completion-composed-review-2026-10-05/README.md; evidence retained in docs/qa/reviews/feature-completion-composed-review-2026-10-05/ -->
<a id="feature-completion-composed-review-2026-10-05"></a>

## Composed review and fixes — 2026-10-05

The scoped authoring, private scenario source-admission, retained browser resource-inspection, document-finality, native HAR ownership and dashboard-loading changes passed their composed source/test qualification. Independent repeat reviews found no further actionable code findings after the fixes. This record does not close the broader feature-completion program.

Authoring preserves complete settings and binding arrays, ordered group/source identities, portable historical defaults and queued-copy provenance. Private source-aware admission checks exact script incarnation/content and publication presence/raw-byte claims within the storage transaction. Public scenario ETags, editor conflict recovery and the rest of AUTH-1–AUTH-3 remain open. Resource inspection remains authorized, bounded and explicitly opened; native HAR qualification remains limited to the scope in [the ownership guide](../../guides/BROWSER_HAR_WORKING_OWNERSHIP.md).

The composed affected API cohort passed 115 named tests and 230 passes across two race-detector runs. [Ten actual PostgreSQL checks](../../qa/reviews/feature-completion-composed-review-2026-10-05/evidence/composed-postgres-affected-1.json) and the [nested pinned SDK race contract](../../qa/reviews/feature-completion-composed-review-2026-10-05/evidence/composed-owned-sdk-contract-race-1.json) passed separately. Their named scopes do not replace broader database, provider or platform acceptance. API, M1 and dashboard contracts are fresh. The [repeat composed source review](../../qa/reviews/feature-completion-composed-review-2026-10-05/evidence/final-composed-api-authoring-source-review.json) preserves exact stable input hashes.

The first full Go run failed only two stale route-contract tests: the new resource route lacked an access golden and the modular archive count remained 17. The test-only fix adds the exact golden, count 18 and explicit durable resolver, viewer policy, service scope, middleware, timeout and handler assertions. [Independent applied-test review](../../qa/reviews/feature-completion-composed-review-2026-10-05/evidence/route-contract-applied-independent-review.json) is clean; [focused verification](../../qa/reviews/feature-completion-composed-review-2026-10-05/evidence/composed-route-contract-focused-2.json) includes the new resource assertion. The [second full Go run](../../qa/reviews/feature-completion-composed-review-2026-10-05/evidence/composed-full-go-2.json) passed with native browser tests required and unchanged source/embedded inventories. Its [final metadata and skip ledger](../../qa/reviews/feature-completion-composed-review-2026-10-05/evidence/full-go-2-final-green-skip-ledger.json) keeps default-profile and acceptance omissions explicit. The [original failed run](../../qa/reviews/feature-completion-composed-review-2026-10-05/evidence/composed-full-go-1.json) and raw output remain retained.

The [full dashboard cycle](../../qa/reviews/feature-completion-composed-review-2026-10-05/evidence/composed-dashboard-full-cycle-2.json) passed typecheck, lint, 6,127 unit tests across 531 files and a fresh embedded build. Dev-admin passed 257 workflows with five skips; local-auth passed 197 with one skip. [All four restricted-persona cases skipped under dev-admin passed under local-auth](../../qa/reviews/feature-completion-composed-review-2026-10-05/evidence/local-auth-skip-closure.json). Both profiles skipped the existing optional private LoadRunner archive acceptance case because its fixture was not configured; that acceptance remains unqualified. Temporary runner trace directories retired after the completed run.

Dashboard review fixes defer the resource inspector with a local load-error boundary, move access-only styles to their six consumers, and separate API service assembly from consumer hooks. Explicit finite chunk boundaries remove the reverse application-entry dependency from Scripts and Execution. [The final bundle candidate](../../qa/reviews/feature-completion-composed-review-2026-10-05/evidence/composed-dashboard-bundle-candidate-5-bundle-result.json) passes all limits: shell 183.89 KiB, Scripts 168.93 KiB and Execution 369.26 KiB gzip. Individual-asset and route ceilings remain unchanged. Aggregate limits are calibrated against the retained committed-source baseline and measured composition; neither lazy-route static closure includes `index.html`.

Historical failures remain visible. The first dashboard cycle passed its units/build but its unfinished browser phase was deliberately cancelled after the separate bundle gate reported nine violations; exit 143 is not a passing cycle. The [owned cancellation receipt](../../qa/reviews/feature-completion-composed-review-2026-10-05/evidence/dashboard-cycle-1-owned-cancellation.json), original bundle failure, intermediate candidates, loading-test/typecheck failures and their fixes remain beside the final receipts. The first full Go run's [skip ledger](../../qa/reviews/feature-completion-composed-review-2026-10-05/evidence/full-go-1-final-red-skip-ledger.json) remains explicitly associated with that failed run.

Each gate records exact repository source and embedded inventories, command completion, raw-output hash and disk observations. The inventories do not claim to cover every transitive native dependency. Default-profile native reconciliation, opt-in HA/performance/provider tests and missing customer fixtures are not promoted by a passing default suite. Private HAR content and target replay are not retained here. The analytics-engine branch is preserved; native scale/platform/restart acceptance, general HAR crash cleanup, aggregate active-writer/RSS quotas, managed generator transfer, native/synthetic archive exports and the remaining AUTH/BE/BI/TEL/NA requirements stay open.
