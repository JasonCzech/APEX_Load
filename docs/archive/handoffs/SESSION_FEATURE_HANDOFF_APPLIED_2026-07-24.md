# Applied Changes Report — 2026-07-24

Status: Historical (archived 2026-09-24). Point-in-time record of applied working-tree changes; not current documentation.

Applied by: Claude Code · Parent baseline commit: `7bb80c469` (branch `main`)

Two bodies of work were landed: the **LOCAL_CHANGES patch** (87 sections) and the **4 documented
enterprise-import / Java-Vuser features** from the session handoff.

Nothing was committed — all changes are working-tree edits for review in GitHub Desktop. No
`git stash` / `checkout` / `reset` / `pull` was run at any point.

---

## 1. Patch repair (LOCAL_CHANGES_2026-07-24.patch)

The delivered patch was unusable as shipped and required two deterministic repairs:

| Defect | Detail | Repair |
|---|---|---|
| Double-spacing | A blank line after every line (12,803 lines → 6,402 real). Verified strictly alternating: all 6,401 blanks on even lines, **zero** legitimate blank lines lost. | Drop even-numbered lines |
| Mojibake | 9 lines with UTF-8 punctuation misdecoded as CP1252 | `┬╖`→`·`, `ΓÇô`→`–`, `ΓÇö`→`—`, `ΓÇª`→`…`, `ΓÇó`→`•`, `Γ£ò`→`✕` |

Repaired patch: 0 residual mojibake; **86 of 87 sections applied cleanly**.
(An earlier delivery of the same file had all newlines stripped and was unrecoverable.)

## 2. What landed

**112 files modified, 18 new files, +3232 / −184.**

New files include `.env.example`, `dashboard/.npmrc`, `scripts/load-env.ps1`, `pkg/config/dotenv.go`,
`cmd/controller/main_tls_compat_test.go`, plus the pre-existing untracked Java-classpath work.

Themes: enterprise/corporate deployment support (dotenv loading, TLS compatibility defaults, proxy /
npm registry config, CI workflow), secret-binding and correlation improvements, topbar pending/queued
run surfacing, chart granularity persistence, and the enterprise-import feature set below.

## 3. Deviations from a verbatim patch apply — and why

### 3a. `EnterpriseArchiveImport.tsx` — SKIPPED (user decision)
The patch's version of this file was **corrupt**, not a refactor:
- It contained literal unresolved conflict markers (`<<<<<<< Updated upstream` / `>>>>>>> Stashed changes`) — a syntax error inside JSX.
- It deleted the secret-binding review (`secretBindingRequirements` 12→0, `SecretBindingReview` 2→0), the combined-Java-Vuser governance gating (`combinedJavaVuserGate` 10→0, `importedEditorAvailable` 40→0), `fidelityReport`, parameterization review, and the request-token race guards.
- Removing `fidelityReport` would have broken the Required Files feature, which derives from it.

The file was left as-is (current component + Java-classpath work intact) and the Required Files
feature was added on top.

### 3b. Conflict markers in 3 other files — resolved to HEAD
The same bad stash pop injected conflict markers into `dashboard/src/App.tsx`, `dashboard/src/App.css`,
and `dashboard/src/components/Pages.css`. In every case "Updated upstream" == HEAD and was the *newer*
code (the global-socket health comment, `color-mix` fallbacks); "Stashed changes" was stale. All four
conflicts were resolved to the HEAD side, leaving those three files byte-identical to HEAD.

### 3c. Restored capability the patch reverted (user decision)
The patch removed production code whose tests were not included, breaking 3 test packages. All were
restored while keeping the patch's enterprise additions (`loadLocalDotEnv`, `applyTLSCompatibilityDefaults`):

- `cmd/controller/main.go` — OpenTelemetry tracing, `brunoArtifactDirFromEnv`, `activeEndpointPublisherFromEnv` (HA ExecutionEndpointPublisher), `waitForControllerListener` + `controllerListenerState`, and the `BrunoArtifactDir`/`ActiveEndpointPublisher`/`TracingStatus` server config.
- `cmd/generator/main.go` — OpenTelemetry tracing and `configureBrunoResolver`.
- `pkg/distributed/generatorrpc/archive.go` — `maxGeneratorArchiveJSONLRecordBytes` and the bounded `bufio.Scanner` JSONL read (the patch had replaced it with an unbounded `json.Decoder`, dropping an oversized-record guard).

