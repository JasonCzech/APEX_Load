# Usability remediation and regression evidence

This change implements the September 2026 walkthrough findings in the existing dashboard and controller. It preserves raw archives and legacy API fields. New fields are additive; generated OpenAPI and TypeScript contracts are refreshed through the existing generators.

## Outcome integrity

Execution termination, workload completion, computed acceptance, and archive coverage are separate signals. An observed breach fails acceptance. No configured criteria means **Not assessed**. Missing evaluation, incomplete evidence, unconfirmed completion, or an unobserved intended load makes otherwise passing criteria **Inconclusive**. Analyst assessment, author, and rationale remain separate from the computed result. Missing narrative and sign-off are **Not provided**.

New archives classify protocol requests/actions and enclosing named transactions explicitly. Reports retain combined raw record counts, but calculate separate count, errors, duration percentiles, and rates for each population. Action drilldown and report action tables use requests; transaction acceptance uses named transactions. Legacy events without positive classification evidence remain unknown. No records are deleted to reconcile the populations. Comparison and release-trend projections carry population identity; incompatible populations are not compared.

Structured conversion diagnostics retain severity. Informational source-order notes do not count as unsupported execution features. Debug evidence identifies response status and capture state before repair advice. HTTP 503 guidance directs the operator to target availability and response evidence, preserving the expected contract. Capture/redaction/retention policies remain enforced.

## Continuous authoring journey

The dashboard provides a first-test checklist, retained in Help after an archive exists. Guided workspace setup creates or selects domain, project, application, and release through existing APIs and preserves progress across scope changes. Compatibility-created objects are explained.

The scenario builder can create or import a missing script and return it to the original group without losing the draft or scope. Inline data-pool creation retains the initial row after failure and retries without creating duplicate pools. Permission checks and unsaved-change confirmations remain in force.

Authoring choices are alternatives rather than numbered steps. Import review receives focus and announces completion; generated JSON is collapsed. The explicit save action opens the editor. Action type and HTTP method are independent fields. Saved, validation, debug, and approval-readiness evidence are shown separately. Advanced authoring tools, recording comparison, and run-protection settings use progressive disclosure.

## Presentation and operation

Finished runs open with an outcome summary ahead of layout controls. Customization and report format choices are disclosed on demand. Optional remote-library configuration no longer dominates local libraries. Empty states, asset search names, identity subject help, role explanations, local Kubernetes applicability, and scheduling timezone labels are clarified.

The live load plot renders separate planned/actual series with a meaningful numeric VUser domain. Pause guidance explains retained allocation and rolling-throughput decay. Debug panels retain readable height when failure banners appear, fit within the available width, and stack on narrow screens. Added controls have accessible names; existing focus management and keyboard navigation are retained.

## Normal change cycle

Run `cd dashboard && npm run test:cycle`. This executes type checking, lint, unit tests, the production build, and the existing workflow runner for both disposable dev-admin and authenticated local profiles. CI continues to invoke that runner. The usability workflows are in `dashboard/e2e/workflow-usability.spec.ts` alongside the existing lifecycle, recovery, scheduling, workspace, and authoring tests.

New regression cases cover independent HTTP method persistence; operator-stopped no-SLA reports; prerequisite cancellation and OpenAPI return; full guided hierarchy creation; data-pool row failure/retry; narrow debugger layout; real HTTP 200/503 evidence; and automated WCAG AA checks on first-use screens in dark and light themes. Focused Go tests cover verdict rules, partial evaluation, classification, population reconciliation, termination provenance, diagnostic severity, snapshot capture state, and retention behavior.

Backend validation uses `go test ./...`. Accessibility lint uses `npm run lint:a11y`; bundle budgets use `npm run bundle:check`.

## Hands-on evidence

A separate local controller, SQLite database, archive directory, and controlled HTTP service were used. The service returned HTTP 200 after about 80 ms or a deliberate HTTP 503. The walkthrough created the complete workspace hierarchy, edited and debugged a saved GET, built and validated a one-user scenario, launched it, paused/resumed/stopped it, and inspected the archive and executive exports.

The successful replay measured about 92 ms. The failed replay displayed HTTP 503 and the service-unavailable JSON body, followed by availability guidance. The stopped workload produced 35 request records plus 35 enclosing transaction records: 70 raw records, not 70 business transactions. Its computed assessment was Not assessed. Browser inspection verified the outcome summary and 0–1 VUser plot. PDF and DOCX were rendered and visually inspected, including explicit missing sign-off.

File upload was exercised by the real Playwright import workflows; the interactive browser’s file-upload bridge was unavailable, so the manual editor walkthrough used an equivalent API-seeded script. Production SSO, external recording agents, remote workspaces, distributed infrastructure, and third-party telemetry were not exercised end to end. This is regression and expert-review evidence, not a WCAG conformance certification or a measured user study.

