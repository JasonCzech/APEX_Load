# HTTP authoring unification plan

Status: Implemented (Slices U1–U6).
Date: 2026-10-26.
Inspection baseline: working checkout `d439d80d9` plus `origin/main` through `df61bafcd`.
Review: source-checked and implemented on 2026-10-26 across `pkg/convert/sample`, `pkg/convert/har`, `pkg/convert/salesforce`, `pkg/convert/enterprisearchive`, `pkg/convert/importcontract`, `pkg/internal/paritytest`, `pkg/internal/importreview`, `pkg/recordbridge`, `pkg/recorder`, `pkg/dsl`, `pkg/api`, and `dashboard`.

## Objective

Make equivalent HTTP evidence and equivalent effective conversion settings produce
equivalent executable DSL across HAR import (dedicated HAR, Salesforce HAR, and HAR
submitted through the generic sample importer), HTTP proxy recording, and the browser
recorder's HTTP output. Extend the same request-fidelity contracts to LoadRunner web
imports wherever the source semantics match.

Two captures of the same user workflow are not necessarily equivalent evidence.
Cached requests, capture filters, missing or budget-omitted bodies, negotiated
protocol versions, timestamps, credentials, and recorder annotations can differ.
APEX should explain those differences rather than invent missing evidence to make
scripts match.

For LoadRunner, the goal is equivalent request behavior plus preservation of the
authored test. A traffic capture alone cannot establish the source script's loops,
parameter allocation, transaction status, pacing, or runtime settings.

## Findings from the current implementation and in-flight work