### 3d. Incomplete-patch fixes made independently
- `pkg/api/handlers_scripts.go` — added `SupplementalData`/`SupplementalName` to `enterpriseArchiveImportRequest`; the patch used these fields but omitted the struct change (build break).
- `pruneUnusedWholeBodyExtractors` — the patch shipped a test expecting `[]string` while the source returned `int`. Enriched the return to `[]string` (removed extractor names) and updated both call sites (`converter.go`, `zip.go`) and 5 existing tests.
- `dashboard/src/components/ApiGuidePage.tsx` — reverted to HEAD. The patch's version was a pure revert that removed the User/Admin Guide rendering **and the admin-guide direct-navigation access guard**, with no enterprise-related addition; its test file was not included and 5 tests failed.
- `dashboard/src/appTopbar.test.ts` — the patch added pending/queued runs to the topbar and updated one test but not its sibling; updated the stale assertion to match the feature intent (plus an ABORTED-clears case).
- `dashboard/src/components/TestExecutionCharts.interaction.test.tsx` — the patch's new test never opened the "Configure Graphs" drawer that contains the granularity control; fixed the test to open it.
- `dashboard/src/components/EnterpriseArchiveImport.test.tsx` — the patch asserted the *old* secret-binding copy; updated to the current archive-wide wording.

## 4. The 4 handoff features

| # | Feature | Status |
|---|---|---|
| F1 | Unparsed-source fidelity blockers scoped to the selected Java runner hierarchy; analysis-limit placeholders stay blocking (fail-closed) | **Implemented** — new `javaVuserRelevantUnparsedRegions` in `pkg/convert/enterprisearchive/java_import.go` |
| F2 | Combined Java Vuser repair guidance: named blockers → blocker-specific; none → "Review combined Java Vuser draft readiness" | Already in the patch; **test added** |
| F3 | Required Files section in import review (parse blockers → candidates, missing/attached status, attach/remove/clear **per queued item**) | **Implemented** in `EnterpriseArchiveImport.tsx` + `.css` |
| F4 | Repeatable multipart `supplemental_file`; backend validates safe relative paths and merges into the ZIP in-memory before convert/validate | Backend in the patch (+ struct fix); **frontend `supplemental_files` added** to `useScriptApi.ts` |

Out of scope by earlier decision (bundled in the handoff diff but not ported): Salesforce
native-preview rework, credential-redaction editor-gating removal, the Salesforce `cshc` converter
test, and ZIP magic-byte validation.

### Tests added
- `TestConvertJavaIgnoresUnparsedRegionsOutsideSelectedRunnerHierarchy`
- `TestConvertJavaKeepsAnalysisLimitPlaceholdersBlockingOutsideHierarchy` (fail-closed guard)
- `TestCombinedJavaVuserWithoutNamedFidelityBlockersUsesReviewReadinessGuidance`
- `TestHandleValidateEnterpriseArchiveImportMergesSupplementalFiles` (from patch — verified passing)
- `EnterpriseArchiveImport.test.tsx` → "lets users attach required files from fidelity blockers and sends them on re-validation"

## 5. Verification results

| Check | Result |
|---|---|
| `go build ./...` | **PASS** |
| `go vet ./...` | **PASS** (all packages) |
| `go test ./pkg/... ./cmd/...` | **PASS — exit 0** (was exit 1 with 8 failures; see CORRECTION and REVIEW FIXES below) |
| `gofmt -l ./pkg ./cmd` | **PASS** (0 offenders) |
| `go test ./pkg/convert/enterprisearchive -run Java` | **PASS** |
| `go test ./pkg/api -run 'JavaVuser\|...MergesSupplementalFiles'` | **PASS** |
| `tsc --pretty false` | **PASS** (no output) |
| `vitest run --no-file-parallelism` | **PASS — 257 files, 2443 tests, 0 failures** |
| `vite build --outDir ../pkg/api/static` | **PASS** (107 assets emitted) |
| `go build -o ./bin/controller ./cmd/controller` | **PASS** (140 MB) |
| Runtime `GET /api/v1/system/version` | **200** |
| Runtime `GET /` | **200** |
| Browser: Required Files panel | **Renders** with correct copy, CSS, and `accept=".java,.zip,.jar,.json,.txt" multiple` input |

