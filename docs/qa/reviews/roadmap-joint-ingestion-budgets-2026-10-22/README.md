# Joint evidence ingestion budgets — 2026-10-22

This [NA-B slice](../../../ROADMAP_IMPLEMENTATION_SPEC.md#na-b--bounded-joint-evidence-across-execution-entry-points) bounds joint evidence v4 where samples are ingested, before the per-window state and the published envelope are built.

## Contract

- **Category and cardinality.** A projected `property:<category>` must be promoted (see the [entry-point slice](../roadmap-joint-entry-points-2026-10-21/README.md)).
  - Each script that promotes a category approves 1 to 16 values (`dsl.ValidatePropertyAnalytics`).
  - Before a sample reaches the joint accumulator (`promotedPropertyDimensions`), any other observed value counts as `other` and an absent one as `missing`.
  - Samples from groups whose scripts do not promote the category carry the empty value.
  - A run's values for a category are therefore bounded by the union of its promoting scripts' lists plus those three. The existing limits of 512 tuples per publication, 256-byte labels and 1 KiB keys bound the window's tuples.
- **Distribution state.** A joint tuple and the window's other bucket now count only what joint evidence publishes: count and errors (`mergeJointSample`). The duration distribution and the average's total are recorded beside them.
  - Previously each tuple ran the full action merge (`mergeActionResult`). That built per-tuple server and Java telemetry summaries, transport timings, byte totals, duration bounds and correlation samples, none of which joint evidence published. That state stays with the marginal action metrics.
  - Each distribution keeps the existing limits: a t-digest of at most `engine.TDigestMaxCentroids` (1,024) centroids and a sketch of at most `engine.DurationSketchMaxBuckets` (4,096) bins.
- **Encoded bytes.** A generator publishes each tuple with only the joint fields (`jointAggregateFromEngine`): name, count, errors, average, P50/P90/P95/P99, digest and sketch.
  - Previously tuples went through the generic action conversion, which also copied exemplars, messaging and Java HTTP summaries, transport timings and bytes.
  - The worst case is 512 tuples with 1 KiB keys plus the other bucket, each with a full digest and sketch, under the longest projected dimensions and with the widest scalars. That joint part encodes to 51,052,433 bytes (about 48.7 MiB, 76.1% of the controller's 64 MiB joint envelope).
  - The controller's joint check therefore never refuses a valid publication whole for its size. The existing wire preflight still refuses malformed or oversized peers, and the transport's per-message limit applies to the whole batch separately.
- The digest limit is now one exported constant (`engine.TDigestMaxCentroids`), used everywhere it had been a literal (engine copy and validation, both protobuf conversions and the joint wire admission). The key and label limits are exported (`engine.JointMaxKeyBytes`, `engine.JointMaxLabelBytes`).

## Validation

- `TestJointLargestPublicationFitsTheEnvelope`:
  - builds the worst case from the exported limits through the real conversion (`jointAggregatesFromEngine`), the generator's group merge (`mergeMetricsAggregateSets`) and protobuf;
  - asserts the encoded size is within `maxJointEncodedBytes` and that `admitJointPublicationProto` admits it;
  - shares one full-size distribution across the wire tuples, so it runs without allocating 513 copies.
  
  If any of those limits rises past the envelope, the test fails.
- `TestJointAggregateCarriesOnlyJointFields` asserts that a tuple publishes only joint fields, before and after the group merge, even when its engine metrics carry diagnostics.
- `TestJointAccumulatorKeepsOnlyPublishedState` asserts that a sample's diagnostics, timings, bytes, duration bounds and correlation identifiers do not become per-tuple state. Count, errors, average and both distributions stay exact.

Gates on the final tree:
- `go vet` and golangci-lint (0 issues) on the changed packages;
- the `pkg/engine` and `pkg/distributed/...` suites, and the joint tests under `-race`;
- `go test ./docs/qa`;
- the full `pkg/api` suite. Its only failure was the swagger freshness check of a separate, uncommitted slice; this slice changes no API type.

One unrelated `pkg/distributed/generatorrpc` failure was not captured. It happened in a run that shared the host with other suites, and two later full runs passed. Repeating that package inside one process (`-count=3`) fails `TestMessagingQuiescenceAdvertisementRequiresAvailableSupportedRuntime`, which registers a protocol globally. That test predates this slice.

## Review

Separate reviewers read scratch snapshots.

- Round 2 found no high or medium issues; fixed: one remaining centroid literal (`tdigestSnapshotFromProto`), a stale test comment, and a key construction in the worst-case test that would panic rather than fail if the limits changed.
- Round 1 found no high or medium issues. Fixed:
  - The worst-case test now goes through the generator's group merge, is built from exported limits rather than literals, and shares one distribution to bound its memory under `-race`.
  - The cardinality claim was per script, not per run.
  - The envelope claim now names the joint check and separates the transport limit.
  - Duration bounds were kept per tuple but never published, so they are now dropped.
  - A promoted property dimension now has the longest admitted name in the worst case.
  - Units are consistent.

## Remaining

Durable source-window ownership and recovery remain open, as the [roadmap NA-B status](../../../ROADMAP_IMPLEMENTATION_SPEC.md#na-b--bounded-joint-evidence-across-execution-entry-points) describes. These checks bound memory and encoding by construction and by test. They do not measure reference-host throughput, which belongs to TEL-C and NA-D.
