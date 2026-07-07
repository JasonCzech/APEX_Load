# Run Report — Implementation Plan

> Answer-first, single-page results viewer for **completed** load-test runs, replacing the 8-tab `TestExecution` archive path and the ~2,100-line `TestExecutionArchive.tsx` scroll. Conceptual mockup and UX rationale precede this doc; this is the engineering plan.
>
> **Status:** Proposed · **Authored:** 2026-07-06 · **Method:** multi-agent codebase investigation (7 parallel deep-reads) → drafted plan → adversarial verification of 12 load-bearing assumptions against source → synthesis. All assumptions CONFIRMED except A10 (corrected). Behind feature flag `apexload.featureFlags.runReport`; live command center untouched.

---

## Run Report — Answer-First Archived-Run Viewing Experience

An engineering-ready implementation plan for replacing the archived-run viewing path (`TestExecution.tsx`'s `showArchiveSection` branch + the ~2112-line `TestExecutionArchive.tsx` vertical scroll) with a bespoke, answer-first single-page **Run Report** for COMPLETED runs, mounted on a new hash route behind a feature flag. The live command center and the live path of `TestExecution.tsx` stay untouched.

All 12 draft assumptions were adversarially verified against the code. **A10 was PARTIAL and is corrected below** (release_trend is conditional-in-effect, has 5 delta fields not 4, and can be absent). A1 gets a de-risking refinement (verdict inputs are already serialized). Everything else is CONFIRMED.

---

### 1. Goals & Non-Goals

**Goals**
- One answer-first `#/report/<testId>` page for COMPLETED runs: verdict banner + 6-tile KPI scorecard → synchronized multi-lane timeline → merged Transaction SLA scoreboard → progressive-disclosure diagnostics → in-place audience lens → compare/baseline overlay.
- Reuse the single `getTestArchiveReport` fetch plus the existing terminal-gated auto-paginating history hooks. No new data-orchestration engine.
- Small, surgical backend work: serialize the already-computed-but-render-only verdict + per-action SLA rows + SLA rollup into the `/archive/report` JSON by reusing `deriveReportVerdict` / `buildSLAComplianceRows` / `evaluateSLARow` verbatim; fix three TS-mirror gaps.
- Fold the `GenerateReportModal` + `ReportComposer` modal handoff into the same page as an audience lens mapped 1:1 onto the existing executive/engineer/audit profiles.
- Ship incrementally behind a flag with zero risk to existing behavior at every phase.

**Non-Goals**
- No change to the live command center (`CommandCenterPrototype.tsx`) or the live path of `TestExecution.tsx`.
- No new report **profile ids** and no new document **sections** (both would break the Go export coverage-matrix test — see §7).
- No `@testing-library` adoption; keep the existing raw `createRoot` + `act()` + `@vitest-environment jsdom` convention (confirmed A12: 124/125 test files, zero `@testing-library` deps, no frontend CI).
- No new SQLite columns → **no `rewriteSQL()`/PG or typed-nil concerns** (all Phase-0 backend changes are in-memory report-struct fields serialized from already-persisted data; nothing touches the DB schema).

---

### 2. Target Architecture, Routing, Mount, Feature Flag

**NEW ROUTE + BESPOKE PAGE — not a `DashboardWorkspace` profile.** `DashboardWorkspace.tsx` is a user-rearrangeable card-stack with per-user localStorage layout and a 3-value width enum; it is presentational and cannot express a cross-lane shared crosshair/brush/breach-shading (independent draggable cards cannot share one x-domain or cursor). The redesign is answer-first with a prescribed hierarchy. `DashboardWorkspace` remains the unchanged live/QUEUED experience.

**Routing (`dashboard/src/appRouting.ts`, 227 lines — verified structure).**
- Add an `AppView` variant to the union at `appRouting.ts:4-14`:
  `| { page: 'report'; testId: string; lens?: ReportLens; compareId?: string }`
- Add `export type ReportLens = 'executive' | 'engineer' | 'audit'` and a `REPORT_LENSES` guard array (mirror `SCRIPT_MODES`/`isScriptMode` at `:32,:51`).
- Add a parse branch in `parseAppViewFromHash` **before** the `isNavPage` fallback at `:133`, modeled on the `test` branch at `:70-77`:
  ```
  if (page === 'report' && segments[1]) {
    const lens = params.get('lens')
    const compareId = params.get('compare') || undefined
    return { page: 'report', testId: segments[1],
      ...(isReportLens(lens) ? { lens } : {}),
      ...(compareId ? { compareId } : {}) }
  }
  ```
  `segments[1]` is already run through `cleanSegment` (`:66` → `decodeURIComponent`), so slashed run ids round-trip.
- Add a `viewToLocationHash` serialize branch before `:192`, mirroring the `test` branch at `:137-142`, emitting `#/report/${encodeURIComponent(view.testId)}?lens=&compare=`. **Lens/compare must appear in the serialized hash** or back/forward won't track — `handleGuardedRouteChange` short-circuits on equal hashes at `appRouting.ts:215`.
- **A7 CONFIRMED / mandatory:** `report` is NOT in `NAV_PAGES` (`:16-30`) nor the `NavPage` type (`Sidebar.tsx:11`), so an unhandled `#/report` silently resolves to `{page:'dashboard'}` via the `:133` fallback. The parse/serialize branches AND `appRouting.test.ts` cases are mandatory — the round-trip suite (`appRouting.test.ts:30-73`) enumerates cases explicitly (no reflective all-pages generator), so the new route goes untested/dead unless added by hand.

**Mount (`App.tsx`).**
- Add `RunReportPage` as a `React.lazy` import alongside the existing lazies at `App.tsx:68-71`.
- Render a `view.page === 'report'` block in AppShell JSX next to the `test` block (~`App.tsx:1051`).
- Add `view.page === 'report' → activePage='results'` at `App.tsx:322` for sidebar highlight.
- Add `navigateToRunReport(id, lens?, compareId?)` → `setGuardedView({page:'report', testId:id, lens, compareId})`. **Route through `setGuardedView`, NOT `setView`**, to preserve the dirty-form `NavigationGuard`.

**Feature flag.** No `FeatureFlagProvider` exists. Read once in AppShell:
`runReportEnabled = readStoredString('apexload.featureFlags.runReport') === 'on' || import.meta.env.VITE_APEXLOAD_RUN_REPORT === '1'` (reuse `utils/browserStorage.ts` + the `import.meta.env.VITE_*` precedent from `apiClient.ts:65`). When ON, point the single `onOpenTestArchive` prop at `navigateToRunReport` — this flips both `ResultsPage` (`App.tsx:1080`) and `DashboardPage` (`App.tsx:1007`) at once. When OFF, keep the legacy `navigateToTestArchive` (`{page:'test', tab:'archive'}`).

**Live-run guard (two layers — the router cannot know status from the hash; status arrives async via polling).**
- (a) `ResultsPage` already computes per-row `isCompletedTestStatus` (`ResultsPage.tsx:269-273`) — only surface the report entry for completed rows.
- (b) Inside `RunReportPage`, after a one-shot snapshot, if `!isCompletedTestStatus(resolveTestStatus(...))` call `onOpenCommandCenter(testId)` to bounce to the live experience. Reuse `utils/testStatusUtils.ts` — do NOT write a 5th `isTerminalStatus` copy.

---

### 3. New Component Tree

```
dashboard/src/
├── appRouting.ts                         (EDIT: 'report' variant + ReportLens + parse/serialize)
├── appRouting.test.ts                    (EDIT: round-trip + invalid-lens cases)
├── App.tsx                               (EDIT: lazy import, mount block, flag, navigateToRunReport, activePage)
├── components/
│   ├── RunReportPage.tsx                 (NEW — composition root)
│   ├── useExecutionTestPolling.ts        (EDIT: add `enabled`/terminal guard — A8)
│   ├── TestExecutionArchive.tsx          (EDIT: decompose show*View seams into exported sections)
│   └── runReport/
│       ├── RunReportVerdictBanner.tsx    (NEW)
│       ├── RunReportKpiScorecard.tsx     (NEW)
│       ├── SynchronizedTimeline.tsx      (NEW — flagship)
│       ├── timelineZoomModel.ts          (NEW — pure reducer)
│       ├── TransactionSlaScoreboard.tsx  (NEW)
│       ├── RunReportDiagnostics.tsx      (NEW — progressive disclosure)
│       ├── reportSectionLens.ts          (NEW — lens→section selector)
│       ├── RunReportLensSwitcher.tsx     (NEW — in-page segmented control)
│       ├── RunReportCompareBar.tsx       (NEW — baseline overlay)
│       └── RunReportExportActions.tsx    (NEW — PDF/CSV/HTML export)
└── types.ts                              (EDIT: p50-p99 on overview, throughput fields on summary point, verdict + SLA interfaces)

pkg/api/
├── report_verdict.go     (EDIT: export reportVerdict→ReportVerdict, slaComplianceRow→SLAComplianceRow)
├── archive.go            (EDIT: add Verdict, per-action SLA fields, SLARollup to report structs)
├── archive_report.go     (EDIT: populate verdict + SLA in build/finalize)
└── handlers_archive.go   (EDIT: attach verdict near attachReleaseTrend on JSON path)
```

| Component | Responsibility | Key props | Data source |
|---|---|---|---|
| `RunReportPage` | Composition root; one-shot snapshot + terminal guard (bounce live→command center); instantiates `useExecutionArchiveModel`/`SummaryHistory`/`ResourceHistory`; owns `activeLens`+`compareId`+section-collapse state (persisted to `apexload.runReport.*`); fixed vertical layout | `testId`, `initialLens?`, `initialCompareId?`, `onOpenCommandCenter`, `onNavigateLens` | `getTestArchiveReport` (`RunArchiveReport`) via `useExecutionArchiveModel.archiveReport`; status via one-shot snapshot + `resolveTestStatus` |
| `RunReportVerdictBanner` | Pass/Conditional/Fail banner + one-line "why"; editable override folds `ReportComposer`'s `verdict_override`/`verdict_explanation` onto `saveTestReportAnnotations` | `verdict`, `override?`, `onSaveOverride` | NEW `report.verdict` (Phase-0); override via `saveTestReportAnnotations` (`types.ts:1275`) |
| `RunReportKpiScorecard` | 6 tiles (avg latency, P95, error rate, peak TPS, peak VUsers, completeness), each value + signed delta + sparkline | `overview`, `releaseTrend?`, `samples` | `report.overview` (needs `p95_ms` added — A3); deltas from `report.release_trend.deltas` (5 fields, possibly absent — A10); sparklines from `summary_timeline` |
| `SynchronizedTimeline` | 4 stacked `ComposedChart`s sharing `syncId`; one shared crosshair; one shared `Brush`; SLA `ReferenceLine`; breach `ReferenceArea` down all lanes | `points: MetricsHistoryPoint[]`, `sla`, `zoom`, `onZoom`, `baselinePoints?` | `useExecutionArchiveSummaryHistory` → `mapArchiveSummarySamples`; `useExecutionArchiveResourceHistory` merged by `timestampMs`; SLA from `manifest.provenance.goal_config` |
| `timelineZoomModel.ts` | Pure reducer for shared `[startIndex,endIndex]` + breach-window derivation | n/a | pure state over `MetricsHistoryPoint[]` |
| `TransactionSlaScoreboard` | Merged Action Summary + Transaction Drill-Down + per-transaction SLA; sortable/filterable; SLA pills; virtualized | `byAction`, `slaRows?`, `drillDown` | `report.by_action` + NEW per-action `sla_result`/`kind`/`has_sla`/`sla_ms`; drill-down via `useExecutionArchiveModel.archiveActionEntries` |
| `RunReportDiagnostics` | Collapsed-by-default blocks: error patterns, network phases, resource peaks, completeness/evidence | `report`, `lensSections` | `report.error_patterns`/`network_phases`/`resource_peaks`/`completeness`/`evidence_tiers` |
| `reportSectionLens.ts` | Selector mapping `activeLens` → visible/emphasized modules; reads `report.report_profiles[lens].included_sections`; replicates Go `stringSet` alias map | n/a | `report.report_profiles` |
| `RunReportLensSwitcher` | In-page `role=tab` segmented control replacing the modal handoff; re-fetches profile-scoped annotations on switch; preserves audit-only integrity re-verify | `activeLens`, `onLensChange`, `profiles` | `report.report_profiles`; `getTestReportAnnotations(id, lens)` |
| `RunReportCompareBar` | Pick baseline; overlay muted curves aligned by wall-clock; delta columns | `currentId`, `baselineId?`, `onPickBaseline` | `pinComparisonBaseline`; `getTestArchiveAnalysis(baseline_id)`; 2nd `useExecutionArchiveSummaryHistory` keyed on `baselineId` |
| `RunReportExportActions` | HTML = `downloadTestReport(id, lens)`; NEW `downloadTestReportFormat(id, lens, 'pdf'\|'csv')` | `testId`, `lens` | `downloadTestReport` (`useTestApi.ts:584`) + NEW client method |

---

### 4. Synchronized-Timeline Technical Design

**Chosen approach: stacked recharts `ComposedChart`s sharing `syncId="metrics"`.** This is the proven production pattern — verified (A5) at exactly 7 sites in `TestExecutionCharts.tsx:2390/2515/2564/2628/2654/2690/2728`, each a `ComposedChart`. recharts is **2.15.4** installed, and `Brush` IS exported (`node_modules/recharts/types/index.d.ts:51`) but used nowhere today.

**Lanes (top→bottom):**
1. VUsers — `Area` (`active_vusers`).
2. Throughput — two `Line`s: actual `tps` + target `target_tps`. **Note (A6):** the target-TPS overlay in the existing chart at `TestExecutionCharts.tsx:2533` is *also* gated `surface==='command-center'`, so this lane needs the same un-gating as the crosshair.
3. Response time — two `Line`s: avg + P95, with the SLA drawn ON the chart.
4. Error rate — `Line` (`error_pct`), with the max-error SLA drawn on.

**One data array.** All 4 lanes MUST be fed ONE `MetricsHistoryPoint[]` keyed identically by `timestampMs` (**Risk:** recharts `syncId` synchronizes by matching x-category value only — a per-lane array desyncs the crosshair/brush silently). Never assume a fixed interval step — `MetricsIntervalSeconds` is configurable (`metrics.go:587`).

**Shared crosshair.** **A6 CONFIRMED (+ refinement):** `renderSharedCursor` (`TestExecutionCharts.tsx:870-884`) opens with `if (surface !== 'command-center') return null` at `:871`, and the ONLY production caller passing `surface="command-center"` is `CommandCenterPrototype.tsx:3270` — so the crosshair renders on NEITHER the archived NOR the live run-detail view today (it is a local closure capturing `sharedCursor`/`activeCursor`/`localActiveCursor` at `:859-868`). Two options: (a) render the new lanes with `surface='command-center'` passed in, or (b) lift `renderSharedCursor` + its shared-cursor state plumbing into a reusable primitive (`resolveSharedCursorTime` + `{label,timestampMs}`). Prefer (b) — extract into `testExecutionChartModel.ts` — so the Run Report doesn't depend on a magic surface string.

**Shared brush/zoom.** Add a real recharts `<Brush>` on the bottom lane, lifting `[startIndex,endIndex]` to shared state applied to every lane's `XAxis` domain. Back it with `timelineZoomModel.ts` (pure reducer modeled on `testExecutionChartZoomModel.ts` for cheap unit tests). **Fallback (A5):** if `Brush` proves unusable, fall back to the existing `executionChartZoomModel` + `ReferenceArea` drag-select already in the repo — the plan still works, only the affordance changes.

**SLA line + breach band.** Draw the SLA threshold as `<ReferenceLine y={slaValue}>` on the RT and error lanes; derive breach windows and render identical `<ReferenceArea x1 x2>` in EVERY lane. Thresholds come from `manifest.provenance.goal_config.p95_latency_ms`/`max_error_pct` — **nullable at every hop** (`manifest.provenance?.goal_config?.p95_latency_ms`, mirroring Go nil-guards at `report_verdict.go:263-269`); render an "SLA not configured" state when absent.

**Theming.** Use CSS vars only — `var(--chart-*)`, `var(--warning)`, `var(--danger)` + `chartTooltipTheme.ts`. Do NOT reuse `ReportCharts.tsx`'s hardcoded sepia palette (PDF/print export only; "sepia" is not a theme token — themes are `default-dark`/`light`/`solarized-*`/`monokai-dimmed`).

**Wall-clock x-axis.** `MetricsHistoryPoint.timestampMs` drives a time-scaled `XAxis`; labels via the existing `{label,timestampMs}` formatting. Merge resource history onto the same array by nearest `timestampMs`.

---

### 5. Data & Plumbing Map

| Component | Hook | Endpoint | Type field / note |
|---|---|---|---|
| KPI scorecard | `useExecutionArchiveModel.archiveReport` | `GET /tests/{id}/archive/report` | `overview.{avg_response_ms, p95_ms*, error_pct, peak_tps, peak_active_vusers}` + `completeness.score_pct`; `*p95_ms` needs TS add (A3) |
| KPI deltas | same | same | `release_trend.deltas.{error_pct_delta, avg_response_ms_delta, avg_response_pct_delta, peak_tps_delta, completeness_delta}` — **5 fields, may be absent (A10)**. P95/peak_vusers/target_tps have NO server delta → client baseline diff OR server extension |
| Verdict banner | same | same | NEW `report.verdict` (Phase-0) |
| Timeline lanes | `useExecutionArchiveSummaryHistory` → `mapArchiveSummarySamples` | `GET /tests/{id}/archive/summaries?limit=1000&offset=` | `IntervalSummaryRecord.{avg_response_ms, p50/p90/p95/p99_ms, active_vusers, tps, target_tps, error_pct}` — **A4: `summary_timeline` alone lacks avg + p50/p90/p99, so lanes MUST use the paginated hook**, gated on `test.archive.interval_summary_count>0` |
| Timeline resource merge | `useExecutionArchiveResourceHistory` | `GET /tests/{id}/archive/resources?limit=1000&offset=` | merged by `timestampMs` |
| SLA line | `archiveReport` | report | `manifest.provenance?.goal_config?.{p95_latency_ms, max_error_pct}` (nullable, A2) |
| Scoreboard | `archiveReport` + `useExecutionArchiveModel.archiveActionEntries` | report + `analysis`/`transactions` | `by_action` + NEW per-action `sla_result`/`sla_result_kind`/`has_sla`/`sla_ms` (Phase-0) |
| Diagnostics | `archiveReport` | report | `error_patterns`/`network_phases`/`resource_peaks`/`completeness`/`evidence_tiers` |
| Lens | `archiveReport` | report | `report_profiles[lens].included_sections`/`section_order` |
| Compare deltas | `getTestArchiveAnalysis(baseline_id)` | `analysis?baseline_id=` | auto-falls-back to self on baseline error (`useExecutionArchiveModel.ts:281-292`, A11) |
| Compare curves | 2nd `useExecutionArchiveSummaryHistory` on `baselineId` | `summaries` | **no combined endpoint (A11)** — second full pagination pass |

**Client-side derivations called out:**
- P95/peak-VUsers/target-TPS **KPI deltas** (no server `release_trend` field — A10): fetch the prior run's overview and subtract, OR extend the backend (see §6, optional).
- **Breach-window derivation** from SLA thresholds vs the point array (in `timelineZoomModel.ts`).
- **Wall-clock baseline alignment** for compare (nearest-timestamp, NOT sample index).
- Every `release_trend`-derived delta must **degrade to no-delta** when the field is absent (A10: `release_trend` is `omitempty` and only present when the run has a non-empty `Provenance.ReleaseID` and appears in its release's trend series).

**Polling (A8 CONFIRMED).** `useExecutionTestPolling` (`useExecutionTestPolling.ts:15`) takes only `testId`, has no terminal guard, and `setInterval(load, 2000)` at `:35` polls `getTest` forever (pausing only on hidden tab at `:22-24`). Add an `enabled`/terminal-stop param, or a `useCompletedRunSnapshot` one-shot, before reusing it on a completed run.

---

### 6. Backend / Data Changes (only what verification CONFIRMED is missing)

All changes are in-memory report-struct fields serialized from already-persisted data. **No new DB columns → no `rewriteSQL()`/PG or typed-nil work.**

**6a. Verdict (A1 CONFIRMED).** `deriveReportVerdict` (`report_verdict.go:50`) returns the unexported `reportVerdict` (`:38-43`) — structurally un-serializable — and its sole caller is the HTML/PDF render path (`report_generator.go:1039`). The JSON handler `handleGetRunArchiveReport` (`handlers_archive.go:304`) writes `report` verbatim at `:363` and never calls it. `RunArchiveReport` (`archive.go:269-305`) has no verdict field; TS mirror (`types.ts:1203`) has none.
- **Refinement (A1):** the raw inputs ARE already serialized (`manifest.sla_breached`/`sla_details` at `archive.go:45-46`, `overview.telemetry`, `overview.throughput_missed_intervals`, `completeness.status`/`score_pct`). Two options: (a) **serialize a new `Verdict` field** (recommended — single source of truth with the PDF), or (b) mirror `deriveReportVerdict` logic in TS. Choose (a).
- Export `reportVerdict → ReportVerdict` with JSON tags `{status, conditional, failed, explanation}`. Add `Verdict *ReportVerdict json:"verdict,omitempty"` to `RunArchiveReport`. Populate it in `handleGetRunArchiveReport` next to `attachReleaseTrend` (`handlers_archive.go:359`) via `report.Verdict = deriveReportVerdictExported(report)` — keeping `buildRunArchiveReport` pure, consistent with how `ReleaseTrend` is attached (`archive.go:299-304`).

**6b. Per-action SLA + rollup (A2 CONFIRMED).** `slaComplianceRow` (`report_verdict.go:218-227`) and `evaluateSLARow` (`:285`) are backend-only, used at `report_generator.go:1063`. `by_action` (`types.ts:1217`) carries no result.
- Export `slaComplianceRow → SLAComplianceRow`. In the report build/finalize, evaluate each `ByAction` entry via `evaluateSLARow` verbatim and attach `{sla_result, sla_result_kind, has_sla, sla_ms}` to a per-action structure (either enrich `RunArchiveActionStats` or emit a parallel `report.sla_rows` map keyed by action name — prefer the parallel map to avoid touching the widely-shared `RunArchiveActionStats`).
- Add `report.sla_rollup {evaluated, passed, warned, failed, na, worst_kind}`.
- **Exact semantics to preserve (from `evaluateSLARow:285-301`):** fail when `p95_ms > slaMs` OR `error_pct > max_error_pct`; the **warn band is a still-passing near-threshold band** (`p95` within 90–100% of SLA → `"Pass — N% of SLA"`, kind `"warn"`), NOT a discrete SLA level; with no SLA configured → `"—"`/`"na"` unless the error limit is broken. Serializing the evaluated rows keeps parity with the PDF/HTML and lets the frontend avoid re-deriving thresholds.

**6c. TS-mirror fixes (A3, A4 CONFIRMED).**
- `RunArchiveOverview` (`types.ts:1117-1135`) is missing percentiles although Go emits them (`archive.go:496-499`, `omitempty`; populated `archive_report.go:147-150`). Add `p50_ms?`, `p90_ms?`, `p95_ms?`, `p99_ms?: number` after `:1134`. (Reuse the naming from action-stats interfaces ~`types.ts:908-911`; do not confuse them with the overview object.)
- `RunArchiveSummaryPoint` TS (`types.ts:1192-1201`) omits `throughput_gap_pct`/`throughput_target_missed` that Go writes (`archive.go:424-425`). Add `throughput_gap_pct?: number` + `throughput_target_missed?: boolean`.
- Add `RunArchiveVerdict`, per-action SLA fields, and `RunArchiveSLARollup` interfaces mirroring 6a/6b.

**6d. (Optional, de-risks Phase 3 & 2) enrich `RunArchiveSummaryPoint` server-side** with `avg_ms` + `p50/p90/p99` (and add the missing target-vs-actual fields) so the coarse single-fetch timeline could drive RT + throughput-vs-target lanes without the summaries pagination, and so P95/target-TPS KPI deltas could come from the server. Purely additive; defer if pagination performance is acceptable.

**Tests:** `go test ./pkg/api/...` — assert `/archive/report` JSON now carries `verdict` + per-action `sla_result` + `sla_rollup`; keep `TestRunArchiveReportProfilesAreConsistentAndRenderable` (`archive_report_test.go:518`) GREEN (adding report-struct FIELDS is safe; adding/removing SECTIONS is not — see §7). `npm run build` after the `types.ts` additions.

---

### 7. Audience Lens ↔ Report-Profile Mapping (coverage-matrix contract)

**A9 CONFIRMED.** `buildRunArchiveReportProfiles` (`archive_report.go:516-554`) defines exactly `executive`/`engineer`/`audit`, each with parallel `IncludedSections`+`SectionOrder` of identical member sets, serialized at `:362` and mirrored as `RunArchiveReportProfile` (`types.ts:1365`). The lens is a **client-side density re-weighting of the already-declared section vocabulary** (verdict, sla_rollup/sla_detail, actions, summary_timeline, graphs, errors, resources, network_phases, completeness, evidence_tiers) → **zero Go changes, no new profile id, no new section.**

- `reportSectionLens.ts` reads `report.report_profiles[lens].included_sections`/`section_order` and replicates the Go `stringSet` alias map (`archive_report_csv.go:239-270`, e.g. `graphs` aliasing). Generalizes `ReportComposer.buildSectionSet` (which only aliases `graphs`); deletes ad-hoc `isExecutive`/`isEngineer`/`isAudit` fallbacks.
- `RunReportLensSwitcher` drives `RunReportDiagnostics` collapse/emphasis per lens; re-fetches profile-scoped `getTestReportAnnotations(id, lens)` on switch (cache per lens); preserves audit-only integrity re-verify (`verifyTestArchive`). **Per-lens annotation storage (Risk):** keys are `report:annotations:{test}:{profile}` — a verdict override under `executive` does NOT appear under `engineer`; re-fetch on switch, never assume shared state.
- **Contract guardrail (A9 refinement):** `TestRunArchiveReportProfilesAreConsistentAndRenderable` (`:518-577`) enforces (a) `IncludedSections≡SectionOrder` set-equality, (b) no dup sections, (c) `executive`/`engineer`/`audit` exist, (d) every export format renders. It does NOT contain a per-section `HonorsIncludedSections` assertion, and renderers silently ignore unknown sections (`Has()`/`stringSet` membership only). So the lens work needs to touch NONE of the renderer templates. If a future section were ever needed, adding it to BOTH `IncludedSections` and `SectionOrder` (plus a CSV `stringSet` alias only if it maps to an aliased CSV block) keeps this test green — but the redesign does not require it.

**Export consolidation.** `RunReportExportActions`: HTML = existing `downloadTestReport(id, lens)` (`useTestApi.ts:584`); ADD `downloadTestReportFormat(id, lens, 'pdf'|'csv')` hitting `GET /tests/{id}/archive/report?format=&profile=` (handler exists, `handlers_archive.go:304`, with the csv/pdf switch at `:314-355`) to replace `ReportComposer`'s `window.print()`. Fold `ReportComposer` editing (persist, note→finding/commentary insertion, finding CRUD, narrative, section_comments) into progressive-disclosure edit panels; retire the `GenerateReportModal` + `ReportComposer` full-screen shells.

---

### 8. Compare Mode

**A11 CONFIRMED.** No combined/JSON compare endpoint — the only comparison route `GET /tests/{id}/archive/comparison` is CSV-only (rejects non-csv with 400, `handlers_archive.go:421-424`); `downloadTestArchiveComparison`/`MultiComparison` hardcode `format:'csv'` (`useTestApi.ts:617/623`).
- `RunReportCompareBar`: pin baseline via `pinComparisonBaseline`/`test.comparison_baseline.source_test_id`.
- Delta columns from `getTestArchiveAnalysis(baseline_id)` (`useTestApi.ts:590-611`) — handle the auto-fallback-to-self (`useExecutionArchiveModel.ts:281-292` surfaces "Showing current archive only.").
- Baseline lane curves from a SECOND `useExecutionArchiveSummaryHistory` keyed on `baselineId` (no combined endpoint → second full pagination pass).
- **Align baseline vs current by elapsed/wall-clock nearest-timestamp, NOT sample index** (generalize `resolveSharedCursorTime`); overlay as muted `Line`s per lane. Reuse `resultsComparisonChartModel` for delta math/colors.

---

### 9. Migration / Rollout Coexisting with the Live Command Center

- **Flag OFF (default):** everything is unchanged. `onOpenTestArchive` → `navigateToTestArchive` (`{page:'test', tab:'archive'}`); `TestExecution.tsx` + `TestExecutionArchive.tsx` serve archived runs as today.
- **Flag ON:** `onOpenTestArchive` → `navigateToRunReport`; completed runs open the new page; the live path of `TestExecution.tsx` and `CommandCenterPrototype.tsx` are never touched.
- **Changes:** `ResultsPage.tsx` and `DashboardPage` only swap the target of the single `onOpenTestArchive` prop (via the AppShell flag switch). `appRouting.ts`/`App.tsx` gain the route + mount. `useExecutionTestPolling.ts` gains a terminal guard. `TestExecutionArchive.tsx` is decomposed (its `show*View` seams at `:823-838` mark the section boundaries) into exported per-section components consumed by both the legacy scroll AND `RunReportDiagnostics`/`TransactionSlaScoreboard` — no behavior change to the legacy page.
- **Deprecated once the flag defaults ON and stabilizes:** the `showArchiveSection` branch of `TestExecution.tsx`, the standalone `TestExecutionArchive.tsx` scroll, and the `GenerateReportModal`/`ReportComposer` full-screen shells. Keep them until the flag is removed.

---

### 10. Testing Strategy

**A12 CONFIRMED:** raw `createRoot` + `act()` + first-line `// @vitest-environment jsdom` pragma + `IS_REACT_ACT_ENVIRONMENT=true`; no `@testing-library`; run the frontend gate (`npm run build` + `npm run lint` + `npm test`) **locally** as an explicit acceptance gate — there is no frontend CI (desktop.yml builds the dashboard but runs no tsc/lint/vitest).

| Area | Tests |
|---|---|
| Routing | `appRouting.test.ts` round-trip for `#/report/<id>?lens=&compare=` incl. slashed id (`run%2F123`) and invalid-lens fallback |
| Page shell | `RunReportPage.test.tsx`: flag-off keeps legacy route; flag-on routes to report; live-status bounces to command center; no-archive empty state |
| Verdict/KPI | banner text/class per Pass/Conditional/Fail (feed `report.verdict` via mock); 6 tiles render label+value+signed delta (mirror `ResultsPage` `'+2.0'`/`'+35.0 ms'`); **delta absent → no-delta render** (A10); sparkline stub; role/aria-label |
| Timeline | `timelineZoomModel.test.ts` pure reducer (one brush action updates all lanes' range); interaction test copies the wholesale recharts mock from `TestExecutionCharts.interaction.test.tsx` — **UPGRADE the `ReferenceArea` stub to emit `x1/x2`** (Risk: it's a no-op passthrough today → breach coverage is a FALSE GREEN otherwise); assert all lanes share one `syncId`, `onMouseMove` on one lane reads all, SLA `ReferenceLine` present, breach `ReferenceArea` identical in every lane |
| Scoreboard | sort toggles (`aria-sort`); SLA pill class/text per pass/warn/fail/na; filter narrowing; virtualization asserts only a windowed row subset in DOM |
| Lens/export | lens switch shows/hides/re-weights sections in place (not a modal); active lens persists to `apexload.runReport.lens.v1` and round-trips across remount; `go test ./pkg/api` stays green (coverage-matrix untouched) |
| Compare | baseline curves overlay each lane; delta columns render signed values + `aria-selected` metric tabs (mirror `ResultsPage.test.tsx:496-520`); baseline-unavailable soft-error |
| Backend | `go test ./pkg/api/...` asserts verdict + per-action SLA + rollup in JSON; `archive_report_test.go:518` GREEN |

---

### 11. Accessibility, Theming, Performance

- **A11y:** each chart lane `role="img"` + `aria-describedby` a visually-hidden `<table>` of its series (also satisfies the interaction tests); scoreboard `aria-sort`/`aria-pressed`; lens switcher `role=tab`/`aria-selected`; tiles labeled.
- **Theming:** CSS vars only — `var(--chart-*)`, `var(--warning)`, `var(--danger)`, `chartTooltipTheme.ts`. Never the `ReportCharts.tsx` sepia palette. Verify across all 5 `data-theme` values.
- **Performance/virtualization:** virtualize scoreboard rows with `@tanstack/react-virtual` (`SourceMappingReview.tsx:257` template, `estimateSize` ~44) + a visually-hidden full `<table>` for a11y/coverage. Summaries/resources use the existing terminal-gated one-shot pagination (page size 1000). Keep the single `getTestArchiveReport` fetch as the primary source; only paginate for full-fidelity lanes and drill-down.

---

### 12. Risks & Gotchas

1. **Coverage-matrix contract:** never alter `buildRunArchiveReportProfiles` `IncludedSections`/`SectionOrder` — `archive_report_test.go:518` enforces set-equality + all-format rendering. Adding report-struct FIELDS is safe; adding/removing SECTIONS is not.
2. **recharts `syncId` syncs by matching x-category only** — feed ONE array keyed by `timestampMs`; never assume a fixed interval step (`MetricsIntervalSeconds` configurable, `metrics.go:587`).
3. **`ReferenceArea` mock is a no-op passthrough** → breach coverage is a false green until the mock emits `x1/x2`.
4. **`react-hooks/recommended` is ON, no CI** — lens switcher + timeline must call all hooks unconditionally (`App.hookOrder.test.tsx` guards this class). Run lint/build/test locally.
5. **Sepia is not a theme token** — `ReportCharts.tsx` sepia is print-only; all new colors must be CSS vars.
6. **Per-lens annotation keys** `report:annotations:{test}:{profile}` — overrides don't cross lenses; re-fetch on switch.
7. **`useExecutionTestPolling` polls forever** on a completed run (A8) — guard/replace before reuse.
8. **`summary_timeline` is deliberately compact** (A4: no avg/p50/p90/p99/per-group) — source lanes from `useExecutionArchiveSummaryHistory`, not the single-fetch point, unless enriched server-side (6d).
9. **`release_trend` can be absent** (A10 correction) — `omitempty`, present only for runs with a non-empty `Provenance.ReleaseID` in a trend series; treat as optional and degrade every delta to no-delta.
10. **SLA thresholds nullable at every hop** (A2) — `manifest.provenance?.goal_config?.p95_latency_ms`; render "SLA not configured" (mirrors Go `"na"`).
11. **The target-TPS lane is also command-center-gated** (A6, `TestExecutionCharts.tsx:2533`) — un-gate alongside the crosshair.
12. **Compare has no combined endpoint** (A11) — second pagination pass + analysis self-fallback.

---

### 13. Phase-by-Phase Breakdown (effort + incremental shipping)

Each phase ships behind the flag with zero risk to existing behavior.

- **Phase 0 — Backend data contract (verdict + SLA + mirror fixes). ~2–3 days.** §6a–6c. Export `ReportVerdict`/`SLAComplianceRow`; attach `verdict` + per-action SLA + `sla_rollup` on the JSON path; fix the three TS gaps. Gate: `go test ./pkg/api/...` (incl. new JSON assertions, `archive_report_test.go:518` green) + `npm run build`. **No DB changes.** Optional 6d deferred.
- **Phase 1 — Route, flag, page shell (flag-gated, no redesign). ~2 days.** §2. `report` variant + `ReportLens` + parse/serialize + `appRouting.test.ts`; flag read + `navigateToRunReport`; `RunReportPage` with one-shot snapshot + terminal-guard bounce + no-archive empty state; guard `useExecutionTestPolling`; lazy mount. Gate: routing + shell vitest, lint. **Shippable, zero-risk.**
- **Phase 2 — Verdict banner + 6-tile KPI scorecard. ~2–3 days.** §3. `RunReportVerdictBanner` (+ override), `RunReportKpiScorecard` (deltas from `release_trend`, client baseline diff for P95/vusers/target-tps, absent→no-delta), sparklines. Gate: banner + tiles vitest.
- **Phase 3 — Synchronized multi-lane timeline (flagship). ~4–6 days.** §4. `SynchronizedTimeline` (4 lanes, `syncId`, extracted crosshair, `Brush`, SLA `ReferenceLine`, breach `ReferenceArea`), `timelineZoomModel.ts`, extract crosshair helper, un-gate target-TPS. Gate: reducer + interaction vitest (with upgraded `ReferenceArea` mock).
- **Phase 4 — Transaction SLA scoreboard. ~3 days.** §3. `TransactionSlaScoreboard` from `by_action` + Phase-0 SLA rows; virtualized; decompose `TestExecutionArchive.tsx` seams. Gate: sort/filter/pill/virtualization vitest.
- **Phase 5 — Audience lens + progressive diagnostics + export consolidation. ~3–4 days.** §7. `reportSectionLens.ts`, `RunReportLensSwitcher`, `RunReportDiagnostics`, `RunReportExportActions` (+ `downloadTestReportFormat`); fold `ReportComposer` editing; retire modals. Gate: lens-in-place + persistence vitest; `go test ./pkg/api` green.
- **Phase 6 — Compare / baseline overlay. ~3 days.** §8. `RunReportCompareBar` + timeline overlay; wall-clock alignment; self-fallback handling. Gate: overlay + delta-columns + soft-error vitest.

**Total ~19–24 engineer-days.** Phases 0–1 are independently shippable; each later phase adds a section to the flag-gated page without disturbing prior phases or any live experience.

---

**Files an engineer starts in today:** `pkg/api/report_verdict.go`, `pkg/api/archive.go` (`:269-305`, `:419-430`, `:489-511`), `pkg/api/handlers_archive.go:359`, `dashboard/src/types.ts` (`:1117-1135`, `:1192-1201`) for Phase 0; then `dashboard/src/appRouting.ts` (`:63-134`, `:136-193`) + `appRouting.test.ts` + `App.tsx` for Phase 1.

---

## Appendix A — Facts corrected by verification (vs. the draft)

- A10 corrected: release_trend is unconditional as a CALL but conditional in EFFECT (omitempty; present only when the run has a non-empty Provenance.ReleaseID and appears in its release's trend series) — the KPI-tile code must treat report.release_trend as optional/possibly-undefined and degrade every delta to no-delta when absent.
- A10 corrected: there are 5 delta fields, not 4 — the draft omitted avg_response_pct_delta. Server-side baseline+delta exists only for error_pct, avg_response_ms (abs+pct), peak_tps, completeness; P95, peak_vusers, and target_tps have NO server delta (peak_active_vusers is a raw point value only; P95/target_tps absent from ReleaseTrendPoint/Deltas).
- A1 refined: the RAW inputs to the verdict are already serialized (manifest.sla_breached/sla_details, overview.telemetry, overview.throughput_missed_intervals, completeness.status/score_pct), so Phase 0 could either serialize a new verdict field (recommended) or re-derive in TS; the banner cannot read report.verdict as-is today.
- A6 refined: renderSharedCursor's surface==='command-center' gate suppresses the crosshair on the LIVE run-detail view too, not just archived — the only surface rendering it today is CommandCenterPrototype. The target-TPS overlay (TestExecutionCharts.tsx:2533) is gated identically and also needs un-gating for the throughput-vs-target lane.
- A9 refined: the coverage test (archive_report_test.go:518) has NO per-section HonorsIncludedSections assertion and renderers silently ignore unknown sections, so the lens work touches no renderer templates; a future section would need both IncludedSections+SectionOrder (plus a CSV stringSet alias only if aliased) but is not required by this redesign.
- A2 refined: exact evaluateSLARow semantics to preserve — fail when p95_ms>slaMs OR error_pct>max_error_pct; the warn band is a still-PASSING near-threshold band (90-100% of SLA, 'Pass — N% of SLA', kind 'warn'), not a discrete SLA level; no-SLA → '—'/'na' unless error limit broken. All goal_config hops are nullable.
- No new DB columns are introduced by any Phase-0 backend change, so the rewriteSQL()/Postgres and typed-nil storage gotchas do NOT apply (all new fields are in-memory report-struct fields serialized from already-persisted data).

## Appendix B — Open questions / decisions needed before build

1. Phase-0 verdict: serialize a new report.verdict field (recommended, single source of truth with the PDF) vs re-derive deriveReportVerdict logic client-side from the already-serialized raw inputs — confirm the preferred option before implementation.
2. Optional 6d: enrich RunArchiveSummaryPoint server-side (avg_ms + p50/p90/p99 + target-vs-actual) to drive the timeline off the single fetch and provide P95/target-TPS KPI deltas — is the extra summaries pagination acceptable for large runs, or should this land in Phase 0?
3. P95 / peak-VUsers / target-TPS KPI deltas have no server release_trend field (A10): prefer a client-side prior-run overview diff, or extend ReleaseTrendDeltas/Point server-side?
4. Crosshair reuse (A6): pass surface='command-center' into the new lanes (quick) vs lift renderSharedCursor + shared-cursor state into a reusable primitive in testExecutionChartModel.ts (cleaner) — pick one.
5. Per-action SLA serialization shape: enrich the widely-shared RunArchiveActionStats vs emit a parallel report.sla_rows map keyed by action name (recommended to avoid churn) — confirm.
6. Flag default and rollout timeline: when does apexload.featureFlags.runReport flip to ON by default, and when are the legacy TestExecutionArchive scroll + GenerateReportModal/ReportComposer shells removed?

**Effort estimate.** ~19–24 engineer-days across 7 phases (Phase 0 backend contract ~2–3d; Phase 1 route/flag/shell ~2d; Phase 2 verdict+KPI ~2–3d; Phase 3 synchronized timeline ~4–6d; Phase 4 scoreboard ~3d; Phase 5 lens+diagnostics+export ~3–4d; Phase 6 compare ~3d). Phases 0–1 independently shippable; every phase gated behind the feature flag with zero risk to the live command center.

## Appendix C — Assumption verification ledger

| ID | Verdict | Claim |
|----|---------|-------|
| A1 | CONFIRMED | The verdict (Pass/Conditional Pass/Fail + explanation) is NOT in the getTestArchiveReport JSON — deriveReportVerdict is computed only on … |
| A2 | CONFIRMED | Per-transaction SLA pass/warn/fail + threshold are NOT in the JSON; by_action is raw RunArchiveActionStats with no result field, but SLA … |
| A3 | CONFIRMED | RunArchiveOverview in TS is MISSING p50/p90/p95/p99_ms even though the Go payload emits them (archive.go:498, omitempty). |
| A4 | CONFIRMED | summary_timeline (RunArchiveSummaryPoint) lacks avg_response_ms and p50/p90/p99 (only p95_ms), so full-fidelity RT-avg + percentile lanes… |
| A5 | CONFIRMED | recharts 2.15.4 is installed and Brush IS exported (the codebase simply never uses it); syncId synchronization is already in production a… |
| A6 | CONFIRMED | renderSharedCursor (the timestamp-keyed crosshair) is gated to surface==='command-center', so it does NOT render on archived views today … |
| A7 | CONFIRMED | Routing is hash-based with a strict round-trip coverage test; 'report' is not a NavPage, so an unhandled #/report silently resolves to {p… |
| A8 | CONFIRMED | useExecutionTestPolling has NO terminal-status guard and polls getTest every 2s (pausing only on hidden tab), so reusing it on a complete… |
| A9 | CONFIRMED | The audience LENS can map 1:1 onto existing executive/engineer/audit profiles (report.report_profiles ships in JSON) WITHOUT new profile … |
| A10 | PARTIAL | release_trend is attached UNCONDITIONALLY on the JSON path (so the frontend always gets it) but only covers 4 delta metrics (error_pct, a… |
| A11 | CONFIRMED | Compare mode has NO combined endpoint returning both runs' timeseries; baseline lane curves require a second full summaries pagination pa… |
| A12 | CONFIRMED | There is NO @testing-library and NO frontend CI; all component tests are raw createRoot + act() with a per-file @vitest-environment jsdom… |
