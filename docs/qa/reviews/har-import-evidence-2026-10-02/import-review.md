# HAR import fidelity evidence — 2026-10-02

Two supplied HAR 1.2 recordings were converted offline. Source URLs, credentials,
cookies, payload values and the original recordings are excluded from this report
and its regression fixtures. No recorded request was executed. Original files were
read without modification; their SHA-256 digests are in the aggregate evidence.

## Observed results

| Recording | Source entries | HTTP actions | Static entries filtered | Skipped requests | Decoded JSON responses, before → after | Correlation candidates, before → after |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| WLS_MultiLineCRP.har | 158 | 135 | 23 | 0 | 0 → 117 | 32 → 54 |
| CM_Perfhyper1_RegressionRel_Sept_HAR.har | 301 | 202 | 99 | 0 | 0 → 175 | 31 → 72 |

Both final drafts pass DSL validation. Every retained request has a status
assertion. Their request bodies equal the corresponding recorded baseline bodies;
no automatic correlation substitutions were applied to either recording.
Default filtering accounts for all 459 source entries: 337 actions plus 122
static entries. Static filtering is configurable with `include_static_assets`.

The normal dashboard HAR uploader uses multipart transport. Both individual
files fit the then-existing 50 MiB HAR request-body limit (21,441,296 and 45,630,835
bytes). Their individually JSON-wrapped contents also fit that limit; no upload
limit was changed in this first pass. The [additional capture review](additional-review.md)
subsequently raised HAR capture uploads to 100 MiB. The recordings describe different journeys and were assessed
independently rather than treated as a record/replay comparison pair.

Machine-readable [before evidence](before.json), [after evidence](after.json),
and [static-asset mode evidence](with-static-assets.json) contain only aggregate
counts, diagnostic categories, file names and digests.
Diagnostic counts can be lower than affected-entry counts because identical
findings at the same method/URL are deduplicated.

## Fixes supported by the recordings

1. **Response decoding.** Both exporters retain `Content-Encoding: gzip` after
   decompression. Many response resources then receive HAR base64 encoding. The
   importer previously attempted gzip decompression again and fell back to the
   base64 spelling, preventing JSON analysis. It now recognizes readable UTF-8
   resources before attempting transport decompression. Gzip, zlib/raw deflate
   and Brotli exports containing compressed bytes remain supported, and the
   existing 10 MiB decompression expansion limit remains enforced. This follows
   the [HAR content contract](https://w3c.github.io/web-performance/specs/HAR/Overview.html),
   which describes response resources after transport decoding. Unencoded/Raw
   JavaScript resources containing Unicode control characters are preserved
   without attempting decompression.
2. **Unavailable version metadata.** The exporter sentinel `unknown` is retained
   in `har.request.http_version`, with an informational finding. It no longer
   creates unsupported-version execution gates. Go uses its normal negotiation
   policy. Explicit unsupported versions and exact-version conflicts still pass
   through the shared fidelity gates; `_http2_info` alone is not treated as proof
   of an exact recorded protocol version.
3. **Recorded body framing.** A recorded Content-Length matching the captured
   request bytes is removed from executable headers and retained as
   `har.request.content_length`. Go regenerates framing after template expansion.
   Invalid, signed, conflicting and mismatched lengths still require review.
   Byte comparison covers UTF-8 and base64 binary bodies, while unrelated
   duplicate headers retain their order.
4. **Bounded correlation review.** The CM recording contains a response whose
   discovery exceeds a bounded scan. Previously, incomplete checked analysis
   discarded every candidate. HAR import now retains the bounded discoveries
   for manual review and surfaces a warning in both warnings and diagnostics.
   Every candidate from incomplete analysis has `pre_applied: false` and
   `review_action: manual_value_replacement_required`. No extractor or request
   rewrite is generated from that incomplete result. Complexity limits remain
   unchanged; other sample formats retain their existing checked-analysis policy.

## Review and execution limits

The 72 CM candidates are a bounded, incomplete review result, not a claim of
complete correlation coverage. The warning identifies observation 17 and the
retained discovery count. The WLS analysis completes, but its detected consumers
require manual replacement under the existing correlation application rules.
Detected candidates are not equivalent to executable extractors.

Opting into static assets produces 158 and 301 actions respectively, with zero
request-body mismatches or decompression warnings. Both opt-in analyses are
incomplete and retain manual candidates (78 and 116). CM additionally retains
seven exact HTTP/1.1 version-review gates because a script mixing explicitly
recorded versions and unavailable versions cannot establish strict transport
parity through the current script-wide negotiation contract. These gates are
preserved rather than inferred away.

Each recording still has a request-URL warning and imported-credential review
warnings. These remain visible; decoding and framing fixes do not prove that
recorded credentials are current or that the target hosts are reachable. No
runtime, browser replay or live-host compatibility claim is made. Reimporting is
required to obtain the new behavior; previously saved drafts are unchanged.

## Regression coverage

Synthetic tests use `example.invalid` and invented values. They cover decoded
plain/Raw/base64 resources with retained gzip, deflate, Brotli and zstd headers;
UTF-8 and whitespace preservation; real compressed resources; corrupt data and
expansion bounds; matching and invalid body lengths; preserved duplicate order;
unavailable and unsupported versions; candidate provenance; and incomplete
analysis without automatic rewriting.

Run the affected tests with:

```sh
go test ./pkg/convert/sample ./pkg/convert/har ./pkg/convert/correlate
```

Validation status: affected-package tests pass with caching disabled. The full
`GOMAXPROCS=4 go test -p 1 ./...` suite passes (117 tested packages and
6 packages without tests). Documentation checks and `git diff --check`
also pass. Initial full-suite attempts encountered disk exhaustion during
compilation/linking; older regenerable compiler-cache archives were removed and
the complete final-source suite was rerun successfully. See
[validation summary](validation.json).
