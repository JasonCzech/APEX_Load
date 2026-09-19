# Bruno import design implementation and review

Date: 2026-09-19

Implemented the recommendations in [the design review](bruno-import-design-review-2026-09-19.md) across source setup, Summary, APIs, Credentials, Data, and Review & save.

## Delivered behavior

- Source setup collapses after inspection, with a compact source/mode/destination summary and a reset warning. Canonical execution controls use progressive disclosure.
- A linked readiness summary separates prerequisites for saving drafts from requirements before replay. Native conversion fidelity is distinct from source-bundle preservation.
- APIs support search, readiness filtering, Bruno sequence sorting, persistent collection-wide selection, request method/target, provenance, and one details entry point with repair navigation.
- Credentials reuse the existing grouped, redacted binding controls and readable provider guidance. Replay counts reflect valid configured destinations and pending secret creation, including validation conflicts.
- Data separates required runtime inputs from optional substitutions. Search, unresolved filtering, affected API links, selected-request scope, and immediate pending-value readiness updates replace duplicate lists.
- Review & save groups conversion findings, links to affected source and repairs, shows the final save/replay checklist, and uses a stable draft-save action. Acknowledgement does not remove a replay gate.
- Form sizing, wrapping, keyboard focus, tab navigation, and narrow layouts were checked. Wide API tables scroll inside a labeled, keyboard-focusable region.

## Code review findings fixed

1. Partial imports invalidated location-derived shared-value IDs when the backend re-detected candidates against only selected scripts. Commit now derives trusted candidates from the immutable, masked inspection and rebases server-owned locations onto the selected batch. Client-provided locations remain untrusted. Tests cover selected subsets, deselected no-ops, unknown-ID rejection, immutable source descriptors, atomic saving, and retries.
2. Credential readiness could remain stale after a valid destination was configured. Shared review state now exposes execution readiness separately from destination validity, and invalid/conflicting secret configurations do not clear missing-secret counts.
3. API details could show an input as missing after it was supplied in Data. Details now use the same pending-value calculation as the readiness summary. Original diagnostics are explicitly labeled as inspection evidence.
4. Narrow API columns compressed long targets and status text. Fixed column sizing and contained horizontal scrolling preserve readable content without page overflow.
5. The new local-auth browser test needed to refresh workspace hierarchy after API fixture creation. It now follows the existing authenticated workflow fixture pattern.

## Acceptance archive

Used `CTA_Collection 6a.zip` locally, without executing hooks or making target requests. Inspection retained 41 HTTP APIs and 41 native candidates. The initial review exposes 5 missing secret bindings, 16 unresolved runtime inputs, and 2 conversion-review gates. Entering a synthetic pending `env` value reduces the input count to 15 and removes it from affected API missing-variable lists. No actual archive drafts or credentials were saved during manual validation.

`$isoTimestamp` remains an explicit migration requirement with source navigation and a saved-draft repair fallback. Bruno documents ISO output with milliseconds; the existing APEX timestamp shortcut uses second precision. An unverified substitution would not establish parity. See [Bruno dynamic variables](https://raw.githubusercontent.com/usebruno/bruno-docs/main/v2/testing/script/dynamic-variables.mdx).

## Validation

- Full dashboard cycle: typecheck, lint, all 4,322 unit tests, and embedded build passed. Browser profiles completed: dev-admin 112 passed / 8 failed / 4 skipped; local-auth 38 passed / 1 failed / 1 skipped.
- The nine broader browser failures are outside Bruno: command-center details, initial dev-admin workspace, live metrics, two run lifecycle workflows, workspace creation, authoring scope/history, guided setup scope, and the general local-auth launch workflow. They match the previously recorded suite failures; the full cycle is not green.
- Focused final component tests: 140 passed across Bruno import, Data review, readiness helpers, and shared credentials.
- Bruno inspect/edit/acknowledge/atomic-save browser workflow passed independently in both dev-admin and local-auth profiles using disposable fixtures.
- Targeted backend selection/shared-value regression tests passed.
- Manual browser validation used the rebuilt embedded frontend, the actual archive, all review panels, linked repair navigation, search/selection, live input counts, and 1280px/768px layouts. Neither viewport produced page-level horizontal overflow. Browser viewport overrides were reset.
- Full `go test ./...` passed after the backend fix (exit 0).

## Evidence and rollout

Local visual evidence:

- [API layout at 1280px](/Users/jasonczech/.codex/visualizations/2026/09/19/01a0b9de-7e8c-7221-a5ac-e2ae5f564188/bruno-import-audit/implementation/apis-1280.png)
- [Review layout at 768px](/Users/jasonczech/.codex/visualizations/2026/09/19/01a0b9de-7e8c-7221-a5ac-e2ae5f564188/bruno-import-audit/implementation/review-768.png)
- [Data layout at 1280px](/Users/jasonczech/.codex/visualizations/2026/09/19/01a0b9de-7e8c-7221-a5ac-e2ae5f564188/bruno-import-audit/implementation/data-1280.png)

The embedded dashboard assets were rebuilt. The existing running controller was not restarted as part of this implementation. Live credentialed replay and Bruno baseline comparison remain separate validation steps.
