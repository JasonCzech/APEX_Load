# APEX Load — Refactoring Opportunities (2026-06-27)

> **Scope:** 30-area multi-agent audit of ~325k LOC Go + ~180k LOC TypeScript, each finding adversarially re-verified against the source.
> **131 confirmed findings** (4 rejected on verification) across 29 areas + 1 re-run. **~47 are quick wins** (S effort / low risk); **14 high-impact**.

## Executive Summary

APEX Load is, on the whole, a **healthy, multiply-refactored codebase**. Across ~30 analysis areas the recurring verdict is the same: god-files have already been decomposed (pkg/api, pkg/storage, the convert importers, the chart subsystem), shared helpers genuinely exist and are reused, route-access is catalog-driven with parity tests, and the metrics hot path is lock-free. Dead code is sparse and mostly leaf-level. There is **no structural emergency** here — what remains is concentrated, opportunistic cleanup plus a handful of genuine correctness-adjacent gaps.

Two things stand out as real outliers:

1. **`dashboard/src/components/ScriptEditor.tsx` (16,590 LOC)** is the single biggest target in the repo — a ~9,300-line god-component with 76 useState / 33 useEffect. Decomposition is mechanical (clean prop-driven seams already exist), not risky surgery.
2. A cluster of **latent correctness gaps** hides inside otherwise-clean code: divergent `do_while` semantics across the 3 control-loop copies (`engine-1`), JMS/Kafka/EventHubs validators silently skipping base64/extractor checks (`dsl-1`), 4 disagreeing env-bool parsers (`xc-duplication-3`), and 2 web-vitals formatters rendering `NaN ms` (`dash-execution-1`). These matter more than their effort suggests.

### Highest-leverage opportunities

| # | Opportunity | Why it leads |
|---|-------------|--------------|
| 1 | **ScriptEditor.tsx decomposition** (`dash-scripteditor-1..7`) | Largest maintainability + merge-conflict liability; seams are clean |
| 2 | **Control-loop driver unification** (`engine-1`) | 3 hand-mirrored copies already drifted (`do_while` pass-0); hot path |
| 3 | **DSL validator fork** (`dsl-1`) | Duplication + a real latent gap: base64/extractor checks absent for JMS/Kafka/EH |
| 4 | **Import-commit handler triplication** (`api-imports-1`) | ~165 lines copy-paste; high impact, low risk, tests exist |
| 5 | **generatorrpc proto-mapping helpers + tests** (`distributed-rpc-storage-1,2,4`) | Silent cross-boundary metric-loss risk; tests unblock the file split |
| 6 | **Dead code sweep** (`xc-deadcode-1..9`) | One orphaned 426-line file + ~40 leaf functions; pure subtraction |
| 7 | **Env-bool parser unification** (`xc-duplication-3`) | 4 parsers disagree on `yes`/`on`/`y` — real config bug |
| 8 | **CRUD storage-guard boilerplate** (`api-library-misc-1`) | 128 repeated `s.store == nil` guards; one helper |

---

## Themes

Findings cluster into the patterns below. Cross-cutting smells reported by multiple areas are merged here with all sites noted.

### T1 — ScriptEditor.tsx decomposition (dashboard)
The dominant dashboard debt. One umbrella (`dash-scripteditor-1`) tracking 6 concrete extractions, all with **zero closure-coupling** at the proposed seams (verified), so the work is file-extraction not logic surgery.
- Replay/debug state machine → `useReplayDebugSession` hook (`dash-scripteditor-2`)
- Code-pane + JSON minimap → own file (`dash-scripteditor-3`, lowest-risk first cut, ~1,100 LOC)
- ~3,900-line parameter cluster → own module (`dash-scripteditor-4`)
- Debug/Author workbench JSX split (`dash-scripteditor-5`)
- Consolidate 73 stray useState (`dash-scripteditor-6`)
- Split the 8,282-line flat test file in lockstep (`dash-scripteditor-7`)

### T2 — Other dashboard god-components
Same pattern, smaller scale, in the other large pages:
- `SettingsPage.tsx` 4667 LOC, 8 inline section bodies (`dash-settings-types-access-2`)
- `TestHub.tsx` ~700-line `renderWizardStep()` + inline goal-sizing panel (`dash-testhub-newtest-1`, `-2`)
- `NewTestPage.tsx` ~1050-line dual-mode return (`dash-testhub-newtest-3`)
- `EnterpriseArchiveImport.tsx` ~850-line queue-orchestration god-component (`dash-imports-misc-3`)

