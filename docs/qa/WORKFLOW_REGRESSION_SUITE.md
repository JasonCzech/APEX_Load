# User workflow regression suite

The browser library now includes a continuous authenticated first-run journey and focused recovery tests based on [U01–U24](FRONTEND_USER_WORKFLOW_REVIEW.md). Tests exercise the embedded production dashboard against a real disposable controller and SQLite store. The [Figma review board](https://www.figma.com/design/9tjL68AABdh0cOtSDvexeT) records the original frontend evaluation.

## Normal code-change cycle

From `dashboard/`, run:

```sh
npm run test:cycle
```

This runs TypeScript checks, strict lint, all Vitest tests, a fresh production build, and both browser profiles. CI runs these checks in its existing dashboard job and invokes the same `scripts/run-dashboard-workflows.sh` entry point. Both browser profiles execute even if the first fails; either failure makes the command fail. There are no automatic test retries.

Prerequisites: the repository's Go toolchain, Node version from `dashboard/package.json`, installed dashboard dependencies, and Chromium (`dashboard/node_modules/.bin/playwright install chromium`). The controller uses ports 18081 and 18082 by default; override with `APEXLOAD_E2E_PORT` and `APEXLOAD_E2E_AUTH_PORT`.

For browser-only reruns after building, run `npm run test:workflows`. For focused diagnosis from the repository root:

```sh
scripts/run-e2e.sh workflow-authoring.spec.ts
APEXLOAD_E2E_PROFILE=local-auth scripts/run-e2e.sh workflow-local-auth.spec.ts
```

`npm test` remains the fast unit-test command. Use `test:cycle` for completed frontend or workflow changes, as required by `AGENTS.md`.

## Executable coverage

IDs in test titles link failures to the audit. A mapped ID means the assertions listed here are implemented, not that every proposed variant in the audit is automated.

| Audit IDs | Browser library | User-visible assertions |
|---|---|---|
| U01 | `workflow-local-auth.spec.ts` | Real sign-in; UI-created domain/project/application/release; OpenAPI validation, conversion and commit; script edit/save; scenario creation; named run launch; advancing controlled-target requests; graceful completion; Results → report → archive; nonempty download with matching server bytes. Business objects are not seeded between steps. |
| U02 | `tier0-local-auth.spec.ts`, `workflow-local-auth.spec.ts` | Authentication and incorrect-password handling; viewer launch denial; sign-out and browser Back remain outside the protected workspace. |
| U03, U08 | `workflow-authoring.spec.ts` | Empty prerequisites, invalid scenario rejection, no invalid persistence, script-picker focus, and draft retention through cancellation. |
| U04 | `workflow-authoring.spec.ts`, `tier0-workspace.spec.ts` | Similarly named scripts in separate scopes retain distinct identities through reload/history; workspace UI persists the selected owner and scope. |
| U05, U06 | `tier0-script-import.spec.ts`, `workflow-authoring.spec.ts` | Dry-run and conversion do not persist; explicit commit and library reopen; malformed input can be replaced and revalidated. |
| U07, U18 | `workflow-authoring.spec.ts` | Keep, save and discard branches; injected 500, 409 and 401 save responses preserve the draft and stored version; retry succeeds. These response tests do not simulate real concurrent writers or session expiration. |
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

## Fixtures and evidence

The dev-admin profile covers focused workflows. The local-auth profile uses real seeded test personas and runs the complete journey. Each profile gets an independent temporary database/archive directory. Controlled HTTP traffic stays on loopback with small VUser counts. Newly created runs are registered for fixture teardown; unfinished runs are aborted and awaited before shutting down their target. The harness terminates its controller and removes its store even on failure.

Only explicit fault tests intercept API responses. Normal import, persistence, launch, execution, reports and downloads use the real backend. WebSocket recovery closes the actual browser connection rather than substituting fake metrics. The full-arc/report specs explicitly enable the report feature; archive tests also exercise the default legacy path.

Failure screenshots and traces are retained separately under `dashboard/test-results/dev-admin/` and `dashboard/test-results/local-auth/`. CI also writes per-profile JUnit XML and uploads browser evidence for 14 days. Record the revision and command with any reported result; ID mappings and old GREEN labels are not execution evidence.

## Remaining extended cases

The original audit is broader than this deterministic local regression baseline. The following are still explicit extensions, not passing claims: expired import-inspection sessions; real concurrent-editor/session-expiry behavior; multi-group data-pool/placement round trips on an external generator fleet; schedule editing and DST boundaries; dependency-agent configuration; true browser zoom, contrast and screen-reader review; large catalog filtering; the complete governed-runtime matrix; and connected native recording-agent record/stop/redaction/save workflows. Those need their relevant fixtures or environments before being added as required integration lanes. The empty picker still lacks the proposed inline create/import return path; current tests preserve and check its existing recovery behavior.


## Verification and known CI blocker

Verification is against the working tree based on `87ce7626d`, on 2026-09-06, using Chromium on macOS. The expanded library adds 22 browser tests. The normal cycle runs 41 dev-admin tests and 4 local-auth tests, including U01. The complete `npm run test:cycle` command exited 0: **41/41 dev-admin browser tests, 4/4 local-auth browser tests, and 3,842/3,842 unit tests in 341 files passed**, along with type checking, lint and the production build. Separate accessibility lint, documentation checks, workflow syntax (`actionlint`) and diff whitespace checks also passed.

The separate CI `bundle:check` gate still fails its existing aggregate JavaScript budgets: 5.28 MiB raw versus 5.22 MiB allowed, and 1.42 MiB gzip versus 1.41 MiB allowed. An isolated `git archive HEAD` baseline build with the same installed dependencies reproduces both violations; the initial-shell, script-library and execution-route budgets pass. These thresholds were not raised. CI checks the bundle budget after browser execution so this pre-existing failure does not prevent collecting workflow evidence. The overall CI job still fails until bundle size is addressed.
