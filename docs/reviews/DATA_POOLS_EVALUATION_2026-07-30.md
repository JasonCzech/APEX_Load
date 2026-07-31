# Data Pools — Feature Evaluation (API + UX)

**Date:** 2026-07-30
**Scope:** Shared Data Pools end to end — REST API (`/api/v1/data-pools*`, scenario bindings), storage, runtime consumption (engine + distributed), importers, and every dashboard surface that touches pools.
**Method:** three parallel code-exploration passes (backend API, dashboard UX, runtime/importers) followed by direct source verification of every High-severity claim. File:line references were checked against the working tree on the date above.

---

## 1. Executive summary

**Verdict: solid foundation, not yet production-usable at scale, with two selection strategies that silently violate their documented contract.**

The control plane is genuinely well-built: per-row optimistic concurrency, hierarchical project→pool locking that prevents orphans, cross-project binding blocked at four independent layers, value-free audit events on every mutation, and Swagger/catalog/user-guide docs that match the 11 implemented routes. Runs snapshot pool contents at materialization time, so concurrent edits cannot corrupt an active test, and distributed sharding of pool rows across generators is provably disjoint (stride partition with namespace-aware VU IDs, covered by an overlap test).

What undermines it:

1. **There is no bulk data path.** Rows enter the system exactly one `POST` at a time and there is no CSV upload, export, or bulk insert. A 100k-row pool requires 100k HTTP calls. For a load-testing platform whose competitors treat CSV parameter files as the primary artifact, this is the single biggest gap.
2. **The UI is a prototype.** Rows are edited as raw JSON in a textarea, rendered as `JSON.stringify` single cells, silently truncated at 100 with the page length displayed as the total, with no loading/busy/success states, no rename, no schema editor, no pagination, no permission gating, and zero tests. Meanwhile the Script Editor's *script-local* data sources have a full grid editor with CSV import/export, search, sort, and auto-fixups — shared pools received none of it.
3. **"Unique" isn't unique — at either level.** Parameter-level `selection: "unique"` performs the identical per-VUser index walk as `sequential`, so two VUsers emit the same sequence. Pool-level `unique` pins one fixed row per VUser via modulo and silently duplicates when VUsers outnumber rows.
4. **Pools and scripts are connected by an unvalidated string.** The scenario binding alias must match `{{data.<alias>.<column>}}` tokens the script author typed by hand; nothing validates, hints, or autocompletes that relationship at authoring time (a server-side fit report exists but only surfaces after an explicit Validate).

Sections 4–6 enumerate the findings; section 8 is the recommended roadmap. A remediation change-set accompanying this report addresses Tier A (correctness) and the CSV import/export endpoints — see section 9.

---

## 2. What the feature is

- `SharedDataPool` / `SharedDataPoolRow` / `ScenarioDataPoolBinding` structs at `pkg/storage/storage.go:928-959`; SQLite tables at `pkg/storage/sqlite_schema.go:215-247`; implementation `pkg/storage/sqlite_datapools.go`.
- 11 HTTP endpoints in `pkg/api/handlers_data_pools.go`, registered via `pkg/api/server.go:781-793` (legacy mux + `accessPolicy` fallback, **not** `route_catalog.go`): pool CRUD, single-row CRUD (paginated list, 100/page cap), scenario binding list/replace.
- Bindings: `{pool_id, alias, access_mode, selection_strategy}` per scenario (`UNIQUE(scenario_id, alias)`), normalized by `normalizeDataPoolBindingIntentWithLegacy` (`pkg/api/scenario.go:488-537`) — alias required and case-insensitively unique, `access_mode` only `read_only` accepted, `selection_strategy ∈ {sequential, random, unique, pop}`.
- At test creation, `buildSharedDataSourcesLocked` (`pkg/api/testcreation.go:1815-1862`) loads **all** rows of each bound pool, flattens them into an inline CSV string via `inlineCSVFromDataPoolRows` (`:1895-1933` — columns = union of all row keys, sorted alphabetically; missing keys become empty cells), and appends `dsl.DataSource{Type:"csv", InlineData:…, Strategy: binding.SelectionStrategy, FirstRowHeader:true}` to **every** group's script.
- The engine has no pool concept. `DataProvider` (`pkg/engine/data.go`) serves `{{data.<alias>.<column>}}` per iteration; `ParameterRuntime` (`pkg/engine/parameters.go`) serves `{{param.<name>}}` and can read pool-derived sources via `data_source`/`column`. Distributed runs ship the inline CSV inside the script JSON to every generator (`pkg/distributed/generatorrpc/assignment_assets.go:33-123` deliberately leaves inline data inline; only `path`/`storage_ref` sources become content-addressed `DataFile` assets).

