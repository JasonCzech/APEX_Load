# APEX cross-application contracts, version 1

APEX Load produces `outcome` on `GET /api/v1/tests/{id}/archive/report`. Orchestration validates it, binds `execution_id` to the requested run, and persists it in the execution summary. Native analysis imports preserve the same object. See the JSON schema and representative fixture in this directory.

## Assessment

`passed`, `failed`, `inconclusive`, `not_assessed`, and `unavailable` describe computed acceptance, separately from execution termination and evidence coverage. Completion never implies acceptance. Analyst annotations do not override this object. `observed_at` is the time Load computed the report, not the test end time. Durations are seconds. Missing durations, coverage, and criteria revisions remain null; they must not be guessed. Scope IDs come from archived provenance.

Old Load versions can omit `outcome`; the existing nullable `passed` contract remains supported. Version 1 is authoritative over the legacy boolean. Unsupported or malformed provider outcomes fail collection; malformed stored outcomes suppress a legacy pass in public projection. Coordinate schema changes across both consumers before changing the producer's version.

## Evidence links

Supported report destinations are `#/report/{executionId}?section=summary|performance|diagnostics|evidence`. Load synchronizes these with its section tabs and browser history. Existing test/report routes continue to work. Orchestration only constructs these from validated HTTP(S) execution URLs; credentials, unrelated parameters, and invalid execution identifiers are rejected.

Return context stays in the outer URL query: `orchestrationReturn` and `orchestrationRun`. The existing return validator requires an exact execution match. Report pages expose the same return action as execution pages.

## Release mappings

Configure an APEX Load connection with an existing `project_id` and:

```json
{"release_mapping":{"application_id":"load-app-id","revision":"mapping-v1"}}
```

Use one connection for each mapped Load application. The native capabilities response includes the mapping and active release choices, validated against the exact provider application/project. The run wizard exposes this catalog when an enabled native connection is selected. The chosen `load_test.release_id` and `load_test.mapping_revision` survive review and are checkpointed before provisioning.

Provisioning checks the selected revision, exact application/project/release ownership, and archived status before uploading scripts or creating a test. It writes application/release IDs into the provider test request and stores all three IDs plus the mapping revision in the execution handle. Retry adoption checks that the existing test matches the selected application/release. A changed mapping requires an explicit review and a new run identity; historical handles are never recomputed from current settings. Run-specific options cannot override the connection mapping.

Native imports use the archived run's scope rather than assigning the current connection mapping to historical executions. An imported run has no Orchestration launch mapping revision unless one was recorded at launch; the UI says “Not recorded.”

## Rollout and validation

Deploy Load's additive report contract first, then Orchestration's consumer. Existing connections without mappings continue using the old launch behavior. Add a mapping and increment its revision whenever its meaning changes. Validate with an enabled, project-scoped connection before production use; local fixtures do not prove a deployed end-to-end handoff.

Validation results and remaining checks are in [Validation](#validation); the follow-up review is in [Code review](#code-review).

## Validation

Both repositories contained existing changes, and a parallel task continued changing APEX Load during validation. No deployed native connection was available for a real cross-app launch/import/return round trip.

### Orchestration

- Production build and TypeScript checks pass.
- Dashboard lint: no errors; 22 existing Fast Refresh warnings.
- Python lint on changed implementation and test files passes.
- Shared event/assessment contract package: 46 tests pass, including the shared JSON fixture and unsupported-version/non-finite-duration rejection.
- Full backend unit run: 5,718 passed, 12 skipped, one projection assertion failed. Removed the unnecessary per-phase engine-handle projection; the subsequent 300-test run covering facade projection, structured outcomes, and the Load adapter passes.
- Full dashboard run: 888 passed, one asynchronous launch-navigation assertion failed. Changed it to await the rendered destination; the subsequent 28-test run covering launch navigation, release selection, outcome display helpers, and execution links passes.
- Browser inspection of the release-connection controls in the offline demo confirmed the layout and saved configuration. Screenshot: `output/playwright/cross-app-contracts/release-connection.png` in the Orchestration repository. Populated release selection and revision persistence are covered by the component test.

### APEX Load

- TypeScript, lint, and production build pass.
- Two-worker unit run: 4,000 passed, one new section-navigation fixture failed because no test was loaded. Corrected the fixture; all 48 report/navigation/outcome tests pass on recheck.
- Dev-admin browser profile: 76 passed, two Settings failures remain: returning to an unsaved Grafana settings form, and finding the Settings search control. The report and run-result journeys pass.
- Local-auth browser profile: all 9 pass on isolated port 18092. The first default-port attempt stopped during setup.
- Full Go validation was attempted twice. Generated Swagger and QA artifacts were refreshed. The later API suite hit its 10-minute timeout during substantial concurrent machine load, and a distributed-execution timing assertion failed. The outcome contract, affected execution tests, and Swagger freshness pass together with `GOMAXPROCS=2`; QA freshness passes separately.

### Remaining verification

The full Load suite cannot be described as clean. Resolve/recheck the two Settings workflows and repeat its full Go suite in a quiet environment. Configure an enabled, project-scoped Load connection, deploy Load's additive producer before Orchestration's consumer, and exercise a real mapped launch plus native import and return navigation before production rollout.

### Subsequent code review

The follow-up [code review](#code-review) fixed five findings. Its full Load dashboard cycle passed, including both previously failing Settings workflows. The deployed cross-app handoff and earlier full Go-suite limitation remain unverified.

## Code review

Reviewed the outcome producer/consumer, release selection and retry binding, and report/return navigation in Orchestration and the companion Load checkout. Existing unrelated changes were preserved.

### Findings fixed

- **P1 — Contradictory assessment at the execution boundary.** A provider/checkpoint summary could retain `passed: true` alongside a failed or unassessed structured outcome. `TestResultSummary` now derives the legacy boolean from the structured status during validation, including checkpoint restoration. Regression tests cover all five states.
- **P1 — Retry could drop release identity after mapping removal.** An unmapped connection could adopt a previously scoped execution and return an unscoped handle. Provisioning now rejects orphan mapping revisions before remote calls and rejects adoption of a scoped execution without its mapping.
- **P2 — Saved connection misrepresented as project default.** An unavailable connection remained in the launch payload while the picker displayed the default. The picker now shows the unavailable identity and requires an explicit replacement. Cached releases cannot be used for an ineligible connection; failed/loading capability data disables release selection.
- **P2 — Namespaced execution links rejected.** The adapter permits colon-containing execution IDs, but the browser URL validator rejected their unescaped form. Links now accept both raw and percent-encoded colons while preserving the exact return binding.
- **P2 — Canceled report navigation changed the visible section.** Load selected a report section before its unsaved-assessment guard committed navigation. The visible section now follows the committed route. A regression test verifies that an uncommitted request leaves the current section visible.

### Review validation

- Orchestration: 390 affected backend tests and 101 dashboard tests passed.
- Orchestration dashboard typecheck, focused ESLint, backend Ruff, and whitespace checks passed.
- Load: the full `npm run test:cycle` passed: typecheck, lint, 4,002 unit tests, production build, 78 dev-admin browser workflows, and 9 local-auth browser workflows. The two Settings workflow failures recorded in the earlier validation did not recur.

This review does not establish a deployed cross-application handoff. The earlier broad Go-suite timeout remains a separate validation limitation; this review made no Go changes.