### T3 — Import-handler scaffolding duplication (API)
- **OpenAPI/Postman/AsyncAPI commit handlers** triplicate ~165 lines of validate→preflight→create/replace→audit (`api-imports-1`) — the highest-value API dedup.
- Inline MuleSoft-review attach diverges from the helper, attaching empty sections (`api-imports-2`).
- `*CorrelationsToPreApplied` ×3 (`api-imports-3`); `read*ImportRequest` content-type dispatch ×10 (`api-imports-4`, conditional).
- 8 keyed support-matrix fetch effects on the dashboard side (`dash-imports-misc-1`).

### T4 — Auth pipeline & resolver duplication (API)
- `authMiddleware` vs `projectScopedAuthMiddleware` copy-paste the full auth pipeline (`api-auth-rbac-2` = `api-server-routing-1`, **same finding, two areas**).
- Legacy project-lookup resolvers never migrated to existing factories (`api-auth-rbac-1`).
- Session routes bypass the route catalog → 12 inline closures + under-documented swagger (`api-server-routing-2`).
- Small idioms: auth-settings access ×20 (`api-auth-rbac-3`), audit actor-resolution (`api-auth-rbac-4`).

### T5 — Cross-cutting duplication (repo-wide)
- **CRUD storage guards**: `s.store == nil` ×128 / ×149 (`api-library-misc-1`); single-table CRUD skeleton ×3 (`api-authoring-ai-1`); project-filter loops (`api-library-misc-2`).
- **Env-bool parsers ×4 disagree** (`xc-duplication-3`, overlaps `cmd-infra-5` — merge: `config.envBool`/`api.envBool`/`storage.envBool`/`parseBoolDefault`).
- **first-non-empty helpers**: `firstPresentString`==`firstNonEmpty` (`api-scheduling-plans-1`), 3 archive variants (`api-archive-reporting-3`).
- **Request-body decode idioms** inconsistent, 6 swallow errors (`xc-consistency-2`).
- **Leader-gated cadence goroutines ×3** + retention boilerplate (`xc-duplication-1`, `-2`).
- **Browser web-vitals mapping** duplicated across distributed/metrics/persistence/archive (`api-distributed-metrics-3`, `api-archive-reporting-1`) and dashboard (`dash-execution-1`, `-2`).
- **value-any→*T marshal-roundtrip decode ×8** (`api-server-routing-3`).
- **scheme://host extraction ×6** convert subpackages (`convert-script-perf-3`); **SupportMatrixEntry ×9** packages (`convert-shared-support-1`).
- **Sort-state/handler/comparator** dashboard pattern (`dash-testhub-newtest-4`, `dash-settings-types-access-3`); **formatDate/Time ×7-10** (`dash-testhub-newtest-5`).

### T6 — Engine & runtime shared-logic drift
- **Control-loop driver reimplemented ×3** with a confirmed `do_while` divergence (`engine-1`, overlaps `xc-consistency-1` on the same `VUser.Run` god-function).
- ThinkTimeConfig conversion ×4 (`engine-2`); VUser spawn cleanup boilerplate ×2 with gauge-leak risk (`engine-3`).
- Distributed threshold/auto-stop evaluation **misdiagnosed but worth investigating** — appears dead on both live paths (`api-orchestration-1`).
- Pending-field reset ×8 (`api-orchestration-2`); multiEngine-vs-backend dispatch (`api-orchestration-3`).

### T7 — Validator & extractor duplication (dsl/protocol)
- **JMS/Kafka/EventHubs validators fork** the pipeline + skip base64/extractor checks (`dsl-1`).
- ~46 enum checks (`dsl-3`), 39 non-negative checks (`dsl-4`), 136-line grab-bag (`dsl-2`), forked dispatch (`dsl-5`).
- **jsonextract reimplemented in grpc/websocket/kafka/jms** with 3 divergent dialects (`protocol-1` — real but a behavior-reconciliation, not a delete).

### T8 — Convert-package god-converters
- `salesforce/analyzer.go` 2188 LOC, 5 clusters (`convert-script-perf-1`).
- `enterprisearchive Convert()` 593-line god-function (`convert-enterprise-lr-2`).
- `jmeter walkThreadGroup` 226-line (`convert-script-perf-2`); `trueclient convertJavaScriptStatement` 16-branch table (`convert-enterprise-lr-1`).
- **API-spec importers** (`convert-api-specs-1..3`): `asyncapi.collectOperations` god-function + small `firstPositive`/source-mapping dups (see appendix T8b).

### T9 — Other god-files (Go, behavior-preserving splits)
`jms.go` 2169 (`protocol-2`), `service.go` 2015 (`distributed-rpc-storage-3`), `handlers_auth.go` 2560 (`xc-duplication-4`; `api-auth-rbac-5` flagged the **same file, not worthwhile speculatively**), `browser/recorder.go` 1384 (`recorder-3`), `TestExecutionArchive.tsx` (`dash-execution-3`), `buildRunArchiveReport` 349 (`api-archive-reporting-2`).

