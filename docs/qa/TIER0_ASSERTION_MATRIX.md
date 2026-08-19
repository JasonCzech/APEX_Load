# Tier-0 Assertion Matrix

This is the completion inventory for M1. `GREEN` means every assertion in the row is implemented
and passing on the current branch; `PARTIAL` means some executable coverage exists; `MISSING` means
the required behavior has no journey assertion. File counts are deliberately not used as a proxy.

| Step | Required assertions | State | Current evidence / missing work |
|---|---|---|---|
| S1 | Real sign-in, dashboard landing, connected stream, entitled navigation | GREEN | `tier0-local-auth.spec.ts` proves invalid-login rejection and a real seeded `admin/admin` login; `tier0-auth.spec.ts` separately pins dev-admin harness posture. |
| S2 | UI creates Domain → Application → Project → Release; selected project owns the application; scope propagates | PARTIAL | API hierarchy coherence and repository rendering are covered; UI creation and wrong-project regression remain. |
| S3 | Deterministic OpenAPI import, validate, commit, library visibility | MISSING | Add a dependency-free fixture and UI/API flow. |
| S4 | Action-list-first editor, tab preservation, save/reopen, bounded large-script render | MISSING | Add editor assertions using the S3 script. |
| S5 | Six wizard steps, back/forward preservation, review parity, guarded navigation cancellation | MISSING | Add scenario-builder specs. |
| S6 | Preflight, estimate, launch to RUNNING, `pool_id` survives save/reload | MISSING | Add launch and assignment round-trip specs. |
| S7 | Two advancing metric samples and rendered live metrics | GREEN | `tier0-live-metrics.spec.ts`. |
| S8 | Confirmation dismiss is inert; UI pause/resume; duration and VUser changes become visible | PARTIAL | API pause/resume/scale is covered; UI confirmation, duration, and visible state remain. |
| S9 | Stop and abort separately reach their exact terminal semantics | PARTIAL | Stop/non-failure is covered; abort and exact statuses remain. |
| S10 | Three lenses identify the run and reconcile shared headline values | PARTIAL | Lens rendering is covered; numeric reconciliation remains. |
| S11 | Manifest, checksum verification, and download of a listed artifact | PARTIAL | Manifest and checksum verification are covered; browser download remains. |
| S12 | Results filter locates run and opens its report | PARTIAL | Results visibility is covered; filter and report navigation remain. |

The full-arc smoke remains `MISSING` until S1–S12 can execute sequentially against one workspace.

## API contract coverage

The generated [API contract matrix](evidence/M1/api-contract-matrix.json) is the route-level
completion inventory. It derives its totals from the endpoint catalog, checks catalog-to-router
public/authenticated parity, proves anonymous rejection through the generic middleware, and runs
allowed and denied RBAC cases from structured access requirements. Its own `GREEN`, `PARTIAL`, and
`MISSING` totals are authoritative: legacy resource-scoped recorder/replay routes remain missing
until the endpoint catalog carries their role metadata, while capability-token routes remain
partial until their handler-specific anonymous/authenticated cases are incorporated into the
generated suite.
