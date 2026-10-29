# Completed browser implementation reviews — October 2026

Status: Historical. Consolidated on 2026-10-19. These are completed, scoped implementation reviews, not closure of their parent requirements. Original dates, failed checks, pending acceptance and qualification limits remain below. Evidence files remain at their original paths. Use the [review index](../../qa/reviews/README.md) for outstanding work and other records.

## Records
- [Browser legacy-summary availability correction — 2026-10-05](#feature-completion-browser-availability-2026-10-05)
- [Browser resource/document evidence — 2026-10-05](#feature-completion-browser-document-2026-10-05)
- [HAR import compatibility — scoped converter qualification](#feature-completion-har-import-compatibility-2026-10-05)

<!-- Original record: docs/qa/reviews/feature-completion-browser-availability-2026-10-05/README.md; evidence kept outside git (formerly tracked in docs/qa/reviews/feature-completion-browser-availability-2026-10-05/) -->
<a id="feature-completion-browser-availability-2026-10-05"></a>

## Browser legacy-summary availability correction — 2026-10-05

Status: corrective implementation, independent review and final composed repository qualification verified. Full browser evidence and intelligence completion remains open.

This slice implements the missing-data correction required by BE-2 and BI-1 without claiming document-finality or standards-qualified Core Web Vitals. The active frontend model is `legacy-summary-availability-v2`, applicable only to existing action-summary averages. Its seven equally weighted legacy components require finite nonnegative values and a positive safe-integer `Samples` count. Explicitly present zero is an observation; absent, malformed, negative and nonfinite values are unavailable. Missing required components prevent an overall estimate. Observed components, fixed contributions, field availability and exclusion reasons remain visible. Field availability is not document or time coverage.

Only complete device summaries are ranked. The former arithmetic is available solely through an explicit `legacy-canary-v1` historical reproduction helper. Existing API browser scalars cannot establish qualified zero or independent per-metric eligibility. Root-cause reporting therefore leaves browser qualification unscored, preserves explicit SLA/critical/warning priority, and emits only clearly labeled legacy advisories for valid observed positive exceedances.

<a id="feature-completion-browser-availability-2026-10-05--implementation-and-review"></a>

### Implementation and review

The shared browser breakdown model now supplies live, archived, comparison-chart and comparison-card admission. Missing fallback members cannot become zero or disappear from a weighted cohort. Malformed null, primitive, array and nested identity values retain explicit gap rows and cannot crash React labels. Internal cohort identities use raw identities and unavailable-source tuples rather than friendly display labels; case aliases and labels resembling unavailable markers remain separate. CSV exports preserve exact cohort identities and empty unavailable cells, and charts do not connect across missing measurements.

Review/fix/review corrected stable weighted-mean rounding, unsafe runtime cohort dereferences, object-valued display labels, friendly-label cohort collisions, contradictory duplicate comparison-card averaging, and clipped screenshot geometry. Regressions cover boundary values, malformed archives, sample-count admission, permutation/collision behavior, mixed cohorts, live/archive rendering and real CSV downloads. Independent final source and visual reviews found no additional actionable issue.

The registered `workflow-browser-resource-evidence.spec.ts` retains resource filtering and initiating-action inspection and adds legacy availability, complete-device ranking and mixed/malformed comparison workflows. Both authentication profiles exercise all four cases. Controlled API fixtures and authorized queued runs supply the evidence; these checks do not measure browser document metrics or qualify native HAR publication. Narrow-view whole-box, scroll-owner, overflow and center-hit assertions are retained. Final element captures are dev-admin live (`../../qa/reviews/feature-completion-browser-availability-2026-10-05/evidence/browser-full-final-dev-admin-live.png`), dev-admin archive (`../../qa/reviews/feature-completion-browser-availability-2026-10-05/evidence/browser-full-final-dev-admin-archive.png`), local-auth live (`../../qa/reviews/feature-completion-browser-availability-2026-10-05/evidence/browser-full-final-local-auth-live.png`) and local-auth archive (`../../qa/reviews/feature-completion-browser-availability-2026-10-05/evidence/browser-full-final-local-auth-archive.png`). Capture provenance (`../../qa/reviews/feature-completion-browser-availability-2026-10-05/evidence/browser-full-final-captures.json`) records exact sources, bytes, hashes and the full-cycle receipt.

<a id="feature-completion-browser-availability-2026-10-05--final-verification"></a>

### Final verification

Pinned Node 22.22 and Go 1.26.5 use the recorded constrained profile, shared session Go cache and ports 18471/18472. Every recorded check has a sibling raw log and JSON execution receipt containing command, working directory, UTC interval, duration, exit and raw-log hash.

| Check | Actual result | Receipt |
| --- | --- | --- |
| Final affected frontend units | 97/97 passed across six files | receipt (`../../qa/reviews/feature-completion-browser-availability-2026-10-05/evidence/browser-units-composed-final.json`) |
| Final focused dev-admin workflow | 4/4 passed; Playwright 9.9 seconds | receipt (`../../qa/reviews/feature-completion-browser-availability-2026-10-05/evidence/browser-dev-affected-composed.json`) |
| Final focused local-auth workflow | 4/4 passed; Playwright 37.3 seconds | receipt (`../../qa/reviews/feature-completion-browser-availability-2026-10-05/evidence/browser-auth-affected-composed.json`) |
| Full `npm run test:cycle` | Exit 0; typecheck, lint, build; 509 unit files / 5,596 tests; dev-admin 236 passed / 5 skipped; local-auth 167 passed / 1 skipped; 2,370.810 seconds | receipt (`../../qa/reviews/feature-completion-browser-availability-2026-10-05/evidence/browser-full-cycle-final.json`) |
| Affected Go root-cause normal | Passed; package 3.170 seconds, command 90.451 seconds | receipt (`../../qa/reviews/feature-completion-browser-availability-2026-10-05/evidence/browser-root-cause-normal.json`) |
| Affected Go root-cause race | Passed; package 3.122 seconds, command 133.414 seconds | receipt (`../../qa/reviews/feature-completion-browser-availability-2026-10-05/evidence/browser-root-cause-race.json`) |
| Final composed `go test ./...` | Passed; 1,133.940 seconds, including the frozen final embedded dashboard | receipt (`../../qa/reviews/feature-completion-browser-availability-2026-10-05/evidence/browser-composed-full-go-repeat1.json`) |

The full dashboard raw log is retained as `evidence/browser-full-cycle-final.log`, 87,435 bytes, SHA256 `e53dd254002d90e490dedc37f186c6cee519ea4afe95f701c659e3dcc1a53534`. All affected browser cases passed in both profiles. Four dev-admin skips are viewer/non-administrator profile cases; the fifth dev-admin skip and the one local-auth skip require the unavailable private LoadRunner acceptance archive. None is a skipped browser-availability case.

The final embedded inventory (`../../qa/reviews/feature-completion-browser-availability-2026-10-05/evidence/browser-final-embedded-files.json`) records the completed full-cycle build: 247 files, 8,424,956 bytes, inventory SHA256 `135f7fc7882ad6c8809c6bb600bab6ba1b0bf770e1ad213e3955a646143d6efc`. Its timestamp precedes the remaining full-cycle browser phases; those phases subsequently passed. The bundle remains frozen. Earlier full Go execution used an earlier build inventory and failed the newly added opt-in Kafka suite documentation/runner registration checks. That failed `browser-composed-full-go.log/.json` is retained as failed. Registration was corrected in a separate reviewed commit. The final composed repeat passed against the frozen final bundle; its raw log SHA256 is `3ed271eba780f376f237b2cb025cb963230cf34a5147e39c5713f0c1093e77e6`. All embedded file hashes were rechecked after that gate. The final source inventory (`../../qa/reviews/feature-completion-browser-availability-2026-10-05/evidence/browser-final-source-inventory.json`) captures the reviewed composed source after the completed gate, rather than claiming a historical start-time capture.

<a id="feature-completion-browser-availability-2026-10-05--retained-failures-and-limits"></a>

### Retained failures and limits

All failed executions remain in `evidence/` with their receipts. Initial typecheck exposed an over-narrow fixture type; initial units exposed weighted-mean roundoff; two comparison-card attempts exposed missing application/release fixture context; the first composed build exposed an implicit-any fixture; one build attempt ran from the wrong directory and failed with ENOENT. Corrected affected repeats and the final complete dashboard cycle passed. Historical focused runs before the final comparison-card/source corrections are not used to qualify the final source.

Open completion requirements include document/navigation/request identity, clock qualification and lifecycle finality; independently eligible final-document distributions; standards CLS/INP/LCP qualification; native/synthetic HAR receipt, protected finalization, quotas and crash/deletion/recovery; Chromium/WebKit acceptance and measured benchmark publication; versioned persisted qualified findings, baseline matching, interaction/journey models and full browser intelligence workflows. This correction does not close BE-1–BE-4 or BI-1–BI-4. Native 1 GiB reference-load, five-platform, process-restart/HA and remote-backend acceptance also remain open elsewhere in the program. Only the 10 GB analytics benchmark is waived at user direction.

<!-- Original record: docs/qa/reviews/feature-completion-browser-document-2026-10-05/README.md; evidence kept outside git (formerly tracked in docs/qa/reviews/feature-completion-browser-document-2026-10-05/) -->
<a id="feature-completion-browser-document-2026-10-05"></a>

## Browser resource/document evidence — 2026-10-05

Status: the native capture, typed propagation, protected sidecars and bounded document inspection slice is implemented, reviewed and qualified by the gates below. Full BE-1–BE-4 and BI-1–BI-4 remain open. [Usage and evidence semantics](../../guides/BROWSER_INTELLIGENCE_WORKFLOWS.md#document-and-navigation-evidence).

Document capture is explicit and capability-gated. Resource identity v2 separates request, frame, document/navigation, redirect and initiation/completion action lineage. The current producer proves request/frame/navigation/redirect and both action IDs; resource document association/offset, cache and initiator provenance remain unavailable. Native missing phase sentinels stay unavailable. Document evidence uses the original pinned web-vitals 5.1.0 implementation, measurement revisions, provisional/final/censored lifecycle states, compatible actual cohorts and independent eligibility counts. Bounded mergeable histograms estimate quantiles; action latency and legacy common Samples do not acquire document semantics. Live first-seen provisional history can overlap later final history, whereas archive provisional counts mean latest pending measurements.

The engine/RPC/collector path retains typed bounded evidence and explicit drops. JSONL/Parquet sidecars use canonical names, opened-object hashes, bounded counts, quota accounting and owned remote materialization. Capture authority survives stored groups and terminal trimming; an explicitly requested missing peer remains incomplete. Strict canonical JSON admission rejects malformed, omitted, duplicate, aliased and over-limit fields. Invalid optional evidence does not manufacture successful zero-duration transactions. Authorized live/archive/report and apexctl analysis browser-documents project the same populations without a health or CI verdict.

<a id="feature-completion-browser-document-2026-10-05--native-correctness-and-review"></a>

### Native correctness and review

The native qualification index (`../../qa/reviews/feature-completion-browser-document-2026-10-05/evidence/browser-runtime-final-qualified-index.json`) separates genuine native acquisition from source tests and transport fixtures, retaining failures as well as successful repeats. Chromium 149.0.7827.55 and WebKit 26.5 exercised same-URL frame identities, reload, no-interaction availability, a request spanning distinct actions, cancellation and physically blocked drain. Chromium separately exercises real layout-shift session ground truth and 50 trusted interaction rank/outlier ground truth. The runtime source freeze (`../../qa/reviews/feature-completion-browser-document-2026-10-05/evidence/browser-runtime-source-freeze.json`) records pinned assets and reviewed inputs; the runtime/engine/RPC race gate (`../../qa/reviews/feature-completion-browser-document-2026-10-05/evidence/browser-runtime-shared-engine-rpc-final-race.json`) passed. These are local correctness fixtures, not overhead, whole-page or hardware benchmarks.

Review/fix/review corrected stale-generation callbacks, immutable redaction ownership, effective cohort/cache qualification, distinct runtime availability, malformed wire admission, mixed legacy intervals, sidecar integrity and transfer bounds, summary deduplication/finality, required-peer preservation, nullable generated contracts, and bounded CLI projection. The final reviewed native sources passed their named genuine acquisition and race repeats. Runtime evidence remains independently scoped from the dashboard's controlled transport fixtures.

<a id="feature-completion-browser-document-2026-10-05--dashboard-and-repository-gates"></a>

### Dashboard and repository gates

The final [frontend verification record](BROWSER_SLICES_2026-10.md#feature-completion-browser-document-2026-10-05-frontend-verification) qualifies 5,688 unit tests and the complete dashboard cycle: dev-admin 238 passed / five scoped skips; local-auth 169 passed / one private-fixture skip. None of the eight affected browser-evidence workflows was skipped. Their final corrected-controller repeat passed all eight cases in each profile against the exact unchanged frontend and embedded bundle, retaining 44 successful JSON geometry/focus attachments and ten screenshots. The proof indices separately bind the final six corrected backend inputs. Root inspection of the final natural narrow live/archive screenshots confirms readable evidence within its owned scrolling regions; transport fixtures remain separate from genuine native acquisition.

Review/fix/review corrected an authoring deferred-focus race and a native comparison card's screen-reader description overflow. Their initial full-cycle failures and retained trace/source inspections remain historical evidence. The subsequent corrected full cycle passed; an earlier Go gate does not qualify a later changed source.

Additional engine/archive review separates dropped document observations from request-identity losses. Unknown provider/identity-ledger exclusions mark incompleteness without inventing distinct loss counts; unchanged unavailable providers do not repeat idle publications. A controlled terminal-window regression exposed final evidence arriving after the last action and active-VUser-zero acknowledgement. Pending observation windows now keep idle/shutdown publication alive until the snapshot acknowledges them, independently of coverage-change markers. Normal/race repeats and the final independent review are retained.

The CLI now validates the actual typed archive report's nested manifest identity. Canonical IDs require no extra request; authorized RefKey aliases use one bounded exact canonical/ref_key/project lookup. Duplicate, malformed and conflicting identities are refused; historical missing identities are explicitly unverified. Normal/race repeats and the actual-wire independent review are retained. The final corrected-controller repeats above include these corrections.

The first frozen full Go gate (`../../qa/reviews/feature-completion-browser-document-2026-10-05/evidence/browser-composed-full-go-final.json`) remains historical acceptance for its exact earlier source. The final corrected-source repository run hit Go's default ten-minute API-package alarm while still progressing through its more than five thousand tests; all other packages passed, and no individual test failure preceded the alarm. Its immutable failed receipt (`../../qa/reviews/feature-completion-browser-document-2026-10-05/evidence/browser-terminal-and-cli-final-full-go.json`) and raw log remain intact. The test running at the alarm passed three focused repeats in the triage receipt (`../../qa/reviews/feature-completion-browser-document-2026-10-05/evidence/browser-final-readiness-timeout-triage.json`). The complete corrected-source repeat (`../../qa/reviews/feature-completion-browser-document-2026-10-05/evidence/browser-terminal-and-cli-final-full-go-timeout-repeat1.json`), using a thirty-minute package timeout, passed in 1,268.365 seconds; the API package passed in 671.392 seconds. Its source bookends are identical and the raw log SHA-256 is `d46a4debb0b05c4194c2bae95ca604cadae97b9c8c8c50fe062812c8510333ef`. The final independent six-input/timeout review (`../../qa/reviews/feature-completion-browser-document-2026-10-05/evidence/browser-final-timeout-and-six-input-independent-review.json`) found no actionable regression on the unchanged final six-input digest `b3af9298582e169c779629ff8a526e3f0b187442d0ca37d73f24946fc086c398`.

<a id="feature-completion-browser-document-2026-10-05--remaining-qualification"></a>

### Remaining qualification

Real BFCache and hidden-resume breadth across engines; wider cache/service-worker/redirect/cross-origin/flood/secret/crash cases; complete resource drill-down and native/synthetic HAR lifecycle; source overhead, clock uncertainty and whole-page completeness; browser intelligence/baseline/heatmap/journey workflows; and hardware/platform acceptance remain open. This slice does not claim real-device fidelity or a new browser-density target. Native 1 GiB reference, five-platform, process-restart/HA and remote artifact acceptance remain open elsewhere in the program. Only the 10 GB benchmark was waived by the user.

<!-- Original record: docs/qa/reviews/feature-completion-har-import-compatibility-2026-10-05/README.md; evidence kept outside git (formerly tracked in docs/qa/reviews/feature-completion-har-import-compatibility-2026-10-05/) -->
<a id="feature-completion-har-import-compatibility-2026-10-05"></a>

## HAR import compatibility — scoped converter qualification

This slice adds two synthetic regression files against the existing shared HAR parser and LoadRunner companion-capture matching seam. It makes no production decoder or replay-policy changes. The parent independently rereviewed the final source and qualification receipts with no remaining findings in this scope. The broader feature program remains open.

<a id="feature-completion-har-import-compatibility-2026-10-05--delivered-coverage"></a>

### Delivered coverage

The [sample regression](../../../pkg/convert/sample/har_sample_shape_test.go) covers raw and base64 logical text with retained gzip framing, binary resources, missing text, explicit truncated-content provenance, original source-entry IDs, source timestamp precision, unchanged unavailable or invalid timing metadata, and private correlation-header normalization. Unsupported textual HTTP versions remain value-free quality observations under inherited replay policy; they do not acquire a fabricated version gate. Explicit unavailable content provenance keeps its existing durable review gate.

The [companion regression](../../../pkg/convert/salesforce/har_sample_shape_test.go) uses the same private source-observation parser. Repeated method/URL requests match their original entry IDs using their distinct request bodies. The actual analyzer reports complete fixture matching without mutating the script or capture bytes. These fixtures contain only newly invented URLs, credentials, headers, bodies, identities and timestamps. They do not read or reproduce the operator's capture.

The current-checkout reuse audit (`docs/qa/reviews/feature-completion-har-import-compatibility-2026-10-05/evidence/current-checkout-har-reuse-audit.md`) identifies existing LoadRunner companion `har_file` handling, private baseline/protected credential plumbing and source governance. The resume implementation's existing bounded storage-only decoder is reused; no parallel decoder or older primary-checkout decompression implementation was introduced. The protected primary checkout remains unchanged.

<a id="feature-completion-har-import-compatibility-2026-10-05--final-checks"></a>

### Final checks

| Gate | Exact receipt | Result |
|---|---|---|
| Two synthetic regression functions, two repeats | focused checks (`../../qa/reviews/feature-completion-har-import-compatibility-2026-10-05/evidence/har-sample-shape-focused-final-2.json`) | PASS, 8.711 seconds |
| Complete sample, Salesforce, HAR and request-import package suites | affected packages (`../../qa/reviews/feature-completion-har-import-compatibility-2026-10-05/evidence/har-sample-shape-affected-final-1.json`) | PASS, 10.113 seconds |
| Two synthetic functions with the race detector, two repeats | targeted races (`../../qa/reviews/feature-completion-har-import-compatibility-2026-10-05/evidence/har-sample-shape-race-final-1.json`) | PASS, 42.054 seconds |
| Bounded original-capture parsing and conversion offline | aggregate probe (`../../qa/reviews/feature-completion-har-import-compatibility-2026-10-05/evidence/original-har-offline-probe-1.json`) | PASS, 10.608 seconds |
| Independent final source/receipt rereview | parent review (`../../qa/reviews/feature-completion-har-import-compatibility-2026-10-05/evidence/parent-independent-repeat-review.json`) | No additional findings |

All four final execution receipts verify the same eleven directly inspected source/module inputs before and after execution. This is not a full transitive Go dependency inventory. The final source inventory (`../../qa/reviews/feature-completion-har-import-compatibility-2026-10-05/evidence/final-source-inventory.json`), qualification record (`../../qa/reviews/feature-completion-har-import-compatibility-2026-10-05/evidence/final-qualification.json`) and bundle index (`../../qa/reviews/feature-completion-har-import-compatibility-2026-10-05/evidence/bundle-index.json`) preserve exact hashes. Raw logs are retained beside the receipts. Helper source is retained as non-executable text; its paths describe the historical private execution environment. New-file whitespace checks emitted no errors; their `git diff --no-index --check` exit 1 denotes the expected file difference against `/dev/null`.

<a id="feature-completion-har-import-compatibility-2026-10-05--original-capture-aggregate-compatibility-only"></a>

### Original capture: aggregate compatibility only

The user-provided input was 21,441,296 bytes with SHA-256 `e7b8dc723f4bfb4c28489297606b97cccb75c06adad8c7777757c016e43c3703`. Its before/after hash remained identical. The original HAR, private response bodies, headers, URLs, credentials, observations and correlation values were not copied into this bundle or Git.

The existing converter retained 158 of 158 requests: 148 complete logical text bodies, 2 binary bodies and 8 unavailable bodies. It generated 158 status assertions, 39 correlation candidates and 32 extractors; these are authoring outputs, not executed target assertions or validated replay correlations. Diagnostics counted 437 informational messages, 307 warnings and zero blockers.

All 158 version observations were unavailable quality gaps, so the importer preserved inherited version policy. Source timing metadata retained 28 unavailable send phases, 28 unavailable wait phases and 146 invalid negative connect values. These counts do not qualify analytical timing series, establish measured zeros or supply missing native browser transport boundaries.

The probe installed rejecting default HTTP transports and recorded zero attempted HTTP requests. It constructed no controller, managed run or browser, and performed no replay. Its aggregate-only output and bounded process/source limits are retained in the exact receipt and helper source.

<a id="feature-completion-har-import-compatibility-2026-10-05--retained-attempts-and-limits"></a>

### Retained attempts and limits

Three red attempts remain evidence: the first used an action-style method on the internal exchange's string HTTP version; the second compared the preserved offset timestamp directly with a UTC rendering; the third incorrectly expected an unsupported textual version to receive a durable gate. The fixtures were corrected to the existing typed quality/provenance contract. None required a production code change. Earlier focused and four-package green checks remain superseded because their source preceded the final projection-count and typed-quality assertions; they verify nine direct inputs and are not the final candidate's qualification.

The bundle index (`../../qa/reviews/feature-completion-har-import-compatibility-2026-10-05/evidence/bundle-index.json`) labels each historical or final attempt and verifies every copied receipt/log hash. API upload and protected persistence, actual runtime replay, native completed-run HAR publication, cross-process capture ownership/cleanup, source-clock alignment and full program/release acceptance are not established by this slice. Full repository qualification against the final composed candidate remains a separate merge gate.
