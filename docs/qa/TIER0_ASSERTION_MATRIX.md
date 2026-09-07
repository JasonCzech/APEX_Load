# Tier-0 Assertion Matrix

This is the completion inventory for M1. `GREEN` means every assertion in the row is implemented
and passing on the current branch; `PARTIAL` means some executable coverage exists; `MISSING` means
the required behavior has no journey assertion. File counts are deliberately not used as a proxy.

| Step | Required assertions | State | Current evidence / missing work |
|---|---|---|---|
| S1 | Real sign-in, dashboard landing, connected stream, entitled navigation | GREEN | `tier0-local-auth.spec.ts` proves invalid-login rejection and a real seeded `admin/admin` login; `tier0-auth.spec.ts` separately pins dev-admin harness posture. |
| S2 | UI creates Domain (and its provisioned repository) → Project → Application → Release; selected project owns the application; scope propagates | GREEN | `tier0-workspace.spec.ts` drives every create through the UI, targets an owner while a decoy project is active, verifies persisted application ownership, and carries project/application/release scope into the repository tabs. |
| S3 | Deterministic OpenAPI import, validate, commit, library visibility | GREEN | `tier0-script-import.spec.ts` uploads the dependency-free `tier0-orders.openapi.yaml` fixture, proves dry-run preflight and generated action semantics, verifies inspection does not persist, commits through the protected endpoint, and finds the saved script in the scoped library. |
| S4 | Action-list-first editor, tab preservation, save/reopen, bounded large-script render | GREEN | `tier0-script-editor.spec.ts` independently seeds the S3-shaped OpenAPI script, proves Action List is primary, preserves a runtime edit across Source/Runtime/Action List switches, saves and reopens it from the library, and requires a 400-action script to expose a usable virtualized outline within a bounded test. |
| S5 | Six wizard steps, back/forward preservation, review parity, guarded navigation cancellation | GREEN | `tier0-scenario-builder.spec.ts` walks Start → Scripts → Shape / Source → Run Settings → Data → Review, verifies the name, selected script, duration, unit, and VUsers after backward/forward traversal, reconciles Review and the stored scenario, and proves Keep editing cancels dirty navigation without losing the draft. |
| S6 | Preflight, estimate, launch to RUNNING, `pool_id` survives save/reload | GREEN | `tier0-run-launch.spec.ts` requires successful estimate and preflight before Start Now, then observes the created run at `RUNNING`; payload-unit and API materialization tests preserve and resolve `pool_id`. |
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
[normal test cycle and exact coverage](WORKFLOW_REGRESSION_SUITE.md) for commands,
remaining extended cases and execution evidence.

## API contract coverage

The generated [API contract matrix](evidence/M1/api-contract-matrix.json) is the route-level
completion inventory. It derives its totals from the endpoint catalog, checks catalog-to-router
public/authenticated parity, proves anonymous rejection through the generic middleware, and runs
allowed and denied RBAC cases from structured access requirements. Its own `GREEN`, `PARTIAL`, and
`MISSING` totals are authoritative. Legacy resource-scoped recorder/replay routes share role
profiles with their runtime middleware, while metrics, recording-bridge, and collector-token routes
run their handler-specific anonymous/authenticated cases. Route-contract coverage is fully green;
that does not change the S1–S12 journey states above.