### CORRECTION — the Go suite does NOT pass

An earlier revision of this document reported `go test ./pkg/... ./cmd/...` as passing. **That was wrong.**
The command had been run as `go test ... | grep -Ev '^ok ' | head -60`, so the observed exit status was
`head`'s (always 0), not `go test`'s. Re-run without the pipe, the suite exits **1** with **8 failing tests**:

| Package | Failing test | Root cause (all from the external patch) |
|---|---|---|
| `pkg/api` | `TestReconcileMaskedScriptSecretsRejectsPromotedOrLegacyExecutableEvidence` (3 subtests) | `secret_bindings.go` now excludes ALL action `metadata` from redaction reconciliation |
| `pkg/api` | `TestHandleConvertBrowserRecordingStoresOnlyGovernedSecretReferences` | same metadata exclusion |
| `pkg/api` | `TestDraftValidationDataSourceFitUsesAutoDelimiter` | new `dsl/validate.go` check ignores `auto`/`dat` delimiter rules |
| `pkg/api` | `TestDraftValidationReturnsDataSourceFitHealth` | same |
| `pkg/api` | `TestTrustedHeaderAuthReadsEnterpriseArchiveMultipartProjectID` | new ZIP magic pre-check rejects the 3-byte body before auth is characterized |
| `pkg/convert/correlate` | `TestSalesforceFormExtractorUsesFormFieldKey` | new `salesforce.cshc` rule unreachable for form bodies |
| `pkg/convert/correlate` | `TestDetectSalesforceLoginTokensFromDecodedForms` | same |
| `pkg/engine` | `TestExecuteFailsClosedBeforeSendingUnresolvedParameter` | duplicate unresolved-`{{param.*}}` gate changes the error message |

None of these originate in the hand-written work (§3c/§3d) — they are all pre-existing defects **inside the
applied patch**, now surfaced. `go build ./...` and `go vet ./...` genuinely pass, and the frontend result
below is genuine (vitest prints its own pass/fail summary rather than relying on an exit code).

Notes on the test runs:
- Vitest **must** be run with `--no-file-parallelism` on this machine. Parallel runs produce
  `[vitest-pool]: Failed to start forks worker` / `Timeout waiting for worker to respond` — worker-pool
  contention, not assertion failures. The serialized run is clean at 2443/2443.
- One benign unhandled rejection remains (`Request timed out after 50ms` from `apiClient.ts`), from a
  deliberate 50 ms timeout test whose rejection isn't awaited. No test fails because of it.

## 6. Follow-ups

1. Review §3a — the Required Files UI is built on the current component; if the patch's simplified
   component was actually wanted, that is a separate, deliberate decision.
2. `DEVELOPMENT.md` contains 39 pre-existing mojibake sequences (present in HEAD, untouched here).
3. Consider whether the topbar change (PENDING/QUEUED/RESERVED/SCHEDULED treated as active runs) is
   the intended product behavior — it is now covered by tests either way.

---

# REVIEW FIXES — applied after the multi-agent code review

A 17-agent adversarial review (8 dimensions, each verified by an independent
refuter, plus a completeness critic) produced 50 confirmed findings. The fixes below
were applied; the full Go suite now exits 0 and the frontend suite is 2444/2444.

## Fail-open promotion gate (my own F1 work — 2 HIGH)
`pkg/convert/enterprisearchive/java_import.go`

1. **Analysis-limit carve-out missed 7 truncation reasons.** The predicate required
   the words "analysis" AND "limit", but the *aggregate* budget reasons
   ("Java sources exceed aggregate retained-type limit", …) contain no "analysis".
   Those are the most global signals — when one trips, every remaining `.java` file
   is skipped entirely. Replaced with `javaVuserUnparsedRegionIsGlobalTruncation`,
   which also treats any "aggregate" reason as permanently blocking.