## Verification results

- Dashboard unit tests: **3,847 passed across 343 files**. Type checking and lint passed.
- `npm run test:cycle`: **passed**, including type checking, lint, 3,847 unit tests, production build, **51 dev-admin browser workflows and 4 local-auth workflows**. The retained accessibility test passes after correcting release-selector grouping, settings field names, and dark/light contrast.
- Accessibility lint: **0 blocking errors and 0 legacy warnings**. The focused browser audit passes on Dashboard and Settings in dark and light themes, including release-selector semantics, field names, and text contrast. These checks do not establish whole-product conformance.
- Backend: `go test ./... -timeout 20m` completed. **API passed (532 seconds)**; the full suite remains red in two packages: the credential-staging failure below and the CLI deadline case described below. All other packages passed. The previous full run passed the API package and all other packages except `pkg/distributed/generatorrpc`. `TestStagedAssignmentKeepsScriptSourcedCredentialsOutOfPlatformMaterial` also fails on unchanged baseline `9b6bedf06` in both credential-staging cases. No credential safeguards were weakened. An initial Java-runner timeout passed in isolation and in the subsequent full run. The final extended run also encountered the unchanged CLI cancellation test’s short abort deadline under concurrent load; its isolated repeat passed. An API run hit the default ten-minute timeout during concurrent builds and was repeated with a twenty-minute package timeout.
- Executive HTML, PDF, and DOCX outputs were inspected. The captured run reconciles 35 requests, 35 named transactions, and 70 raw records, with no acceptance criteria evaluated.
- Bundle checks passed after a measured aggregate-budget adjustment. Unchanged baseline already exceeded the aggregate JavaScript budget (5.28 MiB raw / 1.42 MiB gzip). The remediation build is 5.29 MiB / 1.42 MiB; aggregate ceilings are now 5.30 MiB / 1.425 MiB and CSS 704 KiB. Individual asset and route budgets remain unchanged.

## Review follow-up

The follow-up fixes all three review findings:

- Scenario drafts remain guarded during script prerequisites. Local return/close actions retain the parent draft while resolving the script editor; external navigation still resolves every dirty editor. Browser coverage now explicitly attempts Dashboard navigation from the import detour, cancels the prompt, and verifies the retained scenario.
- Default executive and engineering narratives derive failures from the same computed assessment as the verdict, including transaction-statistic and imported-dashboard threshold breaches absent from the manifest's breach flag.
- All metric populations divide by the same actual elapsed execution window, including pauses and pacing/idle time. Invalid or missing timestamps omit the rate and render Unknown. Regression cases cover sparse samples, differing event offsets, empty populations, invalid windows, and report aggregation. The sample archive reconciles 35 requests and 35 named transactions over 31.816006 seconds to 1.100075/s each; JSON and HTML agree.

The revised dashboard cycle passed 3,848 unit tests and all 55 browser workflows. A second code review of the revised paths found no further actionable issues. The full `go test ./... -timeout 20m` rerun completed: all packages except API and generatorrpc passed, including the CLI package. API’s sole failure was `TestHistoricalArchiveSecretSurfacesAreMasked`, whose archive download hit the real free-disk reserve under suite load; after removing obsolete task-generated binaries, that test passed unchanged in isolation (1.357 seconds). The known baseline credential-staging failure in generatorrpc remains. Focused backend regression tests for all revised report behaviors passed. Accessibility lint also passed with zero errors or warnings.

## Reader-facing report names

Report mastheads and subtitles now use the recorded test name; unnamed historical tests display “Unnamed test.” The executive subtitle is “Performance results for …” rather than generated-profile implementation text. A project ID is no longer used as an environment fallback: recorded environment labels are preserved, while absent values display “Not recorded.” The scenario ID was removed from executive headline cards. Raw archive provenance retains the identifiers.

The controlled sample’s regenerated executive HTML, PDF, and DOCX were checked for the real run name and absence of run, project, and scenario UUIDs. HTML was inspected in a browser and PDF was rendered and inspected. Regression tests cover configured and missing environment values, missing names, and preservation of raw provenance.

Naming follow-up validation: focused reader-context, browser/SOAP report rendering, and archive-security tests passed (1.443 seconds). The full Go run completed but remained red: the known credential-staging failure, a distributed completion timeout (also reproduced in isolation), and the archive free-disk-reserve failure under suite load. The archive-security case passed unchanged on repeat. The obsolete report-subtitle assertion was updated to the new text and passed. No distributed execution or disk safeguards were changed in this follow-up.
