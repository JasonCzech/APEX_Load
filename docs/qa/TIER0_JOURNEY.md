# Tier-0 Journey

The single end-to-end arc a real user walks, from an empty workspace to a downloaded run archive.
This is the specification the M1 Playwright suite implements — roughly twenty specs, each owning one
step below, plus a full-arc smoke spec that runs all of them in sequence.

**Why this exists.** The dashboard has ~24 routable views and six wizard/tab systems, covered today
by three Playwright specs. Rather than chase coverage view-by-view, M1 locks the one path that must
never break: if this journey is green, the product is usable; if any step is red, it is not,
regardless of what unit coverage says.

**Status:** S2, S7, S8, S9, S10, S11 and S12 implemented and green (`dashboard/e2e/tier0-*.spec.ts`),
plus a breadth guard covering all 16 top-level views. Run them with `scripts/run-e2e.sh`.
Remaining: S1, S3, S4, S5, S6.

**Observed behaviour worth recording**, since this document told the implementer to treat surprises
as findings rather than adjusting assertions to match:

- **Archive integrity reports `verified`** — every artifact recomputed to its recorded SHA-256. The
  verdict vocabulary is `verified` / `verified_partial` / `tampered` / `corrupt` / `unverifiable`,
  and the payload also carries a `mismatched` **count**. Assert on `status` and that count; a
  substring search for "mismatch" matches the field name and flags a healthy archive.
- **`PATCH /api/v1/tests/{id}/vusers` takes a single group**, `{group, target_vusers}` — not a
  `groups` array. An array decodes cleanly and is then rejected as a missing group name, so the
  mistake surfaces as a 400 that reads like a server fault.
- **`GET /api/v1/tests/{id}/report` serves rendered HTML, not JSON.** Parsing it as JSON fails with
  `Unexpected token '<'`, which reads like a server error rather than a wrong content type.
- **The views render a run's NAME, not its UUID.** `startLiveRun` names its run `${label}-run`.
  Asserting on `testId` in the results or report views finds nothing, and the resulting
  "element not found" looks like a broken page rather than a wrong selector.
- **Teardown must use the `seedLiveRun` fixture, never a body `try/finally`.** Playwright stops
  awaiting the body on timeout and begins disposing fixtures, so a `finally` runs against a closed
  request context; the repository measured it reaching the server zero times, leaving the run
  executing against the controller the next spec asserts on.

## Conventions

- **Route** is the hash route from `dashboard/src/appRouting.ts` (`parseAppViewFromHash`,
  `viewToLocationHash`). There is no React Router; routing is hand-rolled.
- **API** calls should be re-confirmed against `docs/api-endpoint-catalog.json`, which is generated
  from the routing layer by `cmd/swaggergen` and freshness-enforced by `pkg/api`. Treat this
  document as the narrative and that catalog as the contract.
- Every route carries optional workspace scope params: `projectId`, `applicationId`, `releaseId`.
- **Fixture:** extend `dashboard/e2e/support/liveRun.ts`, which already seeds a genuine controller
  run through the API so specs assert against real backend metrics rather than mocks.

---

## S1 · Authenticate and land

| | |
|---|---|
| Route | `#/dashboard` |
| Component | `DashboardPage.tsx` (+ `DashboardWorkspace.tsx`) |
| API | `/api/v1/auth/*` |

Assert the landing view renders, the connection indicator reaches connected, and the nav exposes the
pages the current role is entitled to. Auth mode for E2E should be explicit rather than inherited —
the controller refuses `dev`/`local` modes under `APEXLOAD_ENV=production`, so the harness must pin
a known mode and assert it, otherwise a misconfigured environment produces a green run that proves
nothing.

## S2 · Build the workspace hierarchy

| | |
|---|---|
| Route | `#/repository` |
| Component | `ApplicationRepositoryPage.tsx` |
| API | `/api/v1/domains`, `/applications`, `/projects`, `/releases` |

Create Domain → Application Repository → Project → Release. Assert each appears in its tab and that
the scope params propagate into subsequent routes.

**Known trap, assert explicitly:** any project-picking UI must go through the scoped
`setActiveProject`. A local-only cursor previously caused both a spurious "Select a project before
creating an application" error and silent creation against the wrong project. A spec that creates an
application and then asserts *which project it landed in* is the regression guard.

## S3 · Import a script

| | |
|---|---|
| Route | `#/scripts?mode=import` |
| Components | `ScriptsPage.tsx` → `ScriptCreateOptions.tsx` → per-format import component |
| API | `/api/v1/scripts/import/*` (49 routes), `.../validate`, `.../commit` |

Use one deterministic, dependency-free format for the Tier-0 arc — OpenAPI or Bruno — and leave the
other fifteen import types to M2's golden-file work. Assert the three-stage `POST` → `validate` →
`save`/`commit` flow completes and the script appears in the library.

Do **not** put LoadRunner archive import on the critical path: it is the largest and slowest
importer, and a Tier-0 failure there would be ambiguous between "the journey broke" and "one
converter regressed."

## S4 · Open the script in the editor

| | |
|---|---|
| Route | `#/scripts?mode=edit&scriptId=…` |
| Components | `ScriptsAuthoringShell.tsx`, `ScriptEditor.tsx` |
| Tabs | `action-list`, `source`, `runtime-settings`, `imported-metadata`, `parameter-data` (full list in `scriptEditorWorkbench.ts:30`) |