2. **Hierarchy scoping dropped external lifecycle sources.** `classHierarchy()` walks
   superclasses only, but the runner also executes `vuser_init.java` / `vuser_end.java`
   and any `.usr`-declared lifecycle source. An unparsed `vuser_init.java` promoted
   clean, so every VUser would run `action()` with login/session setup missing. Added
   `javaVuserRunnerSourceFiles` (hierarchy + lifecycle sources, via the existing
   `javaExternalLifecycleFileMatches`) and `javaVuserSourceIsReferencedByRunner`, so a
   helper is scoped away only when the runner never names it and its type surface is
   known. Everything else fails closed.

## Secret handling (4 HIGH)
- **`pkg/api/runner.go` + `secret_bindings.go`** — the dev bypass keyed on
  `APEXLOAD_ENV` in (dev|development|local), and `compose.yaml` ships
  `APEXLOAD_ENV: local`, so the shipped local stack sent the constant
  `"apexload-dev-secret"` to real targets while reporting every secret as configured.
  Now requires explicit `APEXLOAD_DEV_FAKE_SECRETS=true`, is refused in
  production/prod, consults the real store first, and logs each substitution.
- **`handlers_project_secrets.go`** — stopped reporting unconfigured secrets as
  configured; real status is returned with a `placeholder_substitution` flag.
- **`secret_bindings.go`** — restored review-action-scoped metadata handling, so a
  masked read-modify-write can no longer overwrite a stored credential with
  `[REDACTED]` and the promoted-evidence guard fires again.
- **`pkg/engine/secret_references.go`** — action metadata is back in the runtime
  redaction set (kafka/JMS copy it onto the wire); only the specific provenance keys
  `original_url` / `source_transaction` are exempt. `original_*` is deliberately NOT a
  prefix rule: `original_request` holds raw review evidence.
- **`script_projection.go`** — an unmodified projection now restores the stored value
  even without a `[REDACTED]` sentinel, fixing permanent data loss for
  `<messageId>` (masked to `{{fn.uuid()}}`, which is sentinel-free).

## Supplemental ZIP merge (1 HIGH + 1 MEDIUM)
`pkg/api/handlers_import_enterprise.go`
- The merge re-inflated every entry with an unbounded `io.ReadAll` **before**
  `ConvertZip`, so attaching one supplemental file bypassed the 64 MiB/512 MiB/4096
  bomb caps (~100 MiB body → ~100 GiB). Caps are now exported from
  `enterprisearchive` (`MaxArchiveEntryBytes`/`MaxArchiveTotalBytes`/`MaxArchiveEntries`)
  and enforced in the merge, with per-file and count limits on the uploads themselves.
- Supplemental files now replace the matching **nested** entry by base name (browsers
  only send base names), instead of being appended at the root where `ConvertZip`
  aborted on a duplicate normalized entry.

## Correctness / regressions from the patch
- `pkg/dsl/validate.go` — delimiter resolution moved into `dsl` as the single source
  of truth (`ResolveDataSourceDelimiter`, honoring `auto` and the `dat` tab default);
  `pkg/engine/data.go` delegates to it. A missing parameter column is again a
  data-source **fit** report rather than a structural error that blocked execution
  staging for already-stored scripts.
- `pkg/engine/secrets_gate.go` — removed the duplicate unresolved-`{{param.*}}` gate;
  the existing broader gate in `vuser_execute.go` now names the offending reference.
- `pkg/convert/correlate/correlate.go` — added `cshc` to `correlationFormKey` so the
  new Salesforce rule is reachable for form/query bodies.
- `pkg/config/dotenv.go` — fixed a startup panic on a value that is a single quote
  (`value[1:0]`); added tests, including one pinning that `.env` never overrides the
  process environment.
- `dashboard/src/components/useExecutionMetricsStream.ts` — restored a bound on live
  history (deleted outright, its guarding test reworded to bless the removal). Now
  `EXECUTION_STREAM_HISTORY_LIMIT = 5000` (~83 min at 1 pt/s), keeping the author's
  intent of retaining more than the old 300 while staying bounded.
- `dashboard/src/hooks/apiClient.ts` — a caller/unmount abort is no longer misreported
  as a timeout (and the `DOMException` is preserved); the timeout test no longer leaks
  an unhandled rejection into the whole suite.
- `.gitignore` — `.npmrc` ignored. `dashboard/.npmrc` pins an internal Artifactory
  mirror unreachable from GitHub runners; committing it would break `npm ci` in
  ci.yml and release.yml. It stays local.