### T10 — Dead / prototype code
One orphaned 426-line file (`xc-deadcode-1`) + scattered leaf functions (`xc-deadcode-2..9`), plus `api-distributed-metrics-2`, `api-github-1`, `convert-enterprise-lr-3`, `recorder-2`, `cmd-infra-4`. Note `dash-imports-misc-2`: ~570 lines of mock fixtures embedded in a **live** component (not dead, just misplaced).

---

## Recommended Sequence

### Phase 0 — Quick wins (S effort, low risk)
Safe, mechanical, mostly independent. Batch into a few cleanup PRs.

| Finding | What | Notes / dependency |
|---|---|---|
| `xc-deadcode-1..9` | Delete orphaned file + leaf functions | **Do `xc-deadcode-7` carefully** — `normalizeSourceReviewMapping` (singular) is LIVE, delete only the 3 truly-dead helpers |
| `api-distributed-metrics-2` | Delete dead inline `Resources` init | trivially safe |
| `api-github-1` | Delete `githubJSONSummary` + `bytes` import | — |
| `convert-enterprise-lr-3` | Drop vestigial `unsupported` return path | — |
| `recorder-2` | Delete `truncate` wrapper + its test | — |
| `cmd-infra-4` | Delete dead `ControllerConfig` | — |
| `api-scheduling-plans-1` | `firstPresentString`→`firstNonEmpty` rename | — |
| `api-library-misc-3` | `err==sql.ErrNoRows`→`errors.Is` (3 sites + 1 in authoring_sessions) | wrap-safe fix |
| `dash-execution-1` | Shared web-vitals formatters w/ `Number.isFinite` guard | fixes `NaN ms` bug |
| `dash-settings-types-access-3` | Hoist 4 `onSort` handlers in AccessPage | — |
| `dash-testhub-newtest-5` | Move `formatDate/Time` to `timeUtils.ts` | — |
| `protocol-3` | Collapse 3 `unavailable*Adapter` → 1 | verify both build tags |
| `api-settings-fleet-2` | `loadProvisioningRequest` helper | mirrors `loadFleetPool` |
| `dsl-4` | `appendNonNegative` helper (39 sites) | identical messages |
| `api-distributed-metrics-1` | `resolveRawGroupTPS`/`resolveGroupName` | — |
| `api-library-misc-4` | Add `handlers_folders_test.go` | **de-risks `api-library-misc-1/2`** |

### Phase 1 — High-impact, low-risk correctness + dedup (S–M)
| Finding | What | Dependency |
|---|---|---|
| `xc-duplication-3` | Unify env-bool parsers — **call out the `yes`/`on`/`y` behavior change in PR** | merge with `cmd-infra-5` |
| `dsl-1` | Extract validator preamble + per-action driver; **gate the new base64/extractor checks deliberately** | high value |
| `api-imports-1` | `commitImportedScript` helper (3 handlers) | add asyncapi commit test first |
| `api-imports-2` | Route OpenAPI/Postman through `attachMuleSoftReview` | — |
| `distributed-rpc-storage-4` | **Backfill merge/percentile unit tests** | unblocks `-1/-2/-3` |
| `distributed-rpc-storage-1`,`-2` | ServerTelemetry + TransactionMetric to/fromProto helpers | after `-4` |
| `api-orchestration-2` | `clearPendingState()` helper (8 sites) | — |
| `api-library-misc-1`,`-2` | `requireStore(w)` + `filterByProjectID[T]` | after `api-library-misc-4` |
| `api-authoring-ai-1`,`-3` | CRUD skeleton + AI authorize helper | — |
| `api-server-routing-3` | `decodeStoredValue[T]` generic | keep typed-nil guard |
| `cmd-infra-2`,`-3` | `runOutcome` + writer-based renderers (adds missing exit-code tests) | do together |
| `dash-imports-misc-1` | `useSourceImportSupport(kind)` hook | — |

### Phase 2 — Medium structural extractions (M, behavior-preserving)
| Finding | What | Dependency |
|---|---|---|
| `engine-1` | Generic control-loop driver — **fixes `do_while` drift** | characterization tests first; folds `xc-consistency-1` |
| `engine-2`,`-3` | ThinkTime helper; VUser-spawn register/unregister (`-race`) | — |
| `api-auth-rbac-2`/`api-server-routing-1` | Single `runAuthPipeline` | **same finding** — do once |
| `api-auth-rbac-1` | Migrate legacy resolvers onto factories | cover 404→500 nuance |
| `api-archive-reporting-1` | `browserMetricCore` embedded struct | marshaling regression test mandatory |
| `api-distributed-metrics-3` | Browser web-vitals adapters | — |
| `convert-shared-support-1` | Shared `supportmatrix.Entry` | start with brokerconfig/eventhubscapture |
| `convert-enterprise-lr-1` | TruClient JS descriptor table | — |
| `convert-script-perf-1`,`-3` | salesforce file split; `convertutil.OriginOf` | — |
| `dash-execution-2`,`-3` | `BrowserDeviceCard`; archive model file | `-4` subsumed by `-2` |
| `dash-testhub-newtest-2`,`-6` | Goal-sizing panel; scenarioSearchText fix | `-6` fixes a real search bug |
| File splits | `protocol-2`, `distributed-rpc-storage-3` (after `-4`), `recorder-3`, `api-archive-reporting-2`, `xc-duplication-4` | pure same-package moves |

