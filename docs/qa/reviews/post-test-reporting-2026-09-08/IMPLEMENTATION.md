# Post-test reporting implementation and review

The audit's seven findings are addressed in the completed-run workspace. Summary, Performance, Diagnostics and Evidence replace the competing primary destinations. Compare and Export remain header actions; the execution snapshot is secondary. Explicit feature-flag opt-out and old deep links remain compatible.

| Audit finding | Implemented change |
|---|---|
| Contradictory acceptance | Shared computed assessment in results and preview; analyst commentary cannot replace it. No criteria remains Not assessed. |
| Mixed populations and false zeros | Explicit action, named-transaction and legacy populations; sample counts and whole-run scope; absent percentiles and empty baselines remain unavailable; tiny errors retain nonzero notation; no fabricated sparklines. Generated narratives and document percentages follow the same distinction. |
| Competing completed-run destinations | View results is the primary library/dashboard action; completed-run selection opens the consolidated workspace by default. |
| Repeated exports | One audience/format workflow with read-only preview and explicit Edit commentary; raw evidence lives under Evidence. |
| Inconsistent comparison | Explicit completed baseline picker, no automatic pinned selection, context warnings, elapsed-time alignment and filter-preserving diagnostic comparison. Multi-run exploration identifies its baseline; release-trend output is identified separately as prior-run comparison. |
| Analysis mixed with governance | Four operator-oriented areas; metadata and raw files progressively disclosed; generic HTTP payload sizes no longer produce SOAP diagnostics. |
| Clipped/dense presentation | Wrapped preview title and toolbar; modal portal above the application header; retained table styles and contained horizontal scrolling; library action remains visible. |

## Review fixes

The implementation review additionally fixed missing-data sparklines, baseline curves extending beyond their interval range, stale report/baseline state, lost diagnostic filters, preview download persistence after switching back from editing, preview toolbar interception, missing diagnostic stylesheet imports, overflowing filter/table layouts, and hidden baseline context when the comparison controls are collapsed.

## Verification

- `go test ./...`: passed on the final backend changes (`/tmp/report-go-complete.log`).
- Dashboard unit, type, lint, embedded build and browser workflow checks are recorded in the final QA entry in [`design-qa.md`](../design-qa.md).
- Browser evidence: `09-implemented-summary.jpg`, `10-implemented-preview.jpg`, `11-implemented-no-samples.jpg`, `12-implemented-diagnostics.jpg`, `13-implemented-evidence.jpg`.
- Existing unrelated workspace edits were retained.

## Runtime boundary

The port 4000 preview uses the revised frontend with the already-running controller on port 8080. That controller was not interrupted because it contains existing runs. The backend changes were tested with freshly built isolated workflow controllers. Rebuild/restart the normal controller to activate the revised generated-export narratives, SOAP detection and optional release-trend omission there. Existing saved annotations are preserved.
