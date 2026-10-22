# AUTH cross-cutting review — 2026-10-21

Before NA-B, the AUTH-A, AUTH-B and AUTH-C commits on this branch were reviewed together, across slices, by three reviewers (security, backend and dashboard). Each slice had already passed its own reviews ([receipts](../roadmap-authoring-intent-receipts-2026-10-21/README.md), [revise-copy intents](../roadmap-revise-copy-intents-2026-10-21/README.md), [validation context](../roadmap-validation-context-2026-10-21/README.md), [lifecycle matrix](../roadmap-lifecycle-property-matrix-2026-10-21/README.md)). The contracts are documented in [script lifecycle](../../../SCRIPT_LIFECYCLE.md#retry-safe-creates).

## Findings and fixes

### Validation context (AUTH-B)

- **Secret-value oracle (medium, two reviewers).** Each referenced secret's value was digested under one unwrapped, deployment-wide key, with nothing tying it to the scope it came from. Comparing digests across projects, or between an application-assigned and an unassigned draft, confirmed guesses of a write-only value; a database reader with one captured context could test guesses offline. Validation depends only on whether a secret is configured, so no value is digested any more. The `secret_references` component now covers where the script's secrets resolve (its project, an application ahead of it, or an assignment that cannot supply secrets in this project) and each name's availability. Replacing a value leaves the context unchanged; a rebinding or a change of availability still changes it.
- **Re-check ignored the resolver's rule (medium, two reviewers).** The re-check of missed secrets read an assigned application's secrets without the resolver's check that the application belongs to the script's project, so a draft carrying another project's script ID revealed which names that project's application holds. The resolver and the re-check now share one binding (`scriptSecretBinding`): an application in another project, or one that no longer exists, resolves nothing and has nothing to re-check, while a failed application or assignment read is an error. A dangling assignment no longer makes the context permanently unavailable.
- **Draft bound to the wrong project (low).** Draft validation built its context for the draft's own `project_id` (or the default project) rather than the authorized one; a body with only `folder_id` was refused by the probe. Both handlers now bind the draft to the authorized project, as a draft save does, and a draft ID selects a library assignment and an approval only when it names a stored script in that project, read durably rather than from a replica's cache.
- **Probe route (low).** Another project's `script_id` answered 403, revealing it; it now answers 404 like a missing one. The probe keeps the `scripts:write` service-account scope of the validation it re-checks, now documented.

### Receipts (AUTH-A)

- **Raw credentials in an unwrapped digest (low).** Without a key wrapper, a keyed run create digested its raw request, credentials included, although run creates were never digested before. Such a create is now refused with 422 `idempotency_credentials_unprotected` before its key is claimed, which the dashboard treats as a definitive refusal and shows the server's advice to use secret references (scenario creates keep the earlier, documented fallback). A wrapper on a store that cannot keep the credential key atomically is an error, never a reason to fall back. The stale comment and the docs are corrected.
- **Completion after the scrub (low).** A pending receipt resumed just before its key expired could complete after the expiry scrub and keep its outcome for good. Completion now requires an unscrubbed, unexpired receipt; otherwise the create rolls back (409, and a retry answers the key as expired). The request's cleanup can no longer delete such a receipt either, which would have let the retry claim the key afresh.
- **Projection inside the transaction (low).** A script whose stored content alone exceeds the outcome bound now omits its projection without building and marshalling it inside the create transaction. Scenario creates are bounded by the 8 MiB request limit.
- **Outcome retention (low).** The retention of a create's outcome for up to 24 hours after edits (scripts as authored, scenario credentials masked), replayed only to the same caller and key, is now documented.

### Dashboard

- **Replayed scenario create kept the old form (high).** A replay paired the builder's original form with the current scenario's version tag, so the next save could silently overwrite edits made elsewhere. TestHub now opens the scenario as it is now, with its own baseline, keeping the wizard step, and applies the library Edit's checks: an archived scenario stays in the library, and one read without a baseline asks for a reload before saving. **Save and configure run** shows a replayed scenario the same way instead of configuring a run from it.
- **Duplicate run after a replay (medium).** A replayed run create ended the draft's intent, so clicking Create again queued a second run. The key now stays with the open draft (creating again replays; a changed draft is refused, saying the run exists and keeping **Open run**), its reload record is dropped, and the replay notice offers **Save as new**.
- **Key reused across queued runs (low-medium).** A revised copy's recorded key was recalled by payload alone, so revising another queued run with the same settings replayed the first one's copy. The recall now includes the source run.
- **Save as new beside unrelated errors (low).** The offer is tied to the refusal it answers in the script editor, TestHub and the run modal.
- **Probe load (low).** The periodic probe runs only while the window has focus; a failed probe doubles the spacing (up to 10 minutes, reset by a new result); a stored script's 409 `validation_context_revision_changed` makes the result stale ("the stored script changed since the check").
- **Weak tests (low).** The reload e2e test now clears the record of unanswered keys first, so only the browser checkpoint can supply the key. The scenario-assembly test is renamed to what it proves and covers spaced references.

## Not changed here

Every other `/api/v1/scripts/{id}/…` route, the stored-script validate route included, resolves the project from the script ID in the access middleware, which answers 403 for a script in a project the caller cannot access and 404 for a missing one. That platform-wide convention predates AUTH; the probe answers 404 because its project comes from the request body. Hiding foreign IDs on every script route is a separate change.

## Validation

| Gate | Result |
| --- | --- |
| `go build ./...`, `go vet ./pkg/api ./pkg/storage` | Passed |
| Full `go test ./pkg/api` | Passed (768 s) |
| `go test ./pkg/storage`; PostgreSQL `-tags postgres_integration ./pkg/storage` and `./pkg/api -run Postgres` | Passed |
| `go generate ./pkg/api` (swagger, catalog, generated TypeScript) and `go test ./docs/qa` | Regenerated; passed |
| Dashboard `tsc`, eslint on changed files, full Vitest | Passed (725 files, 8,115 tests) |
| `workflow-authoring-intent.spec.ts`, dev-admin and local-auth profiles, on a rebuilt bundle | Passed (2 tests each) |

New tests cover the value-free secret binding, the shared resolver binding, durable draft-ID checks, folder-only drafts, the 404 probe, the run-create refusal without key wrapping, a wrapper without compare-and-swap, completion and abandonment past a key's lifetime, the projection size bound, replayed scenario and run creates, source-scoped revise keys, Save as new pairing, and probe focus, backoff and revision changes. The two validation-context binding tests were run against the code before the fix and failed; the others assert behaviour the earlier code did not have.

## Review

- Round 1 (security, backend and dashboard, on a scratch snapshot of the AUTH commits): 16 findings after merging duplicates (1 high, 6 medium, 9 low), all fixed above.
- Round 2 (the same reviewers): every round-1 finding confirmed fixed; six low findings and two nits. Five lows and both nits are fixed: the run-create refusal shared a transient error code; a rolled-back create's cleanup deleted its expired receipt; the draft-ID check read the replica cache; the replay path skipped the library Edit's archived and baseline checks; **Save and configure run** ignored a replay; a later refusal after a run replay hid **Open run** and said the run "may" exist; the probe backoff outlived its result. The sixth low, the stored-script validate route's 403, is the platform convention noted above.
- Round 3: security and backend found nothing new (the backend reviewer asked for the generated API documents to be regenerated, done); the dashboard reviewer found one low and one nit, fixed: a "run was deleted" refusal left the replay notice and **Open run** showing, and a changed-draft refusal showed two **Save as new** buttons.
- Round 4 (dashboard): no findings.