### Phase 3 — Larger initiatives (L–XL)
| Finding | What | Notes |
|---|---|---|
| `dash-scripteditor-1..7` | ScriptEditor decomposition | **Sequence: `-3` → `-4` → `-2` → `-5` → `-6` → `-7` incrementally**, never big-bang |
| `dash-settings-types-access-2` | SettingsPage section extraction | one section/PR |
| `dash-testhub-newtest-1`,`-3` | TestHub wizard steps; NewTestPage dual-mode | after goal-sizing panel `-2` |
| `dash-imports-misc-3` | EnterpriseArchiveImport queue hook | — |
| `convert-enterprise-lr-2` | enterprisearchive `Convert()` decomposition | order-sensitive; extensive tests guard |
| `protocol-1` | Route 6 protocols through `jsonextract` | **reconcile 3 dialects + kafka numeric-key semantics first** |
| `recorder-1` | WS proxy `bridgeWebSocket` | MITM path; 2 of 3 variants lack direct tests |
| `api-server-routing-2` | Session-route table + swagger fix | regen swagger, `TestSwaggerYAMLIsFresh` |
| `api-github-5` | `executeGitHubPush` extraction | — |

---

## Borderline / Do-Not-Over-Invest

Real smells whose payoff does **not** justify the churn/risk on an already-refactored codebase:

- **`api-server-routing-4`** — single route-descriptor table: XL / high-risk / ergonomic-only; parity tests already enforce the security direction. **Leave as-is.**
- **`api-auth-rbac-5`** — split `handlers_auth.go` speculatively. The finding itself says "only if a churn hotspot." `xc-duplication-4` covers the *worthwhile* part (extracting the testable RBAC-visibility cluster). **Defer the rest.**
- **`api-authoring-ai-4`** — `buildAIRepairSuggestions` table refactor: output-visible IDs/sort vs golden tests, no functional gain. **Only when adding a 7th detector.**
- **`convert-enterprise-lr-4`** — coverage-markdown rule-of-three: crosses a package boundary against 3 byte-pinned freshness tests for ~7 shared lines. **Skip unless docs churn.**
- **`recorder-5`** — `convertStep` sentinel refactor: trades one implicit control flow for another on heavily-churned, well-tested code. **Skip.**
- **`api-imports-4`** — `readImportEnvelope`: riskiest parsing surface, no imminent new importer. **Opportunistic only.**
- **`api-distributed-metrics-4`** — ns→ms helper: 8 lines, 2 sites. Fold into a sweep, not a PR.
- **`api-settings-fleet-3`**, **`api-authoring-ai-2`**, **`dash-execution-4`**, **`dash-execution-6`**, **`cmd-infra-1`**, **`cmd-infra-5`** — genuine but marginal; bundle opportunistically.
- **`api-orchestration-1`** — **analysis was inaccurate**: the threshold/auto-stop block runs on *neither* live path (`processMetricsSnapshot` has only a test caller). Worth *investigating* whether error-rate auto-stop is effectively dead in production, but the proposed fix is misaimed — re-scope before acting.
- **`dash-imports-misc-2` rationale correction** — the claim "test already imports the mocks" is false; the move requires *adding* exports. Still worthwhile (S/medium), but verify the prototype fallback path is even wanted.

### Areas that are genuinely clean (no action)
`types.ts` (live type barrel, do not split), the chart subsystem (`TestExecutionCharts` already model-extracted), `pkg/storage` (decomposed; `rewriteSQL` brittleness is an intentional tested chokepoint), `pkg/api` flat catalog/data files, cross-cutting error-wrapping & JSON-response conventions, `AccessPage.tsx`.

---

## Appendix — All Confirmed Findings

Effort/Impact/Risk reflect post-verification (downgrades applied).

