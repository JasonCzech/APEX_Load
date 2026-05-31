# LoadRunner function catalog — sourcing & maintenance

These per-family CSV files are the source of truth for how the LoadRunner importer
deterministically dispositions **every** VuGen Function Reference function. They are embedded
into the converter via `//go:embed testdata/loadrunner_functions/*.csv`
(`pkg/convert/loadrunner/function_registry.go`).

## Row format

```
name,family,support_mode[,reason]
```

- **name** — the VuGen function name (case-insensitive; stored lowercased).
- **family** — the dispatch key used by `convertLoadRunnerFunctionAction`
  (`pkg/convert/loadrunner/utility_actions.go`). Families with rich converter/engine handlers
  (e.g. `message`, `transaction`, `parameter`, `metric`, `runtime`, `timer`, `json`, `xml`,
  `database`, `secret`) produce executable `loadrunner_*` actions; all other families fall to
  a deterministic `loadrunner_review` / `loadrunner_external` preserve action.
- **support_mode** — the disposition: `executable`, `native_noop`, `review_only`,
  `external_required`. (A fifth mode, `custom_review`, is assigned at runtime to parsed C
  calls that are **not** in any catalog — the total fallback that guarantees no function ever
  reaches a generic "unsupported" bucket.)
- **reason** — optional. When omitted, a deterministic default is derived from
  family + support_mode by `defaultLoadRunnerReason` so the source-review audit trail always
  has a human-readable disposition note.

## Determinism guarantee

`TestLoadRunnerFunctionRegistryCoversDocumentedFunctions`
(`pkg/convert/loadrunner/converter_test.go`) round-trips every catalog row through `Convert`
and asserts zero skipped/unsupported functions and a matching source-review support mode.
Any non-catalogued C call becomes a `custom_call` review action
(`TestConvertUnknownCFunctionAsCustomCallReviewAction`).

## Refreshing from a new VuGen version (offline)

The catalog is curated; the generator only **adds** functions and never clobbers curated
rows. CI never crawls the live documentation site.

1. From the VuGen Function Reference category index pages
   (https://admhelp.microfocus.com/vugen/en/latest/help/function_reference/), save each
   protocol family's index HTML into `tools/lrcatalog/fixtures/<family>.html`. Only the
   function-name `<a>` anchors are needed. The fixture stem must match the catalog CSV stem
   and have an entry in the `policies` map in `tools/lrcatalog/main.go`.
2. Run the idempotent generator:
   ```bash
   go run ./tools/lrcatalog          # appends only the newly-documented functions
   git diff pkg/convert/loadrunner/testdata/loadrunner_functions/
   ```
3. Review the diff. Promote functions from the default `review_only` to `executable` (and add
   the matching rich handler) as Phase B/C support lands; tune `reason` text as needed.
4. `go test ./...` — the catalog/fixture drift guard
   (`tools/lrcatalog`: `TestCatalogUpToDateWithFixtures`) and the completeness test must pass.

To verify the catalog is in sync without writing files:

```bash
go run ./tools/lrcatalog -check
```