Two parameterization systems coexist and do not share selection logic:

| | `{{data.<alias>.<col>}}` (DataProvider) | `{{param.<name>}}` (ParameterRuntime) |
|---|---|---|
| State scope | shared across all VUsers in a group | per-VUser |
| Strategies | sequential, random, unique, pop | sequential, random, unique |
| Exhaustion policy | none (sequential wraps forever; pop → nil) | wrap / last / empty / error |
| Shard-aware | yes | partially (`GetColumnValues` partition) |
| Replay-seedable | yes (`SeedRandom`) | only via per-param `seed` field |

A pool binding's `selection_strategy` drives **only** the `{{data.*}}` path. LoadRunner-style `update` / `same_line_as` / `block_size` / `on_exhaustion` live on `Parameter` and are unreachable from a binding.

---

## 3. What's genuinely good (keep)

- **Storage concurrency.** `UpdateDataPoolIfUnchanged` (CAS on `updated_at` under a project lock), `UpdateDataPoolRowIfVersion` (version CAS scoped by `pool_id`, so a stale editor can't overwrite *or move* a row), `lockDataPoolParentForMutation` (`pkg/storage/sqlite_datapools.go:491`) locking project→pool in hierarchy-deletion order, scenario-aggregate CAS that force-bumps `updated_at` by a microsecond when the clock didn't move, and refusal to relocate a pool across projects.
- **Authorization.** Cross-project binding is blocked at four layers: API (`handlers_data_pools.go:423`), runner (`testcreation.go:1868`), inside the DB transaction (`sqlite_scenarios.go:585`), and execution plans (`execution_plan_service.go:140`). `requireDataPoolRow` deliberately returns identical 404s for missing vs cross-pool rows to prevent ID enumeration. Role matrix and audit round-trips are tested (`middleware_test.go:1930-2060`, `handlers_resource_audit_test.go:178-284`).
- **Deliberate fail-closed semantics.** `read_write`/`consume` access modes are rejected at authoring *and* re-checked at the executable boundary (`testcreation.go:1819-1827`) with an explicit comment: persisted legacy records must not silently degrade into read-only snapshot behavior. This is the right call until durable row coordination exists.
- **Run immutability.** The inline CSV snapshot frozen at materialization means editing or deleting rows mid-run cannot affect a running test.
- **Distributed sharding correctness** for `{{data.*}}`: disjoint stride partitions (`shardedRowCount`/`shardedRowIndex`, `pkg/engine/data.go:399-422`), VU IDs offset into disjoint 2³¹ namespaces, covered by `TestDataProvider_DistributedShardPartitionsDoNotOverlap`.
- **Deterministic replay** for the DataProvider `random` strategy (`SeedRandom`, `defaultReplaySeed`, plus a regression test guarding seed-before-first-row ordering).
- **Docs in sync.** `docs/swagger.yaml`, `docs/api-endpoint-catalog.json`, `docs/API_USER_GUIDE.md:651-661`, and `docs/APPLICATION_USER_GUIDE.md:288-300` all match the implemented routes, and the API guide honestly documents the consume/read-write restriction.

---

## 4. Correctness findings

### High

| # | Finding | Evidence |
|---|---|---|
| **C1** | **Parameter-level `selection:"unique"` is not unique across VUsers.** The `unique` branch of `generateSelectedValue` performs the same per-VUser `state.nextIndex` walk as the `sequential` default branch — `parameterState` is per-VUser, so every VUser starts at index 0 and emits the same sequence. The only behavioral difference is one `on_exhaustion:"last"` sub-case. Cross-VUser uniqueness exists *only* when `block_size` or `value_for_each_vuser` is set. Zero tests cover `selection:"unique"`. LoadRunner's Unique guarantees no two Vusers ever see the same value. | `pkg/engine/parameters.go:365-411` |
| **C2** | **Pool-level `unique` = `vuserID % shardRowCount`.** Each VUser gets one *fixed* row for the entire run (never advances across iterations), and when VUsers > rows the modulo silently assigns duplicates. Both halves diverge from LR Unique semantics; the duplicate case is acknowledged only in a code comment ("wraps if more VUsers than rows"). No authoring-time or pre-run warning existed. | `pkg/engine/data.go:358-369` |
| **C3** | **The UI silently truncates pools at 100 rows.** `defaultDataPoolRowListLimit = maxDataPoolRowListLimit = 100`; `getDataPool()` never paginates and the UI renders `dataPoolRows.length` as the total — a 50,000-row pool displays "100 rows" with no pagination control and no "showing X of Y". Runs still consume all rows, so what the user sees ≠ what executes. | `pkg/api/handlers_data_pools.go:16-18`, `dashboard/src/components/SettingsPage.tsx:4374` |
| **C4** | **The scenario wizard destroys bindings it doesn't show.** The Data step exposes only binding[0] via compact fields; bindings 2..N are invisible. Clearing the pool select emits `[]` — deleting every binding without confirmation; saving rewrites binding[0] and silently coerces any persisted `consume`/`read_write` mode to `read_only` (asserted as intended in tests, but it is silent semantic data loss triggered by unrelated edits). | `dashboard/src/components/TestHub.tsx:1212-1220` |
| **C5** | **`ArchivedAt` is inert.** Nothing in binding validation, scenario save, execution-plan validation, or materialization checks it — an archived pool binds and executes normally, contradicting `docs/ENTERPRISE_GOVERNANCE_MODEL.md` ("prevent new use"). Compounding it, archive is one-way in the UI: the repository page can archive but not restore, and Settings lists pools without `include_archived`, so an archived pool vanishes from the only editor. | `pkg/api/testcreation.go:1868-1893`, `pkg/api/handlers_data_pools.go:423-441`, `dashboard/src/components/ApplicationRepositoryPage.tsx:527` |
| **C6** | **Row `Status` is never filtered at materialization.** A row marked `consumed`/`disabled`/anything still feeds every run — the field exists to support consume semantics that don't exist, and today is pure decoration with a UI column. | `pkg/api/testcreation.go:1895-1933`, `pkg/storage/sqlite_datapools.go:370` |
| **C7** | **The API's CAS protects the wrong window.** `handleUpdateDataPool`/`handleUpdateDataPoolRow` CAS against the value they *just loaded* (`existing.UpdatedAt` / `existing.Version`) and ignore the client's token — preventing concurrent-request races but not read→edit→overwrite lost updates across a user's edit session. The UI compounds it: a 409 lands in a generic error banner, rows aren't reloaded, and the stale editor retries forever. | `pkg/api/handlers_data_pools.go:123-147, 253-271`, `dashboard/src/components/SettingsPage.tsx:1679-1681` |

### Medium

- **M1 — `block_size`/`value_for_each_vuser` degrade in distributed runs.** `applyParameterRowAllocation` uses the runtime VU ID, which is offset by `ordinal × 2³¹` in distributed mode, so the in-range check is essentially always false and allocation falls into the exhaustion branch (`wrap` → multiplicative hash, `last` → last row, else unresolvable). `pkg/engine/parameters.go:499-560`.
- **M2 — Missing indexes.** No index on `shared_data_pool_rows(pool_id)`, `scenario_data_pool_bindings(pool_id)` (scanned by the delete guard), or `shared_data_pools(project_id)`. Every row list and every test-start materialization was a full table scan. `pkg/storage/sqlite_schema.go:1249-1290`.
- **M3 — Alias silently mangled; alias↔script contract unvalidated.** The wizard lowercases/underscores the alias at submit while the input shows the original (`TestHub.tsx:1217`); nothing anywhere validates or hints that the alias matches the `{{data.*}}` tokens or `parameter.data_source` names in the bound scripts.
- **M4 — `pop` exhaustion is a silent nil.** `GetRow` returns `nil` after the shard's cursor runs out → downstream unresolved variables rather than an explicit error; the `pop` cursor is per-generator-process, so "consume-once" is really "consume-once within my stride" (correct only thanks to disjoint shards). `pkg/engine/data.go:371-380`.
- **M5 — `Schema` is write-only decoration.** Stored, returned, displayed in the repository catalog ("N field(s)"), but never validated against row keys and never used to derive columns (columns are the sorted union of row keys). No UI writes it, so app-created pools always show "No schema fields".
- **M6 — Reproducibility gaps.** Nothing records which rows a run consumed; pool contents are mutable after the run (only the inline snapshot in the stored test is stable); `pkg/api/test_manifest.go` has zero pool fields; `{{param}}` with `selection:random` seeds from wall-clock unless the script sets `seed` — `ReplayOptions.Seed` reaches only `DataProvider`, not `ParameterRuntime` (`pkg/engine/parameters.go:64`).
- **M7 — No limits where they matter, plus N-fold shipping.** Rows per pool, cell size, and scenario binding count are unbounded (execution plans cap at 64/entry; scenarios cap at nothing). The inline CSV bypasses staged-asset budget accounting and is JSON-escaped into the script shipped to *every* generator (the 64 MiB generator script ceiling is the only backstop; engine load caps: 128 MiB input / 1M records / 4096 cols / 2M cells).
- **M8 — Contract nits.** `DataSource.Strategy` is not validated at authoring (`validateDataSourceIssues` ignores it; invalid strategy surfaces only at run start); `DELETE /data-pools/{id}` of a nonexistent pool returns 200 `{"deleted": id}`; unscoped `GET /data-pools` returns every project's pools to any global viewer (asserted as intended in `middleware_test.go:1941-1948`, but worth knowing).

---

## 5. Completeness gaps

### API

1. **No CSV upload, no export/download, no bulk row insert** — `SaveDataPool`/`SaveDataPoolRow` have exactly two non-test callers (the create handlers). Loading a pool of any real size means one HTTP POST per row against an 8 MiB default body limit.
2. **No `total` in row-list responses** (`count` = page length), and `GET /data-pools` is unpaginated.
3. **No pool duplicate/copy, no dedicated archive/restore endpoint** (archiving = PUTting the whole pool with `archived_at`), **no ETag/If-Match contract** (see C7).
4. **No importer produces pools.** LoadRunner `.prm`/`.dat` (`pkg/convert/enterprisearchive/parameters.go`), JMeter CSV Data Set Config (`pkg/convert/jmeter/converter_csv_timers.go`), Postman variables, and `pkg/convert/paramize` all emit *script-local* inline data sources; there is no "promote to shared pool" path. (JMeter's `recycleOnEOF`/`stopThreadOnEOF`/`shareMode` are also unmapped; Gatling feeders and k6 SharedArray have no importer at all.)
5. `DeleteScenarioDataPoolBinding` exists on the `Store` interface with no HTTP route and no caller — bindings are only replaced wholesale.
6. Several storage capabilities (paging, CAS, project-scoped list, pool-scoped row ops) are optional interface upgrades type-asserted in handlers rather than `Store` members — non-SQLite backends get 501s on core paths.

### UX

1. **Row editing = raw JSON textarea** seeded with `{"customer_id": "C-10001"}`; rows render as single-cell `JSON.stringify`. The full grid editor with CSV import/export/search/sort/auto-fix that exists for script-local sources (`ScriptEditorParameterDataSources.tsx:483-745`) was never applied to pools.
2. **No rename, description, or schema editing** after creation (the API supports all three via PUT).
3. **No loading state** (initial fetch renders the empty-state text), **no busy/disabled state** on mutations (double-click double-creates), **no success feedback**, no duplicate-name check, no inline JSON validation (errors surface post-submit in a distant section banner).
4. **Execution plans require pasting pool UUIDs** into a free-text input (`ExecutionPlansPanel.tsx:503`) and hardcode `sequential`.
5. **No permission gating** on the Settings section — the RBAC scopes `view_data_pools`/`manage_data_pools` exist but are used only to render API documentation; every viewer sees enabled CRUD controls (server still rejects, but the UX lies).
6. **No exhaustion-policy control for bindings** (the concept doesn't exist below the parameter level — see roadmap).
7. **No pool→parameter binding UX**: a user cannot bind a script parameter to a pool column anywhere; the server-side fit report (`data_source_fit`/`data_pool_rows` dimensions, `pkg/api/authoring_data_source_fit.go`, `load_model_validation.go:305-341`) only surfaces after an explicit Validate click.
8. **Dead code:** `listScenarioDataPools`/`updateScenarioDataPools` hook methods are defined, exported, memoized, and never called; the dedicated rows endpoint has no hook at all. Bindings are editable only through the wizard.
9. **Repository catalog:** pool rows have no open/edit action (action column shows the muted word "Catalog"); archive is one-way (no restore anywhere).
10. **Naming chaos:** "Data Pools" (settings topbar, repository tab) vs "Shared Data Pools" (section title, launch tile) vs "Reusable Data" (subsection) vs "Data" (wizard step) — plus a true collision: the Script Editor block-library category is *also* labeled "Data Pools" (`ScriptEditor.tsx:308`) but means saved script snippets.
11. **Accessibility:** unlabeled JSON textarea, N identical "Delete" buttons with no pool/row name, `aria-label` on a `<td>`, list selection state conveyed by CSS class only. The delete-confirm copy references scenario bindings but nothing shows *which* scenarios bind the pool.
12. **Zero tests** cover the Settings Data Pools UI; no e2e touches pools.

---

## 6. Test coverage assessment

**Well covered:** binding contract normalization (8 tests), cross-project security at API and runner layers, scenario-aggregate CAS 409s, storage CAS + parent-integrity/orphan races, audit round-trips for all 7 actions, role matrix per endpoint, DataProvider strategies + shard partition disjointness + deterministic replay, LR `.prm` mapping including block-size regressions.

**Not covered (pre-remediation):** parameter-level `selection:"unique"`/`"random"` (the C1 divergence), any end-to-end pool→engine-iteration path (pool tests stop at `[]dsl.DataSource`; engine tests start from hand-written sources), the binding-guarded pool delete 400, `include_archived`, the 501 fallback branches, `inlineCSVFromDataPoolRows` edge cases (ragged rows, commas/quotes/newlines in keys), row `Status` semantics, `pop`-bound pools reaching exhaustion, `block_size` under distributed VU-ID offsets, and the entire Settings pools UI.

---

## 7. LoadRunner parity snapshot (parameter/data semantics)

Implemented and mapped from `.prm`: update policies (`once`/`each_iteration`/`each_occurrence`), selection (`sequential`/`random`/`unique`*), `same_line_as`, exhaustion (`wrap`/`last`/`empty`/`error`), `start_row`, `block_size`, `value_for_each_vuser`, and 12 parameter types with LR spelling aliases. Missing/divergent: true cross-VUser Unique (C1/C2), `auto_allocate_block_size` (importer emits a review warning), global consume at parameter level, per-VU sequential walk (pool `sequential` is a shared group-wide cursor — closer to JMeter `shareMode=all` than LR sequential), and LR Unique's per-iteration block advance.

---

## 8. Roadmap (recommended)

### Tier A — correctness (small, one change-set)
1. Filter non-active rows and reject archived pools at bind, materialize, execution-plan, and load-model validation time.
2. Add the three missing indexes (plain `CREATE INDEX IF NOT EXISTS` passes the Postgres `rewriteSQL` translator unchanged).
3. Honor client-supplied CAS tokens (row `version`, pool `updated_at`) when present.
4. Wizard: clearing the pool select removes only the visible binding, preserving bindings 2..N.
5. Pre-run validation items for `unique` (VUsers > active rows → blocked) and `pop` (demand vs rows → needs_review) bindings.

### Tier B — table stakes
1. **CSV import** endpoint: multipart, `mode=append|replace`, delimiter auto-detect (`dsl.DetectDataSourceDelimiter`), `dry_run` preview, ~50 MiB cap via an explicit route-catalog entry (do **not** raise the generic 8 MiB data-pools fallback), transactional bulk insert (never loop `SaveDataPoolRow` — per-row tx + locks).
2. **CSV export** endpoint: streamed page-by-page, `Content-Disposition: attachment`.
3. **Settings overhaul:** grid row editor (reuse the parameter-grid model helpers/CSS but keep per-row `{id, version}` identity — don't shim rows into a fake `inline_data` blob), pagination + `total`, rename/schema editing, archived filter + restore, 409 → reload + notice, gate mutations on `manage_data_pools`.
4. **Wizard multi-binding array editor** with pool dropdowns, alias hints from the bound scripts' `data_sources`/`parameters` (already client-side in `StoredScript.script`), duplicate-alias warnings, the missing `pop` option, and inline fit surfacing; execution-plan panel gets a pool `<select>`; wire the dead scenario-binding hooks into a bindings editor on the scenario detail.
5. **Naming sweep** → standardize on "Data Pools"; relabel the Script Editor block category (label only — `kind: 'data_pool'` is persisted API surface).

### Tier C — high-value extensions
1. **`on_exhaustion` on bindings** → new field on `ScenarioDataPoolBinding` + `dsl.DataSource` + engine `GetRow` (`wrap|last|empty|error`). It must live on DataSource, **not** be mapped onto Parameters — `{{data.*}}` resolution bypasses ParameterRuntime entirely (`pkg/engine/vuser_resolve.go:80-94`). Absent field = legacy behavior, so stored scenarios and replays stay byte-identical. This is also the vehicle for making `unique` honest (policy instead of silent modulo duplication).
2. **Storage-ref materialization for large pools** (> ~1 MiB): write a content-addressed CSV and set `StorageRef` instead of `InlineData`; `PrepareDistributedScript` already stages `storage_ref` sources as sha256 `DataFile`s, so distributed transport comes free. One engine gap: `openDataSourceReader` lacks a StorageRef branch for csv/dat (~5 lines).
3. **Consume-on-start row leasing** (the smallest honest `consume`): transactional `ConsumeDataPoolRows(poolID, runID, limit)` marking rows consumed at materialization — single-writer safe via execution leadership + the pool lock — then enable `access_mode: consume` and a gated "reset consumed" bulk action. Explicitly *not* distributed runtime checkout: assignments are deliberately self-contained and a chatty per-row data path would break the resilience/evacuation model.
4. **Promote imported data source → shared pool**: `POST /data-pools/promote` (script_id + source_name → pool + suggested binding), surfaced in the Script Editor data-source row and import review. Keeps `pkg/convert` storage-free.
5. **Run provenance**: record pool ID/name/row-count/content-hash in the test manifest (currently zero pool fields) so results can be audited against the data that produced them.
6. Smaller: `{{data.*}}` token scan in the fit report (flag unbound aliases and unreferenced bindings); per-group bindings (today every pool attaches to every group); pool duplicate endpoint; seed plumbing from `ReplayOptions` into `ParameterRuntime`.

### Do NOT do (anti-patterns verified against the architecture)
- Raise `MaxBodyBytes` via the generic data-pools access-policy fallback — it would lift the 8 MiB guard for every tiny row PUT.
- Bulk-import by looping `SaveDataPoolRow` (per-row transactions and project+pool locks).
- Change engine `unique`/`pop` behavior unconditionally — gate new semantics on an explicitly set `on_exhaustion` so existing stored scripts and replays are untouched.
- Put row contents in audit details — rows may hold PII/credentials; current pool events are deliberately value-free.
- Rename the persisted block-library `kind: 'data_pool'` value (label-only fix).
- Accept CSV uploads above ~50 MiB — `fileasset.ReadAllLimited` caps engine-side reads at 50 MiB and the distributed script budget is 64 MiB; a bigger pool would upload fine and then fail every run.

---

## 9. Remediation in this change-set (2026-07-30)

Implemented alongside this report (see the working tree / accompanying commits):

- **Tier A** (items 1–5 above): status filtering + archived enforcement at all four validation/materialization layers, the three indexes, client-supplied CAS tokens, wizard binding preservation, and unique/pop pre-run validation items — with tests.
- **Tier B items 1–2**: `POST /api/v1/data-pools/{id}/rows/import` (multipart; append/replace; delimiter auto/comma/tab; header/columns handling; dry-run preview; 50 MiB route-scoped cap; engine-limit pre-enforcement; audit) and `GET /api/v1/data-pools/{id}/rows/export` (streamed two-pass CSV; status filter; audit), plus `total` on row-list responses and minimal Settings UI wiring (import with dry-run preview, export, honest row counts).

Everything else in Tiers B and C remains open and is sized/sequenced above.
