# Main integration — 2026-10-05 / resumed 2026-10-06

This integration preserves reviewed local commit `6bbdc9971d23bcd03e27ff6a54f30b5e0f191a43` and remote main `da1d6dd00ffdd250c630de7a73328a08040571a1`, which diverged by 17 local and 30 incoming commits. Work remains in the owned `codex/feature-completion-resume` worktree; the primary checkout and analytics-engine worktree are preserved. The unrelated untracked telemetry supplement is excluded.

Independent source reviews confirm the automatic merge retains authoring revision/publication guards, browser evidence and redaction, and incoming import, parameter, JSON-helper and pacing behavior. The only textual conflicts were the two Swagger artifacts, genuinely regenerated from the combined source alongside DSL, TypeScript and coverage contracts. A fresh resume audit verifies all reviewed runtime/UI inputs, generated conflict resolutions, 17 overlap/supporting inputs, and all 184 incoming-only Git blobs and modes. See [the resumed audit](evidence/apexload-main-resume-readonly-stage-preparation-20261006.json).

## Findings and corrections

The first merged-tree repository run found that browser request-finished callbacks could enqueue metadata that had not been committed when action finalization copied its evidence. Detailed/document evidence now waits for a bounded snapshot of already accepted work. Exact worker identity and generation fence the final detached snapshot; cancellation, timeout, drops and lifecycle changes produce advisory acquisition warnings. The ordinary browser path, target-action duration, resource limits and physical teardown remain unchanged.

The original dashboard cycle also exposed a Command Center resize test scrolling before ResizeObserver published the new sticky-header/shelf offsets. The test now waits for measured offsets before scrolling, retaining the original visibility threshold. Its two cases additionally run under genuine local authentication with a fixture-owned authenticated API context.

Focused execution found an invalid newly added clone-test fixture. Required version, generation-qualified identity, UTC timestamp and timing/state fields now satisfy the existing validator; explicit nonnil assertions preserve the detached mutation oracle. Production evidence validation was not relaxed. Independent repeat reviews of the runtime, composed UI changes and fixture correction are clean.

## Qualification

| Check | Result |
| --- | --- |
| DSL/API/TypeScript/coverage generation and M1 contract freshness | Passed against the combined source |
| Documentation checks and strict bundle budgets | Passed; original thresholds preserved |
| Full unfiltered Go repository repeat | 127 package passes / 29,489 test-subtest pass events; no failures |
| Focused metadata regressions | 24 top-level tests passed |
| Targeted race checks, two repetitions | 48 top-level tests and 4 subtests passed; no race findings |
| Actual Chromium acquisition/archive/API test, two repetitions | Passed with all 81 fixture requests retained in each run |
| Serialized affected UI cases | 2 passed in dev-admin and 2 passed in local-auth; no retries, skips or deadline changes |
| Full sequential dashboard repeat | Typecheck, lint, 531 unit-test files / 6,135 tests and production build passed; dev-admin 257 passed / 5 scoped skips; local-auth 199 passed / 1 scoped skip |
| Final rebuilt static assets and API docs | 7 root tests and 2 subtests passed; source and embedded output match the completed dashboard cycle |

The actual native test reconciles 81 JSONL and physical Parquet observations, 50+31 API pages, source timestamps, explicit measurable zero body sizes, service-worker false values, producer manifests and artifact hashes. Its unchanged fixture still requires more than 50 observations. See [the bounded native acceptance record](evidence/apexload-main-native-browser-api-acceptance-count2-20261006.json) and [the serial UI qualification](evidence/apexload-main-targeted-ui-serial-qualified-20261006.json).

The first complete dashboard cycle passed typecheck, lint, 531 unit-test files / 6,135 tests and the embed build. Dev-admin had 256 passes, five scoped skips and the original resize failure; local-auth had 197 passes and one private-fixture skip. All eight browser evidence workflows passed in each profile. The four restricted-persona skips in dev-admin passed under local-auth; the unavailable private LoadRunner archive remains unqualified in both profiles.

Later parallel targeted attempts failed on controller startup/API timeouts before proving any auth or visibility regression. Their completed receipts and raw failures remain intact. The unchanged cases passed when serialized; contention is an inference, not a claimed production defect. The interrupted first native attempt has an empty log and no completed receipt and remains unqualified. Subsequent successful checks retain separate labels.

The fresh full Go repository repeat passed and physically joined with unchanged inventoried source and embedded output. Its 28 test skips and seven no-test packages are enumerated in [the full Go qualification ledger](evidence/apexload-main-full-go2-qualification-outcome-20261006.json). One additional skip versus the first run is an unchanged macOS secure-save test whose filesystem precondition did not retain both special permission bits; this is unqualified filesystem coverage, not a passing save-behavior test. Required native browser checks passed.

The complete sequential dashboard repeat passed and physically joined with unchanged repository source, byte-for-byte identical to the source qualified by the full Go repeat. The four restricted-persona cases passed under local-auth; the private LoadRunner acceptance fixture remains unavailable in both profiles. See [the full UI qualification ledger](evidence/apexload-main-full-ui2-qualified-metadata-20261006.json).

The ordinary production rebuild updates Vite build-time metadata and hashed asset references, so its final embedded inventory differs from the prior full Go inventory. The original receipts retain both inventories honestly. The [build-delta proof](evidence/apexload-main-ui2-embedded-build-delta-proof-20261006.json) recovers prior payloads and verifies that reversing only the 32 renamed JavaScript asset references and three build-time literals reproduces all 33 changed payload hashes, including index.html; it does not claim the original and rebuilt bytes are equal. Seven existing static-serving/API-documentation Go tests and two subtests passed against exactly that final embedded inventory, with unchanged source/output and a physical process join. The readiness unit fixtures remain fixture coverage; the full browser cycle exercised actual controller-served assets. The staged audit precedes the merge commit and authorized main push.

Gate receipts retain exact repository-source/embedded inventories, commands, process joins and raw-output hashes. The first focused diagnostic gate records an explicitly reconciled temporary-helper hash change; all passing repeats use frozen V4. Native/provider/platform/scale and customer-fixture omissions keep their existing scope. Only the 10 GB benchmark was waived; no private original HAR is included or replayed and no broader completion milestone is promoted here.