- `gofmt -w` across the previously unformatted files (CI gates on `gofmt -l`).

## Tests added
`TestConvertJavaKeepsAggregateTruncationBlockingOutsideHierarchy` (7 reason strings),
`TestConvertJavaKeepsUnparsedExternalLifecycleSourceBlocking`,
`TestConvertJavaKeepsUnparsedHelperReferencedByRunnerBlocking`,
`TestConvertJavaBlocksPromotionWhenLifecycleSourceFailsToParse`,
`TestJavaSourceMentionsTypeMatchesWholeIdentifiersOnly`,
`TestSecretBindingEnforcementRequiresExplicitDevOptIn` (9 cases),
`TestReferencedSecretNamesKeepsNonProvenanceActionMetadataSecrets`,
`TestReconcileRestoresMessageIDWhenProjectionIsUnmodified`,
`TestReconcileKeepsClientEditsOverStoredValue`,
`TestMergeEnterpriseArchiveZipRejectsOversizedEntry`,
`TestMergeEnterpriseArchiveZipReplacesNestedEntryByBaseName`,
`TestLoadDotEnvHandlesDegenerateQuotesWithoutPanicking` (5 cases),
`TestLoadDotEnvDoesNotOverrideExistingEnvironment`,
`caps retained execution history at EXECUTION_STREAM_HISTORY_LIMIT`.

Two patch-authored tests encoded the regressions they shipped with and were converted
into explicit pins of the correct contract:
`TestValidateScriptIssues_UnresolvableParameterColumnIsFitNotStructural` and the
`appTopbar` queued-run assertion.

## Not fixed (deliberate — needs a product decision)
- `applyTLSCompatibilityDefaults` still relaxes X.509 (`x509negativeserial=1`,
  ML-KEM off) whenever `APEXLOAD_ENV` is not exactly production/prod, i.e. by default.
- `.env.example` / `scripts/load-env.ps1` / `.github/workflows/desktop.yml` still
  carry internal AT&T hostnames.
- Classpath-asset findings (storage_ref not project-fenced; materialized JAR trusted
  by filename), the Command Center run-scoping bug, `README.md`'s `APEXLOAD_AUTH_MODE`
  (an env var that does not exist), and the archive masked-body redaction rewrite.

---

# REVIEW ROUND 2 — fixes applied

A second 17-agent adversarial review targeted the round-1 fixes themselves. It confirmed
**33 findings (1 high, 10 medium, 22 low)** — 8 of them regressions of a round-1 fix.
(The workflow's own aggregation initially reported only 16: it matched verifier verdicts to
findings by exact title and the verifiers rewrote titles, so 17 were silently dropped. The
full set was recovered from the run journal.)

## HIGH — public CI break
`.github/workflows/desktop.yml` — the new "Build Dashboard" step set
`NPM_CONFIG_REGISTRY: ${{ vars.NPM_CONFIG_REGISTRY || '<internal Artifactory> }}`. The repo has
**zero** Actions variables, so the internal fallback was always taken, on all five matrix legs,
shadowing the workflow-level default (`vars.NPM_REGISTRY_URL || registry.npmjs.org`) — and it
used bare `npm install` rather than the repo-standard `npm ci --ignore-scripts`. Removed the
step-level override (the safe workflow default is inherited) and switched to `npm ci --ignore-scripts`.

## Round-1 fixes that did not hold

- **messageId data loss was NOT actually fixed.** `reconcileMaskedScriptSecrets` returns early
  when the payload carries no `[REDACTED]` sentinel — and the messageId case is sentinel-free by
  construction — so the restore branch was unreachable on the real path. Reconciliation is now
  driven by "a prior version exists" instead. Verified by temporarily reverting the gate: the new
  test fails with the placeholder persisted over the recorded id, and passes with the fix.
- **F1 scoping still failed open for `implements` and header-only references.** `class.Source`
  holds the class BODY, so `extends`/`implements`/`import` text was never searched. Added
  `javaClassReferenceText` (body + supertypes + imports) and closed `javaVuserRunnerSourceFiles`
  over interfaces as well as superclasses. Verified by reverting both parts: an unparsed
  implemented interface promoted clean before, blocks now.
