# Additional HAR import evidence — 2026-10-02

The three additional sources were inspected offline. Both ZIP files were bounded
and extracted into private temporary storage; source files were unchanged. Their
contents were treated as data, and no recorded host was contacted. Customer
captures, URLs, credentials, cookies and payload values are excluded from the
repository. See the aggregate evidence (`additional-evidence.json`) for digests.

## Source results

| Source | State | Entries | Default application actions | Filtered static assets | Skipped proxy tunnels | Decoded JSON responses | Generic correlation candidates |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Desktop_attone.lightning.force.zip | Valid HAR; extracted size 96,121,070 bytes | 347 | 153 | 194 | 0 | 88 | 208 |
| Login_Prod_7_18_2.zip | Valid HAR with UTF-8 BOM; extracted size 31,375,024 bytes | 273 | 124 | 71 | 78 | 89 | 110 |
| KM_ACCPerf_1121_LoginWorking.har | Truncated JSON; complete import rejected | Unknown final count | — | — | — | — | — |

The two valid captures account for all 620 entries: 277 application actions,
265 default-filtered assets and 78 proxy tunnels. Both drafts pass DSL validation,
retain status assertions and preserve request bodies. Neither applies automatic
correlations. Candidate counts describe detected values requiring review, not
proven replay fidelity.

The Desktop ZIP contains a HAR JSON document with a `.com` filename rather than
a `.har` extension. Its extracted private copy was named `.har` for evaluation;
the ZIP and member were not executed. The dashboard HAR picker still expects an
extracted HAR or JSON file; this work does not introduce automatic ZIP ingestion.

KM ends inside a JSON string. A private analysis-only prefix containing the first
68 complete entries was evaluated to check decoding: 56 application actions,
12 filtered assets, 49 decoded JSON responses and nine correlation candidates.
Five correlations generated extractors and rewrote 11 request bodies, as expected
from template substitution. This prefix is not a recovered complete recording,
was not committed, and does not change the importer's rejection of truncated
JSON. A complete re-export is needed before KM can be imported faithfully.

## Evidence-driven changes

1. HAR parsing accepts one leading UTF-8 BOM without altering original source
   bytes retained for review. Misplaced/repeated BOMs and truncated JSON remain
   errors.
2. Proxy CONNECT entries are excluded from application actions and correlation
   observations. They remain counted as skipped requests, with source-indexed
   informational findings and original source evidence. A proxy tunnel response
   cannot generate an application extractor. Replay proxy policy still requires
   separate configuration.
3. Salesforce review uses the same HAR body, base64, compression, form-parameter,
   cookie and repeated-header decoding as generic import. It preserves original
   one-based source entry IDs across skipped tunnels and carries the binary-body
   rewrite guard into correlation analysis.
4. Authenticated HAR and Salesforce upload envelopes are raised from 50 MiB to
   100 MiB to accommodate the Desktop recording. The primary and comparison HAR
   pickers use the same per-file ceiling. The aggregate HTTP request, including
   both files and multipart framing, must fit the 100 MiB envelope. Other file
   pickers and the 50 MiB import-asset upload ceiling retain their existing limits.

## Remaining review limits

Salesforce review matches all retained application requests in both captures:
153/153 for Desktop and 124/124 for Login. Its bounded analysis retains 64 and
113 native correlation candidates respectively. Both native previews pass DSL
validation, but neither applies correlations automatically; Salesforce review
blockers remain visible. These counts differ from generic correlation because
Salesforce inspects static entries and includes shape-specific discovery.

Desktop records both HTTP/1.1 and HTTP/2, preserving 153 default-mode version
blockers through the script-wide transport fidelity gate. Nine Content-Length
findings remain. Login retains 15 Content-Length findings. These mismatches are
not inferred away. Recorded credentials and request-URL findings also remain.

Including static assets yields 347 Desktop actions and 195 Login actions, with
zero request-body changes. Desktop's broader correlation scan reaches bounded
analysis limits and retains 183 manual candidates; Login retains 131 candidates.
Proxy tunnels remain skipped even in static-asset mode. No execution, reachable
host, live credential or browser replay compatibility claim is made.

## Validation

Synthetic regression fixtures cover BOM acceptance/source preservation and
malformed input, proxy accounting and correlation exclusion, dense baseline IDs
versus original source IDs, Salesforce decoding/native extractors, bounded upload
acceptance/rejection, authorization snapshots and HAR picker limits. The picker
checks retain the smaller limit for other formats and exercise both primary and
comparison HAR files. Validation results are recorded in
the additional validation summary (`additional-validation.json`).

The full integrated Go suite passes (117 tested packages and 6 packages without tests).
Dashboard type checking, lint, all 4,756 unit tests across 455 files and the build
pass. The affected HAR comparison and file-picker browser workflows pass in both
dev-admin and local-auth profiles (six tests per profile). API documentation was
regenerated with unchanged outputs; documentation and whitespace checks pass.

Changes require reimporting the capture; previously saved drafts are unchanged.
