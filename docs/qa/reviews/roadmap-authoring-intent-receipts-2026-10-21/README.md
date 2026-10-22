# Authoring intent keys and original-outcome receipts — 2026-10-21

This [AUTH-A slice](../../../ROADMAP_IMPLEMENTATION_SPEC.md#auth-a--durable-user-intent-and-explicit-receipt-semantics) adds the negotiated receipt contract and the client-side intent keys for ordinary script and scenario creates. The contract is documented in [script lifecycle](../../../SCRIPT_LIFECYCLE.md#receipt-contract-version-2).

## What changed

- **Receipt contract 2.** A keyed `POST /api/v1/scripts` or `POST /api/v1/scenarios` that sends `X-APEX-Authoring-Receipt-Version: 2` completes its receipt inside the create's own transaction (schema v72). The transaction also stores the disclosure-safe original outcome: ID, project, content revision, creation time and authoring projection, omitted past a stated bound. A retry returns that outcome unchanged; a deleted resource still answers 410. A lost reservation rolls the create back. Version 1 keys, version 1 retries, and receipts an older controller completed or orphaned during a rolling upgrade keep the legacy replay; every `201` echoes the contract that applied. Unknown versions are refused (400), and stores that cannot complete atomically refuse version 2 (503) before claiming.
- **Retention.** A committed delete clears the stored outcome. An hourly janitor scrubs the request digest and outcome of every receipt past its 24-hour replay lifetime; a scrubbed receipt answers only deleted or expired, whatever the claiming replica's clock says.
- **Credential fingerprints.** Where a key wrapper is configured, a scenario create's runtime credential values are fingerprinted under a per-deployment key stored only wrapped, never under the unwrapped request-digest key. Receipts claimed under the earlier digest still match their retries. Without a wrapper the earlier digest applies (documented).
- **CORS.** The contract header is allowed and exposed for approved origins (the desktop webview is cross-origin).
- **Dashboard intents.** Every new script and scenario draft owns an intent key, sent under contract 2. The key is recorded as uncertain before each send. It survives lost responses, failed follow-up reads, superseded results and later refusals of a retry; it is replaced only after a definitive refusal and a changed draft, or by an explicit **Save as new**. Scenario keys live in the browser checkpoint; a bounded local record of unanswered keys (no draft content) recalls a key for the same work after a reload or a lost checkpoint. A replayed original outcome is followed by a read of the current object before editing.

## Validation

| Gate | Result |
| --- | --- |
| Storage receipts, SQLite and PostgreSQL (`-tags postgres_integration`, `fc-wave1-pg`) | Passed: atomic create/receipt, lost and tombstoned reservations, renderer failure, outcome bound, legacy-receipt refusal, scrub, lagging-clock expiry, earlier-digest match |
| API receipts, idempotency, scenario/script authoring, runtime credentials, CORS, OpenAPI and generated-contract freshness | Passed (`go test ./pkg/api -run 'Scenario\|Script\|Receipt\|Idempot\|Runtime\|CORS\|Security\|OpenAPI\|Swagger\|Generated\|StoreOverlay\|Authoring'`) |
| Dashboard typecheck and eslint on changed files | Passed |
| Dashboard unit tests: intent module, keyed create client, both workflows, checkpoint parsing, ScriptEditor, TestHub and the five import/recording surfaces that render the editor | 859 tests in 13 files passed |
| Playwright dev-admin: new `workflow-authoring-intent.spec.ts` plus `tier0-scenario-builder`, `tier0-script-editor` and `workflow-authoring` | 20 passed |
| Playwright local-auth (its allow-list, now including the new spec): `workflow-authoring-intent.spec.ts` and `workflow-authoring.spec.ts` | 9 + 1 passed |
| Documentation checks | Passed |

The e2e runner pins Node 22.23.3, which is not installed on this host. As in the PR 254 review, the specs ran through a scratch copy of `scripts/run-e2e.sh` with the version assertion removed (browser launch check kept), under Node 24.13.0.

## Review

Two independent reviewers (backend and dashboard) read scratch snapshots only, for six rounds:

- Round 1: 13 findings, all fixed. The material ones: CORS blocked the new header for the desktop app; a failed read after a replay or a superseded result cleared the key; a 4xx on a retry freed a key whose earlier send may have committed; stored outcomes and raw-request digests outlived deletes and expiry; credential values entered the unwrapped digest.
- Round 2: 7 findings (5 dashboard, 2 backend), all fixed: pre-send records not marked uncertain, follow-up read 4xx, digest pinning, ledger expiry, refused-intent recall, expired-digest scrubbing, adoption of an older controller's orphan.
- Round 3: 3 low findings, all fixed: scrub versus a lagging clock, closure-assigned results, Save as new resuming a recorded key.
- Round 4: no findings from either reviewer.
- Round 5 (the e2e spec and these records): two low findings, fixed: a looser failure assertion in the spec and an incomplete list of remaining work.
- Round 6: both fixes confirmed; one wording fix to this record.

## Follow-up

The full `pkg/api` run during the [lifecycle property matrix slice](../roadmap-lifecycle-property-matrix-2026-10-21/README.md) found that this slice's receipt-completing script create stored its store call in a closure before passing it to `contentmutation.OutsideLock`. The store call still ran outside the runner lock, but `TestEveryLockHeldDurableWriteIsCounted` recognizes only an inline callback, so it failed. Each create call is now an inline callback again.

## Remaining

Queued revise-copy (`POST /tests`) intent keys, crash injection at each receipt/resource boundary, two-tab concurrent-click qualification, and browser coverage (both profiles) of the conflict, expiry and deletion paths that lead to Save as new remain open for AUTH-A; those paths are covered by unit and Go tests only. Credential fingerprints stay on the earlier digest where no key wrapper is configured.
