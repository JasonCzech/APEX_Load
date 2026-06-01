# VuGen Web 2.0 And TruClient 2.0 Web Parity Plan

## Objective

Add first-class APEX Load support for OpenText VuGen's browser-driven Web 2.0 scripting profile, represented by **TruClient 2.0 - Web** in the current LoadRunner/VuGen family. The goal is practical parity for import, authoring, replay, repair, and optional protocol-load conversion while preserving APEX Load's current claim boundary: browser VUsers are diagnostic canaries unless benchmark evidence proves broader density.

This is a new planned scope beyond the completed TruClient Mobile Web MVP. It does not reopen native desktop UI automation as a goal.

## External Baseline

OpenText positions TruClient and TruClient 2.0 as real-browser, Web 2.0 scripting workflows with plain-English steps, transactions, parameterization, JavaScript/API customization, browser recording/replay, and conversion to Web HTTP/HTML when protocol-level density is desired.

Primary references:

- TruClient - Web protocol: https://admhelp.microfocus.com/vugen/en/26.1/help/WebHelp/Content/TruClient/p_truclient_protocol.htm
- TruClient 2.0 - Web protocol: https://admhelp.microfocus.com/vugen/en/latest/help/WebHelp/Content/TruClient/TruClient-2.0/TC2-intro.htm
- Convert TruClient to Web HTTP/HTML: https://admhelp.microfocus.com/vugen/en/26.1/help/WebHelp/Content/TruClient/_tc_t_convertTruClientToWebScript.htm
- Web protocols overview: https://admhelp.microfocus.com/vugen/en/25.1-25.3/help/WebHelp/Content/VuGen/c_web_protocols_overview.htm

## Current APEX Load Baseline

APEX Load already has:

- LoadRunner Web HTTP/HTML ZIP import, runtime settings classification, validation, replay, and support-matrix review.
- TruClient Mobile Web and TruClient 2.0 Mobile Web import MVP for common browser/mobile actions.
- Browser replay, diagnostics, mobile profiles, screenshots, DOM, console, network evidence, and browser archive reporting.
- Guided authoring sessions, health scoring, AI guardrails, replay-before-save, and approval gates.
- Explicit out-of-scope handling for legacy desktop-style TruClient archives.

Remaining gap:

- Desktop browser **TruClient 2.0 - Web / VuGen Web 2.0** archives are not a first-class import target.
- APEX Load does not yet expose a named Web 2.0 browser script creation profile with object descriptors, step outline, parameterization, transaction UX, and conversion-to-protocol-load workflow.

## Scope Decision

| Area | Decision |
| --- | --- |
| TruClient 2.0 - Web import | In scope |
| New APEX Load Web 2.0 browser authoring profile | In scope |
| Browser replay/debug evidence for imported and newly authored scripts | In scope |
| Optional browser-to-HTTP conversion for protocol-load density | In scope |
| Legacy desktop/native application automation | Out of scope |
| High-density real-browser load claims | Evidence-gated non-claim |
| Byte-for-byte VuGen proprietary project compatibility | Out of scope; preserve metadata and provide practical conversion |

## Proposed APEX Load Shape

Add a named script profile:

```json
{
  "protocol": "browser",
  "metadata": {
    "import": {
      "source": "vugen_truclient2_web",
      "preserved": {
        "apexload_profile": "vugen_web2",
        "source_script_type": "TruClient 2.0 - Web"
      }
    }
  },
  "config": {
    "browser": {
      "engine": "chromium"
    }
  }
}
```

The execution runtime should remain the existing browser protocol. The new profile is an authoring/import/migration profile layered on top of browser DSL actions, review metadata, runtime settings, and replay evidence.

## Workstream V2.1: Scope Matrix And Fixture Contract

| ID | Work Item | Priority | Status |
| --- | --- | --- | --- |
| V2.1.1 | Add code-owned `TruClient 2.0 - Web` support matrix covering archive files, metadata, steps, object identification, parameters, transactions, runtime settings, and conversion limits | P0 | Complete |
| V2.1.2 | Extend `.usr` script-type detection to distinguish `TruClient 2.0 - Web`, `TruClient - Web`, `TruClient - Mobile Web`, and legacy unsupported desktop/native variants | P0 | Complete |
| V2.1.3 | Define golden fixture contract with 5-8 representative archives: login form, SPA checkout, dynamic token flow, iframe/window flow, parameterized search, transactions, waits/verify, and custom JavaScript/API step | P0 | Complete |
| V2.1.4 | Update support endpoints and import review docs so current Mobile Web support and planned Web 2.0 support are separate rows | P0 | Complete |