Assert the action list renders the imported actions, and that switching tabs preserves state. The
editor is Action-List-First by approved design; JSON is read-only/diff, so assert the action list —
not the JSON view — is the primary surface.

**Performance guard:** large imports previously froze the editor for ~2.5 minutes, and CodeMirror
still full-renders very large scripts. Give this step a generous but *bounded* timeout so a
regression surfaces as a failure rather than a hang.

## S5 · Build a scenario

| | |
|---|---|
| Route | `#/tests?view=builder` |
| Component | `TestHub.tsx` |
| Wizard | `Start` → `Shape/Source` → `Scripts` → `Run Settings` → `Data` → `Review` |
| API | `/api/v1/scenarios` |

Walk all six steps. Assert forward/back navigation preserves entered values, and that Review shows
what was actually entered. The unsaved-changes guard (`guardedNavigation.ts`,
`NavigationGuardProvider.tsx`) needs its own spec: navigate away mid-wizard and assert the modal
appears and that cancelling keeps the draft.

## S6 · Configure and launch a run

| | |
|---|---|
| Route | `#/new-test?scenarioId=…` |
| Components | `NewTestPage.tsx`, `CreateTestGroupsEditor.tsx`, `CreateTestRuntimeOverrides.tsx`, `CreateTestCapacityPreview.tsx` |
| API | `POST /api/v1/tests/preflight`, `POST /api/v1/tests/estimate`, `POST /api/v1/tests`, `POST /api/v1/tests/{id}/start` |

Set groups, VUsers, ramp, duration. Assert preflight and estimate return before launch is offered,
and that the created run reaches `RUNNING`.

**Assert generator assignment survives the round trip.** `pool_id` was previously stripped by the
dashboard's payload builders, silently destroying a fleet-pool binding on re-save. It is fixed
(`types/testConfiguration.ts:26`, `scenarioDraftUtils.ts:29`, `scenarioPayloadUtils.ts:218`) but has
no round-trip test. Create with a `pool_id` via the API, load in the dashboard, save, and assert it
is still there — this is the M7 regression guard, and it belongs here because this is where the
payload is built.

## S7 · Watch it run

| | |
|---|---|
| Routes | `#/command-center/{testId}`, `#/test/{testId}?tab=overview` |
| Components | `CommandCenterPrototype.tsx`, `TestExecution.tsx` |
| API | `GET /api/v1/tests/{id}/metrics`, `/stream` (SSE), `/api/v1/ws` (WebSocket) |

Assert live metrics arrive and advance — not merely that the panel renders. A frozen dashboard
showing stale numbers is the failure this step exists to catch, so assert a *second* sample differs
from the first.

**Expect flakiness here and design against it.** This is the step most exposed to timing, and the
codebase has no fake clock. Prefer polling with a deadline over fixed waits, and assert monotonic
progress rather than exact values.

## S8 · Control it mid-flight

| | |
|---|---|
| Route | `#/test/{testId}?tab=control` |
| API | `POST .../pause`, `.../resume`, `PATCH .../vusers`, `PATCH .../duration` |

Pause and assert metrics stop advancing; resume and assert they continue. Scale VUsers up and assert
the fleet reflects the new count. Extend duration and assert the projected end time moves.

Run controls sit behind a confirmation modal (`ConfirmRunActionModal.tsx`); assert the modal appears
and that dismissing it does **not** perform the action.

## S9 · Stop

| | |
|---|---|
| API | `POST .../stop`, and separately `.../abort` |

Assert a stopped run terminates cleanly and reaches a terminal status. Note that stop and abort have
different semantics — a prior review changed stop to yield `ABORTED` in some paths — so assert the
*specific* terminal status per control, and treat any disagreement as a finding rather than
adjusting the assertion to match observed behavior.

## S10 · Read the report

| | |
|---|---|
| Route | `#/report/{testId}?lens=executive\|engineer\|audit` |
| Components | `RunReportPage.tsx`, `ReportCharts.tsx` |
| API | `GET .../report`, `.../archive/analysis`, `.../archive/transactions` |

Assert all three lenses render and that headline numbers agree across them — the same run reported
three ways must not disagree with itself. The report is feature-flagged
(`normalizeReportViewForFeatureFlag`), so the harness must pin the flag rather than inherit it.

## S11 · Download and verify the archive

| | |
|---|---|
| Route | `#/test/{testId}?tab=archive` |
| API | `GET .../archive/manifest`, `.../archive/verify`, `.../archive/files/{name}` |

Assert the manifest lists the expected artifacts and that `verify` passes its SHA-256 integrity
check. This is the strongest single assertion in the journey: it proves the run produced complete,
intact evidence, which is the product's actual output.

## S12 · Find it again

| | |
|---|---|
| Route | `#/results` |
| Component | `ResultsPage.tsx` |

Assert the completed run appears in history, filters find it, and its report opens from there.

---

## Ordering and independence

The full-arc smoke spec runs S1→S12 in order against one run. The per-step specs must **not** depend
on each other: each seeds its own state through the API via the `liveRun.ts` fixture and asserts one
step. CI runs Playwright with `workers: 1` and `fullyParallel: false` today, but per-step
independence is what makes it safe to raise that later, and what keeps a failure in S5 from
cascading into eight misleading downstream failures.

## Definition of done for M1

- The twelve steps above have specs, and the full-arc smoke spec passes.
- The suite runs on every PR, not nightly.
- Any step that flakes twice is logged in `FLAKE_LOG.md` and either fixed or quarantined with a
  dated commitment — never silently retried.
