# LoadRunner Function Catalog Plan

## Objective

Make the LoadRunner/VuGen importer **natively handle every function in the entire VuGen
Function Reference** with a *deterministic disposition* for each function — nothing is dropped
to a generic "unsupported" bucket — and execute the behavior **full-stack** (DSL action type +
engine execution) for every protocol family that has an APEX Load runtime.

Confirmed scope: entire VuGen reference / deterministic disposition / full-stack execution.

Status: **Phase A complete. Phase B in progress** (web correlation + data points shipped;
database, network, and messaging families remain). Phase C and D not started.

Related docs: [LOADRUNNER_PROTOCOL_IMPORT_ROUTING.md](LOADRUNNER_PROTOCOL_IMPORT_ROUTING.md)
(archive-level protocol routing), [LOADRUNNER_PARITY_EXECUTION_PLAN.md](LOADRUNNER_PARITY_EXECUTION_PLAN.md)
(higher-level parity epics), and the in-repo
[`pkg/convert/loadrunner/testdata/loadrunner_functions/CATALOG_SOURCING.md`](../pkg/convert/loadrunner/testdata/loadrunner_functions/CATALOG_SOURCING.md)
(catalog refresh procedure).

## Architecture (already in place — extend, don't rebuild)

The importer dispatches functions through a **catalog-driven handler registry**, not a
hand-written switch:

- **Catalog** — per-family CSVs embedded via `//go:embed testdata/loadrunner_functions/*.csv`
  in `pkg/convert/loadrunner/function_registry.go`. Row format: `name,family,support_mode[,reason]`.
  `support_mode` ∈ `executable | native_noop | review_only | external_required`. Uncatalogued C
  calls get `custom_review` → `custom_call` at runtime — the total fallback that guarantees no
  function ever reaches a generic "unsupported" bucket.
- **Dispatch** — `convertLoadRunnerFunctionAction` (`pkg/convert/loadrunner/utility_actions.go`)
  switches on `family` → a `loadrunner_<family>` action. Families with rich handlers (message,
  transaction, parameter, metric, runtime/rendezvous, timer, json, xml, database, secret) become
  executable; other families fall to a deterministic `loadrunner_review` / `loadrunner_external`
  preserve action.
- **Engine** — `pkg/engine/vuser.go` `handleEngineAction` executes these (transactions via
  `Metrics.RecordTransaction`, parameter saves, timers, data points via `Metrics.RecordDataPoint`,
  etc.). New engine action types are whitelisted in `pkg/dsl/validate.go` `isEngineActionType`.
- **Determinism guarantee** — `TestLoadRunnerFunctionRegistryCoversDocumentedFunctions`
  (`pkg/convert/loadrunner/converter_test.go`) round-trips every catalog row and asserts zero
  skipped/unsupported + a matching source-review support mode.
- **Sourcing** — `tools/lrcatalog` regenerates the catalog from saved VuGen index HTML fixtures
  with an idempotent upsert (curated rows preserved; CI never crawls live). A drift test
  (`TestCatalogUpToDateWithFixtures`) surfaces newly-documented functions.

### Pattern: making a function executable

1. Add a dedicated converter handler that emits a real DSL action / extractor / config.
2. Remove the function from its catalog CSV (it is no longer a generic catalog entry).
3. Add the name to the `handledExternally` set in `tools/lrcatalog/main.go` so the realistic
   doc fixture does not trip the drift guard.
4. Ensure `pkg/dsl/validate.go` accepts the action type and `pkg/engine` executes it.
5. Add table-driven converter tests + engine execution tests (`go test ./...`).

GUI/proprietary families that have **no** APEX Load runtime stay `review_only` with a
family-specific reason — that is the honest deterministic disposition, not a silent drop.

## Shipped

Branch `loadrunner-function-catalog-phase-a` (off `main`).

### Phase A — catalog covers the reference (commit `be585b1c`)
- Catalog format: directory embed + optional `reason` column + `Reason` field + family/mode
  default reasons (`function_registry.go`).
