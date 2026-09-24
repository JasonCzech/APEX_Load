# JMeter fidelity follow-up code review

Two review-and-fix passes examined conversion, extraction, finite execution, generator capability/admission, archive generation, persistence, and download handling. Review scope was the JMeter fidelity implementation; unrelated concurrent LoadRunner and messaging work was preserved.

## Pass 1

| Finding | Fix | Regression |
| --- | --- | --- |
| A missing JSONPath ancestor returned a miss before an invalid trailing expression was parsed, allowing `on_miss: default` to hide invalid configuration. | Parse the complete supported expression before evaluating any response fields. | `TestHTTPExtractorDefaultDoesNotSuppressErrors`: missing ancestors, malformed suffixes, unsupported indices, and wrong response types. |
| Java date patterns containing doubled apostrophes lost the literal apostrophe. | Preserve escaped apostrophes both outside and inside quoted literals. | `TestJavaTimeLayoutPreservesEscapedApostrophes`. |
| CSV filenames containing a quote produced malformed Content-Disposition headers; international filenames were not encoded. | Use the standard MIME parameter encoder for artifact downloads. | `TestCSVExportDownloadFilenameEncoding`: quoted, international, ordinary, and uppercase-extension filenames through the API. |

## Pass 2

| Finding | Fix | Regression |
| --- | --- | --- |
| The native CSV recognizer ignored processor ordering: a writer before extraction, or before a later overwrite, could export a different value than JMeter. | Translate only when the last enabled assignment to the selected extraction precedes the writer; retain a durable processor gate otherwise. | `TestCSVWriterRequiresFinalSourceExtraction`, including disabled later extractors. |
| Converter, DSL, and artifact filename rules disagreed on uppercase extensions and noncanonical names. | Share one filename validator across all three layers; preserve uppercase extensions, and review-gate whitespace, Windows drive-relative names, control characters, and empty stems. | `TestCSVExportDeclarationValidation`, `TestCSVWriterRejectsUnrepresentableFilenames`, and the download filename regression. |
| Redaction could collapse distinct action names or change a selected name, yielding extra or missing CSV rows. | Bind samples to declared export IDs before redaction; retain bindings, including explicitly empty bindings, in local and generator JSONL archives. Legacy records without bindings retain the previous matching path. | `TestCSVExportIdentityPrecedesNameRedaction`, `TestCSVExportIdentitySurvivesRedactedNameCollision`, and `TestCSVExportIdentityGeneratorArchiveRoundTrip`. |
| Sensitive extraction defaults could bypass name-based redaction when the sample lacked a value. | Apply the extraction name’s sensitivity policy to both archived provenance defaults and exported fallback cells. | `TestCSVExportSensitiveFallbackIsRedacted`. |

Generated OpenAPI and TypeScript contracts include the archive export bindings. The original attachment remains unchanged.

## Validation

- Focused regressions across JMeter conversion, HTTP extraction, DSL validation, engine, generator, and API: passed.
- Focused race checks covering engine, HTTP extraction, generator execution/archive, and API export paths: passed.
- Dashboard type checking, lint, fresh embedded build, and all 422 unit-test files / 4,465 tests: passed.
- Initial browser cycle: dev-admin had 136 passes, four expected profile/fixture skips, and one scroll assertion failure; local-auth had 56 passes, one private-fixture skip, and the same assertion failure. The geometry test now uses the supported reduced-motion preference, captures scroll at the actual click event after Playwright positioning, and accounts for clamping to a shorter tab. The assertion still rejects an application-induced jump. The complete workflow passed twice in each authentication profile after this correction. The initial single `test:cycle` command was not green; every failed case has a passing final rerun.
- Final-build JMeter browser workflows: two tests passed in dev-admin and two in local-auth.
- Final CSV boundary race tests across converter, DSL, and API: passed.
- Full Go suite: passed (`go test -timeout 30m -p 2 ./...`), including generated contract freshness. The API package completed in 364.603 seconds.

The first full Go attempt exhausted local disk space. Unused compiler scratch directories were removed only after confirming no process held them open; the failed job was stopped and the full suite restarted. This environment failure is not counted as a passing or skipped check.

All seven review findings above are fixed and covered by regressions. No unresolved finding remains from these review passes. The original attachment SHA-256 remains `b92245ad1443767a6d9e20c87bc6988b04cff804368b5dcafca180e1be9b0cb0`.