| Area | Current behavior | Planning implication |
| --- | --- | --- |
| Conversion core | Proxy recordings become an in-memory HAR and use `sample.Convert`; dedicated HAR, Salesforce HAR, sample HAR, and browser HTTP use the same core. See [shared conversion](../pkg/recorder/shared_conversion.go) and [HAR conversion](../pkg/convert/har/converter.go). | Extend this pipeline and its contracts; avoid another universal converter. |
| Correlation application | Proxy sets `HighConfidenceOnly`; HAR defaults do not. [The existing test](../pkg/convert/sample/recorder_fields_test.go) demonstrates a lower-confidence value rewritten only under the latter policy. | Resolve an explicit common policy rather than keying behavior on the capture source. |
| Final processing | Dedicated HAR (`POST /api/v1/scripts/import/har`) enables native Salesforce output by default unless disabled; Salesforce HAR (`POST /api/v1/scripts/import/salesforce`) forces native Salesforce output (`force: true`) and skips the recording-profile correlation-policy lookup that dedicated HAR runs; browser HTTP enables native Salesforce output. All three refresh client UUIDs/timestamps. Proxy conversion does not compose the same passes. See [HAR handler](../pkg/api/handlers_import_har.go), [Salesforce HAR handler](../pkg/api/handlers_import_salesforce.go), and [browser HTTP conversion](../pkg/recordbridge/conversion_service.go). | Unify ordered composition across all five capture/HAR entry points under an explicit resolved policy, retaining legacy defaults during migration. |
| Native-stage policy | [Salesforce analysis](../pkg/convert/salesforce/analyzer.go) performs another correlation scan and uses its own high-confidence application rule; its options do not carry the recording profile's correlation policy. [Agentforce augmentation](../pkg/internal/importreview/import_augment.go) can change executable actions and introduce secret references. | Every mutating stage must honor effective policy and finish before final mapping/credential reconciliation and qualification. |
| Alternate HAR entry | [Generic sample import](../pkg/api/handlers_scripts.go) accepts HAR through `sample.Convert`, without the dedicated HAR handler's composition or recording-profile lookup. | Include both `/api/v1/scripts/import/sample` and `/api/v1/scripts/import/salesforce` in the core equivalence contract. |
| Proxy baseline attestation | In [recorder preview](../pkg/recordbridge/conversion_service.go) and [script recording attestation](../pkg/api/script_recording.go), even the primary attested-claim save path computes `RecordingBaselineSnapshotDigest` and persists `observations` via `recorder.SessionBaseline(session)` (`ConvertSession` with default `ConvertOptions{}`), while preview converted the script with request-specific `ResourceMode`, `AsyncScan`, `Profile`, and `HTTPVersionMode`. Only direct `POST /api/v1/recorder/{id}/convert` persists `result.Baseline` from `ConvertSessionWithOptions`. | Bind preview claims and baseline observations to the exact option-resolved conversion graph; never attest or store a default-options baseline against a non-default or native-rewritten action graph. |
| Browser dual-script preview | [Browser conversion preview](../pkg/recordbridge/conversion_service.go) builds `Parameterization` only for `result.Script` (`script_index: 0`) and omits per-output parameterization suggestions, secret-binding requirements, and preflight for `http_script`, even though [verification](../pkg/recordbridge/conversion_service.go) and [save preparation](../pkg/recordbridge/conversion_prepare.go) accept `ParameterizationSelections` and `SecretBindings` targeting `httpScriptIndex`. | Return per-output review, preflight, parameterization suggestions, and secret-binding requirements for the HTTP companion in preview so clients can inspect and select them before verify/save. |
| Options and clients | Recorder preview/save duplicate anonymous option structs. [Recorder client hooks](../dashboard/src/hooks/api/useRecorderApi.ts) send only `{ name, parameterize }`, omitting `profile_id`, `resource_mode`, `async_scan`, `page_title_checks`, and `http_version_mode` that the backend already accepts. Profile fallback also happens in several converters. | Reuse named preparation contracts and expose the same applicable options to clients. |
| Parameterization | Proxy applies its own optional automatic input rewrite (`recorder.ParameterizeScript`). Browser's legacy `Parameterize` boolean targets the browser script, while explicit `ParameterizationSelections` and `SecretBindings` apply per `script_index`. In-flight import wizard changes (`EnterpriseArchiveImport.tsx`) also preserve `reviewedDraftEdits` directly on save instead of reconverting the archive and discarding reviewed edits. | Reuse the existing selected-edit contract across preview, verify, and save; distinguish automatic generation, preview suggestions, and applied selections. |
| Shared variable readiness and guided repairs (in-flight `origin/main` `e3d084f46`) | `pkg/dsl/varflow` (and `dashboard/src/components/variableFlow.ts`) is now the single execution-order producer/consumer model across `requestimport.AnalyzeVariableReadiness`, `importreview.AnalyzeDependencies`, and `BuildReplayPreflight` (`LateProducedVariables`, `ParamBlockingVariables`, `ConfigBlockingVariables`, `PlaceholderVariables`). Saved scripts also support `POST /api/v1/scripts/{id}/repairs/{preview,apply}` (`pkg/api/script_repairs.go`) and refresh server-owned import evaluation on `PUT /api/v1/scripts/{id}` (`pkg/api/script_save_import_evaluation.go`). | Use `pkg/dsl/varflow` as the single producer/consumer oracle in U1/U3/U5, and ensure U3/U4's persisted review metadata participates cleanly in `refreshSavedImportEvaluation` and guided repairs on reopen/edit. |
| Synthetic debugger HAR and bounded native browser HAR (in-flight `origin/main` `819563141`/`746c34f6a`/`b7afd9b19` and `BE-B3`/`BE-B4`) | Debug replays export synthetic HARs (`pkg/api/script_debug_har.go`, `pkg/protocol/replay_har.go`, `pkg/recordbridge/synthetic_har.go`) with configurable body retention (`none`, `request`, `response`, `both`; 16–1,024 KiB per body, 1–16 MiB run-wide), inline JS credential redaction (`pkg/scrub/body_text.go`), and optional intermediate redirect hops. Native browser HAR capture (`browser_native_har_v1`) enforces 512 KiB per-resource / 32 MiB session / 100 MiB working-file bounds and HA optional source-cache receipts (`_collection/optional_receipt.json`). | Treat synthetic debug HARs and bounded browser HARs as explicit evidence profiles: preserve body-omission and redaction reasons when re-imported or compared, rather than mistaking budget-omitted bodies for empty requests. |
| Headers and cookies | Proxy applies its own browser-header defaults and session cookie history. HAR profile filtering applies explicit drops, while ordinary HAR cookies can become an explicit Cookie header. See [recorder policy](../pkg/recorder/converter.go) and [HAR profile handling](../pkg/convert/sample/recorded_shape.go). | Separate irreversible capture choices from the common replay policy. |
| LoadRunner web conversion | [Enterprise archive conversion](../pkg/convert/enterprisearchive/converter.go) maintains pending registrations, headers/cookies, phases, transactions, concurrency, and source runtime controls. It already reuses shared [multipart](../pkg/convert/enterprisearchive/request_multipart.go), [credential projection](../pkg/convert/enterprisearchive/runtime_credentials.go), and runtime-fidelity helpers. | Share common request decisions after source resolution, preserving the authored execution graph. |
| LoadRunner delegated results | The [enterprise DevWeb ZIP adapter](../pkg/convert/enterprisearchive/zip.go) constructs a result without transferring the delegated converter's `ProtectedRequirements`. Genuine credentials currently remain in executable fields; unavailable-value requirements can carry source/status information that later generic discovery cannot recreate. | Test requirement identity and availability through actual import/save paths, not only requirement counts. Do not infer lost usable secrets or a disclosure defect from this omission alone. |
| Nested HTTP requests | [LoadRunner sequence construction](../pkg/convert/enterprisearchive/grouping.go) copies fields manually into `map[string]any`. For actions already admitted by `isRequestGroupLeaf`, `sourceSequenceChild` silently drops `ThinkTimeAfter` (`think_time_after`, which [sequence validation](../pkg/dsl/catalog_http_validation.go) explicitly validates) and `Attribution` (`attribution`). Meanwhile, `BodyMultipart` (`body_multipart`) is currently blocked from sequence folding by `isRequestGroupLeaf` (`action.BodyMultipart != nil`) and rejected on sequence children by `validateHTTPSequenceRequestIssues`, even though `httpSequenceChildActions` in `pkg/engine/vuser_execute.go` decodes children into full `dsl.Action` structs. | Fix the immediate `ThinkTimeAfter` and `Attribution` drops in `sourceSequenceChild`, and enable grouped `BodyMultipart` across `grouping.go`, `pkg/dsl/catalog_http.go`, `pkg/dsl/catalog_http_validation.go`, and `pkg/dsl/varflow` in lockstep with a multi-request transaction reproducer. |
| Tests | [Import contracts](../pkg/convert/importcontract/contract_test.go) check per-importer stability and common invariants. HAR, proxy, and LoadRunner have wire-replay corpora, but those do not establish general equivalence between sources or whole workflows. | Add paired fixtures and independent expected traffic to the existing infrastructure. |

The scope is authoring and import consistency. This inspection is not a claim that
every importer or every platform subsystem has been audited.

## Equivalence contract and boundaries

Use three explicit levels of equivalence:

1. **Conversion structure:** equivalent capture evidence under the same resolved
   policy, or equivalent authored representations such as C/JavaScript twins,
   should produce the same executable graph after narrowly defined provenance
   normalization. This is not a requirement that an authored LoadRunner script
   have the same graph as a capture of one of its iterations.
2. **HTTP behavior:** compare effective settings, methods/targets, redirects,
   cookies, assertions, correlation bindings, and the relevant request dependency
   order. Require exact body bytes when the source contract requires them. For
   generated multipart or compressed representations, use a declared comparison
   of ordered parts/payload and encoding policy. Preserve duplicate part names,
   filenames, media types, part bytes, and relevant headers; do not normalize body
   whitespace or encoding indiscriminately. Compare ordered header occurrences
   within each name, without claiming cross-name HTTP wire order or spelling.
3. **Lifecycle and workload behavior:** independently verify phases, transaction
   status, waits, pacing/iterations, resource concurrency, async lifetime, blockers,
   credential readiness, and source-to-action/baseline mappings. A single replay
   with matching requests cannot qualify these properties.

Allow only named presentation/provenance differences, such as creation time,
source label, source filename, and source location (including non-interpolated
evidence keys governed by `messaging.MetadataKeyIsEvidence`). Do not discard all
metadata: metadata also carries execution gates and runtime-relevant evidence. If
variable names differ, prove matching producer/consumer bindings using
`pkg/dsl/varflow` (`varflow.Analyze`, `varflow.StepOutputs`, `varflow.ActionReferences`)
before normalizing them. Keep `http_sequence` wrapper `Target` values as
attribution-only (`varflow` reads child requests in `params.requests[*]`).

Preserve these source boundaries:

- A redacted exported HAR or a synthetic debugger HAR (`pkg/api/script_debug_har.go`
  with `body_mode` / byte-budget omissions and `pkg/scrub/body_text.go` inline JS
  credential redaction) is different evidence from the private recording used for
  conversion. Keep correlation before disclosure projection and retain the existing
  protected credential handoffs. Do not expose raw credentials to make public
  results identical, and surface explicit body-omission/redaction provenance when
  synthetic or bounded HARs (`browser_native_har_v1`) are re-imported.
- Proxy client/origin legs and browser HTTP/2 or HTTP/3 observations may differ.
  Preserve version provenance and replay-fidelity review.
- Browser HTML-level HTTP output requires DOM-derived step evidence. Compare
  URL-level outputs by default; compare HTML-level outputs only when equivalent
  HTML-step evidence is available.
- Recorder edits and APEX HAR extensions can carry names, classifications, and
  timing that a third-party HAR does not contain. Preserve these annotations.
- LoadRunner source declarations outrank capture inference. An authored extractor,
  `lr_think_time`, async registration, concurrent group, or parameter policy must
  not be replaced by a heuristic inferred from a single recorded iteration.
- Retain project authorization, source-session ownership, attestation, revision
  checks, approval gates, multi-script atomic saves, and distinct input limits.

## Proposed ownership and policy

Keep source parsers responsible for source meaning. Reuse `recordingprofile` for
saved recording options, `sample` for captured exchanges, `recordedshape` for
capture-derived shaping, `requestimport` and `pkg/dsl/varflow` for common request
projection and execution-order variable readiness, and `pkg/internal/importreview`
plus `recordbridge` for workflow composition.
Place stateless composition below API and recording ownership adapters; accept
normalized evidence and resolved-policy data rather than a service or storage
handle. Profile lookup, request parsing, authorization, persistence, and session
ownership stay in the adapters. Preserve the existing
[dependency guard](../pkg/internal/importreview/dependency_test.go), which forbids
`internal/importreview` from importing API, storage, recordbridge, or recordagent
even transitively (and keep low-level OS/filesystem helpers in `pkg/internal/localfs`
and `pkg/internal/procidentity` free of higher-level authoring imports). Keep
lower-level converters independent of higher-level review orchestration; choose an
existing owner or a small lower-level package only after checking the import graph.

Resolve conversion settings once: explicit request choice, then selected project
profile, then common defaults. Source declarations and recorded capture choices
are inputs with their own precedence, not values to overwrite with defaults.
Preserve unset versus false and unset versus empty-list behavior. Return a bounded,
disclosure-safe effective-settings summary with policy version and reasons for
unavailable capabilities or source-dependent differences.

For the proposed `unified_v1` policy, recommend applying high-confidence inferred
correlations and retaining other candidates for review. Preserve the existing
wider HAR behavior through the legacy policy, and support explicit review-only
behavior. Reuse `correlate.HighConfidence`; no policy may bypass encoding,
missing-evidence, or credential protections. Before enabling `unified_v1`, freeze
fixture-backed defaults for correlation, replay headers/initial cookies, and native
rewrite selection. A policy name with unresolved defaults cannot establish parity.

Resolve replay headers and cookies independently from capture retention. Transport
headers remain runtime-owned. Explicit project header choices and intentional
source-authored headers remain authoritative where replay supports them. Define
the remaining browser-header and initial-cookie defaults against the fixture
matrix before changing them. Preserve a proxy session's explicit capture choice;
conversion cannot restore data it did not retain. Any common replay default that
changes existing behavior needs an intentional compatibility record and visible
effective-settings explanation.

### Compatibility and conversion identity

Add a negotiated/versioned conversion-policy selector. Omitted policy on existing
clients retains the documented legacy route behavior; upgraded dashboard/MCP
callers explicitly request `unified_v1`. Unknown versions fail clearly, and an old
controller must not silently ignore a requested policy and present legacy output
as unified. Qualify equivalent settings within the same policy version. A later
change to omitted-option defaults requires a separate compatibility decision.

