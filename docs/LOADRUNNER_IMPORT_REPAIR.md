# LoadRunner import fidelity and guided repair

Re-import affected archives to obtain corrected semantics. Existing saved scripts are never rewritten.

The HTTP importer preserves ordered ITEMDATA fields and treats EXTRARES as its terminator. Explicit resources live on the originating request in `params.resources`; the bounded resource fetcher executes them for POST and JSON responses as well as HTML pages. Resource work contributes to the parent request's timing. The shared cookie jar, cache and download filters remain in effect.

`source_verification` registers, pauses or resumes a check at its original position. State belongs to the VUser, and a registration in actions does not affect earlier init requests. Its ID, case flag, failure condition and search scope are preserved. Existing explicitly authored script-wide checks continue to apply globally. Unsupported verification forms remain execution-gated.

The active user-agent selection respects `UseCustomAgent`. Navigator language metadata does not supply an HTTP Accept-Language header. Conflicting active declarations still require review. Transaction-order declarations accept `__*delimiter*__` without splitting punctuation inside transaction names.

## Unique allocation

`parameters[].auto_allocate` records LR automatic allocation. Allocation compiles before execution using the planned VUser population and usable rows after StartRow. Each VUser receives a contiguous block identified by a dense ordinal independently of distributed runtime IDs.

- A duration run divides usable rows by planned VUsers using integer division; remainder rows are unused.
- An iteration run uses the effective iteration requirement per VUser.
- Once reserves one value per VUser. An explicit block size is authoritative.
- Exhaustion stays within the VUser's block. ContinueWithLast cannot lend another VUser's final row to an unallocated VUser.
- Insufficient allocation capacity fails before execution. Automatic each-occurrence allocation requires an explicit block size.
- Blocks remain frozen across planned ramps and shards. Groups executing the same script reserve disjoint ordinals from their combined planned population. Conflicting parameter contracts or iteration requirements for that shared script are rejected. Population changes and generator-loss resharding are rejected when they would lose allocation state. Open workloads, stage-driven rescaling, parallel each-occurrence cursors, and shared at-once init are not accepted for these allocations. Fixed-population ramp-up/ramp-down uses the reserved identities.

Generators advertise `lr_fidelity_v1`; assignments carry population and ordinal offset, and their retry fingerprint includes both. Older generators cannot be selected silently for the new semantics.

Allocation rules follow [OpenText's allocation examples](https://admhelp.microfocus.com/vugen/en/26.1/help/WebHelp/Content/VuGen/UI/116150_ui_parameter_simulation_db.htm). Verification scope follows [OpenText's global verification reference](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/web/lrFr_web_global_verification.htm).

## Protected repair

The existing archive review screen offers source-linked repair and allocation previews. Repair requires encrypted candidate storage. The preview/apply endpoints are `/api/v1/scripts/import/repairs/preview` and `/api/v1/scripts/import/repairs/apply`; see the generated API contract for typed fields.

Requests identify the project, protected candidate, opaque revision, finding and action path. Operations restore a body, replace an encrypted proxy-password secret binding, bind an existing producer, add a parameter, add an extractor on a preceding response, select supported TLS settings, or preview allocation. Preview returns a redacted before/after draft and remaining requirements. Apply rotates the candidate and preserves its original expiry. Encrypted receipts make retries of the same operation stable and fence the replaced revision. Project/actor authorization and existing protected save paths remain authoritative.

Restored request bodies remain encrypted in candidate storage and are fully redacted in repair previews. There is no native LR ciphertext decoder. Ordinary LR masking and stronger encryption have distinct diagnostics. An encrypted proxy password requires a replacement secret, and that secret must be configured before replay. SSL_VERSION=2&3 stays blocked until an operator chooses supported TLS behavior; the choice is recorded as an intentional migration, not exact parity.

Dependency analysis examines executable request fields and nested requests. Definite missing producers block execution; conditional or later producers require review. Commented source is not reactivated, and TSTAMP receives no invented meaning. Every repair refreshes the shared findings and fidelity summary. Acknowledgement cannot clear an execution blocker.

The private acceptance archive is not committed. Regression tests use synthetic values and local HTTP servers. Live Salesforce replay and comparison with an LR baseline remain separate validation requiring usable credentials and target access.

## Local acceptance evidence

The supplied private archive was re-imported locally on 2026-09-19. No target traffic was sent.

| Check | Result |
| --- | --- |
| Primary HTTP requests | 492 retained |
| Transaction boundaries | 25 starts and 25 ends |
| Subtransaction boundaries | 445 starts and 445 ends |
| Correlation extractors | 58 retained |
| Largest request body | 113,839 bytes retained |
| Explicit resource parent requests | 4 |
| False ITEMDATA/EXTRARES blockers | 0 |

Three bodies still need restoration. The encrypted proxy credential, legacy SSL selection, 12 missing producer names (including TSTAMP), and one conditional/later producer remain requirements. The parameter-conversion producer's lowercase alias is recognized. Commented correlations remain disabled. These results establish import structure and local replay semantics, not Salesforce/LR baseline parity.

## Validation results

- `go test -p 2 ./...` passed for the final implementation. Package concurrency was limited after earlier concurrent builds exhausted temporary disk space; the regenerable Go build cache was cleared before the final runs.
- `npm run test:cycle` completed type checking, lint, all 399 unit test files (4,301 tests), and the embedded production build successfully.
- All seven LoadRunner browser workflows passed in both dev-admin and local-auth profiles, including guided repair preview/apply.
- The complete browser profiles are not green: dev-admin finished with 111 passing, nine failing and three skipped tests; local-auth finished with 38 passing and one failing test. Eight dev-admin failures and the local-auth failure assert existing workspace selectors or run controls that have been renamed or moved. The additional source-pane width failure passed in an isolated rerun. No unrelated navigation behavior was changed for this work.
- The required opt-in-suite documentation inventory omissions were corrected. DSL, API, protobuf and QA artifacts were regenerated.

The browser failures are in `command-center-details.spec.ts`, `tier0-auth.spec.ts`, `tier0-live-metrics.spec.ts`, two tests in `tier0-run-lifecycle.spec.ts`, `tier0-workspace.spec.ts`, `workflow-authoring.spec.ts`, `workflow-usability.spec.ts`, and `workflow-local-auth.spec.ts`; the transient width failure was in `workflow-script-sidebars.spec.ts`.

## Post-implementation code review

The follow-up review corrected five issues and added regression tests:

- Dependency deduplication now distinguishes `var` and `param`, so a conditional variable producer cannot hide a missing parameter blocker with the same name.
- Producer-binding repairs accept whitespace in template expressions and preserve the `var`/`param` namespace. Source evidence remains unchanged.
- Unknown global-verification failure modes remain execution blockers instead of silently using `Fail=Found`.
- Explicit resources take precedence over automatically discovered duplicates, preserving the authored referer and reserving their bounded fetch slots.
- Positional-verification template values participate in dependency analysis; missing inputs block execution.

The focused regression tests and final `go test -p 2 ./...` run passed after these corrections. The private-archive structural acceptance probe retained all counts above and the same explicit unresolved requirements. `git diff --check` passed. No dashboard code changed during the follow-up review; the earlier browser-profile limitations still apply.