Implementation note: V2.1 is backed by `pkg/convert/trueclient/support.go`, the LoadRunner support-matrix endpoint, and tests in `pkg/convert/trueclient/support_test.go` plus `pkg/api/handlers_scripts_test.go`. `TruClient - Web` and `TruClient 2.0 - Web` are now recognized as planned/review scopes and classified with explicit review-artifact guidance instead of being misclassified as Mobile Web or legacy desktop TruClient.

Acceptance:

- Support matrix names every covered and non-covered construct.
- Unsupported proprietary constructs become review warnings with stable IDs.
- Fixture contract is documented before importer behavior expands.

## Workstream V2.2: Import Parser And Metadata Preservation

| ID | Work Item | Priority | Status |
| --- | --- | --- | --- |
| V2.2.1 | Extend `pkg/convert/trueclient` archive discovery beyond Mobile Web to parse TruClient 2.0 Web project metadata, `default.xml`, object repositories, parameters, and runtime files when present | P0 | Complete |
| V2.2.2 | Preserve original script type, browser target, step IDs, object descriptor IDs, transaction names, parameter definitions, snapshots, and unsupported source fragments in import metadata | P0 | Complete |
| V2.2.3 | Add import response review sections for profile assumptions, unsupported steps, object-identification confidence, runtime settings, parameter mapping, conversion-to-HTTP eligibility, and replay readiness | P0 | Complete |
| V2.2.4 | Add deterministic repair suggestions for selector stabilization, wait refinement, missing assertions, parameter source repair, unsupported JavaScript/API steps, and conversion blockers | P0 | Complete |

Implementation note: V2.2.1 is backed by `trueclient.InspectArchive`, which classifies project files, `default.xml`, object repositories, parameter files, runtime files, Web 2.0 capability statuses, and executable step inventory. `TruClient 2.0 - Web` archives with supported browser step shapes now produce review-only browser drafts and preserve the archive inspection, source script type, source profile, runtime files, project settings, and parsed parameter files in import metadata. V2.2.2 now preserves step-level IDs, indexes, object descriptor IDs, parsed object repository descriptors, frame/window context, parameter references, snapshot references, transaction source labels, and review/unsupported source snippets. V2.2.3 is surfaced through LoadRunner import review sections for Web 2.0 profile summary, unsupported/review-only fragments, object identification, parameter mapping, replay readiness, and HTTP conversion eligibility. V2.2.4 adds deterministic repair suggestions for replay evidence, object identification, parameter fit, source-fragment repair, and HTTP conversion gating.

Acceptance:

- A TruClient 2.0 Web ZIP produces a browser-profile draft instead of an out-of-scope error.
- No unsupported step is silently dropped.
- Import review can be rendered by the existing LoadRunner import dashboard path.

## Workstream V2.3: Step Mapping

| Step Family | APEX Load Mapping | Priority |
| --- | --- | --- |
| Navigate / page transition | `browser_navigate` | P0 |
| Click / double-click / submit | `browser_click` plus action metadata | P0 |
| Type / set / clear | `browser_fill` | P0 |
| Select / checkbox / radio | `browser_select`, `browser_click` | P0 |
| Wait / sync / object exists | `browser_wait` or `wait_for` on action | P0 |
| Verify text/title/url/object | action assertions | P0 |
| Start/end transaction | transaction metadata and action grouping | P0 |
| Think time | `think_time` on actions or script config | P0 |
| Scroll / hover | `browser_swipe`, planned hover action if needed | P1 |
| Iframe/window/tab context | browser context metadata and replay validation | P1 |
| Conditions/loops | review-first mapping to grouped action blocks; executable subset where deterministic | P1 |
| JavaScript/API/C custom code | decompose safe patterns; preserve and warn for unsupported code | P1 |
| Screenshots/snapshots | browser capture config and archive evidence | P1 |

Acceptance:

- P0 steps replay in APEX Load browser runtime.
- P1 steps either replay or show guided review with repair suggestions.
- Transactions and verification evidence survive import.

Implementation note: The converter now maps VuGen Web 2.0 click-like steps including `Double Click`, `Submit`, checkbox selection, checkbox uncheck, and radio selection into `browser_click` actions with source metadata and intent parameters. Hover remains review-only until a public browser hover action is available.

## Workstream V2.4: Object Identification

| ID | Work Item | Priority | Status |
| --- | --- | --- | --- |
| V2.4.1 | Add object descriptor model to import metadata: selector, XPath, text, role, attributes, ordinal, regex, frame/window context, and fallback candidates | P0 | Complete |
| V2.4.2 | Map stable descriptors into CSS/role/text selectors where safe | P0 | Complete |
| V2.4.3 | Add selector confidence and stability scoring to TruClient 2.0 Web import review | P0 | Complete |
| V2.4.4 | Add guided repair to choose alternate selectors from replay DOM evidence | P1 | Complete |
| V2.4.5 | Add replay diagnostics for object not found, ambiguous match, frame mismatch, detached DOM, and timing timeout | P1 | Complete |