Bind each prepared draft and its verification/save receipt to the admitted source
revision, resolved effective settings/profile revision, policy version, selected
output, and selected edits/bindings. Resolve and freeze these inputs once per
operation. A changed profile or policy requires a new preview/qualification;
retries use the existing prepared intent rather than resolving today's defaults.
Reuse current fingerprints and receipts instead of introducing a second ownership
mechanism. Public summaries expose safe policy facts, not private captured values.

Fix both the primary attested-claim path and the legacy fallback in
[recorder preview](../pkg/recordbridge/conversion_service.go) and
[script recording attestation](../pkg/api/script_recording.go):
- On the primary `importClaims` path, `HandlePreviewRecordingConversion` and
  `attestRecordingBaselineCapture` currently compute `RecordingBaselineSnapshotDigest`
  and persist `observations` via `recorder.SessionBaseline(session)` (`ConvertSession`
  with default `ConvertOptions{}`), ignoring the preview request's `ResourceMode`,
  `AsyncScan`, `Profile`, `HTTPVersionMode`, and any graph-changing rewrites. Both
  the claim's baseline digest and the persisted `observations` must come from the
  option-resolved conversion result (`result.Baseline`, matching direct
  `POST /api/v1/recorder/{id}/convert`), never from a default-options reconversion.
- On the legacy un-attested fallback path, it reconverts using only the
  parameterization toggle today. Old drafts must continue through the legacy
  algorithm only when no non-default options or unified policy were requested, or
  else receive an explicit refresh-required result; they must never be attested
  against a mismatched conversion graph or baseline.

## Delivery sequence

Each row is a separate reviewable implementation slice. U1 starts with LoadRunner
fixtures so archive compatibility is a prerequisite from the beginning. U5 is a
required workstream of this plan, not an optional postscript.

The two concrete U5 preservation fixes can proceed after their U1 reproducers,
independently of U2–U4. U1 seeds a small representative corpus; each subsequent
slice adds its own acceptance cases. Land the harness with passing characterization
tests, and land each failing correctness regression together with its fix. Do not
leave permanently skipped tests or normalize away a known defect to obtain parity.

| Slice | Deliverable | Dependencies | Exit condition |
| --- | --- | --- | --- |
| U1 | Minimal evidence/capability matrix, comparison contracts, and paired fixtures | None | Sequential fidelity, timing/concurrency, and lifecycle proofs are distinguished; first capture and LoadRunner reproductions are ready. |
| U2 | Versioned option resolution, frozen defaults, and policy propagation | U1 | Equivalent effective settings across all five capture/HAR entry points (including `/import/salesforce` and `/import/sample`) and enabled native stages; overrides, legacy omissions, and policy negotiation covered. |
| U3 | Shared ordered HTTP draft composition and option-bound baseline identity | U1, U2 | All mutations finish before reconciliation; qualified mappings/baselines and UUID/timestamp/native-rewrite behavior agree for equivalent inputs. |
| U4 | Common review/preparation contract, per-output browser companion preview, and durable round trips | U2, U3 | Preview, guided repair, save, reopen, editor save refresh, and replay preserve the same qualified executable definition and blockers. |
| U5 | LoadRunner delegated-result preservation, nested request projection, and web conformance | U1; U2/U3 only for applicable shared behavior | URL/HTML web imports and supported DevWeb paths preserve authored semantics and protected handoffs while satisfying common request contracts. |
| U6 | Client alignment, documentation, compatibility qualification | U2–U5 | Applicable controls and effective settings agree, both dashboard auth profiles pass, and intentional deltas are recorded. |

### U1 — Establish evidence and independent expectations

Extend [importcontract](../pkg/convert/importcontract) and the existing
[wire fixture harness](../pkg/internal/paritytest). Represent one synthetic
workflow as a private proxy session, equivalent full HAR, browser HTTP artifact,
HAR submitted through the sample importer, and (for Salesforce-detected traffic)
HAR submitted through `/api/v1/scripts/import/salesforce`. Add a LoadRunner URL-level
web script with equivalent resolved request facts; include C/JavaScript twins where
the existing corpus supports them.

For sequential cases, compare normalized executable structure and replay each
result against independently specified expected requests. Return fresh tokens and
business IDs to catch scripts that merely replay recorded literals. Use existing
hooks or asserted freshness/reuse relationships for UUID/time checks. Include both
successful responses and failed checks/extractions; two scripts succeeding on a
permissive fixture is insufficient.

The existing `paritytest.ReplayOnce` sets `IgnoreThinkTime: true` and runs one
debugger iteration; `Server.Compare` imposes total arrival order. Keep that lane
for sequential request fidelity. Add controlled engine tests for waits, pacing,
iteration counts, and transaction duration/status, plus a bounded dependency-order
and concurrency oracle for parallel resources and async lifetimes. Assert required
overlap/start-stop ordering and absence of extra traffic; do not sort all requests
and thereby hide a scheduling regression. Use synchronization or injected clocks
where possible rather than fragile exact wall-clock expectations.

Use the harness's existing multipart-part and decoded-body expectations for the
declared semantic comparisons. Extend multivalue response fixtures and per-name
header-occurrence assertions: its current response header map and comma-joined
request checks cannot distinguish all duplicate-header cases. Prove that separate
occurrences differ from one comma-containing value, particularly for Set-Cookie
correlation sources.

