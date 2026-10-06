# HAR import compatibility — scoped converter qualification

This slice adds two synthetic regression files against the existing shared HAR parser and LoadRunner companion-capture matching seam. It makes no production decoder or replay-policy changes. The parent independently rereviewed the final source and qualification receipts with no remaining findings in this scope. The broader feature program remains open.

## Delivered coverage

The [sample regression](../../../../pkg/convert/sample/har_sample_shape_test.go) covers raw and base64 logical text with retained gzip framing, binary resources, missing text, explicit truncated-content provenance, original source-entry IDs, source timestamp precision, unchanged unavailable or invalid timing metadata, and private correlation-header normalization. Unsupported textual HTTP versions remain value-free quality observations under inherited replay policy; they do not acquire a fabricated version gate. Explicit unavailable content provenance keeps its existing durable review gate.

The [companion regression](../../../../pkg/convert/salesforce/har_sample_shape_test.go) uses the same private source-observation parser. Repeated method/URL requests match their original entry IDs using their distinct request bodies. The actual analyzer reports complete fixture matching without mutating the script or capture bytes. These fixtures contain only newly invented URLs, credentials, headers, bodies, identities and timestamps. They do not read or reproduce the operator's capture.

The [current-checkout reuse audit](evidence/current-checkout-har-reuse-audit.md) identifies existing LoadRunner companion `har_file` handling, private baseline/protected credential plumbing and source governance. The resume implementation's existing bounded storage-only decoder is reused; no parallel decoder or older primary-checkout decompression implementation was introduced. The protected primary checkout remains unchanged.

## Final checks

| Gate | Exact receipt | Result |
|---|---|---|
| Two synthetic regression functions, two repeats | [focused checks](evidence/har-sample-shape-focused-final-2.json) | PASS, 8.711 seconds |
| Complete sample, Salesforce, HAR and request-import package suites | [affected packages](evidence/har-sample-shape-affected-final-1.json) | PASS, 10.113 seconds |
| Two synthetic functions with the race detector, two repeats | [targeted races](evidence/har-sample-shape-race-final-1.json) | PASS, 42.054 seconds |
| Bounded original-capture parsing and conversion offline | [aggregate probe](evidence/original-har-offline-probe-1.json) | PASS, 10.608 seconds |
| Independent final source/receipt rereview | [parent review](evidence/parent-independent-repeat-review.json) | No additional findings |

All four final execution receipts verify the same eleven directly inspected source/module inputs before and after execution. This is not a full transitive Go dependency inventory. The [final source inventory](evidence/final-source-inventory.json), [qualification record](evidence/final-qualification.json) and [bundle index](evidence/bundle-index.json) preserve exact hashes. Raw logs are retained beside the receipts. Helper source is retained as non-executable text; its paths describe the historical private execution environment. New-file whitespace checks emitted no errors; their `git diff --no-index --check` exit 1 denotes the expected file difference against `/dev/null`.

## Original capture: aggregate compatibility only

The user-provided input was 21,441,296 bytes with SHA-256 `e7b8dc723f4bfb4c28489297606b97cccb75c06adad8c7777757c016e43c3703`. Its before/after hash remained identical. The original HAR, private response bodies, headers, URLs, credentials, observations and correlation values were not copied into this bundle or Git.

The existing converter retained 158 of 158 requests: 148 complete logical text bodies, 2 binary bodies and 8 unavailable bodies. It generated 158 status assertions, 39 correlation candidates and 32 extractors; these are authoring outputs, not executed target assertions or validated replay correlations. Diagnostics counted 437 informational messages, 307 warnings and zero blockers.

All 158 version observations were unavailable quality gaps, so the importer preserved inherited version policy. Source timing metadata retained 28 unavailable send phases, 28 unavailable wait phases and 146 invalid negative connect values. These counts do not qualify analytical timing series, establish measured zeros or supply missing native browser transport boundaries.

The probe installed rejecting default HTTP transports and recorded zero attempted HTTP requests. It constructed no controller, managed run or browser, and performed no replay. Its aggregate-only output and bounded process/source limits are retained in the exact receipt and helper source.

## Retained attempts and limits

Three red attempts remain evidence: the first used an action-style method on the internal exchange's string HTTP version; the second compared the preserved offset timestamp directly with a UTC rendering; the third incorrectly expected an unsupported textual version to receive a durable gate. The fixtures were corrected to the existing typed quality/provenance contract. None required a production code change. Earlier focused and four-package green checks remain superseded because their source preceded the final projection-count and typed-quality assertions; they verify nine direct inputs and are not the final candidate's qualification.

The [bundle index](evidence/bundle-index.json) labels each historical or final attempt and verifies every copied receipt/log hash. API upload and protected persistence, actual runtime replay, native completed-run HAR publication, cross-process capture ownership/cleanup, source-clock alignment and full program/release acceptance are not established by this slice. Full repository qualification against the final composed candidate remains a separate merge gate.