Acceptance:

- Imported object IDs stay traceable to original VuGen descriptors.
- Replay failures explain whether the problem is selector, frame/window, timing, or unsupported construct.

Implementation note: Archive inspection now parses object repositories into descriptor records with CSS, XPath, text, role, common HTML attributes, ordinal, regex, frame context, and window context. Step inspection merges repository candidates with step-level candidates, ranks candidates by confidence, preserves parameter/snapshot references, and emits replay diagnostics for object-not-found risk, ambiguous ordinal matching, frame/window context, regex selectors, and timing/low-confidence risk. The dashboard Object Descriptors dialog surfaces context diagnostics and lets users apply alternate selector candidates as guided repairs before replay approval.

## Workstream V2.5: APEX Load Creation UX

| ID | Work Item | Priority | Status |
| --- | --- | --- | --- |
| V2.5.1 | Add "VuGen Web 2.0 / TruClient 2.0 Web" starter profile to Create Script | P0 | Complete |
| V2.5.2 | Add graphical step outline focused on browser actions, transactions, parameters, waits, and assertions | P0 | Complete |
| V2.5.3 | Add dialogs for object descriptor editing, parameter data, runtime settings, replay output, and conversion-to-HTTP review | P0 | Complete |
| V2.5.4 | Add reusable Web 2.0 browser components: login, search, add-to-cart, checkout, iframe flow, modal flow, auth callback, and teardown/logout | P1 | Complete |
| V2.5.5 | Add AI-assisted repair hooks using the existing governed AI workflow for selector/wait/assertion/data-source repairs | P1 | Complete |

Acceptance:

- Users can create a Web 2.0 browser script without writing JSON.
- The central script editor remains focused on outline and script code; supporting evidence appears in dialogs or side lanes.
- New drafts use authoring sessions and approval gates.

Implementation note: The authoring capabilities endpoint now exposes `trueclient2_web` as a review-only browser-replay gated source, reusable Web 2.0 browser components can be filtered by the profile, and the backend template catalog includes a `VuGen Web 2.0 Review Profile` starter for migration review drafts. The dashboard script editor now renders a graphical outline that summarizes browser actions, transaction coverage, parameter references, waits, and assertions while keeping configuration, RTS, parameter data, replay output, imported metadata, object descriptor review, and browser-to-HTTP conversion review behind dialogs. Governed AI repair suggestions now include Web 2.0 selector repair hooks alongside existing wait, assertion, and data-source repair categories.

## Workstream V2.6: Runtime Settings And Replay Evidence

| ID | Work Item | Priority | Status |
| --- | --- | --- | --- |
| V2.6.1 | Map TruClient 2.0 browser/runtime settings into APEX Load browser config, web runtime config, log config, think time, pacing, proxy, and capture settings | P0 | Complete |
| V2.6.2 | Extend runtime settings support matrix with `vugen_web2` profile entries and status classification | P0 | Complete |
| V2.6.3 | Add browser replay timeline labels that preserve original TruClient step IDs and transaction groups | P1 | Complete |
| V2.6.4 | Archive screenshots, DOM, console, network, Core Web Vitals, and object-match diagnostics for imported Web 2.0 scripts | P1 | Complete |
| V2.6.5 | Add report/profile sections for browser Web 2.0 migration evidence | P1 | Complete |

Implementation note: V2.6 is backed by first-class `vugen_web2` / `trueclient2_web` runtime settings support-matrix entries and the `vugen_web2_migration_review` preset. TruClient source step IDs, indexes, source actions, transaction groups, and profile labels are now attached during import and carried into browser replay events and archive transaction records. Archive reports include `web2_migration` JSON/CSV evidence for replay status, captured browser artifacts, transaction groups, source profiles, and HTTP-conversion readiness.

Acceptance:

- Runtime settings support is visible before save, before replay, and in reports.
- Replay evidence can prove or reject migration readiness.

## Workstream V2.7: Browser-To-HTTP Conversion

| ID | Work Item | Priority | Status |
| --- | --- | --- | --- |
| V2.7.1 | Add "Generate protocol-load script" action from Web 2.0 browser profile | P1 | Complete |
| V2.7.2 | Capture replay network flow as HAR-like evidence and convert to APEX Load HTTP DSL | P1 | Complete |
| V2.7.3 | Detect dynamic values, candidate correlations, parameter sources, auth headers, cookies, and request body variables | P1 | Complete |
| V2.7.4 | Produce side-by-side browser script and generated HTTP script review with correlation/data-source repair suggestions | P1 | Complete |
| V2.7.5 | Add scenario template for "browser canary plus generated protocol load" | P1 | Complete |