- **The engine-side provenance exemption was unsafe.** `source_transaction` is not provenance-only:
  `TestJMSPublishWithMetadataAndNoPropertiesConfig` asserts it is delivered as a JMS property.
  Reverted the engine exemption entirely — every action-metadata secret is back in the runtime
  redaction set and the fail-closed preflight. Suppressing the "configure this secret" *prompt* for
  provenance keys is a UI concern and stays in `pkg/api` only. Two tests that encoded the unsafe
  assumption (one from the patch, one of mine) were rewritten to the safe contract.
- **ZIP merge rewrote entry names.** Writing `path.Clean(name)` shifted which leading segment
  `ConvertZip` strips, so attaching a supplemental file could break a `./`-prefixed archive that
  imports fine without one. Entries now keep their original names; the cleaned form is a match key only.
- **Case-colliding supplemental files** resolved to the same entry with the winner chosen by map
  iteration order. Resolution is now sorted and deterministic, and a genuine collision is rejected.
- **Dev bypass disabled binding discovery**, leaving create/update with an unsatisfiable flat 400
  (discovery suppressed, orphan-sentinel guard still active). Discovery is no longer gated; the
  bypass affects runtime resolution only.
- **Dev resolver could not tell a store error from a miss** — `EngineResolver` collapses both into
  `("", false)`, so a transient provider failure substituted a fake credential and cached it for the
  whole run. It now probes the store directly and fails closed on non-`ErrNotFound` errors.
- **`scripts/load-env.ps1`** had the same degenerate-quote crash fixed in `dotenv.go`; guard mirrored.

## Tests: several of mine did not pin what I claimed
- `TestValidateScriptIssues_UnresolvableParameterColumnIsFitNotStructural` asserted the absence of a
  string **no validator ever emits** — it could not fail. Rewritten to assert the positive contract,
  plus a positive-control test so it cannot pass merely because validation stopped running. Verified
  it now fails when the rule is re-escalated.
- The messageId test **self-skipped** via `t.Skipf` on exactly the regression conditions and called
  the inner helper rather than the real entry point. Replaced with an end-to-end test.
- The history-cap test only covered backfill; added one that drives the live WS append path and
  asserts the oldest point is evicted.
- The apiClient test passed under the broken implementation; added a caller-abort test that pins the
  `isTimeoutAbort(error, timedOut)` discrimination (verified failing on revert).
- The dev resolver and `placeholder_substitution` had **zero** coverage; added a table test for
  store-hit / genuine-miss / provider-error, plus a no-opt-in case.
- Only 1 of 4 merge bounds was tested; added entry-count, supplemental-count, name round-trip and
  case-collision tests, and normalized the total-bytes error so all bound errors say "limit".

## Also corrected
- Docs: dev-server port `:3000` → `:4000` (README, DEVELOPMENT, AGENTS, API_USER_GUIDE) and
  `.claude/launch.json`; `desktop/app.go` CORS allow-list now includes `:4000`.
- `APEXLOAD_AUTH_MODE` **does not exist**. Three docs prescribed it; they now describe the real
  mechanism (the persisted `auth.mode` setting, enforced at boot by `enforceAuthPosture`).

## Deliberately NOT fixed
- **Duplicate header-as-data validation** (`validate.go`): the parameter-level and data-source-level
  checks both fire, giving two messages for one defect. Unifying them changes behavior for existing
  fixtures (the data-source check is delimiter-naive; making it delimiter-aware breaks a passing test
  whose `dat` fixture is comma-separated). Cosmetic; needs a product call.
- **Round-1 TLS concern downgraded.** `x509negativeserial=1` only restores parsing of RFC-5280-invalid
  negative serials — chain, signature, expiry and hostname validation are untouched — and the GODEBUG
  merge preserves operator-set values. Confirmed LOW, not the exposure round 1 implied.
- Classpath-asset findings (storage_ref not project-fenced, cache trusted by filename, no quota) and
  the internal AT&T hostnames in `.env.example` / `load-env.ps1`.

## Verification
`go build` ✓ · `go vet ./...` ✓ · `gofmt -l` 0 · **`go test ./pkg/... ./cmd/... ./desktop/...` exit 0**
· `tsc` ✓ · **vitest 257 files / 2446 tests** ✓ · vite build ✓ · controller build ✓ · `GET /` 200 ·
`GET /api/v1/system/version` 200.