Keep per-importer golden results for source-specific metadata. Add assertions that
the comparator detects changed gates, header occurrences, body bytes, transaction
placement, and broken variable bindings (verified via `pkg/dsl/varflow` and its
shared readiness corpus `pkg/dsl/varflow/testdata/readiness_cases.json`). Equality
between two converters alone cannot establish correct behavior.

### U2 — Resolve policies once

Replace source-specific correlation switches with a named resolved policy. Keep
candidate detection separate from automatic application and review. Cover disabled
scan, disabled rules, user rules, low/high confidence, incomplete analysis, encoded
consumers, and explicit compatibility behavior.

Propagate the resolved policy into every enabled native analyzer and rewriter,
not just `sample.Convert`. Salesforce's second scan must honor disabled scans and
rules, excluded values/content types, user-rule precedence, review-only application,
and applicable assertion/resource options. Include `POST /api/v1/scripts/import/salesforce`
([handlers_import_salesforce.go](../pkg/api/handlers_import_salesforce.go)), which
currently skips the project recording-profile correlation lookup performed by
`handleImportHAR` and forces native Salesforce rewrite with default correlation
settings. Use detected-Salesforce fixtures that would otherwise trigger the second
scan. Verify that a later stage cannot restore a forbidden inference or change a
policy decision silently; report unsupported combinations explicitly.

Use a named recorder conversion request shared by preview/save, following the
browser recorder's existing [preparation pattern](../pkg/recordbridge/conversion_prepare.go).
Resolve title checks, resource mode, async scanning, HTTP-version mode, applicable
assertion/extractor options, think-time options, and optional input parameterization
consistently. Reuse the existing suggestion/apply machinery for common HTTP input
parameterization rather than maintaining a proxy-only rewrite. Specify data-source
generation, naming/collision handling, selected consumer bindings, and protection
of server-correlated values. Reuse the browser HTTP companion's existing selected
parameterization and secret-binding paths, and extend
`HandlePreviewBrowserRecordingConversion` ([conversion_service.go](../pkg/recordbridge/conversion_service.go))
so dual-script previews return parameterization suggestions, secret-binding
requirements, and preflight for `http_script` alongside the browser script rather
than only for `result.Script`. Preserve or explicitly migrate the proxy's existing
governed credential data rows and source-encoded bytes; a blanket credential
exclusion must not discard an operator-supplied value. Keep credentials out of
ordinary public suggestion/sample projections. Browser and HTTP outputs must each
report whether an automatic choice or reviewed selection applies. Preserve authored
LoadRunner parameters and their allocation policies; extra parameterization requires
an explicit selection. Make both `/api/v1/scripts/import/sample` and
`/api/v1/scripts/import/salesforce` use the same applicable project-profile
resolution. Inapplicable options need an explicit disposition rather than silent
acceptance.

Inventory capture-time filtering versus replay-time header/cookie decisions. Test
initial cookies separately from server-issued cookies and cookie-jar replay,
including explicit headers and duplicate occurrences. Preserve profile changes
made after capture and report irreversible omissions (including synthetic debug HAR
`body_mode` / byte-cap omissions from `pkg/api/script_debug_har.go` and native
browser HAR `browser_native_har_v1` size-cap omissions).

### U3 — Share ordered draft composition

Provide one composition operation used by the dedicated HAR route, Salesforce HAR
route, sample-HAR route, proxy HTTP conversion, and browser URL-level HTTP
conversion. Keep transport and source-security adapters around it.

Make the processing stages and their mutation boundaries explicit:

1. Normalize and correlate private evidence under the resolved policy; retain the
   timestamp analysis/application that must happen before async folding.
2. Shape captured traffic and select the native output permitted by policy. Apply
   all executable augmentation, including optional Agentforce transformation.
3. Complete allowed UUID/timestamp rewriting on the chosen output, then apply
   selected parameterization and secret bindings to that output. Preserve
   source-generated values and revalidate any selection invalidated by a rewrite.
4. Reconcile final correlation producers/consumers (via `pkg/dsl/varflow`),
   credential requirements, source mappings, completeness, and baseline coverage
   against the resulting graph. Produce read-only review/preflight
   (`LateProducedVariables`, `ParamBlockingVariables`, `ConfigBlockingVariables`,
   `PlaceholderVariables`), persistence preparation, and the final identity
   (including the option-resolved `Baseline` and `RecordingSnapshotDigest`) used by
   existing verification/save mechanisms. If a later preparation step changes
   executable content, requalify that final content.

`ImportDraft.AugmentWithAgentforceTransform` is a mutating stage, despite its
review-oriented name. It must precede final reconciliation and qualification.
No stage may mutate an already-qualified executable graph without invalidating
that qualification. Test stage ordering, repeated preparation, stale selection
rejection, and absence of duplicate extractors/parameters/diagnostics.

Timestamp refresh already exists inside
[the sample shaping path](../pkg/convert/sample/recorded_shape.go); the outer pass
can also handle a replacement native script. UUID refresh currently lives in
[import-review helpers](../pkg/internal/importreview/handlers_scripts.go).
Do not move every pass to the end indiscriminately. Make optional native rewrites
an explicit common policy, preserving current feature restrictions and fallback
findings. Graph-changing rewrites (and non-default recorder `ResourceMode` or
`AsyncScan` options) must remap the baseline to valid destinations and account for
omitted or folded observations; never attach old or default-options action IDs to a
new graph. Update `HandlePreviewRecordingConversion` and
`attestRecordingBaselineCapture` so the attested baseline snapshot digest and stored
`observations` always reflect the option-resolved `result.Baseline`. An informational
coverage gap is insufficient where durable save requires a baseline. If a rewrite
cannot meet that contract, retain the qualified pre-rewrite draft with an explicit
warning when policy allows fallback, otherwise reject the dependent output/save.
Preserve existing multi-output save rules and require a refreshed preview if
fallback changes what the user reviewed.

