# Contextual validation revisions — 2026-10-21

This [AUTH-B slice](../../../ROADMAP_IMPLEMENTATION_SPEC.md#auth-b--contextual-validation-revisions) binds a script validation result to the controller context it depended on, beyond its document fingerprint and baseline revision. The contract is documented in [script lifecycle](../../../SCRIPT_LIFECYCLE.md#validation-context).

## What changed

- **Validation context.** Both script validation endpoints return `validation_context`: an opaque revision and four keyed digests. Capabilities covers the authoring catalog and the browser runtime the script needs. Policy covers the domain feature policy, the project's authoring settings and features, and the system script-import policy. Approvals covers whether a Java Vuser approval is needed and still valid. Secret references covers each referenced secret (sorted, bounded at 256) with its availability and a keyed value digest. Contextual findings (`secret_unavailable`, `approval_required`, `approval_stale`, `capability_unavailable`) stay separate from static issues and from execution admission.
- **Failures are not changes.** A failed read of the project, a Java approval session, or the project or application secret stores and their scoping reads is an error, never a different context. A secret the resolver missed while a managed store holds it counts as such a failure. A Key Vault fallback reports no read errors, so its outage still reads as an unavailable secret (documented).
- **Probe endpoint.** `POST /api/v1/scripts/validation-context` returns the current context for a draft or a stored script (the latter at the validated revision, else 409), with the same project authorization as validation.
- **Editor.** The script editor re-reads the context for the result it holds on focus, on visibility and every 60 seconds while visible, one probe at a time, at most one every 5 seconds, each bounded at 30 seconds. A different revision makes the result stale like an edit, and the readiness card names what changed (for example "project secrets"). A controller that reports no context keeps document-only binding.

## Validation

| Gate | Result |
| --- | --- |
| API: secret availability and rebinding, project policy, unrelated settings, Java approval finding, probe/validation agreement, ambiguous probes, stored revision 409, read failures, inconsistent secret reads, trusted-header authorization | Passed, with the route, access-requirement golden, catalog, OpenAPI, generated-contract, validation, script and Java suites |
| Dashboard typecheck and eslint on changed files; session helpers, session hook (probe pacing, late answers, document-only binding), ScriptEditor (stale notice) | 276 tests in 4 files passed |
| Documentation checks | Passed |

Browser workflows were not rerun: the editor change is covered by component tests, and no Playwright spec exercises the probe.

## Review

One reviewer read scratch snapshots only:

- Round 1: six findings, all fixed. Two were medium: read failures were reported as context changes, and the policy digest covered every project setting. The four low ones: duplicate probes, contradictory docs, a stored probe reading a newer revision, and untested authorization branches.
- Round 2: two low findings, fixed: secret failures outside the two managed stores, and a stalled probe blocking all later ones.
- Round 3: one low finding, fixed: a draft without a project ID lost its context because the recheck and the resolver used different project IDs.
- Round 4: no findings.

## Follow-up

The [cross-cutting AUTH review](../roadmap-auth-cross-cutting-2026-10-21/README.md) replaced the secret-reference value digest above with a value-free binding (where the script's secrets resolve, and each name's availability), made the secret re-check share the resolver's application rule, bound drafts to their authorized project, answers 404 for another project's script, and made the editor treat a stored-revision 409 as stale and back off failed probes.

## Remaining

Policy revoke, capability change, secret rebinding and approval expiry are qualified at the API and component levels. Both-profile browser coverage of the stale-context notice, and a scenario-level validation context, remain open.