### T1 — ScriptEditor.tsx (dashboard/src/components/ScriptEditor.tsx)
| ID | Title | Cat | Eff | Imp | Risk |
|---|---|---|---|---|---|
| dash-scripteditor-1 | ScriptEditor god-component (umbrella) `:3140` | god-file | XL | high | med |
| dash-scripteditor-2 | Extract `useReplayDebugSession` `:4249` | abstraction | L | high | med |
| dash-scripteditor-3 | Move code-pane + minimap `:591-1717` | poor-separation | M | high | low |
| dash-scripteditor-4 | Extract parameter cluster `:12432-16338` | god-file | M | high | low |
| dash-scripteditor-5 | Split Debug/Author workbench `:8036-12432` | long-function | L | med | med |
| dash-scripteditor-6 | Consolidate 73 stray useState | poor-separation | L | low | med |
| dash-scripteditor-7 | Split flat test file (8,282 LOC) | test-structure | M | med | low |

### T2 — Other dashboard god-components
| ID | Title | Cat | Eff | Imp | Risk |
|---|---|---|---|---|---|
| dash-settings-types-access-1 | Project-GitHub-policy 16 hooks `SettingsPage.tsx:218` | duplication | M | low | med |
| dash-settings-types-access-2 | SettingsPage 8 inline sections `:1760` | god-file | L | high | med |
| dash-testhub-newtest-1 | `renderWizardStep()` god-func `TestHub.tsx:2638` | long-function | L | high | med |
| dash-testhub-newtest-2 | Extract goal-sizing panel `TestHub.tsx:2310` | poor-separation | M | high | med |
| dash-testhub-newtest-3 | NewTestPage dual-mode return `:1513` | god-file | L | high | med |
| dash-imports-misc-3 | EnterpriseArchiveImport queue god-component `:557` | god-file | L | med | med |

### T3 — Import-handler scaffolding
| ID | Title | Cat | Eff | Imp | Risk |
|---|---|---|---|---|---|
| api-imports-1 | Commit-handler triplication `handlers_web_api_imports.go:151`,`asyncapi_import_commit.go:31` | duplication | M | high | low |
| api-imports-2 | Inline MuleSoft attach divergence `:101,:560` | duplication | S | med | low |
| api-imports-3 | `*CorrelationsToPreApplied` ×3 `import_paramize.go:76` | duplication | S | low | low |
| api-imports-4 | `read*ImportRequest` dispatch ×10 | duplication | L | low | med |
| dash-imports-misc-1 | 8 support-matrix fetch effects `ProtocolAssetImport.tsx:560` | duplication | M | med | low |

### T4 — Auth pipeline & resolvers
| ID | Title | Cat | Eff | Imp | Risk |
|---|---|---|---|---|---|
| api-auth-rbac-1 | Legacy resolvers bypass factories `access_resolvers.go` | duplication | M | med | low |
| api-auth-rbac-2 / api-server-routing-1 | `projectScopedAuthMiddleware` dup pipeline `middleware.go:174` *(same finding)* | duplication | M | high | med |
| api-auth-rbac-3 | `isAuthBypassMode(...)` idiom ×20 | duplication | S | low | low |
| api-auth-rbac-4 | Audit actor-resolution dup `audit.go:40`,`audit_auth_events.go:52` | duplication | S | low | low |
| api-server-routing-2 | Session routes bypass catalog `server.go:251-349` | poor-separation | L | med | med |

