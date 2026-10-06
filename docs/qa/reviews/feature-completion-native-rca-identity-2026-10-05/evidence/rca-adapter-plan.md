# Native analytics adapter plan — confirmed RCA engine

The authoritative in-progress engine is `claude/rca-correlation-start` in `/Users/jasonczech/Documents/GitHub/Project_Stormrunner/.claude/worktrees/rca-start`, observed HEAD `512bc699b058383540297dae7dec9f3271f1e40a`. This worktree and primary provider work remain protected and read-only. Source bytes are pinned in `/private/tmp/apexload-rca-native-adapter-sources-20261005.json` (SHA256 `5d6a33c83d5fbc7361880c2e969faac2d5007133bbd982de5c734468d38c5323`); these are working-file observations, not an atomic engine release.

## Existing contracts and overlap

`pkg/rca` already owns Analyze, Detect, BuildGraph, validity, hypotheses, capacities, and Little's Law. It is a pure bounded engine; API adapters own sources, persistence, and jobs. Detect matches cross-run baselines by Series.ID. The current monitoring ID omits unit, InputKind, derivation, population/version, and grid semantics although the shared analysis comparisons already preserve these distinctions. This is an adapter identity gap, not a missing analysis engine.

The protected API/storage work already owns immutable evidence revisions, input digests, bounded evidence payloads, feature-gated jobs, HA lease/owner fencing, and fenced completion. Native attachments must extend those existing contracts rather than introduce another job or persistence framework. Imported labels cannot confer managed run ownership or provider/node classification. Native partition/result authorization and integrity must precede adaptation.

## Approved smallest isolated slice

Owned worktree: `/Users/jasonczech/.codex/worktrees/native-rca-identity/Project_Stormrunner`, branch `codex/native-rca-identity`, base `81bd5eb760007adce060450a1f0cce03d8846732`.

Add `pkg/analysis/series_identity.go` and adjacent tests. Export `SeriesIdentityVersion = "apexload.series_identity.v1"`, `SeriesObservation{Mode string, BucketNanos int64}`, `SeriesIdentity{Version string, SHA256 string}`, and `MeasurementSeriesIdentity(MetricPoint, SeriesObservation) (SeriesIdentity, error)`. Interval/cumulative require a positive grid no larger than exactly 24h; snapshot permits zero/unknown grid or a positive grid within the same bound. Exact unit spelling remains semantic. Return a full SHA256 of a bounded structured canonical encoding, including the version and observation contract. Validate UTF-8 in every encoded string before JSON encoding so replacement characters cannot collapse distinct byte identities.

Factor one private typed projection for existing equality and fingerprinting. Comparisons borrow maps/slices and compare scalars exactly plus maps.Equal/slices.Equal; they do not hash, encode, or clone. Preserve the exact existing general-provider and Prometheus compared and ignored fields. Nil/empty maps and derivation compare identically. Exclude dataset/run ownership, values, timestamps, quality and mutable settlement. The helper grants neither qualification nor unit-alias conversion, and changes no capability, IPC, CatalogVersion, SchemaVersion, public REST API or protected engine code.

Meaningful tests cover every scalar/provider identity field, every ignored provider field, equality parity against the pre-change comparators, dimension order and nil/empty representations, derivation lineage, embedded NUL collision resistance, invalid UTF-8 in each encoded string/map/derivation field, ownership/value/time exclusions, exact unit aliases, all observation/boundary modes, input immutability and concurrent deterministic calls. Run portable `go test ./pkg/analysis` and `go test -race ./pkg/analysis`, self-review/fix/review, then freeze diff/hashes for parent review before committing.

## Later call sites and dependency gates

Once authoritative engine work is committed/relocated, `pkg/api/rca_*` monitoring series ID/template adapters should call this helper before grouping, include its helper stamp/full digest, and advance the existing new-job engine version. Existing archived IDs remain readable. `queryAnalysisDataset` can use the same identity after authorized hash-verified native worker publication. It must separately retain qualification, explicit baseline selection, frozen mapping, population and metric-version requirements, and engine series/measures/node authorization.

Imported RCA evidence requires an immutable native descriptor/revision/mapping/partition digest in the existing evidence and input digests. Dataset project/state/revision and artifact digest must be checked in the same fenced completion transaction and again on reads; a pre-publication check alone leaves a deletion/revision race. Pending registry/timing/storage/API/schema dependencies prevent a safe engine adapter cherry-pick today. The helper avoids those dependencies and has no runtime adapter adoption yet.

Summary/histogram work is deferred. Current native mapping/version identity coupling and worker-result scalar-only lineage cannot be widened by normalization. A future source adapter needs explicit statistic/window/count/sum or bounded histogram-bin contracts, qualified scalar projection, separate mapping/IPC compatibility versions and reviewed engine adoption. Do not replicate existing arithmetic or RCA core.

## Acceptance limits

This slice only establishes a shared identity seam. It does not establish completed native import/RCA integration, HA/restart behavior, platform packaging, 1 GiB native acceptance, resource limits, histogram qualification, or full-program completion. The 10 GB gate alone has been waived by the user. Heavy native/API/full-Go/dashboard runs remain parent-owned. No source changes, tests or processes were run in the protected engine or primary checkout.
