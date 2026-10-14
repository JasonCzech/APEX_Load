# E2E suites

Single reference for the repository's browser end-to-end suites. Live status is in [STATUS.md](STATUS.md); the program scope is in [TEST_PROGRAM.md](TEST_PROGRAM.md).

| Suite | Specs | Runs in | Entry point |
|---|---|---|---|
| [1. Tier-0 journey (M1)](#1-tier-0-journey-m1) | `dashboard/e2e/tier0-*.spec.ts` | `npm run test:cycle` (both profiles) | `scripts/run-e2e.sh` |
| [2. Workflow regression suite](#2-workflow-regression-suite) | `dashboard/e2e/workflow-*.spec.ts` plus the Tier-0 specs | `npm run test:cycle`, CI dashboard job | `scripts/run-dashboard-workflows.sh` |
| [3. Recording and replay suite](#3-recording-and-replay-suite) | `dashboard/e2e-recording/*.spec.ts` (`authoring`, `browser`, `controls`, `conversion`, `options`, `proxy`, `recovery`) | opt-in; `recording-e2e.yml` | `scripts/run-recording-e2e.sh` |

## 1. Tier-0 journey (M1)

### Journey specification

The single end-to-end arc a real user walks, from an empty workspace to a downloaded run archive.
This is the specification the M1 Playwright suite implements. Completion is measured by the
assertion inventory, not by a target number of spec files; independently seeded step coverage is
supplemented by a full-arc smoke spec.

**Why this exists.** Rather than chase coverage view-by-view, M1 locks the one path that must never
break: if this journey is green, the product is usable; if any step is red, it is not, regardless
of what unit coverage says. Generated inventories track changing route and spec breadth separately.

**Status:** [Tier-0 assertion matrix](#tier-0-assertion-matrix) is the completion inventory.
S1–S12 retain their independent step coverage. `workflow-local-auth.spec.ts` now
adds U01, a continuous real-login → UI workspace → import/edit → scenario → run →
stop → Results/report → download journey, without API-seeded business objects
between stages. Lifecycle variations and report lenses remain separate tests.
The breadth guard covers all 16 top-level views. Run the normal change cycle with
`cd dashboard && npm run test:cycle`; see [workflow coverage](#2-workflow-regression-suite). A step is not green merely because one of its API assertions exists.

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
- **Graceful stop must finish as `COMPLETE`; force abort must finish as `ABORTED`.** The local
  backend previously collapsed every durable `STOPPING` run to `ABORTED`, even when the engine had
  recorded an explicit graceful-stop boundary. The controller now carries the engine's stop mode
  into terminalization, and both UI paths have exact assertions.
- **The report UI is feature-gated off by default.** Report journey specs set
  `apexload.featureFlags.runReport=true` before application initialization; otherwise report hashes
  deliberately normalize to the legacy archive workspace and a lens assertion tests the wrong UI.

#### Conventions

- **Route** is the hash route from `dashboard/src/appRouting.ts` (`parseAppViewFromHash`,
  `viewToLocationHash`). There is no React Router; routing is hand-rolled.
- **API** calls should be re-confirmed against `docs/api-endpoint-catalog.json`, which is generated
  from the routing layer by `cmd/swaggergen` and freshness-enforced by `pkg/api`. Treat this
  document as the narrative and that catalog as the contract.
- Every route carries optional workspace scope params: `projectId`, `applicationId`, `releaseId`.
- **Fixture:** extend `dashboard/e2e/support/liveRun.ts`, which already seeds a genuine controller
  run through the API so specs assert against real backend metrics rather than mocks.

---

#### S1 · Authenticate and land

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

#### S2 · Build the workspace hierarchy

| | |
|---|---|
| Routes | `#/settings`, `#/repository` |
| Components | `SettingsPage.tsx`, `ApplicationRepositoryPage.tsx` |
| API | `/api/v1/domains`, `/api/v1/projects`, `/api/v1/applications`, `/api/v1/projects/{id}/releases` |

Create a Domain (which provisions its Application Repository), then a Project, Application, and
Release. Assert each appears on its management surface and that the project, application, and
release scope params propagate into subsequent routes.

**Known trap, assert explicitly:** any project-picking UI must go through the scoped
`setActiveProject`. A local-only cursor previously caused both a spurious "Select a project before
creating an application" error and silent creation against the wrong project. A spec that creates an
application and then asserts *which project it landed in* is the regression guard.

#### S3 · Import a script

| | |
|---|---|
| Route | `#/scripts?mode=create` → **Import From Source** |
| Components | `ScriptsPage.tsx` → `ScriptCreateOptions.tsx` → per-format import component |
| API | `POST /api/v1/scripts/import/openapi` (dry-run validation and conversion inspection), `POST /api/v1/scripts/import/openapi/commit` |

Use one deterministic, dependency-free format for the Tier-0 arc — OpenAPI or Bruno — and leave the
other fifteen import types to M2's golden-file work. OpenAPI does not expose a separate validation
route: **Validate Only** and **Convert OpenAPI** are distinct UI stages backed by the protected
inspection endpoint. Assert that validation reports a ready preflight while persisting nothing,
conversion produces the expected action and source mapping, the explicit commit succeeds, and the
script appears in the project library.

Do **not** put LoadRunner archive import on the critical path: it is the largest and slowest
importer, and a Tier-0 failure there would be ambiguous between "the journey broke" and "one
converter regressed."

#### S4 · Open the script in the editor

| | |
|---|---|
| Route | `#/scripts?mode=edit&scriptId=…` |
| Components | `ScriptsAuthoringShell.tsx`, `ScriptEditor.tsx` |
| Tabs | `action-list`, `source`, `runtime-settings`, `imported-metadata`, `parameter-data` (full list: `CenterWorkbenchTabId` in `dashboard/src/components/scriptEditorWorkbench.ts`) |

Assert the action list renders the imported actions, and that switching tabs preserves state. The
editor is Action-List-First by approved design; JSON is read-only/diff, so assert the action list —
not the JSON view — is the primary surface.

**Performance guard:** large imports previously froze the editor for ~2.5 minutes, and CodeMirror
still full-renders very large scripts. Give this step a generous but *bounded* timeout so a
regression surfaces as a failure rather than a hang.

The Tier-0 guard uses 400 deterministic actions, requires the editor to become usable within the
30-second test boundary, verifies that fewer outline rows are mounted than the 400 source actions,
then filters to the last action to prove the windowed outline still addresses the complete script.

#### S5 · Build a scenario

| | |
|---|---|
| Route | `#/tests?view=builder` |
| Component | `TestHub.tsx` |
| Wizard | `Start` (new scenario from scripts, or copy an existing scenario) → `Model & Shape` → `Run Settings` → `Data` → `Review` |
| API | `/api/v1/scenarios` |

Walk all five steps. Assert forward/back navigation preserves entered values, and that Review shows
what was actually entered. The unsaved-changes guard (`guardedNavigation.ts`,
`NavigationGuardProvider.tsx`) has its own spec: it navigates away mid-wizard, requires the modal,
and proves that cancelling keeps the draft.

The implemented guard uses the user-facing **Keep editing** action for cancellation. Review exposes
duration and mode plus a per-group table (script, role, VUsers, ramp, and initialization), so parity
is asserted on the actual configuration rather than on a group count alone.

#### S6 · Configure and launch a run

| | |
|---|---|
| Route | `#/new-test?scenarioId=…` |
| Components | `NewTestPage.tsx`, `CreateTestGroupsEditor.tsx`, `CreateTestRuntimeOverrides.tsx`, `CreateTestCapacityPreview.tsx` |
| API | `POST /api/v1/tests/preflight`, `POST /api/v1/tests/estimate`, `POST /api/v1/tests`, `POST /api/v1/tests/{id}/start` |

Set groups, VUsers, ramp, duration. Assert preflight and estimate return before launch is offered,
and that the created run reaches `RUNNING`.

**Assert generator assignment survives the round trip.** `pool_id` was previously stripped by the
dashboard's payload builders, silently destroying a fleet-pool binding on re-save. It is fixed
(`dashboard/src/types/testConfiguration.ts`, `dashboard/src/components/scenarioDraftUtils.ts`, `dashboard/src/components/scenarioPayloadUtils.ts`) but has
no round-trip test. Create with a `pool_id` via the API, load in the dashboard, save, and assert it
is still there — this is the M7 regression guard, and it belongs here because this is where the
payload is built.

#### S7 · Watch it run

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

#### S8 · Control it mid-flight

| | |
|---|---|
| Route | `#/test/{testId}?tab=control` |
| API | `POST .../pause`, `.../resume`, `PATCH .../vusers`, `PATCH .../duration` |

Pause and assert metrics stop advancing; resume and assert they continue. Scale VUsers up and assert
the fleet reflects the new count. Extend duration and assert the projected end time moves.

Run controls sit behind a confirmation modal (`ConfirmRunActionModal.tsx`); assert the modal appears
and that dismissing it does **not** perform the action.

#### S9 · Stop

| | |
|---|---|
| API | `POST .../stop`, and separately `.../abort` |

Assert a stopped run terminates cleanly and reaches a terminal status. Note that stop and abort have
different semantics — a prior review changed stop to yield `ABORTED` in some paths — so assert the
*specific* terminal status per control, and treat any disagreement as a finding rather than
adjusting the assertion to match observed behavior.

#### S10 · Read the report

| | |
|---|---|
| Route | `#/report/{testId}?lens=executive\|engineer\|audit` |
| Components | `RunReportPage.tsx`, `ReportCharts.tsx` |
| API | `GET .../report`, `.../archive/analysis`, `.../archive/transactions` |

Assert all three lenses render and that headline numbers agree across them — the same run reported
three ways must not disagree with itself. The report is feature-flagged
(`normalizeReportViewForFeatureFlag`), so the harness must pin the flag rather than inherit it.

#### S11 · Download and verify the archive

| | |
|---|---|
| Route | `#/test/{testId}?tab=archive` |
| API | `GET .../archive/manifest`, `.../archive/verify`, `.../archive/files/{name}` |

Assert the manifest lists the expected artifacts and that `verify` passes its SHA-256 integrity
check. This is the strongest single assertion in the journey: it proves the run produced complete,
intact evidence, which is the product's actual output.

#### S12 · Find it again

| | |
|---|---|
| Route | `#/results` |
| Component | `ResultsPage.tsx` |

Assert the completed run appears in history, filters find it, and its report opens from there.

---

#### Ordering and independence

The full-arc smoke spec runs S1→S12 in order against one run. The per-step specs must **not** depend
on each other: each seeds its own state through the API via the `liveRun.ts` fixture and asserts one
step. CI runs Playwright with `workers: 1` and `fullyParallel: false` today, but per-step
independence is what makes it safe to raise that later, and what keeps a failure in S5 from
cascading into eight misleading downstream failures.

The journey uses two explicit profiles. `dev-admin` is the fast functional profile for S2–S12.
`local-auth` configures the fresh store for Local Test Login, restarts the controller, and drives the
seeded `admin/admin` login for S1. A dev-bypass status check remains useful harness validation, but
does not satisfy the sign-in assertion.

#### Definition of done for M1

- The twelve steps above have specs, and the full-arc smoke spec passes.
- The suite runs on every PR, not nightly.
- Any step that flakes twice is logged in [`FLAKE_LOG.md`](FLAKE_LOG.md) and either fixed or quarantined with a
  dated commitment — never silently retried.

### Tier-0 assertion matrix

This is the completion inventory for M1. `GREEN` means every assertion in the row is implemented
and passing on the current branch; `PARTIAL` means some executable coverage exists; `MISSING` means
the required behavior has no journey assertion. File counts are deliberately not used as a proxy.

| Step | Required assertions | State | Current evidence / missing work |
|---|---|---|---|
| S1 | Real sign-in, dashboard landing, connected stream, entitled navigation | GREEN | `tier0-local-auth.spec.ts` proves invalid-login rejection and a real seeded `admin/admin` login; `tier0-auth.spec.ts` separately pins dev-admin harness posture. |
| S2 | UI creates Domain (and its provisioned repository) → Project → Application → Release; selected project owns the application; scope propagates | GREEN | `tier0-workspace.spec.ts` drives every create through the UI, targets an owner while a decoy project is active, verifies persisted application ownership, and carries project/application/release scope into the repository tabs. |
| S3 | Deterministic OpenAPI import, validate, commit, library visibility | GREEN | `tier0-script-import.spec.ts` uploads the dependency-free `tier0-orders.openapi.yaml` fixture, proves dry-run preflight and generated action semantics, verifies inspection does not persist, commits through the protected endpoint, and finds the saved script in the scoped library. |
| S4 | Action-list-first editor, tab preservation, save/reopen, bounded large-script render | GREEN | `tier0-script-editor.spec.ts` independently seeds the S3-shaped OpenAPI script, proves Action List is primary, preserves a runtime edit across Source/Runtime/Action List switches, saves and reopens it from the library, and requires a 400-action script to expose a usable virtualized outline within a bounded test. |
| S5 | Five wizard steps, back/forward preservation, review parity, guarded navigation cancellation | GREEN | `tier0-scenario-builder.spec.ts` walks Start → Model & Shape → Run Settings → Data → Review, verifies the name, selected script, duration, unit, and VUsers after backward/forward traversal, reconciles Review and the stored scenario, and proves Keep editing cancels dirty navigation without losing the draft. |
| S6 | Preflight, estimate, launch to RUNNING, `pool_id` survives save/reload | GREEN | `tier0-run-launch.spec.ts` requires successful estimate and preflight before Start now, then observes the created run at `RUNNING`; payload-unit and API materialization tests preserve and resolve `pool_id`. |
| S7 | Two advancing metric samples and rendered live metrics | GREEN | `tier0-live-metrics.spec.ts`. |
| S8 | Confirmation dismiss is inert; UI pause/resume; duration and VUser changes become visible | GREEN | `tier0-run-lifecycle.spec.ts` cancels and confirms the modal paths, observes `PAUSED`/`RUNNING`, scales the visible group to three VUsers, and applies a duration extension. |
| S9 | Stop and abort separately reach their exact terminal semantics | GREEN | `tier0-run-lifecycle.spec.ts` proves graceful Stop reaches `COMPLETE` and force Abort reaches `ABORTED`; the controller regression test preserves that distinction. |
| S10 | Three lenses identify the run and reconcile shared headline values | GREEN | `tier0-report-results.spec.ts` pins the report feature and requires identical KPI scorecard text in executive, engineer, and audit lenses. |
| S11 | Manifest, checksum verification, and download of a listed artifact | GREEN | `tier0-run-lifecycle.spec.ts` verifies SHA-256 integrity, reads the manifest-listed transactions filename, downloads it in Chromium, and requires a non-empty file. |
| S12 | Results filter locates run and opens its report | GREEN | `tier0-report-results.spec.ts` filters by the completed run name and follows the explicit Open Report action to that run's report route. |

The continuous U01 smoke is implemented in `workflow-local-auth.spec.ts` and has
been exercised under real local authentication: one UI-created workspace, imported
and edited script, scenario, run, completion, Results/report and downloaded archive.
It supplements the independent step assertions above; pause/scale/abort variations
and the three report lenses remain separate specs. See the
[normal test cycle and exact coverage](#2-workflow-regression-suite) for commands,
remaining extended cases and execution evidence.

#### API contract coverage

The generated [API contract matrix](evidence/M1/api-contract-matrix.json) is the route-level
completion inventory. It derives its totals from the endpoint catalog, checks catalog-to-router
public/authenticated parity, proves anonymous rejection through the generic middleware, and runs
allowed and denied RBAC cases from structured access requirements. Its own `GREEN`, `PARTIAL`, and
`MISSING` totals are authoritative. Legacy resource-scoped recorder/replay routes share role
profiles with their runtime middleware, while metrics, recording-bridge, and collector-token routes
run their handler-specific anonymous/authenticated cases. Route-contract coverage is fully green;
that does not change the S1–S12 journey states above.

## 2. Workflow regression suite

The browser library now includes a continuous authenticated first-run journey and focused recovery tests based on [U01–U24](../archive/qa-reviews/AUDITS_2026-09.md#dashboard-ux-reviews-2026-09-06-to-2026-09-20). Tests exercise the embedded production dashboard against a real disposable controller and SQLite store. The [Figma review board](https://www.figma.com/design/9tjL68AABdh0cOtSDvexeT) records the original frontend evaluation.

### Normal code-change cycle

From `dashboard/`, run:

```sh
npm run test:cycle
```

This runs TypeScript checks, strict lint, all Vitest tests, a fresh production build, and both browser profiles. CI runs these checks in its existing dashboard job and invokes the same `scripts/run-dashboard-workflows.sh` entry point. Both browser profiles execute even if the first fails; either failure makes the command fail. There are no automatic test retries.

Prerequisites: the repository's Go toolchain, Node version from `dashboard/package.json`, installed dashboard dependencies, and Chromium (`dashboard/node_modules/.bin/playwright install chromium`). The controller uses ports 18081 and 18082 by default; override with `APEXLOAD_E2E_PORT` and `APEXLOAD_E2E_AUTH_PORT`. A port that another process already listens on (a local controller left running, another run) is refused before the build, and once the controller answers the runner checks that the process it started is the one listening, so a run never tests a server it did not start.

For browser-only reruns after building, run `npm run test:workflows`. For focused diagnosis from the repository root:

```sh
scripts/run-e2e.sh workflow-authoring.spec.ts
APEXLOAD_E2E_PROFILE=local-auth scripts/run-e2e.sh workflow-local-auth.spec.ts
```

`npm test` remains the fast unit-test command. Use `test:cycle` for completed frontend or workflow changes, as required by `AGENTS.md`.

### Executable coverage

IDs in test titles link failures to the audit. A mapped ID means the assertions listed here are implemented, not that every proposed variant in the audit is automated.

| Audit IDs | Browser library | User-visible assertions |
|---|---|---|
| U01 | `workflow-local-auth.spec.ts` | Real sign-in; UI-created domain/project/application/release; OpenAPI validation, conversion and commit; script edit/save; scenario creation; named run launch; advancing controlled-target requests; graceful completion; Results → report → archive; nonempty download with matching server bytes. Business objects are not seeded between steps. |
| U02 | `tier0-local-auth.spec.ts`, `workflow-local-auth.spec.ts` | Authentication and incorrect-password handling; viewer launch denial; sign-out and browser Back remain outside the protected workspace. |
| U03, U08 | `workflow-authoring.spec.ts` | Empty prerequisites, invalid scenario rejection, no invalid persistence, script-picker focus, and draft retention through cancellation. |
| U04 | `workflow-authoring.spec.ts`, `tier0-workspace.spec.ts` | Similarly named scripts in separate scopes retain distinct identities through reload/history; workspace UI persists the selected owner and scope. |
| U05, U06 | `tier0-script-import.spec.ts`, `workflow-authoring.spec.ts` | Dry-run and conversion do not persist; explicit commit and library reopen; malformed input can be replaced and revalidated. |
| U07, U18 | `workflow-authoring.spec.ts` | Both authentication profiles exercise keep, save and discard branches. Injected 409/401 responses preserve the draft and stored version before an explicit retry. An uncertain 500 blocks blind retry; a real intervening conditioned write, explicit Load/Diff/Reapply and a fresh conditional save preserve both authors' changes. The injected responses do not simulate session expiration or a committed lost receipt; the separate revision workflow covers a genuinely committed lost receipt. |
| U09 | `tier0-scenario-builder.spec.ts` | Wizard back/forward preserves script, duration, units and VUsers; Review agrees with the stored scenario. |
| U10, U11 | `tier0-run-launch.spec.ts`, `workflow-execution.spec.ts` | Successful estimate/preflight; failed preflight explains the problem and blocks Start; recovery and rapid repeated activation create one run; scenario defaults remain unchanged. |
| U12 | `tier0-live-metrics.spec.ts`, `workflow-execution.spec.ts` | Advancing metrics; actual WebSocket interruption is visible; reconnect keeps the same run and continued target traffic. |
| U13, U14 | `tier0-run-lifecycle.spec.ts` | Confirmation cancellation; pause/resume, scale and extend; exact COMPLETE versus ABORTED terminal states. |
| U15 | `workflow-execution.spec.ts` | Held run remains RESERVED without traffic across reload, then starts explicitly; scheduled run retains its future timestamp without immediate execution. |
| U16, U17 | `tier0-report-results.spec.ts`, `tier0-run-lifecycle.spec.ts`, `workflow-execution.spec.ts` | Report lenses, Results filtering, archive integrity/download; injected archive-read failure is explicit and recovery restores the same run's evidence. |
| U19 | `workflow-recovery.spec.ts` | Monitoring readiness failure is visible; restoring real responses recovers metadata-store readiness and WebSocket status. |
| U20 | `workflow-recovery.spec.ts` | Keyboard navigation, modal focus trapping/restoration, draft retention and reachable wizard controls at 1440×900, 932×1096 and 1280×720. |
| U21 | `tier0-script-editor.spec.ts` | Bounded rendering of a 400-action editor and edit preservation across tabs/save/reopen. |
| U22 | `tier0-routes.spec.ts`, `workflow-recovery.spec.ts` | Resolved route content and page-error checks across 16 views; creation-capability failure exposes an actionable retry. |
| U23 | `tier0-bruno-import.spec.ts` | Governed Bruno import inspection, acknowledgement and protected commit behavior. |
| U24 | `workflow-recovery.spec.ts` | Recorder startup failure preserves its target and leaves the retry control usable. |
| Response content fidelity | `workflow-response-content.spec.ts` | Standard HAR base64 stores decoded JSON despite gzip wire headers, preserving correlation extraction through save/reopen; real gzip replay supplies a fresh extracted value; incomplete recorder response content remains a source-located execution blocker after saving and reopening; actual gzip SSE collection extracts sampled events while explicitly qualifying the response as partial, without origin EOF. |
| Mixed HTTP version replay | `workflow-http-version.spec.ts` | Per-request Inherit/Auto/exact policies preserve nested source through save/reopen; real TLS siblings use the requested wire versions on a shared cookie session and join before consumption; automatic negotiation overrides an exact script default on a real HTTP/1.1-only target. |
| Concurrent HTTP authoring | `workflow-http-concurrent.spec.ts` | Explicit mode/fanout preserve every child and unknown field through save/reopen; real siblings overlap on the inherited parent session; source-order parameter allocation and extraction follow the join; debugger stepping pauses only at the group boundary. |
| LoadRunner concurrent import | `workflow-http-concurrent.spec.ts` | A synthetic source archive preserves exact child source mappings through protected import/save/reopen and executes the concurrent group against a loopback session target. |
| Unavailable runtime evidence | `workflow-http-concurrent.spec.ts` | Synthetic transport DTOs verify sticky omission across watches, event details, run history and JSON downloads while retaining execution outcomes, ordinals and evidence-retention counts. |

### Fixtures and evidence

The dev-admin profile covers focused workflows. The local-auth profile uses real seeded test personas and runs the complete journey. Each profile gets an independent temporary database/archive directory. Controlled HTTP traffic stays on loopback with small VUser counts. Newly created runs are registered for fixture teardown; unfinished runs are aborted and awaited before shutting down their target. The harness terminates its controller and removes its store even on failure.

Only explicit fault tests intercept API responses. Normal import, persistence, launch, execution, reports and downloads use the real backend. WebSocket recovery closes the actual browser connection rather than substituting fake metrics. The full-arc/report specs explicitly enable the report feature; archive tests also exercise the default legacy path.

Failure screenshots and traces are retained separately under `dashboard/test-results/dev-admin/` and `dashboard/test-results/local-auth/`. CI also writes per-profile JUnit XML and uploads browser evidence for 14 days. Record the revision and command with any reported result; ID mappings and old GREEN labels are not execution evidence.

### Remaining extended cases

The original audit is broader than this deterministic local regression baseline. The following are still explicit extensions, not passing claims: expired import-inspection sessions; real concurrent-editor/session-expiry behavior; multi-group data-pool/placement round trips on an external generator fleet; schedule editing and DST boundaries; dependency-agent configuration; true browser zoom, contrast and screen-reader review; large catalog filtering; the complete governed-runtime matrix; and connected native recording-agent record/stop/redaction/save workflows. Those need their relevant fixtures or environments before being added as required integration lanes. The empty picker still lacks the proposed inline create/import return path; current tests preserve and check its existing recovery behavior.


### Verification and known CI blocker

Verification is against the working tree based on `87ce7626d`, on 2026-09-06, using Chromium on macOS. The expanded library adds 22 browser tests. The normal cycle runs 41 dev-admin tests and 4 local-auth tests, including U01. The complete `npm run test:cycle` command exited 0: **41/41 dev-admin browser tests, 4/4 local-auth browser tests, and 3,842/3,842 unit tests in 341 files passed**, along with type checking, lint and the production build. Separate accessibility lint, documentation checks, workflow syntax (`actionlint`) and diff whitespace checks also passed.

The separate CI `bundle:check` gate still fails its existing aggregate JavaScript budgets: 5.28 MiB raw versus 5.22 MiB allowed, and 1.42 MiB gzip versus 1.41 MiB allowed. An isolated `git archive HEAD` baseline build with the same installed dependencies reproduces both violations; the initial-shell, script-library and execution-route budgets pass. These thresholds were not raised. CI checks the bundle budget after browser execution so this pre-existing failure does not prevent collecting workflow evidence. The overall CI job still fails until bundle size is addressed.

## 3. Recording and replay suite

This opt-in suite starts disposable controllers and a real recording agent, drives
recorder-owned Chromium and an HTTP proxy, and checks the captured DSL through the
dashboard. The local HTTP/HTTPS fixture keeps an independent request ledger and
rejects stale or reused CSRF tokens. No external application is required.

### Setup and commands

Use the Node version in `.nvmrc`, Go from `go.mod`, OpenSSL, and Python 3. Install
both browser runtimes; their revisions are pinned independently by this repository:

```sh
cd dashboard
npm ci --ignore-scripts
npx playwright install chromium
cd ..
go run github.com/mxschmitt/playwright-go/cmd/playwright@v0.6100.0 install chromium
```

On Linux install Xvfb and Playwright's system dependencies. The entry point wraps
itself in `xvfb-run -a` when no display is configured. macOS requires a logged-in
desktop session; recording windows will appear. Recording remains headed, while the
dashboard driver is headless. Missing prerequisites and failed CDP attachments fail.

```sh
# Complete suite, both dev-admin and local-auth profiles
scripts/run-recording-e2e.sh

# One profile and one independently runnable scenario
APEXLOAD_E2E_PROFILE=local-auth scripts/run-recording-e2e.sh browser.spec.ts --grep 'agent:'
APEXLOAD_E2E_PROFILE=dev-admin scripts/run-recording-e2e.sh proxy.spec.ts
scripts/run-recording-e2e.sh authoring.spec.ts --grep 'every loop mode'
```

The command builds the embedded dashboard and test-tagged controller/agent before
running. It does not reuse your controller, database, browser profile or proxy
settings. Each test owns its workspace, SQLite database, archive and CA directories,
target servers and ephemeral loopback ports. Tests run serially with zero retries. The entry point accepts spec-file and grep
filters; it rejects options that override retry, discovery or evidence policy.
An empty selection, skipped tests, failures and flaky results fail the command.
The complete run must report **42 passed**: 21 scenarios under each profile.
Ordinary `npm run test:cycle` does not discover these specs. The aggregate opt-in
runner includes this suite with `--all`.

### Evidence and troubleshooting

Evidence is retained under `output/playwright/recording/<timestamp>/`; override with
`APEXLOAD_RECORDING_E2E_OUTPUT=/absolute/path`. Inspect `report.json`, per-case
controller/agent logs, `fixture-events.json`, `fixture-summary.json`, `cleanup.json`, conversion/replay attachments and
Playwright traces. The output path is printed even after a failed run. Disposable
working directories and process groups are cleaned after success, failure or a
signal. No test database or private CA is installed in the workspace.

A failure is actionable: do not rerun until green as a substitute for diagnosis.
Inspect the first failing assertion, its controller log, and the fixture ledger.
For a missing browser, install the Go and Node runtimes separately. For a connection
failure, confirm a desktop/Xvfb display and available loopback ports. A normal
controller build intentionally ignores `APEXLOAD_RECORDING_E2E_CDP_PORT`; always use
the entry point. CDP is enabled only under the `recording_e2e` build tag, on loopback,
with separate ports for the controller and agent. There are no new REST routes.

### CI

`recording-e2e.yml` supports manual dispatch, relevant pull-request/main changes and
merge queues. It follows the existing same-repository PR trust restriction and
GHES unprivileged-runner convention. Hosted Linux installs Xvfb; GHES runners must
provide it and use configured dependency mirrors. Evidence uploads even on failure.

### Manual installation checks

Automated HTTPS tests trust only the disposable proxy client's context. For a
release that changes packaging or workstation setup, additionally check:

- Start the packaged recording agent on each supported operating system; verify
  browser installation, dashboard origin checks, pairing and disconnect behavior.
- Follow the UI's CA download/trust instructions in a disposable OS/browser profile;
  verify the displayed fingerprint, HTTPS capture and trust removal afterward.
- Configure an individual application's HTTP/HTTPS proxy using the displayed
  address, check loopback bypass behavior, then restore that application's settings.
- Close the recorder window and dashboard independently, interrupt the agent, and
  confirm a new session can start without stale processes or sockets.
- Verify native desktop packaging, browser focus/window placement and OS file dialogs.

OS trust installation, external applications, distributed load generation and
performance benchmarking are intentionally outside this automated suite.

### Coverage matrix

The exact scenario inventory is `scripts/recording-scenarios.json`. A full run
requires every listed scenario once under **each** authentication profile; renamed,
missing, duplicate, skipped and retried scenarios fail validation. File/`--grep`
selections run a subset with the same zero-skip and zero-retry requirement.

| Scenario (file) | Controls/features | Observable acceptance evidence |
| --- | --- | --- |
| `controller: capture checkout, verify, save and replay` (`browser`) | Recording Studio launch; session identity/project; live panel; dashboard assertion picker; HAR; verification; verified save; reopening; browser debugger; one VUser | Real input/click/assert steps, fresh CSRF/order IDs, successful assertion and replay reports, four distinct journeys, terminal COMPLETE, exactly one main checkout and teardown in execution |
| `agent: capture checkout, verify, save and replay` (`browser`) | Real agent startup, Connect (agent check, then pairing); recording/control/convert bridge; save into controller library and execute | Real pairing response and CDP attachment; same fresh-token checkout, verification, debugger and one-VUser assertions as controller launch |
| `HTTP proxy captures HTTP and HTTPS, edits requests, correlates and replays` (`proxy`) | Separate proxied Chromium; HTTPS CONNECT; redirects, cookies, query/form/JSON bodies; include/exclude hosts and static filtering; pause/resume/stop; naming, request details, transaction, note, exclusion, think time; HAR; conversion; save/reopen/delete | Requests reach independent HTTP/HTTPS fixture; paused/excluded/static requests absent; form body preserved; diagnostic excluded from generated HTTP; automatic CSRF extractor present; lower-confidence order ID requires review and is bound through the editor; fresh cookies/tokens/IDs accepted on replay and one-VUser execution; deleted session disappears |
| `dashboard and closed-shadow toolbar control the same live session` (`controls`) | No companion window; bar opens collapsed; focus; pause/resume, navigation, inclusion and data/secret marking on both control surfaces; section selection; both transaction/note controls; suggestions; toolbar undo and dashboard delete-last; assertion picker and Esc cancel; collapse/expand, edge placement, hide-to-tab and placement carried to the next document with no site storage; reload; thumbnails; HAR | Authoritative state and exact live step counts; no paused/control input captured; init/main/teardown retained; markers retained; thumbnails loaded through UI; conversion verified |
| `capture modern web surfaces and preserve conversion warnings` (`controls`) | Text/password, keyboard, select, checkbox/radio, click/double-click, hover, drag/drop, upload, dialog, iframe, open shadow root, popup/window switching, SPA, scrolling, WebSocket payloads | Captured kinds/values/selectors/frame context; masked password and scrubbed socket secret; upload filename and password environment binding preserved; explicit preparation warnings retained with converted DSL |
| `HAR host filtering preserves browser actions and disabled WebSocket payloads stay absent` (`options`) | Browser HAR host exclusion and WebSocket payload switch | Excluded host request reaches fixture but is absent from exported HAR; browser action still replays; socket observed without payload retention |
| `transaction splitting accounts for every output` (`options`) | Transaction splitting, per-output verification, verified batch save, workspace placement and reopening | Exactly two outputs; both verified, saved and debugged; exact six-event capture/verification/replay ledger |
| `recording verification promotes a working fallback selector and re-verifies the changed conversion` (`options`) | Selector self-heal and promotion through recording verification UI | Fixture changes the control ID; fallback succeeds, Accept changes the authoritative candidate, new verification and saved-script replay pass |
| `parameterization review applies a recorded input and saves it into the selected folder` (`options`) | Review selection/application; folder creation/selection; verified save | Applied badge/selection survives update; generated parameter persists; saved project/folder IDs match; capture value received on verification and reopened replay |
| `conversion preview is ephemeral and changing options invalidates verification before unverified save` (`conversion`) | Preview without persistence; conversion settings; verification invalidation; unverified save | Script list remains empty after preview; option change clears verification and disables old output verification; unverified saved draft reopens and debugs successfully |
| `controller: ipad_air recording engine receipt survives dual-output save and replay review` (`environment`) | Actual desktop Chromium recording with declared emulation; browser/HTTP output receipts; configured replay engine review | CDP viewport/scale/touch match applied receipt; actual observed engine stays Chromium; iPad configured WebKit difference is explicit; matching Pixel outputs replay without upgrading physical qualification; saved/reopened receipts survive configured-engine edits |
| `controller: pixel_8 recording engine receipt survives dual-output save and replay review` (`environment`) | Actual desktop Chromium recording with declared emulation; browser/HTTP output receipts; configured replay engine review | CDP viewport/scale/touch match applied receipt; actual observed engine stays Chromium; iPad configured WebKit difference is explicit; matching Pixel outputs replay without upgrading physical qualification; saved/reopened receipts survive configured-engine edits |
| `agent: ipad_air recording engine receipt survives dual-output save and replay review` (`environment`) | Actual desktop Chromium recording with declared emulation; browser/HTTP output receipts; configured replay engine review | CDP viewport/scale/touch match applied receipt; actual observed engine stays Chromium; iPad configured WebKit difference is explicit; matching Pixel outputs replay without upgrading physical qualification; saved/reopened receipts survive configured-engine edits |
| `agent: pixel_8 recording engine receipt survives dual-output save and replay review` (`environment`) | Actual desktop Chromium recording with declared emulation; browser/HTTP output receipts; configured replay engine review | CDP viewport/scale/touch match applied receipt; actual observed engine stays Chromium; iPad configured WebKit difference is explicit; matching Pixel outputs replay without upgrading physical qualification; saved/reopened receipts survive configured-engine edits |
| `browser: recorded actions exercise branches and every loop mode` and `http: recorded actions exercise branches and every loop mode` (`authoring`) | Source-editor authoring around captured actions: if/else, switch/default, counted/while/do-while/foreach/nested loops, break/continue, safety limit; HTTP parallel branches | Both initial browser/HAR outputs verified and saved; exact ordered 26-event sequential ledger; HTTP additionally has two order-independent branch outcomes; nested iteration evidence and no leaked loop/row variables |
| `recorded browser inputs use list parameters, CSV rows and session overrides` (`authoring`) | Literal/list values, CSV-backed input, runtime overrides, invalid override JSON, missing and configured secret bindings | Fixture receives values different from capture; invalid JSON marked invalid; missing secret disables replay; UI-configured secret reaches target but is absent from replay report |
| `recorded HTTP correlation fails with the wrong extraction and succeeds after repair` (`authoring`) | Manual JSON extraction/substitution; deliberate broken correlation; diagnosis and source-editor repair | Wrong token rejected with 409; Problems identifies failure; repaired extraction gets a new token and succeeds |
| `recorded browser flow consumes a fresh browser extraction result` (`authoring`) | Browser evaluation/extraction workflow | Runtime `result` is present in replay extractions and supplies the fixture's subsequent input |
| `debugger steps a recorded HTTP script, diagnoses failure and repairs it` (`authoring`) | Run, start paused, pause during an in-flight request, step, continue, stop/restart, permanent breakpoint, run-to, break on failure, runtime profile save/reload, watches, timeline selection, HTTP evidence, history comparison | Paused action names and exact run-to ledger; deliberate assertion failure then successful repair; captured response body; watched extraction; historical outcome comparison; second breakpoint honored; in-flight pause gates the next action, stop prevents it, and a clean restart completes |
| `saved browser and HTTP recordings execute one VUser through New Test` (`authoring`) | Both conversion outputs, New Test workload and iteration override, normal engine execution | One VUser, one iteration, COMPLETE status; exact fixture-side main and final teardown events; execution manifest retained |
| `reject duplicate capture, survive dashboard reload and recover after window closure` (`recovery`) | Invalid URL; duplicate active session; dashboard reload; window-close recovery; minimal toolbar stop and HAR off | Invalid field, 409 duplicate, adopted live session, preserved steps/`browser_closed`, successful verification and subsequent session without HAR |
| `mobile recording reuses exported session state with HAR disabled` (`recovery`) | Mobile preset, minimal toolbar, HAR off, storage-state export/reuse | Touch-enabled runtime; no HAR; exported state verifies; next browser actually restores a localStorage choice from capture |
| `force-finish preserves captured steps and releases a stuck recording for the next session` (`recovery`) | Deterministic test-tag stop gate, Force Finish | Stopped status with steps retained, successful verification, subsequent capture/stop succeeds |
| `unavailable and disconnected agent can be connected again` (`browser`) | Unreachable verification, killed agent, failed launch, Disconnect, replacement agent | Visible verification/launch failures; new real agent pairs, captures and verifies successfully |

Every `debug()` journey exports its session and checks the report outcome. Multi-output conversions explicitly check output count. Executable replay cases
verify each output; the iPad emulation identity cases deliberately save unverified
preparation drafts because recorded Chromium and configured WebKit differ. They
verify receipt retention and review visibility, without qualifying physical iPad
or WebKit replay.
The modern-surfaces output deliberately remains a preparation case: password
values are never captured, and local upload names cannot become executable paths
without an operator-provided fixture asset. Its test requires those exact warnings
and bindings; it does not count generation as a successful replay. Its recorded
iframe/shadow/window context is retained for inspection in the attachment.

Browser parallel control blocks are not an executable engine feature: the engine
rejects browser actions in parallel branches. Parallel assertions therefore use
HTTP branches. The proxy UI always excludes static assets; it offers no static
inclusion toggle. The Recording Studio no longer opens a companion control
panel in the recording browser: the recorded page's bar and the docked studio
panel are the control surfaces. The control-panel endpoint remains for older
dashboards, and its traffic stays outside the application HAR
(`TestControlPanelTrafficIsExcludedFromHAR`). The available browser extraction
workflow is an authored `browser_evaluate_js` action in the source editor.

Package-level regression tests additionally cover the normal/test build boundary,
last-click flush, one-shot assertion picking, scoped import preservation, loop and
parallel variable readiness, sparse/out-of-order debugger events, bridge CORS
credentials, authoring state during pairing, and applied parameterization state.
Harness tests exercise real child-process cleanup/socket release, startup failure,
chunked log redaction, report inventory enforcement and trace/report sanitization.
