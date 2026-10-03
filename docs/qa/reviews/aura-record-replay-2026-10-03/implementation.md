# Recording/replay implementation slices — 2026-10-03

Requested scope: implement the evidence-sufficient roadmap items, commit each slice after tests and an independent review/fix cycle with no remaining actionable findings. No supplied target host or physical device is needed for local development. Actual Chrome/iPad and live Salesforce session qualification remain separate evidence requirements.

Planned slices, in delivery order:

1. Structured Aura envelope/outcome validation in HTTP replay, baseline outcome review and review UI.
2. Reusable local React/Aura fixtures for reordered batches, HTTP-200 errors, delayed UI results and client-state behavior.
3. Nested encoded Aura parameterization with precise bounded edits and substitution tests.
4. Correlation producer/consumer and incomplete-coverage review improvements.
5. Recording engine/device identity, explicit iPad emulation and record/replay mismatch review.
6. Parent-session concurrent HTTP request groups with consistent load/replay behavior.
7. Per-request HTTP version policy and transport isolation.
8. Shared bounded response decoding and compression handling.
9. Authoritative, bounded, redacted debugger pause-state snapshots.

Each slice records implementation, test evidence, independent findings and repairs, clean re-review, and its commit before the next slice begins. Customer payloads, hosts, cookies and credentials stay outside committed evidence. Existing safeguards and unresolved source gates remain explicit.

## Slice 1 — complete and independently reviewed

Implemented a shared value-free Aura envelope parser, strict `aura_actions_success` HTTP assertion, durable native-import review gates for invalid requests, baseline outcome evidence in HAR/archive/recording review, and a paginated source-linked outcome panel. Actual replay uses resolved outgoing wire bytes and preserves HTTP status when business actions fail. The editor admits the assertion only for HTTP and clears incompatible legacy fields when selecting it.

Independent review and fixes:

| Finding | Repair and regression evidence |
| --- | --- |
| Large runtime batches could silently lose diagnostic details through shared evidence bounds | Retain 64 outcomes and 64 issues, prioritize failure outcomes, preserve full validation/counts, and disclose omitted rows; actual 256-action replay tests cover late and all-error failures. |
| Aura requests nested inside control trees were omitted | Bounded traversal of every typed control branch and HTTP-sequence request, stable exact action paths, mutable rewrite pointers, malformed-request gate persistence and save/reopen tests. |
| An incidental REST `descriptor` field could trigger a false native Aura gate | Known REST/UI API routes take precedence; concrete Aura endpoint/envelope evidence is required. |
| Import review metadata could exceed disclosure and save limits | Global 2,048 outcome and 2,048 shared finding budgets, explicit per-batch/aggregate omissions and global failure priority; synthetic 400×256 case preserves 102,400 matches and keeps the script at 4,030,822 bytes, where an unbounded review produced 7,405,550 bytes and failed projection. |
| Encoded-body admission could gate a valid imported Aura request | Validate bounded decoded base64/Windows-1252 wire bytes while preserving source body and encoding; invalid wire bytes keep safe enum gates through reopen. Independent encoded-wire review and regressions passed. |
| Editor legacy assertion fields could block the selected strict assertion; recorder protocol could expose an HTTP-only option | Clear incompatible fields on selection and pass the actual effective protocol; stateful editor tests. |
| Real HAR upload failed before review because the project field followed file bytes | HAR and Salesforce wrappers send project scope as a leading multipart field and query parameter, retaining native/transform options; hook tests and actual import API browser flows. |
| Review could hide imported-request diagnostics or imply complete evidence from inconsistent wire counts | Separate request/capture diagnostics, defensive completeness checks, retained-detail disclosures and accessible remaining-detail controls. |

Two independent core/runtime re-reviews and the independent importer/API re-review reported zero remaining actionable findings after these repairs. Root independently reviewed the dashboard/client changes and visually inspected synthetic desktop and narrow screenshots. Encoded-wire admission also passed independent re-review with zero remaining actionable findings. Final full-suite gates passed before commit.

Private offline evidence: 409 inspected batches, 504 request actions, 503 structurally matched responses and 25 captured ERROR actions under HTTP 200. One Login response with duplicate nested fields remains incomplete under the strict parser. All 409 original HAR source positions are accounted for. No private target was contacted; these counts are capture inspection rather than live Salesforce/device qualification. See [capture-check.json](capture-check.json).

Verification: full Go suite passed with the finalized source (`GOMEMLIMIT=8GiB go test -p 2 -timeout 20m ./...`); race-enabled Aura parser/HTTP/engine tests passed; dashboard `npm run test:cycle` passed global typecheck, lint, 4,781 tests across 457 files, production build, 194 dev-admin workflows (five optional skips), and 115 local-auth workflows (one optional skip). The new Aura workflow executed in both profiles, including safe state display, filtered-HAR source navigation and narrow layout. Documentation checks passed for 103 active Markdown files; generated DSL/API and QA artifacts are fresh, and `git diff --check` is clean. Synthetic screenshots are saved in [screenshots/](screenshots/), with machine-readable results in [slice1-validation.json](slice1-validation.json).

Commit: the first implementation commit on `codex/aura-record-replay-fidelity`; Git history records its identity. The next slice begins only after this commit.
