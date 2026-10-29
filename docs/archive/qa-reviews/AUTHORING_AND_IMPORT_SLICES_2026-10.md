# Completed authoring, scenario & import slice reviews — Sep–Oct 2026

Status: Historical. Consolidated on 2026-10-29 from completed scoped review records under `docs/qa/reviews/`. Original dates, validation outcomes, failed intermediate checks, source hashes and qualification limits are preserved intact below. Untracked raw evidence remains local at the cited paths. Use the [active review index](../../qa/reviews/README.md) for current capstone records.

## Records
- [Recording/replay implementation slices — 2026-10-03 (`aura-record-replay-2026-10-03/implementation.md`)](#aura-record-replay-2026-10-03-implementation)
- [Independent import-fidelity code review — 2026-10-01 (`clm-import-2026-10-01/code-review.md`)](#clm-import-2026-10-01-code-review)
- [CLM import response performance — 2026-10-01 (`clm-import-2026-10-01/import-response-performance.md`)](#clm-import-2026-10-01-import-response-performance)
- [CLM VuGen collection import and review — 2026-10-01 (`clm-import-2026-10-01/import-review.md`)](#clm-import-2026-10-01-import-review)
- [CLM Java Kafka import fidelity — 2026-10-01 (`clm-import-2026-10-01/kafka-fidelity.md`)](#clm-import-2026-10-01-kafka-fidelity)
- [Web archive import fidelity — 2026-10-01 (`clm-import-2026-10-01/web-fidelity.md`)](#clm-import-2026-10-01-web-fidelity)
- [Authoring fidelity repair — 2026-10-05 (`feature-completion-authoring-fidelity-2026-10-05/README.md`)](#feature-completion-authoring-fidelity-2026-10-05)
- [Import publication primitive qualification (`feature-completion-import-publication-2026-10-05/README.md`)](#feature-completion-import-publication-2026-10-05)
- [Scenario revision foundation and capability refusal — 2026-10-05 (`feature-completion-scenario-revision-2026-10-05/README.md`)](#feature-completion-scenario-revision-2026-10-05)
- [Private scenario source admission qualification (`feature-completion-scenario-source-admission-2026-10-05/README.md`)](#feature-completion-scenario-source-admission-2026-10-05)
- [Additional HAR import evidence — 2026-10-02 (`har-import-evidence-2026-10-02/additional-review.md`)](#har-import-evidence-2026-10-02-additional-review)
- [HAR fidelity code review — 2026-10-02 (`har-import-evidence-2026-10-02/code-review.md`)](#har-import-evidence-2026-10-02-code-review)
- [HAR import fidelity evidence — 2026-10-02 (`har-import-evidence-2026-10-02/import-review.md`)](#har-import-evidence-2026-10-02-import-review)
- [LoadRunner import, Script Editor and debug readiness — 2026-10-24 (`import-repair-readiness-2026-10-24/README.md`)](#import-repair-readiness-2026-10-24)
- [Import reviews with open items (September 2026) (`import-reviews-2026-09.md`)](#import-reviews-2026-09)
- [Independent review: LoadRunner TLS import and Review-tab editing (`import-tls-independent-review-2026-10-27.md`)](#import-tls-independent-review-2026-10-27)
- [Java VuGen import and review — 2026-10-01 to 2026-10-02 (`java-dependency-import-2026-10-01/import-review.md`)](#java-dependency-import-2026-10-01-import-review)
- [Java dependency import fidelity — 2026-10-01 (`java-dependency-import-2026-10-01/java-dependency-fidelity.md`)](#java-dependency-import-2026-10-01-java-dependency-fidelity)
- [Monitoring and fleet audit (2026-09-12) (`monitoring-audit-2026-09-12/README.md`)](#monitoring-audit-2026-09-12)
- [Lifecycle property matrix — 2026-10-21 (`roadmap-lifecycle-property-matrix-2026-10-21/README.md`)](#roadmap-lifecycle-property-matrix-2026-10-21)
- [Revise-copy intent keys — 2026-10-21 (`roadmap-revise-copy-intents-2026-10-21/README.md`)](#roadmap-revise-copy-intents-2026-10-21)
- [Scenario prerequisite handoff — 2026-10-19 (`roadmap-scenario-prerequisite-2026-10-19/README.md`)](#roadmap-scenario-prerequisite-2026-10-19)
- [Contextual validation revisions — 2026-10-21 (`roadmap-validation-context-2026-10-21/README.md`)](#roadmap-validation-context-2026-10-21)
- [TruClient import and review — 2026-10-01 (`truclient-import-2026-10-01/import-review.md`)](#truclient-import-2026-10-01-import-review)
- [TruClient import fidelity — 2026-10-01 (`truclient-import-2026-10-01/truclient-fidelity.md`)](#truclient-import-2026-10-01-truclient-fidelity)
- [UX reviews with open items (September 2026) (`ux-reviews-2026-09.md`)](#ux-reviews-2026-09)
- [Browser and HTTP recording/replay roadmap (`web-record-replay-roadmap-2026-10-03/roadmap.md`)](#web-record-replay-roadmap-2026-10-03-roadmap)
- [Recording and replay usability audit (`web-record-replay-roadmap-2026-10-03/usability-audit.md`)](#web-record-replay-roadmap-2026-10-03-usability-audit)

<!-- Original record: docs/qa/reviews/aura-record-replay-2026-10-03/implementation.md; local evidence kept outside git -->
<a id="aura-record-replay-2026-10-03-implementation"></a>

## Recording/replay implementation slices — 2026-10-03


Requested scope: implement the evidence-sufficient roadmap items, commit each slice after tests and an independent review/fix cycle with no remaining actionable findings. No supplied target host or physical device is needed for local development. Actual Chrome/iPad and live Salesforce session qualification remain separate evidence requirements.

Planned slices, in delivery order:

1. Structured Aura envelope/outcome validation in HTTP replay, baseline outcome review and review UI.
2. Reusable local React/Aura fixtures for reordered batches, HTTP-200 errors, delayed UI results and client-state behavior.
3. Nested encoded Aura parameterization with precise bounded edits and substitution tests.
4. Correlation producer/consumer and incomplete-coverage review improvements.
5. Recording engine/device identity, explicit iPad emulation and record/replay mismatch review.
6. Parent-session concurrent HTTP request groups with consistent load/replay behavior.
7. Per-request HTTP version policy and transport isolation.
8. Shared bounded response decoding and compression handling.
9. Authoritative, bounded, redacted debugger pause-state snapshots.

Each slice records implementation, test evidence, independent findings and repairs, clean re-review, and its commit before the next slice begins. Customer payloads, hosts, cookies and credentials stay outside committed evidence. Existing safeguards and unresolved source gates remain explicit.

<a id="aura-record-replay-2026-10-03-implementation--slice-1-complete-and-independently-reviewed"></a>
### Slice 1 — complete and independently reviewed

Implemented a shared value-free Aura envelope parser, strict `aura_actions_success` HTTP assertion, durable native-import review gates for invalid requests, baseline outcome evidence in HAR/archive/recording review, and a paginated source-linked outcome panel. Actual replay uses resolved outgoing wire bytes and preserves HTTP status when business actions fail. The editor admits the assertion only for HTTP and clears incompatible legacy fields when selecting it.

Independent review and fixes:

| Finding | Repair and regression evidence |
| --- | --- |
| Large runtime batches could silently lose diagnostic details through shared evidence bounds | Retain 64 outcomes and 64 issues, prioritize failure outcomes, preserve full validation/counts, and disclose omitted rows; actual 256-action replay tests cover late and all-error failures. |
| Aura requests nested inside control trees were omitted | Bounded traversal of every typed control branch and HTTP-sequence request, stable exact action paths, mutable rewrite pointers, malformed-request gate persistence and save/reopen tests. |
| An incidental REST `descriptor` field could trigger a false native Aura gate | Known REST/UI API routes take precedence; concrete Aura endpoint/envelope evidence is required. |
| Import review metadata could exceed disclosure and save limits | Global 2,048 outcome and 2,048 shared finding budgets, explicit per-batch/aggregate omissions and global failure priority; synthetic 400×256 case preserves 102,400 matches and keeps the script at 4,030,822 bytes, where an unbounded review produced 7,405,550 bytes and failed projection. |
| Encoded-body admission could gate a valid imported Aura request | Validate bounded decoded base64/Windows-1252 wire bytes while preserving source body and encoding; invalid wire bytes keep safe enum gates through reopen. Independent encoded-wire review and regressions passed. |
| Editor legacy assertion fields could block the selected strict assertion; recorder protocol could expose an HTTP-only option | Clear incompatible fields on selection and pass the actual effective protocol; stateful editor tests. |
| Real HAR upload failed before review because the project field followed file bytes | HAR and Salesforce wrappers send project scope as a leading multipart field and query parameter, retaining native/transform options; hook tests and actual import API browser flows. |
| Review could hide imported-request diagnostics or imply complete evidence from inconsistent wire counts | Separate request/capture diagnostics, defensive completeness checks, retained-detail disclosures and accessible remaining-detail controls. |

Two independent core/runtime re-reviews and the independent importer/API re-review reported zero remaining actionable findings after these repairs. Root independently reviewed the dashboard/client changes and visually inspected synthetic desktop and narrow screenshots. Encoded-wire admission also passed independent re-review with zero remaining actionable findings. Final full-suite gates passed before commit.

Private offline evidence: 409 inspected batches, 504 request actions, 503 structurally matched responses and 25 captured ERROR actions under HTTP 200. One Login response with duplicate nested fields remains incomplete under the strict parser. All 409 original HAR source positions are accounted for. No private target was contacted; these counts are capture inspection rather than live Salesforce/device qualification. See `capture-check.json`.

Verification: full Go suite passed with the finalized source (`GOMEMLIMIT=8GiB go test -p 2 -timeout 20m ./...`); race-enabled Aura parser/HTTP/engine tests passed; dashboard `npm run test:cycle` passed global typecheck, lint, 4,781 tests across 457 files, production build, 194 dev-admin workflows (five optional skips), and 115 local-auth workflows (one optional skip). The new Aura workflow executed in both profiles, including safe state display, filtered-HAR source navigation and narrow layout. Documentation checks passed for 103 active Markdown files; generated DSL/API and QA artifacts are fresh, and `git diff --check` is clean. Synthetic screenshots are saved in `screenshots/`, with machine-readable results in `slice1-validation.json`.

Commit: the first implementation commit on `codex/aura-record-replay-fidelity`; Git history records its identity. The next slice begins only after this commit.

<a id="aura-record-replay-2026-10-03-implementation--slice-2-local-reactaura-evidence-fixture"></a>
### Slice 2 — local React/Aura evidence fixture

Added a reusable bounded loopback target with real installed React18 assets, controlled input, accessible controls and per-mount DOM identities. Session bootstrap supplies a cookie, token, framework context and fresh action IDs. The target rejects cross-session tokens/IDs and reused IDs, reverses response ordering, and supports delayed SUCCESS, ERROR, INCOMPLETE, missing and duplicate action outcomes under HTTP 200. Exported fixture statistics contain counts only. Unknown routes return 404; assets, request bytes, action counts, session/ID retention and delay are bounded.

The browser regression records actual interactions with sidecar HAR capture, waits for the final business result before stopping, and imports that completed HAR through the real HAR/Salesforce pipeline. It verifies original HAR entry identity and complete matched Aura outcomes. The recorded browser actions run in two fresh contexts and again after navigation in each existing context. Exact client-state and business-result assertions are independent of successful clicks. Generated DOM identities are compared only in memory; diagnostics expose counts. Four additional replay contexts prove fulfilled HTTP-200 failure responses cannot satisfy a successful business assertion. CI explicitly requires this real browser regression alongside the existing React polling regression.

Independent review found that revision-only DOM IDs could repeat across fresh contexts. The fixture now uses a per-mount random identity plus revision; actual browser replay verifies distinct identities across nine mounted pages. Review also caught repeated protocol registration in the HTTP table tests; registration now occurs once and repeated shuffled runs pass. An implementation-mirroring HTML substring test was removed; actual captured assets and matched business outcomes are checked by the required browser regression. Root independently reviewed the shared helper and engine tests; peer review of the helper, browser regression and CI selection reported zero remaining actionable findings. This fixture is synthetic desktop Chromium evidence; physical Chrome/iPad and live Salesforce qualification remain outstanding. No generated HAR, session values or raw request/response captures are committed.

Verification: affected packages (`pkg/internal/aurafixture`, `pkg/engine`, `pkg/recorder/browser`, `pkg/convert/har`, `pkg/convert/salesforce`) passed. The new real browser regression passed three consecutive runs; the exact two-test CI selection passed with `APEXLOAD_BROWSER_TESTS=require` and `-race`. Helper/engine fixture tests passed with race detection, three runs and shuffled ordering. Documentation checks passed for 103 active Markdown files; QA artifact freshness and whitespace checks passed. Slice two adds test infrastructure and CI enforcement without modifying production replay behavior. Full Go/dashboard merge gates remain required before integrating the complete series.

<a id="aura-record-replay-2026-10-03-implementation--slice-3-encoded-aura-parameter-editing-and-replay"></a>
### Slice 3 — encoded Aura parameter editing and replay

Added a strict bounded codec for direct JSON and URL-form Aura message bodies, including base64 storage and up to three JSON-in-string layers. Typed routes identify one decoded leaf independently of repeated values. Apply verifies the trusted original value, full private shape, public descriptor and encoding layers before atomically editing a cloned script. Unselected source spans retain their original bytes. Automatic advice admits string business fields on disclosure-safe known paths; numeric/other fields, unknown source-valued keys, malformed bodies and exhausted budgets remain explicit.

The runtime opt-in resolves authored templates once, escapes inserted values at every JSON/form layer, and produces matching executable/redacted bodies. Inserted literal template syntax remains data. Unsupported encoding, unresolved authored parameters, invalid shapes and work/occurrence limits fail before sending a request. Secret preflight inspects the same decoded surfaces. Legacy runtime substitution behavior is unchanged.

Import advice carries exact action paths, decoded fields, ordered encoding layers and count-only coverage. Salesforce inspection uses the same codec and keeps distinct bindings for equal values at distinct routes. The server regenerates trusted instructions and rebinds protected drafts after credential projection; client samples do not authorize edits. Native Aura review now requires selection and preview before apply. Public advice/diffs hide captured literals and raw payloads.

Independent review repaired private/public shape mismatches across protected drafts, source-valued key disclosure, repeated-field binding collisions, shared multi-script work/detail accounting, and the native review bypass discovered by the real browser test. Runtime reviews repaired template rescanning, static-secret preflight gaps and bounded escape/allocation accounting. Existing metrics tests now wait for an observed published snapshot instead of relying on fixed short sleeps; two dashboard stress tests have explicit local time budgets. No production metrics behavior changed.

Offline inspection of the four supplied captures recognizes 409 Aura bodies: 405 pass strict structured inspection and four are rejected. All 86 admitted business suggestions are retained. An additional 16 unclassified bodies are omitted by detection admission; unknown/source-valued paths remain explicitly incomplete. None of the 57 captured JSON-as-string leaves yields an admitted nested business suggestion under the safe route policy. Synthetic fixtures establish nested-edit/replay capability; these captures do not establish complete application parameterization or new-session replay. See `slice3-capture-coverage.json`.

Final boundary review found that ordinary disclosure did not inspect neutral encoded JSON wrappers, and malformed/trailing JSON could hide a credential from replica collection. Shared credential projection now redacts nested semantic leaves under bounded work/value/depth/output limits; known-value collection handles duplicate members, Unicode, malformed credential hints and cross-field replicas. Explicit `apiToken` vocabulary preserves the exact-name classifier boundary. Allocation guards run before form splitting. JSON query strings and actual GraphQL envelopes retain distinct parsing. Protected draft classification falls back to bounded request-body projection differences when guided locators do not cover every masking change, preserving exact source attestation and tamper protection. Exact operator-owned import drafts retain executable source values; these masking changes apply to disclosure copies.

The broad Go gate exposed legacy credential commit tests that bypassed the authenticated inspection capability. Their revised regressions prove rejection and no persistence without that capability, then verify an authorized save preserves exact base64 and typed JSON source bytes. Malformed JSON with completely read scalar values can still produce a masked, gated review when only a final container terminator is missing; unread or hidden credential syntax remains fail-closed. Invalid template-shaped credential literals and their outside replicas are masked, while supported references and builtins remain portable without evaluation. Canonical receipt comparison ignores only the recognized transient projection marker on a private clone, preserving durable review gates, source behavior and the exact execution digest. Actual JMeter batch save/retry tests cover this compatibility repair. Coverage wording now reports bodies not fully inspected without attributing all detection omissions to inspection limits.

Final semantic boundary review also repaired malformed query dispatch in JSON wrappers, declared and inferred GraphQL bodies, and URL-form query fields. Supported GraphQL arguments, comments, block strings and numeric exponents retain their source spelling, as do ordinary SQL/search text and already masked query scalars. Incomplete quoted text cannot hide escaped credential member syntax or trigger repeated suffix rescanning. Repeated central lexer passes share bounded byte/token admission before semantic allocation, with no limit fallback. Protected source bytes remain unchanged throughout these disclosure repairs.

Two independent final core/API re-reviews reported zero remaining actionable findings. Root independently reviewed the final shared codec, runtime, protected-draft and dashboard changes, reproduced the repaired masking and budget boundaries with count-only synthetic probes, and visually inspected all four final desktop/narrow parameter-preview screenshots. Final affected package and focused race gates passed.

Verification: the finalized frozen source passed the full Go suite (`GOMEMLIMIT=8GiB go test -p 2 -timeout 20m ./...`), including the API package in 457.319 seconds. Final affected credential/disclosure package tests and focused race tests passed. Dashboard final typecheck, global lint, 23 targeted UI tests and production build passed; both required Aura browser workflows passed in dev-admin and local-auth. The earlier full dashboard cycle passed 4,791 tests across 458 files, 195 dev-admin workflows (five optional skips) and 116 local-auth workflows (one optional skip); the final wording/caption regressions were covered by the final affected checks. Documentation checks passed for 103 active Markdown files, QA/DSL/API generated artifacts are fresh and whitespace checks are clean. Results are saved in `slice3-validation.json`. Full integrated dashboard checks remain required before merging the completed series.

Commit: the third implementation commit on `codex/aura-record-replay-fidelity`; Git history records its identity. The next slice begins only after this commit.

<a id="aura-record-replay-2026-10-03-implementation--slice-4-correlation-relationships-and-original-source-review"></a>
### Slice 4 — correlation relationships and original-source review

Added bounded, value-free producer/consumer observation coverage to both diagnosis APIs. One checked detection/candidate pass supplies advice and relationship states. Full counts distinguish missing producers, unavailable extracted values, missing consumers, stale literals, unchanged producers and unresolved encoded/surface uses; at most 256 detail rows carry numeric step identities and fixed rule/state enums. Work exhaustion returns no partial advice. Zero detected bindings and truncated baselines remain incomplete, and completeness describes inspected evidence rather than successful correlation or replay. An HTTP-200 replay failure retains independent manual failure evidence even when stale-value advice also exists. Missing, foreign, expired, wrong-script and unavailable trusted replay capabilities have the same 404 response.

Native Salesforce review now separates original import matches, exact original-source links and native paths at import. Full aggregate counts are computed before detail retention. Private nonserialized identity/weight catalogs rebind paths only after the last executable transform. Source joins require the original path, globally unique mapping ID and exact source file; native joins require verified sequence plus HAR identity and a unique final path. Traversal failure clears native joins. Source/catalog indexing, attempted detail work, rows, consumer references and actual serialized bytes are bounded; omitted details remain explicit. Catalog values cannot be reconstructed from reopened JSON to manufacture new verified links.

The shared review panels paginate retained evidence and expose exact producer/consumer source navigation through import, save and reopen. Native-selected mappings carry an original-import domain and cannot become current editor destinations, repaired bindings or acknowledgements. Historical native drafts without the new domain use a narrow compatibility rule: Salesforce-selected provenance plus native-preview evidence. Generic HAR/archive drafts keep their existing mapping behavior. Saving a source-only draft removes stale legacy mapping annotations while preserving the captured evidence. Unknown domains, malformed rows/references/state counts and omitted details stay incomplete; displayed labels cannot reveal unrecognized source values.

Independent review repaired refundable detail-work budgets that allowed repeated oversized processing, globally duplicated source IDs, stale archive summaries copied before final transforms, missing legacy native provenance, inherited report pagination, malformed row coercion crashes and unsupported state counts hidden behind a complete result. Actual browser import also exposed percent-encoded CometD channels missed by loop admission and generic HTTP request actions skipped by native path binding. Strict single-message parsing preserves malformed, ambiguous, bounded-out and differently configured requests as discrete actions; only registered HTTP request semantics receive the added native binding. Meaningful regressions cover nonrefundable work and JSON escaping, final Agentforce path movement and summary/save-reopen parity, unavailable replay capability disclosure, source-only decisions and editor inference, safe malformed admission, supported/unsupported count controls and pagination reset. The real HAR pipeline regression covers encoded connect conversion, exact source/native joins, unselected form bytes and collapsed-source omissions. Three final independent source re-reviews report zero remaining actionable findings after the strict connect, execution-equivalence and one-pass work repairs. Root and UI peer also reviewed the final actual browser test and all six readable desktop/narrow viewport screenshots, with zero remaining actionable findings.

Offline bounded inspection of the four supplied captures detects 510 correlations and 8,848 consumer request references. Exact source joins account for 290 producers and 7,171 consumer references; 220 producers and 1,677 consumer references remain unmatched. Detail bounds retain 149 rows and 3,642 consumer references, with explicit omissions. Constructed native previews also join 290 producers and 7,171 consumers to exact preview paths; these counts refer only to that construction boundary. All captures remain incomplete and have zero draft replacements in this probe. The probe runs native-preview construction, not the final API transform chain; it establishes no final native path, bootstrap portability, fresh-session replay or physical-device qualification. See `slice4-capture-coverage.json`.

Verification: affected correlation, Salesforce and source-view packages passed; the final Salesforce package passed in 6.082 seconds and its focused shuffled race selection passed three runs. Final API integration tests passed in 2.623 seconds, with two shuffled race runs in 31.891 seconds. Dashboard typecheck (including E2E), global lint, final E2E lint, 180 affected unit tests across 10 files and production build passed. The full affected actual browser selection passed 23 workflows in each of dev-admin and local-auth, with one optional private-file case skipped per profile. Source navigation, native-loop omissions, legacy save/reopen, real HTTP-200 assertion failure and missing-consumer diagnosis all ran against the real local controller. Six synthetic viewport screenshots are saved in `screenshots/`; results are recorded in `slice4-validation.json`. Documentation checks passed for 103 active Markdown files, QA/DSL/API artifacts are fresh, and whitespace checks are clean. Full integrated Go/dashboard gates remain required before merging the completed series.

Commit: the fourth implementation commit on `codex/aura-record-replay-fidelity`; Git history records its identity. The next slice begins only after this commit.


<a id="aura-record-replay-2026-10-03-implementation--slice-5-recording-environment-receipts-and-replay-mismatch-review"></a>
### Slice 5 — recording environment receipts and replay mismatch review

Added a versioned historical recording receipt after successful Chromium launch and context creation. It records the actual engine separately from the requested emulation preset and its browser family, plus explicitly applied viewport, scale, mobile and touch options. Missing options remain unavailable; an explicitly applied false stays distinct. No requested preset alone creates an observation. Device options, receipt pointers, session snapshots, viewer projections, late recovery and converted outputs are detached. Browser transaction splits and HTTP siblings retain the receipt without replacing existing Salesforce/HAR source review, and completed-save recovery retains it after a controller restart.

The shared bounded normalizer reads only recognized fields, rejects malformed versions, engines, profiles, numeric claims, contradictory emulation and excessive viewport/device pixels, and never traverses unrelated claim structures. The controller forces recording-agent handoff claims to agent-reported provenance and unverified physical qualification before attestation and persistence. A direct agent preview reports its own local recording process; the review UI qualifies that channel separately. Saved receipts are historical, editable script metadata rather than independent live device attestation.

The launcher labels presets as workstation emulation. Accessible recording-environment panels appear during recording, in each browser/HTTP output review, and in saved Source Mapping even without source-review metadata. They separate recorded engine, preset browser family and current configured replay engine. The existing iPad preset continues to configure WebKit replay while the observed workstation recording uses Chromium; the mismatch stays visible. Editing replay configuration does not rewrite the receipt. Receipt changes invalidate prior verification. Physical Chrome/iPad qualification remains Unverified after a successful matching Chromium replay.

Independent review repaired viewer receipt retention, combined device-pixel admission, oversized profile/numeric bounds, contradictory emulation, and an accidentally replaced test file by restoring all three existing DOM-review regressions. Actual recording validation repaired test setup for CDP screen observation, agent pairing order and the direct-agent-preview versus controller-save provenance boundary without removing the engine, context or qualification assertions. Final independent Go/API, UI and repaired browser-test source re-reviews report zero remaining actionable findings.

Verification: the final real recording selection passed all ten workflows across dev-admin and local-auth, with no skips or retries. Both controller and agent iPad/Pixel cases check actual screen extent, scale and touch, browser/HTTP receipts, save/reopen, observation-source qualification and replay-config edits. Matching Pixel outputs perform actual browser and HTTP replay; iPad outputs preserve the mismatch and save unverified. The existing mobile HAR-disabled workflow also passes in both profiles. Eight representative synthetic desktop/narrow screenshots are saved in `screenshots/` and were visually reviewed without actionable findings. Focused API tests passed in 1.985 seconds; three shuffled race runs passed in 7.039 seconds; the broader recorder API and generated-contract selection passed in 6.834 seconds. Full recorder unit tests passed in 0.625 seconds and required actual default/iPad/Pixel loopback contexts passed in 2.210 seconds. Dashboard typecheck, global lint, final recording-test lint, 178 affected unit tests and production build passed. Generated DSL/API artifacts are fresh. Results are recorded in `slice5-validation.json`. Full integrated Go/dashboard gates remain required before merging the completed series.

Commit: the fifth implementation commit on `codex/aura-record-replay-fidelity`; Git history records its identity. The next slice begins only after this commit.


<a id="aura-record-replay-2026-10-03-implementation--slice-6-parent-session-concurrent-http-request-groups"></a>
### Slice 6 — parent-session concurrent HTTP request groups

Added explicit sequential/concurrent execution to HTTP sequences, with an optional fanout of 1–16 and a maximum of 256 concurrent children. Omitted mode remains sequential. All outgoing sibling values are prepared deterministically in source order before dispatch; workers share the parent HTTP session and return immutable outcomes. Both execution paths join every worker, publish outcomes and extractions in source order, and report group wall time. Debugger stepping and break-on-failure use the outer group boundary, while retained leaf events identify each exact request and its actual timing. The authoring controls preserve typed children, metadata and unknown parameters through save/reopen and explain session sharing, extraction timing and the join boundary.

Whole-group admission rejects unsupported protocols, invalid headers/auth/targets, sibling extractor dependencies, conflicting outputs and child think time before traffic. Preparation has byte, work, template and node budgets; completed results have separate aggregate rich-evidence and private-correlation bounds. Combined extraction admission is shared by load and replay. Omitted evidence remains explicit without changing real assertion outcomes. Runtime secret values remain executable while published evidence stays redacted.

The LoadRunner importer now turns admissible source concurrency markers into exact HTTP sequences before control/transaction compaction. It preserves full typed requests and exact child source paths. Marker scope includes source file, lifecycle, function, branch and loop identity. Unsupported operations, transaction boundaries inside the group, malformed markers, dependency hazards and exhausted bounds retain durable source-review barriers. Archive-wide inspection and detail accounting expose omissions. Existing isolated parallel-session semantics remain separate from parent-session HTTP concurrency.

Independent reviews repaired stale all-mode extractor dependencies, encoded-body admission and allocation accounting, immutable host/auth preflight, cumulative response retention, completeness claims, attribution and percent think time. They also found sensitive extraction values overwritten by their disclosure copies, load/replay aggregate extraction-limit drift, debugger pause snapshots retaining the last child instead of the failed joined group, and public retention truncating a known secret before redaction. Narrow signed-in browser review also repaired a responsive account footer that extended beyond its rail and covered the new explanation. Regressions cover executable sensitive tokens with safe publication, actual 3×16 KiB admitted versus 5×16 KiB rejected outputs in both paths, and first/last failed siblings with an authoritative joined pause. Further independent review repaired late sibling-secret replicas, immutable inherited matcher sharing, observed-value history across variable-storage failures, incomplete/invalid adapter outputs and diagnostic result-plus-error discard. Sensitive outputs are classified before serial logs, retries or variable storage. Classification failure propagates through joined workers and isolated branches, installs an immutable fail-closed protocol callback redactor, and governs retained loggers, buffered-log epochs, conditions, callback errors, lifecycle teardown and panic details. Exact private execution values and declared aggregation identities retain their existing contracts; public invalidated detail is omitted. Regressions cover real load/replay/serial paths, the 97-child storage-capacity case, long tokens, retained callbacks and every affected lifecycle cleanup path.

The terminal report, API caches and UI now share an explicit unavailable-evidence signal. It removes older details from progress, polling, logs, conditions, screenshots, history, comparisons and downloads. The first signal is immediate even during cancelled-worker drain or before deferred teardown returns. Stale snapshots and embedded IDs cannot clear it; a new run resets it. Historical status, success, timing, source coordinates, leaf ordinals and retention counts remain distinct from missing detail. Bounded report eviction and nil-safe API projection handle omitted events without inventing outcomes. A separate browser case uses synthetic transport DTOs to verify admission, while the other two cases exercise the real importer and HTTP engine. Final independent source re-review reports zero remaining actionable findings. The finalized runtime, API and browser gates passed. Browser validation repaired an empty required base URL in the synthetic DTO fixture; independent re-review confirmed transport interception and zero target traffic for that case.

Offline inspection of the nested web script in the supplied Kafka archive accounts for three groups, six markers and eight requests, with group sizes 1, 4 and 3. All three groups retain review barriers because source transaction operations occur inside them; zero are claimed as native executable groups. Inspection is complete with no detail omissions. No customer host was contacted. See `slice6-source-coverage.json`.

Verification: full affected engine, DSL, protocol, HTTP and Aura payload tests passed; the engine package completed in 81.749 seconds. The finalized concurrent, serial, lifecycle and publication race selection passed three shuffled runs in 471.252 seconds. Full importer tests passed in 7.317 seconds and three shuffled race runs in 11.306 seconds. API debugger tests passed in 4.522 seconds; two shuffled race runs for the joined boundary and evidence invalidation passed in 8.132 seconds. Dashboard checks passed typecheck including E2E, global and final affected lint, the 260-test broad affected unit gate and final admission/history regressions, and production build. The final browser selection passed all six workflows in each auth profile with no skips or retries: two real importer/engine cases, one synthetic transport DTO case and three shared sidebar workflows. Four fresh synthetic desktop/narrow screenshots were visually inspected. Documentation checks passed for 103 active Markdown files, generated DSL/API/coverage and QA artifacts are fresh, and whitespace checks are clean. Results are recorded in `slice6-validation.json`. Full integrated Go/dashboard gates remain required before merging the completed series.

Commit: the sixth implementation commit on `codex/aura-record-replay-fidelity`; Git history records its identity. The next slice begins only after this commit.


<a id="aura-record-replay-2026-10-03-implementation--slice-7-per-request-http-version-policy"></a>
### Slice 7 — per-request HTTP version policy

HTTP request leaves now distinguish an omitted inherited policy from explicit `auto`, `1.1` and `2`. Null, empty, whitespace and malformed leaf values require correction. The existing global setting keeps its contract, including legacy nonempty whitespace selecting automatic negotiation before an exact protocol alias. Exact HTTP/2 retains HTTPS h2 and cleartext prior-knowledge h2c without HTTP/1 fallback.

Per-VUser immutable policy pools share cookies, authentication, cache ownership and a physical first-hop connection ceiling. Cache entries are partitioned by policy. OAuth and PAC derivatives retain session restrictions and connection accounting. Captured request and response versions remain separate observations; replay receipts report the effective policy and an actual supported response version only when wire evidence exists. A cache hit supplies no negotiated version.

HAR conversion preserves supported per-request versions through native Salesforce conversion, grouping, save and reopen. Missing, unknown, malformed and unsupported observations retain durable review gates. Recorder capture/export records client-to-proxy and proxy-to-origin observations explicitly; differing or unavailable legs require review. Legacy recorder exports with fabricated HTTP/1.1 values cannot establish an observation. Valid HTTP/1.1 headers and disabled header occurrences remain preserved; inherited incompatible headers are gated at affected leaves after precedence.

Independent source reviews repaired cloned-transport ALPN inheritance, capture provenance labels, untrusted legacy capture display, premature HAR Connection-header stripping, disabled-header projection and whitespace-only global setting normalization under aliases. The browser coverage description was corrected to claim only exercised behavior. Additional real resource-policy and alias-fallback regressions strengthen verification. Further review repaired capture promotion overriding exact protocol-alias defaults, and generic HTTP evidence projection discarding typed Aura outcomes, resource counts and warning flags. Native receipts now append only fixed transport enums to existing typed evidence; they do not infer details from raw response bodies. Connection-limit regressions exposed a native proxy queue stalled behind an idle socket and an idle destination incorrectly protected by an unrelated active socket. The allocator repair delegates idle selection to the native transport while guarding newly dialed sockets before assignment. Further re-review bounded failed-dial guard retention and strengthened a real detached HTTP/2 handoff regression: a non-idempotent POST must attempt the retired native adapter, retry before body consumption and reach the target exactly once under the physical ceiling. Repeated Aura tests also required stateless one-time fixture registration. Final core and targeted race gates pass. Importer review then corrected credential body classification using a disabled Content-Type; repeated projection/finalization/reopen retains exact source headers and body while active malformed declarations remain gated. Independent source re-reviews report zero remaining actionable findings. The finalized importer, API and actual both-profile browser gates pass. The browser fixture was corrected to persist an explicit policy before removing it; selecting a policy and immediately returning to the original baseline correctly left no change to save. Independent re-review of that correction is clean.

Offline inspection accounts for 1,079 entries in four complete supplied HAR captures: 351 supported HTTP/1.1 observations, 276 HTTP/2 observations and 452 unavailable request-version observations. Actual offline conversion succeeds for all four captures and emits 1,001 HTTP request leaves: 273 exact HTTP/1.1, 276 exact HTTP/2 and 452 inherited policies with exactly 452 durable unavailable-version gates. The 78-entry difference is entirely CONNECT tunnel setup in the Login capture. Other review gates remain independent; these counts do not establish replay readiness. See `slice7-source-coverage.json`. The truncated KM capture is excluded. No supplied host is contacted. Synthetic loopback targets provide transport and replay evidence; physical Chrome/iPad and live Salesforce qualification remain outstanding.

Verification: full affected core tests pass across 22 packages, with final engine tests in 48.737 seconds after the repeat-fixture repair. Allocator regressions pass ten shuffled runs; targeted races pass three shuffled runs across seven core packages. Full converter/recorder tests pass across 35 packages, and repeated shuffled races pass the five affected importer/recorder packages. API scalar, generated-contract and actual replay integration tests pass in 10.539 seconds. Dashboard typecheck, global lint, 495 affected unit tests across 16 files and production build pass. All 11 affected browser workflows pass in each of dev-admin and local-auth with no skips or retries; the real TLS fixture verifies stored policy removal, mixed wire versions, concurrent parent-session cookies, reverse completion with joined extraction, Auto fallback on an H1-only target, and matching retained UI/export receipts. Six fresh synthetic desktop/narrow authoring and replay screenshots in `screenshots/` were reviewed at original resolution by root and an independent peer with zero actionable findings. Results are recorded in `slice7-validation.json`. Documentation checks pass for 103 active Markdown files, generated DSL/API/coverage and QA artifacts are fresh, and whitespace checks are clean. Full integrated Go/dashboard gates remain required before merging the completed series.

Commit: the seventh implementation commit on `codex/aura-record-replay-fidelity`; Git history records its identity. The next slice begins only after this commit.


<a id="aura-record-replay-2026-10-03-implementation--slice-8-shared-bounded-response-decoding"></a>
### Slice 8 — shared bounded response decoding

Shared streaming response decoding now admits only explicit Content-Encoding declarations, in reverse application order, for identity, gzip, zlib/raw deflate, Brotli and Zstandard. Repeated headers retain their order. Encoded input, decoded output, intermediate work, layers, decoder windows and aggregate workspace have separate bounds; cancellation closes the owned source. Malformed, unknown, truncated, trailing, oversized and cancelled representations fail without publishing partial finite-response bytes, extractors or cached content. HTTP, SOAP and recorder paths share the decoder. Request-body decoding remains separate.

Finite HTTP replay validates response completion before admitting logical content. Resource responses validate the full bounded stream even when only a smaller prefix fits the capture/cache budget. Wire byte counters and original headers remain separate from normalized logical snapshots. Cache revalidation retains the actual wire HTTP-version observation while qualifying the body as cached content. Compressed SSE collection may retain sampled events and authored extractors at its collection boundary; it explicitly remains partial and cannot provide complete-response correlation evidence. Current and retained debugger views show those fixed qualifications.

Normal HAR content.text is already decompressed; base64 is a storage encoding rather than another Content-Encoding layer. The importer preserves this distinction, original headers and source positions. A strict recorder provenance envelope distinguishes complete decoded capture from unavailable content. Malformed or contradictory envelopes, uncertain legacy encoding, storage errors and incomplete capture retain source-located execution barriers through finalization, save and reopen. Ordinary optional HAR response absence remains explicitly unavailable without inventing a source failure. Binary resources retain exact source storage and byte counts while being omitted from textual/Aura/correlation baselines. Recorder and HAR response storage share the existing recorder maximum of 16MiB, with independent runtime capture bounds.

Independent review repaired bodyless response handling, 304 cache-header overlay, large resource decoding bypass, misleading unavailable representation basis, partial-SSE formatting failure, deleted capture reason/gate bypass, binary/control-byte roundtrip classification, strict response-only base64 padding, contradictory legacy/bodyless capture and missing HAR response objects. Meaningful regressions use actual open compressed streams, corrupt large resources, bodyless revalidation and wire recording/export/reimport. An actual open gzip SSE replay also exposed a parent snapshot retention enum that rejected partial content; the runtime now preserves that qualification through report, JSON export and UI.

Usable protected-recording credential fixtures now declare their observed HTTP/1.1 proxy legs. Their exact credential, privacy, lineage, baseline, save-proof and tamper assertions remain intact, while a separate legacy fixture retains the missing-version barrier. Trace-based product review repaired nested import-tab gutters and the narrow debugger header flex layout; actual browser checks require readable issue prose, visible header controls and unoccluded receipts. Two long sidebar journeys received bounded 120-second case deadlines after valid traces showed only accumulated 30-second test deadlines. Their 15-second action/expect bounds, 30-second navigation bound and all geometry/behavior assertions remain intact.

Final production and fixture source re-reviews report zero remaining actionable findings. Root and an independent UI peer reviewed all 24 final synthetic screenshots at original resolution with zero remaining findings. The narrow views use a real operator sidebar-collapse action and a 390px desktop Chromium viewport; the issue article is 258px wide, and complete/partial receipts remain readable within a maximum screenshot height of 1,400px. See `slice8-screenshots.json` and `screenshots/`. The final three sidebar journeys passed in both auth profiles. The affected browser checks cover all 24 workflows per profile: dev-admin passed the full selection, and local-auth passed 22 in the full selection plus all three final sidebar regates after the scoped test deadline repair. The initial two deadline failures remain recorded in `slice8-validation.json`.

Offline conversion accounts for all 1,079 entries in four complete supplied captures: 1,001 HTTP leaves and 78 CONNECT tunnel entries. It admits 912 complete response representations (826 text and 86 binary), with 129,139,445 aggregate declared decoded bytes. Of 89 unavailable representations, 23 have explicit null content.text storage fields and retain source gates; 66 have ordinary optional response absence. Existing version/other gates remain independent, and conversion does not imply replay readiness. No supplied host was contacted. See `slice8-source-coverage.json`.

The HAR representation distinction is supported by the [archived HAR1.2 historical draft](https://raw.githubusercontent.com/w3c/web-performance/gh-pages/specs/HAR/Overview.html). The pinned Zstandard [decoder options](https://github.com/klauspost/compress/blob/v1.19.0/zstd/decoder_options.go) inform the independent window/work/output limits. Supplied captures provide ordinary gzip/Brotli-header and plain/base64 storage evidence; stacked and Zstandard wire decoding are established by synthetic fixtures. Physical Chrome/iPad and live Salesforce qualification remain outstanding.


Verification: final core checks pass across 22 packages; full converter/recorder checks pass across 35 packages, with targeted race coverage. API response-content and replay checks pass. Dashboard app/Node type checking, lint, affected unit tests and production build pass. Documentation checks pass for 103 active Markdown files, generated contracts/QA artifacts are fresh, and whitespace checks are clean. Exact commands, durations, initial failures and final regates are retained in `slice8-validation.json`. Full integrated Go/dashboard gates remain required before merging the completed series.

Commit: `495962e59` — Decode bounded response content and preserve capture provenance. Slice nine began after this commit.

<a id="aura-record-replay-2026-10-03-implementation--slice-9-authoritative-debugger-pause-state"></a>
### Slice 9 — authoritative debugger pause state

The engine captures detached variable bindings, parameter preparation/actual-use receipts, original source-row selections and active phase/control/loop/branch frames at the existing before/after action hooks. Extended hooks replace the legacy hook once. Inspection reads existing state and never resolves a parameter or template, allocates a data row, or advances random/time state. Concurrent HTTP groups capture only the parent before dispatch and after join. Publication still requires an actual held hook; a successful After hook does not itself create a pause.

Full API responses bind the snapshot to a fresh session incarnation and a canonical decimal-string uint64 revision. Explicit null marks unavailable state. Status-only pause requests do not authorize values; the hook must actually hold a validated boundary. Resume, step, stop, cancellation, lost execution ownership, worker finish, retirement and terminal completion revoke authority synchronously. A rapid resume/pause may re-admit only the candidate owned by the same still-waiting hook. Revision exhaustion saturates and permanently removes current-state evidence. Leaf WebSocket messages carry fixed identity/revision/status metadata without repeating binding maps.

Projection validates a closed DTO, redacts before display selection, and bounds the combined count to 256, rows and frames to 64 each, each authored field to 4 KiB, and JSON to 64 KiB. Separate private input/work limits stop expensive projection. Whole oversized/unscannable values are omitted; missing values never use raw prefixes. A complete privacy closure masks neutral replicas even when the credential binding is omitted from display. Classification failure wins over ordinary work exhaustion and removes older runtime detail for the run. Public copies retain no VUser, parameter-runtime, script or caller-owned pointers/backing strings.

The Variables pane defaults to Current paused state. Historical variable reconstruction and selected-execution variable values require an explicit Values-at selection; they cannot fill missing current values. Follow execution may automatically select an event and its resource evidence. Prepared versus observed, empty, redacted and omitted values are distinct. Row receipts preserve original 1-based source coordinates; navigable frames open exact unchanged authored source. Narrow panes display labeled receipt cards.

Review/fix cycles repaired switch-default path admission, canonical parameter aliases, detached string storage, ordinary oversized-name classification, matcher-failure priority, rapid re-pause admission, irreversible private-candidate clearing, and worker-finish revocation before ownership release. UI review repaired foreign-session invalidation, closed-shape validation, the early-start WebSocket race, qualified data lookup, runtime-frame source priority, and stale same-incarnation privacy admission. Actual browser review also repaired a narrow inspector allocation that clipped complete parameter receipts. A scoped shell-height rule now preserves the dock floor and provides a real center-body of at least 440px; regressions retain every card field, clipping intersection, pixel-hit and bounded viewport assertion. Synthetic transport fixtures now provide the required identity/revision/full-null metadata and explicitly select retained history. They separately exercise an older terminal publication and a later valid terminal publication, preserving all privacy, export and history assertions.

A real browser failure exposed a terminal polling recovery gap: the full GET contained the failed event and captured resource, but retirement left the selected event empty. Polling now selects the actual terminal report's first failed event only while Follow execution remains enabled. Explicit operator selection with Follow off is preserved, privacy masks still apply, and the retained report cannot restore current-state authority. Three deferred-GET component cases verify those behaviors. A deterministic real-controller browser fixture suppresses only script-debug WebSocket signals, releases the actual resource target after POST proves a running session with no events, and requires the terminal GET's captured resource before checking the original resource/body assertions. The final browser regate passed in both authentication profiles, retaining the original resource and diagnostic-body assertions.

The final affected unit selection passed 285 tests across seven files in 25.97 seconds; exact application/Node type checks, global lint and the fresh embedded dashboard build passed. Root and independent API/core peers found zero remaining actionable production or fixture source findings. The immutable 36-image evidence set remains accepted for the two passed runtime-state cases in each auth profile. Root and an independent UI peer reviewed every image at original resolution. It records real held boundaries, last actual parameter resolutions and exact frame-source navigation in desktop Chromium, including 390px views with the sidebar collapsed through the operator control and a maximum viewport height of 1,400px. See `slice9-screenshots.json`.

Verification: all 14 affected actual browser workflows passed in each authentication profile with no skips or retries (dev-admin 31.5 seconds; local-auth displayed 2.1 minutes). The repaired HAR credential fixture passed normal-package race detection in 9.172 seconds; debugger runtime/control/lease/privacy races passed three shuffled runs in 15.664 seconds. Engine focused/full and three shuffled race runs passed. Final dashboard type checks, lint, 285 affected tests and fresh embedded build passed. The original 36 reviewed images are unchanged; two additional terminal-GET resource context images are qualified separately in `slice9-terminal-get-screenshots.json`. The initial full API run failed only the repaired synthetic HAR fixture; the complete integrated Go/dashboard gates remain required before pushing the series. Exact commands, earlier failures and final slice results are retained in `slice9-validation.json`. These are synthetic desktop/loopback capabilities; physical Chrome/iPad and live Salesforce qualification remain outstanding.

Commit: `ba45664d` — Expose authoritative paused runtime state and debugger receipts. Integration with newer main began after this commit.


<a id="aura-record-replay-2026-10-03-implementation--final-main-integration"></a>
### Final main integration

The nine reviewed slice commits are retained while merging upstream main `7cfc031c`. Its managed AppDynamics run correlation now applies across the new HTTP version pools, approved-origin redirects, Digest retries, resources and meta refreshes. Isolated OAuth requests exclude the application run marker. Concurrent preflight rejects an effective authored marker conflict before any sibling HTTP request or TCP connection. Native HTTP/1.1 and HTTP/2 aliases resolve default headers and participate in managed preparation. Actual public test creation also exposed an omitted monitoring store in the detached validation snapshot; preserving that read-only store repairs selected-monitor admission without changing the outer admission fence or durable commit.

The eight added integration tests exercise real local servers and native engines, including exact approved/foreign-origin behavior, OAuth isolation, whole-group zero-traffic failure, frozen monitor jobs, disabled-policy controls and the public asynchronous preparation path. Independent root/core/API/UI review and fix cycles finished with zero remaining actionable source findings. Generated runtime, HTTP-version and upstream AppDynamics contracts also passed independent semantic review.

The separate final Go run passed all 122 packages in 1,017.50 seconds against the completed embedded dashboard, with all 3,741 checked source, module, contract and asset files unchanged after the gate. The earlier 1,077.10-second pass remains qualified at its pre-theme-repair boundary. Focused API integration passed in 6.575 seconds and three shuffled race runs in 65.239 seconds; HTTP and engine integration race selections also passed three shuffled runs. Both required synthetic React/Aura recording and replay browser regressions passed in 7.938 seconds.

The final dashboard cycle passed application and E2E Node typechecking, lint, all 481 unit files and 5,209 tests, and the production build. Full browser profiles passed 209 dev-admin and 133 local-auth workflows with zero failures, one worker and zero retries. The five dev-admin skips are four local-only permission cases plus the optional private LoadRunner acceptance archive; local-auth skips only that optional archive. All three migrated workflows passed in both full profiles. The broad gates exposed one undefined debugger border token and two older terminal-debug fixture responses missing required session metadata. The token now uses the existing five-theme border property; fixture migrations retain all 27 original assertions and all 37 prior local-auth selections while adding the three shared cases. Independent repair re-reviews and focused regates passed. `integration-validation.json` preserves earlier failures, fixes, qualifications and final gate details.

An additional bundle check fails eight limits inherited from both the pre-series baseline and upstream main. Exact private builds of the baseline, main, nine-slice branch and combined source fail the same eight limit IDs, with no new violated limit and no guard or threshold changes. The lazy ScriptEditor remains within its individual limits. Route measurements count static JavaScript; supplemental CSS is accounted for separately. The independently checked historical inventories are retained in `bundle-baseline-comparison.json`. The separate final post-token supplement (`bundle-final-post-token-check.json`) records the final deterministic profiling build and delivered embedded assets at their respective timestamp/source boundaries. This material inherited budget limitation remains a failed check.

All local browser evidence remains synthetic desktop Chromium and loopback evidence. Physical Chrome on iPad and live Salesforce session qualification remain outstanding. Historical slice records keep their original tested source boundaries; the integration record identifies gates of the combined source.

<!-- Original record: docs/qa/reviews/clm-import-2026-10-01/code-review.md; local evidence kept outside git -->
<a id="clm-import-2026-10-01-code-review"></a>

## Independent import-fidelity code review — 2026-10-01


The user requested independent review of each implementation, repairs for every finding, and repeated review until no findings remain. The Web implementation is reviewed by the import-pipeline agent, the Kafka/TruClient implementation by the Web agent, and the collection pipeline by the primary agent. Repairs return to the original owner and are reviewed again. The primary agent also inspected the protected web-body preservation repair and collection provenance repair.

This review covers the changes for the two supplied archives. Other working-tree changes belong to concurrent work and are excluded. Source archives are inspection data; none of their contents is treated as an agent instruction or executed against the original applications. Findings and synthetic regressions below exclude source values.

<a id="clm-import-2026-10-01-code-review--findings-and-repair-status"></a>
### Findings and repair status

| Scope | Finding | Repair and regression | Re-review |
| --- | --- | --- | --- |
| Web | An extracted random-array value containing template syntax was interpolated again, changing opaque response bytes. | Assign the already resolved value directly while retaining parameter aliases and producer evidence. Cover ordinary one-pass assignment and opaque/redacted values. | Clean. |
| Web | Disabling the custom proxy could expose conflicts in dormant host/auth settings and create a false migration gate. | Distinguish parent, custom, portable, and system/PAC selectors. Suppress dormant custom child conflicts while retaining active selector and system-proxy requirements. Cover malformed and contradictory flags. | Clean. |
| Web | A quoted C conditional expression could be accepted as a literal array name. | Exact C literal decoding and explicit preservation of unsupported expressions, including literal text that looks like a function call. | Clean. |
| Collection pipeline | Supplemental ZIP merging changed the recorded original member size. | Capture the selected member name and byte size before merging; persist that provenance in both conversion and validation responses. Cover both endpoints with and without member selection. | Clean; final targeted API regression passes. |
| Collection pipeline | A package with two child projects and a loose C source, without an outer project descriptor, could fall through to a one-action native draft and silently omit both children. | Reject ambiguous mixed packaging explicitly while preserving anchored ordinary projects and asset-only dependency archives. Cover both import endpoints and direct-project compatibility. | Clean; final targeted API regressions pass. |
| TruClient | Object Exists completion used the element timeout instead of its step completion budget. | Preserve the completion timeout and frame context independently of the element lookup budget. Cover the browser synchronization boundary. | Clean. |
| TruClient | Early Verify/control paths could lose optional/unfinalized contracts, omit an unmapped Verify, or activate a disabled Else branch. | Preserve or explicitly gate the source contract before returning; suppress disabled branch children. Cover each early path and context mismatch. | Clean. |
| Unique Number | Malformed padded numeric suffixes could be accepted as source counters. | Require a fully valid source suffix and preserve unsupported values for review. | Clean. |
| TruClient | Multiple same-basename XML members could make conversion and source mapping choose different files through map iteration. | Shared deterministic source selection and an explicit ambiguity gate; synthetic duplicate-path regression. | Clean. |
| TruClient completeness | Firefox profile preferences and a CSP override were outside the selected CFG/USP accounting. | Preserve and classify browser-profile preferences and the authentication-filter selector separately; reconcile active/commented statements, duplicate keys, and all source-asset roles in value-free appendices. | Clean. |
| TruClient auxiliary provenance | New JSON/INI evidence rows used a hard-coded declaration line instead of the actual key location. | Capture the bounded source declaration location and add line-offset regressions so runtime links match the appendices. Prioritize mapping-referenced files within the existing source-preview bounds. | Clean. |
| Shared source preview | A basename alias could give an unused root file the same priority as exact mapped paths and displace a mapped file from the bounded preview. | Give exact mapped paths priority over basename fallbacks; the independent regression retains all 20 mapped files and explicitly omits the unused alias. | Clean. |

<a id="clm-import-2026-10-01-code-review--review-boundaries"></a>
### Review boundaries

The collection review inspected archive recognition, combined decompression and entry limits, exact member selection, ordinary-project compatibility, supplemental evidence ownership, partial/all-failed envelopes, deadline propagation, protected capability cleanup, and generated contract definitions. After the provenance repair, it found no further actionable issue in that scope.

The Web review also inspected source time conversion, positional producer readiness, native HTTP/browser-setting classification, proxy compatibility, protected credential-body reconciliation, and frontend response-body deadlines. The Kafka/TruClient review includes run-wide Unique Number allocation, Java/Kafka evidence, disclosure projection reuse and cache boundaries, source decoding, control flow, selectors, timing, transaction anchors, runtime settings, and the persistent review-only execution gate.

All three implementation review loops have definitive clean signoffs after repeated repair and independent re-review. No actionable findings remain in their supplied scopes. The shared sourceview suite and final converter boundary checks pass. Final API/OpenAPI TypeScript generation, the controller build, and both post-generation TypeScript configurations pass. The final targeted API regressions pass. Actual browser import/save/reopen checks pass for all three native web drafts and the named TruClient review; the named Java review also reopens. The final full Go rerun passes all 117 tested packages with no failures after a stale API golden expectation was corrected. That correction adds explicit HTTP browser-device compatibility and executable proxy assertions; its focused API/converter tests pass. An unchanged metrics timing test passed five isolated repetitions and then the full engine suite after its first complete-run failure; no metrics implementation or test edits were made.

A clean code review does not establish replay fidelity against unreachable hosts. Unsupported source behavior remains explicit review evidence or an execution gate. TruClient authentication-filter list-format markers remain conservatively unparsed behind a durable policy-review gate.

Final verification: `go test -p 2 -timeout 20m ./...` passes with stable embedded assets (117 tested packages); the API package completes in 474.790 seconds and engine in 34.186 seconds. Both TypeScript configurations, scoped lint, affected dashboard units, both auth-profile collection browser workflows, reviewed controller build, documentation checks, and `git diff --check` pass. Actual source drafts are saved/reopened in the local review workspace; the private backup integrity check passes. These are import/authoring results, not original-host replay evidence.

<a id="clm-import-2026-10-01-code-review--validation-before-publication-to-main"></a>
### Validation before publication to main

The reviewed task changes were isolated onto main commit `1e4fba788969546ba141a8cdae238aca20179df5`. Independent scope reviews found no remaining actionable findings and confirmed that concurrent diagnostics, analytics, and recorder changes were preserved. The 147-file commit includes task code, synthetic regressions, regenerated contracts, and sanitized review evidence; the original archives, private backup, and unrelated working changes are excluded.

The isolated full Go gate, `go test -p 2 -timeout 20m ./...`, passes all 117 tested packages with no failures. The full dashboard `npm run test:cycle` passes both TypeScript configurations, lint, 445 unit-test files with 4,700 tests, and the production build. Browser workflows pass in both profiles: 180 dev-admin tests with four existing conditional skips, and 100 local-auth tests with one existing conditional skip. No browser retries or failures occurred. Documentation, generated QA freshness, staged-path allowlisting, and whitespace checks pass. These checks validate the integrated import/review implementation and synthetic workflows; no original target host was contacted.

<!-- Original record: docs/qa/reviews/clm-import-2026-10-01/import-response-performance.md; local evidence kept outside git -->
<a id="clm-import-2026-10-01-import-response-performance"></a>

## CLM import response performance — 2026-10-01


The first actual collection validation took 130.662 seconds. The controller's
60-second HTTP write deadline expired, so the browser received a Bad Gateway
response. A read-only macOS process sample located repeated work in private
credential disclosure projections and JSON fallback scrubbing. No broker or
application host was contacted during this investigation.

The performance fixes retain the credential boundary:

- The response builder reuses the primary script's private projection when
  its exact normalized source equals a member of the script collection.
- Source-credential discovery reuses that private projection rather than
  projecting the same scripts again. The projection is a separate internal
  return value and is never an HTTP response field.
- Attestation fingerprinting clones its existing detached projection for
  canonicalization instead of repeating credential projection. All digest,
  persistent-review, and capability-removal checks remain in place.
- Both field-aware API review projection and generic script disclosure memoize
  repeated large text within one projection. Each memo is capped at 8 MiB;
  the API memo additionally caps entries at 512, and the generic memo at 256.
  Sensitive-field decisions precede API memo lookup, operation modes have
  separate keys, and known-credential checks still follow generic memo lookup.
- Template protection and restoration scan the text once each instead of
  copying it once per reference. Source-collision checks remain active.
- JSON fallback redaction reuses the original regular-expression capture
  offsets and skips that stage when the required quote/colon syntax is absent.
  Malformed string, scalar, and compound protections remain unchanged.

Source scripts and source-review material remain exact authoring artifacts.
These optimizations reuse private disclosure work; they do not remove source
settings or flatten the evidence needed to inspect fidelity.

<a id="clm-import-2026-10-01-import-response-performance--measurements"></a>
### Measurements

| Measurement | Result | Scope |
| --- | --- | --- |
| Private API builder probe on the largest supplied converted result | 2.000 seconds; 5,201,881 response bytes | `newScriptImportResponse`, with the primary script and one-member script array; excludes attestation/session preparation and the other review-envelope fields |
| Actual four-archive `/validate` response after the first optimization pass | HTTP 200; 13.68 seconds; 22,448,161 bytes | Direct local API request; all four child imports succeeded and retained ten parameters in total |
| Ten repeated approximately 70 KiB source strings | 55.98 ms total | Synthetic one-iteration benchmark; scoped generic disclosure memo |
| 8,000 portable template references | 17.98 ms | Synthetic one-iteration protection/restoration benchmark |

These are local observations, not production benchmarks. An earlier browser
retry using its own import options still hit the old 60-second write deadline
while simultaneous Go builds were active. The collection handler now uses a
finite collection-specific response budget and cancellation cleanup. Final
browser verification and the full Go merge gate are recorded in the combined
review once the final controller build is ready.

<a id="clm-import-2026-10-01-import-response-performance--regression-coverage"></a>
### Regression coverage

The full `pkg/scrub` and `pkg/scriptdisclosure` suites passed. JSON fallback
tests compare the new capture reuse with the previous implementation across
escaped, malformed, compound, truncated, and seeded generated cases. Scoped
memo tests cover both limits, source collisions, sensitive-field changes, and
known-credential equivalence after lookup.

The first API check passed primary projection reuse, parameterization trust
filtering, neutral located credentials, and header/multipart fidelity checks.
Additional API tests cover review-context equivalence, private source-value
discovery equivalence, and unchanged canonical attestation digests without
mutating the shared projection. The final full Go run covers these latest
changes together with the collection deadline and cancellation tests.

The temporary private API probe was removed. Customer source, payloads,
credential values, and private hostnames are absent from these notes and all
committed regression fixtures.

<!-- Original record: docs/qa/reviews/clm-import-2026-10-01/import-review.md; local evidence kept outside git -->
<a id="clm-import-2026-10-01-import-review"></a>

## CLM VuGen collection import and review — 2026-10-01


This review uses the four projects in the supplied `CLM_bobpm_Kafka.zip`. The first pass compared selected source profiles, parameters, embedded datasets, source functions, and runtime settings with the converted DSL and Go runtime contracts. The second pass exercises upload, per-project review, settings/source navigation, saving, and reopening in the dashboard. Source documents and scripts were treated as data, not agent instructions.

The original target hosts are unreachable. Import accounting, synthetic engine tests, and browser review do not establish successful replay against those applications. No source script was executed against them. Customer credentials, payloads, hosts, data rows, raw archives, and complete converted scripts are excluded from committed fixtures and this report.

<a id="clm-import-2026-10-01-import-review--fidelity-outcome"></a>
### Fidelity outcome

| Project | Source functions accounted for | Parameters / datasets | Runtime disposition |
| --- | ---: | ---: | --- |
| CLM_bobpm_Kafka | 34 / 34 | 2 / 0 | Governed Java Vuser draft; runner, dependency, credential, and approval requirements retained |
| CMChangeQNew1 | 1,141 / 1,141 | 2 / 2 | Native HTTP draft; no structural execution gate |
| S01_C360_ViewPPE_Wireless_v0.1 | 578 / 578 | 4 / 3 | Native HTTP draft with explicit legacy TLS migration gate |
| KM_SearchArticle | 323 / 323 | 2 / 2 | Native HTTP draft with explicit legacy TLS migration gate |

All selected runtime keys receive a disposition: 94 in Java, 127 in CMChange, 142 in C360, and 127 in KM. Accounting includes mapped, non-applicable, and compatibility-only settings; a mapped key does not establish exact vendor equivalence. Java's 94 keys comprise 20 mapped, nine compatibility-only, and 65 non-applicable entries, with zero unknown keys. All eight Java VM settings and all 17 explicit Kafka producer properties receive source-linked review evidence. Kafka support is 12 supported counterparts, three non-applicable consumer properties, one credential-binding decision, and one partial batching semantic. The Java source remains in its governed runner lane; automatic native Kafka migration is explicitly unavailable.

Details and source-reference limits are in [web fidelity](#clm-import-2026-10-01-web-fidelity) and [Kafka fidelity](#clm-import-2026-10-01-kafka-fidelity). The complete selected-runtime checklist is available as CSV (`runtime-settings.csv`) and JSON (`runtime-settings.json`): 490 keys, comprising 141 mapped, 330 non-applicable, and 19 compatibility-only dispositions. It includes source locations and static mapping explanations, but no source values. Its validation-probe context predates the later save repairs; the selected runtime inventory is unchanged.

The follow-on [TruClient review](#truclient-import-2026-10-01-import-review) applies the same exercise to the second attached archive. The user-requested independent review rounds and repair status are recorded in [the code review](#clm-import-2026-10-01-code-review).

<a id="clm-import-2026-10-01-import-review--changes-grounded-in-this-archive"></a>
### Changes grounded in this archive

- Recognize a transport ZIP of independently packaged `.USZ`/ZIP projects without conflating their protocol, settings, credentials, or readiness. Ordinary project ZIPs containing nested dependency archives retain their existing behavior. Exact member selection supports revalidation and supplemental evidence per project.
- Add `.USZ` to browser/native import routing and file selection. Queue expansion keeps the original outer upload and identifies its member, source size, settings, review, and save outcome.
- Preserve Unique Number parameter blocks with run-wide logical VUser allocation and overflow/exhaustion checks; import both Java parameters.
- Correct timestamp resolution, indexed random-array selection, positional variable-producer evidence, selected proxy mode, native HTTP versus browser-device accounting, and source date/time directive/literal boundaries.
- Display Kafka source properties and Go counterparts in runtime review while preserving credential masking and migration decisions.
- Continue reviewing later queue entries after a middle entry is removed. Completion counts use reviewed members rather than outer uploads.
- Keep a labelled project selector beside the review tabs, including a fallback when the selected member failed import. An operator can reach every member without finding hidden queue rows. Saved review sessions use their imported script name instead of a generic protocol title.
- Count successfully saved authoring sessions separately from library scripts. Duplicate saves count once; revalidation and stale completion cannot mark a different review as saved. Execution requirements stay visible after the durable save.
- Register executable source-parameter producers and their aliases in preflight, inspect their actual runtime inputs, and retain execution-order and optional-branch rules. The private web corpus had nine false missing-value findings (one CMChange, three KM, five C360); all nine disappear without supplying invented values. Real legacy TLS gates remain.
- Avoid repeated disclosure projections and repeated source-text scrubbing. Keep the full credential projector, bounded per-projection reuse, and equivalent malformed-JSON masking. Retain exact authoring/source capabilities separately from the masked UI projection.
- Discard unpublished protected draft and source-secret capabilities after cancellation, protected-builder failure, or failed response publication; preserve successfully published partial reviews.

<a id="clm-import-2026-10-01-import-review--browser-evidence-and-design-findings"></a>
### Browser evidence and design findings

The audit followed the actual supplied archive in an isolated local dashboard and controller. It inspected rendered screenshots from this run and exercised project selection, review tabs, Kafka migration decisions, parameter inventory, source mapping, and durable saving. Screenshots below contain project names and review metadata; protected source, target hosts, credentials, and data rows are excluded.

The initial upload reached a visible `Bad Gateway` state: source protection took about 130 seconds while the HTTP server had a 60-second write deadline. The error left the queued archive and destination available for retry. Bounded disclosure reuse and a collection-specific server budget repair that failure; a finite frontend deadline also covers response-body reads. That initial error is a captured failure state, not evidence of a completed import. Performance methodology and limits are in [the response performance review](#clm-import-2026-10-01-import-response-performance).

1. **Choose the import source — Good.** The source catalog and importer identify ZIP/USZ projects and collections. The final entry describes independent project review before upload.

   Final import source catalog (`01-source-entry-final.png`)

2. **Upload and choose a destination — Good.** The original outer archive is queued with the intended project folder; import checks do not execute it.

   Actual collection queued for review (`02-collection-upload.png`)

3. **Review independent projects — Good after repair.** All four members receive independent results. The initial capture shows only two rows in the short queue; that observation prompted the project selector now visible in subsequent captures. A synthetic failed-first member exposed and repaired the selector's missing fallback.

   Independent project reviews before the selector improvement (`03-independent-project-reviews.png`)

4. **Inspect settings and parameters — Needs review for execution.** The Kafka panel exposes supported counterparts, protected credential binding, and partial batching semantics without advertising automatic native migration. All ten source parameters and seven embedded datasets are conserved. Java runner qualification and TLS migration are explicit preparation requirements.

   Kafka migration decisions and persistent project selector (`04-kafka-migration-decisions.png`)

5. **Trace source mappings — Good, with disclosed preview limits.** CMChange shows 1,145 source mappings, zero unsupported mappings, and zero DSL actions without source origin. Its 20 captured files include seven limited or truncated previews; the visible banner explains the bounded display. Source-linked draft metadata retains mapping evidence, and the original archive stays in the private inspection area. The capture predates the readiness repair and shows a superseded CMChange blocker badge; the final saved draft has no structural gate. Mapping counts do not certify byte-for-byte runtime fidelity or complete source retention inside the saved draft.

   Source mapping coverage and preview limit explanation (`05-source-mapping.png`)

6. **Save the Java review — Good.** The combined project is durably saved as the named `CLM_bobpm_Kafka` authoring session. Runner, dependency, and approval requirements remain attached; no approval or execution action was taken.

   Confirmed named Java review save (`06-java-session-saved.png`)

7. **Find and resume the Java review — Good, with discovery friction.** Script Library → Authoring review → `CLM_bobpm_Kafka` → Resume reopens the saved draft and its authoring panels. An earlier generic-name session is retained as before-change evidence. Approval readiness on the queue card concerns draft review; the resumed editor separately shows missing validation/debug evidence.

   Named session available in the library (`07-authoring-review-library.png`)

   Saved Java project reopened in the authoring editor (`08-resumed-java-review.png`)

8. **Prepare native web credentials — Failed at the captured attempt; repaired and verified.** Actual CMChange saving exposed a protected-script projection failure. Its initial error appeared only in the hidden Credentials panel; a guarded callback now reports it at the active import surface and clears it on retry. The visible error below records the failure. Exact protected body evidence now reconciles credential binding without changing executable body bytes, and the subsequent native saves complete.

   Credential preparation error visible on the active review tab (`09-visible-save-error.png`)

9. **Save the native drafts — Good.** The actual source produces three durably saved library scripts, with per-member Saved states and named editor links. The Java authoring review remains separate from these library-script receipts. KM and C360 retain their legacy TLS migration gates; CMChange has no structural replay gate.

   Three native web drafts saved with independent receipts (`10-native-web-drafts-saved-final.png`)

10. **Find and reopen the native drafts — Good; execution needs evidence.** Filtering Script Library to Attached VuGen projects yields all three scripts. Each reopens with “Saved: up to date”; CMChange shows zero authoring problems, and the source review gate remains in KM and C360. Two, two, and four parameters and two, two, and three datasets persist respectively. No Debug action or original-host replay was taken.

   Completed native imports listed in their destination folder (`11-native-web-library.png`)

Remaining design findings concern clarity rather than unavailable import evidence: long runner explanations repeat across runtime-property rows; the library's authoring queue is collapsed by default; and a resumed authoring session uses the generic “Create Script” page title. These are opportunities to make durable review completion easier to recognise. They do not imply the original hosts were replayed or that unresolved prerequisites were satisfied.

Keyboard review exercised ArrowRight between the review tabs and End to reach Source Mapping. The labelled native project selector gives the short, scrolling queue an additional route. The original hidden error was a discoverability risk when Credentials was inactive; its active-surface alert repair was verified on the actual source. These checks do not establish screen-reader behavior, contrast compliance, or complete keyboard focus order.

<a id="clm-import-2026-10-01-import-review--verification"></a>
### Verification

The complete dashboard unit run passed 442 files and 4,669 tests. Additional focused queue/count/type-contract checks passed six tests. The dashboard build passed both TypeScript configurations and produced embedded assets; lint passed, including the later focused UI edits. Scrub and script-disclosure package suites passed their new equivalence, credential, bounded-cache, and template-collision checks.

The initial full Go run encountered an embedded-asset compile race when the dashboard build replaced old asset filenames after Go captured them. It was stopped after that known build failure; the completed packages had no assertion failures. The final full gate is synchronized with stable embedded assets and frozen API/performance changes. Its first complete run found a stale HTTP/browser-device golden expectation and an unchanged metrics timing failure. The golden now asserts compatibility-only browser emulation and executable proxy semantics explicitly; focused API/converter regressions pass. The metrics case passes five isolated repetitions without edits. The corrected final full rerun passes all 117 tested packages, including the full API and engine suites, with no failures. No metrics source or test edit was needed.

The first new collection browser test used an ambiguous unanchored locator that also matched the Remove button. The corrected workflow then exposed the missing project selector when its first member failed; that product defect was repaired and verified in both dev-admin and local-auth browser profiles. The final collection workflow, including named review-session save receipts, passes without automatic retries in dev-admin (8.1 seconds) and local-auth (38.3 seconds). The full enterprise-import component file passes 90 tests; additional duplicate-save, revalidation, stale-response, and active-surface-error cases pass. Both post-generation TypeScript configurations and the reviewed controller build pass. The final targeted protected API/collection regression gate passes. Both first failures are retained in [the first-failure log](../../qa/FLAKE_LOG.md) and [browser failure triage](BROWSER_SLICES_2026-10.md#clm-import-2026-10-01-browser-failure-triage).

<a id="clm-import-2026-10-01-import-review--evidence-limits"></a>
### Evidence limits

Synthetic fixtures use safe `example.test` inputs. The attached archives and raw converted outputs stay in a private local inspection area. Browser screenshots can establish visible result ownership, settings navigation, save confirmation, and reopening; they cannot establish full accessibility compliance or end-to-end application fidelity. Legacy TLS replacement, runner qualification, private identities, proxy/broker availability, external dependencies, timezone policy, DFE semantics, and nested automatic transactions remain explicit limits in the detailed reports.

A private, Git-ignored snapshot under `artifacts/import-reviews/2026-10-01/` retains an online SQLite backup, import storage and its key, destination metadata, and both supplied original archives. Restricted file permissions and SQLite integrity were checked. It preserves the local review results separately from bounded source previews; it does not qualify a production runner.

Final full Go gate: `go test -p 2 -timeout 20m ./...` passes all 117 tested packages. Documentation checks and final whitespace checks pass. The isolated review library remains available at the local dashboard on port 4001, backed by the reviewed controller on port 18091; existing user services were left in place.

<a id="clm-import-2026-10-01-import-review--validation-before-publication-to-main"></a>
### Validation before publication to main

The reviewed task changes were isolated onto main commit `1e4fba788969546ba141a8cdae238aca20179df5`. Independent scope reviews found no remaining actionable findings and confirmed that concurrent diagnostics, analytics, and recorder changes were preserved. The 147-file commit includes task code, synthetic regressions, regenerated contracts, and sanitized review evidence; the original archives, private backup, and unrelated working changes are excluded.

The isolated full Go gate, `go test -p 2 -timeout 20m ./...`, passes all 117 tested packages with no failures. The full dashboard `npm run test:cycle` passes both TypeScript configurations, lint, 445 unit-test files with 4,700 tests, and the production build. Browser workflows pass in both profiles: 180 dev-admin tests with four existing conditional skips, and 100 local-auth tests with one existing conditional skip. No browser retries or failures occurred. Documentation, generated QA freshness, staged-path allowlisting, and whitespace checks pass. These checks validate the integrated import/review implementation and synthetic workflows; no original target host was contacted.

<!-- Original record: docs/qa/reviews/clm-import-2026-10-01/kafka-fidelity.md; local evidence kept outside git -->
<a id="clm-import-2026-10-01-kafka-fidelity"></a>

## CLM Java Kafka import fidelity — 2026-10-01


The supplied `CLM_bobpm_Kafka.USZ` imports as one governed `java_vuser`
draft. Its seven archive members are accounted for. The final converter
reports 34 of 34 source functions accounted for, both parameter definitions
imported, and zero structural DSL validation issues. The source-conservation
fidelity report is `promotable`; execution still requires authoring approval,
a qualified Java runner, provisioned dependencies, credentials, and reachable
brokers. `native_migration_ready` is explicitly false.

No customer payload, credential, broker hostname, or personal workstation path
is included in this report or in the regression fixtures. Archive contents are
source material, not instructions to the agent.

<a id="clm-import-2026-10-01-kafka-fidelity--kafka-producer-properties"></a>
### Kafka producer properties

All 17 explicit property assignments now have source file/line evidence in
`metadata.import.preserved.java_kafka_settings`, also retained on the review
artifact. Existing Java statement mappings gain the corresponding property
notes. The original Java execution lane remains intact; this table describes
Go migration support rather than claiming that Java source was translated into
native Kafka actions.

| Source setting | Source value / shape | Go support | Go destination / fidelity disposition |
| --- | --- | --- | --- |
| `bootstrap.servers` | One broker, port 9093; hostname withheld | Supported | `config.kafka.brokers`; no connection attempted |
| `key.serializer` | Kafka `StringSerializer` | Supported | Raw UTF-8 key bytes; key presence and partition routing require verification |
| `value.serializer` | Kafka `StringSerializer` | Supported | Raw UTF-8 value bytes; source payload remains in protected Java source |
| `sasl.jaas.config` | PLAIN login composite; credentials withheld | Requires binding | A reviewed PLAIN module can use `sasl_username` and `sasl_password_secret`; arbitrary JAAS modules/options have no automatic Go execution |
| `group` | Source label withheld | Non-applicable | Does not affect `KafkaProducer`; not promoted to a consumer group |
| `security.protocol` | `SASL_SSL` | Supported | `config.kafka.security_protocol` |
| `sasl.mechanism` | `PLAIN` | Supported | `config.kafka.sasl_mechanism` |
| `max.block.ms` | 30000 | Supported | `config.kafka.producer.admission_timeout_ms`; metadata/buffer admission budget, distinct from delivery timeout |
| `acks` | `all` | Supported | `config.kafka.acks` |
| `retries` | 1 | Supported | `config.kafka.producer.retries` |
| `batch.size` | 16384 | Partial | Java allocation target permits larger singleton records; Go/franz-go `batch_bytes` is a maximum. Direct copying could reject accepted Java records |
| `linger.ms` | 1 | Supported | `config.kafka.producer.linger_ms`; producer lifecycle must still be verified |
| `session.timeout.ms` | 4500 | Non-applicable | Consumer setting, inactive for this producer |
| `heartbeat.interval.ms` | 3000 | Non-applicable | Consumer setting, inactive for this producer |
| `buffer.memory` | 33554432 | Supported | `config.kafka.producer.buffer_bytes` |
| `enable.idempotence` | `false` | Supported | `config.kafka.idempotence = disabled` |
| `ssl.enabled.protocols` | `TLSv1.2` | Supported | Both TLS minimum and maximum must be `1.2`; setting only a minimum would permit an unintended protocol |

Counts: **12 supported**, **3 non-applicable**, **1 requiring credential
binding**, **1 partial**. Unknown or dynamic properties remain review evidence;
unknown property values and JAAS composites are withheld. Duplicate assignment
rows are retained, conditional values are not asserted as effective, and the
inventory has a bounded limit that explicitly reports truncation.

The native Kafka runtime already supports the applicable typed tuning and TLS
fields. Generic Kafka property maps fail closed rather than silently ignoring
unrecognized properties.

<a id="clm-import-2026-10-01-kafka-fidelity--parameter-fidelity"></a>
### Parameter fidelity

An actual import uncovered a skipped `Type=Unique` parameter: the first
conversion retained only one of two source parameters. This is now repaired.

| Parameter source behavior | Final mapping |
| --- | --- |
| Time, updated each occurrence | `type=time`, `update=each_occurrence`, original `%Y-%m-%dT%H:%M:%SZ` pattern retained with `format_dialect=loadrunner_strftime` |
| Trailing `Z` in the time format | Literal text; no UTC timezone is inferred from a literal suffix |
| Unique number starting at 2 | `type=counter`, `selection=unique`, `start=2`, `step=1` |
| Block size per VUser 999999 | `block_size=999999`; dense run-wide VUser ordinals reserve disjoint numeric blocks across scenario groups and generator shards |
| Unique number updated each iteration | `update=each_iteration` |
| `%01d` minimum width | `pad_width=1` |
| Continue cyclic when exhausted | `on_exhaustion=wrap` within that VUser's reserved block |

The first three logical VUser block starts are 2, 1000001, and 2000000.
Transport VUser IDs do not change those assignments. Number-block arithmetic
rejects overflow; the last integer in a block is returned before wrapping.
Continue-with-last and abort are supported. Invalid source bounds, formats, or
selectors retain a structurally valid draft with a durable allocation review
gate. Parallel each-occurrence branches are rejected because isolated cursors
could repeat a number.

The DSL schema and dashboard parameter contract already include `block_size`,
`selection`, `start`, `step`, and `on_exhaustion`; the repair uses these existing
fields. LoadRunner's documented Unique Number block behavior is described in
the [Parameter Properties dialog documentation](https://admhelp.microfocus.com/vugen/en/26.1/help/WebHelp/Content/VuGen/UI/116100_ui_parameters_property_db.htm).

<a id="clm-import-2026-10-01-kafka-fidelity--selected-runtime-profile"></a>
### Selected runtime profile

The importer selects `default.cfg` and its referenced `default.usp` as one
profile. Every selected section/key has a durable
`metadata.import.preserved.runtime_setting_dispositions` entry. These counts
cover the complete profile, including the Java VM settings below:

| Selected file | Source keys | Mapped | Compatibility only | Non-applicable | Unknown |
| --- | ---: | ---: | ---: | ---: | ---: |
| `default.cfg` | 39 | 18 | 3 | 18 | 0 |
| `default.usp` | 55 | 2 | 6 | 47 | 0 |
| Total | **94** | **20** | **9** | **65** | **0** |

The nine compatibility rows are `FailTransOnErrorMsg`, `Use_Xboot`, the
classpath UI mirror, the profile action name, the main action order and run
mode, and the three Java interpreter labels. Their source values survive;
their source semantics are not advertised as verified native execution. Four
of these known rows lack a dedicated support concept and appear in the
`runtime_unmapped` rollup; this does not indicate missing source keys.

The 65 non-applicable rows comprise 42 source-tool metadata rows, 13 values
for unused modes, five superseded settings, four behaviors managed by APEX,
and one disabled-feature row. The concept-level parity report separately has
19 entries: 13 `executed`, five `compatibility_only`, and one
`non_applicable`. Its `executed` label describes supported configuration
consumption, not an observed run against a broker.

The effective run logic is **one iteration**, ASAP pacing, automatic
transactions per action, and sequential lifecycle metadata. Init, action,
and end remain owned by the combined Java runner. Dormant 60/90-second pace
values are preserved and classified as unused-mode values; they do not add a
delay to an ASAP profile. Runtime-setting metadata and both parameter
definitions therefore remain inspectable even though replay is gated.

<a id="clm-import-2026-10-01-kafka-fidelity--java-vm-and-dependencies"></a>
### Java VM and dependencies

| Source Java VM setting | Accounting / support |
| --- | --- |
| `enable_CL_per_vuser=1` | `config.java_vuser.dedicated_class_loader=true`; requires the qualified persistent per-VUser runner |
| `Use_Xboot=0` | Preserved disabled; no boot-classpath behavior requested |
| `use_jdk=search_jdk` | Normalized to governed runner `jdk_selection=internal` |
| Empty `jdk_home` | Preserved empty; no workstation JDK path is required |
| Empty `Java_VM_Params` | Accounted for; no extra JVM arguments requested |
| `SkipAutomaticParameterEvaluation=0` | `skip_parameter_evaluation=false`; parameter snapshots remain enabled |
| `Java_Env_ClassPath` | Source ordering and all 12 entries preserved; 11 distinct JAR references, with one repeated entry |
| `Java_Env_ClassPath_UI` | Preserved source/UI mirror of the same classpath; no independent runtime behavior added |

None of the referenced JARs are included among the seven archive members.
The source classpath consists of workstation-specific paths and cannot be
assumed to exist on a Linux/Go generator or Java execution image. The Kafka
client, compatibility, and logging dependencies must be provisioned and
qualified on the governed runner before replay. No dependencies are installed
or downloaded by this import.

<a id="clm-import-2026-10-01-kafka-fidelity--remaining-execution-and-native-migration-limits"></a>
### Remaining execution and native migration limits

The source constructs and closes its producer within the action, sends
asynchronously, then closes the producer. Native Go Kafka uses a different
producer lifecycle and delivery boundary. Producer ownership, close-time error
handling, Java partition routing, batching allocation, and implicit Kafka
client defaults therefore require source-specific conformance work before an
automatic native migration can be claimed.

The default Go build has the mock Kafka adapter; live Kafka requires the
`kafka_real` build. A mock run would not verify the supplied credentials,
broker, TLS trust, topic permissions, or native fidelity. The user identified
the target hosts as unreachable; no success beyond import is claimed.

<a id="clm-import-2026-10-01-kafka-fidelity--validation-evidence"></a>
### Validation evidence

- New Kafka inventory tests verify all 17 source rows, classifications, source
  links, withholding of JAAS/unknown values, duplicate assignments,
  conditional/dynamic evidence, bounded review output, and the unchanged
  singleton Java runner lane.
- New importer tests verify Unique settings, both source parameters conserved,
  padding/update/exhaustion policies, and durable gates for invalid definitions.
- New Go engine tests verify disjoint blocks across scenario groups, arbitrary
  transport IDs, per-block wrap/last/empty/error behavior, once-only caching,
  inherited allocation state, integer boundaries, invalid allocation, and
  parallel cursor restrictions.
- New DSL tests verify valid Unique counters and reject incomplete or
  incompatible block definitions.
- Targeted tests passed for `pkg/convert/enterprisearchive`, `pkg/engine`, and
  `pkg/dsl`. The final private archive conversion reports **2/2 parameters**,
  **34/34 functions**, **17 Kafka properties**, and **0 validation issues**.
- An earlier full affected-package run under the tool sandbox hit
  `httptest` listener permission errors; broad suite outcomes are recorded in
  the combined import review after the approved rerun.

<!-- Original record: docs/qa/reviews/clm-import-2026-10-01/web-fidelity.md; local evidence kept outside git -->
<a id="clm-import-2026-10-01-web-fidelity"></a>

## Web archive import fidelity — 2026-10-01


This pass inspected the three Web HTTP/HTML `.USZ` children of the supplied collection. Source artifacts were treated as data. Private hosts, credentials, dataset values, request bodies, cookies, and full converted scripts are excluded from this report and repository fixtures. Actual converter outputs remain in the private temporary inspection directory. Regression fixtures use synthetic `example.test` sources.

The selected project lifecycle, runtime profiles, parameter profiles, embedded data files, generated DSL, and review findings were compared. Import accounting and synthetic runtime tests provide evidence about conversion and supported Go behavior. They do not certify application replay equivalence: the supplied application hosts are unreachable, and no successful application transaction was observed.

<a id="clm-import-2026-10-01-web-fidelity--selected-source-profiles-and-conservation"></a>
### Selected source profiles and conservation

All three `.usr` projects select `default.cfg` and `default.usp`, with the declared C lifecycle `vuser_init.c`, `Action.c`, and `vuser_end.c`. The USP has a flat, sequential run root containing `Action`. Init/end scaffolding runs once. The error-handler root names `vuser_errorhandler`, but the project carries no corresponding action file; it is reported as inert scaffolding. Unselected backup sources and SDK declarations do not inflate the selected function counts.

| Archive | Parsed selected functions | Native classifications / review only | DSL actions | Source HTTP requests | Requests compacted into sequences | Parameters / datasets |
|---|---:|---:|---:|---:|---:|---:|
| CMChangeQNew1 | 1,141 | 1,141 / 0 | 678 | 218 | 0 | 2 / 2 |
| S01_C360_ViewPPE_Wireless_v0.1 | 578 | 577 / 1 | 485 | 152 | 1 | 4 / 3 |
| KM_SearchArticle | 323 | 322 / 1 | 131 | 77 | 38 | 2 / 2 |

Request compaction retains the requests inside an ordered `http_sequence`; it is not discarded traffic. All three reports contain zero detected dropped request bodies and zero masked-body secret gates. These are structural checks, not a byte-for-byte proof for every request or a claim that all source runtime behaviors execute.

<a id="clm-import-2026-10-01-web-fidelity--runtime-settings"></a>
### Runtime settings

Each key in the selected CFG and USP has an explicit disposition. Counts include run-logic tree bookkeeping, stale authoring fields, and inactive child settings. A `mapped` key may select a portable replacement with a partial parity note; the label alone does not imply exact vendor equivalence.

| Archive | Mapped keys | Non-applicable keys | Compatibility-only keys | Total selected keys | Effective iterations | Proxy |
|---|---:|---:|---:|---:|---:|---|
| CMChangeQNew1 | 39 | 84 | 4 | 127 | 1 | Custom proxy |
| S01_C360_ViewPPE_Wireless_v0.1 | 43 | 97 | 2 | 142 | 3 | Generator environment/system proxy, partial portability |
| KM_SearchArticle | 39 | 84 | 4 | 127 | 3 | Generator environment/system proxy, partial portability |

The `default.cfg` iteration values are superseded by the selected USP. C360 and KM preserve three iterations even though their CFG says one. Duration-driven scenarios can supersede a script iteration cap; a standalone or explicitly iteration-driven run uses the imported cap.

| Setting category | Observed source policy | Destination and evidence | Limits |
|---|---|---|---|
| Encoding | ANSI, recorder code page 1252 | Strict Windows-1252 source decoding | Request bodies and binary assets retain separate byte semantics; no additional body transcoding is inferred. |
| HTTP transport | HTTP/1.1, keep-alive on, `MaxConnections=0` | Native Go HTTP settings, with zero represented by destination defaults | This is not a benchmark of source and Go connection scheduling. |
| Proxy | CMChange custom; C360/KM browser-default | Custom HTTP proxy or generator `HTTP_PROXY`/`HTTPS_PROXY` policy | Windows browser configuration cannot be inherited by Kubernetes generators. Export/configure its portable equivalent. Credentials stay governed by the existing credential projection. |
| Browser identity | Explicit custom User-Agent; saved IE4 browser identity | Custom User-Agent and imported Accept-Language/Accept-Encoding headers | Saved browser type is dormant. Go HTTP replay does not create a browser DOM. |
| Accepted encodings | gzip/deflate; C360/KM also advertise br | Go HTTP transport supports gzip, deflate, and Brotli response decoding | Application-specific encoded bodies and DFE transforms are a separate concern. |
| Legacy HTML checks | `EnableChecks=0` | Imported legacy `web_find`/`web_image_check` checks disabled | Registered searches and authored assertions remain independent. |
| Embedded resources | `SearchForImages=1` | Bounded HTTP HTML/resource discovery | No browser JavaScript execution, DOM layout, or rendered screenshot equivalence is implied. |
| Iterations/run order | Flat sequential `Action`; 1 or 3 iterations | Verified native init/action/teardown ordering | Nested/custom block scheduling is not claimed. |
| Pacing | C360 explicit ASAP; CMChange/KM USP omit pace type | C360 maps exactly; CMChange/KM use no pacing and retain a partial finding | The importer does not silently splice stale CFG pacing into the selected USP. Missing source selectors require review. |
| Think time | `Options=NOTHINK`; caps inactive | Recorded delays ignored | Stored factor, limit, and C360 random percentages are dormant under this source mode. |
| Automatic transactions | Enabled in CMChange/KM; disabled in C360 | Native per-action wrapping where enabled | `automatic_nested_transactions=1` in CMChange/KM remains compatibility-only; exact source nested automatic sub-transactions are not generated. C360 child toggles are dormant. |
| Logging | Extended log; error-only auto log off; per-class toggles vary | Native supported parameter/data/trace controls | Vendor file routing, environment banners, timestamps, and logger presentation are non-applicable; retention/redaction uses APEX bounds and policy. |
| Error policy | C360 explicit continue-on-error off, fail-on-error-message off, snapshot-on-error off | Native error handling preserves explicit false settings | No successful application failures were replayed against the original hosts. |
| Snapshot resources | Saved preference on, no replay snapshot selector | Preserved partial diagnostic finding | It does not enable capture or reproduce LoadRunner snapshot/rendered resources. |
| Data Format Extensions | Enabled in CMChange/KM; disabled in C360 | Enabled source toggle retained as compatibility-only; disabled toggle is dormant | No source DFE chain definition is present. APEX-native transforms cannot safely substitute an unknown source chain. |
| Other saved/runtime controls | `AnalogMode=0` | Preserved compatibility-only | Its vendor semantics remain unverified; zero is not treated as proof of inactivity. |
| Recorded screen/context | Screen dimensions, recording timestamp, recorder version, ResetContext | Explicitly non-applicable to native HTTP | These fields no longer inflate the report as executed browser viewport/device behavior. |

<a id="clm-import-2026-10-01-web-fidelity--parameter-and-data-policies"></a>
### Parameter and data policies

Every selected `.prm` parameter is represented. All referenced `.dat` files are embedded in the converted data sources; this report records only row counts. File delimiters, header interpretation, selected column names, starting rows, update modes, selection modes, and exhaustion policies are retained.

| Archive | Parameter | Policy | Dataset rows |
|---|---|---|---:|
| CMChangeQNew1 | p_CaseNum | Unique rows, each iteration, start row 1, automatic allocation, error on exhaustion | 17 |
| CMChangeQNew1 | p_User | Unique rows, once per VUser, start row 5, automatic allocation, retain last on exhaustion | 226 |
| C360 | p_BAN | Random rows, each iteration, start row 1, retain last on exhaustion | 199 |
| C360 | p_Letter | Random rows, each iteration, start row 1, retain last on exhaustion | 26 |
| C360 | p_UserName | Unique rows, once per VUser, start row 1, automatic allocation, retain last on exhaustion | 220 |
| C360 | p_DateTime | Time, each occurrence; source pattern `%Y-%m-%dT%H:%M:%S.000Z`; zero offset and working-days filtering disabled | — |
| KM | p_Article | Sequential rows, each iteration, start row 1, retain last on exhaustion | 2 |
| KM | p_UserName | Unique rows, each iteration, start row 1, automatic allocation, retain last on exhaustion | 501 |

The file-row allocator uses runtime VUser identity and the compiled allocation plan; the source labels do not themselves prove uniqueness across an arbitrary external run topology. Retain-last explicitly permits reused values after exhaustion. Test volume must respect the available rows when strict uniqueness is required. Nonzero business-day offsets/calendar behavior were not exercised; those source options are disabled in this collection.

C360's date pattern now uses `format_dialect=loadrunner_strftime`. Source `.000` is literal text rather than Go's fractional-second token. A literal trailing `Z` is preserved as text and no longer silently chooses UTC. The source contains no timezone setting, so the native runtime uses its local clock unless the operator explicitly sets a timezone. Container/workstation timezone differences require review. The supported formatter rejects unknown and locale-dependent compound directives instead of guessing.

<a id="clm-import-2026-10-01-web-fidelity--concrete-fixes-from-this-pass"></a>
### Concrete fixes from this pass

- Separate `ProxyUseProxy` (proxy enable) from `ProxyUseProxyServer` (custom-mode selection). The valid C360/KM combination `ProxyUseProxy=1`, `ProxyUseBrowser=1`, `ProxyUseProxyServer=0` now retains system mode without a false synonym conflict. Malformed and contradictory selectors still fail closed.
- Publish executable producer/read metadata for positional parameter assignments. C360 `lr_save_timestamp` and KM `lr_save_string` no longer create false missing-producer blockers. An absent input array still creates a real dependency finding.
- Make API variable readiness recognize native source-parameter outputs and their exact/lowercase aliases. Analyze the scalar values actually evaluated by the handler instead of preserved source-call evidence. Missing inputs, late assignments, optional branch outputs, and freed variables remain blocked.
- Execute the supplied `lr_save_string(lr_paramarr_random(...), ...)` against indexed array values and their count. It no longer substitutes an undefined base variable. Empty/malformed arrays produce no selected value; vendor NULL/error behavior has not been certified as identical.
- Correct `lr_save_timestamp` default to epoch milliseconds and honor supported `DIGITS` resolutions. Synthetic tests cover the default, seconds, microseconds, lower-width truncation, malformed options, and documented fallback.
- Preserve LoadRunner date/time directive/literal boundaries and explicit timezone policy in the native parameter engine.
- Scope browser-device parity to the browser engine; native HTTP screen recording fields are non-applicable. Custom User-Agent selection now makes saved browser identity and disabled recorded-agent inheritance explicitly dormant in per-key review.

The timestamp and array contracts were checked against [OpenText lr_save_timestamp](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/c_vuser/lrFr_lr_save_timestamp.htm), [OpenText lr_paramarr_random](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/c_vuser/lrFr_lr_paramarr_random.htm), and the [parameter properties reference](https://admhelp.microfocus.com/vugen/en/26.1/help/WebHelp/Content/VuGen/UI/116100_ui_parameters_property_db.htm). Proxy portability follows the [runtime settings reference](https://admhelp.microfocus.com/vugen/en/26.1/help/WebHelp/Content/VuGen/c_rts_general_page.htm).

<a id="clm-import-2026-10-01-web-fidelity--remaining-source-execution-gates-and-validation"></a>
### Remaining source execution gates and validation

CMChange has no structural execution gate after import. C360 and KM each retain the `legacy_tls_replacement` gate at `init[1]` for source `SSL_VERSION=2&3`. Those legacy SSL selectors must be replaced by an explicitly chosen supported TLS policy; the importer does not mislabel them TLS 1.2 or remove the gate automatically. Original hosts, proxy availability, external identities, source credentials, and data appropriateness still require environment preparation even for CMChange.

The actual collection API review exposed an additional disagreement between converter dependency findings and shared API preflight. The converted producers were already present; the API readiness analysis only registered extractors, browser evaluation, and OAuth outputs. It also scanned the diagnostic base-array argument retained for KM's random selection. The shared check now follows the native parameter handler's reads/writes and evaluates them in the existing control-flow analysis.

| Actual imported web script | Original API missing-value count | Corrected missing-value count | Remaining source execution gate |
|---|---:|---:|---|
| CMChangeQNew1 | 1 | 0 | None |
| KM_SearchArticle | 3 | 0 | Legacy SSL selector at `init[1]` |
| S01_C360_ViewPPE_Wireless_v0.1 | 5 | 0 | Legacy SSL selector at `init[1]` |

These results were measured against the decoded scripts in the private actual API response, without replay seeds or invented parameter values. CMChange's correlation extractor at `actions[56]` precedes its URL conversion at `actions[58]`, which publishes the previously reported missing value. KM's bootstrap sequence produces its correlation before `init[7]` encodes it; the save-all article extractor at `actions[23]` supplies the indexed array/count read by random selection at `actions[25]`. The bare save-all name is not a live read for that selection and is still correctly reported missing if a normal request directly references it. C360's bootstrap extractors precede the three encoded correlation assignments; `actions[0]` publishes the timestamp and `actions[44]` encodes its declared date/time parameter.

The nine reported missing names were false preflight blocks, not lost source producers or values the operator must manually supply. The corrected readiness result does not prove that remote responses will match the imported extractor expressions or contain a nonempty array. Those remain application-dependent runtime conditions. The Kafka child also retained zero missing variables in this shared check; its governed Java/runtime constraints are documented separately.

The focused DSL, converter, and Go engine tests passed, including source parameter dependencies, missing-array rejection evidence, random indexed selection, timestamp resolution, source date/time literals/local clock/explicit UTC, proxy selectors, selected profile behavior, and runtime parity. `go generate ./pkg/dsl` completed. An initial broad converter/engine run encountered sandbox-denied loopback listeners; it also exposed one stale golden parity count, which was corrected and passed in the focused rerun. The root task owns the final broad Go suite and browser workflow validation; this document does not claim those gates passed before their result is available.

<a id="clm-import-2026-10-01-web-fidelity--actual-credential-preparation-save-correction"></a>
### Actual credential-preparation save correction

After variable readiness was corrected, the real CMChange review reached credential preparation and failed with `failed to project resolved script`. The failure came from a retained JSON credential locator at `actions[258]`: a default whole-body secret binding changed a structured JSON document into a template while leaving its original JSON field locators. It was not a request-body decoder failure or an exceeded script disclosure byte/work limit. The review now also shows credential preparation errors on the main review screen, so a save failure is visible when the Credentials pane is closed.

The four CMChange default binding choices did not represent four missing source payloads:

| CMChange body surface | Actual source evidence | Correct import treatment |
|---|---|---|
| `actions[41].body` | One original credential field is empty | Keep the original empty value and exact body; optional migration has no source value |
| `actions[258].body` | All 23 retained JSON credential fields are available | Keep the original structured body; its aggregate scalar encoding refusal does not mean its credential leaves are unavailable |
| `actions[453].body` | Its retained form credential value is available | Keep the original encoded form and its structured component |
| `actions[590].body` | Its retained form credential value is available | Keep the original encoded form and its structured component |

The scalar source-value classifier has a bounded encoded-candidate guard. Applying that guard to an entire serialized JSON document or a percent-encoded structured form component produced false `unsafe_source_encoding` availability choices here. The corrected destination-preservation check requires the import's preservation marker, bounded JSON/form decoding, available or source-empty leaves, unchanged source bytes, and shared byte/value/depth budgets. Duplicate JSON members are inspected in order, so a later duplicate cannot hide an unavailable earlier leaf. This proof does not make the entire opaque document available for optional secret migration. Genuine disclosure placeholders, ambiguous alignment, malformed bodies, unsupported opaque bodies, and individual credential leaves that exceed the existing guard retain their existing restrictions.

Explicit full-body secret binding now retains the governed authoring token and changes only the affected body's runtime locators to an opaque body locator. Other header/auth/URL locators, named `body` headers, foreign request keys, sibling actions, and HTTP-sequence children retain their original attributes. Malformed ordinary JSON still fails the existing locator validation.

| Actual private API candidate | Original default unpreserved occurrences | Corrected default unpreserved occurrences | Corrected source-preserving preparation and commit sanitizer |
|---|---:|---:|---|
| CMChangeQNew1 | 4 | 0 | Both succeeded; zero action body changes |
| KM_SearchArticle | 1 | 0 | Both succeeded; zero action body changes |
| S01_C360_ViewPPE_Wireless_v0.1 | 1 | 0 | Both succeeded; zero action body changes |

These are actual private API candidate checks through the credential-preserving resolver and the import commit sanitizer, with no typed credentials or fabricated full-body bindings. KM and C360's original empty bootstrap fields can remain empty during import. Whether the remote application later requires another value is an environment/runtime question. The focused synthetic guard, ownership, empty-source, explicit whole-body reference, and byte-fidelity regressions passed, alongside the actual private API check. The temporary private diagnostic test was removed. Durable receipt/reopen verification and the final broad suites belong to the root task; this section does not claim remote replay or persistence merely from successful preparation.

<a id="clm-import-2026-10-01-web-fidelity--independent-code-review-corrections"></a>
### Independent code-review corrections

The requested independent review found and corrected three additional boundaries. A selected array entry is now assigned as an opaque runtime value, so response bytes containing `{{var.other}}` are not expanded a second time; exact/lowercase aliases and configured run-evidence redaction remain intact. Random-array names require a complete C string-literal proof, including safe adjacent literals. Conditional expressions and trailing calls become source review actions with execution gates, while an authored string containing function-looking text retains its literal bytes in both the runtime argument and dependency evidence.

Proxy conflict activation now distinguishes the parent enable flag from custom, browser, and PAC selectors. A disabled custom choice leaves its saved static host/port/auth fields dormant, including when the parent is active. Parent/browser/PAC selector conflicts and the Windows-system-proxy migration requirement remain visible. Synthetic tests cover disabled/unset parent combinations, active portable modes, malformed and contradictory selectors, and conflicting dormant hosts.

The independent reviewer rechecked the production corrections. Focused native assignment/evidence tests and the full existing proxy test family passed; the exact literal-expression regressions also passed. The final full Go gate passes all 117 tested packages. Actual native save/reopen checks and the collection browser workflow in both auth profiles pass; the parent import review retains screenshots and evidence limits.

The final actual import saves all three native web drafts to the isolated local Script Library. CMChange, KM, and C360 each reopen with Saved: up to date. Two, two, and four parameter definitions and two, two, and three inline data assets remain present respectively. CMChange has no authoring errors; the KM/C360 source TLS migration gates remain. Protected body reconciliation preserves exact original executable bytes rather than substituting values or accepting a masked body as proof. No original target was replayed. Screenshots and the full gate result are tracked in the parent import review.

<!-- Original record: docs/qa/reviews/feature-completion-authoring-fidelity-2026-10-05/README.md; local evidence kept outside git -->
<a id="feature-completion-authoring-fidelity-2026-10-05"></a>

## Authoring fidelity repair — 2026-10-05


Current composed source/test qualification: the default full Go suite, full dashboard cycle in both authentication profiles, affected API races, actual PostgreSQL cohort, SDK contract and generated-contract checks passed in the [composed review](INTEGRATION_2026-10.md#feature-completion-composed-review-2026-10-05). That qualification is scoped; the broader feature requirements and explicitly unrun acceptance remain open. The following component checkpoint predates these composed results. Its original bytes are preserved in the historical checkpoint (`README.component-checkpoint.txt`).

Status: frozen v11 fixture correction with clean scoped authoring reviews. Actual SQLite/PostgreSQL authoring normal/race lifecycle suites and twice-repeated browsers in both auth profiles pass on their captured inputs. Exact successful JSON snapshots and settled dialog evidence remain retained. Both full Go failures remain preserved. The eight API failures from v9 now pass three focused normal repeats; the fixture ownership correction and ordering checks pass twice under the race detector. The further native finality correction is separately reviewed and qualified in parent integration. One final composed full Go run and full dashboard cycle in both auth profiles, generation/documentation checks and final review remain pending. The program is not fully qualified, and no whole AUTH-1 completion is claimed. [Guide](../../guides/AUTHORING_SCENARIO_FIDELITY.md).

The implementation restores Results/WebSocket sections in the real payload and script-inheritance paths, durable named-shape storage, complete owned library snapshots and group SLA. The actual run group serializer retains start offsets and SLA during group overrides. It adds the reachable, permission-gated queued-copy action to Command Center, with fresh canonical identity/incarnation checks, owned snapshots, request-generation modal keys and late callback retirement. Advanced Details no longer mounts the orphan editor and retains its inspection-only boundary. Native SLA form constraints match the existing positive float64 API contract rather than imposing an unadvertised step or one-millisecond floor.

<a id="feature-completion-authoring-fidelity-2026-10-05--scope-and-semantics"></a>
### Scope and semantics

Section absence, present empty replacement, explicit false/zero and run-only clears retain their existing execution semantics. The actual workload configuration survives queued copies. Named-shape run metadata is not introduced. Opaque scenario revisions/CAS and broader shared-owner/replay-retirement work remain open.

Library export and local hashing share one portable projection. Only historically omitted default duration mode and empty shape normalize away. Nondefault mode and SLA stay hash-visible; older links whose nondefault fields were formerly omitted may now report a real conflict. Complete import snapshot shape clears occur after hash admission, while ordinary PUT absence still preserves current shape. Rollback/recovery retains explicit clears and separately owned children.

<a id="feature-completion-authoring-fidelity-2026-10-05--retained-review-findings-and-failed-attempts"></a>
### Retained review findings and failed attempts

Immutable sibling receipts retain command, UTC times, durations, raw logs, environment and source hashes. Earlier native and script-authoring QA artifacts are preserved verbatim.

- Before-fix pure/real fixture tests proved missing Results sections, WebSocket payload/draft fields and snapshot SLA/shape/mode. Fixture-only HA/duration/stage mismatches remain failed evidence; admission rules were preserved.
- Actual portable library update proved that omitted empty shape wrongly preserved a named destination. The corrected snapshot adapter retains hash validation and default portable/recovery compatibility.
- Inherited WebSocket zero incorrectly displayed the `500` fallback; the placeholder uses nullish absence.
- Queued-copy owner tests proved A→B→A and permission-return stale admission, late close of a newer owner, and child success-after-close retirement. Token-fenced ownership and completion-before-close correct those paths.
- Native form validation proved retained 1250/1500ms SLA values could not submit because `min=0.001, step=0.1` seconds defined a mismatched grid. Controls now preserve accepted fractional values, including 0.001ms, without bypassing application positivity validation.
- StrictMode replay proved the initial hook cleanup could prevent any fresh read. Mounted setup/cleanup now retires requests without destroying current scope. Actual unmount remains fenced.
- Initial expanded type/lint failures were new fixture completeness, an anonymous mocked component hook name and the removed orphan's unused destructured callback; each was corrected and receipts remain historical.
- The first composed browser attempt passed 21 cases, skipped the existing dev-admin restricted-persona case and failed three cases. The actual UI's manual hold creates a RESERVED run; the corrected journey separately proves held-run serialization, then seeds a real QUEUED source from that exact submitted intent for the reachable copy flow. Two fixtures now carry the actual workspace application/release rather than relying on legacy defaults. No status response, scope guard or admission is bypassed.
- Actual rename-only browser evidence proved an absent `goal_config.early_stop` was invented. Red unit regressions also proved both guided shape patchers added unrelated defaults. The blank draft factory now owns defaults; extraction and shape editing clone authored presence, and an explicit new-shape seed carries only already-owned settings. All 107 adjacent tests pass, including saved-scenario edit/clone, empty nested sections, false/zero and source no-alias cases.
- The second actual dev-admin attempt passed 23 cases, skipped the same existing persona case and found only a fixture expectation for explicit outbound `role: load`. The established compact serializer omits that default and the real canonical copy response retained `load` on both groups. Only missing/nullish outbound roles normalize for the request assertion; exact reopened roles and every other group field remain compared. Raw submitted/created evidence attaches before assertions, and load/observer draft-to-serializer regressions pass. No production or embed changed.
- A test oracle compared legitimate mutable scenario read context. It now excludes exactly `associated_releases`, `last_run_test_id` and `last_run_at`; every other authored and read field remains compared. Exact raw held/seed/source snapshots attach before assertions.
- An initial composed PostgreSQL attempt overlapped dashboard asset replacement while the compiler read `go:embed` files. No test or schema executed. The scheduling failure remains retained; the subsequent frozen-embed normal and race gates used the real fixture and verified removal of only their owned schemas.

<a id="feature-completion-authoring-fidelity-2026-10-05--resumed-review-findings-and-qualification"></a>
### Resumed review findings and qualification

The third dev-admin attempt retained three further failures: synchronous parent navigation still encountered the creating form's own dirty guard; the actual planned Command Center had no configured-group settings action; and one shape-edit fixture queried a control before opening its real Run Settings tab. The guard now releases only its own registration for the exact submitted draft. Later edits retain the form, the successful created-run receipt and their guard; unmounted completions cannot release a replacement form. Planned group settings require a canonical unstarted QUEUED/RESERVED owner in the current project and do not manufacture live load measurements. The fixture opens the actual settings tab.

Subsequent red regressions exposed source capture IDs omitted from queued-copy drafts, source goal defaults invented while cloning, guardrails omitted in VUser mode, and an all-clear goal becoming absent and inheriting the source. Private capture IDs and goal-presence intent now survive exact drafts; unavailable connections require explicit review or Off. The VUser serializer retains supported authored guardrails without creating a goal controller. An explicit empty goal is sent only when clearing a source that originally had a goal configuration. Actual SQLite/PostgreSQL and browser fixtures now exercise these paths; their expanded execution remains pending.

The interrupted seven-file unit repeat retained two five-second timeouts and a worker-start timeout. Its raw evidence remains `55b155bc904cd69d9fd5b0362239ad51b06fd200d0d60c31054cc69f518793ed`. Typecheck then found an optional generic-clone return in required draft fields and an incomplete runtime-receipt fixture; lint found a fixture's optional-chain non-null assertion. Exact structured clones and complete typed fixtures correct those findings. The current sequential unit repeat passed all 232 tests in 19.720s, raw `a31c3a35cb04284a32fff7736cbb2fbe383f8b068ef458c52bca4b56007f4eba`, without changing its frozen source. Current source inventory (`evidence/fidelity-copy-closure-resume-source-inventory.json`) and independent review (`evidence/fidelity-copy-closure-parent-independent-review.json`) record the reviewed scope. The current typecheck/lint repeats also passed in 361.784/203.541s, raw `a691c3ed7f868f4224b319aa9ea49b8db48d41717efab023a87b3a64b7133ee9` / `bf7ea878e62d715055d29dd6955852b7c24f575ba3d1aa177c99df05cc0a6eb6`, without changing reviewed source. Passing these repeats does not erase the retained failures or qualify the pending browser/full gates.

<a id="feature-completion-authoring-fidelity-2026-10-05--full-source-repeat-and-actual-lifecycle-findings"></a>
### Full-source repeat and actual lifecycle findings

The independent full-source pass found four P2 issues: direct creation and navigation-guard Save could submit twice, iteration copies invented a duration and lost completion mode, clearing the original global delay inherited it again, and clearing the final root runtime inherited it again. The instance-owned submission promise joins the complete preflight/creation/receipt operation. Iteration copies retain read-only completion mode and exact empty duration. Private source-presence intent distinguishes explicit clears from ordinary absence. Supplied group runtime and assignment clears use the existing pristine-source rebuild semantics without adding speculative group markers.

The five red frontend assertions remain in raw `42a8712e6eed8b01c8b2ed04d6511c1ddbea77e6481587a745841c4c06238333`; the corrected 86-test subset passed, followed by all 249 affected tests in 104.556s, raw `ba650b641badad651f71880ddb285617fc0caac9329abf9b88f126b46ec9345b`. Current typecheck/lint passed in 43.143/15.811s. The 48-input source inventory (`evidence/fidelity-four-findings-fixed-source-inventory-v2.json`), focused parent repeat (`evidence/fidelity-four-findings-parent-repeat-review.json`) and full independent repeat (`evidence/fidelity-four-findings-independent-repeat-review.json`) have no additional findings within that exact scope. The subsequent embedded build passed in 34.730s, raw `474e604d3e7dd387f8dded6e2edf058630c02b7b61d22fb2a4625beac9c1b747`; later guide changes require a fresh build.

Actual SQLite/PostgreSQL qualification first refused to compile because a new fixture used `LiveMetrics.VUsers` rather than the real `ActiveVUsers` field. No fixture/schema ran in that attempt, raw `ed99412630f3daf5c4909c59a03fcc91203f8371c7e4532cc7bebdaf61a1407d`. The corrected repeat found deterministic SQLite build-fence staleness and a stopped session-owned PostgreSQL container, raw `8d73db9f5cd7adf9bb418ac2c18e6ba5abc486ac43b9f275139cde7ea1f123ab`. The existing container was restarted, preserving data, and `pg_isready` confirmed readiness; that alone is not PostgreSQL acceptance.

A diagnostic located false staleness when creating retained VUser guardrails after property-analytics peers. Lightweight capacity clones discarded executable DSL while their admission fingerprint still recomputed script-dependent capabilities. Red regressions proved seven omitted feature families, an unchanged-peer false refusal and a second sequential queued-create refusal, raw `b44754a93148bc6af21a50e144ad78167f29b99c1cbf17bda7886a0daa92c218`. The correction preserves the complete private feature string only on lightweight snapshots, with an explicit known marker; a present executable script always recomputes its features. Thirteen feature cases and frozen/live-change/attachment/sequential-clear checks pass. Independent cache review (`evidence/fidelity-private-feature-cache-independent-review.json`) records its precise scope; it does not qualify later delay/fence changes.

The lifecycle then reached the previously assumed `0s` clear and proved the backend rejected it, raw `b55b254f1ff29aa0cd7ed1fd7477bab2c838ddfa2481b08804e6b4afa71eff8f`. The correction supplies only the actual admitted queued source to a private validator; a client source ID alone cannot enable a clear, and ordinary/scenario checks remain strict. Canonical source identity/project, unstarted/unended state and matching supported duration or iteration completion mode gate the exception. Existing positive iteration and saved-scenario delays remain supported; ordinary and scenario explicit zero requests remain refused even with forged revision fields. Missing/foreign/started-source and controlled final-reference-fence cases cover admission. Concrete review additionally found that the old fingerprint omitted EndedAt despite the new unended requirement. Controlled red tests proved both EndedAt-only and completion-mode-only changes could commit a copy. The fingerprint now includes EndedAt and canonical completion mode, and the lightweight clone retains the mode; exact review and repeat acceptance remain pending.

A later SQLite reopen assertion incorrectly compared raw duration spelling and historical default-mode spelling (`1m`/empty versus `1m0s`/duration). The fixture now compares numeric duration and canonical mode, while retaining exact empty iteration duration, delay, runtime presence/value and group intent. Raw `f77c3fd1b1970ee4b38e83812792be6d961a16de229005f09d1bd0c09bf62973` remains a failed attempt. No backend storage normalization or production guard was weakened for that oracle correction.

<a id="feature-completion-authoring-fidelity-2026-10-05--verified-affected-gates"></a>
### Verified affected gates

The immutable receipts are in evidence (`docs/qa/reviews/feature-completion-authoring-fidelity-2026-10-05/evidence`). Earlier pre-composition gates remain historical; current rows below qualify only their captured source. The historical backend rows precede the expanded capture, guardrail and goal-presence lifecycle tests; their production backend remains unchanged, but the new fixture scope must execute before it is qualified.

| Gate | Result and retained raw SHA-256 |
|---|---|
| Composed library/scenario/runtime/recovery compatibility | Passed, 182.348s; `872dc3602f12e64fba9ebe5af39adc6c6ddd62e991eb1a8da46d196a8b41dbfe` |
| Actual PostgreSQL16.15 lifecycle, close/reopen and verified owned-schema removal | Passed, 84.636s; `99702cc9b160291830c6a440f4ed37722f41d8d0f950157917604838343b7e8c` |
| Composed SQLite/PostgreSQL/API targeted race, two repeats; both owned schemas removed | Passed, 141.024s; `d139d5cbf915a6fbd037dc25f4235bd9b277bef15776f10b28c55a831c994520` |
| Composed affected frontend and Help units, 13 files/290 tests, before guided-goal fixes | Passed, 30.952s; `3ab7f58a7fad6f218f709459c0dafbd623c9a70aa8925fb293451cdeba366865` |
| Final adjacent TestHub units, 107 tests | Passed, 4.428s; `968292adf326ba5f8f282f4492bae6178bd9f3fc148244333982fc94b52ca235` |
| Final frontend typecheck/lint | Passed, 43.834/18.048s; `a691c3ed7f868f4224b319aa9ea49b8db48d41717efab023a87b3a64b7133ee9` / `bf7ea878e62d715055d29dd6955852b7c24f575ba3d1aa177c99df05cc0a6eb6` |
| Final dashboard build | Passed, 46.985s; `61ec04e5479d5d50b3fda45a5b2fd4573c1af3d11e27ddee386535846400e46a` |
| Evidence-order-only typecheck/lint repeat | Passed, 41.001/23.758s; source and embed unchanged |
| Default-role request oracle and actual draft/serializer units, 45 tests | Passed, 1.487s; `b5e0197b31f78c8b0f1d3fda5ee5df1c55026faf88dade4642faf1e931b65dab` |
| Test-only default-role typecheck/lint repeat | Passed, 34.711/17.171s; production and embed unchanged |

Generated OpenAPI, DSL and TypeScript freshness checks, documentation checks and diff checks pass for the composed backend. Missing PostgreSQL fixture skips are explicitly unqualified; the rows above used the actual fixture. Registered browser cases cover scenario rename/reopen, group-overridden held runs, reachable queued copies, concurrent real Start against an owned HTTP target, local-auth viewer refusal and actual Results/WebSocket group save/clear paths. These are not accepted until their corrected both-profile execution completes.

The parent independently reviewed the three final TestHub/workflow inputs and the evidence-only ordering correction with no additional findings. Final source/embed freeze (`evidence/fidelity-default-role-final-source-embed-freeze.json`) captures 52 relevant source inputs and all 251 embedded files; independent review (`evidence/fidelity-guided-goal-parent-clean-review.json`) records the exact scope. The final embedded guide states narrow factual semantics without claiming that future gates passed.

<a id="feature-completion-authoring-fidelity-2026-10-05--pending-qualification-and-session-ownership"></a>
### Pending qualification and session ownership

The frontend four-finding and v6 Go correction reviews are clean; the fresh v6 embed and actual SQLite/PostgreSQL normal/race gates pass. The corrected v7 browsers passed twice in both auth profiles, and the v8 screenshot correction has a clean focused parent repeat. Both full Go attempts physically joined and remain retained as failed; their generated QA correction, native prerequisites and the v11 fixture qualification are separately scoped. Parent owns one final composed full Go run with the required native runtimes, the full dashboard cycle in both auth profiles, generation/documentation checks and final review. The child heavy/API window was released after the v11 race gate joined. No child feature commit/push or shared cache cleanup has occurred. Only the 10 GB benchmark was waived; unrelated mandatory scale/platform/restart/HA/provider acceptance stays open.

The coherent v6 inventory (`evidence/fidelity-same-mode-delay-state-fence-source-inventory-v6.json`), SHA `881f11df99a5c8c7576fc10a2d40401e4389e85a0b5a6ce7c8aa6df338eb89bf`, freezes 53 inputs and nine stable dependencies. The independent v6 repeat (`evidence/fidelity-same-mode-delay-fence-independent-review-v6.json`), SHA `c8f8b5132afa1c109b1a4fbef55c278f1a8b0a20e0720f773eae11171934261e`, found no additional issues in the source-admission, private feature projection, final EndedAt/mode fence and compatibility/reopen scope. The generic iteration validator is restored exactly to HEAD. Earlier v5 evidence retains the temporary broader restriction and is superseded by this corrected scope.

The eight affected dashboard files now pass 250 tests, raw `8404d0f4be4e0cfd8202a04a7c3a8330e076218844ff7ca877008389ffeb5877`. A fresh v6 build/typecheck/lint passed in 52.730s, raw `33e58ed0a2e9e567c7a6188b836322d01d80d013ab28b4060fa8972734b1f2e8`; documentation checks passed for 178 active Markdown files. The actual SQLite/PostgreSQL normal repeat (`evidence/fidelity-same-mode-sqlite-postgres-normal-1.json`) passed all 35 top-level tests in 98.464s, raw `1d32d60e51eec8984f6bba68e489f32f08966b23adfe513b2ae32d1a71a9f935`, with complete source and 251-file embed bookends unchanged. Both database lifecycle tests executed rather than skipped, including positive iteration delay copy, explicit `0s` clear and durable reopen. Targeted race, fresh actual browsers and full gates remain pending.


The parent v6 focused repeat (`evidence/fidelity-delay-parent-focused-review-v6.json`), SHA `bb1ffe7e5dbc1cb7fed2fd94c566b6f213592861a282df202801aea5ecd8813d`, is also clean. The actual SQLite/PostgreSQL race repeat passed 70 top-level executions with zero skips in 161.338s, raw `db4910b0a55b6dca05692874dc658cc97c5d639820ae701391e4bdd0d4d58ffe`. Both temporary PostgreSQL schemas were independently checked absent on cleanup, and complete source/embed bookends stayed unchanged.

The v6 twice-repeated dev-admin browser attempt (`evidence/fidelity-same-mode-browser-dev-admin-repeat-1.json`) is retained as failed: 48 passed, eight failures across four cases, two expected profile skips, 261.213s, raw `15a6eaaac67fd6138574c4a09b029e01637e4aa5c567a4de5b51cbe320f7c4bc`. Screenshots show the intended controls; exact locators omitted accessible label text or the submit group-count suffix. The complete original artifacts (`evidence/browser-v6-dev-admin-failed/artifact-manifest.json`) preserve all 66 files byte-for-byte. The v7 source inventory (`evidence/fidelity-browser-accessible-name-source-inventory-v7.json`), SHA `d2cbc44d186bf66f5e24ad0492a7b3144f457d46900faa272b1fd64c811706c1`, changes only the two E2E fixture files to scoped control roles and observed names. Payload, source immutability, zero traffic and single-POST assertions are retained; no production or embed change is involved. Corrected actual repeat acceptance remains pending.

Both corrected v7 actual auth repeats pass with complete source/embed bookends unchanged: dev-admin 56 passed/two expected profile skips in 114.929s, raw `25f233e4c67b34cbbe2a049cce3853e8fac03a361c6aba798cbc3526d0a18f79`; local-auth all 58 passed in 511.502s, raw `7fa06909b5c9f9c8f7d68de71f7e3a781b36f3aeae4d017acee775b2131e8da3`. The focused v7 fixture review (`evidence/fidelity-browser-fixture-parent-repeat-review-v7.json`), SHA `17d8fc98570637fa5f7898e309556d4630a12391e178d8754e9ab5359326c3d7`, is clean. Both 40-PNG artifact sets are retained and separately indexed.

Default line/JUnit reporting omits successful in-memory JSON attachments. Separate unchanged-source eight-case reporter runs passed in both profiles and retain the exact original reports plus 20 decoded JSON body attachments each: dev-admin (`evidence/browser-v7-json-dev-admin-passed/artifact-manifest.json`), raw `3cc4d6360bd9d96132f54501340ee353ae82cbe2231041b819b6fcbe716b488a`, and local-auth (`evidence/browser-v7-json-local-auth-passed/artifact-manifest.json`), raw `372f31db0d2a6119b0c8bc85def6486d6c23034f352bc8a38482d76368cdae87`. Assertions are unchanged; retention is separately qualified from the earlier JUnit outcomes.

Parent visual inspection found a translucent queued-copy screenshot. The unchanged-application settled runtime probe (`evidence/browser-v7-settled-dev-admin-passed/artifact-manifest.json`) passed in 35.814s, raw `832452c54e048c3288d33040700b60a4058756f5697223dc8a7cb01788ab55c9`. It joined actual animations, measured all five ancestor opacities/effective opacity exactly one and an opaque `rgb(11,12,16)` dialog surface, recorded 58 controls, verified unchanged source/zero target requests and retained three animations-disabled screenshots. Existing Modal CSS supplies a 0.5s opacity animation; a product opacity defect was not demonstrated. The original screenshots remain unchanged. The v8 freeze (`evidence/fidelity-settled-screenshot-source-inventory-v8.json`), SHA `172c65d5abb410e83f08130f8ad631bf452b4f3b142389d375add02779a14dea`, changes one fixture screenshot call to disable animations. Application/API/guide/embed and all other inputs remain unchanged. Current broad Go/dashboard acceptance remains pending.

The v8 focused screenshot repeat (`evidence/fidelity-v8-settled-fixture-parent-repeat-review.json`), SHA `6ccbf0da507ef30c89dcff12191fd1fcf378a2d6fae76279adfc1c68ec7d4657`, confirms all 62 input hashes match and the only fixture delta disables screenshot animations. Parent visual inspection of the actual settled viewport and enabled-submit images found the dialog opaque and readable. This review is separate from the pending broad execution gates.

The first v8 full Go attempt (`evidence/fidelity-v8-full-go-1.json`) completed in 1342.081s with exit one, raw `535285d355cdc5a3094bb65e953eeb7d0700f3091598cdfe9101aad6322943e1`. All 5220 source and 251 embed bookends match. The complete failure context (`evidence/fidelity-v8-full-go-1-failure-context.json`) retains exactly two failing packages: QA generated-dashboard freshness and native WebKit `TestBrowserDocumentActualNativeIdentityAndNoInteraction`, whose independent frame INP finality assertion failed with WebKit 26.5. Every other package passed, including the API suite in 578.367s. The browser failure remains unqualified and under review; it has not been dismissed as a fixture race.

The generated QA correction (`evidence/fidelity-v8-generated-qa-count-fix.json`) runs the repository generator and changes only the tracked Playwright count from 65 to 66. Exact original bytes and a one-cell diff are retained; generator input hashes are unchanged. This count applies only to the authoring worktree. Integration must regenerate after composing its additional spec files. No application, Go, guide or embed source changed for this correction.

The parent exact generated-count review (`evidence/fidelity-v8-generated-qa-count-parent-review.json`), SHA `7875d42247b52dcf0f82092920042c03923846da83b13be056ea574fdb32ed5d`, is clean. The repository freshness checker passed in 1.779s, and current documentation checks passed for 186 active Markdown files in 0.922s, with full source/embed bookends unchanged. These light checks close only the generated QA delta; the native WebKit failure and broad execution remain pending.

The v9 composition freeze (`evidence/fidelity-native-frame-prerequisite-source-embed-inventory-v9.json`) retains all 62 authoring v8 hashes unchanged, count66 generated QA and the qualified native frame isolation prerequisite. Origin `a4217dcd9` was cherry-picked as `6e2f1082efcc83054c79d74ccf89e43625b4a4b0`; its two source hashes match the clean independent static review (`evidence/fidelity-native-frame-isolation-independent-review.json`) and retained qualification audit. The original native oracle is unchanged. Full Go repeats in required native mode for Chromium/WebKit; application-specific acceptance still waits for the current full Go/dashboard cycle.

<a id="feature-completion-authoring-fidelity-2026-10-05--required-native-full-go-repeat-v9"></a>
### Required native full Go repeat — v9

The complete receipt (`evidence/fidelity-v9-full-go-2.json`), raw log (`evidence/fidelity-v9-full-go-2.log`) and failure contexts (`evidence/fidelity-v9-full-go-2-failure-context.json`) retain the 1592.586-second exit-1 run. All 5221 source and 251 embedded-file bookends match. Generated QA freshness now passes, but `pkg/api` and `pkg/protocol/browser` remain red. The qualified stale-frame isolation prerequisite did not eliminate the WebKit failure under the full package run; no native skips or relaxed finality assertions were introduced. Archive disk guards stay intact, and the unrelated historical masking case is not evidence of a disclosure failure. Current diagnosis and resource remediation precede any further broad acceptance claim.


<a id="feature-completion-authoring-fidelity-2026-10-05--owned-distributed-fixture-correction-v11"></a>
### Owned distributed fixture correction — v11

The v9 failure log exposed post-test runner/generator work outliving gRPC and caller-owned SQLite. A fixture-only correction now retains the canonical runtime, actual generator, run driver, archive commit fence, reconciliation/journal completion, gRPC Serve and Hub.Run barriers. Idempotent cleanup stops and physically joins those owned workers before returning to the caller that closes storage. The real concrete distributed backend, independent 1500ms RPC budgets, 900ms delays, activation waits and persistence assertions are preserved. No production timeout or archive disk reserve changed.

Both meaningful early/activated cleanup regressions first ran against transport-only cleanup. The first RED (`evidence/fidelity-v10-ordering-cleanup-red-1.json`) additionally exposed an introduced safety-oracle mistake: CreateTest returns a read snapshot, so terminal polling must retain the canonical runtime. Its stack and exact owned-process SIGQUIT termination remain retained, without acceptance claims. The corrected natural-join RED (`evidence/fidelity-v10-ordering-cleanup-red-2.json`) then failed only the intended live-driver, retained-capacity and missing lifecycle-join assertions.

The exact fixture freeze (`evidence/fidelity-ordering-owned-cleanup-source-inventory-v11.json`) and clean parent review (`evidence/fidelity-ordering-fixture-parent-review-v11.json`) retain the sole changed test file and ten dependencies. Three normal repeats (`evidence/fidelity-v11-eight-failures-ordering-normal-3.json`) passed all 36 top-level executions across the eight previously failing API paths and four existing/new ordering seams in 84.912s, raw `b9d014cdb9e22a0ca91ef32498669973b7e759efd4f691ce75fe64d5818f68b7`. This repeat reaches the historical ZIP masking assertions that the earlier HTTP-500 attempt did not reach; prior JSON masking checks had already passed. Two race repeats (`evidence/fidelity-v11-ordering-owned-cleanup-race-2.json`) passed all ten top-level ordering/cleanup executions in 141.781s, raw `298ab19955c17a8d57c96d0a8de93e8423173b6934701d0bd01e4f23375bd130`. Neither gate skipped tests; their 5221 source and 251 embedded-file bookends match.

The qualification index (`evidence/fidelity-ordering-owned-cleanup-qualification-v11.json`) distinguishes these actual SQLite/generator checks from earlier PostgreSQL authoring checks. All 53 authored feature inputs and nine stable dependencies remain v9; production and embedded source did not change for this repair. The child independently reviewed the separate neutral document finality correction (`evidence/fidelity-neutral-document-finality-independent-review-v1.json`) in parent integration with no additional finding; that static receipt does not claim local execution. Parent semantic composition (`evidence/fidelity-authoring-root-semantic-composition.json`) preserves seven shared merges and excludes the stale standalone embedded build. Final combined required-native Go and both-profile dashboard gates remain pending.

The final owned documentation check (`evidence/fidelity-v11-fixture-evidence-docs-1.json`) passed for 187 active Markdown files in 0.703s with source/embed bookends unchanged. This light check qualifies the refreshed QA links and inventory only; composed generation and broad execution remain parent-owned.

<!-- Original record: docs/qa/reviews/feature-completion-import-publication-2026-10-05/README.md; local evidence kept outside git -->
<a id="feature-completion-import-publication-2026-10-05"></a>

## Import publication primitive qualification


This slice adds a dedicated durable fence for `library.import_claims.v1`, an exact raw-byte SHA-256 paired with row presence, bounded SQL reads, and dedicated claims CAS. Existing API claim mutation and publication reads use the qualified primitive; blank-script-ID repair acquires the same fence before source work. Generic settings behavior remains unchanged. Expected-empty CAS inserts only a missing row; taking the fence does not create a placeholder claims row.

The publication fence is acquired before project/source locks. Real PostgreSQL tests observe the specific advisory-key wait for absent and existing settings, then independently probe project/source rows with `NOWAIT` before releasing the fence. SQLite tests use two independent stores and preserve single-winner CAS behavior. Raw-byte bounds use SQLite BLOB length and PostgreSQL octet length. Both byte and entry limits remain 64 MiB and 10,000. Invalid UTF-8 is refused before decoding identities, and failed repair preserves original source rows and serialized bytes.

The existing losing, unmutated direct-import rollback contract remains: revalidate receipt ownership before releasing claims, record terminal no-op claims, and preserve completed peer assets. Related failure/count fixtures now intercept the dedicated seam so qualification still exercises real durable failures. The existing lost-unmutated regression body is unchanged from the foundation HEAD.

Public scenario authoring support and dependency-aware scenario admission remain separate work. The foundation's public capability guards are unchanged. No commit, push, or build-cache cleanup has occurred in this slice.

<a id="feature-completion-import-publication-2026-10-05--evidence"></a>
### Evidence

Source inventory v1 (`evidence/publication-primitive-source-inventory-v1.json`) freezes 11 changed Go files and 14 unchanged dependencies. Gate index v4 (`evidence/publication-gate-index-v4.json`) records exact receipt/log hashes, independent verification of API test counts, and remaining composition limits. Historical indexes and the original red regression remain alongside the final evidence. The execution helper is retained. Root's race driver output (`evidence/publication-affected-api-import-race-1.driver.stdout.txt.gz`) is preserved as deterministic gzip; its packaging record (`evidence/publication-driver-lossless-compression-v1.json`) verifies the exact original bytes and hash after decompression. The original staging whitespace failure is retained in the gate index.

- The red bounds regression failed for both excessive entry count and excessive bytes, proving the previous blank-ID rewrite accepted data beyond the existing publication limits.
- Focused normal storage tests passed, including exact row presence and bytes, two-store concurrency, corrupt-identity refusal, output expansion bounds, and complete source/reference rollback.
- An earlier race run passed with two repeats on the same production implementation. The final frozen-source storage race repeat including those additions passed twice (80.979 s), with unchanged source bookends.
- Actual PostgreSQL 16.15 passed exact byte/presence tests and all six absent/existing snapshot/CAS/repair lock-order cases. All waiters joined and owned schemas were removed by the fixture.
- Affected API/import/rollback qualification passed 113 top-level tests normally (106.174 s) and the same set twice under the race detector (226 top-level passes, 356.346 s). Both runs had zero skips or failures. The existing lost-unmutated oracle passed once normally and twice under race; every frozen source and dependency hash remained unchanged.

The independent source review (`evidence/publication-parent-independent-review-v1.json`) and repeat scoped review (`evidence/publication-final-scoped-review-v2.json`) found no additional actionable issues. Documentation and diff checks are recorded in the final gate index. Semantic composition with the authoring-fidelity changes in `library_import_claims.go` and the composed full Go merge gate remain pending. A separate native WebKit INP failure is being fixed outside this slice; these scoped checks do not establish full-program acceptance.

<!-- Original record: docs/qa/reviews/feature-completion-scenario-revision-2026-10-05/README.md; local evidence kept outside git -->
<a id="feature-completion-scenario-revision-2026-10-05"></a>

## Scenario revision foundation and capability refusal — 2026-10-05


This AUTH-1 slice supplies durable scenario revision primitives and refuses unavailable HTTP save features explicitly. It does not implement canonical scenario `view=authoring`, strong scenario ETags, supported conditional saves, or editor conflict recovery. Those remain a separate vertical. Existing queued-run copy behavior retains its own compatibility contract; this slice does not qualify atomic inherited-script runtime materialization.

Migration 38 appends `durable-scenario-content-revision` with fingerprint `185b01e0a6e99c3a5d43a668d2113d7258b5cd89aa0f30e45f094bb2f9c7f5d4`. Released migrations 1–37 remain unchanged. Database-issued `scn-v1-` nonces rotate for authored root/group/pool/placement/monitor changes, including both parents of reparented children. Controlled initialization backfills only missing nonces; malformed persisted values refuse qualified reads and reopen.

Exact-ID and bounded exact-RefKey readers return parent, groups, pool bindings, placement identities, monitor selection, derived durable project and nonce in one repeatable transaction. Ambiguous references, missing or mixed source ownership, and incoherent placement or monitor payloads refuse authority. Internal snapshots retain clocks, incarnation and lifecycle context for admission; a future HTTP authoring projection must exclude mutable display context and clocks. These snapshots are not themselves a tagged HTTP representation.

Conditioned storage operations clone caller input, preserve explicit empty stages and zero pointer overrides, lock the durable project before scenario/source/pool dependencies, and return their final-transaction receipt only after commit succeeds. SQLite reserves its writer before reads to prevent competing deferred writer upgrades. Legacy create-only replay, timestamp/internal/import operations keep their existing independent contracts. Ordinary script content does not rotate scenario authority; supported script saves refuse ownership replacement, while persisted script-owner repairs invalidate affected scenario nonces.

The review found that adding a configurable scenario strict-save policy before the HTTP vertical would advertise a protection the ordinary PUT did not honor. Public project POST/PUT now reject `require_scenario_save_if_match=true` with 400 before project or hierarchy persistence. False/omitted values and supported script strict-save settings remain accepted. Scenario PUT revalidates canonical durable ownership before refusing every presented `If-Match` with 503, including empty, repeated and noncanonical header-map keys. Externally configured strict policy also refuses the existing unconditioned save. Known capability refusals expose only fixed error/code guidance and an opaque reference; private persistence failures retain generic error responses. Regressions prove no authored-state, secret or provisioning-journal mutation on refusal.

This compatibility read is not a final transaction fence against policy enablement outside public APIs during a save. That fence, public conditioned-save admission, source dependency revisions, secrets/publication integration and dashboard paired authority/recovery remain part of the next vertical. No canonical authoring GET or strong ETag is advertised in generated contracts.

<a id="feature-completion-scenario-revision-2026-10-05--source-and-review"></a>
### Source and review

The frozen 12-file storage inventory (`evidence/scenario-revision-final-source-inventory.json`) has SHA-256 `a832fc33213abf654601039ec71a5de3eb0377fc3e54e49730f22647d3877c8f`. The frozen seven-file API/OpenAPI inventory (`evidence/scenario-revision-policy-guard-openapi-final-source-inventory.json`) has SHA-256 `808fe3bb36588053dd7818acc43e072ec65e792f66eb8921e146ac63b3f75507`. Root storage review (`evidence/scenario-revision-root-independent-source-review.json`) and root corrective API/OpenAPI review (`evidence/scenario-revision-policy-guard-openapi-root-independent-source-review.json`) found no additional actionable finding. The resumed agent re-read the corrective guard, safe responses, generated hook and regressions without a new finding. All 19 current source hashes still match their frozen inventories.

The exact five-artifact generated inventory (`evidence/scenario-revision-policy-guard-generated-contract-final-inventory.json`) records the repository Swagger/catalog, both actual embedded `pkg/api/apidocs` copies and generated TypeScript. Only both Swagger copies and TypeScript changed from the base; both endpoint catalogs remain unchanged. The final generated diff exposes the optional unavailable policy and header and fixed optional capability codes without advertising a canonical authoring GET or strong ETag.

<a id="feature-completion-scenario-revision-2026-10-05--executed-gates-and-current-qualification"></a>
### Executed gates and current qualification

| Gate | Command duration | Evidence |
| --- | --- | --- |
| Full affected storage package normal | 52.089 s | receipt (`evidence/storage-final-all-normal.json`), raw log (`evidence/storage-final-all-normal.log`) |
| Scenario, legacy scenario and script-authoring targeted race | 76.433 s | receipt (`evidence/storage-final-scenario-and-script-race.json`), raw log (`evidence/storage-final-scenario-and-script-race.log`) |
| Actual PostgreSQL 16.15 normal | 26.132 s | receipt (`evidence/storage-final-actual-postgres-normal.json`), raw log (`evidence/storage-final-actual-postgres-normal.log`) |
| Actual PostgreSQL 16.15 race | 55.674 s | receipt (`evidence/storage-final-actual-postgres-race.json`), raw log (`evidence/storage-final-actual-postgres-race.log`) |
| Corrective API and compatibility tests, three repeats | 1049.903 s | receipt (`evidence/foundation-policy-api-normal-1.json`), raw log (`evidence/foundation-policy-api-normal-1.stdout.txt`) |
| Corrective API and compatibility race tests, two repeats | 1384.786 s | receipt (`evidence/foundation-policy-api-race-2.json`), raw log (`evidence/foundation-policy-api-race-2.stdout.txt`) |
| Corrected five-artifact API generation | 34.733 s | receipt (`evidence/foundation-api-generation-2.json`), raw log (`evidence/foundation-api-generation-2.stdout.txt`) |
| Explicit TypeScript generation repeat | 0.708 s | receipt (`evidence/foundation-typescript-generation-2.json`), raw log (`evidence/foundation-typescript-generation-2.stdout.txt`) |
| Final API/OpenAPI/TypeScript freshness | 80.94 s | receipt (`evidence/foundation-generated-freshness-3.json`), raw log (`evidence/foundation-generated-freshness-3.stdout.txt`) |

The four final storage gates and the resumed corrective API/generation gates passed with matching source bookends. The API normal and race raw results were 10.606 s and 294.823 s respectively; the command durations include compilation. The actual PostgreSQL fixture logged server 16.15, used unique owned `apexload_scenario_revision_*` schemas and dropped each schema after its stores closed. It exercised observed hierarchy-lock waits/NOWAIT probes, strict-policy races, stale root/group/pool/monitor conditions, trigger/function repair, missing-only backfill, both-parent reparenting, rollback, coherent cross-store reads and one-winner creation. SQLite additionally exercises duplicate aliases, legacy corruption refusal, deferred commit failure with no receipt, caller preservation and receipt equality.

The final generated freshness gate passed with unchanged Go sources and exact matching hashes for all five generated artifacts. Documentation and diff closure are recorded separately below. Full composed Go and dashboard merge gates, public authoring/secret integration and both-auth editor/browser acceptance remain separate and pending. No full AUTH-1 or whole-program completion is claimed.

The finalized QA narrative passed documentation validation (`evidence/foundation-final-docs-check-2.json`), covering 177 active Markdown files, and the diff check (`evidence/foundation-final-diff-check-2.json`). Both commands exited 0 with unchanged source and generated-artifact bookends. The gate index selects current passing receipts; superseded and interrupted records remain historical.

<a id="feature-completion-scenario-revision-2026-10-05--retained-development-failures-and-inventory-limits"></a>
### Retained development failures and inventory limits

All failed and superseded storage receipts/logs remain preserved and indexed in the historical foundation gate index (`evidence/scenario-revision-final-gate-index.json`). Initial probes ran with the migration fingerprint deliberately pending. Subsequent SQLite tests exposed lost explicit empty stages, an impossible foreign-key fixture mutation and competing deferred writer upgrades; meaningful regressions were retained. Initial PostgreSQL failures involved a fixture passing an integer to a bool and a SQLite trigger name. The full storage run also exposed contextual JSON-validation precedence and a historical migration fixture retaining current-column triggers. The final four storage gates passed after these corrections.

The original generation-1 receipts enumerated only three actual generated artifacts and do not attest the two actual `pkg/api/apidocs` files. Their bytes and that limitation remain preserved. The separate original source-only generated review enumerated five paths; current v2 generation receipts and the current inventory cover all five directly. The final gate index preserves those earlier generation/doc/diff receipts as historical, selects the passed corrective API normal/race and v2 generation/freshness receipts, and closes only the ineffective public-policy configuration finding. Full conditional save support remains pending.

The abandoned prior race invocation retains its exact empty raw log (`evidence/foundation-policy-api-race-1.stdout.txt`) and a separate interrupted-not-acceptance observation (`evidence/foundation-policy-api-race-1-interrupted-observation.json`). Its original command, exit result and duration were unavailable on resume and remain unknown. The recovered repeat-2 race gate supplies current acceptance instead.

The interrupted freshness-2 attempt retains its exact empty raw log (`evidence/foundation-generated-freshness-2.stdout.txt`) and its interrupted observation (`evidence/foundation-generated-freshness-2-interrupted-observation.json`). Session 29282 was no longer registered and no matching owned process remained. Its exit code and duration remain unknown; freshness-3 supplies acceptance.

No cache cleanup was performed because dependent resumed builds, tests, browser verification and fixes still need the shared session caches.

<!-- Original record: docs/qa/reviews/feature-completion-scenario-source-admission-2026-10-05/README.md; local evidence kept outside git -->
<a id="feature-completion-scenario-source-admission-2026-10-05"></a>

## Private scenario source admission qualification


Current composed source/test qualification: the default full Go suite, full dashboard cycle in both authentication profiles, affected API races, actual PostgreSQL cohort, SDK contract and generated-contract checks passed in the [composed review](INTEGRATION_2026-10.md#feature-completion-composed-review-2026-10-05). That qualification is scoped; the broader feature requirements and explicitly unrun acceptance remain open. The following component checkpoint predates these composed results. Its original bytes are preserved in the historical checkpoint (`README.component-checkpoint.txt`).

This backend slice shares bounded publication facts between storage and API visibility while retaining the rich API recovery metadata validator. The optional private source-aware scenario interface captures durable source snapshots and canonical source conditions, then checks exact source incarnation/content nonces and publication row presence/raw SHA in the final transaction. Public scenario capability guards remain unchanged.

Final lock order is publication, project, existing scenario, sorted unique sources, then sorted unique pools. Initial and final checks reject a prepared uncommitted direct-import claim over a surviving source even when its script nonce is unchanged. Create collisions return a conflict without adopting a winner or issuing the attempted candidate's receipt. Ordinary low-level operations retain their prior behavior. No callback, request publication flag, or cached DSL establishes write authority.

Final source inventory v1 (`evidence/scenario-source-final-source-inventory-v1.json`) freezes 11 changed/new Go files and 17 unchanged dependencies. The earlier development inventories, failing fixture run, raw logs, execution helper, and design/development reviews remain in evidence. The first corrected development gate passed 23 top-level storage tests, including existing scenario-authoring compatibility; that gate predates the final API and PostgreSQL test additions and is not final acceptance.

The final frozen-source normal storage gate passed 23 top-level tests (52 including subtests), with no skips or failures. Actual PostgreSQL 16.15 passed five top-level suites (23 including subtests), including nine observed lock-boundary cases, prepared-survivor barriers for create/update, two source editor/archive commit waits, and the raw administrative owner inversion. All 19 owned fixture schemas cleaned up without fixture errors and every owned waiter joined. The frozen-source storage race gate passed both repetitions: 46 top-level passes (104 including subtests), with no skips, failures, source changes or generated-artifact changes. Gate index v3 (`evidence/scenario-source-gate-index-v3.json`) retains exact hashes and the earlier fixture failures.

Qualification remains in progress. The independent final static review (`evidence/scenario-source-parent-final-static-review-v1.json`) found no additional source issues and verified all 28 frozen hashes. Minimal API and affected import compatibility checks and composed merge acceptance remain pending. The parent coordinates the shared heavy build window; API and native work remain deferred while the separate native-runtime finality investigation uses that window. No native runtime, commit, push or cache cleanup is inferred from these storage checks.

<!-- Original record: docs/qa/reviews/har-import-evidence-2026-10-02/additional-review.md; local evidence kept outside git -->
<a id="har-import-evidence-2026-10-02-additional-review"></a>

## Additional HAR import evidence — 2026-10-02


The three additional sources were inspected offline. Both ZIP files were bounded
and extracted into private temporary storage; source files were unchanged. Their
contents were treated as data, and no recorded host was contacted. Customer
captures, URLs, credentials, cookies and payload values are excluded from the
repository. See the aggregate evidence (`additional-evidence.json`) for digests.

<a id="har-import-evidence-2026-10-02-additional-review--source-results"></a>
### Source results

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

<a id="har-import-evidence-2026-10-02-additional-review--evidence-driven-changes"></a>
### Evidence-driven changes

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

<a id="har-import-evidence-2026-10-02-additional-review--remaining-review-limits"></a>
### Remaining review limits

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

<a id="har-import-evidence-2026-10-02-additional-review--validation"></a>
### Validation

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

<!-- Original record: docs/qa/reviews/har-import-evidence-2026-10-02/code-review.md; local evidence kept outside git -->
<a id="har-import-evidence-2026-10-02-code-review"></a>

## HAR fidelity code review — 2026-10-02


Scope: the HAR response decoding, framing/version projection,
correlation degradation, BOM/proxy handling, shared Salesforce observations,
route-specific upload limits, dashboard file validation, synthetic regressions
and disclosure-safe capture reports.

<a id="har-import-evidence-2026-10-02-code-review--findings-fixed"></a>
### Findings fixed

**P2 — Base64 C1 text was mistaken for compressed bytes.** The new decoded-text
check rejected every Unicode control character, including UTF-8 C1 characters
that legitimately occur in JavaScript literals. Raw text was preserved, but the
same resource under HAR base64 encoding was incorrectly decompressed using stale
wire headers, falling back to its base64 spelling and losing response analysis.
The check now distinguishes binary-looking ASCII control bytes while accepting
UTF-8 C1 text. A regression reproduces the failure on the reviewed code under
retained gzip, deflate, Brotli and zstd headers, then passes with the correction.
Compressed-byte compatibility, corrupt-data warnings and expansion bounds remain
covered by the existing tests.

**P2 — Generated QA dashboard was stale.** The new browser regression adds a
Playwright spec file. The integrated documentation freshness test detected that
the generated inventory still showed 55 files. Regeneration updates it to 56;
the freshness regression and the complete integrated Go suite pass.

<a id="har-import-evidence-2026-10-02-code-review--final-review"></a>
### Final review

Checked parser/source preservation, request accounting, correlation provenance
and incomplete-analysis behavior, binary-body rewrite guards, repeated headers,
Content-Length rejection paths, transport version gates, upload authorization
and size envelopes, primary/comparison picker validation, and test/evidence scope.
No further actionable findings were identified in this review. Recorded-host
replay remains outside the evidence; known source/runtime blockers are retained.

The changes are integrated with main at `2314ab698c805f5b75ac8b984143a6389deef03b`.
The integrated result passes the complete Go suite, dashboard checks and both
browser auth profiles. See
validation results (`additional-validation.json`).

<!-- Original record: docs/qa/reviews/har-import-evidence-2026-10-02/import-review.md; local evidence kept outside git -->
<a id="har-import-evidence-2026-10-02-import-review"></a>

## HAR import fidelity evidence — 2026-10-02


Two supplied HAR 1.2 recordings were converted offline. Source URLs, credentials,
cookies, payload values and the original recordings are excluded from this report
and its regression fixtures. No recorded request was executed. Original files were
read without modification; their SHA-256 digests are in the aggregate evidence.

<a id="har-import-evidence-2026-10-02-import-review--observed-results"></a>
### Observed results

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
limit was changed in this first pass. The [additional capture review](#har-import-evidence-2026-10-02-additional-review)
subsequently raised HAR capture uploads to 100 MiB. The recordings describe different journeys and were assessed
independently rather than treated as a record/replay comparison pair.

Machine-readable before evidence (`before.json`), after evidence (`after.json`),
and static-asset mode evidence (`with-static-assets.json`) contain only aggregate
counts, diagnostic categories, file names and digests.
Diagnostic counts can be lower than affected-entry counts because identical
findings at the same method/URL are deduplicated.

<a id="har-import-evidence-2026-10-02-import-review--fixes-supported-by-the-recordings"></a>
### Fixes supported by the recordings

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

<a id="har-import-evidence-2026-10-02-import-review--review-and-execution-limits"></a>
### Review and execution limits

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

<a id="har-import-evidence-2026-10-02-import-review--regression-coverage"></a>
### Regression coverage

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
validation summary (`validation.json`).

<!-- Original record: docs/qa/reviews/import-repair-readiness-2026-10-24/README.md; local evidence kept outside git -->
<a id="import-repair-readiness-2026-10-24"></a>

## LoadRunner import, Script Editor and debug readiness — 2026-10-24


An evaluation of a real LoadRunner 24.3 Salesforce Aura iPad archive (customer data, not committed) walked the import wizard, the Script Editor and **Debug one iteration** and found that the three surfaces told different stories: guided repairs applied during validation were lost on save, so the editor showed blockers the import had resolved and debug was refused; the import and the editor reported missing values the runtime provides; the Debug button stayed enabled over known blockers and failed with a raw gate string; and every count disagreed. The user approved four fixes and the standard independent review cycle, stopping at the first round with no medium findings. This record states the resulting contract and the review.

<a id="import-repair-readiness-2026-10-24--contract"></a>
### Contract

<a id="import-repair-readiness-2026-10-24--fix1-a-reviewed-draft-is-saved-as-reviewed"></a>
#### FIX1: a reviewed draft is saved as reviewed

- A validated archive whose draft was changed in the review (guided repair, applied parameterization or shared values; `reviewedDraftEdits`) is saved from that draft, never converted again (`runConvertValidatedItems` reuses it). Validate and convert return the same envelope, and the save restores the exact attested candidate, so no server change was needed for the save itself.
- Optional parameterization suggestions never halt a save and are not applied automatically.
- Reviewed drafts are protected from batch validation; the queue row offers **Validate again** (which discards the repairs), except while an unconfirmed atomic save of that draft is being reconciled. A reviewed draft keeps the name its review showed (`reviewNameOverride`), and a repaired lane keeps its own credential requirements (`script_index`).
- A refused save is classified: an expired or used candidate, or `409 import_update_target_stale` (and other definitive `4xx`), drops the retained operation and keeps the draft; an unconfirmed save (`5xx`, transport) is resent unchanged, even if the re-import choice changed since, so the server's operation receipt answers whether it committed.

<a id="import-repair-readiness-2026-10-24--fix2-one-producer-contract"></a>
#### FIX2: one producer contract

- `pkg/dsl/varflow` models every source step's writes, reads and removals as the engine performs them (exact name plus lower-case alias, `SelectAll` arrays `NAME_1..n` and `NAME_count`, `web_convert_param`, the `lr_save_*` family, OAuth outputs, live tables) with the execution-order walk (branches intersected, loops promoted only when they run, `do_while` bodies before their condition, parallel outputs prefixed). Server readiness (`requestimport`), the import dependency review (`importreview.AnalyzeDependencies`) and the engine conformance test use it; `dashboard/src/components/variableFlow.ts` ports it, and the shared corpus `pkg/dsl/varflow/testdata/readiness_cases.json` holds both to the same expectations.
- Runtime fidelity: engine steps that only log or use their arguments literally (transactions, timers, metrics, runtime settings, rendezvous) read nothing; an `http_sequence` wrapper reads only its attribution, not the copy of its first request's target; HTTP, SOAP and Java HTTP keep a missing `{{var.*}}` as placeholder text (a Java HTTP stored body that is not base64-encoded excepted); an undeclared `{{param.*}}` blocks.
- Import-evidence metadata (`messaging.MetadataKeyIsEvidence`: `original_url`, `source_*` provenance, `source_evidence`, `asyncapi_*`) is attribution, never interpolated and never refused by the engine; Kafka and JMS sends no longer put `source_evidence` on the wire. `bind_variable` rewrites exactly the metadata replay reads and leaves nested steps and evidence as recorded.
- The engine dispatches source steps on the canonical action type, so legacy `loadrunner_*` spellings write their outputs.

<a id="import-repair-readiness-2026-10-24--fix3-debug-follows-the-server-and-blockers-are-repairable-in-the-editor"></a>
#### FIX3: Debug follows the server, and blockers are repairable in the editor

- The start gate (`replayStartGate.ts`) counts only server-equivalent blockers (review gates, blocking variables, data-source issues, missing project secrets, current server validation issues other than promotion folds). While any exist, Debug reads **Resolve N blockers**; each click (and F5) opens the next blocker where it is fixed and never saves. A draft clean of gates whose saved copy still has them reads **Save and debug**.
- A refused stored-script start answers `400` with `code: script_replay_blocked`, `gate_scope`, `issues` and the saved copy's `preflight` (step names scrubbed); the editor shows it as readiness data, not an error, and never counts it twice.
- `POST /api/v1/scripts/{id}/repairs/preview` and `POST /api/v1/scripts/{id}/repairs/apply` apply the guided repairs (`select_tls`, `replace_proxy_secret`, `bind_variable`, `add_parameter`, `add_extractor`; `inspect` for preview; `restore_body` only by re-import) to a saved script: strong `If-Match` (`428`, `412`), editor access, Java Vuser and memory-only stores refused (`409`, `503 script_revisions_unavailable`), a credential allowance that admits only the operation's own transport fields, a trusted durable write and the audit event `script.repair.apply`. Apply returns the stored copy and its validation without repeating the script. One `select_tls` clears every legacy TLS step.
- The blocked step's inspector explains the gate and hosts the repair panel; repairs need a clean editor.

<a id="import-repair-readiness-2026-10-24--fix4-one-readiness-story"></a>
#### FIX4: one readiness story

- The import wizard, the queue chip, the editor header, Author Problems, Checks & readiness, the start gate and the repair previews count the same blockers, warnings and review notes for the same script; promotion-only review notes are counted separately. Converter TLS warnings are covered by their gates, configuration references are one script-wide blocker, multi-code gates count per code, and a gated value counts once.
- A repair restamps the preserved fidelity report and replay preflight: archive-level converter blockers are kept, project-secret state never enters the persisted report, a repair never turns a promotable report blocked, and the launch refusal names only code-shaped review codes.
- An editor save of an imported script refreshes its server-owned import evaluation inside the durable write (`UpdateScriptEditorSaveDurable`); the client cannot author it, and project secrets are resolved before the write takes its locks.

<a id="import-repair-readiness-2026-10-24--credential-guard-and-disclosure"></a>
#### Credential guard and disclosure

- `RetainedImportCredentialContextChange` ignores non-executable preserved review evidence (`source_review`, `review_artifact`, `project_settings` and other review subtrees) and server-stamped dependency codes, while `HasNewProjectedCredential` still covers them. On the real archive a recorded transaction-order list and long-named review codes had pinned every edit "at script"; credentials kept at script level still hold the whole script.
- `fidelity_report.secret_gated` is exempt from masking only when it is a number.
- Draft validation of a saved credential-preserving import uses the debug start's validator only for a caller that may read the saved script (`requestMayReadStoredScript`); everyone else gets strict draft validation.

<a id="import-repair-readiness-2026-10-24--real-data-verification"></a>
### Real-data verification

Against a scratch controller built from this branch under a `sandbox-exec` profile that denies outbound connections (so debug never reached the customer's systems): validation read `2 block replay · 1 warning · 3 review notes` (the false `SelectAll` and `web_convert_param` warnings gone), one TLS repair cleared both gates, one **Save draft and open in Visual Editor** saved the repaired draft without reconversion, the editor showed the same single warning, Author Problems and the Problems tab agreed, and debug started and executed until the first outbound request was refused locally. Saved unrepaired, the editor offered **Resolve 2 blockers**, focused the repair, and applied it with the editor updated in place (after round 3 slimmed the apply response under the dashboard's 12 MiB cap). A request edit that the credential guard had refused saved after round 4.

<a id="import-repair-readiness-2026-10-24--review"></a>
### Review

Nine rounds of independent reviewers read a frozen snapshot (diff plus after-files) with no git; each candidate finding was verified by skeptics before it was fixed, and every fix carries a test shown to fail without it.

| Round | Confirmed | Medium or higher |
| --- | --- | --- |
| 1 | 23 | 7 (repair dropped converter blockers; secret state persisted; config gates no repair clears; nested gates unreachable; fidelity blockers double-counted) |
| 2 | 16 | 1 high, 4 medium (repair turned a promotable report blocked; Kafka/JMS metadata reads; re-validation dropped repairs; merged Author Problems rows; duplicated refusal issue) |
| 3 | 9 + 2 | apply response over the client cap (also found on the real archive); no-op steps read; preview tiles disagreed; a regressed bind test |
| 4 | 8 + 3 | unscrubbed refusal labels; stale wizard preflight; phantom credential blocker; editor closing; the real-archive guard pin and stale review codes |
| 5 | 10 | allowance reset review keys; draft validation disagreed with the debug start |
| 6 | 4 | SOAP and Java HTTP placeholder rule |
| 7 | 9 | Resolve saved unsaved drafts; sequence wrapper reads; stale-target save loop |
| 8 | 8 | draft-validation equality oracle (re-scoped to callers who cannot read the script) |
| 9 | 8 | none: the cycle stopped and its lows were fixed with tests |

<a id="import-repair-readiness-2026-10-24--decisions"></a>
### Decisions

- An undeclared `{{param.*}}` blocks (the runtime refuses it); promotion-only review notes are a separate count; Debug is gated only on server-equivalent blockers; one TLS repair clears every legacy TLS step; in-editor repairs need a clean editor and never offer `restore_body`.
- The user approved loosening the credential guard for the real archive; the cause turned out to be non-executable evidence, so no executable credential's protection changed.

<a id="import-repair-readiness-2026-10-24--open"></a>
### Open

- Converted-path saves still derive their operation identity from the current update targets (pre-existing), and a principal allowed to start a saved script but not to read it can observe credential reconciliation through the start routes (pre-existing; a product decision).
- A repair preview carries the script before and after, so a script over about 6 MiB cannot be previewed in the editor (it says so).
- The six `TestReplayArtifact*` tests fail locally on macOS on the base commit as well; Linux CI runs them.

<!-- Original record: docs/qa/reviews/import-reviews-2026-09.md; local evidence kept outside git -->
<a id="import-reviews-2026-09"></a>

## Import reviews with open items (September 2026)


Historical `.log` files are ignored private evidence and are not included in a clean checkout. Their paths below identify the original artifacts; they are not bundled links or new validation results.

Status: Open. Every review below still carries remaining work; see each section's Status line.

Grouped from the JMeter, LoadRunner, importer-consistency and fixture-matrix reviews. The evidence folders (`import-fixture-matrix-2026-09-12/`, `importer-consistency-2026-09-23/`, `jmeter-import-fidelity-2026-09-23/`, `loadrunner-issue-detail-2026-09-23/`, `loadrunner-panel-design-2026-09-23/`) stay next to this file. Completed import reviews were moved to [AUDITS_2026-09.md](AUDITS_2026-09.md) (sections "Import reviews 2026-09" and "Import From Source audit").

<a id="import-reviews-2026-09--import-fixture-matrix-and-broker-runbook"></a>
### Import fixture matrix and broker runbook

Date: 2026-09-12. Status: Open: Service Bus, Event Hubs, IBM MQ and customer enterprise replay cases are blocked on fixtures/credentials (see 'Remaining gaps and completion criteria')

<a id="import-reviews-2026-09--import-conversion-and-replay-fixture-matrix"></a>
#### Import conversion and replay fixture matrix

Date: 2026-09-12. This follows the [Import From Source usability audit](AUDITS_2026-09.md#import-from-source-audit-2026-09-12). The matrix validates observable source behavior and records unavailable evidence separately.

<a id="import-reviews-2026-09--result"></a>
##### Result

**16 of 20 cases passed; 4 are blocked; no executed case failed.** The final matrix includes real Kafka and Solace round trips, three attached canonical Bruno SOAP assets, source preservation, mock messaging, and enterprise execution constraints. It does not certify every customer application or full original-tool parity.

The source-backed [manifest](../../qa/import-fixture-matrix.json) and [reproduction runbook](#import-reviews-2026-09--import-fixture-matrix-and-broker-runbook) are checked in; the machine-readable results (`import-fixture-matrix-2026-09-12/evidence/results.json`) and source hashes are local evidence that is not tracked. Per-case private logs use the paths recorded below and require the original evidence collection.

<a id="import-reviews-2026-09--matrix"></a>
##### Matrix

| Case | Result | Provenance | Evidence scope |
| --- | --- | --- | --- |
| bruno-attached-preservation | PASSED (private log: `import-fixture-matrix-2026-09-12/evidence/bruno-attached-preservation.log`) | repository-attached | Byte preservation, variables, headers, body and scripts across three attached SOAP sources |
| bruno-canonical-replay | PASSED (private log: `import-fixture-matrix-2026-09-12/evidence/bruno-canonical-replay.log`) | repository-attached-with-synthetic-environment | Attached source → encrypted bundle → Go replay → pinned Node runner → loopback SOAP target |
| bruno-runner-conformance | PASSED (private log: `import-fixture-matrix-2026-09-12/evidence/bruno-runner-conformance.log`) | synthetic | Pinned runtime behavior and egress/lease/cleanup contracts against local test servers |
| bruno-commit-revisions | PASSED (private log: `import-fixture-matrix-2026-09-12/evidence/bruno-commit-revisions.log`) | synthetic | Canonical inspection, persisted source revision, synchronization and rollback |
| asyncapi-mock-replay | PASSED (private log: `import-fixture-matrix-2026-09-12/evidence/asyncapi-mock-replay.log`) | synthetic | Kafka JSON/Avro/request-reply, JMS publish/consume and mixed-provider split; in-memory brokers |
| broker-topology-mock-replay | PASSED (private log: `import-fixture-matrix-2026-09-12/evidence/broker-topology-mock-replay.log`) | synthetic | MQSC and JNDI topology drafts replay through mock IBM MQ/Solace adapters |
| capture-mock-replay | PASSED (private log: `import-fixture-matrix-2026-09-12/evidence/capture-mock-replay.log`) | synthetic | Capture Avro → persisted/reloaded DSL → mock Event Hubs produce/observe |
| provider-adapter-contracts | PASSED (private log: `import-fixture-matrix-2026-09-12/evidence/provider-adapter-contracts.log`) | synthetic | Real adapter builds and local/unit transport contracts; no live provider certification |
| enterprise-persisted-gates | PASSED (private log: `import-fixture-matrix-2026-09-12/evidence/enterprise-persisted-gates.log`) | synthetic | CloudTest, SAP GUI and disabled Java runner reject execution after draft persistence |
| cloudtest-commit-attestation | PASSED (private log: `import-fixture-matrix-2026-09-12/evidence/cloudtest-commit-attestation.log`) | synthetic | CloudTest commit cannot replace unproven mappings with unattested executable actions |
| enterprise-runtime-fidelity | PASSED (private log: `import-fixture-matrix-2026-09-12/evidence/enterprise-runtime-fidelity.log`) | synthetic | Source runtime options that are not implemented cannot claim execution parity |
| truclient-conversion | PASSED (private log: `import-fixture-matrix-2026-09-12/evidence/truclient-conversion.log`) | synthetic | TruClient archive inventory and preservation; supported browser subset is not universally gated |
| java-conversion-lifecycle | PASSED (private log: `import-fixture-matrix-2026-09-12/evidence/java-conversion-lifecycle.log`) | synthetic | Java VUser and JMS-family sources retain combined lifecycle, manifests and runner constraints |
| pinned-public-corpus | PASSED (private log: `import-fixture-matrix-2026-09-12/evidence/pinned-public-corpus.log`) | public-pinned-corpus | Pinned public LoadRunner/DevWeb/JMeter corpus conversion and protocol posture; no customer replay parity |
| kafka-live-import-replay | PASSED (private log: `import-fixture-matrix-2026-09-12/evidence/kafka-live-import-replay.log`) | synthetic-live-broker | Imported AsyncAPI order event → real Kafka broker → exact consumed JSON, isolated topic and cleanup |
| solace-live | PASSED (private log: `import-fixture-matrix-2026-09-12/evidence/solace-live.log`) | synthetic-live-provider | Imported JNDI topology → live Solace AMQP queue → exact consumed payload |
| servicebus-live | BLOCKED | synthetic-live-provider | Live Azure Service Bus queue publish/consume; topic/session cases remain separate |
| eventhubs-live | BLOCKED | synthetic-live-provider | Existing live Event Hubs produce/observe smoke; does not assert exact marker identity |
| ibmmq-live | BLOCKED | customer-provider-needed | IBM MQ provider certification |
| enterprise-customer-replay | BLOCKED | customer-fixtures-needed | Customer-authenticated enterprise replay parity |

<a id="import-reviews-2026-09--what-the-replay-evidence-establishes"></a>
##### What the replay evidence establishes

- **Canonical Bruno:** all three attached SOAP sources are imported without rewriting their source files, saved as encrypted immutable bundles, resolved through the real artifact store, and executed through the Go replay engine and pinned Bruno 3.5.2 runner. Synthetic environment values direct requests to a loopback target. Exact request paths, SOAP bodies, headers and basic-auth values are checked. Original source assertions pass for the expected HTTP 500 SOAP fault and fail when its fault code is altered. The literal attached session identifier is replaced with a synthetic binding for execution.
- **Kafka:** a synthetic AsyncAPI order contract converts to publish/consume actions. A real Kafka 4.2.0 broker returns the exact unique order marker, numeric amount, currency and item array, with an actual offset. Each run creates and deletes its own topic.
- **Solace:** a JNDI queue definition converts to a draft, which is configured for a real Solace 10.26.0.8827 AMQP broker. The replay engine publishes and consumes the exact unique JSON payload using synthetic credentials and a dedicated queue. This is queue round-trip evidence, not certification of all selector, transaction, topic or HA behavior.
- **Mock messaging:** AsyncAPI exercises Kafka JSON/Avro/request-reply, JMS flows and mixed-provider splitting. MQSC/JNDI topology drafts preserve each destination's own payload. Capture Avro fixtures retain Unicode/binary payload content and application properties after DSL JSON persistence. These are explicitly in-memory results.
- **Enterprise:** persisted CloudTest drafts still fail on `cloudtest_fixture_mapping_unproven`; SAP GUI still has no registered runtime; Java lifecycle execution is rejected when its runner is disabled. Generic CloudTest commit cannot substitute unattested executable actions. Separate Java source tests retain lifecycle and protocol-family behavior for the governed runner.
- **Public corpus:** all 26 source directories at pinned LR_Scripts revision `cc010f01ea6f0120d1e22bc1acb41522bf98d03d` pass conversion smoke checks across 11 source families. Public examples are not customer fixtures, and conversion is not replay equivalence.

<a id="import-reviews-2026-09--defects-and-corrections"></a>
##### Defects and corrections

<a id="import-reviews-2026-09--fixed-solace-amqp-login-identity-changed-when-a-vpn-was-set"></a>
###### Fixed: Solace AMQP login identity changed when a VPN was set

The live test failed with a configured VPN and succeeded with the identical broker, queue and credentials when that field was omitted. The adapter appended `@vpn` to the SASL username. Removed this rewrite; the exact-payload test then passed with `SOLACE_VPN=default`. A local SASL peer regression test checks the transmitted identity for ordinary usernames, names containing `@`, and surrounding whitespace.

This aligns with Solace's documented [per-VPN AMQP listen ports](https://docs.solace.com/Services/AMQP-on-VPN-Management.htm) and [TLS virtual-hostname routing on global ports](https://docs.solace.com/Services/Managing-Services.htm). No broader authentication policy or production broker configuration changed.

<a id="import-reviews-2026-09--fixed-public-corpus-test-demanded-source-families-absent-from-its-pin"></a>
###### Fixed: public corpus test demanded source families absent from its pin

The existing corpus test converted all 26 directories, then failed because it demanded Java/JMS examples that do not exist anywhere in the pinned repository tree. The required corpus families now match that immutable source set. Java coverage remains a separate explicit matrix case; it was not relabeled as public-corpus evidence.

<a id="import-reviews-2026-09--clarified-truclient-is-not-universally-execution-blocked"></a>
###### Clarified: TruClient is not universally execution-blocked

The repository's simple TruClient navigation fixture converts into supported browser actions. Its source-tool branding alone does not imply a durable execution blocker. The matrix therefore records archive inventory/preservation and limits, without claiming complete browser replay parity or weakening any unsupported-feature gates.

<a id="import-reviews-2026-09--remaining-gaps-and-completion-criteria"></a>
##### Remaining gaps and completion criteria

| Blocked case | Needed to complete it |
| --- | --- |
| Service Bus | Dedicated test queue and connection configuration; execute exact-payload queue round trip. Topic, session and request/reply fidelity require additional fixtures. |
| Event Hubs | Dedicated namespace/hub and credentials. Strengthen the current live smoke to identify the just-published unique payload before calling it fidelity evidence. |
| IBM MQ | Supported live client/sidecar, dedicated queue, and baseline behavior for acknowledgment, selectors and transactions. Mock topology replay is insufficient. |
| Customer enterprise replay | Dependency-complete customer CloudTest/TruClient/Java/Oracle exports, source-tool versions, original-tool expected results, approved runner configuration and controlled target applications. |

No new customer fixture paths or cloud-provider configuration were supplied during this run. The three repository-attached SOAP files retain their source provenance; their controlled synthetic responses establish assertion behavior, not the behavior of a customer backend.

<a id="import-reviews-2026-09--validation-and-cleanup"></a>
##### Validation and cleanup

- Full Go suite passed: **105 packages**, **10,812 top-level tests passed**, 4 top-level environment-dependent skips and 11 packages without tests. See validation record (`import-fixture-matrix-2026-09-12/VALIDATION.json`).
- The matrix passed 16 cases without skipped tests inside those passing cases. It separately records four unavailable cases as blocked. Exit 0 never implies those gaps are covered.
- Seven Python harness tests passed, covering empty/missing tests, skips, failures, live prerequisites, bounded timeouts, output capture and manifest references. The pinned Compose configuration validates.
- The first full Go attempt ran out of disk space. About 8 GB of unused Docker build cache older than 24 hours was reclaimed; the full suite was rerun successfully. No source files or application data were removed.
- An initial Node reporter mismatch was corrected by requesting explicit TAP output. Broker setup was corrected to explicitly provision Kafka's test topic and enable internal authentication on the disposable Solace VPN. These harness/setup failures are not product conversion defects.
- Both task-owned broker containers and their anonymous volumes were removed after the final matrix. The local fixture runner, HTTP target and temporary artifact store are cleaned up by their tests. No customer service was used as a successful replay target.
- No dashboard code changed in this follow-up. The earlier UI audit's browser evidence remains separate from these conversion/runtime checks.

<a id="import-reviews-2026-09--reproducing-the-import-fixture-matrix"></a>
#### Reproducing the import fixture matrix

Run from the repository root. The [manifest](../../qa/import-fixture-matrix.json) defines each case's source, assertions, command and prerequisites. The Python runner records hashes and distinguishes failed, skipped/partial and unavailable checks; exit 0 means no executed case failed, **not** that every provider is certified. Inspect `results.json` for blocked rows.

<a id="import-reviews-2026-09--local-prerequisites"></a>
##### Local prerequisites

Use the repository Go toolchain and install the pinned dependencies in `runners/bruno` (`pnpm install --frozen-lockfile`). Node 24.13.0 and Go 1.26.5 were used for this run. Canonical replay starts its own loopback-only runner and SOAP target and uses a temporary encrypted artifact store. Its direct-egress override applies only to those test processes.

Fetch the public source corpus with the repository's pinned fetch script. Choose a new destination; that script replaces a non-Git destination directory.

```sh
scripts/fetch-lr-scripts-corpus.sh /tmp/apex-fixture-public-corpus
export APEXLOAD_LR_SCRIPTS_CORPUS=/tmp/apex-fixture-public-corpus
```

For offline cases only:

```sh
python3 scripts/run-import-fixture-matrix.py --output /tmp/apex-fixture-evidence-offline
```

The output directory must be new. `--only CASE_ID` selects one case and can be repeated. Node/browser-independent prerequisites are evaluated from actual test results: a skipped canonical replay cannot become a passing matrix row.

<a id="import-reviews-2026-09--disposable-live-kafka-and-solace"></a>
##### Disposable live Kafka and Solace

The included [Compose file](../../qa/reviews/import-fixture-matrix-2026-09-12/brokers.compose.yaml) pins the exact images used in this validation: Kafka 4.2.0 and Solace 10.26.0.8827. Its credentials are intentionally synthetic and its exposed ports bind only to loopback. It consumes up to 3 GB of container memory plus build/test resources.

```sh
docker compose -p apex-fixture-matrix -f docs/qa/reviews/import-fixture-matrix-2026-09-12/brokers.compose.yaml up -d
```

Wait for Solace's SEMP endpoint to return 200 and Kafka to report started in the container logs. Configure the disposable VPN, client and empty queue. Run these creation requests once per new broker:

```sh
curl --fail --user admin:fixture-admin --header 'Content-Type: application/json' \
  --request PATCH --data '{"authenticationBasicType":"internal"}' \
  http://127.0.0.1:18090/SEMP/v2/config/msgVpns/default
curl --fail --user admin:fixture-admin --header 'Content-Type: application/json' \
  --data '{"queueName":"apex-fixture-roundtrip","accessType":"exclusive","permission":"consume","ingressEnabled":true,"egressEnabled":true}' \
  http://127.0.0.1:18090/SEMP/v2/config/msgVpns/default/queues
curl --fail --user admin:fixture-admin --header 'Content-Type: application/json' \
  --data '{"clientUsername":"fixture-client","password":"fixture-client","enabled":true,"clientProfileName":"default","aclProfileName":"default"}' \
  http://127.0.0.1:18090/SEMP/v2/config/msgVpns/default/clientUsernames
```

Run the complete matrix. Missing cloud/customer prerequisites remain blocked:

```sh
SOLACE_AMQP_URL=amqp://127.0.0.1:15672 \
SOLACE_QUEUE=apex-fixture-roundtrip \
SOLACE_USERNAME=fixture-client SOLACE_PASSWORD=fixture-client SOLACE_VPN=default \
KAFKA_BROKERS=127.0.0.1:19092 \
python3 scripts/run-import-fixture-matrix.py --include-live --output /tmp/apex-fixture-evidence-live
```

Kafka creates a unique one-partition topic and deletes that topic in cleanup. The Solace queue must be dedicated and empty before the case; a leftover or unrelated message causes an exact-body failure instead of a false pass. Do not point these cases at shared application queues.

After testing, remove only this fixture project and its volumes:

```sh
docker compose -p apex-fixture-matrix -f docs/qa/reviews/import-fixture-matrix-2026-09-12/brokers.compose.yaml down --volumes
```

The official setup references are [Apache Kafka Docker](https://kafka.apache.org/42/getting-started/docker/) and [Solace's first broker setup](https://docs.solace.com/Get-Started/tutorial/event-broker-set-up.htm). Solace AMQP selects the VPN by [per-VPN listen port](https://docs.solace.com/Services/AMQP-on-VPN-Management.htm) or [TLS virtual hostname on a global port](https://docs.solace.com/Services/Managing-Services.htm); it does not require the adapter to append a VPN to the SASL username.

<a id="import-reviews-2026-09--adding-customerprovider-evidence"></a>
##### Adding customer/provider evidence

Record source-tool/export version, sanitized fixture location and SHA-256, original-tool expected behavior, and any permitted local substitutions. Keep provenance explicit: the three attached repository SOAP assets have preserved source bytes but no independently supplied customer execution baseline. Public LR_Scripts assets are public examples, not customer evidence.

- **Service Bus:** configure a dedicated queue and `SERVICEBUS_CONNECTION_STRING` / `SERVICEBUS_QUEUE`. Topic, session and request/reply variants require separate fixture rows.
- **Event Hubs:** configure a dedicated hub and `EVENTHUBS_CONNECTION_STRING` / `EVENTHUBS_EVENT_HUB`. The existing live smoke observes an event; it does not prove that the observed body is the just-published unique marker. Strengthen that assertion before using it as fidelity evidence.
- **IBM MQ:** provide a supported real client/sidecar, queue and expected selector/acknowledgment/transaction behavior. Mock MQSC replay is not IBM MQ runtime parity.
- **Enterprise:** provide dependency-complete customer exports, original-tool expected results, and controlled application/runner targets. CloudTest fixture gates and SAP/Java runtime limits must remain in force until the corresponding evidence exists.

<a id="import-reviews-2026-09--loadrunner-accwlsreg1-missing-producer-investigation"></a>
### LoadRunner acc_wls_reg_1 missing-producer investigation

Date: 2026-09-19. Status: Open: see 'Remaining producer findings'

Re-imported the supplied private archive with the current converter and traced all
12 missing producer names plus the conditional producer back to source on
2026-09-19. Source contents and parameter values are not copied into this report.
No target traffic was sent.

<a id="import-reviews-2026-09--reported-examples"></a>
#### Reported examples

| Finding | Imported consumer | Source evidence |
| --- | --- | --- |
| `c_rewardcard` | `actions[581]`, request starting at `Action.c:7215` | The `c_RewardCard` registration at `Action.c:4222` is commented out. |
| `c_transaction1` | `actions[909]` at `Action.c:14587`; `actions[1042]` at `Action.c:16175` | The registration at `Action.c:14510` is commented out. |
| `productspecificationtype08` | `actions[1062]`, request starting at `Action.c:16436` | Referenced by body templates at `lrw_custom_body.h:200` and `:254`; no producer or parameter definition was found. |
| `tstamp` | First reported consumer `actions[678]`, request starting at `Action.c:11212` | `lr_save_timestamp` calls at `Action.c:8347` and `:11930` are commented out. This unresolved name affects 64 consumer actions. |

The action source locations identify the start of each request call, while the
reference may occur later in a multiline body or in an included body template.
Lowercase diagnostic names reflect converted template names; these cases are
not failures to recognize mixed-case active producers.

<a id="import-reviews-2026-09--remaining-producer-findings"></a>
#### Remaining producer findings

- Commented registrations: `c_idptrace1` (`Action.c:14511`),
  `c_manualpromotions` (`:3270`), `c_productreferenaceid3` (`:3798`),
  `c_productspecificationtype5` (`:4219`), and `c_promotioncatalogid1` (`:3274`).
- `c_productreferenaceid4`: the comment at `Action.c:3800` ends with a backslash.
  C line splicing continues the comment onto the apparent registration at
  `:3801`. The converter correctly excludes that registration.
- `c_offerid` and `c_productspecification01`: references exist, but no matching
  producer or parameter definition was found. Similar names do not establish
  interchangeable meanings.
- `c_offerid1`: an earlier registration at `Action.c:2492` is commented out;
  an active registration at `:3223` occurs after an earlier consumer. This is
  retained as an ordering review finding rather than a definite missing producer.

There are 79 missing-producer occurrences across 12 names, plus one conditional
producer finding. The `.prm` file supplies six parameters, none of which supplies
these names. Repetition reflects affected consumers, not 79 distinct variables.

<a id="import-reviews-2026-09--outcome-and-validation"></a>
#### Outcome and validation

No parsing correction was warranted for these findings, and no warning was
suppressed. The existing guided repair can add or bind parameters and configure
extractors on preceding responses. Restoring a timestamp producer requires an
explicit source or repair decision about its intended format and lifecycle;
the converter does not infer that behavior from the name `Tstamp`.

Both `go test -p 2 ./pkg/convert/enterprisearchive ./pkg/convert/importreview`
packages passed. Existing regression coverage includes comment continuations,
producer ordering, parameter namespaces, and source-parameter lowercase aliases.
The archive retains 492 primary requests, 58 extractors, 25 transaction pairs,
445 subtransaction pairs, and zero false form blockers.

No implementation files were changed for this investigation. Existing working-tree
changes for the separate NEXT31 import work were left intact.

<a id="import-reviews-2026-09--importer-review-consistency-audit"></a>
### Importer review consistency audit

Date: 2026-09-23. Status: Open: audit recommendations only partly addressed (LoadRunner panel and issue detail below); other importers not re-verified

Date: 2026-09-23

<a id="import-reviews-2026-09--scope"></a>
#### Scope

This audit compares the current importer catalog and the non-LoadRunner importer families against the LoadRunner review pattern now in the dashboard. Evidence was captured from the live dashboard on port 4012.

The catalog exposes 16 importers outside LoadRunner. They resolve to five review architectures:

| Architecture | Importers |
| --- | --- |
| Shared protocol asset review | OpenAPI, Postman, WSDL, AsyncAPI, RAML, DevWeb, k6, Akamai CloudTest, Event Hubs Capture, Broker Config, HAR, Recorder Export |
| Shared sample review | Request / Response Sample |
| JMeter review | JMeter |
| Bruno review | Bruno |
| Review-only report | Oracle E-Business / Siebel |

<a id="import-reviews-2026-09--user-goal-and-accessibility-target"></a>
#### User goal and accessibility target

An operator should be able to move among importers without relearning where issues, credentials, data, runtime settings, generated DSL, and source evidence live. The common shell should remain keyboard navigable and expose clear labels, statuses, and recovery actions. Importer-specific fidelity controls must stay available where they are needed.

<a id="import-reviews-2026-09--overall-verdict"></a>
#### Overall verdict

The importers already share a strong six-tab foundation and consistent source-to-review workflow. The largest gap is inside the Review tab: LoadRunner now uses a persistent issue list with specific evidence and in-context repair, while the other families still expose findings through summary cards and detached dialogs. The UI looks related, but the operator workflow is not yet consistent.

<a id="import-reviews-2026-09--strengths"></a>
#### Strengths

- The catalog clearly groups all 17 sources by job and provides searchable, descriptive cards.
- OpenAPI, JMeter, Sample, and most protocol importers reuse the same tab names and order.
- Tab lists expose tab/tabpanel semantics, selected state, and readable count text.
- Severity is expressed in text as well as color in issue dialogs.
- Importer-specific capabilities remain visible: JMeter multi-script/scenario output, Bruno native-versus-canonical execution, HAR comparison and scrubbing, and Oracle review-only posture.
- Source and conversion controls explain that changes require a new review, which protects the operator from silently reviewing stale output.

<a id="import-reviews-2026-09--ux-risks"></a>
#### UX risks

<a id="import-reviews-2026-09--1-issue-resolution-is-inconsistent-and-loses-context"></a>
##### 1. Issue resolution is inconsistent and loses context

JMeter, OpenAPI, and Oracle route the operator from a summary into `IssueDetailsDialog`. The dialog has only a Close action. It does not show the selected issue beside source evidence, offer a repair, or route directly to the relevant Credentials, Data, Runtime, DSL, or Source tab.

Recommendation: use the LoadRunner vertical issue rail and detail pane in every executable importer. Group repeated findings by rule, show occurrence counts and affected assets, and place the most relevant fix or handoff at the top of the detail pane.

<a id="import-reviews-2026-09--2-repeated-diagnostics-are-not-grouped-into-operator-decisions"></a>
##### 2. Repeated diagnostics are not grouped into operator decisions

The JMeter blocker dialog shows three cards with the same generic title, `Blocking Import Diagnostics`, and a diagnostic code. Each says three diagnostics require review, but the operator cannot tell what setting to change or whether one change resolves all three.

Recommendation: title the issue by the actual decision, for example `Set a finite JMeter termination rule`. List all affected thread groups under one issue and provide an inline duration/iteration editor that applies to selected or all groups.

<a id="import-reviews-2026-09--3-navigation-and-issue-summaries-duplicate-the-same-work"></a>
##### 3. Navigation and issue summaries duplicate the same work

Shared reviews show tabs, a `Resolve import requirements` button row, readiness cards, Conversion reference, and Import issue summary. Bruno repeats the same destinations through readiness buttons and tabs. This creates several ways to reach the same place without clarifying which one is primary.

Recommendation: keep the tabs as the stable navigation. Replace the extra button rows with one compact readiness strip that summarizes unresolved counts and moves focus to the corresponding tab. Keep raw conversion diagnostics inside a collapsed Conversion reference rather than repeating them as primary issues.

<a id="import-reviews-2026-09--4-the-action-hierarchy-changes-by-importer"></a>
##### 4. The action hierarchy changes by importer

OpenAPI places editor and save actions at the top of the review. JMeter does the same, but its generated asset inventory immediately becomes the dominant content. Bruno places change-source, readiness actions, API selection, tabs, content, and save guidance in separate blocks. Oracle leaves the entire source form expanded above its review.

Recommendation: standardize the review header as: title/status, primary Edit/Open action, Save action, compact readiness summary, then tabs. Collapse source options after a successful review and expose one `Change source or options` action in the header.

<a id="import-reviews-2026-09--5-tab-labels-and-counts-are-not-fully-consistent"></a>
##### 5. Tab labels and counts are not fully consistent

Bruno uses `Runtime & source settings` where other importers use `Runtime settings`. It also shows `Review 0`, which reads as an empty review even though the review contains important readiness guidance. Oracle exposes six tabs although Credentials, DSL Preview, and Source Mapping contain only review-only explanations.

Recommendation: keep the six canonical labels and order. Counts should represent unresolved actionable items and zero badges should be hidden. Review-only importers may disable or omit inapplicable tabs, as long as the remaining tabs retain the same names and positions.

<a id="import-reviews-2026-09--6-project-scope-fails-late-in-har"></a>
##### 6. Project scope fails late in HAR

HAR accepts a source file and enables `Inspect & review`, then returns `project_id query parameter or leading multipart field is required for multipart imports` in the unassigned workspace. The operator cannot resolve this from the review surface.

Recommendation: treat Unassigned as a valid project scope if supported, or disable Inspect with an inline explanation and a direct workspace selection action before the upload is submitted.

<a id="import-reviews-2026-09--7-the-review-shell-becomes-visually-unstable-at-narrower-widths"></a>
##### 7. The review shell becomes visually unstable at narrower widths

At the captured width, Bruno and Oracle tabs wrap into two rows. The selected tab and related panel content shift vertically between importers, and long labels make scanning harder.

Recommendation: use a single-row horizontally scrollable tab list with visible overflow affordance, or switch to a compact select/list pattern below the breakpoint. Preserve arrow-key behavior and keep the active tab visible.

<a id="import-reviews-2026-09--accessibility-risks"></a>
#### Accessibility risks

- The common tab semantics and textual severity labels are strong foundations.
- Detached issue dialogs interrupt spatial context. A persistent issue workspace would reduce focus transitions and make source/fix relationships easier to understand.
- Wrapped tabs can create a visual order that differs from the expected left/right keyboard sequence.
- Raw diagnostic codes and underscored posture values are exposed as primary copy in several places. Human-readable labels should be primary, with codes available as secondary technical detail.
- The HAR error is technically announced as an alert, but it appears only after a preventable submission and does not identify a usable recovery control.
- Screenshot evidence cannot confirm focus return, screen-reader announcements after repairs, contrast ratios, zoom reflow, or full keyboard completion. Those need browser-based accessibility tests.

<a id="import-reviews-2026-09--importer-specific-guidance"></a>
#### Importer-specific guidance

<a id="import-reviews-2026-09--shared-protocol-and-sample-importers"></a>
##### Shared protocol and sample importers

Adopt the LoadRunner issue workspace inside `ScriptImportReview`. Preserve protocol-specific panels such as auth schemes, selector candidates, Agentforce/Salesforce review, SOAP faults, messaging topology, and support coverage. Convert their diagnostics into specific issue models with a target tab, source location, evidence, and optional repair action.

<a id="import-reviews-2026-09--jmeter"></a>
##### JMeter

Keep multi-plan selection, generated scripts, scenario draft, messaging preview, CSV sources, extractors, and termination semantics. Move blocker/warning/unsupported findings into the vertical issue workspace. Add inline fixes for finite duration/iteration rules, timestamp policy, unsupported sampler disposition, and per-script parameterization. Group repeated issues across thread groups.

<a id="import-reviews-2026-09--bruno"></a>
##### Bruno

Keep native-versus-canonical mode, API selection, destination requirements, encrypted source custody, sandbox profile, environments, and source manifest. Normalize the tab label to `Runtime settings`; show source execution profile inside it. Replace the readiness button grid with a compact summary above the tabs, and use the Review tab for grouped acknowledgements/conversion issues. Do not show a zero Review badge.

<a id="import-reviews-2026-09--oracle-e-business-siebel"></a>
##### Oracle E-Business / Siebel

Keep the review-only posture and package detection. Collapse the source form after review. Use the vertical issue workspace for package posture and correlation candidates. Since the page cannot apply changes, each issue should provide a precise handoff such as `Open LoadRunner importer`, `Open HAR importer`, or `Copy correlation recipe`, with the source evidence beside it. Omit tabs that cannot contain executable content.

<a id="import-reviews-2026-09--har-and-recorder"></a>
##### HAR and Recorder

Keep comparison capture, scrubbing, static-asset filtering, Salesforce/Agentforce detection, selectors, and replay evidence. Validate project scope before conversion. Correlation findings should use the issue workspace and offer inline extractor/parameter creation where the draft supports it.

<a id="import-reviews-2026-09--prioritized-recommendations"></a>
#### Prioritized recommendations

1. Replace `IssueDetailsDialog` as the primary review path with a shared issue workspace in `ScriptImportReview`.
2. Define a common issue contract: specific title, severity, affected assets, evidence, source location, target tab, repair action, and occurrence grouping.
3. Standardize review header actions, tab labels/order, count rules, and source-panel collapse behavior.
4. Remove duplicate readiness navigation and keep raw diagnostics in Conversion reference.
5. Add importer-specific repair adapters for JMeter, Bruno, HAR/Recorder, protocol assets, and Oracle review handoffs.
6. Fix HAR project-scope validation before Inspect is enabled.
7. Add keyboard, focus, scroll-retention, and narrow-width regression coverage across all five review architectures.

<a id="import-reviews-2026-09--evidence-limits"></a>
#### Evidence limits

The live run captured the importer catalog plus representative JMeter, OpenAPI, Bruno, HAR, and Oracle flows. OpenAPI represents the shared protocol-asset review architecture; source inspection confirms that Postman, WSDL, AsyncAPI, RAML, DevWeb, k6, CloudTest, Event Hubs Capture, Broker Config, HAR, and Recorder share that component. HAR review generation was blocked by the current unassigned-project multipart validation error, which is recorded as a finding. No source data was saved and no replay was started.

<a id="import-reviews-2026-09--implementation-status"></a>
#### Implementation status

Implemented in the dashboard after this audit:

- Shared protocol, sample, and JMeter reviews now use a persistent vertical issue queue with grouped occurrences, evidence, filters, keyboard navigation, and direct links to the relevant review tab.
- JMeter runtime settings can apply a finite duration or per-VUser iteration rule to every imported thread group, clearing the matching unbounded-run diagnostic from the active review.
- Duplicate review-navigation buttons were replaced with a compact readiness strip. Informational converter output now lives under `Conversion reference`.
- Review tabs remain on one horizontally scrollable row, hide zero badges, and can omit inapplicable tabs. Bruno now uses the canonical `Runtime settings` label.
- Oracle review results use the shared issue workspace, collapse source inputs after a successful review, and expose only Review, Data & parameters, Runtime settings, and Source Mapping.
- HAR blocks inspection before submission when the workspace has no project scope and explains how to recover.
- Large issue groups page their evidence at 100 occurrences so large imports remain bounded.

Verification included the full 4,469-test dashboard unit suite, production build, live inspection on port 4012, and the affected Playwright workflows under both dev-admin and local-auth profiles.

<a id="import-reviews-2026-09--evidence"></a>
#### Evidence

1. `00-import-catalog.png` — importer discovery catalog.
2. `01-jmeter-review.png` — JMeter shared review shell and generated assets.
3. `02-jmeter-blockers.png` — repeated generic blocker dialog.
4. `03-openapi-review.png` — shared protocol review shell.
5. `04-bruno-review.png` — importer-specific readiness and tab layout.
6. `05-har-project-error.png` — late project-scope failure after source selection.
7. `06-oracle-review.png` — review-only flow with source form retained above tabs.
8. `07-oracle-details.png` — specific findings shown in a detached dialog without repair actions.

<a id="import-reviews-2026-09--loadrunner-issue-detail-review"></a>
### LoadRunner issue-detail review

Date: 2026-09-23. Status: Open: implementation recorded, but dev-admin browser profile still listed unrelated failures at the time and the full profile rerun was pending

<a id="import-reviews-2026-09--resolve-import-issues-focused-design-review"></a>
#### Resolve import issues — focused design review

Reviewed 23 September 2026 on the current-source dashboard at port 4012, using the populated archive already open in the operator’s browser. The tab is currently called **Review**. No application code, imported values, or saved scripts were changed.

Keep the vertical issue queue. The right pane should become a focused resolution workspace: explain the exact difference, show its consequences, offer the relevant controls, and verify the outcome in place.

<a id="import-reviews-2026-09--captured-flow"></a>
##### Captured flow

<a id="import-reviews-2026-09--1-select-runtime-behavior-differences-navigation-good-resolution-unclear"></a>
###### 1. Select runtime behavior differences — navigation good, resolution unclear

Runtime issue (`loadrunner-issue-detail-2026-09-23/01-runtime-detail.jpg`)

The selected state is clear and the queue remains visible. The detail pane instead offers a generic migration paragraph, aggregate counts (34 executed, 5 partial, 1 compatibility-only, 3 non-applicable), and “Script requirement / Source location unavailable.” None identifies the particular settings requiring a decision. “1 affected location” actually represents a grouped script-level diagnostic. The execution-blocker warning also appears for this nonblocking review item.

**Recommendation:** show the affected settings as rows with source value, current replay behavior, consequence, and available action. Keep compatible settings collapsed. Label the scope “Script-wide” when there is no request location. Put exact technical diagnostics in a secondary disclosure.

<a id="import-reviews-2026-09--2-select-converter-warnings-poor-specificity-and-duplicated-information"></a>
###### 2. Select converter warnings — poor specificity and duplicated information

Warning issue (`loadrunner-issue-detail-2026-09-23/02-warning-detail.jpg`)

The title is the internal key `diagnostic:warnings`. The same generic instruction appears in the introduction and Diagnostic evidence. The actual warning is available elsewhere in Conversion reference: line 28 uses `web_reg_find Search=ALL`, while replay checks the main response body only and does not reproduce the source’s full search scope.

**Recommendation:** title the issue “Text check searches a narrower response scope.” Show “Source: all response content / Replay: main response body” and explain the risk of a missed or changed assertion. Display the relevant source line and assertion. Offer a supported assertion edit here, or an explicit source-edit handoff when equivalent behavior is unavailable. Do not imply that acknowledging this limitation restores behavioral parity.

<a id="import-reviews-2026-09--3-follow-open-related-review-destination-works-context-is-lost"></a>
###### 3. Follow Open related review — destination works, context is lost

Source handoff (`loadrunner-issue-detail-2026-09-23/03-source-handoff.jpg`)

The warning’s button opens Source Mapping at `lr_continue_on_error`, the first selected mapping, rather than the warned line 28. It shows all 30 mappings and no warning-specific selection. The operator must rediscover the problem.

**Recommendation:** prefer editing in the selected issue pane. When navigation is needed, label the button by destination and action, such as “Inspect assertion at line 28”; carry the issue, file, line, target path, and current draft through the handoff. Provide “Back to this issue” after the focused edit.

<a id="import-reviews-2026-09--proposed-detail-pane-structure"></a>
##### Proposed detail-pane structure

1. **Issue and impact:** plain-language title, Blocks replay / Needs review, and meaningful scope.
2. **What differs:** concise source-versus-current behavior; one specific explanation of the effect on replay.
3. **Fix this issue:** operation-specific fields using the existing runtime, parameter, credential, or assertion editor components. Lead with a supported recommended option; retain alternatives when the operator must choose.
4. **Check the change:** Preview change → concise affected-field diff, affected occurrences, and remaining requirements → Apply change. Keep protected values masked. Backend validation remains authoritative.
5. **Outcome:** show what changed and what remains; offer “Next unresolved issue” without automatically moving before the operator can read the result. Preserve edits when navigating or request a clear discard decision.

A persistent local action row would keep Preview/Apply available for long issue content. Advanced diagnostics and full source evidence belong below the primary repair controls. Retain separate states for resolved, reviewed with an accepted limitation, and unresolved; a review decision must never silently clear an execution blocker.

<a id="import-reviews-2026-09--inline-repair-coverage"></a>
##### Inline repair coverage

| Issue type | Suggested in-place interaction |
| --- | --- |
| Runtime setting | Source/current values and the existing setting editor; guided TLS migration where required |
| Missing value | Choose an existing producer, add a parameter, or configure extraction from a preceding response |
| Missing credential | Reuse the project-binding picker and existing masked credential controls |
| Missing body | Restore the body through the governed editor; redact previews |
| Unsupported or partial conversion | Show the exact unsupported behavior and supported alternatives; otherwise a focused source/editor handoff |

The current implementation already supports several protected-draft inline operations in `ImportGuidedRepair.tsx`, including parameter creation, producer binding, extraction, restored bodies, proxy secret references, and TLS migration. Reuse those operations rather than creating another repair backend. Diagnostic-prefixed items currently bypass that form; their structured source/setting details must be retained to make them actionable.

<a id="import-reviews-2026-09--priority"></a>
##### Priority

1. Replace summary-only diagnostic items with specific findings, useful titles, actual evidence, and accurate scope.
2. Embed existing editors and repair operations in the detail pane with preview/apply/refresh.
3. Fix focused handoffs and add explicit recovery for unavailable/expired protected drafts.
4. Improve queue progression, action placement, and keyboard behavior.

<a id="import-reviews-2026-09--accessibility-and-verification-limits"></a>
##### Accessibility and verification limits

Selection is conveyed visually and in the accessibility tree. Selecting an issue moves focus to its detail heading; preserve a clear keyboard route back to the queue and announce repair results. The queue uses pressed buttons, not ARIA tabs: keep its semantics consistent and test arrow-key behavior if tab-like navigation is introduced. Muted supporting text in Solarized Dark should receive a measured contrast check; screenshots alone cannot establish compliance.

The current draft reports that protected import storage is unavailable, so a live repair preview/apply could not be completed in this audit. The existing operations above were confirmed by code inspection, not by a successful live repair in this run. Do not promise that a layout change alone enables them. Show a concise “Repairs unavailable for this draft” state with an actionable re-import/admin route. The in-app capture was cropped, so accepted screenshots were captured from the existing Chrome tab on the same current-source server. No stale screenshots or earlier test traces were used as audit evidence.

<a id="import-reviews-2026-09--resolve-import-issues-implementation"></a>
#### Resolve import issues — implementation

Implemented against the current workspace dashboard, served from Vite at http://127.0.0.1:4012/. The adjacent review.md and JPGs document the pre-change baseline.

<a id="import-reviews-2026-09--changes"></a>
##### Changes

- Removed the redundant Original import diagnostics section from LoadRunner review; detailed warnings and blockers remain in the issue workspace and conversion reference. Other importers retain their diagnostics.
- Kept the vertical issue queue; added descriptive titles, replay impact, accurate script-wide/location scope, source-versus-replay comparisons, and secondary technical evidence.
- Preserved individual converter warnings instead of replacing them with a generic summary. Deduplicated repeated warnings and consolidated nonblocking runtime coverage into the runtime issue; independent blockers remain visible. Warning identities remain stable when their order changes.
- Embedded the existing runtime editor with source/current/consequence rows and a local change preview before server-validated apply. Existing settings state and API remain shared with the Runtime tab.
- Moved the same credential editor into a selected credential issue using a stable portal host; switching placement preserves pending input and does not create another editor instance.
- Kept governed body, producer, parameter, extractor, proxy-secret, and TLS operations. Added a field-path-only repair summary and persistent preview/apply action row. Protected values are not included in the new summary.
- Added session-only “Reviewed with limitation,” explicit next-issue progression, retained repair drafts, keyboard queue navigation, and Escape-to-queue keyboard support. Removed the unused Back to issue list button and moved runtime edit/preview/apply controls above the table, as requested. Review notes do not clear execution requirements or claim source parity.
- Exact source handoffs retain the mapping identity and include “Back to this issue.” Ambiguous source locations are not assigned an arbitrary file. Fixed StrictMode mount selection overwriting the requested mapping.
- Protected-draft recovery appears when relevant; unsupported source behavior remains explicit. No new assertion-repair backend was invented for Search=ALL semantics.

<a id="import-reviews-2026-09--verification"></a>
##### Verification

- Final typecheck, targeted ESLint, embedded Vite build, and git diff whitespace checks passed.
- Final affected component suite: **133 tests passed with the standard timeout**, covering issue repair/navigation, runtime editing, credential placement, warning grouping, ScriptImportReview, and SecretBindingReview. EnterpriseArchiveImport: **85 tests passed**, including removal of the duplicate diagnostics and retention of warnings from older responses. SourceMappingReview's StrictMode handoff regression also passed.
- Current-source LoadRunner browser suite: **15 passed, 1 optional private-fixture test skipped**. The final edit-control positioning/removal flow was rerun and passed at desktop and narrow widths.
- The required `npm run test:cycle` was invoked. The broad unit sweep overlapped the subsequent requested UI adjustments and reported 4,461 passed / 4 failed. Two were superseded layout tests and two were large-input timeouts; all affected tests passed in the final standard-timeout rerun. The full unit sweep was not rerun after that final adjustment.
- Full dev-admin browser profile: **131 passed, 6 failed, 4 skipped**. All runnable LoadRunner tests passed. Remaining failures: workspace hierarchy; project-scope/history navigation; JMeter shared review evidence; JMeter messaging preview; Source Mapping resize/restore timeout; guided workspace setup. Other tasks were also updating this shared workspace during validation.
- Full local-auth browser profile: all 15 runnable LoadRunner tests passed; one optional private-fixture test skipped. Remaining wider workflow checks are still running.

Current screenshots: warning (`loadrunner-issue-detail-2026-09-23/issue-warning.png`), runtime (`loadrunner-issue-detail-2026-09-23/issue-runtime.png`), narrow layout (`loadrunner-issue-detail-2026-09-23/issue-narrow.png`).

Runtime previews are local comparisons; backend validation occurs on apply. Review decisions are session notes, not persisted approvals. Unsupported source behavior is not declared repaired by acknowledging it.

<a id="import-reviews-2026-09--loadrunner-panel-design-audit"></a>
### LoadRunner panel design audit

Date: 2026-09-23. Status: Open: implementation recorded; local-auth profile left two unrelated failures at the time

<a id="import-reviews-2026-09--loadrunner-import-review-design-consistency-audit"></a>
#### LoadRunner import review — design consistency audit

> Historical baseline from the older Docker dashboard. The recommendations have since been implemented against current working-tree source. See [implementation and current verification](#import-reviews-2026-09--loadrunner-panel-design-audit); these original screenshots are not the current dashboard.

Reviewed 23 September 2026 against the populated local Docker application at port 8080, using the existing `acc_wls_reg_1.zip` review. Theme: Solarized Dark. Capture size: 2048 × 1163. All screenshots were captured and inspected during this audit.

**Verdict: the outer panel is consistent; the six tab interiors are not yet a coherent workspace.** The shared palette, border treatment, top-level tabs, and persistent save row are sound. Most friction comes from inconsistent content hierarchy, unexplained count scopes, and several overlapping ways to make or review changes.

This is an evaluation and implementation recommendation. No application code, draft values, credentials, or saved scripts were changed.

<a id="import-reviews-2026-09--highest-impact-findings"></a>
##### Highest-impact findings

<a id="import-reviews-2026-09--1-high-tab-badges-do-not-lead-to-the-issues-they-advertise"></a>
###### 1. High — tab badges do not lead to the issues they advertise

Evidence: steps 1–4.

- Data & parameters has a **13** badge, but opens a table of **6** imported parameters with **Needs attention (0)**. The 13 missing-value/correlation issues are in Review. The return link appears after the entire parameter table, so the mismatch is not resolved at the point of entry.
- Credentials shows **2** on its tab, **4 unresolved credential requirements**, **12 credential groups**, and **1 binding needing setup**. These may all be correct at different levels of aggregation; the interface makes users reconcile them.
- Runtime settings shows **1** on the tab, **6 Requires decision** overall, and **5** in the current HTTP category. These represent different scopes, without an equally prominent explanation of each scope.

**Recommendation:** define tab badges consistently as unresolved grouped replay issues. Put an actionable sentence at the top of each relevant tab: “13 replay issues · Review missing values and correlations,” or “2 replay issues · 1 project binding needs setup.” Keep inventory totals in their tables. Label runtime totals “All categories” and the section results “HTTP — 5 settings.” A badge must either open the matching filtered content or have a conspicuous link to it. Do not force different data models to have identical counts.

<a id="import-reviews-2026-09--2-high-navigation-and-repair-ownership-overlap"></a>
###### 2. High — navigation and repair ownership overlap

Evidence: Credentials, Runtime, and the Source Mapping → Review handoff in step 6.

Credentials has both **Back to issue queue** and **View remaining execution requirements**, in addition to the Review tab. Source inspection correctly links to a specific TLS issue, but Review then offers a TLS repair while Runtime settings exposes minimum/maximum TLS configuration. A user cannot tell from the presentation whether these paths have identical effects on migration acknowledgment and readiness.

**Recommendation:** keep Review as the issue index and use a single repair component/state contract for each issue type. Both Review and the relevant domain tab may open that component, but should share preview, apply, and readiness refresh behavior. Retain source-to-issue deep links. Replace the two generic Credentials return actions with one consistently positioned “Review issues” link, or a contextual “Back to selected issue” link when arriving from an issue. Preserve migration acknowledgment and protected-draft validation; a raw setting change must not silently stand in for those operations.

The audit establishes overlapping user-facing controls, not that their backend effects are identical.

<a id="import-reviews-2026-09--3-medium-headers-spacing-and-usable-width-change-by-tab"></a>
###### 3. Medium — headers, spacing, and usable width change by tab

Evidence: all six primary screenshots.

Review uses a large nested card with a sentence-case heading. Credentials adds a separate introduction card followed by an uppercase section heading. Data and Runtime use larger sentence-case headings. DSL Preview and Source Mapping use small uppercase headings; DSL's heading and download explanation are flush against the panel edge while nearby controls are inset. “Back to issue queue” is above most tabs' content but below Data's inventory.

Runtime's content is capped at 80rem, leaving a substantial unused right-hand area at this viewport. Data, Credentials, and the code comparison use the available width. The Runtime cap can be a valid readability choice, but currently reads as an unrelated layout rule.

**Recommendation:** give every tab one shared header with a title, one-line purpose, optional contextual return link, and local actions. Use one content inset (the existing 20px pattern is a suitable starting point), one heading scale, and one toolbar alignment. Keep prose at a readable width while allowing tables and code to fill the workspace. Standardize sentence case, including “DSL preview” and “Source mapping.” Keep distinct layouts where the task warrants them: a comparison editor should not be forced into a credential table.

<a id="import-reviews-2026-09--4-medium-filters-look-and-behave-like-unrelated-components"></a>
###### 4. Medium — filters look and behave like unrelated components

Evidence: steps 1–4 and 6.

Review has a very wide search with a right-aligned Show dropdown. Credentials has a short search, an inline state dropdown, and a separate review checkbox. Data has compact type/source filters; Runtime has rectangular checkbox status filters above a small search and category rail. Source Mapping uses pill-shaped status controls and very wide file/search fields. Static credential summary pills resemble interactive mapping filters.

**Recommendation:** share a toolbar shell: labeled search, scoped filters, result count, then secondary actions. Use one appearance for multi-select status filters and another for static totals. Make local differences explicit (“Search parameters,” “Search mappings”) without changing control height, label style, and spacing. Avoid a short “Name or source column” field that visibly clips its placeholder at this desktop size.

<a id="import-reviews-2026-09--5-medium-data-contains-three-change-workflows-with-different-action-rules"></a>
###### 5. Medium — Data contains three change workflows with different action rules

Evidence: step 3 and its supplementary screenshots.

The parameter inventory has row editing; Optional substitutions uses “Preview substitutions”; Parameterization review uses “Apply Selected (0).” Their headings, typography, and action positions vary. Optional substitutions also repeats “Used in 478 requests” in both the summary and the helper sentence. Allocation repeats selection/update and exhaustion information already present in Parameters, then pushes its distinctive preview further down the page.

**Recommendation:** distinguish the jobs explicitly: **Parameters**, **Data sources**, **Allocation preview**, and an expandable **Suggested changes** area. Within suggestions, label environment substitution separately from parameter creation. Share the selection → preview → apply interaction and result feedback, while retaining each operation's semantics. Make Allocation primarily a simulation view with a compact rule summary and links that open the relevant parameter editor. Keep the read-only source inventory; it answers a different question from parameter editing.

<a id="import-reviews-2026-09--6-medium-code-inspection-repeats-useful-capability-with-inconsistent-controls"></a>
###### 6. Medium — code inspection repeats useful capability with inconsistent controls

Evidence: steps 5–6.

DSL Preview offers section/action navigation, a visible word-wrap control, and “Focus preview.” Source Mapping offers a generated-DSL pane, “Focus destination,” and word wrap hidden under “View options & export.” It also supports an original/current destination choice. These are related inspection capabilities, but their controls and wording diverge. Several rows of guidance and controls precede the code; in the Source Mapping overview the actual comparison begins near the bottom of the viewport.

**Recommendation:** retain both tabs for now. DSL Preview is the whole-draft inspection view; Source Mapping is the provenance comparison. Share code-pane controls and wording: “Word wrap,” “Expand pane,” “Copy,” with clearly scoped export actions. Move the preview-limits explanation into one compact notice with expandable details. Keep “Download protected review JSON” distinct from “Export filtered mappings”: these are different artifacts, not duplicate exports.

<a id="import-reviews-2026-09--7-medium-visual-and-accessible-status-communication-need-alignment"></a>
###### 7. Medium — visual and accessible status communication need alignment

Evidence: the visible badge counts in all screenshots and the accessibility tree during tab navigation; source confirmation below.

The top-level tabs expose their names and selected state, but their numeric badges are excluded from accessible names. Runtime status checkboxes similarly expose their status name without the adjacent count. The information is visible elsewhere, but users navigating controls do not receive the same immediate summary. Small, muted labels and outlined actions also appear faint in Solarized Dark; no contrast-ratio conclusion is claimed without measurement.

**Recommendation:** expose counts with explicit meaning, for example “Data & parameters, 13 unresolved issues” and “Requires decision, 6 settings across all categories.” Preserve roving tab focus and active-state indicators. Measure text and control contrast in each supported theme and use the established semantic text tokens. Single-selection Data views should use the shared tab pattern rather than looking like independent toggle buttons.

<a id="import-reviews-2026-09--tab-by-tab-evidence"></a>
##### Tab-by-tab evidence

<a id="import-reviews-2026-09--1-review-usable-needs-clearer-hierarchy"></a>
###### 1. Review — usable, needs clearer hierarchy

Strengths: grouped issue queue, affected-location counts, a visible next-issue action, and a useful selected-issue pane. The global and local issue totals repeat. Long review content scrolls the tab strip away. The captured initial state leaves a large detail pane empty until selection; the linked TLS issue shows that the detail pane becomes useful once a specific task is chosen.

Review tab (`loadrunner-panel-design-2026-09-23/01-review.jpg`)

The source-to-issue link selected the intended TLS repair. It did not apply a repair.

Linked TLS repair in Review (`loadrunner-panel-design-2026-09-23/01b-linked-issue.jpg`)

<a id="import-reviews-2026-09--2-credentials-needs-consolidation"></a>
###### 2. Credentials — needs consolidation

Strengths: secret values remain hidden, affected locations are grouped, and destination/current state are separated. The multiple count scopes, duplicate return links, and two introductory/header layers make the one binding needing setup harder to distinguish from preserved values.

Credentials tab (`loadrunner-panel-design-2026-09-23/02-credentials.jpg`)

<a id="import-reviews-2026-09--3-data-parameters-greatest-information-architecture-friction"></a>
###### 3. Data & parameters — greatest information-architecture friction

Strengths: a useful compact parameter table, explicit sample-value caveat, source evidence, and distinct source/allocation views. The tab badge and “Needs attention” filter describe different populations. Optional workflows below the table follow older card patterns and separate apply conventions.

Data and parameters tab (`loadrunner-panel-design-2026-09-23/03-data-parameters.jpg`)

The read-only Data sources inventory is worth keeping. It reports source files, row availability, columns, and consuming parameters.

Data sources subview (`loadrunner-panel-design-2026-09-23/03b-data-sources.jpg`)

Allocation repeats much of the parameter table before presenting its distinctive simulation tool. Prioritize the preview and make rule edits directly reachable.

Allocation subview (`loadrunner-panel-design-2026-09-23/03c-allocation.jpg`)

The return-to-queue link appears between the inventory and optional substitutions, rather than in the shared tab header.

Optional substitutions position (`loadrunner-panel-design-2026-09-23/03d-optional-substitutions.jpg`)

The two optional change sections use different confirmation patterns and action placements.

Substitution and parameterization workflows (`loadrunner-panel-design-2026-09-23/03e-parameterization.jpg`)

<a id="import-reviews-2026-09--4-runtime-settings-strong-comparison-model-inconsistent-framing"></a>
###### 4. Runtime settings — strong comparison model, inconsistent framing

Strengths: source/current comparison, per-row evidence, category navigation, and separation of mapped versus preserved behavior. Counts need their scope labeled. The narrow capped workspace, raw source category names such as FILTERS/ModemSpeed, and adjacent Log/Logging categories interrupt the product's terminology.

Normalize display categories such as Log/Logging where their meaning warrants it, retaining original source-section names in evidence. Do not discard source provenance while simplifying navigation.

Runtime settings tab (`loadrunner-panel-design-2026-09-23/04-runtime.jpg`)

<a id="import-reviews-2026-09--5-dsl-preview-useful-capability-needs-layout-cleanup"></a>
###### 5. DSL Preview — useful capability, needs layout cleanup

Strengths: read-only labeling, action search, section selection, word wrap, and explicit truncation disclosure. The heading scale, edge alignment, and download explanation differ from the neighboring tabs. A shared code-inspection header and compact notice would give more room to the JSON.

DSL Preview tab (`loadrunner-panel-design-2026-09-23/05-dsl-preview.jpg`)

<a id="import-reviews-2026-09--6-source-mapping-valuable-specialist-view-too-much-before-the-code"></a>
###### 6. Source Mapping — valuable specialist view, too much before the code

Strengths: source/destination comparison, coverage filters, explicit source-preview limitations, original/current destination choices, and a working link to the related repair. Mapping-review counts describe provenance review and should remain distinct from replay issue counts. Retain that distinction in labels rather than merging the two systems.

Source Mapping tab (`loadrunner-panel-design-2026-09-23/06-source-mapping.jpg`)

Expanded display controls and the actual comparison show the useful core experience. Consolidate the surrounding controls, and keep the common tab navigation available during long inspection sessions.

Source Mapping comparison and controls (`loadrunner-panel-design-2026-09-23/06b-source-comparison.jpg`)

<a id="import-reviews-2026-09--duplicate-functionality-decisions"></a>
##### Duplicate-functionality decisions

| Area | Assessment | Recommended treatment |
| --- | --- | --- |
| Review tab + Back to issue queue + View remaining execution requirements | Confirmed redundant generic navigation; both buttons call the same overview selection | Keep one contextual return link in a shared header |
| Review TLS repair + Runtime TLS configuration | Overlapping user task; backend equivalence not established | Share a canonical repair flow and readiness refresh; preserve explicit migration acknowledgment |
| Parameters versus Allocation rule table | Confirmed repeated presentation; allocation preview is distinct | Keep preview, compact the repeated rules, deep-link to edits |
| Parameter editing / optional substitutions / parameterization suggestions | Adjacent operations with inconsistent workflow; not exact functional duplicates | Separate purposes, share preview/apply conventions and feedback |
| DSL Preview versus generated DSL in Source Mapping | Intentional overlap serving different inspection tasks | Retain both; share code controls and selected-action navigation where appropriate |
| Protected JSON download versus mapping export | Different output contracts | Retain both and explicitly label scope and protection |
| Current issue queue versus original conversion diagnostics | Current actionable state versus historical evidence | Keep original diagnostics collapsed and clearly marked as original/static |

<a id="import-reviews-2026-09--implementation-starting-points"></a>
##### Implementation starting points

- `dashboard/src/components/EnterpriseArchiveImport.tsx:4402`: six-tab assembly and issue badges. At 4453 and 4527, the two generic return actions both select the overview. The shared return link is rendered after the parameter inventory because the latter is injected earlier by ScriptImportReview.
- `dashboard/src/components/ScriptImportReview.tsx:932`: shared status/save header. At 1002 the non-tabbed parameter inventory precedes children; at 1106 the data extras mount separate substitution and parameterization components.
- `dashboard/src/components/ScriptImportReview.css:73`: legacy uppercase 13px section headings and credential framing.
- `dashboard/src/components/ImportSettingsTables.css:33`: newer 1rem workspace padding. At 44 Runtime alone gets an 80rem maximum width; at 40 the settings toolbar defines another control pattern.
- `dashboard/src/components/ImportParameterInventory.tsx:62`: the Data subviews and their shared table content. Keep Parameters as the editing owner while promoting Allocation preview's distinctive value.
- `dashboard/src/components/ImportRuntimeSettingsTable.tsx:53`: runtime heading, global status totals, search, category rail, and result groups.
- `dashboard/src/components/ImportGuidedRepair.tsx:179`: guided TLS selection and repair preview.

Suggested implementation order: fix issue scope and navigation first; extract shared tab header/toolbar and code-pane controls second; then consolidate repair entry points and simplify Data's optional workflows. Add workflow coverage for badge-to-issue navigation, shared counts, pending edits, source-to-repair links, and keyboard names. Run the repository's dashboard test cycle and verify the result visually after implementation.

<a id="import-reviews-2026-09--evidence-limits"></a>
##### Evidence limits

The audit covers one populated HTTP archive, six tabs, three Data subviews, optional data sections, the Source Mapping display options, and its TLS issue handoff. Right-arrow keyboard navigation from Data & parameters to Runtime settings was verified. The browser was returned to Runtime settings, the originally selected tab.

No saves, applies, revalidations, downloads, credential edits, or replay runs were performed. Save/batch-save equivalence and backend repair equivalence were not tested. Empty/error states, alternate protocols, multi-archive states, other themes, narrow viewports, screen-reader operation, and measured color contrast remain unverified. No application tests were run because this pass only created audit evidence and recommendations. Repository source was used to corroborate visible patterns; the running Docker build was not rebuilt or asserted to match the checkout byte for byte.

<a id="import-reviews-2026-09--loadrunner-import-review-implementation-and-verification"></a>
#### LoadRunner import review — implementation and verification

Updated 23 September 2026. The original audit used the older Docker application on port 8080. Implementation verification uses the current working tree through Vite at [port 4012](http://127.0.0.1:4012/#/scripts), backed by a newly compiled controller and a disposable database. The embedded dashboard was also rebuilt. Existing Docker instances are separate deployments and do not receive source changes automatically.

<a id="import-reviews-2026-09--implemented"></a>
##### Implemented

- Shared section headings, purpose descriptions, 20px content insets, search/filter controls, and full-width workspaces across the six tabs. Tables and source comparisons retain layouts appropriate to their tasks.
- Consistent tab badges for unresolved grouped replay issues, accessible count descriptions, and a single contextual link into the matching Review category. Inventory, definition-validation, and runtime-mapping totals now explain their separate scopes.
- One guided TLS migration path while migration is unresolved; raw TLS controls cannot bypass the migration review. Source-to-issue navigation remains available.
- Collapsed optional suggestions with explicit preview/apply steps. Parameter previews mask protected values and are invalidated when the selection or draft changes.
- Allocation rules collapsed into a reference disclosure, with links into the existing parameter editor. Data sub-tabs support keyboard navigation.
- Shared word-wrap controls and consistent Expand/Restore labels across DSL and source inspection. Source preview limits are compact and distinguish display limits from conversion issues. Source-pane actions wrap rather than hiding controls behind horizontal scrolling.
- Sticky review tabs, responsive header behavior, opaque themed tab backgrounds, and corrected category scroll height for short windows.

<a id="import-reviews-2026-09--code-review-fixes"></a>
##### Code review fixes

A late repair-preview response could restore a preview after the user switched issues or categories. Preview generations now invalidate that response. Resolved-only queues disable “Resolve next issue.” Regression tests cover both navigation handoffs and preview invalidation. Browser checks also caught and corrected short-window category focus positioning and obsolete assertions for removed duplicate controls.

<a id="import-reviews-2026-09--validation"></a>
##### Validation

Manual review covered all six tabs, narrow layouts, and Dark, Light, Solarized Dark, Solarized Light, and Monokai themes. Sanitized archive fixtures were used; private acceptance archives are not available in this checkout.

- Full unit run: **419 files / 4,453 tests passed**. After the additional navigation-race regression, the affected component suite passed **150 tests**.
- Typechecking, ESLint, and the production embedded build passed. A final CSS-only bundle rebuild also passed; the existing large lazy-chunk warning remains.
- Fresh embedded-build local-auth import and source-editor workflows: **17 passed, 1 skipped**. This includes the final source-action wrapping assertion.
- Current-source dev-admin import and source-editor workflows: **18 passed, 1 skipped**. A further source-action wrapping regression passed after the last CSS adjustment.
- `test:cycle` was attempted, but concurrent changes elsewhere in the working tree caused typechecking failures on those attempts. Its stages were subsequently run separately, including the full dev-admin and local-auth workflow profiles. This is not a claim of a green repository-wide cycle.

Useful distinctions are retained: copying the selected mapping differs from copying the complete file; viewing data-source evidence differs from editing parameter definitions. These are labeled separately rather than removed as duplicates.

<a id="import-reviews-2026-09--repository-wide-workflow-findings"></a>
###### Repository-wide workflow findings

The full dev-admin profile completed with 124 passed, 11 failed, and 4 skipped. Two failures were in the import work (the moved readiness assertion and short-window category positioning); both were fixed and passed in the final targeted run. The other failures were in Command Center details, dev-admin landing, live metrics, run lifecycle/abort, workspace hierarchy, authoring scope/history, JMeter execution/CSV export, and guided workspace setup. Those touch concurrent work outside this change and were not silently altered to make this run green.

The full local-auth profile completed with **53 passed, 2 failed, 1 skipped**. Its remaining failures were the JMeter execution/CSV-export workflow and the sign-in/workspace/launch/stop workflow. All LoadRunner and source-sidebar tests in that profile passed. The final focused local-auth rerun additionally verified the last source-toolbar change.

<a id="import-reviews-2026-09--current-screenshot-evidence"></a>
##### Current screenshot evidence

These captures come from the final current-source browser runs, not the port-8080 Docker baseline. They use sanitized fixtures and represent particular review/empty states rather than every possible imported archive.

| Tab | Current evidence |
| --- | --- |
| Review | Issue workspace (`loadrunner-panel-design-2026-09-23/current/review.png`) |
| Credentials | Populated credential group (`loadrunner-panel-design-2026-09-23/current/credentials.png`) |
| Data & parameters | Desktop (`loadrunner-panel-design-2026-09-23/current/data.png`), narrow populated editor (`loadrunner-panel-design-2026-09-23/current/data-narrow.png`) |
| Runtime settings | Populated runtime settings (`loadrunner-panel-design-2026-09-23/current/runtime.png`) |
| DSL Preview | DSL controls and preview (`loadrunner-panel-design-2026-09-23/current/dsl.png`) |
| Source Mapping | Comparison with wrapping actions (`loadrunner-panel-design-2026-09-23/current/source.png`) |

<a id="import-reviews-2026-09--source-identity"></a>
##### Source identity

The source identity below records the local implementation; it does not imply that another running deployment has been refreshed.

Base HEAD: `92889f6c28f9c17bb386d111ae1dfffa3885b83d`.

SHA-256 at verification:

```text
38a574d599775d48e3a17ee78362e20f4e0d45f929c56100025da8c9d74db229  EnterpriseArchiveImport.tsx
21ab2a762d2b2569e0335b78511fb9e14f0b48edb5cc0ae4450d286dd6c4b7d9  EnterpriseArchiveImport.css
c1e9042f652e01003a1f2d21ee6e0d2a02710a5a0384c06f0ac37b0757d159d2  ImportGuidedRepair.tsx
18feb1cc9ec23abce6c149ac05372ea65ea60a3d3e8a2ec1e64b887f4fe5191a  ImportReviewWorkspace.css
bfaf0cf12fec9fe248b9f04202f6c0cb3d8619fd904be28eb0882072860fe799  ImportReviewSectionHeader.tsx
b70e846347463517a02428526503dc719ceb74d2b89f96b89dc95f4262a1849e  ParameterizationReview.tsx
70b223aa092a41b6f86fd02dbfdc2b8e7b98fc0e62ce50f51ca06c925bce13c6  SourceMappingReview.tsx
```

<a id="import-reviews-2026-09--jmeter-import-fidelity-review-code-review-and-recheck"></a>
### JMeter import fidelity review, code review and recheck

Date: 2026-09-23 / 2026-09-24. Status: Open: see 'Remaining source requirements' and 'Remaining product issues' in the recheck

<a id="import-reviews-2026-09--jmeter-import-fidelity-verification-2026-09-23"></a>
#### JMeter import fidelity verification — 2026-09-23

Source attachment SHA-256 remains `b92245ad1443767a6d9e20c87bc6988b04cff804368b5dcafca180e1be9b0cb0`. The attachment was not modified. Sanitized original and repaired fixtures were committed in `86b15ac1`.

The implementation and finding-to-test matrix are documented in [JMeter import fidelity](../../IMPORT_FIDELITY_AND_REPAIRS.md#jmeter-import-fidelity). The subsequent two-pass review and fixes are recorded in [Code review](#import-reviews-2026-09--jmeter-import-fidelity-review-code-review-and-recheck).

<a id="import-reviews-2026-09--verified-behavior"></a>
##### Verified behavior

- The original fixture retains its HTTPS endpoint, 50 users, one-second ramp, and 200 iterations per user. Missing credential bindings, masked source values, and unresolved timezone semantics remain actionable blockers.
- UTC repair generates one timestamp with milliseconds. Source-timezone repair preserves the source wall-clock fields and records the literal-suffix decision. Unsupported date expressions remain blocked.
- HTTP extraction defaults overwrite an earlier trace ID on a miss; malformed expressions/responses and resource-limit errors fail instead of falling back.
- Local and generator-backed loopback tests each execute exactly 10,000 samples for 50 × 200 normal completion. A separate race-enabled test sends protobuf assignments and commits to two independent generator services over gRPC, with 25 users per shard, and verifies exactly 10,000 requests. Pause/resume, cancellation, failed samples, multiple groups, and older-generator rejection have focused regressions.
- Native CSV generation uses finalized full archives, excludes synthetic transaction duplicates, retains failed samples, uses first-byte latency, and rejects incomplete archives or failed writes. Distributed merge, redaction, concurrency, escaping, and extraction-retention limits are covered.
- The actual browser flow passes under both dev-admin and local-auth: upload, inspect source mappings and blockers, regenerate explicit repairs, save a two-user/three-iteration scenario, run six loopback samples, and download `traceId-results.csv` through the results UI. The file has one header and six sample rows.

<a id="import-reviews-2026-09--checks"></a>
##### Checks

- Focused engine, HTTP, generator, and API archive race tests: passed.
- SQLite and PostgreSQL termination migration tests: passed against a disposable PostgreSQL 16 database; the container was removed afterward.
- Import contract registry and golden corpus checks: passed.
- Dashboard unit suite: 422 files / 4,465 tests passed.
- Focused JMeter browser workflows: two tests passed in each authentication profile.
- Full Go suite, including generated contract freshness: passed (`go test -timeout 30m -p 2 ./...`).
- The full dashboard cycle passed type checking, lint, all unit tests, and the embedded build. Its initial dev-admin profile ran all 141 cases: 125 passed, 4 fixture/profile-dependent cases skipped, and 12 failures. Nine stale navigation/label expectations were corrected; all 37 targeted dev-admin rechecks passed in an isolated checkout. The remaining three LoadRunner review cases also passed against the latest isolated build. Every initially failing case has a passing rerun.
- The full local-auth profile passed in an isolated checkout: 57 passed and one expected private-fixture case skipped. The isolated checkout avoided concurrent dashboard builds replacing shared embedded assets during controller compilation.

The initial `npm run test:cycle` invocation did not exit successfully. Its failed browser cases were corrected or rebuilt and all passed the isolated reruns above; local-auth was rerun in full. These are recovered validation results, not a claim that the original single invocation was green. No toolchain-related failure is counted as a pass or an accepted skip.

Go commands use `DEVELOPER_DIR=/Library/Developer/CommandLineTools`; dashboard commands use Node 22.22.0. The full Go run uses a 30-minute package timeout and two-package concurrency. Dashboard unit tests use four workers to avoid contention with other concurrent workspace validation. No required test is excluded.

The optional repository documentation checker also reports pre-existing audit-link and documentation-index issues outside this change; the new JMeter guide is indexed.

<a id="import-reviews-2026-09--jmeter-fidelity-follow-up-code-review"></a>
#### JMeter fidelity follow-up code review

Two review-and-fix passes examined conversion, extraction, finite execution, generator capability/admission, archive generation, persistence, and download handling. Review scope was the JMeter fidelity implementation; unrelated concurrent LoadRunner and messaging work was preserved.

<a id="import-reviews-2026-09--pass-1"></a>
##### Pass 1

| Finding | Fix | Regression |
| --- | --- | --- |
| A missing JSONPath ancestor returned a miss before an invalid trailing expression was parsed, allowing `on_miss: default` to hide invalid configuration. | Parse the complete supported expression before evaluating any response fields. | `TestHTTPExtractorDefaultDoesNotSuppressErrors`: missing ancestors, malformed suffixes, unsupported indices, and wrong response types. |
| Java date patterns containing doubled apostrophes lost the literal apostrophe. | Preserve escaped apostrophes both outside and inside quoted literals. | `TestJavaTimeLayoutPreservesEscapedApostrophes`. |
| CSV filenames containing a quote produced malformed Content-Disposition headers; international filenames were not encoded. | Use the standard MIME parameter encoder for artifact downloads. | `TestCSVExportDownloadFilenameEncoding`: quoted, international, ordinary, and uppercase-extension filenames through the API. |

<a id="import-reviews-2026-09--pass-2"></a>
##### Pass 2

| Finding | Fix | Regression |
| --- | --- | --- |
| The native CSV recognizer ignored processor ordering: a writer before extraction, or before a later overwrite, could export a different value than JMeter. | Translate only when the last enabled assignment to the selected extraction precedes the writer; retain a durable processor gate otherwise. | `TestCSVWriterRequiresFinalSourceExtraction`, including disabled later extractors. |
| Converter, DSL, and artifact filename rules disagreed on uppercase extensions and noncanonical names. | Share one filename validator across all three layers; preserve uppercase extensions, and review-gate whitespace, Windows drive-relative names, control characters, and empty stems. | `TestCSVExportDeclarationValidation`, `TestCSVWriterRejectsUnrepresentableFilenames`, and the download filename regression. |
| Redaction could collapse distinct action names or change a selected name, yielding extra or missing CSV rows. | Bind samples to declared export IDs before redaction; retain bindings, including explicitly empty bindings, in local and generator JSONL archives. Legacy records without bindings retain the previous matching path. | `TestCSVExportIdentityPrecedesNameRedaction`, `TestCSVExportIdentitySurvivesRedactedNameCollision`, and `TestCSVExportIdentityGeneratorArchiveRoundTrip`. |
| Sensitive extraction defaults could bypass name-based redaction when the sample lacked a value. | Apply the extraction name’s sensitivity policy to both archived provenance defaults and exported fallback cells. | `TestCSVExportSensitiveFallbackIsRedacted`. |

Generated OpenAPI and TypeScript contracts include the archive export bindings. The original attachment remains unchanged.

<a id="import-reviews-2026-09--validation"></a>
##### Validation

- Focused regressions across JMeter conversion, HTTP extraction, DSL validation, engine, generator, and API: passed.
- Focused race checks covering engine, HTTP extraction, generator execution/archive, and API export paths: passed.
- Dashboard type checking, lint, fresh embedded build, and all 422 unit-test files / 4,465 tests: passed.
- Initial browser cycle: dev-admin had 136 passes, four expected profile/fixture skips, and one scroll assertion failure; local-auth had 56 passes, one private-fixture skip, and the same assertion failure. The geometry test now uses the supported reduced-motion preference, captures scroll at the actual click event after Playwright positioning, and accounts for clamping to a shorter tab. The assertion still rejects an application-induced jump. The complete workflow passed twice in each authentication profile after this correction. The initial single `test:cycle` command was not green; every failed case has a passing final rerun.
- Final-build JMeter browser workflows: two tests passed in dev-admin and two in local-auth.
- Final CSV boundary race tests across converter, DSL, and API: passed.
- Full Go suite: passed (`go test -timeout 30m -p 2 ./...`), including generated contract freshness. The API package completed in 364.603 seconds.

The first full Go attempt exhausted local disk space. Unused compiler scratch directories were removed only after confirming no process held them open; the failed job was stopped and the full suite restarted. This environment failure is not counted as a passing or skipped check.

All seven review findings above are fixed and covered by regressions. No unresolved finding remains from these review passes. The original attachment SHA-256 remains `b92245ad1443767a6d9e20c87bc6988b04cff804368b5dcafca180e1be9b0cb0`.

<a id="import-reviews-2026-09--original-jmeter-import-recheck-2026-09-24"></a>
#### Original JMeter import recheck — 2026-09-24

Reimported the original Downloads attachment through the current API router using a temporary SQLite-backed server. Both import and validation returned HTTP 200 and agreed on preflight, scenario settings, statistics, and review sections. No requests were sent to the source endpoint, and no repairs were saved to the original attachment.

Source SHA-256: `b92245ad1443767a6d9e20c87bc6988b04cff804368b5dcafca180e1be9b0cb0`.

<a id="import-reviews-2026-09--correctly-preserved"></a>
##### Correctly preserved

- Original HTTPS endpoint, including port 443.
- One enabled sampler; 50 users, 1-second ramp, 200 iterations per user; iteration termination with no duration cutoff.
- JSONPath trace extraction with `NOT_FOUND` default and `on_miss: default`.
- Native `traceId-results.csv` archive declaration with ordered `traceId,latencyMs` columns and the source writer's `NOT_FOUND` fallback.
- Disabled CSV source and throughput timer ignored; extractor, assertion, and postprocessor source ranges retained.
- UTC trial produces a single millisecond timestamp parameter. This was a diagnostic trial, not an operator-selected repair.

<a id="import-reviews-2026-09--remaining-source-requirements"></a>
##### Remaining source requirements

1. Explicit timestamp policy: UTC normalization or an acknowledged wall-clock timezone.
2. Explicit name repair: references `client_id`/`client_secret` differ from declarations `cid`/`cs`.
3. Real credential values/bindings: trying the name repairs plus UTC still produces masked-source and missing-secret gates. No placeholder became runnable.

The unmodified import has `preflight.valid: false`; this is correct. Credential storage was not configured in this temporary server, and no credential was provisioned.

<a id="import-reviews-2026-09--remaining-product-issues"></a>
##### Remaining product issues

<a id="import-reviews-2026-09--p2-structured-diagnostic-payload-is-absent"></a>
###### P2 — Structured diagnostic payload is absent

The response contains `diagnostics: null`, although the review sections contain two blocker diagnostics, a credential warning, and informational records. Consumers cannot use the structured diagnostics field to render/filter those findings. The JMeter response writer passes review sections and unsupported strings but does not populate structured diagnostics.

<a id="import-reviews-2026-09--p2-review-accounting-still-mixes-categories"></a>
###### P2 — Review accounting still mixes categories

The original response reports `stats.unsupported: 0`, but `unsupported_elements` contains six strings: the successfully translated CSV writer, two informational listeners, two blockers, and a warning. `stats.review_required: 1` describes a blocked sampler while the review section reports two distinct blocker behaviors. These fields remain inconsistent as behavior-level review evidence, even though execution stays correctly gated. The compatibility report also includes the credential warning among unsupported fragments.

No additional conversion/runtime defect was reproduced in this check. These reporting issues mean the earlier requirement for a single normalized review outcome model is not fully satisfied.

<a id="import-reviews-2026-09--verification"></a>
##### Verification

- Original import, validate, UTC-only trial, and UTC plus name-binding trial: passed through the API router.
- Converter/date-pattern/extractor regressions: passed.
- Local and distributed iteration regressions, including exact 10,000-request completion: passed against loopback services.
- Finalized CSV samples, incomplete archives/write failures, concurrent/distributed merge, filename encoding, redacted identity, and sensitive fallback regressions: passed.
- Import and validation readiness agree.

This was an API and regression-test recheck. It was not a new browser walkthrough or a live execution against the source service.

<a id="import-reviews-2026-09--resolution-normalized-jmeter-review-evidence"></a>
##### Resolution: normalized JMeter review evidence

The two findings above are addressed by one JMeter-specific review projection built from typed findings, source mappings, and durable gate reasons. Structured diagnostics include canonical reason codes and source references. Source mapping IDs keep distinct elements separate even on the same line of minified XML; document paths keep ZIP entries separate.

| Original fixture evidence | Before | After |
| --- | --- | --- |
| Structured diagnostics | `null` | Typed source-qualified diagnostic array |
| Blocking source behaviors | Implicit in mixed strings | `review_summary.blocking_behaviors: 2` |
| Unsupported behaviors | Zero statistic but six mixed strings | Zero behaviors and empty unsupported list |
| Workload | One converted, blocked sampler | Unchanged; separate from behavior counts |
| UTC-only diagnostic trial | Missing-variable gate remains | One blocking behavior; timestamp finding removed |
| UTC + source-name binding trial | Masked values remain blocked | Two credential/source-value behaviors, without duplicate gate evidence |

Review sections, compatibility evidence, and review-gate preflight use the normalized model. Other readiness checks remain intact. New single-script drafts preserve the evidence through attested save/reload. The dashboard uses behavior counts, renders typed findings once, retains source navigation, and falls back to legacy evidence when normalized evidence is absent. Client-side runtime edits and reviewed marks do not clear server gates.

Regression coverage includes original/UTC/binding cases, attested save and reload, ZIP source identity, nested controllers, unsupported and repeated processors, minified same-line samplers, empty diagnostics, normalized UI rendering, and legacy fallback. The browser workflow checks the original fixture and explicit repairs under both authentication profiles.

<a id="import-reviews-2026-09--resolution-verification"></a>
###### Resolution verification

- Full `go test -timeout 30m -p 2 ./...`: passed, including API, converter, import-contract golden, DSL/OpenAPI freshness, and storage tests.
- Dashboard cycle: type checking, lint, 424 unit-test files / 4,474 tests, fresh embedded build, and dev-admin workflows (137 passed, four intentional skips) passed. The first local-auth profile run passed 56 tests with one intentional skip, but its unrelated viewer login/sign-out test timed out; a full-profile retry is recorded below.
- Final focused JMeter browser workflows: three passed under dev-admin and three under local-auth, including source navigation, durable blockers after a reviewed mark, explicit repairs, save, finite execution, and CSV download.
- Final focused dashboard regression run: 63 tests passed. Shared-checkout integration: focused API/converter regressions and dashboard type checking passed after merging alongside concurrent changes.
- Broad validation used an isolated checkout to avoid concurrent source edits; the implementation was then merged into the shared checkout without replacing the other task's work.
- The original attachment's SHA-256 remains `b92245ad1443767a6d9e20c87bc6988b04cff804368b5dcafca180e1be9b0cb0`. Final conversion yields seven structured diagnostics: two blockers, two warnings, three informational records, and no unsupported behaviors.
- Full local-auth retry: **57 passed, one intentional skip**. The viewer login/sign-out test passed without code or timeout changes. Thus both browser profiles passed; the first cycle's transient authentication timeout remains documented above.

<a id="import-reviews-2026-09--follow-up-code-review-loop"></a>
###### Follow-up code review loop

Two review/fix passes found and corrected these normalization defects:

- Empty action paths could compare equal and collapse separate source findings. Matching now requires a nonempty identity and respects qualified source references.
- A durable gate could suppress a separate authored reason on the same action. Independent canonical reasons now survive reconciliation, including coded diagnostics.
- Duplicate evidence merging depended on arrival order and could discard unsupported status, promotion/execution gates, acknowledgement requirements, or stronger severity. Merging retains the strongest readiness evidence and execution gates always emit blocker diagnostics.
- Enriching a finding's display detail could discard its stronger gate/severity. The normalized merger now receives that evidence too.

Regression tests reproduced the first two defects and all four merge-order cases before the fixes. Additional tests cover coded diagnostics and stronger authored findings. Existing original/UTC/credential, nested, disabled, distinct-source, and import-contract regressions remain passing.

Final review-loop verification: full `go test -timeout 30m -p 1 ./...` passed, including contract freshness. The initial two-package run encountered temporary-disk exhaustion during builds; its retry completed without disabling checks. Dashboard type checking and lint passed. The initial dashboard unit run had one unrelated editor-completion failure; it passed in isolation, and the full retry passed 425 files / 4,476 tests with two workers and no code changes to that test. The fresh embedded build passed, followed by both full browser profiles: dev-admin 137 passed / four intentional skips; local-auth 57 passed / one intentional skip. No unresolved finding remains from the two review passes.

<a id="import-reviews-2026-09--jmeter-source-plan-batch-repair-and-code-review"></a>
### JMeter source-plan batch repair and code review

Date: 2026-09-24. Status: Open: see 'Remaining evidence boundary'

<a id="import-reviews-2026-09--source-plan-execution-and-protected-batch-repair-verification"></a>
#### Source-plan execution and protected batch repair verification

This change supplies grouped-source execution and atomic repair publication. It
does not establish strict JMeter client parity or advertise
`jmeter_messaging_v1`.

<a id="import-reviews-2026-09--verified-implementation"></a>
##### Verified implementation

- Versioned, hash-bound manifests stage all groups before one commit epoch;
  complete-population reservations, failed-stage cleanup, immutable membership,
  retry handling and capability negotiation preserve single-generator affinity.
- One `MultiGroupEngine` owns source state and lifecycle. Group sinks and secret
  resolvers remain separate; shared telemetry is emitted once. Owner failures
  release every waiter, and shared resources drain with the owner.
- Encrypted batch parents preserve original source bytes, source identities,
  scripts, scenario and reviewed overlays. Preview digests bind exact results.
  Lease-fenced publication receipts prevent competing repair/save revisions.
- Restart recovery, stale revisions, stripped identities, mixed membership,
  source/workload preservation, independent credentials and concurrent repair
  and save are covered. Payload edits reject trailing JSON and invalid pointers.
- Browser repairs replace the whole batch and refresh review/preflight evidence;
  credentials remain reference-only and strict blockers remain enforced.

<a id="import-reviews-2026-09--checks"></a>
##### Checks

- `go test -p 2 -timeout 30m ./...`: passed (the full API suite took 698 seconds).
- Latest focused API batch/manifest tests: passed after the final payload checks.
- Race tests: engine, generator RPC, import artifact/session packages and protected
  batch API tests passed. A loaded-host iteration-count failure passed isolated
  recheck and a subsequent complete generator race run.
- Kafka and Event Hubs packages with both real-adapter build tags: passed. These
  are adapter tests, not authorized live-service conformance.
- API/protobuf artifacts were regenerated through their repository generators;
  generated-artifact checks passed in the full Go run.
- Dashboard type checking, lint, embedded build and 4,476 unit tests passed.
- Dev-admin browser profile: 137 passed, four expected skips.
- Protected browser checks passed in dev-admin and local-auth modes. The latter
  binds separate Kafka/east/west references, saves/reopens the scenario, rejects
  a stale revision and verifies blocked launch.
- Full local-auth browser profile: 56 passed, one expected skip, one environmental
  failure (`ENOSPC` while writing a dashboard-analysis screenshot). After freeing
  reproducible cache space, that exact test passed on an isolated port/output
  directory. The original `npm run test:cycle` therefore exited nonzero; its sole
  remaining failed test passed the targeted rerun.

Concurrent embedded builds interrupted earlier Go compilations; a stable source
snapshot was used for the protected API race run. Reproducible old Go build-cache
objects were pruned when local disk pressure interrupted another build.

<a id="import-reviews-2026-09--remaining-evidence-boundary"></a>
##### Remaining evidence boundary

Authorized Kafka/OAuth and Event Hubs endpoints were unavailable. No live strict
parity claim is made. PostgreSQL service-level crash/failover testing was not run
against an external database; publication uses the existing protected-store
fence and asset saves use the existing transactional persistence contract.

<a id="import-reviews-2026-09--jmeter-source-plan-and-atomic-batch-review"></a>
#### JMeter source-plan and atomic batch review

<a id="import-reviews-2026-09--findings-fixed"></a>
##### Findings fixed

1. Protected batch child capabilities could be consumed by older single-script
   create/mutation paths, bypassing whole-batch publication. Only the dedicated
   JMeter batch save can now claim those children. Ordinary create rejects batch
   drafts even when their child token is omitted. Legacy single-script repair
   also explicitly rejects a batch candidate.
2. The generator checked source/project compatibility only when a script already
   declared JMeter compatibility. Every manifested script now checks its project,
   and a compatibility-bearing manifest cannot admit an ordinary script.
3. A rejected assignment that omitted its manifest did not immediately abort an
   existing staged source plan. Failed staging now releases that plan's complete
   reservation regardless of the failed request's manifest presence, while
   retaining generation, supervision-token and committed-run fences.
4. The new draft guard would also constrain copies of successfully saved library
   scripts if their active batch marker survived persistence. Atomic save now
   converts the marker to `jmeter_batch_origin_v1` provenance. Saved assets retain
   source identity without depending on an expiring draft capability. Interrupted-save
   recovery normalizes the requested draft to the same provenance representation
   before comparing it with persisted assets.

<a id="import-reviews-2026-09--regression-coverage"></a>
##### Regression coverage

Tests cover standalone child claims and tokenless partial creation, successful
whole-batch claims/save, persistent provenance, SQLite-backed repeated save
receipts and interrupted-save identity recovery, mismatched project/profile
identities, and reservation release after manifest omission.
The protected browser workflow also checks standalone creation is rejected before
repairing, saving and reopening the complete import.

The subsequent code review found no further actionable issues in the revised
candidate publication, save, staging, ownership and cleanup paths. Final verification:

- Focused batch tests pass, including SQLite receipt replay and recovery identity.
- API batch race tests pass (146 seconds); full generator/engine/protected-store
  race suites pass.
- Kafka/Event Hubs real-adapter build-tag tests pass; these are not authorized
  live-service conformance evidence.
- The protected local-auth browser journey passes, including the partial-save
  rejection, repairs, save/reopen and launch blocker.
- The full dashboard cycle passes: type checking, lint, 4,476 unit tests, fresh
  build, dev-admin browser workflows (137 passed, four skipped) and local-auth
  workflows (57 passed, one skipped).
- `go test -p 1 -timeout 30m ./...` passes on the final validation snapshot
  (API package: 522 seconds), including generated-artifact freshness checks.
  The snapshot isolates tests from concurrent dashboard rebuilds in the shared
  checkout; all nine reviewed source/test files were verified identical.
  Initial snapshot-only Git-history failures were resolved before this final run.

Strict client parity and authorized live-service conformance remain outside these
fixes.

<!-- Original record: docs/qa/reviews/import-tls-independent-review-2026-10-27.md; local evidence kept outside git -->
<a id="import-tls-independent-review-2026-10-27"></a>

## Independent review: LoadRunner TLS import and Review-tab editing


Date: 2026-10-27

Scope: PR #293, commit `8ed6a39f7` (merged as `41d6165b3`). Fixes were integrated with `e7cc0ac0a`, including PR #294, before validation.

Reviewer: independent agent `/root/independent_review`. The reviewer inspected the diff and surrounding implementation without editing files or running builds. The implementing agent applied the fixes and ran validation.

<a id="import-tls-independent-review-2026-10-27--findings-and-fixes"></a>
### Findings and fixes

- **P2 — Stale TLS fidelity in multi-lane imports.** Settings responses updated a script but retained the original aggregate fidelity report. That report is also initially copied into each lane, so selecting a lane's preserved report alone did not suffice. The selected view, queue outcome, and approval checks now use lane-aware fidelity. Once an updated server report exists, gate-backed summaries are reconciled with each lane's actual findings. Real sibling gates and converter-level limitation sentences remain. Aggregate-only legacy blockers remain until updated evidence is available. Guided repair also preserves the original fallback for sibling lanes.
- **P3 — Migration warning contradicted later TLS selections.** `AUTO` or legacy SSL followed by explicit TLS 1.2 retained a warning that said TLS 1.3 was required. Warnings now describe the import-time default and require endpoint compatibility with the final configured range.
- **Regression-fixture review.** The initial browser sibling fixture cleared `init` although its TLS gate was in `actions`. The fixture now retains only the home request and asserts that the sibling has no findings before exercising the copied-report case.

<a id="import-tls-independent-review-2026-10-27--regression-coverage"></a>
### Regression coverage

- Multi-lane UI settings apply and subsequent sibling selection, with both clean and blocked siblings.
- A clean sibling inheriting the initial TLS report; real sibling gates; non-gate archive limitations; unchanged legacy aggregate-only blockers; source-report immutability.
- `AUTO` and SSL3 followed by explicit TLS 1.2, with warnings checked against the final range.
- Server-backed TLS settings apply with a synthetic read-only sibling carrying the initial copied report, in both browser auth profiles.

<a id="import-tls-independent-review-2026-10-27--validation"></a>
### Validation

Passed after integration:

- `go test ./pkg/convert/enterprisearchive -count=1 -timeout=5m`.
- Dashboard build (including TypeScript checks) and full ESLint run.
- 233 tests across the importer, importer memoization, review outcome, guided repair, and runtime editor suites.
- Existing inline TLS workflow and new multi-lane regression in both `dev-admin` and `local-auth` profiles: two browser tests passed per profile.
- Documentation and whitespace checks.

The independent reviewer completed a final re-review of all seven source/test files and the review record with no further actionable findings. The full repository suite remains a CI merge gate.

Browser artifacts are local evidence under `/private/tmp/apex-tls-independent-e2e/` and are not committed.

<!-- Original record: docs/qa/reviews/java-dependency-import-2026-10-01/import-review.md; local evidence kept outside git -->
<a id="java-dependency-import-2026-10-01-import-review"></a>

## Java VuGen import and review — 2026-10-01 to 2026-10-02


Status: import, attachment storage, save and reopen verified on 2026-10-02; independent code review is clean. Validation is complete; the authenticated browser profile passed its full rerun. The initial timeout is retained in the validation history. The original application was not contacted and supplied Java/JAR/class/EXE code was not executed.

<a id="java-dependency-import-2026-10-01-import-review--fidelity-result"></a>
### Fidelity result

The primary `DP_Create_Account_IR_GLF_Billcycle.zip` and supplemental `OneDrive_2026-10-01.zip` account for 107 regular source members. Six supplied JAR basenames match all six JAR entries in the source classpath; the seventh classpath entry is a directory. Dependency fingerprints are inspection evidence, not runner installation or dependency closure.

All 205 selected CFG/USP assignments, 77 USR assignments, 17 parameter definitions and 221 selected PRM assignments are accounted for. Five datasets retain seven data rows (1, 1, 1, 3, 1), with 584 combined bytes including headers. An earlier audit counted the string length of `inline_data` as rows; the browser and independent CSV parsing corrected that report error. Primary and paired data definitions are byte-identical.

Paired conversion conserves 362 of 362 function dispositions: 116 native LR bridge dispositions and 246 requiring the Java runner. The review shows 384 source mappings, a different denominator that includes other statement mappings. Four Java source files retain a 26,396-byte full-source manifest. The supplied helper text resolves two source-inheritance accounting findings; its equivalence to the compiled helper remains unproved.

Four fidelity gates remain: malformed imported VM quoting and safe VM replacement in init, action and teardown. Three governed lifecycle actions are structurally valid; approval, runner qualification, JDK/classpath configuration and application reachability remain prerequisites. No source VM arguments were guessed or replay gates removed.

The [detailed fidelity report](#java-dependency-import-2026-10-01-java-dependency-fidelity) links value-free setting inventories, physical-member fingerprints and manifest dependency checks.

<a id="java-dependency-import-2026-10-01-import-review--product-design-flow-audit"></a>
### Product Design flow audit

Current-run Codex in-app browser captures support this review. Original hosts, credentials, data values and source code are excluded from repository screenshots. Private attachment-policy evidence stays with the private local review artifacts.

| Step | Task | Health | Evidence and finding |
| --- | --- | --- | --- |
| 1 | Queue the project and attach the dependency ZIP | Good | Entry explains source/settings/dependencies and says import does not execute the script. Required Files accepts the intact supplemental ZIP. |
| 2 | Validate and inspect runtime coverage | Good, replay gated | Every selected source assignment is accounted for. Categories, status filters and search make the large settings list navigable. The UI merges related source keys; its 187 displayed rows differ from 205 physical assignments. |
| 3 | Inspect parameters, data and source mapping | Good, review required | Parameter definitions and read-only datasets are separated from replay issues. Source Mapping distinguishes bounded previews from full-source evidence. Converted mapping counts represent conservation, including external Java dispositions; they do not imply native Go execution. |
| 4 | Inspect inert inventory and attach JARs | Good, runtime qualification required | All six JAR uploads completed, including the 53.8 MB WebLogic JAR. Stored bytes match the supplied SHA-256 and sizes in the project-owned storage namespace. Inventory remains inert; policy-off upload gives operator guidance. The seventh classpath entry is a directory, not a missing JAR. |
| 5 | Save the combined authoring review | Good, approval blocked | The receipt confirms zero executable scripts and one saved review. Database readback verifies settings, data, source accounting, original/member fingerprints and all six exact bindings; four fidelity blockers remain. |
| 6 | Find and reopen the saved review | Good, replay gated | Scripts → Authoring review → Resume reopens the named review, three lifecycle actions and 17 parameter mappings. Repeated after controller restart with uploads and Java execution disabled; API readback preserves all six bindings. Checks & readiness explains the safe-VM replacement and governed runner prerequisites. |

<a id="java-dependency-import-2026-10-01-import-review--captured-evidence"></a>
#### Captured evidence

1. Entry and destination:

Import entry (`screenshots/01-import-entry.jpg`)

2. Runtime categories and an explicit unresolved source policy:

Runtime settings (`screenshots/02-runtime-settings.jpg`)

3. Data source counts and column/parameter mappings, with no row values:

Data sources (`screenshots/03-data-sources.jpg`)

4. Source coverage, full-source fingerprint and preview limit:

Source summary (`screenshots/05-source-summary.jpg`)

5. Inert dependency evidence (cropped before recorded host paths):

Dependency inventory (`screenshots/06-dependency-inventory.jpg`)

6. Save receipt:

Saved review receipt (`screenshots/07-review-saved.jpg`)

7. Durable review queue and Resume:

Saved review in the Script Library (`screenshots/08-library-review.jpg`)

8. Reopened authoring workbench:

Reopened authoring review (`screenshots/09-resumed-review.jpg`)

9. Explicit replay gates after reopening:

Reopened readiness gates (`screenshots/10-resumed-readiness.jpg`)

The sanitized saved-review verification (`save-verification.json`) records exact attachment hashes and typed import counts. The complete archive backups, raw database, file capability references and values remain in the private local review directory.

<a id="java-dependency-import-2026-10-01-import-review--design-and-accessibility-observations"></a>
#### Design and accessibility observations

The import status and execution status are distinct, and blocked replay still permits a durable review save. Tab labels, issue counts, filters and mapping navigation support a long review. The native details controls expose dependency evidence without requiring it all to stay expanded.

At some scrolled positions, the sticky review tabs overlap the section heading or status. The first source-mapping capture was recaptured at a stable heading position. This is a visible layout issue to refine; it did not prevent tab selection, filtering or saving. Long data-source names wrap into short fragments in narrow table columns, and the dense hash list needs generous width. Keyboard Home moves to the first review tab by normal tablist behavior; Page Up/Down scrolls the containing view. These observations do not establish screen-reader or WCAG compliance. Contrast measurement, zoom/mobile layout and a complete assistive-technology pass were outside this audit.

<a id="java-dependency-import-2026-10-01-import-review--independent-code-review"></a>
### Independent code review

The converter, API and frontend were reviewed by agents other than their authors, with root review of the combined diff and actual browser verification. Findings were repaired and regression tests added. Final independent re-review found no remaining actionable code findings. Validation outcomes and the browser-cycle timing caveat are recorded below.

| Finding | Repair / regression scope |
| --- | --- |
| Opaque JAR/class/EXE/store/certificate bytes could enter source previews | Known extensions and binary signatures stay inert; disguised binary and valid UTF-16 source cases are tested. |
| Dependency-only archives lacked a clear project attachment path and durable inventory | Recognize dependency bundles without inventing an executable script; retain finalized path/size/hash inventories and explicit attachment guidance. |
| Supplemental ZIP handling could lose member-level provenance or merge ambiguous content | Bounded one-level expansion, combined budgets, collision/project/nested-ZIP rejection and original/member fingerprints. |
| Primary merge could erase unsafe ZIP header metadata | Reject encryption, nonregular entries, invalid names and directory/path mismatch before rewriting, including explicit replacements. |
| Authoring save lost uploaded Java bindings | Carry exact-entry bindings into the governed review draft; save/reopen contract is checked by real API browser regression. |
| Attachment exception could accept executable changes or forged/foreign files | Server-owned project capability, size, hash and ZIP signature verification; exact full execution baseline after masked-secret reconciliation. |
| Concurrent or stale uploads could overwrite bindings across reviews/projects | Serialized uploads, operation locks, project/item/generation fencing, explicit empty removal and changed save identity. |
| Authoring PUT did not apply the attachment checks | Same capability checks on changed bindings; preserve the raw-credential execution-context guard. |
| Submitted protocol whitespace or a protected protocol switch could skip the Java guard | Select the strict guard from the trusted original family and normalize family comparison; exact baseline still rejects protocol changes. |
| Equal inactive bindings could activate through protocol/enabled-state transitions | Verify changed protocol, project, ordered classpath and false-to-true activation. A server-private checked marker limits the attested-create exception; all other claims receive attachment verification. Legacy activation and policy-off unchanged evidence are covered. |

<a id="java-dependency-import-2026-10-01-import-review--validation"></a>
### Validation

| Gate | Result |
| --- | --- |
| Complete converter/sourceview package suites | Passed and repeated independently. |
| Final API trust regressions | Passed in 8.334 s; independent final repeat passed in 5.082 s. |
| Java attachment policy/auth matrix | 4/4 passed against the final reviewed controller: dev-admin and local-auth, each with uploads disabled (404) and explicitly enabled (201). Exact durable bindings, Resume, disabled runner and zero executable scripts are asserted. |
| Dashboard type checking, lint, units and build | Passed on Node 22.22.0: both TypeScript configurations, full lint, 447 unit files / 4,712 tests and the final embedded build. The existing ScriptEditor chunk-size warning remains. |
| Complete browser profiles | Dev-admin: 181 passed / four expected skips. Final full local-auth rerun: 101 passed / one expected skip, exit 0 (14.2 min). |
| Full Go suite | Passed all 117 tested packages, with six additional packages containing no tests (`go test -count=1 -p 2 -timeout 20m ./...`). |
| Documentation and whitespace | Passed: 92 active Markdown files and `git diff --check`. |

The initial `npm run test:cycle` exited 1. Its broad dev-admin profile passed 181 tests with four skips; local-auth passed 100 tests with one skip and one existing Source Mapping layout test exceeding its 30-second timeout. The new Java import/save/reopen case passed in both broad profiles. Three dev-admin skips require restricted local-auth personas; the private acceptance-archive case was skipped in both profiles because its environment variable was unset. Actual supplied-script import/save/reopen was independently checked in the local review workspace.

The unchanged failed workflow subsequently passed its focused local-auth rerun against the exact final reviewed controller and fresh test state, completing all layout assertions. Two independent reviews found no demonstrated product or layout-contract defect in the retained artifacts. The initial cycle failure and truncated trace remain retained privately; the focused pass is a separate result, and the initial full cycle is not reported as green. No source, assertion, timeout or asset changes were made for that rerun.

Before publication, the entire unchanged standard local-auth profile was rerun using Node 22.22.0, fresh test state and the standard workflow harness. It passed all 101 eligible cases with one optional private-archive skip and exit 0. The previously timed-out Source Mapping case passed in 11.6 seconds, and the Java JAR policy/save/reopen case passed. Together with the completed dev-admin profile, this closes the browser-profile validation gate; the original cycle failure remains part of the history.

Test fixture/type/assertion corrections are tracked separately from production findings; the final test-only JSON/Outline corrections received a clean independent review. The new fixtures are synthetic inert JAR/ZIP files. Public evidence was checked independently against private counts, fingerprints and endpoint/value candidates; supplied customer bytes remain private. All nine screenshot copies match the individually inspected accepted captures.

<a id="java-dependency-import-2026-10-01-import-review--review-handoff"></a>
### Review handoff

The saved `DP_Create_Account_IR_GLF_Billcycle` review is available through Scripts → Authoring review → Resume. The queue clearly marks approval blocked; reopening exposes the three lifecycle actions, 17 parameter mappings and Checks & readiness. A controller restart with new uploads and Java execution disabled retained the saved six-JAR bindings.

Implementation and validation used the isolated `codex/java-dependency-fidelity` worktree, based on the previously pushed import work. The original shared checkout’s unrelated working files were preserved.

<!-- Original record: docs/qa/reviews/java-dependency-import-2026-10-01/java-dependency-fidelity.md; local evidence kept outside git -->
<a id="java-dependency-import-2026-10-01-java-dependency-fidelity"></a>

## Java dependency import fidelity — 2026-10-01


Static inspection accounts for the supplied dependency bundle and the primary DP Java project. No customer Java, classes, JAR entry points, supplied executable, or original target host was executed. Original binaries, source code, credentials, and data values are excluded from these public artifacts.

<a id="java-dependency-import-2026-10-01-java-dependency-fidelity--dependency-bundle"></a>
### Dependency bundle

`OneDrive_2026-10-01.zip` contains **nine regular files, six outer JARs**, one standalone class, one supplied helper text, and one executable. There are no outer directory entries, encrypted members, or symlinks. Its size is 61,443,208 bytes; expanded outer members total 61,441,886 bytes. SHA-256 is `57da3246409502cbd567f27c1d47eb108862f6d10d1451723aab6fd672e35d91`. The complete member fingerprint inventory (`dependency-members.csv`) includes all nine files and both nested JAR resources; structured metadata (`static-dependency-inventory.json`) retains the size, count, and version evidence.

| JAR | Class count | Major.minor class versions and counts |
| --- | ---: | --- |
| ClientBeans.jar | 140 | 49.0: 140 |
| ClientData.jar | 1545 | 49.0: 1545 |
| sbmsIrdSec.jar | 14 | 49.0: 14 |
| uams.jar | 2196 | 50.0: 711, 52.0: 1483, 45.3: 2 |
| util.jar | 46 | 45.3: 46 |
| weblogic.jar | 29342 | 48.0: 23378, 49.0: 3220, 45.3: 1804, 46.0: 940 |

The six outer JARs contain 33,283 classes. Two nested schema JAR resources inside `weblogic.jar` each contain one additional class. The standalone class adds one more, giving 33,286 inspected class records including nested resources. All outer class structures parsed without a format-boundary error; this is not JVM bytecode verification or a runtime linkage test. No native-library resources, duplicate entry names, or JAR signature files were found in the six outer JARs. One class occurs in both ClientBeans and ClientData; the two definitions are byte-identical. Its proprietary identifier is withheld.

Major 45 is the original Java generation; 46 is Java 1.2, 48 is Java 1.4, 49 is Java 5, 50 is Java 6, and 52 is Java 8. The highest supplied major version therefore requires a VM that accepts Java 8 bytecode. Older bytecode acceptance does not establish library/API compatibility. [Official class-file version specification](https://docs.oracle.com/javase/specs/jvms/se26/html/jvms-4.html).

`weblogic.jar` declares implementation version 9.2.1.0, while `uams.jar` includes both Java 6 and Java 8 classes. This mix requires qualification with the selected runner/JDK and remote deployment; it is not evidence that this legacy WebLogic library is supported on a modern JDK. Oracle's historical 9.2 compatibility and supported-configuration pages describe older JDK environments. [WebLogic 9.2 compatibility](https://docs.oracle.com/cd/E13179_01/common/docs92/upgrade/compat.html), [historical supported configuration](https://docs.oracle.com/cd/E13196_01/platform/suppconfigs/configs/oracle_ubl/oel50_92.html).

The WebLogic manifest declares **38 Class-Path JAR names**. None is present as an ordinary supplied sibling. Two matching basenames exist only as nested schema resources. The manifest checklist (`manifest-classpath.csv`) separates these cases; no extraction or automatic classpath binding occurred. This server-oriented library includes optional subsystems, so absence from this full manifest is not proof that every named library is required by the selected client path. Conversely, the supplied JARs do not prove complete transitive/runtime dependency closure. Manifest classpath resolution is a separate class-loader operation. [Official JAR manifest Class-Path rules](https://docs.oracle.com/en/java/javase/26/docs/specs/jar/jar.html).

Static member references connect ClientBeans to ClientData/WebLogic; ClientData to ClientBeans/security/UAMS/WebLogic; the security library to ClientBeans/UAMS; UAMS and util to WebLogic. References to classes outside the supplied files also exist, including platform and optional subsystem families. Counts over an entire server JAR are not a reachable-call dependency proof for this test.

<a id="java-dependency-import-2026-10-01-java-dependency-fidelity--basicclient-evidence"></a>
### BasicClient evidence

The class stored at `Infra/BasicClient.class` declares the **default-package name `BasicClient`**, not `Infra.BasicClient`. It is public and abstract, major 46/minor 0, with 22 fields and 10 methods. Its structural references include JNDI and the supplied client/data/security libraries. A conventional class loader therefore needs the directory containing this class as its lookup root, or a separately verified relocation; the archive folder name does not establish a package. No helper class was loaded.

`BasicClient.txt` has 196 lines and identifies itself as supplied decompiled helper text. The importer can normalize recognizable Java text from `.txt` to `.java` for source inspection. This preserves the text bytes; it does **not** establish equivalence with the separately supplied `.class` file or provenance of the decompilation. JNDI/reflection behavior and inherited construction remain Java runtime semantics requiring review. The `.txt` and `.class` have distinct fingerprints in the inventory.

`jad.exe` is a PE x86 executable (machine 0x14c), retained only as inert inspection evidence. It was not launched or used to decompile any file.

<a id="java-dependency-import-2026-10-01-java-dependency-fidelity--primary-project-and-earlier-kafka-comparison"></a>
### Primary project and earlier Kafka comparison

The primary `DP_Create_Account_IR_GLF_Billcycle.zip` contains **104 entries: 98 files and six directories**, totaling 724,370 expanded file bytes. Its archive SHA-256 is `ddd194787af00ab058fcb8d5516fbabb8b5f8024054d2914848eb9ff17d1849d`. The all-member source inventory (`dp-source-asset-inventory.csv`) accounts for selected Java source/settings/data, compiled evidence, prior logs, backups, command wrappers, authoring support, stores, databases, and icons. Original contents remain private/in supplied inputs. The primary `Actions.class` has major 52/minor 0 (Java 8 bytecode) and 16,907 bytes, corroborating the bundle's Java 8 bytecode floor. It remains inert compiled evidence; no equivalence with `Actions.java` was proven.

Three declared classpath rows parse to seven entries (six JARs and one directory) and require six unique JAR basenames, and the new bundle supplies **6/6**: ClientBeans, ClientData, sbmsIrdSec, UAMS, util, and WebLogic. This establishes basename completeness only. It does not resolve recorded workstation paths, search roots, classpath order, bytecode/source equivalence, remote identity, credentials, or supported runner/JDK behavior.

The earlier CLM Kafka project has two classpath rows and 11 unique required JAR basenames. None matches the new six JARs (**0/11**). Its Kafka, SLF4J/Log4j, and Kafka/JMeter helper dependencies remain separate requirements; this bundle is not a replacement for them.

Conversion of the primary project, before supplemental source attachment, produces one governed `java_vuser` action in each lifecycle phase (three total). All **272 expected source functions are accounted for**: 116 native LR-bridge dispositions and 156 external-Java dispositions, with zero skipped functions. The 75 transaction markers refer to 25 transaction names. These counters describe source conservation, not 272 native Go operations.

All **205 CFG/USP keys** reconcile: 150 CFG plus 55 USP, with 64 mapped, 38 compatibility-only, and 103 non-applicable. The value-free setting checklist (`runtime-settings.csv`) and JSON accounting (`runtime-settings.json`) include every key. All 77 USR keys across 14 sections are preserved. The two `result1` INI files contain another 12 prior-run/report metadata keys (10 GeneralInfo and two MediaInfo), separately accounted in the value-free prior-run checklist (`prior-run-settings.csv`); they are not selected replay settings. GeneralInfo has a UTF-16 BOM; MediaInfo is UTF-16LE. The supplied `uams.properties` resource is empty, contributes no setting assignments, and is not provisioned to a runner. Both physical PRMs are byte-identical; one logical parameter file retains 221 setting assignments and all 17 file parameter definitions. Five inline data assets retain 64, 109, 367, 22, and 22 bytes, 584 total, containing 1, 1, 1, 3, and 1 data rows, seven total (headers excluded); no data values are included here.

The initial conversion has zero structural DSL issues and six fidelity blockers: malformed source additional-VM-parameter quoting, two unresolved BasicClient inheritance/construction findings, and three lifecycle-specific safe-VM-replacement gates. These are not missing table parameters. Supplemental source inspection may resolve superclass accounting, but an unproved helper equivalence or unsafe VM string must not be silently promoted. A static paired conversion of unchanged source members resolves those two superclass accounting findings; the helper text remains unproved against the compiled class. The paired result stays blocked by four findings: malformed VM quoting and the three lifecycle safe-VM-replacement gates. All eight source VM-parameter rows remain preserved; four numbered rows contain unclosed quotes, so no command-line repair was guessed. This paired check uses a private one-level repack for converter evidence; the actual API route and save workflow are validated separately.

<a id="java-dependency-import-2026-10-01-java-dependency-fidelity--retention-and-execution-limits"></a>
### Retention and execution limits

Dependency inventory carries paths, roles, sizes, SHA-256 fingerprints, and bounded class-header versions, with `runtime_provisioned=false`. It does not copy original binary bytes into public metadata or establish an effective classpath. A standalone dependency archive remains a review artifact with no executable script, and directs the operator to attach it to its Java project. Existing unsupported source projects with libraries retain their original classification.

Executable, certificate/private-key, store/database, and other binary assets are now excluded from source-text previews, including recognized binary magic hidden under a text or unknown extension. Known UTF-16 BOM/source signatures remain readable. Inert source-asset evidence accounts for those files. Fingerprints and bounded previews are not immutable full-bundle retention or runner provisioning; original binaries/archives must be retained separately through the approved private workflow.


<a id="java-dependency-import-2026-10-01-java-dependency-fidelity--completed-converter-checks"></a>
### Completed converter checks

The actual standalone dependency ZIP produces no executable script, nine normalized regular-file inventory records, eight opaque assets, and one eligible text preview. Its six JARs, one class, and one executable remain inert with `runtime_provisioned=false`. The primary project produces 98 regular-file records, nine opaque assets, and a bounded 20-file source preview. All 104 physical entries, including the six directory records, are independently accounted for in the physical source inventory.

The private paired conversion accounts for **107 regular files**, comprising 98 primary files and nine dependency files. Its 17 opaque assets comprise six JARs, two classes, one executable, and eight other non-preview support assets. The latter include encoded prior-run settings (`Mediainfo.ini`, UTF-16LE without a BOM), so 17 opaque assets does not mean 17 semantically binary files. Its two keys remain separately accounted as prior-run metadata; the conservative source-preview decoder does not claim arbitrary BOM-less UTF-16 support. It preserves all 17 parameters, the same five data sources and seven data rows (584 inline-data bytes), all 205 runtime keys, all 77 project keys, and all 221 selected PRM assignments. It emits the same three governed lifecycle actions with zero structural DSL issues. Including supplied helper source, expected and accounted source-function counts are both **362**: 116 LR-bridge and 246 external-Java dispositions, with zero skipped functions. The original project remains 272/272; the additional 90 dispositions belong to the supplied helper source. The paired Source Mapping view contains 384 mappings, a separate count that includes settings and other source evidence.

The 205 source-key dispositions (64 mapped, 38 compatibility-only, 103 non-applicable) count physical CFG/USP assignments. The separate runtime-concept summary counts 47 concepts: 23 executed, 20 compatibility-only, two partial, and two non-applicable. These are different denominators; neither implies that the malformed VM parameters or Java dependencies have been executed.

Finalized `ConvertZip` serialization preserves identical inert inventories at the canonical `preserved.review_artifact.metadata` location and direct preserved inventory fields. Meaningful synthetic regressions cover opaque bytes disguised as text, explicitly mapped binary assets, recognized UTF-16 source, dependency-only classification versus unsupported source projects, completed-import serialization, fingerprint/header evidence, and absence of runner/classpath provisioning.

Complete affected-package tests passed: `go test -p 2 ./pkg/convert/sourceview ./pkg/convert/enterprisearchive` (sourceview 0.080 s; enterprise archive 6.644 s). A second agent independently reviewed the final changes and repeated both complete suites successfully, with no remaining actionable converter/sourceview findings. Original source and binaries were never executed. API save/resume and broader merge-gate results are recorded in the workflow audit after the final API review.


Independent API review repaired bounded-import safety gaps: supplemental rewriting must reject unsupported primary file metadata before it can erase symlink/nonregular headers, and authoring-session updates must verify changed JAR bindings through the same project-owned capability, exact-entry, size, and SHA-256 checks as creation. Java review selection comes from the trusted protocol, so whitespace or a client protocol switch cannot skip it. Final attachment validation also covers non-Java claims, protocol transitions into Java, runner activation, and changes to ordered classpath entries or project ownership. Regression tests cover direct replacements and both import routes, legacy/protected saved sessions, forged/cross-project updates, valid runtime-bound updates, policy-off unchanged evidence, and dormant legacy bindings activated through protocol or classpath changes. The independent repeat passed: `go test -count=1 -p 1 ./pkg/api -run 'TestEnterpriseSupplementalZIP|TestAuthoringJavaClasspathAssets'` (5.082 s against the final frozen repairs). No actionable findings remained in that final API review scope.


Dataset counts above were checked by parsing the `inline_data` strings as CSV with the declared delimiter and header flag. An earlier report draft mislabeled string lengths as row counts; the corrected values are seven data rows and 584 bytes. The primary and paired data-source definitions are exactly equal, so this correction does not indicate an importer data loss. The remaining numeric assertions were rechecked against typed JSON, physical archive members, and independent source-key/CSV checklist counts.

The final uncached full Go merge gate passed: `GOCACHE=/private/tmp/apex-clm-go-cache GOMAXPROCS=4 go test -count=1 -p 2 -timeout 20m ./...`. All 117 packages with tests passed, and six packages reported no test files (123 packages total). There were no failure or panic lines. The complete API package passed in 636.679 s, enterprise archive in 8.791 s, and source review in 0.102 s; this run shared CPU with another unrelated broad test job. The original source scripts, JARs, classes, supplied executable, and unreachable hosts were not executed.

<!-- Original record: docs/qa/reviews/monitoring-audit-2026-09-12/README.md; local evidence kept outside git -->
<a id="monitoring-audit-2026-09-12"></a>

## Monitoring and fleet audit (2026-09-12)


Status: Implemented; one verification limit remains open: remote Prometheus / vendor APM ingestion was never verified against a real datasource (see 'Remaining environment limits' in the Remediation section).

Merged from the original per-topic files of this review. Each section below was one file; the screenshots and evidence next to this README are unchanged.

<a id="monitoring-audit-2026-09-12--monitoring-usability-and-functional-completeness-audit"></a>
### Monitoring usability and functional-completeness audit

Reviewed September 12, 2026, against the running local APEX Load dashboard.

**Verdict: useful operational coverage, with incomplete paths from status to diagnosis and resolution.** The five tabs cover the right areas, but status wording, hidden calendar events, collector onboarding, and keyboard behavior need attention before this can be treated as a dependable enterprise monitoring workspace.

<a id="monitoring-audit-2026-09-12--scope-and-evidence"></a>
#### Scope and evidence

User goal: determine whether the platform can run tests, identify what needs attention, inspect monitoring coverage, and understand upcoming work and capacity.

Inspected the running application at `127.0.0.1:8080`, using the Codex in-app browser, in the existing ACC project / Legacy application / Imported Legacy release context. The runtime identifies itself as local development with authentication bypass, SQLite, in-memory cache, no distributed generator inventory, no registered collectors, and no configured APM profiles. Existing queued and recurring runs populated the calendar. Desktop and narrow layouts were inspected; the narrow browser reported 704 CSS pixels of content width.

All screenshots were captured and inspected during this audit. Source inspection corroborates behavior and identifies implementation limitations; it is explicitly distinguished from live verification below. Existing uncommitted work was present. No product source was edited, settings saved, collectors registered, tokens issued, tests executed, bookings reserved, or services restarted.

<a id="monitoring-audit-2026-09-12--flow-and-health"></a>
#### Flow and health

| Step | Task and captured states | Health |
|---|---|---|
| 1 | Overview: initial narrow layout and desktop health/readiness | Usable; weak prioritization and ambiguous monitoring coverage |
| 2 | Fleet: capacity, empty inventory, alerts, audit | Partially complete; good setup links, weak explanations and drill-down |
| 3 | Telemetry: catalog, expanded APM/pack details, collector registration | Needs improvement; setup instructions and form layout interrupt onboarding |
| 4 | Alerts: empty state, keyboard navigation, policy settings, return to Alerts | Needs improvement; warning visibility and coverage wording undermine confidence |
| 5 | Schedules: month/week/day, run detail, booking form, unsaved draft, narrow layout | Broad functionality; hidden-event access and context preservation are incomplete |

<a id="monitoring-audit-2026-09-12--what-works"></a>
#### What works

- Five recognizable tabs keep the scope manageable. Selected tabs have both visible styling and tab semantics.
- Overview distinguishes controller connectivity from production readiness. Optional Kubernetes is explicitly “Not applicable” in local mode; the auth-bypass warning explains the actual degraded check.
- Fleet explicitly separates local execution from generator inventory and offers relevant configuration links.
- Telemetry differentiates native support from integration contracts and says that support does not prove live data reception.
- Month, week, and day views respond. Day view makes full run names easier to scan, and selecting a run exposes workload, test reference, status, and an Open Test action.
- Booking has separate availability and reservation actions. Leaving a modified draft triggered an unsaved-changes dialog; discarding only the audit’s temporary value worked.
- Alert policy settings opened at the correct section. “Return to originating workflow” restored the Alerts tab and project/application/release context.
- Narrow schedules reflow into an agenda without document-level horizontal overflow in the inspected state.

<a id="monitoring-audit-2026-09-12--prioritized-findings"></a>
#### Prioritized findings

<a id="monitoring-audit-2026-09-12--1-high-clear-does-not-establish-that-sla-monitoring-is-configured-or-has-sufficient-evidence"></a>
##### 1. High — “Clear” does not establish that SLA monitoring is configured or has sufficient evidence

**Evidence: steps 1 and 4, captures 02, 07, 11; source corroboration.** The SLA card reports “CLEAR” and “No global SLA threshold breaches on running tests.” Following its policy link showed the global error-rate, P99, server-processing, and transfer thresholds unconfigured. The card does not surface that coverage state. This does not mean every possible test-specific quality gate is disabled; it means this card’s claim does not explain the coverage of the global policies it describes.

The source derives “clear” from an empty active-alert array, without a policy-configuration or observation-window check: [MonitoringPage.tsx](../../../dashboard/src/components/MonitoringPage.tsx#L572).

**Recommendation:** distinguish “Not configured,” “Waiting for samples,” “No breaches observed,” and “Active breaches.” Show the number of enabled policies and last evaluated sample, with a direct Configure policies action. Keep alert-feed freshness separate from ordinary page refresh.

**Acceptance:** a fresh session with no enabled global thresholds cannot present the same state as a configured, recently evaluated policy with no breaches.

<a id="monitoring-audit-2026-09-12--2-high-the-calendar-hides-runs-behind-non-interactive-n-more-text"></a>
##### 2. High — The calendar hides runs behind non-interactive “+N more” text

**Evidence: step 5, capture 08; source corroboration.** Four continuing queued runs occupy each early-month cell, hiding additional cron executions behind “+1 more.” That text is a span rather than an action. Users must discover a filter or change view to reach the hidden run. Long-lived queued runs also dominate many days without clearly explaining whether they are executing or merely waiting.

[MonitoringPage.tsx](../../../dashboard/src/components/MonitoringPage.tsx#L2420) renders the overflow indicator without an interaction. Week/day switching works, so this is a broken direct discovery path rather than total loss of access.

**Recommendation:** make “+N more” open the selected date’s complete agenda, retain the selected date, and offer a way to collapse continuing queued work. Use “Queued since…” to distinguish waiting from execution.

**Acceptance:** every hidden run can be reached from its calendar cell using mouse and keyboard; four continuing runs cannot permanently obscure a new execution.

<a id="monitoring-audit-2026-09-12--3-medium-the-advisory-warning-cannot-be-investigated-from-alerts"></a>
##### 3. Medium — The advisory warning cannot be investigated from Alerts

**Evidence: steps 1 and 4, captures 02 and 07.** “1 advisory warning” remains above an Alerts tab containing Clear, No diagnostic samples, and No inventory. The actual warning is the auth-mode readiness issue visible in Overview. The chip exposes only a title tooltip (“Readiness: 1 degraded”) and is not a navigable action.

**Recommendation:** include readiness/controller advisories in the Alerts workspace, or make the summary open a list with a link to the responsible card. Preserve the distinction between deployment advisories and test alerts.

**Acceptance:** every warning counted in the top summary has an accessible explanation and next action reachable from that summary.

<a id="monitoring-audit-2026-09-12--4-medium-collector-registration-has-a-concrete-layout-defect"></a>
##### 4. Medium — Collector registration has a concrete layout defect

**Evidence: step 3, capture 06 and loaded accessibility snapshot.** Metric-pack checkboxes appear in the middle of wide rows while their labels start at the left on another line. Status and signal text run together, for example “Native supportcpu_process_pct.” This makes the relationship between a checkbox, a pack, and its signals difficult to scan.

Source corroboration: [MetricPackPicker.tsx](../../../dashboard/src/components/MetricPackPicker.tsx#L18) lacks a structured row treatment, while [parityPanels.css](../../../dashboard/src/components/parityPanels.css#L224) applies full-width field styles to all inputs, including checkboxes.

**Recommendation:** give each pack a compact checkbox row, a clearly separated support badge, and a second line for signals. Scope text-input styles to the relevant input types. Use a shorter accessible label with supplementary descriptions.

**Acceptance:** each checkbox is visually adjacent to its pack name, text has proper separation, and the picker works at desktop and narrow widths.

<a id="monitoring-audit-2026-09-12--5-medium-telemetry-presents-capabilities-but-does-not-complete-setup-to-verification-workflows"></a>
##### 5. Medium — Telemetry presents capabilities but does not complete setup-to-verification workflows

**Evidence: step 3, captures 04–06.** Expanding OpenTelemetry provides environment-variable instructions and says to check datasource/collector freshness, but supplies no contextual setup guide, configuration action, or connection-verification result. JVM’s integration contract is similarly a technical instruction to publish archive metrics. Four narrow columns and nested scroll areas make these details harder to use while much of the page remains empty. Amber styling for supported capabilities further resembles an operational fault.

**Recommendation:** separate “Available capabilities” from “Connected and receiving data.” Use wider detail panels with explicit Configure, Setup guide, Verify connection, and View latest data actions appropriate to each integration. Reserve warning styling for actual degraded or incomplete required configuration, rather than all optional catalog entries.

**Acceptance:** an operator can move from choosing an integration to confirming its data is arriving, with unsupported automation clearly identified and a usable manual setup path.

<a id="monitoring-audit-2026-09-12--6-medium-collector-data-can-look-current-while-it-is-a-one-time-snapshot"></a>
##### 6. Medium — Collector data can look current while it is a one-time snapshot

**Evidence: step 3, capture 04 anchors the collector surface; this is a source-confirmed limitation, not a live heartbeat test.** The page refreshes available monitoring data every five seconds, but `MonitoringCollectorsCard` loads its list on mount/scope change and after its own mutations. The Metrics action makes a single request and opens a dialog titled “Live metrics”; there is no polling or Refresh action in that dialog. The page’s updated timestamp can therefore be newer than collector information.

References: [collector load](../../../dashboard/src/components/MonitoringCollectorsCard.tsx#L160), [metrics fetch](../../../dashboard/src/components/MonitoringCollectorsCard.tsx#L248), [parent refresh](../../../dashboard/src/components/MonitoringPage.tsx#L789).

**Recommendation:** integrate collectors into the refresh lifecycle and show per-source last-success/age. Either refresh the metric dialog or name it “Latest captured metrics” and provide Refresh. Add a retry action for collector-list failures.

**Acceptance:** a collector heartbeat received while the tab is open updates its visible status within the documented interval, and a snapshot cannot be mistaken for a live stream.

<a id="monitoring-audit-2026-09-12--7-medium-keyboard-tab-navigation-loses-focus-calendar-selection-state-is-visual-only"></a>
##### 7. Medium — Keyboard tab navigation loses focus; calendar selection state is visual only

**Evidence: steps 4 and 5; `keyboard-focus-evidence.txt`, captures 08–09.** ArrowLeft from Schedules selected Alerts, but focus moved to the main region. A subsequent Left key did not continue through the tabs. The route-focus effect corroborates the focus move: [App.tsx](../../../dashboard/src/App.tsx#L3061).

Month/Week/Day and All/Cron/Ad-hoc/Plans/Current render as ordinary buttons with an `active` class, without an exposed pressed/selected state: [MonitoringPage.tsx](../../../dashboard/src/components/MonitoringPage.tsx#L2320).

**Recommendation:** preserve focus on the newly selected tab when navigation originates in the tab strip. Expose mutually exclusive view/filter selection through appropriate button or tab semantics. Verify focus order and visible focus across the complete flow.

**Acceptance:** repeated arrow keys navigate all five tabs without requiring the user to find the strip again; assistive technology can identify the active calendar view and filter.

<a id="monitoring-audit-2026-09-12--8-medium-sla-alert-acknowledgement-is-session-local-with-no-durable-triage-history-here"></a>
##### 8. Medium — SLA alert acknowledgement is session-local, with no durable triage history here

**Evidence: step 4, capture 07 anchors the surface; source-confirmed, not reproduced using live breaches.** Threshold alerts and their acknowledgement are stored in React state; the client bounds the list at 50. Fleet acknowledgement uses a backend API. These are materially different behaviors under similar alert workflows. An operator cannot assume SLA acknowledgement is shared with another operator or survives reload, and this page has no searchable persisted SLA incident history.

References: [App.tsx](../../../dashboard/src/App.tsx#L400), [acknowledgement callback](../../../dashboard/src/App.tsx#L755), [thresholdAlerts.ts](../../../dashboard/src/thresholdAlerts.ts#L33).

**Recommendation:** document the present session boundary in the UI; for enterprise triage, persist alert lifecycle and acknowledgement with actor/time, and add severity/status/time filters and history. Do not describe acknowledgement as resolution while a breach remains active.

<a id="monitoring-audit-2026-09-12--9-medium-fleet-capacity-and-audit-summaries-need-stronger-explanations"></a>
##### 9. Medium — Fleet capacity and audit summaries need stronger explanations

**Evidence: step 2, capture 03; source corroboration for audit sampling.** Effective Available is zero and Project Pending Slots is 32, but the card does not explain what is waiting or offer a drill-down to affected work. Project demand is partly labeled with a UUID. The empty audit card suggests adjusting filters/timeframe, but the controls are elsewhere.

The Fleet audit request reads the latest 20 general audit events, then filters locally and displays up to eight. A “quiet” result can therefore omit fleet activity outside that sample: [MonitoringPage.tsx](../../../dashboard/src/components/MonitoringPage.tsx#L766). This is not a claim that omitted events existed during this run.

**Recommendation:** explain capacity units and pending-demand scope, use project names, and link to affected queued runs. Label audit sampling explicitly or request fleet-filtered history server-side. Present one recommended fleet setup action for local/unconfigured deployments.

<a id="monitoring-audit-2026-09-12--10-lower-overview-hierarchy-and-schedule-context-add-avoidable-friction"></a>
##### 10. Lower — Overview hierarchy and schedule context add avoidable friction

**Evidence: steps 1 and 5, captures 01–02, 08–10, 12.** Version strings occupy substantial Overview space while the advisory’s cause sits lower in the readiness card. Calendar detail displays a cross-date queued interval as “May 9, 9:47 PM – 8:58 AM,” omitting the end date. Day view offers no equivalent to the date-cell Schedule a run button. Returning to Schedules after visiting another tab reset the inspected Day view to Month. The calendar’s visible counts combine source categories and overlapping state counts without explaining their relationship.

**Recommendation:** lead Overview with actionable health/readiness and move build detail into a disclosure. Show both dates for multi-day intervals, expose the display timezone, preserve calendar view/date/filter in navigation, and offer a persistent Schedule run action distinct from New booking. Explain that Current and Projected overlap the source totals.

<a id="monitoring-audit-2026-09-12--functionality-coverage-and-remaining-gaps"></a>
#### Functionality coverage and remaining gaps

| Area | Available now | Completeness assessment |
|---|---|---|
| Platform health | API/WebSocket, runtime/build identity, readiness checks, services | Useful current status; warning drill-down and coverage wording need work |
| Fleet | Capacity, inventory entry, alerts, audit link, configuration handoffs | Broad surface; zero-inventory state verified, populated operations not verified |
| APM and packs | Capability catalog, setup text, native/contract distinction | Partial onboarding; no observed end-to-end connection/ingestion verification path |
| Collectors | Registration form; source includes metrics, editing, token rotation, deletion | Management surface exists; registration layout and freshness gaps remain; no credentials issued during audit |
| Alerts | SLA, telemetry, fleet cards; policy and notification links; source includes Open/Ack | Operational feed, not yet a complete shared incident workflow |
| Scheduling | Calendar views/filters, queued and projected events, detail, Open Test, booking form | Broadest functionality; hidden-event access, creation consistency, and state retention need improvement |
| Historical monitoring | Current/sampled health and metrics; links to other product areas | No unified time-range investigation or historical telemetry charts observed in these tabs; decide whether to provide this here or clearly route to Results/APM |

<a id="monitoring-audit-2026-09-12--accessibility-risks-and-limits"></a>
#### Accessibility risks and limits

Confirmed issues are the keyboard focus interruption, missing programmatic calendar selection, and collector label/checkbox layout described above. Statuses generally include text alongside color, and dialogs have names and usable cancellation/return behavior. Muted text, small labels, and small calendar controls warrant contrast and target-size measurements. No screen-reader session, automated accessibility scan, formal contrast audit, full zoom matrix, or compliance determination was performed.

The local environment had no distributed fleet, collectors, active threshold-breach samples, or connected APM data. Successful collector deployment, token lifecycle, metric ingestion, alert delivery/acknowledgement across users, restart/drain operations, reservation conflict handling, and production RBAC remain unverified. The booking form was inspected but availability was not submitted: the browser automation did not retain entered datetime values. This is an audit-tool limitation, not evidence that the product’s date fields are broken. The only temporary form value was discarded. No load run was started to manufacture evidence.

Existing frontend changes and the running backend’s build identity mean source findings should be validated against the final built application before implementation sign-off. This was an evaluation; no code changes or regression suite were required or run.

<a id="monitoring-audit-2026-09-12--recommended-order"></a>
#### Recommended order

1. Fix trustworthy states and access: SLA configuration/observation state, actionable advisory summary, and calendar overflow drill-down.
2. Repair collector form layout, refresh behavior, and keyboard continuity.
3. Complete integration setup/verification paths and durable alert triage, then improve Fleet explanations and calendar context retention.

<a id="monitoring-audit-2026-09-12--current-run-screenshot-walkthrough"></a>
#### Current-run screenshot walkthrough

<a id="monitoring-audit-2026-09-12--step-1-overview-usable-needs-clearer-prioritization"></a>
##### Step 1 — Overview: usable, needs clearer prioritization

Initial narrow entry shows a large control-plane/build card ahead of readiness. The desktop view makes all three cards visible and clearly distinguishes optional Kubernetes from degraded auth mode.

01 — Initial Overview in the narrow app panel (`01-overview.png`)

02 — Desktop Overview and readiness (`02-overview-desktop.png`)

<a id="monitoring-audit-2026-09-12--step-2-fleet-partial-with-useful-setup-handoffs"></a>
##### Step 2 — Fleet: partial, with useful setup handoffs

Local mode and missing inventory are explicit. Pending demand lacks an affected-work drill-down; the audit sample needs a clearer scope statement.

03 — Fleet capacity and empty inventory (`03-fleet.png`)

<a id="monitoring-audit-2026-09-12--step-3-telemetry-and-collectors-onboarding-needs-repair"></a>
##### Step 3 — Telemetry and collectors: onboarding needs repair

The catalog differentiates support levels, but expanded instructions are constrained by narrow, internally scrolling cards. Registration’s checkbox layout is visibly broken.

04 — Telemetry catalog and collector empty state (`04-telemetry.png`)

05 — Expanded OpenTelemetry and JVM instructions (`05-telemetry-expanded.png`)

06 — Collector registration and pack-picker alignment defect (`06-register-collector.png`)

<a id="monitoring-audit-2026-09-12--step-4-alerts-and-policy-handoff-trust-gaps-working-return-path"></a>
##### Step 4 — Alerts and policy handoff: trust gaps, working return path

Alerts does not surface the readiness advisory. The policy handoff works and shows unconfigured global thresholds; returning restores Alerts and its scope. Keyboard evidence is saved separately in `keyboard-focus-evidence.txt`.

07 — Alerts with an unexplained advisory and Clear SLA state (`07-alerts.png`)

11 — Alert policy handoff with unconfigured thresholds and return link (`11-alert-policy-handoff.png`)

<a id="monitoring-audit-2026-09-12--step-5-schedules-useful-depth-incomplete-overflow-and-context-handling"></a>
##### Step 5 — Schedules: useful depth, incomplete overflow and context handling

Month view hides executions behind non-interactive overflow text. Week and Day switches work. Run detail exposes useful workload/context; cross-day dates and waiting status need clarity. Booking provides separate checking/reserving controls and an unsaved-change guard. Narrow layout reflows to an agenda.

08 — Calendar with continuing queued runs and hidden events (`08-schedules.png`)

09 — Day agenda and queued-run details (`09-schedule-details.png`)

10 — New scheduled booking form, no reservation submitted (`10-new-booking.png`)

12 — Narrow schedule layout reflows into an agenda (`12-schedules-narrow.png`)

<a id="monitoring-audit-2026-09-12--monitoring-audit-remediation"></a>
### Monitoring audit remediation

The Monitoring workspace now distinguishes configuration, observed samples, and active breaches. SLA incident episodes and acknowledgement actor/time are stored per project and survive reloads. Search, severity, status, and time filters run before pagination. Acknowledgement leaves active severity intact; a recurrence creates a new episode. Existing session alerts are not backfilled into durable history.

The journal uses a bounded worker outside metrics publication and drains during graceful shutdown. Persistence failures or backlog overflow appear as incomplete history. Abrupt process termination can lose samples still queued for storage. History follows test/project deletion. Completed runs cease contributing active incidents.

Overview prioritizes readiness, with build metadata under a disclosure. System advisories open Alerts and expose individual causes and next actions. Calendar overflow opens a day view; queued runs can be hidden, view/filter/date persist per project within the browser session, selected controls expose pressed state, and time labels include the local timezone and dates. Scheduling is available in every view.

Telemetry uses wider panels and neutral styling for optional capabilities; an enabled APM integration reporting unavailable retains a danger badge. Datasource evidence queries real Prometheus samples for 15-minute, 1-hour, or 24-hour windows, with charts, latest sample time, and refresh. Catalog configuration does not claim successful remote APM ingestion; setup guidance explains how to verify a run in the destination. Collector health refreshes every five seconds while visible. Metric dialogs explicitly show captured snapshots and offer refresh.

Fleet capacity exposes project names and pending-run links. Fleet audit filtering happens in storage before pagination, so unrelated recent access events cannot hide fleet activity.

<a id="monitoring-audit-2026-09-12--validation-and-review"></a>
#### Validation and review

Validation ran against an isolated source copy at `/tmp/apexload-monitoring-review-20260912` because other tasks were editing and rebuilding the shared checkout. Their changes were preserved. The existing controller on port 8080 was not restarted.

- Dashboard type checking, lint, and production build passed.
- The required `npm run test:cycle` was started. Its 4,120-test unit run exposed two stale assertions in concurrent workspace changes. After updating those expectations, all 50 tests in the affected files passed. Subsequent Monitoring review added an unavailable-APM regression; the complete 54-test MonitoringPage suite passed. The remaining cycle stages were run separately after the fixes.
- The full browser profiles exercised 99 dev-admin cases and 17 local-auth cases. Stale Results search selectors and Monitoring fixture setup failures were corrected, and every failed case passed on rerun. One Help role-specific case intentionally skips in dev-admin. Final Monitoring browser checks were repeated against the final build in both profiles, including keyboard navigation, retained calendar context, live collector heartbeats, snapshot refresh, and the unavailable-APM badge. The badge failure state uses an intercepted profile response; the heartbeat uses the real collector API.
- `go test ./...` completed all other packages but exceeded the API package's default ten-minute timeout. All 4,160 API tests/examples/fuzz seed entrypoints then passed in 17 bounded batches. Storage tests passed, including schema migration, PostgreSQL SQL compatibility, incident recurrence, acknowledgement persistence, project isolation, and fleet filtering before pagination. Two Java tests that failed during the concurrent full run passed when their packages were rerun separately. Generated API contracts and QA inventory checks passed after regeneration.
- Manual browser inspection covered desktop and narrow Telemetry layouts, 18-pixel collector checkboxes and adjacent labels, calendar overflow-to-day navigation, retained filters, and repeated arrow-key focus. `git diff --check` passed.

Review also corrected the configured-profile count, stale Results test selectors, a report empty-data guard, generated route/schema expectations, and the distinction between optional APM setup and an enabled integration failure.

<a id="monitoring-audit-2026-09-12--remaining-environment-limits"></a>
#### Remaining environment limits

No external Prometheus or vendor APM datasource was configured in the local environment, so remote ingestion was not verified. Prometheus request windows, empty/configuration/error states, and chart rendering are covered by component tests. Durable incident behavior is verified through storage and API tests; existing historical session alerts cannot be reconstructed. The original running controller requires a normal rebuild/restart to serve the new incident endpoints.

<a id="monitoring-audit-2026-09-12--captured-implementation-evidence"></a>
#### Captured implementation evidence

- Telemetry layout (`implemented-telemetry.png`)
- Collector metric-pack picker (`implemented-collector-picker.png`)
- Narrow collector picker (`implemented-picker-narrow.png`)
- Calendar day view (`implemented-calendar.png`)

<!-- Original record: docs/qa/reviews/roadmap-lifecycle-property-matrix-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-lifecycle-property-matrix-2026-10-21"></a>

## Lifecycle property matrix — 2026-10-21


This [AUTH-C slice](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#auth-c--lifecycle-edges-and-property-level-parity) expands the generated [lifecycle parity matrix](../../qa/LIFECYCLE_PARITY_MATRIX.md) from entry-point presence to every AUTH-2 property, inventories the script producers outside the ordinary import routes, and closes the property gaps it found.

<a id="roadmap-lifecycle-property-matrix-2026-10-21--what-changed"></a>
### What changed

- **Property matrix.** Each of the nine AUTH-2 rows names, for each of the nine AUTH-2 properties (ownership and destination, warnings and provenance, secret references, capability and policy gates, pending edits, dirty navigation, delayed replies, exact resolved payload, durable reopen), the tests that prove it. `TestLifecycleParityPropertiesAreProven` checks every cell. A Go proof is a `pkg/api` test outside a build-tagged file that can skip neither itself nor through a test helper. A Vitest proof is a test under `dashboard/src` in a file that skips, focuses, conditions or marks todo no test at all. A Playwright proof runs in both e2e profiles. A shared cell defers to a row that proves the property directly (template and clone drafts, and debugger navigation inside the editor, use the shared script editor's guard and pending-edit handling). No cell is open.
- **Script producers.** `TestLifecycleParityScriptProducersAreInventoried` walks the controller package's call graph up from every call that adds a library script: the store entry points that reach a script insert and the runner's create cores. It follows calls and references, including package-level initializers, but treats a route registration's references to handlers as bindings. It continues past a handler that another handler calls. Every route it reaches is either an inventoried import route or has a producer row with its AUTH-2 row and a `pkg/api` test that requests the route with its method.
  - A function whose name is ambiguous must have its callers listed. That list is checked against every function that names the receiver type, or a struct field or function result that holds it, and calls the method. The graph discovers those fields and results itself.
  - A declaration that nothing calls must be classified.
  - In `pkg/storage`, every function that inserts into `scripts` is classified, and a walk of its calls and references admits only the listed store entry points, which are also where the controller walk starts. The `SaveScript` upsert may be reached only through its classified callers.
  - Outside the two packages, subpackages included, every production call to a store entry point, or to an exported function the walk reached, is classified. Today the only such call is the CLI's HTTP client.
  - The walk found eight producer routes outside `/api/v1/scripts/import/`: script create, authoring-session approval, both recorder conversions, the three library imports (the archive route refuses every non-result item) and GitHub sync jobs. The GitHub sync job was not in the earlier survey.
- **Recording leave guard.** The recorder panel registers an unsaved-work guard. A conversion preview, or a Salesforce draft, that differs from the review generation last saved is unsaved: leaving asks first, a save releases it, and a new conversion after the save is unsaved again. The panel's Dismiss asks the same question before it discards an unsaved review. The standalone recorder window sits inside the same guard provider, so closing or reloading it with an unsaved review asks the browser's leave question.
- **Scenario secret references.** In a scenario response (create, read, list, update, archive, runtime apply, and the authoring document), a runtime environment value that is only a project-secret reference (`{{secret.NAME}}`) names a secret rather than holding one, so it is returned as written, as a script read does. A controller-owned runtime reference (`APEX_RUNTIME_OVERRIDE_*`), which an editor cannot author, and every other value under a sensitive name or with a credential shape stay masked. The builder's checkpoint accepts only such references in an environment map, so a reopened scenario that bound a secret could not be checkpointed while the value read back as `[REDACTED]`; the checkpoint and the server's project-draft check now also accept the spacing the runtime accepts (`{{ secret.NAME }}`). Library export, sync-link and import-claim hashes, test reads, manifests and provenance keep the previous masking. A scenario create's stored original outcome (its receipt) now holds the editor form, references included, as a script's already does; no hash or comparison reads it.
- **New proofs.**
  - An import review, and a recording review, ask before they are left unsaved and release after their save.
  - A recording keeps the operator's rename through a late refresh that changes the recording environment, and saves it.
  - Debugging a stored script sends the edited, resolved draft, including an unblurred data cell.
  - A scenario's root and group secret references survive create, read and a controller restart, and a reopened scenario keeps them through the builder checkpoint and its save request.
  - A clone, a component insert and a library import each keep the source's import provenance.
  - The GitHub sync-job route itself creates the repository's script.
  - The library archive route refuses a script bundle.

  Each new component test failed against a temporary control that removed the behavior it covers.

<a id="roadmap-lifecycle-property-matrix-2026-10-21--validation"></a>
### Validation

| Gate | Result |
| --- | --- |
| `go test ./pkg/api -run 'TestLibrary\|TestGitHub\|TestCloneDraft\|TestScriptSecretReference\|TestLifecycleParity'`, the masking and scenario reference tests | Passed |
| Full `go test ./pkg/api` | Passed (773 s). The first full run found the AUTH-A lock-held write guard failure, fixed in its own commit |
| Temporary controls: a producer row removed, a missing test named, a handler delegating to a producer handler, a package-level dispatch table, a new storage create method, an unlisted sync-service caller, a struct field holding the sync service, a create call in a `pkg/api` subpackage | Each failed the build as intended; removed afterwards |
| Dashboard typecheck, ESLint on the changed files, and the changed Vitest files | Passed |
| The 14 Playwright specs the matrix cites, both profiles (dev-admin, local-auth) | 76 passed in each; one declared skip (the private LoadRunner acceptance archive) |
| `workflow-recording-review.spec.ts` after it began asserting the leave prompt with a bounded wait, both profiles | 2 passed in each |
| Documentation checks | Passed |

<a id="roadmap-lifecycle-property-matrix-2026-10-21--review"></a>
### Review

One reviewer read scratch snapshots only:

- Round 1: eleven findings, all fixed. Five were medium: storage classifications were unchecked text, the walk stopped at the first handler, package-level initializers were not walked, the GitHub sync-job proofs never created a script (and route mentions ignored the method), and the multi-script reopen proofs reopened nothing. The six low ones: a multi-script navigation proof that left nothing unsaved; three proofs too weak to stand (then open cells, now closed with new tests and the two product changes); a Vitest skip check that missed conditional skips; one-way stale checks; contradicting deviation bullets in the feature-completion spec; and duplicated helpers.
- Round 2: four findings, all fixed. The medium one: the store entry points and the walk's starting points were separate lists. The low ones: a library provenance proof that never checked provenance, holders that had to be named by hand, and smaller blind spots (subpackages, hard-coded outside names, storage method values, skipping helpers, `postman`/`postgres` helpers accepted as a POST).
- Round 3: four findings, all fixed. The medium one: unmasking references in the shared masking changed library export content and persisted hashes; the change is now confined to scenario responses. The low ones: the recorder panel's Dismiss bypassed the guard, the scenario reference proofs covered only the API, and nits (a misplaced comment, a spec bullet, a one-level skipping-helper check).
- Round 4: three low findings, all fixed: this record overstated that no stored content changed (the scenario create receipt stores the editor form), the builder's checkpoint refused the spaced reference form the editor now shows, and the recording spec would have waited for a leave prompt until the test timeout rather than fail clearly.

<a id="roadmap-lifecycle-property-matrix-2026-10-21--remaining"></a>
### Remaining

The import saves' persisted-copy check is still exercised directly rather than through each save's HTTP request. Seam retirement (AUTH-3) is unchanged.

<!-- Original record: docs/qa/reviews/roadmap-revise-copy-intents-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-revise-copy-intents-2026-10-21"></a>

## Revise-copy intent keys — 2026-10-21


The second [AUTH-A slice](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#auth-a--durable-user-intent-and-explicit-receipt-semantics) extends the [intent and receipt slice](INTEGRATION_AND_CODEBASE_REVIEWS_2026-10.md#roadmap-authoring-intent-receipts-2026-10-21) to run creates, so a queued run's revised copy has a draft-owned intent key. The contract is documented in [script lifecycle](../../SCRIPT_LIFECYCLE.md#retry-safe-creates).

<a id="roadmap-revise-copy-intents-2026-10-21--what-changed"></a>
### What changed

- **Keyed run creates.** `POST /api/v1/tests` accepts `Idempotency-Key` under receipt contract 1 only (contract 2 is refused with 400). The key fixes the run ID; a retry returns the run the first attempt created, as it is now, and adopts a run whose response and receipt completion were lost. A failure whose outcome cannot be confirmed answers 503 `test_create_outcome_unknown` and frees the lease for an immediate retry. With a caller-supplied `requested_test_id`, nothing is adopted: an existing run there answers 409 `test_identity_in_use`, and a refusal unrelated to that run keeps its original status. The generic duplicate-request fence no longer intercepts keyed run creates.
- **Deletion.** Deleting a run tombstones its keys in the delete transaction, so a retry answers 410 and never recreates it.
- **Credential fingerprints** use the same wrapped-key scheme as scenarios (shared helper).
- **Dashboard.** The revised-copy editor owns an intent key, sent through the shared intent helper. A replay is reported as "This draft already created run X (status); no new run was queued." Opening that run over later edits only asks to discard them. Save as new starts another run.

<a id="roadmap-revise-copy-intents-2026-10-21--validation"></a>
### Validation

| Gate | Result |
| --- | --- |
| API: keyed run create replay, change conflict, contract 2 refusal, deletion, lost-response adoption, unconfirmed commit, caller-chosen ID in use and unrelated refusal, revised copy with an unchanged source, real (trusted-header) authentication, OpenAPI | Passed, with the run-create, revision, receipt, idempotency, middleware, security and generated-contract suites |
| Storage, SQLite and PostgreSQL (`-tags postgres_integration`): delete-transaction tombstone and receipt suites | Passed |
| Dashboard typecheck and eslint on changed files; CreateTestModal, queued-revision and intent unit tests | 59 tests in 3 files passed |
| Playwright dev-admin and local-auth: `workflow-authoring-intent.spec.ts` (both tests) and `workflow-new-test.spec.ts` | 20 passed in each profile |

Playwright ran through the same Node 24 scratch copy of `scripts/run-e2e.sh` as the first slice (the pinned Node 22.23.3 is not installed here).

<a id="roadmap-revise-copy-intents-2026-10-21--review"></a>
### Review

One reviewer read scratch snapshots only:

- Round 1: four findings, all fixed. The generic idempotency middleware intercepted keyed run creates under real authentication (high). A possibly committed failure answered 400. A caller-supplied ID's unconfirmed attempt was over-promised. A replayed run was presented as new.
- Round 2: two findings, fixed. A refusal with an unrelated run at a caller-chosen ID answered 503. Opening a replayed run discarded later edits without a prompt.
- Round 3: one finding, fixed: saving from the leave prompt queued another run.
- Round 4: no findings (also covering the second Playwright test).

<a id="roadmap-revise-copy-intents-2026-10-21--follow-up"></a>
### Follow-up

The [cross-cutting AUTH review](INTEGRATION_AND_CODEBASE_REVIEWS_2026-10.md#roadmap-auth-cross-cutting-2026-10-21) refused keyed run creates that carry credential values where no key wrapper is configured (rather than digesting them unprotected), kept a replayed draft's key so creating again replays, and recalls a revised copy's key only for its own source run.

<a id="roadmap-revise-copy-intents-2026-10-21--remaining"></a>
### Remaining

As for the first slice: crash injection at each receipt/resource boundary, two-tab concurrent-click qualification, and both-profile browser coverage of the conflict, expiry and deletion paths remain open.

<!-- Original record: docs/qa/reviews/roadmap-scenario-prerequisite-2026-10-19/README.md; local evidence kept outside git -->
<a id="roadmap-scenario-prerequisite-2026-10-19"></a>

## Scenario prerequisite handoff — 2026-10-19


AUTH-C slice from the [implementation spec](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#auth-c--lifecycle-edges-and-property-level-parity).

Script save notifications now tell the scenario wrapper when authoring must stay
open. Source review returns, later edits and secret setup preserve the embedded
workflow while the confirmed script is assigned to the selected group. Later
saves update the library projection without adding another group. Ordinary
finished saves retain automatic return. Explicit return uses the existing dirty
navigation guards and preserves the scenario draft.

An archive review also stays mounted when its editor becomes a saved editor with
later changes. The source return survives subsequent saves and canonical reload
failure; save callbacks remain distinct from Java approval receipts.

Protected imports still require save-before-edit. The new browser case models a
legacy plain response from synthetic source archives and uses real HTTP saves.
No customer capture or converted private payload is included.

<a id="roadmap-scenario-prerequisite-2026-10-19--validation-and-review"></a>
### Validation and review

The complete dashboard cycle passed: typecheck, lint, 7,988 unit tests in 716
files, asset generation and both browser profiles. Dev-admin passed 320 tests
with 6 skips; local-auth passed 259 with 1 skip. Both exercised
the new prerequisite/late-edit/sibling test and the Help workflows. Skips remain
skips. Main was integrated through `5a9355e16` before the build.

Actual multipart HTTP import of the private HAR returned HTTP 200 with 476
request actions from 803 entries, 327 filtered assets, 479 assertions, 37
extractors, 43 correlations, four warnings and zero DSL validation issues.
All recorded structural fields match the retained baseline; the source hash is
unchanged. No private capture, converted script, traffic replay or library save
is included.

API embedded-document serving and generated lifecycle freshness passed. The
related QA dashboard spec-file count was regenerated from 89 to 90; this changes
an inventory count, not qualification status.

See the validation summary (`evidence/validation-summary.json`),
review rounds (`evidence/review.txt`), commands (`evidence/commands.txt`),
controller hashes (`evidence/browser-controller-hashes.json`),
staged source/asset hashes (`evidence/staged-source-and-asset-hashes.json`) and
HAR comparison (`evidence/external-har-comparison.json`). Compressed logs retain
failed fixture iterations and the superseded full cycle as well as the final
passing cycle. Checksums cover retained evidence.

Build caches remain available for the subsequent authorized slices. Their
cleanup is deferred until dependent session validation is complete.

<a id="roadmap-scenario-prerequisite-2026-10-19--remaining-scope"></a>
### Remaining scope

This closes the named prerequisite and later-edit lifecycle edges. Durable user intent,
contextual revisions, the complete property matrix and external qualification remain open. No adapter is retired here.

<!-- Original record: docs/qa/reviews/roadmap-validation-context-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-validation-context-2026-10-21"></a>

## Contextual validation revisions — 2026-10-21


This [AUTH-B slice](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#auth-b--contextual-validation-revisions) binds a script validation result to the controller context it depended on, beyond its document fingerprint and baseline revision. The contract is documented in [script lifecycle](../../SCRIPT_LIFECYCLE.md#validation-context).

<a id="roadmap-validation-context-2026-10-21--what-changed"></a>
### What changed

- **Validation context.** Both script validation endpoints return `validation_context`: an opaque revision and four keyed digests. Capabilities covers the authoring catalog and the browser runtime the script needs. Policy covers the domain feature policy, the project's authoring settings and features, and the system script-import policy. Approvals covers whether a Java Vuser approval is needed and still valid. Secret references covers each referenced secret (sorted, bounded at 256) with its availability and a keyed value digest. Contextual findings (`secret_unavailable`, `approval_required`, `approval_stale`, `capability_unavailable`) stay separate from static issues and from execution admission.
- **Failures are not changes.** A failed read of the project, a Java approval session, or the project or application secret stores and their scoping reads is an error, never a different context. A secret the resolver missed while a managed store holds it counts as such a failure. A Key Vault fallback reports no read errors, so its outage still reads as an unavailable secret (documented).
- **Probe endpoint.** `POST /api/v1/scripts/validation-context` returns the current context for a draft or a stored script (the latter at the validated revision, else 409), with the same project authorization as validation.
- **Editor.** The script editor re-reads the context for the result it holds on focus, on visibility and every 60 seconds while visible, one probe at a time, at most one every 5 seconds, each bounded at 30 seconds. A different revision makes the result stale like an edit, and the readiness card names what changed (for example "project secrets"). A controller that reports no context keeps document-only binding.

<a id="roadmap-validation-context-2026-10-21--validation"></a>
### Validation

| Gate | Result |
| --- | --- |
| API: secret availability and rebinding, project policy, unrelated settings, Java approval finding, probe/validation agreement, ambiguous probes, stored revision 409, read failures, inconsistent secret reads, trusted-header authorization | Passed, with the route, access-requirement golden, catalog, OpenAPI, generated-contract, validation, script and Java suites |
| Dashboard typecheck and eslint on changed files; session helpers, session hook (probe pacing, late answers, document-only binding), ScriptEditor (stale notice) | 276 tests in 4 files passed |
| Documentation checks | Passed |

Browser workflows were not rerun: the editor change is covered by component tests, and no Playwright spec exercises the probe.

<a id="roadmap-validation-context-2026-10-21--review"></a>
### Review

One reviewer read scratch snapshots only:

- Round 1: six findings, all fixed. Two were medium: read failures were reported as context changes, and the policy digest covered every project setting. The four low ones: duplicate probes, contradictory docs, a stored probe reading a newer revision, and untested authorization branches.
- Round 2: two low findings, fixed: secret failures outside the two managed stores, and a stalled probe blocking all later ones.
- Round 3: one low finding, fixed: a draft without a project ID lost its context because the recheck and the resolver used different project IDs.
- Round 4: no findings.

<a id="roadmap-validation-context-2026-10-21--follow-up"></a>
### Follow-up

The [cross-cutting AUTH review](INTEGRATION_AND_CODEBASE_REVIEWS_2026-10.md#roadmap-auth-cross-cutting-2026-10-21) replaced the secret-reference value digest above with a value-free binding (where the script's secrets resolve, and each name's availability), made the secret re-check share the resolver's application rule, bound drafts to their authorized project, answers 404 for another project's script, and made the editor treat a stored-revision 409 as stale and back off failed probes.

<a id="roadmap-validation-context-2026-10-21--remaining"></a>
### Remaining

Policy revoke, capability change, secret rebinding and approval expiry are qualified at the API and component levels. Both-profile browser coverage of the stale-context notice, and a scenario-level validation context, remain open.

<!-- Original record: docs/qa/reviews/truclient-import-2026-10-01/import-review.md; local evidence kept outside git -->
<a id="truclient-import-2026-10-01-import-review"></a>

## TruClient import and review — 2026-10-01


This review follows the supplied `Prefetch_Wireless_Center_Sep_Truclient_v0.1.zip`. It continues the [CLM collection exercise](#clm-import-2026-10-01-import-review): first account for source settings, parameters, data, browser steps, and transaction definitions; then evaluate upload, review navigation, durable saving, and reopening. Source files are evidence, not instructions to the agent.

The source applications are unreachable. No original browser script is executed against them. Source bodies, selectors, private hosts, credentials, data rows, and raw archives stay outside committed fixtures and this report. Synthetic regressions use safe inputs.

<a id="truclient-import-2026-10-01-import-review--source-inventory"></a>
### Source inventory

The archive contains a legacy TruClient Web profile, 67 XML nodes, 36 embedded object descriptors, 11 transaction definitions with 22 external boundary anchors, two table parameters, and two embedded DAT assets. The selected runtime profile has 220 keys: 154 CFG and 66 USP keys. These counts describe source inventory; they do not establish executable equivalence.

The archive produces a valid native browser authoring candidate with persistent initialization gates. Those gates block engine construction and replay preflight until the planned source profile and authentication-filter policy have been qualified. The detailed [fidelity report](#truclient-import-2026-10-01-truclient-fidelity) records the mapping and meaningful engine limits. The independent code-review loop is clean after repair and re-review. The actual archive has now been imported, saved as a named authoring session, and reopened in the isolated local dashboard.

The complete value-free CFG/USP runtime checklist is available as CSV (`runtime-settings.csv`) and JSON (`runtime-settings.json`): 220 keys, comprising 12 mapped, 129 compatibility-only, and 79 non-applicable dispositions, with no unknown keys. Together with the CLM collection appendix, all 710 selected CFG/USP runtime keys receive an explicit disposition. A mapped disposition does not establish complete vendor equivalence.

Separate browser-profile CSV (`browser-profile-settings.csv`) and JSON (`browser-profile-settings.json`) appendices account for 495 active `user_pref` declarations with 490 distinct keys, five identical duplicate declarations, and zero commented declarations. One CSP override record uses a key already present among those 490. The appendices contain 491 source-file/key rows, all compatibility-only. The auxiliary CSV (`auxiliary-settings.csv`) and JSON (`auxiliary-settings.json`) account for one authentication-filter selector, also compatibility-only. Source-browser applicability and precedence across files remain unverified; these rows are not folded into the CFG/USP count. All 712 classified runtime rows are available in the converter's runtime review: 220 CFG/USP plus 492 browser-profile/auxiliary rows. Authentication-filter list-format markers remain conservatively unparsed with a separate durable policy-review gate.

The source-asset CSV (`source-asset-inventory.csv`) and JSON (`source-asset-inventory.json`) reconcile all 27 ZIP entries: 25 regular files and two directories. All 67 project settings across 15 sections and 26 parameter-setting assignments across two definitions remain preserved in their separate source roles. The generated C wrapper has 44 distinct step keys matching 44 indexed XML nodes in source preorder. That correspondence establishes dispatch/order evidence, including disabled and alternative parents; it does not prove enabled replay or alternative branch selection.

<a id="truclient-import-2026-10-01-import-review--browser-flow-evidence"></a>
### Browser flow evidence

The audit follows the actual supplied archive through the isolated local dashboard and controller. Captures were saved, inspected for disclosure and settled rendering, then copied without altering their pixels. No source script was approved or replayed.

1. **Queue the source archive — Good.** Both supplied outer archives are retained in the queue with the intended save folder. The collection expands into independent projects while TruClient receives its own result.

   Both attached archives queued in the local import flow (`01-both-archives-queued.png`)

2. **Inspect runtime settings — Good, with category discovery friction.** All 712 imported runtime rows survive the API and durable draft. The initial HTTP category displays two additional APEX settings; selecting All categories exposes the complete table. Its 714 visible rows are presentation rows: mapped keys can share a draft field, compatibility keys can share a run-logic concept, one error-handler concept adds a non-applicable row, and four configured APEX fields add rows. The 27 unconfigured APEX defaults are hidden. These display totals do not replace the source-key appendix.

   Complete runtime categories and mapping totals (`02-runtime-all-categories.png`)

3. **Account for parameters and datasets — Good.** Two table parameters and two embedded DAT assets remain present, with 26 and two available rows respectively. The capture shows file and column metadata, not customer data rows.

   Two imported datasets and their row counts (`03-data-source-accounting.png`)

4. **Trace source evidence — Good, with disclosed limits.** The review exposes 64 mappings: 39 converted and 25 needing review. Two generated initialization gates have no direct DSL source origin. Twenty of 23 eligible source files have bounded previews; three omitted files and two truncated files produce five preview-limit notices. These limits concern the display. The complete supplied archive is retained separately in the private backup; the authoring session does not contain an immutable copy of every source byte.

   Source mapping accounting and bounded preview explanation (`04-source-mapping-accounting.png`)

5. **Save the review — Good; replay remains blocked.** The named `Prefetch_Wireless_Center_Sep_Truclient_v0.1` session contains 57 actions, two initialization gates, both parameters, both datasets, and all 712 runtime evidence rows. The current queue receipt separates three library scripts from one saved review session. Its one awaiting-save item is the freshly revalidated Java queue entry; that project's named authoring session was saved earlier and remains in the library.

   Named TruClient authoring session save confirmation (`05-truclient-review-saved.png`)

6. **Find the durable review — Good, with discovery friction.** Script Library → Authoring review exposes the named TruClient and Java sessions. The section is collapsed on entry. Its generic data-repair advice refers to two warning-level Unique Number wrap-policy reviews, with zero blocked data-fit checks. Both data-source bindings and all 28 rows persist; the advice does not indicate missing source data.

   Named TruClient and Java reviews in the library (`06-authoring-review-library.png`)

7. **Resume the review — Good; execution needs repair and evidence.** Resume reopens the complete draft with “Saved: up to date”, the source profile gate, parameter-file review, and runtime/source panels. The editor reports seven issues for three unresolved browser-window operations and two targetless waits. These authoring checks are distinct from the converter's Go DSL validation and are not a claim that the source files were lost. The generic Create Script title makes the resumed state less obvious. Validation/debug evidence is absent; no Debug or Approve action was taken.

   Saved TruClient draft reopened with explicit authoring issues (`07-resumed-truclient-review.png`)

<a id="truclient-import-2026-10-01-import-review--verification-and-limits"></a>
### Verification and limits

Affected converter, source-preview, DSL, browser-engine, protected API, and focused dashboard checks pass. The collection save/reopen Playwright workflow passes in both dev-admin and local-auth profiles without automatic retries. Both TypeScript configurations, scoped lint, generated API contracts, and the reviewed controller build pass. The final full Go rerun passes all 117 tested packages after a stale HTTP/browser-device golden expectation was corrected. An unchanged metrics timing test passes five isolated repetitions and the final full engine suite after its first full-run failure. Documentation and final whitespace checks also pass. Importing and reopening a review artifact establish durable authoring evidence; they do not establish successful target replay, approval readiness, or full accessibility compliance.

The private snapshot is stored under `artifacts/import-reviews/2026-10-01/` (Git ignored, restricted file permissions). It contains the online SQLite backup, import asset storage and key material, destination metadata, and both original archives. SQLite integrity passes. No temporary file references occur in the saved script data-source metadata or named review drafts; this is a local backup, not a production deployment.

<a id="truclient-import-2026-10-01-import-review--validation-before-publication-to-main"></a>
### Validation before publication to main

The reviewed task changes were isolated onto main commit `1e4fba788969546ba141a8cdae238aca20179df5`. Independent scope reviews found no remaining actionable findings and confirmed that concurrent diagnostics, analytics, and recorder changes were preserved. The 147-file commit includes task code, synthetic regressions, regenerated contracts, and sanitized review evidence; the original archives, private backup, and unrelated working changes are excluded.

The isolated full Go gate, `go test -p 2 -timeout 20m ./...`, passes all 117 tested packages with no failures. The full dashboard `npm run test:cycle` passes both TypeScript configurations, lint, 445 unit-test files with 4,700 tests, and the production build. Browser workflows pass in both profiles: 180 dev-admin tests with four existing conditional skips, and 100 local-auth tests with one existing conditional skip. No browser retries or failures occurred. Documentation, generated QA freshness, staged-path allowlisting, and whitespace checks pass. These checks validate the integrated import/review implementation and synthetic workflows; no original target host was contacted.

<!-- Original record: docs/qa/reviews/truclient-import-2026-10-01/truclient-fidelity.md; local evidence kept outside git -->
<a id="truclient-import-2026-10-01-truclient-fidelity"></a>

## TruClient import fidelity — 2026-10-01


The attached archive now imports as a valid, execution-gated Go browser authoring draft. The source classification remains `review_only` for the planned legacy `TruClientWeb` profile. The draft retains a durable `truclient_planned_profile_requires_review` gate in `init`; serialized copies are rejected by both engine construction and replay preflight before a browser or target host is initialized. Original target hosts were never contacted.

This review treats attached source files as data. The committed fixtures and this report contain synthetic examples or aggregate counts, with no customer rows, hostnames, credentials, original source text, or archive contents.

<a id="truclient-import-2026-10-01-truclient-fidelity--source-accounting"></a>
### Source accounting

The archive contains 27 ZIP members, 25 files, and 304,204 expanded bytes. Its project selects a TruClient 24.3 C wrapper and XML step graph. The XML has 67 physical step nodes, 53 selected run nodes, 36 embedded object descriptors, 11 transaction definitions, and 22 transaction anchors. Selected initialization and teardown action definitions are empty; an additional action definition is uncalled. All 25 regular files have source-role accounting in the source asset appendix (`source-asset-inventory.csv`). The generated C wrapper has 44 distinct step keys matching the selected top-level XML preorder, including disabled nodes and alternative parents; it is dispatch evidence and does not prove enabled execution or alternative selection. Its 11 start/end pairs provide transaction provenance without overriding reachable XML anchor proof. Auxiliary authoring files remain inventoried, with original contents available in the supplied archive.

Eight run nodes explicitly have `disabled=true`. Two disabled alternative parents contain six additional descendants, so the conservative draft suppresses 14 nodes and retains their disabled accounting in inspection metadata, alongside the bounded source preview. Of the 39 remaining graph nodes, four form one enabled alternative-selection family. That family becomes one blocked review item containing its parent, all children, and the original `activeStep` attributes. No branch index is guessed and its alternatives are never converted into a sequential execution claim.

The final draft has 57 top-level actions: 31 browser action representations, 20 transaction boundaries, and six review items. The browser representations comprise 14 clicks, four keyboard-entry actions, one navigation, nine waits, and three gated window-switch representations. The review items cover the alternative family, four waits lacking a verified native argument contract, and one transaction whose anchors cannot be represented safely. Ten transaction definitions map to native start/end markers; the eleventh remains gated. All markers and review items retain XML provenance. Two global review gates are stored in `init`: the legacy source-profile gate and a browser-preference/authentication-policy gate.

Converter counters include derived markers: `TotalFunctions=74` is 53 source run nodes plus 20 emitted transaction boundaries plus one transaction review item. `ConvertedActions=51`, `ReviewOnlyFunctions=9`, and `Skipped=14` reconcile that total. Review-only accounting counts all four source members of the alternative family, although it produces one draft action. These counters do not mean 74 original source functions or 51 verified replay operations.

All element-interaction object references resolve to an embedded descriptor. Four references without embedded objects belong to navigation/window actions; navigation uses its Location argument, while the three window operations remain gated for identity and lifecycle review. All 36 descriptors retain XPath candidates.

<a id="truclient-import-2026-10-01-truclient-fidelity--settings-and-parameters"></a>
### Settings and parameters

Every selected runtime key is preserved and classified: 154 CFG keys plus 66 USP keys, 220 total, with zero unaccounted keys. The final dispositions are 12 `mapped`, 129 `compatibility_only`, and 79 `non_applicable`. The value-free per-key appendix (`runtime-settings.csv`) records each source file, section, key, disposition, target path, and reason; JSON is also available (`runtime-settings.json`).

| Source settings | Go disposition |
| --- | --- |
| StepTimeout = 180 seconds | Bounded `actions[].timeout_ms=180000`; known completion synchronization receives the same step budget. |
| ObjectTimeout = 20 seconds | Selector/object budgets use 20000 ms; one positive explicit per-step override is retained and applied. |
| SimulateNewUser | Mapped to `config.browser.new_user_per_iteration`; the whole-profile gate still requires verification of browser identity and exact source reset behavior. |
| Selected run iterations and pacing | Effective iterations = 1, sequential run logic, source ASAP pacing. Initialization/teardown and uncalled phase scaffolding remain separately accounted for. |
| Source logging and automatic transaction selectors | Preserved through existing protocol-neutral config mappings. Explicit XML transaction definitions remain independently accounted for. |
| Saved window width/height | Preserved as inactive because source `ApplyWinSizeByUser` is disabled; no viewport identity is inferred. |
| UseRecordedDuration = 0 | Inactive source replay timing. All recorded timing attributes remain preserved. |
| Network shaping selectors | Disabled source selectors are non-applicable; the draft does not advertise the HTTP engine's network shaping as a Go browser capability. |
| Other browser/network/cache/security/preferences | Preserved per key with compatibility review. Native browser identity, network completion, DNS/cache/connection policy, source step pacing, and browser-specific preferences require verified replacements before promotion. |

The project preserves all 67 USR setting assignments across 15 sections. Browser profile settings are additional to the 220 CFG/USP keys: `user.js` contains 495 active literal preference declarations for 490 distinct keys, including five same-value duplicate declarations. Each parsed declaration line is retained in the value-free inventory; the last parsed line is source evidence, without claiming effective runtime values. The current literal preference and JSON override files contain no unparsed content. `user_modified_perfs.json` contributes one CSP override row for a key also declared in `user.js`; neither override precedence nor locking semantics is inferred. TCIE authentication policy contributes one `General.FilterType` row; six populated list records (four AllowList and two DenyList) retain value-free section/line evidence as unparsed, compatibility-only policy data. No filter-policy inactivity or native Go equivalence is claimed. These 492 file/key rows are all `compatibility_only` and are navigable in the existing Runtime settings table with per-file paths. Preference values are not copied into public inventory metadata. The browser preference appendix (`browser-profile-settings.csv`), JSON inventory (`browser-profile-settings.json`), and authentication setting appendix (`auxiliary-settings.csv`) provide sanitized checklists. The parser accepts literal `user_pref` calls and executes no JavaScript. It bounds input to 2 MiB/10,000 declarations per file and preserves a durable review gate for unknown, default-branch `pref`, dynamic, malformed, or over-limit input. JSON override and INI key locations are recorded from actual source lines; duplicate override keys/subfields are preserved as ambiguous evidence. Authentication assignments and list records share the 10,000-record bound; list content remains opaque and is never exposed as public setting names or values.

Two table parameters retain all 26 source parameter setting assignments. Both import as file parameters with unique selection, an update per iteration, start row 1, and wrap-on-exhaustion behavior. Two data assets retain 26 and two records respectively, 28 total. Both XML `param=true` references now use the canonical `{{param.*}}` namespace instead of becoming literal source parameter names. Parameter/data-source mapping produces no warnings or allocation blockers.

<a id="truclient-import-2026-10-01-truclient-fidelity--browser-semantics"></a>
### Browser semantics

Positive Select ordinals now have a bounded native mapping: source ordinal 1 selects Go option index 0. Native `browser_select.params.option_index` accepts 0–999999 and rejects fractional, negative, oversized, or mixed value/index requests. Source random ordinal 0 and Text-plus-Ordinal combinations remain gated. In this archive, Select nodes belong to disabled or alternative families; the new primitive supports subsequent reviewed repair without claiming those families can replay automatically. OpenText documents both the one-based source index and the matching-text occurrence rule. [Official Select arguments](https://admhelp.microfocus.com/vugen/en/26.1-26.3/help/WebHelp/Content/TruClient/functions_step_arguments.htm).

Wait Interval and Unit are preserved in native duration representations when explicit. Milliseconds and seconds remain distinct. Source Wait intervals default to think-time behavior; a fixed native wait therefore carries an additional policy-review gate unless the source explicitly disables that behavior. Four argument-incomplete waits remain review items. [Official Wait arguments](https://admhelp.microfocus.com/vugen/en/26.1-26.3/help/WebHelp/Content/TruClient/functions_step_arguments.htm).

Type maps to the existing keyboard-entry mode, retaining explicit typing intervals. A positive source interval is an average, while native delay is fixed, so that approximation remains gated. Explicit typing without clearing also remains gated. [Official Type arguments](https://admhelp.microfocus.com/vugen/en/26.1-26.3/help/WebHelp/Content/TruClient/functions_step_arguments.htm).

The source has 33 explicit completion rules, 31 on individually enabled nodes; three optional flags, two on individually enabled nodes; 27 zero manual-duration attributes; and 24 positive recorded-duration attributes. Optional and unfinalized behavior is preserved with explicit gates. Zero manual durations introduce no additional duration gate. Known Object Exists completion means visibility and maps to `element_visible` with the step timeout; four reachable completion objects produce those synchronization policies. Source Step network completed remains gated because its network rule is not proven equivalent to Playwright network-idle behavior. Tab activation with missing qualified identity and tab close lifecycle remain gated. [Official completion rules](https://admhelp.microfocus.com/vugen/en/26.1/help/WebHelp/Content/TruClient/_tc_lp_step_structure.htm).

The linked public documentation is newer than the attached 24.3 source profile. The profile gate explicitly prevents treating those newer descriptions as complete historical replay proof.

<a id="truclient-import-2026-10-01-truclient-fidelity--source-navigation-and-verification"></a>
### Source navigation and verification

The source pane now decodes recognized UTF-16 TruClient XML before applying its bounded display limit. The actual default XML displays with zero NUL characters and retains its original byte count of 230,340. USP run logic is included as navigable INI source. The bounded source review offers 20 displayed files, reports 23 eligible files and three omitted files, and retains its truncation indicators. Mapping-referenced files are prioritized within this unchanged display budget so preference/CSP evidence remains accessible before auxiliary files. The final conversion has 64 source mappings, including 57 XML-to-draft links and three browser-profile configuration links. When multiple `default.xml` members exist, conversion and source mappings share a deterministic selected path, and a durable ambiguity gate requires explicit source-project selection.

Retention has a precise limit: the saved draft includes bounded SourceReview content, structured step evidence, data/parameter definitions, and value-free complete setting inventories. ReviewArtifact includes the source filename manifest and inspection metadata; it does **not** store an exact immutable ZIP bundle. Complete source bytes and declarations remain in the supplied archive/private review inputs, which the operator must retain separately. Displayed files can be truncated or omitted with explicit indicators; no full-archive durability is claimed for an authoring session.

Actual isolated-controller validation returned HTTP 200 in 5.49 seconds with one browser candidate, 57 actions, two global gates, and all 712 classified runtime file/key rows. The runtime table intentionally presents draft fields and source concepts as well as individual keys, so its group totals differ from the source checklist. Eight mapped keys become six shared field rows, producing 10 mapped table rows from 12 source keys. Three compatibility keys become two run-logic concept rows, producing 620 preserved rows from 621 source keys. An additional inactive error-handler concept produces 80 non-applicable rows from 79 source keys. This gives 710 source/concept presentation rows. The 31 additional APEX settings comprise four configured fields and 27 unconfigured fields hidden by default, so the default All categories navigation count is 714. Choosing the default HTTP category shows only two configured HTTP fields; choose All categories or the source-browser categories to review the complete inventory. These presentation counts do not indicate source settings were lost.

The actual saved and reopened authoring draft was independently compared with the API candidate. Config, 57 actions, two global gates, two parameter definitions, and both inline data assets match exactly. The assets still provide 26 and two data rows, and both parameter-to-data-source bindings remain present. Read-only API validation reports 31 issues, all named import-fidelity blockers, with no additional structural issues. Authoring health retains replay and validation blockers from the planned profile and semantic gates. Data-source fit has zero blocked checks and two warning-level checks: source unique allocation wraps on exhaustion, so duplicate reuse needs workload review despite adequate rows for the current one-iteration model. The generic next-action text about repairing data-source bindings refers to those warning checks; it does not indicate missing assets or import loss.

Private conversion validation returned zero DSL issues and 31 explicit fidelity blockers, with two global replay gates. CFG/USP220, project67, parameter26, and browser/authentication492 file-key rows all reconcile with their source inventories. The complete affected package suite passed:

```text
GOCACHE=/private/tmp/apex-clm-go-cache GOMAXPROCS=4 go test -p 2 \
  ./pkg/convert/sourceview ./pkg/convert/trueclient \
  ./pkg/convert/enterprisearchive ./pkg/dsl ./pkg/protocol/browser
```

Meaningful regressions cover disabled descendants, alternative families, canonical parameter references, ordinal bounds and ambiguity, Wait units, typing semantics, completion visibility and missing objects, transaction ordering/duplicate/missing/reversed anchors, all-key runtime accounting, source UTF-16 decoding/display bounds, serialized draft retention, and engine/replay preflight rejection. A disposable localhost browser fixture verifies both label selection and actual option-index selection. Independent review added verification/control-step preservation, disabled Else handling, same-context completion checks, source duration-wait budgets, exact Unique-format validation, repeatable XML selection/provenance, literal-only browser preference accounting, partial/default-branch evidence, duplicate JSON/INI declarations and exact key lines, unknown-content/size limits, and mapped-source display prioritization. A delayed-visible iframe fixture proves completion synchronization can use a longer step timeout than the trigger's object timeout. `go generate ./pkg/dsl` completed and generated schema/reference freshness checks passed. No replay evidence against the original application is claimed.

<!-- Original record: docs/qa/reviews/ux-reviews-2026-09.md; local evidence kept outside git -->
<a id="ux-reviews-2026-09"></a>

## UX reviews with open items (September 2026)


Status: Open. Both reviews still have unimplemented recommendations.

The `command-center-usability-2026-09-09/` and `heatmap-audit-2026-09-14/` folders next to this file hold the screenshots. Completed UX reviews are in [the consolidated September audit record](AUDITS_2026-09.md).

<a id="ux-reviews-2026-09--command-center-usability-review"></a>
### Command Center usability review

Date: 2026-09-09. Status: Open: the implementation follow-up covered the Details navigation and zero-error attribution only; the remaining original audit recommendations are unchanged

The Command Center has useful operational depth, but its visual hierarchy makes a healthy run look busier and more concerning than it is. Preserve the controls and diagnostics; simplify their presentation and fix the places where content becomes obscured.

Evaluated September 9, 2026 against the current local development dashboard, including existing uncommitted changes. Used a real three-group HTTP run with one VUser per group and one-second pacing against the local controller health endpoint. No application code changed. The audit run was stopped after capture; its records remain available for inspection.

<a id="ux-reviews-2026-09--implementation-follow-up"></a>
#### Implementation follow-up

The two requested follow-up changes are implemented:

- Details is now Advanced run details and opens the Overview tab of the advanced execution workspace. Back to Command Center explicitly targets the same run, including after a reload or run completion. During an ordinary details visit, the cockpit stays mounted so staged load values, table filters, and graph selections survive; keyboard focus returns to its entry button. Draft state is in memory and does not survive a full page reload.
- Highest Error Rate now shows neutral No errors with no group attribution when sampled groups have zero errors. No samples remains separate. Actual small error rates display <0.01% instead of rounding misleadingly to zero, positive ties name the tied groups, and unavailable group attribution is distinguished from zero errors. Overall zero-error styling is neutral as well.
- The summary and action row have separate space so the longer navigation label does not squeeze No errors into an ellipsis. Full metric values are also available in their titles.

Validation: type checking, lint, and the embedded build passed; 368 unit-test files passed (4,025 tests). The dev-admin suite passed 79 checks initially; the new details check failed on a case-sensitive test selector, then passed on its isolated rerun after correction. All 10 local-auth checks passed. The full cycle command therefore recorded a nonzero initial browser result, with its only failing check subsequently resolved and reverified. No unresolved failures remain from these checks.

The remaining original audit recommendations are unchanged. This implementation does not remove the advanced workspace or any of its controls.

Browser checks confirmed staged load and filter retention, Graph Wall retention, and focus restoration. Regression coverage includes the same-run navigation path and reload return, zero errors, no samples, positive errors, ties, small nonzero rates, and the return action for completed runs.

Implemented Graph Wall return (`command-center-usability-2026-09-09/12-implemented-graph-return.png`)

Implemented advanced return action (`command-center-usability-2026-09-09/13-implemented-details.png`)

<a id="ux-reviews-2026-09--priority-findings"></a>
#### Priority findings

| Priority | Evidence | Finding | Recommended change |
| --- | --- | --- | --- |
| High | 9–11 | Details unexpectedly opens a second execution workspace with repeated live controls and a generic Back button. The destination and return path are unclear. | Rename to Advanced run details, add a prominent Back to Command Center link bound to this run, and preserve the originating mode, filters, and staged edits. Consider an in-context details drawer while retaining the complete advanced workspace. |
| High | 5 | The graph configuration drawer is transparent enough that underlying labels, cards, and buttons overlap its contents. Reopening confirmed this was a settled state. | Use an opaque surface and a separate backdrop. Preserve every graph toggle and Reset action. |
| High | 7–8 | The fixed page header covers the upper portion of the run shelf after scrolling, partially obscuring run identity, summary, and controls. | Give the run shelf a correct sticky offset, or combine the two headers into one persistent region. Verify keyboard focus is never covered. |
| High | 1 | Narrow group cards lose visible metric headings. Numbers such as 1 and 1.0 appear without nearby Current VU and TPS labels. | Give each responsive metric a visible label and unit; use a compact labeled grid. |
| High | 4 | Graph titles and legends truncate; the first chart's controls crowd out its title. Meanwhile substantial card height is unused. | Protect a dedicated title row, move secondary toggles into chart options, size plots to available height, and wrap or expand legends. Use two columns when three cannot fit readable headings; retain the user's dense layout option. |
| Medium | 1–2, 4, 9 | Highest Error Rate incorrectly attributes a healthy zero-error tie to Browse; this is misleading group attribution as well as excessive red emphasis. | When valid samples have zero errors, show No errors with no group name and neutral styling. Name a group only when it has actual errors. Keep No samples distinct from zero errors, handle positive ties explicitly, and retain all group metrics. |
| Medium | 2 | Current/target, planned/actual/delta, and allocated/projected VUsers occupy several prominent panels. Empty error detail and explanatory copy add more weight. | Establish one primary run summary, compress capacity to a status row, and make the load model expandable while retaining a small trend preview. Expand exceptions when relevant. |
| Medium | 2 | The Scenario Run table has 17 columns; even on the wide capture, latency and errors require horizontal scrolling. | Default to Name, Group, Current/Target VU, TPS, Error %, and p95. Preserve every column in a column picker and a full-detail preset; freeze identity columns. |
| Medium | 3 | Runtime Settings and Parameters are reachable through the group name, but that name lacks a clear menu indicator. | Add a visible chevron or a labeled group-actions button. Keep the same menu destinations. |
| Medium | 2, 4–5 | Graph configuration is split between Configure Graphs, the sidebar, and per-chart actions. TPS and Change duration disappear from the top shelf in Graph Wall. | Keep a stable run summary and intervention shelf across both modes. Group chart selection and display options in one organized drawer; retain per-chart actions. |
| Medium | 7 | With no filter entered and no errors, the table says no patterns match the current filter and shows a second empty evidence panel. | Distinguish No errors recorded from No matching errors. Show the evidence pane only after selection; keep diagnostics one click away. |
| Medium | 8 | Infrastructure combines capacity and route rows; WARNING badges have no immediate explanatory reason in the visible columns. | Show the reason beside each warning and distinguish unknown telemetry from confirmed pressure. Preserve all route and capacity metrics. The underlying warnings were not independently validated. |

<a id="ux-reviews-2026-09--functionality-preserving-organization"></a>
#### Functionality-preserving organization

| Always visible | Available on demand |
| --- | --- |
| Run name, state, current/target load, remaining time, TPS, error rate, connection freshness | Full run metadata and details |
| Pause/Resume, Stop, distinct Abort, duration control, Notes | Existing confirmation and editing dialogs |
| Operations / Graphs switch in one stable location | Existing graph categories and configuration |
| Compact group health rows and explicit target editing | Slider, immediate/gradual ramp controls, staged values and Apply, runtime settings, parameters |
| Small load-profile preview and capacity status | Full profile, planned/actual/delta values, allocation/projection and generator details |
| Scenario, Errors, Infrastructure tabs | All existing columns, percentiles, signatures and transaction evidence |

Retain the purple accent, group colors, and established components. Reduce decorative gradients, repeated card borders, unnecessary status prose, and fixed empty space. Keep errors discoverable with a count/status indicator even when the detailed empty panel is collapsed. Any disclosure change should preserve staged inputs when switching views.

<a id="ux-reviews-2026-09--capture-walkthrough"></a>
#### Capture walkthrough

<a id="ux-reviews-2026-09--1-open-a-running-test-in-the-narrow-layout-needs-improvement"></a>
##### 1. Open a running test in the narrow layout — needs improvement

Live status, current/target load, remaining time, and intervention buttons are available. Group metrics lose visible labels, the highest-error group truncates, and stacked panels push diagnostics below the initial viewport. Small supporting text is an accessibility risk.

Narrow live run (`command-center-usability-2026-09-09/01-live-narrow.png`)

<a id="ux-reviews-2026-09--2-inspect-operations-on-a-wide-layout-usable-but-crowded"></a>
##### 2. Inspect Operations on a wide layout — usable but crowded

Group colors and current/target controls help orientation. Repeated VUser values, red zero-error tiles, a large empty error panel, and a horizontally scrolling detail table compete for attention. The group button shows a clear focus outline after dismissing its menu.

Wide operations view (`command-center-usability-2026-09-09/02-live-desktop.png`)

<a id="ux-reviews-2026-09--3-open-group-actions-functional-weak-discoverability"></a>
##### 3. Open group actions — functional, weak discoverability

Runtime Settings and Parameters are available in a compact menu. Escape dismisses the menu and returns focus to the group button. The closed trigger needs a visible affordance. Editing these settings was not exercised.

Group actions menu (`command-center-usability-2026-09-09/03-group-menu.png`)

<a id="ux-reviews-2026-09--4-switch-to-graph-wall-needs-improvement"></a>
##### 4. Switch to Graph Wall — needs improvement

Live charts, group selection, expansion controls, graph categories, and display options are present. Three-column cards truncate titles and legends. The first chart's options dominate its heading. TPS and duration adjustment disappear from the shared run shelf. Accessible chart measurement tables and named expansion/reorder controls are exposed in the accessibility tree, a useful foundation to retain.

Graph Wall (`command-center-usability-2026-09-09/04-graph-wall.png`)

<a id="ux-reviews-2026-09--5-open-graph-configuration-readability-defect"></a>
##### 5. Open graph configuration — readability defect

Graph selection and Reset are discoverable, and focus enters on Close. The drawer's background allows underlying content to remain visible through labels and options. This is a concrete readability problem, not a preference about visual density. Graph settings were inspected without changing them.

Graph configuration drawer (`command-center-usability-2026-09-09/05-graph-configuration.png`)

<a id="ux-reviews-2026-09--6-inspect-pause-confirmation-generally-clear"></a>
##### 6. Inspect Pause confirmation — generally clear

The dialog names the run and explains the action boundary and resumability. Cancel initially receives focus. Preserve that specificity. Button contrast deserves measurement. The dialog was cancelled; pause/resume execution was not tested.

Pause confirmation (`command-center-usability-2026-09-09/06-pause-confirmation.png`)

<a id="ux-reviews-2026-09--7-open-error-details-and-scroll-needs-improvement"></a>
##### 7. Open Error Details and scroll — needs improvement

Error patterns and evidence share one tab, which is useful. An unfiltered healthy run receives filter-related empty copy and two large blank regions. The page header overlaps the sticky run shelf. Populated signatures, evidence retrieval, and failure recovery were not tested.

Error Details and header overlap (`command-center-usability-2026-09-09/07-error-details.png`)

<a id="ux-reviews-2026-09--8-open-infrastructure-useful-detail-unclear-warning-context"></a>
##### 8. Open Infrastructure — useful detail, unclear warning context

Capacity and route information are available with status text as well as color. Warning reasons are not apparent in the visible columns, and horizontal scrolling separates identity from later details. The same header overlap persists.

Infrastructure (`command-center-usability-2026-09-09/08-infrastructure.png`)

<a id="ux-reviews-2026-09--follow-up-details-navigation-and-zero-error-attribution"></a>
#### Follow-up: Details navigation and zero-error attribution

A second local HTTP run with two one-VUser groups was used for this follow-up. It was stopped after capture. Only review documentation and screenshots changed.

<a id="ux-reviews-2026-09--9-locate-details-and-inspect-highest-error-rate-misleading-label-and-attribution"></a>
##### 9. Locate Details and inspect Highest Error Rate — misleading label and attribution

Details is a small generic action beside Notes and duration controls. It does not explain that it leaves Command Center for a different full execution workspace. The accessibility tree again reports Highest Error Rate as 0.00% · Browse while both groups have zero errors; the screenshot also shows how severely the compact card truncates this content.

Source inspection confirms that `highestGroupErrorRate` chooses the first group even when every rate is zero. That is a ranking artifact, not an unhealthy group. Require actual recorded errors before attributing a group. Do not simply test the formatted percentage: a small nonzero rate can round to 0.00%, so show adequate precision or an error count in that case. For positive ties, say that multiple groups are tied or make the tie discoverable rather than implying a unique worst group.

Details entry (`command-center-usability-2026-09-09/09-details-entry.png`)

<a id="ux-reviews-2026-09--10-follow-details-to-the-execution-workspace-confusing-context-switch"></a>
##### 10. Follow Details to the execution workspace — confusing context switch

The route changes from the run's Command Center to its `/test/` workspace. In this session the Graphs tab was selected, with expanded VUser scaling and duration controls filling the initial viewport. The destination repeats operational controls, including Pause VUsers and Pause, and uses a different information hierarchy. It feels like another runtime dashboard rather than a focused details view.

A Back button does exist at the upper left of the action row. Its generic text, low prominence, and placement among operational actions do not clearly promise a return to this run's Command Center. This is a discoverability and orientation issue; the captured build does not lack a return control entirely. Keep the full advanced capabilities, but label the destination and give navigation its own clearly named return link. A focused details drawer could serve common inspection needs, with Advanced execution workspace available explicitly.

Details destination (`command-center-usability-2026-09-09/10-details-destination.png`)

<a id="ux-reviews-2026-09--11-use-back-return-works-in-the-inspected-path"></a>
##### 11. Use Back — return works in the inspected path

Clicking the page's Back button returned to the same run's Command Center with its application/release scope. This verifies the direct return path in this local build. It does not establish retention of Graph Wall mode, filters, scroll position, or staged load edits, nor behavior after deep links, reloads, or intermediate navigation. Those must be regression cases.

Return from Details (`command-center-usability-2026-09-09/11-details-back.png`)

Follow-up acceptance criteria: Details announces its destination; a visible Back to Command Center action always targets the same run; keyboard focus returns to the Details trigger; the original view and draft inputs survive; zero-error runs never name a highest-error group; no-sample and real low-rate-error states remain distinct. Cover both Operations and Graph Wall entry, deep links, reloads, positive ties, and run completion while viewing details.

<a id="ux-reviews-2026-09--evidence-limits-and-acceptance-criteria"></a>
#### Evidence limits and acceptance criteria

This is a design inspection of a low-load, healthy HTTP run in the dark theme, not an operator study or a complete functional/accessibility certification. Captures cover narrow and wide rendered layouts. A temporary viewport override produced incorrectly scaled captures; those were rejected and replaced after reset. No findings rely on that capture artifact.

Not covered: large group counts, real error evidence, missing/disconnected streams, other themes, full keyboard traversal, screen-reader announcements, measured contrast, backend correctness of infrastructure warnings, and successful changes to duration/load/pause/abort. The accessibility tree also exposes prototype-oriented names such as Grafana wall mockup and Final graph configuration; replace them with user-facing names.

For a follow-up implementation, preserve all existing capabilities and verify: no overlapping sticky headers; readable titles, labels and key values at both widths; opaque drawers; all prior metrics reachable; stable run controls across modes; staged edits survive navigation; neutral healthy states and explanatory warnings; keyboard focus remains visible. Add meaningful regression tests and run the repository's full dashboard test cycle, including both browser workflow profiles.

Recommended next step: fix the obscured controls, transparent drawer, and missing/truncated labels before reorganizing the broader layout.

<a id="ux-reviews-2026-09--implementation-interface-cleanup"></a>
#### Implementation: interface cleanup

Implemented the remaining recommendations in the existing Command Center:

- The sticky run shelf measures the wrapping application header. TPS and duration controls remain available in both views.
- Graph selection, layout, time display, and reset share one opaque configuration drawer. Chart titles wrap on their own row; secondary chart toggles use a disclosure. The dense grid adapts to available width and card heights follow their content.
- Responsive group values have visible labels and the group actions trigger has a chevron.
- Scenario Run starts with seven essential columns. Individual column selection and a full 17-column preset retain every metric. Name and Group remain pinned while scrolling.
- Capacity is a compact disclosure, expanded for pressure. Missing generator counts display as unavailable instead of invented defaults. The load trend stays visible with its numerical comparison on demand.
- Healthy error tables say “No errors recorded”; filtered results say “No matching errors.” Evidence appears only for a visible selected signature. Warning reasons sit beside infrastructure status badges.
- Filters, sorting, column selection, and graph category/display mode survive Operations/Graph Wall switches within the same run. Filter text is independent per table. Presentation state resets for another run.

Code review corrected the inherited wide table minimum, pinned-header stacking, run-specific presentation state, and unavailable capacity values. Existing run controls, group settings, graph actions, metrics, and diagnostics remain available.

<a id="ux-reviews-2026-09--review-and-validation"></a>
##### Review and validation

The final diff review additionally corrected VUser projections for TPS-based runs, delayed error-signature messaging, and keyboard focus spacing including the scroll container's padding. The observer also handles the run shelf appearing after asynchronous loading. No unresolved findings remain from this review.

`npm run test:cycle` was run, covering type checking, lint, 4,027 unit tests across 368 files, the embedded production build, 81 dev-admin workflows, and 10 local-auth workflows. An initial complete cycle passed. The strengthened narrow-screen focus assertion in the subsequent cycle exposed a small overlap; that implementation was corrected and both Command Center browser tests passed on a fresh build. All other browser workflows passed. The error-signature edge case also passed the focused 68-test Command Center suite. Desktop, narrow, and drawer screenshots were visually inspected.

Latest evidence is saved below. These are low-load HTTP smoke runs; they validate the interface and navigation rather than production-scale performance.

Operations with essential columns (`command-center-usability-2026-09-09/14-clean-operations.png`)

Narrow group metrics and visible keyboard focus (`command-center-usability-2026-09-09/15-clean-narrow.png`)

Opaque unified graph configuration (`command-center-usability-2026-09-09/16-clean-graph-configuration.png`)

<a id="ux-reviews-2026-09--load-impact-heatmap-2d3d-operator-experience-review"></a>
### Load impact heatmap (2D/3D) operator-experience review

Date: 2026-09-14. Status: Open: audit only, no implementation record

Reviewed 2026-09-14. Verdict: retain 2D as the default operational view; treat 3D as optional exploration until its scale, motion, labeling, and interaction controls are explicit. The most consequential improvements concern interpretation and investigation, followed by presentation polish.

<a id="ux-reviews-2026-09--scope-and-evidence"></a>
#### Scope and evidence

Current dashboard source was served by Vite against the existing local controller binary and an isolated SQLite backup. Schedules were disabled and nonterminal tests marked complete only in that disposable backup to prevent executions. No application implementation was edited or tests launched. Browser evidence used the archived `web wiki` run, `LOC-RUN-20260730-001`: 275 interval samples, 15 available metrics, one observed load level of 10 VUsers. Its legacy interval population differs from the request/action KPI population, as the report itself explains.

The audit followed Results → Performance → Load impact, switched 2D/3D, selected P95, and opened/closed configuration. Screenshots were captured during this review, saved, reopened, and inspected. They are viewport captures, not complete-page captures. The browser's native surface changed width during the review; the accepted 2D capture is 1422px wide and the remaining captures are 690px wide. These are not same-viewport visual comparisons. Temporary viewport overrides produced capture scaling artifacts and were reset; those captures were replaced.

| Step | Task | Health |
| --- | --- | --- |
| 1 | Find and read the 2D map | Usable numeric comparison; unclear color meaning and excessive surrounding chrome |
| 2 | Switch to 3D and inspect the retained selection | Renders and preserves selection; difficult labels, automatic motion, unexplained height |
| 3 | Select P95 and inspect samples | Details work; separated from selection, wide table, duplicate P95 column |
| 4 | Open configuration | Logical metric groups and focus restoration; translucent panel damages readability |

<a id="ux-reviews-2026-09--prioritized-recommendations"></a>
#### Prioritized recommendations

<a id="ux-reviews-2026-09--p1-explain-what-color-and-height-measure"></a>
##### P1 — Explain what color and height measure

Evidence: steps 1–2. In the single-load run, ART 476ms, P95 514ms, and Error 0% have the same 2D shade. The 3D bars have the same model height across metrics. Source inspection confirms that each metric independently normalizes its bucket averages to that row's observed minimum/maximum. A constant row receives intensity 0.65, including a constant zero row. Height is `0.14 + intensity * 2.7`.

Add a persistent legend: “Relative value within each metric · lighter = lower · darker = higher.” Explain that shading does not establish an SLA breach and that values cannot be compared across different units. For constant rows use a neutral “No variation” treatment. For a single load, explain “One observed load level; load sensitivity cannot be assessed,” and show a compact value summary by default.

Offer a separate threshold or baseline comparison mode when supported by valid evidence. Threshold direction must be metric-specific: high error rate and low free memory mean different things; rising TPS alone does not establish poor health. Use text/icons with severity colors. In live views, consider locking scale and load buckets during inspection; this is an implementation-derived follow-up, not a behavior verified during this archived-run audit.

Source: `dashboard/src/components/loadImpactModel.ts` (buildLoadImpactModel), `LoadImpactHeatmap.tsx` (cell alpha), `LoadImpact3DHeatmap.tsx` (heightValue).

<a id="ux-reviews-2026-09--p1-make-3d-stable-labeled-and-deliberately-interactive"></a>
##### P1 — Make 3D stable, labeled, and deliberately interactive

Evidence: step 2. The view rotates without operator input. Metric labels are tiny and overlap in perspective; there is no visible height scale, pause control, reset camera, or interaction instruction. Small chips repeat some axis labels but expose only eight metric names plus “+7.” The selected P95 remains selected across the view switch, which is a useful behavior to preserve.

Disable automatic rotation by default. Add explicit Rotate/Pause, Reset view, Top view, and zoom controls plus concise drag/zoom instructions. Fit labels to available space, keep critical axis titles outside the moving scene, and offer a focus-on-metric control. Keep selected and hovered values visually distinct. Provide keyboard-accessible selection outside the canvas; current canvas semantics expose an image, while 2D cells expose named buttons.

If the current cross-metric layout remains, label height as normalized relative value. A future exploration design should consider a single selected metric with a real unit-bearing height axis and meaningful load/time dimensions, but only when the available sampling supports those dimensions. That would be a design proposal, not an existing capability.

Automatic motion warrants accessibility review against [W3C's Pause, Stop, Hide guidance](https://www.w3.org/WAI/WCAG22/Understanding/pause-stop-hide). The existing 2D switch can hide 3D, so this review does not assert a formal WCAG failure. Also honor reduced-motion preferences and test keyboard/screen-reader equivalents.

<a id="ux-reviews-2026-09--p1-put-aggregation-and-evidence-scope-beside-the-map"></a>
##### P1 — Put aggregation and evidence scope beside the map

Evidence: steps 1 and 3. The row label is P95, but its selected summary is “Avg 514ms.” Source confirms an arithmetic mean of interval P95 values, rather than recomputing a pooled percentile. “275 samples” represents telemetry points, not necessarily 275 requests. The report's top KPI shows a different P95 population; its contextual explanation is far above the map.

Label the display “Mean interval P95” or add an always-visible “Cells show averages of interval metrics” explanation. Show population, time window, sample interval, and resource ownership near the map. Resource metrics should explicitly identify the load generators; they should not invite inference about application-server saturation. Distinguish missing data, zero values, and intervals with no relevant observations where the source evidence supports that distinction. Mark low-coverage cells using a defined, evidence-based rule rather than treating sample counts as confidence by themselves.

<a id="ux-reviews-2026-09--p2-make-a-heatmap-read-like-a-matrix"></a>
##### P2 — Make a heatmap read like a matrix

Evidence: step 1. The outer “Load impact” disclosure repeats the inner “Load Impact” title. Nested bordered surfaces, a tall header, three count chips, cell-level sample counts, and a table-pagination toolbar consume space. Only 10 of the 15 enabled metric rows are initially visible because the global table-pagination manager applies to the matrix.

Flatten to one panel with a compact toolbar and legend. Exempt this small metric matrix from generic row pagination; show grouped Performance and Generator resources rows together, with sticky headings where needed. Offer a concise key-metrics preset such as ART, P95, TPS, Error %, CPU, and memory. Put repeated sample counts in a secondary detail treatment while keeping coverage discoverable. Preserve exact numeric values and row/column highlighting.

Source-only follow-up: clicking a load header currently sorts different metric rows by their raw numeric averages, mixing milliseconds, percentages, MB, and rates. Such a ranking has no common meaning. Replace it with within-unit sorting or a defined dimensionless baseline/threshold deviation. This behavior was confirmed in source, not exercised as a browser task.

<a id="ux-reviews-2026-09--p2-shorten-the-path-from-an-interesting-cell-to-an-explanation"></a>
##### P2 — Shorten the path from an interesting cell to an explanation

Evidence: step 3. Clicking P95 adds details below the entire matrix; the click leaves focus on the originating cell. The table contains both the selected-metric P95 column and the standard P95 column, and requires horizontal scrolling at the narrower width. It supplies timestamps and useful adjacent measurements, but no direct investigation action.

Use a compact pinned inspector beside the map on wide screens and directly below the selected area on smaller screens. Start with value, load, units, sample count, min/max, and coverage; show raw samples as a secondary disclosure. Remove duplicate metric columns. Add a small time series and a “View these intervals in timeline” action; a load bucket can contain disjoint intervals, so preserve that distinction. Consider related error and generator-metric links where evidence exists. Provide a visible close action and an accessible update announcement.

<a id="ux-reviews-2026-09--p2-use-an-opaque-configuration-surface-and-task-presets"></a>
##### P2 — Use an opaque configuration surface and task presets

Evidence: step 4. Text and blue cells from the underlying page remain visible through the drawer. A read-only computed-style inspection confirmed background `rgba(15, 17, 26, 0.65)` with opacity 1. This is the panel's color, not a transitional screenshot. The panel uses `--bg-card`.

Use an opaque modal/drawer token, stronger text and control contrast, and a clearly dimmed backdrop. Preserve the existing Performance/Resource grouping, native checkboxes, initial Close-button focus, and return focus to Configure Map. Add category-level selection and useful presets; rename Add All/Remove All to Select all/Clear selection, and clarify that Reset means default selection. If an empty selection is allowed, place a direct Restore defaults action in that state. These configuration recommendations do not imply the current controls are broken.

<a id="ux-reviews-2026-09--screenshots"></a>
#### Screenshots

<a id="ux-reviews-2026-09--1-2d-map-readable-values-unexplained-uniform-shading"></a>
##### 1. 2D map — readable values, unexplained uniform shading

Step 1: 2D load impact map (`heatmap-audit-2026-09-14/01-2d-map.png`)

<a id="ux-reviews-2026-09--2-3d-map-tiny-labels-and-retained-p95-selection"></a>
##### 2. 3D map — tiny labels and retained P95 selection

Step 2: 3D map (`heatmap-audit-2026-09-14/02-3d-map.png`)

<a id="ux-reviews-2026-09--3-sample-details-useful-evidence-behind-a-wide-table"></a>
##### 3. Sample details — useful evidence behind a wide table

Step 3: P95 sample details (`heatmap-audit-2026-09-14/03-cell-details.png`)

<a id="ux-reviews-2026-09--4-configuration-background-content-shows-through-the-drawer"></a>
##### 4. Configuration — background content shows through the drawer

Step 4: Heatmap configuration (`heatmap-audit-2026-09-14/04-configuration.png`)

<a id="ux-reviews-2026-09--limits-and-recommended-validation"></a>
#### Limits and recommended validation

This was an operator-experience review of the rendered archived-run flow and shared component source, not an audit of telemetry correctness. No live stream, multilevel ramp, sparse-load fixture, WebGL failure, full keyboard-only workflow, screen reader, formal contrast measurement, or complete responsive matrix was tested. No full accessibility compliance is claimed. Detailed live behavior and dense multibucket occlusion remain follow-up checks. The legacy run cannot establish whether current runtime telemetry encodes missing observations correctly.

For an implementation pass, validate fixed load, ramp-up/down, constant-zero rows, sparse/missing observations, and different metric populations. Test scale meaning, population labels, duplicate-column removal, selection preservation, keyboard access, reduced motion, and the opaque drawer. Repository policy requires the dashboard test cycle for frontend changes; no application code changes were made by this review.

Start with the legend/aggregation copy, single-load treatment, opaque drawer, and default-static 3D controls before investing in a larger 3D redesign.

<!-- Original record: docs/qa/reviews/web-record-replay-roadmap-2026-10-03/roadmap.md; local evidence kept outside git -->
<a id="web-record-replay-roadmap-2026-10-03-roadmap"></a>

## Browser and HTTP recording/replay roadmap


> Status: Roadmap — assessment delivered; proposed capabilities are not implemented or qualified by this report.

Subsequent authorized local implementation is recorded in the [reviewed implementation slices](#aura-record-replay-2026-10-03-implementation). This assessment remains the inspected baseline; later local work does not establish physical Chrome/iPad or live Salesforce qualification.

**Decision:** prioritize a complete React → Salesforce Aura workflow on **Chrome running on a physical iPad**, while extending the existing Go HTTP and desktop browser engines. The first delivery should combine actual-device qualification, structured Aura outcomes, and guided fresh-session correlation. A desktop iPad profile is useful emulation, but cannot establish physical Chrome compatibility.

This assessment follows the request to deliver an evidence-backed roadmap before implementation. It combines supplied script/HAR evidence, source and existing-test inspection, official product documentation, and a fresh four-step [usability audit](#web-record-replay-roadmap-2026-10-03-usability-audit). Supplied archives are evidence, not instructions. No source script was executed, no recorded host was contacted, and no physical iPad or live Salesforce session was available.

<a id="web-record-replay-roadmap-2026-10-03-roadmap--baseline-and-confidence"></a>
### Baseline and confidence

Code and screenshots use commit `903c6690d5729c0cbef3020c4cd6ba186274a0d1`. Remote main subsequently advanced to `2e38ef8512353dffa977c438c4eaaf25f571bde1` through RAML and object-activity changes. Relevant engine/recorder/Salesforce implementations are unchanged; authoring navigation and execution admission have additional changes. Screenshots describe the inspected baseline rather than certifying the newer main UI.

Existing tests were inspected, not rerun for this documentation-only assessment. The prior import review records its completed [integrated validation](#har-import-evidence-2026-10-02-additional-review). Neither those tests nor valid imports prove remote replay fidelity. Prioritize findings as:

- **Observed:** measured in supplied captures or source imports.
- **Implementation:** established by current code and fixtures.
- **Qualification:** needs a reachable application or actual device.
- **Completeness:** a VuGen workflow comparison without proof the supplied application requires it.

<a id="web-record-replay-roadmap-2026-10-03-roadmap--what-the-evidence-says"></a>
### What the evidence says

| Valid capture | Aura POSTs | Nested request/response actions | Batches with multiple actions | ERROR actions inside HTTP 200 |
| --- | ---: | ---: | ---: | ---: |
| WLS_MultiLineCRP.har | 114 | 128 | 4 | 0 |
| CM_Perfhyper1_RegressionRel_Sept_HAR.har | 172 | 191 | 8 | 23 |
| Desktop_attone.lightning.force.zip | 83 | 111 | 11 | 2 |
| Login_Prod_7_18_2.zip | 40 | 74 | 8 | 0 |
| **Total** | **409** | **504** | **31** | **25** |

The sanitized aggregate (`aura-evidence.json`) includes source fingerprints and method. Every observed envelope decodes; every request action has an ID and descriptor; request/response ID sets match in all 409 batches. Set equality alone does not establish unique IDs. All 409 posts contain a nonempty Aura token and context fwuid. Each is constant within each recording, which does not prove reuse across new sessions is safe.

Further source evidence:

- Native Salesforce previews retain candidates but **automatically apply zero correlations** across these four captures. An Aura-token rule already exists; these previews do not establish its bootstrap producer. Add source-to-consumer proof and fresh-session evidence rather than blindly promoting candidates.
- Aura params contain **57 JSON-as-string leaves**: WLS 13, CM 6, Desktop 36, Login 2. Current parameter walking treats string leaves as opaque. Outer message forms decode in one layer; outer double URL encoding is a prospective compatibility case, not observed evidence.
- Desktop retains **153 transport-version blockers** because HTTP/1.1 and HTTP/2 coexist under a script-wide fidelity policy. Large correlation reviews also reach bounded limits; unresolved work remains explicit.
- CMChange's source parser proves **three active concurrent groups with eight requests**, sizes 1/4/3. Four additional groups are commented out. Current import serializes these groups.
- TruClient Prefetch yields a gated 57-action draft with 31 fidelity blockers, including two global gates. All 220 selected CFG/USP keys are accounted for, but only 12 are mapped; 492 browser/authentication file-key rows remain compatibility-only. Accounting is not engine equivalence. See [TruClient evidence](#truclient-import-2026-10-01-truclient-fidelity).
- KM's additional HAR is truncated and cannot be faithfully imported as a complete recording. Require a complete re-export. Some Login headers declare an iPad, but none of those declarations contain CriOS; headers do not prove physical hardware or browser identity.
- CLM's native web scripts preserve 447 source HTTP requests. Legacy SSL 2/3 migration gates remain; modern TLS replacement requires an explicit reviewed choice. Java/JAR and Kafka execution gaps are separate from browser/HTTP scope. See [web evidence](#clm-import-2026-10-01-web-fidelity) and [additional HAR evidence](#har-import-evidence-2026-10-02-additional-review).

<a id="web-record-replay-roadmap-2026-10-03-roadmap--preserve-the-existing-foundations"></a>
### Preserve the existing foundations

APEX already has HTTP/HTTPS proxy recording, filters, pause/resume, transactions, HAR conversion and browser recording with interactions, frames, open shadow DOM and popups. Browser replay already offers selectors, repair, waits, dialogs, history, window opening, files and downloads. HTTP supports per-VUser cookies/cache, redirects, resources, extractors, assertions, transport settings and SSE. HTML/CSS resource discovery does not execute frontend JavaScript.

Correlation already has a rule catalog, confidence/provenance, preview/apply, two-record comparison, replay diagnosis and project rulesets. Salesforce rules include Aura token/context, fwuid, app/loaded, page URI, Visualforce and session handoffs. Salesforce analysis also classifies CometD, correlates handshake clientId, folds connect requests into counted loops with request timeouts, and synthesizes bounded Bulk polling to terminal states. Browser recording can already generate linked browser and native Salesforce HTTP drafts with shared recording receipts.

Debugging already has breakpoints, stepping, conditions, failure breaks, network/DOM/console evidence and basic variable watches. Parameter preview already resolves VUser, iteration, occurrence and same-row relationships for an isolated unsharded dataset; concurrent reservations and earlier each-occurrence references are explicitly excluded. A real React fixture already tests settled debounced input, keyboard input, labels preferred over generated Aura IDs, polling-aware synchronization, and two fresh-context replays. It runs in desktop Chromium.

Relevant implementations: [browser dispatch](../../../pkg/protocol/browser/action_dispatch.go), [correlation rules](../../../pkg/convert/correlate/rules.go), [Salesforce analyzer](../../../pkg/convert/salesforce/analyzer.go), [recording outputs](../../../pkg/api/handlers_browser_recorder.go), [React fixture](../../../pkg/recorder/browser/react_replay_integration_test.go), [debugger](../../../pkg/api/handlers_script_debug.go), [parameter preview](../../../pkg/api/handlers_parameter_preview.go). Build on these capabilities rather than adding duplicate entry points.

<a id="web-record-replay-roadmap-2026-10-03-roadmap--priority-backlog"></a>
### Priority backlog

| Order | Feature and purpose | Evidence | Completion gate |
| --- | --- | --- | --- |
| 1 | **Physical Chrome/iPad bridge and readiness.** Guided Mac/device pairing, Chrome context selection, capture/replay capability checks and observed identity. Clearly label desktop emulation. | Implementation + qualification | Actual installed Chrome on physical iPad records and replays a controlled React/Aura fixture; observed device/app/build, applied settings and missing capabilities accompany artifacts. |
| 2 | **Structured Aura batch outcomes.** Expand every HTTP envelope into inspectable subactions and evaluate their individual states and business assertions. | Observed: 504 actions, 25 ERROR states under HTTP 200 | Match by request identity rather than response position; test reordered results, ERROR/INCOMPLETE, missing/duplicate IDs and unknown states. Baseline errors warn rather than silently becoming successful reference behavior. |
| 3 | **Guided fresh-session correlation.** Show bootstrap producers, consumers, exact encoded replacements and coverage; resume bounded scans by transaction/range. | Observed: zero automatic native correlations | Two fresh captures prove producers and reuse; a new-session replay uses runtime values. Ambiguous, missing, expired and truncated evidence remains unresolved. |
| 4 | **React readiness and real input.** Extend stable selectors and polling-aware waits to trusted touch, keyboard, DOM replacement and cached Aura behavior. | Existing desktop fixture; synthetic touch implementation | Physical-device tests cover focus, keyboard overlays/composition, scroll, orientation, portals, virtualized lists, delayed results, tabs and cancellation. Completion uses expected UI state and relevant subaction outcome. |
| 5 | **One evidence and repair workspace.** Join interaction → request/batch → subaction → extracted value/consumer → UI/business result; recorded/replay views and exact diff. | Existing debugger, correlation and dual-output receipts | A failure opens the exact producer/consumer and both source/replay evidence; a reviewed repair is undoable, scoped and validated. No array-index-only links. |
| 6 | **Nested, encoding-aware Aura parameter editing.** Decode explicit JSON-in-string paths, parameterize leaves and re-encode with bounded depth/work. | Observed: 57 leaves | Preserve unrelated fields and encoding; test escaping, Unicode, duplicate members, invalid JSON, limits and binary guards. Show exact replacement preview. |
| 7 | **Live session and client-state preflight.** Verify login/SSO, expiry, identity, storage/cache conditions and endpoint eligibility. | Qualification; Salesforce session policy | Fresh login and cold/warm runs succeed without embedded captured credentials. Domain/IP restrictions, CSP/LWS and policy-induced behavior are visible. Reauthentication is bounded. |
| 8 | **Session-faithful concurrent HTTP groups.** Add an HTTP-owned request-group primitive using the parent VUser session and a join barrier. | Observed: 3 groups/8 requests; load/replay semantics differ | Prove overlap in load and replay, login-cookie inheritance, following-cookie visibility, VUser isolation, deterministic parameter allocation, extraction conflict handling and cancellation cleanup. |
| 9 | **Per-request HTTP version policy.** Exact recorded version or explicit negotiated replacement, with appropriate transport pooling. | Observed: 153 Desktop blockers | Preserve source versions; exact-mode mismatch is visible. Negotiated replacement is reviewed. Cookie/auth/cache isolation and HTTP/2 multiplexing tests pass. |
| 10 | **Complete, regenerable capture evidence.** Immutable protected source bundle, full source accounting, explicit capture limits and shared bounded content decoding. | Truncated KM; bounded previews/scans; recorder/replay decoding differ | Retained/skipped/truncated counts reconcile; complete source is recoverable under policy. Test encoding chains, zstd, malformed/expanding bodies and HAR already-decoded cases; regenerate without rerecording. |

<a id="web-record-replay-roadmap-2026-10-03-roadmap--the-physical-device-lane"></a>
#### The physical-device lane

Google documents inspection of Chrome on iOS/iPad through a Mac: enable Chrome's Web Inspector, relaunch, connect the device, and select the Chrome page in Safari's Develop menu. Safari is the inspector; Chrome remains the target. WebKit's Network tab provides request inspection and HAR export. This supports a **guided manual capture milestone**; it does not prove integrated automatic UI-event capture. [Google Chrome inspection](https://developer.chrome.com/blog/debugging-chrome-on-ios), [WebKit network capture](https://webkit.org/web-inspector/network-tab/).

Appium's XCUITest driver supports real iPadOS devices and inspectable Chrome webviews. Use it as the candidate foundation for native Chrome-shell control through WebDriverAgent and web-context access through the WebKit remote debugger. Begin with a feasibility gate covering the actual Chrome/iPadOS/Xcode/driver versions, trust, Developer Mode, signing, inspection permissions and device-management policy. Historical minimum versions are not a qualification matrix. DOM automation and native WDA input need separate tests. Desktop Chromedriver/CDP and desktop WebKit emulation do not establish this target's compatibility. [Driver overview](https://appium.github.io/appium-xcuitest-driver/latest/overview/), [device setup](https://appium.github.io/appium-xcuitest-driver/latest/getting-started/device-setup/), [hybrid limitations](https://appium.github.io/appium-xcuitest-driver/latest/guides/hybrid/).

Expose automation-induced changes such as disabled autocorrection/predictive text and qualify natural keyboard behavior separately. Record requested and observed browser app/build, OS/model, physical/emulated status, orientation, website mode, viewport, capture method and supported capabilities. Do not infer these from UA alone or assume one engine for every region/build. Current [iPad preset](../../../pkg/protocol/browser/devices.go) is a Safari-UA WebKit emulation profile; the [recorder](../../../pkg/recorder/browser/recorder.go) launches Chromium and conversion can infer WebKit from that preset. Make actual record/replay engine differences visible. [Apple alternative-engine scope](https://developer.apple.com/support/alternative-browser-engines/).

<a id="web-record-replay-roadmap-2026-10-03-roadmap--aura-and-react-behavior"></a>
#### Aura and React behavior

Aura queues and batches actions; result ordering can differ. Match subactions by identity and validate state and returned business data. Existing analyzer checks for literal ERROR/INCOMPLETE substrings are a useful guard, but not a complete structural outcome contract. Storable actions can avoid a server call and can call back first from cache and then from refresh. Qualify cold/warm behavior independently and do not prescribe a fixed cache lifetime. [Aura batching](https://developer.salesforce.com/docs/platform/aura-platform/guide/controllers-server-actions-queue.html), [action states](https://developer.salesforce.com/docs/platform/aura-platform/guide/controllers-server-actions-states.html), [storable actions](https://developer.salesforce.com/docs/platform/aura-platform/guide/controllers-server-storable-actions.html).

React commits DOM updates after rendering and controlled inputs depend on event handling. Extend the current recorder's behavior with re-resolved semantic locators, trusted input where required and business-state waits. Polling, cached callbacks and asynchronous rendering make global network-idle an unreliable sole completion condition. Current touch actions synthesize DOM events; handler counts do not prove native scrolling, user activation or physical keyboard behavior. [React DOM commits](https://react.dev/learn/manipulating-the-dom-with-refs), [controlled inputs](https://react.dev/reference/react-dom/components/input), [touch implementation](../../../pkg/protocol/browser/touch_actions.go).

Session portability must be proven rather than assumed. Salesforce can bind sessions to domains/IPs and restrict Lightning-created API sessions. Diagnose login redirects, cookies, expiry and authorized endpoints before load. Capture tooling should expose CSP/LWS limitations and avoid dependence on unrestricted framework internals. Existing credential/identity policy remains the foundation. [Salesforce session settings](https://help.salesforce.com/s/articleView?err=1&id=admin_sessions.htm&language=en_US&type=5), [Lightning session restrictions](https://resources.docs.salesforce.com/latest/latest/en-us/sfdc/pdf/lightning.pdf), [CSP](https://developer.salesforce.com/docs/platform/lightning-components-security/guide/content-security-policy-intro.html), [LWS](https://developer.salesforce.com/docs/platform/lightning-components-security/guide/lws-build-components.html).

Publish independently qualified, linked **physical browser canary** and **Go HTTP load** outputs. UI timing includes rendering/device behavior; HTTP timing measures its defined backend requests. Existing sibling generation should gain transaction/subaction lineage and equivalence evidence. Physical-device capacity and Go load density need measured reports, not shared performance claims.

<a id="web-record-replay-roadmap-2026-10-03-roadmap--concurrent-http-cannot-simply-use-controlparallel"></a>
#### Concurrent HTTP cannot simply use control_parallel

Current import explicitly flattens `web_concurrent` calls. Native load `control_parallel` creates a fresh protocol instance per branch, including separate HTTP session state; replay walks branches sequentially on the existing protocol. Neither is a faithful shortcut. Use parent-session ownership, immutable prepared inputs, isolated request results, bounded dispatch and a barrier. Define parameter allocation and extractor conflicts rather than allowing scheduler order to choose values. [Import behavior](../../../pkg/convert/enterprisearchive/converter.go), [load parallelism](../../../pkg/engine/control_parallel.go), [replay](../../../pkg/engine/replay.go).

<a id="web-record-replay-roadmap-2026-10-03-roadmap--remaining-vugen-completeness-work"></a>
### Remaining VuGen completeness work

| Area | Existing foundation | Increment to deliver |
| --- | --- | --- |
| Recording profiles/regeneration | Browser and proxy recorders; HAR converter | Named HTML/resource/raw profiles; immutable capture and source map; regenerate reviewed drafts; replace a selected recording segment with stable IDs and an undoable diff. |
| Snapshot and correlation workflow | Recorder/replay evidence, comparison, rulesets | Unified request/response/header/cookie/body views, text/JSON/hex as applicable, wire/decoded provenance, selection-to-extractor/assertion and fresh-session validation. |
| TruClient semantics | Gated import, waits, typing, selectors, native tabs/downloads/history | Preserve optional/finalized/alternative decisions, source completion rules, no-clear and typing-timing policies, qualified close-window and pre-armed download semantics. Generic download support does not prove WaitForDownload import equivalence. |
| Async conversations | HTTP SSE; native WebSocket; polling-aware browser conversion; CometD classification/clientId correlation/connect loops; bounded Bulk terminal-state polling | Preserve interleaved HTTP/WebSocket recordings and frame dependencies; extend CometD advice/reconnect/disconnect handling and explicit whole-conversation deadlines/cancellation. |
| Runtime settings | Rich HTTP/browser config; per-key import accounting | Effective source-to-target diff, explicit engine/platform support and portable proxy/certificate/timezone/cache reset policies; replace obsolete SSL selectors with reviewed modern TLS. |
| Pause-state debugging | Basic watches, stepping, breakpoints | Authoritative bounded/redacted pause snapshots and deltas for loop/scoped/branch values and data-allocation state; inspection must not consume a row or advance allocation. |
| Parameter management | Parameterization, allocators, data sources and isolated VUser/iteration/occurrence preview with same-row traversal | Extend preview to concurrent reservations, sharded allocation and earlier each-occurrence references, with exact encoded consumer previews; diagnose missing producers without inventing values. |
| Enterprise authentication | Native Basic/Bearer/Digest/OAuth, governed credentials | Add NTLM/Kerberos/Negotiate only against a verified requirement and tested adapter; expose credential scope and connectivity failures. These captures do not establish those schemes are required. |
| Coverage and readiness | Import capability catalog and gates | One matrix distinguishing native action support, source import fidelity, record engine, replay engine and physical-device qualification, each tied to fixtures or live evidence. |

VuGen provides Design Studio record/scan/replay correlation and replacement review, detailed recording/replay snapshots, regeneration and debugging/runtime/parameter workflows. These are useful usability benchmarks for completing the APEX flow. Its mobile simulation and real-device protocols also have different scopes; do not collapse them into one parity label. [Design Studio](https://admhelp.microfocus.com/vugen/en/26.1-26.3/help/WebHelp/Content/VuGen/Correlation/AutomaticCorrelation.htm), [snapshots](https://admhelp.microfocus.com/vugen/en/26.1/help/WebHelp/Content/VuGen/UI/ui_snapshot_pane.htm), [recording levels](https://admhelp.microfocus.com/vugen/en/26.1-26.3/help/WebHelp/Content/VuGen/116850_c_recording_levels_overview.htm), [debugging](https://admhelp.microfocus.com/vugen/en/26.1-26.3/help/WebHelp/Content/VuGen/c_debug_overview.htm), [parameters](https://admhelp.microfocus.com/vugen/en/26.1/help/WebHelp/Content/VuGen/114350_data_assignment_methods_for_parameters.htm), [mobile scope](https://admhelp.microfocus.com/vugen/en/26.1/help/WebHelp/Content/VuGen/t_HowToSelectAProtocol.htm).

<a id="web-record-replay-roadmap-2026-10-03-roadmap--delivery-sequence-and-release-evidence"></a>
### Delivery sequence and release evidence

1. **Establish the target and protect outcome correctness.** In parallel, qualify a physical Chrome/iPad bridge against a controlled reachable fixture and implement structured Aura parsing/outcome review plus fresh-session producer proof. First milestone: device readiness → captured operation → imported subactions → one new-session replay with a checked UI/business result. Manual HAR export may precede automatic event/network capture, with that limitation visible.
2. **Complete the usable target workflow.** Add the joined evidence/repair workspace, scoped resumable correlation, nested encoded params, trusted React input/readiness and cold/warm/auth checks. Extend the current linked browser/HTTP outputs and save/reopen receipts.
3. **Make HTTP load preserve the workload.** Deliver parent-session concurrency, per-request versions, complete protected evidence/regeneration and shared bounded decoding. Prove load/replay agreement and independent VUser sessions.
4. **Close the wider VuGen workflow gaps.** Advance TruClient semantics, mixed async capture, runtime migration, pause-state snapshots, concurrent/sharded allocation preview and requirement-driven enterprise auth. Publish a qualified capability matrix.

Each release needs synthetic deterministic fixtures, affected Go tests and broader shared-package gates; dashboard changes need typecheck/lint/unit tests and real browser workflows in both supported auth profiles. Device claims additionally require a documented physical-device run. Include source fingerprints, action/capture accounting, requested/observed runtime identity, cold/warm and fresh-session results, raw-to-decoded provenance, redacted diagnostics and any unresolved gates. Missing bodies, credentials or device evidence remain visible; source secrets are not published with fixtures.

**First implementation recommendation:** deliver the physical Chrome/iPad qualification and Aura outcome/correlation workbench together as the initial end-to-end slice. No product feature changes, live-device validation or full VuGen parity are claimed by this assessment.

<!-- Original record: docs/qa/reviews/web-record-replay-roadmap-2026-10-03/usability-audit.md; local evidence kept outside git -->
<a id="web-record-replay-roadmap-2026-10-03-usability-audit"></a>

## Recording and replay usability audit


Fresh screenshots were saved and inspected from a disposable local controller built at `903c6690`. The audit used a blank browser draft and setup forms, with no customer upload, recording, saved script or replay. It evaluates visible onboarding and navigation. It does not validate live recording/replay, keyboard or screen-reader operation, correlation repair, physical Chrome/iPad behavior, or current main's later navigation changes. The related [roadmap](#web-record-replay-roadmap-2026-10-03-roadmap) separates these evidence limits.

<a id="web-record-replay-roadmap-2026-10-03-usability-audit--captured-flow"></a>
### Captured flow

<a id="web-record-replay-roadmap-2026-10-03-usability-audit--step-1-enter-authoring"></a>
#### Step 1 — Enter authoring

**General health: good foundation.** Create, Record and Import are distinct entry choices. The template/editor/action-block table makes supported choices discoverable. Dense choices could benefit from a task-oriented recommendation once a user selects physical browser, HTTP load or source import.

Step 1: authoring entry choices (`screenshots/01-authoring-entry.jpg`)

<a id="web-record-replay-roadmap-2026-10-03-usability-audit--step-2-choose-an-ipad-recording-profile"></a>
#### Step 2 — Choose an iPad recording profile

**General health: useful desktop setup; incomplete for the requested device.** The selected `ipad_air` profile exposes viewport/device configuration, and the form says it launches controlled Chromium on this machine. There is no physical-device or installed Chrome-app selector in this view. Make “Emulated iPad on this workstation” explicit and provide a separate physical Chrome target with readiness and observed identity. The preset name alone could suggest a broader guarantee than the accompanying copy.

Step 2: iPad recording preset (`screenshots/02-ipad-recording-preset.jpg`)

<a id="web-record-replay-roadmap-2026-10-03-usability-audit--step-3-inspect-proxy-onboarding"></a>
#### Step 3 — Inspect proxy onboarding

**General health: useful desktop guidance; target-specific guidance missing.** Expanded instructions cover macOS/Windows proxy setup and the recorder CA. Add physical iPad Chrome guidance for Mac inspection, inspectability checks and manual HAR export, followed by any separately qualified device-proxy route. Show reachable proxy requirements and explicit trust cleanup where that route is chosen. No proxy settings, certificate trust or device configuration were changed in this audit.

Step 3: proxy onboarding (`screenshots/03-proxy-onboarding.jpg`)

<a id="web-record-replay-roadmap-2026-10-03-usability-audit--step-4-open-browser-debugging"></a>
#### Step 4 — Open browser debugging

**General health: strong existing workspace; live behavior unverified.** Author/Debug navigation, stepping, failure breaks, variable watches, breakpoint controls, action outline and Source/Runtime/Browser Replay tabs are visible. The blank draft is idle, so this is navigation evidence. Extend the same workspace with interaction-to-Aura joins, recorded/replay diffs, authoritative pause-state values and reviewed repairs. Basic watches and the debugger already exist.

Step 4: browser replay workspace (`screenshots/04-browser-replay-workspace.jpg`)

<a id="web-record-replay-roadmap-2026-10-03-usability-audit--design-priorities"></a>
### Design priorities

| Priority | Finding | Recommended flow |
| --- | --- | --- |
| High | Emulated device and actual browser identity need clearer separation. | Choose physical Chrome/iPad → readiness checks → show observed device/app → record. Retain desktop emulation as an explicit option. |
| High | Desktop proxy onboarding does not answer physical Chrome setup. | Route instructions by chosen target and capture method, with concrete prerequisite/error resolution and capture capability limits. |
| High | The requested business correlation spans multiple existing tools. | Review completeness → per-subaction Aura outcome → producer/consumer proof → preview repair → replay result in the existing workspace. |
| Medium | Dense authoring choices require the user to infer the route. | Offer a recommendation based on desired output, while leaving detailed choices available. |
| Medium | Runtime labels should explain what was actually applied. | Compare requested settings with observed engine/device capabilities and preserve unresolved source settings in the review. |

<a id="web-record-replay-roadmap-2026-10-03-usability-audit--visible-accessibility-risks-and-limits"></a>
### Visible accessibility risks and limits

The dense authoring table and debug workspace need keyboard navigation and accessible relationship checks across the action outline, tabs, evidence and repair controls. Preserve textual readiness/blocker/status messages so color does not carry the result alone. Screenshots cannot establish contrast ratios, focus order, screen-reader announcements or touch target dimensions; these are verification requirements, not confirmed violations. Physical iPad testing must include keyboard overlays, zoom/orientation and reachable touch controls.

The desired user journey is: **choose actual target → verify readiness → record → review capture completeness → repair correlation → replay one business operation → save linked browser/HTTP outputs**. Reuse the existing editor/debugger and shared recording receipts so the user does not have to reconstruct the journey across disconnected views.
