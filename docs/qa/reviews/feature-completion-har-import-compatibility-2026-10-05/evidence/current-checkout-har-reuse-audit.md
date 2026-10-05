# Current-checkout HAR reuse audit

The user confirmed the protected current checkout at `/Users/jasonczech/Documents/GitHub/Project_Stormrunner`. This read-only audit used its existing LoadRunner archive companion HAR workflow and the standalone HAR importer as the implementation reference. No primary source was changed.

## Existing reusable seams

- `dashboard/src/components/EnterpriseArchiveImport.tsx` accepts a companion `har_file` and shows match coverage. `pkg/api/handlers_import_enterprise.go` reads that multipart file, passes it to `salesforce.Analyze`, and surfaces coverage/native-preview review. This supports same-recording request matching and correlation; it is not native completed-run browser HAR publication.
- `pkg/convert/salesforce/analyzer.go` retains original one-based source-entry matching, method/URL/body matching and the 80% native-preview coverage gate. The resume implementation already routes companion parsing through `sample.HARSourceObservations`; reuse that shared parser rather than duplicating another JSON/body decoder.
- `pkg/convert/har/converter.go` already exposes standalone conversion, a private correlation baseline, design/pair/runtime-canary artifact modes and governance metadata. Source body capture is explicitly required, the source is sensitive, and protected requirements transfer outside public JSON. Converter results and private observations are not disclosure-safe completed-run export objects.
- Existing API protected import/credential/source-retention/artifact storage workflows remain the authority for immutable source storage and ownership. No raw user HAR or private baseline should enter a public conversion response or Git.

## Sample-shaped compatibility evidence

The original 21,441,296-byte HAR remains only at its user-provided private path; SHA-256 `e7b8dc723f4bfb4c28489297606b97cccb75c06adad8c7777757c016e43c3703`. Offline aggregate inspection found 158 entries, 127 pages, 128 base64 bodies and 22 plain-text bodies. Of those bodies, 148 are already decoded UTF-8 despite a recorded gzip header; 2 are binary. Recorded HTTP version strings are not supported version observations. Unavailable send/wait phases and invalid negative connect values must not become fabricated analytical zero values. These counts are structural evidence, not API acceptance or successful replay.

The protected primary parser still attempts Content-Encoding decompression of logical HAR content and its Salesforce decoder copies raw response text. The resume parser already has bounded storage-only decoding, typed response-content provenance and the shared companion observation seam. Applying the older primary decoder over the resume implementation would regress this sample; no backport or new helper is needed here. Standard base64 storage is decoded once; Content-Encoding is retained as source evidence, not reapplied. Binary, malformed, unavailable and explicit failure provenance stay distinguishable. Unsupported HTTP version strings must not become a replay override or unbounded active metadata.

## Scope for the next implementation

Add two small synthetic-only regression files at the existing sample and Salesforce seams: raw/base64 text with stale gzip framing, binary/missing/explicit-unavailable content, unknown-version/source provenance, immutable source entry IDs and repeated companion request matching. URLs, credentials, headers and bodies are newly invented; no source values are copied from the capture. An optional offline original-capture converter probe may print aggregate counts/quality only and must never create a controller, run or target connection.

Browser HAR delivery remains gated by the separate BE-3 audit and mapping-feasibility supplement: retained browser resource rows lack a measured send-completion boundary and faithful decoded-size/MIME/protocol evidence. Their combined request-to-first-byte duration cannot be invented as separate HAR send/wait phases. Import compatibility does not establish safe native export, multi-process writer ownership, bounded flush/drain or completed-run publication.

## Exact evidence and limits

Source inventory: `/private/tmp/apexload-primary-har-reuse-audit-source-20261005.json` (45 primary files; 22 differ from the reader resume checkout). Function comparison: `/private/tmp/apexload-primary-har-reuse-function-comparison-20261005.json`. Sample evidence remains aggregate-only in the structural, vendor-shape, body-encoding, compression-class and decompression compatibility JSON receipts. This audit inspected bounded sections and relevant functions/tests; it did not run imports, APIs, browsers or target traffic, and does not qualify the whole feature program.