Preserve input/decompression, correlation-work, and traversal bounds across the
combined pipeline. Reuse parsed evidence where supported instead of repeatedly
parsing or copying a large private HAR for each pass. Tests should cover budget
exhaustion, empty results, malformed input, and cleanup on failed composition.
Return the existing incomplete-analysis/gating dispositions; no unbounded retry
or silent promotion of partial evidence is introduced.

### U4 — Preserve review and durable behavior

Share applicable coded findings, correlation review status, completeness
accounting, and effective options while retaining format-specific statistics and
public response compatibility. Reuse `convertresult.Accounting`; do not force
requests, source functions, and archive projects into the same denominator.

Align durable save, reopen, guided repair, and editor-save refresh across all
authored HTTP scripts:
- Ensure preserved import/recording review metadata (`fidelity_report`,
  `replay_preflight`, and `missing_producer:*` / `conditional_producer:*` step
  dependency codes) is compatible with `UpdateScriptEditorSaveDurable` and
  `refreshSavedImportEvaluation` (`pkg/api/script_save_import_evaluation.go`) as
  well as `POST /api/v1/scripts/{id}/repairs/{preview,apply}` (`pkg/api/script_repairs.go`).
- Preserve the `RetainedImportCredentialContextChange` boundary so edits to
  non-executable review subtrees (`fidelity_report`, `replay_preflight`) or
  non-interpolated evidence metadata (`messaging.MetadataKeyIsEvidence`) never
  falsely strip preserved credential policy or trigger spurious secret re-entry.
- Preserve direct save of reviewed drafts edited with guided repairs,
  parameterization, or shared values (`reviewedDraftEdits`), without reconverting
  the source archive and overwriting operator repairs.

Retain the separate handling of server-owned executable recordings, uploaded
protected source values, and disclosure-safe review projections. Exercise actual
preview/save HTTP requests, durable reopen, guided repair preview/apply, editor
save, replay, source-session expiry, changed-profile conversion, stale replies, and
edits during verification. Assert that a late response cannot qualify a different
draft. Browser preview must surface base conversion, per-output preflight,
parameterization suggestions, and secret-binding requirements for both the browser
script and its HTTP companion, while verification/save apply selected edits per
`script_index`. Compare the same resolved selections and bindings at those
boundaries; raw preview JSON need not equal a deliberately edited persisted draft.
Test whole output sets, expected script counts, profile revisions, and existing
replayed save receipts.

