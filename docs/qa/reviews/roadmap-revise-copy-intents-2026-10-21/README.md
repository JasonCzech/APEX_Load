# Revise-copy intent keys — 2026-10-21

The second [AUTH-A slice](../../../ROADMAP_IMPLEMENTATION_SPEC.md#auth-a--durable-user-intent-and-explicit-receipt-semantics) extends the [intent and receipt slice](../roadmap-authoring-intent-receipts-2026-10-21/README.md) to run creates, so a queued run's revised copy has a draft-owned intent key. The contract is documented in [script lifecycle](../../../SCRIPT_LIFECYCLE.md#retry-safe-creates).

## What changed

- **Keyed run creates.** `POST /api/v1/tests` accepts `Idempotency-Key` under receipt contract 1 only (contract 2 is refused with 400). The key fixes the run ID; a retry returns the run the first attempt created, as it is now, and adopts a run whose response and receipt completion were lost. A failure whose outcome cannot be confirmed answers 503 `test_create_outcome_unknown` and frees the lease for an immediate retry. With a caller-supplied `requested_test_id`, nothing is adopted: an existing run there answers 409 `test_identity_in_use`, and a refusal unrelated to that run keeps its original status. The generic duplicate-request fence no longer intercepts keyed run creates.
- **Deletion.** Deleting a run tombstones its keys in the delete transaction, so a retry answers 410 and never recreates it.
- **Credential fingerprints** use the same wrapped-key scheme as scenarios (shared helper).
- **Dashboard.** The revised-copy editor owns an intent key, sent through the shared intent helper. A replay is reported as "This draft already created run X (status); no new run was queued." Opening that run over later edits only asks to discard them. Save as new starts another run.

## Validation

| Gate | Result |
| --- | --- |
| API: keyed run create replay, change conflict, contract 2 refusal, deletion, lost-response adoption, unconfirmed commit, caller-chosen ID in use and unrelated refusal, revised copy with an unchanged source, real (trusted-header) authentication, OpenAPI | Passed, with the run-create, revision, receipt, idempotency, middleware, security and generated-contract suites |
| Storage, SQLite and PostgreSQL (`-tags postgres_integration`): delete-transaction tombstone and receipt suites | Passed |
| Dashboard typecheck and eslint on changed files; CreateTestModal, queued-revision and intent unit tests | 59 tests in 3 files passed |
| Playwright dev-admin and local-auth: `workflow-authoring-intent.spec.ts` (both tests) and `workflow-new-test.spec.ts` | 20 passed in each profile |

Playwright ran through the same Node 24 scratch copy of `scripts/run-e2e.sh` as the first slice (the pinned Node 22.23.3 is not installed here).

## Review

One reviewer read scratch snapshots only:

- Round 1: four findings, all fixed. The generic idempotency middleware intercepted keyed run creates under real authentication (high). A possibly committed failure answered 400. A caller-supplied ID's unconfirmed attempt was over-promised. A replayed run was presented as new.
- Round 2: two findings, fixed. A refusal with an unrelated run at a caller-chosen ID answered 503. Opening a replayed run discarded later edits without a prompt.
- Round 3: one finding, fixed: saving from the leave prompt queued another run.
- Round 4: no findings (also covering the second Playwright test).

## Follow-up

The [cross-cutting AUTH review](../roadmap-auth-cross-cutting-2026-10-21/README.md) refused keyed run creates that carry credential values where no key wrapper is configured (rather than digesting them unprotected), kept a replayed draft's key so creating again replays, and recalls a revised copy's key only for its own source run.

## Remaining

As for the first slice: crash injection at each receipt/resource boundary, two-tab concurrent-click qualification, and both-profile browser coverage of the conflict, expiry and deletion paths remain open.