### T5 — Cross-cutting duplication
| ID | Title | Cat | Eff | Imp | Risk |
|---|---|---|---|---|---|
| api-library-misc-1 | `s.store==nil` guard ×128 | error-boilerplate | M | med | low |
| api-library-misc-2 | Project-filter / audit-fetch loops | duplication | S | low | low |
| api-authoring-ai-1 | Single-table CRUD skeleton ×3 | duplication | S | med | low |
| api-authoring-ai-3 | AI authorize preamble ×3 `ai_assistance.go:372` | duplication | S | med | low |
| xc-duplication-3 | 4 env-bool parsers disagree `runner_env.go:107` etc | duplication | S | med | low |
| cmd-infra-5 | Truthy-env switch reimplemented `main.go:127` | duplication | S | low | low |
| api-scheduling-plans-1 | `firstPresentString`==`firstNonEmpty` `run_calendar.go:1087` | duplication | S | low | low |
| api-archive-reporting-3 | 3 first-non-empty helpers | duplication | S | low | low |
| api-server-routing-3 | marshal-roundtrip decode ×8 `stored_decode.go` | duplication | S | med | low |
| xc-consistency-2 | Request-body decode idioms / 6 swallow errors `handlers.go:1008` | error-boilerplate | S | med | low |
| xc-duplication-1 | Leader-gated cadence goroutines ×3 `audit_retention.go:82` | duplication | M | low | med |
| xc-duplication-2 | Retention env-config + audit boilerplate | duplication | S | med | low |
| api-distributed-metrics-3 | BrowserLiveMetrics mapping ×4 | duplication | M | med | low |
| api-archive-reporting-1 | 4 Browser*LiveMetrics, no shared core | duplication | M | med | low |
| convert-script-perf-3 | scheme://host extraction ×6 | duplication | S | low | low |
| convert-shared-support-1 | SupportMatrixEntry ×9 packages | duplication | M | med | low |
| dash-execution-1 | formatBrowserMetric/Score ×3 (NaN bug) | duplication | S | med | low |
| dash-execution-2 | browser-device-card grid ×6 | duplication | M | med | low |
| dash-testhub-newtest-4 | Sort state/handler pattern (TestHub) | duplication | M | low | low |
| dash-settings-types-access-3 | 30 inline `onSort` closures `AccessPage.tsx:1599` | duplication | S | low | low |
| dash-testhub-newtest-5 | formatDate/Time ×7-10 | duplication | S | low | low |
| dash-testhub-newtest-6 | scenarioSearchText divergent (search bug) `TestHub.tsx:761` | duplication | S | med | low |
| api-distributed-metrics-1 | Group-TPS resolution dup `distributed_metrics.go:334` | duplication | S | med | low |
| api-distributed-metrics-4 | ns→ms inconsistency | naming | S | low | low |
| api-github-2 | OAuth token-exchange POST dup `github_client.go:462` | duplication | S | med | low |
| api-github-3 | Status handler bypasses resolver (refresh gap) `github_handlers.go:565` | duplication | S | med | low |
| api-github-4 | PutFile→link mapping dup | duplication | S | low | low |
| api-settings-fleet-2 | Provisioning load-or-404 dup `handlers_fleet_management.go` | duplication | S | med | low |
| api-settings-fleet-3 | StatusCounts loops ×4 | duplication | S | low | low |
| api-recording-debug-1 | Manager retention/lifecycle ×9 methods | duplication | L | high | med |
| api-recording-debug-2 | snapshotLocked clone spine dup | duplication | M | med | low |
| api-recording-debug-3 | lock→prune→lookup preamble | error-boilerplate | S | med | low |
| api-recording-debug-4 | liveBackend envelope ×8 `browser_recording_manager.go` | error-boilerplate | S | med | low |
| api-recording-debug-5 | Substring error→HTTP-status mapping | error-boilerplate | S | med | low |
| api-archive-reporting-4 | CSV download-header boilerplate ×6 `handlers_archive.go` | error-boilerplate | S | low | low |
| api-authoring-ai-2 | Diagnose counts/tally dup | duplication | S | low | low |
| api-scheduling-plans-2 | scenario→TestGroup mapping dup | duplication | S | med | low |
| api-scheduling-plans-3 | cron projection loop dup | duplication | M | low | med |
| api-scheduling-plans-4 | timeslot bind logic re-impl | duplication | M | low | med |
| api-orchestration-3 | multiEngine/backend dispatch dup | duplication | S | low | low |
| cmd-infra-1 | 4 CLI flag parsers dup | duplication | M | low | low |

### T6 — Engine & runtime
| ID | Title | Cat | Eff | Imp | Risk |
|---|---|---|---|---|---|
| engine-1 | Control-loop driver ×3 (`do_while` drift) `vuser.go:281,570`,`replay.go:536` | duplication | L | high | med |
| xc-consistency-1 | `VUser.Run` god-func / inline interpreter `vuser.go:91` | long-function | M | high | med |
| engine-2 | ThinkTimeConfig conversion ×4 | duplication | S | med | low |
| engine-3 | VUser spawn cleanup boilerplate `group.go:400,504` | duplication | M | med | med |
| engine-4 | `runReplayUnit` mixed concerns `replay.go:699` | long-function | M | low | low |
| api-orchestration-1 | Threshold/auto-stop eval (misdiagnosed; investigate) `metrics.go:640` | poor-separation | M | med | med |
| api-orchestration-2 | Pending-field reset ×8 | duplication | S | med | low |
| api-orchestration-5 | `distributedExecutionBackend.Run` 155-line | long-function | M | med | med |
| api-orchestration-4 | `runner_test.go` 6738-line not split | test-structure | L | low | low |

### T7 — Validators & extractors (dsl/protocol)
| ID | Title | Cat | Eff | Imp | Risk |
|---|---|---|---|---|---|
| dsl-1 | JMS/Kafka/EH validator fork + base64/extractor gap `validate.go:23` | duplication | M | high | med |
| dsl-2 | `validateWebRuntimeIssues` grab-bag `:1508` | long-function | S | low | low |
| dsl-3 | ~46 enum checks | error-boilerplate | M | low | low |
| dsl-4 | 39 non-negative checks | error-boilerplate | S | low | low |
| dsl-5 | Inconsistent protocol dispatch | poor-separation | M | low | med |
| protocol-1 | jsonextract reimplemented ×4-6 (3 dialects) | duplication | L | med | med |

