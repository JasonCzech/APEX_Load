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
| S5 | Six wizard steps, back/forward preservation, review parity, guarded navigation cancellation | GREEN | `tier0-scenario-builder.spec.ts` walks Start → Shape / Source → Scripts → Run Settings → Data → Review, verifies the name, selected script, duration, unit, and VUsers after backward/forward traversal, reconciles Review and the stored scenario, and proves Keep editing cancels dirty navigation without losing the draft. |
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
`MISSING` totals are authoritative. Legacy resource-scoped recorder/replay routes share role
profiles with their runtime middleware, while metrics, recording-bridge, and collector-token routes
run their handler-specific anonymous/authenticated cases. Route-contract coverage is fully green;
that does not change the S1–S12 journey states above.