Extend the [existing lifecycle matrix](qa/LIFECYCLE_PARITY_MATRIX.md) and
[AUTH-C workstream](ROADMAP_IMPLEMENTATION_SPEC.md#auth-c--lifecycle-edges-and-property-level-parity).
Use the [adapter retirement inventory](qa/ADAPTER_RETIREMENT.md) before removing
any caller or wrapper. Common conversion does not remove authorization or revision
boundaries.

### U5 — Include LoadRunner imports as authored tests

Cover enterprise archive Web HTTP/HTML, supported JavaScript web scripts, and
DevWeb both directly and through the archive dispatcher. Include TruClient at the
browser-selector/review boundary; keep Java Vuser and unsupported protocol families
on their existing governed execution/review paths.

Deliver U5 in three bounded sub-slices:

1. **Delegated DevWeb result preservation.** Compare the same admitted ZIP through
   the direct DevWeb importer and the enterprise archive dispatcher. Its current
   adapter copies selected result fields while leaving the delegated protected
   requirements behind. Use an unavailable `rts.yml` proxy credential and a genuine
   credential control: the latter should remain executable under the current
   preserve policy. Compare requirement source file/pointer, availability, target
   binding, reason/context, findings, gates, accounting, save, and reopen. Generic
   rediscovery of a secret reference must not mask the loss of its source facts.
   Add a named result adapter that explicitly transfers or maps every relevant
   field, while retaining source identity and format-specific stats. Transfer
   `ProtectedRequirements` through `Take`/`Store` and clear temporary values; its
   populated container must not be copied by value. Cover consume-once and failure
   cleanup.
2. **Lossless nested HTTP request projection.** Compare leaf, sequence, async,
   and concurrent forms across supported source adapters. Share a bounded typed
   child projection/encoding contract where appropriate; keep source-specific
   grouping and timing rules:
   - Fix the immediate field drops in `sourceSequenceChild`
     ([grouping.go](../pkg/convert/enterprisearchive/grouping.go)): copy
     `ThinkTimeAfter` (`think_time_after`, which [catalog_http_validation.go](../pkg/dsl/catalog_http_validation.go)
     already validates on `http_sequence` children) and `Attribution` (`attribution`),
     both of which `isRequestGroupLeaf` admits into `http_sequence` today.
   - Enable structured `BodyMultipart` (`body_multipart`) inside `http_sequence`
     children in lockstep across all four layers rather than in `sourceSequenceChild`
     alone: (a) allow `action.BodyMultipart != nil` in `isRequestGroupLeaf` and copy
     `body_multipart` in `sourceSequenceChild` ([grouping.go](../pkg/convert/enterprisearchive/grouping.go)),
     (b) replace the child `body_multipart` rejection in `validateHTTPSequenceRequestIssues`
     ([catalog_http_validation.go](../pkg/dsl/catalog_http_validation.go)) with
     `validateMultipartBodyIssues("http", childLabel, decodedChild)`, (c) update the
     `body_multipart` field documentation in [catalog_http.go](../pkg/dsl/catalog_http.go)
     and regenerate `docs/dsl.schema.json` + `docs/DSL_ACTION_REFERENCE.md` via
     `go generate ./pkg/dsl`, and (d) verify `pkg/dsl/varflow` and
     `collectActionFileReferences` (`pkg/dsl/validate.go`) scan `body_multipart`
     template and `{{file.*}}` references inside `http_sequence` children (while
     preserving `varflow`'s rule that the `http_sequence` wrapper `Target` is
     attribution-only).
   - Test structured multipart, binary/base64 and `c_escaped` bodies, repeated
     headers, auth, optional version/redirect fields, SOAP, assertions, extractors,
     gates, `think_time_after`, `attribution`, and credential/source locator paths.
     Preserve scalar types and optional-field presence; a whole-script JSON clone is
     not a substitute for defining the contract. The multipart regression must
     contain an upload followed by another HTTP request in the same transaction (or
     concurrent group): sequence compaction requires at least two requests, so the
     existing single-request `multipart_upload` fixture does not reach sequence
     folding. Add a nested control-flow case and assert the parts survive
     conversion, `dsl.ValidateScriptIssues`, save/reopen, and replay. Keep this fix
     independently deliverable from broad finalizer adoption.
3. **Shared finalization conformance.** Inventory request and script invariants
   already covered by the LoadRunner component passes, then adopt only the missing
   shared decisions. Qualify C/JavaScript twins, archive versus lower-level web
   conversion where their inputs match, and captures with equivalent HTTP facts.

Audit an already-resolved HTTP leaf against `requestimport.EffectiveRequest` and
its finalization contract. Adopt shared body/header/encoding, multipart, credential,
and fidelity helpers incrementally where they preserve the source definition.
Keep source registration ordering and post-parameter-file finalization intact.
Do not route a LoadRunner archive through HAR or apply recording-profile defaults
over explicitly authored runtime settings.

Treat explicit LoadRunner correlation registrations as authored behavior. Their
scope, ordinal, conversion rules, failure policy, and attachment to the correct
response must survive irrespective of the confidence threshold used for inferred
HAR correlations. Infer additional values only with adequate retained evidence,
an explicit policy, and no conflict with authored producers or parameter names.
Do not refresh an intentionally fixed or parameter-generated UUID/timestamp using
capture heuristics.

Extend the [LoadRunner web corpus](../pkg/convert/enterprisearchive/testdata/lr_web_parity)
and its C/JavaScript twins. Pair equivalent cases with HAR/proxy fixtures for URL
requests, redirect login, headers/cookies, duplicate form keys, raw/binary bodies,
multipart uploads, and supported resource handling. Compare observable request
semantics for these pairs; retain additional authored transaction and lifecycle
assertions on the LoadRunner side.

Start from `url_mode_login_flow`, `referer_header`, `cookie_injection`,
`web_set_user_host_scope`, `body_variants`, `multipart_upload`, `resource_steps`,
`async_poll`, and `websocket_chat_after_login`, plus gated encoding/registration/
callback cases. Use HTML-mode cases only when equivalent HTML-step evidence exists.

Add explicit preservation cases for init/action/teardown, pre-registered extractors
and checks, loops and conditions, concurrent groups, authored async start/stop,
transaction status, parameters and data allocation, pacing/iterations, runtime
HTTP/proxy settings, missing assets, masked values, and unsupported callbacks.
Missing source constructs must remain review-gated. A one-iteration traffic match
does not certify runtime-settings or multi-iteration equivalence.

### U6 — Align clients and release behavior

Use common/generated API request types for applicable options in Recording Studio
(`useRecorderApi.ts` `convertRecording` / `previewRecordingConversion`), dedicated
HAR import, Salesforce HAR import, and sample import. Display resolved policy,
per-output readiness/blockers (consistent with `dashboard/src/components/variableFlow.ts`),
and evidence gaps; only expose controls supported by the source. Keep browser-only
HTML controls distinct from ordinary URL-level conversion. MCP should continue
delegating to the REST contracts rather than implementing conversion logic.

Enable the negotiated common policy only after U2's defaults and U3/U4's identity
and save behavior qualify. New dashboard/MCP callers request and verify the policy
version; legacy omitted-field requests retain their compatibility path. Preserve
saved scripts, retained source readers, and protected import sessions. Reimport
shows intentional behavior changes and creates a newly qualified draft. A rollback
disables creation under the new policy without rewriting existing scripts or
silently reinterpreting prepared drafts; unsupported pending drafts require refresh.
Record the resolved policy version and update frozen import-contract baselines
only with a reviewed explanation of each delta.
Update [recording profiles](RECORDING_PROFILES.md),
[script lifecycle](SCRIPT_LIFECYCLE.md), and
[import fidelity](IMPORT_FIDELITY_AND_REPAIRS.md) alongside implementation.

## Other unification opportunities

These are ranked follow-ups; they do not all block the capture/LoadRunner plan.

| Priority | Opportunity and evidence | Recommended scope and limits |
| --- | --- | --- |
| High | HTTP body classification is repeated in [sample](../pkg/convert/sample/request_pipeline.go), [k6](../pkg/convert/k6/requestimport.go), [JMeter](../pkg/convert/jmeter/requestimport.go), and [DevWeb](../pkg/convert/devweb/requestimport.go). Whitespace-only bodies and JSON detection differ. | Share a bounded classifier using explicit source declarations, then effective media type, then conservative inference. Preserve bytes, encoding, duplicate-header authority, and templated source semantics. Include LoadRunner's existing multipart/body helpers in conformance tests. |
| High | [DevWeb conversion](../pkg/convert/devweb/converter.go) intentionally skips `FinalizeScript` because of mixed protocols and known ZIP parameter/data-source constraints. | Inventory missed invariants, fix the documented ZIP constraints, then qualify HTTP-only whole-script finalization. Add protocol-aware validation for mixed scripts before wider adoption. Include traceparent defaults, source variables, base-URL credentials, and requirement remapping. |
| Medium | [Pure WebSocket proxy conversion](../pkg/recorder/converter.go) bypasses shared mixed-traffic shaping and has independent timing/header/message defaults. | Share frame-to-action and policy helpers, with pure/mixed fixtures; retain supported WebSocket-specific runtime behavior. |
| Medium | [Browser replay to HTTP](../pkg/api/browser_to_http_conversion.go) and retained debug HAR export (`pkg/api/script_debug_har.go`, `pkg/recordbridge/synthetic_har.go`) build synthetic HARs through their own options and review paths, while `BE-B3`/`BE-B4` adds bounded native browser HAR capture (`browser_native_har_v1`) and HA optional source-cache receipts (`_collection/optional_receipt.json`). | Add browser-replay-to-HTTP and retained debug/native HAR re-import as adapters to shared policy/composition when evidence permits. Retain replay provenance, `ScriptDebugHARAvailability.Limitations`, body-retention/truncation bounds, inline JS credential redaction markers, and evidence gates. |
| Medium | Public credential-requirement projection loops differ in k6, JMeter, Postman, and DevWeb. | Share explicit disclosure projection and one-shot protected-handoff helpers. Investigate availability/expression-field differences; they are not by themselves evidence of a disclosure vulnerability. Include LoadRunner's governed parameter/credential preservation. |
| Medium | Existing [import contracts](../pkg/convert/importcontract) mostly establish invariants and deterministic outputs within each importer. | Add equivalent-request wire cases across HAR/raw HTTP/cURL/Postman/Bruno/JMeter/k6/LoadRunner where evidence matches. Keep authored control flow, source inheritance, and workload semantics in their adapters. |
| Low | Selector candidate metadata assembly repeats in [Recorder JSON import](../pkg/convert/recorderjson/converter.go) and [native browser conversion](../pkg/recorder/browser/convert.go). | Reuse the existing selector model and a small metadata builder, then evaluate TruClient callers. Preserve frame/context and source-confidence differences; no behavioral defect is established by duplication alone. |

OpenAPI/WSDL generation can share request projection and review helpers, but spec
examples are not recorded response evidence. Authored Postman/JMeter/k6/LoadRunner
control flow must not acquire capture-derived pacing or async inference merely
because those formats contain HTTP requests. Keep format-specific parsers,
protocol boundaries, and governance adapters.

## Verification and completion criteria

The fixture matrix must name the applicable equivalence level and proof lane for
each case. Cover: high/low confidence and user correlations; raw and
encoded consumers; header/form duplicates; cookies/redirects and authentication;
compressed, binary, absent, budget-omitted, and truncated bodies; runtime/in-step/separate resources;
async and WebSocket traffic; UUID/timestamp freshness; explicit/profile defaults;
annotations and transaction timing; changed action graphs and option-bound baseline mappings;
opted-in parameterization and data-source bindings; per-output browser companion preview and bindings;
credential preservation, `RetainedImportCredentialContextChange` boundaries, and disclosure sentinels;
guided repair preview/apply and editor-save import evaluation refresh; LoadRunner authored semantics;
native-stage policy enforcement across dedicated and Salesforce HAR routes;
unavailable-credential handoff; multi-request multipart and `think_time_after`/`attribution` sequence compaction;
budget exhaustion; and legacy/in-flight policy compatibility. Associate each completion claim with a
named test, an expected outcome, and any remaining qualification limit.

Place new tests beside the affected code and extend existing cross-importer
contracts and replay corpora. Run targeted Go tests while developing. Before any
build or full API suite, integrate the latest `origin/main`; use an isolated checkout
when shared work is dirty. Finish dashboard asset generation before Go work that
embeds it. Cross-package changes require the full Go suite before merge.

Dashboard changes require typecheck, lint, relevant unit tests, and affected
Playwright workflows in dev-admin and local-auth profiles. Run the full dashboard
cycle before merge for shared authoring workflows. Update generated API/DSL
contracts when their generators' inputs change; run documentation checks. Review
all changes and fix every finding before committing. Retain raw QA evidence as
ignored artifacts with code-span paths in Markdown review records.

Completion requires passing paired structural and wire tests, qualified durable
round trips, preserved LoadRunner source semantics and gates, documented intentional
deltas, and no unexplained difference for equivalent admitted evidence/settings.
Timing, concurrency, and multi-iteration claims require their own engine proofs;
the sequential one-iteration replay lane cannot close them. Unified behavior is
claimed for the negotiated common policy and supported evidence capabilities;
retained legacy semantics and unsupported combinations remain explicitly identified.
Record unsupported or unqualified cases explicitly. Close agent-opened browser
sessions and clean session build/test caches after dependent work finishes, without
clearing caches another active task needs. Local fixtures establish implementation
behavior; they do not certify every vendor archive or live application workflow.