### T8 — Convert god-converters
| ID | Title | Cat | Eff | Imp | Risk |
|---|---|---|---|---|---|
| convert-script-perf-1 | `salesforce/analyzer.go` 2188 LOC | god-file | M | med | low |
| convert-enterprise-lr-2 | `Convert()` 593-line god-func | long-function | L | high | med |
| convert-script-perf-2 | `jmeter walkThreadGroup` 226-line | long-function | M | med | med |
| convert-enterprise-lr-1 | `convertJavaScriptStatement` 16-branch | duplication | M | high | low |
| convert-script-perf-4 | recorderjson cross-phase index footgun `converter.go:361` | abstraction | S | low | low |
| convert-shared-support-2 | 4 sensitive-name vocabularies (mostly intentional) | duplication | S | low | med |

### T8b — convert-api-specs (API-spec importers)

_Re-analyzed separately (this area's first pass failed structured-output); findings below were adversarially self-verified._

**Area health:** Mature and already heavily refactored. The genuine spec→DSL importers are `openapi`, `asyncapi`, `wsdl`, `postman`; `raml` deliberately hand-rolls a small independent pipeline, and `mulesoft`/`agentforce` are post-import analysis/transform layers over a finished `dsl.Script` (not spec importers). Shared helpers (`convertutil`, `sourceview`, `diag`, `tmpl`, `paramize`) are partially but inconsistently adopted. Headline: a few small mechanical duplications plus one true god-function in `asyncapi.collectOperations`. The high-temptation merges (schema-sampling, auth-scheme handling) are correctly NOT worth doing — the spec dialects diverge too much.

| ID | Title | Cat | Eff | Imp | Risk |
|----|-------|-----|-----|-----|------|
| convert-api-specs-1 | `firstPositive` (+`uniqueSorted`/`copySeen`) duplicated ×4 — `asyncapi/converter.go:2016`, `openapi/converter.go:1658`, `wsdl/converter.go:1589`, `postman/converter.go:1356` | duplication | S | low | low |
| convert-api-specs-2 | Source-mapping struct tail re-implemented per importer — `openapi/converter.go:469`, `postman/converter.go:387`, `wsdl/converter.go:665` | duplication | S | low | low |
| convert-api-specs-3 | `asyncapi.collectOperations` god-function: 3× repeated `kafkaOp` literal + duplicated 2.x publish/subscribe blocks — `asyncapi/converter.go:842-1010` | long-function | M | med | med |

- **convert-api-specs-1** — `firstPositive(values ...int) int` is byte-identical in all four importers; `uniqueSorted`/`copySeen` are semantically identical (only local names differ). `convertutil` was created explicitly to kill ~14 per-importer `FirstNonEmpty` copies — these helpers were simply missed in that sweep. Move them into `convertutil`, delete the copies. **Do NOT** fold in `sanitizeColumnName` — asyncapi's rune-switch and openapi's regex form have subtly different character-class behavior; merging risks changing generated column/data-source names. Blast radius: trivial; each importer has a `converter_test.go`.
- **convert-api-specs-2** — `openAPISourceMapping`/`postmanSourceMapping`/`wsdlSourceMapping` each build a `sourceview.Mapping{}` with an identical 8-field tail plus a near-identical offset→line helper. `sourceview.CoarseMapping` already exists with nearly this signature (hardcodes line=1). Add `sourceview.MappingAt(...)` (or a line param) and route all three through it. Blast radius: small — mapping output is asserted in the openapi/postman/wsdl tests; field values must be preserved exactly (stable IDs derived downstream in `NormalizeMappings`).
- **convert-api-specs-3** — `collectOperations` mixes 2.x/3.x dispatch, schema-format gating, and op construction in one ~168-line func. The `kafkaOp{...}` ~10-field literal is repeated 3× (lines 913, 960, 986); the 2.x `Publish`/`Subscribe` blocks are near-byte-identical except `kind`/default-name. Extract `newKafkaOp(...)` and `collectChannelOp(...)` to collapse the literal and the shared skip/format/oneOf gating. Highest-value structural cleanup here. Blast radius: medium — core of asyncapi import (feeds Kafka + JMS); covered by `asyncapi/converter_test.go` (954 LOC) + `replay_smoke_test.go`, but publish=produce / subscribe=consume semantics and correlation wiring must be preserved exactly.

**Evaluated and dropped (not worth the churn):** schema-sampling merge (`sampleForSchema` — Avro vs JSON-Schema types, different placeholder namespaces, different column-tracking; leaky generic, high risk, ~zero payoff); auth/security-scheme handling (OpenAPI vs Postman vs Kafka-SASL parse different sources, already converge on shared `dsl.AuthConfig` target); `mulesoft`/`agentforce` (not spec importers). No dead code found in these files.

### T9 — Other god-files (Go)
| ID | Title | Cat | Eff | Imp | Risk |
|---|---|---|---|---|---|
| protocol-2 | `jms.go` 2169 LOC | god-file | M | med | low |
| distributed-rpc-storage-1 | ServerTelemetry mapping ×3 `service.go:1185` | duplication | S | high | low |
| distributed-rpc-storage-2 | TransactionMetric build/parse ×2 | duplication | S | high | low |
| distributed-rpc-storage-3 | `service.go` 2015 LOC mixed | god-file | M | med | low |
| distributed-rpc-storage-4 | Merge/percentile math untested | test-structure | M | high | low |
| xc-duplication-4 | `handlers_auth.go` 2560 LOC mixed | god-file | M | med | low |
| api-archive-reporting-2 | `buildRunArchiveReport` 349-line | long-function | M | med | low |
| recorder-1 | WS proxy forward skeleton ×3 `websocket.go:53` | duplication | M | med | med |
| recorder-3 | `browser/recorder.go` 1384 LOC | god-file | M | med | low |
| recorder-4 | Host-substring match ×2 (case bug) | duplication | S | low | low |
| protocol-3 | `unavailable*Adapter` ×3 `jms.go:1447` | duplication | S | med | low |
| protocol-4 | `HTTP.Execute` error-finalize ×8 `http.go:101` | long-function | M | low | med |
| api-settings-fleet-1 | Hand-written settings deep-clone `settings.go:566` | error-boilerplate | S | med | low |
| api-github-5 | `handlePushScriptGitHub` 170-line `github_handlers.go:341` | long-function | M | med | med |
| api-authoring-ai-4 | `buildAIRepairSuggestions` 6 rules | long-function | M | low | med |
| dash-execution-3 | `TestExecutionArchive.tsx` ~40 inline helpers | god-file | M | low | low |
| dash-execution-4 | `renderBrowserSummary` 180-line | long-function | S | low | low |
| dash-execution-6 | ResultsPage comparison model inline | poor-separation | M | low | low |
| dash-imports-misc-2 | ~570 lines mock fixtures in live component `CommandCenterPrototype.tsx:107` | poor-separation | S | med | low |
| cmd-infra-2 | `runTest()` god-func (untested exit codes) `main.go:52` | long-function | M | high | low |
| cmd-infra-3 | run/wait emission dup | duplication | S | med | low |

### T10 — Dead code
| ID | Title | Cat | Eff | Imp | Risk |
|---|---|---|---|---|---|
| xc-deadcode-1 | Orphaned `java_jms_import.go` (426 LOC) | dead-code | S | high | low |
| xc-deadcode-2 | Superseded thin wrappers ×4 | dead-code | S | med | low |
| xc-deadcode-3 | Dead unexported pkg/api helpers ×9 | dead-code | S | med | low |
| xc-deadcode-4 | Dead exported convert API ×3 (`PlannedImportError.Error` LIVE) | dead-code | S | med | low |
| xc-deadcode-5 | Dead Engine methods + helpers | dead-code | S | med | low |
| xc-deadcode-6 | Dead convert importer helpers | dead-code | S | low | low |
| xc-deadcode-7 | source_review.go dead tail (3 of 4; **singular helper LIVE**) | dead-code | S | low | low |
| xc-deadcode-8 | java_vuser coercers + apexctl `RunTest` | dead-code | S | low | low |
| xc-deadcode-9 | Isolated dead helpers ×4 | dead-code | S | low | low |
| api-distributed-metrics-2 | Dead inline `live.Resources` init `:200` | dead-code | S | low | low |
| api-github-1 | Dead `githubJSONSummary` `github_enterprise.go:818` | dead-code | S | low | low |
| convert-enterprise-lr-3 | Dead `unsupported` return path | dead-code | S | med | low |
| recorder-2 | Dead `truncate` wrapper `converter.go:627` | dead-code | S | low | low |
| cmd-infra-4 | Dead `ControllerConfig` `config.go:17` | dead-code | S | low | low |

**Excluded as not worthwhile:** `api-auth-rbac-5`, `api-server-routing-4`, `api-authoring-ai-4` (defer), `convert-enterprise-lr-4`, `recorder-5`, `dash-scripteditor-6` (cheap-cleanup-only). These are documented above under Borderline.

---

## Methodology

Produced by a multi-agent workflow: **30 scoped analysts** (13 Go API clusters, 9 Go core packages, 5 dashboard areas, 3 repo-wide cross-cutting sweeps) each read the real code, then a **skeptical staff-engineer pass adversarially re-verified every finding** against the source with a bias against churn. Of 132 raw findings, 4 were rejected and several downgraded (e.g. `api-orchestration-1` was flagged as a misdiagnosis; `dash-imports-misc-2`'s rationale was corrected). The `convert-api-specs` area was re-run separately after a structured-output failure. Effort/Impact/Risk in the appendix reflect post-verification values.