Implementation note: V2.7 is backed by `POST /api/v1/scripts/browser-to-http/preview`, which accepts browser replay network entries or a replay session ID, converts compact replay network evidence into HAR-like input for the existing HAR importer, and returns a generated HTTP DSL draft plus conversion review evidence. The review flags dynamic query values as correlation/data-source candidates, records auth and cookie follow-up findings, skips static browser assets by default, and emits a browser-canary plus generated-protocol-load scenario template. The dashboard Web 2.0 conversion dialog now includes a `Generate HTTP Draft Preview` action that uses captured browser replay evidence and shows generated action counts, repair candidates, and the scenario template before promotion.

Acceptance:

- Users can keep browser Web 2.0 canaries for UX evidence and generate HTTP scripts for density.
- Generated HTTP scripts go through the same validation, replay, diff review, and approval path.

## Workstream V2.8: Governance, API, And Reporting

| ID | Work Item | Priority | Status |
| --- | --- | --- | --- |
| V2.8.1 | Add import API support for TruClient 2.0 Web through existing LoadRunner import endpoint with explicit source metadata | P0 | Complete |
| V2.8.2 | Extend authoring capabilities with `vugen_web2` source/profile | P0 | Complete |
| V2.8.3 | Extend `/api/v1/system/parity-status` with a Web 2.0 migration area or evidence item | P1 | Complete |
| V2.8.4 | Add migration readiness report with imported steps, converted steps, skipped/review steps, selector confidence, replay status, runtime parity, and conversion-to-HTTP readiness | P1 | Complete |
| V2.8.5 | Update dashboard LoadRunner import review to show Web 2.0 profile-specific review cards and replay/repair actions | P1 | Complete |

Implementation note: V2.8 extends the existing LoadRunner import endpoint and summary persistence with `web2_migration_readiness`, including imported/converted/skipped step counts, selector confidence, replay status, runtime parity status, conversion-to-HTTP readiness, findings, and next actions. `/api/v1/scripts/authoring/capabilities` now exposes `vugen_web2` as a named browser-replay gated source/profile, `/api/v1/runtime-settings/support-matrix` lists the Web 2.0 conversion-readiness evidence, and `/api/v1/system/parity-status` includes a dedicated `vugen_web2_migration` area. Dashboard Web 2.0 editor workflows expose object-descriptor review, replay evidence, source-step timeline labels, and HTTP conversion preview from the outline dialogs.

Acceptance:

- Operators can see whether Web 2.0 support is ready, partial, or blocked.
- Import, replay, repair, and approval actions are auditable.

## Test Plan

- Unit tests for script-type detection and support matrix classification.
- Golden ZIP import fixtures for TruClient 2.0 Web and current Mobile Web to prevent regressions.
- Parser tests for metadata, steps, object descriptors, parameters, transactions, runtime settings, and unsupported fragments.
- API handler tests for successful import, unsupported archive review, RBAC allow/deny, review sections, repair suggestions, and authoring health.
- Browser replay smoke tests for imported P0 step families.
- Dashboard tests for Create Script profile selection, import review, object descriptor editor, replay dialog, runtime settings dialog, parameter data dialog, and conversion-to-HTTP review.
- Archive/report tests proving replay evidence and migration readiness sections are exported.
- Full verification before checkpoints: `go test ./...`, dashboard tests, `npm run build`, and browser smoke for touched dashboard pages.

## Milestones

| Milestone | Scope | Outcome |
| --- | --- | --- |
| V2-M1 | Support matrix, fixture contract, script-type detection | APEX Load can identify TruClient 2.0 Web archives and explain support. |
| V2-M2 | Parser/import P0 steps, metadata, review sections | Representative archives import into browser-profile drafts. |
| V2-M3 | Replay/debug evidence and runtime settings | Imported drafts can be replayed and diagnosed with migration evidence. |
| V2-M4 | Creation UX and authoring profile | Users can create new APEX Load Web 2.0-style scripts. |
| V2-M5 | Browser-to-HTTP conversion and scenario template | Users can pair browser canaries with generated protocol load. |
| V2-M6 | Parity gate, docs, dashboard polish | Web 2.0 parity is visible in status, docs, and operator workflows. |

## Definition Of Done

- TruClient 2.0 Web archives import through the LoadRunner acquisition path with explicit source/profile metadata.
- P0 Web 2.0 steps replay in APEX Load browser runtime.
- Object descriptor issues are scored and repairable.
- New Web 2.0 scripts can be created in APEX Load's authoring UX.
- Conversion to HTTP load scripts is available as an explicit review workflow.
- Runtime settings, replay evidence, and migration readiness are exportable.
- High-density browser load remains evidence-gated and separate from Web 2.0 scripting parity.