- Seed: 280 functions across 10 families (`utility`, `web`, `socket`, `sapgui`, `citrix`,
  `database_lrd`, `oracle_nca`, `rte`, `flex`, `vts`); new protocol families seeded as
  `review_only` / `external_required` / `native_noop` with reasons.
- Tooling: `tools/lrcatalog` offline generator + fixtures + drift test + `CATALOG_SOURCING.md`.

### Phase B (partial) — first executable families (commit `66862009`)
- **B1 web correlation**: `web_reg_save_param_ex/_regexp/_json/_xpath` → executable
  `dsl.Extractor` (boundary/regex/jsonpath/xpath), attached to the next request.
- **B5 data points**: `MetricsCollector.RecordDataPoint` + `MetricsSnapshot.DataPoints`;
  `lr_user_data_point` (and `_ex`/`_instance`) record a real metric.

## Remaining work

### Phase B — finish executable mapping for engine-supported families

| Item | LoadRunner functions | Target | Primary files |
| --- | --- | --- | --- |
| B2 Database | `lrd_*` (init/open/stmt/exec/fetch/bind/save/commit/rollback) | `db_*` actions (`db_connect`, `db_query`, `db_exec`, `db_commit`, `db_rollback`) | `pkg/convert/loadrunner/database_import.go` (`convertDatabaseFunction`), `database_lrd.csv` |
| B3 Network | `lrs_*` (Winsock), FTP `ftp_*`, mail `smtp_*`/`pop3_*`/`imap_*`, `ldap_*`, `dns_*` | `tcp_*`/`udp_*`, `ftp_*`, `smtp_*`/`pop3_*`/`imap_*`, `ldap_*`, `dns_*` actions | `pkg/convert/loadrunner/network_import.go`, `socket.csv` (+ future per-family CSVs) |
| B4 Messaging/streaming | `jms_*`, WebSocket, gRPC reference functions | existing `jms_*` / `ws_*` / `grpc_*` action types | new converter handlers + catalog rows |

For each: apply the "making a function executable" pattern above. Engine execution for these
protocol action types already exists; the work is converter mapping + catalog promotion +
tests. Functions whose operation is not expressible stay `review_only` with a reason.

### Phase C — GUI/proprietary families (deterministic preserve-with-reason)

`sapgui_*`, `sap_*` (web), `ctrx_*`, `nca_*`, `siebel_*`, `flex_*`/`amf_*` RTMP, RTE `TE_*`,
TruClient, Java, .NET: keep `review_only` with a **family-specific reason** (e.g. "SAPGUI GUI
virtual-user step has no APEX Load runtime; preserved for manual rework"). The engine already
logs these via `loadrunner_review` / `custom_call`. No execution — there is no GUI VU runtime;
faithful preservation is the goal. Work here is breadth: expand catalog coverage of these
families via `tools/lrcatalog` fixtures so every documented function is dispositioned.

### Phase D — tests & verification (per CLAUDE.md testing policy)

- Per-family converter handler tests (table-driven) for B2–B4 mappings.
- Engine execution tests for newly executable action types.
- Keep `TestLoadRunnerFunctionRegistryCoversDocumentedFunctions` and the
  `tools/lrcatalog` drift test green as families grow.
- Extend `sampleLoadRunnerFunctionCall` (`converter_test.go`) for any new sample shapes.

## Verification

```bash
go build ./...
go test ./pkg/convert/loadrunner/... ./pkg/engine/... ./pkg/dsl/... ./tools/...
go test ./...
# Catalog in sync with fixtures (offline; never crawls live docs):
go run ./tools/lrcatalog -check
```

Manual smoke: import a `.zip` mixing `web_*` + `lr_*` + a GUI-family call via
`POST /api/v1/scripts/import/loadrunner`; confirm real protocol actions for web/db/network,
executable `loadrunner_*` actions for utilities, `review_only` preserve actions (with reasons)
for GUI families, and zero unsupported functions in the source-review / stats output.
