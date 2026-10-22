# Lifecycle property matrix — 2026-10-21

This [AUTH-C slice](../../../ROADMAP_IMPLEMENTATION_SPEC.md#auth-c--lifecycle-edges-and-property-level-parity) expands the generated [lifecycle parity matrix](../../LIFECYCLE_PARITY_MATRIX.md) from entry-point presence to every AUTH-2 property, inventories the script producers outside the ordinary import routes, and closes the property gaps it found.

## What changed

- **Property matrix.** Each of the nine AUTH-2 rows names, for each of the nine AUTH-2 properties (ownership and destination, warnings and provenance, secret references, capability and policy gates, pending edits, dirty navigation, delayed replies, exact resolved payload, durable reopen), the tests that prove it. `TestLifecycleParityPropertiesAreProven` checks every cell. A Go proof is a `pkg/api` test outside a build-tagged file that can skip neither itself nor through a test helper. A Vitest proof is a test under `dashboard/src` in a file that skips, focuses, conditions or marks todo no test at all. A Playwright proof runs in both e2e profiles. A shared cell defers to a row that proves the property directly (template and clone drafts, and debugger navigation inside the editor, use the shared script editor's guard and pending-edit handling). No cell is open.
- **Script producers.** `TestLifecycleParityScriptProducersAreInventoried` walks the controller package's call graph up from every call that adds a library script: the store entry points that reach a script insert and the runner's create cores. It follows calls and references, including package-level initializers, but treats a route registration's references to handlers as bindings. It continues past a handler that another handler calls. Every route it reaches is either an inventoried import route or has a producer row with its AUTH-2 row and a `pkg/api` test that requests the route with its method.
  - A function whose name is ambiguous must have its callers listed. That list is checked against every function that names the receiver type, or a struct field or function result that holds it, and calls the method. The graph discovers those fields and results itself.
  - A declaration that nothing calls must be classified.
  - In `pkg/storage`, every function that inserts into `scripts` is classified, and a walk of its calls and references admits only the listed store entry points, which are also where the controller walk starts. The `SaveScript` upsert may be reached only through its classified callers.
  - Outside the two packages, subpackages included, every production call to a store entry point, or to an exported function the walk reached, is classified. Today the only such call is the CLI's HTTP client.
  - The walk found eight producer routes outside `/api/v1/scripts/import/`: script create, authoring-session approval, both recorder conversions, the three library imports (the archive route refuses every non-result item) and GitHub sync jobs. The GitHub sync job was not in the earlier survey.
- **Recording leave guard.** The recorder panel registers an unsaved-work guard. A conversion preview, or a Salesforce draft, that differs from the review generation last saved is unsaved: leaving asks first, a save releases it, and a new conversion after the save is unsaved again. The panel's Dismiss asks the same question before it discards an unsaved review. The standalone recorder window sits inside the same guard provider, so closing or reloading it with an unsaved review asks the browser's leave question.
- **Scenario secret references.** In a scenario response (create, read, list, update, archive, runtime apply, and the authoring document), a runtime environment value that is only a project-secret reference (`{{secret.NAME}}`) names a secret rather than holding one, so it is returned as written, as a script read does. A controller-owned runtime reference (`APEX_RUNTIME_OVERRIDE_*`), which an editor cannot author, and every other value under a sensitive name or with a credential shape stay masked. The builder's checkpoint accepts only such references in an environment map, so a reopened scenario that bound a secret could not be checkpointed while the value read back as `[REDACTED]`; the checkpoint and the server's project-draft check now also accept the spacing the runtime accepts (`{{ secret.NAME }}`). Library export, sync-link and import-claim hashes, test reads, manifests and provenance keep the previous masking. A scenario create's stored original outcome (its receipt) now holds the editor form, references included, as a script's already does; no hash or comparison reads it.
- **New proofs.**
  - An import review, and a recording review, ask before they are left unsaved and release after their save.
  - A recording keeps the operator's rename through a late refresh that changes the recording environment, and saves it.
  - Debugging a stored script sends the edited, resolved draft, including an unblurred data cell.
  - A scenario's root and group secret references survive create, read and a controller restart, and a reopened scenario keeps them through the builder checkpoint and its save request.
  - A clone, a component insert and a library import each keep the source's import provenance.
  - The GitHub sync-job route itself creates the repository's script.
  - The library archive route refuses a script bundle.

  Each new component test failed against a temporary control that removed the behavior it covers.

## Validation

| Gate | Result |
| --- | --- |
| `go test ./pkg/api -run 'TestLibrary\|TestGitHub\|TestCloneDraft\|TestScriptSecretReference\|TestLifecycleParity'`, the masking and scenario reference tests | Passed |
| Full `go test ./pkg/api` | Passed (773 s). The first full run found the AUTH-A lock-held write guard failure, fixed in its own commit |
| Temporary controls: a producer row removed, a missing test named, a handler delegating to a producer handler, a package-level dispatch table, a new storage create method, an unlisted sync-service caller, a struct field holding the sync service, a create call in a `pkg/api` subpackage | Each failed the build as intended; removed afterwards |
| Dashboard typecheck, ESLint on the changed files, and the changed Vitest files | Passed |
| The 14 Playwright specs the matrix cites, both profiles (dev-admin, local-auth) | 76 passed in each; one declared skip (the private LoadRunner acceptance archive) |
| `workflow-recording-review.spec.ts` after it began asserting the leave prompt with a bounded wait, both profiles | 2 passed in each |
| Documentation checks | Passed |

## Review

One reviewer read scratch snapshots only:

- Round 1: eleven findings, all fixed. Five were medium: storage classifications were unchecked text, the walk stopped at the first handler, package-level initializers were not walked, the GitHub sync-job proofs never created a script (and route mentions ignored the method), and the multi-script reopen proofs reopened nothing. The six low ones: a multi-script navigation proof that left nothing unsaved; three proofs too weak to stand (then open cells, now closed with new tests and the two product changes); a Vitest skip check that missed conditional skips; one-way stale checks; contradicting deviation bullets in the feature-completion spec; and duplicated helpers.
- Round 2: four findings, all fixed. The medium one: the store entry points and the walk's starting points were separate lists. The low ones: a library provenance proof that never checked provenance, holders that had to be named by hand, and smaller blind spots (subpackages, hard-coded outside names, storage method values, skipping helpers, `postman`/`postgres` helpers accepted as a POST).
- Round 3: four findings, all fixed. The medium one: unmasking references in the shared masking changed library export content and persisted hashes; the change is now confined to scenario responses. The low ones: the recorder panel's Dismiss bypassed the guard, the scenario reference proofs covered only the API, and nits (a misplaced comment, a spec bullet, a one-level skipping-helper check).
- Round 4: three low findings, all fixed: this record overstated that no stored content changed (the scenario create receipt stores the editor form), the builder's checkpoint refused the spaced reference form the editor now shows, and the recording spec would have waited for a leave prompt until the test timeout rather than fail clearly.

## Remaining

The import saves' persisted-copy check is still exercised directly rather than through each save's HTTP request. Seam retirement (AUTH-3) is unchanged.
