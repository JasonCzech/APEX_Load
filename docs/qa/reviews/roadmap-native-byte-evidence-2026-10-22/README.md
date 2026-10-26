# HTTP byte evidence — 2026-10-22

This [NA-C slice](../../../ROADMAP_IMPLEMENTATION_SPEC.md#na-c--complete-workflows-and-durable-lifecycle) gives measured `bytes_sent` and `bytes_received` a bounded query profile.

## Starting point

- **No executable query.** The catalog declared both metrics with an `interval_byte_totals` requirement, but no query profile ran them.
- **Ambiguous zero.** The archive kept each request's byte counts, but a zero there could mean either a measured zero or a protocol that counts no bytes.
- **Different meanings per protocol.** HTTP counted body bytes, gRPC its messages' JSON representation, WebSocket message payloads, database SQL text and Java VUser stdout.
- **HTTP itself was inconsistent.**
  - A response from the wire counted its compressed body, while a cache hit counted the decoded body it stored.
  - A request refused before sending counted its body as sent.
  - A step that dispatched no request (a check) looked like a zero-byte request.

## Contract

**HTTP adapter.** `ByteEvidence` on the result is set by the adapter itself:
- `none` on every HTTP result, until the action dispatches its request or serves it from the cache;
- `http_body.v1` (`protocol.ByteEvidenceHTTPBody`) from that point on.

The counts follow its definition: message body bytes on the wire, after transfer decoding and before content decoding.
- **Sent:** the dispatched request's body, once, plus the body of a digest-auth replay that completed (a failed replay is not counted).
  - Nothing for a fresh cache hit, or a request that failed before its headers were written.
  - A request that failed after its headers were written counts its whole body.
  - A redirect's re-sent body and an engine retry's earlier attempts are not counted.
- **Received:** the response body plus embedded resources and meta-refresh follow-ups. Nothing for a response served from the cache, whether fresh or after a 304.

These counts change for every consumer of HTTP results' byte counts: live metrics, reports and transaction SLA byte rules. Server telemetry's request and response payload sizes keep their earlier basis: compressed for a response read from the wire, the decoded stored body for one served from the cache, and the prepared request body even when it was not sent.

**Archive writers.** Both the controller's and the generator's writers keep the adapter's evidence (`protocol.ByteEvidence`), in JSONL and Parquet.
- They keep none on a business transaction, whose bytes are its actions' sums.
- Other protocols' adapters set none. SOAP and Java HTTP wrap the HTTP adapter and keep its label, but their actions are outside the population. gRPC counts its messages' JSON representation (the payload bytes are in its diagnostics), and a WebSocket close counts its payload even when the write failed.
- **Kept through the engine's evidence clear.** When the engine clears a result's public evidence (failed credential redaction, concurrent groups), a result carrying byte evidence keeps it and its constant protocol. Every other dependency type is still cleared, because an action parameter can shape it (JMS).
- **Kept through meta refresh.** A meta-refresh follow-up refused before sending keeps the page's label.
- A binary from before this slice sets nothing, so its requests stay unavailable.

**Catalog** (`byteMetricDefinitions`, population `byte`):
- `bytes_sent` and `bytes_received` take `sum` per bucket, or `avg`, `min`, `max`, `p50`, `p90`, `p95` and `p99` per request (nearest rank), in bytes.
- `bytes_sent_rate` and `bytes_received_rate` take `rate`: a bucket's sum over its duration, in bytes/s.
- They declare no derivation, baseline or correlation, and require `ByteEvidenceRequirement`.
- One profile executes them: `archive_selection//byte/exact`. It allows one exact `action` filter, no grouping or property, and a comparison run (`validateNativeByteQuery`).

**Execution** (`buildNativeByteSeries`). The population is HTTP requests in the window, optionally of one action, bucketed by request start.
- **Outside the population:** a step marked `none` (counted as `no_request`), a same-named action of another protocol, and a business transaction.
- **Unscored bucket:** a request without byte evidence, or with a negative count, makes its bucket unscored for every aggregation (`byte_evidence_unavailable`, `invalid_byte_count`), and the exclusion is counted.
- **Sums and rates** need a complete bucket:
  - every request measured;
  - every source collected (a local run has no source coverage and counts as collected; a distributed run needs `required` coverage);
  - the bucket ended by the run's execution completion.

  Otherwise the bucket is unscored (`incomplete_bucket`). A sum past 2^53 − 1 is unscored (`exact_sum_limit`).
- **Distributions** read every request of an evidenced bucket. When the collection is incomplete they still read them, with a warning.
- **Empty and zero:** a bucket without requests has no value; a request that sent no body is a measured zero.
- **Limits:** the existing limits apply: 100,000 raw observations and 1,000 identities.

**Dashboard.**
- Panel authoring offers **Archived HTTP body bytes** for archive panels only, with the four metrics and their aggregations, an action filter only, and help text about these bytes.
- The archive panel states their meaning. It shows each bucket's reason, for the current and the baseline run, in the table and the export, and offers no transaction drill-down for this population.

**CLI.** `apexctl analysis run-query` posts the same query.

## Validation

- **`TestHTTPByteEvidenceMeasuresWireBodies`** pins the adapter's meaning against a live server:
  - a gzip body counts its compressed length, and a fresh cache hit of it counts nothing;
  - a response served after a 304 counts nothing, while its first response counts its body;
  - a POST body counts as sent;
  - a refused dial sends nothing;
  - an action refused before dispatch is `none`.

  `TestHTMLStepsResolveAgainstTheCurrentPage` checks that an `image_check` step is `none`.
- **`TestNativeByteSeriesSumsRatesAndDistributions`** checks sums, rates, the per-request minimum, maximum, average and percentiles, the action filter and empty buckets. Its expected values are worked by hand from the fixture. The fixture includes a measured zero, a same-named gRPC action, a business transaction, a `none` step and a legacy request.
- **`TestNativeByteSeriesInvalidCountsAndExactSums`** covers `invalid_byte_count` and `exact_sum_limit`.
- **`TestClearedEvidenceKeepsProtocolAndByteEvidence`** (`pkg/engine`) covers the evidence clear: byte evidence and HTTP survive, and a JMS dependency type is still cleared.
- **`TestMetaRefreshKeepsThePagesByteEvidence`** covers merging a refreshing page into its follow-up, including a follow-up refused before sending.
- **`TestNativeByteSeriesTotalsNeedCompleteBuckets`** covers an uncollected source, best-effort coverage, and a bucket past the run's completion.
- **`TestNativeByteSeriesRoute`** runs `POST /tests/{id}/archive/native-series` with a comparison run, and checks that an unsupported filter is refused.
- **`TestRunArchiveWriterStampsHTTPByteEvidence`** covers the controller writer: HTTP, `none`, a business transaction and another protocol. It also covers the merge of generator archives (`mergeRemoteArchiveTransactions`) and the Parquet row conversion.
- **`TestGeneratorArchiveStampsHTTPByteEvidence`** covers the generator writer in the same way.
- **`TestByteEvidenceIsWhatTheAdapterSet`**, **`TestByteMetricsAdvertiseTheArchiveProfile`** and **`TestNativeByteQueryValidation`** cover the label, the catalog and the query contract.
- **Dashboard tests:**
  - `saves an HTTP body byte panel with only an action filter (NA-C)`;
  - `states HTTP body bytes with their unit and unscored reasons, without a latency inspector (NA-C)`;
  - `says why a byte comparison bucket is unscored, in the table and the export (NA-C)`.

## Review

Round 3 found that keeping the dependency type through the evidence clear was broader than needed, and that some wording was inexact. Both are fixed.

Round 2 found these, all fixed:
- the engine's evidence clear removed the labels;
- a meta-refresh follow-up could relabel the page `none`;
- payload telemetry and failed digest replays were documented wrongly;
- the export reported empty buckets as measured.

Round 1 used two reviewers, one on semantics and one on consumers. They found that:
- the claimed "after content decoding" meaning was wrong, and cache hits counted a different basis;
- steps that dispatched no request were stamped as measured zeros;
- sent bytes followed no stated rule;
- the dashboard hid byte reasons in comparisons and exports.

They also found these, all fixed in code or docs:
- start-time bucketing and hard-stop omissions were undocumented;
- the rolling-upgrade behaviour was undocumented;
- invalid counts had no reason of their own;
- comments were stale;
- some paths were untested.

## Remaining

- **Live byte queries.** Interval byte totals cannot give a distribution.
- **gRPC and WebSocket byte semantics.**
- **Throughput by arrival time.** Requests are bucketed by start.
- **Unlabelled byte counts.** The archive's dataset CSV export, transaction SLA byte rules and the transaction drill-down still read byte counts without the evidence label.
- **The rest of NA-C:** saved-panel revisions and pins, streaming CLI workflows, and the physical-writer ownership left open by the [writer ownership slice](../roadmap-native-writer-ownership-2026-10-25/README.md).
